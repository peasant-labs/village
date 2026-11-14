package handler

import (
	"sort"
	"strconv"
	"strings"
)

// This file is the ONE definition of which session families a contribution read
// selects. Every view of a project's contribution (the snapshot today, a paged
// listing beside it) must select the same families for the same project and the
// same search, so the rules live here, once, as SQL fragments and a small filter
// value, and no view restates them.
//
// A FAMILY is a root session and every session below it. A session below a root
// is reached through parent_session_id, which names the parent's local_id within
// one owner. The family is a tree: local_id is unique per owner, so a session has
// at most one parent, and a cycle has no root, so the walk from a root can never
// reach one. The walk therefore needs no depth cap and no cycle guard.
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
// SEARCH (q) is one string. A member matches when its title contains q, or its id
// is exactly q, or its own project's display name contains q. All matching is
// case-insensitive and literal: %, _ and \ in q are ordinary characters. A family
// is selected when ANY member matches, and then every member is selected, so a
// match on one helper never hides the rest of its family.
//
// PROVIDER keeps the families in which some member has exactly that model
// provider. It is independent of q, so the two combine as an intersection of
// families, not of members.

// contributionRootCondition is the root test for the session aliased alias in a
// query over transcripts. It is the same condition the contribution benchmark
// tool measures.
func contributionRootCondition(alias string) string {
	return "(" + alias + ".parent_session_id IS NULL OR NOT EXISTS (" +
		"SELECT 1 FROM transcripts p WHERE p.owner_id = " + alias + ".owner_id AND p.local_id = " + alias + ".parent_session_id))"
}

// contributionFamilyFilter narrows which families are selected.
type contributionFamilyFilter struct {
	// Query is the trimmed search text; empty means no search.
	Query string
	// QueryProjects are the project hashes whose display name contains Query.
	// The display name is resolved in Go, so the caller supplies the matches.
	QueryProjects []string
	// Provider is an exact model provider; empty means any.
	Provider string
}

func (f contributionFamilyFilter) hasQuery() bool    { return f.Query != "" }
func (f contributionFamilyFilter) hasProvider() bool { return f.Provider != "" }

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

// contributionFamilySQL builds the pieces of a family read. Argument numbering
// starts after the arguments the caller already uses.
type contributionFamilySQL struct {
	// MatchColumns returns the extra SELECT columns (with a leading comma, or
	// empty when no filter is active) that compute one boolean per active filter
	// for the member aliased alias.
	MatchColumns func(alias string) string
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
	arg := func(v any) string {
		out.Args = append(out.Args, v)
		n := next
		next++
		return "$" + strconv.Itoa(n)
	}

	var pattern, exactID, projects, provider string
	if f.hasQuery() {
		pattern = arg("%" + escapeLikePattern(f.Query) + "%")
		exactID = arg(strings.ToLower(f.Query))
		hashes := f.QueryProjects
		if hashes == nil {
			hashes = []string{}
		}
		projects = arg(hashes)
		having = append(having, "bool_or(hit_q)")
	}
	if f.hasProvider() {
		provider = arg(f.Provider)
		having = append(having, "bool_or(hit_provider)")
	}

	out.MatchColumns = func(alias string) string {
		var cols []string
		if f.hasQuery() {
			cols = append(cols, "COALESCE("+alias+".title ILIKE "+pattern+` ESCAPE '\' OR `+alias+".id::text = "+exactID+
				" OR "+alias+".project_hash = ANY("+projects+"::text[]), false) AS hit_q")
		}
		if f.hasProvider() {
			cols = append(cols, "("+alias+".model_provider = "+provider+") AS hit_provider")
		}
		if len(cols) == 0 {
			return ""
		}
		return ", " + strings.Join(cols, ", ")
	}
	out.Having = strings.Join(having, " AND ")
	return out
}
