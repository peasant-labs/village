// Package projectname resolves the single canonical display name for one
// (owner_id, project_hash) pair from the layered evidence a caller has
// already aggregated (an owner override, a consented project name, a git
// remote, and Peasant's own privacy-safe label). It is pure Go: no
// database, no HTTP, no dependency on the schema module. That last point is
// deliberate — see RemoteLabeler below.
package projectname

import "regexp"

// NameSource is the tier a resolved display name came from. It is a closed
// set and travels to the frontend so the UI can style an inferred label
// (remote, privacy) differently from an owner-chosen or user-consented one.
type NameSource string

const (
	// NameSourceOverride means the owner explicitly renamed the project.
	NameSourceOverride NameSource = "override"
	// NameSourceConsented means a transcript in the project carries a
	// project_name the pushing user chose to disclose (not a privacy label).
	NameSourceConsented NameSource = "consented"
	// NameSourceRemote means no override or consented name exists, but the
	// project's git remote could be formatted into a display label.
	NameSourceRemote NameSource = "remote"
	// NameSourcePrivacy means the only evidence available is Peasant's own
	// privacy-safe "project-<12hex>" label, whether that label was carried
	// on a transcript row or synthesised here as a last resort from the
	// project hash.
	NameSourcePrivacy NameSource = "privacy"
)

// Evidence is everything known about ONE (owner_id, project_hash) pair,
// already aggregated across that owner's transcripts by a single query. It
// is the caller's job (the project-identity list query, ListOwnerProjectIdentities) to pick
// ConsentedName, GitRemote, and PrivacyLabel deterministically
// (published_at DESC, id ASC) and to classify a transcript's stored
// project_name into ConsentedName vs PrivacyLabel using IsPrivacyLabel.
type Evidence struct {
	// ProjectHash is the 64 lowercase-hex identity of the project. It is
	// required at the publish boundary elsewhere in the system; Resolve
	// treats it as present whenever it is non-empty and uses it only for
	// the last-resort synthesis below.
	ProjectHash string
	// OverrideName is the owner_overrides value for this project, or "" when
	// the owner has never renamed it.
	OverrideName string
	// ConsentedName is the deterministically picked project_name across the
	// owner's transcripts for this hash, or "" when every row carries a
	// privacy label instead of a consented name.
	ConsentedName string
	// GitRemote is the deterministically picked RAW git remote (e.g.
	// "git@github.com:owner/repo.git"), or "" when unknown. It is NOT a
	// formatted display label — see RemoteLabeler.
	GitRemote string
	// PrivacyLabel is the row-carried "project-<12hex>" label, or "" when
	// absent. This is distinct from the last-resort synthesis: it is a
	// label that was actually stored on a transcript, not one Resolve
	// invented.
	PrivacyLabel string
}

// Resolved is the single answer every surface renders for one project.
type Resolved struct {
	// DisplayName is never empty and never the literal "Other".
	DisplayName string
	// Source names which tier DisplayName came from.
	Source NameSource
	// RemoteLabel is the formatted "host:owner/repo" label when the git
	// remote is known and recognizable, else "". It is populated
	// independently of Source, so a caller can render it as a subtitle even
	// when DisplayName came from a higher-precedence tier (an owner
	// override with a known remote still shows the remote as context).
	RemoteLabel string
}

// RemoteLabeler formats a raw git remote as a "host:owner/repo" display
// label. ok is false when the remote is empty or unrecognizable.
//
// This is an injected seam rather than a direct call to the schema module's
// RemoteLabel: at the time this package was written, that function did not
// exist yet in the pinned schema module version, and this package must
// never depend on unpublished schema surface. The wiring to the real
// schema.RemoteLabel happens at construction time once the module is
// re-pinned — this package only declares and consumes the function shape.
type RemoteLabeler func(remote string) (label string, ok bool)

