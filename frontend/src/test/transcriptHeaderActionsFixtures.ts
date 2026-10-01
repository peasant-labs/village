import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { assertExactKeys, assertNamesMatch } from "@/test/fixtureAssertions";

/** Loader for `src/testdata/transcript-header-actions.yaml`. */

export type HeaderEntry =
  | "link"
  | "copy link"
  | "more"
  | "manage access"
  | "edit title"
  | "download markdown"
  | "download json"
  | "download jsonl";

export type HeaderViewer = "owner" | "reader" | "signed-out" | "preview";

export interface HeaderGroup {
  id: string;
  name: string;
  acceptance_mode: "open" | "verified_only" | "curated";
  member_count: number;
  takes: "approved" | "pending" | "skipped";
}

export interface HeaderContent {
  transcriptId: string;
  title: string;
  ownerId: string;
  ownerUsername: string;
  readerId: string;
  readerUsername: string;
  collectives: Array<{ id: string; name: string }>;
  groups: HeaderGroup[];
}

export interface HeaderCase {
  name: string;
  viewer: HeaderViewer;
  expectEntries: HeaderEntry[];
  expectAccessCaption: string | null;
  readsCollectives: boolean;
  visibility?: "private" | "shared" | "public";
}

export interface ManageAccessCase {
  name: string;
  action: "add" | "remove";
  collective: string;
  answer: number;
  visibility?: "private" | "shared" | "public";
  reopen?: boolean;
  expectRequest: string;
  expectAccess: string[];
  expectAccessCaption: string;
  expectMessage: string | null;
}

export interface DownloadCase {
  viewer: HeaderViewer;
  expectFileName: string;
  expectLines: string[];
}

export interface TranscriptHeaderActionsFixtures {
  entries: HeaderEntry[];
  content: HeaderContent;
  cases: HeaderCase[];
  manageAccessCases: ManageAccessCase[];
  download: DownloadCase;
  readRecovery: Array<{ name: string; path: string; message: string }>;
  consentCancel: { name: string; collective: string };
  departedCollective: { name: string; id: string; collective: string };
  rawDownloads: Array<{ name: string; format: "json" | "jsonl"; expectFileName: string }>;
}

/** The closed set of header entries. The fixture restates it; a drift is a
 *  loader failure, so an entry cannot be added to the page without a case. */
const HEADER_ENTRIES: readonly HeaderEntry[] = [
  "link",
  "copy link",
  "more",
  "manage access",
  "edit title",
  "download markdown",
  "download json",
  "download jsonl",
];

const VIEWERS: readonly HeaderViewer[] = ["owner", "reader", "signed-out", "preview"];

const requiredCaseNames = [
  "the-owner-sees-every-entry",
  "a-public-owner-sees-everyone-in-the-audience-caption",
  "a-signed-in-reader-can-copy-and-download-only",
  "a-signed-out-reader-can-copy-and-download-only",
  "the-preview-column-shows-no-header-entries",
] as const;

const requiredManageAccessCaseNames = [
  "removing-a-collective-withdraws-it-and-rereads-the-list",
  "a-failed-removal-keeps-the-collective-and-says-so",
  "adding-an-open-collective-lists-it",
  "adding-a-curated-collective-waits-for-its-approval",
  "adding-a-private-transcript-requires-consent",
  "a-collective-that-skips-the-submission-says-it-was-not-recorded",
] as const;

