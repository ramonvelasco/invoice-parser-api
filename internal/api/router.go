package api

import (
	"net/http"
	"time"

	"github.com/invoiceparser/api/internal/billing"
	"github.com/invoiceparser/api/internal/db"
	"github.com/invoiceparser/api/internal/parser"
)

func NewRouter(database *db.DB, p *parser.Parser, stripe *billing.StripeClient, allowedOrigins string, maxUploadMB int64) http.Handler {
	handler := NewHandler(database, p, maxUploadMB)

	mux := http.NewServeMux()

	// Public routes
	mux.HandleFunc("GET /health", handler.HealthCheck)
	mux.HandleFunc("POST /v1/register", handler.RegisterKey)

	// Stripe webhook (public, verified by signature)
	if stripe != nil {
		mux.HandleFunc("POST /v1/webhooks/stripe", stripe.HandleWebhook)
	}

	// Protected routes
	authMw := AuthMiddleware(database)
	mux.Handle("POST /v1/parse/invoice", authMw(http.HandlerFunc(handler.ParseInvoice)))
	mux.Handle("GET /v1/usage", authMw(http.HandlerFunc(handler.GetUsage)))

	// Apply global middleware (outermost first)
	var h http.Handler = mux
	h = LoggingMiddleware(h)
	h = IPRateLimitMiddleware(NewIPRateLimiter(60, 1*time.Minute))(h) // 60 req/min per IP
	h = CORSMiddleware(allowedOrigins)(h)
	h = SecurityHeaders(h)

	return h
}
