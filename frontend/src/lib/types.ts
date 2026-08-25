
/**
 * One project's resolved identity, as the server answers it.
 *
 * The correction routes (`PATCH /users/me/projects/{projectHash}` and
 * `DELETE .../display-name`) answer with exactly this shape, so a client reads
 * the new name and the tier it came from out of the response instead of
 * re-deriving either one.
 *
 * `project_remote_label` is a Go `string` on the wire, so an unknown remote
 * arrives as `""`, never `null` — a project with no git remote is a normal
 * state, and the subtitle is simply omitted for it.
 */
export interface ResolvedProject {
  project_hash: string;
  project_display_name: string;
  project_name_source: NameSource;
  project_remote_label: string;
}

/**
 * One collective in a project page's roll-up.
 *
 * It carries NO share counters beyond `transcript_count`. The roll-up is
 * restricted to APPROVED shares, and the pending and rejected tallies belong to
 * the project owner alone — this page can be loaded by someone who is not the
 * owner, so carrying them here would be a disclosure change, not a convenience.
 * The owner reads those counters from their own contributions surface instead.
 */
export interface ProjectCollectiveRollupEntry {
  id: string;
  name: string;
  description: string | null;
  linked_github_org: string | null;
  transcript_count: number;
}

/**
 * `GET /api/v1/users/{username}/projects/{projectHash}` (AuthOptional).
 *
 * `transcripts` holds only the rows this viewer may see, and `collectives` only
 * the collectives this viewer may see whose contributor listing the owner has
 * opted into. Both are normally empty for a viewer who is not the owner; an
 * empty list is an ordinary answer, never an error.
 */
export interface UserProjectPageResponse {
  project: ResolvedProject;
  owner: User;
  transcripts: Transcript[];
  collectives: ProjectCollectiveRollupEntry[];
}
import type { SessionOrigin } from "@/lib/sessionOrigin";

export interface User {
  id: string;
  github_id: number;
  github_username: string;
  display_name: string | null;
  avatar_url: string | null;
  created_at: string;
  updated_at: string;
  is_discoverable: boolean;
  // Canonical handle (github_username) is now user-chosen and globally unique.
  // username_chosen is false until the user completes the post-SSO step;
  // provider_username is the raw handle from the OAuth provider (suggestion).
  username_chosen: boolean;
  provider_username: string | null;
}

/**
 * The tier a project's resolved display name came from (village backend
 * `internal/projectname.NameSource`). These routes are deliberately outside
 * the OpenAPI spec, so there is no generated TypeScript type — this is the
 * frontend's own closed, hand-declared union. Widening the server-side enum
 * without adding a member here must fail the BUILD, not silently render an
 * unstyled fallback: see {@link assertNameSourceExhaustive} for the
 * compile-time proof, and `describeNameSource` in `@/lib/format` for the one
 * live exhaustive consumer.
 */
export type NameSource = "override" | "consented" | "remote" | "privacy";

/**
 * Compile-time exhaustiveness proof for {@link NameSource}. A `switch` over
 * every declared source in production code must reach a `default` branch
 * that assigns its input to `never` — if `NameSource` gains a member without
 * a matching `case`, the `never` assignment fails to typecheck and the build
 * breaks. This function has no runtime purpose; it exists to be called from
 * a `default` branch as `assertNameSourceExhaustive(source)`.
 */
export function assertNameSourceExhaustive(source: never): never {
  throw new Error(`unhandled NameSource: ${String(source)}`);
}

