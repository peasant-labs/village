import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { assertExactKeys, assertNamesMatch } from "./fixtureAssertions";
export type ProfileRecoveryCase = { name: string; first: "handle" | "discoverable"; handle: string; discoverable: boolean };
export function loadAccountProfileRecoveryFixtures(): { cases: ProfileRecoveryCase[]; logout: { name: string; error: string } } {
  const raw = parse(readFileSync(resolve(process.cwd(), "src/testdata/account-profile-recovery.yaml"), "utf8"), { strict: true });
  assertExactKeys(raw, ["cases", "logout"], "account-profile-recovery root");
  if (!Array.isArray(raw.cases)) throw new Error("profile recovery cases must be an array");
  for (const row of raw.cases) {
    assertExactKeys(row, ["name", "first", "handle", "discoverable"], "profile recovery case");
    if (!['handle', 'discoverable'].includes(row.first) || typeof row.handle !== 'string' || !row.handle || typeof row.discoverable !== 'boolean') throw new Error("invalid profile recovery fixture");
  }
  assertNamesMatch(raw.cases.map((row: ProfileRecoveryCase) => row.name), ["a-delayed-handle-response-cannot-restore-old-discoverability", "a-delayed-discoverable-response-cannot-restore-the-old-handle"], "profile recovery");
  assertExactKeys(raw.logout, ["name", "error"], "logout recovery");
  if (raw.logout.name !== "a-failed-sign-out-announces-the-error-and-offers-another-attempt" || typeof raw.logout.error !== 'string' || !raw.logout.error) throw new Error("invalid logout recovery fixture");
  return raw;
}
