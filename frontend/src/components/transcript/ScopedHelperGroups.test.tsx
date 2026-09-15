import { useState } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { AuthProvider } from "@/providers/AuthProvider";
import type { VillageSessionListItem } from "@peasant-labs/schema";
import {
  ScopedContextHelperGroups,
  ScopedHelperGroup,
  type ScopedHelperSelection,
} from "./ScopedHelperGroups";
import {
  loadGroupedHelperMountFixtures,
  memberItem,
  memberUUID,
  type GroupedHelperMountCase,
} from "@/test/groupedHelperMountFixtures";

/**
 * Mounted evidence for the Village helper-group host.
 *
 * The REAL `ScopedHelperGroup` renders Fairtrade's published primitives through
 * the REAL member query hook; only HTTP is controlled, so every request the
 * host sends is observable. One case per behaviour the slice names: disclosure
 * sends no request while collapsed, expansion replays the exact scope, paging
 * keeps that scope, a 409 hides members and offers only an originating-list
 * refresh, selection is per member, and each group is independent.
 */

const fixtures = loadGroupedHelperMountFixtures();

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  document.documentElement.setAttribute("data-theme", "dark");
});

interface Call {
  path: string;
  scope: string | null;
  page: string | null;
  limit: string | null;
}

function installMembersREST(statusByScope: Record<string, number>): Call[] {
  const calls: Call[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(String(input));
      if (url.pathname.endsWith("/auth/me")) {
        return new Response(JSON.stringify({ error: "signed out" }), { status: 401 });
      }
      const scope = url.searchParams.get("scope");
      calls.push({
        path: url.pathname,
        scope,
        page: url.searchParams.get("page"),
        limit: url.searchParams.get("limit"),
      });
      const status = scope == null ? 500 : (statusByScope[scope] ?? 200);
      if (status !== 200) {
        return new Response(
          JSON.stringify({ code: "group_scope_expired", error: "refresh the originating list" }),
          { status },
        );
      }
      const page = Number(url.searchParams.get("page") ?? 1);
      const model = page === 2 ? fixtures.pages.pageTwo : fixtures.pages.pageOne;
      const other = fixtures.pages.otherMembers;
      const spec =
        scope === fixtures.groups.owner.memberScope
          ? model
          : scope === fixtures.groups.other.memberScope
            ? other
            : null;
      if (spec == null) {
        return new Response(JSON.stringify({ error: "unknown scope" }), { status: 409 });
      }
      return new Response(
        JSON.stringify({
          members: spec.members.map(memberItem),
          page: spec.page,
          limit: spec.limit,
          total: spec.total,
        }),
        { status: 200 },
      );
    }),
  );
  return calls;
}

function wrapper(children: React.ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return (
    <QueryClientProvider client={client}>
      <AuthProvider>{children}</AuthProvider>
    </QueryClientProvider>
  );
}

function groupRoot(groupId: string): HTMLElement {
  const root = document.querySelector<HTMLElement>(`.helper-group[data-group-id="${groupId}"]`);
  if (root == null) throw new Error(`helper group ${groupId} is not mounted`);
  return root;
}

function trigger(groupId: string): HTMLButtonElement {
  const button = groupRoot(groupId).querySelector<HTMLButtonElement>("button.helper-group-trigger");
  if (button == null) throw new Error(`helper group ${groupId} has no disclosure control`);
  return button;
}

function threadIds(): string[] {
  return [...document.querySelectorAll<HTMLElement>(".helper-thread-row[data-thread-id]")].map(
    (row) => row.dataset.threadId ?? "",
  );
}

const caseByName = (name: string): GroupedHelperMountCase => {
  const found = fixtures.cases.find((value) => value.name === name);
  if (found == null) throw new Error(`missing case ${name}`);
  return found;
};

it("collapsed-sends-no-request", () => {
  const calls = installMembersREST({});
  render(wrapper(<ScopedHelperGroup group={fixtures.groups.owner} onRefreshOrigin={vi.fn()} />));
  expect(calls).toEqual([]);
  expect(threadIds()).toEqual([]);
  expect(trigger(fixtures.groups.owner.groupId)).toHaveAttribute("aria-expanded", "false");
});

it("expand-loads-exact-scope", async () => {
  const testCase = caseByName("expand-loads-exact-scope");
  const calls = installMembersREST({});
  render(wrapper(<ScopedHelperGroup group={fixtures.groups.owner} onRefreshOrigin={vi.fn()} />));

  fireEvent.click(trigger(fixtures.groups.owner.groupId));
  await waitFor(() => expect(threadIds()).toHaveLength(2));

  expect(calls).toHaveLength(1);
  expect(calls[0].path).toBe(`/api/v1/transcript-groups/${fixtures.groups.owner.groupId}/members`);
  expect({ scope: calls[0].scope, page: calls[0].page, limit: calls[0].limit }).toEqual(
    testCase.expectedRequest,
  );

  // Every member is one explicit, individually linked transcript row.
  expect(threadIds()).toEqual([memberUUID("helper-a"), memberUUID("helper-b")]);
  const links = [...document.querySelectorAll<HTMLAnchorElement>("a.helper-thread-open")].map(
    (anchor) => anchor.getAttribute("href"),
  );
  expect(links).toEqual([
    `/transcripts/${memberUUID("helper-a")}`,
    `/transcripts/${memberUUID("helper-b")}`,
  ]);

  // A measured zero and an absent value stay distinct, and neither becomes a
  // turn count.
  const facts = document.body.textContent ?? "";
  expect(facts).toContain("0 input submissions");
  expect(facts).toContain("unknown input submissions");
  expect(facts).toContain("5 turns");
});

