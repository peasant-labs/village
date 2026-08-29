import { describe, expect, it } from "vitest";
import { render, cleanup } from "@testing-library/react";
import { useQueryClient, type QueryClient } from "@tanstack/react-query";
import QueryProvider from "@/providers/QueryProvider";

// The home page keeps the rows it already holds when a refresh fails, and shows
// a notice offering to try again. That surface is only ever REACHED because the
// app refetches in the background, which TanStack does on window focus unless
// it is told not to. The mounted route test proves what the page DOES once a
// refresh fails, under its own client; this proves the app still lets a refresh
// happen at all.
//
// The client is created inside the provider and is not exported, so it is read
// back out of context the way any component reads it. Turning focus refetching
// off, or making data permanently fresh, retires a surface the mounted tests
// still exercise — and fails here rather than silently.
function readClient(): QueryClient {
  let captured: QueryClient | null = null;
  function Probe() {
    captured = useQueryClient();
    return null;
  }
  render(
    <QueryProvider>
      <Probe />
    </QueryProvider>,
  );
  if (captured == null) throw new Error("QueryProvider did not provide a client");
  return captured;
}

describe("the app's query defaults keep background refreshes possible", () => {
  it("does not disable refetch-on-focus, and lets data go stale", () => {
    const defaults = readClient().getDefaultOptions().queries;
    cleanup();

    // Not disabled: false would mean a failed refresh, and therefore the notice
    // that offers to retry it, could never be reached in the app.
    expect(defaults?.refetchOnWindowFocus).not.toBe(false);

    // And data must be able to GO stale, or the refetch never becomes due.
    const staleTime = defaults?.staleTime;
    expect(typeof staleTime === "number" || staleTime === undefined).toBe(true);
    if (typeof staleTime === "number") {
      expect(Number.isFinite(staleTime)).toBe(true);
    }
  });
});
