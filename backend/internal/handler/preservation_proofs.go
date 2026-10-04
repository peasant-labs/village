package handler

import (
	"errors"
	"sync"
)

// preservationProofVerdicts carries the three boot-time preservation verdicts.
// The base verdict gates the shared enriched-content tokens; the provenance
// verdict independently gates the sealed session_graph_provenance_v1 token;
// the thinking-level verdict independently gates thinking_level_v1.
// A failing proof withholds its advertisement (base failure withholds the
// whole advertisement; provenance or thinking-level failure withholds only its
// own token) and refuses matching publishes before any write.
type preservationProofVerdicts struct {
	base          error
	provenance    error
	thinkingLevel error
}

var (
	preservationProofsOnce   sync.Once
	cachedPreservationProofs preservationProofVerdicts
)

// EvaluatePreservationProofs evaluates the production preservation proofs once
// per process and caches all three verdicts. Call it at server boot before the
// listener starts so runtime capability decisions read the cached verdicts
// instead of re-running proofs on the request path. A failing proof does not
// abort boot; it only withholds its advertisement and refuses matching
// publishes. The first production-evaluator call evaluates once as well, so
// existing request-path callers see no behavior change when boot has not run.
//
// The uncached entry points (executeObservedModelPreservationProof,
// executeProvenancePreservationProof, executeThinkingLevelPreservationProof) and the evaluator-injecting capability
// functions (advertisedContentCapabilitiesWithEvaluator,
// advertisedContentCapabilitiesWithEvaluators) stay uncached for tests.
func EvaluatePreservationProofs() (baseErr error, provenanceErr error, thinkingLevelErr error) {
	preservationProofsOnce.Do(func() {
		cachedPreservationProofs = preservationProofVerdicts{
			base:          errors.Join(executeObservedModelPreservationProof(productionContentRewriteEncoder), provePiPreservation(productionContentRewriteEncoder), proveContentBoundary(validateContentBoundary), proveRetainedUnknownPreservation(productionContentRewriteEncoder)),
			provenance:    executeProvenancePreservationProof(productionContentRewriteEncoder),
			thinkingLevel: executeThinkingLevelPreservationProof(productionContentRewriteEncoder),
		}
	})
	return cachedPreservationProofs.base, cachedPreservationProofs.provenance, cachedPreservationProofs.thinkingLevel
}
