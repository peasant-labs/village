# Contribution scale benchmark

This document records how the contribution read and write paths were measured
against candidate indexes. It is written in two parts. This first part states
the decision rule and the budget. It was committed before any measured run, so
the history shows that the budget was fixed before the results were read. The
results part is appended later, after the run.

Status of the budget: **proposed, pending owner ratification**. The numbers
below were chosen before measurement. They have not been ratified by the owner.

## Scope

The benchmark measures, and does not change, the contribution browse, picker,
snapshot, publish, republish, and visibility paths. No handler, query, migration,
or sqlc output is changed. Candidate indexes are applied only to a benchmark
database.

## Configurations

All configurations run on the same generated dataset, the same PostgreSQL
settings, and the same workloads.

| ID | Indexes added to the existing schema |
|---|---|
| R0 | None. Baseline. |
| R1 | `(owner_id, COALESCE(session_start, published_at, 'epoch') DESC, id)` |
| R2 | `(owner_id, project_hash, COALESCE(session_start, published_at, 'epoch') DESC, id)` |
| R3 | R1 and R2 together. |
| H1 | `(owner_id, parent_session_id)`. Serves helper loading and family expansion. |
| R1+H1 | R1 and H1. |
| R2+H1 | R2 and H1. |
| R3+H1 | R1, R2, and H1. |

## Datasets

| Dataset | Shape | Use |
|---|---|---|
| Small | About 1,000 transcripts for the target owner | Smoke, and determinism check |
| Reported | 12,148 transcripts for the target owner. One project of about 3,000 rows, a long tail of small projects, privacy-labeled projects, sessions with no remote, direct and nested helpers, including helpers in a different project from their root, and every attempt state | Main matrix |
| Large | 1,000,000 transcripts for the target owner, clearly labeled synthetic. Includes one project of about 250,000 rows and one of about 3,000 rows | Build cost, deep page, snapshot, family search |

## Workload definitions

- **Root.** A transcript is a root when it has no `parent_session_id`, or when
  its parent is not among the owner's published transcripts.
- **Browse page.** Keyset pages of 500 roots, ordered by
  `COALESCE(session_start, published_at, 'epoch') DESC, id`. Each page also
  loads the helpers (all descendants through `parent_session_id`) of its roots.
- **First page.** Page one of the browse, including its helpers.
- **Deep page.** A keyset page at root position 100,000 (page 200 at 500 per
  page) on the Large dataset. The Reported dataset has 25 pages at 500 per page,
  so page 200 does not exist there. On Reported the deep page is the page at the
  root midpoint.
- **Project read.** Roots of one small project (about 40 rows) and of the large
  project (about 3,000 rows), with their helpers.
- **Picker aggregate.** Every transcript in the families of the owner's roots,
  counted per root project. Helpers in a different project from their root are
  counted under the root's project.
- **Family search.** A family matches when its root or any helper has a title
  matching `ILIKE`, restricted to roots in a list of project hashes.
- **Snapshot.** The uncapped snapshot. One read-only REPEATABLE READ transaction
  reads every record of one project's families, or of the whole library, and
  returns compact records: id, visibility, latest contribution status, project
  hash, and parent link. There is no cap.
- **Write workloads.** First-time private-to-shared contribution (through the
  generated share-attempt and governance queries, in chunks), republish of one
  transcript, visibility update, and a concurrent publish, republish, and
  contribution run.

## Measurement

- Read latency: p50, p95, and p99 over 30 warm runs after 3 warm-up runs.
  Warm means the same query was just executed on the same connection.
- Cold latency: 3 samples per query, each taken after a PostgreSQL server restart,
  so shared buffers are empty. The operating system page cache is not dropped,
  because the benchmark does not run as root. Cold numbers are therefore
  partially warm and are labeled as such.
- Every read has an `EXPLAIN (ANALYZE, BUFFERS)` capture.
- Write latency and WAL bytes per operation, measured on the same selection
  sets across configurations. Repetitions reset the selected rows between runs.
- Build: wall-clock duration of `CREATE INDEX` on each dataset, index size on
  disk, and the maximum latency of concurrent single-row writes during the build.

## Budget

| Measure | Budget | Dataset |
|---|---|---|
| Chunk p95, 800 rows, first-time private-to-shared | under 4 s | Reported |
| First page p95, 500 roots plus their helpers | under 800 ms | Reported |
| Picker aggregate p95 | under 2 s | Reported |
| Uncapped snapshot p95, whole 12,148 library | under 5 s | Reported |
| Snapshot payload, whole 12,148 library | reported against 8 MiB | Reported |
| Publish-path regression vs R0 (chunk, republish, visibility p95) | under 10% | Reported |
| Index build duration at 1,000,000 rows | under 150 s, half the 300 s `migrationTimeout` | Large |

Deep page and family search have no absolute budget. They are judged only by
the adoption rule below.

Chunk sizes under comparison: 100, 250, 500, and 1,000 rows. The chunk size is a
client policy, not a server maximum. The chunk size that meets the chunk budget
with the lowest p95 per row is recommended.

## Adoption rule

1. A candidate index is adopted only if it improves the p95 of its target read by
   at least 30% against R0 on the same dataset. Targets:
   - R1: first page, deep page, picker aggregate.
   - R2: one small project, one large project, project snapshot.
   - H1: helper load, family search, helper expansion in the snapshot.
2. The write regression of the adopted set against R0, measured as the p95 across
   the write workloads, must be under 10%.
3. Prefer one index over two. If one index meets its targets within budget, a
   pair is not adopted.
4. If no candidate or combination earns its cost, add no index. Record the
   measured read cost and do not change the schema in this change.
5. Results are read only after this section is committed. Any change to these
   numbers after a run is recorded as a change, not as a new budget.

## Results

Not yet measured. The results part is appended after the run.
