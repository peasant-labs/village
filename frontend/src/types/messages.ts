/**
 * TypeScript types mirroring the Go structs in internal/api/messages.go.
 */

// -- Tool Call Classification -------------------------------------------------

/** Mirrors @peasant-labs/schema ToolCallKind — ACP-aligned tool classification. */
export const ToolCallKind = {
  Read:    'read',
  Edit:    'edit',
  Delete:  'delete',
  Move:    'move',
  Search:  'search',
  Execute: 'execute',
  Think:   'think',
  Fetch:   'fetch',
  Other:   'other',
} as const;
export type ToolCallKind = typeof ToolCallKind[keyof typeof ToolCallKind];

// -- Entry Type Classification -----------------------------------------------

/** Mirrors @peasant-labs/schema EntryType — content part classification. */
const EntryType = {
  Text:       'text',
  ToolUse:    'tool_use',
  ToolResult: 'tool_result',
  Thinking:   'thinking',
  System:     'system',
  Error:      'error',
  Result:     'result',
} as const;
type EntryType = typeof EntryType[keyof typeof EntryType];

// -- Stop Reason Classification -----------------------------------------------

/** Mirrors @peasant-labs/schema StopReason — why a turn ended. */
const StopReason = {
  EndTurn:    'end_turn',
  MaxTokens:  'max_tokens',
  Cancelled:  'cancelled',
  ToolUse:    'tool_use',
  Error:      'error',
  MaxTurnRequests: 'max_turn_requests',
  Refusal:    'refusal',
} as const;
type StopReason = typeof StopReason[keyof typeof StopReason];

// -- Provider / Role ----------------------------------------------------------

// Provider is Village's narrow, brand-renderable alias over the canonical
// @peasant-labs/schema Harness. The schema remains the wire source of truth;
// values mirror the backend bestiary
// harness wire values (claude-code, gemini-cli, codex, opencode). The wire key
// is now `harness`; peasant emits it and village serves harness-keyed payloads,
// so the shared viewer reads `detail.harness`.
import type { Harness } from '@peasant-labs/schema';
export type Provider = Extract<Harness, 'claude-code' | 'gemini-cli' | 'codex' | 'opencode' | 'cursor'>;

export type Role = 'user' | 'assistant' | 'tool' | 'system';

// -- Payloads ----------------------------------------------------------------

/** A single conversation turn with optional tool calls. */
export interface TurnDetail {
  index: number;
  role: Role;
  content: string;
  toolCalls?: ToolCallDetail[];
  timestamp: string;
  /** Nesting depth: 0 = main agent, 1+ = subagent levels. */
  depth?: number;
  /** Agent name for subagent turns (e.g., "researcher", "test-runner"). */
  agentName?: string;

  // Enrichment fields — propagated from session_entries.
  /** Entry classification: text, tool_use, tool_result, thinking, system, error, result. */
  entryType?: EntryType;
  /** Whether the turn contains thinking/reasoning blocks. */
  hasThinking?: boolean;
  /** Why the turn ended: end_turn, max_tokens, cancelled, etc. */
  stopReason?: StopReason | null;
  /** Input tokens for this turn. */
  tokensIn?: number | null;
  /** Output tokens for this turn. */
  tokensOut?: number | null;
}

/** Tool invocation metadata inside a turn. */
export interface ToolCallDetail {
  id: string;
  name: string;
  arguments: string;
  result: string;
  /** Wall-clock duration in milliseconds. */
  durationMs?: number | null;
  /** Exit code for Bash tool calls. */
  exitCode?: number | null;
  /** Extracted file path for Read/Write/Edit/Glob. */
  filePath?: string;
  /** True when tool_result had is_error flag set. */
  isError?: boolean;
  /** Classified tool type: read, edit, execute, search, etc. */
  toolKind?: ToolCallKind;
}

/** A git commit made during the session. */
export interface SessionCommit {
  hash: string;
  message: string;
  timestamp: string;
  filesChanged: number;
  insertions: number;
  deletions: number;
}

// SessionGitContext is now provided by the shared
// @peasant-labs/schema package (as part of SessionDetailPayload); the
// village-local duplicate was removed when SessionDetailPayload became a
// re-export. SessionCommit is retained as a public type for callers.

/**
 * Full session detail pushed on the "session_detail" channel / parsed from a
 * REST transcript fetch. Consumed directly from the canonical
 * @peasant-labs/schema wire contract (single source of truth), so the
 * village and the viewer agree on the shape; this is the type fairtrade's
 * `<TranscriptViewer detail={...}>` prop expects, eliminating the previous
 * village-local vs shared-viewer mismatch. The wire key is now `harness`
 * (flipped from `provider`); the village serves harness-keyed
 * payloads via migrate-on-read. The shape includes the optional `scorecard`.
 */
export type { SessionDetailPayload, SessionScorecard } from '@peasant-labs/schema';