export function loadTranscriptHeaderActionsFixtures(): TranscriptHeaderActionsFixtures {
  const path = resolve(process.cwd(), "src/testdata/transcript-header-actions.yaml");
  const parsed: unknown = parse(readFileSync(path, "utf8"), { strict: true });
  if (parsed == null || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error("transcript-header-actions fixture root must be an object");
  }
  assertExactKeys(parsed, ["entries", "content", "cases", "manageAccessCases", "download", "rawDownloads", "readRecovery", "consentCancel", "departedCollective"], "fixture root");
  const fixtures = parsed as TranscriptHeaderActionsFixtures;

  if (JSON.stringify([...fixtures.entries].sort()) !== JSON.stringify([...HEADER_ENTRIES].sort())) {
    throw new Error(
      `transcript-header-actions entries differ from the page's closed set: got ${fixtures.entries.join(", ")}; ` +
        `want ${HEADER_ENTRIES.join(", ")}`,
    );
  }

  assertExactKeys(
    fixtures.content,
    ["transcriptId", "title", "ownerId", "ownerUsername", "readerId", "readerUsername", "collectives", "groups"],
    "content",
  );
  for (const g of fixtures.content.groups) {
    assertExactKeys(g, ["id", "name", "acceptance_mode", "member_count", "takes"], `content group ${g.name}`);
  }

  assertNamesMatch(fixtures.cases.map((c) => c.name), requiredCaseNames, "transcript-header-actions cases");
  for (const c of fixtures.cases) {
    assertExactKeys(c, ["name", "viewer", "expectEntries", "expectAccessCaption", "readsCollectives"].concat("visibility" in c ? ["visibility"] : []), `case ${c.name}`);
    if (!VIEWERS.includes(c.viewer)) throw new Error(`case ${c.name}: unknown viewer ${c.viewer}`);
    for (const e of c.expectEntries) {
      if (!HEADER_ENTRIES.includes(e)) throw new Error(`case ${c.name}: unknown header entry ${e}`);
    }
    const ownerOnly = c.expectEntries.includes("manage access") || c.expectEntries.includes("edit title");
    if (ownerOnly !== (c.viewer === "owner")) {
      throw new Error(`case ${c.name}: only the owner may be offered manage access and edit title`);
    }
    if ((c.expectAccessCaption != null) !== c.expectEntries.includes("manage access")) {
      throw new Error(`case ${c.name}: a who-can-read-it caption is stated exactly when manage access shows`);
    }
  }

  assertNamesMatch(
    fixtures.manageAccessCases.map((c) => c.name),
    requiredManageAccessCaseNames,
    "transcript-header-actions manageAccessCases",
  );
  for (const c of fixtures.manageAccessCases) {
    assertExactKeys(
      c,
      ["name", "action", "collective", "answer", "expectRequest", "expectAccess", "expectAccessCaption", "expectMessage"].concat(["visibility", "reopen"].filter((key) => key in c)),
      `manage access case ${c.name}`,
    );
    if (!fixtures.content.groups.some((g) => g.name === c.collective)) {
      throw new Error(`manage access case ${c.name}: ${c.collective} is not one of the owner's collectives`);
    }
  }

  assertExactKeys(fixtures.download, ["viewer", "expectFileName", "expectLines"], "download");
  assertNamesMatch(fixtures.rawDownloads.map((c) => c.name), ["download-json-from-the-mounted-menu", "download-jsonl-from-the-mounted-menu"], "raw downloads");
  for (const c of fixtures.rawDownloads) assertExactKeys(c, ["name", "format", "expectFileName"], c.name);
  assertNamesMatch(fixtures.readRecovery.map((c) => c.name), [
    "approved-audience-read-failure-is-retryable", "owner-contribution-read-failure-is-retryable",
    "owner-submission-read-failure-is-retryable", "picker-membership-read-failure-is-retryable",
  ], "read recovery");
  for (const c of fixtures.readRecovery) assertExactKeys(c, ["name", "path", "message"], c.name);
  assertExactKeys(fixtures.consentCancel, ["name", "collective"], "consent cancellation");
  assertNamesMatch([fixtures.consentCancel.name], ["cancelling-private-sharing-sends-no-request"], "consent cancellation");
  assertExactKeys(fixtures.departedCollective, ["name", "id", "collective"], "departed collective");
  assertNamesMatch([fixtures.departedCollective.name], ["a-departed-collective-stays-visible-and-withdrawable"], "departed collective");
  return fixtures;
}
