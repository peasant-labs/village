"use client";

import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { ChevronLeft, LogOut, Moon, Sun, UserRound } from "lucide-react";
import { useAuth } from "@/providers/AuthProvider";
import { useLogout } from "@/lib/queries/auth";
import { useTheme } from "@/hooks/useTheme";
import { Avatar, GraphSectionNav, Menu, SignInProviders } from "@/lib/ft-ui";
import { navSections, isSectionActive, backTarget } from "@/lib/nav/sections";
import { SIGN_IN_PROVIDERS, startSignIn } from "@/lib/signIn";

/**
 * The account menu: the signed-in person's handle, and the two things they do
 * with their account from the chrome. The design system's `Menu` owns the
 * trigger, the popout and the keyboard behaviour; the trigger reads as the
 * person (their avatar and handle) rather than as a generic "account" button.
 */
function AccountMenu({ user }: { user: { github_username: string; avatar_url: string | null } }) {
  const router = useRouter();
  const logout = useLogout();
  const handle = user.github_username;

  return (
    <Menu
      align="end"
      label={
        <span className="inline-flex items-center gap-2" data-testid="account-menu-trigger">
          {/* DS Avatar (src/ui/Avatar.jsx): photo when avatar_url is set, else its own
              styled initials. Hidden from assistive technology: the handle beside it
              already names the person, and the avatar would say it twice. */}
          <Avatar name={handle} src={user.avatar_url ?? undefined} size="sm" aria-hidden="true" />
          {/* The space is its own text node: a flex container drops it from the
              layout, and the accessible name keeps it between the two words. */}
          <span className="sr-only">account menu for</span>{" "}
          {/* On a phone the handle is heard, not shown, so the trigger is no
              wider than the avatar and the header still fits. */}
          <span className="sr-only font-mono sm:not-sr-only">@{handle}</span>
        </span>
      }
      items={[
        {
          label: "profile",
          icon: UserRound,
          onSelect: () => router.push(`/users/${encodeURIComponent(handle)}`),
        },
        { label: "", separator: true },
        { label: "sign out", icon: LogOut, onSelect: () => logout.mutate() },
      ]}
    />
  );
}

export default function Navbar() {
  const pathname = usePathname();
  const { user, isLoading, isLoggedIn } = useAuth();
  const { theme, toggle } = useTheme();

  // The fairtrade demo's CommonsApp shell subnav (lowercase, amber-pill active
  // state) via the lifted GraphSectionNav primitive — same primitive +
  // itemClassName/activeItemClassName pattern peasant's own TopNavbar.tsx uses,
  // matching the demo's `.iu-subnav-item.active` (filled amber pill) rather than
  // an underline marker. Village offers `home | collectives`, and only to
  // somebody who is signed in.
  const sections = navSections({ isLoggedIn });
  const activeSection = sections.find((s) => isSectionActive(s, pathname));
  // The demo's subnav shows a "< back" affordance on detail sub-views (CommonsApp.jsx's
  // BACK_TO) — mapped onto village's real routes in backTarget(). Rendered before the
  // section pills, matching the demo's ordering.
  const back = backTarget(pathname);
  // A signed-out `/` IS the sign-in page, and its one button is the page's own.
  // A second one up here would make two front doors on one screen.
  const offerHeaderSignIn = pathname !== "/";

  return (
    // Background: bg-surface, matching the demo's .iu-bar (fairtrade src/index.css:2096
    // `.iu-bar { background: var(--surface); }`) exactly -- NOT bg-canvas (the page's own
    // background token, one step darker). This was the root cause of the "nav shell bg too
    // dark" finding: bg-canvas is inherited from the previous hand-rolled Navbar and was never
    // updated when the shell chrome was adopted. Verified via computed style: village and
    // demo now render the IDENTICAL rgb in both themes (see this fix's commit message).
    <header className="fixed top-0 left-0 right-0 z-50 h-[var(--app-header-height)] border-b border-rule bg-surface">
      <div className="flex h-full items-center justify-between px-8">
        <div className="flex items-center gap-6">
          <Link href="/" className="focus-mono cursor-pointer" aria-label="village home">
            <span className="font-[family-name:var(--font-display)] text-xl font-semibold text-ink">
              village
            </span>
          </Link>

          {back && (
            <Link href={back.href} className="iu-subnav-back">
              <ChevronLeft size={14} aria-hidden />
              back
            </Link>
          )}

          {sections.length > 0 && (
            <GraphSectionNav
              sections={sections}
              activeId={activeSection?.id}
              hrefFor={(s: (typeof sections)[number]) => s.href}
              LinkComponent={Link}
              className="flex items-center gap-0.5"
              // font-mono: matches the demo's .iu-subnav-item exactly (fairtrade
              // src/index.css:2159 `font-family: var(--font-mono)`) -- chrome, not user
              // content.
              itemClassName="border border-transparent px-3 py-1.5 text-sm font-mono font-medium transition-colors duration-150 focus-mono cursor-pointer text-ink-3 hover:text-ink hover:bg-surface-hover"
              activeItemClassName="bg-amber text-on-amber border-amber"
              ariaLabel="main navigation"
            />
          )}
        </div>

        <div className="flex items-center gap-3">
          {/* The privacy notice is the text every publish consent control
              points at, so it is reachable from the persistent chrome on every
              page, signed in or out. Mono lowercase like the section pills; it
              is not a section, so it stays out of the GraphSectionNav registry. */}
          <Link
            href="/privacy"
            className="px-2 py-1.5 text-sm font-mono text-ink-3 transition-colors duration-150 hover:text-ink hover:bg-surface-hover focus-mono cursor-pointer"
          >
            privacy
          </Link>

          <button
            onClick={toggle}
            className="flex h-8 w-8 items-center justify-center text-ink-3 transition-colors duration-150 hover:text-ink hover:bg-surface-hover focus-mono cursor-pointer"
            aria-label={`switch to ${theme === "light" ? "dark" : "light"} mode`}
          >
            {theme === "light" ? (
              <Moon size={15} aria-hidden />
            ) : (
              <Sun size={15} aria-hidden />
            )}
          </button>

          {isLoading ? (
            <div className="h-8 w-8 animate-shimmer" />
          ) : isLoggedIn && user ? (
            <AccountMenu user={user} />
          ) : offerHeaderSignIn ? (
            // DS SignInProviders (src/ui/SignIn.jsx), GitHub only: one amber button,
            // no chevron, because there is nothing behind it.
            <SignInProviders providers={SIGN_IN_PROVIDERS} onSignIn={startSignIn} />
          ) : null}
        </div>
      </div>
    </header>
  );
}
