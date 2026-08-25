/**
 * Pure utility functions for quality analytics.
 */

// ---------------------------------------------------------------------------
// Project path decoding & display
// ---------------------------------------------------------------------------
//
// `displayProject` — a THIRD independent project-name derivation, alongside
// `extractProjectDisplayName` in `@/lib/format` — used to live here. It is
// deleted: every path now collapses to the ONE server-resolved
// `Transcript.project_display_name` (see `@/lib/types`), so a page rendering
// a project name reads that field directly rather than re-deriving one from
// a raw path/host-slug. Its one call site, `transcriptChrome.ts`'s
// `buildTranscriptBreadcrumb`, now takes the resolved name as its `project`
// input instead of calling this function.
