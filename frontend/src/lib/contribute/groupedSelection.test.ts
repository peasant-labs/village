import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { expect, it } from "vitest";
import type { VillageSessionListItem, VillageTranscript } from "@peasant-labs/schema";
import { groupedContributionBatches } from "./groupedSelection";

interface SelectionFixtures {
  rows: { name: string; id: string; nested_group?: string }[];
  cases: { name: string; selected: string[]; expected_ids: string[]; context?: boolean; error_contains?: string }[];
}

function loadSelectionFixtures(): SelectionFixtures {
  const fixture: SelectionFixtures = parse(readFileSync(resolve(process.cwd(), "src/testdata/grouped-contribution-selection.yaml"), "utf8"));
  const rows = new Set(fixture.rows.map((row) => row.name));
  const names = new Set<string>();
  for (const c of fixture.cases) {
    if (!c.name || names.has(c.name) || c.selected.some((name) => !rows.has(name))) throw new Error("invalid grouped selection fixture identity");
    names.add(c.name);
  }
  for (const name of ["select-nested-second-only", "select-helper-owner-not-descendants", "repeated-member-remains-one-identity", "context-container-not-selectable"]) {
    if (!names.has(name)) throw new Error(`missing required selection fixture ${name}`);
  }
  return fixture;
}

// Use the same complete canonical session fixture as the backend constructors;
// only selected identities vary here. This is data supplied to the production
// selection function, not a second implementation of its selection algorithm.
const base: { session: string } = parse(readFileSync(resolve(process.cwd(), "../backend/internal/handler/testdata/collective_grouped_response.yaml"), "utf8"));
const fixture = loadSelectionFixtures();
const items = new Map<string, VillageSessionListItem>();
for (const row of fixture.rows) {
  const session: VillageTranscript = { ...JSON.parse(base.session), id: row.id, local_id: row.name };
  items.set(row.name, {
    kind: "transcript",
    transcript: {
      session,
      contributable: {
        id: session.id, local_id: session.local_id, title: session.title, visibility: session.visibility,
        project_hash: session.project_hash, project_display_name: session.project_display_name,
        project_name_source: session.project_name_source, git_branch: session.git_branch,
        parent_session_id: session.parent_session_id, session_origin: session.session_origin,
        model_provider: session.model_provider, published_at: session.published_at, already_shared: false,
        root_session_id: session.root_session_id, purpose: session.purpose, relationships: session.relationships,
      },
    },
    helperGroups: row.nested_group ? [{ groupId: row.nested_group, purpose: "helper_review", helperThreadCount: 1, memberScope: "scoped-child-query" }] : [],
  });
}

for (const c of fixture.cases) {
  it(c.name, () => {
    const selected = c.selected.map((name) => items.get(name)!);
    if (c.context) selected.push({ kind: "context_container", context: { groupId: "hg_context", ownerStatus: "known_unavailable" } });
    if (c.error_contains) {
      expect(() => groupedContributionBatches(selected)).toThrow(c.error_contains);
    } else {
      expect([...groupedContributionBatches(selected).values()].flat()).toEqual(c.expected_ids);
    }
  });
}
