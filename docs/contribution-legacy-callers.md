# Callers of the legacy contributable and grouped candidate reads

This document inventories every caller of the flat and grouped contributable
reads, and of the shared grouped candidate reads that load a whole scope before
they page it. It records the bound each read has, what was observed above
10,000 rows, and a proposed disposition. It proposes follow-up work and does not
fix anything.

## How the list was built

A second reviewer can repeat the search from the repository root:

```sh
# Backend: the two candidate queries and every route that reaches them.
grep -rn 'ListOwnerContributableTranscripts\|ListCollectiveGroupedCandidates\|ListGroupedTranscriptCandidates' backend/internal --include=*.go | grep -v _test | grep -v /sqlc/
grep -rn 'RegisterGroupedScope(' backend/internal/handler --include=*.go | grep -v _test
grep -rn 'dispatchContributableView\|GroupedRouteContributable\|listCollectiveGrouped' backend/internal/handler --include=*.go | grep -v _test

# Frontend: the routes and the hooks that call them.
grep -rn '/contributable\|/pending\|/my-shares\|transcript-groups\|view.*grouped' frontend/src --include=*.ts --include=*.tsx | grep -v test
grep -rn 'useContributable\|useGroupedContributable\|useGroupedCollective\|useGroupedPendingShares\|useGroupedMyShares\|useGroupedTranscripts\|useHelperGroupMembers' frontend/src --include=*.tsx | grep -v test
```

The reads fall into three backend paths:

| Query | Handler entry | Bound |
|---|---|---|
| `ListOwnerContributableTranscripts` (`backend/internal/database/queries/transcripts.sql`) | `ListContributable`, no `view` (`backend/internal/handler/group_shares.go`) | `contributableRowLimit`, 10,000 by default (`backend/internal/handler/handler.go`), checked after the whole set is loaded |
| `ListCollectiveGroupedCandidates` (`backend/internal/database/queries/collective_grouped.sql`) | `resolveCollectiveGroupedScope` (`backend/internal/handler/collective_grouped.go`), registered for the collective, contributable, pending and my-shares variants | `contributableRowLimit` for the contributable variant only; none for the other three |
| `ListGroupedTranscriptCandidates` (`backend/internal/database/queries/grouped_transcripts.sql`) | `resolveGroupedTranscripts` (`backend/internal/handler/grouped_transcripts.go`), registered for the transcripts, profile and project variants | none; the query comment states it has no pagination |

Every grouped read loads its full candidate set, builds the groups in memory,
and then serves one page. The helper-member read
(`GET /api/v1/transcript-groups/{groupId}/members`, `ListHelperMembers`) replays
the scope that produced the group, so it reloads the same full set.

## Reproduction

The reads were run against the real server binary, built from this branch,
over a copy of the benchmark's Reported dataset
(`docs/contribution-scale-benchmark.md`). One owner holds 12,148 published
transcripts, of which 7,917 are roots, in 642 projects. The largest project
holds 3,051 transcripts. The owner was added as a member of the dataset's
collective, which holds 706 shared transcripts. Times are single warm requests
on a development machine. They show an order of magnitude, not a budget.

The dataset seeds helpers through `parent_session_id` only. No row carries the
session-relationship metadata that makes a grouped read emit a helper thread
(`helperThreadTotal` was 0 on every grouped read). The helper-member replay
could not be reached, so it is marked "not reproduced".

No collective in the dataset holds more than 10,000 shared transcripts, so the
collective-scoped grouped reads were measured at 706 rows. Their behavior above
10,000 rows is marked "not reproduced". They have no bound, so they would not
refuse; the measured cost scales with the collective, not with the caller's
library.

## Inventory

"Internal" means a caller in this repository's frontend. External clients are
unknown: the contract is public, and any client may call any declared
operation. The inventory does not claim that an external client exists or does
not exist.

