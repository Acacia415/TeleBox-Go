package ratelimit

import (
	"sync"
	"time"
)

// Limiter allows one event per key per configured interval. It is intended for
// command admission, not as a substitute for Telegram's own flood-wait rules.
type Limiter struct {
	mu       sync.Mutex
	interval time.Duration
	last     map[int64]time.Time
	checks   uint64
}

func New(interval time.Duration) *Limiter {
	return &Limiter{
		interval: interval,
		last:     make(map[int64]time.Time),
	}
}

func (l *Limiter) Allow(key int64, now time.Time) bool {
	if l.interval <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	if previous, exists := l.last[key]; exists && now.Before(previous.Add(l.interval)) {
		return false
	}
	l.last[key] = now
	l.checks++
	if l.checks%1024 == 0 {
		cutoff := now.Add(-4 * l.interval)
		for existingKey, seen := range l.last {
			if seen.Before(cutoff) {
				delete(l.last, existingKey)
			}
		}
	}
	return true
}
