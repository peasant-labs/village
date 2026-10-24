"use client";

import { useCallback } from "react";
import Link from "next/link";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { Compass, GitPullRequest, Home, Users } from "lucide-react";
import { AppSidebar } from "@peasant-labs/fairtrade/ui";
import { useGroups } from "@/lib/queries/groups";
import { useTheme } from "@/hooks/useTheme";
import { collectiveToneColor } from "@/lib/collectiveTone";
import { DOCS_URL } from "@/lib/appLinks";
import { useAccountMenu } from "@/components/shell/AccountMenu";
import { openExploreSearch, useSearchShortcut } from "@/lib/exploreSearch";
import { CONTEXT_PARAM, parseTranscriptContext, type TranscriptContext } from "@/lib/transcriptContext";
import type { User } from "@/lib/types";

/**
 * The app switcher: the docs, which open in a new tab. fairtrade adds the
 * community, not open yet, as "soon". The app does not list itself (the
 * sidebar already is this app), and the local app is not offered from here:
 * it runs on the person's own computer and is reached from there.
 */
const APPS = [{ id: "docs", label: "docs", href: DOCS_URL }];

/** Where the sidebar marks you: the item for the page you are on, or none.
 *  A transcript marks the place it was opened from (its address's context):
 *  the collective, pull requests, or home. */
export function currentItemFor(
  pathname: string,
  collectiveIds: ReadonlySet<string>,
  context: TranscriptContext | null = null,
): string | undefined {
  if (pathname === "/") return "home";
  if (pathname === "/discover") return "discover";
  if (pathname === "/pulls" || pathname.startsWith("/pulls/")) return "pulls";
  if (pathname.startsWith("/transcripts/")) {
    if (context?.kind === "collective") return collectiveIds.has(context.id) ? `collective:${context.id}` : "collectives";
    if (context?.kind === "pull") return "pulls";
    return "home";
  }
  if (pathname === "/settings") return "settings";
  if (pathname === "/collectives") return "collectives";
  const collective = pathname.match(/^\/collectives\/([^/]+)/);
  if (collective) return collectiveIds.has(collective[1]) ? `collective:${collective[1]}` : "collectives";
  return undefined;
}

/**
 * The signed-in app's sidebar, from fairtrade, in its one order: the
 * shortcuts (the search, the docs and the community), the sections and the
 * collectives the person belongs to (each with its colour), then the footer:
 * the person's own corner, the settings gear and the theme switch. It is the
 * same on every page: a settings page keeps its sections in a bar of its own
 * inside the page body, as `/settings` does.
 *
 * While the session read is in flight there is no person yet, so the corner
 * that names them is left out until it answers.
 *
 * The collectives come from the same membership read the collectives page
 * uses. While it loads, or if it fails, the sections still show; the list of
 * collectives is a convenience beside them, and `/collectives` says why it is
 * missing.
 */
export default function OnlineSidebar({ user }: { user: User | null }) {
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const { theme, setTheme } = useTheme();
  const collectives = useGroups();
  const mine = collectives.data ?? [];
  // The search is /explore's transcript search, its box taking the cursor.
  const router = useRouter();
  const openSearch = useCallback(() => openExploreSearch(router.push, pathname), [router, pathname]);
  useSearchShortcut(openSearch);
  const accountMenu = useAccountMenu(user);
  const ids = new Set(mine.map((c) => c.id));

  const groups = [
    {
      id: "sections",
      ariaLabel: "peasant sections",
      items: [
        { id: "home", label: "home", href: "/", icon: Home },
        { id: "discover", label: "discover", href: "/discover", icon: Compass },
        { id: "pulls", label: "pull requests", href: "/pulls", icon: GitPullRequest },
        { id: "collectives", label: "collectives", href: "/collectives", icon: Users },
        ...mine.map((c) => ({
          id: `collective:${c.id}`,
          label: c.name,
          href: `/collectives/${encodeURIComponent(c.id)}`,
          swatch: collectiveToneColor(c.id),
          depth: 1 as const,
        })),
      ],
    },
  ];
  const currentId = currentItemFor(pathname, ids, parseTranscriptContext(searchParams.get(CONTEXT_PARAM)));

  return (
    <>
      <AppSidebar
        app="online"
        apps={APPS}
        groups={groups}
        currentId={currentId}
        homeHref="/"
        LinkComponent={Link}
        storageKey="peasant-online.sidebar"
        theme={theme}
        onThemeChange={setTheme}
        account={
          user
            ? { handle: user.github_username, items: accountMenu.items, avatarUrl: user.avatar_url ?? undefined }
            : undefined
        }
        search={{ onOpen: openSearch, shortcut: "⌘K", keyShortcuts: "Meta+K Control+K" }}
        settingsHref="/settings"
      />
      {accountMenu.dialog}
    </>
  );
}
