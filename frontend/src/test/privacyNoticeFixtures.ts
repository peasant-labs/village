import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { assertExactKeys, assertNamesMatch } from "@/test/fixtureAssertions";

/**
 * Loader for `src/testdata/privacy-notice.yaml`, the corpus behind the privacy
 * notice route test and the notice-link tests.
 *
 * Deletion protection is a required-NAME manifest per group. The license
 * choice set is deliberately NOT pinned here: the test derives it from the
 * schema contract, so the fixture and the contract are held to each other
 * rather than to a copy of one of them.
 */

export type SectionHeadingCase = { name: string; heading: string };

export type LicenseChoiceCase = {
  name: string;
  /** The wire license id, or "none". */
  choice: string;
  /** The block heading as rendered. */
  heading: string;
  /** The exact consent-control label the block's prose quotes. */
  controlLabel: string;
};

export type NoticeLinkCase = { name: string; viewerUsername: string | null };

export type PrivacyNoticeFixtures = {
  sectionHeadings: SectionHeadingCase[];
  licenseChoices: LicenseChoiceCase[];
  linkCases: NoticeLinkCase[];
};

const REQUIRED_SECTION_HEADINGS = ["operator", "contributions"] as const;
const REQUIRED_LINK_CASES = ["publish-signed-out", "publish-signed-in"] as const;

export function loadPrivacyNoticeFixtures(): PrivacyNoticeFixtures {
  const path = resolve(__dirname, "../testdata/privacy-notice.yaml");
  const raw = parse(readFileSync(path, "utf8")) as PrivacyNoticeFixtures;
  assertExactKeys(raw, ["sectionHeadings", "licenseChoices", "linkCases"], "privacy-notice.yaml");

  raw.sectionHeadings.forEach((c, i) =>
    assertExactKeys(c, ["name", "heading"], `sectionHeadings[${i}]`),
  );
  assertNamesMatch(
    raw.sectionHeadings.map((c) => c.name),
    REQUIRED_SECTION_HEADINGS,
    "privacy-notice sectionHeadings",
  );

  raw.licenseChoices.forEach((c, i) =>
    assertExactKeys(c, ["name", "choice", "heading", "controlLabel"], `licenseChoices[${i}]`),
  );
  if (new Set(raw.licenseChoices.map((c) => c.choice)).size !== raw.licenseChoices.length) {
    throw new Error("privacy-notice licenseChoices must name each choice once");
  }

  raw.linkCases.forEach((c, i) =>
    assertExactKeys(c, ["name", "viewerUsername"], `linkCases[${i}]`),
  );
  assertNamesMatch(
    raw.linkCases.map((c) => c.name),
    REQUIRED_LINK_CASES,
    "privacy-notice linkCases",
  );
  return raw;
}
