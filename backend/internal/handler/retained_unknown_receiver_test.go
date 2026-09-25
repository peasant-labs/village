package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/retained_unknown_receiver.yaml
var retainedReceiverYAML []byte

type retainedReceiverCase struct {
	Name           string  `yaml:"name"`
	Text           string  `yaml:"text"`
	Repeat         int     `yaml:"repeat"`
	Payload        *string `yaml:"payload"`
	ExpectedWrites int     `yaml:"expected_writes"`
	FinalBytes     int     `yaml:"final_bytes"`
	Status         int     `yaml:"status"`
	Content        string  `yaml:"-"`
}

func loadRetainedReceiverCases(t *testing.T) []retainedReceiverCase {
	t.Helper()
	cases, err := loadRetainedUnknownFixtures(retainedUnknownYAML)
	if err != nil {
		t.Fatal(err)
	}
	out, err := decodeRetainedReceiverCases(retainedReceiverYAML, cases[0].Content)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func decodeRetainedReceiverCases(raw []byte, baseContent string) ([]retainedReceiverCase, error) {
	var corpus struct {
		Cases []retainedReceiverCase `yaml:"cases"`
	}
	d := yaml.NewDecoder(bytes.NewReader(raw))
	d.KnownFields(true)
	if err := d.Decode(&corpus); err != nil {
		return nil, err
	}
	var trailing any
	if err := d.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, errors.New("receiver fixture must contain one document")
		}
		return nil, err
	}
	seen := map[string]bool{}
	targets := map[int]bool{}
	for i := range corpus.Cases {
		c := &corpus.Cases[i]
		if c.Name == "" || seen[c.Name] || c.Repeat > 1000000 || c.ExpectedWrites < 0 || c.ExpectedWrites > 2 || c.Status != 201 && c.Status != 409 {
			return nil, errors.New("invalid receiver fixture " + c.Name)
		}
		seen[c.Name] = true
		if c.FinalBytes != 0 {
			if targets[c.FinalBytes] {
				return nil, errors.New("duplicate actual-envelope byte target")
			}
			targets[c.FinalBytes] = true
			if c.FinalBytes < (8<<20)-1 || c.FinalBytes > (8<<20)+1 || (c.Status == 409) != (c.FinalBytes > 8<<20) {
				return nil, errors.New("inconsistent actual byte-boundary fixture")
			}
			built, err := buildRetainedContentAtBytes(baseContent, c.FinalBytes)
			if err != nil {
				return nil, err
			}
			c.Content = built
			continue
		}
		if c.Payload != nil {
			if c.Text != "" || c.Repeat != 0 {
				return nil, errors.New("fixture " + c.Name + " mixes payload and text recipes")
			}
			if c.Status != 201 {
				return nil, errors.New("payload fixture " + c.Name + " must be accepted")
			}
			var envelope schema.TranscriptContent
			if err := json.Unmarshal([]byte(baseContent), &envelope); err != nil {
				return nil, err
			}
			if !json.Valid([]byte(*c.Payload)) {
				return nil, errors.New("fixture " + c.Name + " payload is not JSON")
			}
			envelope.SessionDetail.RetainedUnknown[0].Payload = *c.Payload
			encoded, err := json.Marshal(envelope)
			if err != nil {
				return nil, err
			}
			c.Content = string(encoded)
			if c.Content == baseContent || len(c.Content) >= 8<<20 {
				return nil, errors.New("fixture " + c.Name + " must change the payload within the actual wire limit")
			}
			var value any
			if err := json.Unmarshal([]byte(c.Content), &value); err != nil {
				return nil, err
			}
			continue
		}
		if c.Text == "" || c.Repeat < 1 {
			return nil, errors.New("receiver text recipe is empty")
		}
		// Construct the actual publisher wire, not Go's HTML-escaped serialization.
		// The payload is a JSON string containing only these fixture-owned runes.
		if strings.ContainsAny(c.Text, "\"\\\n\r") {
			return nil, errors.New("receiver text requires an explicitly escaped fixture recipe")
		}
		c.Content = strings.Replace(baseContent, `\"text\":\"x\"`, `\"text\":\"`+strings.Repeat(c.Text, c.Repeat)+`\"`, 1)
		if c.Content == baseContent || len(c.Content) >= 8<<20 {
			return nil, errors.New("fixture " + c.Name + " must change the payload within the actual wire limit")
		}
		var value any
		if err := json.Unmarshal([]byte(c.Content), &value); err != nil {
			return nil, err
		}
		inflated, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		if len(inflated) <= 8<<20 {
			return nil, errors.New("fixture " + c.Name + " no longer exercises serializer expansion")
		}
	}
	for _, name := range strings.Fields("html_payload_below_actual_transport_limit unicode_payload_below_actual_transport_limit actual_envelope_limit_minus_one actual_envelope_limit_exact actual_envelope_limit_plus_one harmless_complete_embedded_json ordinary_non_json_brace_text ordinary_trailing_json_like_text") {
		if !seen[name] {
			return nil, errors.New("required receiver fixture " + name + " missing")
		}
	}
	for target := (8 << 20) - 1; target <= (8<<20)+1; target++ {
		if !targets[target] {
			return nil, errors.New("missing required actual-envelope byte target")
		}
	}
	return corpus.Cases, nil
}

