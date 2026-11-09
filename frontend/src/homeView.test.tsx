import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import {
  installHomeRouteREST,
  installHomeRouteTeardown,
  renderAppRoute,
} from "@/test/mountedHomeRoute";
import { loadHomeViewFixtures, type HomeViewStep } from "@/test/homeViewFixtures";
import {
  ALL_VILLAGE_COLUMNS,
  HOME_VIEW_STORAGE_KEY,
  LEGACY_HOME_VIEW_STORAGE_KEY,
  type HomeView,
} from "@/lib/homeView";

// Mounts the REAL root route for a signed-in person and drives the SHARED
// table's own chrome: the grouping picker, the column picker, the projects
// filter, and the group rows. Assertions read what is drawn, what this browser
// remembers, and what the URL carries — never component state — and every case
// also checks that arranging the table asked the server nothing new.

const fixtures = loadHomeViewFixtures();

installHomeRouteTeardown();

function text(el: Element | null | undefined): string {
  return (el?.textContent ?? "").replace(/\s+/g, " ").trim();
}

function groupLabels(): string[] {
  return [
    ...document.querySelectorAll(
      '[data-testid="home-transcripts"] tr[data-row-kind="group"] .libt-group-label',
    ),
  ].map(text);
}

function rowTitles(): string[] {
  return [
    ...document.querySelectorAll(
      '[data-testid="home-transcripts"] tr[data-row-kind="row"] [data-testid="home-transcript-row"] > a',
    ),
  ].map(text);
}

function columnHeaders(): string[] {
  return [...document.querySelectorAll('[data-testid="home-transcripts"] thead th')].map(text);
}

/** The device half this browser remembers: the visible columns and how many
 *  groups are closed. */
function storedView(): { hiddenColumns: string[]; collapsedGroups: number } | null {
  const raw = localStorage.getItem(HOME_VIEW_STORAGE_KEY);
  if (raw == null) return null;
  try {
    const view = JSON.parse(raw) as HomeView;
    const visible = view.columns.map((column) =>
      typeof column === "string" ? column : column.wire,
    );
    return {
      hiddenColumns: ALL_VILLAGE_COLUMNS.filter((column) => !visible.includes(column.wire)).map(
        (column) => column.wire,
      ),
      collapsedGroups: view.collapsed.length,
    };
  } catch {
    return null;
  }
}

async function perform(step: HomeViewStep): Promise<void> {
  // One press per act: opening a picker changes the DOM the next query reads,
  // and React flushes between the two only outside a single act block.
  if ("groupBy" in step) {
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: /^group by: / }));
    });
    await act(async () => {
      fireEvent.click(screen.getByRole("radio", { name: step.groupBy }));
    });
    return;
  }
  if ("column" in step) {
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: /^columns: / }));
    });
    await act(async () => {
      fireEvent.click(screen.getByRole("checkbox", { name: step.column }));
    });
    return;
  }
  if ("project" in step) {
    await act(async () => {
      fireEvent.click(within(screen.getByTestId("home-projects")).getAllByRole("button")[0]);
    });
    await act(async () => {
      if (step.project === "clear") {
        fireEvent.click(screen.getByRole("button", { name: "clear" }));
      } else {
        fireEvent.click(screen.getByRole("checkbox", { name: new RegExp(`^${step.project}`) }));
      }
    });
    return;
  }
  await act(async () => {
    fireEvent.click(screen.getByRole("button", { name: step.press }));
  });
}

describe("the Library, arranged in this browser", () => {
  for (const c of fixtures.cases) {
    it(c.name, async () => {
      expect(window.location.search, "each case mounts at its own address").toBe("");
      if (c.stored !== null) {
        const key = c.legacy === true ? LEGACY_HOME_VIEW_STORAGE_KEY : HOME_VIEW_STORAGE_KEY;
        localStorage.setItem(key, typeof c.stored === "string" ? c.stored : JSON.stringify(c.stored));
      }
      const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
      const backend = installHomeRouteREST({ viewerUsername: "alice-dev", transcripts: fixtures.rows });
      await renderAppRoute(c.url === undefined ? "/" : `/?${c.url}`);
      await screen.findByTestId("home-transcripts");
      const asked = backend.requested.length;

      for (const step of c.steps) await perform(step);

      await waitFor(() => expect(groupLabels(), c.why).toEqual(c.expect.groups));
      expect(rowTitles(), c.why).toEqual(c.expect.rows);
      expect(columnHeaders(), c.why).toEqual(c.expect.columns);
      if (c.expect.columns.length === 0) {
        // Nothing drawn because of the filter, said as such, with the way back.
        const none = screen.getByTestId("home-no-project-match");
        expect(text(none)).toContain("no transcripts match these projects");
        expect(within(none).getByRole("button", { name: "show all projects" })).toBeInTheDocument();
      }
      expect(storedView(), c.why).toEqual(c.expect.stored);
      if (c.expect.urlGroupBy !== undefined) {
        expect(new URL(window.location.href).searchParams.get("groupBy"), c.why).toBe(
          c.expect.urlGroupBy,
        );
      }
      // Every drawn group says whether it is open.
      for (const button of document.querySelectorAll(
        '[data-testid="home-transcripts"] .libt-group-toggle',
      )) {
        expect(["true", "false"]).toContain(button.getAttribute("aria-expanded"));
      }
      // Arranging the table never asks the server anything.
      expect(backend.requested.length, "arranging the view sent no request").toBe(asked);
      warn.mockRestore();
    });
  }
});
