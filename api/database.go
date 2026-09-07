package main

import (
	"context"
	"errors"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type documentStore interface {
	Create(context.Context, document) error
	List(context.Context) ([]document, error)
	Delete(context.Context, string) error
	ClaimNext(context.Context, string, time.Duration, int) (*document, error)
	RenewLease(context.Context, string, string, time.Duration) error
	Retry(context.Context, string, string, time.Time) error
	Complete(context.Context, string, string, string, uint32, []documentChunk) error
	Fail(context.Context, string, string, string) error
	Find(context.Context, string) (*document, error)
	ListChunks(context.Context, string) ([]documentChunk, error)
	SearchChunks(context.Context, string, string, int) ([]documentChunk, error)
}

var errLeaseLost = errors.New("document processing lease lost")

type documentChunk struct {
	ID          string `gorm:"type:uuid;primaryKey"`
	DocumentID  string `gorm:"type:uuid;index;uniqueIndex:document_chunk_index"`
	ChunkIndex  int    `gorm:"uniqueIndex:document_chunk_index"`
	Text        string `gorm:"type:text"`
	StartOffset uint64
	EndOffset   uint64
	PageStart   uint32
	PageEnd     uint32
	Embedding   string `gorm:"type:vector(768)"`
	CreatedAt   time.Time
}

type document struct {
	ID                string `gorm:"type:uuid;primaryKey"`
	OriginalFilename  string
	StoredPath        string
	MIMEType          string
	Size              int64
	PageCount         uint32
	Status            string
	ExtractedTextPath *string
	ExtractedText     *string `gorm:"type:text"`
	Result            *string `gorm:"type:jsonb"`
	ErrorMessage      *string
	AttemptCount      int `gorm:"not null;default:0"`
	NextAttemptAt     *time.Time
	LeaseToken        *string
	LeaseExpiresAt    *time.Time
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
	if err := s.database.AutoMigrate(&document{}, &documentChunk{}); err != nil {
		return err
	}
	if err := s.database.Exec(`CREATE INDEX IF NOT EXISTS idx_documents_queued_jobs
ON documents (next_attempt_at, created_at, id) WHERE status = 'queued'`).Error; err != nil {
		return err
	}
	return s.database.Exec(`CREATE INDEX IF NOT EXISTS idx_documents_expired_leases
ON documents (lease_expires_at, created_at, id) WHERE status = 'processing'`).Error
}

func (s *postgresDocumentStore) Create(ctx context.Context, document document) error {
	return s.database.WithContext(ctx).Create(&document).Error
}

func (s *postgresDocumentStore) List(ctx context.Context) ([]document, error) {
	var result []document
	err := s.database.WithContext(ctx).Order("created_at DESC").Find(&result).Error
	return result, err
}

func (s *postgresDocumentStore) Delete(ctx context.Context, id string) error {
	return s.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("document_id = ?", id).Delete(&documentChunk{}).Error; err != nil {
			return err
		}
		result := tx.Delete(&document{}, "id = ?", id)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

func (s *postgresDocumentStore) ClaimNext(ctx context.Context, leaseToken string, leaseDuration time.Duration, maxAttempts int) (*document, error) {
	var result document
	err := s.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		exhausted := tx.Exec(`UPDATE documents
SET status = 'failed', error_message = 'document processing failed after maximum attempts',
    next_attempt_at = NULL, lease_token = NULL, lease_expires_at = NULL, updated_at = NOW()
WHERE attempt_count >= ? AND (
    (status = 'queued' AND (next_attempt_at IS NULL OR next_attempt_at <= NOW())) OR
    (status = 'processing' AND (lease_expires_at IS NULL OR lease_expires_at <= NOW()))
)`, maxAttempts)
		if exhausted.Error != nil {
			return exhausted.Error
		}

		if err := tx.Raw(`WITH candidate AS (
    SELECT id FROM documents
    WHERE attempt_count < ? AND (
        (status = 'queued' AND (next_attempt_at IS NULL OR next_attempt_at <= NOW())) OR
        (status = 'processing' AND (lease_expires_at IS NULL OR lease_expires_at <= NOW()))
    )
    ORDER BY COALESCE(next_attempt_at, created_at), created_at, id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE documents
SET status = 'processing', attempt_count = attempt_count + 1, next_attempt_at = NULL,
    lease_token = ?, lease_expires_at = NOW() + (? * INTERVAL '1 millisecond'),
    error_message = NULL, updated_at = NOW()
WHERE id = (SELECT id FROM candidate)
RETURNING *`, maxAttempts, leaseToken, leaseDuration.Milliseconds()).Scan(&result).Error; err != nil {
			return err
		}
		if result.ID == "" {
			return nil
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if result.ID == "" {
		return nil, gorm.ErrRecordNotFound
	}
	return &result, nil
}

func (s *postgresDocumentStore) RenewLease(ctx context.Context, id, leaseToken string, leaseDuration time.Duration) error {
	result := s.database.WithContext(ctx).Model(&document{}).
		Where("id = ? AND status = 'processing' AND lease_token = ? AND lease_expires_at > NOW()", id, leaseToken).
		Updates(map[string]any{"lease_expires_at": gorm.Expr("NOW() + (? * INTERVAL '1 millisecond')", leaseDuration.Milliseconds()), "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errLeaseLost
	}
	return nil
}

func (s *postgresDocumentStore) Retry(ctx context.Context, id, leaseToken string, nextAttemptAt time.Time) error {
	result := s.database.WithContext(ctx).Model(&document{}).
		Where("id = ? AND status = 'processing' AND lease_token = ? AND lease_expires_at > NOW()", id, leaseToken).
		Updates(map[string]any{"status": "queued", "next_attempt_at": nextAttemptAt, "lease_token": nil, "lease_expires_at": nil, "error_message": nil, "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errLeaseLost
	}
	return nil
}

func (s *postgresDocumentStore) Complete(ctx context.Context, id, leaseToken, text string, pageCount uint32, chunks []documentChunk) error {
	return s.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&document{}).
			Where("id = ? AND status = 'processing' AND lease_token = ? AND lease_expires_at > NOW()", id, leaseToken).
			Updates(map[string]any{"status": "completed", "extracted_text": text, "page_count": pageCount, "error_message": nil, "next_attempt_at": nil, "lease_token": nil, "lease_expires_at": nil, "updated_at": time.Now().UTC()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errLeaseLost
		}
		if err := tx.Where("document_id = ?", id).Delete(&documentChunk{}).Error; err != nil {
			return err
		}
		if len(chunks) > 0 {
			if err := tx.Create(&chunks).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *postgresDocumentStore) Fail(ctx context.Context, id, leaseToken, message string) error {
	result := s.database.WithContext(ctx).Model(&document{}).
		Where("id = ? AND status = 'processing' AND lease_token = ? AND lease_expires_at > NOW()", id, leaseToken).
		Updates(map[string]any{"status": "failed", "error_message": message, "next_attempt_at": nil, "lease_token": nil, "lease_expires_at": nil, "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errLeaseLost
	}
	return nil
}

func (s *postgresDocumentStore) Find(ctx context.Context, id string) (*document, error) {
	var result document
	if err := s.database.WithContext(ctx).First(&result, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *postgresDocumentStore) ListChunks(ctx context.Context, documentID string) ([]documentChunk, error) {
	var chunks []documentChunk
	err := s.database.WithContext(ctx).Where("document_id = ?", documentID).Order("chunk_index ASC").Find(&chunks).Error
	return chunks, err
}

func (s *postgresDocumentStore) SearchChunks(ctx context.Context, documentID, embedding string, limit int) ([]documentChunk, error) {
	var chunks []documentChunk
	err := s.database.WithContext(ctx).Raw(`SELECT id, document_id, chunk_index, text, start_offset, end_offset, page_start, page_end, created_at
FROM document_chunks WHERE document_id = ? ORDER BY embedding <=> ?::vector LIMIT ?`, documentID, embedding, limit).Scan(&chunks).Error
	return chunks, err
}
