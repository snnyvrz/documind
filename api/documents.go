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
	"time"

	extractorpb "api/proto"
	"github.com/labstack/echo/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const maxUploadSize = 20 << 20

type documentHandler struct {
	uploadDirectory string
	store           documentStore
}

func newDocumentHandler(uploadDirectory string, store documentStore) *documentHandler {
	return &documentHandler{uploadDirectory: uploadDirectory, store: store}
}

func (h *documentHandler) Upload(c *echo.Context) error {
	request := c.Request()
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
	if err := h.store.Create(context.Background(), document); err != nil {
		_ = os.RemoveAll(filepath.Dir(destinationPath))
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not save document metadata"})
	}

	return c.JSON(http.StatusAccepted, map[string]string{"documentId": documentID, "status": document.Status})
}

func (h *documentHandler) Get(c *echo.Context) error {
	document, err := h.store.Find(context.Background(), c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "document not found"})
	}
	response := map[string]any{"documentId": document.ID, "filename": document.OriginalFilename, "status": document.Status}
	if document.ExtractedText != nil {
		response["text"] = *document.ExtractedText
	}
	if document.ErrorMessage != nil {
		response["error"] = *document.ErrorMessage
	}
	return c.JSON(http.StatusOK, response)
}

func (h *documentHandler) Ask(c *echo.Context) error {
	var request struct {
		Question string `json:"question"`
	}
	if err := json.NewDecoder(c.Request().Body).Decode(&request); err != nil || len([]rune(request.Question)) == 0 || len([]rune(request.Question)) > 4000 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "question must contain between 1 and 4000 characters"})
	}
	document, err := h.store.Find(context.Background(), c.Param("id"))
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
	embedding, err := client.EmbedQuestion(c.Request().Context(), &extractorpb.EmbedQuestionRequest{Text: request.Question})
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
	contexts, err := h.store.SearchChunks(c.Request().Context(), document.ID, vector, 5)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not search document"})
	}
	stream, err := client.AnswerQuestion(c.Request().Context(), &extractorpb.AnswerQuestionRequest{Question: request.Question, Contexts: func() []*extractorpb.AnswerContext {
		result := make([]*extractorpb.AnswerContext, len(contexts))
		for i := range contexts {
			result[i] = &extractorpb.AnswerContext{ChunkIndex: uint32(contexts[i].ChunkIndex), Text: contexts[i].Text}
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
	flusher, canFlush := response.(http.Flusher)
	for {
		event, receiveErr := stream.Recv()
		if receiveErr == io.EOF {
			break
		}
		if receiveErr != nil {
			return receiveErr
		}
		payload, _ := json.Marshal(map[string]string{"text": event.Text})
		fmt.Fprintf(response, "event: token\ndata: %s\n\n", payload)
		if canFlush {
			flusher.Flush()
		}
	}
	sources := make([]map[string]any, len(contexts))
	for i := range contexts {
		sources[i] = map[string]any{"chunkIndex": contexts[i].ChunkIndex, "text": contexts[i].Text, "startOffset": contexts[i].StartOffset, "endOffset": contexts[i].EndOffset}
	}
	payload, _ := json.Marshal(map[string]any{"sources": sources})
	fmt.Fprintf(response, "event: sources\ndata: %s\n\nevent: done\ndata: {}\n\n", payload)
	if canFlush {
		flusher.Flush()
	}
	return nil
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
