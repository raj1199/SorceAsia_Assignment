package store

import (
	"sync"
	"time"
)

type UserStats struct {
	UserID           string    `json:"user_id"`
	TotalRequests    int       `json:"total_requests"`
	AllowedRequests  int       `json:"allowed_requests"`
	RejectedRequests int       `json:"rejected_requests"`
	LastRequestAt    time.Time `json:"last_request_at"`
}

type Store struct {
	mu    sync.RWMutex
	stats map[string]*UserStats
}

func New() *Store {
	return &Store{stats: make(map[string]*UserStats)}
}

// Record atomically updates stats for a user after a rate-limit decision.
func (s *Store) Record(userID string, allowed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	st, ok := s.stats[userID]
	if !ok {
		st = &UserStats{UserID: userID}
		s.stats[userID] = st
	}

	st.TotalRequests++
	if allowed {
		st.AllowedRequests++
	} else {
		st.RejectedRequests++
	}
	st.LastRequestAt = time.Now()
}

// GetStats returns a copy of the stats for a single user.
func (s *Store) GetStats(userID string) (UserStats, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.stats[userID]
	if !ok {
		return UserStats{}, false
	}
	return *st, true
}

// AllStats returns a snapshot of stats for every user seen so far.
func (s *Store) AllStats() []UserStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]UserStats, 0, len(s.stats))
	for _, st := range s.stats {
		out = append(out, *st)
	}
	return out
}
