// Package httptransport wires go-kit Endpoints to HTTP using
// github.com/go-kit/kit/transport/http. Each endpoint gets its own
// Server with typed decode/encode functions. Business errors are mapped
// to appropriate HTTP status codes in errorEncoder.
package httptransport

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	kithttp "github.com/go-kit/kit/transport/http"
	"github.com/rajsonawane/rate-limited-api/internal/endpoint"
	"github.com/rajsonawane/rate-limited-api/internal/service"
)

// NewHTTPHandler builds the root http.Handler for the service.
func NewHTTPHandler(endpoints endpoint.Set) http.Handler {
	opts := []kithttp.ServerOption{
		kithttp.ServerErrorEncoder(errorEncoder),
	}

	postServer := kithttp.NewServer(
		endpoints.PostEndpoint,
		decodePostRequest,
		encodeResponse,
		opts...,
	)

	statsServer := kithttp.NewServer(
		endpoints.StatsEndpoint,
		decodeStatsRequest,
		encodeResponse,
		opts...,
	)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /request", postServer.ServeHTTP)
	mux.HandleFunc("GET /stats", statsServer.ServeHTTP)
	mux.HandleFunc("GET /health", healthHandler)
	return mux
}

// ── Decode functions ──────────────────────────────────────────────────────────

func decodePostRequest(_ context.Context, r *http.Request) (interface{}, error) {
	var body struct {
		UserID  string `json:"user_id"`
		Payload any    `json:"payload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return nil, service.ErrInvalidInput
	}
	body.UserID = strings.TrimSpace(body.UserID)
	if body.UserID == "" {
		return nil, service.ErrInvalidInput
	}
	return endpoint.PostRequest{UserID: body.UserID, Payload: body.Payload}, nil
}

func decodeStatsRequest(_ context.Context, r *http.Request) (interface{}, error) {
	return endpoint.StatsRequest{UserID: strings.TrimSpace(r.URL.Query().Get("user_id"))}, nil
}

// ── Encode functions ──────────────────────────────────────────────────────────

// encodeResponse serialises a successful response to JSON.
// If the response implements StatusCoder the returned code is used;
// otherwise 200 OK is written.
func encodeResponse(_ context.Context, w http.ResponseWriter, response interface{}) error {
	w.Header().Set("Content-Type", "application/json")
	if sc, ok := response.(interface{ StatusCode() int }); ok {
		w.WriteHeader(sc.StatusCode())
	}
	return json.NewEncoder(w).Encode(response)
}

// errorEncoder maps domain errors to HTTP status codes.
func errorEncoder(_ context.Context, err error, w http.ResponseWriter) {
	code := codeFrom(err)
	w.Header().Set("Content-Type", "application/json")
	if code == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", "60")
	}
	w.WriteHeader(code)

	type body struct {
		Error      string `json:"error"`
		RetryAfter int    `json:"retry_after_seconds,omitempty"`
	}
	resp := body{Error: err.Error()}
	if code == http.StatusTooManyRequests {
		resp.RetryAfter = 60
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func codeFrom(err error) int {
	switch err {
	case service.ErrRateLimitExceeded:
		return http.StatusTooManyRequests
	case service.ErrUserNotFound:
		return http.StatusNotFound
	case service.ErrInvalidInput:
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