export interface Transcript {
  id: string;
  owner_id: string;
  local_id: string;
  title: string | null;
  description: string | null;
  visibility: "public" | "private" | "shared";
  model_provider: string;
  model_name: string | null;
  harness_version: string | null;
  session_start: string | null;
  session_end: string | null;
  turn_count: number | null;
  token_count: number | null;
  blob_size_bytes: number | null;
  schema_version: string;
  published_at: string;
  updated_at: string;
  parent_session_id: string | null;
  ingested_at: string | null;
  source_format: string | null;
  git_branch: string | null;
  git_remote: string | null;
  /**
   * A project's identity, not its name. Required at the database trust
   * boundary: `transcripts.project_hash` carries a `NOT NULL` constraint
   * (migration `035_project_hash_required`) enforced behind a publish-time
   * guard, and every response path this frontend renders selects directly
   * `FROM transcripts` — `ListTranscripts`/`GetTranscriptByID`
   * (`backend/internal/database/queries/transcripts.sql`) and
   * `ListGroupTranscripts` (`backend/internal/database/queries/shares.sql`)
   * — with no other source table in the union. sqlc's own generated row
   * types for all three queries already narrow this column to a plain Go
   * `string` (see `backend/internal/database/sqlc/transcripts.sql.go` and
   * `shares.sql.go`), confirming the guarantee independently of this
   * comment. A frontend value that is empty despite the type is therefore a
   * genuine contract violation, not a state to silently paper over —
   * `groupByProject` in `@/lib/format` treats it as such rather than
   * crashing the page.
   */
  project_hash: string;
  project_name: string | null;
  /**
   * The one resolved project display name every surface must render
   * (village project-identity resolver, `override > consented > remote >
   * privacy`). Never empty — the resolver always synthesises a privacy-safe
   * fallback when no other tier applies. Prefer this over `project_name`,
   * which stays a raw, unresolved wire column.
   */
  project_display_name: string;
  /** Which resolution tier produced {@link project_display_name}. */
  project_name_source: NameSource;
  /** `host:owner/repo` when a git remote is known, else null. */
  project_remote_label: string | null;
  tool_call_count: number | null;
  subagent_count: number | null;
  duration_ms: number | null;
  tokens_in: number | null;
  tokens_out: number | null;
  subagents: unknown[] | null;
  diagnostics_warnings: string[] | null;
  diagnostics_partial: boolean | null;
  title_generated: string | null;
  outcome: string | null;
  files_touched: number | null;
  lines_changed: number | null;
  retry_loops: number | null;
  retry_tokens_wasted: number | null;
  within_session_reverts: number | null;
  signal_density: number | null;
  spec_quality_score: number | null;
  exploration_ratio: number | null;
  scope_breadth: number | null;
  discovery_turns: number | null;
  m2_token_outcome_ratio: number | null;
  m3_unique_tool_count: number | null;
  m4_error_recovery_count: number | null;
  m4_consecutive_error_max: number | null;
  m5_context_utilization_pct: number | null;
  m5_peak_context_tokens: number | null;
  m5_avg_message_tokens: number | null;
  m6_output_survival_pct: number | null;
  m6_lines_survived: number | null;
  m6_lines_total: number | null;
  m7_spec_word_count: number | null;
  m7_spec_has_examples: boolean | null;
  m7_spec_has_constraints: boolean | null;
  computed_at: string | null;
  compute_version: number | null;
  content_hash: string | null;
  license_id: string | null;
  /**
   * Who drove the session. Discovery metadata only: an `agent` row is
   * collapsed into a labelled group instead of occupying a root-level list,
   * and every value still opens normally from a direct link.
   */
  session_origin: SessionOrigin;
}

interface Tag {
  id: string;
  name: string;
}

export interface TagWithCount extends Tag {
  usage_count: number;
}

export interface Group {
  id: string;
  name: string;
  description: string | null;
  linked_github_org: string | null;
  display_members: boolean;
  transcript_deletion_policy: "user_choice" | "mandatory";
  created_by: string;
  created_at: string;
  updated_at: string;
  acceptance_mode: "open" | "verified_only" | "curated";
  data_access: "members_only" | "contributors" | "public";
  role: string;
  member_since: string | null;
  // Optional aggregate counts. NOT populated by the current `GET /groups`
  // handler (`ListUserGroups` / `ListAllGroups` select group + membership
  // columns only, no member/transcript aggregate -- only `SearchCollectives`
  // computes these today). Declared optional so the collectives-list card
  // can render them once a future backend change adds them, without a
  // frontend change; until then they are simply omitted (see groups/page.tsx).
  member_count?: number;
  transcript_count?: number;
}

