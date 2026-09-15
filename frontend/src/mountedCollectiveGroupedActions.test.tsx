import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import GroupContributePage from "@/app/groups/[id]/contribute/page";
import GroupDetailPage from "@/app/groups/[id]/page";
import GroupReviewPage from "@/app/groups/[id]/review/page";
import { AuthProvider } from "@/providers/AuthProvider";
import type { Group, User } from "@/lib/types";
import {
  flatBrowseRows,
  flatContributeRows,
  flatPendingRows,
  groupedDetailPayload,
  groupedItems,
  loadCollectiveGroupedActionFixtures,
  memberPage,
  type CollectiveActionCase,
} from "@/test/collectiveGroupedActionFixtures";
import { memberUUID } from "@/test/groupedHelperMountFixtures";

/**
 * Mounted evidence for the collective grouped-helper contribution and review
 * actions, driven by `src/testdata/collective-grouped-actions.yaml`.
 *
 * The REAL `/groups/{id}/contribute` and `/groups/{id}/review` routes render,
 * each through its REAL grouped query hook and REAL mutation hook; only HTTP is
 * controlled. The flat list stays the authority for its own rows; the grouped
 * read attaches the server's saved helper groups to those rows. A person
 * expands a group, ticks ONE member, and the request body names exactly that
 * transcript id — never a group id, never a sibling the viewer did not tick.
 */

const fixtures = loadCollectiveGroupedActionFixtures();
const GROUP_ID = fixtures.groupId;
const VIEWER: User = {
  id: "user-viewer",
  github_id: 1,
  github_username: "viewer",
  display_name: "viewer",
  avatar_url: null,
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
  is_discoverable: true,
  username_chosen: true,
  provider_username: "viewer",
};

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  globalThis.localStorage?.clear();
});

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
}

function groupFixture(role: "member" | "owner"): Group {
  return {
    id: GROUP_ID,
    name: "grouped actions collective",
    description: null,
    linked_github_org: null,
    display_members: true,
    transcript_deletion_policy: "user_choice",
    created_by: "someone-else",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    acceptance_mode: "curated",
    data_access: "members_only",
    role,
    member_since: "2026-01-01T00:00:00Z",
  };
}

interface MutationCall {
  method: string;
  path: string;
  body: { transcript_ids: string[]; project_hash?: string; status?: string };
}

interface Calls {
  grouped: string[];
  members: string[];
  mutations: MutationCall[];
}

