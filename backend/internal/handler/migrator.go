package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/peasant-labs/schema"
)

// --- Migrate-on-read ---------------------------------------------------------
//
// peasant push uploads a versioned schema.TranscriptContent envelope as the
// transcript_file (a "{sessionId}--content.json" blob). The village stores that
// blob verbatim, then normalizes it to the CURRENT schema.SessionDetailPayload
// shape lazily, the first time the blob is read for display — migrate-on-read.
//
// Three classes of stored blob must normalize to the current shape:
//   1. a CURRENT TranscriptContent envelope (fresh push) — unwrap, no rewrite;
//   2. a bare SessionDetailPayload object carrying the OLD provider-keyed shape
//      (json:"provider"/"modelHarness" with values claude/gemini) — key+value
//      migrate to json:"harness" + claude-code/gemini-cli;
//   3. legacy raw provider JSONL (a bare array / newline-delimited objects) —
//      best-effort projection onto SessionDetailPayload turns.
//
// A3 DISTINCT FLOORS: the migrate-on-read DISPLAY floor below is deliberately
// SEPARATE from the Phase-A SQL backfill (storage normalization of
// model_provider) and from the push-acceptance floor (schema.MinPushContractVersion,
// which gates INCOMING uploads). This floor governs how far back a STORED blob
// can be normalized for rendering; it is village-local and not advertised.

// currentContractVersion is the contract the village normalizes stored blobs TO.
// It remains at the existing 0.1.1 envelope because observedModel is an additive
// optional field. Enriched emission is negotiated through exact membership in a
// flat, forward-open opaque-token list, so legacy 0.1.x traffic remains compatible while
// richer clients cannot mistake envelope compatibility for preservation support.
const currentContractVersion schema.PushContractVersion = "0.1.1"

// displayMigrateFloor is the OLDEST stored contract the village will still
// normalize for display. Equal to currentContractVersion today; it may reach
// further back than the push-acceptance floor as older shapes accrue.
const displayMigrateFloor schema.PushContractVersion = "0.1.0"

// sameContractShape reports whether two push-contract versions are shape-
// compatible — equal in MAJOR.MINOR. The wire SHAPE is versioned by MAJOR.MINOR;
// a PATCH-only difference (e.g. 0.1.0 vs 0.1.1, the 1e8tk required-fields
// strictening) carries no shape change. migrate-on-read uses this instead of
// exact equality so a patch bump of currentContractVersion does NOT force a
// pointless no-op rewrite of every stored blob at the prior patch version.
func sameContractShape(a, b schema.PushContractVersion) bool {
	return contractMinor(a) == contractMinor(b)
}

// contractMinor returns the "MAJOR.MINOR" prefix of a dotted semver string, or
// the whole string when it carries fewer than two dot-separated components.
func contractMinor(v schema.PushContractVersion) string {
	s := string(v)
	first := strings.IndexByte(s, '.')
	if first < 0 {
		return s
	}
	second := strings.IndexByte(s[first+1:], '.')
	if second < 0 {
		return s
	}
	return s[:first+1+second]
}

// EnvelopeShape classifies a stored transcript blob from a cheap leading-byte +
// key sniff, so the migrator can dispatch without fully parsing every variant.
type EnvelopeShape int

const (
	// ShapeUnknown is an unrecognized/empty blob.
	ShapeUnknown EnvelopeShape = iota
	// ShapeEnvelope is a JSON object carrying a "contractVersion" key — a
	// schema.TranscriptContent envelope (the current/self-describing form).
	ShapeEnvelope
	// ShapeBarePayload is a JSON object WITHOUT a "contractVersion" key — a bare
	// SessionDetailPayload (possibly carrying the legacy provider/modelHarness
	// keys + claude/gemini values).
	ShapeBarePayload
	// ShapeRawJSONL is a JSON array or newline-delimited JSON — legacy raw
	// provider transcript bytes uploaded before the TranscriptContent envelope.
	ShapeRawJSONL
)

// String renders the shape for logs/test assertions.
func (s EnvelopeShape) String() string {
	switch s {
	case ShapeEnvelope:
		return "envelope"
	case ShapeBarePayload:
		return "bare_payload"
	case ShapeRawJSONL:
		return "raw_jsonl"
	default:
		return "unknown"
	}
}

// ErrEmptyBlob is returned by Migrate when the stored blob has no decodable
// content. Callers should surface it as a server error, not silently render an
// empty transcript.
var ErrEmptyBlob = errors.New("migrate-on-read: empty or undecodable transcript blob")

