package api

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/mail"
	"sync"
	"time"

	"github.com/invoiceparser/api/internal/auth"
	"github.com/invoiceparser/api/internal/billing"
	"github.com/invoiceparser/api/internal/db"
	"github.com/invoiceparser/api/internal/parser"
)

type Handler struct {
	db          *db.DB
	parser      *parser.Parser
	stripe      *billing.StripeClient
	maxUploadMB int64
	metrics     *Metrics
}

type batchFileData struct {
	data     []byte
	filename string
}

func NewHandler(database *db.DB, p *parser.Parser, stripe *billing.StripeClient, maxUploadMB int64, metrics *Metrics) *Handler {
	return &Handler{db: database, parser: p, stripe: stripe, maxUploadMB: maxUploadMB, metrics: metrics}
}

func (h *Handler) ParseInvoice(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	requestID := RequestIDFromContext(r.Context())
	ak := APIKeyFromContext(r.Context())
	if ak == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse("unauthorized", "", requestID))
		return
	}

	maxBytes := h.maxUploadMB << 20
	if err := r.ParseMultipartForm(maxBytes); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse("invalid_request",
			"File too large or invalid multipart form. Max size: "+formatMB(h.maxUploadMB)+".", requestID))
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse("invalid_request",
			"Missing 'file' field. Send a PDF or image as multipart form data.", requestID))
		return
	}
	defer file.Close()

	invoice, err := h.parser.Parse(file, header)
	latencyMs := time.Since(start).Milliseconds()

	if err != nil {
		h.metrics.RecordRequest("/v1/parse/invoice", http.StatusInternalServerError, time.Since(start))
		_ = h.db.LogUsage(ak.ID, "/v1/parse/invoice", http.StatusInternalServerError, latencyMs)
		writeJSON(w, http.StatusInternalServerError, errorResponse("parse_error",
			"Failed to parse invoice: "+err.Error(), requestID))
		return
	}

	_ = h.db.IncrementUsage(ak.ID)
	_ = h.db.LogUsage(ak.ID, "/v1/parse/invoice", http.StatusOK, latencyMs)
	h.metrics.RecordRequest("/v1/parse/invoice", http.StatusOK, time.Since(start))

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":    true,
		"data":       invoice,
		"latency_ms": latencyMs,
		"request_id": requestID,
	})
}

// ParseInvoiceURL handles POST /v1/parse/url — parse invoice from a URL
func (h *Handler) ParseInvoiceURL(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	requestID := RequestIDFromContext(r.Context())
	ak := APIKeyFromContext(r.Context())
	if ak == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse("unauthorized", "", requestID))
		return
	}

	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.URL == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse("invalid_request",
			"Provide a 'url' field pointing to a PDF or image.", requestID))
		return
	}

	// Download the file
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(req.URL)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse("download_error",
			"Failed to download file from URL.", requestID))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		writeJSON(w, http.StatusBadRequest, errorResponse("download_error",
			fmt.Sprintf("URL returned status %d.", resp.StatusCode), requestID))
		return
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, h.maxUploadMB<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse("download_error",
			"Failed to read file from URL.", requestID))
		return
	}

	contentType := resp.Header.Get("Content-Type")
	invoice, err := h.parser.ParseBytes(data, guessFilename(req.URL, contentType))
	latencyMs := time.Since(start).Milliseconds()

	if err != nil {
		h.metrics.RecordRequest("/v1/parse/url", http.StatusInternalServerError, time.Since(start))
		_ = h.db.LogUsage(ak.ID, "/v1/parse/url", http.StatusInternalServerError, latencyMs)
		writeJSON(w, http.StatusInternalServerError, errorResponse("parse_error",
			"Failed to parse invoice: "+err.Error(), requestID))
		return
	}

	_ = h.db.IncrementUsage(ak.ID)
	_ = h.db.LogUsage(ak.ID, "/v1/parse/url", http.StatusOK, latencyMs)
	h.metrics.RecordRequest("/v1/parse/url", http.StatusOK, time.Since(start))

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":    true,
		"data":       invoice,
		"latency_ms": latencyMs,
		"request_id": requestID,
	})
}

