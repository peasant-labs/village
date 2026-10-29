import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { assertExactKeys, assertNamesMatch } from "@/test/fixtureAssertions";

/** Loader for `src/testdata/transcript-integrity.yaml`, the corpus behind `transcriptIntegrityRoute.test.tsx`. */
export interface IntegrityCase {
  name: string;
  why: string;
  surface: "route" | "preview";
  visibility: "public" | "private";
  viewer: "signed-out" | "owner" | "member";
  /** The served `flagged_turns`, or null when the detail response carries no `integrity`. */
  integrity: number[] | null;
  rowHash: string;
  /** `same` serves the row hash, `none` sends no header, anything else is the header value. */
  header: string;
  expectBadge: boolean;
  expectMarks: number;
}

const required = [
  "flagged_public_passes_integrity_and_both_hashes",
  "flagged_private_shared_shows_badge_to_owner",
  "flagged_shared_shows_badge_to_member",
  "badge_visible_signed_out",
  "no_integrity_draws_nothing",
  "header_differs_from_row_hash_shows_badge_without_highlights",
  "missing_header_passes_null_hash",
  "preview_variant_passes_integrity_to_reviewer",
];

export function loadTranscriptIntegrityFixtures(): IntegrityCase[] {
  const { cases } = parse(readFileSync(resolve(process.cwd(), "src/testdata/transcript-integrity.yaml"), "utf8"), {
    strict: true,
  }) as { cases: IntegrityCase[] };
  assertNamesMatch(cases.map((c) => c.name), required, "transcript-integrity cases");
  for (const c of cases) {
    assertExactKeys(
      c,
      ["name", "why", "surface", "visibility", "viewer", "integrity", "rowHash", "header", "expectBadge",
        "expectMarks"],
      `transcript-integrity case ${c.name}`,
    );
    if (!c.why?.trim()) throw new Error(`transcript-integrity case ${c.name} states no reason it exists`);
  }
  return cases;
}
