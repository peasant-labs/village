package storage

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/peasant-labs/village/backend/internal/config"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/bounded-object-read.yaml
var boundedReadFixture []byte

type boundedReadOutcome string

const (
	boundedReadSuccess   boundedReadOutcome = "success"
	boundedReadUncertain boundedReadOutcome = "uncertain"
)

type boundedReadCase struct {
	Name           string             `yaml:"name"`
	Body           string             `yaml:"body"`
	Limit          int64              `yaml:"limit"`
	DeclaredLength int64              `yaml:"declared_length"`
	Outcome        boundedReadOutcome `yaml:"outcome"`
}

func TestEncryptedBoundedReadAuthenticates(t *testing.T) {
	ctx := context.Background()
	store, objects := testEncryptedStore(t)
	id := uuid.New()
	body := []byte("synthetic bounded generation")
	descriptor, expected, err := store.Write(ctx, id, body)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := NewLoadedContentIdentity(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	size := int64(len(body)) + CiphertextOverheadBytes
	plain, actual, err := store.ReadBounded(ctx, id, descriptor, pending, size)
	if err != nil || !bytes.Equal(plain, body) || actual != expected {
		t.Fatalf("bounded authenticated read failed: %v", err)
	}
	plain, _, err = store.ReadBounded(ctx, id, descriptor, pending, size-1)
	if !errors.Is(err, ErrObjectLookupUncertain) || plain != nil {
		t.Fatalf("oversized generation should be uncertain, got %v", err)
	}
	object := objects.objects[descriptor.ObjectKey()]
	object.body[len(object.body)-1] ^= 1
	objects.objects[descriptor.ObjectKey()] = object
	_, _, err = store.ReadBounded(ctx, id, descriptor, pending, size)
	if !errors.Is(err, ErrObjectIntegrityUncertain) {
		t.Fatalf("incorrect transcript authentication should be uncertain, got %v", err)
	}
}

var requiredBoundedReadCases = []string{
	"bounded-read-accepts-exact-size",
	"bounded-read-rejects-oversized-object",
	"bounded-read-rejects-short-response",
	"bounded-read-rejects-unknown-size",
	"bounded-read-allows-empty-object",
}

func TestS3BoundedRead(t *testing.T) {
	var cases []boundedReadCase
	dec := yaml.NewDecoder(bytes.NewReader(boundedReadFixture))
	dec.KnownFields(true)
	if err := dec.Decode(&cases); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		t.Fatalf("trailing fixture document: %v", err)
	}
	seen := make(map[string]bool)
	for _, c := range cases {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("missing or duplicate fixture name %q", c.Name)
		}
		seen[c.Name] = true
		t.Run(c.Name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", ciphertextContentType)
				if c.DeclaredLength < 0 {
					w.Header().Set("Transfer-Encoding", "chunked")
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
				} else {
					w.Header().Set("Content-Length", fmt.Sprint(c.DeclaredLength))
				}
				_, _ = io.WriteString(w, c.Body)
			}))
			defer server.Close()
			store, err := NewS3ObjectStore(&config.Config{
				S3Endpoint: server.URL, S3Bucket: "test",
				S3AccessKey: "test", S3SecretKey: "test", S3UsePathStyle: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			body, contentType, err := store.GetBounded(context.Background(), ObjectKey("test-key"), c.Limit)
			switch c.Outcome {
			case boundedReadSuccess:
				if err != nil || string(body) != c.Body || contentType != ciphertextContentType {
					t.Fatalf("bounded read body=%q content type=%q error=%v", body, contentType, err)
				}
			case boundedReadUncertain:
				if !errors.Is(err, ErrObjectLookupUncertain) || body != nil {
					t.Fatalf("bounded read returned body=%q error=%v, want uncertainty", body, err)
				}
			default:
				t.Fatalf("unhandled outcome %q", c.Outcome)
			}
		})
	}
	for _, name := range requiredBoundedReadCases {
		if !seen[name] {
			t.Fatalf("required bounded read fixture missing %q", name)
		}
	}
}
