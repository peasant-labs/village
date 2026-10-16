import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";

export interface LegacyCollectiveCase {
  name: string;
  suffix: string;
  query: string;
  body: string;
  role: "table" | "heading";
}

export function loadLegacyCollectiveFixtures(): LegacyCollectiveCase[] {
  const data = parse(readFileSync(resolve("src/testdata/collectives-legacy-redirects.yaml"), "utf8")) as {
    requiredNames: string[]; cases: LegacyCollectiveCase[];
  };
  const required = ["list", "detail", "settings", "contribute", "review"].sort();
  if (JSON.stringify([...data.requiredNames].sort()) !== JSON.stringify(required) ||
      JSON.stringify(data.cases.map((row) => row.name).sort()) !== JSON.stringify(required)) {
    throw new Error("Legacy collective redirect fixture names must retain every supported browser route");
  }
  for (const row of data.cases) {
    if (typeof row.suffix !== "string" || typeof row.query !== "string" || !row.body || !["table", "heading"].includes(row.role)) {
      throw new Error(`Invalid legacy collective fixture ${row.name}; provide route, query and mounted body expectation`);
    }
  }
  return data.cases;
}
