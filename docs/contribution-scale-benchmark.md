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

Every number below was copied from the raw output of the harness. Each run wrote
one JSON line per workload to `results.jsonl`, and `summarize` wrote the
Markdown table `summary.md`. The raw files are kept outside the repository in the
benchmark output directory, because they are bulky. The layout is
`<out>/<dataset>/<config>/results.jsonl`, with `plans/` next to each file, and
`<out>/<dataset>/build/results.jsonl`. The manifest and checksum files sit beside
them. The write rounds are in a separate output tree, one directory per round.

### Status of the evidence

- Reads at 12,148 and 1,000,000 come from one run per configuration. Each read
  has 30 warm repetitions at 12,148 and 20 at 1,000,000, after 3 warm-ups. Cold
  samples are 3 at 12,148 and 2 at 1,000,000.
- Write numbers at 12,148 are the two round tables below, not the write rows of
  the first summary. The first summary's write rows were taken while another heavy
  job loaded the machine, so they are not trustworthy and are superseded.
- The read rows of the first summary were not identified as
  affected by load. I could not verify the machine state during those reads from
  the files, so treat their variance as unknown beyond the sample spread shown.
- No write matrix was run at 1,000,000. Only build cost and reads were measured
  there.

### Environment

| Item | Value |
|---|---|
| Host | single machine, Windows Subsystem for Linux 2 (kernel 6.18 WSL2), Intel Core i7-1360P laptop CPU, 16 logical CPUs, 15 GB RAM |
| PostgreSQL | 16.15, Nix `postgresql_16` package, listening on 127.0.0.1 over TCP only |
| PostgreSQL settings | `shared_buffers=1GB`, `work_mem=64MB`, `maintenance_work_mem=1GB`, `max_wal_size=4GB`, `checkpoint_timeout=30min`. These are harness settings, not production or CI settings. |
| CI database | PostgreSQL 16.15 alpine image. The version matches, the build and settings do not. |
| Go | toolchain go1.27.0, module `go 1.25.5`, pgx v5.8.0 |
| Harness | `backend/cmd/contribution-bench`, commits `8955df6`, `60650ca`, `79ce808`, `13c7eaa` |
| Seed | 20260113 for all datasets |
| Pool and concurrency | connection pool of 16 for writes. The concurrent run uses 2 contribution workers, 2 republish workers, 1 visibility worker, and 1 publish worker, for 20 seconds. |
| Cold cache | a PostgreSQL restart before each cold sample. The operating system page cache was not dropped, because the run is not root. Cold samples are therefore partially warm. |

### Datasets

The generated manifests are `<out>/reported/manifest.json` and
`<out>/large/manifest.json`. Both were seeded from the same seed, through the
repository's own migration runner (schema version 50), with the real row and
audit triggers active.

| Dataset | Transcripts | Roots | Helpers | Nested helpers | Cross-project helpers | Orphan roots | Projects | Seed time |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Reported | 12,148 | 8,187 | 3,961 | 776 | 116 | 270 | 642 | 2.0 s |
| Large | 1,000,000 | 671,316 | 328,684 | 63,855 | 9,508 | 20,174 | 48,271 | 104.5 s |

The Large seed ran through `COPY` in batches of 50,000, inside one transaction
that set `app.actor_id` and `app.transcript_writer_version`. Triggers were not
disabled, so each row also produced an audit row. The Small dataset (1,000 rows)
was used as a smoke check and determinism check only.

The largest projects are `project-3000` (3,000 rows) and, at 1,000,000 only,
`project-250000` (250,000 rows). `project-40` (40 rows) is the small project.
Reads that use the root-keyed families count helpers under the root's project.
Projects named by the reads are listed in the manifest.

### Reads at 12,148 transcripts (warm p95, milliseconds)

Warm p95 over 30 runs. Rows and bytes are identical across configurations, which
confirms that every configuration returns the same data.

