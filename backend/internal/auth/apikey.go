package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

const APIKeyPrefix = "peasant_"

// GenerateAPIKey creates a new API key and returns (plaintext key, sha256 hash, prefix).
func GenerateAPIKey() (plaintext, hash, prefix string, err error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", "", "", fmt.Errorf("generate random bytes: %w", err)
	}
	plaintext = APIKeyPrefix + hex.EncodeToString(bytes)
	hash = HashAPIKey(plaintext)
	prefix = plaintext[:8]
	return plaintext, hash, prefix, nil
}

// HashAPIKey returns the SHA-256 hex digest of a key.
func HashAPIKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

// IsAPIKey checks whether a token string looks like an API key.
func IsAPIKey(token string) bool {
	return strings.HasPrefix(token, APIKeyPrefix)
}
