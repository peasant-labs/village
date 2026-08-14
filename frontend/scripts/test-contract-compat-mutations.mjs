import { readFile, writeFile } from "node:fs/promises";
import { spawnSync } from "node:child_process";
import { parse } from "yaml";

const fixtureURL = new URL("./testdata/contract-compat-mutations.yaml", import.meta.url);
const parsed = parse(await readFile(fixtureURL, "utf8"), { strict: true });
if (parsed == null || typeof parsed !== "object" || Array.isArray(parsed) || Object.keys(parsed).join(",") !== "mutations") {
  throw new Error("contract compatibility mutation fixture root must contain only the mutations field");
}
const mutations = parsed.mutations;
const requiredNames = ["off-contract-harness-guard", "nullable-turns-fallback", "observed-model-pass-through"];
if (!Array.isArray(mutations) || mutations.length !== requiredNames.length) {
  throw new Error(`contract compatibility mutation fixture must contain exactly ${requiredNames.length} mutations`);
}
const actualNames = mutations.map(({ name }) => name).sort();
if (new Set(actualNames).size !== actualNames.length || JSON.stringify(actualNames) !== JSON.stringify([...requiredNames].sort())) {
  throw new Error(`contract compatibility mutation name inventory differs: got ${actualNames.join(", ")}; want ${requiredNames.sort().join(", ")}`);
}
for (const mutation of mutations) {
  const actualKeys = Object.keys(mutation).sort();
  const expectedKeys = ["file", "name", "original", "replacement", "testFile", "testName"];
  if (JSON.stringify(actualKeys) !== JSON.stringify(expectedKeys)) {
    throw new Error(`mutation ${mutation.name} has unknown or missing fields: got ${actualKeys.join(", ")}; want ${expectedKeys.join(", ")}`);
  }
  mutation.file = new URL(mutation.file, import.meta.url);
}

for (const mutation of mutations) {
  const source = await readFile(mutation.file, "utf8");
  if (source.split(mutation.original).length !== 2) {
    throw new Error(`${mutation.name} mutation anchor must occur exactly once`);
  }
  await writeFile(mutation.file, source.replace(mutation.original, mutation.replacement));
  try {
    const result = spawnSync("pnpm", ["exec", "vitest", "run", mutation.testFile, "-t", mutation.testName], {
      cwd: new URL("..", import.meta.url),
      encoding: "utf8",
    });
    if (result.status === 0) throw new Error(`${mutation.name} survived: focused production-path test remained green`);
    process.stdout.write(`mutation killed: ${mutation.name}\n`);
  } finally {
    await writeFile(mutation.file, source);
  }
}
