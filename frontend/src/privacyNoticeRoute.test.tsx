import { cleanup, render, screen, within } from "@testing-library/react";
import { AllLicenses } from "@peasant-labs/schema";
import { afterEach, describe, expect, it } from "vitest";
import PrivacyPage, { metadata } from "@/app/privacy/page";
import { NO_LICENSE } from "@/app/privacy/notice";
import { loadPrivacyNoticeFixtures } from "@/test/privacyNoticeFixtures";
import { loadPrivacyIntegrityFixtures } from "@/test/integrityDisclosureFixtures";

// Mounts the REAL /privacy route. The notice is what every publish consent
// control points at, so what matters is the rendered page: its title, its
// sections, and one block per license choice the contract offers, no more and
// no fewer.

const fixtures = loadPrivacyNoticeFixtures();
const integrity = loadPrivacyIntegrityFixtures();

afterEach(() => cleanup());

function noticeSurface(): HTMLElement {
  const el = document.querySelector('[data-testid="privacy-notice"]');
  if (!(el instanceof HTMLElement)) throw new Error("the privacy notice did not mount");
  return el;
}

describe("the /privacy route", () => {
  it("is titled and headed 'privacy notice'", () => {
    render(<PrivacyPage />);
    expect(metadata.title).toBe("privacy notice | peasant");
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("privacy notice");
  });

  it("names the operator and a contact address", () => {
    render(<PrivacyPage />);
    const surface = noticeSurface();
    expect(surface).toHaveTextContent("operated by David Huu Pham");
    const contact = within(surface).getByRole("link", { name: "david@peasantlabs.org" });
    expect(contact).toHaveAttribute("href", "mailto:david@peasantlabs.org");
  });

  for (const c of fixtures.sectionHeadings) {
    it(`shows the ${c.name} section`, () => {
      render(<PrivacyPage />);
      expect(
        within(noticeSurface()).getByRole("heading", { level: 2, name: c.heading }),
      ).toBeInTheDocument();
    });
  }

  it("covers exactly the contract's license menu plus the no-license choice", () => {
    // The accept-set comes from the schema contract, not from the page: a
    // license added to the contract must show up here AND in the fixture
    // before this passes again.
    const contractChoices = [...AllLicenses, NO_LICENSE].sort();
    expect(fixtures.licenseChoices.map((c) => c.choice).sort()).toEqual(contractChoices);

    render(<PrivacyPage />);
    const rendered = Array.from(noticeSurface().querySelectorAll("[data-license-choice]"))
      .map((el) => el.getAttribute("data-license-choice"))
      .sort();
    expect(rendered).toEqual(contractChoices);
  });

  for (const c of fixtures.licenseChoices) {
    it(`states the grant for ${c.name} and quotes its consent control`, () => {
      render(<PrivacyPage />);
      const block = noticeSurface().querySelector(`[data-license-choice="${c.choice}"]`);
      if (!(block instanceof HTMLElement)) throw new Error(`no block for ${c.choice}`);
      expect(within(block).getByRole("heading", { level: 3 })).toHaveTextContent(c.heading);
      expect(block).toHaveTextContent(`“${c.controlLabel},”`);
    });
  }

  it("shows the rights confirmation once, for every choice", () => {
    render(<PrivacyPage />);
    const matches = noticeSurface().textContent?.match(/You confirm that you have the rights/g) ?? [];
    expect(matches).toHaveLength(1);
  });
});

describe("the automated integrity checks section", () => {
  function region(): HTMLElement {
    render(<PrivacyPage />);
    return within(noticeSurface()).getByRole("region", { name: integrity.heading });
  }

  for (const c of integrity.cases) {
    it(c.name, () => {
      const section = region();
      const heading = within(section).getByRole("heading", { level: 2 });
      expect(heading).toHaveAttribute("id", "privacy-integrity");
      if (c.name === "heading_is_lowercase") {
        expect(heading.textContent).toBe(integrity.heading);
        expect(heading.textContent).toBe(heading.textContent?.toLowerCase());
      }
      if (c.name === "notice_date_bumped") {
        expect(noticeSurface().querySelector("time")).toHaveAttribute("datetime", integrity.noticeUpdated);
      }
      const text = section.textContent ?? "";
      for (const phrase of c.contains) expect(text).toContain(phrase);
      const page = noticeSurface().textContent ?? "";
      for (const phrase of c.absent) expect(page).not.toContain(phrase);
    });
  }

  it("follows public contributions", () => {
    render(<PrivacyPage />);
    const ids = Array.from(noticeSurface().querySelectorAll("h2")).map((h) => h.id);
    expect(ids.indexOf("privacy-integrity")).toBe(ids.indexOf("privacy-contributions") + 1);
  });
});
