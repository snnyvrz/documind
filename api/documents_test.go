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
	"sync"
	"testing"
	"time"
)

type memoryDocumentStore struct {
	documents []document
	chunks    []documentChunk
	mutex     sync.Mutex
}

func (s *memoryDocumentStore) Create(_ context.Context, document document) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.documents = append(s.documents, document)
	return nil
}

func (s *memoryDocumentStore) ClaimNext(_ context.Context, leaseToken string, leaseDuration time.Duration, maxAttempts int) (*document, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	now := time.Now().UTC()
	for index := range s.documents {
		document := &s.documents[index]
		eligible := document.Status == "queued" && (document.NextAttemptAt == nil || !document.NextAttemptAt.After(now))
		eligible = eligible || document.Status == "processing" && (document.LeaseExpiresAt == nil || !document.LeaseExpiresAt.After(now))
		if eligible && document.AttemptCount >= maxAttempts {
			message := "document processing failed after maximum attempts"
			document.Status = "failed"
			document.ErrorMessage = &message
			document.NextAttemptAt = nil
			document.LeaseToken = nil
			document.LeaseExpiresAt = nil
		}
	}
	for index := range s.documents {
		document := &s.documents[index]
		eligible := document.Status == "queued" && (document.NextAttemptAt == nil || !document.NextAttemptAt.After(now))
		eligible = eligible || document.Status == "processing" && (document.LeaseExpiresAt == nil || !document.LeaseExpiresAt.After(now))
		if eligible && document.AttemptCount < maxAttempts {
			expiresAt := now.Add(leaseDuration)
			document.Status = "processing"
			document.AttemptCount++
			document.NextAttemptAt = nil
			document.ErrorMessage = nil
			document.LeaseToken = &leaseToken
			document.LeaseExpiresAt = &expiresAt
			claimed := *document
			return &claimed, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (s *memoryDocumentStore) RenewLease(_ context.Context, id, leaseToken string, leaseDuration time.Duration) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	for index := range s.documents {
		document := &s.documents[index]
		if activeLease(document, id, leaseToken) {
			expiresAt := time.Now().UTC().Add(leaseDuration)
			document.LeaseExpiresAt = &expiresAt
			return nil
		}
	}
	return errLeaseLost
}

func (s *memoryDocumentStore) Retry(_ context.Context, id, leaseToken string, nextAttemptAt time.Time) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	for index := range s.documents {
		document := &s.documents[index]
		if activeLease(document, id, leaseToken) {
			document.Status = "queued"
			document.NextAttemptAt = &nextAttemptAt
			document.LeaseToken = nil
			document.LeaseExpiresAt = nil
			document.ErrorMessage = nil
			return nil
		}
	}
	return errLeaseLost
}

func (s *memoryDocumentStore) Complete(_ context.Context, id, leaseToken, text string, pageCount uint32, chunks []documentChunk) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	for index := range s.documents {
		document := &s.documents[index]
		if activeLease(document, id, leaseToken) {
			document.Status = "completed"
			document.ExtractedText = &text
			document.PageCount = pageCount
			document.LeaseToken = nil
			document.LeaseExpiresAt = nil
			s.chunks = append([]documentChunk(nil), chunks...)
			return nil
		}
	}
	return errLeaseLost
}

func (s *memoryDocumentStore) Fail(_ context.Context, id, leaseToken, message string) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	for index := range s.documents {
		document := &s.documents[index]
		if activeLease(document, id, leaseToken) {
			document.Status = "failed"
			document.ErrorMessage = &message
			document.LeaseToken = nil
			document.LeaseExpiresAt = nil
			return nil
		}
	}
	return errLeaseLost
}

func activeLease(document *document, id, leaseToken string) bool {
	return document.ID == id && document.Status == "processing" && document.LeaseToken != nil && *document.LeaseToken == leaseToken && document.LeaseExpiresAt != nil && document.LeaseExpiresAt.After(time.Now().UTC())
}

func (s *memoryDocumentStore) Find(_ context.Context, id string) (*document, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	for index := range s.documents {
		if s.documents[index].ID == id {
			result := s.documents[index]
			return &result, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (s *memoryDocumentStore) SearchChunks(_ context.Context, _ string, _ string, _ int) ([]documentChunk, error) {
	return nil, nil
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
	nextAttemptAt := time.Now().UTC().Add(time.Minute).Truncate(time.Second)
	store := &memoryDocumentStore{documents: []document{{ID: "document-id", OriginalFilename: "document.pdf", Status: "completed", ExtractedText: &text, AttemptCount: 2, NextAttemptAt: &nextAttemptAt}}}
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
	if body["attemptCount"] != float64(2) || body["nextAttemptAt"] == nil {
		t.Fatalf("retry metadata = %+v", body)
	}
}
