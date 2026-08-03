package config

import (
	"bytes"
	_ "embed"
	"io"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

//go:embed testdata/authority/cases.yaml
var authorityCasesYAML []byte

type authorityCase struct {
	Name                string `yaml:"name"`
	Requirement         string `yaml:"requirement"`
	Omit                string `yaml:"omit"`
	Valid               bool   `yaml:"valid"`
	ExpectJWT           bool   `yaml:"expect_jwt"`
	ExpectBlobs         bool   `yaml:"expect_blobs"`
	ExpectServingConfig bool   `yaml:"expect_serving_config"`
	Error               string `yaml:"error"`
}

func loadAuthorityCases(t *testing.T) []authorityCase {
	t.Helper()
	dec := yaml.NewDecoder(bytes.NewReader(authorityCasesYAML))
	dec.KnownFields(true)
	var cases []authorityCase
	if err := dec.Decode(&cases); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		t.Fatalf("authority fixture trailing document: %v", err)
	}
	if len(cases) != 9 {
		t.Fatalf("authority fixture rows=%d, want 9", len(cases))
	}
	return cases
}

func TestLoadAuthorityFixture(t *testing.T) {
	for _, tc := range loadAuthorityCases(t) {
		t.Run(tc.Name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", "postgres://user:password@localhost:5432/village")
			t.Setenv("JWT_SECRET", strings.Repeat("j", jwtSecretMinLength))
			t.Setenv("S3_ENDPOINT", "http://localhost:9000")
			t.Setenv("S3_BUCKET", "transcripts")
			t.Setenv("S3_ACCESS_KEY", "access")
			t.Setenv("S3_SECRET_KEY", "secret")
			t.Setenv("S3_USE_PATH_STYLE", "true")
			t.Setenv("TRANSCRIPT_KEK_ACTIVE_VERSION", "1")
			t.Setenv("TRANSCRIPT_KEK_KEYRING", `{"1":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="}`)
			t.Setenv("PORT", "9443")
			t.Setenv("BASE_URL", "https://api.example.test")
			t.Setenv("FRONTEND_URL", "https://app.example.test")
			t.Setenv("GITHUB_CLIENT_ID", "github-client")
			t.Setenv("GITHUB_CLIENT_SECRET", "github-secret")
			t.Setenv("GITLAB_CLIENT_ID", "gitlab-client")
			t.Setenv("GITLAB_CLIENT_SECRET", "gitlab-secret")
			t.Setenv("HUGGINGFACE_CLIENT_ID", "huggingface-client")
			t.Setenv("HUGGINGFACE_CLIENT_SECRET", "huggingface-secret")
			t.Setenv("CODEBERG_CLIENT_ID", "codeberg-client")
			t.Setenv("CODEBERG_CLIENT_SECRET", "codeberg-secret")
			t.Setenv("SOURCEHUT_CLIENT_ID", "sourcehut-client")
			t.Setenv("SOURCEHUT_CLIENT_SECRET", "sourcehut-secret")
			t.Setenv("GITHUB_APP_ID", "123")
			t.Setenv("GITHUB_APP_PRIVATE_KEY", `line-one\nline-two`)
			switch tc.Omit {
			case "database":
				t.Setenv("DATABASE_URL", "")
			case "jwt":
				t.Setenv("JWT_SECRET", "")
			case "s3":
				t.Setenv("S3_BUCKET", "")
			case "kek":
				t.Setenv("TRANSCRIPT_KEK_ACTIVE_VERSION", "")
			}
			requirement := AuthorityRequirements(255)
			switch tc.Requirement {
			case "postgresql":
				requirement = PostgreSQLAuthority
			case "serving":
				requirement = ServingAuthority
			case "blobs":
				requirement = BlobProcessingAuthority
			}
			authority, err := LoadAuthority(requirement)
			if !tc.Valid {
				if err == nil || !strings.Contains(err.Error(), tc.Error) {
					t.Fatalf("error=%v, want %q", err, tc.Error)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if (authority.Config.JWTSecret != "") != tc.ExpectJWT || (authority.Keyring != nil) != tc.ExpectBlobs || (authority.Config.S3Endpoint != "") != tc.ExpectBlobs {
				t.Fatalf("loaded excess or missing authority: %+v keyring=%v", authority.Config, authority.Keyring != nil)
			}
			servingComplete := authority.Config.Port == "9443" && authority.Config.BaseURL == "https://api.example.test" && authority.Config.FrontendURL == "https://app.example.test" && authority.Config.GitHubClientID == "github-client" && authority.Config.GitHubClientSecret == "github-secret" && authority.Config.GitLabClientID == "gitlab-client" && authority.Config.GitLabClientSecret == "gitlab-secret" && authority.Config.HuggingFaceClientID == "huggingface-client" && authority.Config.HuggingFaceClientSecret == "huggingface-secret" && authority.Config.CodebergClientID == "codeberg-client" && authority.Config.CodebergClientSecret == "codeberg-secret" && authority.Config.SourceHutClientID == "sourcehut-client" && authority.Config.SourceHutClientSecret == "sourcehut-secret" && authority.Config.GitHubAppID == "123" && authority.Config.GitHubAppPrivateKey == "line-one\nline-two"
			if servingComplete != tc.ExpectServingConfig {
				t.Fatalf("serving configuration completeness=%v, want %v: %+v", servingComplete, tc.ExpectServingConfig, authority.Config)
			}
			if !tc.ExpectServingConfig && (authority.Config.Port != "" || authority.Config.BaseURL != "" || authority.Config.GitHubClientID != "") {
				t.Fatalf("non-serving mode received serving authority: %+v", authority.Config)
			}
		})
	}
}
