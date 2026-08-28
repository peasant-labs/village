"use client";

import { use, useState } from "react";
import Link from "next/link";
import {
  ChevronRight,
  EyeOff,
  FileText,
  FolderOpen,
  Library,
  Pencil,
  Users,
} from "lucide-react";
import {
  Button,
  DataState,
  EmptyState,
  Input,
  RailSection,
  RailShell,
  StatGrid,
} from "@/lib/ft-ui";
import TranscriptList from "@/components/transcript/TranscriptList";
import { useAuth } from "@/providers/AuthProvider";
import {
  useClearProjectDisplayName,
  useSetProjectDisplayName,
  useUserProject,
} from "@/lib/queries/transcripts";
import { describeNameSource } from "@/lib/format";
import { isApiErrorStatus } from "@/lib/api";
import { EXPLORE_SECTION } from "@/lib/nav/sections";
import type {
  ProjectCollectiveRollupEntry,
  Transcript,
  TranscriptListItem,
  User,
} from "@/lib/types";

/** Longest display name the correction route accepts (village handler). */
const PROJECT_NAME_MAX = 255;

/**
 * The project page: `/users/{username}/projects/{projectHash}`.
 *
 * One project is a `(owner, project_hash)` pair, so this route is keyed on the
 * hash and never on a name. Everything it renders about identity — the display
 * name, the tier that name came from, the repository label — is resolved by the
 * server and read straight off the payload; the page derives none of it.
 */
