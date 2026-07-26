package main

import (
	"context"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type documentStore interface {
	Create(context.Context, document) error
}

type document struct {
	ID                string `gorm:"type:uuid;primaryKey"`
	OriginalFilename  string
	StoredPath        string
	MIMEType          string
	Size              int64
	Status            string
	ExtractedTextPath *string
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
