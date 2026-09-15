import { readFileSync } from "node:fs";
import { parse } from "yaml";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, screen, waitFor } from "@testing-library/react";
import type { SessionDetailPayload, SessionRelationship, SessionRelationshipNavigation, TurnDetail } from "@peasant-labs/schema";
import {
  installMountedRouteTeardown,
  installRESTFixture,
  renderProductionRoute,
  type MountedRouteTranscriptMetadata,
} from "@/test/mountedProductionRoute";
import { pushedRoutes, resetNextNavigation } from "@/test/nextNavigationMock";

// Mounts the REAL production route (TranscriptDetailPage -> SessionDetailV2 ->
// fairtrade TranscriptViewer) with REST mocked and NO mock of
// @peasant-labs/fairtrade. The assertions read the rendered session-context
// markup and drive the actual navigation callbacks, so a stub that never
// reached Fairtrade's adapter would fail here.

interface ParentNavigationExpected {
  labels: string[];
  links: string[];
  statuses?: string[];
  note?: string;
  earlier_content?: string;
}

interface ParentNavigationCase {
  name: string;
  relationships: SessionRelationship[];
  navigation: SessionRelationshipNavigation[];
  earlier_turn_content?: string;
  expected: ParentNavigationExpected;
}

const fixtures = parse(
  readFileSync("src/testdata/parent-navigation.yaml", "utf8"),
) as { required_names: string[]; cases: ParentNavigationCase[] };

const REQUIRED_NAMES = [
  "distinct-context-and-starter",
  "same-target-combines",
  "unavailable-shows-status",
  "inaccessible-shows-status",
  "context-general-note",
  "earlier-history-expands-locally",
];

function buildDetail(c: ParentNavigationCase): SessionDetailPayload {
  const turns: TurnDetail[] = [
    { index: 0, role: "user", content: "first request", timestamp: "2026-08-28T09:00:00.000Z", depth: 0, entryType: "text", sourceEntryRef: "e_main0" },
    { index: 1, role: "assistant", content: "first response", timestamp: "2026-08-28T09:01:00.000Z", depth: 0, entryType: "text", sourceEntryRef: "e_main1" },
  ];
  const detail: SessionDetailPayload = {
    id: `session-${c.name}`,
    harness: "codex",
    startTime: "2026-08-28T09:00:00.000Z",
    endTime: "2026-08-28T09:02:00.000Z",
    durationMins: 2,
    totalTokens: 20,
    tokensIn: 12,
    tokensOut: 8,
    turnCount: 2,
    toolCallCount: 0,
    relationships: c.relationships,
    turns,
  };
  if (c.earlier_turn_content) {
    detail.earlierHistory = [
      {
        state: "uncertain_migrated",
        turns: [
          { index: 0, role: "user", content: c.earlier_turn_content, timestamp: "2026-08-28T08:00:00.000Z", depth: 0, entryType: "text", sourceEntryRef: "e_earlier0" },
        ],
      },
    ];
  }
  return detail;
}

function installCase(c: ParentNavigationCase, transcriptID: string): void {
  const detail = buildDetail(c);
  const metadata: MountedRouteTranscriptMetadata = {
    transcript: {
      id: transcriptID,
      local_id: detail.id,
      visibility: "public",
      title: "Parent navigation evidence",
      description: null,
      project_name: "parent-navigation",
    },
    owner: { id: "fixture-owner" },
    enriched_shares: [],
    relationshipNavigation: c.navigation,
  };
  installRESTFixture(transcriptID, metadata, detail, "parent-navigation");
}

async function mountCase(c: ParentNavigationCase): Promise<string> {
  const transcriptID = `transcript-${c.name}`;
  installCase(c, transcriptID);
  await renderProductionRoute(transcriptID);
  await waitFor(() => expect(document.querySelector(".txn-app")).not.toBeNull());
  return transcriptID;
}

installMountedRouteTeardown();

afterEach(() => {
  resetNextNavigation();
});

