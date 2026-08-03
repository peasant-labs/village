import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import TranscriptEditDialog from "./TranscriptEditDialog";
import fixtures from "./testdata/edit-errors.json";

type EditErrorFixture = {
  name: string;
  title: string;
  status: number;
  responseError: string;
  expected: string;
};

function MountedTranscriptEditAction() {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button onClick={() => setOpen(true)}>edit transcript</button>
      <TranscriptEditDialog
        open={open}
        onClose={() => setOpen(false)}
        transcriptId="30000000-0000-0000-0000-000000000003"
        initialTitle="safe title"
        initialVisibility="private"
      />
    </>
  );
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("mounted transcript edit errors", () => {
  const editErrorFixtures = fixtures as EditErrorFixture[];
  const fixtureKeys = ["expected", "name", "responseError", "status", "title"];
  if (editErrorFixtures.length !== 2 || new Set(editErrorFixtures.map(({ name }) => name)).size !== editErrorFixtures.length) {
    throw new Error("edit error fixtures must contain exactly two uniquely named behavior arms");
  }
  for (const fixture of editErrorFixtures) {
    if (JSON.stringify(Object.keys(fixture).sort()) !== JSON.stringify(fixtureKeys)) {
      throw new Error(`edit error fixture ${fixture.name} has unknown or missing fields`);
    }
    it(fixture.name, async () => {
      const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
        new Response(JSON.stringify(fixture.responseError ? { error: fixture.responseError } : {}), {
          status: fixture.status,
          headers: { "Content-Type": "application/json" },
        }),
      );
      const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
      const user = userEvent.setup();
      render(
        <QueryClientProvider client={client}>
          <MountedTranscriptEditAction />
        </QueryClientProvider>,
      );

      await user.click(screen.getByRole("button", { name: "edit transcript" }));
      const title = await screen.findByPlaceholderText("Untitled");
      fireEvent.change(title, { target: { value: fixture.title } });
      await user.click(screen.getByRole("button", { name: "Save" }));

      expect(await screen.findByRole("alert")).toHaveTextContent(fixture.expected);
      expect(fetchMock).toHaveBeenCalledOnce();
      expect(fetchMock.mock.calls[0]?.[1]).toMatchObject({ method: "PATCH" });
      expect(fetchMock.mock.calls[0]?.[1]?.body).toBe(
        JSON.stringify({ title: fixture.title, visibility: "private" }),
      );
    });
  }
});
