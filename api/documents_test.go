package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

type memoryDocumentStore struct {
	documents []document
}

func (s *memoryDocumentStore) Create(_ context.Context, document document) error {
	s.documents = append(s.documents, document)
	return nil
}

func TestUploadPDF(t *testing.T) {
	uploadDirectory := t.TempDir()
	store := &memoryDocumentStore{}
	server := newServer(uploadDirectory, store)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	file, err := writer.CreateFormFile("file", "document.pdf")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := file.Write([]byte("%PDF-1.7\nexample document")); err != nil {
		t.Fatalf("write PDF: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/documents", body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, want %d", response.Code, http.StatusCreated)
	}
	var responseBody struct {
		DocumentID string `json:"documentId"`
		Status     string `json:"status"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &responseBody); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	if responseBody.DocumentID == "" || responseBody.Status != "uploaded" {
		t.Fatalf("upload response = %+v, want documentId and uploaded status", responseBody)
	}
	saved, err := os.ReadFile(filepath.Join(uploadDirectory, "documents", responseBody.DocumentID, "original.pdf"))
	if err != nil {
		t.Fatalf("read uploaded PDF: %v", err)
	}
	if !bytes.Equal(saved, []byte("%PDF-1.7\nexample document")) {
		t.Fatalf("saved PDF content = %q", saved)
	}
	if len(store.documents) != 1 {
		t.Fatalf("saved documents = %d, want 1", len(store.documents))
	}
	document := store.documents[0]
	if document.ID != responseBody.DocumentID || document.OriginalFilename != "document.pdf" || document.StoredPath != filepath.Join("documents", responseBody.DocumentID, "original.pdf") || document.MIMEType != "application/pdf" || document.Size != int64(len("%PDF-1.7\nexample document")) || document.Status != "uploaded" {
		t.Fatalf("saved metadata = %+v", document)
	}
}

func TestUploadRejectsNonPDF(t *testing.T) {
	uploadDirectory := t.TempDir()
	server := newServer(uploadDirectory, &memoryDocumentStore{})

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	file, err := writer.CreateFormFile("file", "notes.txt")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := io.WriteString(file, "not a PDF"); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/documents", body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("upload status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}
