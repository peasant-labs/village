package storage

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/peasant-labs/village/backend/internal/config"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/s3_constructor/cases.yaml
var s3ConstructorCasesYAML []byte

type s3ConstructorCase struct {
	Name   string `yaml:"name"`
	Loader string `yaml:"loader"`
	Valid  bool   `yaml:"valid"`
	Error  string `yaml:"error"`
}

func loadS3ConstructorCases(t *testing.T) []s3ConstructorCase {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewReader(s3ConstructorCasesYAML))
	decoder.KnownFields(true)
	var cases []s3ConstructorCase
	if err := decoder.Decode(&cases); err != nil {
		t.Fatal(err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("S3 constructor fixture trailing document: %v", err)
	}
	if len(cases) != 2 {
		t.Fatalf("S3 constructor fixture rows=%d, want 2", len(cases))
	}
	return cases
}

func TestNewS3ObjectStoreFixture(t *testing.T) {
	for _, tc := range loadS3ConstructorCases(t) {
		t.Run(tc.Name, func(t *testing.T) {
			cfg := &config.Config{S3Endpoint: "http://127.0.0.1:9000", S3Bucket: "transcripts", S3AccessKey: "access", S3SecretKey: "secret", S3UsePathStyle: true}
			loader := func(context.Context, ...func(*awsconfig.LoadOptions) error) (aws.Config, error) {
				return aws.Config{}, nil
			}
			if tc.Loader == "malformed" {
				loader = func(context.Context, ...func(*awsconfig.LoadOptions) error) (aws.Config, error) {
					return aws.Config{}, errors.New("malformed SDK configuration")
				}
			}
			store, err := newS3ObjectStore(cfg, loader)
			if tc.Valid {
				if err != nil || store == nil {
					t.Fatalf("valid configuration rejected: %v", err)
				}
				return
			}
			if err == nil || store != nil || !strings.Contains(err.Error(), tc.Error) {
				t.Fatalf("store=%v error=%v, want %q", store, err, tc.Error)
			}
			message := err.Error()
			if !strings.Contains(message, "failed because") || !strings.Contains(message, "storage.NewS3ObjectStore") || !strings.Contains(message, "during") || !strings.Contains(message, "cannot start") || !strings.Contains(message, "correct") {
				t.Fatalf("error is not actionable: %v", err)
			}
		})
	}
}
