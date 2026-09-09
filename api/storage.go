package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type documentStorage interface {
	Put(context.Context, string, io.Reader, int64, string) error
	Get(context.Context, string) (io.ReadCloser, error)
	Delete(context.Context, string) error
}

type filesystemStorage struct{ root string }

func newFilesystemStorage(root string) documentStorage { return &filesystemStorage{root: root} }

func (s *filesystemStorage) path(key string) string {
	return filepath.Join(s.root, filepath.FromSlash(filepath.Clean("/"+key)))
}

func (s *filesystemStorage) Put(_ context.Context, key string, source io.Reader, _ int64, _ string) error {
	path := s.path(key)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".upload-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := io.Copy(temporary, source); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Chmod(temporaryPath, 0o600); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func (s *filesystemStorage) Get(_ context.Context, key string) (io.ReadCloser, error) {
	return os.Open(s.path(key))
}

func (s *filesystemStorage) Delete(_ context.Context, key string) error {
	err := os.RemoveAll(filepath.Dir(s.path(key)))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

type objectStorage struct {
	client *minio.Client
	bucket string
}

func newObjectStorage(ctx context.Context) (documentStorage, error) {
	endpoint := getenv("OBJECT_STORAGE_ENDPOINT", "")
	if endpoint == "" {
		return nil, os.ErrNotExist
	}
	secure := getenv("OBJECT_STORAGE_SECURE", "false") == "true"
	client, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(os.Getenv("OBJECT_STORAGE_ACCESS_KEY"), os.Getenv("OBJECT_STORAGE_SECRET_KEY"), ""), Secure: secure})
	if err != nil {
		return nil, err
	}
	bucket := getenv("OBJECT_STORAGE_BUCKET", "documind")
	found, err := client.BucketExists(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if !found {
		if err := client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
			return nil, err
		}
	}
	return &objectStorage{client: client, bucket: bucket}, nil
}

func (s *objectStorage) Put(ctx context.Context, key string, source io.Reader, size int64, contentType string) error {
	_, err := s.client.PutObject(ctx, s.bucket, strings.TrimPrefix(key, "/"), source, size, minio.PutObjectOptions{ContentType: contentType})
	return err
}

func (s *objectStorage) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	return s.client.GetObject(ctx, s.bucket, strings.TrimPrefix(key, "/"), minio.GetObjectOptions{})
}

func (s *objectStorage) Delete(ctx context.Context, key string) error {
	return s.client.RemoveObject(ctx, s.bucket, strings.TrimPrefix(key, "/"), minio.RemoveObjectOptions{})
}
