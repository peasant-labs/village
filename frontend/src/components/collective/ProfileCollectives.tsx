"use client";

import { useState } from "react";
import Link from "next/link";
import { Boxes, ChevronDown } from "lucide-react";
import { DataState, TeachingEmptyState } from "@/lib/ft-ui";
import { useMyCollectiveContributions } from "@/lib/queries/collectives";
import {
  CONTRIBUTION_COUNTER_EXPLANATION,
  CONTRIBUTION_COUNTER_LABELS,
  CONTRIBUTION_COUNTER_UNITS,
} from "@/lib/shareEvents";
import { cn } from "@/lib/utils";
import type { ContributedCollective } from "@/lib/types";
import CollectiveSubmissions from "./CollectiveSubmissions";

/**
 * One counter, printed with the UNIT it measures.
 *
 * The unit is not decoration. Two of these four count transcripts and two
 * count submission attempts, so a bare "3" beside a bare "2" would invite a
 * comparison that is not meaningful. The unit line is what makes the numbers
 * readable, and it is rendered for every counter so none of them looks like
 * the special case.
 */
function ContributionCounter({
  testId,
  label,
  value,
  unit,
}: {
  testId: string;
  label: string;
  value: number;
  unit: string;
}) {
  return (
    <div data-testid={testId} className="flex min-w-0 flex-col gap-0.5">
      <span className="font-mono text-xs text-ink-3">{label}</span>
      <span className="font-mono text-sm text-ink tabular-nums">{value.toLocaleString()}</span>
      <span data-testid={`${testId}-unit`} className="font-mono text-xs text-ink-4">
        {unit}
      </span>
    </div>
  );
}

/** One collective row: its name, its three counters, and its openable history. */
function ContributedCollectiveRow({
  collective,
  open,
  onToggle,
}: {
  collective: ContributedCollective;
  open: boolean;
  onToggle: () => void;
}) {
  return (
    <li
      data-testid="contributed-collective"
      data-collective-id={collective.id}
      className="border border-rule bg-surface"
    >
      <div className="flex flex-wrap items-start gap-x-8 gap-y-4 px-5 py-4">
        {/* min-w guarantees this column real space before it ever gives any
            up: the four counters + toggle button are `shrink-0` below, so
            they wrap onto their OWN line as a unit rather than being allowed
            to crush the name down to an unreadable "AI…" fragment. A name
            longer than this column can hold still truncates (`truncate`
            below) — that is a deliberate fallback for pathological input,
            not the routine case, which is why the acceptance bar is "every
            fixture name renders in full", not "no name ever truncates". */}
        <div className="min-w-[12rem] flex-1 flex flex-col gap-1">
          <Link
            href={`/groups/${collective.id}`}
            className="font-[family-name:var(--font-display)] text-sm font-semibold text-ink truncate hover:text-ink-2 transition-colors focus-mono cursor-pointer"
          >
            {collective.name}
          </Link>
          {collective.description && (
            <p className="text-[13px] text-ink-3 leading-relaxed line-clamp-2">
              {collective.description}
            </p>
          )}
        </div>
        {/* shrink-0: this cluster never gets crushed to make room for the
            name column above. When it cannot sit beside the name at the
            name's guaranteed minimum width, flex-wrap moves the WHOLE
            cluster to its own line instead — the name is never the one that
            gives. */}
        <div className="flex flex-wrap items-start gap-x-8 gap-y-4 shrink-0">
          <div className="flex items-start gap-8">
            <ContributionCounter
              testId="counter-approved"
              label={CONTRIBUTION_COUNTER_LABELS.approved}
              value={collective.approved_count}
              unit={CONTRIBUTION_COUNTER_UNITS.approved}
            />
            <ContributionCounter
              testId="counter-pending"
              label={CONTRIBUTION_COUNTER_LABELS.pending}
              value={collective.pending_count}
              unit={CONTRIBUTION_COUNTER_UNITS.pending}
            />
            <ContributionCounter
              testId="counter-rejected-attempts"
              label={CONTRIBUTION_COUNTER_LABELS.rejectedAttempts}
              value={collective.rejected_attempt_count}
              unit={CONTRIBUTION_COUNTER_UNITS.rejectedAttempts}
            />
            <ContributionCounter
              testId="counter-withdrawn"
              label={CONTRIBUTION_COUNTER_LABELS.withdrawnAttempts}
              value={collective.withdrawn_attempt_count}
              unit={CONTRIBUTION_COUNTER_UNITS.withdrawnAttempts}
            />
          </div>
          <button
            type="button"
            aria-expanded={open}
            onClick={onToggle}
            className={cn(
              "inline-flex items-center gap-1.5 h-8 px-2.5 shrink-0 self-center",
              "border border-rule bg-surface font-mono text-xs text-ink-3",
              "hover:bg-surface-hover hover:text-ink transition-colors focus-mono cursor-pointer",
            )}
          >
            <ChevronDown className={cn("size-3.5", open && "rotate-180")} />
            {open ? "hide submissions" : "submissions"}
          </button>
        </div>
      </div>
      {open && (
        <div className="border-t border-rule px-5 py-4">
          <CollectiveSubmissions groupId={collective.id} groupName={collective.name} />
        </div>
      )}
    </li>
  );
}

