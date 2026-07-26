package main

import (
	"context"
	"crypto/rand"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/labstack/echo/v5"
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
		Status:           "uploaded",
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := h.store.Create(context.Background(), document); err != nil {
		_ = os.RemoveAll(filepath.Dir(destinationPath))
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not save document metadata"})
	}

	return c.JSON(http.StatusCreated, map[string]string{"documentId": documentID, "status": document.Status})
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
