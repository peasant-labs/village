/**
 * App navigation sections — the single source of truth for the top nav.
 * Mirrors the published Fairtrade CommonsApp demo shell:
 * home | explore | collectives | publish | profile, lowercase, rendered through the
 * lifted GraphSectionNav primitive (@peasant-labs/fairtrade/ui) with real
 * next/link navigation instead of the demo's internal view-switcher.
 *
 * Unlike the demo (which has no auth), village gates collectives/publish/
 * profile on being signed in — same real constraint the previous hand-rolled
 * Navbar enforced (you can't publish or manage a collective, or view "your"
 * profile, while signed out). `navSections()` takes the live auth state and
 * returns only the sections that currently apply.
 */

export interface NavSection {
  id: string;
  href: string;
  label: string;
  /** Extra pathname prefixes that keep this section active. */
  activePrefixes: string[];
  /** Extra pathnames that keep this section active on an EXACT match. A
   *  prefix cannot express "/" — every path starts with it — so a section
   *  that also owns the bare root states it here. */
  exactPaths?: string[];
  title?: string;
}

/** The always-present discovery section. Exported as the single source of
 *  truth for its label/href so callers that need only this one entry (the
 *  detail-page breadcrumb's root crumb, and the dev-only visual harness
 *  that mirrors it) can import the constant directly instead of scanning
 *  `navSections()`'s result for it.
 *
 *  Discovery has its own address, `/explore`. `/` shows it to a signed-out
 *  visitor and the signed-in person's own home page to everyone else, so `/`
 *  cannot be this section's href: for a signed-in person it does not lead
 *  here. */
export const EXPLORE_SECTION: NavSection = {
  id: "explore",
  href: "/explore",
  label: "explore",
  activePrefixes: ["/transcripts"],
  title: "Search redacted AI agent transcripts shared by the community.",
};

/** The signed-in person's own landing page: their recent sessions and the
 *  projects those sessions belong to. Absent for a signed-out visitor, whose
 *  `/` is discovery rather than a home of their own. */
export const HOME_SECTION: NavSection = {
  id: "home",
  href: "/",
  label: "home",
  activePrefixes: [],
  title: "Your recent sessions and the projects they belong to.",
};

export function navSections(opts: { isLoggedIn: boolean; githubUsername?: string }): NavSection[] {
  // A signed-out visitor has no home of their own: `/` serves discovery to
  // them, so the explore entry is the active one while they are there.
  const sections: NavSection[] = opts.isLoggedIn
    ? [HOME_SECTION, EXPLORE_SECTION]
    : [{ ...EXPLORE_SECTION, exactPaths: ["/"] }];

  if (opts.isLoggedIn) {
    sections.push({
      id: "collectives",
      href: "/groups",
      label: "collectives",
      activePrefixes: ["/groups"],
      title: "The collectives you belong to and their governance settings.",
    });
    sections.push({
      id: "publish",
      href: "/publish",
      label: "publish",
      activePrefixes: ["/publish"],
      title: "Share a redacted transcript with the commons.",
    });
    if (opts.githubUsername) {
      sections.push({
        id: "profile",
        href: `/users/${opts.githubUsername}`,
        label: "profile",
        activePrefixes: [`/users/${opts.githubUsername}`],
        title: "Your public profile and shared transcripts.",
      });
    }
  }

  return sections;
}

/**
 * Whether `pathname` is within a section. Home owns `/` exactly; the others
 * match their href prefix plus any extra prefixes.
 */
export function isSectionActive(section: NavSection, pathname: string): boolean {
  const base = section.href === "/" ? pathname === "/" : pathname.startsWith(section.href);
  return (
    base ||
    section.activePrefixes.some((p) => pathname.startsWith(p)) ||
    (section.exactPaths ?? []).some((p) => pathname === p)
  );
}

/**
 * The "< back" affordance the demo's CommonsApp shell shows on its detail
 * sub-views (BACK_TO in CommonsApp.jsx: collective-detail -> collectives,
 * collective-settings -> collective-detail, transcript-detail -> explore).
 * GraphSectionNav (the primitive Navbar.tsx renders sections through) has no
 * built-in back-button support — that only exists on the demo's OTHER shell
 * export, GraphAppShell, which owns its own internal view-switcher state and
 * doesn't apply to village's real routing — so this maps village's actual
 * routes onto the same back-target relationships by hand. Returns `null` on
 * a top-level route (nothing to go back to).
 */
export function backTarget(pathname: string): { href: string } | null {
  const settingsMatch = pathname.match(/^\/groups\/([^/]+)\/settings\/?$/);
  if (settingsMatch) return { href: `/groups/${settingsMatch[1]}` };

  const detailMatch = pathname.match(/^\/groups\/([^/]+)\/?$/);
  if (detailMatch) return { href: "/groups" };

  if (pathname.match(/^\/transcripts\/([^/]+)\/?$/)) return { href: EXPLORE_SECTION.href };

  return null;
}
