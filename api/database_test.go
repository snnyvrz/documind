package main

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestClaimNextCommitsExhaustionCleanupWhenNoJobIsAvailable(t *testing.T) {
	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create SQL mock: %v", err)
	}
	defer database.Close()

	db, err := gorm.Open(postgres.New(postgres.Config{Conn: database, DriverName: "postgres"}), &gorm.Config{})
	if err != nil {
		t.Fatalf("open GORM database: %v", err)
	}
	store := &postgresDocumentStore{database: db}

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE documents
 SET status = 'failed', error_message = 'document processing failed after maximum attempts',
     next_attempt_at = NULL, lease_token = NULL, lease_expires_at = NULL, updated_at = NOW()
 WHERE attempt_count >= $1 AND (
     (status = 'queued' AND (next_attempt_at IS NULL OR next_attempt_at <= NOW())) OR
     (status = 'processing' AND (lease_expires_at IS NULL OR lease_expires_at <= NOW()))
 )`)).
		WithArgs(5).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta(`WITH candidate AS (
     SELECT id FROM documents
     WHERE attempt_count < $1 AND (
         (status = 'queued' AND (next_attempt_at IS NULL OR next_attempt_at <= NOW())) OR
         (status = 'processing' AND (lease_expires_at IS NULL OR lease_expires_at <= NOW()))
     )
     ORDER BY COALESCE(next_attempt_at, created_at), created_at, id
     FOR UPDATE SKIP LOCKED
     LIMIT 1
 )
 UPDATE documents
 SET status = 'processing', attempt_count = attempt_count + 1, next_attempt_at = NULL,
     lease_token = $2, lease_expires_at = NOW() + ($3 * INTERVAL '1 millisecond'),
     error_message = NULL, updated_at = NOW()
 WHERE id = (SELECT id FROM candidate)
 RETURNING *`)).
		WithArgs(5, "lease-token", int64(time.Minute.Milliseconds())).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectCommit()

	_, err = store.ClaimNext(context.Background(), "lease-token", time.Minute, 5)
	if err != gorm.ErrRecordNotFound {
		t.Fatalf("claim error = %v, want no eligible job", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL expectations: %v", err)
	}
}
