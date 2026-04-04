package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/invoiceparser/api/internal/db"
)

func TestAuthMiddleware_MissingKey(t *testing.T) {
	tmpFile := t.TempDir() + "/test.db"
	database, _ := db.New(tmpFile)
	t.Cleanup(func() { database.Close(); os.Remove(tmpFile) })

	handler := AuthMiddleware(database)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestAuthMiddleware_InvalidKey(t *testing.T) {
	tmpFile := t.TempDir() + "/test.db"
	database, _ := db.New(tmpFile)
	t.Cleanup(func() { database.Close(); os.Remove(tmpFile) })

	handler := AuthMiddleware(database)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-API-Key", "inv_nonexistent")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestAuthMiddleware_ValidKey(t *testing.T) {
	tmpFile := t.TempDir() + "/test.db"
	database, _ := db.New(tmpFile)
	t.Cleanup(func() { database.Close(); os.Remove(tmpFile) })

	database.CreateAPIKey("inv_valid123", "test@test.com", "free", 100)

	var gotKey *db.APIKey
	handler := AuthMiddleware(database)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = APIKeyFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-API-Key", "inv_valid123")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if gotKey == nil || gotKey.Key != "inv_valid123" {
		t.Error("expected API key to be set in context")
	}
}

func TestAuthMiddleware_BearerToken(t *testing.T) {
	tmpFile := t.TempDir() + "/test.db"
	database, _ := db.New(tmpFile)
	t.Cleanup(func() { database.Close(); os.Remove(tmpFile) })

	database.CreateAPIKey("inv_bearer123", "bearer@test.com", "free", 100)

	handler := AuthMiddleware(database)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer inv_bearer123")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestAuthMiddleware_RateLimited(t *testing.T) {
	tmpFile := t.TempDir() + "/test.db"
	database, _ := db.New(tmpFile)
	t.Cleanup(func() { database.Close(); os.Remove(tmpFile) })

	// Create key with 0 remaining calls
	ak, _ := database.CreateAPIKey("inv_limited", "limited@test.com", "free", 1)
	database.IncrementUsage(ak.ID)

	handler := AuthMiddleware(database)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-API-Key", "inv_limited")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", w.Code)
	}
}

func TestCORSMiddleware_AllowAll(t *testing.T) {
	handler := CORSMiddleware("")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "https://example.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("expected wildcard CORS when no origins configured")
	}
}

func TestCORSMiddleware_RestrictedOrigins(t *testing.T) {
	handler := CORSMiddleware("https://allowed.com,https://also-allowed.com")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Allowed origin
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "https://allowed.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Header().Get("Access-Control-Allow-Origin") != "https://allowed.com" {
		t.Errorf("expected allowed origin, got %s", w.Header().Get("Access-Control-Allow-Origin"))
	}

	// Disallowed origin
	req2 := httptest.NewRequest("GET", "/test", nil)
	req2.Header.Set("Origin", "https://evil.com")
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)

	if w2.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("expected no CORS header for disallowed origin, got %s", w2.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCORSMiddleware_Preflight(t *testing.T) {
	handler := CORSMiddleware("")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("OPTIONS", "/test", nil)
	req.Header.Set("Origin", "https://example.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204 for preflight, got %d", w.Code)
	}
}

func TestIPRateLimiter(t *testing.T) {
	limiter := NewIPRateLimiter(3, 1*time.Minute)

	for i := 0; i < 3; i++ {
		if !limiter.Allow("1.2.3.4") {
			t.Errorf("request %d should be allowed", i+1)
		}
	}

	if limiter.Allow("1.2.3.4") {
		t.Error("4th request should be rate limited")
	}

	// Different IP should still work
	if !limiter.Allow("5.6.7.8") {
		t.Error("different IP should be allowed")
	}
}

func TestSecurityHeaders(t *testing.T) {
	handler := SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing X-Content-Type-Options header")
	}
	if w.Header().Get("X-Frame-Options") != "DENY" {
		t.Error("missing X-Frame-Options header")
	}
}

func TestExtractIP(t *testing.T) {
	tests := []struct {
		name     string
		xff      string
		xri      string
		remote   string
		expected string
	}{
		{"X-Forwarded-For", "1.2.3.4, 5.6.7.8", "", "9.9.9.9:1234", "1.2.3.4"},
		{"X-Real-IP", "", "1.2.3.4", "9.9.9.9:1234", "1.2.3.4"},
		{"RemoteAddr", "", "", "9.9.9.9:1234", "9.9.9.9"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			if tt.xff != "" {
				req.Header.Set("X-Forwarded-For", tt.xff)
			}
			if tt.xri != "" {
				req.Header.Set("X-Real-IP", tt.xri)
			}
			req.RemoteAddr = tt.remote

			ip := extractIP(req)
			if ip != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, ip)
			}
		})
	}
}
