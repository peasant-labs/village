import { type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AuthProvider } from "@/providers/AuthProvider";
import { providerDisplayName } from "@peasant-labs/fairtrade/ui";
import PublishPage from "@/app/publish/page";
import LinkedRepositories from "@/components/group/LinkedRepositories";
import { installRESTFixture, renderProductionRoute, type MountedRouteTranscriptMetadata } from "@/test/mountedProductionRoute";
import { installCollectiveREST, makeGroup, renderCollectiveSettings } from "@/test/mountedCollectivePages";
import { pushedRoutes } from "@/test/nextNavigationMock";
import type { SessionDetailPayload } from "@peasant-labs/schema";
import PendingApprovalBar from "@/components/transcript/PendingApprovalBar";
import {
  installHomeRouteREST,
  installHomeRouteTeardown,
  renderHeaderAt,
} from "@/test/mountedHomeRoute";
import { installGroupRouteREST, renderGroupDetailRoute } from "@/test/mountedGroupRoute";
import { makeTranscriptFixture } from "@/test/transcriptRowFixture";
import {
  loadLowercaseChromeFixtures,
  type LowercaseChromeCase,
} from "@/test/lowercaseChromeFixtures";
import type { GroupTranscript, UserGroupShare } from "@/lib/types";

// Mounts each surface the lowercase pass covers, through its real component or
// route, and reads what a person sees or hears there as chrome. See
// `src/testdata/lowercase-chrome.yaml` for what counts as chrome and what is
// set aside as user content.

const fixtures = loadLowercaseChromeFixtures();
const content = fixtures.content;

installHomeRouteTeardown();

const GROUP_ID = "acme-platform";
const TRANSCRIPT_ID = "transcript-flaky-ingest";
// Midday, so the formatted day is the same in every timezone the suite runs in.
const WHEN = "2026-01-02T12:00:00.000Z";

/** The one harness every mounted row names. */
const HARNESS = "claude-code";

/** A formatted date is data, in the design system's own form ("Jan 2" or
 *  "Jan 2, 2026"), not chrome. */
const FORMATTED_DATE = /\b(?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec) \d{1,2}(?:, \d{4})?/g;

/** A person's initial on a hand-drawn avatar tile: the first letter of a
 *  user-content name, capitalized the way fairtrade's Avatar draws it. Matched
 *  exactly, so a chrome word such as "OK" is still chrome. */
const INITIALS = new Set(Object.values(content).map((value) => value[0].toUpperCase()));

/** fairtrade's Avatar tile (`.avatar`) draws initials from a person's name and
 *  nothing else; its own text is set aside only while it is initials. */
const AVATAR_INITIALS = /^[A-Z]{1,2}$/;

/** Attributes whose text reaches a person: a tooltip, a field hint, a name
 *  announced by a screen reader. */
const CHROME_ATTRIBUTES = ["title", "placeholder", "aria-label"] as const;

type ChromeString = {
  where: string;
  text: string;
  shownUppercase: boolean;
  avatarTile: boolean;
};

/** Every piece of chrome text under `root`: each element's own text (its
 *  adjacent text children joined, as React splits `{a}/{b}` into three), and
 *  the chrome attributes. Own text inside an `uppercase` element is marked, so
 *  it is read as it renders. */
function chromeStrings(root: Element): ChromeString[] {
  const out: ChromeString[] = [];
  for (const el of [root, ...Array.from(root.querySelectorAll("*"))]) {
    if (el.closest("script, style")) continue;
    const tag = el.tagName.toLowerCase();
    const own = Array.from(el.childNodes)
      .filter((n) => n.nodeType === Node.TEXT_NODE)
      .map((n) => n.textContent ?? "")
      .join("")
      .trim();
    if (own) {
      out.push({
        where: `<${tag}>`,
        text: own,
        shownUppercase: el.closest(".uppercase") != null,
        avatarTile: el.classList.contains("avatar"),
      });
    }
    for (const attr of CHROME_ATTRIBUTES) {
      const value = el.getAttribute(attr)?.trim();
      if (value) out.push({ where: `<${tag} ${attr}>`, text: value, shownUppercase: false, avatarTile: false });
    }
  }
  return out;
}

/** The chrome strings that still carry a capital once user content, the
 *  design system's brand-case provider names, initials and dates are set
 *  aside. */
