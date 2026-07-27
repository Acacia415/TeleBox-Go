package ratelimit

import (
	"testing"
	"time"
)

func TestLimiterIsIndependentPerKey(t *testing.T) {
	t.Parallel()

	limiter := New(time.Second)
	now := time.Unix(100, 0)
	if !limiter.Allow(1, now) {
		t.Fatal("first event was denied")
	}
	if limiter.Allow(1, now.Add(500*time.Millisecond)) {
		t.Fatal("event inside interval was allowed")
	}
	if !limiter.Allow(2, now.Add(500*time.Millisecond)) {
		t.Fatal("different key was denied")
	}
	if !limiter.Allow(1, now.Add(time.Second)) {
		t.Fatal("event at interval boundary was denied")
	}
}
