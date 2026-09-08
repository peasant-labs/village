import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";

const REQUIRED_NAMES = ["anonymous-root-preserves-filters", "anonymous-root-preserves-retained-failure", "anonymous-root-outage-preserves-intent", "signed-root-preserves-home", "signed-root-outage-keeps-home", "welcome-preserves-edited-handle", "welcome-outage-does-not-redirect", "collective-preserves-create-draft", "collective-outage-preserves-draft", "publish-preserves-open-dialog", "publish-outage-keeps-dialog"];
REQUIRED_NAMES.push("signed-root-replaces-viewer", "signed-root-expiry-opens-anonymous");
export type BackgroundCase = { name: string; route: "anonymous-root" | "signed-root" | "welcome" | "groups" | "publish"; outcome: "success" | "failure" | "viewer-change" | "expired"; retained_failure: boolean };
export function loadAuthBackgroundFixtures(): BackgroundCase[] {
  const input = parse(readFileSync(resolve(process.cwd(), "src/testdata/auth-background-lifecycle.yaml"), "utf8"));
  if (!input || Object.keys(input).join(",") !== "cases" || !Array.isArray(input.cases) || !input.cases.length) throw new Error("auth background fixture requires cases");
  const names = new Set<string>();
  const cases = input.cases.map((value: unknown) => {
    if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("auth background case must be object");
    const entry = value as Record<string, unknown>;
    if (Object.keys(entry).sort().join(",") !== "name,outcome,retained_failure,route" || typeof entry.name !== "string" || !entry.name.trim() || names.has(entry.name)) throw new Error("auth background case requires strict fields and unique nonempty name");
    names.add(entry.name);
    if (!["anonymous-root", "signed-root", "welcome", "groups", "publish"].includes(entry.route as string) || !["success", "failure", "viewer-change", "expired"].includes(entry.outcome as string) || typeof entry.retained_failure !== "boolean") throw new Error("unsupported auth background case values");
    if ((entry.outcome === "viewer-change" || entry.outcome === "expired") && entry.route !== "signed-root") throw new Error("identity transition requires signed root");
    if (entry.retained_failure && entry.route !== "anonymous-root") throw new Error("retained failure requires discovery route");
    return entry as BackgroundCase;
  });
  for (const name of REQUIRED_NAMES) if (!names.has(name)) throw new Error(`missing auth background case ${name}`);
  return cases;
}
