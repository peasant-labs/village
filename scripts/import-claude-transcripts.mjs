#!/usr/bin/env node
//
// One-shot importer: takes real Claude Code session transcripts from
// ~/.claude/projects, converts them into a structured `SessionDetailPayload`
// (the shape the v2 transcript viewer consumes), scrubs anything the village
// secret-scanner would reject, and publishes them to the local village via
// the real HTTP publish endpoint.
//
// Auth: mints a short-lived session JWT for an existing village user, using
// the backend's JWT_SECRET — so no API key / browser cookie is needed.
//
// Run:  JWT_SECRET="$(cd .. && docker compose exec -T backend printenv JWT_SECRET)" \
//       node scripts/import-claude-transcripts.mjs
//
import { readFileSync } from "node:fs";
import { createHmac, createHash } from "node:crypto";
import { execSync } from "node:child_process";
import { homedir } from "node:os";

process.env.NODE_TLS_REJECT_UNAUTHORIZED = "0"; // local self-signed Caddy cert

const API = process.env.VILLAGE_API || "https://localhost:8443/api/v1";
const JWT_SECRET = process.env.JWT_SECRET;
if (!JWT_SECRET) {
  console.error('Set JWT_SECRET — e.g. JWT_SECRET="$(docker compose exec -T backend printenv JWT_SECRET)"');
  process.exit(1);
}

// Existing village user (from `SELECT id, github_username FROM users`).
const USER_ID = "a5a60708-0370-47e2-a165-fe80014df2b7";
const USERNAME = "vitorhw";

// Transcripts to import: real Claude Code sessions on this machine.
const HOME = homedir();
const TARGETS = [
  {
    project: "padcomp-website",
    repo: `${HOME}/Documents/Projects/padcomp-website`,
    file: `${HOME}/.claude/projects/-Users-vitorhugo-Documents-Projects-padcomp-website/8b45eb53-f7bb-4f5c-aa2f-d2021a272ef4.jsonl`,
  },
  {
    project: "advwelzel",
    repo: `${HOME}/Documents/Projects/advwelzel`,
    file: `${HOME}/.claude/projects/-Users-vitorhugo-Documents-Projects-advwelzel/329359ba-e1cb-4009-a72f-b70f69e1f6c1.jsonl`,
  },
];

// ── JWT (HS256) ─────────────────────────────────────────────────────────────
const b64url = (buf) =>
  Buffer.from(buf).toString("base64").replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");

function mintJWT(secret, userId, username) {
  const now = Math.floor(Date.now() / 1000);
  const header = b64url(JSON.stringify({ alg: "HS256", typ: "JWT" }));
  const payload = b64url(
    JSON.stringify({ user_id: userId, username, iat: now, exp: now + 3600 }),
  );
  const sig = b64url(createHmac("sha256", secret).update(`${header}.${payload}`).digest());
  return `${header}.${payload}.${sig}`;
}