/**
 * The collectives the profile's OWNER has offered transcripts to.
 *
 * This section exists only on one's own profile, and the gate lives here so
 * there is exactly one place it can be got wrong. For any other viewer the
 * component renders NOTHING and its hook is disabled, so no request is made
 * either: a "you cannot see this" placeholder would itself disclose that
 * contributions exist, and a request fired for a viewer who may not read the
 * answer is the same disclosure moved onto the network.
 *
 * A collective with zero approved contributions and some still awaiting review
 * IS listed, with approved 0. Saying nothing until something is accepted is
 * how a person loses track of what they offered.
 */
export default function ProfileCollectives({ isOwnProfile }: { isOwnProfile: boolean }) {
  const { data, isLoading, isError, error } = useMyCollectiveContributions(isOwnProfile);
  const [openCollectiveId, setOpenCollectiveId] = useState<string | null>(null);

  if (!isOwnProfile) return null;

  const collectives = data ?? [];

  return (
    <section data-testid="profile-collectives" className="border border-rule bg-surface">
      <div className="flex flex-col gap-1 border-b border-rule px-5 py-3">
        {/* Lowercase because this is UI chrome, not user content. The
            neighbouring library card's title-case header predates this
            section and is out of scope here. */}
        <span className="inline-flex items-center gap-2 text-sm font-medium text-ink">
          <Boxes size={14} className="text-ink-3" />
          collectives you contribute to
        </span>
        <p className="font-mono text-xs text-ink-3 leading-relaxed">
          {CONTRIBUTION_COUNTER_EXPLANATION}
        </p>
      </div>

      {isError ? (
        <p className="px-5 py-4 text-[13px] text-danger">
          your contributed collectives could not be loaded:{" "}
          {String((error as Error)?.message ?? "the request failed")}. your contributions are
          unchanged and nothing was written. retry, or reload this page.
        </p>
      ) : (
        <div className="px-5 py-4">
          <DataState
            loading={isLoading}
            empty={collectives.length === 0}
            emptyState={
              <TeachingEmptyState
                icon={Boxes}
                title="you have not offered a transcript to a collective yet"
                body="open a transcript of yours and contribute it to a collective. what you offer, and how each collective answered, is recorded here."
                privacy={null}
                style={{ border: "none", background: "transparent" }}
              />
            }
          >
            <ul className="flex flex-col gap-3">
              {collectives.map((collective) => (
                <ContributedCollectiveRow
                  key={collective.id}
                  collective={collective}
                  open={openCollectiveId === collective.id}
                  onToggle={() =>
                    setOpenCollectiveId(
                      openCollectiveId === collective.id ? null : collective.id,
                    )
                  }
                />
              ))}
            </ul>
          </DataState>
        </div>
      )}
    </section>
  );
}
