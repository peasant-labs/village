import { vi } from "vitest";

/**
 * Minimal `next/navigation` stand-in for the jsdom test environment.
 *
 * Next's real hooks require a mounted App Router and throw
 * `invariant expected app router to be mounted` under Vitest. This mock keeps
 * the pieces the production components use: route pushes/replaces are recorded
 * (so a test can assert a real navigation happened) and pathname/search-params
 * read the live jsdom location (so a test can drive URL state).
 */
export const pushedRoutes: string[] = [];
export const replacedRoutes: string[] = [];

export function resetNextNavigation(): void {
  pushedRoutes.length = 0;
  replacedRoutes.length = 0;
}

const router = {
  push: (href: string) => {
    pushedRoutes.push(href);
  },
  replace: (href: string) => {
    replacedRoutes.push(href);
  },
  back: vi.fn(),
  forward: vi.fn(),
  refresh: vi.fn(),
  prefetch: vi.fn(),
};

export function useRouter() {
  return router;
}

export function usePathname(): string {
  return typeof window === "undefined" ? "/" : window.location.pathname;
}

export function useSearchParams(): URLSearchParams {
  return new URLSearchParams(typeof window === "undefined" ? "" : window.location.search);
}

export function useParams(): Record<string, string> {
  return {};
}

export function useSelectedLayoutSegment(): string | null {
  return null;
}

export function useSelectedLayoutSegments(): string[] {
  return [];
}