describe("mounted parent/context navigation on the production transcript route", () => {
  it("covers every named fixture case", () => {
    expect(new Set(fixtures.required_names)).toEqual(new Set(REQUIRED_NAMES));
    expect(new Set(fixtures.cases.map((c) => c.name))).toEqual(new Set(REQUIRED_NAMES));
  });

  for (const c of fixtures.cases) {
    it(c.name, async () => {
      await mountCase(c);

      const rows = [...document.querySelectorAll(".txn-context-row")];
      expect(rows.map((row) => row.querySelector("span:not(.txn-context-status)")?.textContent)).toEqual(
        c.expected.labels,
      );

      const links = [...document.querySelectorAll<HTMLButtonElement>(".txn-context-link")];
      expect(links.map((link) => link.textContent)).toEqual(c.expected.links.map(() => "open current session"));

      if (c.expected.statuses) {
        const statuses = [...document.querySelectorAll(".txn-context-status")].map((node) => node.textContent);
        expect(statuses).toEqual(c.expected.statuses);
      }

      if (c.expected.note) {
        const note = document.querySelector(".txn-context-note");
        expect(note?.textContent).toContain(c.expected.note);
      }

      if (c.expected.earlier_content) {
        const toggle = document.querySelector<HTMLButtonElement>(".txn-earlier-toggle");
        expect(toggle).not.toBeNull();
        expect(toggle?.getAttribute("aria-expanded")).toBe("false");
        await userEvent.click(toggle!);
        expect(toggle?.getAttribute("aria-expanded")).toBe("true");
        await waitFor(() => expect(document.body.textContent).toContain(c.expected.earlier_content!));
      }

      // Clicking each link opens its own current target through the real
      // host callback — never a historical replay.
      for (let index = 0; index < links.length; index += 1) {
        resetNextNavigation();
        await userEvent.click(links[index]);
        expect(pushedRoutes).toEqual([`/transcripts/${c.expected.links[index]}`]);
      }

      // No parent fetch happened: installRESTFixture throws on any request the
      // route did not make, and an unavailable/inaccessible target renders no
      // link at all.
      if (c.expected.links.length === 0 && c.expected.statuses) {
        expect(document.querySelector(".txn-context-link")).toBeNull();
      }
    });
  }

  it("restores query, scroll, disclosure, and selection after Back from the current parent", async () => {
    const base = fixtures.cases.find((entry) => entry.name === "distinct-context-and-starter");
    if (!base) throw new Error("fixture case distinct-context-and-starter is missing");
    // The Back scenario needs both a link to follow and an earlier-history
    // disclosure (and enough turns) to restore.
    const c: ParentNavigationCase = { ...base, earlier_turn_content: "migrated earlier work" };
    const transcriptID = await mountCase(c);

    const earlierToggle = document.querySelector<HTMLButtonElement>(".txn-earlier-toggle");
    if (!earlierToggle) throw new Error("earlier-history disclosure did not mount");
    await userEvent.click(earlierToggle);
    expect(earlierToggle.getAttribute("aria-expanded")).toBe("true");

    fireEvent.keyDown(document, { key: "f", metaKey: true });
    const searchInput = await waitFor(() => {
      const input = document.querySelector<HTMLInputElement>(".txn-search-input");
      if (!input) throw new Error("search input did not open");
      return input;
    });
    fireEvent.change(searchInput, { target: { value: "needle" } });
    expect(searchInput.value).toBe("needle");

    const stream = document.querySelector<HTMLElement>(".txn-stream");
    if (!stream) throw new Error("transcript stream did not mount");
    stream.scrollTop = 120;
    fireEvent.scroll(stream);
    await waitFor(() => expect(document.querySelector(".txn-turnwrap[data-turn='1'] .txn-turn.txn-active")).not.toBeNull());

    // Follow the context link to its current target.
    const link = document.querySelector<HTMLButtonElement>(".txn-context-link");
    if (!link) throw new Error("context link did not mount");
    await act(async () => {
      await userEvent.click(link);
    });
    expect(pushedRoutes.some((href) => href.startsWith("/transcripts/"))).toBe(true);

    // Browser Back re-mounts the same child route. The host must restore the
    // reading position it persisted, not reset to a blank transcript.
    cleanup();
    resetNextNavigation();
    await renderProductionRoute(transcriptID);
    await waitFor(() => expect(document.querySelector(".txn-app")).not.toBeNull());

    const restoredToggle = document.querySelector<HTMLButtonElement>(".txn-earlier-toggle");
    await waitFor(() => expect(restoredToggle?.getAttribute("aria-expanded")).toBe("true"));

    const restoredStream = document.querySelector<HTMLElement>(".txn-stream");
    await waitFor(() => expect(restoredStream?.scrollTop).toBe(120));

    await waitFor(() => expect(document.querySelector(".txn-turnwrap[data-turn='1'] .txn-turn.txn-active")).not.toBeNull());

    fireEvent.keyDown(document, { key: "f", metaKey: true });
    const restoredInput = await waitFor(() => {
      const input = document.querySelector<HTMLInputElement>(".txn-search-input");
      if (!input) throw new Error("search input did not reopen");
      return input;
    });
    expect(restoredInput.value).toBe("needle");
  });
});
