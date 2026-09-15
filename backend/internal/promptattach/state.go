// Package promptattach carries the pull request prompt-attachment state
// machine: the closed state menu the database CHECK constrains, the closed
// transition table the lifecycle allows, and the one function permitted to
// change an attachment's state.
//
// It is deliberately dependency-light: State and CheckMode are pure values, so
// fixtures can exhaust the transition table without a database, while
// Transition is the single writer of pull_request_attachments.state.
package promptattach

import (
	"fmt"
	"strings"
)

// State is the closed menu of attachment lifecycle states. Its values are the
// exact tokens migration 037's pull_request_attachments_state_menu CHECK
// accepts, so a value that round-trips through the database is always one of
// them.
type State string

const (
	// Requested: a non-author with write access asked. Nothing is exposed.
	Requested State = "requested"

	// Waiting: the author consented, but no accepted matching transcript exists
	// yet. The request completes on a later matching publish, with no second click.
	Waiting State = "waiting"

	// Preview: the digest is computed, but nothing is posted and no visibility
	// changed. A public repository or the author's preview_before_attach setting
	// lands here; confirm on Village moves it to Attached.
	Preview State = "preview"

	// Attached: the comment is posted, the check is green, and the transcripts
	// are shared. A new push or a new accepted matching publish keeps it
	// Attached with the digest recomputed.
	Attached State = "attached"

	// Detached: the comment is deleted, the check is reset, and each attached
	// transcript's recorded previous visibility is restored. The row survives
	// so a later author click can re-enter the machine.
	Detached State = "detached"
)

// All is the canonical menu, in the order the database CHECK lists it. Tests
// derive their accept sets from this slice, so widening the menu is a single
// edit here plus one migration.
var All = []State{Requested, Waiting, Preview, Attached, Detached}

// Valid reports whether s is one of the menu values.
func (s State) Valid() bool {
	switch s {
	case Requested, Waiting, Preview, Attached, Detached:
		return true
	}
	return false
}

// Validate is the fail-closed trust boundary for a value that arrived from
// outside this package: a database column, a webhook field, or a request
// parameter. It never guesses a default - a wrong state would either expose a
// digest that was never confirmed or silently drop an author's consent.
func (s State) Validate() error {
	if s.Valid() {
		return nil
	}
	return fmt.Errorf("pull request attachment state validation failed because value %q is not one of %s in promptattach.State.Validate at a trust boundary between stored or supplied data and the caller; no transition was applied and no value was substituted, because guessing a state would either expose an unconfirmed digest or drop recorded consent; repair the stored or supplied value to one menu member and retry", string(s), Menu())
}

func (s State) String() string { return string(s) }

// Parse validates an untrusted string and returns the menu member it names.
func Parse(value string) (State, error) {
	candidate := State(value)
	if err := candidate.Validate(); err != nil {
		return "", err
	}
	return candidate, nil
}

// Menu renders the accepted values for an actionable error message.
func Menu() string {
	names := make([]string, 0, len(All))
	for _, state := range All {
		names = append(names, string(state))
	}
	return strings.Join(names, ", ")
}

// allowedTransitions is the closed transition table. A transition is legal only
// when this map names it. The lifecycle, one row per source state:
//
//	requested -> attached | preview | waiting   (author click)
//	waiting   -> attached | preview | detached  (matching publish / detach)
//	preview   -> attached | detached            (confirm / detach)
//	attached  -> attached | detached            (new push stays, digest recomputed; detach)
//	detached  -> attached | preview | waiting   (author click re-enters)
//
// Every other pair is refused, including every self-transition except
// attached -> attached (a push refreshes an attached digest in place).
var allowedTransitions = map[State]map[State]bool{
	Requested: {Attached: true, Preview: true, Waiting: true},
	Waiting:   {Attached: true, Preview: true, Detached: true},
	Preview:   {Attached: true, Detached: true},
	Attached:  {Attached: true, Detached: true},
	Detached:  {Attached: true, Preview: true, Waiting: true},
}

// CanTransition reports whether the state machine permits from -> to. A state
// outside the menu permits nothing, so an unexpected stored value fails closed.
func CanTransition(from, to State) bool {
	targets, ok := allowedTransitions[from]
	if !ok {
		return false
	}
	return targets[to]
}

// CheckMode is the closed menu for a collective's prompts-check mode, mirroring
// the groups_prompts_check_mode_menu CHECK from migration 037.
type CheckMode string

const (
	// Informational: the check reports, but never blocks a merge.
	Informational CheckMode = "informational"

	// Required: the check participates in the branch protection decision.
	Required CheckMode = "required"
)

// AllCheckModes is the canonical menu, in the order the database CHECK lists it.
var AllCheckModes = []CheckMode{Informational, Required}

// Valid reports whether m is one of the check-mode menu values.
func (m CheckMode) Valid() bool {
	switch m {
	case Informational, Required:
		return true
	}
	return false
}

// Validate fails closed on a check-mode value from outside this package.
func (m CheckMode) Validate() error {
	if m.Valid() {
		return nil
	}
	return fmt.Errorf("prompts check mode validation failed because value %q is not one of %s in promptattach.CheckMode.Validate at a trust boundary between stored or supplied data and the caller; no setting was changed and no value was substituted", string(m), CheckModeMenu())
}

func (m CheckMode) String() string { return string(m) }

// CheckModeMenu renders the accepted check modes for an error message.
func CheckModeMenu() string {
	names := make([]string, 0, len(AllCheckModes))
	for _, mode := range AllCheckModes {
		names = append(names, string(mode))
	}
	return strings.Join(names, ", ")
}
