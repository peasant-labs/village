// Package contentrepair implements the operator-only transcript content repair.
package contentrepair

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/storage"
)

type Action string

const (
	ClearMalformedIdentity    Action = "clear_malformed_identity"
	RestoreAttestedGeneration Action = "restore_attested_generation"
	ReplaceThirdValue         Action = "replace_third_value"
	DeclareUnavailable        Action = "declare_unavailable"
)

var AllActions = []Action{ClearMalformedIdentity, RestoreAttestedGeneration, ReplaceThirdValue, DeclareUnavailable}

func (a Action) IsValid() bool {
	switch a {
	case ClearMalformedIdentity, RestoreAttestedGeneration, ReplaceThirdValue, DeclareUnavailable:
		return true
	default:
		return false
	}
}
func (a Action) String() string { return string(a) }

type Phase string

const (
	Attempted Phase = "attempted"
	Applied   Phase = "applied"
	Refused   Phase = "refused"
)

var AllPhases = []Phase{Attempted, Applied, Refused}

func (p Phase) IsValid() bool {
	switch p {
	case Attempted, Applied, Refused:
		return true
	default:
		return false
	}
}
func (p Phase) String() string { return string(p) }

type FailureClass string

const (
	PreconditionMismatch FailureClass = "precondition_mismatch"
	ObjectMismatch       FailureClass = "object_mismatch"
	ObjectUncertain      FailureClass = "object_uncertain"
	GuardRefused         FailureClass = "guard_refused"
	WriterFenceRefused   FailureClass = "writer_fence_refused"
	ObjectWriteFailed    FailureClass = "object_write_failed"
)

var AllFailureClasses = []FailureClass{PreconditionMismatch, ObjectMismatch, ObjectUncertain, GuardRefused, WriterFenceRefused, ObjectWriteFailed}

func (f FailureClass) IsValid() bool {
	switch f {
	case PreconditionMismatch, ObjectMismatch, ObjectUncertain, GuardRefused, WriterFenceRefused, ObjectWriteFailed:
		return true
	default:
		return false
	}
}
func (f FailureClass) String() string { return string(f) }

// RawOptions are the untrusted command-line values. ParseOptions performs no
// database, object-store or key-custody operation.
type RawOptions struct {
	RepairID          string
	TranscriptID      string
	Action            string
	ActorID           string
	AuthorizationRef  string
	Reason            string
	ExpectedRevision  int64
	SourceKey         string
	SourceSupplied    bool
	Reconciles        string
	ReconcileSupplied bool
}

type Options struct {
	RepairID         uuid.UUID
	TranscriptID     uuid.UUID
	Action           Action
	ActorID          uuid.UUID
	AuthorizationRef string
	Reason           string
	ExpectedRevision int64
	SourceKey        storage.ObjectKey
	Reconciles       uuid.UUID
}

func ParseOptions(raw RawOptions) (Options, error) {
	var result Options
	var err error
	invalid := func(flag string) (Options, error) {
		return Options{}, fmt.Errorf("operator content repair validation failed for %s before any database or storage access; no repair was attempted; correct the flag using the Operator content repair runbook", flag)
	}
	if result.RepairID, err = uuid.Parse(raw.RepairID); err != nil || result.RepairID == uuid.Nil {
		return invalid("-repair-id")
	}
	if result.TranscriptID, err = uuid.Parse(raw.TranscriptID); err != nil || result.TranscriptID == uuid.Nil {
		return invalid("-repair-transcript")
	}
	if result.ActorID, err = uuid.Parse(raw.ActorID); err != nil || result.ActorID == uuid.Nil || database.IsReservedSystemID(result.ActorID.String()) {
		return invalid("-repair-actor")
	}
	result.Action = Action(strings.ReplaceAll(raw.Action, "-", "_"))
	// Require the documented kebab spelling, not the internal ledger spelling.
	if !result.Action.IsValid() || raw.Action != strings.ReplaceAll(result.Action.String(), "_", "-") {
		return invalid("-repair-action")
	}
	result.AuthorizationRef = strings.TrimSpace(raw.AuthorizationRef)
	result.Reason = strings.TrimSpace(raw.Reason)
	if n := utf8.RuneCountInString(result.AuthorizationRef); !utf8.ValidString(result.AuthorizationRef) || n < 1 || n > 256 {
		return invalid("-repair-authorization-ref")
	}
	if n := utf8.RuneCountInString(result.Reason); !utf8.ValidString(result.Reason) || n < 1 || n > 1000 {
		return invalid("-repair-reason")
	}
	if raw.ExpectedRevision < 1 {
		return invalid("-repair-expect-revision")
	}
	result.ExpectedRevision = raw.ExpectedRevision
	if result.Action == RestoreAttestedGeneration {
		if !raw.SourceSupplied {
			return invalid("-repair-source-key")
		}
		if result.SourceKey, err = storage.NewObjectKey(raw.SourceKey); err != nil {
			return invalid("-repair-source-key")
		}
	} else if raw.SourceSupplied {
		return invalid("-repair-source-key")
	}
	if raw.ReconcileSupplied {
		if result.Reconciles, err = uuid.Parse(raw.Reconciles); err != nil || result.Reconciles == uuid.Nil {
			return invalid("-repair-reconciles")
		}
	}
	return result, nil
}
