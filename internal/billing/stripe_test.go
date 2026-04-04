package billing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"
)

func TestVerifyStripeSignature_Valid(t *testing.T) {
	secret := "whsec_test_secret"
	payload := []byte(`{"type":"checkout.session.completed"}`)
	timestamp := fmt.Sprintf("%d", time.Now().Unix())

	// Compute valid signature
	signedPayload := timestamp + "." + string(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signedPayload))
	sig := hex.EncodeToString(mac.Sum(nil))

	sigHeader := fmt.Sprintf("t=%s,v1=%s", timestamp, sig)

	if !verifyStripeSignature(payload, sigHeader, secret) {
		t.Error("expected valid signature to pass")
	}
}

func TestVerifyStripeSignature_Invalid(t *testing.T) {
	secret := "whsec_test_secret"
	payload := []byte(`{"type":"checkout.session.completed"}`)
	timestamp := fmt.Sprintf("%d", time.Now().Unix())

	sigHeader := fmt.Sprintf("t=%s,v1=invalidsignature", timestamp)

	if verifyStripeSignature(payload, sigHeader, secret) {
		t.Error("expected invalid signature to fail")
	}
}

func TestVerifyStripeSignature_Expired(t *testing.T) {
	secret := "whsec_test_secret"
	payload := []byte(`{"type":"test"}`)
	// 10 minutes ago
	timestamp := fmt.Sprintf("%d", time.Now().Add(-10*time.Minute).Unix())

	signedPayload := timestamp + "." + string(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signedPayload))
	sig := hex.EncodeToString(mac.Sum(nil))

	sigHeader := fmt.Sprintf("t=%s,v1=%s", timestamp, sig)

	if verifyStripeSignature(payload, sigHeader, secret) {
		t.Error("expected expired timestamp to fail")
	}
}

func TestVerifyStripeSignature_EmptyHeader(t *testing.T) {
	if verifyStripeSignature([]byte("test"), "", "secret") {
		t.Error("expected empty header to fail")
	}
}

func TestVerifyStripeSignature_MalformedHeader(t *testing.T) {
	if verifyStripeSignature([]byte("test"), "garbage", "secret") {
		t.Error("expected malformed header to fail")
	}
}

func TestPlansConfig(t *testing.T) {
	starter, ok := Plans["starter"]
	if !ok {
		t.Fatal("starter plan not found")
	}
	if starter.MaxCalls != 2000 {
		t.Errorf("expected starter max 2000, got %d", starter.MaxCalls)
	}

	pro, ok := Plans["pro"]
	if !ok {
		t.Fatal("pro plan not found")
	}
	if pro.MaxCalls != 10000 {
		t.Errorf("expected pro max 10000, got %d", pro.MaxCalls)
	}
}

func TestNewStripeClient_SetsPriceIDs(t *testing.T) {
	// Reset plans first
	Plans["starter"] = PlanConfig{Name: "Starter", MaxCalls: 2000}
	Plans["pro"] = PlanConfig{Name: "Pro", MaxCalls: 10000}

	_ = NewStripeClient("sk_test", "whsec_test", nil, "price_starter_123", "price_pro_456")

	if Plans["starter"].StripePriceID != "price_starter_123" {
		t.Errorf("expected starter price ID to be set, got %s", Plans["starter"].StripePriceID)
	}
	if Plans["pro"].StripePriceID != "price_pro_456" {
		t.Errorf("expected pro price ID to be set, got %s", Plans["pro"].StripePriceID)
	}

	// Cleanup
	Plans["starter"] = PlanConfig{Name: "Starter", MaxCalls: 2000}
	Plans["pro"] = PlanConfig{Name: "Pro", MaxCalls: 10000}
}
