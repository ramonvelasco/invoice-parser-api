package api

import (
	"net/http"
	"time"

	"github.com/invoiceparser/api/internal/billing"
	"github.com/invoiceparser/api/internal/db"
	"github.com/invoiceparser/api/internal/parser"
)

func NewRouter(database *db.DB, p *parser.Parser, stripe *billing.StripeClient, allowedOrigins string, maxUploadMB int64) http.Handler {
	metrics := NewMetrics()
	handler := NewHandler(database, p, stripe, maxUploadMB, metrics)
	oauth := NewOAuthHandler(database)

	mux := http.NewServeMux()

	// Public routes
	mux.HandleFunc("GET /health", handler.HealthCheck)
	mux.HandleFunc("POST /v1/register", handler.RegisterKey)
	mux.HandleFunc("GET /metrics", metrics.Handler())

	// OAuth routes
	mux.HandleFunc("GET /auth/github", oauth.GitHubLogin)
	mux.HandleFunc("GET /auth/github/callback", oauth.GitHubCallback)
	mux.HandleFunc("GET /auth/google", oauth.GoogleLogin)
	mux.HandleFunc("GET /auth/google/callback", oauth.GoogleCallback)
	mux.HandleFunc("GET /auth/me", oauth.GetMe)
	mux.HandleFunc("POST /auth/logout", oauth.Logout)

	// Stripe webhook (public, verified by signature)
	if stripe != nil {
		mux.HandleFunc("POST /v1/webhooks/stripe", stripe.HandleWebhook)
	}

	// Protected routes (API key auth)
	authMw := AuthMiddleware(database)
	mux.Handle("POST /v1/parse/invoice", authMw(http.HandlerFunc(handler.ParseInvoice)))
	mux.Handle("POST /v1/parse/url", authMw(http.HandlerFunc(handler.ParseInvoiceURL)))
	mux.Handle("POST /v1/parse/{type}", authMw(http.HandlerFunc(handler.ParseDocument)))
	mux.Handle("POST /v1/parse/batch", authMw(http.HandlerFunc(handler.BatchParseInvoice)))
	mux.Handle("GET /v1/parse/batch/{id}", authMw(http.HandlerFunc(handler.GetBatchJob)))
	mux.Handle("GET /v1/usage", authMw(http.HandlerFunc(handler.GetUsage)))
	mux.Handle("GET /v1/dashboard", authMw(http.HandlerFunc(handler.GetDashboard)))
	mux.Handle("POST /v1/billing/checkout", authMw(http.HandlerFunc(handler.CreateCheckout)))
	mux.Handle("POST /v1/rotate-key", authMw(http.HandlerFunc(handler.RotateKey)))

	// Portal API routes (session-based auth from OAuth cookie)
	portalAuth := oauth.SessionAuthMiddleware
	mux.Handle("GET /portal/api/usage", portalAuth(http.HandlerFunc(handler.GetUsage)))
	mux.Handle("GET /portal/api/dashboard", portalAuth(http.HandlerFunc(handler.GetDashboard)))
	mux.Handle("POST /portal/api/rotate-key", portalAuth(http.HandlerFunc(handler.RotateKey)))
	mux.Handle("POST /portal/api/checkout", portalAuth(http.HandlerFunc(handler.CreateCheckout)))

	// Apply global middleware (outermost first)
	var h http.Handler = mux
	h = LoggingMiddleware(h)
	h = IPRateLimitMiddleware(NewIPRateLimiter(60, 1*time.Minute))(h)
	h = CORSMiddleware(allowedOrigins)(h)
	h = SecurityHeaders(h)
	h = RecoveryMiddleware(h)
	h = RequestIDMiddleware(h)

	return h
}
