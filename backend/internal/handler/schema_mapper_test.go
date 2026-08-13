package handler

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/schema"
)

func TestExtractRepoName(t *testing.T) {
	tests := []struct {
		name        string
		projectName string
		gitRemote   string
		want        string
	}{
		{
			name:        "git remote URL",
			projectName: "-Users-pigeonzow-Documents-GitHub-Neurondle",
			gitRemote:   "github.com/pigeonzow/Neurondle.git",
			want:        "Neurondle",
		},
		{
			name:        "git remote URL without .git",
			projectName: "",
			gitRemote:   "github.com/user/my-project",
			want:        "my-project",
		},
		{
			name:        "fallback to project name last segment",
			projectName: "-Users-pigeonzow-Documents-GitHub-Neurondle",
			gitRemote:   "",
			want:        "Neurondle",
		},
		{
			name:        "project name with slashes",
			projectName: "Users/pigeonzow/Documents/GitHub/data-leverage-village",
			gitRemote:   "",
			want:        "data-leverage-village",
		},
		{
			name:        "dash-delimited multi-word project",
			projectName: "-Users-pigeonzow-Documents-GitHub-data-leverage-village",
			gitRemote:   "",
			want:        "data-leverage-village",
		},
		{
			name:        "dash-delimited with v2 suffix",
			projectName: "-Users-pigeonzow-Documents-GitHub-Neurondle-v2",
			gitRemote:   "",
			want:        "Neurondle-v2",
		},
		{
			name:        "dash-delimited causal-abm-steering",
			projectName: "-Users-pigeonzow-Documents-GitHub-causal-abm-steering",
			gitRemote:   "",
			want:        "causal-abm-steering",
		},
		{
			name:        "simple project name",
			projectName: "myproject",
			gitRemote:   "",
			want:        "myproject",
		},
		{
			name:        "empty inputs",
			projectName: "",
			gitRemote:   "",
			want:        "",
		},
		{
			name:        "git remote preferred over project name",
			projectName: "some-long-path-Thing",
			gitRemote:   "https://github.com/user/RealName.git",
			want:        "RealName",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractRepoName(tt.projectName, tt.gitRemote)
			if got != tt.want {
				t.Errorf("extractRepoName(%q, %q) = %q, want %q", tt.projectName, tt.gitRemote, got, tt.want)
			}
		})
	}
}

func TestFormatModelShort(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		model    string
		want     string
	}{
		{
			name:     "claude opus with date suffix",
			provider: "ANTHROPIC",
			model:    "claude-opus-4-5-20251101",
			want:     "Claude Opus 4.5",
		},
		{
			name:     "claude sonnet without date suffix",
			provider: "ANTHROPIC",
			model:    "claude-sonnet-4-6",
			want:     "Claude Sonnet 4.6",
		},
		{
			name:     "claude haiku with date",
			provider: "ANTHROPIC",
			model:    "claude-haiku-4-5-20251001",
			want:     "Claude Haiku 4.5",
		},
		{
			name:     "gpt model",
			provider: "openai",
			model:    "gpt-4-turbo",
			want:     "Gpt 4 Turbo",
		},
		{
			name:     "empty model uses provider",
			provider: "ANTHROPIC",
			model:    "",
			want:     "Anthropic",
		},
		{
			name:     "both empty",
			provider: "",
			model:    "",
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatModelShort(tt.provider, tt.model)
			if got != tt.want {
				t.Errorf("formatModelShort(%q, %q) = %q, want %q", tt.provider, tt.model, got, tt.want)
			}
		})
	}
}

func TestDeriveTitle(t *testing.T) {
	strPtr := func(s string) *string { return &s }

	tests := []struct {
		name string
		req  schema.PublishRequest
		want pgtype.Text
	}{
		{
			name: "uses title_generated when available",
			req: schema.PublishRequest{
				Quality: &schema.QualityMetrics{
					TitleGenerated: strPtr("Fix authentication bug"),
				},
			},
			want: pgtype.Text{String: "Fix authentication bug", Valid: true},
		},
		{
			name: "derives from project and model",
			req: schema.PublishRequest{
				Model: schema.ModelInfo{
					Harness: schema.HarnessClaudeCode,
					Model:   "claude-opus-4-5-20251101",
				},
				Project: schema.ProjectContext{
					Name: "-Users-pigeonzow-Documents-GitHub-Neurondle",
				},
				Git: schema.GitContext{
					Remote: strPtr("github.com/pigeonzow/Neurondle.git"),
				},
			},
			want: pgtype.Text{String: "Neurondle - Claude Opus 4.5 session", Valid: true},
		},
		{
			name: "project only, no model",
			req: schema.PublishRequest{
				Project: schema.ProjectContext{
					Name: "-Users-me-code-MyApp",
				},
			},
			want: pgtype.Text{String: "MyApp session", Valid: true},
		},
		{
			name: "model only, no project",
			req: schema.PublishRequest{
				Model: schema.ModelInfo{
					Harness: schema.HarnessClaudeCode,
					Model:   "claude-sonnet-4-6",
				},
			},
			want: pgtype.Text{String: "Claude Sonnet 4.6 session", Valid: true},
		},
		{
			name: "empty title_generated falls through",
			req: schema.PublishRequest{
				Quality: &schema.QualityMetrics{
					TitleGenerated: strPtr(""),
				},
				Model: schema.ModelInfo{
					Harness: schema.HarnessClaudeCode,
					Model:   "claude-opus-4-5-20251101",
				},
			},
			want: pgtype.Text{String: "Claude Opus 4.5 session", Valid: true},
		},
		{
			name: "no info returns invalid",
			req:  schema.PublishRequest{},
			want: pgtype.Text{Valid: false},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deriveTitle(tt.req)
			if got.Valid != tt.want.Valid || got.String != tt.want.String {
				t.Errorf("deriveTitle() = {%q, %v}, want {%q, %v}", got.String, got.Valid, tt.want.String, tt.want.Valid)
			}
		})
	}
}
