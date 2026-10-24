import { type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AuthProvider } from "@/providers/AuthProvider";
import { providerDisplayName } from "@peasant-labs/fairtrade/ui";
import LinkedRepositories from "@/components/group/LinkedRepositories";
import ContributePicker from "@/components/transcript/ContributePicker";
import PendingApprovalBar from "@/components/transcript/PendingApprovalBar";
import {
  installHomeRouteREST,
  installHomeRouteTeardown,
  renderAppRoute,
  renderHeaderAt,
  showHomeAsFlatList,
} from "@/test/mountedHomeRoute";
import { installGroupRouteREST, renderGroupDetailRoute } from "@/test/mountedGroupRoute";
import {
  installGroupSettingsREST,
  renderGroupSettingsRoute,
} from "@/test/mountedAttachmentSurfaces";
import { installCollectiveREST, makeGroup, mountCollectiveSettings } from "@/test/mountedCollectivePages";
import { makeTranscriptFixture } from "@/test/transcriptRowFixture";
import { installProfileRESTFixture, renderProfileRoute } from "@/test/mountedProfileRoute";
import { installSettingsRouteREST, renderSettingsRoute } from "@/test/mountedSettingsRoute";
import { loadAccountSettingsFixtures } from "@/test/accountSettingsFixtures";
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
    // A command is data: it is written the way the terminal needs it.
    if (el.closest("code")) continue;
    // A key cap is written the way the keyboard prints it ("⌘K").
    if (el.closest("kbd")) continue;
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
/** A key chord as the keyboard prints it, inside a tooltip ("search (⌘K)"). */
const KEY_CHORD = /[⌘⌃⌥⇧]+[A-Z0-9]\b/g;

