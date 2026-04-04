package billing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/invoiceparser/api/internal/db"
)

type StripeClient struct {
	secretKey     string
	webhookSecret string
	baseURL       string
	db            *db.DB
}

type CheckoutResponse struct {
	URL string `json:"url"`
}

type PlanConfig struct {
	Name          string
	MaxCalls      int64
	StripePriceID string
}

var Plans = map[string]PlanConfig{
	"starter": {Name: "Starter", MaxCalls: 2000},
	"pro":     {Name: "Pro", MaxCalls: 10000},
}

func NewStripeClient(secretKey, webhookSecret string, database *db.DB, starterPriceID, proPriceID string) *StripeClient {
	if starterPriceID != "" {
		p := Plans["starter"]
		p.StripePriceID = starterPriceID
		Plans["starter"] = p
	}
	if proPriceID != "" {
		p := Plans["pro"]
		p.StripePriceID = proPriceID
		Plans["pro"] = p
	}

	return &StripeClient{
		secretKey:     secretKey,
		webhookSecret: webhookSecret,
		baseURL:       "https://api.stripe.com/v1",
		db:            database,
	}
}

func (s *StripeClient) CreateCheckoutSession(email, plan, apiKeyID string, successURL, cancelURL string) (string, error) {
	planConfig, ok := Plans[plan]
	if !ok {
		return "", fmt.Errorf("unknown plan: %s", plan)
	}

	if planConfig.StripePriceID == "" {
		return "", fmt.Errorf("stripe price ID not configured for plan: %s", plan)
	}

	data := url.Values{}
	data.Set("mode", "subscription")
	data.Set("customer_email", email)
	data.Set("line_items[0][price]", planConfig.StripePriceID)
	data.Set("line_items[0][quantity]", "1")
	data.Set("success_url", successURL)
	data.Set("cancel_url", cancelURL)
	data.Set("metadata[api_key_id]", apiKeyID)
	data.Set("metadata[plan]", plan)

	req, err := http.NewRequest("POST", s.baseURL+"/checkout/sessions", strings.NewReader(data.Encode()))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(s.secretKey, "")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("stripe error (%d): %s", resp.StatusCode, string(body))
	}

	var session struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &session); err != nil {
		return "", err
	}

	return session.URL, nil
}

func (s *StripeClient) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	// Verify webhook signature if secret is configured
	if s.webhookSecret != "" {
		sigHeader := r.Header.Get("Stripe-Signature")
		if !verifyStripeSignature(body, sigHeader, s.webhookSecret) {
			slog.Warn("stripe webhook signature verification failed")
			http.Error(w, "Invalid signature", http.StatusForbidden)
			return
		}
	}

	var event struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}

	if err := json.Unmarshal(body, &event); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	slog.Info("stripe webhook received", "type", event.Type)

	switch event.Type {
	case "checkout.session.completed":
		s.handleCheckoutComplete(event.Data)
	case "customer.subscription.deleted":
		s.handleSubscriptionCanceled(event.Data)
	}

	w.WriteHeader(http.StatusOK)
}

func (s *StripeClient) handleCheckoutComplete(data json.RawMessage) {
	var obj struct {
		Object struct {
			Metadata struct {
				APIKeyID string `json:"api_key_id"`
				Plan     string `json:"plan"`
			} `json:"metadata"`
			CustomerEmail string `json:"customer_email"`
		} `json:"object"`
	}

	if err := json.Unmarshal(data, &obj); err != nil {
		slog.Error("failed to parse checkout event", "error", err)
		return
	}

	plan := obj.Object.Metadata.Plan
	email := obj.Object.CustomerEmail

	planConfig, ok := Plans[plan]
	if !ok {
		slog.Error("unknown plan in checkout event", "plan", plan)
		return
	}

	ak, err := s.db.GetAPIKeyByEmail(email)
	if err != nil {
		slog.Error("failed to find API key for email", "email", email, "error", err)
		return
	}

	if err := s.db.UpgradePlan(ak.ID, plan, planConfig.MaxCalls); err != nil {
		slog.Error("failed to upgrade plan", "api_key_id", ak.ID, "error", err)
		return
	}

	slog.Info("plan upgraded", "email", email, "plan", plan)
}

func (s *StripeClient) handleSubscriptionCanceled(data json.RawMessage) {
	var obj struct {
		Object struct {
			Metadata struct {
				APIKeyID string `json:"api_key_id"`
			} `json:"metadata"`
			CustomerEmail string `json:"customer_email"`
		} `json:"object"`
	}

	if err := json.Unmarshal(data, &obj); err != nil {
		slog.Error("failed to parse subscription canceled event", "error", err)
		return
	}

	// Find the user and downgrade to free tier
	email := obj.Object.CustomerEmail
	if email == "" {
		slog.Warn("subscription canceled but no email in event")
		return
	}

	ak, err := s.db.GetAPIKeyByEmail(email)
	if err != nil {
		slog.Error("failed to find API key for canceled subscription", "email", email, "error", err)
		return
	}

	if err := s.db.UpgradePlan(ak.ID, "free", 100); err != nil {
		slog.Error("failed to downgrade plan", "api_key_id", ak.ID, "error", err)
		return
	}

	slog.Info("plan downgraded to free", "email", email)
}

// verifyStripeSignature verifies a Stripe webhook signature.
// Stripe sends a Stripe-Signature header with format: t=timestamp,v1=signature
func verifyStripeSignature(payload []byte, sigHeader, secret string) bool {
	if sigHeader == "" {
		return false
	}

	var timestamp string
	var signatures []string

	for _, part := range strings.Split(sigHeader, ",") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "t":
			timestamp = kv[1]
		case "v1":
			signatures = append(signatures, kv[1])
		}
	}

	if timestamp == "" || len(signatures) == 0 {
		return false
	}

	// Reject timestamps older than 5 minutes (replay protection)
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}
	if time.Since(time.Unix(ts, 0)) > 5*time.Minute {
		return false
	}

	// Compute expected signature: HMAC-SHA256(secret, "timestamp.payload")
	signedPayload := timestamp + "." + string(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signedPayload))
	expected := hex.EncodeToString(mac.Sum(nil))

	for _, sig := range signatures {
		if hmac.Equal([]byte(expected), []byte(sig)) {
			return true
		}
	}

	return false
}
