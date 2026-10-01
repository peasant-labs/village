import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { assertExactKeys, assertNamesMatch } from "./fixtureAssertions";
export type ProfileRecoveryCase = { name: string; first: "handle" | "discoverable"; handle: string; discoverable: boolean };
type AccountSwitchCase = ProfileRecoveryCase & { nextAccountID: string; changeCredential: boolean; nextHandle: string; nextDiscoverable: boolean };
export function loadAccountProfileRecoveryFixtures(): { headerPending: { name: string }; accountSwitchCases: AccountSwitchCase[]; cases: ProfileRecoveryCase[]; logout: { name: string; error: string }; headerLogout: { name: string; error: string } } {
  const raw = parse(readFileSync(resolve(process.cwd(), "src/testdata/account-profile-recovery.yaml"), "utf8"), { strict: true });
  assertExactKeys(raw, ["cases", "logout", "headerLogout", "accountSwitchCases", "headerPending"], "account-profile-recovery root");
  if (!Array.isArray(raw.cases)) throw new Error("profile recovery cases must be an array");
  for (const row of raw.cases) {
    assertExactKeys(row, ["name", "first", "handle", "discoverable"], "profile recovery case");
    if (!['handle', 'discoverable'].includes(row.first) || typeof row.handle !== 'string' || !row.handle || typeof row.discoverable !== 'boolean') throw new Error("invalid profile recovery fixture");
  }
  assertNamesMatch(raw.cases.map((row: ProfileRecoveryCase) => row.name), ["a-delayed-handle-response-cannot-restore-old-discoverability", "a-delayed-discoverable-response-cannot-restore-the-old-handle"], "profile recovery");
  assertExactKeys(raw.logout, ["name", "error"], "logout recovery");
  if (raw.logout.name !== "a-failed-sign-out-announces-the-error-and-offers-another-attempt" || typeof raw.logout.error !== 'string' || !raw.logout.error) throw new Error("invalid logout recovery fixture");
  assertExactKeys(raw.headerLogout, ["name", "error"], "header logout recovery");
  if (raw.headerLogout.name !== "the-header-sign-out-announces-a-failed-request-and-can-retry" || typeof raw.headerLogout.error !== 'string' || !raw.headerLogout.error) throw new Error("invalid header logout fixture");
  if (!Array.isArray(raw.accountSwitchCases)) throw new Error("account switch cases must be an array");
  for (const row of raw.accountSwitchCases) {
    assertExactKeys(row, ["name", "first", "handle", "discoverable", "nextAccountID", "changeCredential", "nextHandle", "nextDiscoverable"], "account switch case");
    if (!["handle", "discoverable"].includes(row.first) || typeof row.handle !== "string" || !row.handle || typeof row.discoverable !== "boolean" || typeof row.nextAccountID !== "string" || !row.nextAccountID || typeof row.changeCredential !== "boolean" || typeof row.nextHandle !== "string" || !row.nextHandle || typeof row.nextDiscoverable !== "boolean") throw new Error("invalid account switch fixture");
  }
  assertNamesMatch(raw.accountSwitchCases.map((row: AccountSwitchCase) => row.name), ["a-queued-discoverability-save-cannot-write-under-a-new-account", "a-queued-handle-save-cannot-write-under-a-new-account", "a-changed-credential-blocks-a-queued-save-before-account-refresh", "a-changed-account-blocks-a-queued-save-with-the-same-credential"], "account switch recovery");
  assertExactKeys(raw.headerPending, ["name"], "header pending sign out");
  if (raw.headerPending.name !== "the-header-sign-out-prevents-repeat-writes-while-the-request-is-pending") throw new Error("invalid header pending fixture");
  return raw;
}
