package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/mail"
	"time"

	"github.com/invoiceparser/api/internal/auth"
	"github.com/invoiceparser/api/internal/db"
	"github.com/invoiceparser/api/internal/parser"
)

type Handler struct {
	db          *db.DB
	parser      *parser.Parser
	maxUploadMB int64
}

func NewHandler(database *db.DB, p *parser.Parser, maxUploadMB int64) *Handler {
	return &Handler{db: database, parser: p, maxUploadMB: maxUploadMB}
}

func (h *Handler) ParseInvoice(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	ak := APIKeyFromContext(r.Context())
	if ak == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
			"error": "unauthorized",
		})
		return
	}

	maxBytes := h.maxUploadMB << 20
	if err := r.ParseMultipartForm(maxBytes); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error":   "invalid_request",
			"message": "File too large or invalid multipart form. Max size: " + formatMB(h.maxUploadMB) + ".",
		})
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error":   "invalid_request",
			"message": "Missing 'file' field. Send a PDF or image as multipart form data.",
		})
		return
	}
	defer file.Close()

	invoice, err := h.parser.Parse(file, header)
	latencyMs := time.Since(start).Milliseconds()

	if err != nil {
		_ = h.db.LogUsage(ak.ID, "/v1/parse/invoice", http.StatusInternalServerError, latencyMs)
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error":   "parse_error",
			"message": "Failed to parse invoice: " + err.Error(),
		})
		return
	}

	_ = h.db.IncrementUsage(ak.ID)
	_ = h.db.LogUsage(ak.ID, "/v1/parse/invoice", http.StatusOK, latencyMs)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":    true,
		"data":       invoice,
		"latency_ms": latencyMs,
	})
}

func (h *Handler) GetUsage(w http.ResponseWriter, r *http.Request) {
	ak := APIKeyFromContext(r.Context())
	if ak == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "unauthorized"})
		return
	}

	todayCalls, monthCalls, err := h.db.GetUsageStats(ak.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "Failed to get usage stats",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"plan":        ak.Plan,
		"used_calls":  ak.UsedCalls,
		"max_calls":   ak.MaxCalls,
		"today_calls": todayCalls,
		"month_calls": monthCalls,
	})
}

func (h *Handler) RegisterKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Email == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error":   "invalid_request",
			"message": "Provide a valid email address.",
		})
		return
	}

	// Validate email format
	if _, err := mail.ParseAddress(req.Email); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error":   "invalid_request",
			"message": "Invalid email address format.",
		})
		return
	}

	key, err := auth.GenerateAPIKey()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "Failed to generate API key",
		})
		return
	}

	ak, err := h.db.CreateAPIKey(key, req.Email, "free", 100)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "Failed to create API key",
		})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"api_key":   ak.Key,
		"plan":      ak.Plan,
		"max_calls": ak.MaxCalls,
		"message":   "Store this API key securely. It won't be shown again.",
	})
}

func (h *Handler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "healthy",
		"version": "1.0.0",
	})
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func formatMB(mb int64) string {
	return fmt.Sprintf("%dMB", mb)
}
