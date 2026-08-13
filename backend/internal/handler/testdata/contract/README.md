# Versioned push-contract golden corpus

Shared back-compat fixtures for the `peasant push` → village transcript wire.
This tree is **duplicated verbatim** in both repos (separate Go modules cannot
share a `testdata/` dir); keep the two copies byte-identical:

- peasant: `pkg/schema/testdata/contract/`
- village: `backend/internal/handler/testdata/contract/`

The back-compat suites in each repo read their local copy.

## Layout

```
contract/<version>/{valid,invalid}/{metadata,content,annotations}.json
```

- **metadata.json** — a `schema.PublishRequest` (the publish "metadata" multipart field).
- **content.json** — the transcript blob (the `transcript_file`): a
  `schema.TranscriptContent` envelope for the current shape, or a legacy raw blob.
- **annotations.json** — an array of `schema.AnnotationSummary`.

## Versions (≥2 legacy content shapes + current)

| Version | content.json shape | Exercises |
|---|---|---|
| `current` | `TranscriptContent` envelope (`contractVersion` 0.1.0, `kind` session_detail), `sessionDetail` with `harness` key, a **partType-bearing** turn, `scorecard`, `outcome` | the post-merge contract; migrate-on-read no-op (already canonical) |
| `legacy-provider-keyed` | a **bare** `SessionDetailPayload` using the **pre-flip** `"provider"` key + legacy value `claude`; metadata uses `model.modelHarness` | B3 migrate-on-read **key+value** migration (`provider`/`modelHarness`→`harness`, `claude`→`claude-code`) + rewrite |
| `legacy-raw-jsonl` | a raw provider **JSONL array** (no envelope); metadata uses `model.provider: gemini` | B3 raw-JSONL projection onto turns; metadata-surface `provider`→`harness` + `gemini`→`gemini-cli` |
| `legacy-metadata-field` | **current** envelope content, but `metadata.json` carries the legacy `model.modelHarness: claude` key | isolates the **SECOND wire surface** (B-IMP-1): `normalizeMetadataHarnessKey` on the publish path (`modelHarness`→`harness`, `claude`→`claude-code`), independent of the content shape |

## Invalid (negative) corpus — what each rejects

- `*/invalid/metadata.json` — a `source.format` **enum violation** (`xml`/`yaml`/`csv` ∉ {jsonl,json}) that otherwise passes the handler field checks. Drives the village **OpenAPI enforce → 422** path (N1/B5 invalid entry).
- `*/invalid/content.json` — a blob the **ContentMigrator** must error on (envelope with `kind` but no `sessionDetail`; whitespace-only; non-JSON). Asserts migrate-on-read fails loudly rather than rendering garbage.
- `*/invalid/annotations.json` — **malformed JSON** (rejected by `ValidateAnnotation`'s well-formed-JSON check).

## A3 — distinct floors (do NOT conflate)

These fixtures exercise the **display migrate-on-read** floor (village-side, how
far back a STORED blob normalizes for rendering). That is deliberately separate
from the **push-acceptance** floor (B2 negotiation, gates incoming uploads) and
from the **Phase-A SQL backfill** (storage normalization of `model_provider`).
The back-compat suite must not assert one in terms of another.
