package main

import (
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// loginRateLimiter is a small in-memory fixed-window limiter for login
// attempts. A distributed deployment should replace it with a shared store,
// but it already blocks trivial password guessing on one instance.
type loginRateLimiter struct {
	mu       sync.Mutex
	attempts map[string]loginWindow
	limit    int
	window   time.Duration
	now      func() time.Time
}

type loginWindow struct {
	count int
	reset time.Time
}

func newLoginRateLimiter(limit int, window time.Duration) *loginRateLimiter {
	return &loginRateLimiter{
		attempts: make(map[string]loginWindow),
		limit:    limit,
		window:   window,
		now:      time.Now,
	}
}

func (l *loginRateLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	current, ok := l.attempts[key]
	if !ok || !now.Before(current.reset) {
		l.attempts[key] = loginWindow{count: 1, reset: now.Add(l.window)}
		return true
	}
	if current.count >= l.limit {
		return false
	}
	current.count++
	l.attempts[key] = current
	return true
}

func (cfg *apiConfig) middlewareLoginRateLimit(next http.HandlerFunc) http.HandlerFunc {
	return middlewareRateLimit(cfg.loginLimiter, "Too many login attempts", next)
}

// middlewarePasswordResetRateLimit protects the public reset endpoints from
// account-enumeration sweeps and token spam.
func (cfg *apiConfig) middlewarePasswordResetRateLimit(next http.HandlerFunc) http.HandlerFunc {
	return middlewareRateLimit(cfg.passwordResetLimiter, "Too many reset attempts", next)
}

func middlewareRateLimit(limiter *loginRateLimiter, message string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if limiter == nil {
			next.ServeHTTP(w, r)
			return
		}
		if !limiter.allow(clientIP(r)) {
			w.Header().Set("Retry-After", strconv.Itoa(int(limiter.window.Seconds())))
			respondWithError(w, http.StatusTooManyRequests, message, nil)
			return
		}
		next.ServeHTTP(w, r)
	}
}

// clientIP uses the transport address, not client-controlled forwarding
// headers. Behind a trusted proxy this should be derived from a verified
// proxy header instead.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
