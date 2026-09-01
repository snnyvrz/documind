package main

import (
	"bytes"
	"context"
	"encoding/json"
	"gorm.io/gorm"
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

func (s *memoryDocumentStore) ClaimNext(_ context.Context) (*document, error) {
	for index := range s.documents {
		if s.documents[index].Status == "queued" {
			s.documents[index].Status = "processing"
			return &s.documents[index], nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (s *memoryDocumentStore) Complete(_ context.Context, id, text string) error {
	for index := range s.documents {
		if s.documents[index].ID == id {
			s.documents[index].Status = "completed"
			s.documents[index].ExtractedText = &text
			return nil
		}
	}
	return gorm.ErrRecordNotFound
}

func (s *memoryDocumentStore) Fail(_ context.Context, id, message string) error {
	for index := range s.documents {
		if s.documents[index].ID == id {
			s.documents[index].Status = "failed"
			s.documents[index].ErrorMessage = &message
			return nil
		}
	}
	return gorm.ErrRecordNotFound
}

func (s *memoryDocumentStore) Find(_ context.Context, id string) (*document, error) {
	for index := range s.documents {
		if s.documents[index].ID == id {
			return &s.documents[index], nil
		}
	}
	return nil, gorm.ErrRecordNotFound
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

	if response.Code != http.StatusAccepted {
		t.Fatalf("upload status = %d, want %d", response.Code, http.StatusAccepted)
	}
	var responseBody struct {
		DocumentID string `json:"documentId"`
		Status     string `json:"status"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &responseBody); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	if responseBody.DocumentID == "" || responseBody.Status != "queued" {
		t.Fatalf("upload response = %+v, want documentId and queued status", responseBody)
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
	if document.ID != responseBody.DocumentID || document.OriginalFilename != "document.pdf" || document.StoredPath != filepath.Join("documents", responseBody.DocumentID, "original.pdf") || document.MIMEType != "application/pdf" || document.Size != int64(len("%PDF-1.7\nexample document")) || document.Status != "queued" {
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

func TestGetDocument(t *testing.T) {
	text := "extracted text"
	store := &memoryDocumentStore{documents: []document{{ID: "document-id", OriginalFilename: "document.pdf", Status: "completed", ExtractedText: &text}}}
	server := newServer(t.TempDir(), store)

	request := httptest.NewRequest(http.MethodGet, "/documents/document-id", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["status"] != "completed" || body["text"] != text {
		t.Fatalf("response = %+v", body)
	}
}
