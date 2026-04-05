package db

import (
	"os"
	"testing"
)

func setupTestDB(t *testing.T) *DB {
	t.Helper()
	tmpFile := t.TempDir() + "/test.db"
	db, err := New(tmpFile)
	if err != nil {
		t.Fatalf("failed to create test db: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
		os.Remove(tmpFile)
	})
	return db
}

func TestCreateAndGetAPIKey(t *testing.T) {
	db := setupTestDB(t)

	ak, err := db.CreateAPIKey("inv_test123", "test@example.com", "free", 100)
	if err != nil {
		t.Fatalf("CreateAPIKey failed: %v", err)
	}

	if ak.Key != "inv_test123" {
		t.Errorf("expected key inv_test123, got %s", ak.Key)
	}
	if ak.Email != "test@example.com" {
		t.Errorf("expected email test@example.com, got %s", ak.Email)
	}
	if ak.Plan != "free" {
		t.Errorf("expected plan free, got %s", ak.Plan)
	}
	if ak.MaxCalls != 100 {
		t.Errorf("expected max_calls 100, got %d", ak.MaxCalls)
	}

	// Fetch it back
	fetched, err := db.GetAPIKey("inv_test123")
	if err != nil {
		t.Fatalf("GetAPIKey failed: %v", err)
	}
	if fetched.ID != ak.ID {
		t.Errorf("ID mismatch: %d vs %d", fetched.ID, ak.ID)
	}
	if fetched.Email != "test@example.com" {
		t.Errorf("email mismatch: %s", fetched.Email)
	}
}

func TestGetAPIKey_NotFound(t *testing.T) {
	db := setupTestDB(t)

	_, err := db.GetAPIKey("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent key")
	}
}

func TestIncrementUsage(t *testing.T) {
	db := setupTestDB(t)

	ak, _ := db.CreateAPIKey("inv_inc", "inc@test.com", "free", 100)

	if err := db.IncrementUsage(ak.ID); err != nil {
		t.Fatalf("IncrementUsage failed: %v", err)
	}

	fetched, _ := db.GetAPIKey("inv_inc")
	if fetched.UsedCalls != 1 {
		t.Errorf("expected used_calls 1, got %d", fetched.UsedCalls)
	}

	// Increment again
	db.IncrementUsage(ak.ID)
	fetched, _ = db.GetAPIKey("inv_inc")
	if fetched.UsedCalls != 2 {
		t.Errorf("expected used_calls 2, got %d", fetched.UsedCalls)
	}
}

func TestLogUsageAndStats(t *testing.T) {
	db := setupTestDB(t)

	ak, _ := db.CreateAPIKey("inv_log", "log@test.com", "free", 100)

	if err := db.LogUsage(ak.ID, "/v1/parse/invoice", 200, 1500); err != nil {
		t.Fatalf("LogUsage failed: %v", err)
	}
	db.LogUsage(ak.ID, "/v1/parse/invoice", 200, 2000)

	today, month, err := db.GetUsageStats(ak.ID)
	if err != nil {
		t.Fatalf("GetUsageStats failed: %v", err)
	}

	if today != 2 {
		t.Errorf("expected 2 today calls, got %d", today)
	}
	if month != 2 {
		t.Errorf("expected 2 month calls, got %d", month)
	}
}

func TestGetAPIKeyByEmail(t *testing.T) {
	db := setupTestDB(t)

	db.CreateAPIKey("inv_email1", "email@test.com", "free", 100)

	ak, err := db.GetAPIKeyByEmail("email@test.com")
	if err != nil {
		t.Fatalf("GetAPIKeyByEmail failed: %v", err)
	}
	if ak.Key != "inv_email1" {
		t.Errorf("expected key inv_email1, got %s", ak.Key)
	}
}

func TestUpgradePlan(t *testing.T) {
	db := setupTestDB(t)

	ak, _ := db.CreateAPIKey("inv_upgrade", "upgrade@test.com", "free", 100)
	db.IncrementUsage(ak.ID)

	if err := db.UpgradePlan(ak.ID, "starter", 2000); err != nil {
		t.Fatalf("UpgradePlan failed: %v", err)
	}

	fetched, _ := db.GetAPIKey("inv_upgrade")
	if fetched.Plan != "starter" {
		t.Errorf("expected plan starter, got %s", fetched.Plan)
	}
	if fetched.MaxCalls != 2000 {
		t.Errorf("expected max_calls 2000, got %d", fetched.MaxCalls)
	}
	if fetched.UsedCalls != 0 {
		t.Errorf("expected used_calls reset to 0, got %d", fetched.UsedCalls)
	}
}

func TestRotateAPIKey(t *testing.T) {
	db := setupTestDB(t)

	ak, _ := db.CreateAPIKey("inv_old_key", "rotate@test.com", "free", 100)

	if err := db.RotateAPIKey(ak.ID, "inv_new_key"); err != nil {
		t.Fatalf("RotateAPIKey failed: %v", err)
	}

	// Old key should not work
	_, err := db.GetAPIKey("inv_old_key")
	if err == nil {
		t.Error("old key should no longer be found")
	}

	// New key should work
	fetched, err := db.GetAPIKey("inv_new_key")
	if err != nil {
		t.Fatalf("new key not found: %v", err)
	}
	if fetched.Email != "rotate@test.com" {
		t.Errorf("email mismatch: %s", fetched.Email)
	}
}

func TestResetMonthlyUsage(t *testing.T) {
	db := setupTestDB(t)

	ak1, _ := db.CreateAPIKey("inv_reset1", "r1@test.com", "free", 100)
	ak2, _ := db.CreateAPIKey("inv_reset2", "r2@test.com", "starter", 2000)

	db.IncrementUsage(ak1.ID)
	db.IncrementUsage(ak1.ID)
	db.IncrementUsage(ak2.ID)

	if err := db.ResetMonthlyUsage(); err != nil {
		t.Fatalf("ResetMonthlyUsage failed: %v", err)
	}

	f1, _ := db.GetAPIKey("inv_reset1")
	f2, _ := db.GetAPIKey("inv_reset2")

	if f1.UsedCalls != 0 {
		t.Errorf("expected 0 used_calls after reset, got %d", f1.UsedCalls)
	}
	if f2.UsedCalls != 0 {
		t.Errorf("expected 0 used_calls after reset, got %d", f2.UsedCalls)
	}
}
