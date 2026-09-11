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

func TestPostgresRetryOwnedRejectsPermanentFailureAndListsFailureKind(t *testing.T) {
	store := integrationStore(t)
	owner := "retry-owner-" + time.Now().UTC().Format("150405.000000000")
	permanent := integrationDocument(t, store, "failed")
	retryable := integrationDocument(t, store, "failed")
	if err := store.database.Model(&document{}).Where("id = ?", permanent.ID).Updates(map[string]any{
		"owner_id": owner, "failure_kind": failureKindPermanent,
	}).Error; err != nil {
		t.Fatalf("mark permanent failure: %v", err)
	}
	if err := store.database.Model(&document{}).Where("id = ?", retryable.ID).Updates(map[string]any{
		"owner_id": owner, "failure_kind": failureKindRetryable,
	}).Error; err != nil {
		t.Fatalf("mark retryable failure: %v", err)
	}

	if err := store.RetryOwned(context.Background(), owner, permanent.ID); !errors.Is(err, errPermanentFailure) {
		t.Fatalf("permanent retry error = %v, want permanent failure", err)
	}
	permanentResult, err := store.Find(context.Background(), permanent.ID)
	if err != nil {
		t.Fatalf("find permanent document: %v", err)
	}
	if permanentResult.Status != "failed" || permanentResult.FailureKind != failureKindPermanent {
		t.Fatalf("permanent document after retry = %+v", permanentResult)
	}

	if err := store.RetryOwned(context.Background(), owner, retryable.ID); err != nil {
		t.Fatalf("retry retryable failure: %v", err)
	}
	retryableResult, err := store.Find(context.Background(), retryable.ID)
	if err != nil {
		t.Fatalf("find retryable document: %v", err)
	}
	if retryableResult.Status != "queued" || retryableResult.FailureKind != failureKindRetryable || retryableResult.AttemptCount != 0 {
		t.Fatalf("retryable document after retry = %+v", retryableResult)
	}

	rows, err := store.ListOwnedPage(context.Background(), owner, "", 10, nil)
	if err != nil {
		t.Fatalf("list owned page: %v", err)
	}
	seen := map[string]string{}
	for _, row := range rows {
		seen[row.ID] = row.FailureKind
	}
	if seen[permanent.ID] != failureKindPermanent {
		t.Fatalf("listed permanent failure kind = %q, want %q", seen[permanent.ID], failureKindPermanent)
	}
	if seen[retryable.ID] != failureKindRetryable {
		t.Fatalf("listed queued failure kind = %q, want %q", seen[retryable.ID], failureKindRetryable)
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

func TestPostgresConcurrentAnswerReservationsForNewOwnerRespectActiveQuota(t *testing.T) {
	store := integrationStore(t)
	owner := "answer-quota-owner-" + time.Now().UTC().Format("150405.000000000")
	defer store.database.Exec("DELETE FROM answer_reservations WHERE owner_id = ?", owner)
	defer store.database.Exec("DELETE FROM db_owner_usages WHERE owner_id = ?", owner)

	const attempts = maxActiveAnswers + 2
	results := make(chan error, attempts)
	start := make(chan struct{})
	for index := 0; index < attempts; index++ {
		go func(index int) {
			<-start
			requestID := mustIntegrationID(t)
			results <- store.BeginAnswer(context.Background(), owner, requestID, time.Now().UTC().Add(time.Hour))
		}(index)
	}
	close(start)
	accepted := 0
	for index := 0; index < attempts; index++ {
		if err := <-results; err == nil {
			accepted++
		} else if !errors.Is(err, errQuotaExceeded) {
			t.Fatalf("answer reservation error = %v", err)
		}
	}
	if accepted != maxActiveAnswers {
		t.Fatalf("accepted answer reservations = %d, want %d", accepted, maxActiveAnswers)
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

func TestPostgresConcurrentRecoveryAndAnswerCompletionDoNotDeadlock(t *testing.T) {
	store := integrationStore(t)
	owner := "recovery-completion-owner-" + time.Now().UTC().Format("150405.000000000")
	defer store.database.Exec("DELETE FROM answer_reservations WHERE owner_id = ?", owner)
	defer store.database.Exec("DELETE FROM db_owner_usages WHERE owner_id = ?", owner)

	if err := store.database.Create(&dbOwnerUsage{OwnerID: owner, ActiveAnswers: 1}).Error; err != nil {
		t.Fatalf("create usage: %v", err)
	}
	for attempt := 0; attempt < 32; attempt++ {
		reservationID := mustIntegrationID(t)
		if err := store.database.Create(&answerReservation{
			ID: reservationID, OwnerID: owner, RequestID: reservationID,
			State: "active", ExpiresAt: time.Now().UTC().Add(-time.Minute), CreatedAt: time.Now().UTC(),
		}).Error; err != nil {
			t.Fatalf("create answer reservation: %v", err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		start := make(chan struct{})
		results := make(chan error, 2)
		go func() {
			<-start
			results <- store.RecoverExpiredReservations(ctx)
		}()
		go func() {
			<-start
			results <- store.EndAnswer(ctx, reservationID, "released")
		}()
		close(start)
		for range 2 {
			if err := <-results; err != nil {
				cancel()
				t.Fatalf("concurrent quota operation: %v", err)
			}
		}
		cancel()
	}

	var active int64
	if err := store.database.Model(&answerReservation{}).Where("owner_id = ? AND state = 'active'", owner).Count(&active).Error; err != nil {
		t.Fatalf("count active reservations: %v", err)
	}
	if active != 0 {
		t.Fatalf("active reservations = %d, want 0", active)
	}
	var usage dbOwnerUsage
	if err := store.database.First(&usage, "owner_id = ?", owner).Error; err != nil {
		t.Fatalf("load usage: %v", err)
	}
	if usage.ActiveAnswers != 0 {
		t.Fatalf("active answer usage = %d, want 0", usage.ActiveAnswers)
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

func TestPostgresUploadMetadataAndQuotaCommitRollbackTogether(t *testing.T) {
	store := integrationStore(t)
	owner := "atomic-upload-owner-" + time.Now().UTC().Format("150405.000000000")
	defer store.database.Exec("DELETE FROM upload_reservations WHERE owner_id = ?", owner)
	defer store.database.Exec("DELETE FROM documents WHERE owner_id = ?", owner)
	defer store.database.Exec("DELETE FROM db_owner_usages WHERE owner_id = ?", owner)

	documentID := mustIntegrationID(t)
	reservationID := mustIntegrationID(t)
	now := time.Now().UTC()
	if err := store.ReserveUpload(context.Background(), owner, reservationID, documentID, 17, now.Add(time.Hour)); err != nil {
		t.Fatalf("reserve upload: %v", err)
	}

	metadata := document{ID: documentID, OriginalFilename: "atomic.pdf", StoredPath: "documents/" + documentID + "/original.pdf", MIMEType: "application/pdf", Size: 17, Status: "queued", CreatedAt: now, UpdatedAt: now}
	if err := store.CreateOwnedAndCommitUpload(context.Background(), owner, metadata, reservationID); err != nil {
		t.Fatalf("commit upload: %v", err)
	}

	var reservation uploadReservation
	if err := store.database.First(&reservation, "id = ?", reservationID).Error; err != nil {
		t.Fatalf("load committed reservation: %v", err)
	}
	if reservation.State != "committed" {
		t.Fatalf("reservation state = %q, want committed", reservation.State)
	}
	var usage dbOwnerUsage
	if err := store.database.First(&usage, "owner_id = ?", owner).Error; err != nil {
		t.Fatalf("load committed usage: %v", err)
	}
	if usage.CommittedBytes != 17 || usage.CommittedDocuments != 1 || usage.ReservedBytes != 0 || usage.ReservedDocuments != 0 {
		t.Fatalf("committed usage = %+v", usage)
	}

	failedDocumentID := mustIntegrationID(t)
	failedReservationID := mustIntegrationID(t)
	if err := store.ReserveUpload(context.Background(), owner, failedReservationID, failedDocumentID, 23, now.Add(time.Hour)); err != nil {
		t.Fatalf("reserve rollback upload: %v", err)
	}
	duplicate := document{ID: documentID, OriginalFilename: "duplicate.pdf", StoredPath: "duplicate", MIMEType: "application/pdf", Size: 23, Status: "queued", CreatedAt: now, UpdatedAt: now}
	if err := store.CreateOwnedAndCommitUpload(context.Background(), owner, duplicate, failedReservationID); err == nil {
		t.Fatal("duplicate document upload commit succeeded")
	}
	var rolledBackReservation uploadReservation
	if err := store.database.First(&rolledBackReservation, "id = ?", failedReservationID).Error; err != nil {
		t.Fatalf("load rolled-back reservation: %v", err)
	}
	if rolledBackReservation.State != "reserved" {
		t.Fatalf("rolled-back reservation state = %q, want reserved", rolledBackReservation.State)
	}
	if err := store.database.First(&usage, "owner_id = ?", owner).Error; err != nil {
		t.Fatalf("reload rolled-back usage: %v", err)
	}
	if usage.CommittedBytes != 17 || usage.CommittedDocuments != 1 || usage.ReservedBytes != 23 || usage.ReservedDocuments != 1 {
		t.Fatalf("rolled-back usage = %+v", usage)
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
