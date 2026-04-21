package limiter

import (
	"sync"
	"time"
)

// SlidingWindowLimiter enforces per-user request limits using a sliding window.
// Safe for concurrent use across goroutines.
type SlidingWindowLimiter struct {
	mu      sync.RWMutex
	buckets map[string]*userBucket
	limit   int
	window  time.Duration
}

type userBucket struct {
	mu         sync.Mutex
	timestamps []time.Time
}

func New(limit int, window time.Duration) *SlidingWindowLimiter {
	return &SlidingWindowLimiter{
		buckets: make(map[string]*userBucket),
		limit:   limit,
		window:  window,
	}
}

// Allow returns true if the user is within the rate limit and records the attempt.
// The check-and-record is atomic per user bucket.
func (l *SlidingWindowLimiter) Allow(userID string) bool {
	bucket := l.getOrCreateBucket(userID)

	bucket.mu.Lock()
	defer bucket.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-l.window)

	// Compact in-place: keep only timestamps within the window
	n := 0
	for _, ts := range bucket.timestamps {
		if ts.After(cutoff) {
			bucket.timestamps[n] = ts
			n++
		}
	}
	bucket.timestamps = bucket.timestamps[:n]

	if len(bucket.timestamps) >= l.limit {
		return false
	}

	bucket.timestamps = append(bucket.timestamps, now)
	return true
}

// Remaining returns how many requests the user can still make in the current window.
func (l *SlidingWindowLimiter) Remaining(userID string) int {
	bucket := l.getOrCreateBucket(userID)

	bucket.mu.Lock()
	defer bucket.mu.Unlock()

	cutoff := time.Now().Add(-l.window)
	active := 0
	for _, ts := range bucket.timestamps {
		if ts.After(cutoff) {
			active++
		}
	}
	if rem := l.limit - active; rem > 0 {
		return rem
	}
	return 0
}

// getOrCreateBucket uses double-checked locking to safely initialise user buckets.
func (l *SlidingWindowLimiter) getOrCreateBucket(userID string) *userBucket {
	l.mu.RLock()
	b, ok := l.buckets[userID]
	l.mu.RUnlock()
	if ok {
		return b
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if b, ok = l.buckets[userID]; ok {
		return b
	}
	b = &userBucket{timestamps: make([]time.Time, 0, l.limit)}
	l.buckets[userID] = b
	return b
}