| Workload | Rows | R0 | R1 | R2 | R3 | H1 | R1+H1 | R2+H1 | R3+H1 |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| First page, 500 roots with helpers | 736 | 36.9 | 14.2 | 47.6 | 14.3 | 17.0 | 12.9 | 61.6 | 28.9 |
| Deep page at the root midpoint | 745 | 34.9 | 15.5 | 41.4 | 11.3 | 16.4 | 16.0 | 34.5 | 15.2 |
| Project, 40 rows with helpers | 41 | 29.7 | 9.2 | 21.2 | 5.8 | 4.3 | 4.9 | 1.5 | 1.7 |
| Project, 3,000 rows with helpers | 3,051 | 44.8 | 17.7 | 93.9 | 12.2 | 19.7 | 22.2 | 20.9 | 44.5 |
| Picker aggregate | 642 | 85.0 | 20.5 | 51.7 | 15.1 | 21.5 | 21.7 | 45.2 | 26.1 |
| Family search, 20 projects | 18 | 41.7 | 19.7 | 46.2 | 11.2 | 23.3 | 31.7 | 34.0 | 16.3 |
| Snapshot, project of 3,000 rows (uncapped) | 3,051 | 207.8 | 32.4 | 158.4 | 25.4 | 33.4 | 73.3 | 70.4 | 65.7 |
| Snapshot, whole library, 12,148 (uncapped) | 12,148 | 312.7 | 176.6 | 301.9 | 82.3 | 178.1 | 188.0 | 205.6 | 250.5 |

Snapshot payload for the whole library is 2,359,366 bytes of JSON, which is under
8 MiB. Peak heap during the snapshot was 13 to 14 MB. Those figures are the same
for every configuration.

Warm p50 for the first page was 28.3 ms with R0, 38.5 ms with R2, and 11.4 to 18.4 ms
with the other configurations. The full warm and cold tables are in `summary.md`.

### Reads at 1,000,000 transcripts (warm p95, milliseconds)

| Workload | Rows | R0 | R1 | R2 | R3 | H1 | R1+H1 | R2+H1 | R3+H1 |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| First page, 500 roots with helpers | 749 | 3,942 | 3,204 | 3,397 | 3,041 | 831 | 388 | 853 | 664 |
| Deep page, page 200 (root 99,500) | 741 | 2,552 | 1,967 | 3,169 | 2,388 | 861 | 644 | 1,141 | 646 |
| Project, 40 rows with helpers | 40 | 1,533 | 1,119 | 962 | 688 | 301 | 445 | 1.6 | 1.4 |
| Project, 3,000 rows with helpers | 3,008 | 2,163 | 2,791 | 1,760 | 2,728 | 439 | 1,054 | 21.7 | 432 |
| Picker aggregate, 48,271 projects | 48,271 | 2,808 | 2,455 | 2,826 | 1,957 | 2,738 | 1,935 | 2,828 | 2,177 |
| Family search, 20 projects | 32 | 1,788 | 1,608 | 1,056 | 1,244 | 810 | 1,075 | 602 | 780 |
| Snapshot, project of 3,000 rows (uncapped) | 3,008 | 1,075 | 1,459 | 1,213 | 1,742 | 902 | 1,731 | 685 | 1,987 |
| Snapshot, project of 250,000 rows (uncapped) | 255,242 | 5,996 | 6,604 | 5,670 | 5,042 | 5,284 | 4,972 | 3,594 | 4,942 |
| Snapshot, whole library, 1,000,000 (uncapped) | 1,000,000 | 15,653 | 14,147 | 13,671 | 16,405 | 13,328 | 13,667 | 13,030 | 15,424 |

Snapshot payloads at 1,000,000 were 194,240,891 bytes for the library, 49,640,467
bytes for the 250,000-row project, and 584,950 bytes for the 3,000-row project.
Peak heap for the whole-library snapshot ranged from 0.77 to 1.24 GB across
configurations (`peak_heap_bytes` in the raw files).

