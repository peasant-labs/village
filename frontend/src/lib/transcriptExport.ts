import { adaptTranscript, computeAnalytics } from "@peasant-labs/fairtrade/ui";
import type { SessionRelationshipNavigation } from "@peasant-labs/schema";
import type { SessionDetailPayload } from "@/types/messages";
import { API_URL_BASE, ApiError, getAuthHeaders } from "@/lib/api";
import { parseTranscriptRead } from "@/lib/queries/transcriptContent";
import { isSessionDetailPayload } from "@/lib/sessionDetailPayload";
import { overlayStoredTitle } from "@/components/session-detail/v2/transcriptChrome";

/**
 * The transcript header offers markdown, json, and jsonl downloads. Each
 * mounted menu row writes through this module.
 *
 * Markdown is written from the cooked view model, the output of fairtrade's one
 * wire adapter, so the file reads the way the page does: the stored title, each
 * turn's cooked body (thinking already split out) and each tool call's one-line
 * preview. JSON and JSONL are the served payload as the page received it. Both
 * are the redacted public copy village stores; nothing here reads more than the
 * page already shows.
 */

type TranscriptViewModel = ReturnType<typeof adaptTranscript>;

export type TranscriptExportFormat = "json" | "jsonl" | "markdown";

export interface TranscriptFile {
  name: string;
  type: string;
  text: string;
}

interface ExportSource {
  viewModel: TranscriptViewModel;
  detail: SessionDetailPayload;
  /** The transcript's own village link, written into the markdown header. */
  url: string;
  /** The village transcript id; names the file when the title gives no name. */
  transcriptId: string;
}

const EXTENSION: Record<TranscriptExportFormat, string> = {
  json: "json",
  jsonl: "jsonl",
  markdown: "md",
};

const MEDIA_TYPE: Record<TranscriptExportFormat, string> = {
  json: "application/json",
  jsonl: "application/x-ndjson",
  markdown: "text/markdown",
};

/** A file name from the title: lowercase words joined by dashes, bounded, then
 *  the transcript's short id so two transcripts with one title do not collide. */
export function transcriptFileName(
  title: string | undefined,
  transcriptId: string,
  format: TranscriptExportFormat,
): string {
  const slug = (title ?? "")
    .toLowerCase()
    .replace(/[^\p{Letter}\p{Number}]+/gu, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 60)
    .replace(/-+$/g, "");
  const shortId = transcriptId.slice(0, 8);
  return `${slug || "transcript"}-${shortId}.${EXTENSION[format]}`;
}

function oneLine(text: string): string {
  return text.replace(/\s+/g, " ").trim();
}

function quote(text: string): string {
  return text
    .split("\n")
    .map((line) => (line ? `> ${line}` : ">"))
    .join("\n");
}

/** The transcript as a markdown document: a title, its link and facts, then one
 *  section per turn in order. Chrome is lowercase; recorded text keeps its case. */
export function transcriptMarkdown(viewModel: TranscriptViewModel, url: string): string {
  const { session, turns } = viewModel;
  const facts = [
    session.harness ? `- harness: ${String(session.harness).replace(/-/g, " ")}` : null,
    session.model ? `- model: ${session.model}` : null,
    session.startTime ? `- started: ${session.startTime}` : null,
    `- turns: ${turns.length}`,
  ].filter((line): line is string => line !== null);

  const blocks: string[] = [`# ${session.title ?? "transcript"}`, url, facts.join("\n")];
  for (const turn of turns) {
    const parts: string[] = [`## turn ${turn.label} · ${turn.role}`];
    if (turn.thinking?.text) parts.push(quote(`thinking: ${turn.thinking.text}`));
    if (turn.content?.trim()) parts.push(turn.content.trim());
    if (turn.toolCalls.length > 0) {
      parts.push(
        turn.toolCalls
          .map((call) => {
            const preview = oneLine(call.preview ?? "");
            return `- tool ${call.name}${preview ? `: ${preview}` : ""}`;
          })
          .join("\n"),
      );
    }
    blocks.push(parts.join("\n\n"));
  }
  return `${blocks.join("\n\n")}\n`;
}

/** The file one export format produces. */
export function transcriptFile(format: TranscriptExportFormat, source: ExportSource): TranscriptFile {
  const name = transcriptFileName(source.viewModel.session.title, source.transcriptId, format);
  const type = MEDIA_TYPE[format];
  if (format === "markdown") {
    return { name, type, text: transcriptMarkdown(source.viewModel, source.url) };
  }
  if (format === "jsonl") {
    const lines = (source.detail.turns ?? []).map((turn) => JSON.stringify(turn));
    return { name, type, text: `${lines.join("\n")}\n` };
  }
  return { name, type, text: `${JSON.stringify(source.detail, null, 2)}\n` };
}

/** Hands the file to the browser as a download: a blob link clicked once, then
 *  released after the click has been handled. */
export function saveTranscriptFile(file: TranscriptFile): void {
  const blob = new Blob([file.text], { type: `${file.type};charset=utf-8` });
  const href = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = href;
  link.download = file.name;
  link.rel = "noopener";
  document.body.appendChild(link);
  link.click();
  link.remove();
  setTimeout(() => URL.revokeObjectURL(href), 1000);
}

