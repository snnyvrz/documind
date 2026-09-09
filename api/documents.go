package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
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
	admission       *admissionController
	metrics         *metrics
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
	DocumentID string    `json:"documentId"`
	Filename   string    `json:"filename"`
	Status     string    `json:"status"`
	PageCount  uint32    `json:"pageCount"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
	Error      *string   `json:"error,omitempty"`
}

func newDocumentHandler(uploadDirectory string, store documentStore) *documentHandler {
	return &documentHandler{uploadDirectory: uploadDirectory, store: store, admission: newAdmissionController(), metrics: newMetrics()}
}

func (h *documentHandler) Upload(c *echo.Context) error {
	request := c.Request()
	owner := principal(c)
	if request.ContentLength > maxUploadSize {
		return c.JSON(http.StatusRequestEntityTooLarge, map[string]string{"error": "uploaded file is too large"})
	}
	request.Body = http.MaxBytesReader(c.Response(), request.Body, maxUploadSize)

	file, fileHeader, err := request.FormFile("file")
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "a PDF file is required"})
	}
	defer file.Close()

	prefix := make([]byte, 5)
	if _, err := io.ReadFull(file, prefix); err != nil || string(prefix) != "%PDF-" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "file must be a PDF document"})
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not process uploaded file"})
	}
	if fileHeader.Size > maxUploadSize || !h.admission.reserveUpload(owner, fileHeader.Size) {
		return quotaResponse(c, "upload quota exceeded")
	}
	reserved := true
	defer func() {
		if reserved {
			h.admission.releaseUpload(owner, fileHeader.Size)
		}
	}()

	documentID, err := documentID()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not create document ID"})
	}

	storedPath := filepath.Join("documents", documentID, "original.pdf")
	destinationPath := filepath.Join(h.uploadDirectory, storedPath)
	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not prepare upload storage"})
	}

	destination, err := os.OpenFile(destinationPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not save uploaded file"})
	}

	_, copyErr := io.Copy(destination, file)
	closeErr := destination.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.RemoveAll(filepath.Dir(destinationPath))
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
	if err := h.store.CreateOwned(request.Context(), principal(c), document); err != nil {
		_ = os.RemoveAll(filepath.Dir(destinationPath))
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not save document metadata"})
	}
	reserved = false

	return c.JSON(http.StatusAccepted, map[string]string{"documentId": documentID, "status": document.Status})
}

func (h *documentHandler) List(c *echo.Context) error {
	documents, err := h.store.ListOwned(c.Request().Context(), principal(c))
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not list documents"})
	}
	result := make([]documentSummary, len(documents))
	for i, document := range documents {
		result[i] = documentSummary{DocumentID: document.ID, Filename: document.OriginalFilename, Status: document.Status, PageCount: document.PageCount, CreatedAt: document.CreatedAt, UpdatedAt: document.UpdatedAt, Error: document.ErrorMessage}
	}
	return c.JSON(http.StatusOK, result)
}

func (h *documentHandler) Get(c *echo.Context) error {
	document, err := h.store.FindOwned(c.Request().Context(), principal(c), c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "document not found"})
	}
	response := map[string]any{"documentId": document.ID, "filename": document.OriginalFilename, "status": document.Status, "pageCount": document.PageCount, "attemptCount": document.AttemptCount}
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
	path := filepath.Join(h.uploadDirectory, document.StoredPath)
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "document file not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not open document file"})
	}
	defer file.Close()

	c.Response().Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", filepath.Base(document.OriginalFilename)))
	c.Response().Header().Set("Content-Type", document.MIMEType)
	http.ServeContent(c.Response(), c.Request(), document.OriginalFilename, document.UpdatedAt, file)
	return nil
}

func (h *documentHandler) Delete(c *echo.Context) error {
	id := c.Param("id")
	document, err := h.store.FindOwned(c.Request().Context(), principal(c), id)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "document not found"})
	}
	if err := h.store.DeleteOwned(c.Request().Context(), principal(c), id); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not delete document"})
	}
	h.admission.releaseUpload(principal(c), document.Size)
	if err := os.RemoveAll(filepath.Join(h.uploadDirectory, filepath.Dir(document.StoredPath))); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "document metadata deleted but file cleanup failed"})
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *documentHandler) Ask(c *echo.Context) error {
	owner := principal(c)
	if !h.admission.beginAnswer(owner) {
		return quotaResponse(c, "answer generation quota exceeded")
	}
	defer h.admission.endAnswer(owner)
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
		writeSSE(response, flusher, canFlush, "token", payload)
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
	writeSSEDone(response, flusher, canFlush, true)
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
