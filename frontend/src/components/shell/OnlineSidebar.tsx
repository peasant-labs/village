"use client";

import Link from "next/link";
import { usePathname, useSearchParams } from "next/navigation";
import {
  ArrowLeft,
  BookOpen,
  Compass,
  GitPullRequest,
  Github,
  Home,
  KeyRound,
  SlidersHorizontal,
  TriangleAlert,
  Users,
} from "lucide-react";
import { AppSidebar } from "@peasant-labs/fairtrade/ui";
import { useGroup, useGroups } from "@/lib/queries/groups";
import { collectiveToneColor } from "@/lib/collectiveTone";
import { DOCS_URL, LOCAL_APP_URL } from "@/lib/appLinks";
import { SETTINGS_SECTIONS, settingsSectionFrom, settingsSectionHref } from "@/lib/collectiveSections";
import AccountMenu from "@/components/shell/AccountMenu";
import type { User } from "@/lib/types";

const SECTION_ICONS = {
  general: SlidersHorizontal,
  access: KeyRound,
  members: Users,
  github: Github,
  "pull-requests": GitPullRequest,
  danger: TriangleAlert,
} as const;

/** The app switcher: this app, and the two that open outside it. */
const APPS = [
  { id: "online", label: "online" },
  { id: "local", label: "local", href: LOCAL_APP_URL },
  { id: "docs", label: "docs", href: DOCS_URL },
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

/** The collective whose settings the address is on, if it is a settings page. */
export function settingsCollectiveFor(pathname: string): string | null {
  return pathname.match(/^\/collectives\/([^/]+)\/settings\/?$/)?.[1] ?? null;
}

/**
 * The signed-in app's sidebar, from fairtrade: the sections, the collectives
 * the person belongs to (each with its colour), the app switcher and the
 * person's own corner. On a collective's settings page it becomes that page's
 * navigation instead: a way back to the collective, and the settings sections.
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
  const search = useSearchParams();
  const collectives = useGroups();
  const mine = collectives.data ?? [];
  const ids = new Set(mine.map((c) => c.id));

  const settingsOf = settingsCollectiveFor(pathname);
  // The settings page reads its collective under this same key, so naming it
  // here costs no request of its own.
  const settingsCollective = useGroup(settingsOf ?? "");
  let groups;
  let currentId: string | undefined;
  if (settingsOf !== null) {
    const name =
      mine.find((c) => c.id === settingsOf)?.name ?? settingsCollective.data?.group.name ?? "the collective";
    const base = `/collectives/${encodeURIComponent(settingsOf)}`;
    groups = [
      {
        id: "back",
        ariaLabel: "back",
        items: [{ id: "back", label: name, href: base, icon: ArrowLeft, title: `back to ${name}` }],
      },
      {
        id: "settings",
        label: "settings",
        ariaLabel: "collective settings",
        items: SETTINGS_SECTIONS.map((section) => ({
          id: `set-${section.id}`,
          label: section.label,
          href: settingsSectionHref(encodeURIComponent(settingsOf), section.id),
          icon: SECTION_ICONS[section.id],
        })),
      },
    ];
    currentId = `set-${settingsSectionFrom(search).id}`;
  } else {
    groups = [
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
      {
        id: "help",
        ariaLabel: "help",
        items: [{ id: "docs", label: "docs", href: DOCS_URL, icon: BookOpen, external: true }],
      },
    ];
    currentId = currentItemFor(pathname, ids);
  }

  return (
    <AppSidebar
      app="online"
      apps={APPS}
      groups={groups}
      currentId={currentId}
      homeHref="/"
      LinkComponent={Link}
      storageKey="peasant-online.sidebar"
      accountSlot={user ? <AccountMenu user={user} /> : undefined}
    />
  );
}
