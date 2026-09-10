package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type documentStore interface {
	Ping(context.Context) error
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
	CreateOwned(context.Context, string, document) error
	ListOwned(context.Context, string) ([]document, error)
	DeleteOwned(context.Context, string, string) error
	RetryOwned(context.Context, string, string) error
	FindOwned(context.Context, string, string) (*document, error)
	ListChunksOwned(context.Context, string, string) ([]documentChunk, error)
	SearchChunksOwned(context.Context, string, string, string, int) ([]documentChunk, error)
	CreateQuestion(context.Context, documentQuestion) error
	ListQuestionsOwned(context.Context, string, string) ([]documentQuestion, error)
}

type documentCursor struct {
	CreatedAt time.Time
	ID        string
}

type documentSummaryRow struct {
	ID               string
	OriginalFilename string
	Status           string
	PageCount        uint32
	ErrorMessage     *string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type quotaStore interface {
	ReserveUpload(context.Context, string, string, string, int64, time.Time) error
	CommitUpload(context.Context, string) error
	ReleaseUpload(context.Context, string) error
	BeginAnswer(context.Context, string, string, time.Time) error
	EndAnswer(context.Context, string, string) error
	AllowRate(context.Context, string, string, int, time.Duration, time.Time) (bool, error)
	DeleteOwnedWithUsage(context.Context, string, string) (*document, error)
}

type atomicUploadStore interface {
	CreateOwnedAndCommitUpload(context.Context, string, document, string) error
}

type uploadReferenceStore interface {
	UploadReferenceExists(context.Context, string) (bool, error)
}

type queueStatsStore interface {
	QueueStats(context.Context) (uint64, time.Time, error)
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

type documentQuestion struct {
	ID         string    `gorm:"type:uuid;primaryKey" json:"id"`
	OwnerID    string    `gorm:"not null;index" json:"-"`
	DocumentID string    `gorm:"type:uuid;index" json:"documentId"`
	Question   string    `gorm:"type:text" json:"question"`
	Answer     string    `gorm:"type:text" json:"answer"`
	Sources    string    `gorm:"type:jsonb" json:"sources"`
	CreatedAt  time.Time `json:"createdAt"`
}

type document struct {
	ID                string `gorm:"type:uuid;primaryKey"`
	OwnerID           string `gorm:"not null;index"`
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

type dbOwnerUsage struct {
	OwnerID            string `gorm:"primaryKey"`
	CommittedBytes     int64  `gorm:"not null;default:0"`
	CommittedDocuments int    `gorm:"not null;default:0"`
	ReservedBytes      int64  `gorm:"not null;default:0"`
	ReservedDocuments  int    `gorm:"not null;default:0"`
	ActiveAnswers      int    `gorm:"not null;default:0"`
	UpdatedAt          time.Time
}

type uploadReservation struct {
	ID          string    `gorm:"primaryKey"`
	OwnerID     string    `gorm:"not null;index"`
	DocumentID  string    `gorm:"not null;index"`
	Bytes       int64     `gorm:"not null"`
	State       string    `gorm:"not null;index"`
	ExpiresAt   time.Time `gorm:"not null;index"`
	CreatedAt   time.Time
	CommittedAt *time.Time
	ReleasedAt  *time.Time
}

type answerReservation struct {
	ID         string    `gorm:"primaryKey"`
	OwnerID    string    `gorm:"not null;index"`
	RequestID  string    `gorm:"not null;uniqueIndex"`
	State      string    `gorm:"not null;index"`
	ExpiresAt  time.Time `gorm:"not null;index"`
	CreatedAt  time.Time
	FinishedAt *time.Time
}

type rateLimitBucket struct {
	Kind        string    `gorm:"primaryKey"`
	Key         string    `gorm:"primaryKey"`
	WindowStart time.Time `gorm:"primaryKey"`
	Count       int       `gorm:"not null;default:0"`
}

type postgresDocumentStore struct {
	database *gorm.DB
	close    func() error
}

func (s *postgresDocumentStore) Ping(ctx context.Context) error {
	database, err := s.database.DB()
	if err != nil {
		return err
	}
	return database.PingContext(ctx)
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
	connection.SetMaxOpenConns(environmentIntDefault("DB_MAX_OPEN_CONNS", 25))
	connection.SetMaxIdleConns(environmentIntDefault("DB_MAX_IDLE_CONNS", 5))
	connection.SetConnMaxLifetime(environmentDurationDefault("DB_CONN_MAX_LIFETIME", 30*time.Minute))
	connection.SetConnMaxIdleTime(environmentDurationDefault("DB_CONN_MAX_IDLE_TIME", 5*time.Minute))
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
	if s.database.Migrator().HasTable(&document{}) {
		if !s.database.Migrator().HasColumn(&document{}, "OwnerID") {
			if err := s.database.Exec("ALTER TABLE documents ADD COLUMN owner_id text").Error; err != nil {
				return err
			}
		}
		var missing int64
		if err := s.database.Model(&document{}).Where("owner_id IS NULL OR owner_id = ''").Count(&missing).Error; err != nil {
			return err
		}
		if missing > 0 {
			owner := getenv("LEGACY_DOCUMENT_OWNER", "")
			if owner == "" {
				return errors.New("legacy documents require LEGACY_DOCUMENT_OWNER before authentication can start")
			}
			if err := s.database.Model(&document{}).Where("owner_id IS NULL OR owner_id = ''").Update("owner_id", owner).Error; err != nil {
				return err
			}
		}
		if err := s.database.Exec("ALTER TABLE documents ALTER COLUMN owner_id SET NOT NULL").Error; err != nil {
			return err
		}
	}
	if err := s.database.AutoMigrate(&document{}, &documentChunk{}, &documentQuestion{}, &authUser{}, &dbOwnerUsage{}, &uploadReservation{}, &answerReservation{}, &rateLimitBucket{}); err != nil {
		return err
	}
	if err := s.database.Exec(`CREATE INDEX IF NOT EXISTS idx_documents_queued_jobs
ON documents (next_attempt_at, created_at, id) WHERE status = 'queued'`).Error; err != nil {
		return err
	}
	if err := s.database.Exec(`CREATE INDEX IF NOT EXISTS idx_documents_owner_created_id ON documents (owner_id, created_at DESC, id DESC)`).Error; err != nil {
		return err
	}
	if err := s.database.Exec(`INSERT INTO db_owner_usages (owner_id, committed_bytes, committed_documents, updated_at)
SELECT owner_id, COALESCE(SUM(size), 0), COUNT(*), NOW() FROM documents GROUP BY owner_id
ON CONFLICT (owner_id) DO NOTHING`).Error; err != nil {
		return err
	}
	if err := s.RecoverExpiredReservations(context.Background()); err != nil {
		return err
	}
	return s.database.Exec(`CREATE INDEX IF NOT EXISTS idx_documents_expired_leases
ON documents (lease_expires_at, created_at, id) WHERE status = 'processing'`).Error
}

func environmentIntDefault(name string, fallback int) int {
	value := getenv(name, "")
	if value == "" {
		return fallback
	}
	var parsed int
	if _, err := fmt.Sscanf(value, "%d", &parsed); err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func environmentDurationDefault(name string, fallback time.Duration) time.Duration {
	value := getenv(name, "")
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func (s *postgresDocumentStore) Create(ctx context.Context, document document) error {
	return s.database.WithContext(ctx).Create(&document).Error
}

func (s *postgresDocumentStore) CreateOwned(ctx context.Context, ownerID string, document document) error {
	document.OwnerID = ownerID
	return s.Create(ctx, document)
}

func (s *postgresDocumentStore) CreateOwnedAndCommitUpload(ctx context.Context, ownerID string, document document, reservationID string) error {
	return s.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var reservation uploadReservation
		if err := tx.Where("id = ?", reservationID).First(&reservation).Error; err != nil {
			return err
		}
		if reservation.State != "reserved" || reservation.OwnerID != ownerID || reservation.DocumentID != document.ID {
			return errors.New("upload reservation is not available")
		}
		document.OwnerID = ownerID
		if err := tx.Create(&document).Error; err != nil {
			return err
		}
		if err := tx.Model(&dbOwnerUsage{}).Where("owner_id = ?", ownerID).Updates(map[string]any{
			"reserved_bytes":      gorm.Expr("reserved_bytes - ?", reservation.Bytes),
			"reserved_documents":  gorm.Expr("reserved_documents - 1"),
			"committed_bytes":     gorm.Expr("committed_bytes + ?", reservation.Bytes),
			"committed_documents": gorm.Expr("committed_documents + 1"),
			"updated_at":          time.Now().UTC(),
		}).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		return tx.Model(&reservation).Updates(map[string]any{"state": "committed", "committed_at": now}).Error
	})
}

func (s *postgresDocumentStore) List(ctx context.Context) ([]document, error) {
	var result []document
	err := s.database.WithContext(ctx).Order("created_at DESC").Find(&result).Error
	return result, err
}

func (s *postgresDocumentStore) ListOwned(ctx context.Context, ownerID string) ([]document, error) {
	var result []document
	err := s.database.WithContext(ctx).Where("owner_id = ?", ownerID).Order("created_at DESC").Find(&result).Error
	return result, err
}

func (s *postgresDocumentStore) ListOwnedPage(ctx context.Context, ownerID, search string, limit int, cursor *documentCursor) ([]documentSummaryRow, error) {
	query := s.database.WithContext(ctx).Model(&document{}).Select("id, original_filename, status, page_count, error_message, created_at, updated_at").Where("owner_id = ?", ownerID)
	if search != "" {
		query = query.Where("original_filename ILIKE ?", "%"+search+"%")
	}
	if cursor != nil {
		query = query.Where("(created_at, id) < (?, ?)", cursor.CreatedAt, cursor.ID)
	}
	var result []documentSummaryRow
	err := query.Order("created_at DESC, id DESC").Limit(limit).Scan(&result).Error
	return result, err
}

func (s *postgresDocumentStore) ReserveUpload(ctx context.Context, ownerID, reservationID, documentID string, size int64, expiresAt time.Time) error {
	return s.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockOwnerUsage(tx, ownerID); err != nil {
			return err
		}
		if err := recoverExpiredReservations(tx, &ownerID, time.Now().UTC()); err != nil {
			return err
		}
		usage := dbOwnerUsage{OwnerID: ownerID}
		if err := tx.Raw("SELECT * FROM db_owner_usages WHERE owner_id = ?", ownerID).Scan(&usage).Error; err != nil {
			return err
		}
		if usage.CommittedBytes+usage.ReservedBytes+size > maxOwnerStorage || usage.CommittedDocuments+usage.ReservedDocuments+1 > maxOwnerDocuments {
			return errQuotaExceeded
		}
		if err := tx.Model(&dbOwnerUsage{}).Where("owner_id = ?", ownerID).Updates(map[string]any{"reserved_bytes": gorm.Expr("reserved_bytes + ?", size), "reserved_documents": gorm.Expr("reserved_documents + 1"), "updated_at": time.Now().UTC()}).Error; err != nil {
			return err
		}
		return tx.Create(&uploadReservation{ID: reservationID, OwnerID: ownerID, DocumentID: documentID, Bytes: size, State: "reserved", ExpiresAt: expiresAt, CreatedAt: time.Now().UTC()}).Error
	})
}

func (s *postgresDocumentStore) UploadReferenceExists(ctx context.Context, documentID string) (bool, error) {
	var exists bool
	err := s.database.WithContext(ctx).Raw(`SELECT EXISTS (
SELECT 1 FROM documents WHERE id = ?
UNION ALL
SELECT 1 FROM upload_reservations WHERE document_id = ? AND state = 'reserved'
)`, documentID, documentID).Scan(&exists).Error
	if err != nil {
		return false, err
	}
	return exists, nil
}

func (s *postgresDocumentStore) CommitUpload(ctx context.Context, reservationID string) error {
	return s.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var reservation uploadReservation
		if err := tx.Where("id = ?", reservationID).First(&reservation).Error; err != nil {
			return err
		}
		if reservation.State != "reserved" {
			return nil
		}
		var usage dbOwnerUsage
		if err := tx.Raw("SELECT * FROM db_owner_usages WHERE owner_id = ? FOR UPDATE", reservation.OwnerID).Scan(&usage).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		if err := tx.Model(&dbOwnerUsage{}).Where("owner_id = ?", reservation.OwnerID).Updates(map[string]any{"reserved_bytes": gorm.Expr("reserved_bytes - ?", reservation.Bytes), "reserved_documents": gorm.Expr("reserved_documents - 1"), "committed_bytes": gorm.Expr("committed_bytes + ?", reservation.Bytes), "committed_documents": gorm.Expr("committed_documents + 1"), "updated_at": now}).Error; err != nil {
			return err
		}
		return tx.Model(&reservation).Updates(map[string]any{"state": "committed", "committed_at": now}).Error
	})
}