function capitalizedChrome(root: Element): string[] {
  const setAside = [...Object.values(content), providerDisplayName(HARNESS)];
  return chromeStrings(root)
    .map(({ where, text, shownUppercase, avatarTile }) => {
      if (INITIALS.has(text) || (avatarTile && AVATAR_INITIALS.test(text))) return null;
      let rest = text.replace(FORMATTED_DATE, "").replace(KEY_CHORD, "");
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
    pull_requests: { count: 0, recent: [] },
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
    case "chrome": {
      const signedIn = c.state !== "public-bar";
      installHomeRouteREST({
        viewerUsername: signedIn ? content.viewer : null,
        transcripts: [],
        collectives: signedIn ? [{ id: GROUP_ID, name: content.collective, role: "owner", members: 3 }] : [],
      });
      // Signed out, the public bar is drawn only on the privacy notice.
      await renderHeaderAt(signedIn ? "/collectives" : "/privacy");
      if (!signedIn) {
        await screen.findByRole("link", { name: "sign in" });
        return screen.getByTestId("public-top-bar");
      }
      await screen.findByRole("link", { name: content.collective });
      if (c.state === "account-menu-open") {
        fireEvent.click(await screen.findByRole("button", { name: `@${content.viewer}` }));
        await screen.findByRole("menu");
      }
      return document.body;
    }

    case "sign-in-page": {
      installHomeRouteREST({ viewerUsername: null, transcripts: [] });
      await renderAppRoute("/login", { header: true });
      return await screen.findByTestId("leaving-for-sign-in");
    }

    case "home-page": {
      showHomeAsFlatList();
      installHomeRouteREST({
        viewerUsername: content.viewer,
        transcripts:
          c.state === "rows"
            ? [
                {
                  id: TRANSCRIPT_ID,
                  title: content.transcript,
                  projectHash: "a".repeat(64),
                  projectDisplayName: "village-project",
                  publishedAt: WHEN,
                  sharedWith: [content.collective],
                },
              ]
            : [],
      });
      await renderAppRoute("/");
      await screen.findByTestId(c.state === "rows" ? "home-transcripts" : "home-empty-state");
      return screen.getByTestId("home-page");
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
          if (url.endsWith(`/collectives/${GROUP_ID}/repositories/available`)) {
            return json({ repositories: [{ owner, name, is_private: true }] });
          }
          if (url.endsWith(`/collectives/${GROUP_ID}/repositories`)) {
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
            if (url.endsWith(`/collectives/${GROUP_ID}`)) return json({ error: "not found" }, 404);
            throw new Error(`group not-found fixture received an unexpected request to ${url}`);
          }),
        );
        await renderGroupDetailRoute(GROUP_ID);
        await screen.findByText(/collective not found/i);
        return document.body;
      }

      if (c.state === "invite-search") {
        // The invite search lives on the settings page: mount it, open the
        // dialog, and answer the one GitHub search request it makes.
        installGroupSettingsREST({
          id: GROUP_ID,
          ownerUsername: content.viewer,
          name: content.collective,
          postPromptsCheck: true,
          promptsCheckMode: "informational",
        });
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
        // The search sits in the settings page's members section.
        window.history.replaceState(null, "", `/collectives/${GROUP_ID}/settings?section=members`);
        await renderGroupSettingsRoute(GROUP_ID);
        fireEvent.click(await screen.findByRole("button", { name: /find a github user to invite/i }));
        const dialog = await screen.findByRole("dialog", { name: /invite a github user/i });
        fireEvent.change(within(dialog).getByPlaceholderText("github username"), {
          target: { value: "octo" },
        });
        await within(dialog).findAllByRole("menu", { name: "github user results" }, { timeout: 3000 });
        await within(dialog).findAllByText(content.githubUser);
        return document.body;
      }

      const owner = c.state !== "visitor" && c.state !== "signed-out";
      installGroupRouteREST({
        viewer: c.state === "signed-out" ? null : content.viewer,
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
      await renderGroupDetailRoute(GROUP_ID);
      await waitFor(() => expect(document.body.textContent).toContain(content.collective));
      if (!owner) {
        await screen.findAllByText(/data access restricted/i);
        if (c.state === "signed-out") await screen.findByRole("link", { name: "sign in to join" });
        return document.body;
      }
      await screen.findByTestId("my-contributions-panel");
      await screen.findAllByText(/connection isn.t set up/i);
      await screen.findByText("pending");
      if (c.state === "confirm-remove") {
        // The select-all control lives inside the transcript-groups disclosure,
        // which starts folded. The destructive confirm swaps in place.
        fireEvent.click(await screen.findByRole("button", { name: /transcript groups/i }));
        fireEvent.click(await screen.findByRole("checkbox", { name: "select all" }));
        fireEvent.click(await screen.findByRole("button", { name: /remove selected/i }));
        await screen.findByText("remove from collective?");
      }
      return document.body;
    }

    case "collective-settings": {
      // The owner's settings, one section at a time, through the real route.
      const section = c.state === "delete-confirm" ? "danger" : c.state === "not-owner" ? "general" : c.state;
      window.history.replaceState(null, "", `/collectives/${GROUP_ID}/settings?section=${section}`);
      installCollectiveREST({
        viewer: { id: `user-${content.viewer}`, username: content.viewer },
        group: makeGroup({ id: GROUP_ID, name: content.collective, linked_github_org: "acme" }),
        role: c.state === "not-owner" ? "member" : "owner",
        canRead: true,
        members: [
          { id: `user-${content.viewer}`, username: content.viewer, name: null, role: "owner" },
          { id: "user-bob", username: "bob-ai", name: null, role: "member" },
          { id: "user-dan", username: "dan-ops", name: null, role: "contributor" },
        ],
        transcripts: [],
        linked: ["acme/ingest-api"],
        available: [
          { repo: "acme/ingest-api", private: true, publishers: 1 },
          { repo: "acme/web", private: false, publishers: 0 },
        ],
      });
      await mountCollectiveSettings({ group: makeGroup({ id: GROUP_ID, name: content.collective }) } as never);
      if (c.state === "not-owner") {
        await screen.findByText("only the owner can change these settings");
        return document.body;
      }
      await screen.findByRole("navigation", { name: "settings sections" });
      if (c.state === "github") await screen.findByText(/1 of 2 repositories linked/);
      if (c.state === "delete-confirm") {
        fireEvent.click(screen.getByRole("button", { name: "delete collective" }));
        await screen.findByRole("dialog");
      }
      return document.body;
    }

    case "contribute-picker": {
      const shared: string[] = [];
      vi.stubGlobal(
        "fetch",
        vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
          const url = String(input);
          if (url.endsWith("/collectives")) {
            return json(
              c.state === "no-collectives"
                ? []
                : [
                    {
                      id: GROUP_ID,
                      name: content.collective,
                      description: null,
                      linked_github_org: null,
                      display_members: true,
                      transcript_deletion_policy: "user_choice",
                      created_by: "user-owner",
                      created_at: WHEN,
                      updated_at: WHEN,
                      acceptance_mode: "open",
                      data_access: "members_only",
                      role: "member",
                      member_since: WHEN,
                    },
                  ],
            );
          }
          if (url.endsWith(`/transcripts/${TRANSCRIPT_ID}/share`) && init?.method === "POST") {
            shared.push(String(init.body));
            return json({ ok: true });
          }
          throw new Error(`contribute-picker fixture received an unexpected request to ${url}`);
        }),
      );
      await mount(
        <ContributePicker
          open
          onClose={() => {}}
          transcriptId={TRANSCRIPT_ID}
          transcriptTitle={content.transcript}
          transcriptVisibility="shared"
        />,
      );
      if (c.state === "no-collectives") {
        await screen.findByText(/joined any collectives yet/);
        return document.body;
      }
      await screen.findByText(content.collective);
      if (c.state === "contributed") {
        fireEvent.click(screen.getByRole("button", { name: new RegExp(content.collective) }));
        fireEvent.click(screen.getByRole("button", { name: "contribute" }));
        await screen.findByText(`contributed to ${content.collective}.`);
        expect(shared).toEqual([JSON.stringify({ group_ids: [GROUP_ID] })]);
      }
      return document.body;
    }

    case "profile-page": {
      // The profile of the viewer's own handle: their own page when they are
      // signed in as it, somebody else's when a visitor reads it, and the
      // route's not-found state when the server has no such profile.
      installProfileRESTFixture({
        profileUsername: content.viewer,
        viewerUsername: c.state === "own" ? content.viewer : "bob",
        contributions: [],
        library: [["transcript-flaky-ingest", "village-project"]],
        profileStatus: c.state === "not-found" ? 404 : undefined,
      });
      await renderProfileRoute(content.viewer);
      if (c.state === "not-found") {
        await screen.findByTestId("profile-not-found");
        await screen.findByText("back to home");
      } else {
        await screen.findByText("published library");
        await waitFor(() => expect(document.body.textContent).toContain("transcript-flaky-ingest"));
        if (c.state === "own") await screen.findAllByText("discoverable profile");
      }
      return document.body;
    }

    case "account-settings": {
      installSettingsRouteREST(loadAccountSettingsFixtures().cases[0]);
      await renderSettingsRoute();
      await screen.findByTestId("settings-delete");
      return document.body;
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
