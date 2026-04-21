package service

import (
	"context"

	"github.com/go-kit/log"
	"github.com/rajsonawane/rate-limited-api/internal/limiter"
	"github.com/rajsonawane/rate-limited-api/internal/store"
)

// ── Rate-limit middleware ─────────────────────────────────────────────────────

type rateLimitMiddleware struct {
	limiter *limiter.SlidingWindowLimiter
	store   *store.Store
	next    Service
}

// NewRateLimitMiddleware returns a Middleware that enforces per-user rate limits
// and records every attempt (allowed or rejected) in the store.
func NewRateLimitMiddleware(l *limiter.SlidingWindowLimiter, st *store.Store) Middleware {
	return func(next Service) Service {
		return &rateLimitMiddleware{limiter: l, store: st, next: next}
	}
}

func (m *rateLimitMiddleware) Post(ctx context.Context, userID string, payload any) (PostResult, error) {
	allowed := m.limiter.Allow(userID)
	m.store.Record(userID, allowed)
	if !allowed {
		return PostResult{}, ErrRateLimitExceeded
	}
	result, err := m.next.Post(ctx, userID, payload)
	if err == nil {
		result.Remaining = m.limiter.Remaining(userID)
	}
	return result, err
}

func (m *rateLimitMiddleware) Stats(ctx context.Context, userID string) ([]store.UserStats, error) {
	return m.next.Stats(ctx, userID)
}

// ── Logging middleware ────────────────────────────────────────────────────────

type loggingMiddleware struct {
	logger log.Logger
	next   Service
}

// NewLoggingMiddleware returns a Middleware that logs method calls using the
// go-kit structured logger. It captures named return values via defer so it
// records the actual error even when the inner call panics-and-recovers.
func NewLoggingMiddleware(logger log.Logger) Middleware {
	return func(next Service) Service {
		return &loggingMiddleware{logger: logger, next: next}
	}
}

func (m *loggingMiddleware) Post(ctx context.Context, userID string, payload any) (result PostResult, err error) {
	defer func() {
		_ = m.logger.Log("method", "Post", "user_id", userID, "remaining", result.Remaining, "err", err)
	}()
	return m.next.Post(ctx, userID, payload)
}

func (m *loggingMiddleware) Stats(ctx context.Context, userID string) (stats []store.UserStats, err error) {
	defer func() {
		_ = m.logger.Log("method", "Stats", "user_id", userID, "count", len(stats), "err", err)
	}()
	return m.next.Stats(ctx, userID)
}
