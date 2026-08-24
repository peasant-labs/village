// Package sessionorigin classifies who drove a published session and carries
// the closed menu of that classification across the database, the publish
// path, the discovery listing, and the maintenance backfill.
//
// The package is deliberately dependency-free apart from the wire contract, so
// the classification rule is one pure function that every caller shares and
// that fixtures can exercise without a database, object storage, or a handler.
package sessionorigin

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/peasant-labs/redact"
	"github.com/peasant-labs/schema"
)

// Origin is the closed menu of session origins. Its values are the exact
// tokens the transcripts.session_origin CHECK constraint accepts, so a value
// that round-trips through the database is always one of them.
type Origin string

const (
	// User marks a session a person drove in-band: it carries a real user turn
	// with content, a slash-command invocation, or both. This is the ordinary
	// case and is never demoted.
	User Origin = "user"

	// Agent marks a session with no user prompt and no command invocation
	// anywhere in which assistant or tool work still happened, so something
	// other than a person in-band drove it. Only this value is collapsed out of
	// root-level lists.
	Agent Origin = "agent"

	// Unknown marks a session whose origin could not be established: content
	// with no user prompt, no command invocation, and no assistant or tool work
	// at all, or content that could not be read. Unknown is the fail-safe value
	// and behaves exactly like User.
	Unknown Origin = "unknown"
)

// All is the canonical menu, in the order the database CHECK lists it. Tests
// and the backfill derive their accept sets from this slice so widening the
// menu is a single edit here plus one migration.
var All = []Origin{User, Agent, Unknown}

// Valid reports whether o is one of the menu values.
func (o Origin) Valid() bool {
	switch o {
	case User, Agent, Unknown:
		return true
	}
	return false
}

// Validate is the fail-closed trust boundary for a value that arrived from
// outside this package: a database column, a request parameter, or an operator
// argument. It never guesses a default, because guessing "user" would publish
// an agent session into every root-level list and guessing "agent" would hide
// a real person's session.
func (o Origin) Validate() error {
	if o.Valid() {
		return nil
	}
	return fmt.Errorf("session origin validation failed because value %q is not one of %s in sessionorigin.Origin.Validate at a trust boundary between stored or supplied data and the caller; nothing was listed, published, or updated with it, and no value was substituted because both substitutions are wrong (user would expose an agent session in every list, agent would hide a person's session); repair the stored or supplied value to one menu member and retry", string(o), Menu())
}

func (o Origin) String() string { return string(o) }

// Parse validates an untrusted string and returns the menu member it names.
func Parse(value string) (Origin, error) {
	candidate := Origin(value)
	if err := candidate.Validate(); err != nil {
		return "", err
	}
	return candidate, nil
}

// Menu renders the accepted values for an actionable error message.
func Menu() string {
	names := make([]string, 0, len(All))
	for _, origin := range All {
		names = append(names, string(origin))
	}
	return strings.Join(names, ", ")
}

// commandInvocationPrefix matches the opening tag of a harness command wrapper
// at the very start of a turn. The wrapper names come from redact, the one
// package that owns harness markup names, so this file never spells a tag
// literal of its own. Attributes are tolerated but must be preceded by
// whitespace, so a longer unlisted tag name can never match a listed one.
var commandInvocationPrefix = regexp.MustCompile(
	`^<(?:` + regexp.QuoteMeta(redact.WrapperCommandName) + `|` + regexp.QuoteMeta(redact.WrapperCommandMessage) + `)(?:\s[^>]*)?>`)

// isCommandInvocation reports whether a turn opens with a command wrapper, in
// which case a person typed a slash command. The turn's wire role is
// deliberately ignored: Peasant projects command turns to the system role on
// newer payloads and recorded them as user turns on older ones, and both shapes
// describe the same human action.
func isCommandInvocation(content string) bool {
	return commandInvocationPrefix.MatchString(strings.TrimSpace(content))
}

// Classify is the single classification rule, shared by the publish path and
// the maintenance backfill so a stored value and a freshly published one can
// never disagree.
//
// The rule reads turn roles and the opening markup of turn content:
//
//   - At least one user turn carrying non-whitespace content, OR at least one
//     command invocation in any role: User. A person drove the session, either
//     by typing a prompt or by running a slash command.
//   - Neither of those, but at least one assistant or tool turn: Agent. Real
//     work happened with nothing a person typed anywhere in the transcript, the
//     shape of a worker driven by another agent's message.
//   - Neither: Unknown. A payload of system turns with no command invocation
//     carries no evidence either way, so it stays fully visible rather than
//     being demoted on thin evidence. A nil payload is Unknown for the same
//     fail-safe reason.
//
// A command invocation counts wherever it appears because the wire role of that
// turn is a harness detail that changed over time: Peasant has projected
// command blocks to the system role since 2026-08-12, and payloads recorded
// before that carry them as user turns. Reading only the role would classify
// the same human session two different ways depending on when it was recorded.
func Classify(payload *schema.SessionDetailPayload) Origin {
	if payload == nil {
		return Unknown
	}
	work := false
	for _, turn := range payload.Turns {
		if isCommandInvocation(turn.Content) {
			return User
		}
		switch turn.Role {
		case schema.RoleUser:
			if strings.TrimSpace(turn.Content) != "" {
				return User
			}
		case schema.RoleAssistant, schema.RoleTool:
			work = true
		}
	}
	if work {
		return Agent
	}
	return Unknown
}
