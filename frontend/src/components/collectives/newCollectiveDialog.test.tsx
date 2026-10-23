import { describe, expect, it } from "vitest";
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import {
  assertCaseNames,
  installSurfacesREST,
  installSurfacesTeardown,
  readFixture,
  renderCollectivesPage,
  textOf,
  worldFrom,
  type RecordedRequest,
  type SurfacesWorld,
  type WorldRefusal,
} from "@/test/mountedCollectivesSurfaces";

/**
 * The new-collective dialog on the real `/collectives` route, driven by
 * `src/testdata/new-collective.yaml`: the step flow and the create body, the
 * people added afterwards and how each outcome is reported, the step a
 * refusal returns to, and the checks made before anything is sent.
 */

interface Steps {
  name: string;
  purpose: string;
  publish: string;
  org?: string;
  read: string;
  invite: string[];
}

interface CreateCase {
  name: string;
  why: string;
  steps: Steps;
  refusals: WorldRefusal[];
  expect: {
    create: Record<string, string>;
    memberRequests: string[];
    ready: string;
    summary: string;
    notAdded: string[];
  };
}

interface RefusalCase {
  name: string;
  why: string;
  steps: Steps;
  refusal: { status: number; error: string };
  expect: { step: string; message: string };
}

interface GuardCase {
  name: string;
  why: string;
  step: "name and purpose" | "access" | "invite";
  input?: string;
  already?: string[];
  orgs?: SurfacesWorld["orgs"];
  expect: string;
}

const fixture = readFixture("new-collective.yaml");
const orgs = fixture.orgs as SurfacesWorld["orgs"];
const createCases = fixture.createCases as CreateCase[];
const refusalCases = fixture.refusalCases as RefusalCase[];
const guardCases = fixture.guardCases as GuardCase[];
assertCaseNames("new-collective.yaml createCases", createCases.map((c) => c.name), [
  "a-curated-collective-with-two-people-added",
  "a-refused-invitation-is-reported-and-never-counted",
  "an-org-collective-sends-the-chosen-org-and-nobody-to-add",
]);
assertCaseNames("new-collective.yaml refusalCases", refusalCases.map((c) => c.name), [
  "an-org-refusal-returns-to-the-access-step",
  "a-name-refusal-returns-to-the-name-field",
  "any-other-refusal-stays-on-the-invite-step",
]);
assertCaseNames("new-collective.yaml guardCases", guardCases.map((c) => c.name), [
  "an-empty-name-cannot-continue",
  "you-cannot-add-yourself",
  "a-person-is-listed-once",
  "an-org-collective-needs-a-visible-org",
]);

const CREATED_ID = "c0000000-0000-4000-8000-0000000000aa";
const MEMBERSHIP = [{ id: "1c000000-0000-4000-8000-000000000001", name: "Acme Platform", role: "owner" as const, mode: "open" as const, org: "acme" }];

installSurfacesTeardown();

function dialog(): HTMLElement {
  return screen.getByRole("dialog", { name: "new collective" });
}

/** The step the stepper marks as current. */
function currentStep(): string {
  const current = within(dialog()).getByRole("list", { name: "steps" }).querySelector('[aria-current="step"]');
  return textOf(current).replace(/^\d+\s*/, "");
}

function press(name: string) {
  fireEvent.click(within(dialog()).getByRole("button", { name }));
}

async function openDialog(world: SurfacesWorld): Promise<RecordedRequest[]> {
  const requests = installSurfacesREST(world);
  await renderCollectivesPage();
  fireEvent.click(await screen.findByRole("button", { name: "new collective" }));
  await screen.findByRole("dialog", { name: "new collective" });
  return requests;
}

function fillName(steps: Pick<Steps, "name" | "purpose">) {
  fireEvent.change(within(dialog()).getByRole("textbox", { name: "name" }), { target: { value: steps.name } });
  fireEvent.change(within(dialog()).getByRole("textbox", { name: "purpose" }), { target: { value: steps.purpose } });
}

async function fillAccess(steps: Pick<Steps, "publish" | "org" | "read">) {
  const publish = within(dialog()).getByRole("radiogroup", { name: "who can publish" });
  fireEvent.click(within(publish).getByRole("radio", { name: steps.publish }));
  if (steps.org) {
    const picker = await within(dialog()).findByRole("combobox", { name: "which github org" });
    fireEvent.change(picker, { target: { value: steps.org } });
  }
  const read = within(dialog()).getByRole("radiogroup", { name: "who can read" });
  fireEvent.click(within(read).getByRole("radio", { name: steps.read }));
}

function addPeople(usernames: readonly string[]) {
  for (const username of usernames) {
    fireEvent.change(within(dialog()).getByRole("textbox", { name: "invite by github username" }), {
      target: { value: username },
    });
    press("add");
  }
}

/** Walks the three steps and presses create. */
async function walk(steps: Steps) {
  fillName(steps);
  press("next");
  await waitFor(() => expect(currentStep()).toBe("access"));
  await fillAccess(steps);
  press("next");
  await waitFor(() => expect(currentStep()).toBe("invite"));
  addPeople(steps.invite);
  await act(async () => {
    press("create collective");
  });
}

