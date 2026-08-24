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
	"strings"

	"github.com/peasant-labs/schema"
)

// Origin is the closed menu of session origins. Its values are the exact
// tokens the transcripts.session_origin CHECK constraint accepts, so a value
// that round-trips through the database is always one of them.
type Origin string

const (
	// User marks a session with at least one real user turn: a human prompted
	// it in-band. This is the ordinary case and is never demoted.
	User Origin = "user"

	// Agent marks a session with no user turn at all in which assistant or
	// tool work still happened, so something drove it without a human prompt
	// in the transcript. Only this value is collapsed out of root-level lists.
	Agent Origin = "agent"

	// Unknown marks a session whose origin could not be established: content
	// that carries no user turn AND no assistant or tool work (a few slash
	// commands and then a closed session), or content that could not be read
	// at all. Unknown is the fail-safe value and behaves exactly like User.
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

// Classify is the single classification rule, shared by the publish path and
// the maintenance backfill so a stored value and a freshly published one can
// never disagree.
//
// The rule reads only roles and prompt content:
//
//   - At least one user turn carrying non-whitespace content: User. A person
//     prompted the session in-band.
//   - No such user turn, but at least one assistant or tool turn: Agent.
//     Real work happened with no human prompt in the transcript.
//   - Neither: Unknown. A payload made only of system turns is the shape of a
//     person who ran a few slash commands and then closed the session, so it
//     stays fully visible rather than being demoted on thin evidence. A nil
//     payload is Unknown for the same fail-safe reason.
//
// Known residual edge, not solved here: a human session driven purely by
// slash-skill commands puts its whole prompt inside harness-injected blocks,
// so on the wire it is indistinguishable from an agent-spawned session and
// classifies Agent. Such a session stays deep-linkable and is labelled rather
// than hidden. The real disambiguation is an origin signal produced by the
// recording client and carried on the wire; until that exists, no heuristic
// over turn roles can separate the two shapes.
func Classify(payload *schema.SessionDetailPayload) Origin {
	if payload == nil {
		return Unknown
	}
	work := false
	for _, turn := range payload.Turns {
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