| # | Caller (file) | Route and read shape | Current bound | Observed above the bound | Disposition |
|---|---|---|---|---|---|
| 1 | Collective contribute page, `frontend/src/app/collectives/[id]/contribute/page.tsx` through `useContributable` (`frontend/src/lib/queries/groupShares.ts`) | `GET /api/v1/collectives/{id}/contributable`, flat `{group_id, transcripts}` | 10,000, after the whole set is loaded | `413`, 286 bytes, 0.08 s: `{"error":"you have 12148 published transcripts and this listing serves at most 10000 in one answer, so Village will not build a contribute page it cannot render. Nothing was changed. Contribute from a project page, which is scoped to one project, or ask for the listing to be raised."}` | Fixed by this epic: the page moves to `view=paged`, the project picker and `view=snapshot` |
| 2 | Collective contribute page, same file, through `useGroupedContributable` (`frontend/src/lib/queries/groupedCollectives.ts`) | `GET /api/v1/collectives/{id}/contributable?view=grouped`, grouped transcript list | 10,000, after the whole set is loaded | `413`, 264 bytes, 0.65 s: `{"error":"handler.resolveCollectiveGroupedScope refused the collective grouped read: the eligible candidate set exceeds the configured contribute row bound; no transcript list or members were returned and nothing was changed; filter to a project before retrying"}`. Filtered to the 3,051-row project: `200`, 0.48 s | Fixed by this epic: the page stops calling it when the scoped helper disclosure lands |
| 3 | Collective library, `frontend/src/components/group/CollectiveTranscriptLibrary.tsx` through `useGroupedCollective` | `GET /api/v1/collectives/{id}?view=grouped`, collective detail with a grouped list | none; loads every candidate in the collective | Not reproduced above 10,000 (largest collective: 706 rows). At 706 rows: `200`, 0.19 to 0.54 s, about 205 KB for a 20-row page | Follow-up, group A |
| 4 | Collective review page, `frontend/src/app/collectives/[id]/review/page.tsx` through `useGroupedPendingShares` | `GET /api/v1/collectives/{id}/pending?view=grouped`, `VillageSessionListPayload` | none | Not reproduced above 10,000. At the dataset's pending set: `200`, 0.26 s, 127 KB for one page | Follow-up, group A |
| 5 | Collective library, `frontend/src/components/group/CollectiveTranscriptLibrary.tsx` through `useGroupedMyShares` | `GET /api/v1/collectives/{id}/my-shares?view=grouped`, `VillageSessionListPayload` | none | Not reproduced above 10,000. At the dataset's share set: `200`, 0.46 s, 131 KB for one page | Follow-up, group A |
| 6 | Home page, `frontend/src/app/HomePage.tsx`; explore page, `frontend/src/app/explore/ExplorePage.tsx` (through `ScopedGroupedHelperSection`); through `useGroupedTranscriptsPaged` and `useGroupedTranscripts` (`frontend/src/lib/queries/helperGroups.ts`) | `GET /api/v1/transcripts?view=grouped`, grouped transcript list, transcripts variant | none; loads every candidate the viewer can read | Reproduced over 12,148 rows for the owner: `200`, 1.5 to 1.6 s for page 1, 2.9 to 3.7 s for page 100, 100 KB per page. Anonymous, over 1,161 public rows: `200`, 0.97 s. Not refused at any size | Follow-up, group B |
| 7 | Profile page, `frontend/src/app/users/[username]/page.tsx`; project page, `frontend/src/app/users/[username]/projects/[projectHash]/page.tsx`; both through `useGroupedTranscriptsPaged` | `GET /api/v1/transcripts?view=grouped` with the profile or project scope | none | Reproduced for the profile scope over 12,148 rows: as row 6. The project scope is bounded by the project size; not reproduced above 10,000 rows in one project (largest: 3,051) | Follow-up, group B |
| 8 | Helper sections, `frontend/src/components/transcript/ScopedHelperGroups.tsx` through `useHelperGroupMembers`, mounted by the pages in rows 2 to 7 | `GET /api/v1/transcript-groups/{groupId}/members`, replays the originating scope | none; reloads the originating candidate set on every expansion | Not reproduced: the dataset has no helper threads | Follow-up, group C |
| 9 | No internal caller | `GET /api/v1/groups/{id}/contributable`, flat and `view=grouped` (deprecated alias) | as rows 1 and 2 | As rows 1 and 2: `413` with the same bodies, 0.26 s and 0.85 s | Keep mounted; removal is `peasant-labs/village#289` |

Rows 3 to 9 are unchanged by this epic.

### Count by disposition

| Disposition | Rows |
|---|---|
| Fixed by this epic | 2 (rows 1, 2) |
| Follow-up | 6 (rows 3 to 8) |
| Keep mounted, no internal caller | 1 (row 9) |

### Declared operations with no internal caller

- Every `/api/v1/groups/{id}/...` operation, including
  `listContributableTranscriptsDeprecated`,
  `listContributionProjectsDeprecated`, `listPendingSharesDeprecated`,
  `listMyGroupSharesDeprecated`, `batchShareProjectDeprecated` and
  `batchShareSelectionDeprecated`. The frontend calls only the
  `/api/v1/collectives` mounts. They stay declared and mounted for the alias
  window.
- `listContributionProjects` and the `paged` and `snapshot` views of
  `listContributableTranscripts` have no caller on the base branch. The paged
  contribute page in this epic is their first caller.
- `batchShareSelection` (`POST /api/v1/collectives/{id}/shares/selection`) has
  no frontend caller yet. The chunked submit in this epic is its first caller.

## Legacy routes that must stay mounted

The legacy flat and grouped contributable responses stay, and so does the legacy
write, `POST /api/v1/{collectives,groups}/{id}/shares`, including
empty-means-whole-project and the boolean consent. They stay for as long as the
alias window is open, which ends only when a named decision closes it:

- `peasant-labs/village#289` removes the deprecated `/groups` mounts. It
  changes the compatibility claim, so it lands after this epic.
- `peasant-labs/schema#158` removes the deprecated operations from the
  contract after the alias window.

Neither may land before this epic ships, because external clients of the legacy
shapes are unknown.

## What still breaks above 10,000 rows after this epic

- The contribute page no longer does. It reads pages, the project picker and
  per-project snapshots, none of which load the owner's whole library.
- The flat and grouped contributable reads still answer `413` above the bound
  for any client that calls them, including the deprecated `/groups` alias. That
  is the intended legacy behavior, not a regression.
- Every other grouped read (rows 3 to 7) still loads its whole scope before it
  pages. None of them refuses; they get slower and heavier as the scope grows.
  The owner's own grouped transcript list took 1.5 s for page 1 and about 3 s
  for page 100 at 12,148 rows.
- The helper-member replay (row 8) reloads the originating scope on every
  expansion, so its cost follows the scope it came from.

## Proposed follow-up issues

These are proposals for approval. None has been created.

| Group | Rows | Priority | Scope |
|---|---|---|---|
| A | 3, 4, 5 | P2 | Keyset-page the collective, pending and my-shares grouped reads in SQL, so a collective of any size serves one page without loading every candidate. |
| B | 6, 7 | P2 | Keyset-page the transcripts, profile and project grouped reads in SQL, with group counts from an aggregate rather than from the loaded set. |
| C | 8 | P3 | Serve helper members by root id and scope token without replaying the originating candidate set. Depends on A and B. |

Related, not absorbed: `peasant-labs/village#321` is a read-policy defect on
the flat transcript list, a different surface with a different defect.
