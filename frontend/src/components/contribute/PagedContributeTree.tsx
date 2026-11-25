"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ChevronRight } from "lucide-react";
import type { VillageContributionProjectSummary } from "@peasant-labs/schema";
import { Button, SessionGroupDisclosure, Tag } from "@/lib/ft-ui";
import { childSessionGroupLabel, childSessionGroupSelectionLabel } from "@/lib/childSessions";
import {
  classifyContributionReadError,
  ContributionRead,
  ContributionReadFailureKind,
  fetchContributionSnapshot,
  useContributionPages,
  type ContributionFilters,
  type ContributionReadFailure,
} from "@/lib/queries/contributionReads";
import {
  allSelected,
  deselectIds,
  familyState,
  isLiveStatus,
  isLockedRow,
  loadedRows,
  mergeSnapshot,
  selectedInProject,
  toggleFamily,
  type FrozenSelection,
  type LoadedRow,
} from "@/lib/contribute/frozenSelection";
import { pagedRowMeta, sectionBranches, type PagedBranch, type PagedFamily } from "@/lib/contribute/pagedTree";
import RequestFailureState from "@/components/RequestFailureState";
import { ChildSessionDisclosureTestId } from "@/components/transcript/childSessionDisclosureTestId";
import TriStateCheckbox from "./TriStateCheckbox";
import { ContributeLabel } from "./contributeLabel";
import { TreeRail, useTreeRail, type RailRow } from "./treeRail";

export type SelectionUpdate = (update: (previous: FrozenSelection) => FrozenSelection) => void;

interface PagedContributeTreeProps {
  groupId: string;
  filters: ContributionFilters;
  projects: readonly VillageContributionProjectSummary[];
  selection: FrozenSelection;
  onSelectionChange: SelectionUpdate;
  previewId: string | null;
  onPreview: (transcriptId: string) => void;
  /** Every section reports the rows it has loaded, so the page can name them
   *  and notice a selected row that was contributed meanwhile. */
  onLoadedRows: (projectHash: string, rows: LoadedRow[]) => void;
}

/** Connector columns inside one open project: branch, root session, helper. */
const RAIL_DEPTH = { branch: 0, root: 1, helper: 2 } as const;

const plural = (count: number, noun: string) => `${count} ${noun}${count === 1 ? "" : "s"}`;

function SessionRowView({
  row,
  family,
  selection,
  onSelectionChange,
  previewId,
  onPreview,
  setRowElement,
}: {
  row: LoadedRow;
  /** The rows this checkbox acts on: a root's whole family, or the helper alone. */
  family: readonly LoadedRow[];
  selection: FrozenSelection;
  onSelectionChange: SelectionUpdate;
  previewId: string | null;
  onPreview: (transcriptId: string) => void;
  setRowElement: (key: string, element: HTMLElement | null) => void;
}) {
  const id = row.transcript.id;
  const label = row.transcript.title ?? "untitled session";
  const locked = isLockedRow(row);
  const isActive = previewId === id;
  const rowRef = useCallback((element: HTMLDivElement | null) => setRowElement(`s:${id}`, element), [id, setRowElement]);
  // A root whose own row is live can still carry helpers that are not, so it
  // is disabled only when nothing in its family can be selected.
  const disabled = family.every(isLockedRow);
  return (
    <div
      ref={rowRef}
      className={`contribute-session-row flex items-center gap-3 px-4 py-3 transition-colors ${
        isActive ? "bg-surface-hover" : "hover:bg-surface-hover"
      }`}
      data-preview-active={isActive || undefined}
      data-testid={`contribute-session-row-${id}`}
    >
      <TriStateCheckbox
        state={familyState(selection, family)}
        disabled={disabled}
        onChange={() => onSelectionChange((previous) => toggleFamily(previous, family))}
        label={`select session ${label}`}
      />
      <div className="min-w-0 flex-1">
        <div className="contribute-session-heading flex items-center gap-3">
          <button
            type="button"
            aria-pressed={isActive}
            onClick={() => onPreview(id)}
            className={`min-w-0 flex-1 text-sm text-ink truncate text-left cursor-pointer focus-mono ${
              row.transcript.title == null ? "italic text-ink-3" : ""
            }`}
          >
            {label}
          </button>
          {locked && <Tag className="contribute-session-mark shrink-0">{ContributeLabel.AlreadyContributed.wire}</Tag>}
        </div>
        <div className="font-mono text-[length:var(--fs-label)] text-ink-3 tabular-nums [overflow-wrap:anywhere]">
          {pagedRowMeta(row)}
        </div>
      </div>
    </div>
  );
}

