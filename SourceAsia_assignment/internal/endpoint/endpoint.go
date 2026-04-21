// Package endpoint translates domain service calls into go-kit Endpoints.
// Each exported field of Set wraps one Service method so that transport layers
// (HTTP, gRPC, …) can invoke it without knowing the concrete service type.
package endpoint

import (
	"context"
	"net/http"
	"time"

	kitendpoint "github.com/go-kit/kit/endpoint"
	"github.com/rajsonawane/rate-limited-api/internal/service"
	"github.com/rajsonawane/rate-limited-api/internal/store"
)

// Set groups all endpoints for this service.
type Set struct {
	PostEndpoint  kitendpoint.Endpoint
	StatsEndpoint kitendpoint.Endpoint
}

// NewSet constructs a Set from a service implementation.
// Endpoint-level middleware (auth, circuit-breaking, …) can be applied here
// without touching service or transport code.
func NewSet(svc service.Service) Set {
	return Set{
		PostEndpoint:  makePostEndpoint(svc),
		StatsEndpoint: makeStatsEndpoint(svc),
	}
}

// ── POST /request ─────────────────────────────────────────────────────────────

type PostRequest struct {
	UserID  string
	Payload any
}

type PostResponse struct {
	Message   string    `json:"message"`
	UserID    string    `json:"user_id"`
	Remaining int       `json:"remaining_requests"`
	Timestamp time.Time `json:"timestamp"`
}

// StatusCode implements go-kit's StatusCoder so the HTTP transport writes 201.
func (PostResponse) StatusCode() int { return http.StatusCreated }

func makePostEndpoint(svc service.Service) kitendpoint.Endpoint {
	return func(ctx context.Context, request interface{}) (interface{}, error) {
		req := request.(PostRequest)
		result, err := svc.Post(ctx, req.UserID, req.Payload)
		if err != nil {
			return nil, err
		}
		return PostResponse{
			Message:   "request accepted",
			UserID:    result.UserID,
			Remaining: result.Remaining,
			Timestamp: result.Timestamp,
		}, nil
	}
}

// ── GET /stats ────────────────────────────────────────────────────────────────

type StatsRequest struct {
	UserID string // empty means all users
}

type StatsResponse struct {
	Users []store.UserStats `json:"users"`
}

func makeStatsEndpoint(svc service.Service) kitendpoint.Endpoint {
	return func(ctx context.Context, request interface{}) (interface{}, error) {
		req := request.(StatsRequest)
		stats, err := svc.Stats(ctx, req.UserID)
		if err != nil {
			return nil, err
		}
		return StatsResponse{Users: stats}, nil
	}
}