// ContentMigrator normalizes a stored transcript blob to the current
// SessionDetailPayload shape.
//
// Migrate returns:
//   - current: the normalized payload to serve to the viewer (never nil on nil
//     error);
//   - rewrite: true when the stored blob was NOT already in the canonical
//     current form and the caller should re-store the normalized blob so the
//     next read is a no-op (rewrite==false);
//   - err: a decode/normalize failure.
type ContentMigrator interface {
	Migrate(ctx context.Context, raw []byte) (current *schema.SessionDetailPayload, rewrite bool, err error)
}

// blobMigrator is the production ContentMigrator. It is stateless; the N2
// concurrent-rewrite-race guard lives in the read handler (keyedMutex), which
// serializes the download→migrate→re-store sequence per blob key.
type blobMigrator struct{}

// NewContentMigrator returns the production migrate-on-read implementation.
func NewContentMigrator() ContentMigrator { return &blobMigrator{} }

// Package-level migrate-on-read singletons used by the read handler. The
// migrator is stateless; defaultRewriteLocks must be process-wide so the N2
// per-key guard serializes concurrent rewrites across all requests.
var (
	defaultContentMigrator = NewContentMigrator()
	defaultRewriteLocks    = newKeyedMutex()
)

var _ ContentMigrator = (*blobMigrator)(nil)

// Migrate normalizes a stored transcript blob to the current
// SessionDetailPayload shape. See ContentMigrator for the contract.
func (m *blobMigrator) Migrate(ctx context.Context, raw []byte) (*schema.SessionDetailPayload, bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, false, ErrEmptyBlob
	}

	switch sniffShape(trimmed) {
	case ShapeEnvelope:
		var env schema.TranscriptContent
		if err := json.Unmarshal(trimmed, &env); err != nil {
			return nil, false, fmt.Errorf("transcript migrate-on-read failed because the stored envelope could not be decoded as schema.TranscriptContent in handler.blobMigrator.Migrate during typed read normalization; no body was served and no stored generation was rewritten; repair or republish the transcript with a supported envelope, then retry: %w", err)
		}
		if env.Kind != schema.ContentKindSessionDetail || env.SessionDetail == nil {
			return nil, false, fmt.Errorf("transcript migrate-on-read failed because stored envelope kind %q does not carry a sessionDetail payload in handler.blobMigrator.Migrate during typed read normalization; no body was served and no stored generation was rewritten; republish a %q envelope with a non-null sessionDetail payload", env.Kind, schema.ContentKindSessionDetail)
		}
		payload := env.SessionDetail
		// VERSION CONFLICT-WINNER (N1/B5): the envelope's ContractVersion is
		// authoritative for migrate-on-read dispatch (the embedded
		// SchemaVersion is advisory). A SHAPE-COMPATIBLE envelope is already
		// canonical — serve as-is, no rewrite. Shape is versioned by MAJOR.MINOR;
		// a PATCH-only difference (e.g. 0.1.0 vs 0.1.1, the 1e8tk required-fields
		// strictening) carries NO wire-shape change, so a patch-compatible blob is
		// NOT rewritten — exact equality would needlessly churn every stored 0.1.0
		// blob the instant currentContractVersion bumped to 0.1.1.
		if sameContractShape(env.ContractVersion, currentContractVersion) {
			return payload, false, nil
		}
		// Shape-incompatible (older MAJOR.MINOR) envelope: normalize the embedded
		// payload's harness value and re-stamp the current version, then rewrite.
		payload.Harness = canonicalHarness(string(payload.Harness))
		payload.SchemaVersion = currentContractVersion
		return payload, true, nil

	case ShapeBarePayload:
		payload, err := decodeLegacyPayload(trimmed)
		if err != nil {
			return nil, false, err
		}
		payload.SchemaVersion = currentContractVersion
		return payload, true, nil

	case ShapeRawJSONL:
		payload, err := decodeRawJSONL(trimmed)
		if err != nil {
			return nil, false, err
		}
		payload.SchemaVersion = currentContractVersion
		return payload, true, nil

	default:
		return nil, false, ErrEmptyBlob
	}
}

// encodeCanonicalTranscript is the sole typed rewrite/re-emit boundary used by
// the real migrate-on-read handler and by capability preservation evaluation.
func encodeCanonicalTranscript(payload *schema.SessionDetailPayload) ([]byte, error) {
	return productionContentRewriteEncoder.Encode(currentContractVersion, payload)
}

