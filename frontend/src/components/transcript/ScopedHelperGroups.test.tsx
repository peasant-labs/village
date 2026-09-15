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
  memberFactStrings,
  memberItem,
  memberPageFor,
  memberSpecByLabel,
  memberUUID,
  type GroupedHelperMountCase,
  type MemberPageSpec,
  type MemberRequestExpectation,
} from "@/test/groupedHelperMountFixtures";

/**
 * Mounted evidence for the Village helper-group host.
 *
 * The REAL `ScopedHelperGroup` renders Fairtrade's published primitives through
 * the REAL member query hook; only HTTP is controlled, so every request the
 * host sends is observable. This file is an INTERPRETER: the case this test
 * runs — which group, whether it opens, the member page it reaches, the member
 * it selects, the retry it presses — and the expectations it asserts (the exact
 * requests, the mounted identities, the reported selection, the notice) all
 * come from `src/testdata/grouped-helper-mounts.yaml`. Change an expectation
 * there and this test fails; a case whose fields the loader's arm validation
 * cannot honour never mounts.
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

function recordedRequest(call: Call): MemberRequestExpectation {
  return { scope: call.scope ?? "", page: call.page ?? "", limit: call.limit ?? "" };
}

function specFor(call: Call): MemberPageSpec | undefined {
  return call.scope == null ? undefined : memberPageFor(fixtures, call.scope, Number(call.page ?? 1));
}

/**
 * Serves the declared member pages, keyed by scope and page, and answers the
 * case's own scope with its declared status. A status other than 200 is the
 * same shape the server sends — the host branches on the status, never on the
 * words.
 */
