package handler

import (
	"context"
	_ "embed"
	"encoding/json"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/integrity-request.yaml
var integrityRequestYAML []byte

type integrityRequestRow struct {
	PublishedAtMs int64  `yaml:"published_at_ms"`
	UpdatedAtMs   int64  `yaml:"updated_at_ms"`
	TurnCount     int32  `yaml:"turn_count"`
	TokenCount    int32  `yaml:"token_count"`
	ModelName     string `yaml:"model_name"`
	DurationMs    int64  `yaml:"duration_ms"`
}

type integrityRequestWant struct {
	Error         string   `yaml:"error"`
	PublishedAtMs int64    `yaml:"published_at_ms"`
	UpdatedAtMs   int64    `yaml:"updated_at_ms"`
	RecordKeys    []string `yaml:"record_keys"`
	Metadata      string   `yaml:"metadata"`
	Harness       string   `yaml:"harness"`
	SchemaVersion string   `yaml:"schema_version"`
	Validates     bool     `yaml:"validates"`
}

type integrityRequestCase struct {
	Name     string               `yaml:"name"`
	Why      string               `yaml:"why"`
	Row      integrityRequestRow  `yaml:"row"`
	Payload  string               `yaml:"payload"`
	Metadata string               `yaml:"metadata"`
	Want     integrityRequestWant `yaml:"want"`
}

var requiredIntegrityRequestCases = []string{
	"record_is_only_village_times",
	"metadata_passes_through",
	"no_metadata_is_nil",
	"bare_payload_is_migrated",
	"request_json_has_no_row_stats",
	"request_validates",
	"missing_publish_time_is_refused",
}

func loadIntegrityRequestCases(t *testing.T) []integrityRequestCase {
	t.Helper()
	cases, err := decodeFixtureRows[integrityRequestCase](integrityRequestYAML)
	if err != nil {
		t.Fatalf("load the integrity request corpus: %v", err)
	}
	present := map[string]struct{}{}
	for _, c := range cases {
		if c.Name == "" || c.Why == "" {
			t.Fatalf("integrity request case %+v must carry a name and a reason", c)
		}
		if _, dup := present[c.Name]; dup {
			t.Fatalf("integrity request case %q is repeated", c.Name)
		}
		present[c.Name] = struct{}{}
	}
	assertExactTitleFixtureNames(t, "integrity-request", present, requiredIntegrityRequestCases)
	return cases
}

// integrityRequestMetadata is a publish metadata object as village stores it.
func integrityRequestMetadata(t *testing.T) *schema.AuthoritativePublishRequest {
	t.Helper()
	content := canonicalStoredContent(t)
	req := schema.AuthoritativePublishRequest{
		Identity:         schema.AuthoritativeSessionIdentity{SessionID: "request-session", SchemaVersion: 2},
		Model:            schema.AuthoritativeModelInfo{Harness: schema.HarnessClaudeCode, Model: "reported-model"},
		Timestamp:        schema.AuthoritativeTimestampInfo{Start: 1690000000000, End: 1690000060000},
		Source:           schema.AuthoritativeSourceInfo{FilePath: "/fixture/session.jsonl", Format: schema.SourceFormatJSONL},
		Project:          schema.AuthoritativeProjectContext{Hash: schema.ProjectHash(strings.Repeat("a", 64)), Name: "request"},
		ContentHash:      schema.ComputeTranscriptContentHash(content),
		VisibilityIntent: schema.VisibilityIntentPrivate,
		Client:           &schema.AuthoritativeClientInfo{Name: "peasant", Version: "1.2.3"},
	}
	encoded, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := schema.DecodeAuthoritativePublishMetadataRaw(encoded)
	if err != nil {
		t.Fatalf("the fixture metadata is not a valid stored metadata object: %v", err)
	}
	return &decoded
}

func integrityRequestTime(ms int64) pgtype.Timestamptz {
	if ms == 0 {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: time.UnixMilli(ms).UTC(), Valid: true}
}

func TestBuildScryRequest(t *testing.T) {
	for _, c := range loadIntegrityRequestCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			var stored []byte
			switch c.Payload {
			case "canonical":
				stored = canonicalStoredContent(t)
			case "bare":
				stored = legacyBarePayloadJSON("claude")
			default:
				t.Fatalf("unknown payload %q", c.Payload)
			}
			payload, _, err := defaultContentMigrator.Migrate(context.Background(), stored)
			if err != nil {
				t.Fatalf("migrate the stored fixture: %v", err)
			}
			var metadata *schema.AuthoritativePublishRequest
			switch c.Metadata {
			case "present":
				metadata = integrityRequestMetadata(t)
			case "absent":
			default:
				t.Fatalf("unknown metadata %q", c.Metadata)
			}
			id := uuid.New()
			row := sqlc.Transcript{
				ID:          pgtype.UUID{Bytes: id, Valid: true},
				PublishedAt: integrityRequestTime(c.Row.PublishedAtMs),
				UpdatedAt:   integrityRequestTime(c.Row.UpdatedAtMs),
				TurnCount:   pgtype.Int4{Int32: c.Row.TurnCount, Valid: c.Row.TurnCount != 0},
				TokenCount:  pgtype.Int4{Int32: c.Row.TokenCount, Valid: c.Row.TokenCount != 0},
				ModelName:   pgtype.Text{String: c.Row.ModelName, Valid: c.Row.ModelName != ""},
				DurationMs:  pgtype.Int8{Int64: c.Row.DurationMs, Valid: c.Row.DurationMs != 0},
			}

			req, err := buildScryRequest(row, payload, metadata)
			if c.Want.Error != "" {
				if err == nil || !strings.Contains(err.Error(), c.Want.Error) {
					t.Fatalf("build error = %v, want one naming %q (%s)", err, c.Want.Error, c.Why)
				}
				return
			}
			if err != nil {
				t.Fatalf("build the request: %v (%s)", err, c.Why)
			}
			if req.ContractVersion != schema.ScryContractVersion {
				t.Fatalf("contract version %q, want %q", req.ContractVersion, schema.ScryContractVersion)
			}
			if string(req.TranscriptID) != id.String() {
				t.Fatalf("transcript id %q, want the row id %q", req.TranscriptID, id)
			}
			if req.Record.PublishedAtMs != c.Want.PublishedAtMs || req.Record.UpdatedAtMs != c.Want.UpdatedAtMs {
				t.Fatalf("record %+v, want published %d updated %d (%s)", req.Record, c.Want.PublishedAtMs, c.Want.UpdatedAtMs, c.Why)
			}
			if !reflect.DeepEqual(req.SessionDetail, *payload) {
				t.Fatalf("the session detail is not the migrated payload the content route serves (%s)", c.Why)
			}
			switch c.Want.Metadata {
			case "present":
				if req.PublishMetadata == nil || !reflect.DeepEqual(*req.PublishMetadata, *metadata) {
					t.Fatalf("publish metadata %+v, want the stored object unchanged (%s)", req.PublishMetadata, c.Why)
				}
			case "nil":
				if req.PublishMetadata != nil {
					t.Fatalf("publish metadata %+v, want nil (%s)", req.PublishMetadata, c.Why)
				}
			default:
				t.Fatalf("unknown want.metadata %q", c.Want.Metadata)
			}
			if c.Want.Harness != "" && string(req.SessionDetail.Harness) != c.Want.Harness {
				t.Fatalf("session detail harness %q, want %q", req.SessionDetail.Harness, c.Want.Harness)
			}
			if c.Want.SchemaVersion == "current" && req.SessionDetail.SchemaVersion != currentContractVersion {
				t.Fatalf("session detail schema version %q, want %q", req.SessionDetail.SchemaVersion, currentContractVersion)
			}

			encoded, err := json.Marshal(req)
			if err != nil {
				t.Fatalf("marshal the request: %v", err)
			}
			var wire map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &wire); err != nil {
				t.Fatal(err)
			}
			var record map[string]json.RawMessage
			if err := json.Unmarshal(wire["record"], &record); err != nil {
				t.Fatalf("decode the record object: %v", err)
			}
			keys := make([]string, 0, len(record))
			for key := range record {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			if !slices.Equal(keys, []string{"publishedAtMs", "updatedAtMs"}) {
				t.Fatalf("record keys %v, want exactly publishedAtMs and updatedAtMs (%s)", keys, c.Why)
			}
			if c.Want.RecordKeys != nil && !slices.Equal(keys, c.Want.RecordKeys) {
				t.Fatalf("record keys %v, want %v", keys, c.Want.RecordKeys)
			}
			if _, present := wire["publishMetadata"]; present != (c.Want.Metadata == "present") {
				t.Fatalf("publishMetadata on the wire = %v, want %v", present, c.Want.Metadata == "present")
			}
			if c.Want.Validates {
				if err := req.Validate(); err != nil {
					t.Fatalf("the contract refuses the request: %v", err)
				}
				var decoded schema.ScryCheckRequest
				if err := json.Unmarshal(encoded, &decoded); err != nil {
					t.Fatalf("the contract's strict decoder refuses the request: %v", err)
				}
				if err := decoded.Validate(); err != nil {
					t.Fatalf("the decoded request does not validate: %v", err)
				}
			}
		})
	}
}