Cold samples at 1,000,000 are in `summary.md`. Cold p95 is generally close to
warm p95, because the operating system cache was not dropped.

### Index build cost and size

Plain `CREATE INDEX` on each dataset, with a single-row writer running alongside
it. Writer blocking was measured by the time its one update took to return.

| Index | 12,148: build ms | 12,148: index bytes | 1,000,000: build ms | 1,000,000: index bytes | Writer blocked for |
|---|---:|---:|---:|---:|---|
| R1 | 41.7 | 729,088 | 753.4 | 58,908,672 | whole build |
| R2 | 70.0 | 1,736,704 | 1,489.9 | 141,541,376 | whole build |
| H1 | 38.8 | 262,144 | 658.7 | 20,242,432 | whole build |
| R1 then R2 (R3) | 106.3 | 2,465,792 | 2,701.7 | 200,450,048 | whole build |

The `transcripts` heap at 1,000,000 is 465,567,744 bytes. At 12,148 it is
5,767,168 bytes. The migration timeout is 300,000 ms. The largest build at
1,000,000 took 2.7 s, which is 0.9 percent of the timeout. The writer was blocked
for the full duration of every build, because plain `CREATE INDEX` takes a share
lock that conflicts with row updates. The rollout must either accept that block
at production size or use a concurrent build outside the transaction. That choice
was not benchmarked here.

### Writes at 12,148 transcripts, two rounds side by side

Both rounds ran alone on an idle machine, with 30 repetitions for the chunk and
single-row workloads, and 20 seconds for the concurrent run. Only R0, H1, R2+H1,
and R3+H1 were run in the write rounds. Each contribution selection is 800
private, never-submitted transcripts of `project-3000`, identical across
configurations. The chunk size 1000 row is one 800-row transaction. Latencies are
p95 in milliseconds, and WAL is the mean bytes written per operation.

| Workload | R0 r1 | R0 r2 | H1 r1 | H1 r2 | R2+H1 r1 | R2+H1 r2 | R3+H1 r1 | R3+H1 r2 |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Contribution, chunks of 100 (8 transactions per 800 rows) | 407.7 | 519.0 | 504.0 | 508.6 | 493.5 | 510.9 | 654.9 | 499.7 |
| Contribution, chunks of 250 (about 3 transactions) | 554.1 | 862.2 | 602.2 | 986.6 | 638.2 | 800.2 | 806.4 | 900.3 |
| Contribution, chunks of 500 (2 transactions) | 1,199.4 | 1,087.6 | 1,432.2 | 1,154.3 | 1,256.2 | 1,105.2 | 1,167.1 | 1,135.7 |
| Contribution, one transaction of 800 rows | 2,812.4 | 1,977.6 | 2,918.9 | 2,063.9 | 2,261.5 | 2,217.1 | 2,510.5 | 2,462.3 |
| Republish one transcript | 3.7 | 2.2 | 2.7 | 2.1 | 2.4 | 2.5 | 2.7 | 2.8 |
| Visibility update one transcript | 4.8 | 2.8 | 5.8 | 3.0 | 3.2 | 3.0 | 3.3 | 4.1 |
| Concurrent publish insert | 15.9 | 9.9 | 15.1 | 10.8 | 12.1 | 13.2 | 12.5 | 11.2 |
| Concurrent republish | 15.7 | 11.2 | 15.4 | 11.5 | 12.5 | 13.7 | 13.2 | 12.0 |
| Concurrent visibility update | 18.4 | 11.9 | 17.4 | 13.0 | 14.2 | 16.3 | 15.3 | 13.4 |

WAL bytes per operation, mean over the same runs. The percentage is against R0 of the same round.

