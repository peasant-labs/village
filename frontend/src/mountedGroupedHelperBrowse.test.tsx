import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import RootPage from "@/app/page";
import { AuthProvider } from "@/providers/AuthProvider";
import { makeTranscriptFixture } from "@/test/transcriptRowFixture";
import {
  loadGroupedHelperMountFixtures,
  memberItem,
  memberPageFor,
  memberUUID,
} from "@/test/groupedHelperMountFixtures";
import type { TranscriptListItem, User } from "@/lib/types";

/**
 * Mounted evidence for the signed-in home route's helper-group exits.
 *
 * The REAL `RootPage` renders through the REAL grouped query hook and the REAL
 * member query hook; only HTTP is controlled. The flat owner list still draws
 * the row (and the ordinary child chip), and the grouped response attaches the
 * saved helper group to it — the one owner row is expanded in place, and each
 * member is an individually linked transcript.
 */

const fixtures = loadGroupedHelperMountFixtures();

const VIEWER = "alice-dev";
const OWNER_ID = memberUUID("owner-row");
const OWNER_USER: User = {
  id: "user-alice-dev",
  github_id: 1,
  github_username: VIEWER,
  display_name: VIEWER,
  avatar_url: null,
  created_at: "2026-01-01T00:00:00.000Z",
  updated_at: "2026-01-01T00:00:00.000Z",
  is_discoverable: true,
  username_chosen: true,
  provider_username: VIEWER,
};

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  globalThis.localStorage?.clear();
});

const ownerTranscript = makeTranscriptFixture({
  id: OWNER_ID,
  local_id: "ses_ownerrow",
  owner_id: "12345678-1234-4234-8234-123456789099",
  title: "owner session",
  project_hash: "1".repeat(64),
  project_name: "proj",
  project_display_name: "proj",
  published_at: "2026-08-24T12:00:00.000Z",
  updated_at: "2026-08-24T12:00:00.000Z",
});

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

interface Calls {
  grouped: string[];
  members: string[];
}

function installREST(): Calls {
  const calls: Calls = { grouped: [], members: [] };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(String(input));
      if (url.pathname.endsWith("/auth/me")) return json(OWNER_USER);
      if (url.pathname.endsWith("/transcripts")) {
        if (url.searchParams.get("view") === "grouped") {
          calls.grouped.push(url.search);
          return json({
            items: [
              {
                kind: "transcript",
                transcript: { session: ownerTranscript },
                helperGroups: [fixtures.groups.owner],
              },
              {
                kind: "context_container",
                context: {
                  groupId: fixtures.context.groupId,
                  ownerStatus: fixtures.context.ownerStatus,
                },
                helperGroups: [fixtures.groups.context],
              },
            ],
            page: 1,
            limit: 100,
            totalItems: 2,
            ordinarySessionTotal: 1,
            helperThreadTotal: fixtures.groups.owner.helperThreadCount + 1,
          });
        }
        const item: TranscriptListItem = { transcript: ownerTranscript, tags: [], owner: OWNER_USER };
        return json({ transcripts: [item], total: 1, agent_total: 0, page: 1, limit: 24 });
      }
      if (url.pathname.includes("/transcript-groups/")) {
        calls.members.push(url.search);
        const scope = url.searchParams.get("scope");
        const page = Number(url.searchParams.get("page") ?? 1);
        const spec = scope == null ? undefined : memberPageFor(fixtures, scope, page);
        if (spec == null) return json({ error: "unknown scope" }, 409);
        return json({
          members: spec.members.map(memberItem),
          page,
          limit: spec.limit,
          total: spec.total,
        });
      }
      return json({});
    }),
  );
  return calls;
}

async function renderHome(): Promise<void> {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  await act(async () => {
    render(
      <QueryClientProvider client={client}>
        <AuthProvider>
          <RootPage />
        </AuthProvider>
      </QueryClientProvider>,
    );
  });
}

function groupTrigger(groupId: string): HTMLButtonElement {
  const root = document.querySelector<HTMLElement>(`.helper-group[data-group-id="${groupId}"]`);
  if (root == null) throw new Error(`helper group ${groupId} is not mounted on the home route`);
  const button = root.querySelector<HTMLButtonElement>("button.sgd-trigger");
  if (button == null) throw new Error(`helper group ${groupId} has no disclosure control`);
  return button;
}

it("home attaches the grouped helper group to its owner row and expands it in place", async () => {
  const calls = installREST();
  await renderHome();

  const panel = await screen.findByTestId("home-recent-sessions");
  // The flat owner row is still the row on screen.
  await waitFor(() =>
    expect(panel.querySelector(`a[href="/transcripts/${OWNER_ID}"]`)).not.toBeNull(),
  );
  expect(calls.grouped).toHaveLength(1);

  // No member request is made until the disclosure is opened.
  expect(calls.members).toEqual([]);

  fireEvent.click(groupTrigger(fixtures.groups.owner.groupId));
  await waitFor(() => expect(calls.members).toHaveLength(1));
  expect(calls.members[0]).toContain(`scope=${fixtures.groups.owner.memberScope}`);
  expect(calls.members[0]).toContain("page=1");
  expect(calls.members[0]).toContain("limit=20");

  // Each member is an explicit, individually linked transcript row.
  await waitFor(() => {
    const memberLinks = [
      ...panel.querySelectorAll<HTMLAnchorElement>("a.helper-thread-open"),
    ].map((anchor) => anchor.getAttribute("href"));
    expect(memberLinks).toEqual([
      `/transcripts/${memberUUID("helper-a")}`,
      `/transcripts/${memberUUID("helper-b")}`,
    ]);
  });

  // The helper-only container states the authorized owner status and links
  // nowhere.
  expect(panel.textContent).toContain("owner is unavailable");
});
