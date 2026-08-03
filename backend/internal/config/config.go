package config

import (
	"fmt"
	"log"
	"os"
	"strings"
)

// jwtSecretBlocklist contains known-weak JWT secrets that must not be used in production.
var jwtSecretBlocklist = []string{
	"change-me-in-production",
	"dev-jwt-secret-change-in-prod",
	"",
}

// jwtSecretMinLength is the minimum acceptable length for JWT_SECRET.
const jwtSecretMinLength = 32

type Config struct {
	Port                    string
	DatabaseURL             string
	S3Endpoint              string
	S3Bucket                string
	S3AccessKey             string
	S3SecretKey             string
	S3UsePathStyle          bool
	GitHubClientID          string
	GitHubClientSecret      string
	GitLabClientID          string
	GitLabClientSecret      string
	HuggingFaceClientID     string
	HuggingFaceClientSecret string
	CodebergClientID        string
	CodebergClientSecret    string
	SourceHutClientID       string
	SourceHutClientSecret   string
	JWTSecret               string
	FrontendURL             string
	BaseURL                 string

	// GitHub App credentials for the collective-repository feature. When either
	// is empty the feature is disabled and its endpoints return 501. The private
	// key is a PEM string (PKCS#1 or PKCS#8); supply it via GITHUB_APP_PRIVATE_KEY
	// (literal multi-line value or one with \n escapes, which we unescape).
	GitHubAppID         string
	GitHubAppPrivateKey string
}

// Narrow S3 accessors let the raw object adapter depend on storage authority,
// not on the application's broad configuration type.
func (c *Config) ObjectStorageEndpoint() string   { return c.S3Endpoint }
func (c *Config) ObjectStorageBucket() string     { return c.S3Bucket }
func (c *Config) ObjectStorageAccessKey() string  { return c.S3AccessKey }
func (c *Config) ObjectStorageSecretKey() string  { return c.S3SecretKey }
func (c *Config) ObjectStorageUsePathStyle() bool { return c.S3UsePathStyle }

// validateJWTSecret returns an error if the provided secret is empty, blocklisted,
// or shorter than jwtSecretMinLength. This function is extracted so that validation
// logic is independently testable without triggering os.Exit.
func validateJWTSecret(secret string) error {
	for _, blocked := range jwtSecretBlocklist {
		if secret == blocked {
			return fmt.Errorf("config: JWT_SECRET is set to a known-weak value %q — set a strong secret of at least %d characters", blocked, jwtSecretMinLength)
		}
	}
	if len(secret) < jwtSecretMinLength {
		return fmt.Errorf("config: JWT_SECRET must be at least %d characters (got %d)", jwtSecretMinLength, len(secret))
	}
	return nil
}

func Load() *Config {
	jwtSecret := os.Getenv("JWT_SECRET")

	if err := validateJWTSecret(jwtSecret); err != nil {
		log.Fatal(err)
	}

	return &Config{
		Port:                    getEnv("PORT", "8080"),
		DatabaseURL:             getEnv("DATABASE_URL", "postgres://peasant:peasant@localhost:5432/peasant?sslmode=require"),
		S3Endpoint:              getEnv("S3_ENDPOINT", "http://localhost:9000"),
		S3Bucket:                getEnv("S3_BUCKET", "peasant-transcripts"),
		S3AccessKey:             getEnv("S3_ACCESS_KEY", "minioadmin"),
		S3SecretKey:             getEnv("S3_SECRET_KEY", "minioadmin"),
		S3UsePathStyle:          getEnv("S3_USE_PATH_STYLE", "true") == "true",
		GitHubClientID:          getEnv("GITHUB_CLIENT_ID", ""),
		GitHubClientSecret:      getEnv("GITHUB_CLIENT_SECRET", ""),
		GitLabClientID:          getEnv("GITLAB_CLIENT_ID", ""),
		GitLabClientSecret:      getEnv("GITLAB_CLIENT_SECRET", ""),
		HuggingFaceClientID:     getEnv("HUGGINGFACE_CLIENT_ID", ""),
		HuggingFaceClientSecret: getEnv("HUGGINGFACE_CLIENT_SECRET", ""),
		CodebergClientID:        getEnv("CODEBERG_CLIENT_ID", ""),
		CodebergClientSecret:    getEnv("CODEBERG_CLIENT_SECRET", ""),
		SourceHutClientID:       getEnv("SOURCEHUT_CLIENT_ID", ""),
		SourceHutClientSecret:   getEnv("SOURCEHUT_CLIENT_SECRET", ""),
		JWTSecret:               jwtSecret,
		FrontendURL:             getEnv("FRONTEND_URL", "https://localhost"),
		BaseURL:                 getEnv("BASE_URL", "https://localhost"),
		GitHubAppID:             getEnv("GITHUB_APP_ID", ""),
		GitHubAppPrivateKey:     normalizePEM(getEnv("GITHUB_APP_PRIVATE_KEY", "")),
		// Default ON: only the explicit string "false" disables it, so an unset or
		// malformed value fails safe to enabled (legacy NULLs still get backfilled).
	}
}

// normalizePEM converts an env-supplied private key that uses literal "\n"
// escapes (common when a multi-line PEM is stored in a single-line secret) back
// into real newlines so the PEM parser accepts it. A value that already
// contains real newlines is returned unchanged.
func normalizePEM(s string) string {
	if s == "" || strings.Contains(s, "\n") {
		return s
	}
	return strings.ReplaceAll(s, `\n`, "\n")
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
