import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { loadRepoPickerFixtures, type PickerStep } from "@/test/collectivePageFixtures";
import {
  installCollectivePagesTeardown,
  installCollectiveREST,
  renderCollectivePage,
  textOf,
  type RecordedRequest,
} from "@/test/mountedCollectivePages";

/**
 * The repo picker opened from the collective page's `github orgs` rail, through
 * the real route and fairtrade's real `RepoPicker`, driven by
 * `src/testdata/repo-picker.yaml`. Each case asserts the calls the save made,
 * in order, and the message the owner is shown.
 */

installCollectivePagesTeardown();

const fixtures = loadRepoPickerFixtures();

/** A repository's checkbox, found by the name its row shows. */
function checkboxFor(dialog: HTMLElement, repo: string): HTMLInputElement {
  const row = [...dialog.querySelectorAll(".rpk-repo-row")].find(
    (label) => textOf(label.querySelector(".rpk-repo-name")) === repo,
  );
  const box = row?.querySelector<HTMLInputElement>('input[type="checkbox"]');
  if (!box) throw new Error(`the picker shows no repository named ${repo}`);
  return box;
}

function perform(dialog: HTMLElement, step: PickerStep): void {
  if ("tick" in step || "untick" in step) {
    const repo = "tick" in step ? step.tick : step.untick;
    const box = checkboxFor(dialog, repo);
    if (box.checked === "tick" in step) throw new Error(`${repo} is already ${box.checked ? "ticked" : "unticked"}`);
    fireEvent.click(box);
  } else if ("selectAll" in step) {
    fireEvent.click(within(dialog).getByRole("button", { name: "select all" }));
  } else if ("clear" in step) {
    fireEvent.click(within(dialog).getByRole("button", { name: "clear" }));
  } else {
    fireEvent.change(within(dialog).getByRole("searchbox", { name: "search repos" }), { target: { value: step.search } });
  }
}

/** The link and unlink calls, as `{ method, repo }`, in the order they were sent. */
function repositoryWrites(requests: RecordedRequest[]): { method: string; repo: string }[] {
  return requests
    .filter((r) => r.method !== "GET" && r.path.includes("/repositories"))
    .map((r) => {
      if (r.method === "POST") {
        const body = r.body as { owner: string; name: string };
        return { method: r.method, repo: `${body.owner}/${body.name}` };
      }
      const [owner, name] = r.path.split("/repositories/")[1].split("/").map(decodeURIComponent);
      return { method: r.method, repo: `${owner}/${name}` };
    });
}

function orgLine(): string {
  const rail = screen.getByRole("complementary", { name: "github orgs and members" });
  const line = rail.querySelector(".cmg-rail-box .cmg-rail-line")?.cloneNode(true) as Element | undefined;
  line?.querySelectorAll("button").forEach((button) => button.remove());
  return textOf(line);
}

describe("the repo picker", () => {
  it.each(fixtures.cases.map((c) => [c.name, c] as const))("%s", async (_name, pickerCase) => {
    const world = fixtures.worldFor(pickerCase);
    const requests = installCollectiveREST(world);
    await renderCollectivePage(world);

    fireEvent.click(await screen.findByRole("button", { name: "manage" }));
    const dialog = await screen.findByRole("dialog", { name: "link repositories from acme" });
    for (const step of pickerCase.steps) perform(dialog, step);
    fireEvent.click(within(dialog).getByRole("button", { name: /^save/ }));

    const notice = await screen.findByTestId("collective-notice");
    await waitFor(() => expect(textOf(notice.querySelector(".fb-toast-title"))).toBe(pickerCase.expect.title));
    expect(textOf(notice.querySelector(".fb-toast-msg")), pickerCase.why).toBe(pickerCase.expect.message);
    expect(repositoryWrites(requests)).toEqual(pickerCase.expect.calls);
    await waitFor(() => expect(orgLine()).toBe(pickerCase.expect.orgLine));
  });
});
