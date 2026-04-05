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

	mux := http.NewServeMux()

	// Public routes
	mux.HandleFunc("GET /health", handler.HealthCheck)
	mux.HandleFunc("POST /v1/register", handler.RegisterKey)
	mux.HandleFunc("GET /metrics", metrics.Handler())

	// Stripe webhook (public, verified by signature)
	if stripe != nil {
		mux.HandleFunc("POST /v1/webhooks/stripe", stripe.HandleWebhook)
	}

	// Protected routes
	authMw := AuthMiddleware(database)
	mux.Handle("POST /v1/parse/invoice", authMw(http.HandlerFunc(handler.ParseInvoice)))
	mux.Handle("POST /v1/parse/url", authMw(http.HandlerFunc(handler.ParseInvoiceURL)))
	mux.Handle("POST /v1/parse/batch", authMw(http.HandlerFunc(handler.BatchParseInvoice)))
	mux.Handle("GET /v1/parse/batch/{id}", authMw(http.HandlerFunc(handler.GetBatchJob)))
	mux.Handle("GET /v1/usage", authMw(http.HandlerFunc(handler.GetUsage)))
	mux.Handle("GET /v1/dashboard", authMw(http.HandlerFunc(handler.GetDashboard)))
	mux.Handle("POST /v1/billing/checkout", authMw(http.HandlerFunc(handler.CreateCheckout)))
	mux.Handle("POST /v1/rotate-key", authMw(http.HandlerFunc(handler.RotateKey)))

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
