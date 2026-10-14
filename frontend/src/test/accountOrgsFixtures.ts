import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { assertExactKeys, assertNamesMatch } from "./fixtureAssertions";

export type AccountOrgListState = "linked-rows" | "empty-guidance" | "read-error";
export type AccountOrgCase = {
  name: string;
  why: string;
  orgs: Array<{ org_login: string; visible: boolean; avatar_url: string | null }>;
  read: "ok" | "failed";
  write: "ok" | "failed";
  action: "toggle" | "none" | "retry";
  target: string;
  expected_visible: boolean;
  expected_alert: string | null;
  expected_list_state: AccountOrgListState;
};

/** What `/settings` does with the caller's GitHub organizations, one case per way that can go. */
export function loadAccountOrgsFixtures(): AccountOrgCase[] {
  const raw = parse(readFileSync(resolve(process.cwd(), "src/testdata/account-orgs.yaml"), "utf8"));
  assertExactKeys(raw, ["cases"], "account-orgs root");
  if (!Array.isArray(raw.cases)) throw new Error("account-orgs cases must be an array");
  for (const row of raw.cases) {
    assertExactKeys(
      row,
      [
        "name",
        "why",
        "orgs",
        "read",
        "write",
        "action",
        "target",
        "expected_visible",
        "expected_alert",
        "expected_list_state",
      ],
      "account-orgs case",
    );
    if (typeof row.name !== "string" || typeof row.why !== "string" || typeof row.target !== "string") {
      throw new Error("invalid account-orgs case name/why/target");
    }
    if (!Array.isArray(row.orgs)) throw new Error("account-orgs case orgs must be an array");
    for (const org of row.orgs) {
      assertExactKeys(org, ["org_login", "visible", "avatar_url"], "account-orgs org");
      if (
        typeof org.org_login !== "string" ||
        typeof org.visible !== "boolean" ||
        (org.avatar_url !== null && typeof org.avatar_url !== "string")
      ) {
        throw new Error("invalid account-orgs org");
      }
    }
    if (!["ok", "failed"].includes(row.read) || !["ok", "failed"].includes(row.write)) {
      throw new Error("invalid account-orgs read/write");
    }
    if (!["toggle", "none", "retry"].includes(row.action)) throw new Error("invalid account-orgs action");
    if (!["linked-rows", "empty-guidance", "read-error"].includes(row.expected_list_state)) {
      throw new Error("invalid account-orgs expected_list_state");
    }
    if (row.expected_alert !== null && typeof row.expected_alert !== "string") {
      throw new Error("invalid account-orgs expected_alert");
    }
  }
  assertNamesMatch(
    raw.cases.map((row: AccountOrgCase) => row.name),
    [
      "linking_an_organization_shows_it_on_the_profile",
      "unlinking_a_linked_organization",
      "a_refused_write_puts_the_previous_link_back",
      "an_account_with_no_organizations_is_told_how_to_refresh",
      "an_unreadable_list_is_reported_and_offers_a_retry",
    ],
    "account-orgs",
  );
  return raw.cases;
}
