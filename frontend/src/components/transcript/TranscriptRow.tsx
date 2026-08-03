"use client";

// Deprecated compatibility component for the former transcript list, superseded
// by the shared fairtrade <Explore> surface. It has no remaining import sites.

import Link from "next/link";
import { ProviderName, Tag, VisibilityEye } from "@/lib/ft-ui";
import { isHarness } from "@/lib/harness";
import type { TranscriptListItem } from "@/lib/types";
import { resolveAttribution } from "@/lib/format";
import { useAuth } from "@/providers/AuthProvider";

function formatDuration(ms: number): string {
  const mins = Math.floor(ms / 60000);
  if (mins < 60) return `${mins}min`;
  const hrs = Math.floor(mins / 60);
  const remainMins = mins % 60;
  return remainMins > 0 ? `${hrs}h ${remainMins}m` : `${hrs}h`;
}

export default function TranscriptRow({ item }: { item: TranscriptListItem }) {
  const { transcript: t, owner, shares } = item;
  const { user: viewer } = useAuth();
  const attribution = resolveAttribution(owner, viewer?.id);

  return (
    <Link
      href={`/transcripts/${t.id}`}
      className="group flex items-center gap-3 px-5 py-3 transition-colors hover:bg-surface-hover focus-mono cursor-pointer"
    >
      {isHarness(t.model_provider) ? (
        <ProviderName harness={t.model_provider} className="shrink-0" />
      ) : (
        <Tag className="shrink-0">{t.model_provider}</Tag>
      )}

      <span className="text-sm text-ink-3 font-mono tabular-nums shrink-0">
        {new Date(t.published_at).toLocaleDateString("en-US", {
          month: "short",
          day: "numeric",
          year: "numeric",
        })}
        <span className="text-rule mx-1.5">&middot;</span>
        {new Date(t.published_at).toLocaleTimeString("en-US", {
          hour: "numeric",
          minute: "2-digit",
        })}
      </span>

      <div className="flex items-center gap-3 text-[11px] font-mono text-ink-4 tabular-nums">
        {t.duration_ms != null && <span>{formatDuration(t.duration_ms)}</span>}
        {t.turn_count != null && <span>{t.turn_count} turns</span>}
        {t.tool_call_count != null && <span>{t.tool_call_count} tools</span>}
      </div>

      <div className="flex-1" />

      <div className="flex items-center gap-2">
        {attribution.anonymous ? (
          <div className="w-4 h-4 bg-surface-hover border border-rule flex items-center justify-center text-[8px] font-bold text-ink-3">
            ?
          </div>
        ) : owner.avatar_url ? (
          <img
            src={owner.avatar_url}
            alt=""
            className="w-4 h-4 border border-rule"
          />
        ) : (
          <div className="w-4 h-4 bg-surface-hover border border-rule flex items-center justify-center text-[8px] font-bold text-ink-2">
            {owner.github_username[0].toUpperCase()}
          </div>
        )}
        <span className="text-xs text-ink-3">{attribution.label}</span>
      </div>

      <VisibilityEye
        visibility={t.visibility}
        sharedWith={
          shares && shares.length > 0
            ? shares.map((s) => s.group_name).join(", ")
            : undefined
        }
      />
    </Link>
  );
}
