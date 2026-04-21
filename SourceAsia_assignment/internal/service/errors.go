package service

import "errors"

var (
	ErrRateLimitExceeded = errors.New("rate limit exceeded: max 5 requests per minute")
	ErrUserNotFound      = errors.New("no stats found for user")
	ErrInvalidInput      = errors.New("user_id is required")
)
