//go:build integration

package main

import (
	"context"
	"os"
	"testing"
)

// Run with a populated database and RETRIEVAL_BENCHMARK_DOCUMENT_ID to measure
// the production query without embedding or answer-generation latency.
func BenchmarkRetrievalLatency(b *testing.B) {
	documentID := os.Getenv("RETRIEVAL_BENCHMARK_DOCUMENT_ID")
	if documentID == "" {
		b.Skip("RETRIEVAL_BENCHMARK_DOCUMENT_ID is required")
	}
	store, err := openDocumentStore(os.Getenv("DATABASE_URL"))
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()
	vector := "[" + zeroVector(int(maxEmbeddingDimensions)) + "]"
	b.ReportAllocs()
	for b.Loop() {
		if _, err := store.SearchChunks(context.Background(), documentID, vector, retrievalLimit()); err != nil {
			b.Fatal(err)
		}
	}
}

func zeroVector(dimensions int) string {
	result := "0"
	for i := 1; i < dimensions; i++ {
		result += ",0"
	}
	return result
}