// sniffShape classifies a (whitespace-trimmed) blob by its leading byte and, for
// objects, the presence of a "contractVersion" key (B6 3-way discriminator).
func sniffShape(trimmed []byte) EnvelopeShape {
	trimmed = bytes.TrimSpace(trimmed)
	if len(trimmed) == 0 {
		return ShapeUnknown
	}
	switch trimmed[0] {
	case '[':
		return ShapeRawJSONL
	case '{':
		// A single JSON object decodes cleanly into a map; newline-delimited
		// JSON (multiple objects) does not (trailing data) → treat as raw JSONL.
		var probe map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &probe); err != nil {
			return ShapeRawJSONL
		}
		if _, ok := probe["contractVersion"]; ok {
			return ShapeEnvelope
		}
		return ShapeBarePayload
	default:
		return ShapeUnknown
	}
}

// canonicalHarness maps a legacy harness VALUE to its canonical bestiary form.
// Unknown/already-canonical values pass through unchanged.
func canonicalHarness(legacy string) schema.Harness {
	switch legacy {
	case "claude":
		return schema.HarnessClaudeCode
	case "gemini":
		return schema.HarnessGeminiCLI
	default:
		return schema.Harness(legacy)
	}
}

// decodeLegacyPayload decodes a bare SessionDetailPayload, accepting the legacy
// provider-keyed shape: if the canonical json:"harness" key is absent, it falls
// back to json:"provider" then json:"modelHarness", and migrates the VALUE.
func decodeLegacyPayload(raw []byte) (*schema.SessionDetailPayload, error) {
	var p schema.SessionDetailPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("transcript migrate-on-read failed because the stored bare payload could not be decoded as schema.SessionDetailPayload in handler.decodeLegacyPayload during typed read normalization; no body was served and no stored generation was rewritten; repair or republish the transcript with a supported payload, then retry: %w", err)
	}
	if p.Harness == "" {
		var aux struct {
			Provider     string `json:"provider"`
			ModelHarness string `json:"modelHarness"`
		}
		_ = json.Unmarshal(raw, &aux)
		legacy := aux.Provider
		if legacy == "" {
			legacy = aux.ModelHarness
		}
		p.Harness = canonicalHarness(legacy)
	} else {
		p.Harness = canonicalHarness(string(p.Harness))
	}
	return &p, nil
}

// decodeRawJSONL best-effort projects legacy raw provider transcript content (a
// JSON array or newline-delimited JSON objects) onto SessionDetailPayload turns.
// It cannot reconstruct the rich indexed shape the peasant indexer produces — it
// preserves role + textual content so the transcript still renders.
func decodeRawJSONL(raw []byte) (*schema.SessionDetailPayload, error) {
	var rows []map[string]json.RawMessage
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &rows); err != nil {
			return nil, fmt.Errorf("transcript migrate-on-read failed because the stored legacy JSON array could not be decoded in handler.decodeRawJSONL during read normalization; no body was served and no stored generation was rewritten; repair or republish the source transcript, then retry: %w", err)
		}
	} else {
		for _, line := range bytes.Split(trimmed, []byte("\n")) {
			line = bytes.TrimSpace(line)
			if len(line) == 0 {
				continue
			}
			var row map[string]json.RawMessage
			if err := json.Unmarshal(line, &row); err != nil {
				return nil, fmt.Errorf("transcript migrate-on-read failed because a stored legacy JSONL line could not be decoded in handler.decodeRawJSONL during read normalization; no body was served and no stored generation was rewritten; repair or republish the source transcript, then retry: %w", err)
			}
			rows = append(rows, row)
		}
	}

	p := &schema.SessionDetailPayload{Turns: make([]schema.TurnDetail, 0, len(rows))}
	for i, row := range rows {
		turn := schema.TurnDetail{Index: i}
		if r, ok := row["role"]; ok {
			_ = json.Unmarshal(r, &turn.Role)
		}
		if c, ok := row["content"]; ok {
			var s string
			if json.Unmarshal(c, &s) == nil {
				turn.Content = s
			} else {
				turn.Content = string(c)
			}
		}
		p.Turns = append(p.Turns, turn)
	}
	return p, nil
}

