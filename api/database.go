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
	Complete(context.Context, string, string) error
	Fail(context.Context, string, string) error
	Find(context.Context, string) (*document, error)
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
	return s.database.AutoMigrate(&document{})
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

func (s *postgresDocumentStore) Complete(ctx context.Context, id, text string) error {
	return s.database.WithContext(ctx).Model(&document{}).Where("id = ?", id).
		Updates(map[string]any{"status": "completed", "extracted_text": text, "updated_at": time.Now().UTC()}).Error
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
