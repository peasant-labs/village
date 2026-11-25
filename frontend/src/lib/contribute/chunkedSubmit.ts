import { closed } from "@peasant-labs/fairtrade/closed-value";
import {
  VillageContributionErrorCode,
  VillageTranscriptVisibility,
  zVillageBatchShareSelectionRequest,
  zVillageContributionErrorResponse,
  type VillageBatchShareResponse,
  type VillageBatchShareSelectionRequest,
  type VillageContributionErrorDetail,
  type VillageContributionStatus,
} from "@peasant-labs/schema";
import { ApiError } from "../api";
import { bySubmissionProject, type FrozenSelection, type SelectedRecord } from "./frozenSelection";

/**
 * The contribute page's submit: the frozen selection, sent to
 * `POST /collectives/{id}/shares/selection` in chunks, and the receipt of what
 * happened to every record.
 *
 * Each chunk names one project and at most {@link CONTRIBUTION_CHUNK_SIZE}
 * sessions. The server writes a chunk whole or refuses it whole, so a refusal
 * never leaves part of a chunk written. A refusal that names sessions removes
 * or re-asks about exactly those sessions and resends the rest; a refusal of
 * the whole request stops the run. Every record of the selection ends in
 * exactly one bucket of the receipt.
 *
 * The run never falls back to the legacy project write. A server that does not
 * serve the selection write stops the run, and nothing is sent.
 */

/** Sessions per request. The contract allows 1,000; a smaller chunk keeps one
 *  refusal's blast radius and one request's lock time small. */
export const CONTRIBUTION_CHUNK_SIZE = 250;

/** One request's worth of the selection: one project, ids in sorted order. */
export interface PlannedChunk {
  project_hash: string;
  records: SelectedRecord[];
}

function byId(a: SelectedRecord, b: SelectedRecord): number {
  return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
}

/**
 * Splits the selection into chunks, by the project each record is submitted
 * under, projects in hash order and ids sorted within each. The same selection
 * always plans the same chunks.
 */
export function planChunks(selection: FrozenSelection, size: number = CONTRIBUTION_CHUNK_SIZE): PlannedChunk[] {
  if (!Number.isInteger(size) || size < 1) throw new Error(`chunk size must be a positive integer, got ${size}`);
  const chunks: PlannedChunk[] = [];
  const projects = [...bySubmissionProject(selection).entries()].sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0));
  for (const [projectHash, records] of projects) {
    const sorted = [...records].sort(byId);
    for (let start = 0; start < sorted.length; start += size) {
      chunks.push({ project_hash: projectHash, records: sorted.slice(start, start + size) });
    }
  }
  return chunks;
}

/**
 * The private records of the selection, which the consent step lists before
 * the run: every one, including records a snapshot added that the page never
 * loaded. Ordered by project, then id, so the list reads in submission order.
 */
export function privateRecords(selection: FrozenSelection): SelectedRecord[] {
  return planChunks(selection).flatMap((chunk) => chunk.records.filter((record) => record.visibility === VillageTranscriptVisibility.Private));
}

/**
 * Why a record was not contributed, when the run reached it. `Rejected` is a
 * defect, not something the person can fix; the message says what the server
 * said. `Server` and `Network` are failed requests that a retry may get past.
 */
const ContributionFailureReasonValues = {
  Missing: closed("missing", "the session no longer exists or is no longer yours"),
  Moved: closed("moved", "the session is now in another project; select it again there"),
  PrivateUnapproved: closed("private_unapproved", "the session is private and the server refused it after it was approved"),
  DeclinedPrivate: closed("declined_private", "the session is private and you did not approve it"),
  Conflict: closed("conflict", "another write held the session twice in a row"),
  Rejected: closed("rejected", "the server refused the request as malformed"),
  Unconfirmed: closed("unconfirmed", "the server answered without naming this session"),
  Server: closed("server", "the server failed the request"),
  Network: closed("network", "the request did not reach the server"),
} as const;

export type ContributionFailureReason = (typeof ContributionFailureReasonValues)[keyof typeof ContributionFailureReasonValues];
export const ContributionFailureReason = ContributionFailureReasonValues;

/** Failures the person can retry as they are: the record stays selected. */
const RETRYABLE: ReadonlySet<ContributionFailureReason> = new Set([
  ContributionFailureReason.Conflict,
  ContributionFailureReason.Rejected,
  ContributionFailureReason.Unconfirmed,
  ContributionFailureReason.Server,
  ContributionFailureReason.Network,
]);

export function isRetryableFailure(reason: ContributionFailureReason): boolean {
  return RETRYABLE.has(reason);
}

/**
 * Why the run stopped before the end. Every record not yet settled is then
 * unattempted: nothing was written for it.
 */
