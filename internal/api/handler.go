package api

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
		h.metrics.RecordRequest("/v1/parse/invoice", http.StatusInternalServerError, time.Since(start))
		_ = h.db.LogUsage(ak.ID, "/v1/parse/invoice", http.StatusInternalServerError, latencyMs)
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error":   "parse_error",
			"message": "Failed to parse invoice: " + err.Error(),
		})
		return
	}

	_ = h.db.IncrementUsage(ak.ID)
	_ = h.db.LogUsage(ak.ID, "/v1/parse/invoice", http.StatusOK, latencyMs)
	h.metrics.RecordRequest("/v1/parse/invoice", http.StatusOK, time.Since(start))

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":    true,
		"data":       invoice,
		"latency_ms": latencyMs,
	})
}

// BatchParseInvoice handles POST /v1/parse/batch — Pro plan only
func (h *Handler) BatchParseInvoice(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	ak := APIKeyFromContext(r.Context())
	if ak == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "unauthorized"})
		return
	}

	if ak.Plan != "pro" {
		writeJSON(w, http.StatusForbidden, map[string]interface{}{
			"error":   "plan_required",
			"message": "Batch processing requires the Pro plan.",
		})
		return
	}

	maxBytes := h.maxUploadMB << 20
	if err := r.ParseMultipartForm(maxBytes * 10); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error":   "invalid_request",
			"message": "Request too large or invalid multipart form.",
		})
		return
	}

	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error":   "invalid_request",
			"message": "Missing 'files' field. Send multiple files as multipart form data.",
		})
		return
	}

	if len(files) > 20 {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error":   "invalid_request",
			"message": "Maximum 20 files per batch request.",
		})
		return
	}

	remaining := ak.MaxCalls - ak.UsedCalls
	if remaining < int64(len(files)) {
		writeJSON(w, http.StatusTooManyRequests, map[string]interface{}{
			"error":   "rate_limit_exceeded",
			"message": fmt.Sprintf("Not enough API calls remaining. Need %d, have %d.", len(files), remaining),
		})
		return
	}

	webhookURL := r.FormValue("webhook_url")

	// If webhook_url is provided, process asynchronously
	if webhookURL != "" {
		jobID := generateJobID()
		job, err := h.db.CreateBatchJob(jobID, ak.ID, len(files), webhookURL)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"error": "Failed to create batch job",
			})
			return
		}

		// Read all files into memory before returning (can't access form files after response)
			var fileDatas []batchFileData
		for _, fh := range files {
			f, err := fh.Open()
			if err != nil {
				continue
			}
			buf := new(bytes.Buffer)
			buf.ReadFrom(f)
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
	sem := make(chan struct{}, 5) // concurrency limit

	for i, fh := range files {
		wg.Add(1)
		go func(idx int, fileHeader interface{}) {
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
		}(i, fh)
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
		"success":       true,
		"total":         len(files),
		"succeeded":     successCount,
		"failed":        len(files) - successCount,
		"results":       results,
		"latency_ms":    latencyMs,
	})
}

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

	// POST results to webhook URL
	if job.WebhookURL != "" {
		payload, _ := json.Marshal(map[string]interface{}{
			"job_id":    job.ID,
			"status":    "completed",
			"total":     len(files),
			"succeeded": successCount,
			"failed":    len(files) - successCount,
			"results":   results,
		})

		resp, err := http.Post(job.WebhookURL, "application/json", bytes.NewReader(payload))
		if err != nil {
			slog.Error("webhook delivery failed", "job_id", job.ID, "error", err)
		} else {
			resp.Body.Close()
			slog.Info("webhook delivered", "job_id", job.ID, "status", resp.StatusCode)
		}
	}
}

// GetBatchJob handles GET /v1/parse/batch/{id}
func (h *Handler) GetBatchJob(w http.ResponseWriter, r *http.Request) {
	ak := APIKeyFromContext(r.Context())
	if ak == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "unauthorized"})
		return
	}

	jobID := r.PathValue("id")
	if jobID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error":   "invalid_request",
			"message": "Missing job ID.",
		})
		return
	}

	job, err := h.db.GetBatchJob(jobID, ak.ID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{
			"error":   "not_found",
			"message": "Batch job not found.",
		})
		return
	}

	resp := map[string]interface{}{
		"job_id":     job.ID,
		"status":     job.Status,
		"file_count": job.FileCount,
		"completed":  job.Completed,
		"created_at": job.CreatedAt,
	}

	if job.Status == "completed" {
		resp["results"] = json.RawMessage(job.Results)
	}

	writeJSON(w, http.StatusOK, resp)
}

// CreateCheckout handles POST /v1/billing/checkout
func (h *Handler) CreateCheckout(w http.ResponseWriter, r *http.Request) {
	ak := APIKeyFromContext(r.Context())
	if ak == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "unauthorized"})
		return
	}

	if h.stripe == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"error":   "billing_unavailable",
			"message": "Billing is not configured.",
		})
		return
	}

	var req struct {
		Plan       string `json:"plan"`
		SuccessURL string `json:"success_url"`
		CancelURL  string `json:"cancel_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Plan == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error":   "invalid_request",
			"message": "Provide a plan ('starter' or 'pro'), success_url, and cancel_url.",
		})
		return
	}

	if req.Plan != "starter" && req.Plan != "pro" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error":   "invalid_request",
			"message": "Plan must be 'starter' or 'pro'.",
		})
		return
	}

	if req.SuccessURL == "" || req.CancelURL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error":   "invalid_request",
			"message": "success_url and cancel_url are required.",
		})
		return
	}

	checkoutURL, err := h.stripe.CreateCheckoutSession(
		ak.Email,
		req.Plan,
		fmt.Sprintf("%d", ak.ID),
		req.SuccessURL,
		req.CancelURL,
	)
	if err != nil {
		slog.Error("failed to create checkout session", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error":   "checkout_error",
			"message": "Failed to create checkout session.",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"checkout_url": checkoutURL,
	})
}

// Dashboard handlers

func (h *Handler) GetDashboard(w http.ResponseWriter, r *http.Request) {
	ak := APIKeyFromContext(r.Context())
	if ak == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "unauthorized"})
		return
	}

	todayCalls, monthCalls, err := h.db.GetUsageStats(ak.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "Failed to get stats"})
		return
	}

	dailyUsage, err := h.db.GetDailyUsage(ak.ID, 30)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "Failed to get daily usage"})
		return
	}

	recentLogs, err := h.db.GetRecentLogs(ak.ID, 50)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "Failed to get recent logs"})
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

func generateJobID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return "batch_" + hex.EncodeToString(b)
}
