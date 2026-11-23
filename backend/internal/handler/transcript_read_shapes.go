package handler

import (
	"fmt"
	"regexp"

	"github.com/peasant-labs/village/backend/internal/storage"
)

// This file names the two closed sets the paged-read parity check is built
// over: the stored row state of a transcript's content identity and the
// audience kind a viewer reaches it through. A read shape is one pair. The
// synthetic parity corpus has one case per pair, and the production census
// counts rows per pair, so both read the sets from here.

// transcriptRowState is the closed set of stored content-identity states. A is
// the (content_hash, blob_size_bytes) pair, B the (segmented_identity_hash,
// segmented_identity_size) pair, R the content revision.
type transcriptRowState uint8

const (
	// rowStateNew: A valid, B NULL, R >= 1, written by the current publisher
	// (it records a publish metadata generation). Reads authenticate against A.
	rowStateNew transcriptRowState = iota + 1
	// rowStateHashOnly: a hash with a NULL or negative size, or no hash with a
	// negative size; B NULL, R = 1. Reads refuse as the whole-document read does.
	rowStateHashOnly
	// rowStateUntracked: A and B NULL, R = 1. The first read authenticates and
	// installs B.
	rowStateUntracked
	// rowStateExposedLegacy: A NULL, B valid, R >= 1. Reads authenticate against B.
	rowStateExposedLegacy
	// rowStateLegacyKnown: A valid, B NULL, R >= 1, with no publish metadata
	// generation (a row published before the publisher recorded one).
	rowStateLegacyKnown
	// rowStateMalformedHash: a non-canonical hash, B NULL, R = 1. Reads refuse.
	rowStateMalformedHash
	// rowStateDivergent: A and B NULL with R > 1, which no writer produces.
	rowStateDivergent

	rowStateCount // sentinel; not a state
)

// allTranscriptRowStates is every member of the closed set, in declaration
// order. A member added above is included without another edit.
func allTranscriptRowStates() []transcriptRowState {
	states := make([]transcriptRowState, 0, int(rowStateCount)-1)
	for s := rowStateNew; s < rowStateCount; s++ {
		states = append(states, s)
	}
	return states
}

func (s transcriptRowState) String() string {
	switch s {
	case rowStateNew:
		return "n"
	case rowStateHashOnly:
		return "n2-l4"
	case rowStateUntracked:
		return "l1"
	case rowStateExposedLegacy:
		return "l2"
	case rowStateLegacyKnown:
		return "l3"
	case rowStateMalformedHash:
		return "l1m"
	case rowStateDivergent:
		return "l1d"
	case 0:
		return "unexplained"
	default:
		return fmt.Sprintf("transcriptRowState(%d)", uint8(s))
	}
}

// identitySizeClass is how a stored size column reads: absent, negative, or a
// usable nonnegative size.
type identitySizeClass uint8

const (
	identitySizeNull identitySizeClass = iota
	identitySizeNegative
	identitySizeUsable
)

// rowIdentityFacts are the content-free column facts that decide a row state.
// The census reads exactly these, so it never selects a hash, a key or a body.
type rowIdentityFacts struct {
	HashNull      bool
	HashCanonical bool
	Size          identitySizeClass
	SegmentedHash bool // segmented_identity_hash is set
	SegmentedSize bool // segmented_identity_size is set
	Revision      int64
	Metadata      bool // a publish metadata generation is recorded
}

// storedContentHashSyntax is the storage layer's hash syntax, shared so that a
// census in SQL and a classification in Go read one pattern.
var storedContentHashSyntax = regexp.MustCompile(storage.CanonicalContentHashPattern)

// classifyTranscriptRowState maps column facts to their state. ok is false
// for a combination the closed set does not explain; the census reports those
// rows as unexplained rather than forcing them into a state.
func classifyTranscriptRowState(f rowIdentityFacts) (transcriptRowState, bool) {
	if f.Revision < 1 {
		return 0, false
	}
	if f.SegmentedHash || f.SegmentedSize {
		if f.SegmentedHash && f.SegmentedSize && f.HashNull {
			return rowStateExposedLegacy, true
		}
		return 0, false
	}
	switch {
	case f.HashNull && f.Size == identitySizeNegative:
		return rowStateHashOnly, f.Revision == 1
	case f.HashNull && f.Revision == 1:
		return rowStateUntracked, true
	case f.HashNull:
		return rowStateDivergent, true
	case !f.HashCanonical:
		return rowStateMalformedHash, f.Revision == 1
	case f.Size != identitySizeUsable:
		return rowStateHashOnly, f.Revision == 1
	case f.Metadata:
		return rowStateNew, true
	default:
		return rowStateLegacyKnown, true
	}
}

// transcriptAudienceKind is the closed set of grant paths transcriptViewAccess
// has, in its own order. A viewer reaches a readable transcript through
// exactly one kind of viewer relationship:
//
//   - public: an anonymous viewer of a public transcript;
//   - owner: the transcript's owner, whatever the visibility;
//   - collective-member: a member with the contributors read role of a
//     collective holding an approved share of a shared transcript;
//   - collective-owner-preview: the owner of a collective the transcript is
//     submitted to, including a submission still awaiting approval.
//
// Everything else is the identical nondisclosing 404. A grant path added to
// transcriptViewAccess is added here, which extends the parity corpus and the
// census without another edit.
type transcriptAudienceKind uint8

const (
	audiencePublic transcriptAudienceKind = iota + 1
	audienceOwner
	audienceCollectiveMember
	audienceCollectiveOwnerPreview

	audienceKindCount // sentinel; not a kind
)

func allTranscriptAudienceKinds() []transcriptAudienceKind {
	kinds := make([]transcriptAudienceKind, 0, int(audienceKindCount)-1)
	for k := audiencePublic; k < audienceKindCount; k++ {
		kinds = append(kinds, k)
	}
	return kinds
}

func (k transcriptAudienceKind) String() string {
	switch k {
	case audiencePublic:
		return "public"
	case audienceOwner:
		return "owner"
	case audienceCollectiveMember:
		return "collective-member"
	case audienceCollectiveOwnerPreview:
		return "collective-owner-preview"
	default:
		return fmt.Sprintf("transcriptAudienceKind(%d)", uint8(k))
	}
}

// transcriptReadShape is one (row state, audience kind) pair.
type transcriptReadShape struct {
	State    transcriptRowState
	Audience transcriptAudienceKind
}

func (s transcriptReadShape) String() string { return s.State.String() + "-" + s.Audience.String() }

// allTranscriptReadShapes is the product of the two closed sets.
func allTranscriptReadShapes() []transcriptReadShape {
	var shapes []transcriptReadShape
	for _, state := range allTranscriptRowStates() {
		for _, audience := range allTranscriptAudienceKinds() {
			shapes = append(shapes, transcriptReadShape{State: state, Audience: audience})
		}
	}
	return shapes
}
