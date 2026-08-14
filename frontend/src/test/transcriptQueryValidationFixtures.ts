import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";

export type QueryValidationAction = "settle" | "retry" | "seededNewKey";
export type QueryValidationResponse = { page: number; limit: number; total: number };
export type QueryValidationFixture = {
  name: string;
  action: QueryValidationAction;
  params: Record<string, string>;
  mismatchParams?: Record<string, string>;
  nextParams?: Record<string, string>;
  responses: QueryValidationResponse[];
  initialStatus: "success" | "error";
  finalPage?: number;
  errorFragments: string[];
};

const requiredNames = [
  "valid-settled-data-enters-cache",
  "page-mismatch-rejected-before-cache-commit",
  "limit-mismatch-rejected-before-cache-commit",
  "omitted-pagination-remains-compatible",
  "mismatch-retries-the-exact-key",
  "rejected-mismatch-cannot-placeholder-a-new-key",
] as const;

function isObject(value: unknown): value is Record<string, unknown> {
  return value != null && typeof value === "object" && !Array.isArray(value);
}

function assertKeys(value: Record<string, unknown>, required: string[], optional: string[], where: string): void {
  const allowed = new Set([...required, ...optional]);
  const actual = Object.keys(value);
  if (actual.some((key) => !allowed.has(key)) || required.some((key) => !(key in value))) {
    throw new Error(`${where} has unknown or missing fields`);
  }
}

function stringRecord(value: unknown, where: string): Record<string, string> {
  if (!isObject(value) || Object.values(value).some((item) => typeof item !== "string")) {
    throw new Error(`${where} must be a string record`);
  }
  return value as Record<string, string>;
}

export function loadTranscriptQueryValidationFixtures(): QueryValidationFixture[] {
  const path = resolve(process.cwd(), "src/testdata/transcript-query-validation.yaml");
  const root: unknown = parse(readFileSync(path, "utf8"), { strict: true });
  if (!isObject(root)) throw new Error("query validation fixture root must be an object");
  assertKeys(root, ["cases"], [], "query validation fixture root");
  if (!Array.isArray(root.cases)) throw new Error("query validation cases must be an array");

  const cases = root.cases.map((raw, index): QueryValidationFixture => {
    if (!isObject(raw)) throw new Error(`query validation case ${index} must be an object`);
    assertKeys(raw, ["name", "action", "params", "responses", "initialStatus", "errorFragments"], ["mismatchParams", "nextParams", "finalPage"], `query validation case ${index}`);
    if (typeof raw.name !== "string" || !["settle", "retry", "seededNewKey"].includes(String(raw.action))) {
      throw new Error(`query validation case ${index} has invalid name or action`);
    }
    if (!Array.isArray(raw.responses) || raw.responses.length === 0) throw new Error(`${raw.name} responses must be nonempty`);
    const responses = raw.responses.map((response, responseIndex): QueryValidationResponse => {
      if (!isObject(response)) throw new Error(`${raw.name} response ${responseIndex} must be an object`);
      assertKeys(response, ["page", "limit", "total"], [], `${raw.name} response ${responseIndex}`);
      if (![response.page, response.limit, response.total].every(Number.isSafeInteger)) {
        throw new Error(`${raw.name} response ${responseIndex} values must be safe integers`);
      }
      return response as QueryValidationResponse;
    });
    if (!Array.isArray(raw.errorFragments) || raw.errorFragments.some((value) => typeof value !== "string" || value.length === 0)) {
      throw new Error(`${raw.name} errorFragments must be nonempty strings`);
    }
    const action = raw.action as QueryValidationAction;
    const expectedResponses = action === "settle" ? 1 : action === "seededNewKey" ? 3 : 2;
    if (responses.length !== expectedResponses) {
      throw new Error(`${raw.name} response count does not match action ${action}`);
    }
    if ((action === "seededNewKey") !== ("nextParams" in raw)) throw new Error(`${raw.name} nextParams presence does not match action`);
    if ((action === "seededNewKey") !== ("mismatchParams" in raw)) throw new Error(`${raw.name} mismatchParams presence does not match action`);
    if (raw.initialStatus !== "success" && raw.initialStatus !== "error") throw new Error(`${raw.name} initialStatus is invalid`);
    if (action !== "seededNewKey" && (raw.initialStatus === "error") !== (raw.errorFragments.length > 0)) throw new Error(`${raw.name} error fragments contradict initial status`);
    if (raw.initialStatus === "success" && typeof raw.finalPage !== "number") throw new Error(`${raw.name} successful result requires finalPage`);
    if (action !== "settle" && typeof raw.finalPage !== "number") throw new Error(`${raw.name} follow-up action requires finalPage`);
    return {
      name: raw.name,
      action,
      params: stringRecord(raw.params, `${raw.name} params`),
      ...(action === "seededNewKey" ? { mismatchParams: stringRecord(raw.mismatchParams, `${raw.name} mismatchParams`) } : {}),
      ...(action === "seededNewKey" ? { nextParams: stringRecord(raw.nextParams, `${raw.name} nextParams`) } : {}),
      responses,
      initialStatus: raw.initialStatus,
      ...(typeof raw.finalPage === "number" ? { finalPage: raw.finalPage } : {}),
      errorFragments: raw.errorFragments as string[],
    };
  });
  const names = cases.map(({ name }) => name);
  if (new Set(names).size !== names.length || JSON.stringify([...names].sort()) !== JSON.stringify([...requiredNames].sort())) {
    throw new Error("query validation case name inventory differs");
  }
  return cases;
}