export interface UserGroupShare {
  id: string;
  title: string | null;
  model_provider: string;
  model_name: string | null;
  visibility: "private" | "shared" | "public";
  published_at: string;
  turn_count: number | null;
  tokens_in: number | null;
  tokens_out: number | null;
  status: "pending" | "approved" | "rejected";
  shared_at: string;
}

export interface GroupTranscriptStats {
  total_transcripts: number;
  contributor_count: number;
  total_turns: number;
  total_duration_ms: number;
  total_tokens: number;
}

export interface GroupModelBreakdown {
  model_provider: string;
  transcript_count: number;
}

export interface GroupContributor {
  id: string;
  github_username: string;
  avatar_url: string | null;
  transcript_count: number;
}

export interface GroupMember {
  role: string;
  joined_at: string;
  id: string;
  github_username: string;
  display_name: string | null;
  avatar_url: string | null;
  github_orgs?: string[];
}

export interface GroupTranscript extends Transcript {
  owner_username: string;
  owner_avatar_url: string | null;
  owner_is_discoverable: boolean;
}

interface TranscriptShare {
  group_id: string;
  group_name: string;
  shared_at: string;
}

interface EnrichedTranscriptShare {
  transcript_id: string;
  group_id: string;
  group_name: string;
  acceptance_mode: "open" | "verified_only" | "curated";
  status: string;
  shared_at: string;
}

export interface UserOrg {
  org_id: number;
  org_login: string;
  avatar_url: string | null;
  visible: boolean;
  fetched_at: string;
}

export interface CollectiveSearchResult {
  id: string;
  name: string;
  description: string | null;
  linked_github_org: string | null;
  member_count: number;
  transcript_count: number;
}

export interface CollectiveSearchResponse {
  collectives: CollectiveSearchResult[];
}

/**
 * A GitHub repository linked to a collective via the GitHub App. Wire shape of
 * the backend `repoResponse` (handler/collective_repos.go). pgtype-backed
 * fields (`pgtype.UUID`, `pgtype.Timestamptz`) marshal to a string when present
 * and `null` when absent.
 */
export interface LinkedRepository {
  id: string;
  group_id: string;
  owner: string;
  name: string;
  installation_id: number;
  is_private: boolean;
  linked_by: string | null;
  last_synced_at: string | null;
  created_at: string | null;
}

export interface LinkedRepositoriesResponse {
  repositories: LinkedRepository[];
}

/** A cached commit for a linked repository (backend `commitResponse`). */
export interface RepositoryCommit {
  sha: string;
  message: string | null;
  author_name: string | null;
  author_email: string | null;
  authored_at: string | null;
  committed_at: string | null;
}

export interface RepositoryCommitsResponse {
  owner: string;
  name: string;
  refreshed: boolean;
  last_synced: string | null;
  commit_count: number;
  commits: RepositoryCommit[];
}

/**
 * A git commit captured for a single transcript's session (backend
 * `GET /api/v1/transcripts/{id}/commits`). The daemon records the commits a
 * coding session produced; joining these SHAs against a linked repo's cached
 * commits (matched on `sha`) tells us which repo commits a transcript touched.
 *
 * Note the camelCase wire shape — distinct from the snake_case
 * {@link RepositoryCommit} returned by the repository commits endpoint.
 */
export interface TranscriptCommit {
  sha: string;
  message: string | null;
  authorName: string | null;
  authorEmail: string | null;
  authoredAt: string | null;
  committedAt: string | null;
  /** Ordinal position of the commit within the session (0-based). */
  order: number;
}

