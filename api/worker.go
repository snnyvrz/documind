package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"sync"
	"time"

	extractorpb "api/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

const maxGRPCMessageSize = maxUploadSize + (1 << 20)
const maxEmbeddingDimensions uint32 = 768
const maxExtractedTextBytes uint64 = 25 * 1024 * 1024
const maxProcessedPages uint32 = 500
const maxProcessedChunks uint32 = 10000

type workerConfig struct {
	workers           int
	maxAttempts       int
	initialBackoff    time.Duration
	maxBackoff        time.Duration
	leaseDuration     time.Duration
	leaseRenewal      time.Duration
	processingTimeout time.Duration
	pollInterval      time.Duration
}

type jobError struct {
	cause     error
	message   string
	permanent bool
}

const (
	failureKindPermanent = "permanent"
	failureKindRetryable = "retryable"
)

func (e *jobError) Error() string {
	if e.cause == nil {
		return e.message
	}
	return e.message + ": " + e.cause.Error()
}

func loadWorkerConfig() (workerConfig, error) {
	config := workerConfig{
		workers:           1,
		maxAttempts:       5,
		initialBackoff:    5 * time.Second,
		maxBackoff:        5 * time.Minute,
		leaseDuration:     2 * time.Minute,
		leaseRenewal:      30 * time.Second,
		processingTimeout: 30 * time.Minute,
		pollInterval:      time.Second,
	}
	var err error
	if config.workers, err = environmentInt("DOCUMENT_JOB_WORKERS", config.workers); err != nil {
		return workerConfig{}, err
	}
	if config.maxAttempts, err = environmentInt("DOCUMENT_JOB_MAX_ATTEMPTS", config.maxAttempts); err != nil {
		return workerConfig{}, err
	}
	for _, setting := range []struct {
		name     string
		value    *time.Duration
		fallback time.Duration
	}{
		{"DOCUMENT_JOB_INITIAL_BACKOFF", &config.initialBackoff, config.initialBackoff},
		{"DOCUMENT_JOB_MAX_BACKOFF", &config.maxBackoff, config.maxBackoff},
		{"DOCUMENT_JOB_LEASE_DURATION", &config.leaseDuration, config.leaseDuration},
		{"DOCUMENT_JOB_LEASE_RENEWAL", &config.leaseRenewal, config.leaseRenewal},
		{"DOCUMENT_JOB_PROCESSING_TIMEOUT", &config.processingTimeout, config.processingTimeout},
		{"DOCUMENT_JOB_POLL_INTERVAL", &config.pollInterval, config.pollInterval},
	} {
		if *setting.value, err = environmentDuration(setting.name, setting.fallback); err != nil {
			return workerConfig{}, err
		}
	}
	if config.initialBackoff > config.maxBackoff {
		return workerConfig{}, errors.New("DOCUMENT_JOB_INITIAL_BACKOFF must not exceed DOCUMENT_JOB_MAX_BACKOFF")
	}
	if config.leaseRenewal >= config.leaseDuration {
		return workerConfig{}, errors.New("DOCUMENT_JOB_LEASE_RENEWAL must be shorter than DOCUMENT_JOB_LEASE_DURATION")
	}
	return config, nil
}

func environmentInt(name string, fallback int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	result, err := strconv.Atoi(value)
	if err != nil || result <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return result, nil
}

func environmentDuration(name string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	result, err := time.ParseDuration(value)
	if err != nil || result <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", name)
	}
	return result, nil
}

func startWorker(ctx context.Context, storage documentStorage, store documentStore, config workerConfig, metricCollectors ...*metrics) <-chan struct{} {
	var metricCollector *metrics
	if len(metricCollectors) > 0 {
		metricCollector = metricCollectors[0]
	}
	done := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(config.workers)
	for index := 0; index < config.workers; index++ {
		go func() {
			defer wait.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}
				err := processNext(ctx, storage, store, config, metricCollector)
				if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
					log.Printf("document worker: %v", err)
				}
				if err != nil {
					select {
					case <-ctx.Done():
						return
					case <-time.After(config.pollInterval):
					}
				}
			}
		}()
	}
	go func() { wait.Wait(); close(done) }()
	return done
}

