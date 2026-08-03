package storage

import (
	"bytes"
	_ "embed"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/aws/smithy-go"
	httptransport "github.com/aws/smithy-go/transport/http"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/residual/cases.yaml
var residualCasesYAML []byte

type residualCase struct {
	Name     string `yaml:"name"`
	Kind     string `yaml:"kind"`
	Code     string `yaml:"code"`
	Status   int    `yaml:"status"`
	Hash     string `yaml:"hash"`
	HashNull bool   `yaml:"hash_null"`
	Size     int64  `yaml:"size"`
	SizeNull bool   `yaml:"size_null"`
	Expected bool   `yaml:"expected"`
}

func loadResidualCases(t *testing.T) []residualCase {
	t.Helper()
	dec := yaml.NewDecoder(bytes.NewReader(residualCasesYAML))
	dec.KnownFields(true)
	var cases []residualCase
	if err := dec.Decode(&cases); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		t.Fatalf("residual fixture trailing document: %v", err)
	}
	if len(cases) != 13 {
		t.Fatalf("residual fixture rows=%d, want 13", len(cases))
	}
	return cases
}

func TestResidualSecurityFixture(t *testing.T) {
	for _, tc := range loadResidualCases(t) {
		t.Run(tc.Name, func(t *testing.T) {
			switch tc.Kind {
			case "s3_error":
				var cause error = errors.New("provider response")
				if tc.Code != "" {
					cause = &smithy.GenericAPIError{Code: tc.Code, Message: "safe test message"}
				}
				err := &httptransport.ResponseError{Response: &httptransport.Response{Response: &http.Response{StatusCode: tc.Status}}, Err: cause}
				if got := isS3NotFound(err); got != tc.Expected {
					t.Fatalf("isS3NotFound=%v, want %v", got, tc.Expected)
				}
			case "identity":
				var hash *string
				var size *int64
				if !tc.HashNull {
					hash = &tc.Hash
				}
				if !tc.SizeNull {
					size = &tc.Size
				}
				loaded, err := NewLoadedContentIdentity(hash, size)
				if tc.Expected {
					if err != nil || loaded.validate() != nil {
						t.Fatalf("valid identity rejected: %v", err)
					}
					return
				}
				if err == nil {
					t.Fatal("invalid nullable identity accepted")
				}
			case "zero_state":
				if err := (LoadedContentIdentity{}).validate(); err == nil {
					t.Fatal("zero loaded identity state accepted")
				}
			case "unknown_state":
				if err := (LoadedContentIdentity{status: ContentIdentityStatus(255)}).validate(); err == nil {
					t.Fatal("unknown loaded identity state accepted")
				}
			default:
				t.Fatalf("unknown residual fixture kind %q", tc.Kind)
			}
		})
	}
}

func TestBlobDescriptorEqualDoesNotAliasWrappedKey(t *testing.T) {
	key := ObjectKey("transcripts/11111111-1111-4111-8111-111111111111.bin")
	left, err := NewBlobDescriptor(key, []byte("wrapped"), EncryptionAES256GCMRandomNonceV1, 1)
	if err != nil {
		t.Fatal(err)
	}
	right, err := NewBlobDescriptor(key, left.WrappedDEK(), left.Algorithm(), left.KeyVersion())
	if err != nil {
		t.Fatal(err)
	}
	if !left.Equal(right) {
		t.Fatal("equal descriptors differ")
	}
	exposed := right.WrappedDEK()
	exposed[0] ^= 1
	if !left.Equal(right) {
		t.Fatal("wrapped-key accessor mutated descriptor")
	}
}
