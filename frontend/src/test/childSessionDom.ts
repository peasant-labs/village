import { act, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect } from "vitest";
import type { ChildSessionGroupingCase } from "@/test/childSessionGroupingFixtures";

/**
 * The DOM readers every mounted child-session assertion shares.
 *
 * Nine real routes now fold a session that another session started under the
 * session that started it, and they do NOT agree on how a row is drawn: a
 * transcript list, a review queue, a person's own contributions and a selection
 * tree each have their own row. What they DO agree on is the marker every one
 * of them puts on the control: the wrapper carries
 * `data-parent-transcript-id="<the row it hangs under>"` and the control itself
 * carries the `child-session-disclosure` test ids. Reading through those
 * markers here means one reader serves every surface, and a surface that
 * invented its own control would fail rather than quietly assert nothing.
 */

/** Every transcript id the given root currently links to, in document order. */
export function linkedIDs(root: ParentNode = document): string[] {
  return [...root.querySelectorAll<HTMLAnchorElement>('a[href^="/transcripts/"]')].map((anchor) =>
    anchor.getAttribute("href")!.replace("/transcripts/", ""),
  );
}

/** The expandable control hanging under one row. */
export function chipFor(parentID: string): HTMLElement {
  const chip = document.querySelector<HTMLElement>(`[data-parent-transcript-id="${parentID}"]`);
  if (chip == null) throw new Error(`no chip of started sessions is rendered under ${parentID}`);
  return chip;
}

/** Ids of every control on screen, so a surface can be held to exactly the
 *  controls its case expects and no others -- an EXTRA control is a failure
 *  just as a missing one is. */
export function chippedParentIDs(): string[] {
  return [...document.querySelectorAll<HTMLElement>("[data-parent-transcript-id]")]
    .map((chip) => chip.getAttribute("data-parent-transcript-id")!)
    .sort();
}

/** Lets React Query settle its fetches and re-renders. */
export async function flush(): Promise<void> {
  await act(async () => {
    for (let i = 0; i < 4; i += 1) {
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
  });
}

/** The collapsed control under one row, with its own label element. */
export function disclosureFor(parentID: string): {
  chip: HTMLElement;
  toggle: HTMLElement;
  label: HTMLElement;
} {
  const chip = chipFor(parentID);
  return {
    chip,
    toggle: within(chip).getByTestId("child-session-disclosure-toggle"),
    label: within(chip).getByTestId("child-session-disclosure-label"),
  };
}

/** Opens one collapsed control and answers with the element it revealed. */
export async function expandDisclosure(toggle: HTMLElement, chip: HTMLElement): Promise<HTMLElement> {
  await act(async () => {
    await userEvent.click(toggle);
  });
  await flush();
  const rows = within(chip).getByTestId("child-session-disclosure-rows");
  // The control names its own rows for assistive technology; without this a
  // page carrying several controls could point every one of them at the same
  // element and a screen reader would open the wrong group.
  expect(toggle.getAttribute("aria-controls")).toBe(rows.getAttribute("id"));
  return rows;
}

/**
 * The controls a case expects on a surface showing `visibleRows`, walked in
 * order: exactly those controls and no others, each collapsed with the case's
 * own literal label, each revealing exactly the rows the case names.
 *
 * `readRowIDs` reads the row identities out of one region of the surface,
 * because the lists do not agree on what a row is: a transcript list draws an
 * anchor per row, a selection tree draws a checkbox with a test id. Passing the
 * reader in keeps ONE walk over the expectations rather than one per surface.
 */
export async function assertDisclosures(
  testCase: ChildSessionGroupingCase,
  listRoot: HTMLElement,
  visibleRows: string[],
  readRowIDs: (root: ParentNode) => string[],
): Promise<void> {
  // Before any control is opened, the rows on screen are exactly the case's
  // own, in order. This pins the ORDER as well as the set, and fails on an
  // extra row as loudly as on a missing one.
  expect(readRowIDs(listRoot), `${testCase.name}: the rows on screen, in order`).toEqual(visibleRows);

  const shownGroups = testCase.expectedGroups.filter((group) => visibleRows.includes(group.parent));
  expect(chippedParentIDs(), `${testCase.name}: the controls on screen`).toEqual(
    shownGroups.map((group) => group.parent).sort(),
  );

  for (const expectedGroup of shownGroups) {
    const { chip, toggle, label } = disclosureFor(expectedGroup.parent);

    // The EXACT text, not a substring of it. This control announces a bare
    // count and the agent group beside it announces a leading `+`; a
    // containment check would pass on either, so it could not tell the two
    // apart and would not notice the `+` coming back.
    expect(
      label.textContent,
      `${testCase.name}: the collapsed label under ${expectedGroup.parent}`,
    ).toBe(expectedGroup.label);
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    expect(within(chip).queryByTestId("child-session-disclosure-rows")).toBeNull();

    const rows = await expandDisclosure(toggle, chip);
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    // Opening it does not reword it: the count a viewer decided to open is the
    // count they still see.
    expect(
      label.textContent,
      `${testCase.name}: the label under ${expectedGroup.parent} once it is open`,
    ).toBe(expectedGroup.label);
    expect(
      readRowIDs(rows).sort(),
      `${testCase.name}: the rows revealed under ${expectedGroup.parent}`,
    ).toEqual([...expectedGroup.children].sort());
  }

  // With every control open, the surface shows exactly its rows and the
  // sessions they started, each once: nothing duplicated, and nothing on screen
  // the case did not put there.
  const reachable = readRowIDs(listRoot);
  const wanted = [...visibleRows, ...shownGroups.flatMap((group) => group.children)];
  expect(
    [...reachable].sort(),
    `${testCase.name}: everything reachable with every control open`,
  ).toEqual([...wanted].sort());
}
