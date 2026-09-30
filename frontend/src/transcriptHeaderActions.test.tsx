import { Suspense } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import type { SessionDetailPayload } from "@peasant-labs/schema";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import TranscriptPreview from "@/components/contribute/TranscriptPreview";
import {
  installMountedRouteTeardown,
  installRESTFixture,
  renderProductionRoute,
  type MountedRouteHandler,
  type MountedRouteTranscriptMetadata,
  type MountedRouteViewer,
} from "@/test/mountedProductionRoute";
import {
  loadTranscriptHeaderActionsFixtures,
  type HeaderViewer,
} from "@/test/transcriptHeaderActionsFixtures";

// The transcript page's header actions, mounted through the real route with
// only the REST layer faked: which entries each viewer is offered, what the
// owner's `manage access` popup sends and shows once each request settles,
// and what `download markdown` writes into the file.
const fx = loadTranscriptHeaderActionsFixtures();
const { content } = fx;
const API_PREFIX = /^.*\/api\/v1/;

function sessionDetail(): SessionDetailPayload {
  return {
    id: "session-header-actions",
    harness: "claude-code",
    startTime: "2026-08-21T09:00:00.000Z",
    endTime: "2026-08-21T09:02:00.000Z",
    durationMins: 2,
    totalTokens: 200,
    tokensIn: 120,
    tokensOut: 80,
    turnCount: 2,
    toolCallCount: 0,
    project: "village",
    model: "anthropic/claude-fable-5",
    // Carried so the page can prove it hides the outcome chip.
    outcome: "resolved",
    turns: [
      { index: 0, role: "user", content: "why is ingest dropping commits?", timestamp: "2026-08-21T09:00:00.000Z", depth: 0 },
      { index: 1, role: "assistant", content: "Looking at the detector now.", timestamp: "2026-08-21T09:01:00.000Z", depth: 0 },
    ],
  };
}

function metadata(): MountedRouteTranscriptMetadata {
  return {
    transcript: {
      id: content.transcriptId,
      local_id: "session-header-actions",
      visibility: "shared",
      title: content.title,
      description: null,
      project_name: "village",
    },
    owner: { id: content.ownerId, github_username: content.ownerUsername },
    enriched_shares: [],
    viewer_collectives: content.collectives.map((c) => ({ ...c })),
  };
}

function viewerFor(viewer: HeaderViewer): MountedRouteViewer | undefined {
  if (viewer === "owner") return { id: content.ownerId, github_username: content.ownerUsername, orgs: [] };
  if (viewer === "reader") return { id: content.readerId, github_username: content.readerUsername, orgs: [] };
  return undefined;
}

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });

/** The owner's side of the share routes: the collectives the owner belongs to,
 *  and a share or withdrawal that changes the served audience in place, the
 *  way the server's own reads would answer afterwards. */
function audienceRoutes(served: MountedRouteTranscriptMetadata, answer: number, log: string[]): MountedRouteHandler {
  return (url, init) => {
    const method = init?.method ?? "GET";
    const path = url.replace(API_PREFIX, "");
    if (method === "GET" && path === "/groups") {
      return json(
        content.groups.map((g) => ({
          id: g.id,
          name: g.name,
          acceptance_mode: g.acceptance_mode,
          member_count: g.member_count,
          role: "member",
        })),
      );
    }
    if (method === "POST" && path === `/transcripts/${content.transcriptId}/share`) {
      log.push(`POST ${path} ${String(init?.body)}`);
      if (answer !== 200) return json({ error: "share failed" }, answer);
      const { group_ids } = JSON.parse(String(init?.body)) as { group_ids: string[] };
      for (const id of group_ids) {
        const group = content.groups.find((g) => g.id === id)!;
        if (group.takes === "approved") served.viewer_collectives!.push({ id, name: group.name });
        if (group.takes === "pending") {
          served.enriched_shares.push({
            transcript_id: content.transcriptId,
            group_id: id,
            group_name: group.name,
            acceptance_mode: group.acceptance_mode,
            status: "pending",
            shared_at: "2026-08-22T00:00:00.000Z",
          });
        }
      }
      return json([]);
    }
    const unshare = path.match(new RegExp(`^/transcripts/${content.transcriptId}/share/([^/]+)$`));
    if (method === "DELETE" && unshare) {
      log.push(`DELETE ${path}`);
      if (answer !== 200) return json({ error: "unshare failed" }, answer);
      const at = served.viewer_collectives!.findIndex((c) => c.id === unshare[1]);
      if (at >= 0) served.viewer_collectives!.splice(at, 1);
      return json({ status: "unshared" });
    }
    return undefined;
  };
}

