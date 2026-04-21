package service

import (
	"context"
	"time"

	"github.com/rajsonawane/rate-limited-api/internal/store"
)

// PostResult carries the outcome of an accepted POST /request call.
type PostResult struct {
	UserID    string
	Remaining int
	Timestamp time.Time
}

// Service is the domain interface for the rate-limited API.
// Each method maps 1-to-1 with an API endpoint.
type Service interface {
	Post(ctx context.Context, userID string, payload any) (PostResult, error)
	Stats(ctx context.Context, userID string) ([]store.UserStats, error)
}

// Middleware is the go-kit service-middleware type.
type Middleware func(Service) Service

// basicService is the core implementation — no cross-cutting concerns.
type basicService struct {
	store *store.Store
}

func New(st *store.Store) Service {
	return &basicService{store: st}
}

func (s *basicService) Post(_ context.Context, userID string, _ any) (PostResult, error) {
	return PostResult{UserID: userID, Timestamp: time.Now().UTC()}, nil
}

func (s *basicService) Stats(_ context.Context, userID string) ([]store.UserStats, error) {
	if userID != "" {
		st, ok := s.store.GetStats(userID)
		if !ok {
			return nil, ErrUserNotFound
		}
		return []store.UserStats{st}, nil
	}
	return s.store.AllStats(), nil
}
