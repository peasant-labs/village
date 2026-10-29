import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { loadCollectivePageFixtures } from "@/test/collectivePageFixtures";
import { assertNamesMatch } from "@/test/fixtureAssertions";
import { installCollectivePagesTeardown, installCollectiveREST, renderCollectivePage } from "@/test/mountedCollectivePages";

interface PageStep { page: number; first: number; last: number; range: string; query: string }
interface PaginationCase {
  name: string;
  kind: "navigation" | "delayed" | "failure";
  steps?: PageStep[];
  heldPage?: number;
  final?: PageStep;
  target?: PageStep;
  status?: number;
  error?: string;
}

function loadPaginationFixtures() {
  const fixture = parse(readFileSync(resolve("src/testdata/collective-pagination.yaml"), "utf8")) as {
    corpus: { total: number; titlePrefix: string; idPrefix: string };
    cases: PaginationCase[];
  };
  assertNamesMatch(fixture.cases.map((row) => row.name), [
    "numbered-pages-read-bounded-offsets-and-can-return-to-the-first-page",
    "a-delayed-page-never-labels-previous-rows-as-the-new-page",
    "a-failed-page-can-retry-its-own-offset-without-showing-old-rows",
  ], "collective pagination");
  return fixture;
}

installCollectivePagesTeardown();
const fixture = loadPaginationFixtures();
const pages = loadCollectivePageFixtures();

function titles() {
  return within(screen.getByRole("table", { name: /^transcripts/ }))
    .queryAllByRole("link", { name: new RegExp(`^${fixture.corpus.titlePrefix} `) });
}

async function expectPage(step: PageStep) {
  await waitFor(() => expect(titles().map((link) => link.textContent)).toEqual(
    Array.from({ length: step.last - step.first + 1 }, (_, index) => `${fixture.corpus.titlePrefix} ${step.first + index}`),
  ));
  expect(document.querySelector(".on-pager [role=status]")).toHaveTextContent(step.range);
  expect(within(screen.getByRole("navigation", { name: "transcript pages" }))
    .getByRole("button", { name: `page ${step.page}` })).toHaveAttribute("aria-current", "page");
}

for (const row of fixture.cases) {
  it(row.name, async () => {
    const owner = pages.cases.find((candidate) => candidate.role === "owner" && candidate.canRead)!;
    const world = pages.worldFor(owner);
    const seed = world.transcripts[0];
    world.transcripts = Array.from({ length: fixture.corpus.total }, (_, index) => ({
      ...seed,
      id: `${fixture.corpus.idPrefix}${String(index + 1).padStart(12, "0")}`,
      title: `${fixture.corpus.titlePrefix} ${index + 1}`,
    }));
    const reads = installCollectiveREST(world);
    const delegate = globalThis.fetch;
    let release!: () => void;
    const held = new Promise<void>((resolve) => { release = resolve; });
    let failed = false;
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input), "https://village.test");
      const tableRead = url.pathname === `/api/v1/collectives/${world.group.id}` && url.searchParams.has("offset");
      const offset = Number(url.searchParams.get("offset"));
      if (tableRead && row.kind === "failure" && offset === (row.target!.page - 1) * 20 && !failed) {
        failed = true;
        return new Response(JSON.stringify({ error: row.error }), { status: row.status, headers: { "content-type": "application/json" } });
      }
      const response = await delegate(input, init);
      if (tableRead && row.kind === "delayed" && offset === (row.heldPage! - 1) * 20) await held;
      return response;
    }));
    await renderCollectivePage(world);
    const pager = screen.getByRole("navigation", { name: "transcript pages" });
    expect(within(pager).getByRole("button", { name: "previous page" })).toBeDisabled();

    if (row.kind === "navigation") {
      for (const step of row.steps!) {
        fireEvent.click(within(pager).getByRole("button", { name: `page ${step.page}` }));
        await expectPage(step);
        expect(reads.map((request) => request.path)).toContain(`/collectives/${world.group.id}${step.query}`);
        expect(titles()[0]).toHaveAttribute("href", expect.stringContaining(`?in=collective:${world.group.id}`));
      }
      return;
    }
    if (row.kind === "delayed") {
      fireEvent.click(within(pager).getByRole("button", { name: `page ${row.heldPage}` }));
      await waitFor(() => expect(document.querySelector(".on-pager [role=status]")).toHaveTextContent(`loading page ${row.heldPage}`));
      expect(titles()).toEqual([]);
      fireEvent.click(within(pager).getByRole("button", { name: `page ${row.final!.page}` }));
      await expectPage(row.final!);
      await act(async () => release());
      await expectPage(row.final!);
      expect(within(pager).getByRole("button", { name: "next page" })).toBeDisabled();
      return;
    }
    fireEvent.click(within(pager).getByRole("button", { name: `page ${row.target!.page}` }));
    await screen.findByText(new RegExp(row.error!));
    expect(titles()).toEqual([]);
    fireEvent.click(screen.getByRole("button", { name: "retry" }));
    await expectPage(row.target!);
    expect(reads.map((request) => request.path)).toContain(`/collectives/${world.group.id}${row.target!.query}`);
  });
}