/**
 * The whole document as the page shows it: fairtrade's one adapter over the
 * full payload with the stored title overlaid. The whole-document page and
 * every download cook through this, so a file reads the way the page does.
 */
export function cookWholeTranscript(
  detail: SessionDetailPayload,
  options: {
    storedTitle: string | null | undefined;
    relationshipNavigation?: SessionRelationshipNavigation[];
    published?: boolean;
  },
): TranscriptViewModel {
  const analytics = computeAnalytics((detail.turns ?? []) as Parameters<typeof computeAnalytics>[0], {
    scorecard: detail.scorecard ?? undefined,
  } as Parameters<typeof computeAnalytics>[1]);
  const adapted = adaptTranscript(
    detail as Parameters<typeof adaptTranscript>[0],
    undefined,
    analytics,
    // Read metadata, not durable content: the adapter cooks the source and
    // starter links and an exact branch anchor only while the navigation it
    // receives still agrees with the durable relationship.
    {
      relationshipNavigation: options.relationshipNavigation,
      ...(options.published ? { payloadMode: "published" as const } : {}),
    },
  );
  return overlayStoredTitle(adapted, options.storedTitle);
}

/**
 * The reader state a download belongs to. A file is saved only while its
 * epoch is current: the page has not moved to another transcript or another
 * revision of this one, and the page is still mounted.
 */
export interface DownloadEpoch {
  signal: AbortSignal;
  isCurrent(): boolean;
}

/** What a whole-document read needs to parse and cook the stored copy. */
export interface WholeTranscriptRequest {
  transcriptId: string;
  storedTitle: string | null | undefined;
  knownHarness?: string;
  relationshipNavigation?: SessionRelationshipNavigation[];
}

export interface TranscriptDownloadRequest extends WholeTranscriptRequest {
  format: TranscriptExportFormat;
  /** The transcript's own village link, written into the markdown header. */
  url: string;
}

/** The whole stored copy, parsed the way the whole-document page parses it, and cooked. */
export interface WholeTranscript {
  detail: SessionDetailPayload;
  viewModel: TranscriptViewModel;
}

/** What a reader action reads the whole stored copy for; it names the action in a refusal. Closed. */
export const WholeTranscriptPurpose = Object.freeze({ Download: "download", Graph: "graph" } as const);
export type WholeTranscriptPurpose = (typeof WholeTranscriptPurpose)[keyof typeof WholeTranscriptPurpose];

/**
 * One authorized read of the whole stored copy, for a page that holds only
 * part of the transcript: a download, or the trajectory graph. Resolves
 * `null` when the epoch ended first, so nothing stale is used. A refused or
 * unreadable read rejects with a message the page can show.
 */
export async function readWholeTranscript(
  request: WholeTranscriptRequest,
  epoch: DownloadEpoch,
  purpose: WholeTranscriptPurpose = WholeTranscriptPurpose.Download,
): Promise<WholeTranscript | null> {
  let response: Response;
  try {
    response = await fetch(`${API_URL_BASE}/transcripts/${encodeURIComponent(request.transcriptId)}/content`, {
      headers: getAuthHeaders(),
      cache: "no-store",
      signal: epoch.signal,
    });
  } catch (error) {
    if (epoch.signal.aborted) return null;
    throw error;
  }
  if (!response.ok) {
    throw new ApiError(
      response.status,
      `the transcript ${purpose} could not be read (status ${response.status}). try again; if it keeps failing, reload the page.`,
    );
  }
  let text: string;
  try {
    text = await response.text();
  } catch (error) {
    if (epoch.signal.aborted) return null;
    throw error;
  }
  if (epoch.signal.aborted || !epoch.isCurrent()) return null;
  const read = parseTranscriptRead(text, request.knownHarness);
  if (!isSessionDetailPayload(read.content)) {
    throw new Error(
      `the transcript ${purpose} needs the stored copy in a format this page cannot read. publish the transcript again from peasant, then try again.`,
    );
  }
  const detail = read.content;
  const viewModel = cookWholeTranscript(detail, {
    storedTitle: request.storedTitle,
    relationshipNavigation: request.relationshipNavigation,
    published: read.published,
  });
  if (epoch.signal.aborted || !epoch.isCurrent()) return null;
  return { detail, viewModel };
}

/**
 * A download from a page that holds only part of the transcript: one read of
 * the whole stored copy, saved while the epoch is current. Resolves `true`
 * when a file was handed to the browser and `false` when the epoch ended first.
 */
export async function readTranscriptForDownload(
  request: TranscriptDownloadRequest,
  epoch: DownloadEpoch,
): Promise<boolean> {
  const whole = await readWholeTranscript(request, epoch);
  if (!whole) return false;
  saveTranscriptFile(
    transcriptFile(request.format, { viewModel: whole.viewModel, detail: whole.detail, url: request.url, transcriptId: request.transcriptId }),
  );
  return true;
}
