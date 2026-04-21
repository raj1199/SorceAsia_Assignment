package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-kit/log"
	"github.com/rajsonawane/rate-limited-api/internal/endpoint"
	"github.com/rajsonawane/rate-limited-api/internal/limiter"
	"github.com/rajsonawane/rate-limited-api/internal/service"
	"github.com/rajsonawane/rate-limited-api/internal/store"
	httptransport "github.com/rajsonawane/rate-limited-api/internal/transport/http"
)

func main() {
	logger := log.With(
		log.NewJSONLogger(os.Stdout),
		"ts", log.DefaultTimestampUTC,
	)

	st := store.New()
	rl := limiter.New(5, time.Minute)

	// Build the service by wrapping the core implementation with middleware.
	// Execution order (innermost first): core → rateLimitMiddleware → loggingMiddleware
	var svc service.Service
	svc = service.New(st)
	svc = service.NewRateLimitMiddleware(rl, st)(svc)
	svc = service.NewLoggingMiddleware(logger)(svc)

	endpoints := endpoint.NewSet(svc)
	handler := httptransport.NewHTTPHandler(endpoints)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	done := make(chan struct{})
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		sig := <-quit
		_ = logger.Log("msg", "shutdown signal received", "signal", sig)

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		close(done)
	}()

	_ = logger.Log("msg", "server starting", "port", port)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		_ = logger.Log("msg", "server error", "err", err)
		os.Exit(1)
	}
	<-done
	_ = logger.Log("msg", "server stopped")
}
