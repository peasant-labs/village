"use client";

// prior-version, deprecation candidate (V-SHELL): superseded by the shared
// fairtrade <Explore> surface (V-EXPLORE, page.tsx bcb19c7), which renders its own
// .cex-tcard transcript cards. No remaining import sites. Soft-retained per the
// SOFT-RETIRE POLICY (soft-retire-not-delete policy) rather than deleted.

import Link from "next/link";
import { ShieldCheck } from "lucide-react";
import { ProviderTag, Tag, VisibilityEye } from "@/lib/ft-ui";
import { isHarness } from "@/lib/harness";
import type { TranscriptListItem } from "@/lib/types";
import { extractProjectDisplayName, resolveAttribution } from "@/lib/format";
import { useAuth } from "@/providers/AuthProvider";

function formatDuration(ms: number): string {
  const mins = Math.floor(ms / 60000);
  if (mins < 60) return `${mins}min`;
  const hrs = Math.floor(mins / 60);
  const remainMins = mins % 60;
  return remainMins > 0 ? `${hrs}h ${remainMins}m` : `${hrs}h`;
}

export default function TranscriptCard({ item }: { item: TranscriptListItem }) {
  const { transcript: t, tags, owner, shares, attestations } = item;
  const { user: viewer } = useAuth();
  const attribution = resolveAttribution(owner, viewer?.id);
  const projectDisplay = extractProjectDisplayName(t.project_name, t.git_remote);

  return (
    <Link
      href={`/transcripts/${t.id}`}
      className="group block border border-rule bg-surface px-5 py-4 transition-colors hover:bg-surface-hover focus-mono cursor-pointer"
    >
      {/* Header: model + project + visibility */}
      <div className="flex items-center justify-between gap-2 mb-3">
        <div className="flex items-center gap-2 min-w-0">
          {isHarness(t.model_provider) ? (
            <ProviderTag harness={t.model_provider} className="shrink-0" />
          ) : (
            <Tag className="shrink-0">{t.model_provider}</Tag>
          )}
          {projectDisplay && (
            <span className="text-[11px] font-mono text-ink-3 truncate">
              {projectDisplay}
            </span>
          )}
        </div>
        <VisibilityEye
          visibility={t.visibility}
          sharedWith={
            shares && shares.length > 0
              ? shares.map((s) => s.group_name).join(", ")
              : undefined
          }
        />
      </div>

      {/* Title */}
      <h3 className="font-[family-name:var(--font-display)] text-base text-ink leading-snug tracking-tight group-hover:text-ink line-clamp-2">
        {t.title || "Untitled transcript"}
      </h3>

      {/* Description */}
      {t.description && (
        <p className="text-sm text-ink-3 mt-1.5 line-clamp-2 leading-relaxed">
          {t.description}
        </p>
      )}

      {/* Tags */}
      {tags.length > 0 && (
        <div className="flex flex-wrap gap-1.5 mt-3">
          {tags.map((tag) => (
            <Tag key={tag.id}>{tag.name}</Tag>
          ))}
        </div>
      )}

      {/* Collective membership */}
      {shares && shares.length > 0 && (
        <div className="flex items-center flex-wrap gap-1.5 mt-2 text-[11px] font-mono text-ink-4">
          {shares.slice(0, 3).map((s, i) => (
            <span key={s.group_id}>
              {i > 0 && <span className="mx-1">&middot;</span>}
              {s.group_name}
            </span>
          ))}
        </div>
      )}

      {/* Footer: author + stats */}
      <div className="flex items-center justify-between mt-4 pt-3 border-t border-rule">
        <div className="flex items-center gap-2">
          {attribution.anonymous ? (
            <div className="w-5 h-5 bg-surface-hover border border-rule flex items-center justify-center text-[9px] font-bold text-ink-3">
              ?
            </div>
          ) : owner.avatar_url ? (
            <img
              src={owner.avatar_url}
              alt=""
              className="w-5 h-5 border border-rule"
            />
          ) : (
            <div className="w-5 h-5 bg-surface-hover border border-rule flex items-center justify-center text-[9px] font-bold text-ink-2">
              {owner.github_username[0].toUpperCase()}
            </div>
          )}
          <span className="text-xs text-ink-3">{attribution.label}</span>
        </div>

        <div className="flex items-center gap-3 text-[11px] font-mono text-ink-4 tabular-nums">
          {t.duration_ms != null && <span>{formatDuration(t.duration_ms)}</span>}
          {t.tool_call_count != null && <span>{t.tool_call_count} tools</span>}
          {t.turn_count != null && <span>{t.turn_count} turns</span>}
          {attestations && attestations.length > 0 && (
            <span className="flex items-center gap-1 text-success">
              <ShieldCheck size={11} strokeWidth={1.75} />
              {attestations.length}
            </span>
          )}
          <span>
            {new Date(t.published_at).toLocaleDateString("en-US", {
              month: "short",
              day: "numeric",
            })}
          </span>
        </div>
      </div>
    </Link>
  );
}
