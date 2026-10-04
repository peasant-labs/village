"use client";

import type { ComponentProps } from "react";
import { Download, Eye, MoreHorizontal, Pencil } from "lucide-react";
import { CopyIconButton, Menu } from "@/lib/ft-ui";

type MenuItems = NonNullable<ComponentProps<typeof Menu>["items"]>;

/** How many collective names the menu spells out before it counts the rest. */
const ACCESS_NAMES_SHOWN = 2;

/** The link as it reads in the header: no scheme, and a long transcript id
 *  shortened to its first and last four characters. The full link is the
 *  anchor's `href` and its tooltip, and it is what the copy button copies. */
export function displayTranscriptLink(url: string): string {
  const bare = url.replace(/^https?:\/\//, "");
  const cut = bare.lastIndexOf("/");
  const id = bare.slice(cut + 1);
  if (cut < 0 || id.length <= 12) return bare;
  return `${bare.slice(0, cut + 1)}${id.slice(0, 4)}…${id.slice(-4)}`;
}

/** Who can read it now, as the menu states it: the first names, then a count
 *  of the rest. Null while the collectives are loading or when there are none. */
export function accessNamesLine(names: string[] | undefined): string | null {
  if (!names || names.length === 0) return null;
  const shown = names.slice(0, ACCESS_NAMES_SHOWN).join(", ");
  const more = names.length - ACCESS_NAMES_SHOWN;
  return `${shown}${more > 0 ? ` +${more}` : ""}`;
}

interface TranscriptHeaderActionsProps {
  /** The transcript's own village link. */
  url: string;
  /** The owner's rows. Absent for everyone else, who gets only the download. */
  owner?: {
    /** Names of the collectives that can read it now; undefined while loading. */
    accessNames: string[] | undefined;
    visibility: "private" | "shared" | "public";
    onManageAccess: () => void;
    onEditTitle: () => void;
  };
  onDownloadMarkdown: () => void;
  onDownloadJSON: () => void;
  onDownloadJSONL: () => void;
}

/**
 * The transcript page's header actions, as the fairtrade in-use demo composes
 * them: the link, an icon-only copy button, and a `more` menu. The menu holds
 * `manage access` and `edit title` for the owner, and markdown, JSON and JSONL downloads for
 * every reader.
 *
 * For the owner the menu also says who can read it now. fairtrade's menu sets
 * its rows and caption in lowercase, and collective names are user content that
 * keeps its case, so the names sit in the caption inside a span that keeps
 * their case, not in the `manage access` row where they would be lowercased.
 */
export default function TranscriptHeaderActions({ url, owner, onDownloadMarkdown, onDownloadJSON, onDownloadJSONL }: TranscriptHeaderActionsProps) {
  const items: MenuItems = [
    ...(owner
      ? [
          { label: "manage access", icon: Eye, onSelect: () => requestAnimationFrame(() => requestAnimationFrame(owner.onManageAccess)) },
          { label: "edit title", icon: Pencil, onSelect: () => requestAnimationFrame(() => requestAnimationFrame(owner.onEditTitle)) },
        ]
      : []),
    { label: "download markdown", icon: Download, onSelect: onDownloadMarkdown },
    { label: "download json", icon: Download, onSelect: onDownloadJSON },
    { label: "download jsonl", icon: Download, onSelect: onDownloadJSONL },
  ];

  const names = owner?.visibility === "public" ? "anyone with the link" : owner ? accessNamesLine(owner.accessNames) : null;
  const caption =
    names != null ? (
      <span data-testid="transcript-access-caption">
        who can read it: <span className="normal-case">{names}</span>
      </span>
    ) : undefined;

  return (
    <span className="cmg-transcript-actions" data-testid="transcript-header-actions">
      {/* The composite keeps its action row whole and lets the breadcrumb
          give way, so on a phone the link's text would squeeze the crumb to a
          sliver. Below `sm` the link hides and the copy button carries it. */}
      <a className="cmg-transcript-link mono max-sm:hidden" href={url} title={url} data-testid="transcript-link">
        {displayTranscriptLink(url)}
      </a>
      <CopyIconButton value={url} label="copy link" />
      <Menu icon={MoreHorizontal} ariaLabel="more" size="sm" align="end" items={items} caption={caption} />
    </span>
  );
}
