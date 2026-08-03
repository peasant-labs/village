package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// AuthorityRequirements is a closed description of the credentials a runtime
// mode must obtain before it starts work.
type AuthorityRequirements uint8

const (
	PostgreSQLAuthority AuthorityRequirements = iota + 1
	ServingAuthority
	BlobProcessingAuthority
)

// Authority contains only the validated capabilities requested by the caller.
// Keyring is nil when transcript decryption authority was not requested.
type Authority struct {
	Config  *Config
	Keyring *TranscriptKeyring
}

func (r AuthorityRequirements) requiresJWT() bool { return r == ServingAuthority }
func (r AuthorityRequirements) requiresBlobs() bool {
	return r == ServingAuthority || r == BlobProcessingAuthority
}

// LoadAuthority reads and validates exactly the authority selected after mode
// parsing. It never terminates the process, allowing the runtime to report the
// startup failure before creating listeners or jobs.
func LoadAuthority(requirements AuthorityRequirements) (*Authority, error) {
	if requirements != PostgreSQLAuthority && requirements != ServingAuthority && requirements != BlobProcessingAuthority {
		return nil, fmt.Errorf("authority loading failed because requirement value %d is unknown in config.LoadAuthority during post-mode startup; no production capability can be safely initialized; select PostgreSQLAuthority, ServingAuthority, or BlobProcessingAuthority", requirements)
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if err := validateRequiredURL("DATABASE_URL", databaseURL); err != nil {
		return nil, err
	}
	cfg := &Config{DatabaseURL: databaseURL}
	if requirements.requiresJWT() {
		loadServingConfiguration(cfg)
		jwt := os.Getenv("JWT_SECRET")
		if err := validateJWTSecret(jwt); err != nil {
			return nil, fmt.Errorf("serving authority loading failed because JWT_SECRET did not satisfy signing requirements in config.LoadAuthority during post-mode startup; the HTTP listener cannot authenticate callers; configure a unique secret of at least %d characters and retry: %w", jwtSecretMinLength, err)
		}
		cfg.JWTSecret = jwt
	}

	authority := &Authority{Config: cfg}
	if !requirements.requiresBlobs() {
		return authority, nil
	}

	endpoint := os.Getenv("S3_ENDPOINT")
	if err := validateRequiredURL("S3_ENDPOINT", endpoint); err != nil {
		return nil, err
	}
	bucket := strings.TrimSpace(os.Getenv("S3_BUCKET"))
	accessKey := os.Getenv("S3_ACCESS_KEY")
	secretKey := os.Getenv("S3_SECRET_KEY")
	if bucket == "" || accessKey == "" || secretKey == "" {
		return nil, fmt.Errorf("blob authority loading failed because S3_BUCKET, S3_ACCESS_KEY, or S3_SECRET_KEY is missing in config.LoadAuthority during post-mode startup; encrypted transcript storage cannot start; configure all named S3 settings and retry without logging their values")
	}
	pathStyle, err := strconv.ParseBool(os.Getenv("S3_USE_PATH_STYLE"))
	if err != nil {
		return nil, fmt.Errorf("blob authority loading failed because S3_USE_PATH_STYLE is not a boolean in config.LoadAuthority during post-mode startup; encrypted transcript storage cannot start; set the named setting to true or false and retry")
	}
	keyring, err := ParseTranscriptKeyring(os.Getenv("TRANSCRIPT_KEK_ACTIVE_VERSION"), os.Getenv("TRANSCRIPT_KEK_KEYRING"))
	if err != nil {
		return nil, err
	}
	cfg.S3Endpoint, cfg.S3Bucket = endpoint, bucket
	cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3UsePathStyle = accessKey, secretKey, pathStyle
	authority.Keyring = keyring
	return authority, nil
}

func loadServingConfiguration(cfg *Config) {
	cfg.Port = getEnv("PORT", "8080")
	cfg.FrontendURL = getEnv("FRONTEND_URL", "https://localhost")
	cfg.BaseURL = getEnv("BASE_URL", "https://localhost")
	cfg.GitHubClientID = getEnv("GITHUB_CLIENT_ID", "")
	cfg.GitHubClientSecret = getEnv("GITHUB_CLIENT_SECRET", "")
	cfg.GitLabClientID = getEnv("GITLAB_CLIENT_ID", "")
	cfg.GitLabClientSecret = getEnv("GITLAB_CLIENT_SECRET", "")
	cfg.HuggingFaceClientID = getEnv("HUGGINGFACE_CLIENT_ID", "")
	cfg.HuggingFaceClientSecret = getEnv("HUGGINGFACE_CLIENT_SECRET", "")
	cfg.CodebergClientID = getEnv("CODEBERG_CLIENT_ID", "")
	cfg.CodebergClientSecret = getEnv("CODEBERG_CLIENT_SECRET", "")
	cfg.SourceHutClientID = getEnv("SOURCEHUT_CLIENT_ID", "")
	cfg.SourceHutClientSecret = getEnv("SOURCEHUT_CLIENT_SECRET", "")
	cfg.GitHubAppID = getEnv("GITHUB_APP_ID", "")
	cfg.GitHubAppPrivateKey = normalizePEM(getEnv("GITHUB_APP_PRIVATE_KEY", ""))
}

func validateRequiredURL(setting, value string) error {
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("authority loading failed because %s is missing or is not an absolute URL in config.LoadAuthority during post-mode startup; the requested production capability cannot connect; configure the named setting with an absolute connection URL and retry", setting)
	}
	return nil
}
