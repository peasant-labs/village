/**
 * Adapter signature for the Manage lifted surface.
 *
 * Converts the useGroup hook result into the single cooked ManagePayload
 * that the lifted <Manage> component accepts as props. The pattern mirrors
 * SessionDetailV2: fetch → reshape → mount via props.
 *
 * Hook consumed: useGroup(id) from frontend/src/lib/queries/groups.ts
 * Returns: { group, members, transcripts, stats, models, contributors,
 *            can_read, your_role, pending_members? }
 *
 * TRANSFORM (small): all snake_case wire fields are renamed to camelCase;
 * `can_read` is omitted (auth/routing concern, not a display concern inside
 * the component).
 *
 * `pending_members` is optional on the wire. The adapter normalizes an omitted
 * value to [] so the moderation UI always receives a stable collection.
 *
 * Mutations (onCreateGroup, onUpdateGroup, onJoinGroup, onPromoteMember,
 * onRemoveMember, onAddMember, onDeleteGroup) are component-level callback
 * props supplied by the route — they are not part of ManagePayload.
 *
 * This is the village adapter seam: the route layer normalizes the hook output
 * here once, then hands the cooked payload to the manage surface shell.
 *
 * Wire types sourced from: frontend/src/lib/types.ts
 * Payload shape sourced from: @peasant-labs/fairtrade/commons.
 */

import type {
  Group,
  GroupMember,
  GroupTranscript,
  GroupTranscriptStats,
  GroupModelBreakdown,
  GroupContributor,
} from '@/lib/types';

// ── Enum display formatting ──────────────────────────────────────────────────

/**
 * Humanize a snake_case wire enum value into the fairtrade demo's display form:
 * `members_only` -> `members only`, `verified_only` -> `verified only`. The
 * fairtrade demo's GovTile only ever showed pretty text because its mockup
 * fixture happened to fall back to a pre-formatted default (`collective.dataAccess
 * || 'members only'`) rather than a real enum value; a real collective's
 * `data_access`/`acceptance_mode` is never empty, so that fallback never fires in
 * production and the raw wire value otherwise reaches the GovTile unformatted.
 * Applied at the adapter boundary so every Manage-surface consumer of these
 * enums gets the humanized form for free.
 */
export function humanizeEnum(value: string): string {
  return value.replace(/_/g, ' ');
}

// ── Payload type mirror (canonical source: @peasant-labs/fairtrade/commons) ────

type TranscriptVisibility = 'public' | 'private' | 'shared';
type AcceptanceMode = 'open' | 'verified_only' | 'curated';
type DataAccessPolicy = 'members_only' | 'contributors' | 'public';
type TranscriptDeletionPolicy = 'user_choice' | 'mandatory';
// A concrete member-row role. The pendingMembers list carries 'pending' (the
// backend ListGroupPendingMembers query filters gm.role = 'pending').
type CollectiveRole = 'owner' | 'member' | 'contributor' | 'pending';
// The authenticated viewer's role, or '' when not a member / anonymous (the
// backend GetGroup handler defaults your_role to "" for non-members).
type ViewerRole = CollectiveRole | '';

interface CollectiveMemberPayload {
  id: string;
  githubUsername: string;
  displayName: string | null;
  avatarUrl: string | null;
  role: CollectiveRole;
  joinedAt: string;
}

interface CollectiveTranscriptPayload {
  id: string;
  title: string | null;
  visibility: TranscriptVisibility;
  modelProvider: string;
  modelName: string | null;
  sessionStart: string | null;
  turnCount: number | null;
  tokenCount: number | null;
  ownerUsername: string;
  ownerAvatarUrl: string | null;
}

interface CollectiveStatsPayload {
  totalTranscripts: number;
  contributorCount: number;
  totalTurns: number;
  totalDurationMs: number;
  totalTokens: number;
}

interface CollectiveModelBreakdownPayload {
  modelProvider: string;
  transcriptCount: number;
}

interface CollectiveContributorPayload {
  id: string;
  githubUsername: string;
  avatarUrl: string | null;
  transcriptCount: number;
}

interface CollectivePayload {
  id: string;
  name: string;
  description: string | null;
  linkedGithubOrg: string | null;
  displayMembers: boolean;
  transcriptDeletionPolicy: TranscriptDeletionPolicy;
  createdBy: string;
  createdAt: string;
  updatedAt: string;
  acceptanceMode: AcceptanceMode;
  dataAccess: DataAccessPolicy;
  role: ViewerRole;
  memberSince: string | null;
}

