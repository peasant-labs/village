"use client";

import Link from "next/link";
import { usePathname, useSearchParams } from "next/navigation";
import { Moon, Sun } from "lucide-react";
import { useTheme } from "@/hooks/useTheme";
import PeasantWordmark from "@/components/online/PeasantWordmark";
import { loginHref } from "@/lib/signIn";

/**
 * The bar above a page somebody reads without being signed in: a shared
 * transcript, a public collective, a pull request's prompts, the privacy notice.
 * It names the product, keeps the privacy notice and the theme in reach, and
 * offers one way in, which returns to this page once signed in.
 */
export default function PublicTopBar({ offerSignIn }: { offerSignIn: boolean }) {
  const pathname = usePathname();
  const search = useSearchParams();
  const { theme, toggle } = useTheme();
  const query = search.toString();
  const here = query ? `${pathname}?${query}` : pathname;

  return (
    <header className="on-top" data-testid="public-top-bar">
      <div className="on-top-inner">
        <PeasantWordmark href="/" label="peasant home" />
        <nav aria-label="site">
          <Link href="/privacy">privacy</Link>
          <button
            type="button"
            className="on-icon-btn"
            onClick={toggle}
            aria-label={`switch to ${theme === "light" ? "dark" : "light"} mode`}
          >
            {theme === "light" ? <Moon aria-hidden /> : <Sun aria-hidden />}
          </button>
          {offerSignIn && (
            <Link href={loginHref(here)} className="btn btn-secondary">
              sign in
            </Link>
          )}
        </nav>
      </div>
    </header>
  );
}