function installMembersREST(testCase: GroupedHelperMountCase): Call[] {
  const calls: Call[] = [];
  const statusByScope: Record<string, number> = {};
  if (testCase.scopeStatus !== undefined) {
    statusByScope[fixtures.groups[testCase.group].memberScope] = testCase.scopeStatus;
  }
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(String(input));
      if (url.pathname.endsWith("/auth/me")) {
        return new Response(JSON.stringify({ error: "signed out" }), { status: 401 });
      }
      const scope = url.searchParams.get("scope");
      const page = url.searchParams.get("page");
      calls.push({
        path: url.pathname,
        scope,
        page,
        limit: url.searchParams.get("limit"),
      });
      const status = scope == null ? 500 : (statusByScope[scope] ?? 200);
      if (status !== 200) {
        return new Response(
          JSON.stringify({ code: "group_scope_expired", error: "refresh the originating list" }),
          { status },
        );
      }
      const spec = memberPageFor(fixtures, scope ?? "", Number(page ?? 1));
      if (spec == null) {
        return new Response(JSON.stringify({ error: "unknown scope" }), { status: 409 });
      }
      return new Response(
        JSON.stringify({
          members: spec.members.map(memberItem),
          page: Number(page ?? 1),
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

function contextItem(): VillageSessionListItem {
  return {
    kind: "context_container" as const,
    context: { groupId: fixtures.context.groupId, ownerStatus: fixtures.context.ownerStatus },
    helperGroups: [fixtures.groups.context],
  } as unknown as VillageSessionListItem;
}

function SelectionHost({
  group,
  onRefreshOrigin,
  onSelection,
}: {
  group: (typeof fixtures.groups)["owner"];
  onRefreshOrigin: () => void;
  onSelection: (ids: string[]) => void;
}) {
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set());
  const selection: ScopedHelperSelection = {
    selectedIds: selected,
    onToggle: (item, next) => {
      const id = item.transcript?.session.id ?? "";
      setSelected((previous) => {
        const updated = new Set(previous);
        if (next) updated.add(id);
        else updated.delete(id);
        onSelection([...updated]);
        return updated;
      });
    },
  };
  return (
    <ScopedHelperGroup group={group} onRefreshOrigin={onRefreshOrigin} selection={selection} />
  );
}

for (const testCase of fixtures.cases) {
  it(testCase.name, async () => {
    const seen: string[][] = [];
    const onRefreshOrigin = vi.fn();
    const calls = installMembersREST(testCase);

    if (testCase.group === "context") {
      render(wrapper(<ScopedContextHelperGroups item={contextItem()} onRefreshOrigin={onRefreshOrigin} />));
      // The authorized owner status is named with no fabricated row or link, and
      // the control states the count the server reported.
      expect(document.body.textContent).toContain("owner is unavailable");
      expect(document.querySelectorAll("a[href^='/transcripts/']")).toHaveLength(0);
      expect(threadIds()).toEqual([]);
      expect(trigger(fixtures.groups.context.groupId).textContent).toContain(
        `${fixtures.groups.context.helperThreadCount} helper thread`,
      );
    } else {
      const group = fixtures.groups[testCase.group];
      if (testCase.expectedSelected != null) {
        render(
          wrapper(
            <SelectionHost
              group={group}
              onRefreshOrigin={onRefreshOrigin}
              onSelection={(ids) => seen.push(ids)}
            />,
          ),
        );
      } else {
        render(
          wrapper(
            <>
              <ScopedHelperGroup group={group} onRefreshOrigin={onRefreshOrigin} />
              {testCase.secondGroup != null && (
                <ScopedHelperGroup
                  group={fixtures.groups[testCase.secondGroup]}
                  onRefreshOrigin={onRefreshOrigin}
                />
              )}
            </>,
          ),
        );
      }

      if (!testCase.expand) {
        expect(trigger(group.groupId)).toHaveAttribute("aria-expanded", "false");
      } else {
        fireEvent.click(trigger(group.groupId));
        // The in-flight state is asserted before the page can arrive, so a host
        // that skipped it and claimed an empty result would fail here.
        if (testCase.loadingNotice != null) {
          expect(screen.getByTestId(testCase.loadingNotice)).toBeInTheDocument();
        }
        // Reach the case's member page through the real pager controls. The
        // pager is mounted by the loaded page, so the first click waits for it.
        for (let step = 2; step <= (testCase.page ?? 1); step += 1) {
          const pager = await screen.findByRole("button", { name: "next" });
          fireEvent.click(pager);
          await waitFor(() => expect(calls).toHaveLength(step));
        }
        if (testCase.secondGroup != null) {
          fireEvent.click(trigger(fixtures.groups[testCase.secondGroup].groupId));
        }
        if (testCase.expectedNotice === "scope-expired") {
          await waitFor(() =>
            expect(document.body.textContent).toContain("the saved helper query expired"),
          );
          expect(screen.queryByTestId("helper-group-load-failed")).toBeNull();
        } else if (testCase.expectedNotice === "member-load-failed") {
          await waitFor(() => expect(screen.getByTestId("helper-group-load-failed")).toBeInTheDocument());
          // A failed load is never presented as a successful empty result.
          expect(document.body.textContent).not.toContain("no saved helpers match");
        } else {
          await waitFor(() => expect(threadIds()).toEqual(testCase.expectedMembers.map(memberUUID)));
        }
        if (testCase.retry) {
          fireEvent.click(screen.getByRole("button", { name: /retry/i }));
          await waitFor(() => expect(calls).toHaveLength(testCase.expectedRequests.length));
        }
        if (testCase.select != null) {
          const member = calls
            .map(specFor)
            .flatMap((spec) => spec?.members ?? [])
            .find((row) => row.id === testCase.select);
          if (member == null) throw new Error(`case ${testCase.name} selected an unserved member`);
          fireEvent.click(
            screen.getByRole("checkbox", { name: new RegExp(`select ${member.title}`, "i") }),
          );
        }
      }
    }

    // The requests the host actually issued, in order, are the case's own.
    expect(calls.map(recordedRequest)).toEqual(testCase.expectedRequests);
    expect(calls.every((call) => call.path.startsWith("/api/v1/transcript-groups/"))).toBe(true);
    expect(threadIds()).toEqual(testCase.expectedMembers.map(memberUUID));
    // The honest facts line of every MOUNTED member, including a measured zero
    // and an absent value, which never become a turn count. Members the case
    // did not expect to be mounted are not asserted.
    if (testCase.expectedNotice == null) {
      for (const label of testCase.expectedMembers) {
        const spec = memberSpecByLabel(fixtures, label);
        if (spec == null) throw new Error(`case ${testCase.name} expects an undeclared member ${label}`);
        for (const fact of memberFactStrings(spec)) {
          expect(document.body.textContent).toContain(fact);
        }
      }
    }
    if (testCase.expectedSelected != null) {
      expect(seen).toEqual([testCase.expectedSelected.map(memberUUID)]);
      const unselected = testCase.expectedMembers.find(
        (label) => !testCase.expectedSelected?.includes(label),
      );
      if (unselected != null) {
        const spec = memberSpecByLabel(fixtures, unselected);
        expect(
          screen.getByRole("checkbox", { name: new RegExp(`select ${spec?.title}`, "i") }),
        ).not.toBeChecked();
      }
    }
    if (testCase.expectedNotice === "scope-expired") {
      // Fail-closed: the originating list is refreshed only when its own control
      // is pressed, and never an all-members fallback.
      expect(onRefreshOrigin).not.toHaveBeenCalled();
      fireEvent.click(screen.getByRole("button", { name: /refresh list/i }));
      expect(onRefreshOrigin).toHaveBeenCalledTimes(testCase.expectedOriginRefreshes ?? 0);
    } else {
      expect(onRefreshOrigin).not.toHaveBeenCalled();
    }
    if (testCase.collapseSecond && testCase.secondGroup != null) {
      const firstPage = specFor(calls[0]);
      fireEvent.click(trigger(fixtures.groups[testCase.secondGroup].groupId));
      await waitFor(() =>
        expect(threadIds()).toEqual((firstPage?.members ?? []).map((member) => memberUUID(member.id))),
      );
    }
  });
}
