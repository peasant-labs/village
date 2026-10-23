import { describe, expect, it } from "vitest";
import {
  formatTranscriptContext,
  inferTranscriptContext,
  parseTranscriptContext,
  transcriptHref,
} from "@/lib/transcriptContext";
import { buildTranscriptBreadcrumb } from "@/components/session-detail/v2/transcriptChrome";
import { loadTranscriptContextFixtures } from "@/test/transcriptContextFixtures";

const fx = loadTranscriptContextFixtures();

describe("transcript context: read from an address", () => {
  for (const c of fx.parseCases) {
    it(c.name, () => {
      const parsed = parseTranscriptContext(c.raw);
      expect(parsed).toEqual(c.expect);
      if (parsed) expect(formatTranscriptContext(parsed)).toBe(c.raw);
    });
  }
});

describe("transcript context: a transcript's address", () => {
  for (const c of fx.hrefCases) {
    it(c.name, () => {
      expect(transcriptHref(c.transcriptId, c.context, c.turn ?? undefined)).toBe(c.expect);
    });
  }
});

describe("transcript context: given to a link that carries none", () => {
  for (const c of fx.inferCases) {
    it(c.name, () => {
      expect(inferTranscriptContext(c.shares, new Set(c.mine))).toEqual(c.expect);
    });
  }
});

describe("transcript context: the trail above the title", () => {
  for (const c of fx.breadcrumbCases) {
    it(c.name, () => {
      const crumbs = buildTranscriptBreadcrumb({
        project: "village",
        projectHref: "/users/alice/projects/abc",
        isLoggedIn: c.isLoggedIn,
        context: c.context,
        collectiveName: c.collectiveName,
      });
      expect(crumbs.map((crumb) => (crumb.href ? `${crumb.label} -> ${crumb.href}` : crumb.label))).toEqual(c.expect);
    });
  }
});
