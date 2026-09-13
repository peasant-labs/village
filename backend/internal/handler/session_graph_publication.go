package handler

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// decodePublicationDetail uses the contract's raw boundary, before pointer
// decoding can erase nulls or typed decoding can discard read-only fields.
// Native JSONL remains a legacy byte-preserving input, not a graph DTO.
//
// The durable graph is authoritative only for canonical content. A strict
// decode failure for legacy/opaque bytes (which the harness-aware content
// boundary routes to the historical dispatch and migrate-on-read path) carries
// no graph and is not a publish refusal; only content that claims the canonical
// public shape reaches the graph projection, so the failure stays strict there.
func decodePublicationDetail(raw []byte) (*schema.SessionDetailPayload, error) {
	detail, err := decodeCanonicalPublicationDetail(raw)
	if err == nil {
		return detail, nil
	}
	if _, boundaryErr := validateContentBoundary(raw, "", contentPublication); boundaryErr != nil {
		return nil, publicationDetailError(err)
	}
	return nil, nil
}

func decodeCanonicalPublicationDetail(raw []byte) (*schema.SessionDetailPayload, error) {
	switch sniffShape(bytes.TrimSpace(raw)) {
	case ShapeEnvelope:
		envelope, err := schema.DecodeTranscriptContentRaw(normalizeEnvelopeHarnessJSON(raw))
		if err != nil {
			return nil, err
		}
		return envelope.SessionDetail, nil
	case ShapeBarePayload:
		detail, err := schema.DecodeSessionDetailPayloadRaw(normalizeDetailHarnessJSON(raw))
		if err != nil {
			return nil, err
		}
		return &detail, nil
	default:
		return nil, nil
	}
}

// Normalize only the existing legacy harness alias before canonical validation;
// raw graph values and their lexical presence remain untouched.
func normalizeDetailHarnessJSON(raw []byte) []byte {
	// Do not let map decoding collapse duplicate keys or replace bad Unicode.
	// Returning the original leaves the canonical raw decoder to report it.
	if err := schema.ScanRawJSONDocument(raw, schema.RawJSONPathPolicy{}); err != nil {
		return raw
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return raw
	}
	var harness string
	_ = json.Unmarshal(object["harness"], &harness)
	if harness == "" {
		_ = json.Unmarshal(object["provider"], &harness)
	}
	if harness == "" {
		_ = json.Unmarshal(object["modelHarness"], &harness)
	}
	if harness == "" {
		return raw
	}
	canonical := canonicalHarness(harness)
	if string(canonical) == harness && len(object["harness"]) != 0 {
		return raw
	}
	object["harness"], _ = json.Marshal(canonical)
	normalized, err := json.Marshal(object)
	if err != nil {
		return raw
	}
	return normalized
}

func publicationDetailError(err error) error {
	return fmt.Errorf("Village publication validation failed in handler.decodePublicationDetail before scan, locks, or storage because the durable content violates the canonical schema; no transcript, object, audit, or share was changed; correct the producer payload and retry: %w", err)
}

func normalizeEnvelopeHarnessJSON(raw []byte) []byte {
	if err := schema.ScanRawJSONDocument(raw, schema.RawJSONPathPolicy{}); err != nil {
		return raw
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) != nil || envelope == nil {
		return raw
	}
	detail, present := envelope["sessionDetail"]
	if !present {
		return raw
	}
	normalized := normalizeDetailHarnessJSON(detail)
	if bytes.Equal(detail, normalized) {
		return raw
	}
	envelope["sessionDetail"] = normalized
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return raw
	}
	return encoded
}

// validatePublicationGraphMirrors checks the authoritative metadata against the
// durable detail using the schema builder rather than a second graph validator.
// Count presence is not evidence of a producer format: legacy turn totals keep
// their existing meaning, so the builder's projected-main flag stays false.
func validatePublicationGraphMirrors(detail *schema.SessionDetailPayload, req *schema.PublishRequest, authoritative *schema.AuthoritativePublishRequest) error {
	if detail == nil {
		if req.Stats.InputSubmissionCount != nil || authoritative != nil && (authoritative.Identity.RootSessionID != nil || authoritative.Identity.Purpose != "" || len(authoritative.Identity.Relationships) != 0) {
			return publicationDetailError(fmt.Errorf("graph or input-count metadata has no durable session detail to validate its mirrors against"))
		}
		return nil
	}
	if !containsContentCapability(schema.RequiredContentCapabilities(*detail), schema.ContentCapabilitySessionGraphProvenanceV1) && req.Stats.InputSubmissionCount == nil && (authoritative == nil || authoritative.Identity.RootSessionID == nil && authoritative.Identity.Purpose == "" && len(authoritative.Identity.Relationships) == 0) {
		return nil
	}
	metadata := schema.UnifiedMetadata{
		SessionID: req.Identity.SessionID, ParentUUID: req.Identity.ParentSessionID,
		ModelHarness: req.Model.Harness, Stats: req.Stats,
	}
	if authoritative != nil {
		metadata.RootSessionID = authoritative.Identity.RootSessionID
		metadata.Purpose = authoritative.Identity.Purpose
		metadata.Relationships = authoritative.Identity.Relationships
	}
	identity, _, _, err := schema.BuildAuthoritativePublicationProjections(*detail, metadata, req.Identity.SchemaVersion, false)
	if err != nil {
		return publicationDetailError(err)
	}
	req.Identity.ParentSessionID = identity.ParentSessionID
	return nil
}

// installPublicationGraph projects only the already validated durable authority.
// It never infers count or parentage from rendered turns or a nullable FK.
func installPublicationGraph(params *sqlc.CreateTranscriptParams, detail *schema.SessionDetailPayload) error {
	params.SessionRelationships = []byte("[]")
	if detail == nil {
		return nil
	}
	params.InputSubmissionCount = int64PtrToPgInt8(detail.InputSubmissionCount)
	params.RootSessionID = sessionIDPtrToPgText(detail.RootSessionID)
	params.SessionPurpose = requiredStringToPgText(string(detail.Purpose))
	if len(detail.Relationships) != 0 {
		encoded, err := json.Marshal(detail.Relationships)
		if err != nil {
			return publicationDetailError(err)
		}
		params.SessionRelationships = encoded
	}
	return nil
}