function capitalizedChrome(root: Element): string[] {
  const setAside = [...Object.values(content), providerDisplayName(HARNESS)];
  return chromeStrings(root)
    .map(({ where, text, shownUppercase, avatarTile }) => {
      if (INITIALS.has(text) || (avatarTile && AVATAR_INITIALS.test(text))) return null;
      let rest = text.replace(FORMATTED_DATE, "");
      for (const value of setAside) rest = rest.split(value).join("");
      if (shownUppercase) rest = rest.toUpperCase();
      return /[A-Z]/.test(rest) ? `${where} ${JSON.stringify(text)}${shownUppercase ? " (uppercase)" : ""}` : null;
    })
    .filter((s): s is string => s !== null);
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
}

async function mount(element: ReactNode, withAuth = false): Promise<void> {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  await act(async () => {
    render(
      <QueryClientProvider client={client}>
        {withAuth ? <AuthProvider>{element}</AuthProvider> : element}
      </QueryClientProvider>,
    );
  });
}

function groupTranscript(): GroupTranscript {
  return {
    ...makeTranscriptFixture({
      id: TRANSCRIPT_ID,
      owner_id: `user-${content.viewer}`,
      title: content.transcript,
      project_name: "village",
      project_display_name: "village",
      session_start: WHEN,
      published_at: WHEN,
    }),
    license_id: null,
    outcome: null,
    source_format: null,
    subagents: null,
    owner_username: content.viewer,
    owner_avatar_url: null,
    owner_is_discoverable: true,
    pull_requests: { count: 0, recent: [] },
  };
}

function myShare(id: string, status: "approved" | "pending"): UserGroupShare {
  return {
    id,
    owner_id: `user-${content.viewer}`,
    local_id: `local-${id}`,
    parent_session_id: null,
    title: content.transcript,
    model_provider: HARNESS,
    model_name: "claude-fable-5",
    visibility: "shared",
    published_at: WHEN,
    turn_count: 12,
    tokens_in: null,
    tokens_out: null,
    status,
    shared_at: WHEN,
  };
}

/** Mounts `c`'s surface in `c`'s state and returns the element to read, once
 *  it has drawn. */