async function mount(viewer: HeaderViewer, served = metadata(), routes?: MountedRouteHandler) {
  const fetchMock = installRESTFixture(
    content.transcriptId,
    served,
    sessionDetail(),
    "transcript-header-actions",
    viewerFor(viewer),
    routes,
  );
  if (viewer === "preview") {
    // The contribute page's preview column: the real preview component, which
    // mounts the same viewer adapter with `variant="preview"`.
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    await act(async () => {
      render(
        <QueryClientProvider client={client}>
          <Suspense fallback={null}>
            <TranscriptPreview transcriptId={content.transcriptId} />
          </Suspense>
        </QueryClientProvider>,
      );
    });
  } else {
    await renderProductionRoute(content.transcriptId, "", { signedIn: viewer !== "signed-out" });
  }
  await waitFor(() => expect(document.querySelector(".txn-title")).not.toBeNull());
  if (viewer === "owner") {
    // The owner's rows appear once `/auth/me` names the owner.
    await waitFor(() => expect(screen.getByRole("button", { name: "more" })).toBeInTheDocument());
  }
  return fetchMock;
}

async function openMore(): Promise<HTMLElement> {
  fireEvent.click(screen.getByRole("button", { name: "more" }));
  return screen.findByRole("menu");
}

function closeMore() {
  fireEvent.click(screen.getByRole("button", { name: "more" }));
}

function menuLabels(menu: HTMLElement): string[] {
  return within(menu)
    .getAllByRole("menuitem")
    .map((item) => item.textContent?.trim() ?? "");
}

/** Every header entry on screen, read the way a person meets them: the link,
 *  the copy button, the `more` trigger, and the rows the menu opens. */
async function readHeaderEntries(): Promise<string[]> {
  const entries: string[] = [];
  const actions = document.querySelector<HTMLElement>('[data-testid="transcript-header-actions"]');
  if (!actions) return entries;
  if (within(actions).queryByTestId("transcript-link")) entries.push("link");
  if (within(actions).queryByRole("button", { name: "copy link" })) entries.push("copy link");
  if (within(actions).queryByRole("button", { name: "more" })) {
    entries.push("more");
    const menu = await openMore();
    for (const label of menuLabels(menu)) {
      entries.push(label);
    }
    closeMore();
  }
  return entries;
}

function accessRows(): string[] {
  const list = screen.queryByRole("list", { name: "who can read it" });
  if (!list) return [];
  return within(list)
    .getAllByRole("listitem")
    .map((row) => {
      const name = row.querySelector(".pub-access-name")?.textContent?.trim() ?? "";
      const note = row.querySelector(".pub-access-note")?.textContent?.trim();
      return note ? `${name} · ${note}` : name;
    });
}

const writeText = vi.fn(async () => {});

beforeEach(() => {
  writeText.mockClear();
  Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
});

afterEach(() => {
  Reflect.deleteProperty(navigator, "clipboard");
});

installMountedRouteTeardown();

describe("mounted transcript route: header actions per viewer", () => {
  for (const c of fx.cases) {
    it(c.name, async () => {
      const fetchMock = await mount(c.viewer);
      if (c.viewer === "preview") {
        await waitFor(() => expect(screen.getByTestId("preview-header")).toBeInTheDocument());
      }

      const entries = await readHeaderEntries();
      expect([...entries].sort()).toEqual([...c.expectEntries].sort());

      // The composite's own share/more tail and its outcome chip stay off.
      const actionRow = document.querySelector<HTMLElement>(".txn-actions");
      expect(actionRow?.querySelector(".menu-trigger:not([aria-label='more'])") ?? null).toBeNull();
      expect(document.querySelector(".txn-meta")?.textContent ?? "").not.toMatch(/resolved/);

      if (c.expectAccessCaption != null) {
        await openMore();
        await waitFor(() =>
          expect(screen.getByTestId("transcript-access-caption")).toHaveTextContent(c.expectAccessCaption!),
        );
        closeMore();
      } else {
        expect(screen.queryByTestId("transcript-access-caption")).toBeNull();
      }

      if (c.expectEntries.includes("copy link")) {
        const link = screen.getByTestId("transcript-link");
        const url = `${window.location.origin}/transcripts/${content.transcriptId}`;
        expect(link.getAttribute("href")).toBe(url);
        fireEvent.click(screen.getByRole("button", { name: "copy link" }));
        await waitFor(() => expect(writeText).toHaveBeenCalledWith(url));
      }

      const collectiveReads = fetchMock.mock.calls.filter((call) =>
        String(call[0]).endsWith(`/transcripts/${content.transcriptId}/collectives`),
      );
      expect(collectiveReads.length > 0).toBe(c.readsCollectives);
    });
  }
});

