import { readFile, writeFile } from "node:fs/promises";
import { spawnSync } from "node:child_process";
import { parse } from "yaml";

const fixtureURL = new URL("./testdata/transcript-query-guard-mutations.yaml", import.meta.url);
const root = parse(await readFile(fixtureURL, "utf8"), { strict: true });
if (root == null || typeof root !== "object" || Array.isArray(root) || Object.keys(root).join(",") !== "mutations") {
  throw new Error("transcript query guard mutation fixture root must contain only mutations");
}
const requiredNames = ["response-pagination-guard-bypass"];
if (!Array.isArray(root.mutations) || root.mutations.length !== requiredNames.length) {
  throw new Error(`transcript query guard fixture must contain exactly ${requiredNames.length} mutation`);
}
const names = root.mutations.map(({ name }) => name);
if (new Set(names).size !== names.length || JSON.stringify([...names].sort()) !== JSON.stringify([...requiredNames].sort())) {
  throw new Error("transcript query guard mutation name inventory differs");
}

for (const mutation of root.mutations) {
  const fields = ["file", "name", "original", "replacement", "testFile", "testName"];
  if (JSON.stringify(Object.keys(mutation).sort()) !== JSON.stringify(fields.sort())) {
    throw new Error(`${mutation.name} has unknown or missing fields`);
  }
  const sourceURL = new URL(mutation.file, fixtureURL);
  const source = await readFile(sourceURL, "utf8");
  if (source.split(mutation.original).length !== 2) {
    throw new Error(`${mutation.name} anchor must occur exactly once`);
  }
  await writeFile(sourceURL, source.replace(mutation.original, mutation.replacement));
  try {
    const result = spawnSync("pnpm", ["exec", "vitest", "run", mutation.testFile, "-t", mutation.testName], {
      cwd: new URL("..", import.meta.url),
      encoding: "utf8",
    });
    if (result.status === 0) throw new Error(`${mutation.name} survived: the production-hook cache test remained green`);
    process.stdout.write(`mutation killed: ${mutation.name}\n`);
  } finally {
    await writeFile(sourceURL, source);
  }
}