it("page-two-keeps-original-scope", async () => {
  const calls = installMembersREST({});
  render(wrapper(<ScopedHelperGroup group={fixtures.groups.owner} onRefreshOrigin={vi.fn()} />));
  fireEvent.click(trigger(fixtures.groups.owner.groupId));
  await waitFor(() => expect(threadIds()).toHaveLength(2));

  fireEvent.click(screen.getByRole("button", { name: "next" }));
  await waitFor(() => expect(threadIds()).toEqual([memberUUID("helper-c")]));

  expect(calls).toHaveLength(2);
  expect(calls[1].scope).toBe(fixtures.groups.owner.memberScope);
  expect(calls[1].page).toBe("2");
  expect(calls[1].limit).toBe("20");
});

it("expired-scope-hides-members-and-refreshes-origin-only", async () => {
  const onRefreshOrigin = vi.fn();
  installMembersREST({ [fixtures.groups.owner.memberScope]: 409 });
  render(
    wrapper(<ScopedHelperGroup group={fixtures.groups.owner} onRefreshOrigin={onRefreshOrigin} />),
  );
  fireEvent.click(trigger(fixtures.groups.owner.groupId));

  await waitFor(() => expect(document.body.textContent).toContain("the saved helper query expired"));
  expect(threadIds()).toEqual([]);
  expect(onRefreshOrigin).not.toHaveBeenCalled();

  fireEvent.click(screen.getByRole("button", { name: /refresh list/i }));
  expect(onRefreshOrigin).toHaveBeenCalledTimes(1);
});

function SelectionHost({
  group,
  onSelection,
}: {
  group: (typeof fixtures.groups)["owner"];
  onSelection: (ids: string[]) => void;
}) {
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set());
  const selection: ScopedHelperSelection = {
    selectedIds: selected,
    onToggle: (id, next) => {
      setSelected((previous) => {
        const updated = new Set(previous);
        if (next) updated.add(id);
        else updated.delete(id);
        onSelection([...updated]);
        return updated;
      });
    },
  };
  return <ScopedHelperGroup group={group} onRefreshOrigin={vi.fn()} selection={selection} />;
}

it("selection-is-one-member", async () => {
  const seen: string[][] = [];
  installMembersREST({});
  render(wrapper(<SelectionHost group={fixtures.groups.owner} onSelection={(ids) => seen.push(ids)} />));
  fireEvent.click(trigger(fixtures.groups.owner.groupId));
  await waitFor(() => expect(threadIds()).toHaveLength(2));

  fireEvent.click(screen.getByRole("checkbox", { name: /select helper b/i }));
  // Exactly the checked member; no owner, sibling, context or group id.
  expect(seen).toEqual([[memberUUID("helper-b")]]);
  expect(screen.getByRole("checkbox", { name: /select helper a/i })).not.toBeChecked();
});

it("independent-groups-are-independent", async () => {
  const calls = installMembersREST({});
  render(
    wrapper(
      <>
        <ScopedHelperGroup group={fixtures.groups.owner} onRefreshOrigin={vi.fn()} />
        <ScopedHelperGroup group={fixtures.groups.other} onRefreshOrigin={vi.fn()} />
      </>,
    ),
  );

  fireEvent.click(trigger(fixtures.groups.owner.groupId));
  fireEvent.click(trigger(fixtures.groups.other.groupId));
  await waitFor(() =>
    expect(threadIds().sort()).toEqual(
      [memberUUID("helper-a"), memberUUID("helper-b"), memberUUID("helper-z")].sort(),
    ),
  );

  const scopes = calls.map((call) => call.scope).sort();
  expect(scopes).toEqual(
    [fixtures.groups.other.memberScope, fixtures.groups.owner.memberScope].sort(),
  );
  expect(calls.every((call) => call.page === "1")).toBe(true);

  // Closing one group leaves the other's rows mounted.
  fireEvent.click(trigger(fixtures.groups.other.groupId));
  await waitFor(() =>
    expect(threadIds()).toEqual([memberUUID("helper-a"), memberUUID("helper-b")]),
  );
});

it("context-container-has-no-row", () => {
  installMembersREST({});
  const item = {
    kind: "context_container" as const,
    context: { groupId: fixtures.context.groupId, ownerStatus: fixtures.context.ownerStatus },
    helperGroups: [fixtures.groups.context],
  } as unknown as VillageSessionListItem;
  render(wrapper(<ScopedContextHelperGroups item={item} onRefreshOrigin={vi.fn()} />));

  // The authorized owner status is named; no fabricated title, row or link.
  expect(document.body.textContent).toContain("owner is unavailable");
  expect(document.querySelectorAll("a[href^='/transcripts/']")).toHaveLength(0);
  expect(threadIds()).toEqual([]);
  expect(trigger(fixtures.groups.context.groupId).textContent).toContain("1 helper thread");
});