func processNext(ctx context.Context, storageInput any, store documentStore, config workerConfig, metricCollectors ...*metrics) error {
	var metricCollector *metrics
	if len(metricCollectors) > 0 {
		metricCollector = metricCollectors[0]
	}
	if metricCollector != nil {
		refreshQueueMetrics(ctx, store, metricCollector)
		defer refreshQueueMetrics(context.Background(), store, metricCollector)
	}
	storage := asDocumentStorage(storageInput)
	leaseToken, err := documentID()
	if err != nil {
		return fmt.Errorf("create lease token: %w", err)
	}
	claimed, err := store.ClaimNext(ctx, leaseToken, config.leaseDuration, config.maxAttempts)
	if err != nil {
		return err
	}

	processingContext, cancelProcessing := context.WithTimeout(ctx, config.processingTimeout)
	heartbeatContext, stopHeartbeat := context.WithCancel(processingContext)
	heartbeatDone := make(chan error, 1)
	go maintainLease(heartbeatContext, cancelProcessing, store, claimed.ID, leaseToken, config, heartbeatDone)

	text, pageCount, chunks, processingErr := processDocument(processingContext, storage, claimed)
	stopHeartbeat()
	heartbeatErr := <-heartbeatDone
	cancelProcessing()
	if heartbeatErr != nil {
		return heartbeatErr
	}

	if processingErr == nil {
		return store.Complete(ctx, claimed.ID, leaseToken, text, pageCount, chunks)
	}
	log.Printf("document worker: document %s attempt %d: %v", claimed.ID, claimed.AttemptCount, processingErr)
	if processingErr.permanent || claimed.AttemptCount >= config.maxAttempts {
		if metricCollector != nil {
			metricCollector.processingFailures.Add(1)
		}
		kind := failureKindRetryable
		if processingErr.permanent {
			kind = failureKindPermanent
		}
		return store.Fail(ctx, claimed.ID, leaseToken, processingErr.message, kind)
	}
	return store.Retry(ctx, claimed.ID, leaseToken, time.Now().UTC().Add(retryBackoff(claimed.AttemptCount, config)))
}

func refreshQueueMetrics(ctx context.Context, store documentStore, collector *metrics) {
	queue, ok := store.(queueStatsStore)
	if !ok {
		return
	}
	depth, oldest, err := queue.QueueStats(ctx)
	if err != nil {
		return
	}
	collector.queueDepth.Store(depth)
	if oldest.IsZero() {
		collector.queueOldestAgeMs.Store(0)
		return
	}
	age := time.Since(oldest).Milliseconds()
	if age < 0 {
		age = 0
	}
	collector.queueOldestAgeMs.Store(uint64(age))
}

func asDocumentStorage(value any) documentStorage {
	if storage, ok := value.(documentStorage); ok {
		return storage
	}
	if directory, ok := value.(string); ok {
		return newFilesystemStorage(directory)
	}
	panic("unsupported document storage")
}

func maintainLease(ctx context.Context, cancelProcessing context.CancelFunc, store documentStore, id, leaseToken string, config workerConfig, done chan<- error) {
	ticker := time.NewTicker(config.leaseRenewal)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			done <- nil
			return
		case <-ticker.C:
			if err := store.RenewLease(ctx, id, leaseToken, config.leaseDuration); err != nil {
				cancelProcessing()
				done <- fmt.Errorf("renew document lease: %w", err)
				return
			}
		}
	}
}

func retryBackoff(attempt int, config workerConfig) time.Duration {
	delay := config.initialBackoff
	for current := 1; current < attempt && delay < config.maxBackoff; current++ {
		if delay > config.maxBackoff/2 {
			return config.maxBackoff
		}
		delay *= 2
	}
	if delay > config.maxBackoff {
		return config.maxBackoff
	}
	return delay
}

