import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { AuthProvider } from "@/providers/AuthProvider";
import { ScopedHelperGroupedRow, itemSession } from "@/components/transcript/ScopedHelperGroups";
import {
  loadGroupedShapeOracleFixtures,
  ownerItem,
  shapeMemberPageFor,
} from "@/test/groupedShapeOracleFixtures";
import { memberItem, memberUUID } from "@/test/groupedHelperMountFixtures";

/**
 * Mounted evidence for the two named grouping oracles.
 *
 * `src/testdata/grouped-shape-oracles.yaml` states both shapes and this test
 * mounts them through the REAL `ScopedHelperGroupedRow` (the production row the
 * grouped list and the collective fallback both draw) with the REAL member query
 * hook. Only HTTP is controlled. Every assertion comes from the case's own
 * PINNED expectations -- the literal owner facts, disclosure labels, member ids
 * and member facts -- never from the served measures themselves, so an edit to
 * the served data fails a case instead of agreeing with itself.
 */

const fixtures = loadGroupedShapeOracleFixtures();

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  document.documentElement.setAttribute("data-theme", "dark");
});

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

/** Serves the declared member pages; every other request is answerable but empty. */
function installREST(): string[] {
  const calls: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(String(input));
      if (url.pathname.endsWith("/auth/me")) return json({ error: "signed out" }, 401);
      if (url.pathname.includes("/transcript-groups/")) {
        calls.push(url.search);
        const scope = url.searchParams.get("scope") ?? "";
        const page = Number(url.searchParams.get("page") ?? 1);
        const spec = shapeMemberPageFor(fixtures, scope, page);
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

function wrapper(children: React.ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return (
    <QueryClientProvider client={client}>
      <AuthProvider>{children}</AuthProvider>
    </QueryClientProvider>
  );
}

function ownerRoots(): HTMLElement[] {
  return [...document.querySelectorAll<HTMLElement>(".helper-group-item")];
}

/** The top-level owner rows this page draws, in mount order. */
function ownerRowIds(): string[] {
  return ownerRoots().map(
    (root) =>
      root.querySelector<HTMLElement>(
        ".helper-tree-rows > .helper-tree-row:first-child .helper-thread-row",
      )?.dataset.threadId ?? "",
  );
}

function ownerRoot(id: string): HTMLElement {
  const root = ownerRoots().find(
    (candidate) =>
      candidate.querySelector<HTMLElement>(
        ".helper-tree-rows > .helper-tree-row:first-child .helper-thread-row",
      )?.dataset.threadId === id,
  );
  if (root == null) throw new Error(`owner row ${id} is not mounted`);
  return root;
}

function groupTrigger(groupId: string): HTMLButtonElement {
  const root = document.querySelector<HTMLElement>(`.helper-group[data-group-id="${groupId}"]`);
  if (root == null) throw new Error(`helper group ${groupId} is not mounted`);
  const trigger = root.querySelector<HTMLButtonElement>("button.sgd-trigger");
  if (trigger == null) throw new Error(`helper group ${groupId} has no disclosure control`);
  return trigger;
}

/** The member rows a group discloses while it is open. */
function memberRowIds(groupId: string): string[] {
  const root = document.querySelector<HTMLElement>(`.helper-group[data-group-id="${groupId}"]`);
  if (root == null) return [];
  return [...root.querySelectorAll<HTMLElement>(".helper-group-body .helper-thread-row")].map(
    (row) => row.dataset.threadId ?? "",
  );
}

/**
 * The one member row with this identity inside its own group body. A fact is
 * asserted on THIS row, never on the document, so another owner or helper that
 * happens to state the same literal cannot satisfy a missing member fact.
 */
function memberRow(groupId: string, memberId: string): HTMLElement {
  const root = document.querySelector<HTMLElement>(`.helper-group[data-group-id="${groupId}"]`);
  const row = root?.querySelector<HTMLElement>(
    `.helper-group-body .helper-thread-row[data-thread-id="${memberUUID(memberId)}"]`,
  );
  if (row == null) throw new Error(`member row ${memberId} is not mounted in group ${groupId}`);
  return row;
}

for (const testCase of fixtures.cases) {
  it(testCase.name, async () => {
    const calls = installREST();
    const items = testCase.ownerKeys.map((ownerKey) => ownerItem(fixtures, ownerKey));

    render(
      wrapper(
        <>
          {items.map((item) => (
            <ScopedHelperGroupedRow
              key={itemSession(item)?.id}
              item={item}
              onRefreshOrigin={() => {}}
            />
          ))}
        </>,
      ),
    );

    // Every declared top-level owner row is drawn once, in declaration order.
    expect(ownerRowIds()).toEqual(testCase.ownerKeys.map(memberUUID));
    expect(ownerRowIds()).toHaveLength(testCase.topLevelItems);
    expect(testCase.ordinarySessionTotal).toBe(testCase.ownerKeys.length);

    // Each owner states its pinned input-submission and turn facts.
    for (const ownerKey of testCase.ownerKeys) {
      const root = ownerRoot(memberUUID(ownerKey));
      for (const fact of testCase.expectedOwnerFacts[ownerKey]) {
        expect(root.textContent).toContain(fact);
      }
    }

    // Every group hangs off its owning row and states its pinned disclosure label.
    let disclosedThreads = 0;
    for (const ownerKey of testCase.ownerKeys) {
      const root = ownerRoot(memberUUID(ownerKey));
      for (const groupKey of fixtures.owners[ownerKey].groupKeys) {
        const group = fixtures.groups[groupKey];
        disclosedThreads += group.helperThreadCount;
        const trigger = groupTrigger(group.groupId);
        expect(root.contains(trigger)).toBe(true);
        expect(trigger.textContent).toContain(testCase.expectedGroupLabels[groupKey]);
      }
    }
    expect(disclosedThreads).toBe(testCase.helperThreadTotal);

    // Opening a declared disclosure serves exactly its own pinned member page.
    for (const groupKey of testCase.expand) {
      const group = fixtures.groups[groupKey];
      fireEvent.click(groupTrigger(group.groupId));
      await waitFor(() =>
        expect(memberRowIds(group.groupId)).toEqual(
          testCase.expectedMemberIds[groupKey].map(memberUUID),
        ),
      );
      for (const memberId of testCase.expectedMemberIds[groupKey]) {
        const row = memberRow(group.groupId, memberId);
        for (const fact of testCase.expectedMemberFacts[memberId]) {
          expect(row.textContent).toContain(fact);
        }
      }
      expect(calls.some((call) => call.includes(`scope=${group.memberScope}`))).toBe(true);
    }

    // Nothing beyond the declared scopes was requested.
    expect(calls.length).toBe(testCase.expand.length);
    expect(calls.every((call) => call.includes("scope="))).toBe(true);
  });
}
