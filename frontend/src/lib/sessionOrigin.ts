/**
 * Who drove a published session, as served by the Village API.
 *
 * The menu mirrors the closed `transcripts.session_origin` database check and
 * the Go `internal/sessionorigin` menu. Only `agent` is collapsed out of a
 * root-level list; `unknown` is the fail-safe value and is presented exactly
 * like `user`, so a session Village could not classify is never hidden.
 */
export const SESSION_ORIGINS = ["user", "agent", "unknown"] as const;

export type SessionOrigin = (typeof SESSION_ORIGINS)[number];

/** The query-parameter value that asks the list endpoint for agent sessions. */
export const AGENT_ORIGIN: SessionOrigin = "agent";

/**
 * Narrow an untrusted value from the wire. An unrecognised value is treated as
 * unclassified rather than guessed: guessing `agent` would hide a person's
 * session, and there is no reading of an unknown token that justifies that.
 */
export function asSessionOrigin(value: unknown): SessionOrigin {
  return SESSION_ORIGINS.includes(value as SessionOrigin) ? (value as SessionOrigin) : "unknown";
}

/** Whether a row should carry the agent-session label. */
export function isAgentSession(value: unknown): boolean {
  return asSessionOrigin(value) === AGENT_ORIGIN;
}

/**
 * The collapsed group's label. Lowercase chrome with a tabular count, matching
 * the rest of the product's list chrome.
 */
export function agentSessionGroupLabel(count: number): string {
  return `${count} agent session${count === 1 ? "" : "s"}`;
}
