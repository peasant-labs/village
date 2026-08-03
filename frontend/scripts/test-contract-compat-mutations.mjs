import { readFile, writeFile } from "node:fs/promises";
import { spawnSync } from "node:child_process";

const mutations = [
  {
    name: "off-contract harness guard",
    file: new URL("../src/lib/adapters/explore.ts", import.meta.url),
    original: "if (isHarness(value)) return value;",
    replacement: "return value as Harness;",
    testName: "rejected-unknown-harness",
  },
  {
    name: "nullable turns fallback",
    file: new URL("../src/components/session-detail/v2/SessionDetailV2.tsx", import.meta.url),
    original: "const turns = useMemo(() => detail?.turns ?? [], [detail]);",
    replacement: "const turns = useMemo(() => detail!.turns!, [detail]);",
    testName: "null-turns",
  },
];

for (const mutation of mutations) {
  const source = await readFile(mutation.file, "utf8");
  if (source.split(mutation.original).length !== 2) {
    throw new Error(`${mutation.name} mutation anchor must occur exactly once`);
  }
  await writeFile(mutation.file, source.replace(mutation.original, mutation.replacement));
  try {
    const result = spawnSync("pnpm", ["exec", "vitest", "run", "src/finalContractCompatibility.test.tsx", "-t", mutation.testName], {
      cwd: new URL("..", import.meta.url),
      encoding: "utf8",
    });
    if (result.status === 0) throw new Error(`${mutation.name} survived: focused production-path test remained green`);
    process.stdout.write(`mutation killed: ${mutation.name}\n`);
  } finally {
    await writeFile(mutation.file, source);
  }
}
