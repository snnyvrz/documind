package main

import (
	"sync"
	"time"
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
}

func newAuthRateLimiter() *authRateLimiter {
	return &authRateLimiter{attempts: make(map[string][]time.Time)}
}

func (l *authRateLimiter) allow(kind, key string, limit int, window time.Duration) bool {
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
