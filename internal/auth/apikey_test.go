package auth

import (
	"strings"
	"testing"
)

func TestGenerateAPIKey(t *testing.T) {
	key, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.HasPrefix(key, "inv_") {
		t.Errorf("key should have prefix 'inv_', got: %s", key)
	}

	// inv_ (4) + 48 hex chars (24 bytes) = 52 total
	if len(key) != 52 {
		t.Errorf("expected key length 52, got %d: %s", len(key), key)
	}
}

func TestGenerateAPIKey_Unique(t *testing.T) {
	key1, _ := GenerateAPIKey()
	key2, _ := GenerateAPIKey()
	if key1 == key2 {
		t.Error("generated keys should be unique")
	}
}
