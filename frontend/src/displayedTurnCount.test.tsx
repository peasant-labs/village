import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import TranscriptCard from "@/components/transcript/TranscriptCard";
import TranscriptList from "@/components/transcript/TranscriptList";
import TranscriptRow from "@/components/transcript/TranscriptRow";
import { collectiveTranscriptRow, pullRequestCells } from "@/lib/adapters/collective";
import { adaptExplore } from "@/lib/adapters/explore";
import type { GroupTranscript, Transcript, TranscriptListItem } from "@/lib/types";
import { AuthProvider } from "@/providers/AuthProvider";
import {
  loadDisplayedTurnCountFixtures,
  type DisplayedTurnCountCase,
} from "@/test/displayedTurnCountFixtures";
import {
  installHomeRouteREST,
  installHomeRouteTeardown,
  renderAppRoute,
  showHomeAsFlatList,
} from "@/test/mountedHomeRoute";
import { profileOwner } from "@/test/publicOwnerFixture";
import { asTranscriptListRow, makeTranscriptFixture } from "@/test/transcriptRowFixture";

// Every surface that prints a transcript's turn count prints the count the
// server chose for display, and prints nothing when the server chose none. Each
// case is mounted on the real home route and on the list, row and card
// components, and read back through the two adapters that feed the shared
// explore and collective surfaces.

const fixtures = loadDisplayedTurnCountFixtures();
const TITLE = "turn count fixture session";
const OWNER = profileOwner({ id: "user-ada", github_username: "ada" });

installHomeRouteTeardown();

function transcript(c: DisplayedTurnCountCase): Transcript {
  return makeTranscriptFixture({
    id: "11111111-1111-4111-8111-111111111111",
    title: TITLE,
    // Nothing else on the row may put a digit run beside the count.
    duration_ms: null,
    tool_call_count: null,
    turn_count: c.turnCount,
    conversation_turn_count: c.conversationTurnCount,
    displayed_turn_count: c.displayedTurnCount,
  });
}

function listItem(c: DisplayedTurnCountCase): TranscriptListItem {
  return { transcript: transcript(c), tags: [], owner: OWNER, shares: [], attestations: [] };
}

function groupTranscript(c: DisplayedTurnCountCase): GroupTranscript {
  return {
    pull_requests: { count: 0, recent: [] },
    ...transcript(c),
    license_id: null,
    outcome: null,
    source_format: null,
    subagents: null,
    owner: OWNER,
  };
}

/** Every "<n> turns" statement drawn under `root`, as the numbers stated. Each
 *  is read from its own element: neighbouring facts share a line with no space
 *  between them, so the line's joined text has no reliable word boundary. */
function statedTurnCounts(root: ParentNode): number[] {
  return [...root.querySelectorAll("span")].flatMap((el) => {
    // A wrapper around the stating element reads the same text; count the innermost.
    if (el.querySelector("span") !== null) return [];
    const stated = /^(\d[\d,]*) turns?$/.exec((el.textContent ?? "").trim());
    return stated === null ? [] : [Number(stated[1].replace(/,/g, ""))];
  });
}

function expected(c: DisplayedTurnCountCase): number[] {
  return c.shows === null ? [] : [c.shows];
}

describe("the turn count a transcript row prints", () => {
  it.each(fixtures)("home table: $name", async (c) => {
    installHomeRouteREST({
      viewerUsername: "ada",
      transcripts: [
        {
          id: "11111111-1111-4111-8111-111111111111",
          title: TITLE,
          projectHash: "a".repeat(64),
          projectDisplayName: "village",
          publishedAt: "2026-08-20T10:00:00Z",
          turnCounts: {
            stored: c.turnCount,
            conversation: c.conversationTurnCount,
            displayed: c.displayedTurnCount,
          },
        },
      ],
    });
    await renderAppRoute(showHomeAsFlatList());
    await waitFor(() =>
      expect(document.querySelector('[data-testid="home-transcript-row"]')).not.toBeNull(),
    );
    const table = document.querySelector('[data-testid="home-transcripts"] table')!;
    const column = [...table.querySelectorAll("thead th")].findIndex(
      (th) => (th.textContent ?? "").trim() === "turns",
    );
    expect(column, "the home table draws a turns column").toBeGreaterThanOrEqual(0);
    const row = document.querySelector('[data-testid="home-transcript-row"]')!.closest("tr")!;
    const cell = row.children[column];
    expect((cell.textContent ?? "").trim()).toBe(
      c.shows === null ? "not counted" : c.shows.toLocaleString("en-US"),
    );
  });

  it.each(fixtures)("transcript list: $name", async (c) => {
    installHomeRouteREST({ viewerUsername: "ada", transcripts: [] });
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={client}>
        <AuthProvider>
          <TranscriptList items={[listItem(c)]} facts={["turns"]} />
        </AuthProvider>
      </QueryClientProvider>,
    );
    const title = await screen.findByText(TITLE);
    const list = title.closest(".divide-y")!;
    expect(statedTurnCounts(list)).toEqual(expected(c));
    const stated = [...list.querySelectorAll("span")].find(
      (el) => el.querySelector("span") === null && /^\d+ turns$/.test(el.textContent ?? ""),
    );
    if (c.shows !== null) expect(stated?.className).toContain("tabular-nums");
  });

  it.each(fixtures)("row and card: $name", (c) => {
    const row = render(<TranscriptRow item={listItem(c)} />);
    expect(statedTurnCounts(row.container)).toEqual(expected(c));
    row.unmount();
    const card = render(<TranscriptCard item={listItem(c)} />);
    expect(statedTurnCounts(card.container)).toEqual(expected(c));
  });

  it.each(fixtures)("explore and collective adapters: $name", (c) => {
    const explore = adaptExplore(
      {
        transcripts: [asTranscriptListRow(listItem(c))],
        total: 1,
        agent_total: 0,
        page: 1,
        per_page: 20,
        next_items: [],
      },
      { collectives: [] },
      [],
    );
    expect(explore.transcripts.transcripts[0].turnCount).toBe(c.shows);

    const row = groupTranscript(c);
    const made = collectiveTranscriptRow(row, pullRequestCells([row]), new Date("2026-08-21T00:00:00Z"));
    expect(made.turns).toBe(c.shows ?? undefined);
  });
});