| Operation | R0 r1 | R0 r2 | H1 r1 | H1 r2 | R2+H1 r1 | R2+H1 r2 | R3+H1 r1 | R3+H1 r2 |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| 800-row contribution, one transaction | 2,185,466 | 2,110,598 | 2,182,287 (-0.1%) | 2,194,958 (+4.0%) | 2,318,821 (+6.1%) | 2,330,418 (+10.4%) | 2,421,274 (+10.8%) | 2,414,793 (+14.4%) |
| Contribution, chunk of 100 | 277,148 | 276,422 | 286,076 (+3.2%) | 285,985 (+3.5%) | 306,358 (+10.5%) | 305,502 (+10.5%) | 316,624 (+14.3%) | 316,624 (+14.5%) |
| Republish one transcript | 526 | 530 | 536 (+1.9%) | 534 (+0.8%) | 538 (+2.3%) | 540 (+1.9%) | 537 (+2.1%) | 537 (+1.3%) |
| Visibility update, one transcript | 1,806 | 1,797 | 1,917 (+6.1%) | 1,932 (+7.5%) | 2,082 (+15.3%) | 2,063 (+14.8%) | 2,162 (+19.7%) | 2,194 (+22.1%) |

Round-to-round differences in p95 latency are larger than the differences between
configurations. For example, R3+H1 contribution in one transaction is -10.7
percent against R0 in round 1 and +24.5 percent in round 2. Latency comparisons
are therefore not decisive. WAL is deterministic per operation and is the more
reliable write-cost signal here.

### Budget comparison

| Measure (committed budget) | Result | Verdict |
|---|---|---|
| Chunk p95, 800 rows, primary workload, under 4 s | 1.98 to 2.92 s across all four configurations and both rounds | met |
| First page p95, under 800 ms, at 12,148 | 12.9 to 61.6 ms | met |
| First page p95 at 1,000,000 (not a budget, reported for scale) | R0 3,942, H1 831, R1+H1 388 ms | R0 misses; H1 is 31 ms over 800 ms |
| Picker p95 at 12,148, under 2 s | 15.1 to 85.0 ms | met |
| Uncapped snapshot p95, whole 12,148 library, under 5 s | 82.3 to 312.7 ms | met |
| Snapshot payload at 12,148, against 8 MiB | 2,359,366 bytes | met |
| Publish-path regression under 10 percent, measured as p95 | Chunk 1000 p95 against R0: H1 +3.8 and +4.4 percent; R2+H1 -19.6 and +12.1 percent; R3+H1 -10.7 and +24.5 percent | H1 within budget. R2+H1 and R3+H1 are not decidable from latency because the sign flips between rounds. |
| Publish-path regression under 10 percent, measured as WAL (not part of the committed rule) | H1 -0.1 to +4.0; R2+H1 +6.1 to +10.4; R3+H1 +10.8 to +14.4 percent per 800-row contribution | H1 within 10 percent. R3+H1 exceeds 10 percent in both rounds. |
| Index build at 1,000,000 under 150 s (proposed margin) | 0.66 to 2.70 s | met |

### Recommendation

This section applies the adoption rule committed before measurement. It was not
changed to fit the data.

Read targets per index: R1 targets first page, deep page, and picker. R2 targets
one small project, one large project, and the project snapshot. H1 targets helper
loading, family search, and helper expansion in the snapshot. An index is adopted
only if it improves its target by at least 30 percent at p95 against R0 on the same
dataset, its write regression stays under 10 percent, and one index is preferred
over two.

- **H1 is adopted.** At 12,148, its targets improve by 54 percent (first page with
  helpers) and 44 percent (family search) against R0. At 1,000,000 they improve by
  79 percent (first page), 66 percent (deep page), 55 percent (family search), and
  80 percent (40-row and 3,000-row projects). Its WAL overhead on contributions is
  within budget, at 0 to 4 percent.
- **R1 meets its target only at 12,148.** Against R0 it improves the first page by
  61 percent, the deep page by 56 percent, and the picker by 76 percent. At
  1,000,000 it improves the first page by 19 percent, the deep page by 23 percent,
  and the picker by 13 percent, all below the 30 percent threshold. Under the rule
  it is not adopted alone. R1 on top of H1 halves the 1,000,000 first page, from 831
  to 388 ms. That is a second index, so "prefer one index over two" applies.
  **This is the open decision for the owner.** The rule does not resolve it.