interface ManagePayload {
  collective: CollectivePayload;
  members: CollectiveMemberPayload[];
  pendingMembers: CollectiveMemberPayload[];
  transcripts: CollectiveTranscriptPayload[];
  stats: CollectiveStatsPayload;
  models: CollectiveModelBreakdownPayload[];
  contributors: CollectiveContributorPayload[];
  yourRole: ViewerRole;
}

// ── Adapter signature ─────────────────────────────────────────────────────────

/**
 * The decomposed useGroup(id) result the adapter consumes. A single named
 * input object (rather than positional params) because `models` and
 * `contributors` share a shape, so positional order would be silently
 * swappable; named fields make the call site self-documenting and order-proof.
 */
export interface ManageAdapterInput {
  /** useGroup().data.group */
  group: Group;
  /** useGroup().data.members */
  members: GroupMember[];
  /** useGroup().data.transcripts */
  transcripts: GroupTranscript[];
  /** useGroup().data.stats */
  stats: GroupTranscriptStats;
  /** useGroup().data.models */
  models: GroupModelBreakdown[];
  /** useGroup().data.contributors */
  contributors: GroupContributor[];
  /** useGroup().data.pending_members ?? [] — normalize an omitted value to [] */
  pendingMembers: GroupMember[];
  /** useGroup().data.your_role — "" when the viewer is not a member */
  yourRole: ViewerRole;
}

/**
 * Map the useGroup hook result to the cooked ManagePayload.
 * TRANSFORM (small): snake_case → camelCase; can_read omitted; pending_members
 * normalized to [] when absent.
 *
 * The caller (the Manage page/shell) runs useGroup(id) and passes its .data
 * fields as one named object. The adapter is called every time the hook result
 * changes.
 *
 * @param data the decomposed useGroup result (see ManageAdapterInput)
 * @returns Cooked prop payload for the lifted <Manage> surface
 */
export function adaptManage(data: ManageAdapterInput): ManagePayload {
  return {
    collective: {
      id: data.group.id,
      name: data.group.name,
      description: data.group.description,
      linkedGithubOrg: data.group.linked_github_org,
      displayMembers: data.group.display_members,
      transcriptDeletionPolicy: data.group.transcript_deletion_policy,
      createdBy: data.group.created_by,
      createdAt: data.group.created_at,
      updatedAt: data.group.updated_at,
      acceptanceMode: data.group.acceptance_mode,
      dataAccess: data.group.data_access,
      role: data.group.role as ViewerRole,
      memberSince: data.group.member_since,
    },
    members: data.members.map((member) => ({
      id: member.id,
      githubUsername: member.github_username,
      displayName: member.display_name,
      avatarUrl: member.avatar_url,
      role: member.role as CollectiveRole,
      joinedAt: member.joined_at,
    })),
    pendingMembers: data.pendingMembers.map((member) => ({
      id: member.id,
      githubUsername: member.github_username,
      displayName: member.display_name,
      avatarUrl: member.avatar_url,
      role: member.role as CollectiveRole,
      joinedAt: member.joined_at,
    })),
    transcripts: data.transcripts.map((transcript) => ({
      id: transcript.id,
      title: transcript.title,
      visibility: transcript.visibility,
      modelProvider: transcript.model_provider,
      modelName: transcript.model_name,
      sessionStart: transcript.session_start,
      turnCount: transcript.turn_count,
      tokenCount: transcript.token_count,
      ownerUsername: transcript.owner_username,
      ownerAvatarUrl: transcript.owner_avatar_url,
    })),
    stats: {
      totalTranscripts: data.stats.total_transcripts,
      contributorCount: data.stats.contributor_count,
      totalTurns: data.stats.total_turns,
      totalDurationMs: data.stats.total_duration_ms,
      totalTokens: data.stats.total_tokens,
    },
    models: data.models.map((model) => ({
      modelProvider: model.model_provider,
      transcriptCount: model.transcript_count,
    })),
    contributors: data.contributors.map((contributor) => ({
      id: contributor.id,
      githubUsername: contributor.github_username,
      avatarUrl: contributor.avatar_url,
      transcriptCount: contributor.transcript_count,
    })),
    yourRole: data.yourRole,
  };
}