describe("mounted transcript route: the owner's manage access popup", () => {
  for (const c of fx.manageAccessCases) {
    it(c.name, async () => {
      const served = metadata();
      const log: string[] = [];
      const fetchMock = await mount("owner", served, audienceRoutes(served, c.answer, log));

      const menu = await openMore();
      fireEvent.click(within(menu).getByRole("menuitem", { name: "manage access" }));
      const dialog = await screen.findByRole("dialog", { name: "manage access" });
      await waitFor(() => expect(accessRows()).toEqual(content.collectives.map((g) => g.name)));
      const readsBefore = fetchMock.mock.calls.filter((call) => String(call[0]).endsWith("/collectives")).length;

      if (c.action === "remove") {
        fireEvent.click(within(dialog).getByRole("button", { name: `remove ${c.collective}` }));
      } else {
        await waitFor(() =>
          expect(within(dialog).getByRole("button", { name: `add ${c.collective}` })).toBeInTheDocument(),
        );
        fireEvent.click(within(dialog).getByRole("button", { name: `add ${c.collective}` }));
      }

      // The request settles, then the list is read again and shows the answer.
      await waitFor(() => expect(log).toHaveLength(1));
      expect(log[0]).toBe(c.expectRequest.replace("{transcriptId}", content.transcriptId));
      await waitFor(() => expect(accessRows()).toEqual(c.expectAccess));
      const readsAfter = fetchMock.mock.calls.filter((call) => String(call[0]).endsWith("/collectives")).length;
      expect(readsAfter).toBeGreaterThan(readsBefore);

      if (c.expectMessage) {
        await waitFor(() => expect(within(dialog).getByText(c.expectMessage!)).toBeInTheDocument());
      } else {
        expect(within(dialog).queryByRole("alert")).toBeNull();
        expect(within(dialog).queryByText(/did not take it/)).toBeNull();
      }

      fireEvent.click(within(dialog).getByRole("button", { name: "done" }));
      await waitFor(() => expect(screen.queryByRole("dialog", { name: "manage access" })).toBeNull());
      await openMore();
      await waitFor(() =>
        expect(screen.getByTestId("transcript-access-caption")).toHaveTextContent(c.expectAccessCaption),
      );
      closeMore();
    });
  }
});

describe("mounted transcript route: download markdown", () => {
  it("writes the transcript into a markdown file", async () => {
    const saved: Blob[] = [];
    const names: string[] = [];
    vi.spyOn(URL, "createObjectURL").mockImplementation((blob) => {
      saved.push(blob as Blob);
      return "blob:transcript";
    });
    vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => {});
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) {
      names.push(this.download);
    });

    try {
      await mount(fx.download.viewer);
      const menu = await openMore();
      fireEvent.click(within(menu).getByRole("menuitem", { name: "download markdown" }));

      expect(names).toEqual([fx.download.expectFileName]);
      expect(saved).toHaveLength(1);
      expect(saved[0].type).toMatch(/^text\/markdown/);
      const lines = (await saved[0].text()).split("\n").filter((line) => line.trim() !== "");
      // Every expected line is in the file, in the expected order.
      let from = 0;
      for (const expected of fx.download.expectLines) {
        const at = lines.indexOf(expected, from);
        expect(at, `"${expected}" after line ${from} of:\n${lines.join("\n")}`).toBeGreaterThanOrEqual(from);
        from = at + 1;
      }
    } finally {
      click.mockRestore();
      vi.restoreAllMocks();
    }
  });
});
