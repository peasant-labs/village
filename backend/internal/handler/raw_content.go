package handler

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/peasant-labs/schema"
)

type contentBoundaryMode uint8

const (
	contentPublication contentBoundaryMode = iota
	contentStoredRead
)

type contentBoundary struct {
	shape     EnvelopeShape
	canonical *schema.SessionDetailPayload
	observed  bool
}

// validateContentBoundary dispatches from original bytes, never from a failed
// strict decode. Historical formats remain historical; Pi identity, trusted Pi
// context, and structural new-evidence presence always require the public root.
func validateContentBoundary(raw []byte, knownHarness string, mode contentBoundaryMode) (contentBoundary, error) {
	var result contentBoundary
	trimmed := bytes.TrimSpace(raw)
	knownPi := knownHarness == string(schema.HarnessPi)
	policy := schema.RawJSONPathPolicy{
		MaxDocumentBytes:       8 << 20,
		MaxDocumentDepth:       64,
		OpaqueMetadataPointers: []string{"/nativeMetadata/*/data", "/sessionDetail/nativeMetadata/*/data"},
	}
	if len(raw) > policy.MaxDocumentBytes {
		return result, schema.ScanRawJSONDocument(raw, policy)
	}
	jsonShaped := len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[')
	if !jsonShaped && !knownPi {
		// Opaque publication/raw pull predates typed content. This is decided
		// before parsing, never used as a fallback for malformed JSON.
		return result, nil
	}
	documents := [][]byte{raw}
	if jsonShaped && !json.Valid(raw) {
		documents = nil
		for _, line := range bytes.Split(raw, []byte("\n")) {
			if len(bytes.TrimSpace(line)) > 0 {
				documents = append(documents, line)
			}
		}
	}
	for _, document := range documents {
		if err := schema.ScanRawJSONDocument(document, policy); err != nil {
			return result, err
		}
	}
	result.shape = sniffShape(trimmed)
	if len(documents) > 1 {
		result.shape = ShapeRawJSONL
	}
	var root map[string]json.RawMessage
	if result.shape != ShapeRawJSONL {
		if err := json.Unmarshal(raw, &root); err != nil {
			return result, err
		}
		if _, present := root["sessionDetail"]; present {
			result.shape = ShapeEnvelope
		}
	}
	detailRaw := json.RawMessage(raw)
	if result.shape == ShapeEnvelope {
		detailRaw = root["sessionDetail"]
	}
	var detail map[string]json.RawMessage
	_ = json.Unmarshal(detailRaw, &detail)
	strict, namespace := publicEvidencePresence(detail)
	piIdentity := publicPiIdentity(detail)
	strict = strict || knownPi || piIdentity
	if namespace {
		return result, fmt.Errorf("tool namespace preservation is unavailable in handler.validateContentBoundary before decoding or storage because the pinned public contract cannot retain namespace; no content was served or written; use a Village release advertising tool_namespace_v1 after its canonical Schema release is available, then retry")
	}
	if strict {
		if knownPi || piIdentity {
			var harness string
			_ = json.Unmarshal(detail["harness"], &harness)
			if harness != string(schema.HarnessPi) {
				return result, fmt.Errorf("Pi content validation failed in handler.validateContentBoundary before decoding or storage because Pi identity or trusted context disagrees with the canonical public harness; no content was served or written; republish a complete Pi session detail without changing its harness")
			}
		}
		var payload schema.SessionDetailPayload
		var err error
		if result.shape == ShapeEnvelope {
			var envelope schema.TranscriptContent
			envelope, err = schema.DecodeTranscriptContentRaw(raw)
			if err == nil {
				payload = *envelope.SessionDetail
			}
		} else {
			payload, err = schema.DecodeSessionDetailPayloadRaw(raw)
		}
		if err != nil {
			return result, err
		}
		// New evidence never receives the historical non-assistant allowance.
		if err := validateObservedModelEvidence(&payload); err != nil {
			return result, err
		}
		result.canonical = &payload
		return result, nil
	}
	// Public envelopes/bare detail nested in legacy records cannot be projected
	// into role/content and silently lose evidence. Inspect JSON structure only,
	// never the text in provider content or tool arguments/results.
	for _, document := range documents {
		var value any
		decoder := json.NewDecoder(bytes.NewReader(document))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			return result, err
		}
		if embeddedPublicRoot(value, result.shape != ShapeRawJSONL) {
			return result, fmt.Errorf("transcript routing failed in handler.validateContentBoundary before legacy projection because public detail was embedded in legacy records; no content was served, rewritten or written; republish one public content envelope and retry")
		}
	}
	observed, err := inspectObservedModelMembers(raw)
	if err != nil {
		return result, err
	}
	result.observed = observed
	if observed {
		var payload schema.SessionDetailPayload
		if err := json.Unmarshal(detailRaw, &payload); err != nil {
			return result, err
		}
		if mode == contentPublication {
			err = validateObservedModelEvidence(&payload)
		} else {
			err = validateObservedModelValues(&payload)
		}
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

func publicPiIdentity(detail map[string]json.RawMessage) bool {
	for _, key := range []string{"harness", "provider", "modelHarness"} {
		var harness string
		if json.Unmarshal(detail[key], &harness) == nil && harness == string(schema.HarnessPi) {
			return true
		}
	}
	return false
}

// Only public structural paths participate. Presence includes null and empty
// values; deriving this from typed capabilities would erase those markers.
func publicEvidencePresence(detail map[string]json.RawMessage) (strict, namespace bool) {
	_, strict = detail["nativeMetadata"]
	var turns []map[string]json.RawMessage
	_ = json.Unmarshal(detail["turns"], &turns)
	for _, turn := range turns {
		if _, ok := turn["usage"]; ok {
			strict = true
		}
		if _, ok := turn["sourceEntryRef"]; ok {
			strict = true
		}
		var tools []map[string]json.RawMessage
		_ = json.Unmarshal(turn["toolCalls"], &tools)
		for _, tool := range tools {
			for _, key := range []string{"usage", "callEntryRef", "resultEntryRef"} {
				if _, ok := tool[key]; ok {
					strict = true
				}
			}
			if _, ok := tool["namespace"]; ok {
				strict = true
				namespace = true
			}
		}
	}
	return strict, namespace
}

func embeddedPublicRoot(value any, allowRoot bool) bool {
	switch node := value.(type) {
	case map[string]any:
		if !allowRoot {
			if _, ok := node["contractVersion"]; ok {
				return true
			}
			if _, ok := node["sessionDetail"]; ok {
				return true
			}
			if _, ok := node["turns"]; ok {
				return true
			}
		}
		for key, child := range node {
			// The one outer envelope and its detail are not embedded records.
			if embeddedPublicRoot(child, allowRoot && key == "sessionDetail") {
				return true
			}
		}
	case []any:
		for _, child := range node {
			if embeddedPublicRoot(child, false) {
				return true
			}
		}
	}
	return false
}
