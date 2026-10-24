import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";

export interface LegacyCollectiveCase {
  name: string;
  suffix: string;
  query: string;
  body: string;
  role: "table" | "heading";
  /** The sidebar entry this route marks current: the section or the collective. */
  navCurrent: string;
  nameEdits?: Array<{ name: string; value: string }>;
  assertions: Array<{
    role?: "table" | "button" | "searchbox" | "combobox" | "link" | "tab" | "navigation";
    name?: string;
    testId?: string;
    text?: string;
    disabled?: boolean;
  }>;
  back?: { name: string; href: string; role: "table" | "heading"; body: string };
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
	if (row.name === "settings" && (!row.nameEdits || JSON.stringify(row.nameEdits.map((step) => step.name)) !== JSON.stringify(["save-published-contract-name", "restore-original-collective-name"]) || row.nameEdits.some((step) => !step.value))) {
	  throw new Error("Settings journey must execute the named save and restore callbacks through the mounted production page");
	}
    if (typeof row.suffix !== "string" || typeof row.query !== "string" || !row.body || !["table", "heading"].includes(row.role)) {
      throw new Error(`Invalid legacy collective fixture ${row.name}; provide route, query and mounted body expectation`);
    }
    if (typeof row.navCurrent !== "string" || !row.navCurrent) {
      throw new Error(
        `Legacy collective fixture ${row.name} must name the sidebar entry its canonical route marks current ` +
        `(the section on the list, the collective on its own pages); the built journey asserts that one entry, ` +
        `so a route whose current place changed fails here instead of silently asserting nothing`,
      );
    }
    if (!Array.isArray(row.assertions) || row.assertions.length === 0 || row.assertions.some((assertion) =>
      assertion.testId ? typeof assertion.testId !== "string" || typeof assertion.text !== "string" :
        !["table", "button", "searchbox", "combobox", "link", "tab", "navigation"].includes(assertion.role ?? "") || typeof assertion.name !== "string")) {
      throw new Error(`Missing meaningful mounted body/control expectations for ${row.name}`);
    }
    if (row.name !== "list" && (!row.back || !row.back.href.startsWith("/collectives") || !row.back.name || !row.back.body)) {
      throw new Error(`Missing canonical back exit expectation for ${row.name}`);
    }
  }
  return data.cases;
}
