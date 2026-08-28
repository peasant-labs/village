"use client";

import Link from "next/link";
import { FolderOpen, Library, Plus } from "lucide-react";
import { useTranscripts } from "@/lib/queries/transcripts";
import { useAuth } from "@/providers/AuthProvider";
import TranscriptList from "@/components/transcript/TranscriptList";
import { DataState, TeachingEmptyState } from "@/lib/ft-ui";
import { groupByProject } from "@/lib/format";
import type { TranscriptListItem } from "@/lib/types";

/**
 * The signed-in landing surface: the caller's own recent sessions, then the
 * projects those sessions belong to.
 *
 * It reads the SAME owner-scoped list the profile page reads
 * (`useTranscripts({ owner })`) and groups it with the SAME `groupByProject`,
 * so a person's projects can never be described one way here and another way
 * on their profile. No new backend route is involved.
 *
 * Deliberately carries no stat tiles: this page answers "what was I working
 * on, and where do I continue" — a count of transcripts answers neither.
 */

/** How many recent sessions the top list shows before the project list takes over. */
const RECENT_SESSION_LIMIT = 5;

/**
 * Most recently published first.
 *
 * The order is applied here rather than trusted from the response because the
 * owner-scoped list request carries no explicit order parameter, so "recent"
 * would otherwise be a server default this page silently depends on.
 * `published_at` is an ISO-8601 UTC timestamp, so lexicographic comparison is
 * chronological; a value that does not parse sorts last instead of throwing.
 */
export function mostRecentFirst(
  items: TranscriptListItem[],
): TranscriptListItem[] {
  return [...items].sort((a, b) => {
    const at = Date.parse(a.transcript.published_at);
    const bt = Date.parse(b.transcript.published_at);
    if (Number.isNaN(at) && Number.isNaN(bt)) return 0;
    if (Number.isNaN(at)) return 1;
    if (Number.isNaN(bt)) return -1;
    return bt - at;
  });
}

export default function HomePage() {
  const { user } = useAuth();
  const username = user?.github_username ?? "";
  const { data, isLoading } = useTranscripts({ owner: username });

  const items = data?.transcripts ?? [];
  const recent = mostRecentFirst(items).slice(0, RECENT_SESSION_LIMIT);
  const { groups, malformed } = groupByProject(items);

  // One teaching empty state serves both sections: a person with no sessions
  // has no projects either, so two empty panels would say the same thing twice.
  const emptyState = (
    <div data-testid="home-empty-state">
      <TeachingEmptyState
        icon={FolderOpen}
        title="nothing published yet"
        body="publish a redacted transcript to start your library. your sessions and the projects they belong to appear here."
        privacy={null}
        style={{ border: "none", background: "transparent" }}
      />
      <div className="px-6 pb-6">
        <Link
          href="/publish"
          className="inline-flex items-center gap-1.5 text-[13px] font-medium text-ink hover:text-ink-2 transition-colors focus-mono cursor-pointer"
        >
          <Plus size={14} />
          Publish your first transcript
        </Link>
      </div>
    </div>
  );

  if (isLoading) {
    return (
      <div className="max-w-[1600px] mx-auto px-6 pt-6 pb-12 flex flex-col gap-6 animate-fade-up">
        <div className="h-8 w-64 animate-shimmer" />
        <div className="h-48 animate-shimmer" />
        <div className="h-48 animate-shimmer" />
      </div>
    );
  }

  return (
    <div
      className="max-w-[1600px] mx-auto px-6 pt-6 pb-12 flex flex-col gap-6 animate-fade-up"
      data-testid="home-page"
    >
      <h1 className="font-[family-name:var(--font-display)] text-2xl font-semibold tracking-tight text-ink">
        home
      </h1>

      {malformed.length > 0 && (
        // `project_hash` is a required identity column, so a row without one is
        // a backend contract violation rather than an ordinary empty state. It
        // is reported and skipped; every well-formed project still renders.
        <div
          role="alert"
          data-testid="home-malformed-notice"
          className="border border-danger/40 bg-danger-soft px-4 py-3 text-sm text-danger"
        >
          <p className="font-medium">
            {malformed.length} transcript{malformed.length !== 1 ? "s" : ""}{" "}
            could not be grouped by project
          </p>
          <p className="mt-1 text-[13px]">
            Each is missing the project identity the server is expected to
            always provide. They are omitted from the project list below; the
            rest of this page is unaffected.
          </p>
        </div>
      )}

      <DataState empty={items.length === 0} emptyState={emptyState}>
        {/* The two panels carry their own spacing: DataState wraps its children,
            so the page column's gap does not fall between them. */}
        <div className="flex flex-col gap-6">
          {/* Plain `div`s, not `section`s: the design system styles a bare
            `section` with its own max-width and centring, which would inset
            these panels inside the page's own width instead of filling it. */}
          <div
            className="border border-rule bg-surface"
            data-testid="home-recent-sessions"
          >
            <div className="flex items-center justify-between px-5 py-3 border-b border-rule">
              <span className="inline-flex items-center gap-2 text-sm font-medium text-ink">
                <Library size={14} className="text-ink-3" />
                your recent sessions
              </span>
            </div>
            <TranscriptList items={recent} showOwnerActions hideOwner bare />
          </div>

          <div
            className="border border-rule bg-surface"
            data-testid="home-projects"
          >
            <div className="flex items-center justify-between px-5 py-3 border-b border-rule">
              <span className="inline-flex items-center gap-2 text-sm font-medium text-ink">
                <FolderOpen size={14} className="text-ink-3" />
                your projects
              </span>
            </div>
            <ul className="divide-y divide-rule">
              {groups.map((group) => (
                <li key={group.project_hash}>
                  {/* Routed on `project_hash`: a project's identity is the hash,
                    never a display name that could be re-derived. */}
                  <Link
                    href={`/users/${encodeURIComponent(username)}/projects/${group.project_hash}`}
                    data-testid="home-project-row"
                    className="flex items-center gap-3 px-5 py-3 hover:bg-surface-hover transition-colors focus-mono cursor-pointer"
                  >
                    {/* A project's display name is USER CONTENT, so `normal-case`
                      overrides the design system's chrome lowercasing. */}
                    <span className="font-[family-name:var(--font-display)] text-sm font-semibold text-ink truncate normal-case">
                      {group.project}
                    </span>
                    <span className="font-mono text-xs text-ink-3 tabular-nums shrink-0">
                      {group.items.length} session
                      {group.items.length !== 1 ? "s" : ""}
                    </span>
                  </Link>
                </li>
              ))}
            </ul>
          </div>
        </div>
      </DataState>
    </div>
  );
}
