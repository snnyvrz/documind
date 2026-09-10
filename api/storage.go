package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type documentStorage interface {
	Put(context.Context, string, io.Reader, int64, string) error
	Get(context.Context, string) (io.ReadCloser, error)
	Delete(context.Context, string) error
	List(context.Context, string) ([]storageObject, error)
}

type storageObject struct {
	Key          string
	LastModified time.Time
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

func (s *filesystemStorage) List(_ context.Context, prefix string) ([]storageObject, error) {
	root := s.path(prefix)
	objects := make([]storageObject, 0)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		key, err := filepath.Rel(s.root, path)
		if err != nil {
			return err
		}
		objects = append(objects, storageObject{Key: filepath.ToSlash(key), LastModified: info.ModTime()})
		return nil
	})
	return objects, err
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

func (s *objectStorage) List(ctx context.Context, prefix string) ([]storageObject, error) {
	objects := make([]storageObject, 0)
	for object := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: strings.TrimPrefix(prefix, "/"), Recursive: true}) {
		if object.Err != nil {
			return nil, object.Err
		}
		objects = append(objects, storageObject{Key: object.Key, LastModified: object.LastModified})
	}
	return objects, nil
}

func reconcileOrphanedUploads(ctx context.Context, storage documentStorage, references uploadReferenceStore) error {
	objects, err := storage.List(ctx, "documents/")
	if err != nil {
		return err
	}
	minimumAge := environmentDurationDefault("UPLOAD_ORPHAN_MIN_AGE", time.Hour)
	cutoff := time.Now().UTC().Add(-minimumAge)
	for _, object := range objects {
		if object.LastModified.After(cutoff) {
			continue
		}
		parts := strings.Split(object.Key, "/")
		if len(parts) != 3 || parts[0] != "documents" || parts[2] != "original.pdf" {
			continue
		}
		exists, err := references.UploadReferenceExists(ctx, parts[1])
		if err != nil {
			return err
		}
		if !exists {
			if err := storage.Delete(ctx, object.Key); err != nil {
				return err
			}
		}
	}
	return nil
}