func TestRetainedReceiverInventory(t *testing.T) {
	cases, err := loadRetainedUnknownFixtures(retainedUnknownYAML)
	if err != nil {
		t.Fatal(err)
	}
	base := cases[0].Content
	if _, err := decodeRetainedReceiverCases(bytes.ReplaceAll(retainedReceiverYAML, []byte("status:"), []byte("typo:")), base); err == nil {
		t.Fatal("unknown receiver field accepted")
	}
	if _, err := decodeRetainedReceiverCases(append(append([]byte{}, retainedReceiverYAML...), []byte("\n---\n{}\n")...), base); err == nil {
		t.Fatal("trailing receiver document accepted")
	}
	for _, name := range strings.Fields("html_payload_below_actual_transport_limit unicode_payload_below_actual_transport_limit actual_envelope_limit_minus_one actual_envelope_limit_exact actual_envelope_limit_plus_one harmless_complete_embedded_json ordinary_non_json_brace_text ordinary_trailing_json_like_text") {
		if _, err := decodeRetainedReceiverCases(bytes.ReplaceAll(retainedReceiverYAML, []byte(name), []byte("removed_case")), base); err == nil {
			t.Fatalf("missing %s accepted", name)
		}
	}
}

func buildRetainedContentAtBytes(base string, target int) (string, error) {
	const marker = `\"text\":\"x\"`
	if strings.Count(base, marker) != 1 || target < len(base) {
		return "", errors.New("invalid measured-envelope recipe")
	}
	raw := strings.Replace(base, marker, `\"text\":\"`+strings.Repeat("x", target-len(base)+1)+`\"`, 1)
	if len(raw) != target {
		return "", errors.New("constructed byte target mismatch")
	}
	return raw, nil
}