// normalizeMetadataHarnessKey migrates the publish "metadata" field's SECOND
// wire surface (the plain UnifiedMetadata/PublishRequest JSON, distinct from the
// TranscriptContent envelope) onto the canonical harness representation before
// decode + schema enforcement. Two distinct surfaces are normalized:
//
//   - model.harness: the legacy harness KEY (model.modelHarness / model.provider)
//     is folded to model.harness, value-migrated (claude->claude-code,
//     gemini->gemini-cli). A body already using model.harness keeps its key but
//     still has its VALUE canonicalized below.
//   - entries[].harness: each session entry's harness VALUE is canonicalized
//     in place (FB/A unified-schema-c7lco). entries[].harness is enum-keyed by
//     the vendored publish schema, so a body carrying a PRE-RENAME entry harness
//     ("claude"/"gemini" — from session_entries.provider rows the V33 rename
//     never canonicalized) would otherwise 422. The schema itself still rejects
//     pre-rename strings by design; normalizing them here is the handler's job,
//     symmetric with the model.harness treatment.
//
// On any structural surprise it returns the input untouched so decode/enforcement
// see the original bytes.
func normalizeMetadataHarnessKey(raw []byte) []byte {
	var top map[string]json.RawMessage
	if json.Unmarshal(raw, &top) != nil {
		return raw
	}
	changed := false

	if modelRaw, ok := top["model"]; ok {
		var model map[string]json.RawMessage
		if json.Unmarshal(modelRaw, &model) == nil {
			if nm, ok := normalizeModelHarness(model); ok {
				top["model"] = nm
				changed = true
			}
		}
	}

	if entriesRaw, ok := top["entries"]; ok {
		if ne, ok := normalizeEntriesHarness(entriesRaw); ok {
			top["entries"] = ne
			changed = true
		}
	}

	if !changed {
		return raw
	}
	out, err := json.Marshal(top)
	if err != nil {
		return raw
	}
	return out
}

// normalizeModelHarness folds the legacy harness key onto model.harness and
// canonicalizes its value. It reports whether the model object was rewritten.
func normalizeModelHarness(model map[string]json.RawMessage) (json.RawMessage, bool) {
	var current string
	if hv, has := model["harness"]; has {
		// Already canonical KEY — still canonicalize the VALUE in case a
		// pre-rename harness string was sent under the new key.
		if json.Unmarshal(hv, &current) != nil {
			return nil, false
		}
		canon := string(canonicalHarness(current))
		if canon == current {
			return nil, false
		}
		h, err := json.Marshal(canon)
		if err != nil {
			return nil, false
		}
		model["harness"] = h
		nm, err := json.Marshal(model)
		if err != nil {
			return nil, false
		}
		return nm, true
	}

	var legacy string
	if p, ok := model["provider"]; ok {
		_ = json.Unmarshal(p, &legacy)
	}
	if legacy == "" {
		if mh, ok := model["modelHarness"]; ok {
			_ = json.Unmarshal(mh, &legacy)
		}
	}
	if legacy == "" {
		return nil, false
	}
	h, err := json.Marshal(string(canonicalHarness(legacy)))
	if err != nil {
		return nil, false
	}
	model["harness"] = h
	delete(model, "provider")
	delete(model, "modelHarness")
	nm, err := json.Marshal(model)
	if err != nil {
		return nil, false
	}
	return nm, true
}

// normalizeEntriesHarness canonicalizes entries[].harness VALUES in place. It
// reports whether any entry harness was rewritten.
func normalizeEntriesHarness(entriesRaw json.RawMessage) (json.RawMessage, bool) {
	var entries []json.RawMessage
	if json.Unmarshal(entriesRaw, &entries) != nil {
		return nil, false
	}
	changed := false
	for i, entryRaw := range entries {
		var entry map[string]json.RawMessage
		if json.Unmarshal(entryRaw, &entry) != nil {
			continue
		}
		hv, has := entry["harness"]
		if !has {
			continue
		}
		var current string
		if json.Unmarshal(hv, &current) != nil {
			continue
		}
		canon := string(canonicalHarness(current))
		if canon == current {
			continue
		}
		h, err := json.Marshal(canon)
		if err != nil {
			continue
		}
		entry["harness"] = h
		ne, err := json.Marshal(entry)
		if err != nil {
			continue
		}
		entries[i] = ne
		changed = true
	}
	if !changed {
		return nil, false
	}
	out, err := json.Marshal(entries)
	if err != nil {
		return nil, false
	}
	return out, true
}

// keyedMutex provides per-key serialization for the migrate-on-read rewrite
// (N2 guard): concurrent reads of the SAME blob key must not race to re-store
// it. Different keys proceed concurrently.
type keyedMutex struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func newKeyedMutex() *keyedMutex {
	return &keyedMutex{locks: make(map[string]*sync.Mutex)}
}

// Lock acquires the lock for key and returns the unlock func.
func (k *keyedMutex) Lock(key string) func() {
	k.mu.Lock()
	m, ok := k.locks[key]
	if !ok {
		m = &sync.Mutex{}
		k.locks[key] = m
	}
	k.mu.Unlock()

	m.Lock()
	return m.Unlock
}
