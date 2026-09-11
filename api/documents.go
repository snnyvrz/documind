package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	extractorpb "api/proto"
	"github.com/labstack/echo/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const maxUploadSize = 20 << 20
const defaultAnswerGenerationTimeout = 5 * time.Minute
const maxQuestionBodySize = 32 << 10

type documentHandler struct {
	uploadDirectory string
	store           documentStore
	storage         documentStorage
	admission       *admissionController
	metrics         *metrics
}

type documentListResponse struct {
	Items      []documentSummary `json:"items"`
	NextCursor string            `json:"nextCursor,omitempty"`
	HasMore    bool              `json:"hasMore"`
}

type documentSource struct {
	ChunkIndex  int    `json:"chunkIndex"`
	Text        string `json:"text"`
	StartOffset uint64 `json:"startOffset"`
	EndOffset   uint64 `json:"endOffset"`
	PageStart   uint32 `json:"pageStart"`
	PageEnd     uint32 `json:"pageEnd"`
}

type documentSummary struct {
	DocumentID  string    `json:"documentId"`
	Filename    string    `json:"filename"`
	Status      string    `json:"status"`
	PageCount   uint32    `json:"pageCount"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	Error       *string   `json:"error,omitempty"`
	FailureKind string    `json:"failureKind,omitempty"`
}

func newDocumentHandler(uploadDirectory string, store documentStore, storages ...documentStorage) *documentHandler {
	storage := documentStorage(newFilesystemStorage(uploadDirectory))
	if len(storages) > 0 {
		storage = storages[0]
	}
	return newDocumentHandlerWithMetrics(uploadDirectory, store, storage, newMetrics())
}

func newDocumentHandlerWithMetrics(uploadDirectory string, store documentStore, storage documentStorage, collector *metrics) *documentHandler {
	return &documentHandler{uploadDirectory: uploadDirectory, store: store, storage: storage, admission: newAdmissionController(), metrics: collector}
}

func (h *documentHandler) Upload(c *echo.Context) error {
	reject := func(status int, message string) error {
		h.metrics.uploadRejections.Add(1)
		return c.JSON(status, map[string]string{"error": message})
	}
	request := c.Request()
	owner := principal(c)
	if request.ContentLength > maxUploadSize {
		return reject(http.StatusRequestEntityTooLarge, "uploaded file is too large")
	}
	request.Body = http.MaxBytesReader(c.Response(), request.Body, maxUploadSize)

	file, fileHeader, err := request.FormFile("file")
	if err != nil {
		return reject(http.StatusBadRequest, "a PDF file is required")
	}
	defer file.Close()

	prefix := make([]byte, 5)
	if _, err := io.ReadFull(file, prefix); err != nil || string(prefix) != "%PDF-" {
		return reject(http.StatusBadRequest, "file must be a PDF document")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not process uploaded file"})
	}
	if fileHeader.Size > maxUploadSize {
		return reject(http.StatusTooManyRequests, "upload quota exceeded")
	}
	documentID, err := documentID()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not create document ID"})
	}
	reservationID := documentID
	durableQuota, hasDurableQuota := h.store.(quotaStore)
	if hasDurableQuota {
		allowed, rateErr := durableQuota.AllowRate(request.Context(), "upload-owner", owner, maxUploadsPerHour, time.Hour, time.Now().UTC())
		if rateErr != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not check upload quota"})
		}
		if !allowed || durableQuota.ReserveUpload(request.Context(), owner, reservationID, documentID, fileHeader.Size, time.Now().UTC().Add(30*time.Minute)) != nil {
			return reject(http.StatusTooManyRequests, "upload quota exceeded")
		}
	} else if !h.admission.reserveUpload(owner, fileHeader.Size) {
		return reject(http.StatusTooManyRequests, "upload quota exceeded")
	}
	reserved := true
	defer func() {
		if reserved {
			if hasDurableQuota {
				_ = durableQuota.ReleaseUpload(context.Background(), reservationID)
			} else {
				h.admission.releaseUpload(owner, fileHeader.Size)
			}
		}
	}()

	storedPath := filepath.ToSlash(filepath.Join("documents", documentID, "original.pdf"))
	if err := h.storage.Put(request.Context(), storedPath, file, fileHeader.Size, "application/pdf"); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not save uploaded file"})
	}

	now := time.Now().UTC()
	document := document{
		ID:               documentID,
		OriginalFilename: fileHeader.Filename,
		StoredPath:       storedPath,
		MIMEType:         "application/pdf",
		Size:             fileHeader.Size,
		Status:           "queued",
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if atomic, ok := h.store.(atomicUploadStore); ok && hasDurableQuota {
		if err := atomic.CreateOwnedAndCommitUpload(request.Context(), owner, document, reservationID); err != nil {
			_ = h.storage.Delete(context.Background(), storedPath)
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not commit document metadata and upload quota"})
		}
	} else if err := h.store.CreateOwned(request.Context(), principal(c), document); err != nil {
		_ = h.storage.Delete(context.Background(), storedPath)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not save document metadata"})
	} else if hasDurableQuota {
		if err := durableQuota.CommitUpload(request.Context(), reservationID); err != nil {
			_ = h.store.DeleteOwned(context.Background(), owner, documentID)
			_ = h.storage.Delete(context.Background(), storedPath)
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not commit upload quota"})
		}
	}
	reserved = false
	h.metrics.uploads.Add(1)

	return c.JSON(http.StatusAccepted, map[string]string{"documentId": documentID, "status": document.Status})
}

func (h *documentHandler) List(c *echo.Context) error {
	search := strings.TrimSpace(c.QueryParam("search"))
	limit := 50
	if value, err := strconv.Atoi(c.QueryParam("limit")); err == nil && value > 0 {
		if value > 100 {
			value = 100
		}
		limit = value
	}
	var cursor *documentCursor
	if value := c.QueryParam("cursor"); value != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(value)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid cursor"})
		}
		var payload struct {
			CreatedAt time.Time `json:"createdAt"`
			ID        string    `json:"id"`
		}
		if json.Unmarshal(decoded, &payload) != nil || payload.ID == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid cursor"})
		}
		cursor = &documentCursor{CreatedAt: payload.CreatedAt, ID: payload.ID}
	}
	if paged, ok := h.store.(interface {
		ListOwnedPage(context.Context, string, string, int, *documentCursor) ([]documentSummaryRow, error)
	}); ok {
		rows, err := paged.ListOwnedPage(c.Request().Context(), principal(c), search, limit+1, cursor)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not list documents"})
		}
		result := documentListResponse{Items: make([]documentSummary, 0, len(rows))}
		if len(rows) > limit {
			result.HasMore = true
			rows = rows[:limit]
		}
		for _, row := range rows {
			result.Items = append(result.Items, documentSummary{DocumentID: row.ID, Filename: row.OriginalFilename, Status: row.Status, PageCount: row.PageCount, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, Error: row.ErrorMessage, FailureKind: failureKindFor(row.Status, row.FailureKind)})
		}
		if result.HasMore && len(rows) > 0 {
			last := rows[len(rows)-1]
			encoded, _ := json.Marshal(struct {
				CreatedAt time.Time `json:"createdAt"`
				ID        string    `json:"id"`
			}{last.CreatedAt, last.ID})
			result.NextCursor = base64.RawURLEncoding.EncodeToString(encoded)
		}
		return c.JSON(http.StatusOK, result)
	}
	documents, err := h.store.ListOwned(c.Request().Context(), principal(c))
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not list documents"})
	}
	result := make([]documentSummary, 0, len(documents))
	for _, document := range documents {
		if search != "" && !strings.Contains(strings.ToLower(document.OriginalFilename), strings.ToLower(search)) {
			continue
		}
		result = append(result, documentSummary{DocumentID: document.ID, Filename: document.OriginalFilename, Status: document.Status, PageCount: document.PageCount, CreatedAt: document.CreatedAt, UpdatedAt: document.UpdatedAt, Error: document.ErrorMessage, FailureKind: failureKindFor(document.Status, document.FailureKind)})
	}
	if len(result) > limit {
		result = result[:limit]
	}
	return c.JSON(http.StatusOK, documentListResponse{Items: result, HasMore: false})
}

func (h *documentHandler) Get(c *echo.Context) error {
	document, err := h.store.FindOwned(c.Request().Context(), principal(c), c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "document not found"})
	}
	response := map[string]any{"documentId": document.ID, "filename": document.OriginalFilename, "status": document.Status, "pageCount": document.PageCount, "attemptCount": document.AttemptCount}
	if document.Status == "failed" {
		response["failureKind"] = failureKindFor(document.Status, document.FailureKind)
	}
	if document.NextAttemptAt != nil {
		response["nextAttemptAt"] = document.NextAttemptAt
	}
	if document.ExtractedText != nil {
		response["text"] = *document.ExtractedText
	}
	if document.ErrorMessage != nil {
		response["error"] = *document.ErrorMessage
	}
	return c.JSON(http.StatusOK, response)
}

func (h *documentHandler) Chunks(c *echo.Context) error {
	if _, err := h.store.FindOwned(c.Request().Context(), principal(c), c.Param("id")); err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "document not found"})
	}
	chunks, err := h.store.ListChunksOwned(c.Request().Context(), principal(c), c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not load document chunks"})
	}
	result := make([]documentSource, len(chunks))
	for i, chunk := range chunks {
		result[i] = documentSource{ChunkIndex: chunk.ChunkIndex, Text: chunk.Text, StartOffset: chunk.StartOffset, EndOffset: chunk.EndOffset, PageStart: chunk.PageStart, PageEnd: chunk.PageEnd}
	}
	return c.JSON(http.StatusOK, result)
}

func (h *documentHandler) File(c *echo.Context) error {
	document, err := h.store.FindOwned(c.Request().Context(), principal(c), c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "document not found"})
	}
	info, err := h.storage.Stat(c.Request().Context(), document.StoredPath)
	if err != nil {
		if errors.Is(err, errStorageNotFound) || os.IsNotExist(err) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "document file not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not stat document file"})
	}
	return serveStoredFile(c, h.storage, document, info)
}

var singleRangePattern = regexp.MustCompile(`^bytes=(\d*)-(\d*)$`)

func serveStoredFile(c *echo.Context, storage documentStorage, document *document, info storageInfo) error {
	response := c.Response()
	request := c.Request()
	header := response.Header()
	header.Set("Accept-Ranges", "bytes")
	header.Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", filepath.Base(document.OriginalFilename)))
	header.Set("Content-Type", document.MIMEType)
	if !info.LastModified.IsZero() {
		header.Set("Last-Modified", info.LastModified.UTC().Format(http.TimeFormat))
	}
	if request.Header.Get("If-Modified-Since") != "" && !info.LastModified.UTC().Truncate(time.Second).After(parseHTTPTime(request.Header.Get("If-Modified-Since"))) {
		return c.NoContent(http.StatusNotModified)
	}

	start, end := int64(0), info.Size-1
	status := http.StatusOK
	rangeHeader := request.Header.Get("Range")
	if rangeHeader != "" && request.Header.Get("If-Range") != "" && request.Header.Get("If-Range") != info.LastModified.UTC().Format(http.TimeFormat) {
		rangeHeader = ""
	}
	if rangeHeader != "" {
		match := singleRangePattern.FindStringSubmatch(rangeHeader)
		if match == nil || strings.Contains(rangeHeader, ",") {
			header.Set("Content-Range", fmt.Sprintf("bytes */%d", info.Size))
			return c.NoContent(http.StatusRequestedRangeNotSatisfiable)
		}
		var parseErr error
		if match[1] == "" {
			suffix, err := strconv.ParseInt(match[2], 10, 64)
			if err != nil || suffix <= 0 {
				parseErr = errors.New("invalid suffix range")
			} else {
				start = info.Size - suffix
				if start < 0 {
					start = 0
				}
			}
		} else {
			start, parseErr = strconv.ParseInt(match[1], 10, 64)
			if match[2] == "" {
				end = info.Size - 1
			} else {
				end, parseErr = strconv.ParseInt(match[2], 10, 64)
			}
		}
		if parseErr != nil || start < 0 || start >= info.Size || end < start {
			header.Set("Content-Range", fmt.Sprintf("bytes */%d", info.Size))
			return c.NoContent(http.StatusRequestedRangeNotSatisfiable)
		}
		if end >= info.Size {
			end = info.Size - 1
		}
		status = http.StatusPartialContent
		header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, info.Size))
	}
	length := end - start + 1
	header.Set("Content-Length", strconv.FormatInt(length, 10))
	if request.Method == http.MethodHead || length == 0 {
		response.WriteHeader(status)
		return nil
	}
	file, err := storage.GetRange(request.Context(), document.StoredPath, start, end)
	if err != nil {
		if errors.Is(err, errStorageNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "document file not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not read document file"})
	}
	defer file.Close()
	response.WriteHeader(status)
	_, err = io.CopyN(response, file, length)
	return err
}

func parseHTTPTime(value string) time.Time {
	parsed, _ := http.ParseTime(value)
	return parsed
}

func (h *documentHandler) Delete(c *echo.Context) error {
	id := c.Param("id")
	document, err := h.store.FindOwned(c.Request().Context(), principal(c), id)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "document not found"})
	}
	if durable, ok := h.store.(quotaStore); ok {
		deleted, err := durable.DeleteOwnedWithUsage(c.Request().Context(), principal(c), id)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not delete document"})
		}
		document = deleted
	} else if err := h.store.DeleteOwned(c.Request().Context(), principal(c), id); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not delete document"})
	} else {
		h.admission.releaseUpload(principal(c), document.Size)
	}
	if err := h.storage.Delete(context.Background(), document.StoredPath); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "document metadata deleted but file cleanup failed"})
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *documentHandler) Retry(c *echo.Context) error {
	id := c.Param("id")
	if err := h.store.RetryOwned(c.Request().Context(), principal(c), id); err != nil {
		if errors.Is(err, errPermanentFailure) {
			return c.JSON(http.StatusConflict, map[string]string{"error": "this document cannot be retried; OCR is required for scanned PDFs or the PDF must be corrected"})
		}
		return c.JSON(http.StatusConflict, map[string]string{"error": "only failed documents can be retried"})
	}
	return c.JSON(http.StatusAccepted, map[string]string{"documentId": id, "status": "queued"})
}

func failureKindFor(status, kind string) string {
	if status != "failed" {
		return ""
	}
	if kind == failureKindPermanent {
		return failureKindPermanent
	}
	return failureKindRetryable
}

func (h *documentHandler) Questions(c *echo.Context) error {
	if _, err := h.store.FindOwned(c.Request().Context(), principal(c), c.Param("id")); err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "document not found"})
	}
	questions, err := h.store.ListQuestionsOwned(c.Request().Context(), principal(c), c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not load question history"})
	}
	result := make([]map[string]any, len(questions))
	for i, question := range questions {
		var sources []documentSource
		if question.Sources != "" {
			if err := json.Unmarshal([]byte(question.Sources), &sources); err != nil {
				return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not decode question history"})
			}
		}
		result[i] = map[string]any{"id": question.ID, "documentId": question.DocumentID, "question": question.Question, "answer": question.Answer, "sources": sources, "createdAt": question.CreatedAt}
	}
	return c.JSON(http.StatusOK, result)
}

func (h *documentHandler) Ask(c *echo.Context) error {
	owner := principal(c)
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, maxQuestionBodySize)
	generationContext, cancel := context.WithTimeout(c.Request().Context(), answerGenerationTimeout())
	defer cancel()

	var request struct {
		Question string `json:"question"`
	}
	if err := json.NewDecoder(c.Request().Body).Decode(&request); err != nil || len([]rune(request.Question)) == 0 || len([]rune(request.Question)) > 4000 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "question must contain between 1 and 4000 characters"})
	}
	document, err := h.store.FindOwned(generationContext, principal(c), c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "document not found"})
	}
	if document.Status != "completed" {
		return c.JSON(http.StatusConflict, map[string]string{"error": "document is not ready for questions"})
	}
	requestID, _ := documentID()
	durableQuota, hasDurableQuota := h.store.(quotaStore)
	if hasDurableQuota {
		allowed, err := durableQuota.AllowRate(generationContext, "answer-owner", owner, maxAnswersPerDay, 24*time.Hour, time.Now().UTC())
		if err != nil || !allowed {
			return quotaResponse(c, "answer generation quota exceeded")
		}
		if err := durableQuota.BeginAnswer(generationContext, owner, requestID, time.Now().UTC().Add(answerGenerationTimeout())); err != nil {
			return quotaResponse(c, "answer generation quota exceeded")
		}
		defer func() { _ = durableQuota.EndAnswer(context.Background(), requestID, "released") }()
	} else {
		if !h.admission.beginAnswer(owner) {
			return quotaResponse(c, "answer generation quota exceeded")
		}
		defer h.admission.endAnswer(owner)
	}
	answerStarted := time.Now()
	answerFailed := true
	defer func() { h.metrics.recordAnswer(answerStarted, answerFailed) }()
	connection, err := grpc.NewClient(processorAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "document processor unavailable"})
	}
	defer connection.Close()
	client := extractorpb.NewDocumentProcessorClient(connection)
	embedding, err := client.EmbedQuestion(generationContext, &extractorpb.EmbedQuestionRequest{Text: request.Question})
	if err != nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "could not embed question"})
	}
	if embedding.Dimensions != maxEmbeddingDimensions || len(embedding.Embedding) != int(maxEmbeddingDimensions) {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "document processor returned an invalid embedding dimension"})
	}
	vector := "["
	for i, value := range embedding.Embedding {
		if i > 0 {
			vector += ","
		}
		vector += fmt.Sprintf("%g", value)
	}
	vector += "]"
	contexts, err := h.store.SearchChunksOwned(generationContext, principal(c), document.ID, vector, retrievalLimit())
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not search document"})
	}
	stream, err := client.AnswerQuestion(generationContext, &extractorpb.AnswerQuestionRequest{Question: request.Question, Contexts: func() []*extractorpb.AnswerContext {
		result := make([]*extractorpb.AnswerContext, len(contexts))
		for i := range contexts {
			result[i] = &extractorpb.AnswerContext{ChunkIndex: uint32(contexts[i].ChunkIndex), Text: contexts[i].Text, PageStart: contexts[i].PageStart, PageEnd: contexts[i].PageEnd}
		}
		return result
	}()})
	if err != nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "could not start answer"})
	}
	response := c.Response()
	response.Header().Set("Content-Type", "text/event-stream")
	response.Header().Set("Cache-Control", "no-cache")
	response.Header().Set("Connection", "keep-alive")
	response.Header().Set("X-Accel-Buffering", "no")
	flusher, canFlush := response.(http.Flusher)
	var answerFromStream string
	var modelCompleted bool
	for {
		event, receiveErr := stream.Recv()
		if receiveErr == io.EOF {
			if generationContext.Err() != nil {
				if c.Request().Context().Err() != nil {
					return nil
				}
				writeSSEError(response, flusher, canFlush, "generation_timeout", "Answer generation timed out.")
				writeSSEDone(response, flusher, canFlush, false)
				return nil
			}
			if !modelCompleted {
				writeSSEError(response, flusher, canFlush, "generation_failed", "Could not complete the answer.")
				writeSSEDone(response, flusher, canFlush, false)
				return nil
			}
			break
		}
		if receiveErr != nil {
			if generationContext.Err() != nil {
				if c.Request().Context().Err() != nil {
					return nil
				}
				writeSSEError(response, flusher, canFlush, "generation_timeout", "Answer generation timed out.")
			} else {
				writeSSEError(response, flusher, canFlush, "generation_failed", "Could not complete the answer.")
			}
			writeSSEDone(response, flusher, canFlush, false)
			return nil
		}
		payload, _ := json.Marshal(map[string]string{"text": event.Text})
		if event.Done {
			modelCompleted = true
		} else {
			answerFromStream += event.Text
			writeSSE(response, flusher, canFlush, "token", payload)
		}
		if canFlush {
			flusher.Flush()
		}
	}
	sources := make([]documentSource, len(contexts))
	for i := range contexts {
		sources[i] = documentSource{ChunkIndex: contexts[i].ChunkIndex, Text: contexts[i].Text, StartOffset: contexts[i].StartOffset, EndOffset: contexts[i].EndOffset, PageStart: contexts[i].PageStart, PageEnd: contexts[i].PageEnd}
	}
	payload, _ := json.Marshal(map[string]any{"sources": sources})
	writeSSE(response, flusher, canFlush, "sources", payload)
	sourcesJSON, _ := json.Marshal(sources)
	historyID, err := documentID()
	if err != nil {
		writeSSEError(response, flusher, canFlush, "history_failed", "Answer completed but could not be saved.")
		writeSSEDone(response, flusher, canFlush, false)
		return nil
	}
	history := documentQuestion{ID: historyID, OwnerID: principal(c), DocumentID: document.ID, Question: request.Question, Answer: answerFromStream, Sources: string(sourcesJSON), CreatedAt: time.Now().UTC()}
	if err := h.store.CreateQuestion(generationContext, history); err != nil {
		writeSSEError(response, flusher, canFlush, "history_failed", "Answer completed but could not be saved.")
		writeSSEDone(response, flusher, canFlush, false)
		return nil
	}
	historyPayload, _ := json.Marshal(history)
	writeSSE(response, flusher, canFlush, "history", historyPayload)
	writeSSEDone(response, flusher, canFlush, true)
	answerFailed = false
	return nil
}

func answerGenerationTimeout() time.Duration {
	value := os.Getenv("ANSWER_GENERATION_TIMEOUT")
	if value == "" {
		return defaultAnswerGenerationTimeout
	}
	timeout, err := time.ParseDuration(value)
	if err != nil || timeout <= 0 {
		return defaultAnswerGenerationTimeout
	}
	return timeout
}

func writeSSE(response http.ResponseWriter, flusher http.Flusher, canFlush bool, event string, payload []byte) {
	fmt.Fprintf(response, "event: %s\ndata: %s\n\n", event, payload)
	if canFlush {
		flusher.Flush()
	}
}

func writeSSEError(response http.ResponseWriter, flusher http.Flusher, canFlush bool, code, message string) {
	payload, _ := json.Marshal(map[string]string{"code": code, "message": message})
	writeSSE(response, flusher, canFlush, "error", payload)
}

func writeSSEDone(response http.ResponseWriter, flusher http.Flusher, canFlush bool, ok bool) {
	payload, _ := json.Marshal(map[string]bool{"ok": ok})
	writeSSE(response, flusher, canFlush, "done", payload)
}

func retrievalLimit() int {
	value := os.Getenv("RETRIEVAL_LIMIT")
	if value == "" {
		return 5
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit <= 0 {
		return 5
	}
	return limit
}

func documentID() (string, error) {
	identifier := make([]byte, 16)
	if _, err := rand.Read(identifier); err != nil {
		return "", err
	}

	identifier[6] = (identifier[6] & 0x0f) | 0x40
	identifier[8] = (identifier[8] & 0x3f) | 0x80
	return formatUUID(identifier), nil
}

func formatUUID(identifier []byte) string {
	return string([]byte{
		hexDigit(identifier[0] >> 4), hexDigit(identifier[0] & 0x0f),
		hexDigit(identifier[1] >> 4), hexDigit(identifier[1] & 0x0f),
		hexDigit(identifier[2] >> 4), hexDigit(identifier[2] & 0x0f),
		hexDigit(identifier[3] >> 4), hexDigit(identifier[3] & 0x0f), '-',
		hexDigit(identifier[4] >> 4), hexDigit(identifier[4] & 0x0f),
		hexDigit(identifier[5] >> 4), hexDigit(identifier[5] & 0x0f), '-',
		hexDigit(identifier[6] >> 4), hexDigit(identifier[6] & 0x0f),
		hexDigit(identifier[7] >> 4), hexDigit(identifier[7] & 0x0f), '-',
		hexDigit(identifier[8] >> 4), hexDigit(identifier[8] & 0x0f),
		hexDigit(identifier[9] >> 4), hexDigit(identifier[9] & 0x0f), '-',
		hexDigit(identifier[10] >> 4), hexDigit(identifier[10] & 0x0f),
		hexDigit(identifier[11] >> 4), hexDigit(identifier[11] & 0x0f),
		hexDigit(identifier[12] >> 4), hexDigit(identifier[12] & 0x0f),
		hexDigit(identifier[13] >> 4), hexDigit(identifier[13] & 0x0f),
		hexDigit(identifier[14] >> 4), hexDigit(identifier[14] & 0x0f),
		hexDigit(identifier[15] >> 4), hexDigit(identifier[15] & 0x0f),
	})
}

func hexDigit(value byte) byte {
	const digits = "0123456789abcdef"
	return digits[value]
}
