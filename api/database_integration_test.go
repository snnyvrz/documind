//go:build integration

package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
)

func integrationStore(t *testing.T) *postgresDocumentStore {
	t.Helper()
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL is not set")
	}
	store, err := openDocumentStore(databaseURL)
	if err != nil {
		t.Fatalf("open integration database: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func integrationDocument(t *testing.T, store *postgresDocumentStore, status string) document {
	t.Helper()
	id, err := documentID()
	if err != nil {
		t.Fatalf("create document ID: %v", err)
	}
	now := time.Now().UTC()
	document := document{ID: id, OriginalFilename: "integration.pdf", StoredPath: "integration.pdf", MIMEType: "application/pdf", Status: status, CreatedAt: now, UpdatedAt: now}
	if err := store.Create(context.Background(), document); err != nil {
		t.Fatalf("create integration document: %v", err)
	}
	t.Cleanup(func() {
		_ = store.database.Exec("DELETE FROM document_chunks WHERE document_id = ?", id).Error
		_ = store.database.Exec("DELETE FROM documents WHERE id = ?", id).Error
	})
	return document
}

func TestPostgresClaimNextAllowsOnlyOneWorker(t *testing.T) {
	store := integrationStore(t)
	doc := integrationDocument(t, store, "queued")

	type claimResult struct {
		document *document
		err      error
	}
	results := make(chan claimResult, 2)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for _, token := range []string{"worker-a", "worker-b"} {
		workers.Add(1)
		go func(token string) {
			defer workers.Done()
			<-start
			claimed, err := store.ClaimNext(context.Background(), token, time.Minute, 5)
			results <- claimResult{document: claimed, err: err}
		}(token)
	}
	close(start)
	workers.Wait()
	close(results)

	claimed := 0
	for result := range results {
		if result.err == nil {
			claimed++
			if result.document.ID != doc.ID {
				t.Fatalf("claimed document = %s, want %s", result.document.ID, doc.ID)
			}
		} else if !errors.Is(result.err, gorm.ErrRecordNotFound) {
			t.Fatalf("claim error = %v", result.err)
		}
	}
	if claimed != 1 {
		t.Fatalf("successful claims = %d, want 1", claimed)
	}
}

func TestPostgresExpiredLeaseIsReclaimedAndStaleWorkerIsFenced(t *testing.T) {
	store := integrationStore(t)
	doc := integrationDocument(t, store, "queued")
	first, err := store.ClaimNext(context.Background(), "worker-a", time.Minute, 5)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if err := store.database.Exec("UPDATE documents SET lease_expires_at = NOW() - INTERVAL '1 second' WHERE id = ?", doc.ID).Error; err != nil {
		t.Fatalf("expire lease: %v", err)
	}

	second, err := store.ClaimNext(context.Background(), "worker-b", time.Minute, 5)
	if err != nil {
		t.Fatalf("reclaim expired lease: %v", err)
	}
	if second.AttemptCount != first.AttemptCount+1 || second.LeaseToken == nil || *second.LeaseToken != "worker-b" {
		t.Fatalf("reclaimed document = %+v", second)
	}
	if err := store.Complete(context.Background(), doc.ID, "worker-a", "stale result", 1, nil); !errors.Is(err, errLeaseLost) {
		t.Fatalf("stale completion error = %v, want lease lost", err)
	}
	if err := store.Complete(context.Background(), doc.ID, "worker-b", "current result", 1, nil); err != nil {
		t.Fatalf("current completion: %v", err)
	}
}

func TestPostgresConcurrentUploadReservationsRespectStorageQuota(t *testing.T) {
	store := integrationStore(t)
	owner := "quota-owner-" + time.Now().UTC().Format("150405.000000000")
	defer store.database.Exec("DELETE FROM upload_reservations WHERE owner_id = ?", owner)
	defer store.database.Exec("DELETE FROM db_owner_usages WHERE owner_id = ?", owner)

	const attempts = 4
	results := make(chan error, attempts)
	start := make(chan struct{})
	for index := 0; index < attempts; index++ {
		go func(index int) {
			<-start
			reservation := "reservation-" + owner + "-" + string(rune('a'+index))
			results <- store.ReserveUpload(context.Background(), owner, reservation, reservation, maxOwnerStorage/2, time.Now().UTC().Add(time.Hour))
		}(index)
	}
	close(start)
	accepted := 0
	for index := 0; index < attempts; index++ {
		if err := <-results; err == nil {
			accepted++
		} else if !errors.Is(err, errQuotaExceeded) {
			t.Fatalf("reservation error = %v", err)
		}
	}
	if accepted != 2 {
		t.Fatalf("accepted reservations = %d, want 2", accepted)
	}
}

func TestPostgresStartupRecoveryReclaimsExpiredReservationsAndReconcilesUsage(t *testing.T) {
	store := integrationStore(t)
	owner := "recovery-owner-" + time.Now().UTC().Format("150405.000000000")
	defer store.database.Exec("DELETE FROM answer_reservations WHERE owner_id = ?", owner)
	defer store.database.Exec("DELETE FROM upload_reservations WHERE owner_id = ?", owner)
	defer store.database.Exec("DELETE FROM documents WHERE owner_id = ?", owner)
	defer store.database.Exec("DELETE FROM db_owner_usages WHERE owner_id = ?", owner)

	now := time.Now().UTC()
	if err := store.database.Create(&dbOwnerUsage{OwnerID: owner, CommittedBytes: 999, CommittedDocuments: 99, ReservedBytes: 999, ReservedDocuments: 99, ActiveAnswers: 2}).Error; err != nil {
		t.Fatalf("create usage: %v", err)
	}
	document := document{ID: mustIntegrationID(t), OwnerID: owner, Size: 17, Status: "completed", CreatedAt: now, UpdatedAt: now}
	if err := store.database.Create(&document).Error; err != nil {
		t.Fatalf("create document: %v", err)
	}
	if err := store.database.Create(&uploadReservation{ID: mustIntegrationID(t), OwnerID: owner, DocumentID: mustIntegrationID(t), Bytes: 23, State: "reserved", ExpiresAt: now.Add(-time.Minute), CreatedAt: now.Add(-time.Hour)}).Error; err != nil {
		t.Fatalf("create upload reservation: %v", err)
	}
	for index := 0; index < 2; index++ {
		if err := store.database.Create(&answerReservation{ID: mustIntegrationID(t), OwnerID: owner, RequestID: mustIntegrationID(t), State: "active", ExpiresAt: now.Add(-time.Minute), CreatedAt: now.Add(-time.Hour)}).Error; err != nil {
			t.Fatalf("create answer reservation: %v", err)
		}
	}

	if err := store.RecoverExpiredReservations(context.Background()); err != nil {
		t.Fatalf("recover reservations: %v", err)
	}
	if err := store.RecoverExpiredReservations(context.Background()); err != nil {
		t.Fatalf("repeat recovery: %v", err)
	}

	var usage dbOwnerUsage
	if err := store.database.First(&usage, "owner_id = ?", owner).Error; err != nil {
		t.Fatalf("load reconciled usage: %v", err)
	}
	if usage.CommittedBytes != 17 || usage.CommittedDocuments != 1 || usage.ReservedBytes != 0 || usage.ReservedDocuments != 0 || usage.ActiveAnswers != 0 {
		t.Fatalf("reconciled usage = %+v", usage)
	}
	var released int64
	if err := store.database.Model(&uploadReservation{}).Where("owner_id = ? AND state = 'released'", owner).Count(&released).Error; err != nil || released != 1 {
		t.Fatalf("released uploads = %d, err = %v", released, err)
	}
	var expired int64
	if err := store.database.Model(&answerReservation{}).Where("owner_id = ? AND state = 'expired'", owner).Count(&expired).Error; err != nil || expired != 2 {
		t.Fatalf("expired answers = %d, err = %v", expired, err)
	}
}

func TestPostgresAdmissionRecoveryReclaimsExpiredReservationsBeforeQuotaCheck(t *testing.T) {
	store := integrationStore(t)
	owner := "admission-recovery-owner-" + time.Now().UTC().Format("150405.000000000")
	defer store.database.Exec("DELETE FROM answer_reservations WHERE owner_id = ?", owner)
	defer store.database.Exec("DELETE FROM upload_reservations WHERE owner_id = ?", owner)
	defer store.database.Exec("DELETE FROM db_owner_usages WHERE owner_id = ?", owner)

	now := time.Now().UTC()
	uploadID := mustIntegrationID(t)
	uploadDocumentID := mustIntegrationID(t)
	if err := store.database.Create(&dbOwnerUsage{OwnerID: owner, ReservedBytes: maxOwnerStorage, ReservedDocuments: maxOwnerDocuments, ActiveAnswers: maxActiveAnswers}).Error; err != nil {
		t.Fatalf("create usage: %v", err)
	}
	if err := store.database.Create(&uploadReservation{ID: uploadID, OwnerID: owner, DocumentID: uploadDocumentID, Bytes: maxOwnerStorage, State: "reserved", ExpiresAt: now.Add(-time.Minute), CreatedAt: now.Add(-time.Hour)}).Error; err != nil {
		t.Fatalf("create upload reservation: %v", err)
	}
	for index := 0; index < maxActiveAnswers; index++ {
		answerID := mustIntegrationID(t)
		if err := store.database.Create(&answerReservation{ID: answerID, OwnerID: owner, RequestID: answerID, State: "active", ExpiresAt: now.Add(-time.Minute), CreatedAt: now.Add(-time.Hour)}).Error; err != nil {
			t.Fatalf("create answer reservation: %v", err)
		}
	}

	newUploadID := mustIntegrationID(t)
	newDocumentID := mustIntegrationID(t)
	if err := store.ReserveUpload(context.Background(), owner, newUploadID, newDocumentID, 1, now.Add(time.Hour)); err != nil {
		t.Fatalf("reserve upload after recovery: %v", err)
	}
	if err := store.BeginAnswer(context.Background(), owner, mustIntegrationID(t), now.Add(time.Hour)); err != nil {
		t.Fatalf("begin answer after recovery: %v", err)
	}
}

func TestPostgresRateLimitIsSharedAcrossStoreInstances(t *testing.T) {
	first := integrationStore(t)
	second := integrationStore(t)
	key := "rate-key-" + time.Now().UTC().Format("150405.000000000")
	defer first.database.Exec("DELETE FROM rate_limit_buckets WHERE key = ?", key)
	allowed, err := first.AllowRate(context.Background(), "integration", key, 1, time.Hour, time.Now().UTC())
	if err != nil || !allowed {
		t.Fatalf("first rate admission = %v, %v", allowed, err)
	}
	allowed, err = second.AllowRate(context.Background(), "integration", key, 1, time.Hour, time.Now().UTC())
	if err != nil {
		t.Fatalf("second rate admission error = %v", err)
	}
	if allowed {
		t.Fatal("second store bypassed shared rate limit")
	}
}

func TestPostgresExhaustedJobFailsWhenQueueIsEmpty(t *testing.T) {
	store := integrationStore(t)
	doc := integrationDocument(t, store, "queued")
	if err := store.database.Model(&document{}).Where("id = ?", doc.ID).Updates(map[string]any{"attempt_count": 5}).Error; err != nil {
		t.Fatalf("set attempt count: %v", err)
	}

	if _, err := store.ClaimNext(context.Background(), "worker", time.Minute, 5); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("claim error = %v, want no available job", err)
	}
	result, err := store.Find(context.Background(), doc.ID)
	if err != nil {
		t.Fatalf("find exhausted document: %v", err)
	}
	if result.Status != "failed" || result.ErrorMessage == nil || *result.ErrorMessage != "document processing failed after maximum attempts" {
		t.Fatalf("exhausted document = %+v", result)
	}
}

