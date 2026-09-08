package main

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/labstack/echo/v5"
)

type metrics struct {
	uploads            atomic.Uint64
	uploadRejections   atomic.Uint64
	processingFailures atomic.Uint64
	answers            atomic.Uint64
	answerFailures     atomic.Uint64
	answerLatencyMs    atomic.Uint64
	answerLatencyN     atomic.Uint64
	queueDepth         atomic.Uint64
	queueOldestAgeMs   atomic.Uint64
}

func newMetrics() *metrics { return &metrics{} }

func (m *metrics) recordAnswer(start time.Time, failed bool) {
	m.answers.Add(1)
	m.answerLatencyMs.Add(uint64(time.Since(start).Milliseconds()))
	m.answerLatencyN.Add(1)
	if failed {
		m.answerFailures.Add(1)
	}
}

func (m *metrics) handler(c *echo.Context) error {
	average := float64(0)
	if count := m.answerLatencyN.Load(); count > 0 {
		average = float64(m.answerLatencyMs.Load()) / float64(count) / 1000
	}
	c.Response().Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, err := fmt.Fprintf(c.Response(), "# TYPE documind_uploads_total counter\ndocumind_uploads_total %d\n# TYPE documind_upload_rejections_total counter\ndocumind_upload_rejections_total %d\n# TYPE documind_processing_failures_total counter\ndocumind_processing_failures_total %d\n# TYPE documind_answers_total counter\ndocumind_answers_total %d\n# TYPE documind_answer_failures_total counter\ndocumind_answer_failures_total %d\n# TYPE documind_answer_latency_seconds gauge\ndocumind_answer_latency_seconds %g\n# TYPE documind_queue_depth gauge\ndocumind_queue_depth %d\n# TYPE documind_queue_oldest_age_seconds gauge\ndocumind_queue_oldest_age_seconds %g\n", m.uploads.Load(), m.uploadRejections.Load(), m.processingFailures.Load(), m.answers.Load(), m.answerFailures.Load(), average, m.queueDepth.Load(), float64(m.queueOldestAgeMs.Load())/1000)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not write metrics"})
	}
	return nil
}
