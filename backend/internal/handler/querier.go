package handler

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// Querier is the database access interface used by all handlers.
// It is satisfied by *sqlc.Queries and can be mocked in tests.
// Only methods that are actually called from handler files are listed here.
type Querier interface {
	// CLI auth session methods
	InsertCLISession(ctx context.Context, arg sqlc.InsertCLISessionParams) (pgtype.UUID, error)
	GetCLISessionByState(ctx context.Context, oauthState string) (sqlc.CliAuthSession, error)
	UpdateCLISessionWithCode(ctx context.Context, arg sqlc.UpdateCLISessionWithCodeParams) error
	ExchangeCLISession(ctx context.Context, arg sqlc.ExchangeCLISessionParams) (sqlc.CliAuthSession, error)

	// User methods
	UpsertUser(ctx context.Context, arg sqlc.UpsertUserParams) (sqlc.User, error)
	UpsertUserByProvider(ctx context.Context, arg sqlc.UpsertUserByProviderParams) (sqlc.User, error)
	GetUserByID(ctx context.Context, id pgtype.UUID) (sqlc.User, error)
	GetUserByUsername(ctx context.Context, githubUsername string) (sqlc.User, error)
	SetUsername(ctx context.Context, arg sqlc.SetUsernameParams) (sqlc.User, error)
	DeleteUser(ctx context.Context, id pgtype.UUID) error
	UpdateUserDiscoverable(ctx context.Context, arg sqlc.UpdateUserDiscoverableParams) (sqlc.User, error)

	// API key methods
	CreateAPIKey(ctx context.Context, arg sqlc.CreateAPIKeyParams) (sqlc.ApiKey, error)
	GetAPIKeyByHash(ctx context.Context, keyHash string) (sqlc.GetAPIKeyByHashRow, error)
	ListUserAPIKeys(ctx context.Context, userID pgtype.UUID) ([]sqlc.ListUserAPIKeysRow, error)
	RevokeAPIKey(ctx context.Context, arg sqlc.RevokeAPIKeyParams) error
	TouchAPIKeyLastUsed(ctx context.Context, id pgtype.UUID) error

	// Transcript methods
	CreateTranscript(ctx context.Context, arg sqlc.CreateTranscriptParams) (sqlc.Transcript, error)
	GetTranscriptByID(ctx context.Context, id pgtype.UUID) (sqlc.Transcript, error)
	GetTranscriptGovernanceForUpdate(ctx context.Context, id pgtype.UUID) (sqlc.GetTranscriptGovernanceForUpdateRow, error)
	GetTranscriptIDByOwnerAndLocalID(ctx context.Context, arg sqlc.GetTranscriptIDByOwnerAndLocalIDParams) (pgtype.UUID, error)
	UpdateTranscriptByOwnerAndLocalID(ctx context.Context, arg sqlc.UpdateTranscriptByOwnerAndLocalIDParams) (sqlc.Transcript, error)
	UpdateTranscriptMetadata(ctx context.Context, arg sqlc.UpdateTranscriptMetadataParams) (sqlc.Transcript, error)
	DeleteTranscript(ctx context.Context, id pgtype.UUID) error
	RenameUserProject(ctx context.Context, arg sqlc.RenameUserProjectParams) (int64, error)
	ListTranscriptAssociationsByOwnerAndIDs(ctx context.Context, arg sqlc.ListTranscriptAssociationsByOwnerAndIDsParams) ([]sqlc.TranscriptAssociation, error)
	ListTranscriptAssociationsByOwnerTranscriptAndObservedCommitHashes(ctx context.Context, arg sqlc.ListTranscriptAssociationsByOwnerTranscriptAndObservedCommitHashesParams) ([]sqlc.TranscriptAssociation, error)
	InsertTranscriptAssociations(ctx context.Context, arg sqlc.InsertTranscriptAssociationsParams) error
	ListTranscriptAssociationIDsByOwner(ctx context.Context, arg sqlc.ListTranscriptAssociationIDsByOwnerParams) ([]string, error)
	ListTranscriptAssociationsByTranscript(ctx context.Context, transcriptID pgtype.UUID) ([]sqlc.TranscriptAssociation, error)
	SetAcceptedRequestOperationFingerprint(ctx context.Context, arg sqlc.SetAcceptedRequestOperationFingerprintParams) error

	// Pull data: served-blob content hash, pullable listings, and approved-share
	// membership for the canPullTranscript policy.
	SetTranscriptContentHash(ctx context.Context, arg sqlc.SetTranscriptContentHashParams) error
	ListPullableTranscripts(ctx context.Context, arg sqlc.ListPullableTranscriptsParams) ([]sqlc.ListPullableTranscriptsRow, error)
	ListPullableTranscriptsByIDs(ctx context.Context, arg sqlc.ListPullableTranscriptsByIDsParams) ([]sqlc.ListPullableTranscriptsByIDsRow, error)
	CountPullableTranscripts(ctx context.Context, userID pgtype.UUID) (int64, error)
	CountTranscriptAnnotations(ctx context.Context, transcriptID pgtype.UUID) (int64, error)
	CompareAndSwapContentIdentity(ctx context.Context, arg sqlc.CompareAndSwapContentIdentityParams) (int64, error)
	CompareAndSwapTranscriptBlob(ctx context.Context, arg sqlc.CompareAndSwapTranscriptBlobParams) (sqlc.Transcript, error)
	CompareAndSwapWrappedDataKey(ctx context.Context, arg sqlc.CompareAndSwapWrappedDataKeyParams) (int64, error)
	DeleteTranscriptReturningDescriptor(ctx context.Context, id pgtype.UUID) (sqlc.DeleteTranscriptReturningDescriptorRow, error)
	ListTranscriptDescriptorsForRewrap(ctx context.Context, arg sqlc.ListTranscriptDescriptorsForRewrapParams) ([]sqlc.ListTranscriptDescriptorsForRewrapRow, error)
	ListApprovedTranscriptShareGroups(ctx context.Context, transcriptID pgtype.UUID) ([]pgtype.UUID, error)

	// Transcript commit methods
	InsertTranscriptCommits(ctx context.Context, arg sqlc.InsertTranscriptCommitsParams) error
	DeleteTranscriptCommits(ctx context.Context, transcriptID pgtype.UUID) error
	ListTranscriptCommits(ctx context.Context, transcriptID pgtype.UUID) ([]sqlc.TranscriptCommit, error)

	// Tag methods
	GetOrCreateTag(ctx context.Context, name string) (sqlc.Tag, error)
	GetTranscriptTags(ctx context.Context, transcriptID pgtype.UUID) ([]sqlc.Tag, error)
	LinkTranscriptTag(ctx context.Context, arg sqlc.LinkTranscriptTagParams) error
	UnlinkTranscriptTags(ctx context.Context, transcriptID pgtype.UUID) error
	ListTags(ctx context.Context) ([]sqlc.ListTagsRow, error)
	ListPopularTags(ctx context.Context, limit int32) ([]sqlc.ListPopularTagsRow, error)

	// Group methods
	CreateGroup(ctx context.Context, arg sqlc.CreateGroupParams) (sqlc.Group, error)
	GetGroupByID(ctx context.Context, id pgtype.UUID) (sqlc.Group, error)
	UpdateGroup(ctx context.Context, arg sqlc.UpdateGroupParams) (sqlc.Group, error)
	DeleteGroup(ctx context.Context, id pgtype.UUID) error
	ListUserGroups(ctx context.Context, userID pgtype.UUID) ([]sqlc.ListUserGroupsRow, error)
	ListAllGroups(ctx context.Context) ([]sqlc.ListAllGroupsRow, error)
	SearchCollectives(ctx context.Context, arg sqlc.SearchCollectivesParams) ([]sqlc.SearchCollectivesRow, error)
	ListCollectivesByGitHubOrg(ctx context.Context, arg sqlc.ListCollectivesByGitHubOrgParams) ([]sqlc.ListCollectivesByGitHubOrgRow, error)
	AddGroupMember(ctx context.Context, arg sqlc.AddGroupMemberParams) error
	RemoveGroupMember(ctx context.Context, arg sqlc.RemoveGroupMemberParams) error
	GetGroupMember(ctx context.Context, arg sqlc.GetGroupMemberParams) (sqlc.GroupMember, error)
	ListGroupMembers(ctx context.Context, arg sqlc.ListGroupMembersParams) ([]sqlc.ListGroupMembersRow, error)
	ListGroupPendingMembers(ctx context.Context, groupID pgtype.UUID) ([]sqlc.ListGroupPendingMembersRow, error)
	UpdateMemberRole(ctx context.Context, arg sqlc.UpdateMemberRoleParams) error

	// Share methods
	GetLatestShareAttempt(ctx context.Context, arg sqlc.GetLatestShareAttemptParams) (sqlc.TranscriptShareAttempt, error)
	ListShareAttempts(ctx context.Context, arg sqlc.ListShareAttemptsParams) ([]sqlc.TranscriptShareAttempt, error)
	ShareTranscriptWithStatus(ctx context.Context, arg sqlc.ShareTranscriptWithStatusParams) error
	UnshareTranscript(ctx context.Context, arg sqlc.UnshareTranscriptParams) error
	ListTranscriptShares(ctx context.Context, transcriptID pgtype.UUID) ([]sqlc.ListTranscriptSharesRow, error)
	ListGroupTranscripts(ctx context.Context, arg sqlc.ListGroupTranscriptsParams) ([]sqlc.ListGroupTranscriptsRow, error)
	RemoveGroupTranscript(ctx context.Context, arg sqlc.RemoveGroupTranscriptParams) error
	ListSharesByTranscriptIDs(ctx context.Context, transcriptIds []pgtype.UUID) ([]sqlc.ListSharesByTranscriptIDsRow, error)
	ListPendingGroupShares(ctx context.Context, groupID pgtype.UUID) ([]sqlc.ListPendingGroupSharesRow, error)
	UpdateShareStatus(ctx context.Context, arg sqlc.UpdateShareStatusParams) error
	ListUserSharesInGroup(ctx context.Context, arg sqlc.ListUserSharesInGroupParams) ([]sqlc.ListUserSharesInGroupRow, error)
	RetractUserSharesInGroup(ctx context.Context, arg sqlc.RetractUserSharesInGroupParams) error
	ListGroupOwnersForTranscript(ctx context.Context, transcriptID pgtype.UUID) ([]pgtype.UUID, error)
	GetGroupTranscriptStats(ctx context.Context, groupID pgtype.UUID) (sqlc.GetGroupTranscriptStatsRow, error)
	ListGroupModelBreakdown(ctx context.Context, groupID pgtype.UUID) ([]sqlc.ListGroupModelBreakdownRow, error)
	ListGroupContributors(ctx context.Context, arg sqlc.ListGroupContributorsParams) ([]sqlc.ListGroupContributorsRow, error)

	// GitHub org methods
	UpsertUserGitHubOrg(ctx context.Context, arg sqlc.UpsertUserGitHubOrgParams) error
	DeleteStaleUserOrgs(ctx context.Context, arg sqlc.DeleteStaleUserOrgsParams) error
	ListUserVisibleOrgs(ctx context.Context, userID pgtype.UUID) ([]sqlc.ListUserVisibleOrgsRow, error)
	HasUserVisibleOrg(ctx context.Context, arg sqlc.HasUserVisibleOrgParams) (bool, error)
	ListUserAllOrgs(ctx context.Context, userID pgtype.UUID) ([]sqlc.ListUserAllOrgsRow, error)
	SetOrgVisibility(ctx context.Context, arg sqlc.SetOrgVisibilityParams) error
	GetUserVisibleOrgsByUsername(ctx context.Context, githubUsername string) ([]sqlc.GetUserVisibleOrgsByUsernameRow, error)
	ListVisibleOrgsByUserIDs(ctx context.Context, userIds []pgtype.UUID) ([]sqlc.ListVisibleOrgsByUserIDsRow, error)
	SearchOrgs(ctx context.Context, arg sqlc.SearchOrgsParams) ([]sqlc.SearchOrgsRow, error)
	GetOrgStats(ctx context.Context, login string) (sqlc.GetOrgStatsRow, error)
	ListOrgMembers(ctx context.Context, login string) ([]sqlc.ListOrgMembersRow, error)

	// Attestation methods
	CreateAttestation(ctx context.Context, arg sqlc.CreateAttestationParams) (sqlc.Attestation, error)
	DeleteAttestation(ctx context.Context, arg sqlc.DeleteAttestationParams) error
	ListTranscriptAttestations(ctx context.Context, transcriptID pgtype.UUID) ([]sqlc.ListTranscriptAttestationsRow, error)
	ListAttestationsByTranscriptIDs(ctx context.Context, transcriptIds []pgtype.UUID) ([]sqlc.ListAttestationsByTranscriptIDsRow, error)

	// Annotation methods
	BulkUpsertAnnotations(ctx context.Context, arg sqlc.BulkUpsertAnnotationsParams) ([]sqlc.BulkUpsertAnnotationsRow, error)
	CreateManualAnnotation(ctx context.Context, arg sqlc.CreateManualAnnotationParams) (sqlc.Annotation, error)
	ListAnnotationsByTranscriptID(ctx context.Context, transcriptID pgtype.UUID) ([]sqlc.Annotation, error)
	ListAnnotationContentHashesByOwner(ctx context.Context, ownerID pgtype.UUID) ([]string, error)
	ListOwnerAnnotationHashesForTranscriptIDs(ctx context.Context, arg sqlc.ListOwnerAnnotationHashesForTranscriptIDsParams) ([]sqlc.ListOwnerAnnotationHashesForTranscriptIDsRow, error)
	DeleteAnnotationByContentHash(ctx context.Context, arg sqlc.DeleteAnnotationByContentHashParams) error

	// Collective repository / GitHub App methods
	UpsertGitHubAppInstallation(ctx context.Context, arg sqlc.UpsertGitHubAppInstallationParams) error
	GetGitHubAppInstallation(ctx context.Context, installationID int64) (sqlc.GithubAppInstallation, error)
	LinkCollectiveRepository(ctx context.Context, arg sqlc.LinkCollectiveRepositoryParams) (sqlc.CollectiveRepository, error)
	UnlinkCollectiveRepository(ctx context.Context, arg sqlc.UnlinkCollectiveRepositoryParams) (int64, error)
	ListCollectiveRepositories(ctx context.Context, groupID pgtype.UUID) ([]sqlc.CollectiveRepository, error)
	GetCollectiveRepository(ctx context.Context, arg sqlc.GetCollectiveRepositoryParams) (sqlc.CollectiveRepository, error)
	UpdateCollectiveRepositorySync(ctx context.Context, arg sqlc.UpdateCollectiveRepositorySyncParams) error
	UpsertRepositoryCommit(ctx context.Context, arg sqlc.UpsertRepositoryCommitParams) error
	ListRepositoryCommits(ctx context.Context, arg sqlc.ListRepositoryCommitsParams) ([]sqlc.RepositoryCommit, error)
	CountRepositoryCommits(ctx context.Context, arg sqlc.CountRepositoryCommitsParams) (int64, error)
}

// Compile-time assertion: *sqlc.Queries must satisfy Querier.
var _ Querier = (*sqlc.Queries)(nil)