- **R2 is not adopted.** Alone it misses its target at 12,148 (the 40-row project
  improves by 29 percent, just below 30; the 3,000-row project is 110 percent
  slower). At 1,000,000 it improves the 40-row project by 37 percent but the
  3,000-row project by only 19 percent, and the 3,000-row snapshot is slower. Its
  benefit appears only together with H1 (R2+H1 reduces the 3,000-row project to 22
  ms), which again is a second index.
- **Chunk size 250 is recommended.** Its p95 stayed under 1 s in every configuration
  and both rounds, from 554 to 987 ms. The H1 round 2 value of 987 ms is close to
  the line. Chunks of 500 exceeded 1 s in every configuration. Chunks of 100 are
  faster per transaction, at 0.4 to 0.65 s p95, but send eight transactions per 800
  rows. Total run time per chunk size was recorded in the `note` field of the raw
  file, not as a separate figure, so the trade-off is stated from p95 only.
- **Snapshot verdict.** The uncapped whole-library snapshot meets the 5 s budget at
  12,148 with a wide margin (at most 0.31 s p95, 2.36 MB payload). It does not meet
  the budget at 1,000,000 (13 to 16 s p95, 194 MB payload, 0.8 to 1.2 GB peak heap).
  A single 250,000-row project snapshot takes 3.6 to 6.6 s p95 and returns 49.6 MB,
  which is over the 8 MiB reference. Per-project snapshots of 3,000 rows are 0.7 to
  2.0 s at 1,000,000. Any cap or pagination of the snapshot is therefore a
  requirement for the largest projects, not a choice this data settles.
- **Index set recommended for adoption now: H1 alone**, with the 1,000,000 first-page
  decision on R1 left open. No index helps the picker enough to pass the rule alone
  at 1,000,000.

### What the data does not support in the supplied reading

These are corrections to the reading supplied with the summary, each
checked against the raw files.

- "R1 only helps the cross-project first page" is not supported. R1 alone improves
  the 12,148 first page, deep page, and picker by 56 to 76 percent. At 1,000,000
  it helps the first page by only 19 percent alone, and the benefit is largest on
  top of H1.
- "R2 on top makes project-scoped reads milliseconds" is supported only for R2 with
  H1 (1.6 ms and 22 ms at 1,000,000). R2 alone is 0.96 to 1.76 seconds at 1,000,000.
- "No index helps the picker aggregate" is too strong at 12,148. R1 cuts the
  picker p95 from 85 to 21 ms. At 1,000,000 R3 and R1+H1 cut the p95 by about 30
  percent (1,957 and 1,935 ms against 2,808), which is at the threshold and within
  the run-to-run spread.
- "No write regression is measurable from any index" is not supported for WAL.
  R3+H1 adds 11 to 14 percent WAL per 800-row contribution, and R2+H1 adds 6 to 10
  percent. Latency p95 cannot separate the configurations, and the WAL numbers can.
- "H1 is required at 1,000,000" is supported. At 12,148 no read exceeds its budget
  even with R0, so H1 is required only for the 1,000,000 scale.
- The 1,000,000 first page with H1 alone is 831 ms, which is over the 800 ms budget
  value. The budget is defined at 12,148 and is not formally breached, but the margin
  at 1,000,000 is negative.
- "The per-project snapshot is 3 to 6 s for a 250,000-session project" is supported
  (3.6 to 6.6 s p95 across configurations). The payload of 49.6 MB exceeds the 8 MiB
  reference, which the supplied reading did not mention.
- "The whole-library snapshot at 1,000,000 used about 1 GB peak heap" is supported
  (0.77 to 1.24 GB). Its latency of 13 to 16 s p95 is the more important finding.

### Not measured

- Writes at 1,000,000. No write matrix was run at that size, so write cost at scale
  and the concurrent write run there are unknown.
