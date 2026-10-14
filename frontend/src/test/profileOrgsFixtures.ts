import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { assertExactKeys, assertNamesMatch } from "./fixtureAssertions";

export type ProfileOrgCase = {
  name: string;
  why: string;
  orgs: Array<{ org_login: string; avatar_url: string | null }>;
  expect_section: boolean;
  expected_logins: string[];
};

/** What the profile page shows for the organizations a person has linked. */
export function loadProfileOrgsFixtures(): ProfileOrgCase[] {
  const raw = parse(readFileSync(resolve(process.cwd(), "src/testdata/profile-orgs.yaml"), "utf8"));
  assertExactKeys(raw, ["cases"], "profile-orgs root");
  if (!Array.isArray(raw.cases)) throw new Error("profile-orgs cases must be an array");
  for (const row of raw.cases) {
    assertExactKeys(
      row,
      ["name", "why", "orgs", "expect_section", "expected_logins"],
      "profile-orgs case",
    );
    if (typeof row.name !== "string" || typeof row.why !== "string") {
      throw new Error("invalid profile-orgs case name/why");
    }
    if (!Array.isArray(row.orgs)) throw new Error("profile-orgs case orgs must be an array");
    for (const org of row.orgs) {
      assertExactKeys(org, ["org_login", "avatar_url"], "profile-orgs org");
      if (typeof org.org_login !== "string" || (org.avatar_url !== null && typeof org.avatar_url !== "string")) {
        throw new Error("invalid profile-orgs org");
      }
    }
    if (typeof row.expect_section !== "boolean" || !Array.isArray(row.expected_logins)) {
      throw new Error("invalid profile-orgs expect_section/expected_logins");
    }
    if (row.expected_logins.some((login: unknown) => typeof login !== "string")) {
      throw new Error("invalid profile-orgs expected_logins");
    }
  }
  assertNamesMatch(
    raw.cases.map((row: ProfileOrgCase) => row.name),
    [
      "a_visitor_sees_the_organizations_linked_to_the_profile",
      "a_profile_with_no_linked_organizations_shows_no_section",
    ],
    "profile-orgs",
  );
  return raw.cases;
}
