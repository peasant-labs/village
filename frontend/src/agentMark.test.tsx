import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { AgentCell } from "@/components/collective/page/CollectiveTranscriptsTable";
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
});