const ContributionStopReasonValues = {
  StaleSelection: closed("stale_selection", "a selected session was contributed elsewhere since the page read it"),
  Policy: closed("policy", "the collective does not accept this contribution from you"),
  Membership: closed("membership", "you are not a contributing member of the collective"),
  SignedOut: closed("signed_out", "the session expired"),
  CollectiveGone: closed("collective_gone", "the collective no longer exists"),
  Deployment: closed("deployment", "the server does not serve the selection write"),
  Cancelled: closed("cancelled", "you cancelled the run"),
} as const;

export type ContributionStopReason = (typeof ContributionStopReasonValues)[keyof typeof ContributionStopReasonValues];
export const ContributionStopReason = ContributionStopReasonValues;

export interface ContributedRecord {
  record: SelectedRecord;
  status: VillageContributionStatus;
}

export interface FailedRecord {
  record: SelectedRecord;
  reason: ContributionFailureReason;
  /** For `moved`: the project the session is in now. */
  current_project_hash?: string;
  /** The server's own words, when it gave any. */
  message?: string;
}

/** What the run did with every record of the frozen selection. */
export interface ContributionReceipt {
  /** The size of the frozen selection; the four buckets sum to it. */
  total: number;
  contributed: ContributedRecord[];
  /** Already contributed by an earlier attempt; nothing new was opened. */
  already: SelectedRecord[];
  failed: FailedRecord[];
  unattempted: SelectedRecord[];
  stopped?: { reason: ContributionStopReason; message?: string };
}

/** Progress after each request, for the page to show. */
export interface ContributionProgress {
  chunk: number;
  chunks: number;
  settled: number;
  total: number;
}

/** The answer of the step that re-asks about private sessions. */
const PrivateApprovalValues = {
  Approve: closed("approve", "contribute the private sessions and make them visible"),
  Decline: closed("decline", "leave the private sessions out"),
} as const;

export type PrivateApproval = (typeof PrivateApprovalValues)[keyof typeof PrivateApprovalValues];
export const PrivateApproval = PrivateApprovalValues;

export interface ContributionRunInput {
  selection: FrozenSelection;
  /** The private ids the person approved in the consent step. A private record
   *  outside this set is reported as declined and never sent. */
  approved: ReadonlySet<string>;
  /** Sends one request. Throws {@link ApiError} for a refusal; anything else it
   *  throws is a network failure, or a cancellation when `signal` aborted. */
  submit: (request: VillageBatchShareSelectionRequest) => Promise<VillageBatchShareResponse>;
  /** Re-asks about sessions the server reports private and unapproved, which
   *  happens when a session became private after it was selected. Asked at
   *  most once per session. */
  approvePrivate: (records: SelectedRecord[]) => Promise<PrivateApproval>;
  onProgress?: (progress: ContributionProgress) => void;
  signal?: AbortSignal;
  chunkSize?: number;
}

type Settlement =
  | { bucket: "contributed"; status: VillageContributionStatus }
  | { bucket: "already" }
  | { bucket: "failed"; reason: ContributionFailureReason; current_project_hash?: string; message?: string };

/** How one refusal of a chunk is handled: the run stops, every record in the
 * chunk fails, or each named record is answered on its own. */
const RefusalKind = Object.freeze({ Stop: "stop", FailAll: "fail_all", PerRecord: "per_record" } as const);

type Refusal =
  | { kind: typeof RefusalKind.Stop; reason: ContributionStopReason; message?: string }
  | { kind: typeof RefusalKind.FailAll; reason: ContributionFailureReason; message?: string }
  | { kind: typeof RefusalKind.PerRecord; details: VillageContributionErrorDetail[]; message: string };

const DETAIL_CODES = new Set<string>([
  VillageContributionErrorCode.Missing,
  VillageContributionErrorCode.Moved,
  VillageContributionErrorCode.PrivateUnapproved,
  VillageContributionErrorCode.Conflict,
  VillageContributionErrorCode.StaleSelection,
]);

function isAbort(err: unknown, signal: AbortSignal | undefined): boolean {
  if (signal?.aborted) return true;
  return err instanceof Error && err.name === "AbortError";
}

