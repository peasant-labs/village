import "@testing-library/jest-dom/vitest";
import { vi } from "vitest";

// Next's navigation hooks require a mounted App Router and throw under jsdom.
// The test stand-in records pushes/replaces and reads the live jsdom location;
// a test that needs different behavior still overrides it with its own
// file-level `vi.mock("next/navigation", ...)`.
vi.mock("next/navigation", () => import("./src/test/nextNavigationMock"));

// jsdom implements no layout and no scrolling. Fairtrade restores an active
// turn by scrolling it into view, so a test that mounts a restored selection
// needs these two no-ops instead of "not implemented" throws.
if (typeof Element !== "undefined") {
  Element.prototype.scrollIntoView = () => {};
  Element.prototype.scrollTo = (() => {}) as typeof Element.prototype.scrollTo;
}
