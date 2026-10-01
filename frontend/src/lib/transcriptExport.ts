import type { adaptTranscript } from "@peasant-labs/fairtrade/ui";
import type { SessionDetailPayload } from "@/types/messages";

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
