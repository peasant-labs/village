/**
 * Which part of a collective a page shows: the tab on `/collectives/{id}`
 * (`?tab=`) and the section on `/collectives/{id}/settings` (`?section=`).
 *
 * Both are read from the address so a link (the sidebar, a notification, the
 * GitHub App install return) can open the right one, and an unknown or
 * unavailable value falls back to the first, never to an empty page.
 */

// ── the collective page's tabs ───────────────────────────────────────────────

export type CollectiveTabId = "transcripts" | "waiting" | "members" | "stats";

/** The query parameter that names the open tab. */
export const TAB_PARAM = "tab";

export interface CollectiveTabDef {
  id: CollectiveTabId;
  label: string;
}

/** Every tab, in the order the page draws them. */
export const COLLECTIVE_TABS: readonly CollectiveTabDef[] = [
  { id: "transcripts", label: "transcripts" },
  { id: "waiting", label: "waiting for approval" },
  { id: "members", label: "members" },
  { id: "stats", label: "stats" },
];

/**
 * The tabs a viewer is offered. Only the owner of a curated collective has
 * sessions waiting on them: the pending queue is owner-only on the server, and
 * only a curated collective holds anything in it.
 */
export function collectiveTabsFor(viewer: { role: string; acceptanceMode: string }): CollectiveTabDef[] {
  const reviews = viewer.role === "owner" && viewer.acceptanceMode === "curated";
  return COLLECTIVE_TABS.filter((tab) => tab.id !== "waiting" || reviews);
}

/** The tab the address asks for, when it is one this viewer is offered; else the first. */
export function collectiveTabFrom(param: string | null, offered: readonly CollectiveTabDef[]): CollectiveTabId {
  const found = offered.find((tab) => tab.id === param);
  return found ? found.id : offered[0]?.id ?? "transcripts";
}

// ── the settings page's sections ─────────────────────────────────────────────

export type SettingsSectionId = "general" | "access" | "members" | "github" | "pull-requests" | "danger";

/** The query parameter that names the open section. */
export const SECTION_PARAM = "section";

/** The query the GitHub App install handshake returns to the settings page with. */
export const INSTALLED_PARAM = "github_installed";

export interface SettingsSectionDef {
  id: SettingsSectionId;
  /** The section's name in the section list and its page title. */
  label: string;
}

export const SETTINGS_SECTIONS: readonly SettingsSectionDef[] = [
  { id: "general", label: "general" },
  { id: "access", label: "access" },
  { id: "members", label: "members" },
  { id: "github", label: "github" },
  { id: "pull-requests", label: "pull requests" },
  { id: "danger", label: "danger zone" },
];

/**
 * The section the address asks for. The install handshake's return lands on
 * `github`, where the repositories it made available are picked; anything
 * else unknown lands on `general`.
 */
export function settingsSectionFrom(params: { get(name: string): string | null }): SettingsSectionDef {
  if (params.get(INSTALLED_PARAM) === "1") return SETTINGS_SECTIONS.find((s) => s.id === "github")!;
  const asked = params.get(SECTION_PARAM);
  return SETTINGS_SECTIONS.find((s) => s.id === asked) ?? SETTINGS_SECTIONS[0];
}

/** The settings page's address for one section. */
export function settingsSectionHref(collectiveId: string, section: SettingsSectionId): string {
  const base = `/collectives/${collectiveId}/settings`;
  return section === "general" ? base : `${base}?${SECTION_PARAM}=${section}`;
}
