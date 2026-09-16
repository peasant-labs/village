import { act, cleanup, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { installGroupRouteREST, renderGroupDetailRoute } from "@/test/mountedGroupRoute";
import {
  flatMyShare,
  groupedMySharesFirstPage,
  groupedMySharesPage,
  groupedMyShareOwner,
  loadCollectiveMyShareFixtures,
  memberUUIDFor,
  myShareContextContainer,
  myShareMemberPage,
  myShareRow,
  type MyShareCase,
} from "@/test/collectiveGroupedMySharesFixtures";

/**
 * Mounted evidence for the grouped my-shares panel on the REAL collective page,
 * driven by `src/testdata/collective-grouped-my-shares.yaml`.
 *
 * The REAL `/groups/{id}` route renders through the REAL grouped query hook;
 * only HTTP is controlled. The panel reads the grouped variant of the same
 * `my-shares` route as a supplement to the flat contributions it already read,
 * so what is proved here is:
 *
 *  • a saved helper group the server grouped under one of the caller's own
 *    contributions renders beneath that contribution, expanding to individually
 *    linked members, while the contribution keeps its own state -- its link, its
 *    pending badge, its unshare control and the panel's count;
 *  • a helper whose starter was not shared with this collective has no owner row
 *    and mounts on the grouped exit instead of disappearing;
 *  • a grouped read that carries nothing mounts no panel at all;
 *  • a group on a later grouped page is unreachable until the continuation
 *    reads it, and mounts once when it does.
 */

const fixtures = loadCollectiveMyShareFixtures();
const GROUP_ID = fixtures.groupId;
/** The signed-in contributor the panel requires. */
const VIEWER = "ada";
/** The member page size the group disclosure requests. */
const MEMBER_PAGE_SIZE = 20;

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  globalThis.localStorage?.clear();
});

/** Every group's member page, keyed by that group's own opaque scope. */
const helperMembers = {
  [fixtures.groups.owner.memberScope]: myShareMemberPage(fixtures, "owner", MEMBER_PAGE_SIZE),
  [fixtures.groups.context.memberScope]: myShareMemberPage(fixtures, "context", MEMBER_PAGE_SIZE),
};

/** The flat contributions a case's own read serves. */
function flatRows(testCase: MyShareCase) {
  return testCase.flat === "rendered" ? [flatMyShare(fixtures, testCase.row)] : [];
}

/** The grouped items a case's own grouped page serves. */
function groupedItems(testCase: MyShareCase) {
  if (testCase.grouped === "owner") {
    return [groupedMyShareOwner(fixtures, testCase.row, [fixtures.groups.owner])];
  }
  if (testCase.grouped === "context") return [myShareContextContainer(fixtures)];
  return [];
}

/** The panel's own contribution row, found by the wrapper its group hangs in. */
function contributionRow(transcriptID: string): HTMLElement {
  const row = document.querySelector<HTMLElement>(`[data-helper-group-owner="${transcriptID}"]`);
  if (row == null) throw new Error(`the panel drew no contribution row for ${transcriptID}`);
  return row;
}

async function linkedMember(transcriptID: string): Promise<HTMLElement> {
  return waitFor(() => {
    const link = document.querySelector<HTMLElement>(
      `a.helper-thread-open[href="/transcripts/${transcriptID}"]`,
    );
    expect(link).not.toBeNull();
    return link!;
  });
}

