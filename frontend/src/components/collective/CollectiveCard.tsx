"use client";

// Deprecated compatibility component superseded by the shared fairtrade
// <Explore> surface, which renders its own collective and transcript cards.
// It has no remaining import sites.

import Link from "next/link";
import { Users, Building2 } from "lucide-react";
import type { CollectiveSearchResult } from "@/lib/types";

export default function CollectiveCard({
  collective,
}: {
  collective: CollectiveSearchResult;
}) {
  return (
    <Link
      href={`/groups/${collective.id}`}
      className="block border border-rule bg-surface hover:bg-surface-hover transition-colors focus-mono cursor-pointer p-4 flex flex-col gap-2"
    >
      {/* Header row */}
      <div className="flex items-center gap-2 min-w-0">
        <div className="flex h-6 w-6 shrink-0 items-center justify-center border border-rule bg-surface-hover">
          <Users className="h-3.5 w-3.5 text-ink-3" strokeWidth={1.75} />
        </div>
        <span className="font-[family-name:var(--font-display)] text-sm tracking-tight text-ink truncate">
          {collective.name}
        </span>
      </div>

      {/* Description */}
      {collective.description && (
        <p className="text-[12px] text-ink-3 leading-relaxed line-clamp-2">
          {collective.description}
        </p>
      )}

      {/* Footer row */}
      <div className="flex items-center gap-2 flex-wrap">
        <span className="font-mono tabular-nums text-[11px] text-ink-3">
          {collective.member_count} members &middot; {collective.transcript_count} transcripts
        </span>
        {collective.linked_github_org && (
          <span className="inline-flex items-center gap-1 border border-rule px-1.5 py-0.5 font-mono text-[10px] text-ink-3">
            <Building2 className="h-2.5 w-2.5" strokeWidth={1.75} />
            @{collective.linked_github_org}
          </span>
        )}
      </div>
    </Link>
  );
}
