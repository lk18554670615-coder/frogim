package httpapi

import (
	"sync"
	"time"
)

type limitEntry struct {
	count int
	reset time.Time
}

type limiter struct {
	mu sync.Mutex
	m  map[string]limitEntry
}

func newLimiter() *limiter { return &limiter{m: make(map[string]limitEntry)} }

func (l *limiter) allow(key string, max int, window time.Duration) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.m[key]
	if e.reset.Before(now) {
		e = limitEntry{reset: now.Add(window)}
	}
	if e.count >= max {
		return false
	}
	e.count++
	l.m[key] = e
	if len(l.m) > 10000 {
		for k, v := range l.m {
			if v.reset.Before(now) {
				delete(l.m, k)
			}
		}
	}
	return true
}