func (s *postgresDocumentStore) ReleaseUpload(ctx context.Context, reservationID string) error {
	return s.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var reservation uploadReservation
		if err := tx.Where("id = ?", reservationID).First(&reservation).Error; err != nil {
			return err
		}
		if reservation.State != "reserved" {
			return nil
		}
		now := time.Now().UTC()
		if err := tx.Model(&dbOwnerUsage{}).Where("owner_id = ?", reservation.OwnerID).Updates(map[string]any{"reserved_bytes": gorm.Expr("GREATEST(reserved_bytes - ?, 0)", reservation.Bytes), "reserved_documents": gorm.Expr("GREATEST(reserved_documents - 1, 0)"), "updated_at": now}).Error; err != nil {
			return err
		}
		return tx.Model(&reservation).Updates(map[string]any{"state": "released", "released_at": now}).Error
	})
}

func (s *postgresDocumentStore) BeginAnswer(ctx context.Context, ownerID, requestID string, expiresAt time.Time) error {
	return s.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockOwnerUsage(tx, ownerID); err != nil {
			return err
		}
		if err := recoverExpiredReservations(tx, &ownerID, time.Now().UTC()); err != nil {
			return err
		}
		usage := dbOwnerUsage{OwnerID: ownerID}
		if err := tx.Raw("SELECT * FROM db_owner_usages WHERE owner_id = ?", ownerID).Scan(&usage).Error; err != nil {
			return err
		}
		if usage.ActiveAnswers >= maxActiveAnswers {
			return errQuotaExceeded
		}
		if err := tx.Model(&dbOwnerUsage{}).Where("owner_id = ?", ownerID).UpdateColumns(map[string]any{"active_answers": gorm.Expr("active_answers + 1"), "updated_at": time.Now().UTC()}).Error; err != nil {
			return err
		}
		return tx.Create(&answerReservation{ID: requestID, OwnerID: ownerID, RequestID: requestID, State: "active", ExpiresAt: expiresAt, CreatedAt: time.Now().UTC()}).Error
	})
}

