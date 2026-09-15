import { readFileSync } from "node:fs";
import { parse } from "yaml";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";
import { cleanup, fireEvent, waitFor } from "@testing-library/react";
import type {
  SessionDetailPayload,
  SessionRelationship,
  SessionRelationshipNavigation,
  ToolCallDetail,
  TurnDetail,
} from "@peasant-labs/schema";
import {
  installMountedRouteTeardown,
  installRESTFixture,
  renderProductionRoute,
  type MountedRouteTranscriptMetadata,
} from "@/test/mountedProductionRoute";
import { pushedRoutes, resetNextNavigation } from "@/test/nextNavigationMock";
import { writeTranscriptReadState } from "@/lib/transcriptReadState";

// Mounts the REAL production route (TranscriptDetailPage -> SessionDetailV2 ->
// fairtrade TranscriptViewer) with REST mocked and NO mock of
// @peasant-labs/fairtrade. The assertions read the rendered session-context
// markup and drive the actual navigation callbacks, so a stub that never
// reached Fairtrade's adapter would fail here. Following a context link also
// mounts the actual TARGET route so the branch point and Back are exercised on
// production paths, not just asserted as a pushed string.

interface AnchorExpectation {
  entry: string;
  entry_kind: string;
  entry_revision: string;
}

interface TargetTurnCase {
  source_ref?: string;
  role: string;
  content: string;
  tool_calls?: Array<{ call_ref?: string; result_ref?: string }>;
}

interface ParentNavigationTarget {
  content_hash: string;
  turns: TargetTurnCase[];
}

interface ParentNavigationExpected {
  labels: string[];
  links: string[];
  statuses?: string[];
  note?: string;
  earlier_content?: string;
  link_anchors?: Array<AnchorExpectation | null>;
  target_active_ref?: string | null;
}