function FamilyView({
  family,
  open,
  onToggleOpen,
  ...rowProps
}: {
  family: PagedFamily;
  open: boolean;
  onToggleOpen: () => void;
  selection: FrozenSelection;
  onSelectionChange: SelectionUpdate;
  previewId: string | null;
  onPreview: (transcriptId: string) => void;
  setRowElement: (key: string, element: HTMLElement | null) => void;
}) {
  const rootId = family.root.transcript.id;
  const rowsId = `contribute-child-sessions-${rootId}`;
  return (
    <div>
      <SessionRowView row={family.root} family={family.rows} {...rowProps} />
      {family.helpers.length > 0 && (
        <div data-parent-transcript-id={rootId}>
          <SessionGroupDisclosure
            label={childSessionGroupLabel(family.helpers.length)}
            collapsedLabel={childSessionGroupSelectionLabel(
              family.helpers.length,
              family.helpers.filter((helper) => rowProps.selection.has(helper.transcript.id)).length,
            )}
            expanded={open}
            onToggle={onToggleOpen}
            rowsID={rowsId}
            testID={ChildSessionDisclosureTestId.Root.wire}
            bare
            indent
          >
            <div id={rowsId} data-testid={ChildSessionDisclosureTestId.Rows.wire} className="contribute-subtree">
              {family.helpers.map((helper) => (
                <SessionRowView key={helper.transcript.id} row={helper} family={[helper]} {...rowProps} />
              ))}
            </div>
          </SessionGroupDisclosure>
        </div>
      )}
    </div>
  );
}

function BranchView({
  branch,
  projectHash,
  openFamilies,
  onToggleFamily,
  setRowElement,
  ...rowProps
}: {
  branch: PagedBranch;
  projectHash: string;
  openFamilies: ReadonlySet<string>;
  onToggleFamily: (rootId: string) => void;
  selection: FrozenSelection;
  onSelectionChange: SelectionUpdate;
  previewId: string | null;
  onPreview: (transcriptId: string) => void;
  setRowElement: (key: string, element: HTMLElement | null) => void;
}) {
  const label = `branch · ${branch.label}`;
  const key = `b:${projectHash}:${branch.key}`;
  const headingRef = useCallback((element: HTMLHeadingElement | null) => setRowElement(key, element), [key, setRowElement]);
  return (
    <div>
      <h3
        ref={headingRef}
        className="flex items-center gap-3 px-4 py-2 font-mono text-sm text-ink-3"
        data-testid={`contribute-branch-row-${projectHash}-${branch.key}`}
      >
        {/* The branch checkbox acts on the LOADED sessions only; "select all"
            on the project is what takes every matching session. */}
        <TriStateCheckbox
          state={familyState(rowProps.selection, branch.rows)}
          disabled={branch.rows.every(isLockedRow)}
          onChange={() => rowProps.onSelectionChange((previous) => toggleFamily(previous, branch.rows))}
          label={`select the loaded sessions on ${label}`}
        />
        <span className="truncate normal-case">{label}</span>
      </h3>
      <div className="contribute-subtree">
        {branch.families.map((family) => (
          <FamilyView
            key={family.root.transcript.id}
            family={family}
            open={openFamilies.has(family.root.transcript.id)}
            onToggleOpen={() => onToggleFamily(family.root.transcript.id)}
            setRowElement={setRowElement}
            {...rowProps}
          />
        ))}
      </div>
    </div>
  );
}

