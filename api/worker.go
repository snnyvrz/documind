package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	extractorpb "api/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"gorm.io/gorm"
)

func startWorker(uploadDirectory string, store documentStore) {
	go func() {
		for {
			err := processNext(context.Background(), uploadDirectory, store)
			if err != nil {
				time.Sleep(time.Second)
			}
			if errors.Is(err, gorm.ErrRecordNotFound) {
				time.Sleep(time.Second)
			}
		}
	}()
}

func processNext(ctx context.Context, uploadDirectory string, store documentStore) error {
	document, err := store.ClaimNext(ctx)
	if err != nil {
		return err
	}
	pdf, err := os.ReadFile(filepath.Join(uploadDirectory, document.StoredPath))
	if err != nil {
		return store.Fail(ctx, document.ID, "could not read uploaded PDF")
	}
	connection, err := grpc.NewClient(extractorAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return store.Fail(ctx, document.ID, "could not connect to text extractor")
	}
	defer connection.Close()
	response, err := extractorpb.NewTextExtractorClient(connection).Extract(ctx, &extractorpb.ExtractRequest{DocumentId: document.ID, Pdf: pdf})
	if err != nil {
		return store.Fail(ctx, document.ID, "text extraction failed")
	}
	return store.Complete(ctx, document.ID, response.Text)
}

func extractorAddress() string {
	if address := os.Getenv("EXTRACTOR_GRPC_URL"); address != "" {
		return address
	}
	return "127.0.0.1:50051"
}