for (const testCase of fixtures.cases) {
  it(testCase.name, async () => {
    installGroupRouteREST({
      viewer: VIEWER,
      groupId: GROUP_ID,
      groupName: "commons",
      role: "member",
      transcripts: [],
      myShares: flatRows(testCase),
      groupedMyShares: (page) => groupedMySharesPage(groupedItems(testCase), page, groupedItems(testCase).length),
      helperMembers,
    });

    await act(async () => {
      await renderGroupDetailRoute(GROUP_ID);
    });

    if (testCase.grouped === "empty") {
      // Neither read carries a contribution, so the panel must not be mounted at
      // all -- no header, no grouped exit.
      await waitFor(() => expect(screen.queryByTestId("my-contributions-panel")).toBeNull());
      expect(document.querySelector('[data-testid="grouped-helper-fallback"]')).toBeNull();
      return;
    }

    const row = myShareRow(fixtures, testCase.row);
    const panel = await screen.findByTestId("my-contributions-panel");
    // The panel still counts the flat contributions it always counted.
    expect(panel.textContent).toContain("Your contributions");
    expect(panel.textContent).toContain(String(flatRows(testCase).length));

    if (testCase.grouped === "context") {
      // A helper whose starter was not shared has no owner row: its group
      // mounts on the grouped exit, outside the flat branch, and states the
      // authorized owner status rather than fabricating a row.
      const fallback = await waitFor(() => {
        const found = document.querySelector<HTMLElement>('[data-testid="grouped-helper-fallback"]');
        expect(found).not.toBeNull();
        return found!;
      });
      expect(within(fallback).getByText(/owner is unavailable/i)).toBeInTheDocument();
      const group = fallback.querySelector<HTMLElement>(
        `.helper-group[data-group-id="${fixtures.groups.context.groupId}"]`,
      );
      expect(group, "the context container's saved helper group mounts").not.toBeNull();
      // The flat contribution this helper belongs to is still its own unshareable
      // row: the grouped exit adds the disclosure, it does not replace the row.
      const flatRowLink = document.querySelector(
        `a[href="/transcripts/${row.id}"]`,
      );
      expect(flatRowLink, "the flat contribution keeps its own row").not.toBeNull();

      act(() => {
        fireEvent.click(group!.querySelector<HTMLButtonElement>("button.helper-group-trigger")!);
      });
      await linkedMember(memberUUIDFor(fixtures.members.context[0].name));
      return;
    }

    // The grouped owner row the flat contribution already draws keeps its own
    // state: the link to the transcript, its pending badge, its shared date and
    // its unshare control, and the panel's count above it.
    expect(
      document.querySelector('[data-testid="grouped-helper-fallback"]'),
      "an already-drawn owner is not mounted a second time",
    ).toBeNull();
    const contribution = await waitFor(() => contributionRow(row.id));
    expect(
      contribution.querySelector(`a[href="/transcripts/${row.id}"]`),
      "the contribution still links to its own transcript",
    ).not.toBeNull();
    if (row.status === "pending") {
      expect(within(contribution).getByText("pending")).toBeInTheDocument();
    }
    expect(
      within(contribution).getByTitle("Unshare from this collective"),
      "the contribution still carries its unshare control",
    ).toBeInTheDocument();

    // The saved helper group hangs under that very contribution row.
    const group = contribution.querySelector<HTMLElement>(
      `.helper-group[data-group-id="${fixtures.groups.owner.groupId}"]`,
    );
    expect(group, "the saved helper group mounts under its contribution").not.toBeNull();

    // Expanding it reaches each member as its own link.
    act(() => {
      fireEvent.click(group!.querySelector<HTMLButtonElement>("button.helper-group-trigger")!);
    });
    for (const member of fixtures.members.owner) {
      expect((await linkedMember(memberUUIDFor(member.name))).textContent).toContain(member.title);
    }
  });
}

for (const testCase of fixtures.continuationCases) {
  it(testCase.name, async () => {
    const row = myShareRow(fixtures, testCase.laterRow);
    const grouped = (page: number) =>
      page === 1
        ? groupedMySharesPage(groupedMySharesFirstPage(testCase.firstPageItems), 1, testCase.totalItems)
        : groupedMySharesPage(
            [groupedMyShareOwner(fixtures, testCase.laterRow, [fixtures.groups.owner])],
            page,
            testCase.totalItems,
          );
    const requests = installGroupRouteREST({
      viewer: VIEWER,
      groupId: GROUP_ID,
      groupName: "commons",
      role: "member",
      transcripts: [],
      myShares: [flatMyShare(fixtures, testCase.laterRow)],
      groupedMyShares: grouped,
      helperMembers,
    });

    await act(async () => {
      await renderGroupDetailRoute(GROUP_ID);
    });

    // Page one is read at the panel's own page size, and the later group is
    // unreachable until the continuation asks for page two.
    const groupedCalls = () => requests.filter((request) => request.url.includes("/my-shares?"));
    await waitFor(() => expect(groupedCalls()).toHaveLength(1));
    const first = new URL(groupedCalls()[0].url);
    expect(first.searchParams.get("page")).toBe("1");
    expect(first.searchParams.get("limit")).toBe(String(testCase.pageSize));
    expect(first.searchParams.get("view")).toBe("grouped");
    expect(document.querySelector(`[data-helper-group-owner="${row.id}"]`)).toBeNull();

    const remaining = testCase.totalItems - testCase.firstPageItems;
    const continuation = await screen.findByTestId("grouped-helper-continuation");
    expect(continuation.textContent).toContain(
      `${remaining} more grouped ${remaining === 1 ? "result" : "results"}`,
    );

    act(() => {
      fireEvent.click(screen.getByRole("button", { name: "load more" }));
    });
    await waitFor(() => expect(groupedCalls()).toHaveLength(2));
    const second = new URL(groupedCalls()[1].url);
    expect(second.searchParams.get("page")).toBe("2");
    expect(second.searchParams.get("limit")).toBe(String(testCase.pageSize));

    // The later contribution's group mounts under its own row, and the
    // continuation is gone once the total is exhausted.
    const contribution = await waitFor(() => contributionRow(row.id));
    expect(
      contribution.querySelector(`.helper-group[data-group-id="${fixtures.groups.owner.groupId}"]`),
    ).not.toBeNull();
    await waitFor(() => expect(screen.queryByTestId("grouped-helper-continuation")).toBeNull());

    act(() => {
      fireEvent.click(
        contribution.querySelector<HTMLButtonElement>("button.helper-group-trigger")!,
      );
    });
    for (const member of fixtures.members.owner) {
      await linkedMember(memberUUIDFor(member.name));
    }
  });
}