func lockOwnerUsage(tx *gorm.DB, ownerID string) error {
	if err := tx.Exec(`INSERT INTO db_owner_usages (owner_id, updated_at)
VALUES (?, ?)
ON CONFLICT (owner_id) DO NOTHING`, ownerID, time.Now().UTC()).Error; err != nil {
		return err
	}
	var usage dbOwnerUsage
	return tx.Raw("SELECT * FROM db_owner_usages WHERE owner_id = ? FOR UPDATE", ownerID).Scan(&usage).Error
}

func (s *postgresDocumentStore) RecoverExpiredReservations(ctx context.Context) error {
	return s.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return recoverExpiredReservations(tx, nil, time.Now().UTC())
	})
}

func recoverExpiredReservations(tx *gorm.DB, ownerID *string, now time.Time) error {
	ownerFilter := ""
	if ownerID != nil {
		ownerFilter = " AND owner_id = ?"
	}
	uploadArgs := []any{now, now}
	if ownerID != nil {
		uploadArgs = append(uploadArgs, *ownerID)
	}
	if err := tx.Exec("UPDATE upload_reservations SET state = 'released', released_at = ? WHERE state = 'reserved' AND expires_at <= ?"+ownerFilter, uploadArgs...).Error; err != nil {
		return err
	}
	answerArgs := []any{now, now}
	if ownerID != nil {
		answerArgs = append(answerArgs, *ownerID)
	}
	if err := tx.Exec("UPDATE answer_reservations SET state = 'expired', finished_at = ? WHERE state = 'active' AND expires_at <= ?"+ownerFilter, answerArgs...).Error; err != nil {
		return err
	}

	where := ""
	whereArgs := []any{}
	if ownerID != nil {
		where = " WHERE owner_id = ?"
		whereArgs = append(whereArgs, *ownerID)
	}
	query := `WITH owners AS (
    SELECT owner_id FROM db_owner_usages` + where + `
    UNION SELECT owner_id FROM documents` + where + `
    UNION SELECT owner_id FROM upload_reservations` + where + `
    UNION SELECT owner_id FROM answer_reservations` + where + `
), committed AS (
    SELECT owner_id, COALESCE(SUM(size), 0) AS bytes, COUNT(*) AS documents
    FROM documents` + where + ` GROUP BY owner_id
), reserved AS (
    SELECT owner_id, COALESCE(SUM(bytes), 0) AS bytes, COUNT(*) AS documents
    FROM upload_reservations WHERE state = 'reserved'` + func() string {
		if ownerID == nil {
			return ""
		}
		return " AND owner_id = ?"
	}() + ` GROUP BY owner_id
), active AS (
    SELECT owner_id, COUNT(*) AS answers
    FROM answer_reservations WHERE state = 'active'` + func() string {
		if ownerID == nil {
			return ""
		}
		return " AND owner_id = ?"
	}() + ` GROUP BY owner_id
)
INSERT INTO db_owner_usages (owner_id, committed_bytes, committed_documents, reserved_bytes, reserved_documents, active_answers, updated_at)
SELECT owners.owner_id,
       COALESCE(committed.bytes, 0), COALESCE(committed.documents, 0),
       COALESCE(reserved.bytes, 0), COALESCE(reserved.documents, 0),
       COALESCE(active.answers, 0), ?
FROM owners
LEFT JOIN committed ON committed.owner_id = owners.owner_id
LEFT JOIN reserved ON reserved.owner_id = owners.owner_id
LEFT JOIN active ON active.owner_id = owners.owner_id
ON CONFLICT (owner_id) DO UPDATE SET
    committed_bytes = EXCLUDED.committed_bytes,
    committed_documents = EXCLUDED.committed_documents,
    reserved_bytes = EXCLUDED.reserved_bytes,
    reserved_documents = EXCLUDED.reserved_documents,
    active_answers = EXCLUDED.active_answers,
    updated_at = EXCLUDED.updated_at`
	args := make([]any, 0, len(whereArgs)*7+1)
	for range 5 {
		args = append(args, whereArgs...)
	}
	if ownerID != nil {
		args = append(args, *ownerID, *ownerID)
	}
	args = append(args, now)
	return tx.Exec(query, args...).Error
}

