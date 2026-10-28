import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { parse } from "yaml";
import TranscriptHeaderActions from "@/components/transcript/TranscriptHeaderActions";
import { assertNamesMatch } from "@/test/fixtureAssertions";

const fixture = parse(readFileSync(resolve(process.cwd(), "src/testdata/transcript-copy-feedback.yaml"), "utf8")) as {
  url: string;
  cases: Array<{ name: string; reject: boolean; notifies: boolean }>;
};
assertNamesMatch(fixture.cases.map((c) => c.name), ["accepted-copy-calls-success", "rejected-copy-does-not-call-success"], "transcript-copy-feedback cases");

afterEach(() => { Reflect.deleteProperty(navigator, "clipboard"); });

describe("transcript copy success boundary", () => {
  for (const c of fixture.cases) {
    it(c.name, async () => {
      const writeText = vi.fn(async () => { if (c.reject) throw new Error("permission denied"); });
      Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
      const onCopied = vi.fn();
      const noop = () => {};
      render(<TranscriptHeaderActions url={fixture.url} onCopied={onCopied} onDownloadMarkdown={noop} onDownloadJSON={noop} onDownloadJSONL={noop} />);
      await act(async () => { fireEvent.click(screen.getByRole("button", { name: "copy link" })); });
      await waitFor(() => expect(writeText).toHaveBeenCalledWith(fixture.url));
      await waitFor(() => expect(onCopied).toHaveBeenCalledTimes(c.notifies ? 1 : 0));
    });
  }
});
