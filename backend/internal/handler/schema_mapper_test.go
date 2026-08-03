package handler

import (
	_ "embed"
	"testing"

	"github.com/peasant-labs/schema"
)

//go:embed testdata/schema_mapping/repository_names.yaml
var repositoryNameFixtureYAML []byte

//go:embed testdata/schema_mapping/model_names.yaml
var modelNameFixtureYAML []byte

//go:embed testdata/schema_mapping/titles.yaml
var titleFixtureYAML []byte

type repositoryNameFixture struct {
	Name        string `yaml:"name"`
	ProjectName string `yaml:"project_name"`
	GitRemote   string `yaml:"git_remote"`
	Expected    string `yaml:"expected"`
}

type modelNameFixture struct {
	Name     string `yaml:"name"`
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
	Expected string `yaml:"expected"`
}

type titleFixture struct {
	Name                  string         `yaml:"name"`
	GeneratedTitle        string         `yaml:"generated_title"`
	GeneratedTitlePresent bool           `yaml:"generated_title_present"`
	Harness               schema.Harness `yaml:"harness"`
	Model                 string         `yaml:"model"`
	ProjectName           string         `yaml:"project_name"`
	GitRemote             string         `yaml:"git_remote"`
	Expected              string         `yaml:"expected"`
	ExpectedValid         bool           `yaml:"expected_valid"`
}

func TestExtractRepoName(t *testing.T) {
	tests := loadFixtureRows[repositoryNameFixture](t, repositoryNameFixtureYAML, 10)
	for _, tt := range tests {
		t.Run(tt.Name, func(t *testing.T) {
			got := extractRepoName(tt.ProjectName, tt.GitRemote)
			if got != tt.Expected {
				t.Errorf("extractRepoName(%q, %q) = %q, want %q", tt.ProjectName, tt.GitRemote, got, tt.Expected)
			}
		})
	}
}

func TestFormatModelShort(t *testing.T) {
	tests := loadFixtureRows[modelNameFixture](t, modelNameFixtureYAML, 6)
	for _, tt := range tests {
		t.Run(tt.Name, func(t *testing.T) {
			got := formatModelShort(tt.Provider, tt.Model)
			if got != tt.Expected {
				t.Errorf("formatModelShort(%q, %q) = %q, want %q", tt.Provider, tt.Model, got, tt.Expected)
			}
		})
	}
}

func TestDeriveTitle(t *testing.T) {
	tests := loadFixtureRows[titleFixture](t, titleFixtureYAML, 6)
	for _, tt := range tests {
		t.Run(tt.Name, func(t *testing.T) {
			request := schema.PublishRequest{
				Model:   schema.ModelInfo{Harness: tt.Harness, Model: schema.ModelID(tt.Model)},
				Project: schema.ProjectContext{Name: tt.ProjectName},
			}
			if tt.GitRemote != "" {
				request.Git.Remote = &tt.GitRemote
			}
			if tt.GeneratedTitlePresent {
				request.Quality = &schema.QualityMetrics{TitleGenerated: &tt.GeneratedTitle}
			}

			got := deriveTitle(request)
			if got.Valid != tt.ExpectedValid || got.String != tt.Expected {
				t.Errorf("deriveTitle() = {%q, %v}, want {%q, %v}", got.String, got.Valid, tt.Expected, tt.ExpectedValid)
			}
		})
	}
}
