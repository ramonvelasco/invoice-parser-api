package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

const keyPrefix = "inv_"

func GenerateAPIKey() (string, error) {
	bytes := make([]byte, 24)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generating random bytes: %w", err)
	}
	return keyPrefix + hex.EncodeToString(bytes), nil
}
