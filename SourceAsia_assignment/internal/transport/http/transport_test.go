package httptransport_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-kit/log"
	"github.com/rajsonawane/rate-limited-api/internal/endpoint"
	"github.com/rajsonawane/rate-limited-api/internal/limiter"
	"github.com/rajsonawane/rate-limited-api/internal/service"
	"github.com/rajsonawane/rate-limited-api/internal/store"
	httptransport "github.com/rajsonawane/rate-limited-api/internal/transport/http"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	st := store.New()
	rl := limiter.New(5, time.Minute)

	var svc service.Service
	svc = service.New(st)
	svc = service.NewRateLimitMiddleware(rl, st)(svc)
	svc = service.NewLoggingMiddleware(log.NewNopLogger())(svc)

	return httptest.NewServer(httptransport.NewHTTPHandler(endpoint.NewSet(svc)))
}

func post(t *testing.T, srv *httptest.Server, rawBody string) *http.Response {
	t.Helper()
	resp, err := http.Post(srv.URL+"/request", "application/json", bytes.NewBufferString(rawBody))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func postUser(t *testing.T, srv *httptest.Server, userID string) *http.Response {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"user_id": userID, "payload": "test"})
	return post(t, srv, string(body))
}

func decodeBody(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}
	return m
}

// ═════════════════════════════════════════════════════════════════════════════
// POST /request
// ═════════════════════════════════════════════════════════════════════════════

// TestPost_ValidRequest verifies the happy-path response shape.
func TestPost_ValidRequest(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	resp := postUser(t, srv, "alice")

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status: want 201, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type: want application/json, got %s", ct)
	}

	body := decodeBody(t, resp)
	if body["message"] != "request accepted" {
		t.Errorf("message: want \"request accepted\", got %v", body["message"])
	}
	if body["user_id"] != "alice" {
		t.Errorf("user_id: want \"alice\", got %v", body["user_id"])
	}
	if _, ok := body["remaining_requests"]; !ok {
		t.Error("response must include remaining_requests")
	}
	if _, ok := body["timestamp"]; !ok {
		t.Error("response must include timestamp")
	}
}

// TestPost_RateLimitSequence fires 5 allowed requests and asserts
// remaining_requests counts down correctly, then asserts the 6th is rejected.
func TestPost_RateLimitSequence(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	for i := 1; i <= 5; i++ {
		resp := postUser(t, srv, "bob")
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("request %d: want 201, got %d", i, resp.StatusCode)
		}
		body := decodeBody(t, resp)
		wantRemaining := float64(5 - i)
		if body["remaining_requests"] != wantRemaining {
			t.Errorf("request %d: remaining_requests want %.0f, got %v", i, wantRemaining, body["remaining_requests"])
		}
	}

	// 6th request must be rejected
	resp := postUser(t, srv, "bob")
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("6th request: want 429, got %d", resp.StatusCode)
	}
}

// TestPost_429ErrorBody checks that a rate-limited response has the correct
// body fields and headers.
func TestPost_429ErrorBody(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	for i := 0; i < 5; i++ {
		postUser(t, srv, "charlie")
	}
	resp := postUser(t, srv, "charlie")

	// Status
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("want 429, got %d", resp.StatusCode)
	}
	// Header
	if got := resp.Header.Get("Retry-After"); got != "60" {
		t.Errorf("Retry-After header: want \"60\", got %q", got)
	}
	// Content-Type
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type: want application/json, got %s", ct)
	}
	// Body
	body := decodeBody(t, resp)
	if body["error"] == nil {
		t.Error("error response must have \"error\" field")
	}
	if body["retry_after_seconds"] != float64(60) {
		t.Errorf("retry_after_seconds: want 60, got %v", body["retry_after_seconds"])
	}
}

// TestPost_ErrorCases covers every invalid-input scenario for POST /request.
func TestPost_ErrorCases(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantStatus int
		wantErrMsg bool
	}{
		{
			name:       "missing user_id field",
			body:       `{"payload":"hello"}`,
			wantStatus: http.StatusBadRequest,
			wantErrMsg: true,
		},
		{
			name:       "empty string user_id",
			body:       `{"user_id":"","payload":"hello"}`,
			wantStatus: http.StatusBadRequest,
			wantErrMsg: true,
		},
		{
			name:       "whitespace-only user_id",
			body:       `{"user_id":"   ","payload":"hello"}`,
			wantStatus: http.StatusBadRequest,
			wantErrMsg: true,
		},
		{
			name:       "invalid JSON",
			body:       `{bad json}`,
			wantStatus: http.StatusBadRequest,
			wantErrMsg: true,
		},
		{
			name:       "empty body",
			body:       ``,
			wantStatus: http.StatusBadRequest,
			wantErrMsg: true,
		},
		{
			name:       "array instead of object",
			body:       `[{"user_id":"alice"}]`,
			wantStatus: http.StatusBadRequest,
			wantErrMsg: true,
		},
	}

	srv := newTestServer(t)
	defer srv.Close()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := post(t, srv, tc.body)
			if resp.StatusCode != tc.wantStatus {
				t.Errorf("status: want %d, got %d", tc.wantStatus, resp.StatusCode)
			}
			if tc.wantErrMsg {
				body := decodeBody(t, resp)
				if body["error"] == nil {
					t.Error("response must contain an \"error\" field")
				}
			}
		})
	}
}

// TestPost_UsersAreIndependent asserts that exhausting one user's quota
// does not affect a different user.
func TestPost_UsersAreIndependent(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	for i := 0; i < 5; i++ {
		postUser(t, srv, "userA")
	}
	// userA exhausted — userB must still be allowed
	resp := postUser(t, srv, "userB")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("userB should not be affected by userA's limit; got %d", resp.StatusCode)
	}
}

