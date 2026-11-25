"use client";

import { useId, useState, type ReactNode } from "react";
import { ChevronRight } from "lucide-react";
import { VillageContributionStatus } from "@peasant-labs/schema";
import { Button } from "@/lib/ft-ui";
import {
  ContributionFailureReason,
  isRetryableFailure,
  type ContributionReceipt as Receipt,
  type ContributionStopReason,
  type FailedRecord,
} from "@/lib/contribute/chunkedSubmit";
import { ContributionReceiptTestId } from "./contributeTestId";
import { ContributeLabel } from "./contributeLabel";
import type { SelectedRecord } from "@/lib/contribute/frozenSelection";

const plural = (count: number, noun: string) => `${count} ${noun}${count === 1 ? "" : "s"}`;

/** Why the run stopped, in the person's terms. Restates the server's answer;
 *  decides nothing. */
const STOP_COPY: Record<ContributionStopReason["wire"], string> = {
  stale_selection: "a selected session was contributed elsewhere meanwhile. refresh, then contribute the rest.",
  policy: "this collective does not accept this contribution from you.",
  membership: "you are not a contributing member of this collective.",
  signed_out: "your session expired. sign in again, then contribute the rest.",
  collective_gone: "this collective no longer exists.",
  deployment: "this server does not take contributions of this kind yet. nothing more was sent.",
  cancelled: "you stopped the run.",
};

function failureCopy(failure: FailedRecord, projectLabel: (hash: string) => string | undefined): string {
  const reasons: Record<ContributionFailureReason["wire"], string> = {
    missing: "no longer exists, or is no longer yours",
    moved: failure.current_project_hash
      ? `moved to ${projectLabel(failure.current_project_hash) ?? "another project"}; select it again there`
      : "moved to another project; select it again there",
    private_unapproved: "private, and the server still refused it",
    declined_private: "private, and you kept it private",
    conflict: "another change held it; retry",
    rejected: "the server refused the request",
    unconfirmed: "the server did not confirm it; retry",
    server: "the server failed; retry",
    network: "the request did not reach the server; retry",
  };
  return reasons[failure.reason.wire];
}

const titleOf = (record: SelectedRecord) => record.title ?? "untitled session";

interface BucketProps {
  testId: string;
  label: string;
  count: number;
  children: ReactNode;
}

/** One bucket of the receipt: its count, and on demand the sessions in it. */
function Bucket({ testId, label, count, children }: BucketProps) {
  const [open, setOpen] = useState(false);
  const listId = useId();
  if (count === 0) return null;
  return (
    // An open bucket takes its own row, so its names never sit beside another
    // bucket's.
    <li data-testid={testId} className={open ? "basis-full" : undefined}>
      <button
        type="button"
        className="inline-flex items-center gap-1 tabular-nums text-ink-2 hover:text-ink"
        aria-expanded={open}
        aria-controls={listId}
        onClick={() => setOpen((was) => !was)}
      >
        <ChevronRight aria-hidden="true" className={`size-4 transition-transform ${open ? "rotate-90" : ""}`} />
        {count} {label}
      </button>
      {open && (
        <ul id={listId} className="ml-5 mt-1 flex flex-col gap-1" aria-label={`${label} sessions`}>
          {children}
        </ul>
      )}
    </li>
  );
}

interface ContributionReceiptProps {
  receipt: Receipt;
  projectLabel: (projectHash: string) => string | undefined;
  /** Starts a new run of the failures that can be retried as they are. */
  onRetry: (records: SelectedRecord[]) => void;
  retryDisabled?: boolean;
}

/**
 * What a run did with every session of the selection: a line of counts, each
 * bucket opening to name its sessions, why the run stopped when it stopped
 * early, and a retry for the failures a second attempt can fix.
 */
export default function ContributionReceipt({ receipt, projectLabel, onRetry, retryDisabled = false }: ContributionReceiptProps) {
  const retryable = receipt.failed.filter((failure) => isRetryableFailure(failure.reason)).map((failure) => failure.record);
  return (
    <div className="flex flex-col gap-1 text-[length:var(--fs-body)] text-ink-3" data-testid={ContributionReceiptTestId.Root.wire}>
      <ul
        // A long run can name hundreds of sessions. The receipt scrolls as a
        // whole inside the sticky footer, so open buckets never take over the
        // tree above it; the stop reason and retry below stay in view.
        className="flex max-h-[40vh] flex-wrap gap-x-4 gap-y-1 overflow-y-auto"
        tabIndex={0}
        aria-label={ContributeLabel.Receipt.wire}
      >
        <Bucket testId={ContributionReceiptTestId.Contributed.wire} label="contributed" count={receipt.contributed.length}>
          {receipt.contributed.map(({ record, status }) => (
            <li key={record.id}>
              <span className="normal-case">{titleOf(record)}</span>
              {status === VillageContributionStatus.Pending && <span className="text-ink-3"> · awaiting approval</span>}
            </li>
          ))}
        </Bucket>
        <Bucket testId={ContributionReceiptTestId.Already.wire} label={ContributeLabel.AlreadyContributed.wire} count={receipt.already.length}>
          {receipt.already.map((record) => (
            <li key={record.id} className="normal-case">
              {titleOf(record)}
            </li>
          ))}
        </Bucket>
        <Bucket testId={ContributionReceiptTestId.Failed.wire} label="not contributed" count={receipt.failed.length}>
          {receipt.failed.map((failure) => (
            <li key={failure.record.id}>
              <span className="normal-case">{titleOf(failure.record)}</span>
              {": "}
              {failureCopy(failure, projectLabel)}
              {failure.reason === ContributionFailureReason.Rejected && failure.message && (
                <span className="normal-case"> ({failure.message})</span>
              )}
            </li>
          ))}
        </Bucket>
        <Bucket testId={ContributionReceiptTestId.Unattempted.wire} label="not attempted" count={receipt.unattempted.length}>
          {receipt.unattempted.map((record) => (
            <li key={record.id} className="normal-case">
              {titleOf(record)}
            </li>
          ))}
        </Bucket>
      </ul>
      {receipt.stopped && (
        <p role="status" data-testid={ContributionReceiptTestId.Stopped.wire}>
          {STOP_COPY[receipt.stopped.reason.wire]}
          {receipt.unattempted.length > 0 &&
            ` ${plural(receipt.unattempted.length, "session")} ${receipt.unattempted.length === 1 ? "stays" : "stay"} selected.`}
        </p>
      )}
      {retryable.length > 0 && (
        <div>
          <Button size="sm" variant="secondary" disabled={retryDisabled} onClick={() => onRetry(retryable)}>
            retry {retryable.length} failed
          </Button>
        </div>
      )}
    </div>
  );
}
