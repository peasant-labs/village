package handler

import (
	"errors"
	"testing"

	"github.com/peasant-labs/schema"
)

type droppingContentPartsEncoder struct{}
type droppingEarlierContentPartsEncoder struct{}

func (droppingContentPartsEncoder) Encode(version schema.PushContractVersion, payload *schema.SessionDetailPayload) ([]byte, error) {
	for i := range payload.Turns {
		payload.Turns[i].ContentParts = nil
	}
	return productionContentRewriteEncoder.Encode(version, payload)
}

func (droppingEarlierContentPartsEncoder) Encode(version schema.PushContractVersion, payload *schema.SessionDetailPayload) ([]byte, error) {
	for i := range payload.EarlierHistory {
		for j := range payload.EarlierHistory[i].Turns {
			payload.EarlierHistory[i].Turns[j].ContentParts = nil
		}
	}
	return productionContentRewriteEncoder.Encode(version, payload)
}

func TestContentPartsPreservationProof(t *testing.T) {
	if err := proveContentParts(productionContentRewriteEncoder); err != nil {
		t.Fatal(err)
	}
	err := proveContentParts(droppingContentPartsEncoder{})
	if err == nil {
		t.Fatal("lossy rewrite passed the preservation proof")
	}
	if err := proveContentParts(droppingEarlierContentPartsEncoder{}); err == nil {
		t.Fatal("lossy earlier-history rewrite passed the preservation proof")
	}
	capabilities := advertisedContentCapabilitiesWithParts(fixedPreservationEvaluator{}, fixedProvenancePreservationEvaluator{}, fixedThinkingLevelPreservationEvaluator{}, fixedPreservationEvaluator{err: errors.New("loss")})
	if containsContentCapability(capabilities, schema.ContentCapabilityContentPartsV1) {
		t.Fatalf("lossy proof advertised content parts: %v", capabilities)
	}
}
