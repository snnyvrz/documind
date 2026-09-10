package main

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/labstack/echo/v5"
)

type metrics struct {
	uploads              atomic.Uint64
	uploadRejections     atomic.Uint64
	processingFailures   atomic.Uint64
	answers              atomic.Uint64
	answerFailures       atomic.Uint64
	answerLatencyMs      atomic.Uint64
	answerLatencyN       atomic.Uint64
	answerLatencyBuckets [8]atomic.Uint64
	retrievalLatencyMs   atomic.Uint64
	retrievalLatencyN    atomic.Uint64
	retrievalFailures    atomic.Uint64
	queueDepth           atomic.Uint64
	queueOldestAgeMs     atomic.Uint64
}

func (m *metrics) recordRetrieval(start time.Time, failed bool) {
	m.retrievalLatencyMs.Add(uint64(time.Since(start).Milliseconds()))
	m.retrievalLatencyN.Add(1)
	if failed {
		m.retrievalFailures.Add(1)
	}
}

func newMetrics() *metrics { return &metrics{} }

func (m *metrics) recordAnswer(start time.Time, failed bool) {
	latency := time.Since(start)
	m.answers.Add(1)
	m.answerLatencyMs.Add(uint64(latency.Milliseconds()))
	m.answerLatencyN.Add(1)
	for index, boundary := range []time.Duration{time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute} {
		if latency <= boundary {
			m.answerLatencyBuckets[index].Add(1)
		}
	}
	if failed {
		m.answerFailures.Add(1)
	}
}

func (m *metrics) handler(c *echo.Context) error {
	c.Response().Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, err := fmt.Fprintf(c.Response(), "# TYPE documind_uploads_total counter\ndocumind_uploads_total %d\n# TYPE documind_upload_rejections_total counter\ndocumind_upload_rejections_total %d\n# TYPE documind_processing_failures_total counter\ndocumind_processing_failures_total %d\n# TYPE documind_answers_total counter\ndocumind_answers_total %d\n# TYPE documind_answer_failures_total counter\ndocumind_answer_failures_total %d\n# TYPE documind_answer_latency_seconds histogram\ndocumind_answer_latency_seconds_sum %g\ndocumind_answer_latency_seconds_count %d\n# TYPE documind_retrieval_latency_seconds summary\ndocumind_retrieval_latency_seconds_sum %g\ndocumind_retrieval_latency_seconds_count %d\ndocumind_retrieval_failures_total %d\n", m.uploads.Load(), m.uploadRejections.Load(), m.processingFailures.Load(), m.answers.Load(), m.answerFailures.Load(), float64(m.answerLatencyMs.Load())/1000, m.answerLatencyN.Load(), float64(m.retrievalLatencyMs.Load())/1000, m.retrievalLatencyN.Load(), m.retrievalFailures.Load())
	if err == nil {
		for index, boundary := range []float64{1, 5, 10, 30, 60, 120, 300, 600} {
			_, err = fmt.Fprintf(c.Response(), "documind_answer_latency_seconds_bucket{le=\"%g\"} %d\n", boundary, m.answerLatencyBuckets[index].Load())
			if err != nil {
				break
			}
		}
	}
	if err == nil {
		_, err = fmt.Fprintf(c.Response(), "documind_answer_latency_seconds_bucket{le=\"+Inf\"} %d\n# TYPE documind_queue_depth gauge\ndocumind_queue_depth %d\n# TYPE documind_queue_oldest_age_seconds gauge\ndocumind_queue_oldest_age_seconds %g\n", m.answerLatencyN.Load(), m.queueDepth.Load(), float64(m.queueOldestAgeMs.Load())/1000)
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not write metrics"})
	}
	return nil
}