function failureMessage(failure: ContributionReadFailure): string {
  switch (failure.kind) {
    case ContributionReadFailureKind.Deployment:
    case ContributionReadFailureKind.Failed:
      return failure.message;
    case ContributionReadFailureKind.TooLarge:
      return "this project has too many matching sessions to read at once. narrow the search and try again.";
    case ContributionReadFailureKind.Busy:
      return "the server is busy. try again in a moment.";
    case ContributionReadFailureKind.Cancelled:
      return "the read was cancelled.";
  }
}

/** The loaded families of one open project. */
function ProjectBody({
  groupId,
  project,
  filters,
  selection,
  onSelectionChange,
  previewId,
  onPreview,
  onLoadedRows,
}: Omit<PagedContributeTreeProps, "projects"> & { project: VillageContributionProjectSummary }) {
  const projectHash = project.project_hash;
  const pageFilters = useMemo(() => ({ ...filters, project_hash: projectHash }), [filters, projectHash]);
  const pages = useContributionPages(groupId, pageFilters);
  const { items } = pages;
  const rows = useMemo(() => loadedRows(items), [items]);
  const branches = useMemo(() => sectionBranches(items), [items]);
  const [openFamilies, setOpenFamilies] = useState<ReadonlySet<string>>(new Set());

  useEffect(() => {
    onLoadedRows(projectHash, rows);
  }, [onLoadedRows, projectHash, rows]);

  const railRows = useMemo<RailRow[]>(() => {
    const ordered: RailRow[] = [];
    for (const branch of branches) {
      ordered.push({ key: `b:${projectHash}:${branch.key}`, depth: RAIL_DEPTH.branch });
      for (const family of branch.families) {
        ordered.push({ key: `s:${family.root.transcript.id}`, depth: RAIL_DEPTH.root });
        if (!openFamilies.has(family.root.transcript.id)) continue;
        for (const helper of family.helpers) ordered.push({ key: `s:${helper.transcript.id}`, depth: RAIL_DEPTH.helper });
      }
    }
    return ordered;
  }, [branches, openFamilies, projectHash]);
  const { containerRef, setRowElement, path } = useTreeRail(railRows, [selection, previewId]);

  const toggleOpenFamily = useCallback((rootId: string) => {
    setOpenFamilies((before) => {
      const next = new Set(before);
      if (next.has(rootId)) next.delete(rootId);
      else next.add(rootId);
      return next;
    });
  }, []);

  if (pages.isLoading) {
    return (
      <div className="flex flex-col gap-2 px-4 py-3" aria-busy="true" data-testid={`contribute-project-loading-${projectHash}`}>
        <span className="sr-only">loading sessions</span>
        <div className="h-10 w-full bg-surface-hover animate-shimmer" />
        <div className="h-10 w-full bg-surface-hover animate-shimmer" />
      </div>
    );
  }

  // A failed later page keeps the rows already loaded and retries that page.
  const failure = pages.failure && pages.failure.kind !== ContributionReadFailureKind.Cancelled ? pages.failure : null;
  const failedLater = failure != null && items.length > 0;

  return (
    <div className="contribute-subtree" data-testid={`contribute-project-body-${projectHash}`}>
      {failure && !failedLater && (
        <RequestFailureState
          title="this project's sessions could not load"
          headingLevel={3}
          message={failureMessage(failure)}
          retryLabel="retry this project"
          retryDisabled={pages.isFetching}
          onRetry={() => void pages.refresh()}
        />
      )}
      {!failure && items.length === 0 && (
        <p className="px-4 py-4 text-sm text-ink-3">no sessions in this project match any more. refresh to update the counts.</p>
      )}
      {branches.length > 0 && (
        <TreeRail containerRef={containerRef} path={path}>
          {branches.map((branch) => (
            <BranchView
              key={branch.key}
              branch={branch}
              projectHash={projectHash}
              openFamilies={openFamilies}
              onToggleFamily={toggleOpenFamily}
              selection={selection}
              onSelectionChange={onSelectionChange}
              previewId={previewId}
              onPreview={onPreview}
              setRowElement={setRowElement}
            />
          ))}
        </TreeRail>
      )}
      {items.length > 0 && (
        <div className="flex flex-wrap items-center justify-between gap-3 px-4 py-3">
          <p className="font-mono text-[length:var(--fs-label)] tabular-nums text-ink-3" data-testid={`contribute-project-loaded-${projectHash}`}>
            loaded {rows.length} of {plural(project.matching_count, "session")}
          </p>
          {pages.hasNextPage && !failedLater && (
            <Button size="sm" variant="secondary" loading={pages.isFetchingNextPage} disabled={pages.isFetchingNextPage} onClick={() => void pages.fetchNextPage()}>
              load more sessions
            </Button>
          )}
        </div>
      )}
      {failedLater && (
        <RequestFailureState
          title="more sessions could not load"
          headingLevel={3}
          message={failureMessage(failure)}
          retryLabel="retry loading more"
          retryDisabled={pages.isFetching}
          onRetry={() => void pages.fetchNextPage()}
        />
      )}
    </div>
  );
}

