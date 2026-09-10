package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

func testWorkerConfig() workerConfig {
	return workerConfig{
		maxAttempts:       5,
		initialBackoff:    5 * time.Second,
		maxBackoff:        20 * time.Second,
		leaseDuration:     10 * time.Second,
		leaseRenewal:      time.Second,
		processingTimeout: 2 * time.Second,
		pollInterval:      time.Millisecond,
	}
}

func TestClaimRecoversExpiredLeaseAndFencesStaleWorker(t *testing.T) {
	expired := time.Now().UTC().Add(-time.Second)
	oldToken := "old-token"
	store := &memoryDocumentStore{documents: []document{{
		ID:             "document-id",
		Status:         "processing",
		AttemptCount:   1,
		LeaseToken:     &oldToken,
		LeaseExpiresAt: &expired,
	}}}

	claimed, err := store.ClaimNext(context.Background(), "new-token", time.Minute, 5)
	if err != nil {
		t.Fatalf("claim expired job: %v", err)
	}
	if claimed.AttemptCount != 2 || claimed.LeaseToken == nil || *claimed.LeaseToken != "new-token" {
		t.Fatalf("claimed document = %+v", claimed)
	}
	if err := store.Complete(context.Background(), claimed.ID, oldToken, "stale", 1, nil); !errors.Is(err, errLeaseLost) {
		t.Fatalf("stale completion error = %v, want lease lost", err)
	}
	if err := store.Complete(context.Background(), claimed.ID, "new-token", "current", 1, nil); err != nil {
		t.Fatalf("current completion: %v", err)
	}
	result, err := store.Find(context.Background(), claimed.ID)
	if err != nil {
		t.Fatalf("find completed document: %v", err)
	}
	if result.Status != "completed" || result.ExtractedText == nil || *result.ExtractedText != "current" {
		t.Fatalf("completed document = %+v", result)
	}
}

func TestClaimSkipsFutureRetryAndActiveLease(t *testing.T) {
	future := time.Now().UTC().Add(time.Minute)
	token := "active-token"
	store := &memoryDocumentStore{documents: []document{
		{ID: "retry", Status: "queued", NextAttemptAt: &future},
		{ID: "active", Status: "processing", LeaseToken: &token, LeaseExpiresAt: &future},
	}}

	if _, err := store.ClaimNext(context.Background(), "new-token", time.Minute, 5); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("claim error = %v, want no eligible job", err)
	}
}

func TestClaimFailsExpiredJobAtMaximumAttempts(t *testing.T) {
	expired := time.Now().UTC().Add(-time.Second)
	token := "expired-token"
	store := &memoryDocumentStore{documents: []document{{
		ID: "document-id", Status: "processing", AttemptCount: 5,
		LeaseToken: &token, LeaseExpiresAt: &expired,
	}}}

	if _, err := store.ClaimNext(context.Background(), "new-token", time.Minute, 5); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("claim error = %v, want no eligible job", err)
	}
	result, _ := store.Find(context.Background(), "document-id")
	if result.Status != "failed" || result.ErrorMessage == nil {
		t.Fatalf("exhausted document = %+v", result)
	}
}

func TestMaintainLeaseRenewsActiveClaim(t *testing.T) {
	store := &memoryDocumentStore{documents: []document{{ID: "document-id", Status: "queued"}}}
	claimed, err := store.ClaimNext(context.Background(), "lease-token", 100*time.Millisecond, 5)
	if err != nil {
		t.Fatalf("claim job: %v", err)
	}
	initialExpiry := *claimed.LeaseExpiresAt
	config := testWorkerConfig()
	config.leaseDuration = 500 * time.Millisecond
	config.leaseRenewal = 10 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go maintainLease(ctx, cancel, store, claimed.ID, "lease-token", config, done)
	if err := <-done; err != nil {
		t.Fatalf("maintain lease: %v", err)
	}
	result, _ := store.Find(context.Background(), claimed.ID)
	if result.LeaseExpiresAt == nil || !result.LeaseExpiresAt.After(initialExpiry) {
		t.Fatalf("lease expiry = %v, want after %v", result.LeaseExpiresAt, initialExpiry)
	}
}

func TestProcessNextPermanentlyFailsMissingPDF(t *testing.T) {
	store := &memoryDocumentStore{documents: []document{{ID: "document-id", Status: "queued", StoredPath: "missing.pdf"}}}

	if err := processNext(context.Background(), t.TempDir(), store, testWorkerConfig()); err != nil {
		t.Fatalf("process missing PDF: %v", err)
	}
	result, _ := store.Find(context.Background(), "document-id")
	if result.Status != "failed" || result.AttemptCount != 1 || result.ErrorMessage == nil || *result.ErrorMessage != "could not read uploaded PDF" {
		t.Fatalf("failed document = %+v", result)
	}
}

func TestProcessNextRetriesUnavailableProcessor(t *testing.T) {
	uploadDirectory := t.TempDir()
	if err := os.WriteFile(filepath.Join(uploadDirectory, "document.pdf"), []byte("%PDF-1.7\n"), 0o600); err != nil {
		t.Fatalf("write PDF: %v", err)
	}
	t.Setenv("DOCUMENT_PROCESSOR_GRPC_URL", "127.0.0.1:1")
	config := testWorkerConfig()
	config.processingTimeout = 500 * time.Millisecond
	store := &memoryDocumentStore{documents: []document{{ID: "document-id", Status: "queued", StoredPath: "document.pdf"}}}

	if err := processNext(context.Background(), uploadDirectory, store, config); err != nil {
		t.Fatalf("process with unavailable processor: %v", err)
	}
	result, _ := store.Find(context.Background(), "document-id")
	if result.Status != "queued" || result.AttemptCount != 1 || result.NextAttemptAt == nil || !result.NextAttemptAt.After(time.Now().UTC()) {
		t.Fatalf("retrying document = %+v", result)
	}
}

func TestRetryBackoffDoublesAndCaps(t *testing.T) {
	config := testWorkerConfig()
	wants := []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, 20 * time.Second}
	for index, want := range wants {
		if got := retryBackoff(index+1, config); got != want {
			t.Errorf("attempt %d backoff = %v, want %v", index+1, got, want)
		}
	}
}

func TestProcessorErrorClassification(t *testing.T) {
	if !processorError(status.Error(codes.InvalidArgument, "bad PDF")).permanent {
		t.Fatal("invalid argument must be permanent")
	}
	if processorError(status.Error(codes.Unavailable, "offline")).permanent {
		t.Fatal("unavailable must be retryable")
	}
}

func TestPermanentFailureCannotBeRetried(t *testing.T) {
	message := "OCR is required"
	store := &memoryDocumentStore{documents: []document{{ID: "document-id", OwnerID: "owner", Status: "failed", ErrorMessage: &message, FailureKind: failureKindPermanent}}}

	if err := store.RetryOwned(context.Background(), "owner", "document-id"); !errors.Is(err, errPermanentFailure) {
		t.Fatalf("retry permanent failure error = %v, want %v", err, errPermanentFailure)
	}
}

func TestLoadWorkerConfigRejectsInvalidLeaseRenewal(t *testing.T) {
	t.Setenv("DOCUMENT_JOB_LEASE_DURATION", "10s")
	t.Setenv("DOCUMENT_JOB_LEASE_RENEWAL", "10s")
	if _, err := loadWorkerConfig(); err == nil {
		t.Fatal("loadWorkerConfig succeeded with renewal equal to lease duration")
	}
}
