import { render, screen } from "@testing-library/react";
import { providerDisplayName } from "@peasant-labs/fairtrade/ui";
import { Harness } from "@peasant-labs/schema";
import { describe, expect, it } from "vitest";
import { AgentCell } from "@/components/collective/page/CollectiveTranscriptsTable";
import { ProviderIcon } from "@/lib/ft-ui";
import { loadFinalContractCompatibilityFixtures } from "@/test/finalContractCompatibilityFixtures";

// Every harness the contract names leads with its brand mark wherever a list
// names the agent; only a value outside the contract falls back to plain text.
const fixtures = loadFinalContractCompatibilityFixtures();

describe("the agent cell marks every harness the contract names", () => {
  for (const harness of fixtures.harnesses) {
    it(harness.name, () => {
      const { container } = render(<AgentCell provider={harness.value} />);
      expect(container.querySelector("svg"), `${harness.value} leads with its mark`).not.toBeNull();
    });
  }

  it(fixtures.offContractHarness.name, () => {
    const { container } = render(<AgentCell provider={fixtures.offContractHarness.value} />);
    expect(container.querySelector("svg")).toBeNull();
    expect(container.textContent).toBe(fixtures.offContractHarness.value);
  });

  it("renders the fairtrade name and gemini mark for antigravity-cli", () => {
    const { container } = render(
      <>
        <AgentCell provider={Harness.AntigravityCLI} />
        <ProviderIcon harness={Harness.AntigravityCLI} label accent />
      </>,
    );
    const displayName = providerDisplayName(Harness.AntigravityCLI);
    expect(container.querySelector(".on-none"), "a contract harness must not use the neutral fallback").toBeNull();
    expect(container.querySelector("svg[data-brand='gemini']"), "fairtrade maps this harness to the gemini mark").not.toBeNull();
    expect(container.querySelector(".pv-name-label")?.textContent).toBe(Harness.AntigravityCLI);
    const mark = screen.getByRole("img", { name: displayName });
    expect(mark.getAttribute("data-brand")).toBe("gemini");
    expect(mark.closest<HTMLElement>(".pv-icon")?.style.color).toBe("var(--teal)");
    expect(displayName).toBe("Antigravity CLI");
  });
});