interface SnapshotTaken {
  filtersKey: string;
  /** Every id the snapshot listed, selectable or live. */
  listed: ReadonlySet<string>;
  /** The ids "select all" took: the deselect control clears exactly these. */
  selectableIds: string[];
}

/**
 * "select all" on a project reads the exact set of its matching sessions and
 * adds the selectable ones in one update. The selection is then frozen: a
 * session that matches later never joins it, and one selected earlier that
 * the snapshot does not list stays selected and is reported.
 */
function useProjectSnapshot(
  groupId: string,
  projectHash: string,
  filters: ContributionFilters,
  selection: FrozenSelection,
  onSelectionChange: SelectionUpdate,
) {
  const filtersKey = JSON.stringify([filters.q ?? "", filters.provider ?? ""]);
  const [taken, setTaken] = useState<SnapshotTaken | null>(null);
  const [failure, setFailure] = useState<{ filtersKey: string; failure: ContributionReadFailure } | null>(null);
  const [loading, setLoading] = useState(false);
  const controller = useRef<AbortController | null>(null);

  // A new search makes an earlier snapshot answer a different question.
  useEffect(() => () => controller.current?.abort(), [filtersKey]);

  const current = taken?.filtersKey === filtersKey ? taken : null;
  const currentFailure = failure?.filtersKey === filtersKey ? failure.failure : null;
  const covered = current != null && allSelected(selection, current.selectableIds);
  const omitted = useMemo(() => {
    if (!current) return 0;
    let count = 0;
    for (const record of selection.values()) {
      if (record.family_project_hash === projectHash && !current.listed.has(record.id)) count += 1;
    }
    return count;
  }, [current, projectHash, selection]);

  const selectAll = async () => {
    controller.current?.abort();
    const abort = new AbortController();
    controller.current = abort;
    setLoading(true);
    setFailure(null);
    try {
      const snapshot = await fetchContributionSnapshot(groupId, projectHash, filters, abort.signal);
      onSelectionChange((previous) => mergeSnapshot(previous, snapshot).selection);
      setTaken({
        filtersKey,
        listed: new Set(snapshot.records.map((record) => record.id)),
        selectableIds: snapshot.records.filter((record) => !isLiveStatus(record.latest_status)).map((record) => record.id),
      });
    } catch (err) {
      const classified = classifyContributionReadError(err, ContributionRead.Snapshot);
      if (classified.kind !== ContributionReadFailureKind.Cancelled) setFailure({ filtersKey, failure: classified });
    } finally {
      if (controller.current === abort) {
        controller.current = null;
        setLoading(false);
      }
    }
  };

  const deselectAll = () => {
    if (current) onSelectionChange((previous) => deselectIds(previous, current.selectableIds));
  };

  return { covered, omitted, failure: currentFailure, loading, selectAll, deselectAll };
}