// BatchParseInvoice handles POST /v1/parse/batch — Pro plan only
func (h *Handler) BatchParseInvoice(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	requestID := RequestIDFromContext(r.Context())
	ak := APIKeyFromContext(r.Context())
	if ak == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse("unauthorized", "", requestID))
		return
	}

	if ak.Plan != "pro" {
		writeJSON(w, http.StatusForbidden, errorResponse("plan_required",
			"Batch processing requires the Pro plan.", requestID))
		return
	}

	maxBytes := h.maxUploadMB << 20
	if err := r.ParseMultipartForm(maxBytes * 10); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse("invalid_request",
			"Request too large or invalid multipart form.", requestID))
		return
	}

	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		writeJSON(w, http.StatusBadRequest, errorResponse("invalid_request",
			"Missing 'files' field. Send multiple files as multipart form data.", requestID))
		return
	}

	if len(files) > 20 {
		writeJSON(w, http.StatusBadRequest, errorResponse("invalid_request",
			"Maximum 20 files per batch request.", requestID))
		return
	}

	remaining := ak.MaxCalls - ak.UsedCalls
	if remaining < int64(len(files)) {
		writeJSON(w, http.StatusTooManyRequests, map[string]interface{}{
			"error":      "rate_limit_exceeded",
			"message":    fmt.Sprintf("Not enough API calls remaining. Need %d, have %d.", len(files), remaining),
			"request_id": requestID,
		})
		return
	}

	webhookURL := r.FormValue("webhook_url")

	// If webhook_url is provided, process asynchronously
	if webhookURL != "" {
		jobID := generateJobID()
		job, err := h.db.CreateBatchJob(jobID, ak.ID, len(files), webhookURL)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse("internal_error",
				"Failed to create batch job", requestID))
			return
		}

		var fileDatas []batchFileData
		for _, fh := range files {
			f, err := fh.Open()
			if err != nil {
				continue
			}
			buf := new(bytes.Buffer)
			if _, err := buf.ReadFrom(f); err != nil {
				f.Close()
				continue
			}
			f.Close()
			fileDatas = append(fileDatas, batchFileData{data: buf.Bytes(), filename: fh.Filename})
		}

		go h.processBatchAsync(job, fileDatas, ak.ID)

		writeJSON(w, http.StatusAccepted, map[string]interface{}{
			"job_id":      jobID,
			"status":      "pending",
			"file_count":  len(files),
			"webhook_url": webhookURL,
			"message":     "Batch job queued. Results will be POSTed to webhook_url.",
			"request_id":  requestID,
		})
		return
	}

	// Synchronous batch processing
	type batchResult struct {
		Index    int         `json:"index"`
		Filename string      `json:"filename"`
		Success  bool        `json:"success"`
		Data     interface{} `json:"data,omitempty"`
		Error    string      `json:"error,omitempty"`
	}

	results := make([]batchResult, len(files))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 5)

	for i := range files {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			fh := files[idx]
			f, err := fh.Open()
			if err != nil {
				results[idx] = batchResult{Index: idx, Filename: fh.Filename, Success: false, Error: "failed to open file"}
				return
			}
			defer f.Close()

			invoice, err := h.parser.Parse(f, fh)
			if err != nil {
				results[idx] = batchResult{Index: idx, Filename: fh.Filename, Success: false, Error: err.Error()}
			} else {
				results[idx] = batchResult{Index: idx, Filename: fh.Filename, Success: true, Data: invoice}
			}
		}(i)
	}
	wg.Wait()

	successCount := 0
	for _, r := range results {
		if r.Success {
			successCount++
		}
	}

	_ = h.db.IncrementUsageBy(ak.ID, successCount)
	latencyMs := time.Since(start).Milliseconds()
	_ = h.db.LogUsage(ak.ID, "/v1/parse/batch", http.StatusOK, latencyMs)
	h.metrics.RecordRequest("/v1/parse/batch", http.StatusOK, time.Since(start))

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":    true,
		"total":      len(files),
		"succeeded":  successCount,
		"failed":     len(files) - successCount,
		"results":    results,
		"latency_ms": latencyMs,
		"request_id": requestID,
	})
}

const maxWebhookRetries = 3