- Concurrent readers during the snapshot. Snapshot latency was measured on an idle
  database, so interference from other sessions is unknown.
- Operating-system cold cache. Cold samples restarted PostgreSQL only.
- Production or CI hardware. All results are from one laptop running WSL2 with a
  Nix PostgreSQL build and non-CI settings.
- Concurrent index builds. Only plain `CREATE INDEX` was measured.

### Limits of the evidence

- One machine, one run per configuration at each size, with the sample counts
  stated above. Latency variance across repetitions and across rounds is of the
  same order as the differences between configurations. Differences below about 30
  percent are not reliable.
- The write path approximates the handler's outer authority transaction, the group
  policy read, and the publish lock. Republish and the publish insert are raw SQL,
  not the generated sqlc statements. The contribution path uses the generated
  statements for the share attempt, governance read, and metadata update.
- Reads at 1,000,000 used 20 warm repetitions and 2 cold samples. Their p95 is
  therefore less stable than at 12,148.
- Results depend on PostgreSQL 16.15 under Nix. CI runs a different build of the
  same version, with different defaults.

### Reproduce

From `backend/`, with the admin DSN pointing at a local PostgreSQL 16 server:

```sh
export BENCH_ADMIN_DATABASE_URL="postgres://<role>:<password>@127.0.0.1:<port>/postgres?sslmode=disable"
export BENCH_OUT_DIR=<output directory>
export BENCH_PG_CTL=<path to pg_ctl>   # optional, enables cold samples
export BENCH_PGDATA=<PGDATA directory> # optional, enables cold samples
cd .. && make contribution-bench BENCH_ARGS="seed -dataset reported -db village_bench_reported_base -replace"
make contribution-bench BENCH_ARGS="measure -dataset reported -config R1 -base village_bench_reported_base -reps 30 -cold-samples 3 -matrix reads,writes -concurrent-seconds 20"
make contribution-bench BENCH_ARGS="build -dataset reported -base village_bench_reported_base"
make contribution-bench BENCH_ARGS="summarize"
```

Each `measure` copies the base database into a scratch database, applies the
configuration's indexes, runs the matrix, and drops the copy. Run configurations
one at a time, because concurrent runs contend for the same machine and invalidate
the timings. `seed` applies the schema through the repository's own migration
runner. The Large dataset is seeded the same way with `-dataset large`.

### Test promotion checklist

This harness is a measurement tool, not a product invariant. Its own correctness is
covered by pure unit tests in `backend/cmd/contribution-bench` (determinism, shape
of the generated data, and error propagation). No integration test promotes the
measurement itself.

1. Subject: the benchmark's generated data and its error handling. It is not a
   product invariant, so the protected property is that the measurements are
   reproducible from the seed.
2. Necessity: the generator's determinism and the harness's failure exits can be
   checked directly, with no database, so no elaborate harness is needed for them.
3. Production path: the write and read statements are the repository's generated
   queries and real migrations, except for the approximations listed under Limits.
4. Cost: the full matrix runs for hours and needs a local PostgreSQL. It is not part
   of `go test ./...`. The unit tests run in under 0.1 s.
5. Lifetime: scratch databases are created with a prefix and dropped after each
   `measure`. Base databases persist and must be dropped by hand. A timeout of 90 s
   bounds each cold-cache restart.
6. Concurrency: each run uses its own scratch database. Two runs against the same
   server contend for CPU and locks and invalidate timings, so runs are sequential.
7. CI parity: the generator tests run in CI as part of `go test ./...`. The
   matrix does not run in CI. Environment values are explicit flags or
   environment variables.
8. Evidence: the results are raw JSON with plan files and a manifest, not the
   production path. They are evidence for the index decision, not for production
   behavior.
9. Mutation: a mutated generator changes the digest in the determinism test. A
   removed error check fails the propagation tests.
10. Exit condition: the harness can be retired once the index decision is made and
    recorded. Until then its maintenance cost is limited to the generated queries
    it mirrors, which must be updated if those queries change.