func retainedContentAtBytes(t *testing.T, base string, target int) string {
	t.Helper()
	raw, err := buildRetainedContentAtBytes(base, target)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// Independent original-wire oracle: keep payload as a string and numbers as
// lexical json.Number values, without invoking a Schema decoder on the expected
// side. This detects first-decode data loss as well as rewrite loss.
func assertRetainedWire(t *testing.T, original, received []byte) *schema.SessionDetailPayload {
	t.Helper()
	evidence := func(raw []byte) (any, any) {
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		var envelope map[string]any
		if err := d.Decode(&envelope); err != nil {
			t.Fatal(err)
		}
		detail, ok := envelope["sessionDetail"].(map[string]any)
		if !ok {
			t.Fatal("response lacks session detail")
		}
		return detail["retainedUnknown"], detail["diagnostics"]
	}
	wantRecords, wantDiagnostics := evidence(original)
	gotRecords, gotDiagnostics := evidence(received)
	if !reflect.DeepEqual(wantRecords, gotRecords) || !reflect.DeepEqual(wantDiagnostics, gotDiagnostics) {
		t.Fatal("receiver changed original retained payload bytes, coordinates or diagnostics")
	}
	decoded, err := schema.DecodeTranscriptContentRaw(received)
	if err != nil {
		t.Fatal(err)
	}
	if !containsContentCapability(schema.RequiredContentCapabilities(*decoded.SessionDetail), schema.ContentCapabilityRetainedUnknownV1) {
		t.Fatal("receiver dropped retained evidence capability")
	}
	return decoded.SessionDetail
}

func TestRetainedUnknownActualWireBudget(t *testing.T) {
	for _, c := range loadRetainedReceiverCases(t) {
		if c.FinalBytes != 0 {
			continue
		} // Actual size edges run on the real receiver below.
		t.Run(c.Name, func(t *testing.T) {
			// A real publication must pass both the receiver boundary and metadata
			// mirror path before any encrypted store side effect.
			raw := []byte(c.Content)
			if err := requireSupportedContentForHarness(raw, "claude-code", productionObservedModelPreservationEvaluator{}, productionProvenancePreservationEvaluator{}); err != nil {
				t.Fatal(err)
			}
			detail, err := decodePublicationDetail(raw)
			if err != nil {
				t.Fatal(err)
			}
			var metadata schema.AuthoritativePublishRequest
			if err := json.Unmarshal(retainedUnknownMetadata(t, raw), &metadata); err != nil {
				t.Fatal(err)
			}
			var legacy schema.PublishRequest
			if err := json.Unmarshal(retainedUnknownMetadata(t, raw), &legacy); err != nil {
				t.Fatal(err)
			}
			if err := validatePublicationGraphMirrors(detail, &legacy, &metadata); err != nil {
				t.Fatal(err)
			}
			assertRetainedWire(t, raw, raw)
			encoded, err := encodeCanonicalTranscript(detail)
			if c.ExpectedWrites == 2 {
				if err != nil {
					t.Fatal(err)
				}
				if len(encoded) > 8<<20 {
					t.Fatal("canonical rewrite exceeded its actual transport budget")
				}
				assertRetainedWire(t, raw, encoded)
			} else {
				if !errors.Is(err, errCanonicalRewriteTooLarge) {
					t.Fatalf("expected bounded rewrite refusal, got %v", err)
				}
				original, err := originalContentEnvelope(raw)
				if err != nil || !bytes.Equal(original, raw) {
					t.Fatalf("expanded rewrite changed original envelope: %v", err)
				}
				var envelope map[string]json.RawMessage
				if err := json.Unmarshal(raw, &envelope); err != nil {
					t.Fatal(err)
				}
				wrapped, err := originalContentEnvelope(envelope["sessionDetail"])
				if err != nil {
					t.Fatal(err)
				}
				assertRetainedWire(t, raw, wrapped)
			}
		})
	}
}

func TestRetainedUnknownInspectionAcceptance(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range loadRetainedReceiverCases(t) {
		if c.Payload == nil {
			continue
		}
		seen[c.Name] = true
		t.Run(c.Name, func(t *testing.T) {
			raw := []byte(c.Content)
			// Independent original-byte oracle: ordinary encoding/json with
			// UseNumber, never a second Schema production decode.
			assertRetainedWire(t, raw, raw)
			owner := pgtype.UUID{Bytes: uuid.MustParse("00000000-0000-4000-8000-000000000111"), Valid: true}
			tid := pgtype.UUID{Bytes: uuid.MustParse("00000000-0000-4000-8000-000000000222"), Valid: true}
			now := pgtype.Timestamptz{Time: time.Unix(1700000000, 0), Valid: true}
			mq := &mockQuerier{
				getTranscriptIDByOwnerAndLocalID: func(context.Context, sqlc.GetTranscriptIDByOwnerAndLocalIDParams) (pgtype.UUID, error) {
					return pgtype.UUID{}, pgx.ErrNoRows
				},
				listTranscriptAssociationsByOwnerAndIDs: func(context.Context, sqlc.ListTranscriptAssociationsByOwnerAndIDsParams) ([]sqlc.TranscriptAssociation, error) {
					return nil, nil
				},
				createTranscript: func(_ context.Context, arg sqlc.CreateTranscriptParams) (sqlc.Transcript, error) {
					return sqlc.Transcript{ID: tid, OwnerID: owner, LocalID: arg.LocalID, Title: arg.Title, Visibility: "private", ModelProvider: arg.ModelProvider, BlobKey: arg.BlobKey, BlobSizeBytes: arg.BlobSizeBytes, SchemaVersion: arg.SchemaVersion, PublishedAt: now, UpdatedAt: now, LicenseID: arg.LicenseID}, nil
				},
				insertTranscriptAssociations: func(context.Context, sqlc.InsertTranscriptAssociationsParams) error {
					return nil
				},
				setTranscriptContentHash: func(context.Context, sqlc.SetTranscriptContentHashParams) error {
					return nil
				},
				setAcceptedRequestOperationFingerprint: func(context.Context, sqlc.SetAcceptedRequestOperationFingerprintParams) error {
					return nil
				},
				deleteTranscriptCommits: func(context.Context, pgtype.UUID) error { return nil },
				listTranscriptAssociationsByTranscript: func(context.Context, pgtype.UUID) ([]sqlc.TranscriptAssociation, error) {
					return nil, nil
				},
			}
			h := newTestHandler(mq, &mockTranscriptBlobStore{})
			w := publishPiParts(t, h, &AuthUser{ID: uuid.UUID(owner.Bytes), Username: "fixture-owner"}, retainedUnknownMetadata(t, raw), raw)
			if w.Code != http.StatusCreated {
				t.Fatalf("publish=%d %s", w.Code, w.Body.String())
			}
		})
	}
	for _, name := range strings.Fields("harmless_complete_embedded_json ordinary_non_json_brace_text ordinary_trailing_json_like_text") {
		if !seen[name] {
			t.Fatalf("required inspection acceptance fixture %q missing", name)
		}
	}
}
