package main

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
)

func TestMetricsRecordAnswerAndExposeHistogram(t *testing.T) {
	collector := newMetrics()
	collector.recordAnswer(time.Now().Add(-2*time.Second), false)
	collector.recordAnswer(time.Now().Add(-11*time.Second), true)

	response := httptest.NewRecorder()
	context := echo.New().NewContext(httptest.NewRequest("GET", "/metrics", nil), response)
	if err := collector.handler(context); err != nil {
		t.Fatalf("render metrics: %v", err)
	}
	body := response.Body.String()
	for _, metric := range []string{
		"documind_answers_total 2",
		"documind_answer_failures_total 1",
		"# TYPE documind_answer_latency_seconds histogram",
		"documind_answer_latency_seconds_bucket{le=\"5\"} 1",
		"documind_answer_latency_seconds_bucket{le=\"+Inf\"} 2",
	} {
		if !strings.Contains(body, metric) {
			t.Fatalf("metrics missing %q:\n%s", metric, body)
		}
	}
}
