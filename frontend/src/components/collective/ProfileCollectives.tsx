"use client";

import { Suspense, useState } from "react";
import { useSearchParams } from "next/navigation";
import Link from "next/link";
import { Boxes, ChevronDown } from "lucide-react";
import { DataState, TeachingEmptyState } from "@/lib/ft-ui";
import { useMyCollectiveContributions } from "@/lib/queries/collectives";
import {
  CONTRIBUTION_COUNTER_LABELS,
} from "@/lib/shareEvents";
import { cn } from "@/lib/utils";
import type { ContributedCollective } from "@/lib/types";
import CollectiveSubmissions from "./CollectiveSubmissions";

/**
 * One counter: its label and its value.
 *
 * The four counters do not all measure the same unit — two count transcripts,
 * two count submission attempts — so each label names its own unit (see
 * {@link CONTRIBUTION_COUNTER_LABELS} for why the distinction matters).
 */
function ContributionCounter({
  testId,
  label,
  shortLabel,
  value,
}: {
  testId: string;
  label: string;
  shortLabel?: string;
  value: number;
}) {
  return (
    <div data-testid={testId} className="on-contribution-counter">
      <dt>{shortLabel ? <><span aria-hidden="true">{shortLabel}</span><span className="sr-only">{label}</span></> : label}</dt>
      <dd>{value.toLocaleString()}</dd>
    </div>
  );
}

/** One collective row: its name, its four counters, and its openable history. */
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
      className="on-contribution-row"
    >
      <div className="on-contribution-summary">
        <div className="on-contribution-name">
          <Link
            href={`/collectives/${collective.id}`}
            className="on-contribution-link"
          >
            {collective.name}
          </Link>
          {collective.description && (
            <p className="text-[length:var(--fs-body)] text-ink-3 leading-relaxed line-clamp-2">
              {collective.description}
            </p>
          )}
        </div>
        <div className="on-contribution-facts">
          <dl className="on-contribution-counters">
            <ContributionCounter
              testId="counter-approved"
              label={CONTRIBUTION_COUNTER_LABELS.approved}
              shortLabel="approved"
              value={collective.approved_count}
            />
            <ContributionCounter
              testId="counter-pending"
              label={CONTRIBUTION_COUNTER_LABELS.pending}
              shortLabel="awaiting review"
              value={collective.pending_count}
            />
            <ContributionCounter
              testId="counter-rejected-attempts"
              label={CONTRIBUTION_COUNTER_LABELS.rejectedAttempts}
              value={collective.rejected_attempt_count}
            />
            <ContributionCounter
              testId="counter-withdrawn"
              label={CONTRIBUTION_COUNTER_LABELS.withdrawnAttempts}
              value={collective.withdrawn_attempt_count}
            />
          </dl>
          <button
            type="button"
            aria-expanded={open}
            onClick={onToggle}
            className={cn(
              "btn btn-ghost btn-sm on-contribution-toggle",
            )}
          >
            <ChevronDown className={cn("size-3.5", open && "rotate-180")} />
            submissions
          </button>
        </div>
      </div>
      {open && (
        <div className="on-contribution-submissions">
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
  return <Suspense fallback={null}><ProfileCollectivesLocation isOwnProfile={isOwnProfile} /></Suspense>;
}

function ProfileCollectivesLocation({ isOwnProfile }: { isOwnProfile: boolean }) {
  const initialCollectiveId = useSearchParams().get("collective");
  return <ProfileCollectivesBody key={initialCollectiveId ?? ""} isOwnProfile={isOwnProfile} initialCollectiveId={initialCollectiveId} />;
}

function ProfileCollectivesBody({ isOwnProfile, initialCollectiveId }: { isOwnProfile: boolean; initialCollectiveId: string | null }) {
  const { data, isLoading, isError, error } = useMyCollectiveContributions(isOwnProfile);
  const [openCollectiveId, setOpenCollectiveId] = useState(initialCollectiveId);

  if (!isOwnProfile) return null;

  const collectives = data ?? [];

  return (
    <section
      id="contributions"
      data-testid="profile-collectives"
      className="on-profile-collectives"
    >
      <div className="on-library-heading">
        {/* Lowercase because this is UI chrome, not user content. The
            neighbouring library card's title-case header predates this
            section and is out of scope here. */}
        <span className="inline-flex items-center gap-2 text-sm font-medium text-ink">
          <Boxes size={14} className="text-ink-3" />
          collectives you contribute to
        </span>
      </div>

      {isError ? (
        <p className="px-5 py-4 text-[length:var(--fs-body)] text-danger">
          your contributed collectives could not be loaded:{" "}
          {String((error as Error)?.message ?? "the request failed")}. your contributions are
          unchanged and nothing was written. retry, or reload this page.
        </p>
      ) : (
        <div className="on-contribution-list">
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
            <ul className="on-contribution-list">
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