// Resolver holds the one dependency Resolve needs beyond its Evidence
// argument: the RemoteLabeler seam. A caller resolves many projects per
// request (a profile page resolves one per (owner_id, project_hash) pair),
// so the seam is injected once at construction and reused across every
// Resolve call rather than threaded through each one.
//
// The zero value is safe to use: a Resolver with a nil Label behaves
// exactly as one whose labeler always reports ok=false — the remote tier
// and the RemoteLabel subtitle are simply unavailable, never a panic.
type Resolver struct {
	Label RemoteLabeler
}

// Resolve applies the ratified precedence
//
//	override > consented > remote > privacy
//
// and never returns an empty DisplayName or the literal "Other".
//
// r.Label formats Evidence.GitRemote into the display label
// Resolved.RemoteLabel carries; it is applied once regardless of which tier
// ultimately supplies DisplayName, because RemoteLabel is rendered as a
// subtitle independent of Source.
//
// Last-resort rule: when OverrideName, ConsentedName, GitRemote (or its
// formatted label), and PrivacyLabel are all unusable but ProjectHash is
// non-empty, Resolve synthesises "project-" + ProjectHash[:12] with
// Source = NameSourcePrivacy, matching Peasant's own privacySafeProjectLabel.
// When even ProjectHash is empty — a case the publish boundary elsewhere in
// the system does not allow to reach storage — Resolve still never returns
// an empty DisplayName or "Other": it falls back to a fixed, clearly-marked
// placeholder so a caller can never observe either forbidden value.
func (r Resolver) Resolve(e Evidence) Resolved {
	remoteLabel := ""
	if r.Label != nil && e.GitRemote != "" {
		if formatted, ok := r.Label(e.GitRemote); ok && formatted != "" {
			remoteLabel = formatted
		}
	}

	switch {
	case e.OverrideName != "":
		return Resolved{DisplayName: e.OverrideName, Source: NameSourceOverride, RemoteLabel: remoteLabel}
	case e.ConsentedName != "":
		return Resolved{DisplayName: e.ConsentedName, Source: NameSourceConsented, RemoteLabel: remoteLabel}
	case remoteLabel != "":
		return Resolved{DisplayName: remoteLabel, Source: NameSourceRemote, RemoteLabel: remoteLabel}
	case e.PrivacyLabel != "":
		return Resolved{DisplayName: e.PrivacyLabel, Source: NameSourcePrivacy, RemoteLabel: remoteLabel}
	case e.ProjectHash != "":
		hash := e.ProjectHash
		if len(hash) > 12 {
			hash = hash[:12]
		}
		return Resolved{DisplayName: "project-" + hash, Source: NameSourcePrivacy, RemoteLabel: remoteLabel}
	default:
		// No caller in this system can reach this branch: ProjectHash is a
		// required, NOT NULL identity column at the publish boundary. It is
		// handled anyway so Resolve can never be observed to return an
		// empty DisplayName or "Other", whatever a future caller passes.
		return Resolved{DisplayName: "project-unknown", Source: NameSourcePrivacy, RemoteLabel: remoteLabel}
	}
}

// privacyLabelPattern is Peasant's privacy-safe project label shape:
// "project-" followed by exactly 12 lowercase hex digits.
var privacyLabelPattern = regexp.MustCompile(`^project-[0-9a-f]{12}$`)

// IsPrivacyLabel reports whether a stored project_name is Peasant's
// privacy-safe label rather than a consented project name. The rule is
// exactly ^project-[0-9a-f]{12}$.
//
// This is a syntactic check only. A real project that happens to be named
// "project-abcdef123456" is indistinguishable from a synthesised privacy
// label and IS accepted as one — a documented, deliberate collision (see
// testdata/privacy-label.yaml). Widening this rule to disambiguate that
// case is out of scope; it would require a provenance signal this package
// does not have.
func IsPrivacyLabel(projectName string) bool {
	return privacyLabelPattern.MatchString(projectName)
}
