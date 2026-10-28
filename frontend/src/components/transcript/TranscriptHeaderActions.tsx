"use client";

import type { ComponentProps } from "react";
import { Download, Eye, MoreHorizontal, Pencil } from "lucide-react";
import { CopyIconButton, Menu } from "@/lib/ft-ui";

type MenuItems = NonNullable<ComponentProps<typeof Menu>["items"]>;

/** How many collective names the menu spells out before it counts the rest. */
const ACCESS_NAMES_SHOWN = 2;

/** Who can read it now, as the menu states it: the first names, then a count
 *  of the rest. Null while the collectives are loading or when there are none. */
export function accessNamesLine(names: string[] | undefined): string | null {
  if (!names || names.length === 0) return null;
  const shown = names.slice(0, ACCESS_NAMES_SHOWN).join(", ");
  const more = names.length - ACCESS_NAMES_SHOWN;
  return `${shown}${more > 0 ? ` +${more}` : ""}`;
}

interface TranscriptHeaderActionsProps {
  /** The transcript's own link, which the copy button copies. */
  url: string;
  /** Invoked only after the clipboard accepts the link. */
  onCopied?: () => void;
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
 * The transcript page's header actions: an icon-only copy-link button and a
 * `more` menu. The address itself is not spelled out; the button copies it. The menu holds
 * `manage access` and `edit title` for the owner, and markdown, JSON and JSONL downloads for
 * every reader.
 *
 * For the owner the menu also says who can read it now. fairtrade's menu sets
 * its rows and caption in lowercase, and collective names are user content that
 * keeps its case, so the names sit in the caption inside a span that keeps
 * their case, not in the `manage access` row where they would be lowercased.
 */
export default function TranscriptHeaderActions({ url, onCopied, owner, onDownloadMarkdown, onDownloadJSON, onDownloadJSONL }: TranscriptHeaderActionsProps) {
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
      <CopyIconButton value={url} label="copy link" onCopied={onCopied} />
      <Menu icon={MoreHorizontal} ariaLabel="more" size="sm" align="end" items={items} caption={caption} />
    </span>
  );
}