export interface TranscriptCommitsResponse {
  commits: TranscriptCommit[];
}

export interface Attestation {
  id: string;
  transcript_id: string;
  org_login: string;
  attestation_type: "used_in_training" | "referenced" | "evaluated" | "deployed";
  note: string | null;
  created_at: string;
  attester_username: string;
  attester_avatar: string | null;
}

interface AttestationSummary {
  transcript_id: string;
  org_login: string;
  attestation_type: string;
  created_at: string;
}

export interface TranscriptListItem {
  transcript: Transcript;
  tags: Tag[];
  owner: User;
  shares?: EnrichedTranscriptShare[];
  attestations?: AttestationSummary[];
}

export interface TranscriptListResponse {
  transcripts: TranscriptListItem[];
  total: number;
  /**
   * How many agent-driven sessions the SAME filters match. The listed rows
   * never include them, so this is what the collapsed group counts.
   */
  agent_total: number;
  page: number;
  limit: number;
}

export interface TranscriptDetailResponse {
  transcript: Transcript;
  tags: Tag[];
  shares: TranscriptShare[];
  enriched_shares: EnrichedTranscriptShare[];
  owner: User;
  attestations?: Attestation[];
}

/**
 * One collective the signed-in person has offered transcripts to, as served by
 * `GET /users/me/collectives/contributions`.
 *
 * The four counters DO NOT ALL COUNT THE SAME UNIT, and that asymmetry is part
 * of the contract rather than an implementation detail:
 *  - {@link approved_count} and {@link pending_count} count DISTINCT
 *    TRANSCRIPTS. A transcript is either held by a collective or it is not.
 *  - {@link rejected_attempt_count} and {@link withdrawn_attempt_count} count
 *    EVENTS. One transcript refused three times by one collective is three;
 *    one transcript withdrawn and resubmitted twice is two withdrawals.
 * Any surface rendering these numbers side by side has to say which unit each
 * one measures, or they read as comparable when they are not. See
 * {@link CONTRIBUTION_COUNTER_UNITS} in `@/lib/shareEvents` for the one place
 * that wording is declared.
 *
 * `withdrawn_attempt_count` groups the two withdrawal outcomes (`retracted`,
 * the owner's own act; `revoked`, the collective's) into ONE counter. That
 * grouping is a counter-level simplification only — the per-submission event
 * history (`ShareEvent`) still distinguishes them by actor, and must keep
 * doing so; only the tally here folds them together.
 */
export interface ContributedCollective {
  id: string;
  name: string;
  description: string | null;
  linked_github_org: string | null;
  /** Distinct transcripts of yours this collective currently holds. */
  approved_count: number;
  /** Distinct transcripts of yours currently awaiting this collective's review. */
  pending_count: number;
  /** Refusal EVENTS, not transcripts. */
  rejected_attempt_count: number;
  /** Withdrawal EVENTS (`retracted` + `revoked` combined), not transcripts. */
  withdrawn_attempt_count: number;
}

/**
 * One accepted membership of a transcript in a collective the viewer may see,
 * as served by `GET /transcripts/{id}/collectives`.
 *
 * The server answers with an EMPTY LIST, never a refusal, when the collective
 * is invisible to the viewer or the transcript's owner has not opted in to
 * being listed as a contributor. A consumer therefore renders an empty result
 * as plain emptiness: anything that reads as "there is something here you may
 * not see" re-creates exactly the disclosure the empty list exists to avoid.
 */
export interface TranscriptCollective {
  id: string;
  name: string;
  description: string | null;
  linked_github_org: string | null;
  shared_at: string;
}

/**
 * The outcome recorded on one share event (village backend
 * `transcript_share_attempts.status`). These profile routes are deliberately
 * outside the OpenAPI spec, so there is no generated TypeScript type — this is
 * the frontend's own closed, hand-declared union, and
 * {@link assertShareEventStatusExhaustive} is its compile-time proof.
 *
 * `retracted` (the owner withdrew) and `revoked` (the collective removed) are
 * distinct terminal states, distinct from each other AND from `rejected`.
 * Collapsing any of them into another makes the history unreadable.
 */