// ── secret scrub (mirrors backend internal/scanner/redaction.go) ─────────────
// Applied to EVERY string field that ends up in the published blob — turn
// content, tool arguments, tool results. Tool I/O is where secrets live, so
// nothing structured escapes scrubbing.
function scrub(text) {
  if (typeof text !== "string") return text;
  return text
    .replace(/\/Users\/[A-Za-z0-9._-]+\//g, "~/")
    .replace(/C:\\Users\\[A-Za-z0-9._-]+\\/g, "C:\\Users\\~\\")
    .replace(/AKIA[0-9A-Z]{16}/g, "[REDACTED-AWS-KEY]")
    .replace(/(?:aws_secret_access_key)\s*=\s*\S+/gi, "aws_secret_access_key=[REDACTED]")
    .replace(/gh[pousr]_[A-Za-z0-9_]{36,}/g, "[REDACTED-GH-TOKEN]")
    .replace(/-----BEGIN (?:RSA|DSA|EC|OPENSSH) PRIVATE KEY-----/g, "[REDACTED-PRIVATE-KEY]")
    .replace(/((?:api[_-]?key|apikey))\s*[:=]\s*["']?[A-Za-z0-9_\-]{20,}/gi, "$1=[REDACTED]")
    .replace(/xox[baprs]-[0-9A-Za-z-]+/g, "[REDACTED-SLACK-TOKEN]")
    .replace(/((?:private[_-]?key))\s*[:=]\s*["']?\S{20,}/gi, "$1=[REDACTED]");
}

/** Deep-scrub every string inside an arbitrary JSON value. */
function scrubDeep(value) {
  if (typeof value === "string") return scrub(value);
  if (Array.isArray(value)) return value.map(scrubDeep);
  if (value && typeof value === "object") {
    const out = {};
    for (const [k, v] of Object.entries(value)) out[k] = scrubDeep(v);
    return out;
  }
  return value;
}

// ── content-block helpers ────────────────────────────────────────────────────
function extractText(content) {
  if (typeof content === "string") return content;
  if (Array.isArray(content)) {
    return content
      .map((b) => (b && typeof b === "object" && b.type === "text" ? b.text : null))
      .filter((t) => typeof t === "string" && t.trim())
      .join("\n");
  }
  return "";
}

function hasThinkingBlock(content) {
  return (
    Array.isArray(content) &&
    content.some((b) => b && typeof b === "object" && b.type === "thinking")
  );
}

/** Normalize a tool_result `content` field (string or block array) to a string. */
function toolResultToString(content) {
  if (typeof content === "string") return content;
  if (Array.isArray(content)) {
    return content
      .map((b) => {
        if (typeof b === "string") return b;
        if (b && typeof b === "object") {
          if (b.type === "text" && typeof b.text === "string") return b.text;
          return JSON.stringify(b);
        }
        return "";
      })
      .filter(Boolean)
      .join("\n");
  }
  if (content == null) return "";
  return JSON.stringify(content);
}

// ── tool classification ──────────────────────────────────────────────────────
/** Map a Claude Code tool name → schema ToolCallKind. */
function classifyTool(name) {
  switch ((name || "").toLowerCase()) {
    case "read":
    case "notebookread":
      return "read";
    case "edit":
    case "write":
    case "multiedit":
    case "notebookedit":
      return "edit";
    case "bash":
    case "bashoutput":
    case "killbash":
      return "execute";
    case "grep":
    case "glob":
    case "ls":
      return "search";
    case "webfetch":
      return "fetch";
    case "websearch":
      return "search";
    case "task":
    case "todowrite":
    case "exitplanmode":
      return "other";
    default:
      return "other";
  }
}

/** Pull a file path out of Read/Write/Edit/Glob tool input. */
function extractFilePath(input) {
  if (!input || typeof input !== "object") return undefined;
  const p = input.file_path || input.path || input.notebook_path || input.filePath;
  return typeof p === "string" ? p : undefined;
}

/** Pull a Bash exit code out of a tool result, if the harness embeds one. */
function extractExitCode(resultText) {
  if (typeof resultText !== "string") return undefined;
  const m = resultText.match(/(?:exit code|exited with code|exit status)[:\s]+(\d+)/i);
  return m ? Number(m[1]) : undefined;
}

// ── conversion: raw Claude Code JSONL → SessionDetailPayload ─────────────────
function buildSessionDetail(filePath, sessionId, projectName) {
  const lines = readFileSync(filePath, "utf8").split("\n").filter(Boolean);

  // Parse all events first.
  const events = [];
  for (const line of lines) {
    try {
      events.push(JSON.parse(line));
    } catch {
      /* skip malformed line */
    }
  }

  // First pass: index every tool_result by tool_use_id (results appear in a
  // later user event's content array).
  const resultsById = new Map();
  for (const e of events) {
    const content = e?.message?.content;
    if (!Array.isArray(content)) continue;
    for (const b of content) {
      if (b && typeof b === "object" && b.type === "tool_result" && b.tool_use_id) {
        resultsById.set(b.tool_use_id, {
          text: toolResultToString(b.content),
          isError: !!b.is_error,
        });
      }
    }
  }

  // Second pass: build one TurnDetail per user/assistant message.
  const turns = [];
  let startMs = null;
  let endMs = null;
  let model = "claude";
  let toolCallCount = 0;
  let tokensIn = 0;
  let tokensOut = 0;

  for (const e of events) {
    if (e?.timestamp) {
      const ms = Date.parse(e.timestamp);
      if (!Number.isNaN(ms)) {
        startMs ??= ms;
        endMs = ms;
      }
    }

    if ((e.type !== "user" && e.type !== "assistant") || !e.message) continue;
    const msg = e.message;
    if (msg.model) model = msg.model;

    const usage = msg.usage || {};
    const turnIn = (usage.input_tokens || 0) + (usage.cache_read_input_tokens || 0) +
      (usage.cache_creation_input_tokens || 0);
    const turnOut = usage.output_tokens || 0;
    tokensIn += turnIn;
    tokensOut += turnOut;

    const content = msg.content;

    // Tool calls from this message's tool_use blocks.
    const toolCalls = [];
    if (Array.isArray(content)) {
      for (const b of content) {
        if (b && typeof b === "object" && b.type === "tool_use") {
          const res = resultsById.get(b.id);
          const resultText = res ? res.text : "";
          const args = JSON.stringify(b.input ?? {});
          const kind = classifyTool(b.name);
          const tc = {
            id: String(b.id || ""),
            name: String(b.name || ""),
            arguments: scrub(args),
            result: scrub(resultText),
            isError: res ? res.isError : false,
            toolKind: kind,
          };
          const fp = extractFilePath(b.input);
          if (fp) tc.filePath = scrub(fp);
          if (kind === "execute") {
            const code = extractExitCode(resultText);
            if (code !== undefined) tc.exitCode = code;
          }
          toolCalls.push(tc);
        }
      }
    }
    toolCallCount += toolCalls.length;

    const text = scrub(extractText(content)).trim();

    // A turn carries weight if it has text OR tool calls. Skip pure
    // tool_result-only user events (their payload lives on the assistant
    // turn's toolCalls instead).
    const isToolResultOnly =
      Array.isArray(content) &&
      content.length > 0 &&
      content.every((b) => b && typeof b === "object" && b.type === "tool_result");
    if (!text && toolCalls.length === 0 && isToolResultOnly) continue;
    if (!text && toolCalls.length === 0) continue;

    const role = msg.role || e.type;
    const turn = {
      index: turns.length,
      role,
      content: text,
      timestamp: e.timestamp || new Date().toISOString(),
      entryType: toolCalls.length > 0 ? "tool_use" : "text",
      hasThinking: hasThinkingBlock(content),
      tokensIn: turnIn,
      tokensOut: turnOut,
    };
    if (toolCalls.length > 0) turn.toolCalls = toolCalls;
    if (msg.stop_reason) turn.stopReason = msg.stop_reason;
    turns.push(turn);
  }

  const start = startMs ?? Date.now() - 3600000;
  const end = endMs ?? Date.now();
  const durationMs = Math.max(0, end - start);

  const detail = {
    id: sessionId,
    provider: "claude",
    startTime: new Date(start).toISOString(),
    endTime: new Date(end).toISOString(),
    durationMins: Math.round(durationMs / 60000),
    totalTokens: tokensIn + tokensOut,
    tokensIn,
    tokensOut,
    turnCount: turns.length,
    toolCallCount,
    turns,
    source: "imported",
    project: projectName,
    model,
  };

  return { detail, startMs: start, endMs: end, model, durationMs };
}

function gitRemote(repo) {
  try {
    return execSync(`git -C "${repo}" remote get-url origin`, { stdio: ["ignore", "pipe", "ignore"] })
      .toString().trim() || undefined;
  } catch { return undefined; }
}

// ── publish ─────────────────────────────────────────────────────────────────
async function publish(token, target) {
  const sessionId = target.file.split("/").pop().replace(/\.jsonl$/, "");
  const { detail, startMs, endMs, model, durationMs } = buildSessionDetail(
    target.file,
    sessionId,
    target.project,
  );
  if (detail.turns.length === 0) throw new Error("no conversation turns extracted");

  const metadata = {
    identity: { sessionId, schemaVersion: 2 },
    model: { modelHarness: "claude", model, version: "claude-code" },
    timestamp: { start: startMs, end: endMs },
    source: { format: "json", filePath: `~/.claude/projects/${target.project}/${sessionId}.jsonl` },
    git: { branch: "main", remote: gitRemote(target.repo) },
    project: {
      hash: createHash("sha256").update(target.repo).digest("hex"),
      name: target.project,
      filePath: target.repo,
    },
    stats: {
      turnCount: detail.turnCount,
      toolCallCount: detail.toolCallCount,
      subagentCount: 0,
      durationMs,
      tokensIn: detail.tokensIn,
      tokensOut: detail.tokensOut,
    },
    diagnostics: { warnings: [] },
  };

  // The uploaded blob IS a SessionDetailPayload. Deep-scrub once more as a
  // belt-and-braces guard so no /Users/x/ path or token slips through.
  const blob = JSON.stringify(scrubDeep(detail));
  const fd = new FormData();
  fd.append("metadata", JSON.stringify(metadata));
  fd.append("transcript_file", new Blob([blob], { type: "application/json" }), "transcript.json");

  const res = await fetch(`${API}/transcripts/publish`, {
    method: "POST",
    headers: { Authorization: `Bearer ${token}` },
    body: fd,
  });
  const body = await res.text();
  console.log(
    `  ${target.project}: ${detail.turnCount} turns, ${detail.toolCallCount} tool calls → HTTP ${res.status}`,
  );
  if (!res.ok) console.log(`    ${body}`);
  return res.ok;
}

// ── run ─────────────────────────────────────────────────────────────────────
const token = mintJWT(JWT_SECRET, USER_ID, USERNAME);
console.log(`Importing ${TARGETS.length} transcripts to ${API} as @${USERNAME}…`);
let ok = 0;
for (const t of TARGETS) {
  try { if (await publish(token, t)) ok++; }
  catch (e) { console.log(`  ${t.project}: FAILED — ${e.message}`); }
}
console.log(`\nDone: ${ok}/${TARGETS.length} imported.`);
console.log("They publish as visibility=private — sign in as @vitorhw at https://localhost:8443 to see them.");
process.exit(ok === TARGETS.length ? 0 : 1);
