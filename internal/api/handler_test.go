package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/invoiceparser/api/internal/db"
	"github.com/invoiceparser/api/internal/parser"
)

func setupTestHandler(t *testing.T) (*Handler, *db.DB) {
	t.Helper()
	tmpFile := t.TempDir() + "/test.db"
	database, err := db.New(tmpFile)
	if err != nil {
		t.Fatalf("failed to create test db: %v", err)
	}
	t.Cleanup(func() {
		database.Close()
		os.Remove(tmpFile)
	})

	p := parser.New("fake-key")
	m := NewMetrics()
	h := NewHandler(database, p, nil, 10, m)
	return h, database
}

func TestHealthCheck(t *testing.T) {
	h, _ := setupTestHandler(t)

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	h.HealthCheck(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)

	if resp["status"] != "healthy" {
		t.Errorf("expected status healthy, got %v", resp["status"])
	}
}

func TestRegisterKey_Success(t *testing.T) {
	h, _ := setupTestHandler(t)

	body := bytes.NewBufferString(`{"email": "test@example.com"}`)
	req := httptest.NewRequest("POST", "/v1/register", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.RegisterKey(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)

	if resp["api_key"] == nil || resp["api_key"] == "" {
		t.Error("expected api_key in response")
	}
	if resp["plan"] != "free" {
		t.Errorf("expected plan free, got %v", resp["plan"])
	}
}

func TestRegisterKey_MissingEmail(t *testing.T) {
	h, _ := setupTestHandler(t)

	body := bytes.NewBufferString(`{}`)
	req := httptest.NewRequest("POST", "/v1/register", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.RegisterKey(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestRegisterKey_InvalidEmail(t *testing.T) {
	h, _ := setupTestHandler(t)

	body := bytes.NewBufferString(`{"email": "not-an-email"}`)
	req := httptest.NewRequest("POST", "/v1/register", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.RegisterKey(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestGetUsage_Unauthorized(t *testing.T) {
	h, _ := setupTestHandler(t)

	req := httptest.NewRequest("GET", "/v1/usage", nil)
	w := httptest.NewRecorder()

	h.GetUsage(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestParseInvoice_Unauthorized(t *testing.T) {
	h, _ := setupTestHandler(t)

	req := httptest.NewRequest("POST", "/v1/parse/invoice", nil)
	w := httptest.NewRecorder()

	h.ParseInvoice(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestRegisterKey_DuplicateEmail(t *testing.T) {
	h, _ := setupTestHandler(t)

	// First registration
	body := bytes.NewBufferString(`{"email": "dupe@example.com"}`)
	req := httptest.NewRequest("POST", "/v1/register", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.RegisterKey(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("first registration failed: %d", w.Code)
	}

	// Second registration with same email
	body2 := bytes.NewBufferString(`{"email": "dupe@example.com"}`)
	req2 := httptest.NewRequest("POST", "/v1/register", body2)
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	h.RegisterKey(w2, req2)

	if w2.Code != http.StatusConflict {
		t.Errorf("expected 409 for duplicate email, got %d", w2.Code)
	}
}

func TestRotateKey_Unauthorized(t *testing.T) {
	h, _ := setupTestHandler(t)

	req := httptest.NewRequest("POST", "/v1/rotate-key", nil)
	w := httptest.NewRecorder()
	h.RotateKey(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestParseInvoiceURL_Unauthorized(t *testing.T) {
	h, _ := setupTestHandler(t)

	req := httptest.NewRequest("POST", "/v1/parse/url", nil)
	w := httptest.NewRecorder()
	h.ParseInvoiceURL(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}