func (s *postgresDocumentStore) EndAnswer(ctx context.Context, reservationID, state string) error {
	return s.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var reservation answerReservation
		if err := tx.Where("id = ?", reservationID).First(&reservation).Error; err != nil {
			return err
		}
		if reservation.State != "active" {
			return nil
		}
		now := time.Now().UTC()
		if err := tx.Model(&dbOwnerUsage{}).Where("owner_id = ?", reservation.OwnerID).UpdateColumns(map[string]any{"active_answers": gorm.Expr("GREATEST(active_answers - 1, 0)"), "updated_at": now}).Error; err != nil {
			return err
		}
		return tx.Model(&reservation).Updates(map[string]any{"state": state, "finished_at": now}).Error
	})
}

func (s *postgresDocumentStore) AllowRate(ctx context.Context, kind, key string, limit int, window time.Duration, now time.Time) (bool, error) {
	windowStart := now.UTC().Truncate(window)
	result := s.database.WithContext(ctx).Exec(`INSERT INTO rate_limit_buckets (kind, key, window_start, count) VALUES (?, ?, ?, 1)
ON CONFLICT (kind, key, window_start) DO UPDATE SET count = rate_limit_buckets.count + 1
WHERE rate_limit_buckets.count < ?`, kind, key, windowStart, limit)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

func (s *postgresDocumentStore) DeleteOwnedWithUsage(ctx context.Context, ownerID, id string) (*document, error) {
	var deleted document
	err := s.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ? AND owner_id = ?", id, ownerID).First(&deleted).Error; err != nil {
			return err
		}
		var usage dbOwnerUsage
		if err := tx.Raw("SELECT * FROM db_owner_usages WHERE owner_id = ? FOR UPDATE", ownerID).Scan(&usage).Error; err != nil {
			return err
		}
		if err := tx.Where("document_id = ?", id).Delete(&documentChunk{}).Error; err != nil {
			return err
		}
		if err := tx.Where("document_id = ? AND owner_id = ?", id, ownerID).Delete(&documentQuestion{}).Error; err != nil {
			return err
		}
		if err := tx.Delete(&deleted).Error; err != nil {
			return err
		}
		return tx.Model(&dbOwnerUsage{}).Where("owner_id = ?", ownerID).Updates(map[string]any{"committed_bytes": gorm.Expr("GREATEST(committed_bytes - ?, 0)", deleted.Size), "committed_documents": gorm.Expr("GREATEST(committed_documents - 1, 0)"), "updated_at": time.Now().UTC()}).Error
	})
	return &deleted, err
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

