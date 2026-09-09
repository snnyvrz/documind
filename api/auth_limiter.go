package main

import (
	"context"
	"sync"
	"time"

	"gorm.io/gorm"
)

const (
	authRegisterIPLimit      = 3
	authRegisterAccountLimit = 3
	authLoginIPLimit         = 10
	authLoginAccountLimit    = 5
	authRegisterWindow       = time.Hour
	authLoginWindow          = 15 * time.Minute
	maxAuthRateEntries       = 10000
)

type authRateLimiter struct {
	mutex    sync.Mutex
	attempts map[string][]time.Time
	database *gorm.DB
}

func newAuthRateLimiter(database ...*gorm.DB) *authRateLimiter {
	limiter := &authRateLimiter{attempts: make(map[string][]time.Time)}
	if len(database) > 0 {
		limiter.database = database[0]
	}
	return limiter
}

func (l *authRateLimiter) allow(kind, key string, limit int, window time.Duration) bool {
	if l.database != nil {
		windowStart := time.Now().UTC().Truncate(window)
		result := l.database.WithContext(context.Background()).Exec(`INSERT INTO rate_limit_buckets (kind, key, window_start, count) VALUES (?, ?, ?, 1)
ON CONFLICT (kind, key, window_start) DO UPDATE SET count = rate_limit_buckets.count + 1
WHERE rate_limit_buckets.count < ?`, kind, key, windowStart, limit)
		return result.Error == nil && result.RowsAffected == 1
	}
	l.mutex.Lock()
	defer l.mutex.Unlock()
	now := time.Now()
	since := now.Add(-window)
	for name, values := range l.attempts {
		values = recent(values, since)
		if len(values) == 0 {
			delete(l.attempts, name)
		} else {
			l.attempts[name] = values
		}
	}
	name := kind + ":" + key
	values := recent(l.attempts[name], since)
	if len(values) >= limit {
		l.attempts[name] = values
		return false
	}
	if len(values) == 0 && len(l.attempts) >= maxAuthRateEntries {
		return false
	}
	l.attempts[name] = append(values, now)
	return true
}
