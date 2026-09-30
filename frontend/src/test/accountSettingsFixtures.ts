import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { assertExactKeys, assertNamesMatch } from "@/test/fixtureAssertions";

/**
 * Loader for `src/testdata/account-settings.yaml` — the case corpus behind
 * `src/accountSettings.test.tsx`.
 *
 * Deletion protection is the required-NAME manifest below. Every consistency
 * rule is derived from the fixture's own data, never from the page's code: a
 * rule that read a production constant would move with the code it is meant to
 * hold still.
 */

export type SettingsAction =
  | "none"
  | "change-handle"
  | "toggle-discoverable"
  | "sign-out-everywhere"
  | "delete-account";

export type SettingsServer = "ok" | "conflict" | "failure";

export type SettingsKey = {
  id: string;
  label: string | null;
  lastUsed: string | null;
  revoked: boolean;
};

export type SettingsCase = {
  name: string;
  why: string;
  discoverable: boolean;
  keys: SettingsKey[];
  action: SettingsAction;
  handleTo: string | null;
  server: SettingsServer;
  failOnKey: number | null;
  confirm: boolean;
  expectHandle: string;
  expectDiscoverable: boolean;
  expectPeasant: string;
  expectLastUsed: string | null;
  expectAlert: string | null;
  expectWrites: string[];
};

/** The handle every case's account starts with. */
export const SETTINGS_VIEWER = "alice-dev";

/** The label the CLI's sign-in writes on the keys it makes, as the corpus
 *  states it (not read from the page). */
const CLI_LABEL = "peasant-cli";

const requiredCaseNames = [
  "a-handle-change-saves-the-new-handle",
  "a-taken-handle-says-so-as-welcome-does",
  "the-discoverable-switch-turns-off",
  "the-discoverable-switch-turns-on",
  "a-failed-discoverable-write-restores-the-switch",
  "no-peasant-keys-says-peasant-is-not-signed-in",
  "one-peasant-key-is-one-computer",
  "several-peasant-keys-count-and-revoked-ones-do-not",
  "sign-out-everywhere-revokes-every-active-key",
  "a-failure-on-the-second-key-stops-and-says-so",
  "delete-account-confirmed-deletes-it",
  "delete-account-cancelled-deletes-nothing",
] as const;

const caseKeys = [
  "name",
  "why",
  "discoverable",
  "keys",
  "action",
  "handleTo",
  "server",
  "failOnKey",
  "confirm",
  "expectHandle",
  "expectDiscoverable",
  "expectPeasant",
  "expectLastUsed",
  "expectAlert",
  "expectWrites",
];
const keyKeys = ["id", "label", "lastUsed", "revoked"];
const actions: readonly SettingsAction[] = [
  "none",
  "change-handle",
  "toggle-discoverable",
  "sign-out-everywhere",
  "delete-account",
];
const servers: readonly SettingsServer[] = ["ok", "conflict", "failure"];

/** The keys a case's CLI sign-ins made that still work, in the order served. */
export function activeCliKeys(c: SettingsCase): SettingsKey[] {
  return c.keys.filter((key) => key.label === CLI_LABEL && !key.revoked);
}

function peasantLabel(count: number): string {
  return count === 0
    ? "peasant is not signed in on any computer"
    : `peasant on ${count} ${count === 1 ? "computer" : "computers"}`;
}

/** The writes a case's own inputs entail, in order. */
function writesFor(c: SettingsCase): string[] {
  switch (c.action) {
    case "none":
      return [];
    case "change-handle":
      return [`PATCH /auth/me/username ${JSON.stringify({ username: c.handleTo })}`];
    case "toggle-discoverable":
      return [`PATCH /auth/me/settings ${JSON.stringify({ is_discoverable: !c.discoverable })}`];
    case "sign-out-everywhere": {
      const active = activeCliKeys(c);
      const sent = c.failOnKey == null ? active : active.slice(0, c.failOnKey);
      return sent.map((key) => `DELETE /auth/api-keys/${key.id}`);
    }
    case "delete-account":
      return c.confirm ? ["DELETE /auth/me"] : [];
  }
}

