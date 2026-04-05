package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/invoiceparser/api/internal/api"
	"github.com/invoiceparser/api/internal/billing"
	"github.com/invoiceparser/api/internal/db"
	"github.com/invoiceparser/api/internal/parser"
)

func main() {
	// Structured JSON logging
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	// --- Config validation ---
	aiKey := os.Getenv("GROQ_API_KEY")
	if aiKey == "" {
		slog.Error("GROQ_API_KEY environment variable is required")
		os.Exit(1)
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "invoiceparser.db"
	}

	allowedOrigins := os.Getenv("CORS_ORIGINS") // comma-separated, empty = allow all

	maxUploadMB := int64(10) // default 10MB, could be made configurable via env

	// --- Initialize components ---
	database, err := db.New(dbPath)
	if err != nil {
		slog.Error("failed to initialize database", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	// Start monthly usage reset background job
	database.StartMonthlyResetJob()

	p := parser.New(aiKey)

	// Stripe is optional — runs fine without it for free tier
	var stripe *billing.StripeClient
	stripeKey := os.Getenv("STRIPE_SECRET_KEY")
	stripeWebhook := os.Getenv("STRIPE_WEBHOOK_SECRET")
	if stripeKey != "" {
		// Load Stripe price IDs from env
		starterPriceID := os.Getenv("STRIPE_STARTER_PRICE_ID")
		proPriceID := os.Getenv("STRIPE_PRO_PRICE_ID")
		stripe = billing.NewStripeClient(stripeKey, stripeWebhook, database, starterPriceID, proPriceID)
		slog.Info("stripe billing enabled")
	} else {
		slog.Info("stripe not configured, free tier only")
	}

	router := api.NewRouter(database, p, stripe, allowedOrigins, maxUploadMB)

	// Serve landing page for root, API routes for /v1/ and /health
	mux := http.NewServeMux()
	mux.Handle("/v1/", router)
	mux.Handle("/health", router)
	mux.Handle("/", http.FileServer(http.Dir("landing")))

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// --- Graceful shutdown ---
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)

	go func() {
		slog.Info("InvoiceParser API starting", "port", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	<-done
	slog.Info("shutting down gracefully...")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		slog.Error("forced shutdown", "error", err)
	}

	slog.Info("server stopped")
}
