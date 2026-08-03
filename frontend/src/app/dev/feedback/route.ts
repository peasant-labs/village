import { NextResponse } from "next/server";
import { promises as fs } from "fs";
import path from "path";

/**
 * Dev-only feedback sink at `POST /dev/feedback`. The in-app comment tool
 * (`DevAnnotateOverlay`) POSTs captured DOM context + a comment here; each
 * entry is appended as a markdown block to `dev-feedback.md` at the frontend
 * project root.
 *
 * The path is intentionally OFF the `/api/*` prefix: under the Docker stack,
 * Caddy routes `/api/*` to the Go backend, so an `/api/...` path would never
 * reach this handler.
 *
 * Disabled outside development so it can never write files in production.
 */

export const runtime = "nodejs";

interface FeedbackPayload {
  route?: string;
  anchor?: string;
  selector?: string;
  snippet?: string;
  comment?: string;
}

const FEEDBACK_FILE = path.join(process.cwd(), "dev-feedback.md");
const FEEDBACK_HEADER =
  "# Dev Feedback\n\n" +
  "UI feedback captured via the in-app comment tool (`DevAnnotateOverlay`).\n\n" +
  "---\n\n";

export async function POST(req: Request) {
  if (process.env.NODE_ENV !== "development") {
    return NextResponse.json(
      { error: "dev-feedback is only available in development" },
      { status: 403 },
    );
  }

  let body: FeedbackPayload;
  try {
    body = (await req.json()) as FeedbackPayload;
  } catch {
    return NextResponse.json({ error: "invalid JSON body" }, { status: 400 });
  }

  const comment = (body.comment ?? "").trim();
  if (!comment) {
    return NextResponse.json({ error: "comment is required" }, { status: 400 });
  }

  const entry = [
    `## ${new Date().toISOString()} — \`${body.route || "/"}\``,
    "",
    `- **Anchor:** ${body.anchor || "—"}`,
    `- **Selector:** \`${body.selector || "—"}\``,
    "",
    comment,
    "",
    "<details><summary>HTML snippet</summary>",
    "",
    "```html",
    (body.snippet ?? "").trim(),
    "```",
    "",
    "</details>",
    "",
    "---",
    "",
  ].join("\n");

  try {
    try {
      await fs.access(FEEDBACK_FILE);
    } catch {
      await fs.writeFile(FEEDBACK_FILE, FEEDBACK_HEADER, "utf8");
    }
    await fs.appendFile(FEEDBACK_FILE, entry, "utf8");
  } catch (e) {
    return NextResponse.json(
      { error: e instanceof Error ? e.message : "failed to write feedback file" },
      { status: 500 },
    );
  }

  return NextResponse.json({ ok: true, file: "dev-feedback.md" });
}
