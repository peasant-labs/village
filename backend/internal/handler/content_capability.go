package handler

import "github.com/peasant-labs/schema"

func advertisedContentCapabilities() []schema.ContentCapability {
	return advertisedContentCapabilitiesWithEvaluator(productionObservedModelPreservationEvaluator{})
}

func (h *Handler) preservationProof() observedModelPreservationEvaluator {
	if h.preservationEvaluator != nil {
		return h.preservationEvaluator
	}
	return productionObservedModelPreservationEvaluator{}
}

func advertisedContentCapabilitiesWithEvaluator(evaluator observedModelPreservationEvaluator) []schema.ContentCapability {
	if err := evaluator.Evaluate(); err != nil {
		return []schema.ContentCapability{}
	}
	return []schema.ContentCapability{schema.ContentCapabilityObservedModelV1}
}