function ProjectSection({
  project,
  open,
  onToggleOpen,
  ...bodyProps
}: Omit<PagedContributeTreeProps, "projects"> & {
  project: VillageContributionProjectSummary;
  open: boolean;
  onToggleOpen: () => void;
}) {
  const { groupId, filters, selection, onSelectionChange } = bodyProps;
  const projectHash = project.project_hash;
  const name = project.project_display_name;
  const selected = selectedInProject(selection, projectHash);
  const live = project.matching_count - project.selectable_count;
  const snapshot = useProjectSnapshot(groupId, projectHash, filters, selection, onSelectionChange);
  const bodyId = `contribute-project-${projectHash}`;

  return (
    <section className="border-b border-rule last:border-b-0" aria-label={`project ${name}`} data-testid={`contribute-project-${projectHash}`}>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2 px-4 py-3">
        <div className="min-w-0 flex-1">
          <h2>
          <button
            type="button"
            aria-expanded={open}
            aria-controls={bodyId}
            onClick={onToggleOpen}
            className="flex w-full min-w-0 items-center gap-2 text-left font-mono font-semibold text-sm text-ink cursor-pointer focus-mono"
          >
            <ChevronRight aria-hidden="true" className={`size-3.5 shrink-0 text-ink-3 transition-transform duration-150 ${open ? "rotate-90" : ""}`} />
            <span className="truncate normal-case">{name}</span>
          </button>
          </h2>
          <p
            className="mt-1 pl-[calc(0.875rem+var(--sp-2))] font-mono text-[length:var(--fs-label)] tabular-nums text-ink-3"
            data-testid={`contribute-project-counts-${projectHash}`}
          >
            {plural(project.matching_count, "session")}
            {live > 0 && ` · ${live} already contributed`}
            {selected > 0 && ` · ${selected} selected`}
          </p>
        </div>
        {snapshot.covered ? (
          <Button size="sm" variant="ghost" pressed onClick={snapshot.deselectAll}>
            {ContributeLabel.DeselectAll.wire}
          </Button>
        ) : (
          <Button
            size="sm"
            variant="ghost"
            loading={snapshot.loading}
            disabled={snapshot.loading || project.selectable_count === 0}
            onClick={() => void snapshot.selectAll()}
            aria-label={`select all ${project.selectable_count} selectable sessions in ${name}`}
          >
            {snapshot.loading ? "selecting…" : `select all ${project.selectable_count}`}
          </Button>
        )}
      </div>
      {(snapshot.failure || snapshot.omitted > 0) && (
        <div className="px-4 pb-3 text-sm text-ink-2" role="status" data-testid={`contribute-project-notice-${projectHash}`}>
          {snapshot.failure
            ? `could not select every session in this project: ${failureMessage(snapshot.failure)}`
            : `${plural(snapshot.omitted, "session")} selected earlier ${snapshot.omitted === 1 ? "is" : "are"} not in this search. ${
                snapshot.omitted === 1 ? "it stays" : "they stay"
              } selected.`}
        </div>
      )}
      <div id={bodyId}>{open && <ProjectBody project={project} {...bodyProps} />}</div>
    </section>
  );
}

/**
 * The contribute page's project list. Each project is a collapsed section that
 * states its counts from the project list; opening one reads its sessions a
 * page at a time and draws them as branch > session, with each session's
 * helpers behind the same disclosure every transcript list uses.
 */
export default function PagedContributeTree({ projects, ...sectionProps }: PagedContributeTreeProps) {
  const [openProjects, setOpenProjects] = useState<ReadonlySet<string>>(new Set());
  return (
    <div>
      {projects.map((project) => (
        <ProjectSection
          key={project.project_hash}
          project={project}
          open={openProjects.has(project.project_hash)}
          onToggleOpen={() =>
            setOpenProjects((before) => {
              const next = new Set(before);
              if (next.has(project.project_hash)) next.delete(project.project_hash);
              else next.add(project.project_hash);
              return next;
            })
          }
          {...sectionProps}
        />
      ))}
    </div>
  );
}