export function loadAccountSettingsFixtures(): { cases: SettingsCase[] } {
  const fixturePath = resolve(process.cwd(), "src/testdata/account-settings.yaml");
  const parsed: unknown = parse(readFileSync(fixturePath, "utf8"), { strict: true });
  if (parsed == null || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error("account-settings fixture root must be an object");
  }
  assertExactKeys(parsed, ["cases"], "fixture root");
  const { cases } = parsed as { cases: SettingsCase[] };
  assertNamesMatch(
    cases.map((c) => c.name),
    requiredCaseNames,
    "account-settings cases",
  );

  for (const c of cases) {
    assertExactKeys(c, caseKeys, `settings case ${c.name}`);
    for (const key of c.keys) assertExactKeys(key, keyKeys, `settings case ${c.name} key ${key.id}`);
    if (!actions.includes(c.action)) {
      throw new Error(`settings case ${c.name}: ${c.action} is not one of ${actions.join(", ")}`);
    }
    if (!servers.includes(c.server)) {
      throw new Error(`settings case ${c.name}: ${c.server} is not one of ${servers.join(", ")}`);
    }
    if ((c.action === "change-handle") !== (c.handleTo !== null)) {
      throw new Error(`settings case ${c.name}: handleTo belongs to change-handle, and only to it`);
    }
    // A 409 means a handle is taken; nothing else on the page can conflict.
    if (c.server === "conflict" && c.action !== "change-handle") {
      throw new Error(`settings case ${c.name}: only a handle change can conflict`);
    }
    const active = activeCliKeys(c);
    if (c.failOnKey !== null) {
      if (c.action !== "sign-out-everywhere") {
        throw new Error(`settings case ${c.name}: failOnKey belongs to sign-out-everywhere`);
      }
      if (c.failOnKey < 1 || c.failOnKey > active.length) {
        throw new Error(
          `settings case ${c.name}: failOnKey ${c.failOnKey} names no revocation; ${active.length} are sent`,
        );
      }
    }
    if (!c.confirm && c.action !== "delete-account" && c.action !== "sign-out-everywhere") {
      throw new Error(`settings case ${c.name}: only an action behind an inline confirm can be cancelled`);
    }

    const wantWrites = writesFor(c);
    if (JSON.stringify(c.expectWrites) !== JSON.stringify(wantWrites)) {
      throw new Error(
        `settings case ${c.name}: its inputs send ${JSON.stringify(wantWrites)}; got ` +
          `${JSON.stringify(c.expectWrites)}`,
      );
    }
    const handleSaved = c.action === "change-handle" && c.server === "ok";
    if (c.expectHandle !== (handleSaved ? c.handleTo : SETTINGS_VIEWER)) {
      throw new Error(`settings case ${c.name}: expectHandle does not follow from the write's answer`);
    }
    const switched = c.action === "toggle-discoverable" && c.server === "ok";
    if (c.expectDiscoverable !== (switched ? !c.discoverable : c.discoverable)) {
      throw new Error(`settings case ${c.name}: expectDiscoverable does not follow from the write's answer`);
    }
    // What still works after the action: a completed sign-out leaves none; a
    // failed one leaves every key from the one that failed onwards.
    const remaining =
      c.action !== "sign-out-everywhere" || !c.confirm
        ? active.length
        : c.failOnKey == null
          ? 0
          : active.length - (c.failOnKey - 1);
    if (c.expectPeasant !== peasantLabel(remaining)) {
      throw new Error(
        `settings case ${c.name}: ${remaining} working peasant keys read as "${peasantLabel(remaining)}"; ` +
          `got "${c.expectPeasant}"`,
      );
    }
    if (remaining === 0 && c.expectLastUsed !== null) {
      throw new Error(`settings case ${c.name}: with no working key there is no "last used" to state`);
    }
    const failed = c.server !== "ok" || c.failOnKey !== null;
    if (failed !== (c.expectAlert !== null)) {
      throw new Error(
        `settings case ${c.name}: an alert is announced exactly when a write fails; server is ` +
          `${c.server} and failOnKey is ${c.failOnKey}, but expectAlert is ${c.expectAlert ?? "null"}`,
      );
    }
  }

  // A key that is revoked, and one with another label, must each be served
  // somewhere while NOT counted, or a page that counted every key would pass.
  if (!cases.some((c) => c.keys.some((k) => k.revoked && k.label === CLI_LABEL))) {
    throw new Error("account-settings cases: no case serves a revoked peasant-cli key");
  }
  if (!cases.some((c) => c.keys.some((k) => !k.revoked && k.label !== CLI_LABEL))) {
    throw new Error("account-settings cases: no case serves a working key with another label");
  }
  return { cases };
}