func (h *Handler) processBatchAsync(job *db.BatchJob, files []batchFileData, apiKeyID int64) {
	_ = h.db.UpdateBatchJob(job.ID, "processing", 0, json.RawMessage("[]"))

	type batchResult struct {
		Index    int         `json:"index"`
		Filename string      `json:"filename"`
		Success  bool        `json:"success"`
		Data     interface{} `json:"data,omitempty"`
		Error    string      `json:"error,omitempty"`
	}

	results := make([]batchResult, len(files))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 5)

	for i, f := range files {
		wg.Add(1)
		go func(idx int, fileData batchFileData) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			invoice, err := h.parser.ParseBytes(fileData.data, fileData.filename)
			if err != nil {
				results[idx] = batchResult{Index: idx, Filename: fileData.filename, Success: false, Error: err.Error()}
			} else {
				results[idx] = batchResult{Index: idx, Filename: fileData.filename, Success: true, Data: invoice}
			}
		}(i, f)
	}
	wg.Wait()

	successCount := 0
	for _, r := range results {
		if r.Success {
			successCount++
		}
	}

	_ = h.db.IncrementUsageBy(apiKeyID, successCount)

	resultsJSON, _ := json.Marshal(results)
	_ = h.db.UpdateBatchJob(job.ID, "completed", len(files), resultsJSON)

	// POST results to webhook URL with retry
	if job.WebhookURL != "" {
		payload, _ := json.Marshal(map[string]interface{}{
			"job_id":    job.ID,
			"status":    "completed",
			"total":     len(files),
			"succeeded": successCount,
			"failed":    len(files) - successCount,
			"results":   results,
		})

		deliverWebhook(job.WebhookURL, payload, job.ID)
	}
}

func deliverWebhook(url string, payload []byte, jobID string) {
	client := &http.Client{Timeout: 10 * time.Second}
	for attempt := 0; attempt < maxWebhookRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt)) * time.Second
			slog.Warn("retrying webhook delivery", "job_id", jobID, "attempt", attempt+1, "backoff", backoff)
			time.Sleep(backoff)
		}

		resp, err := client.Post(url, "application/json", bytes.NewReader(payload))
		if err != nil {
			slog.Error("webhook delivery failed", "job_id", jobID, "attempt", attempt+1, "error", err)
			continue
		}
		resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			slog.Info("webhook delivered", "job_id", jobID, "status", resp.StatusCode)
			return
		}
		slog.Warn("webhook returned non-2xx", "job_id", jobID, "status", resp.StatusCode)
	}
	slog.Error("webhook delivery exhausted retries", "job_id", jobID, "url", url)
}

// GetBatchJob handles GET /v1/parse/batch/{id}
func (h *Handler) GetBatchJob(w http.ResponseWriter, r *http.Request) {
	requestID := RequestIDFromContext(r.Context())
	ak := APIKeyFromContext(r.Context())
	if ak == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse("unauthorized", "", requestID))
		return
	}

	jobID := r.PathValue("id")
	if jobID == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse("invalid_request", "Missing job ID.", requestID))
		return
	}

	job, err := h.db.GetBatchJob(jobID, ak.ID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorResponse("not_found", "Batch job not found.", requestID))
		return
	}

	resp := map[string]interface{}{
		"job_id":     job.ID,
		"status":     job.Status,
		"file_count": job.FileCount,
		"completed":  job.Completed,
		"created_at": job.CreatedAt,
		"request_id": requestID,
	}

	if job.Status == "completed" {
		resp["results"] = json.RawMessage(job.Results)
	}

	writeJSON(w, http.StatusOK, resp)
}

