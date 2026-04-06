package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/invoiceparser/api/internal/db"
)

type contextKey string

const (
	apiKeyContextKey    contextKey = "apiKey"
	requestIDContextKey contextKey = "requestID"
)

func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDContextKey).(string)
	return id
}

// RequestIDMiddleware generates a unique request ID for each request.
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			b := make([]byte, 8)
			_, _ = rand.Read(b)
			id = hex.EncodeToString(b)
		}
		w.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(r.Context(), requestIDContextKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func APIKeyFromContext(ctx context.Context) *db.APIKey {
	ak, _ := ctx.Value(apiKeyContextKey).(*db.APIKey)
	return ak
}

func setAPIKeyContext(ctx context.Context, ak *db.APIKey) context.Context {
	return context.WithValue(ctx, apiKeyContextKey, ak)
}

func AuthMiddleware(database *db.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get("X-API-Key")
			if key == "" {
				auth := r.Header.Get("Authorization")
				if strings.HasPrefix(auth, "Bearer ") {
					key = strings.TrimPrefix(auth, "Bearer ")
				}
			}

			if key == "" {
				writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
					"error":   "unauthorized",
					"message": "Missing API key. Include X-API-Key header or Authorization: Bearer <key>",
				})
				return
			}

			ak, err := database.GetAPIKey(key)
			if err != nil {
				writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
					"error":   "unauthorized",
					"message": "Invalid API key",
				})
				return
			}

			if ak.UsedCalls >= ak.MaxCalls {
				writeJSON(w, http.StatusTooManyRequests, map[string]interface{}{
					"error":      "rate_limit_exceeded",
					"message":    "Monthly API call limit reached. Upgrade your plan.",
					"used_calls": ak.UsedCalls,
					"max_calls":  ak.MaxCalls,
				})
				return
			}

			ctx := context.WithValue(r.Context(), apiKeyContextKey, ak)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// CORSMiddleware handles CORS with configurable allowed origins.
// Pass empty string to allow all origins.
func CORSMiddleware(allowedOrigins string) func(http.Handler) http.Handler {
	origins := parseOrigins(allowedOrigins)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" {
				if len(origins) == 0 {
					w.Header().Set("Access-Control-Allow-Origin", "*")
				} else if originAllowed(origin, origins) {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Vary", "Origin")
				}
			}

			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-API-Key, Authorization")
			w.Header().Set("Access-Control-Max-Age", "86400")

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func parseOrigins(s string) []string {
	if s == "" {
		return nil
	}
	var origins []string
	for _, o := range strings.Split(s, ",") {
		o = strings.TrimSpace(o)
		if o != "" {
			origins = append(origins, o)
		}
	}
	return origins
}

func originAllowed(origin string, allowed []string) bool {
	for _, o := range allowed {
		if strings.EqualFold(o, origin) {
			return true
		}
	}
	return false
}

// SecurityHeaders adds common security headers.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}

// IPRateLimiter limits requests per IP address using a token bucket.
type IPRateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*visitor
	rate     int           // requests per window
	window   time.Duration // window duration
}

type visitor struct {
	tokens    int
	lastReset time.Time
}

func NewIPRateLimiter(rate int, window time.Duration) *IPRateLimiter {
	rl := &IPRateLimiter{
		visitors: make(map[string]*visitor),
		rate:     rate,
		window:   window,
	}
	// Cleanup stale entries every 5 minutes
	go rl.cleanup()
	return rl
}

func (rl *IPRateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	v, ok := rl.visitors[ip]
	now := time.Now()

	if !ok || now.Sub(v.lastReset) >= rl.window {
		rl.visitors[ip] = &visitor{tokens: rl.rate - 1, lastReset: now}
		return true
	}

	if v.tokens <= 0 {
		return false
	}

	v.tokens--
	return true
}

func (rl *IPRateLimiter) cleanup() {
	for {
		time.Sleep(5 * time.Minute)
		rl.mu.Lock()
		now := time.Now()
		for ip, v := range rl.visitors {
			if now.Sub(v.lastReset) > rl.window*2 {
				delete(rl.visitors, ip)
			}
		}
		rl.mu.Unlock()
	}
}

func IPRateLimitMiddleware(limiter *IPRateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := extractIP(r)
			if !limiter.Allow(ip) {
				writeJSON(w, http.StatusTooManyRequests, map[string]interface{}{
					"error":   "rate_limit_exceeded",
					"message": "Too many requests. Please slow down.",
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func extractIP(r *http.Request) string {
	// Check X-Forwarded-For for proxied requests
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.SplitN(xff, ",", 2)
		return strings.TrimSpace(parts[0])
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		wrapped := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(wrapped, r)

		slog.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", wrapped.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"ip", extractIP(r),
			"request_id", RequestIDFromContext(r.Context()),
		)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
