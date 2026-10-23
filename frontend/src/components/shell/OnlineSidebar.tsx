"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { Compass, GitPullRequest, Home, Users } from "lucide-react";
import { AppSidebar } from "@peasant-labs/fairtrade/ui";
import { useGroups } from "@/lib/queries/groups";
import { useTheme } from "@/hooks/useTheme";
import { collectiveToneColor } from "@/lib/collectiveTone";
import { DOCS_URL } from "@/lib/appLinks";
import AccountMenu from "@/components/shell/AccountMenu";
import type { User } from "@/lib/types";

/**
 * The app switcher: the docs, which open in a new tab, and the community,
 * which is not open yet and says "soon". The app does not list itself (the
 * sidebar already is this app), and the local app is not offered from here:
 * it runs on the person's own computer and is reached from there.
 */
const APPS = [
  { id: "docs", label: "docs", href: DOCS_URL },
  { id: "community", label: "community", tag: "soon" },
];

/** Where the sidebar marks you: the item for the page you are on, or none. */
export function currentItemFor(pathname: string, collectiveIds: ReadonlySet<string>): string | undefined {
  if (pathname === "/") return "home";
  if (pathname === "/discover") return "discover";
  if (pathname === "/pulls" || pathname.startsWith("/pulls/")) return "pulls";
  // A transcript is read from home, so home stays marked under it.
  if (pathname.startsWith("/transcripts/")) return "home";
  if (pathname === "/collectives") return "collectives";
  const collective = pathname.match(/^\/collectives\/([^/]+)/);
  if (collective) return collectiveIds.has(collective[1]) ? `collective:${collective[1]}` : "collectives";
  return undefined;
}

/**
 * The signed-in app's sidebar, from fairtrade: the sections, the collectives
 * the person belongs to (each with its colour), the app switcher and the
 * footer: the theme switch, then the person's own corner. It is the same on
 * every page: a settings page keeps its sections in a bar of its own inside the
 * page body, as `/settings` does.
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
  const { theme, setTheme } = useTheme();
  const collectives = useGroups();
  const mine = collectives.data ?? [];
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
  const currentId = currentItemFor(pathname, ids);

  return (
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
      accountSlot={user ? <AccountMenu user={user} /> : undefined}
    />
  );
}