func processDocument(ctx context.Context, storage documentStorage, document *document) (string, uint32, []documentChunk, *jobError) {
	file, err := storage.Get(ctx, document.StoredPath)
	if err != nil {
		return "", 0, nil, &jobError{cause: err, message: "could not read uploaded PDF", permanent: true}
	}
	defer file.Close()
	pdf, err := io.ReadAll(io.LimitReader(file, maxUploadSize+1))
	if err != nil {
		return "", 0, nil, &jobError{cause: err, message: "could not read uploaded PDF", permanent: true}
	}
	if int64(len(pdf)) > maxUploadSize {
		return "", 0, nil, &jobError{message: "uploaded PDF exceeds the maximum size", permanent: true}
	}
	connection, err := grpc.NewClient(
		processorAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallSendMsgSize(maxGRPCMessageSize),
			grpc.MaxCallRecvMsgSize(maxGRPCMessageSize),
		),
	)
	if err != nil {
		return "", 0, nil, &jobError{cause: err, message: "document processor unavailable"}
	}
	defer connection.Close()
	stream, err := extractorpb.NewDocumentProcessorClient(connection).Process(ctx, &extractorpb.ProcessRequest{DocumentId: document.ID, Pdf: pdf})
	if err != nil {
		return "", 0, nil, processorError(err)
	}
	var text string
	var chunks []documentChunk
	metadataReceived := false
	var expectedChunks uint32
	var pageCount uint32
	nextBatch := uint32(0)
	nextChunk := uint32(0)
	for {
		event, receiveErr := stream.Recv()
		if errors.Is(receiveErr, io.EOF) {
			break
		}
		if receiveErr != nil {
			return "", 0, nil, processorError(receiveErr)
		}
		if metadata := event.GetMetadata(); metadata != nil {
			if metadataReceived || metadata.EmbeddingDimensions != maxEmbeddingDimensions || metadata.DocumentId != document.ID || metadata.PageCount == 0 || metadata.PageCount > maxProcessedPages || uint64(len(metadata.Text)) > maxExtractedTextBytes || metadata.ChunkCount > maxProcessedChunks {
				return "", 0, nil, &jobError{message: "document processor returned an invalid document", permanent: true}
			}
			text = metadata.Text
			expectedChunks = metadata.ChunkCount
			pageCount = metadata.PageCount
			metadataReceived = true
			continue
		}
		batch := event.GetChunkBatch()
		if batch == nil || !metadataReceived || batch.BatchIndex != nextBatch {
			return "", 0, nil, &jobError{message: "document processor returned invalid chunk batches", permanent: true}
		}
		nextBatch++
		for _, chunk := range batch.Chunks {
			if uint32(len(chunks)) >= maxProcessedChunks {
				return "", 0, nil, &jobError{message: "document processor returned too many chunks", permanent: true}
			}
			if chunk.Index != nextChunk {
				return "", 0, nil, &jobError{message: "document processor returned invalid chunk indexes", permanent: true}
			}
			nextChunk++
			if len(chunk.Embedding) != int(maxEmbeddingDimensions) {
				return "", 0, nil, &jobError{message: "document processor returned an invalid embedding", permanent: true}
			}
			if len(chunk.Text) == 0 || len(chunk.Text) > 100000 {
				return "", 0, nil, &jobError{message: "document processor returned invalid chunk text", permanent: true}
			}
			if chunk.PageStart == 0 || chunk.PageEnd < chunk.PageStart || chunk.PageEnd > pageCount {
				return "", 0, nil, &jobError{message: "document processor returned invalid page metadata", permanent: true}
			}
			values := "["
			for index, value := range chunk.Embedding {
				if index > 0 {
					values += ","
				}
				values += fmt.Sprintf("%g", value)
			}
			values += "]"
			chunkID, idErr := documentID()
			if idErr != nil {
				return "", 0, nil, &jobError{cause: idErr, message: "could not create chunk ID"}
			}
			chunks = append(chunks, documentChunk{ID: chunkID, DocumentID: document.ID, ChunkIndex: int(chunk.Index), Text: chunk.Text, StartOffset: chunk.StartOffset, EndOffset: chunk.EndOffset, PageStart: chunk.PageStart, PageEnd: chunk.PageEnd, Embedding: values, CreatedAt: time.Now().UTC()})
		}
	}
	if !metadataReceived {
		return "", 0, nil, &jobError{message: "document processor returned no metadata", permanent: true}
	}
	if uint32(len(chunks)) != expectedChunks {
		return "", 0, nil, &jobError{message: "document processor returned an incomplete result", permanent: true}
	}
	return text, pageCount, chunks, nil
}

func processorError(err error) *jobError {
	return &jobError{
		cause:     err,
		message:   "document processing failed",
		permanent: status.Code(err) == codes.InvalidArgument,
	}
}

func processorAddress() string {
	if address := os.Getenv("DOCUMENT_PROCESSOR_GRPC_URL"); address != "" {
		return address
	}
	return "127.0.0.1:50051"
}

func embeddingDimensions() uint32 {
	if value := os.Getenv("EMBEDDING_DIMENSIONS"); value != "" {
		var dimensions uint32
		if _, err := fmt.Sscanf(value, "%d", &dimensions); err == nil && dimensions > 0 {
			return dimensions
		}
	}
	return maxEmbeddingDimensions
}
