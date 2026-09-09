package main

import (
	"errors"
	"net/http"
	"sync"
	"time"
)

var errQuotaExceeded = errors.New("quota exceeded")

const (
	maxOwnerStorage   = int64(10 << 30)
	maxOwnerDocuments = 1000
	maxUploadsPerHour = 10
	maxActiveAnswers  = 2
	maxAnswersPerDay  = 100
)

type ownerUsage struct {
	bytes     int64
	documents int
	uploads   []time.Time
	answers   []time.Time
	active    int
}

type admissionController struct {
	mutex  sync.Mutex
	owners map[string]*ownerUsage
}

func newAdmissionController() *admissionController {
	return &admissionController{owners: make(map[string]*ownerUsage)}
}

func (a *admissionController) reserveUpload(owner string, size int64) bool {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	now := time.Now()
	usage := a.owner(owner)
	usage.uploads = recent(usage.uploads, now.Add(-time.Hour))
	if usage.bytes+size > maxOwnerStorage || usage.documents >= maxOwnerDocuments || len(usage.uploads) >= maxUploadsPerHour {
		return false
	}
	usage.bytes += size
	usage.documents++
	usage.uploads = append(usage.uploads, now)
	return true
}

func (a *admissionController) releaseUpload(owner string, size int64) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	usage := a.owner(owner)
	usage.bytes -= size
	usage.documents--
}

func (a *admissionController) beginAnswer(owner string) bool {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	usage := a.owner(owner)
	now := time.Now()
	usage.answers = recent(usage.answers, now.Add(-24*time.Hour))
	if usage.active >= maxActiveAnswers || len(usage.answers) >= maxAnswersPerDay {
		return false
	}
	usage.active++
	usage.answers = append(usage.answers, now)
	return true
}

func (a *admissionController) endAnswer(owner string) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	usage := a.owner(owner)
	if usage.active > 0 {
		usage.active--
	}
}

func (a *admissionController) owner(owner string) *ownerUsage {
	usage := a.owners[owner]
	if usage == nil {
		usage = &ownerUsage{}
		a.owners[owner] = usage
	}
	return usage
}

func recent(values []time.Time, since time.Time) []time.Time {
	index := 0
	for index < len(values) && values[index].Before(since) {
		index++
	}
	return values[index:]
}

func quotaResponse(c responseWriter, message string) error {
	return c.JSON(http.StatusTooManyRequests, map[string]string{"error": message})
}

type responseWriter interface {
	JSON(int, any) error
}
