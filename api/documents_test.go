package main

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestUploadPDF(t *testing.T) {
	uploadDirectory := t.TempDir()
	server := newServer(uploadDirectory)

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
	files, err := os.ReadDir(uploadDirectory)
	if err != nil {
		t.Fatalf("read upload directory: %v", err)
	}
	if len(files) != 1 || filepath.Ext(files[0].Name()) != ".pdf" {
		t.Fatalf("uploaded files = %v, want one PDF", files)
	}
	saved, err := os.ReadFile(filepath.Join(uploadDirectory, files[0].Name()))
	if err != nil {
		t.Fatalf("read uploaded PDF: %v", err)
	}
	if !bytes.Equal(saved, []byte("%PDF-1.7\nexample document")) {
		t.Fatalf("saved PDF content = %q", saved)
	}
}

func TestUploadRejectsNonPDF(t *testing.T) {
	uploadDirectory := t.TempDir()
	server := newServer(uploadDirectory)

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
