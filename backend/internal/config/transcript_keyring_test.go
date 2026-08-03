package config

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"io"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

//go:embed testdata/transcript_keyring/cases.yaml
var keyringCasesYAML []byte

type keyringCase struct {
	Name    string `yaml:"name"`
	Active  string `yaml:"active"`
	Keyring string `yaml:"keyring"`
	Valid   bool   `yaml:"valid"`
	Error   string `yaml:"error"`
}

func loadKeyringCases(t *testing.T) []keyringCase {
	t.Helper()
	dec := yaml.NewDecoder(bytes.NewReader(keyringCasesYAML))
	dec.KnownFields(true)
	var cases []keyringCase
	if err := dec.Decode(&cases); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		t.Fatalf("fixture trailing document: %v", err)
	}
	if len(cases) != 14 {
		t.Fatalf("fixture rows=%d, want 14", len(cases))
	}
	for _, c := range cases {
		if c.Name == "" || (c.Valid == (c.Error != "")) {
			t.Fatalf("invalid fixture row %+v", c)
		}
	}
	return cases
}

func TestParseTranscriptKeyringFixture(t *testing.T) {
	for _, tc := range loadKeyringCases(t) {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			ring, err := ParseTranscriptKeyring(tc.Active, tc.Keyring)
			if tc.Valid {
				if err != nil {
					t.Fatal(err)
				}
				wrapped, version, err := ring.Wrap(context.Background(), []byte("0123456789abcdef0123456789abcdef"), []byte("aad"))
				if err != nil {
					t.Fatal(err)
				}
				plain, err := ring.Unwrap(context.Background(), wrapped, version, []byte("aad"))
				if err != nil || string(plain) != "0123456789abcdef0123456789abcdef" {
					t.Fatalf("roundtrip failed: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.Error) {
				t.Fatalf("error=%v, want substring %q", err, tc.Error)
			}
			if strings.Contains(err.Error(), tc.Keyring) {
				t.Fatal("error disclosed keyring value")
			}
			if !strings.Contains(err.Error(), "failed because") || !strings.Contains(err.Error(), "during") || !strings.Contains(err.Error(), "cannot") || !strings.Contains(err.Error(), "correct") {
				t.Fatalf("error is not actionable: %v", err)
			}
		})
	}
}

func TestTranscriptKeyringUnknownVersionAndAuthentication(t *testing.T) {
	ring, _ := ParseTranscriptKeyring("1", `{"1":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="}`)
	if _, err := ring.Unwrap(context.Background(), []byte("x"), 2, nil); err == nil {
		t.Fatal("unknown version accepted")
	}
	wrapped, v, _ := ring.Wrap(context.Background(), []byte("dek"), []byte("one"))
	if _, err := ring.Unwrap(context.Background(), wrapped, v, []byte("two")); err == nil {
		t.Fatal("wrong AAD accepted")
	}
	_ = fmt.Sprint(ring.ActiveVersion())
}