export type ShareEventStatus =
  | "pending"
  | "approved"
  | "rejected"
  | "retracted"
  | "revoked";

/**
 * Compile-time exhaustiveness proof for {@link ShareEventStatus}, used the same
 * way as {@link assertNameSourceExhaustive}: a `switch` in production code
 * reaches a `default` branch that assigns its input to `never`, so widening the
 * union without handling the new member fails the BUILD.
 */
export function assertShareEventStatusExhaustive(status: never): never {
  throw new Error(`unhandled ShareEventStatus: ${String(status)}`);
}

/**
 * WHO acted on a share event, in what capacity — never WHO they are.
 *
 * The server sends a closed actor CLASS and deliberately never a user id:
 * telling a submitter which moderator refused their work is a disclosure the
 * design does not make. There is therefore no name to look up and no lookup
 * that could be missing, so a consumer must not render "unknown" or any other
 * wording that implies one failed.
 *
 * The empty string is the wire's value for an event that has not been decided
 * yet (a `pending` event has no actor because nothing has been decided).
 */
export type ShareEventActor = "" | "owner" | "collective" | "moderator";

/** Compile-time exhaustiveness proof for {@link ShareEventActor}. */
export function assertShareEventActorExhaustive(actor: never): never {
  throw new Error(`unhandled ShareEventActor: ${String(actor)}`);
}

/**
 * One entry of the owner-only share-event history for a (transcript,
 * collective) pair, as served by
 * `GET /users/me/collectives/{groupId}/transcripts/{transcriptId}/events`.
 *
 * The server returns the FULL history in ascending {@link event_num} order, so
 * it reads top to bottom as an audit log: every state change is an event,
 * including the withdrawals nobody submitted. {@link event_num} is therefore an
 * event ordinal and never an "attempt number" in rendered copy.
 */
export interface ShareEvent {
  event_num: number;
  status: ShareEventStatus;
  recorded_at: string;
  /** Null until the event is decided. */
  decided_at: string | null;
  decided_by_actor: ShareEventActor;
}

/**
 * One (transcript, collective) LEDGER PAIR, as served by the owner-only
 * `GET /users/me/collectives/{groupId}/submissions` (a BARE JSON array, the
 * same envelope-free shape as the sibling events endpoint).
 *
 * This is EVERY pair the owner has ever had with the collective, including a
 * pair whose every event ended in a withdrawal and so has no row left in the
 * legacy current-state list (`GET /groups/{id}/my-shares`). A fully-withdrawn
 * pair still appears here, carrying its latest ({@link status},
 * {@link event_num}, {@link recorded_at}) — the same fields the events
 * endpoint's last row would show for it. This is deliberately NOT the
 * current-state (`transcript_shares`) source: that source drops a pair the
 * moment its last event is a withdrawal, which is the exact contradiction
 * this endpoint exists to close (a nonzero withdrawn counter beside an empty
 * list, as one user acceptance test caught).
 *
 * WHEN THE OWNER HAS NO PAIRS FOR THE COLLECTIVE, the endpoint answers 404,
 * never a 200 with an empty array — the SAME disposition as "no such
 * collective", so asking cannot be used to discover which collectives exist
 * or who contributed to them. See {@link useMyCollectiveSubmissions} in
 * `@/lib/queries/collectives`, which normalizes that 404 to an empty list for
 * consumers: the rendered empty state is unaffected either way.
 */
export interface CollectiveSubmissionPair {
  transcript_id: string;
  group_id: string;
  /** Null when the transcript has no title. Never derived or guessed. */
  title: string | null;
  status: ShareEventStatus;
  event_num: number;
  recorded_at: string;
}
