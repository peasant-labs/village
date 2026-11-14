package handler

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/projectname"
)

// This file is the ONE definition of which session families a contribution read
// selects. The project picker, the paged view and the snapshot view answer
// different questions about the same library, so for the same viewer, collective,
// search and provider they must agree exactly: the picker's count for a project
// is the number of sessions in that project's snapshot, the families the paged
// view lists for a project are the roots of that snapshot, and the picker's total
// is the number of sessions across every paged family.
//
// A FAMILY is a root session and every session below it. A session below a root
// is reached through parent_session_id, which names the parent's local_id within
// one owner. The family is a tree: local_id is unique per owner, so a session has
// at most one parent. A session reachable from no root (its parent chain forms a
// cycle) belongs to no family and no read lists it.
//
// A ROOT is a session with no parent_session_id, or whose parent is not among the
// owner's published transcripts (an orphan: its parent was never published or was
// removed). An orphan is treated as a root so it is never silently dropped from
// the project it belongs to.
//
// A family belongs to the project of its ROOT. Helpers keep their own project:
// a helper in another project is part of this family, and a helper here whose
// root is elsewhere is part of the other project's family, not this one's.
//
// SEARCH (q) is one string, trimmed; empty or blank means no search. A MEMBER
// matches when
//   - its title contains q, case-insensitively, with %, _ and \ taken literally;
//   - or q equals its transcript id, case-insensitively;
//   - or q equals its local session id exactly.
//
// The ROOT also matches when the DISPLAYED name of its project contains q,
// case-insensitively and literally. The displayed name is the one the project
// name resolver gives (never the raw path or remote evidence), so a search finds
// exactly what a person sees. A helper's own project name does not match: a
// helper that happens to live in a differently named project does not pull its
// family into a search for that name. A family is selected when its root or any
// helper matches, and then the whole family is counted and returned.
//
// PROVIDER keeps the families in which some member has exactly that model
// provider (trimmed; empty means any). It is independent of q, so the two combine
// as an intersection of families, not of members. The picker's provider facet
// ignores the provider filter and applies q.
//
// LIVE is one definition: the latest attempt event of a session for the
// collective is pending or approved. A session that is live is not selectable.
//
// REQUEST PARAMETERS. Every read ignores a parameter it does not use. A
// parameter it does use and that is sent more than once is a 400, as is q or
// provider carrying invalid UTF-8 or a NUL byte. A parameter that does not apply
// to a view (limit or cursor on a snapshot, say) is ignored even when repeated.
//
// WHERE THE RULE LIVES. The Go half (parameter parsing, the pattern, the project
// name match, root project resolution) is used by all three reads. The SQL half
// has one canonical text per fragment, built below. The snapshot builds its
// statement from those builders; the sqlc statements of the picker and the paged
// view carry the same text with named placeholders, because sqlc has no way to
// include a fragment. contribution_family_drift_test.go fails when any sqlc
// statement stops containing a canonical fragment, and the cross-read integration
// test fails when the three reads stop agreeing.

// contributionLiveStatuses are the latest-attempt statuses that make a session
// live in a collective.
var contributionLiveStatuses = []string{"pending", "approved"}

// contributionRootCondition is the root test for the session aliased alias in a
// query over transcripts. It is the same condition the contribution benchmark
// tool measures.
func contributionRootCondition(alias string) string {
	return "(" + alias + ".parent_session_id IS NULL OR NOT EXISTS (" +
		"SELECT 1 FROM transcripts p WHERE p.owner_id = " + alias + ".owner_id AND p.local_id = " + alias + ".parent_session_id))"
}

// contributionMemberMatchSQL is the text match of one member aliased alias.
// pattern is an expression holding the ILIKE pattern of the search and text an
// expression holding the trimmed search itself.
func contributionMemberMatchSQL(alias, pattern, text string) string {
	return "(COALESCE(" + alias + ".title ILIKE " + pattern + ", false)" +
		" OR lower(" + alias + ".id::text) = lower(" + text + ")" +
		" OR " + alias + ".local_id = " + text + ")"
}

// contributionRootNameMatchSQL is the project-name match of a ROOT aliased alias.
// projects is an expression holding the project hashes whose displayed name
// contains the search.
func contributionRootNameMatchSQL(alias, projects string) string {
	return "COALESCE(" + alias + ".project_hash = ANY(" + projects + "), false)"
}

