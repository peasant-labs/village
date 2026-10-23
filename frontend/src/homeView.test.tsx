import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import {
  installHomeRouteREST,
  installHomeRouteTeardown,
  renderAppRoute,
} from "@/test/mountedHomeRoute";
import { loadHomeViewFixtures, type HomeViewStep } from "@/test/homeViewFixtures";
import { HOME_COLUMNS, HOME_VIEW_STORAGE_KEY, type HomeView } from "@/lib/homeView";

// Mounts the REAL root route for a signed-in person and drives the table's own
// toolbar: group by, the column picker, the projects filter, and the group
// rows. Assertions read what is drawn and what this browser remembers, never
// component state, and every case also checks that arranging the table asked
// the server nothing new.

const fixtures = loadHomeViewFixtures();

installHomeRouteTeardown();

function text(el: Element | null | undefined): string {
  return (el?.textContent ?? "").replace(/\s+/g, " ").trim();
}

function groupRows(): string[] {
  return [...document.querySelectorAll('[data-testid="home-table"] tr.is-group button')].map(text);
}

function rowTitles(): string[] {
  return [...document.querySelectorAll('[data-testid="home-table"] [data-testid="home-transcript-row"] > a')].map(text);
}

function columnHeaders(): string[] {
  return [...document.querySelectorAll('[data-testid="home-table"] thead th')].map(text);
}

function storedView(): { groupBy: string; hiddenColumns: string[]; openGroups: number } | null {
  const raw = localStorage.getItem(HOME_VIEW_STORAGE_KEY);
  if (raw == null) return null;
  try {
    const view = JSON.parse(raw) as HomeView;
    return {
      groupBy: view.groupBy,
      hiddenColumns: HOME_COLUMNS.filter((key) => !view.columns[key]),
      openGroups: view.open.length,
    };
  } catch {
    return null;
  }
}

const MENU_TESTIDS: Record<string, string> = {
  projects: "home-projects",
  "group by": "home-group-by",
  columns: "home-columns",
};

async function perform(step: HomeViewStep): Promise<void> {
  await act(async () => {
    if ("open" in step) {
      fireEvent.click(within(screen.getByTestId(MENU_TESTIDS[step.open])).getAllByRole("button")[0]);
    } else if ("pick" in step) {
      fireEvent.click(screen.getByRole("radio", { name: step.pick }));
    } else if ("toggle" in step) {
      fireEvent.click(screen.getByRole("checkbox", { name: new RegExp(`^${step.toggle}`) }));
    } else {
      fireEvent.click(screen.getByRole("button", { name: step.press }));
    }
  });
}

describe("the home table, arranged in this browser", () => {
  for (const c of fixtures.cases) {
    it(c.name, async () => {
      if (c.stored !== null) {
        localStorage.setItem(HOME_VIEW_STORAGE_KEY, typeof c.stored === "string" ? c.stored : JSON.stringify(c.stored));
      }
      const backend = installHomeRouteREST({ viewerUsername: "alice-dev", transcripts: fixtures.rows });
      await renderAppRoute("/");
      await screen.findByTestId("home-transcripts");
      const asked = backend.requested.length;

      for (const step of c.steps) await perform(step);

      await waitFor(() => expect(groupRows(), c.why).toEqual(c.expect.groups));
      expect(rowTitles(), c.why).toEqual(c.expect.rows);
      expect(columnHeaders(), c.why).toEqual(c.expect.columns);
      if (c.expect.columns.length === 0) {
        // Nothing drawn because of the filter, said as such, with the way back.
        const none = screen.getByTestId("home-no-project-match");
        expect(text(none)).toContain("no transcripts match these projects");
        expect(within(none).getByRole("button", { name: "show all projects" })).toBeInTheDocument();
      }
      expect(storedView(), c.why).toEqual(c.expect.stored);
      // Every group row says whether it is open.
      for (const button of document.querySelectorAll('[data-testid="home-table"] tr.is-group button')) {
        expect(["true", "false"]).toContain(button.getAttribute("aria-expanded"));
      }
      // Arranging the table never asks the server anything.
      expect(backend.requested.length, "arranging the view sent no request").toBe(asked);
    });
  }
});