// TestPost_WrongMethod checks that GET /request returns 405.
func TestPost_WrongMethod(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/request")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("want 405, got %d", resp.StatusCode)
	}
}

// TestPost_ConcurrentRateLimit fires 20 goroutines simultaneously for the same
// user and asserts exactly 5 get HTTP 201. This validates the atomic
// check-and-record under real network concurrency.
func TestPost_ConcurrentRateLimit(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	const total = 20
	const limit = 5

	var (
		wg      sync.WaitGroup
		allowed atomic.Int32
		start   = make(chan struct{})
	)

	for i := 0; i < total; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // all goroutines unblock simultaneously
			resp := postUser(t, srv, "concurrent-user")
			if resp.StatusCode == http.StatusCreated {
				allowed.Add(1)
			}
		}()
	}

	close(start)
	wg.Wait()

	if got := int(allowed.Load()); got != limit {
		t.Fatalf("concurrent: exactly %d requests should be allowed, got %d", limit, got)
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// GET /stats
// ═════════════════════════════════════════════════════════════════════════════

// TestStats_EmptyStore verifies stats returns an empty list when no requests
// have been made.
func TestStats_EmptyStore(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	resp, _ := http.Get(srv.URL + "/stats")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	body := decodeBody(t, resp)
	users := body["users"].([]any)
	if len(users) != 0 {
		t.Fatalf("want empty users list, got %d entries", len(users))
	}
}

// TestStats_AllUsers checks that the all-users endpoint lists every unique
// user seen so far.
func TestStats_AllUsers(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	postUser(t, srv, "alice")
	postUser(t, srv, "alice")
	postUser(t, srv, "bob")

	resp, _ := http.Get(srv.URL + "/stats")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	body := decodeBody(t, resp)
	users := body["users"].([]any)
	if len(users) != 2 {
		t.Fatalf("want 2 users, got %d", len(users))
	}
}

// TestStats_SingleUser verifies per-user query and that allowed/rejected counts
// are accurate after hitting the rate limit.
func TestStats_SingleUser_Counts(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	// 5 allowed + 1 rejected
	for i := 0; i < 6; i++ {
		postUser(t, srv, "dave")
	}

	resp, _ := http.Get(srv.URL + "/stats?user_id=dave")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}

	body := decodeBody(t, resp)
	users := body["users"].([]any)
	if len(users) != 1 {
		t.Fatalf("want 1 user entry, got %d", len(users))
	}

	stats := users[0].(map[string]any)
	checks := []struct {
		field string
		want  float64
	}{
		{"total_requests", 6},
		{"allowed_requests", 5},
		{"rejected_requests", 1},
	}
	for _, c := range checks {
		if stats[c.field] != c.want {
			t.Errorf("stats.%s: want %.0f, got %v", c.field, c.want, stats[c.field])
		}
	}
	if stats["user_id"] != "dave" {
		t.Errorf("user_id: want \"dave\", got %v", stats["user_id"])
	}
	if stats["last_request_at"] == nil {
		t.Error("last_request_at must be present")
	}
}

// TestStats_UnknownUser expects 404 for a user that has never made a request.
func TestStats_UnknownUser(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	resp, _ := http.Get(srv.URL + "/stats?user_id=ghost")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
	body := decodeBody(t, resp)
	if body["error"] == nil {
		t.Error("404 response must include \"error\" field")
	}
}

// TestStats_MultipleUsers_AllCounts verifies aggregate stats across multiple
// users including one that has been rate-limited.
func TestStats_MultipleUsers_AllCounts(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	// alice: 3 allowed
	for i := 0; i < 3; i++ {
		postUser(t, srv, "alice")
	}
	// bob: 5 allowed + 2 rejected
	for i := 0; i < 7; i++ {
		postUser(t, srv, "bob")
	}

	check := func(userID string, wantTotal, wantAllowed, wantRejected float64) {
		t.Helper()
		resp, _ := http.Get(fmt.Sprintf("%s/stats?user_id=%s", srv.URL, userID))
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: want 200, got %d", userID, resp.StatusCode)
			return
		}
		body := decodeBody(t, resp)
		users := body["users"].([]any)
		s := users[0].(map[string]any)
		if s["total_requests"] != wantTotal {
			t.Errorf("%s total: want %.0f, got %v", userID, wantTotal, s["total_requests"])
		}
		if s["allowed_requests"] != wantAllowed {
			t.Errorf("%s allowed: want %.0f, got %v", userID, wantAllowed, s["allowed_requests"])
		}
		if s["rejected_requests"] != wantRejected {
			t.Errorf("%s rejected: want %.0f, got %v", userID, wantRejected, s["rejected_requests"])
		}
	}

	check("alice", 3, 3, 0)
	check("bob", 7, 5, 2)
}

// TestStats_WrongMethod checks that POST /stats returns 405.
func TestStats_WrongMethod(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	body, _ := json.Marshal(map[string]any{})
	resp, err := http.Post(srv.URL+"/stats", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("want 405, got %d", resp.StatusCode)
	}
}

// TestStats_ContentType checks that stats responses carry the correct header.
func TestStats_ContentType(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	resp, _ := http.Get(srv.URL + "/stats")
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type: want application/json, got %s", ct)
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// GET /health
// ═════════════════════════════════════════════════════════════════════════════

func TestHealth_Response(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	resp, _ := http.Get(srv.URL + "/health")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	body := decodeBody(t, resp)
	if body["status"] != "ok" {
		t.Errorf("status: want \"ok\", got %v", body["status"])
	}
}
