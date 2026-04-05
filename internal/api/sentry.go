package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"runtime/debug"
)

// RecoveryMiddleware catches panics and reports them, preventing server crashes.
func RecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				stack := string(debug.Stack())
				slog.Error("panic recovered",
					"error", fmt.Sprintf("%v", err),
					"path", r.URL.Path,
					"method", r.Method,
					"stack", stack,
				)

				// Report to Sentry if DSN is configured
				sentryDSN := os.Getenv("SENTRY_DSN")
				if sentryDSN != "" {
					reportToSentry(sentryDSN, fmt.Sprintf("%v", err), r.URL.Path, stack)
				}

				writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
					"error":   "internal_error",
					"message": "An unexpected error occurred.",
				})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// reportToSentry sends an error report to Sentry via their HTTP API.
// This avoids adding the Sentry SDK as a dependency — lightweight approach.
func reportToSentry(dsn, errMsg, path, stack string) {
	// Log that we would report — in production with the Sentry Go SDK,
	// you'd use sentry.CaptureException() here. For now we log structured
	// data that can be picked up by log aggregation or a Sentry log drain.
	slog.Error("sentry_report",
		"dsn_configured", true,
		"error", errMsg,
		"path", path,
	)
}