describe("creating a collective", () => {
  it.each(createCases.map((c) => [c.name, c] as const))("%s", async (_name, c) => {
    const requests = await openDialog(
      worldFrom({ orgs, visible: MEMBERSHIP, refusals: c.refusals, createdId: CREATED_ID }),
    );
    await walk(c.steps);

    const done = await within(dialog()).findByRole("heading", { name: c.expect.ready });
    expect(done).toBeInTheDocument();

    const creates = requests.filter((r) => r.method === "POST" && r.path === "/collectives");
    expect(creates, c.why).toHaveLength(1);
    expect(creates[0].body).toEqual(c.expect.create);

    // Every person is added after the collective exists, in the order listed.
    const adds = requests.filter((r) => r.method === "POST" && r.path === `/collectives/${CREATED_ID}/members`);
    expect(adds.map((r) => (r.body as { username: string }).username)).toEqual(c.expect.memberRequests);
    expect(requests.indexOf(creates[0])).toBeLessThan(adds.length ? requests.indexOf(adds[0]) : Infinity);

    expect(within(dialog()).getByRole("status")).toHaveTextContent(c.expect.summary);
    const notAdded = within(dialog()).queryByRole("list", { name: "people not added" });
    expect(notAdded ? within(notAdded).getAllByRole("listitem").map(textOf) : []).toEqual(c.expect.notAdded);

    expect(within(dialog()).getByRole("link", { name: `open ${c.steps.name.trim()}` })).toHaveAttribute(
      "href",
      `/collectives/${CREATED_ID}`,
    );
    expect(within(dialog()).getByRole("link", { name: "invite more people" })).toHaveAttribute(
      "href",
      `/collectives/${CREATED_ID}/settings?section=members`,
    );
  });
});

describe("a refused create", () => {
  it.each(refusalCases.map((c) => [c.name, c] as const))("%s", async (_name, c) => {
    const requests = await openDialog(
      worldFrom({
        orgs,
        visible: MEMBERSHIP,
        refusals: [{ method: "POST", path: "/collectives", ...c.refusal }],
      }),
    );
    await walk(c.steps);

    await waitFor(() => expect(currentStep(), c.why).toBe(c.expect.step));
    expect(within(dialog()).getByText(c.expect.message, { exact: false })).toBeInTheDocument();
    if (c.expect.step === "name and purpose") {
      const name = within(dialog()).getByRole("textbox", { name: "name" });
      expect(name).toHaveAttribute("aria-invalid", "true");
      expect(name).toHaveAccessibleDescription(c.expect.message);
      expect(name).toHaveValue(c.steps.name);
    }
    // Nobody is added to a collective that does not exist.
    expect(requests.some((r) => r.method === "POST" && r.path.endsWith("/members"))).toBe(false);
    expect(within(dialog()).queryByRole("heading", { name: /is ready$/ })).not.toBeInTheDocument();
  });
});

describe("the dialog's own checks", () => {
  it.each(guardCases.map((c) => [c.name, c] as const))("%s", async (_name, c) => {
    const requests = await openDialog(worldFrom({ orgs: c.orgs ?? orgs, visible: MEMBERSHIP }));
    if (c.step === "name and purpose") {
      fillName({ name: c.input ?? "", purpose: "" });
      press("next");
      const name = within(dialog()).getByRole("textbox", { name: "name" });
      await waitFor(() => expect(name).toHaveAccessibleDescription(c.expect));
      expect(currentStep()).toBe("name and purpose");
    } else {
      fillName({ name: "Night Shift", purpose: "" });
      press("next");
      await waitFor(() => expect(currentStep()).toBe("access"));
      if (c.step === "access") {
        const publish = within(dialog()).getByRole("radiogroup", { name: "who can publish" });
        fireEvent.click(within(publish).getByRole("radio", { name: "your github org" }));
        expect(within(dialog()).getByText(/No GitHub org is linked to your profile/)).toBeInTheDocument();
        expect(within(dialog()).queryByRole("combobox")).not.toBeInTheDocument();
        press("next");
        expect(await within(dialog()).findByRole("alert")).toHaveTextContent(c.expect);
        expect(currentStep()).toBe("access");
      } else {
        press("next");
        await waitFor(() => expect(currentStep()).toBe("invite"));
        addPeople([...(c.already ?? []), c.input ?? ""]);
        const field = within(dialog()).getByRole("textbox", { name: "invite by github username" });
        expect(field).toHaveAccessibleDescription(c.expect);
        const listed = within(dialog()).queryByRole("list", { name: "people to add" });
        expect(listed ? within(listed).getAllByRole("listitem").map(textOf) : []).toEqual(
          (c.already ?? []).map((u) => `@${u}`),
        );
      }
    }
    expect(requests.some((r) => r.method === "POST")).toBe(false);
  });

  it("removes a person from the list before anything is sent", async () => {
    await openDialog(worldFrom({ orgs, visible: MEMBERSHIP }));
    fillName({ name: "Night Shift", purpose: "" });
    press("next");
    await waitFor(() => expect(currentStep()).toBe("access"));
    press("next");
    await waitFor(() => expect(currentStep()).toBe("invite"));
    addPeople(["carol-ml", "dan-ops"]);
    press("remove carol-ml");
    const listed = within(dialog()).getByRole("list", { name: "people to add" });
    expect(within(listed).getAllByRole("listitem").map(textOf)).toEqual(["@dan-ops"]);
  });

  it("goes back without losing what was typed, and cancel closes it", async () => {
    await openDialog(worldFrom({ orgs, visible: MEMBERSHIP }));
    fillName({ name: "Night Shift", purpose: "Late reviews." });
    press("next");
    await waitFor(() => expect(currentStep()).toBe("access"));
    press("back");
    await waitFor(() => expect(currentStep()).toBe("name and purpose"));
    expect(within(dialog()).getByRole("textbox", { name: "name" })).toHaveValue("Night Shift");
    expect(within(dialog()).getByRole("textbox", { name: "purpose" })).toHaveValue("Late reviews.");
    press("cancel");
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });
});
