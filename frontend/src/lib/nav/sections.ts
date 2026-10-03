/**
 * App navigation sections — the single source of truth for the top nav.
 *
 * The nav is `home | collectives`, lowercase, rendered through the lifted
 * GraphSectionNav primitive (@peasant-labs/fairtrade/ui) with real next/link
 * navigation instead of the demo's internal view-switcher. The account menu
 * (profile, settings, sign out) sits beside it in the Navbar, not in this registry.
 *
 * Both sections belong to somebody who is signed in, so a signed-out visitor is
 * offered none: their `/` is the sign-in page. `navSections()` takes the live
 * auth state and returns only the sections that currently apply.
 *
 * Explore and the publish dashboard are hidden from the nav, not deleted:
 * `/explore` and `/publish` still resolve by URL, and the discovery section's
 * label and href stay exported below for the in-page links that still point
 * at it.
 */

export interface NavSection {
  id: string;
  href: string;
  label: string;
  /** Extra pathname prefixes that keep this section active. */
  activePrefixes: string[];
}

/** The public discovery list's label and address. It is not offered in the
 *  nav; it is exported as the single source of truth for the in-page links
 *  that still lead to it (the detail-page breadcrumb's root crumb, the profile
 *  and project breadcrumbs, and the dev-only visual harness that mirrors
 *  them), so they import the constant instead of re-typing `/explore`. */
export const EXPLORE_SECTION: Pick<NavSection, "id" | "href" | "label"> = {
  id: "explore",
  href: "/explore",
  label: "explore",
};

/** The signed-in person's own landing page. It also stays active on the pages
 *  a person reaches from it — a transcript and a pull request's prompts — so
 *  the nav still says where they are. */
export const HOME_SECTION: NavSection = {
  id: "home",
  href: "/",
  label: "home",
  activePrefixes: ["/transcripts", "/pulls"],
};

/** The collectives the signed-in person belongs to, and each one's page. */
export const COLLECTIVES_SECTION: NavSection = {
  id: "collectives",
  href: "/groups",
  label: "collectives",
  activePrefixes: ["/groups"],
};

export function navSections(opts: { isLoggedIn: boolean }): NavSection[] {
  return opts.isLoggedIn ? [HOME_SECTION, COLLECTIVES_SECTION] : [];
}

/**
 * Whether `pathname` is within a section. Home owns `/` exactly; the others
 * match their href prefix plus any extra prefixes.
 */
export function isSectionActive(section: NavSection, pathname: string): boolean {
  const base = section.href === "/" ? pathname === "/" : pathname.startsWith(section.href);
  return base || section.activePrefixes.some((p) => pathname.startsWith(p));
}

/**
 * The "< back" affordance the demo's CommonsApp shell shows on its detail
 * sub-views (BACK_TO in CommonsApp.jsx: collective-detail -> collectives,
 * collective-settings -> collective-detail). GraphSectionNav (the primitive
 * Navbar.tsx renders sections through) has no built-in back-button support —
 * that only exists on the demo's OTHER shell export, GraphAppShell, which owns
 * its own internal view-switcher state and doesn't apply to village's real
 * routing — so this maps village's actual routes onto the same back-target
 * relationships by hand. A transcript goes back to home, the section it is
 * shown under, rather than to the discovery list the nav no longer offers. A
 * signed-out reader of a shared transcript link is offered no back link: their
 * `/` is the sign-in page, which is not where they came from. Returns `null`
 * on a top-level route (nothing to go back to).
 */
export function backTarget(pathname: string, isLoggedIn: boolean): { href: string } | null {
  const settingsMatch = pathname.match(/^\/groups\/([^/]+)\/settings\/?$/);
  if (settingsMatch) return { href: `/groups/${settingsMatch[1]}` };

  const detailMatch = pathname.match(/^\/groups\/([^/]+)\/?$/);
  if (detailMatch) return { href: COLLECTIVES_SECTION.href };

  if (pathname.match(/^\/transcripts\/([^/]+)\/?$/)) {
    return isLoggedIn ? { href: HOME_SECTION.href } : null;
  }

  return null;
}