// contributionProviderMatchSQL is the provider match of one member.
func contributionProviderMatchSQL(alias, provider string) string {
	return "COALESCE(" + alias + ".model_provider = " + provider + ", false)"
}

// contributionLatestAttemptSQL joins the latest attempt of the member aliased
// member in the collective expression group, as the row aliased outer.
func contributionLatestAttemptSQL(member, group, outer string) string {
	return "LEFT JOIN LATERAL (SELECT s.status FROM transcript_share_attempts s" +
		" WHERE s.transcript_id = " + member + ".id AND s.group_id = " + group +
		" ORDER BY s.event_num DESC LIMIT 1) " + outer + " ON true"
}

// contributionLiveSQL is the live test over the latest attempt joined as outer.
func contributionLiveSQL(outer string) string {
	quoted := make([]string, len(contributionLiveStatuses))
	for i, status := range contributionLiveStatuses {
		quoted[i] = "'" + status + "'"
	}
	return "COALESCE(" + outer + ".status IN (" + strings.Join(quoted, ", ") + "), false)"
}

// contributionFamilyFilter narrows which families are selected.
type contributionFamilyFilter struct {
	// Query is the trimmed search text; empty means no search.
	Query string
	// QueryProjects are the project hashes whose displayed name contains Query.
	// Names are resolved in Go, so the caller supplies the matches.
	QueryProjects []string
	// Provider is an exact model provider; empty means any.
	Provider string
}

func (f contributionFamilyFilter) hasQuery() bool    { return f.Query != "" }
func (f contributionFamilyFilter) hasProvider() bool { return f.Provider != "" }

// contributionSearch is the validated q and provider of a request.
type contributionSearch struct {
	// Query is trimmed; empty means no search.
	Query string
	// Provider is trimmed; empty means any provider.
	Provider string
}

// validSearchText reports whether s can be sent to the database as text.
func validSearchText(s string) bool {
	return utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}

// refuseRepeatedParameters names the first of keys that values carries more than
// once, or returns "". Only the keys a view reads are passed, so a view ignores a
// repeat of a parameter it does not use.
func refuseRepeatedParameters(values url.Values, keys ...string) string {
	for _, key := range keys {
		if n := len(values[key]); n > 1 {
			return fmt.Sprintf("the parameter %q was sent %d times; send it once. Nothing was read.", key, n)
		}
	}
	return ""
}

// parseContributionSearch reads q and provider the one way every contribution
// read does: each at most once, trimmed, valid text. A refusal is a message for
// the caller, already actionable.
func parseContributionSearch(values url.Values) (contributionSearch, string) {
	if refusal := refuseRepeatedParameters(values, "q", "provider"); refusal != "" {
		return contributionSearch{}, refusal
	}
	search := contributionSearch{
		Query:    normalizeContributionQuery(values.Get("q")),
		Provider: strings.TrimSpace(values.Get("provider")),
	}
	if !validSearchText(search.Query) {
		return contributionSearch{}, "q contains a character a search cannot use (a NUL byte or invalid UTF-8). Nothing was read. Retype the search."
	}
	if !validSearchText(search.Provider) {
		return contributionSearch{}, "provider contains a character a filter cannot use (a NUL byte or invalid UTF-8). Nothing was read. Pick a provider from the list."
	}
	return search, ""
}

// normalizeContributionQuery trims q. A blank query is no query.
func normalizeContributionQuery(q string) string { return strings.TrimSpace(q) }

