"use client";

import { useState, useRef, useEffect } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { Moon, Sun } from "lucide-react";
import { useAuth } from "@/providers/AuthProvider";
import { useLogout } from "@/lib/queries/auth";
import { useTheme } from "@/hooks/useTheme";
import { API_URL_BASE } from "@/lib/api";
import { ChevronLeft } from "lucide-react";
import { Avatar, GraphSectionNav, SignInProviders } from "@/lib/ft-ui";
import { navSections, isSectionActive, backTarget } from "@/lib/nav/sections";

function UserMenu({ user }: { user: { github_username: string; avatar_url: string | null } }) {
  const [open, setOpen] = useState(false);
  const menuRef = useRef<HTMLDivElement>(null);
  const logout = useLogout();

  useEffect(() => {
    function handleClickOutside(e: MouseEvent) {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) {
        setOpen(false);
      }
    }
    if (open) {
      document.addEventListener("mousedown", handleClickOutside);
      return () => document.removeEventListener("mousedown", handleClickOutside);
    }
  }, [open]);

  return (
    <div className="relative" ref={menuRef}>
      <button
        type="button"
        onClick={() => setOpen(!open)}
        className="flex focus-mono cursor-pointer"
        aria-label={`${user.github_username}'s account menu`}
      >
        {/* DS Avatar (src/ui/Avatar.jsx): photo when avatar_url is set, else its own
            styled initials fallback — replaces the bare bg-mark/text-mark-fg white
            box (that token pair renders near-white in dark theme, reading as
            off-DS chrome; see the "white A avatar" UAT finding). */}
        <Avatar name={user.github_username} src={user.avatar_url ?? undefined} size="sm" />
      </button>

      {open && (
        <div className="absolute right-0 mt-2 w-48 border border-rule bg-surface py-1 z-50">
          <Link
            href={`/users/${user.github_username}`}
            onClick={() => setOpen(false)}
            className="block px-4 py-2 text-sm text-ink transition-colors hover:bg-surface-hover focus-mono cursor-pointer"
          >
            Profile
          </Link>
          <div className="my-1 border-t border-rule" />
          <button
            type="button"
            onClick={() => logout.mutate()}
            className="w-full px-4 py-2 text-left text-sm text-danger transition-colors hover:bg-danger-soft focus-mono cursor-pointer"
          >
            Sign out
          </button>
        </div>
      )}
    </div>
  );
}

export default function Navbar() {
  const pathname = usePathname();
  const { user, isLoading, isLoggedIn } = useAuth();
  const { theme, toggle } = useTheme();

  // The fairtrade demo's CommonsApp shell subnav (explore | collectives |
  // publish | profile, lowercase, amber-pill active state) via the lifted
  // GraphSectionNav primitive — same primitive + itemClassName/activeItemClassName
  // pattern peasant's own TopNavbar.tsx uses for its GraphSectionNav, matching the
  // demo's `.iu-subnav-item.active` (filled amber pill) rather than an underline
  // marker.
  const sections = navSections({ isLoggedIn, githubUsername: user?.github_username });
  const activeSection = sections.find((s) => isSectionActive(s, pathname));
  // The demo's subnav shows a "< back" affordance on detail sub-views (CommonsApp.jsx's
  // BACK_TO) — mapped onto village's real routes in backTarget(). Rendered before the
  // section pills, matching the demo's ordering.
  const back = backTarget(pathname);

  return (
    // Background: bg-surface, matching the demo's .iu-bar (fairtrade src/index.css:2096
    // `.iu-bar { background: var(--surface); }`) exactly -- NOT bg-canvas (the page's own
    // background token, one step darker). This was the root cause of the "nav shell bg too
    // dark" finding: bg-canvas is inherited from the previous hand-rolled Navbar and was never
    // updated when the shell chrome was adopted. Verified via computed style: village and
    // demo now render the IDENTICAL rgb in both themes (see this fix's commit message).
    <header className="fixed top-0 left-0 right-0 z-50 h-16 border-b border-rule bg-surface">
      <div className="flex h-full items-center justify-between px-8">
        <div className="flex items-center gap-6">
          <Link href="/" className="focus-mono cursor-pointer" aria-label="Village home">
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

          <GraphSectionNav
            sections={sections}
            activeId={activeSection?.id}
            hrefFor={(s: (typeof sections)[number]) => s.href}
            LinkComponent={Link}
            className="flex items-center gap-0.5"
            // font-mono: matches the demo's .iu-subnav-item exactly (fairtrade
            // src/index.css:2159 `font-family: var(--font-mono)`) -- chrome, not user
            // content. Missing here previously; every nav item (not just explore's)
            // rendered in the body font instead of mono.
            itemClassName="border border-transparent px-3 py-1.5 text-sm font-mono font-medium transition-colors duration-150 focus-mono cursor-pointer text-ink-3 hover:text-ink hover:bg-surface-hover"
            activeItemClassName="bg-amber text-on-amber border-amber"
            ariaLabel="Main navigation"
          />
        </div>

        <div className="flex items-center gap-3">
          <button
            onClick={toggle}
            className="flex h-8 w-8 items-center justify-center text-ink-3 transition-colors duration-150 hover:text-ink hover:bg-surface-hover focus-mono cursor-pointer"
            aria-label={`Switch to ${theme === "light" ? "dark" : "light"} mode`}
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
            <UserMenu user={user} />
          ) : (
            // DS SignInProviders (src/ui/SignIn.jsx): amber-filled split button ("continue
            // with github" + a chevron menu for the rest) — replaces village's former
            // hand-rolled duplicate (components/auth/SignInProviders.tsx, soft-retired),
            // which used the same off-DS bg-mark/text-mark-fg token pair (renders white
            // in dark theme; see the "Sign in with GitHub is white" UAT finding).
            <SignInProviders onSignIn={(id) => { window.location.href = `${API_URL_BASE}/auth/${id}`; }} />
          )}
        </div>
      </div>
    </header>
  );
}