func TestPostgresCompleteRollsBackWhenChunkInsertionFails(t *testing.T) {
	store := integrationStore(t)
	doc := integrationDocument(t, store, "queued")
	if _, err := store.ClaimNext(context.Background(), "worker", time.Minute, 5); err != nil {
		t.Fatalf("claim document: %v", err)
	}
	vector := "[" + strings.TrimSuffix(strings.Repeat("0,", 768), ",") + "]"
	chunks := []documentChunk{
		{ID: mustIntegrationID(t), DocumentID: doc.ID, ChunkIndex: 0, Text: "first", Embedding: vector, CreatedAt: time.Now().UTC()},
		{ID: mustIntegrationID(t), DocumentID: doc.ID, ChunkIndex: 0, Text: "duplicate", Embedding: vector, CreatedAt: time.Now().UTC()},
	}

	if err := store.Complete(context.Background(), doc.ID, "worker", "extracted text", 1, chunks); err == nil {
		t.Fatal("complete succeeded with duplicate chunk indexes")
	}
	result, err := store.Find(context.Background(), doc.ID)
	if err != nil {
		t.Fatalf("find rolled-back document: %v", err)
	}
	if result.Status != "processing" || result.LeaseToken == nil || *result.LeaseToken != "worker" || result.ExtractedText != nil {
		t.Fatalf("document after rollback = %+v", result)
	}
	var chunkCount int64
	if err := store.database.Model(&documentChunk{}).Where("document_id = ?", doc.ID).Count(&chunkCount).Error; err != nil {
		t.Fatalf("count rolled-back chunks: %v", err)
	}
	if chunkCount != 0 {
		t.Fatalf("chunk count after rollback = %d, want 0", chunkCount)
	}
}

func mustIntegrationID(t *testing.T) string {
	t.Helper()
	id, err := documentID()
	if err != nil {
		t.Fatalf("create chunk ID: %v", err)
	}
	return id
}
