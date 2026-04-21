package service_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rajsonawane/rate-limited-api/internal/limiter"
	"github.com/rajsonawane/rate-limited-api/internal/service"
	"github.com/rajsonawane/rate-limited-api/internal/store"
)

// ── fixtures ──────────────────────────────────────────────────────────────────

func newCore() (service.Service, *store.Store) {
	st := store.New()
	return service.New(st), st
}

func withRateLimit(limit int) (service.Service, *store.Store) {
	st := store.New()
	rl := limiter.New(limit, time.Minute)
	svc := service.NewRateLimitMiddleware(rl, st)(service.New(st))
	return svc, st
}

// ═════════════════════════════════════════════════════════════════════════════
// Core service
// ═════════════════════════════════════════════════════════════════════════════

func TestPost_ReturnsUserIDAndTimestamp(t *testing.T) {
	svc, _ := newCore()
	res, err := svc.Post(context.Background(), "alice", "payload")
	if err != nil {
		t.Fatal(err)
	}
	if res.UserID != "alice" {
		t.Errorf("user_id: want alice, got %s", res.UserID)
	}
	if res.Timestamp.IsZero() {
		t.Error("timestamp must not be zero")
	}
}

func TestPost_DifferentUsersReturnCorrectIDs(t *testing.T) {
	svc, _ := newCore()
	for _, id := range []string{"alice", "bob", "charlie"} {
		res, err := svc.Post(context.Background(), id, nil)
		if err != nil {
			t.Fatalf("user %s: unexpected error %v", id, err)
		}
		if res.UserID != id {
			t.Errorf("user_id: want %s, got %s", id, res.UserID)
		}
	}
}

// ── Stats ─────────────────────────────────────────────────────────────────────

func TestStats_UnknownUserReturnsError(t *testing.T) {
	svc, _ := newCore()
	_, err := svc.Stats(context.Background(), "nobody")
	if err != service.ErrUserNotFound {
		t.Fatalf("want ErrUserNotFound, got %v", err)
	}
}

func TestStats_AllUsers_EmptyWhenNoRequests(t *testing.T) {
	svc, _ := newCore()
	stats, err := svc.Stats(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 0 {
		t.Fatalf("want 0 entries, got %d", len(stats))
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// Rate-limit middleware
// ═════════════════════════════════════════════════════════════════════════════

func TestRateLimit_AllowsExactlyFive(t *testing.T) {
	svc, _ := withRateLimit(5)
	for i := 1; i <= 5; i++ {
		if _, err := svc.Post(context.Background(), "u1", nil); err != nil {
			t.Fatalf("request %d should be allowed, got %v", i, err)
		}
	}
}

func TestRateLimit_RejectsOnSixth(t *testing.T) {
	svc, _ := withRateLimit(5)
	for i := 0; i < 5; i++ {
		svc.Post(context.Background(), "u2", nil) //nolint:errcheck
	}
	_, err := svc.Post(context.Background(), "u2", nil)
	if err != service.ErrRateLimitExceeded {
		t.Fatalf("want ErrRateLimitExceeded, got %v", err)
	}
}

func TestRateLimit_RemainingCountsDown(t *testing.T) {
	svc, _ := withRateLimit(5)
	for i := 0; i < 5; i++ {
		res, err := svc.Post(context.Background(), "u3", nil)
		if err != nil {
			t.Fatal(err)
		}
		want := 5 - (i + 1)
		if res.Remaining != want {
			t.Errorf("after request %d: remaining want %d, got %d", i+1, want, res.Remaining)
		}
	}
}

func TestRateLimit_StoreCountsAllowedAndRejected(t *testing.T) {
	svc, st := withRateLimit(5)
	for i := 0; i < 7; i++ {
		svc.Post(context.Background(), "u4", nil) //nolint:errcheck
	}
	stats, ok := st.GetStats("u4")
	if !ok {
		t.Fatal("no stats found for u4")
	}
	if stats.TotalRequests != 7 {
		t.Errorf("total: want 7, got %d", stats.TotalRequests)
	}
	if stats.AllowedRequests != 5 {
		t.Errorf("allowed: want 5, got %d", stats.AllowedRequests)
	}
	if stats.RejectedRequests != 2 {
		t.Errorf("rejected: want 2, got %d", stats.RejectedRequests)
	}
}

func TestRateLimit_UsersAreIsolated(t *testing.T) {
	svc, _ := withRateLimit(5)
	for i := 0; i < 5; i++ {
		svc.Post(context.Background(), "userA", nil) //nolint:errcheck
	}
	if _, err := svc.Post(context.Background(), "userB", nil); err != nil {
		t.Fatalf("userB must not be affected by userA's limit: %v", err)
	}
}

func TestRateLimit_WindowExpiry(t *testing.T) {
	st := store.New()
	rl := limiter.New(2, 100*time.Millisecond) // tiny window for testing
	svc := service.NewRateLimitMiddleware(rl, st)(service.New(st))

	svc.Post(context.Background(), "u5", nil) //nolint:errcheck
	svc.Post(context.Background(), "u5", nil) //nolint:errcheck

	_, err := svc.Post(context.Background(), "u5", nil)
	if err != service.ErrRateLimitExceeded {
		t.Fatalf("3rd request within window should be rejected, got %v", err)
	}

	time.Sleep(110 * time.Millisecond)

	if _, err := svc.Post(context.Background(), "u5", nil); err != nil {
		t.Fatalf("request after window expiry should be allowed, got %v", err)
	}
}

// TestRateLimit_ConcurrentExactlyFive fires 20 goroutines simultaneously
// and asserts exactly 5 are admitted — verifying no over-admission under load.
func TestRateLimit_ConcurrentExactlyFive(t *testing.T) {
	svc, _ := withRateLimit(5)

	const goroutines = 20
	var (
		wg      sync.WaitGroup
		allowed atomic.Int32
		start   = make(chan struct{})
	)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := svc.Post(context.Background(), "concurrent", nil); err == nil {
				allowed.Add(1)
			}
		}()
	}

	close(start)
	wg.Wait()

	if got := int(allowed.Load()); got != 5 {
		t.Fatalf("concurrent: want exactly 5 allowed, got %d", got)
	}
}
