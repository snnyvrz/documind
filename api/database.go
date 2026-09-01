package main

import (
	"context"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type documentStore interface {
	Create(context.Context, document) error
	ClaimNext(context.Context) (*document, error)
	Complete(context.Context, string, string, []documentChunk) error
	Fail(context.Context, string, string) error
	Find(context.Context, string) (*document, error)
	SearchChunks(context.Context, string, string, int) ([]documentChunk, error)
}

type documentChunk struct {
	ID          string `gorm:"type:uuid;primaryKey"`
	DocumentID  string `gorm:"type:uuid;index;uniqueIndex:document_chunk_index"`
	ChunkIndex  int    `gorm:"uniqueIndex:document_chunk_index"`
	Text        string `gorm:"type:text"`
	StartOffset uint64
	EndOffset   uint64
	Embedding   string `gorm:"type:vector(768)"`
	CreatedAt   time.Time
}

type document struct {
	ID                string `gorm:"type:uuid;primaryKey"`
	OriginalFilename  string
	StoredPath        string
	MIMEType          string
	Size              int64
	Status            string
	ExtractedTextPath *string
	ExtractedText     *string `gorm:"type:text"`
	Result            *string `gorm:"type:jsonb"`
	ErrorMessage      *string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type postgresDocumentStore struct {
	database *gorm.DB
	close    func() error
}

func openDocumentStore(databaseURL string) (*postgresDocumentStore, error) {
	database, err := gorm.Open(postgres.Open(databaseURL), &gorm.Config{})
	if err != nil {
		return nil, err
	}

	connection, err := database.DB()
	if err != nil {
		return nil, err
	}
	if err := connection.Ping(); err != nil {
		connection.Close()
		return nil, err
	}

	store := &postgresDocumentStore{database: database, close: connection.Close}
	if err := store.initialize(); err != nil {
		connection.Close()
		return nil, err
	}

	return store, nil
}

func (s *postgresDocumentStore) Close() error {
	return s.close()
}

func (s *postgresDocumentStore) initialize() error {
	if err := s.database.Exec("CREATE EXTENSION IF NOT EXISTS vector").Error; err != nil {
		return err
	}
	return s.database.AutoMigrate(&document{}, &documentChunk{})
}

func (s *postgresDocumentStore) Create(ctx context.Context, document document) error {
	return s.database.WithContext(ctx).Create(&document).Error
}

func (s *postgresDocumentStore) ClaimNext(ctx context.Context) (*document, error) {
	var result document
	err := s.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Raw(`UPDATE documents SET status = 'processing', updated_at = NOW()
WHERE id = (SELECT id FROM documents WHERE status = 'queued' ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1)
RETURNING *`).Scan(&result).Error; err != nil {
			return err
		}
		if result.ID == "" {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *postgresDocumentStore) Complete(ctx context.Context, id, text string, chunks []documentChunk) error {
	return s.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("document_id = ?", id).Delete(&documentChunk{}).Error; err != nil {
			return err
		}
		if len(chunks) > 0 {
			if err := tx.Create(&chunks).Error; err != nil {
				return err
			}
		}
		return tx.Model(&document{}).Where("id = ?", id).
			Updates(map[string]any{"status": "completed", "extracted_text": text, "updated_at": time.Now().UTC()}).Error
	})
}

func (s *postgresDocumentStore) Fail(ctx context.Context, id, message string) error {
	return s.database.WithContext(ctx).Model(&document{}).Where("id = ?", id).
		Updates(map[string]any{"status": "failed", "error_message": message, "updated_at": time.Now().UTC()}).Error
}

func (s *postgresDocumentStore) Find(ctx context.Context, id string) (*document, error) {
	var result document
	if err := s.database.WithContext(ctx).First(&result, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *postgresDocumentStore) SearchChunks(ctx context.Context, documentID, embedding string, limit int) ([]documentChunk, error) {
	var chunks []documentChunk
	err := s.database.WithContext(ctx).Raw(`SELECT id, document_id, chunk_index, text, start_offset, end_offset, created_at
FROM document_chunks WHERE document_id = ? ORDER BY embedding <=> ?::vector LIMIT ?`, documentID, embedding, limit).Scan(&chunks).Error
	return chunks, err
}