async function mountSurface(c: LowercaseChromeCase): Promise<Element> {
  switch (c.surface) {
    case "navbar": {
      const signedIn = c.state !== "signed-out";
      installHomeRouteREST({ viewerUsername: signedIn ? content.viewer : null, transcripts: [] });
      await renderHeaderAt(signedIn ? "/groups" : "/explore");
      await waitFor(() => expect(document.querySelector("header .animate-shimmer")).toBeNull());
      if (c.state === "account-menu-open") {
        fireEvent.click(await screen.findByRole("button", { name: `account menu for @${content.viewer}` }));
        await screen.findByRole("menu");
      } else {
        await screen.findByText(/continue with/);
      }
      return document.querySelector("header")!;
    }

    case "linked-repositories": {
      const [owner, name] = content.repository.split("/");
      vi.stubGlobal(
        "fetch",
        vi.fn(async (input: RequestInfo | URL) => {
          const url = String(input);
          if (c.state === "not-configured" && url.includes("/repositories")) {
            return json({ error: "not configured" }, 501);
          }
          if (url.endsWith(`/groups/${GROUP_ID}/repositories/available`)) {
            return json({ repositories: [{ owner, name, is_private: true }] });
          }
          if (url.endsWith(`/groups/${GROUP_ID}/repositories`)) {
            return json({
              repositories: [
                {
                  id: "repo-1",
                  group_id: GROUP_ID,
                  installation_id: 1,
                  owner,
                  name,
                  is_private: true,
                  linked_by: "user-owner",
                  created_at: WHEN,
                  last_synced_at: WHEN,
                },
              ],
            });
          }
          throw new Error(`linked-repositories fixture received an unexpected request to ${url}`);
        }),
      );
      await mount(<LinkedRepositories groupId={GROUP_ID} isOwner transcripts={[]} />);
      if (c.state === "not-configured") {
        await screen.findByText(/connection isn.t set up/i);
      } else {
        await waitFor(() => expect(document.body.textContent).toContain(content.repository));
        await waitFor(() => expect(screen.queryByText(/loading repositories/i)).toBeNull());
      }
      return document.body;
    }

    case "group-page": {
      if (c.state === "not-found") {
        // A signed-out visitor to an address no collective answers.
        vi.stubGlobal(
          "fetch",
          vi.fn(async (input: RequestInfo | URL) => {
            const url = String(input);
            if (url.endsWith("/auth/me")) return json({ error: "not signed in" }, 401);
            if (url.endsWith(`/groups/${GROUP_ID}`)) return json({ error: "not found" }, 404);
            throw new Error(`group not-found fixture received an unexpected request to ${url}`);
          }),
        );
        await renderGroupDetailRoute(GROUP_ID);
        await screen.findByText(/could not read the collective/i);
        return document.body;
      }

      const owner = c.state !== "visitor";
      installGroupRouteREST({
        viewer: content.viewer,
        groupId: GROUP_ID,
        groupName: content.collective,
        role: owner ? "owner" : null,
        acceptanceMode: owner ? "curated" : "open",
        transcripts: owner ? [groupTranscript()] : [],
        pendingShares: owner
          ? [
              {
                transcript_id: "transcript-pending",
                title: content.transcript,
                model_provider: HARNESS,
                owner_id: "user-bob",
                local_id: "local-pending",
                parent_session_id: null,
                project_hash: "village-project",
                project_name: "village",
                branch: "main",
                owner_username: "bob",
                owner_is_discoverable: true,
                shared_at: WHEN,
              },
            ]
          : [],
        myShares: owner
          ? [
              myShare(TRANSCRIPT_ID, "approved"),
              // A contribution still awaiting review draws its `pending` label.
              myShare("transcript-awaiting-review", "pending"),
            ]
          : [],
      });
      if (c.state === "invite-search") {
        // The invite field searches GitHub itself; answer that one request.
        const groupRoute = globalThis.fetch;
        vi.stubGlobal(
          "fetch",
          vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
            if (String(input).startsWith("https://api.github.com/search/users")) {
              return json({ items: [{ login: content.githubUser, avatar_url: "" }] });
            }
            return groupRoute(input, init);
          }),
        );
      }
      await renderGroupDetailRoute(GROUP_ID);
      await waitFor(() => expect(document.body.textContent).toContain(content.collective));
      if (!owner) {
        await screen.findAllByText(content.collective);
        expect(screen.queryByTestId("my-contributions-panel")).toBeNull();
        return document.body;
      }
      await screen.findByTestId("my-contributions-panel");
      await screen.findByText("pending");
      if (c.state === "confirm-remove") {
        fireEvent.click(await screen.findByTestId("collective-library-disclosure-toggle"));
        fireEvent.click(screen.getByRole("checkbox", { name: "select all" }));
        fireEvent.click(await screen.findByRole("button", { name: "remove selected (1)" }));
        await screen.findByText("remove from collective?");
      }
      if (c.state === "invite-search") {
        fireEvent.click(screen.getByRole("button", { name: "settings" }));
        expect(pushedRoutes).toContain(`/groups/${GROUP_ID}/settings`);
        // Follow the real settings exit; its retained search opens the
        // canonical Dialog without replacing the canonical invite form.
        const world = { viewer: { id: `user-${content.viewer}`, username: content.viewer }, group: makeGroup({ id: GROUP_ID, name: content.collective }), role: "owner", canRead: true, members: [], transcripts: [], linked: [], available: [] };
        installCollectiveREST(world);
        const route = globalThis.fetch;
        vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
          if (String(input).startsWith("https://api.github.com/search/users")) return json({ items: [{ login: content.githubUser, avatar_url: "" }] });
          return route(input, init);
        }));
        await renderCollectiveSettings(world);
        fireEvent.click(screen.getByRole("button", { name: "find a github user to invite" }));
        const dialog = await screen.findByRole("dialog", { name: "invite a github user" });
        fireEvent.change(within(dialog).getByRole("combobox", { name: "github username" }), { target: { value: "octo" } });
        await within(dialog).findByRole("menu", { name: "github user results" }, { timeout: 3000 });
        await within(dialog).findByText(content.githubUser);
        return document.body;
      }
      return document.body;
    }

    case "publish-page": {
      const viewer = {
        id: `user-${content.viewer}`,
        github_id: 1,
        github_username: content.viewer,
        display_name: content.viewer,
        avatar_url: null,
        created_at: WHEN,
        updated_at: WHEN,
        is_discoverable: true,
        username_chosen: true,
        provider_username: content.viewer,
      };
      vi.stubGlobal(
        "fetch",
        vi.fn(async (input: RequestInfo | URL) => {
          const url = String(input);
          if (url.endsWith("/auth/me")) return json(viewer);
          if (url.includes("/transcripts?") && url.includes(`owner=${content.viewer}`)) {
            // The list echoes the page and limit it was asked for; the client
            // refuses an answer to a different question.
            const query = new URL(url, "https://village.test").searchParams;
            return json({
              transcripts: [
                {
                  transcript: makeTranscriptFixture({
                    id: TRANSCRIPT_ID,
                    owner_id: viewer.id,
                    title: content.transcript,
                    project_name: "village",
                    project_display_name: "village",
                    session_start: WHEN,
                    published_at: WHEN,
                  }),
                  tags: [],
                  owner: viewer,
                },
              ],
              total: 1,
              agent_total: 0,
              page: Number(query.get("page") ?? 1),
              limit: Number(query.get("limit") ?? 24),
            });
          }
          throw new Error(`publish-page fixture received an unexpected request to ${url}`);
        }),
      );
      await mount(<PublishPage />, true);
      await waitFor(() => expect(document.body.textContent).toContain(content.transcript));
      if (c.state === "import-dialog-open") {
        fireEvent.click(screen.getByRole("button", { name: "import" }));
        await screen.findByText("how to import transcripts");
      }
      return document.body;
    }

    case "contribute-picker": {
      const shared: string[] = [];
      const ownerID = `user-${content.viewer}`;
      const served: MountedRouteTranscriptMetadata = { transcript: { id: TRANSCRIPT_ID, local_id: "local-picker", visibility: "shared", title: content.transcript, description: null, project_name: "village" }, owner: { id: ownerID, github_username: content.viewer }, enriched_shares: [], viewer_collectives: [] };
      const detail: SessionDetailPayload = { id: "local-picker", harness: HARNESS, startTime: WHEN, endTime: WHEN, durationMins: 0, totalTokens: 0, tokensIn: 0, tokensOut: 0, turnCount: 0, toolCallCount: 0, project: "village", model: "anthropic/claude-fable-5", turns: [] };
      installRESTFixture(TRANSCRIPT_ID, served, detail, "lowercase manage access", { id: ownerID, github_username: content.viewer, orgs: [] }, (url, init) => {
        const path = new URL(url).pathname.replace(/^\/api\/v1/, "");
        const method = init?.method ?? "GET";
        if (path === "/groups") return json(c.state === "no-collectives" ? [] : [{ ...makeGroup({ id: GROUP_ID, name: content.collective }), role: "member", member_since: WHEN }]);
        if (path === "/users/me/collectives/contributions") return json({ collectives: shared.length ? [{ id: GROUP_ID, name: content.collective, approved_count: 1, pending_count: 0, rejected_attempt_count: 0, withdrawn_attempt_count: 0 }] : [] });
        if (path === `/groups/${GROUP_ID}/my-shares`) return json(shared.length ? [{ id: TRANSCRIPT_ID, status: "approved" }] : []);
        if (path === `/transcripts/${TRANSCRIPT_ID}/share` && method === "POST") {
          shared.push(String(init?.body));
          served.viewer_collectives!.push({ id: GROUP_ID, name: content.collective });
          return json([{ group_id: GROUP_ID, group_name: content.collective, shared_at: WHEN }]);
        }
        return undefined;
      });
      await renderProductionRoute(TRANSCRIPT_ID, "", { signedIn: true });
      fireEvent.click(await screen.findByRole("button", { name: "more" }));
      fireEvent.click(await screen.findByRole("menuitem", { name: "manage access" }));
      const dialog = await screen.findByRole("dialog", { name: "manage access" });
      if (c.state === "no-collectives") {
        fireEvent.change(await within(dialog).findByPlaceholderText("collective or github org"), { target: { value: "unmatched" } });
        await within(dialog).findByText("none of your collectives matches that name.");
        return dialog;
      }
      await within(dialog).findByText(content.collective);
      if (c.state === "contributed") {
        fireEvent.click(within(dialog).getByRole("button", { name: `add ${content.collective}` }));
        await within(dialog).findByText(`submitted to ${content.collective}.`);
        expect(shared).toEqual([JSON.stringify({ group_ids: [GROUP_ID] })]);
      }
      return dialog;
    }

    case "pending-approval-bar": {
      await mount(
        <PendingApprovalBar
          transcriptId={TRANSCRIPT_ID}
          reviews={[{ groupId: GROUP_ID, groupName: content.collective }]}
        />,
      );
      await screen.findByText(content.collective);
      return document.body;
    }
  }
}

describe("mounted surfaces: chrome is lowercase and user content keeps its case", () => {
  for (const c of fixtures.cases) {
    it(c.name, async () => {
      const root = await mountSurface(c);

      for (const key of c.expectContent) {
        expect(root.textContent, `${key} is shown exactly as written`).toContain(content[key]);
      }
      expect(capitalizedChrome(root)).toEqual([]);
    });
  }
});