/** Reads one failed request in the terms the run acts on. */
export function classifyRefusal(err: unknown, signal?: AbortSignal): Refusal {
  if (isAbort(err, signal)) return { kind: RefusalKind.Stop, reason: ContributionStopReason.Cancelled };
  if (!(err instanceof ApiError)) {
    return { kind: RefusalKind.FailAll, reason: ContributionFailureReason.Network, message: err instanceof Error ? err.message : undefined };
  }
  const message = err.fromServer ? err.message : undefined;
  switch (err.status) {
    case 401:
      return { kind: RefusalKind.Stop, reason: ContributionStopReason.SignedOut, message };
    case 403:
      // A request-level refusal: the code says which, and anything else is
      // still a refusal of the whole request.
      return { kind: RefusalKind.Stop, reason: err.code === "policy" ? ContributionStopReason.Policy : ContributionStopReason.Membership, message };
    case 404:
      // The handler names a deleted collective in its own words; a route the
      // server does not mount answers with the router's plain-text default.
      return err.fromServer
        ? { kind: RefusalKind.Stop, reason: ContributionStopReason.CollectiveGone, message }
        : { kind: RefusalKind.Stop, reason: ContributionStopReason.Deployment };
    case 405:
    case 501:
      return { kind: RefusalKind.Stop, reason: ContributionStopReason.Deployment };
    case 409: {
      const body = zVillageContributionErrorResponse.safeParse({ error: err.message, code: err.code, details: err.details });
      const details = body.success ? (body.data.details ?? []) : [];
      if (details.some((detail) => detail.code === VillageContributionErrorCode.StaleSelection)) {
        return { kind: RefusalKind.Stop, reason: ContributionStopReason.StaleSelection, message };
      }
      const usable = details.length > 0 && details.every((detail) => detail.transcript_id !== undefined && DETAIL_CODES.has(detail.code));
      return usable
        ? { kind: RefusalKind.PerRecord, details, message: err.message }
        : { kind: RefusalKind.FailAll, reason: ContributionFailureReason.Rejected, message };
    }
    default:
      if (err.status >= 500 || err.status === 0) return { kind: RefusalKind.FailAll, reason: ContributionFailureReason.Server, message };
      return { kind: RefusalKind.FailAll, reason: ContributionFailureReason.Rejected, message };
  }
}

/**
 * Runs the submit over the whole selection and returns the receipt. It never
 * throws for a refusal: every outcome is a bucket or a stop reason.
 */
export async function runContribution(input: ContributionRunInput): Promise<ContributionReceipt> {
  const { selection, submit, approvePrivate, onProgress, signal } = input;
  const approved = new Set(input.approved);
  const asked = new Set<string>();
  const settled = new Map<string, Settlement>();
  const chunks = planChunks(selection, input.chunkSize);
  let stopped: ContributionReceipt["stopped"];

  const settle = (id: string, settlement: Settlement) => {
    if (!settled.has(id)) settled.set(id, settlement);
  };

  for (const [index, chunk] of chunks.entries()) {
    if (stopped) break;
    if (signal?.aborted) {
      stopped = { reason: ContributionStopReason.Cancelled };
      break;
    }
    let pending = chunk.records.filter((record) => {
      if (record.visibility === VillageTranscriptVisibility.Private && !approved.has(record.id)) {
        settle(record.id, { bucket: "failed", reason: ContributionFailureReason.DeclinedPrivate });
        return false;
      }
      return true;
    });
    let conflictRetried = false;

    while (pending.length > 0) {
      // Parsed against the contract, which brands the project hash and holds
      // the request to the contract's own limits.
      const request: VillageBatchShareSelectionRequest = zVillageBatchShareSelectionRequest.parse({
        project_hash: chunk.project_hash,
        transcript_ids: pending.map((record) => record.id),
        private_approvals: pending.filter((record) => approved.has(record.id)).map((record) => record.id),
      });
      let response: VillageBatchShareResponse;
      try {
        response = await submit(request);
      } catch (err) {
        const refusal = classifyRefusal(err, signal);
        if (refusal.kind === RefusalKind.Stop) {
          stopped = { reason: refusal.reason, ...(refusal.message ? { message: refusal.message } : {}) };
          break;
        }
        if (refusal.kind === RefusalKind.FailAll) {
          for (const record of pending) {
            settle(record.id, { bucket: "failed", reason: refusal.reason, ...(refusal.message ? { message: refusal.message } : {}) });
          }
          break;
        }
        pending = await reconcile(pending, refusal.details, {
          approved,
          asked,
          settle,
          approvePrivate,
          conflictRetried,
          message: refusal.message,
        });
        if (refusal.details.some((detail) => detail.code === VillageContributionErrorCode.Conflict)) conflictRetried = true;
        if (signal?.aborted) {
          stopped = { reason: ContributionStopReason.Cancelled };
          break;
        }
        continue;
      }
      const shared = new Map(response.shared.map((entry) => [entry.transcript_id, entry.status]));
      const already = new Set(response.already_shared);
      for (const record of pending) {
        const status = shared.get(record.id);
        if (status !== undefined) settle(record.id, { bucket: "contributed", status });
        else if (already.has(record.id)) settle(record.id, { bucket: "already" });
        else settle(record.id, { bucket: "failed", reason: ContributionFailureReason.Unconfirmed });
      }
      break;
    }

    onProgress?.({ chunk: index + 1, chunks: chunks.length, settled: settled.size, total: selection.size });
  }

  return receiptOf(selection, settled, stopped);
}

