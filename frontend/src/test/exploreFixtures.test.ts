import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { describe, expect, it } from "vitest";
import { validateExploreAdapterFixtures } from "./exploreAdapterFixtures";
import { validateExploreQueryBoundaryFixtures } from "./exploreQueryBoundaryFixtures";
import { validateExploreViewerFixtures } from "./exploreViewerFixtures";

const required = ["adapter-required-deletion", "adapter-duplicate", "adapter-invalid-name", "adapter-invalid-token", "adapter-invalid-expected", "query-required-deletion", "query-unsupported-kind", "query-invalid-accepted", "query-invalid-name", "query-unsupported-scope", "viewer-required-deletion", "viewer-empty-row", "viewer-empty-facet", "viewer-identical-row", "viewer-unsupported-kind"];
function read(name: string) { return parse(readFileSync(resolve(process.cwd(), `src/testdata/${name}.yaml`), "utf8")); }
type Rejection = { name: string; family: "adapter" | "query" | "viewer"; operation: "delete-case" | "duplicate-case" | "set-case" | "set-viewer"; target: string; field?: string; value?: unknown; error: string };
const fixtures = read("explore-fixture-rejections") as { cases: Rejection[] };
const names = new Set<string>();
for (const entry of fixtures.cases) {
  if (typeof entry.name !== "string" || !entry.name || names.has(entry.name)) throw new Error("rejection fixture names must be unique nonempty strings");
  names.add(entry.name);
  if (!["adapter", "query", "viewer"].includes(entry.family) || !["delete-case", "duplicate-case", "set-case", "set-viewer"].includes(entry.operation) || typeof entry.target !== "string" || !entry.target || typeof entry.error !== "string" || !entry.error) throw new Error("invalid rejection fixture");
  const setting = entry.operation.startsWith("set-");
  if (Object.keys(entry).sort().join(",") !== (setting ? "error,family,field,name,operation,target,value" : "error,family,name,operation,target") || (setting && typeof entry.field !== "string")) throw new Error("invalid rejection fixture fields");
}
for (const name of required) if (!names.has(name)) throw new Error(`missing rejection fixture ${name}`);
describe("Explore fixture guards fail for the declared reason", () => {
  for (const entry of fixtures.cases) it(entry.name, () => {
    const input = read(entry.family === "adapter" ? "explore-adapter" : entry.family === "query" ? "explore-query-boundary" : "explore-viewer-schedules");
    const index = input.cases.findIndex((c: { name: string }) => c.name === entry.target);
    if (entry.operation !== "set-viewer") expect(index).toBeGreaterThanOrEqual(0);
    switch (entry.operation) {
      case "delete-case": input.cases.splice(index, 1); break;
      case "duplicate-case": input.cases.push(structuredClone(input.cases[index])); break;
      case "set-case": input.cases[index][entry.field!] = entry.value; break;
      case "set-viewer":
        expect(input.viewers[entry.target]).toBeDefined();
        input.viewers[entry.target][entry.field!] = entry.value;
        break;
      default: throw new Error("unsupported rejection operation");
    }
    const validate = entry.family === "adapter" ? validateExploreAdapterFixtures : entry.family === "query" ? validateExploreQueryBoundaryFixtures : validateExploreViewerFixtures;
    expect(() => validate(input)).toThrow(entry.error);
  });
});
