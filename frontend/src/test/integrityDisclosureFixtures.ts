import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { assertExactKeys, assertNamesMatch } from "@/test/fixtureAssertions";

/** Loaders for the integrity disclosure corpora: the share-surface line and the privacy section. */

export type DisclosureCase = {
  name: string;
  surface: "manage_access" | "contribute_confirm" | "make_public" | "share_choice" | "already_public";
  visibility: "private" | "public";
  check: "shown" | "link" | "no_middle_dot" | "no_access_claim";
};

export type DisclosureFixtures = {
  line: string;
  linkLabel: string;
  href: string;
  forbidden: string[];
  cases: DisclosureCase[];
};

const REQUIRED_DISCLOSURE = [
  "disclosure_line_shown_private",
  "disclosure_line_shown_public",
  "links_to_privacy_integrity",
  "copy_has_no_middle_dot",
  "copy_never_claims_no_access",
  "disclosure_on_contribute_confirm",
  "disclosure_on_make_public",
  "disclosure_on_share_choice",
  "disclosure_on_already_public",
] as const;

export function loadDisclosureFixtures(): DisclosureFixtures {
  const path = resolve(__dirname, "../testdata/manage-access-disclosure.yaml");
  const raw = parse(readFileSync(path, "utf8")) as DisclosureFixtures;
  assertExactKeys(raw, ["line", "linkLabel", "href", "forbidden", "cases"], "manage-access-disclosure.yaml");
  raw.cases.forEach((c, i) => assertExactKeys(c, ["name", "surface", "visibility", "check"], `cases[${i}]`));
  assertNamesMatch(raw.cases.map((c) => c.name), REQUIRED_DISCLOSURE, "manage-access-disclosure cases");
  return raw;
}

export type PrivacyIntegrityCase = { name: string; contains: string[]; absent: string[] };

export type PrivacyIntegrityFixtures = {
  heading: string;
  noticeUpdated: string;
  cases: PrivacyIntegrityCase[];
};

const REQUIRED_PRIVACY = [
  "integrity_region_present",
  "heading_is_lowercase",
  "says_no_person_reads",
  "says_stores_nothing_logs_no_content",
  "says_only_the_result_is_kept",
  "says_servers_decrypt_for_every_read",
  "says_pending_submissions_are_covered",
  "says_editing_does_not_clear_a_flag",
  "says_recheck_when_the_check_is_updated",
  "says_checks_stop_only_when_nobody_else_can_read",
  "never_claims_no_access",
  "notice_date_bumped",
] as const;

export function loadPrivacyIntegrityFixtures(): PrivacyIntegrityFixtures {
  const path = resolve(__dirname, "../testdata/privacy-integrity.yaml");
  const raw = parse(readFileSync(path, "utf8")) as PrivacyIntegrityFixtures;
  assertExactKeys(raw, ["heading", "noticeUpdated", "cases"], "privacy-integrity.yaml");
  raw.cases.forEach((c, i) => assertExactKeys(c, ["name", "contains", "absent"], `cases[${i}]`));
  assertNamesMatch(raw.cases.map((c) => c.name), REQUIRED_PRIVACY, "privacy-integrity cases");
  return raw;
}
