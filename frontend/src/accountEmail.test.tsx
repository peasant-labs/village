import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { installSettingsRouteREST, installSettingsRouteTeardown, renderSettingsRoute, type MountedSettingsEmail } from "@/test/mountedSettingsRoute";
import { loadAccountSettingsFixtures } from "@/test/accountSettingsFixtures";
import { assertCaseNames, readFixture } from "@/test/mountedCollectivesSurfaces";

vi.mock("next/navigation", () => ({ useRouter: () => ({ push: vi.fn(), replace: vi.fn() }), usePathname: () => "/settings" }));

/**
 * The email section of the real `/settings` route, driven by
 * `src/testdata/account-email.yaml`: adding an address, verifying it by code
 * or by the email's link, the answers of a Village that sends no email and of
 * the verification limit, removing it, and the invitation email switch. Every
 * write is asserted with its exact body.
 */

type Step =
  | { kind: "type"; field: string; value: string }
  | { kind: "press"; button: string }
  | { kind: "toggle"; switch: string };

interface EmailCase {
  name: string;
  why: string;
  server: MountedSettingsEmail;
  search: string;
  steps: Step[];
  expect: { writes: string[]; shows: string[]; alert: string | null; form: boolean };
}

const cases = readFixture("account-email.yaml").cases as EmailCase[];
assertCaseNames("account-email.yaml cases", cases.map((c) => c.name), [
  "an-address-is-added-and-a-code-sent",
  "a-code-verifies-the-address",
  "a-wrong-code-says-so-without-saying-why",
  "the-email-link-verifies-on-arrival",
  "a-village-without-email-says-so",
  "too-many-codes-says-so",
  "removing-the-address",
  "the-invitation-email-switch-sends-only-its-field",
]);

installSettingsRouteTeardown();
const profile = loadAccountSettingsFixtures().cases[0];

describe("the email settings", () => {
  it.each(cases.map((c) => [c.name, c] as const))("%s", async (_name, c) => {
    window.history.replaceState(null, "", `/settings${c.search}`);
    const backend = installSettingsRouteREST(profile, undefined, undefined, undefined, { ...c.server });
    await renderSettingsRoute();
    const section = await screen.findByTestId("settings-email");
    await waitFor(() => expect(within(section).queryByText("checking your email settings")).toBeNull());

    for (const form of section.querySelectorAll("form")) {
      expect(form.querySelector("input")).toHaveClass("input", "is-input");
      expect(form.querySelector('button[type="submit"]')).not.toHaveClass("btn-sm");
    }
    for (const step of c.steps) {
      if (step.kind === "type") {
        fireEvent.change(await within(section).findByLabelText(step.field), { target: { value: step.value } });
      } else if (step.kind === "press") {
        await act(async () => {
          fireEvent.click(within(section).getByRole("button", { name: step.button }));
        });
      } else {
        const control = within(section).getByRole("switch", { name: step.switch });
        await waitFor(() => expect(control).not.toBeDisabled());
        await act(async () => {
          fireEvent.click(control);
        });
      }
    }

    await waitFor(() => expect(backend.writes, c.why).toEqual(c.expect.writes));
    for (const text of c.expect.shows) {
      await waitFor(() => expect(section.textContent).toContain(text));
    }
    if (c.expect.alert) {
      await waitFor(() => expect(within(section).getByRole("alert")).toHaveTextContent(c.expect.alert!));
    }
    expect(within(section).queryByTestId("settings-email-form") !== null, `${c.name}: whether the add form is offered`).toBe(c.expect.form);
    // A verification link's code never stays in the address.
    expect(window.location.search).not.toContain("verify_email");
  });
});