// escapeLikePattern makes q literal inside a LIKE or ILIKE pattern that uses the
// backslash escape.
func escapeLikePattern(q string) string {
	if !strings.ContainsAny(q, `%_\`) {
		return q
	}
	var b strings.Builder
	b.Grow(len(q) + 4)
	for _, r := range q {
		if r == '%' || r == '_' || r == '\\' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// contributionTitlePattern is the ILIKE pattern that finds q anywhere in a title.
func contributionTitlePattern(q string) string { return "%" + escapeLikePattern(q) + "%" }

// projectHashesNamed returns, sorted, the hashes whose display name contains q,
// case-insensitively and literally.
func projectHashesNamed(q string, displayNames map[string]string) []string {
	if q == "" {
		return nil
	}
	needle := strings.ToLower(q)
	var hashes []string
	for hash, name := range displayNames {
		if strings.Contains(strings.ToLower(name), needle) {
			hashes = append(hashes, hash)
		}
	}
	sort.Strings(hashes)
	return hashes
}

// contributionRootProjects are the projects of an owner's roots, which are the
// only projects a family can belong to, with the displayed name of each.
type contributionRootProjects struct {
	// Hashes are the root projects, sorted.
	Hashes []string
	// Identities hold the resolved name of each root project.
	Identities map[projectIdentityKey]projectname.Resolved
	// Named are the root projects whose displayed name contains the search,
	// sorted; empty when there is no search.
	Named []string
}

// readContributionRootProjects resolves the displayed name of every project the
// owner has a root in and matches q against them. Every contribution read gets
// the project-name half of the search from here. Names are resolved by the one
// resolver every surface uses, over the same reads the other statements of the
// caller use (queries may be bound to a transaction), so the names are the ones
// a person sees and a protected name is never exposed by matching it.
func (h *Handler) readContributionRootProjects(ctx context.Context, queries *sqlc.Queries, owner pgtype.UUID, q string) (contributionRootProjects, error) {
	hashes, err := queries.ListOwnerContributionRootProjects(ctx, owner)
	if err != nil {
		return contributionRootProjects{}, fmt.Errorf("list the owner's root projects: %w", err)
	}
	keys := make([]projectIdentityKey, 0, len(hashes))
	for _, hash := range hashes {
		keys = append(keys, projectIdentityKey{OwnerID: owner, ProjectHash: hash})
	}
	out := contributionRootProjects{
		Hashes:     hashes,
		Identities: h.resolveProjectIdentitiesFrom(ctx, queries, keys),
	}
	if q != "" {
		names := make(map[string]string, len(keys))
		for _, key := range keys {
			names[key.ProjectHash] = out.Identities[key].DisplayName
		}
		out.Named = projectHashesNamed(q, names)
	}
	return out, nil
}

// contributionFamilySQL builds the pieces of a hand-built family read. Argument
// numbering starts after the arguments the caller already uses.
type contributionFamilySQL struct {
	// RootColumns and MemberColumns return the extra SELECT columns (with a
	// leading comma, or empty when no filter is active) that compute one boolean
	// per active filter for the session aliased alias, a root or a helper.
	RootColumns   func(alias string) string
	MemberColumns func(alias string) string
	// Having keeps only the families the filter selects, or is empty.
	Having string
	// Args are the values for the placeholders the fragments use.
	Args []any
}

// buildContributionFamilySQL returns the SQL fragments and arguments for f,
// numbering placeholders from firstArg.
func buildContributionFamilySQL(f contributionFamilyFilter, firstArg int) contributionFamilySQL {
	var out contributionFamilySQL
	var having []string
	next := firstArg
	arg := func(v any, cast string) string {
		out.Args = append(out.Args, v)
		n := next
		next++
		return "$" + strconv.Itoa(n) + cast
	}

	var pattern, text, projects, provider string
	if f.hasQuery() {
		pattern = arg(contributionTitlePattern(f.Query), "::text")
		text = arg(f.Query, "::text")
		hashes := f.QueryProjects
		if hashes == nil {
			hashes = []string{}
		}
		projects = arg(hashes, "::text[]")
		having = append(having, "bool_or(hit_q)")
	}
	if f.hasProvider() {
		provider = arg(f.Provider, "::text")
		having = append(having, "bool_or(hit_provider)")
	}

	columns := func(alias string, root bool) string {
		var cols []string
		if f.hasQuery() {
			hit := contributionMemberMatchSQL(alias, pattern, text)
			if root {
				hit = "(" + hit + " OR " + contributionRootNameMatchSQL(alias, projects) + ")"
			}
			cols = append(cols, hit+" AS hit_q")
		}
		if f.hasProvider() {
			cols = append(cols, contributionProviderMatchSQL(alias, provider)+" AS hit_provider")
		}
		if len(cols) == 0 {
			return ""
		}
		return ", " + strings.Join(cols, ", ")
	}
	out.RootColumns = func(alias string) string { return columns(alias, true) }
	out.MemberColumns = func(alias string) string { return columns(alias, false) }
	out.Having = strings.Join(having, " AND ")
	return out
}