interface ReconcileContext {
  approved: Set<string>;
  asked: Set<string>;
  settle: (id: string, settlement: Settlement) => void;
  approvePrivate: (records: SelectedRecord[]) => Promise<PrivateApproval>;
  conflictRetried: boolean;
  message: string;
}

/**
 * Applies a 409's per-session details to the chunk and returns what to resend.
 * Each resend either drops a named session, consumes the single re-ask of a
 * private session, or consumes the single conflict retry, so the loop ends.
 */
async function reconcile(
  pending: SelectedRecord[],
  details: VillageContributionErrorDetail[],
  ctx: ReconcileContext,
): Promise<SelectedRecord[]> {
  const byIdMap = new Map(pending.map((record) => [record.id, record]));
  const dropped = new Set<string>();
  const unapproved: SelectedRecord[] = [];
  let conflict = false;

  for (const detail of details) {
    const id = detail.transcript_id!;
    const record = byIdMap.get(id);
    if (!record) continue;
    switch (detail.code) {
      case VillageContributionErrorCode.Missing:
        ctx.settle(id, { bucket: "failed", reason: ContributionFailureReason.Missing });
        dropped.add(id);
        break;
      case VillageContributionErrorCode.Moved:
        ctx.settle(id, {
          bucket: "failed",
          reason: ContributionFailureReason.Moved,
          ...(detail.current_project_hash ? { current_project_hash: detail.current_project_hash } : {}),
        });
        dropped.add(id);
        break;
      case VillageContributionErrorCode.PrivateUnapproved:
        if (ctx.asked.has(id)) {
          ctx.settle(id, { bucket: "failed", reason: ContributionFailureReason.PrivateUnapproved, message: ctx.message });
          dropped.add(id);
        } else {
          unapproved.push(record);
        }
        break;
      case VillageContributionErrorCode.Conflict:
        conflict = true;
        break;
    }
  }

  // A detail naming no session of this chunk leaves nothing to drop or ask
  // about; the chunk fails rather than resend the same request.
  if (dropped.size === 0 && unapproved.length === 0 && !conflict) {
    for (const record of pending) ctx.settle(record.id, { bucket: "failed", reason: ContributionFailureReason.Rejected, message: ctx.message });
    return [];
  }

  if (unapproved.length > 0) {
    for (const record of unapproved) ctx.asked.add(record.id);
    const answer = await ctx.approvePrivate(unapproved);
    for (const record of unapproved) {
      if (answer === PrivateApproval.Approve) ctx.approved.add(record.id);
      else {
        ctx.settle(record.id, { bucket: "failed", reason: ContributionFailureReason.DeclinedPrivate });
        dropped.add(record.id);
      }
    }
  }

  let rest = pending.filter((record) => !dropped.has(record.id));
  if (conflict && ctx.conflictRetried) {
    for (const record of rest) ctx.settle(record.id, { bucket: "failed", reason: ContributionFailureReason.Conflict, message: ctx.message });
    rest = [];
  }
  return rest;
}

function receiptOf(
  selection: FrozenSelection,
  settled: ReadonlyMap<string, Settlement>,
  stopped: ContributionReceipt["stopped"],
): ContributionReceipt {
  const receipt: ContributionReceipt = { total: selection.size, contributed: [], already: [], failed: [], unattempted: [] };
  for (const chunk of planChunks(selection)) {
    for (const record of chunk.records) {
      const settlement = settled.get(record.id);
      if (!settlement) receipt.unattempted.push(record);
      else if (settlement.bucket === "contributed") receipt.contributed.push({ record, status: settlement.status });
      else if (settlement.bucket === "already") receipt.already.push(record);
      else {
        receipt.failed.push({
          record,
          reason: settlement.reason,
          ...(settlement.current_project_hash ? { current_project_hash: settlement.current_project_hash } : {}),
          ...(settlement.message ? { message: settlement.message } : {}),
        });
      }
    }
  }
  if (stopped) receipt.stopped = stopped;
  return receipt;
}

/**
 * The selection after a run. Contributed and already contributed records
 * leave it, and so do records the person cannot submit as they are: missing,
 * moved (select it again in its new project), refused private, and declined
 * private. Records that failed for a reason a retry can fix stay selected, and
 * so do unattempted ones.
 */
export function selectionAfterRun(selection: FrozenSelection, receipt: ContributionReceipt): FrozenSelection {
  const next = new Map(selection);
  for (const { record } of receipt.contributed) next.delete(record.id);
  for (const record of receipt.already) next.delete(record.id);
  for (const failure of receipt.failed) if (!isRetryableFailure(failure.reason)) next.delete(failure.record.id);
  return next;
}
