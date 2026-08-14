import { createElement, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { TranscriptListResponse } from "@/lib/types";

/**
 * Shared helpers for TanStack Query hook tests. Kept in one place so common test
 * values (a minimal discovery response, a retry-disabled client wrapper) are not
 * re-declared per file as more hook tests are added.
 */

/** A minimal, valid discovery list response for the given confirmed page. */
export function transcriptListResponse(
  page: number,
  overrides: Partial<TranscriptListResponse> = {},
): TranscriptListResponse {
  return { transcripts: [], total: 0, page, limit: 24, ...overrides };
}

/**
 * A QueryClientProvider wrapper with retries disabled, for deterministic hook
 * tests. Each call builds a fresh client so tests never share cache state.
 */
export function makeQueryClientWrapper(): (props: { children: ReactNode }) => ReactNode {
  return makeQueryClientHarness().wrapper;
}

/** Fresh client plus provider wrapper for tests that must inspect cache state. */
export function makeQueryClientHarness(): {
  client: QueryClient;
  wrapper: (props: { children: ReactNode }) => ReactNode;
} {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const wrapper = function QueryClientWrapper({ children }: { children: ReactNode }) {
    return createElement(QueryClientProvider, { client }, children);
  };
  return { client, wrapper };
}
