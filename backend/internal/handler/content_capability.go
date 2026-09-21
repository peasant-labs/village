package handler

import "github.com/peasant-labs/schema"

// advertisedContentCapabilities returns the deployment's closed content
// capability inventory with every gating preservation proof applied. The base
// enriched-content proof gates the shared evidence tokens; the provenance
// preservation proof independently gates the sealed session_graph_provenance_v1
// token for durable session-relationship evidence. A deployment whose proof
// fails advertises less, never more.
func advertisedContentCapabilities() []schema.ContentCapability {
	return advertisedContentCapabilitiesWithEvaluators(
		productionObservedModelPreservationEvaluator{},
		productionProvenancePreservationEvaluator{},
	)
}

func (h *Handler) preservationProof() observedModelPreservationEvaluator {
	if h.preservationEvaluator != nil {
		return h.preservationEvaluator
	}
	return productionObservedModelPreservationEvaluator{}
}

func (h *Handler) provenanceProof() provenancePreservationEvaluator {
	if h.provenanceEvaluator != nil {
		return h.provenanceEvaluator
	}
	return productionProvenancePreservationEvaluator{}
}

// advertisedContentCapabilitiesWithEvaluator applies the base enriched-content
// proof and the production provenance proof. It is the seam tests use to drive
// the base gate.
func advertisedContentCapabilitiesWithEvaluator(evaluator observedModelPreservationEvaluator) []schema.ContentCapability {
	return advertisedContentCapabilitiesWithEvaluators(evaluator, productionProvenancePreservationEvaluator{})
}

// advertisedContentCapabilitiesWithEvaluators is fail-closed per proof.
//
// A failing base proof withholds the WHOLE advertisement: the shared evidence
// tokens are only trustworthy together, and a producer that cannot see them must
// refuse to emit enriched content.
//
// A failing provenance proof withholds ONLY the provenance token. The shared
// evidence tokens keep their own passing proof and stay advertised because a
// session with observedModel, detailed usage, or native metadata but no
// durable session-relationship evidence is still safe to publish; dropping those
// tokens too would refuse content Village can in fact preserve. The provenance
// token stays absent until its proof passes, so a producer correctly refuses to
// emit the relationship evidence that would otherwise be lost.
//
// The returned list is always the closed inventory in canonical lexicographic
// order, so it always satisfies schema.ValidateContentCapabilityAdvertisements.
func advertisedContentCapabilitiesWithEvaluators(evaluator observedModelPreservationEvaluator, provenance provenancePreservationEvaluator) []schema.ContentCapability {
	if err := evaluator.Evaluate(); err != nil {
		return []schema.ContentCapability{}
	}
	capabilities := []schema.ContentCapability{
		schema.ContentCapabilityDetailedUsageV1,
		schema.ContentCapabilityNativeMetadataV1,
		schema.ContentCapabilityObservedModelV1,
		schema.ContentCapabilityRetainedUnknownV1,
	}
	if err := provenance.Evaluate(); err == nil {
		capabilities = append(capabilities, schema.ContentCapabilitySessionGraphProvenanceV1)
	}
	return append(capabilities, schema.ContentCapabilityToolNamespaceV1)
}