function installREST(surface: "browse" | "contribute" | "review", testCase: CollectiveActionCase): Calls {
  const calls: Calls = { grouped: [], members: [], mutations: [] };
  const items = groupedItems(fixtures, testCase);
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input));
      const path = url.pathname;
      const method = (init?.method ?? "GET").toUpperCase();
      const grouped = url.searchParams.get("view") === "grouped";

      if (path.endsWith("/auth/me")) return json(VIEWER);
      if (path.endsWith("/auth/orgs")) return json([]);
      if (path === `/api/v1/groups/${GROUP_ID}`) {
        if (grouped) {
          calls.grouped.push(url.search);
          return json(groupedDetailPayload(fixtures, testCase));
        }
        return json({
          group: groupFixture(surface === "review" ? "owner" : "member"),
          members: [],
          transcripts: surface === "browse" ? flatBrowseRows(fixtures) : [],
          stats: {},
          models: [],
          contributors: [],
          can_read: true,
          your_role: surface === "review" ? "owner" : "member",
          pending_members: [],
        });
      }
      if (path === `/api/v1/groups/${GROUP_ID}/contributable`) {
        if (grouped) {
          calls.grouped.push(url.search);
          return json({
            groupId: GROUP_ID,
            transcriptList: {
              items,
              page: 1,
              limit: 100,
              totalItems: items.length,
              ordinarySessionTotal: fixtures.rows.length,
              helperThreadTotal: 2,
            },
          });
        }
        return json({ group_id: GROUP_ID, transcripts: flatContributeRows(fixtures) });
      }
      if (path === `/api/v1/groups/${GROUP_ID}/pending`) {
        if (grouped) {
          calls.grouped.push(url.search);
          return json({
            items,
            page: 1,
            limit: 100,
            totalItems: items.length,
            ordinarySessionTotal: fixtures.rows.length,
            helperThreadTotal: 2,
          });
        }
        return json(flatPendingRows(fixtures));
      }
      if (path.includes("/transcript-groups/")) {
        calls.members.push(url.search);
        const scope = url.searchParams.get("scope");
        const key = (Object.keys(fixtures.groups) as (keyof typeof fixtures.groups)[]).find(
          (candidate) => fixtures.groups[candidate].memberScope === scope,
        );
        if (key == null) return json({ error: "unknown scope" }, 409);
        return json(memberPage(fixtures, key, surface));
      }
      if (path === `/api/v1/groups/${GROUP_ID}/shares` && method === "POST") {
        const body = JSON.parse(String(init?.body));
        calls.mutations.push({ method, path, body });
        return json({
          project_hash: body.project_hash,
          shared: (body.transcript_ids as string[]).map((id) => ({ transcript_id: id, status: "pending" })),
          already_shared: [],
        });
      }
      if (path === `/api/v1/groups/${GROUP_ID}/shares` && method === "PATCH") {
        const body = JSON.parse(String(init?.body));
        calls.mutations.push({ method, path, body });
        return json({ decided: body.transcript_ids, already_decided: [] });
      }
      throw new Error(`collective grouped action fixture received an unexpected ${method} ${path}${url.search}`);
    }),
  );
  return calls;
}

function renderRoute(surface: "browse" | "contribute" | "review") {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <AuthProvider>
        {surface === "contribute" ? (
          <GroupContributePage params={Promise.resolve({ id: GROUP_ID })} />
        ) : surface === "review" ? (
          <GroupReviewPage params={Promise.resolve({ id: GROUP_ID })} />
        ) : (
          <GroupDetailPage params={Promise.resolve({ id: GROUP_ID })} />
        )}
      </AuthProvider>
    </QueryClientProvider>,
  );
}

function groupOwnerSlot(transcriptID: string): HTMLElement {
  const slot = document.querySelector<HTMLElement>(`[data-helper-group-owner="${transcriptID}"]`);
  if (slot == null) throw new Error(`row ${transcriptID} mounted no helper-group slot`);
  return slot;
}

async function expandGroupUnder(transcriptID: string, memberTitle: string): Promise<void> {
  const trigger = groupOwnerSlot(transcriptID).querySelector<HTMLButtonElement>(
    "button.helper-group-trigger",
  );
  if (trigger == null) throw new Error(`row ${transcriptID} carries no helper-group disclosure`);
  act(() => {
    fireEvent.click(trigger);
  });
  await waitFor(() =>
    expect(
      document.querySelector(`input[aria-label^="select ${memberTitle} ("]`),
      "the expanded group mounted no member row",
    ).not.toBeNull(),
  );
}

function memberCheckbox(label: string): HTMLInputElement {
  const box = document.querySelector<HTMLInputElement>(`input[aria-label^="select ${label} ("]`);
  if (box == null) throw new Error(`member ${label} has no selection checkbox`);
  return box;
}

