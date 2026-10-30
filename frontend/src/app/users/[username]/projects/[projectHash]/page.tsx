"use client";

import { use, useState, type ReactNode } from "react";
import Link from "next/link";
import {
  EyeOff,
  Library,
  Pencil,
  Users,
} from "lucide-react";
import {
  Button,
  DataState,
  Input,
  RailSection,
  RailShell,
} from "@/lib/ft-ui";
import TranscriptList from "@/components/transcript/TranscriptList";
import { SessionGroupDisclosure } from "@/lib/ft-ui";
import {
  ScopedContextContainerList,
  ScopedOwnerHelperGroups,
  helperGroupsByTranscript,
} from "@/components/transcript/ScopedHelperGroups";
import ScopedGroupedContinuation from "@/components/transcript/ScopedGroupedContinuation";
import { useAuth } from "@/providers/AuthProvider";
import {
  useClearProjectDisplayName,
  useSetProjectDisplayName,
  useUserProject,
} from "@/lib/queries/transcripts";
import { useGroupedTranscriptsPaged } from "@/lib/queries/helperGroups";
import { describeNameSource } from "@/lib/format";
import { isOwnedBy } from "@/lib/publicOwner";
import { childSessionsByParentID, groupProjectSessions } from "@/lib/childSessions";
import { isApiErrorStatus } from "@/lib/api";
import { HOME_SECTION } from "@/lib/nav/sections";
import { OnCrumb, OnEmpty, OnPage } from "@/components/online/Page";
import type {
  ProjectCollectiveRollupEntry,
  Transcript,
  TranscriptListItem,
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
  const { user, isLoggedIn } = useAuth();
  const { data, isLoading, error } = useUserProject(username, projectHash);
  // The server's own helper-group fold, scoped to this project and owner. It
  // rides beside the flat project list, whose rows, child chip and pagination
  // are unchanged; a grouped read that fails contributes no groups. The read
  // pages, so a project whose transcripts exceed one grouped page still reaches
  // every later owner's and context container's grouped exit.
  const grouped = useGroupedTranscriptsPaged({
    owner: username,
    project_hash: projectHash,
  });
  const groupedItems = grouped.items;
  const helperGroups = helperGroupsByTranscript(groupedItems);
  const refreshGrouped = grouped.refreshOrigin;
  const setName = useSetProjectDisplayName();
  const clearName = useClearProjectDisplayName();

  // `null` means "showing the server's answer". Any edit parks a draft here;
  // a completed set or clear drops it, so the field re-reads the resolved name
  // the server just returned rather than echoing what was typed.
  const [draft, setDraft] = useState<string | null>(null);
  const [orphansExpanded, setOrphansExpanded] = useState(false);

  const isOwner = !!data && isOwnedBy(data.owner, user?.id);

  // ── Not found ───────────────────────────────────────────────────────────────
  // ONE answer for every refusal this route can make. A missing user, a user who
  // has turned discoverability off, and a project neither of them owns are
  // deliberately indistinguishable here: the whole point of the boundary is that
  // the refusal does not confirm which case it was, so this must never grow a
  // friendlier, case-specific message.
  if (isApiErrorStatus(error, 404)) {
    return (
      <OnPage tight>
        <Crumbs username={username} current={null} signedIn={isLoggedIn} />
        {/* One zero state for every refusal, with the way back in its action
            slot. Its wording says nothing about WHICH refusal produced it. */}
        <OnEmpty
          headingLevel={1}
          icon={<EyeOff aria-hidden="true" />}
          title="project not found"
          body="peasant has no project page here that you can open."
          testId="project-page-not-found"
          actions={
            isLoggedIn ? (
              <Link href={HOME_SECTION.href} className="btn btn-secondary">
                back to home
              </Link>
            ) : undefined
          }
        />
      </OnPage>
    );
  }

  if (error) {
    return (
      <OnPage tight>
        <Crumbs username={username} current={null} signedIn={isLoggedIn} />
        <div className="on-notice" data-tone="danger" role="alert">
          <p>
            <strong className="text-[color:var(--ink-strong)]">this project page could not be loaded.</strong>{" "}
            The request to peasant failed before the project could be read, so nothing is
            shown rather than a partial page. Reload to try again.
          </p>
        </div>
      </OnPage>
    );
  }

  if (isLoading || !data) {
    return (
      <OnPage tight>
        <div className="h-4 w-40 animate-shimmer" />
        <div className="h-8 w-2/3 animate-shimmer" />
        <div className="h-24 w-full animate-shimmer" />
        <div className="h-96 w-full animate-shimmer" />
      </OnPage>
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
    owner: data.owner,
  }));

  // A project page is where a person asks what a session did, so a session that
  // another session started is listed under the genuine root of its complete
  // ancestry. Unresolved ancestry is kept reachable in one display-only group.
  //
  // The panel's own count still counts every transcript in the project,
  // including the ones inside a chip. It answers how much this project holds,
  // which is not changed by where a row is drawn.
  const grouping = groupProjectSessions(items);
  const childSessions = childSessionsByParentID(grouping);

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
        <span
          data-testid="project-transcript-count"
          className="font-mono text-sm text-ink-3 tabular-nums"
        >
          {data.transcripts.length.toLocaleString()}
        </span>
      </div>
      <DataState
        empty={data.transcripts.length === 0}
        emptyState={
          <div className="px-5 py-10">
            <h3 className="on-section-title">no transcripts to show</h3>
            <p className="on-muted">this project has no published sessions you can open.</p>
          </div>
        }
      >
        <TranscriptList
          items={grouping.rootItems}
          childSessions={childSessions}
          showOwnerActions={isOwner}
          hideOwner
          bare
          helperGroupSlot={(item) => (
            <ScopedOwnerHelperGroups
              groups={helperGroups.get(item.transcript.id)}
              onRefreshOrigin={refreshGrouped}
            />
          )}
        />
        <ScopedContextContainerList
          items={groupedItems}
          onRefreshOrigin={refreshGrouped}
        />
        {/* A grouped page holds at most one server page of top-level units; the
            continuation reads the next one so a later owner or helper-only
            container in this project still gets its grouped exit. */}
        <ScopedGroupedContinuation
          remaining={grouped.remainingItems}
          busy={grouped.isFetchingNextPage}
          onLoadMore={() => void grouped.fetchNextPage()}
        />
        {grouping.orphanItems.length > 0 && (
          <SessionGroupDisclosure
            label={`orphan sessions ${grouping.orphanItems.length}`}
            collapsedLabel={`orphan sessions ${grouping.orphanItems.length}`}
            expanded={orphansExpanded}
            onToggle={() => setOrphansExpanded((open) => !open)}
            rowsID="project-orphan-session-rows"
            testID="project-orphan-session-group"
            bare
            indent
          >
            <div
              id="project-orphan-session-rows"
              data-testid="project-orphan-session-rows"
              className="border-t border-rule"
            >
              <TranscriptList
                items={grouping.orphanItems}
                showOwnerActions={isOwner}
                hideOwner
                bare
              />
            </div>
          </SessionGroupDisclosure>
        )}
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
            <h3 className="on-section-title">no collectives to show</h3>
            <p className="on-muted">no collective you can see holds a transcript from this project.</p>
          </div>
        }
      >
        <ul className="divide-y divide-rule">
          {data.collectives.map((c: ProjectCollectiveRollupEntry) => (
            <li key={c.id} className="flex items-start gap-3 px-5 py-3">
              <div className="min-w-0 flex-1 flex flex-col gap-0.5">
                <Link
                  href={`/collectives/${c.id}`}
                  className="text-sm font-medium text-ink hover:text-ink-2 transition-colors focus-mono cursor-pointer truncate"
                >
                  {c.name}
                </Link>
                {c.description && (
                  <span className="text-[length:var(--fs-body)] text-ink-3 leading-relaxed line-clamp-2">
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
        <p className="text-[length:var(--fs-body)] text-ink-3 leading-relaxed">
          {hasOverride
            ? "Resetting removes your name and returns the project to the name peasant resolves from its transcripts."
            : "This project uses the name peasant resolves from its transcripts. There is nothing to reset."}
        </p>
        {mutationError && (
          <p role="alert" className="text-[length:var(--fs-body)] text-danger leading-relaxed">
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
    <OnPage tight testId="project-page">
      <Crumbs username={username} current={resolvedName} signedIn={isLoggedIn} />

      {/* Header: the project's name, and the repository it came from as its one
          subtitle. Both are USER CONTENT and are rendered exactly as stored:
          never lowercased, never re-derived on the client. The repository label
          carries no port, so two forges on the same host read alike by design. */}
      <header className="on-head">
        <div className="min-w-0 flex flex-col gap-1.5">
          {/* `normal-case` is load-bearing: the design system lowercases
              h1/h2/h3 as UI chrome, and a project's display name is USER
              CONTENT, which is never lowercased. */}
          <h1 data-testid="project-display-name" className="on-title normal-case">
            {resolvedName}
          </h1>
          {remoteLabel && (
            <p data-testid="project-remote-label" className="on-chrome m-0">
              {remoteLabel}
            </p>
          )}
        </div>
      </header>

      <dl className="on-stats on-profile-stats" aria-label="project in numbers">
        <div className="on-stat"><dt>transcripts</dt><dd>{data.transcripts.length.toLocaleString()}</dd></div>
        <div className="on-stat"><dt>collectives</dt><dd>{data.collectives.length.toLocaleString()}</dd></div>
      </dl>

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
        canvas
      )}
    </OnPage>
  );
}

/**
 * The path back: the reader's own home when they are signed in, then the
 * person whose project this is, then the project. A signed-out visitor has no
 * home here, so their trail starts at the person. Nothing here leads to the
 * discovery list, which is hidden.
 */
function Crumbs({
  username,
  current,
  signedIn,
}: {
  username: string;
  current: string | null;
  signedIn: boolean;
}) {
  const items: { label: ReactNode; href?: string }[] = [];
  if (signedIn) items.push({ label: HOME_SECTION.label, href: HOME_SECTION.href });
  items.push({ label: `@${username}`, href: `/users/${encodeURIComponent(username)}` });
  // The project's name is user content: it keeps its case. A project the page
  // could not open has no name to show, so the step is named generically.
  items.push({ label: current === null ? "project" : <span className="normal-case">{current}</span> });
  return <OnCrumb items={items} />;
}
