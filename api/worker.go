package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	extractorpb "api/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"gorm.io/gorm"
)

func startWorker(uploadDirectory string, store documentStore) {
	go func() {
		for {
			err := processNext(context.Background(), uploadDirectory, store)
			if err != nil {
				time.Sleep(time.Second)
			}
			if errors.Is(err, gorm.ErrRecordNotFound) {
				time.Sleep(time.Second)
			}
		}
	}()
}

func processNext(ctx context.Context, uploadDirectory string, store documentStore) error {
	document, err := store.ClaimNext(ctx)
	if err != nil {
		return err
	}
	pdf, err := os.ReadFile(filepath.Join(uploadDirectory, document.StoredPath))
	if err != nil {
		return store.Fail(ctx, document.ID, "could not read uploaded PDF")
	}
	connection, err := grpc.NewClient(processorAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return store.Fail(ctx, document.ID, "could not connect to document processor")
	}
	defer connection.Close()
	stream, err := extractorpb.NewDocumentProcessorClient(connection).Process(ctx, &extractorpb.ProcessRequest{DocumentId: document.ID, Pdf: pdf})
	if err != nil {
		return store.Fail(ctx, document.ID, "document processing failed")
	}
	var text string
	var chunks []documentChunk
	metadataReceived := false
	var expectedChunks uint32
	nextBatch := uint32(0)
	nextChunk := uint32(0)
	for {
		event, receiveErr := stream.Recv()
		if errors.Is(receiveErr, io.EOF) {
			break
		}
		if receiveErr != nil {
			return store.Fail(ctx, document.ID, "document processing failed")
		}
		if metadata := event.GetMetadata(); metadata != nil {
			if metadataReceived || metadata.EmbeddingDimensions != embeddingDimensions() || metadata.DocumentId != document.ID {
				return store.Fail(ctx, document.ID, "document processor returned an invalid document")
			}
			text = metadata.Text
			expectedChunks = metadata.ChunkCount
			metadataReceived = true
			continue
		}
		if batch := event.GetChunkBatch(); batch != nil {
			if !metadataReceived || batch.BatchIndex != nextBatch {
				return store.Fail(ctx, document.ID, "document processor returned invalid chunk batches")
			}
			nextBatch++
			for _, chunk := range batch.Chunks {
				if chunk.Index != nextChunk {
					return store.Fail(ctx, document.ID, "document processor returned invalid chunk indexes")
				}
				nextChunk++
				if len(chunk.Embedding) == 0 {
					return store.Fail(ctx, document.ID, "document processor returned an empty embedding")
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
					return store.Fail(ctx, document.ID, "could not create chunk ID")
				}
				chunks = append(chunks, documentChunk{ID: chunkID, DocumentID: document.ID, ChunkIndex: int(chunk.Index), Text: chunk.Text, StartOffset: chunk.StartOffset, EndOffset: chunk.EndOffset, Embedding: values, CreatedAt: time.Now().UTC()})
			}
		}
	}
	if !metadataReceived {
		return store.Fail(ctx, document.ID, "document processor returned no metadata")
	}
	if uint32(len(chunks)) != expectedChunks {
		return store.Fail(ctx, document.ID, "document processor returned an incomplete result")
	}
	return store.Complete(ctx, document.ID, text, chunks)
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
	return 768
}