for (const testCase of fixtures.cases) {
  it(testCase.name, async () => {
    const row = fixtures.rows.find((candidate) => candidate.name === testCase.row)!;
    const calls = installREST(testCase.surface, testCase);

    await act(async () => {
      renderRoute(testCase.surface);
    });

    if (testCase.surface === "browse") {
      // The collective's browse list is still its own list; the grouped read
      // attaches the server's saved helper group under the owner row it was
      // grouped with, in exactly one place.
      await waitFor(() => expect(calls.grouped).toHaveLength(1));
      const roots = await waitFor(() => {
        const found = document.querySelectorAll(
          `.helper-group[data-group-id="${fixtures.groups.owner.groupId}"]`,
        );
        expect(found).toHaveLength(1);
        return found;
      });
      const slot = document.querySelector('[data-testid="owner-helper-groups"]');
      expect(slot, "the group hangs off the owner row the grouped read named").not.toBeNull();
      expect(slot!.contains(roots[0])).toBe(true);

      const trigger = roots[0].querySelector<HTMLButtonElement>("button.helper-group-trigger");
      expect(trigger).not.toBeNull();
      act(() => {
        fireEvent.click(trigger!);
      });
      const member = fixtures.members.owner[0];
      await waitFor(() => expect(calls.members).toHaveLength(1));
      const link = await waitFor(() => {
        const found = document.querySelector<HTMLAnchorElement>("a.helper-thread-open");
        expect(found).not.toBeNull();
        return found!;
      });
      expect(link.getAttribute("href")).toBe(`/transcripts/${memberUUID(member.name)}`);
      // The browse list is read-only here: no per-member selection box appears.
      expect(document.querySelector('input[aria-label^="select "]')).toBeNull();
      return;
    }

    // The flat tree row is still the row on screen; the grouped read only
    // supplements it.
    await waitFor(() =>
      expect(screen.getByTestId(`contribute-session-row-${row.id}`)).toBeInTheDocument(),
    );
    await waitFor(() => expect(calls.grouped).toHaveLength(1));

    if (testCase.context) {
      // The helper-only context container states the authorized owner status and
      // carries no selectable row, so nothing in it can enter a mutation.
      await waitFor(() =>
        expect(document.body.textContent).toContain("owner is unavailable"),
      );
      const contextRoot = document.querySelector(
        `.helper-group[data-group-id="${fixtures.groups.context.groupId}"]`,
      )!;
      expect(contextRoot.querySelectorAll('input[type="checkbox"]')).toHaveLength(0);
      expect(contextRoot.querySelectorAll("a.helper-thread-open")).toHaveLength(0);
      // Nothing is selected, so the action is unavailable and no request fires.
      expect(screen.getByRole("button", { name: /contribute 0 transcripts/ })).toBeDisabled();
      expect(calls.mutations).toHaveLength(0);
      return;
    }

    const selectedMember = fixtures.members[testCase.group].find((m) => m.name === testCase.select)!;
    await expandGroupUnder(row.id, selectedMember.title);
    const box = memberCheckbox(selectedMember.title);
    act(() => {
      fireEvent.click(box);
    });

    if (testCase.surface === "contribute") {
      // The group is attached to the row the server grouped it under, and the
      // member's own explicit id is the only one the batch can name.
      expect(calls.mutations).toHaveLength(0);
      const button = screen.getByRole("button", { name: /contribute 1 transcript/ });
      await act(async () => {
        fireEvent.click(button);
      });
      await waitFor(() => expect(calls.mutations).toHaveLength(1));
      expect(calls.mutations[0].method).toBe("POST");
      expect(calls.mutations[0].body.transcript_ids).toEqual(
        testCase.expected_ids.map((label) => memberUUID(label)),
      );
      // The mutation names explicit transcript ids and this collective's
      // project, never a group id.
      expect(JSON.stringify(calls.mutations[0].body)).not.toContain("GroupID");
      expect(JSON.stringify(calls.mutations[0].body)).not.toContain("groupId");
    } else {
      expect(calls.mutations).toHaveLength(0);
      const approve = screen.getByRole("button", { name: /approve selected/ });
      await act(async () => {
        fireEvent.click(approve);
      });
      await waitFor(() => expect(calls.mutations).toHaveLength(1));
      expect(calls.mutations[0].method).toBe("PATCH");
      expect(calls.mutations[0].body.transcript_ids).toEqual(
        testCase.expected_ids.map((label) => memberUUID(label)),
      );
      expect(calls.mutations[0].body.status).toBe("approved");
      expect(JSON.stringify(calls.mutations[0].body)).not.toContain("groupId");
    }
  });
}
