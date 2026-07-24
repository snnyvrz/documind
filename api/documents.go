package main

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/labstack/echo/v5"
)

const maxUploadSize = 20 << 20

type documentHandler struct {
	uploadDirectory string
}

func newDocumentHandler(uploadDirectory string) *documentHandler {
	return &documentHandler{uploadDirectory: uploadDirectory}
}

func (h *documentHandler) Upload(c *echo.Context) error {
	request := c.Request()
	request.Body = http.MaxBytesReader(c.Response(), request.Body, maxUploadSize)

	fileHeader, _, err := request.FormFile("file")
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "a PDF file is required"})
	}
	defer fileHeader.Close()

	prefix := make([]byte, 5)
	if _, err := io.ReadFull(fileHeader, prefix); err != nil || string(prefix) != "%PDF-" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "file must be a PDF document"})
	}
	if _, err := fileHeader.Seek(0, io.SeekStart); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not process uploaded file"})
	}

	if err := os.MkdirAll(h.uploadDirectory, 0o755); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not prepare upload storage"})
	}

	filename, err := documentFilename()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not create document ID"})
	}
	destinationPath := filepath.Join(h.uploadDirectory, filename)
	destination, err := os.OpenFile(destinationPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not save uploaded file"})
	}

	_, copyErr := io.Copy(destination, fileHeader)
	closeErr := destination.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(destinationPath)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not save uploaded file"})
	}

	return c.JSON(http.StatusCreated, map[string]string{"id": filename})
}

func documentFilename() (string, error) {
	identifier := make([]byte, 16)
	if _, err := rand.Read(identifier); err != nil {
		return "", err
	}
	return hex.EncodeToString(identifier) + ".pdf", nil
}
