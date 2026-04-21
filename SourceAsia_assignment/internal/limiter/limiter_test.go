package limiter_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rajsonawane/rate-limited-api/internal/limiter"
)

func TestAllow_BasicLimit(t *testing.T) {
	l := limiter.New(5, time.Minute)

	for i := 0; i < 5; i++ {
		if !l.Allow("u1") {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}
	if l.Allow("u1") {
		t.Fatal("6th request should be rejected")
	}
}

func TestAllow_WindowExpiry(t *testing.T) {
	l := limiter.New(2, 100*time.Millisecond)

	if !l.Allow("u1") || !l.Allow("u1") {
		t.Fatal("first two requests should be allowed")
	}
	if l.Allow("u1") {
		t.Fatal("3rd request within window should be rejected")
	}

	time.Sleep(110 * time.Millisecond)

	if !l.Allow("u1") {
		t.Fatal("request after window expiry should be allowed")
	}
}

func TestAllow_PerUserIsolation(t *testing.T) {
	l := limiter.New(5, time.Minute)

	for i := 0; i < 5; i++ {
		l.Allow("userA")
	}
	// userA exhausted; userB should still be free
	if !l.Allow("userB") {
		t.Fatal("userB should not be affected by userA's limit")
	}
}

// TestAllow_ConcurrentSafety fires 20 goroutines simultaneously for the same
// user (limit=5) and asserts exactly 5 are allowed. This validates that the
// atomic check-and-record in Allow prevents over-admission under load.
func TestAllow_ConcurrentSafety(t *testing.T) {
	const goroutines = 20
	const limit = 5

	l := limiter.New(limit, time.Minute)

	var (
		wg      sync.WaitGroup
		allowed atomic.Int32
		start   = make(chan struct{})
	)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // all goroutines unblock at once
			if l.Allow("concurrent-user") {
				allowed.Add(1)
			}
		}()
	}

	close(start)
	wg.Wait()

	if got := int(allowed.Load()); got != limit {
		t.Fatalf("expected exactly %d allowed, got %d", limit, got)
	}
}

func TestRemaining(t *testing.T) {
	l := limiter.New(5, time.Minute)
	if r := l.Remaining("u1"); r != 5 {
		t.Fatalf("expected 5 remaining for new user, got %d", r)
	}
	l.Allow("u1")
	l.Allow("u1")
	if r := l.Remaining("u1"); r != 3 {
		t.Fatalf("expected 3 remaining after 2 requests, got %d", r)
	}
}