func (s *postgresDocumentStore) DeleteOwned(ctx context.Context, ownerID, id string) error {
	return s.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var document document
		if err := tx.Where("id = ? AND owner_id = ?", id, ownerID).First(&document).Error; err != nil {
			return err
		}
		if err := tx.Where("document_id = ?", id).Delete(&documentChunk{}).Error; err != nil {
			return err
		}
		if err := tx.Where("document_id = ? AND owner_id = ?", id, ownerID).Delete(&documentQuestion{}).Error; err != nil {
			return err
		}
		return tx.Delete(&document).Error
	})
}

func (s *postgresDocumentStore) RetryOwned(ctx context.Context, ownerID, id string) error {
	result := s.database.WithContext(ctx).Model(&document{}).
		Where("id = ? AND owner_id = ? AND status = 'failed'", id, ownerID).
		Updates(map[string]any{"status": "queued", "error_message": nil, "next_attempt_at": nil, "lease_token": nil, "lease_expires_at": nil, "attempt_count": 0, "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
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

func (s *postgresDocumentStore) QueueStats(ctx context.Context) (uint64, time.Time, error) {
	var result struct {
		Depth  uint64
		Oldest *time.Time
	}
	err := s.database.WithContext(ctx).Raw(`SELECT COUNT(*) AS depth, MIN(created_at) AS oldest FROM documents WHERE status = 'queued' AND (next_attempt_at IS NULL OR next_attempt_at <= NOW())`).Scan(&result).Error
	if err != nil {
		return 0, time.Time{}, err
	}
	if result.Oldest == nil {
		return result.Depth, time.Time{}, nil
	}
	return result.Depth, *result.Oldest, nil
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

func (s *postgresDocumentStore) FindOwned(ctx context.Context, ownerID, id string) (*document, error) {
	var result document
	if err := s.database.WithContext(ctx).First(&result, "id = ? AND owner_id = ?", id, ownerID).Error; err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *postgresDocumentStore) ListChunks(ctx context.Context, documentID string) ([]documentChunk, error) {
	var chunks []documentChunk
	err := s.database.WithContext(ctx).Where("document_id = ?", documentID).Order("chunk_index ASC").Find(&chunks).Error
	return chunks, err
}

func (s *postgresDocumentStore) ListChunksOwned(ctx context.Context, ownerID, documentID string) ([]documentChunk, error) {
	var chunks []documentChunk
	err := s.database.WithContext(ctx).Joins("JOIN documents ON documents.id = document_chunks.document_id").Where("document_chunks.document_id = ? AND documents.owner_id = ?", documentID, ownerID).Order("chunk_index ASC").Find(&chunks).Error
	return chunks, err
}

func (s *postgresDocumentStore) SearchChunks(ctx context.Context, documentID, embedding string, limit int) ([]documentChunk, error) {
	var chunks []documentChunk
	err := s.database.WithContext(ctx).Raw(`SELECT id, document_id, chunk_index, text, start_offset, end_offset, page_start, page_end, created_at
FROM document_chunks WHERE document_id = ? ORDER BY embedding <=> ?::vector LIMIT ?`, documentID, embedding, limit).Scan(&chunks).Error
	return chunks, err
}

func (s *postgresDocumentStore) SearchChunksOwned(ctx context.Context, ownerID, documentID, embedding string, limit int) ([]documentChunk, error) {
	var chunks []documentChunk
	err := s.database.WithContext(ctx).Raw(`SELECT c.id, c.document_id, c.chunk_index, c.text, c.start_offset, c.end_offset, c.page_start, c.page_end, c.created_at
FROM document_chunks c JOIN documents d ON d.id = c.document_id
WHERE c.document_id = ? AND d.owner_id = ? ORDER BY c.embedding <=> ?::vector LIMIT ?`, documentID, ownerID, embedding, limit).Scan(&chunks).Error
	return chunks, err
}

func (s *postgresDocumentStore) CreateQuestion(ctx context.Context, question documentQuestion) error {
	return s.database.WithContext(ctx).Create(&question).Error
}

func (s *postgresDocumentStore) ListQuestionsOwned(ctx context.Context, ownerID, documentID string) ([]documentQuestion, error) {
	var result []documentQuestion
	err := s.database.WithContext(ctx).Where("owner_id = ? AND document_id = ?", ownerID, documentID).Order("created_at DESC").Find(&result).Error
	return result, err
}