export default function UserProjectPage({
  params,
}: {
  params: Promise<{ username: string; projectHash: string }>;
}) {
  const { username, projectHash } = use(params);
  const { user } = useAuth();
  const { data, isLoading, error } = useUserProject(username, projectHash);
  const setName = useSetProjectDisplayName();
  const clearName = useClearProjectDisplayName();

  // `null` means "showing the server's answer". Any edit parks a draft here;
  // a completed set or clear drops it, so the field re-reads the resolved name
  // the server just returned rather than echoing what was typed.
  const [draft, setDraft] = useState<string | null>(null);

  const isOwner =
    !!user &&
    !!data &&
    user.github_username.toLowerCase() === data.owner.github_username.toLowerCase();

  // ── Not found ───────────────────────────────────────────────────────────────
  // ONE answer for every refusal this route can make. A missing user, a user who
  // has turned discoverability off, and a project neither of them owns are
  // deliberately indistinguishable here: the whole point of the boundary is that
  // the refusal does not confirm which case it was, so this must never grow a
  // friendlier, case-specific message.
  if (isApiErrorStatus(error, 404)) {
    return (
      <div className="max-w-[1600px] mx-auto px-6 pt-6 pb-12 flex flex-col gap-6 animate-fade-up">
        <Crumbs username={username} current={null} />
        {/* The design system's own zero-state block, the same component the
            transcript and collectives panels use, with the way back in its
            action slot. Its wording stays lowercase chrome like its siblings
            and says nothing about WHICH refusal produced it. */}
        <div data-testid="project-page-not-found">
          <EmptyState
            icon={EyeOff}
            as="h3"
            title="project not found"
            message="Village has no project page here that you can open."
            action={
              <Link
                href={EXPLORE_SECTION.href}
                className="text-[13px] text-ink-3 hover:text-ink transition-colors focus-mono cursor-pointer"
              >
                back to commons
              </Link>
            }
          />
        </div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="max-w-[1600px] mx-auto px-6 pt-6 pb-12 flex flex-col gap-6 animate-fade-up">
        <Crumbs username={username} current={null} />
        <div className="border border-danger/40 bg-danger-soft px-5 py-6 text-sm text-danger">
          <p className="font-medium">This project page could not be loaded</p>
          <p className="mt-1 text-[13px]">
            The request to Village failed before the project could be read, so nothing is
            shown rather than showing a partial page. Reload to try again.
          </p>
        </div>
      </div>
    );
  }

  if (isLoading || !data) {
    return (
      <div className="max-w-[1600px] mx-auto px-6 pt-6 pb-12 flex flex-col gap-6">
        <div className="h-4 w-40 animate-shimmer" />
        <div className="h-8 w-2/3 animate-shimmer" />
        <div className="h-24 w-full animate-shimmer" />
        <div className="h-96 w-full animate-shimmer" />
      </div>
    );
  }

  const project = data.project;
  const resolvedName = project.project_display_name;
  const remoteLabel = project.project_remote_label;
  const hasOverride = project.project_name_source === "override";
  const nameValue = draft ?? resolvedName;
  const trimmedName = nameValue.trim();
  const pending = setName.isPending || clearName.isPending;
  const mutationError = setName.error ?? clearName.error;

  const items: TranscriptListItem[] = data.transcripts.map((t: Transcript) => ({
    transcript: t,
    tags: [],
    owner: data.owner as User,
  }));

  // Panels are <div>s, not <section>s. The design system styles the bare
  // `section` element itself (a capped max-width plus auto side margins), which
  // inside a flex column collapses a panel to its content width and centres it.
  // Every shipped panel in this app is a div for the same reason, and the
  // transcript list is embedded `bare` so it contributes no section of its own.
  const transcriptsPanel = (
    <div className="border border-rule bg-surface">
      <div className="flex items-center justify-between border-b border-rule px-5 py-3">
        <span className="inline-flex items-center gap-2 text-sm font-medium text-ink">
          <Library size={14} className="text-ink-3" />
          transcripts
        </span>
        <span className="font-mono text-sm text-ink-3 tabular-nums">
          {data.transcripts.length.toLocaleString()}
        </span>
      </div>
      <DataState
        empty={data.transcripts.length === 0}
        emptyState={
          <div className="px-5 py-10">
            <EmptyState
              icon={Library}
              as="h3"
              title="no transcripts to show"
              message="This project has no published sessions you can open."
            />
          </div>
        }
      >
        <TranscriptList items={items} showOwnerActions={isOwner} hideOwner bare />
      </DataState>
    </div>
  );

  // The roll-up is gated server-side by collective visibility AND the owner's
  // contributor opt-in, so an empty list is an ORDINARY answer for a viewer who
  // is not the owner. It renders as an ordinary empty state: no error, and no
  // wording that would confirm that memberships exist but are hidden.
  const collectivesPanel = (
    <div data-testid="project-collectives" className="border border-rule bg-surface">
      <div className="flex items-center justify-between border-b border-rule px-5 py-3">
        <span className="inline-flex items-center gap-2 text-sm font-medium text-ink">
          <Users size={14} className="text-ink-3" />
          collectives
        </span>
        <span className="font-mono text-sm text-ink-3 tabular-nums">
          {data.collectives.length.toLocaleString()}
        </span>
      </div>
      <DataState
        empty={data.collectives.length === 0}
        emptyState={
          <div className="px-5 py-10">
            <EmptyState
              icon={Users}
              as="h3"
              title="no collectives to show"
              message="No collective you can see holds a transcript from this project."
            />
          </div>
        }
      >
        <ul className="divide-y divide-rule">
          {data.collectives.map((c: ProjectCollectiveRollupEntry) => (
            <li key={c.id} className="flex items-start gap-3 px-5 py-3">
              <div className="min-w-0 flex-1 flex flex-col gap-0.5">
                <Link
                  href={`/groups/${c.id}`}
                  className="text-sm font-medium text-ink hover:text-ink-2 transition-colors focus-mono cursor-pointer truncate"
                >
                  {c.name}
                </Link>
                {c.description && (
                  <span className="text-[13px] text-ink-3 leading-relaxed line-clamp-2">
                    {c.description}
                  </span>
                )}
                {c.linked_github_org && (
                  <span className="font-mono text-sm text-ink-3 truncate">
                    {c.linked_github_org}
                  </span>
                )}
              </div>
              <span
                data-testid="collective-transcript-count"
                className="shrink-0 font-mono text-sm text-ink-3 tabular-nums"
              >
                {c.transcript_count.toLocaleString()}
              </span>
            </li>
          ))}
        </ul>
      </DataState>
    </div>
  );

  // ── Owner-only correction control ───────────────────────────────────────────
  // Rendered ONLY when the signed-in viewer is this project's owner. Both halves
  // are keyed on the project HASH: the set targets
  // PATCH /users/me/projects/{projectHash} and the clear targets
  // DELETE .../display-name. Neither sends a display name as a key, which is the
  // defect the hash-keyed routes replaced.
  const ownerRail = isOwner ? (
    <RailSection title="project name" icon={Pencil} meta={undefined}>
      <div data-testid="project-rename-control" className="flex flex-col gap-3">
        <Input
          label="display name"
          aria-label="project display name"
          value={nameValue}
          maxLength={PROJECT_NAME_MAX}
          disabled={pending}
          hint={describeNameSource(project.project_name_source)}
          onChange={(e) => setDraft(e.target.value)}
        />
        <div className="flex flex-wrap items-center gap-2">
          <Button
            size="sm"
            className="shrink-0"
            disabled={pending || trimmedName.length === 0 || trimmedName === resolvedName}
            onClick={() => {
              setName.mutate(
                { projectHash, displayName: trimmedName },
                { onSuccess: () => setDraft(null) },
              );
            }}
          >
            {setName.isPending ? "saving" : "save name"}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            className="shrink-0"
            disabled={pending || !hasOverride}
            onClick={() => {
              clearName.mutate(
                { projectHash },
                { onSuccess: () => setDraft(null) },
              );
            }}
          >
            {clearName.isPending ? "resetting" : "reset to default"}
          </Button>
        </div>
        <p className="text-[13px] text-ink-3 leading-relaxed">
          {hasOverride
            ? "Resetting removes your name and returns the project to the name Village resolves from its transcripts."
            : "This project uses the name Village resolves from its transcripts. There is nothing to reset."}
        </p>
        {mutationError && (
          <p role="alert" className="text-[13px] text-danger leading-relaxed">
            {mutationError.message}
          </p>
        )}
      </div>
    </RailSection>
  ) : null;

  const canvas = (
    <div className="flex flex-col gap-6">
      {transcriptsPanel}
      {collectivesPanel}
    </div>
  );

  return (
    <div className="max-w-[1600px] mx-auto px-6 pt-6 pb-12 flex flex-col gap-6 animate-fade-up">
      <Crumbs username={username} current={resolvedName} />

      {/* Header: the project's name, and the repository it came from as its one
          subtitle. Both are USER CONTENT and are rendered exactly as stored —
          never lowercased, never re-derived on the client. The repository label
          carries no port, so two forges on the same host read alike by design. */}
      <div className="flex flex-col gap-1.5">
        {/* `normal-case` is load-bearing: the design system lowercases h1/h2/h3
            as UI chrome, and a project's display name is USER CONTENT, which is
            never lowercased. */}
        <h1
          data-testid="project-display-name"
          className="font-[family-name:var(--font-display)] text-2xl font-semibold tracking-tight text-ink normal-case"
        >
          {resolvedName}
        </h1>
        {remoteLabel && (
          <p
            data-testid="project-remote-label"
            className="font-mono text-sm text-ink-3"
          >
            {remoteLabel}
          </p>
        )}
      </div>

      <StatGrid
        tiles={[
          {
            key: "transcripts",
            label: "transcripts",
            value: data.transcripts.length.toLocaleString(),
            icon: FileText,
          },
          {
            key: "collectives",
            label: "collectives",
            value: data.collectives.length.toLocaleString(),
            icon: Users,
          },
        ]}
      />

      {ownerRail ? (
        <RailShell
          toolbar={undefined}
          sheetMeta={undefined}
          sheetTitle="project settings"
          rail={ownerRail}
        >
          {canvas}
        </RailShell>
      ) : (
        // No rail for a viewer who is not the owner, so the canvas would run the
        // full page width. It is held to the design system's own content measure
        // instead: the transcript rows carry a `group` class that the design
        // system also styles as a capped layout group, so a wider container
        // leaves the rows centred inside their own panel.
        <div style={{ maxWidth: "var(--maxw)" }}>{canvas}</div>
      )}
    </div>
  );
}

/**
 * The path back. fairtrade's own Breadcrumb emits bare anchors, which would drop
 * client-side navigation on every crumb, so this mirrors the profile page's
 * next/link trail instead — the same markup, tokens and chevron, one route up.
 */
function Crumbs({ username, current }: { username: string; current: string | null }) {
  return (
    <nav aria-label="breadcrumb" className="flex items-center gap-1.5 text-xs">
      <Link
        href={EXPLORE_SECTION.href}
        className="text-ink-3 hover:text-ink transition-colors focus-mono cursor-pointer"
      >
        Commons
      </Link>
      <ChevronRight size={12} strokeWidth={2} className="text-ink-4" />
      <Link
        href={`/users/${encodeURIComponent(username)}`}
        className="text-ink-3 hover:text-ink transition-colors focus-mono cursor-pointer"
      >
        @{username}
      </Link>
      {current !== null && (
        <>
          <ChevronRight size={12} strokeWidth={2} className="text-ink-4" />
          <span className="text-ink font-medium inline-flex items-center gap-1.5">
            <FolderOpen size={12} className="text-ink-4" />
            {current}
          </span>
        </>
      )}
    </nav>
  );
}