// CreateCheckout handles POST /v1/billing/checkout
func (h *Handler) CreateCheckout(w http.ResponseWriter, r *http.Request) {
	requestID := RequestIDFromContext(r.Context())
	ak := APIKeyFromContext(r.Context())
	if ak == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse("unauthorized", "", requestID))
		return
	}

	if h.stripe == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse("billing_unavailable",
			"Billing is not configured.", requestID))
		return
	}

	var req struct {
		Plan       string `json:"plan"`
		SuccessURL string `json:"success_url"`
		CancelURL  string `json:"cancel_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Plan == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse("invalid_request",
			"Provide a plan ('starter' or 'pro'), success_url, and cancel_url.", requestID))
		return
	}

	if req.Plan != "starter" && req.Plan != "pro" {
		writeJSON(w, http.StatusBadRequest, errorResponse("invalid_request",
			"Plan must be 'starter' or 'pro'.", requestID))
		return
	}

	if req.SuccessURL == "" || req.CancelURL == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse("invalid_request",
			"success_url and cancel_url are required.", requestID))
		return
	}

	checkoutURL, err := h.stripe.CreateCheckoutSession(
		ak.Email, req.Plan, fmt.Sprintf("%d", ak.ID), req.SuccessURL, req.CancelURL,
	)
	if err != nil {
		slog.Error("failed to create checkout session", "error", err, "request_id", requestID)
		writeJSON(w, http.StatusInternalServerError, errorResponse("checkout_error",
			"Failed to create checkout session.", requestID))
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"checkout_url": checkoutURL,
		"request_id":   requestID,
	})
}

// Dashboard handlers

func (h *Handler) GetDashboard(w http.ResponseWriter, r *http.Request) {
	requestID := RequestIDFromContext(r.Context())
	ak := APIKeyFromContext(r.Context())
	if ak == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse("unauthorized", "", requestID))
		return
	}

	todayCalls, monthCalls, err := h.db.GetUsageStats(ak.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse("internal_error", "Failed to get stats", requestID))
		return
	}

	dailyUsage, err := h.db.GetDailyUsage(ak.ID, 30)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse("internal_error", "Failed to get daily usage", requestID))
		return
	}

	recentLogs, err := h.db.GetRecentLogs(ak.ID, 50)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse("internal_error", "Failed to get recent logs", requestID))
		return
	}

	type logEntry struct {
		Endpoint  string `json:"endpoint"`
		Status    int    `json:"status"`
		LatencyMs int64  `json:"latency_ms"`
		CreatedAt string `json:"created_at"`
	}
	var logs []logEntry
	for _, l := range recentLogs {
		logs = append(logs, logEntry{
			Endpoint:  l.Endpoint,
			Status:    l.Status,
			LatencyMs: l.LatencyMs,
			CreatedAt: l.CreatedAt.Format(time.RFC3339),
		})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"plan":         ak.Plan,
		"used_calls":   ak.UsedCalls,
		"max_calls":    ak.MaxCalls,
		"today_calls":  todayCalls,
		"month_calls":  monthCalls,
		"daily_usage":  dailyUsage,
		"recent_logs":  logs,
		"member_since": ak.CreatedAt.Format(time.RFC3339),
		"request_id":   requestID,
	})
}

func (h *Handler) GetUsage(w http.ResponseWriter, r *http.Request) {
	requestID := RequestIDFromContext(r.Context())
	ak := APIKeyFromContext(r.Context())
	if ak == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse("unauthorized", "", requestID))
		return
	}

	todayCalls, monthCalls, err := h.db.GetUsageStats(ak.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse("internal_error",
			"Failed to get usage stats", requestID))
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"plan":        ak.Plan,
		"used_calls":  ak.UsedCalls,
		"max_calls":   ak.MaxCalls,
		"today_calls": todayCalls,
		"month_calls": monthCalls,
		"request_id":  requestID,
	})
}

func (h *Handler) RegisterKey(w http.ResponseWriter, r *http.Request) {
	requestID := RequestIDFromContext(r.Context())
	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Email == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse("invalid_request",
			"Provide a valid email address.", requestID))
		return
	}

	if _, err := mail.ParseAddress(req.Email); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse("invalid_request",
			"Invalid email address format.", requestID))
		return
	}

	// Check if email already has a key
	existing, err := h.db.GetAPIKeyByEmail(req.Email)
	if err == nil && existing != nil {
		writeJSON(w, http.StatusConflict, errorResponse("email_exists",
			"This email already has an API key. Use the key rotation endpoint if you need a new key.", requestID))
		return
	}

	key, err := auth.GenerateAPIKey()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse("internal_error",
			"Failed to generate API key", requestID))
		return
	}

	ak, err := h.db.CreateAPIKey(key, req.Email, "free", 100)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse("internal_error",
			"Failed to create API key", requestID))
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"api_key":    ak.Key,
		"plan":       ak.Plan,
		"max_calls":  ak.MaxCalls,
		"message":    "Store this API key securely. It won't be shown again.",
		"request_id": requestID,
	})
}

// RotateKey handles POST /v1/rotate-key — generates a new API key for the authenticated user
func (h *Handler) RotateKey(w http.ResponseWriter, r *http.Request) {
	requestID := RequestIDFromContext(r.Context())
	ak := APIKeyFromContext(r.Context())
	if ak == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse("unauthorized", "", requestID))
		return
	}

	newKey, err := auth.GenerateAPIKey()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse("internal_error",
			"Failed to generate new API key", requestID))
		return
	}

	if err := h.db.RotateAPIKey(ak.ID, newKey); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse("internal_error",
			"Failed to rotate API key", requestID))
		return
	}

	slog.Info("api key rotated", "api_key_id", ak.ID, "email", ak.Email)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"api_key":    newKey,
		"message":    "New API key generated. Old key is now invalid. Store this securely.",
		"request_id": requestID,
	})
}

// ParseDocument handles POST /v1/parse/{type} for all document types
func (h *Handler) ParseDocument(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	requestID := RequestIDFromContext(r.Context())
	ak := APIKeyFromContext(r.Context())
	if ak == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse("unauthorized", "", requestID))
		return
	}

	docType := parser.DocType(r.PathValue("type"))
	validTypes := map[parser.DocType]bool{
		parser.DocTypeReceipt:       true,
		parser.DocTypeBankStatement: true,
		parser.DocTypeContract:      true,
		parser.DocTypeIDDocument:    true,
		parser.DocTypeTaxForm:       true,
		parser.DocTypeBusinessCard:  true,
	}
	if !validTypes[docType] {
		writeJSON(w, http.StatusBadRequest, errorResponse("invalid_request",
			"Unsupported document type. Use: receipt, bank_statement, contract, id_document, tax_form, business_card", requestID))
		return
	}

	maxBytes := h.maxUploadMB << 20
	if err := r.ParseMultipartForm(maxBytes); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse("invalid_request",
			"File too large or invalid multipart form. Max size: "+formatMB(h.maxUploadMB)+".", requestID))
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse("invalid_request",
			"Missing 'file' field. Send a PDF or image as multipart form data.", requestID))
		return
	}
	defer file.Close()

	result, err := h.parser.ParseDocument(docType, file, header)
	latencyMs := time.Since(start).Milliseconds()
	endpoint := "/v1/parse/" + string(docType)

	if err != nil {
		h.metrics.RecordRequest(endpoint, http.StatusInternalServerError, time.Since(start))
		_ = h.db.LogUsage(ak.ID, endpoint, http.StatusInternalServerError, latencyMs)
		writeJSON(w, http.StatusInternalServerError, errorResponse("parse_error",
			"Failed to parse document: "+err.Error(), requestID))
		return
	}

	_ = h.db.IncrementUsage(ak.ID)
	_ = h.db.LogUsage(ak.ID, endpoint, http.StatusOK, latencyMs)
	h.metrics.RecordRequest(endpoint, http.StatusOK, time.Since(start))

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":    true,
		"type":       string(docType),
		"data":       json.RawMessage(result),
		"latency_ms": latencyMs,
		"request_id": requestID,
	})
}

func (h *Handler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "healthy",
		"version": "1.1.0",
	})
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func errorResponse(errCode, message, requestID string) map[string]interface{} {
	resp := map[string]interface{}{
		"error":      errCode,
		"request_id": requestID,
	}
	if message != "" {
		resp["message"] = message
	}
	return resp
}

func formatMB(mb int64) string {
	return fmt.Sprintf("%dMB", mb)
}

func generateJobID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based ID
		return fmt.Sprintf("batch_%d", time.Now().UnixNano())
	}
	return "batch_" + hex.EncodeToString(b)
}

func guessFilename(url, contentType string) string {
	switch {
	case contentType == "application/pdf":
		return "document.pdf"
	case contentType == "image/png":
		return "image.png"
	case contentType == "image/jpeg":
		return "image.jpg"
	case contentType == "image/webp":
		return "image.webp"
	default:
		// Try to guess from URL
		for _, ext := range []string{".pdf", ".png", ".jpg", ".jpeg", ".webp", ".gif"} {
			if len(url) > len(ext) && url[len(url)-len(ext):] == ext {
				return "file" + ext
			}
		}
		return "document.png"
	}
}
