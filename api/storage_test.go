package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/labstack/echo/v5"
)

func TestFilesystemStorageRange(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "documents", "id", "original.pdf")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	storage := &filesystemStorage{root: root}
	info, err := storage.Stat(context.Background(), "documents/id/original.pdf")
	if err != nil || info.Size != 10 {
		t.Fatalf("stat = %#v, %v", info, err)
	}
	reader, err := storage.GetRange(context.Background(), "documents/id/original.pdf", 3, 6)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	content, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "3456" {
		t.Fatalf("range content = %q", content)
	}
}

func TestServeStoredFileRangeDoesNotReadWholeFile(t *testing.T) {
	storage := &recordingStorage{content: []byte("0123456789")}
	e := echo.New()
	record := httptest.NewRequest(http.MethodGet, "/file", nil)
	record.Header.Set("Range", "bytes=2-5")
	response := httptest.NewRecorder()
	context := e.NewContext(record, response)
	document := &document{StoredPath: "documents/id/original.pdf", OriginalFilename: "file.pdf", MIMEType: "application/pdf"}

	if err := serveStoredFile(context, storage, document, storageInfo{Size: 10}); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusPartialContent || response.Body.String() != "2345" {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	if storage.rangeStart != 2 || storage.rangeEnd != 5 || storage.fullReads != 0 {
		t.Fatalf("storage calls = %#v", storage)
	}
}

type recordingStorage struct {
	content              []byte
	rangeStart, rangeEnd int64
	fullReads            int
}

func (s *recordingStorage) Put(context.Context, string, io.Reader, int64, string) error { return nil }
func (s *recordingStorage) Get(context.Context, string) (io.ReadCloser, error) {
	s.fullReads++
	return io.NopCloser(bytesReader(s.content)), nil
}
func (s *recordingStorage) Stat(context.Context, string) (storageInfo, error) {
	return storageInfo{Size: int64(len(s.content))}, nil
}
func (s *recordingStorage) GetRange(_ context.Context, _ string, start, end int64) (io.ReadCloser, error) {
	s.rangeStart, s.rangeEnd = start, end
	return io.NopCloser(bytesReader(s.content[start : end+1])), nil
}
func (s *recordingStorage) Delete(context.Context, string) error                  { return nil }
func (s *recordingStorage) List(context.Context, string) ([]storageObject, error) { return nil, nil }

func bytesReader(value []byte) io.Reader { return &byteReader{value: value} }

type byteReader struct {
	value  []byte
	offset int
}

func (r *byteReader) Read(p []byte) (int, error) {
	if r.offset == len(r.value) {
		return 0, io.EOF
	}
	n := copy(p, r.value[r.offset:])
	r.offset += n
	return n, nil
}