interface ParentNavigationCase {
  name: string;
  relationships: SessionRelationship[];
  navigation: SessionRelationshipNavigation[];
  earlier_turn_content?: string;
  target?: ParentNavigationTarget;
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
  "context-anchor-via-folded-call-ref",
  "context-target-revision-changed",
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

/** The CURRENT target's own durable content, served by the mounted target route. */
function buildTargetDetail(target: ParentNavigationTarget): SessionDetailPayload {
  const turns: TurnDetail[] = target.turns.map((turn, i) => {
    const detail: TurnDetail = {
      index: i,
      role: turn.role as TurnDetail["role"],
      content: turn.content,
      timestamp: `2026-08-28T10:0${i}:00.000Z`,
      depth: 0,
      entryType: "text",
    };
    if (turn.source_ref) detail.sourceEntryRef = turn.source_ref;
    if (turn.tool_calls?.length) {
      detail.toolCalls = turn.tool_calls.map<ToolCallDetail>((call, n) => ({
        id: `call-${i}-${n}`,
        name: "Read",
        arguments: "{}",
        result: "ok",
        ...(call.call_ref ? { callEntryRef: call.call_ref } : {}),
        ...(call.result_ref ? { resultEntryRef: call.result_ref } : {}),
      }));
    }
    return detail;
  });
  return {
    id: `session-target-${target.content_hash}`,
    harness: "codex",
    startTime: "2026-08-28T10:00:00.000Z",
    endTime: "2026-08-28T10:05:00.000Z",
    durationMins: 5,
    totalTokens: 30,
    tokensIn: 20,
    tokensOut: 10,
    turnCount: turns.length,
    toolCallCount: turns.reduce((n, turn) => n + (turn.toolCalls?.length ?? 0), 0),
    turns,
  };
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

/** Install the CURRENT target route's fixture, with its own public revision. */
function installTargetFixture(c: ParentNavigationCase, targetID: string): void {
  if (!c.target) throw new Error(`fixture case ${c.name} declares no target content`);
  const detail = buildTargetDetail(c.target);
  const metadata: MountedRouteTranscriptMetadata = {
    transcript: {
      id: targetID,
      local_id: detail.id,
      visibility: "public",
      title: "Branch point target",
      description: null,
      project_name: "parent-navigation",
      content_hash: c.target.content_hash,
    },
    owner: { id: "fixture-owner" },
    enriched_shares: [],
  };
  installRESTFixture(targetID, metadata, detail, "parent-navigation-target");
}

async function mountCase(c: ParentNavigationCase): Promise<string> {
  const transcriptID = `transcript-${c.name}`;
  installCase(c, transcriptID);
  await renderProductionRoute(transcriptID);
  await waitFor(() => expect(document.querySelector(".txn-app")).not.toBeNull());
  return transcriptID;
}

function caseNamed(name: string): ParentNavigationCase {
  const c = fixtures.cases.find((entry) => entry.name === name);
  if (!c) throw new Error(`fixture case ${name} is missing`);
  return c;
}

function splitHref(href: string): { targetID: string; search: string } {
  const url = new URL(href, "http://localhost");
  const targetID = decodeURIComponent(url.pathname.replace(/^\/transcripts\//, ""));
  return { targetID, search: url.search };
}

/** Click the actual canonical context button and return the pushed href. */
async function followContextLink(): Promise<string> {
  const link = document.querySelector<HTMLButtonElement>(".txn-context-link");
  if (!link) throw new Error("context link did not mount");
  resetNextNavigation();
  fireEvent.click(link);
  await waitFor(() => expect(pushedRoutes.length).toBeGreaterThan(0));
  const href = pushedRoutes[0];
  if (!href) throw new Error("context link did not push a route");
  return href;
}

/** The turnwrap the anchor is expected to select, by cooked source ref. */
function turnwrapByRef(ref: string): HTMLElement | null {
  return document.querySelector<HTMLElement>(`.txn-turnwrap[data-source-entry-ref='${ref}']`);
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

      // Clicking each link opens its own current target through the real host
      // callback. A context link that carries a verified exact branch point
      // must push that anchor too; a general link stays the bare route.
      for (let index = 0; index < links.length; index += 1) {
        resetNextNavigation();
        await userEvent.click(links[index]);
        expect(pushedRoutes).toHaveLength(1);
        const url = new URL(pushedRoutes[0], "http://localhost");
        expect(url.pathname).toBe(`/transcripts/${c.expected.links[index]}`);
        const anchor = c.expected.link_anchors?.[index] ?? null;
        if (anchor) {
          expect(url.searchParams.get("entry")).toBe(anchor.entry);
          expect(url.searchParams.get("entryKind")).toBe(anchor.entry_kind);
          expect(url.searchParams.get("entryRevision")).toBe(anchor.entry_revision);
        } else {
          expect(url.search).toBe("");
        }
      }

      // No parent fetch happened: installRESTFixture throws on any request the
      // route did not make, and an unavailable/inaccessible target renders no
      // link at all.
      if (c.expected.links.length === 0 && c.expected.statuses) {
        expect(document.querySelector(".txn-context-link")).toBeNull();
      }
    });
  }

  it("opens the exact branch point on the mounted target route", async () => {
    const c = caseNamed("distinct-context-and-starter");
    if (!c.target || !c.expected.target_active_ref) {
      throw new Error("distinct-context-and-starter needs target content and an expected turn");
    }
    await mountCase(c);

    const href = await followContextLink();
    const { targetID, search } = splitHref(href);
    expect(targetID).toBe(c.expected.links[0]);
    expect(search).toContain("entry=e_parent");

    // Follow the pushed route for real: the TARGET transcript mounts through
    // the same production page + adapter with its own metadata revision.
    cleanup();
    resetNextNavigation();
    installTargetFixture(c, targetID);
    await renderProductionRoute(targetID, search);
    await waitFor(() => expect(document.querySelector(".txn-app")).not.toBeNull());

    const expected = turnwrapByRef(c.expected.target_active_ref);
    expect(expected).not.toBeNull();
    // The referenced turn is non-first, so landing on it can only come from the
    // carried anchor.
    expect(expected?.getAttribute("data-turn")).not.toBe("0");
    await waitFor(() =>
      expect(
        expected?.querySelector(".txn-turn.txn-active"),
      ).not.toBeNull(),
    );
    expect(turnwrapByRef(c.target.turns[0].source_ref!)?.querySelector(".txn-turn.txn-active")).toBeNull();
  });

  it("lets a verified branch point own the first scroll over a saved position", async () => {
    const c = caseNamed("distinct-context-and-starter");
    if (!c.target || !c.expected.link_anchors?.[0]) {
      throw new Error("distinct-context-and-starter needs target content and an anchor");
    }
    const targetID = c.expected.links[0];
    installTargetFixture(c, targetID);
    // A previous visit to THIS target saved a different position. The carried
    // one-time branch point must win: the host's saved-scroll restore may not
    // undo the composite's initial-position scroll.
    writeTranscriptReadState(targetID, {
      search: "saved",
      activeTurn: 1,
      earlierHistoryOpen: {},
      scrollTop: 240,
    });

    const anchor = c.expected.link_anchors[0];
    const search = `?entry=${anchor.entry}&entryKind=${anchor.entry_kind}&entryRevision=${encodeURIComponent(anchor.entry_revision)}`;
    await renderProductionRoute(targetID, search);
    await waitFor(() => expect(document.querySelector(".txn-app")).not.toBeNull());

    await waitFor(() =>
      expect(turnwrapByRef(c.expected.target_active_ref!)?.querySelector(".txn-turn.txn-active")).not.toBeNull(),
    );
    const stream = document.querySelector<HTMLElement>(".txn-stream");
    expect(stream?.scrollTop).not.toBe(240);
  });

  it("resolves an anchor that names a folded tool-call ref", async () => {
    const c = caseNamed("context-anchor-via-folded-call-ref");
    if (!c.expected.target_active_ref) {
      throw new Error("context-anchor-via-folded-call-ref needs an expected turn");
    }
    await mountCase(c);
    const href = await followContextLink();
    const { targetID, search } = splitHref(href);
    expect(search).toContain("entry=e_call_ref");

    cleanup();
    resetNextNavigation();
    installTargetFixture(c, targetID);
    await renderProductionRoute(targetID, search);
    await waitFor(() => expect(document.querySelector(".txn-app")).not.toBeNull());

    const expected = turnwrapByRef(c.expected.target_active_ref);
    await waitFor(() =>
      expect(expected?.querySelector(".txn-turn.txn-active")).not.toBeNull(),
    );
  });

  it("stays general when the target's public revision no longer matches the carried anchor", async () => {
    const c = caseNamed("context-target-revision-changed");
    await mountCase(c);
    const href = await followContextLink();
    const { targetID, search } = splitHref(href);
    expect(search).toContain("entry=e_parent");
    expect(search).toContain("entryRevision=sha3-256%3Aread");

    cleanup();
    resetNextNavigation();
    installTargetFixture(c, targetID);
    await renderProductionRoute(targetID, search);
    await waitFor(() => expect(document.querySelector(".txn-app")).not.toBeNull());

    // The target was republished: the anchor is discarded and the ordinary
    // first-turn default stands, never the stale referenced turn.
    await waitFor(() =>
      expect(
        document.querySelector(".txn-turnwrap[data-turn='0'] .txn-turn.txn-active"),
      ).not.toBeNull(),
    );
    expect(turnwrapByRef("e_parent")?.querySelector(".txn-turn.txn-active")).toBeNull();
  });

  it("restores query, scroll, disclosure, and selection after Back from the current parent", async () => {
    const base = caseNamed("distinct-context-and-starter");
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

    // Follow the context link to its current target, then come Back.
    const href = await followContextLink();
    const { targetID, search } = splitHref(href);

    cleanup();
    resetNextNavigation();
    installTargetFixture(c, targetID);
    await renderProductionRoute(targetID, search);
    await waitFor(() => expect(document.querySelector(".txn-app")).not.toBeNull());

    // Browser Back re-mounts the same child route. The host must restore the
    // reading position it persisted, not reset to a blank transcript.
    cleanup();
    resetNextNavigation();
    installCase(c, transcriptID);
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
