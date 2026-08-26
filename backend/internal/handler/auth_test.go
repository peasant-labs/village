package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/redact"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/projectname"
	"github.com/peasant-labs/village/backend/internal/scanner"
	"github.com/peasant-labs/village/backend/internal/storage"
)

// ----------------------------------------------------------------------------
// mockQuerier — a zero-value stub that panics on any call not overridden.
// Tests embed it and override only the methods they exercise.
// ----------------------------------------------------------------------------

type mockQuerier struct {
	// CLI session stubs
	insertCLISession         func(ctx context.Context, arg sqlc.InsertCLISessionParams) (pgtype.UUID, error)
	getCLISessionByState     func(ctx context.Context, oauthState string) (sqlc.CliAuthSession, error)
	updateCLISessionWithCode func(ctx context.Context, arg sqlc.UpdateCLISessionWithCodeParams) error
	exchangeCLISession       func(ctx context.Context, arg sqlc.ExchangeCLISessionParams) (sqlc.CliAuthSession, error)

	// User stubs
	upsertUser           func(ctx context.Context, arg sqlc.UpsertUserParams) (sqlc.User, error)
	upsertUserByProvider func(ctx context.Context, arg sqlc.UpsertUserByProviderParams) (sqlc.User, error)
	getUserByID          func(ctx context.Context, id pgtype.UUID) (sqlc.User, error)
	getUserByUsername    func(ctx context.Context, githubUsername string) (sqlc.User, error)
	setUsername          func(ctx context.Context, arg sqlc.SetUsernameParams) (sqlc.User, error)
	deleteUser           func(ctx context.Context, id pgtype.UUID) error

	// API key stubs
	createAPIKey        func(ctx context.Context, arg sqlc.CreateAPIKeyParams) (sqlc.ApiKey, error)
	getAPIKeyByHash     func(ctx context.Context, keyHash string) (sqlc.GetAPIKeyByHashRow, error)
	listUserAPIKeys     func(ctx context.Context, userID pgtype.UUID) ([]sqlc.ListUserAPIKeysRow, error)
	revokeAPIKey        func(ctx context.Context, arg sqlc.RevokeAPIKeyParams) error
	touchAPIKeyLastUsed func(ctx context.Context, id pgtype.UUID) error

	// Transcript stubs
	getTranscriptIDByOwnerAndLocalID                                   func(ctx context.Context, arg sqlc.GetTranscriptIDByOwnerAndLocalIDParams) (pgtype.UUID, error)
	updateTranscriptMetadata                                           func(ctx context.Context, arg sqlc.UpdateTranscriptMetadataParams) (sqlc.Transcript, error)
	createTranscript                                                   func(ctx context.Context, arg sqlc.CreateTranscriptParams) (sqlc.Transcript, error)
	updateTranscriptByOwnerAndLocalID                                  func(ctx context.Context, arg sqlc.UpdateTranscriptByOwnerAndLocalIDParams) (sqlc.Transcript, error)
	getTranscriptByID                                                  func(ctx context.Context, id pgtype.UUID) (sqlc.Transcript, error)
	getTranscriptGovernanceForUpdate                                   func(ctx context.Context, id pgtype.UUID) (sqlc.GetTranscriptGovernanceForUpdateRow, error)
	listTranscriptAssociationsByOwnerAndIDs                            func(ctx context.Context, arg sqlc.ListTranscriptAssociationsByOwnerAndIDsParams) ([]sqlc.TranscriptAssociation, error)
	listTranscriptAssociationsByOwnerTranscriptAndObservedCommitHashes func(ctx context.Context, arg sqlc.ListTranscriptAssociationsByOwnerTranscriptAndObservedCommitHashesParams) ([]sqlc.TranscriptAssociation, error)
	insertTranscriptAssociations                                       func(ctx context.Context, arg sqlc.InsertTranscriptAssociationsParams) error
	listTranscriptAssociationIDsByOwner                                func(ctx context.Context, arg sqlc.ListTranscriptAssociationIDsByOwnerParams) ([]string, error)

	// Pull data stubs
	setTranscriptContentHash               func(ctx context.Context, arg sqlc.SetTranscriptContentHashParams) error
	setAcceptedRequestOperationFingerprint func(ctx context.Context, arg sqlc.SetAcceptedRequestOperationFingerprintParams) error
	listTranscriptAssociationsByTranscript func(ctx context.Context, transcriptID pgtype.UUID) ([]sqlc.TranscriptAssociation, error)
	getOrCreateTag                         func(ctx context.Context, name string) (sqlc.Tag, error)
	getTranscriptTags                      func(ctx context.Context, transcriptID pgtype.UUID) ([]sqlc.Tag, error)
	linkTranscriptTag                      func(ctx context.Context, arg sqlc.LinkTranscriptTagParams) error
	unlinkTranscriptTags                   func(ctx context.Context, transcriptID pgtype.UUID) error
	getTranscriptContentHash               func(ctx context.Context, id pgtype.UUID) (pgtype.Text, error)
	listPullableTranscripts                func(ctx context.Context, arg sqlc.ListPullableTranscriptsParams) ([]sqlc.ListPullableTranscriptsRow, error)
	listPullableTranscriptsByIDs           func(ctx context.Context, arg sqlc.ListPullableTranscriptsByIDsParams) ([]sqlc.ListPullableTranscriptsByIDsRow, error)
	countPullableTranscripts               func(ctx context.Context, userID pgtype.UUID) (int64, error)
	countTranscriptAnnotations             func(ctx context.Context, transcriptID pgtype.UUID) (int64, error)
	compareAndSwapTranscriptBlob           func(ctx context.Context, arg sqlc.CompareAndSwapTranscriptBlobParams) (sqlc.Transcript, error)
	listApprovedTranscriptShareGroups      func(ctx context.Context, transcriptID pgtype.UUID) ([]pgtype.UUID, error)

	// Project identity stubs. listOwnerProjectIdentities also counts its calls,
	// so a test can prove a page of transcripts resolves its project names in ONE
	// statement rather than one per row.
	listOwnerProjectIdentities      func(ctx context.Context, arg sqlc.ListOwnerProjectIdentitiesParams) ([]sqlc.ListOwnerProjectIdentitiesRow, error)
	listOwnerProjectIdentitiesCalls int
	countOwnerTranscriptsInProject  func(ctx context.Context, arg sqlc.CountOwnerTranscriptsInProjectParams) (int64, error)
	listProjectTranscriptsForViewer func(ctx context.Context, arg sqlc.ListProjectTranscriptsForViewerParams) ([]sqlc.Transcript, error)
	upsertOwnerOverride             func(ctx context.Context, arg sqlc.UpsertOwnerOverrideParams) (sqlc.OwnerOverride, error)
	deleteOwnerOverride             func(ctx context.Context, arg sqlc.DeleteOwnerOverrideParams) (int64, error)
	getOwnerOverride                func(ctx context.Context, arg sqlc.GetOwnerOverrideParams) (sqlc.OwnerOverride, error)

	// Transcript commit stubs
	insertTranscriptCommits func(ctx context.Context, arg sqlc.InsertTranscriptCommitsParams) error
	deleteTranscriptCommits func(ctx context.Context, transcriptID pgtype.UUID) error
	listTranscriptCommits   func(ctx context.Context, transcriptID pgtype.UUID) ([]sqlc.TranscriptCommit, error)

	// Annotation stubs
	listAnnotationsByTranscriptID             func(ctx context.Context, transcriptID pgtype.UUID) ([]sqlc.Annotation, error)
	createManualAnnotation                    func(ctx context.Context, arg sqlc.CreateManualAnnotationParams) (sqlc.Annotation, error)
	bulkUpsertAnnotations                     func(ctx context.Context, arg sqlc.BulkUpsertAnnotationsParams) ([]sqlc.BulkUpsertAnnotationsRow, error)
	listAnnotationContentHashesByOwner        func(ctx context.Context, ownerID pgtype.UUID) ([]string, error)
	listOwnerAnnotationHashesForTranscriptIDs func(ctx context.Context, arg sqlc.ListOwnerAnnotationHashesForTranscriptIDsParams) ([]sqlc.ListOwnerAnnotationHashesForTranscriptIDsRow, error)
	deleteAnnotationByContentHash             func(ctx context.Context, arg sqlc.DeleteAnnotationByContentHashParams) error

	// Group membership stub (used for owner/admin gating)
	getGroupMember func(ctx context.Context, arg sqlc.GetGroupMemberParams) (sqlc.GroupMember, error)

	// Collectives stubs. They are overridable func fields (rather than the
	// constant-nil shorthand used by the older group stubs) so a test can count
	// how many times a surface reaches the database and prove one aggregate
	// answers a whole page instead of one query per collective.
	listOwnerCollectiveContributions   func(ctx context.Context, ownerID pgtype.UUID) ([]sqlc.ListOwnerCollectiveContributionsRow, error)
	listOwnerCollectiveSubmissions     func(ctx context.Context, arg sqlc.ListOwnerCollectiveSubmissionsParams) ([]sqlc.ListOwnerCollectiveSubmissionsRow, error)
	listProjectCollectiveRollup        func(ctx context.Context, arg sqlc.ListProjectCollectiveRollupParams) ([]sqlc.ListProjectCollectiveRollupRow, error)
	listTranscriptCollectivesForViewer func(ctx context.Context, arg sqlc.ListTranscriptCollectivesForViewerParams) ([]sqlc.ListTranscriptCollectivesForViewerRow, error)

	// Collective repository / GitHub App stubs
	upsertGitHubAppInstallation    func(ctx context.Context, arg sqlc.UpsertGitHubAppInstallationParams) error
	getGitHubAppInstallation       func(ctx context.Context, installationID int64) (sqlc.GithubAppInstallation, error)
	linkCollectiveRepository       func(ctx context.Context, arg sqlc.LinkCollectiveRepositoryParams) (sqlc.CollectiveRepository, error)
	unlinkCollectiveRepository     func(ctx context.Context, arg sqlc.UnlinkCollectiveRepositoryParams) (int64, error)
	listCollectiveRepositories     func(ctx context.Context, groupID pgtype.UUID) ([]sqlc.CollectiveRepository, error)
	getCollectiveRepository        func(ctx context.Context, arg sqlc.GetCollectiveRepositoryParams) (sqlc.CollectiveRepository, error)
	updateCollectiveRepositorySync func(ctx context.Context, arg sqlc.UpdateCollectiveRepositorySyncParams) error
	upsertRepositoryCommit         func(ctx context.Context, arg sqlc.UpsertRepositoryCommitParams) error
	listRepositoryCommits          func(ctx context.Context, arg sqlc.ListRepositoryCommitsParams) ([]sqlc.RepositoryCommit, error)
	countRepositoryCommits         func(ctx context.Context, arg sqlc.CountRepositoryCommitsParams) (int64, error)
}

// ---- CLI session ---------------------------------------------------------

func (m *mockQuerier) InsertCLISession(ctx context.Context, arg sqlc.InsertCLISessionParams) (pgtype.UUID, error) {
	if m.insertCLISession != nil {
		return m.insertCLISession(ctx, arg)
	}
	panic("InsertCLISession: not stubbed")
}

func (m *mockQuerier) GetCLISessionByState(ctx context.Context, oauthState string) (sqlc.CliAuthSession, error) {
	if m.getCLISessionByState != nil {
		return m.getCLISessionByState(ctx, oauthState)
	}
	panic("GetCLISessionByState: not stubbed")
}

func (m *mockQuerier) UpdateCLISessionWithCode(ctx context.Context, arg sqlc.UpdateCLISessionWithCodeParams) error {
	if m.updateCLISessionWithCode != nil {
		return m.updateCLISessionWithCode(ctx, arg)
	}
	panic("UpdateCLISessionWithCode: not stubbed")
}

func (m *mockQuerier) ExchangeCLISession(ctx context.Context, arg sqlc.ExchangeCLISessionParams) (sqlc.CliAuthSession, error) {
	if m.exchangeCLISession != nil {
		return m.exchangeCLISession(ctx, arg)
	}
	panic("ExchangeCLISession: not stubbed")
}

// ---- Users ---------------------------------------------------------------

func (m *mockQuerier) UpsertUser(ctx context.Context, arg sqlc.UpsertUserParams) (sqlc.User, error) {
	if m.upsertUser != nil {
		return m.upsertUser(ctx, arg)
	}
	panic("UpsertUser: not stubbed")
}

func (m *mockQuerier) UpsertUserByProvider(ctx context.Context, arg sqlc.UpsertUserByProviderParams) (sqlc.User, error) {
	if m.upsertUserByProvider != nil {
		return m.upsertUserByProvider(ctx, arg)
	}
	panic("UpsertUserByProvider: not stubbed")
}

func (m *mockQuerier) GetUserByID(ctx context.Context, id pgtype.UUID) (sqlc.User, error) {
	if m.getUserByID != nil {
		return m.getUserByID(ctx, id)
	}
	panic("GetUserByID: not stubbed")
}

func (m *mockQuerier) GetUserByUsername(ctx context.Context, githubUsername string) (sqlc.User, error) {
	if m.getUserByUsername != nil {
		return m.getUserByUsername(ctx, githubUsername)
	}
	panic("GetUserByUsername: not stubbed")
}

func (m *mockQuerier) SetUsername(ctx context.Context, arg sqlc.SetUsernameParams) (sqlc.User, error) {
	if m.setUsername != nil {
		return m.setUsername(ctx, arg)
	}
	panic("SetUsername: not stubbed")
}

func (m *mockQuerier) DeleteUser(ctx context.Context, id pgtype.UUID) error {
	if m.deleteUser != nil {
		return m.deleteUser(ctx, id)
	}
	panic("DeleteUser: not stubbed")
}

func (m *mockQuerier) UpdateUserDiscoverable(ctx context.Context, arg sqlc.UpdateUserDiscoverableParams) (sqlc.User, error) {
	return sqlc.User{}, nil
}

// ---- API keys ------------------------------------------------------------

func (m *mockQuerier) CreateAPIKey(ctx context.Context, arg sqlc.CreateAPIKeyParams) (sqlc.ApiKey, error) {
	if m.createAPIKey != nil {
		return m.createAPIKey(ctx, arg)
	}
	panic("CreateAPIKey: not stubbed")
}

func (m *mockQuerier) GetAPIKeyByHash(ctx context.Context, keyHash string) (sqlc.GetAPIKeyByHashRow, error) {
	if m.getAPIKeyByHash != nil {
		return m.getAPIKeyByHash(ctx, keyHash)
	}
	panic("GetAPIKeyByHash: not stubbed")
}

func (m *mockQuerier) ListUserAPIKeys(ctx context.Context, userID pgtype.UUID) ([]sqlc.ListUserAPIKeysRow, error) {
	if m.listUserAPIKeys != nil {
		return m.listUserAPIKeys(ctx, userID)
	}
	panic("ListUserAPIKeys: not stubbed")
}

func (m *mockQuerier) RevokeAPIKey(ctx context.Context, arg sqlc.RevokeAPIKeyParams) error {
	if m.revokeAPIKey != nil {
		return m.revokeAPIKey(ctx, arg)
	}
	panic("RevokeAPIKey: not stubbed")
}

func (m *mockQuerier) TouchAPIKeyLastUsed(ctx context.Context, id pgtype.UUID) error {
	if m.touchAPIKeyLastUsed != nil {
		return m.touchAPIKeyLastUsed(ctx, id)
	}
	panic("TouchAPIKeyLastUsed: not stubbed")
}

// ---- Stubs required by Querier interface but unused in auth tests --------
// These delegate to a shared panicStub so that an unexpected call gives a
// clear error message rather than a confusing nil-pointer panic.
// For transcript tests, we provide default no-op implementations to avoid
// having to stub every single method.

func (m *mockQuerier) CreateTranscript(ctx context.Context, arg sqlc.CreateTranscriptParams) (sqlc.Transcript, error) {
	if m.createTranscript != nil {
		return m.createTranscript(ctx, arg)
	}
	panic("CreateTranscript: not stubbed")
}

func (m *mockQuerier) GetTranscriptByID(ctx context.Context, id pgtype.UUID) (sqlc.Transcript, error) {
	if m.getTranscriptByID != nil {
		return m.getTranscriptByID(ctx, id)
	}
	return sqlc.Transcript{}, nil
}
func (m *mockQuerier) GetTranscriptGovernanceForUpdate(ctx context.Context, id pgtype.UUID) (sqlc.GetTranscriptGovernanceForUpdateRow, error) {
	if m.getTranscriptGovernanceForUpdate != nil {
		return m.getTranscriptGovernanceForUpdate(ctx, id)
	}
	// Default: derive the narrow row from the wide GetTranscriptByID stub, so
	// tests that stub only the wide getter keep working.
	if m.getTranscriptByID != nil {
		t, err := m.getTranscriptByID(ctx, id)
		if err != nil {
			return sqlc.GetTranscriptGovernanceForUpdateRow{}, err
		}
		return sqlc.GetTranscriptGovernanceForUpdateRow{
			ID: t.ID, Title: t.Title, Description: t.Description,
			Visibility: t.Visibility, LicenseID: t.LicenseID,
		}, nil
	}
	return sqlc.GetTranscriptGovernanceForUpdateRow{}, nil
}
func (m *mockQuerier) GetTranscriptIDByOwnerAndLocalID(ctx context.Context, arg sqlc.GetTranscriptIDByOwnerAndLocalIDParams) (pgtype.UUID, error) {
	if m.getTranscriptIDByOwnerAndLocalID != nil {
		return m.getTranscriptIDByOwnerAndLocalID(ctx, arg)
	}
	panic("GetTranscriptIDByOwnerAndLocalID: not stubbed")
}
func (m *mockQuerier) UpdateTranscriptByOwnerAndLocalID(ctx context.Context, arg sqlc.UpdateTranscriptByOwnerAndLocalIDParams) (sqlc.Transcript, error) {
	if m.updateTranscriptByOwnerAndLocalID != nil {
		return m.updateTranscriptByOwnerAndLocalID(ctx, arg)
	}
	return sqlc.Transcript{}, nil
}
func (m *mockQuerier) InsertTranscriptCommits(ctx context.Context, arg sqlc.InsertTranscriptCommitsParams) error {
	if m.insertTranscriptCommits != nil {
		return m.insertTranscriptCommits(ctx, arg)
	}
	return nil
}
func (m *mockQuerier) DeleteTranscriptCommits(ctx context.Context, transcriptID pgtype.UUID) error {
	if m.deleteTranscriptCommits != nil {
		return m.deleteTranscriptCommits(ctx, transcriptID)
	}
	return nil
}
func (m *mockQuerier) ListTranscriptCommits(ctx context.Context, transcriptID pgtype.UUID) ([]sqlc.TranscriptCommit, error) {
	if m.listTranscriptCommits != nil {
		return m.listTranscriptCommits(ctx, transcriptID)
	}
	return nil, nil
}
func (m *mockQuerier) UpdateTranscriptMetadata(ctx context.Context, arg sqlc.UpdateTranscriptMetadataParams) (sqlc.Transcript, error) {
	if m.updateTranscriptMetadata != nil {
		return m.updateTranscriptMetadata(ctx, arg)
	}
	return sqlc.Transcript{}, nil
}
func (m *mockQuerier) DeleteTranscript(ctx context.Context, id pgtype.UUID) error {
	return nil
}
func (m *mockQuerier) ListOwnerProjectIdentities(ctx context.Context, arg sqlc.ListOwnerProjectIdentitiesParams) ([]sqlc.ListOwnerProjectIdentitiesRow, error) {
	m.listOwnerProjectIdentitiesCalls++
	if m.listOwnerProjectIdentities != nil {
		return m.listOwnerProjectIdentities(ctx, arg)
	}
	return nil, nil
}
func (m *mockQuerier) CountOwnerTranscriptsInProject(ctx context.Context, arg sqlc.CountOwnerTranscriptsInProjectParams) (int64, error) {
	if m.countOwnerTranscriptsInProject != nil {
		return m.countOwnerTranscriptsInProject(ctx, arg)
	}
	return 0, nil
}
func (m *mockQuerier) ListProjectTranscriptsForViewer(ctx context.Context, arg sqlc.ListProjectTranscriptsForViewerParams) ([]sqlc.Transcript, error) {
	if m.listProjectTranscriptsForViewer != nil {
		return m.listProjectTranscriptsForViewer(ctx, arg)
	}
	return nil, nil
}
func (m *mockQuerier) UpsertOwnerOverride(ctx context.Context, arg sqlc.UpsertOwnerOverrideParams) (sqlc.OwnerOverride, error) {
	if m.upsertOwnerOverride != nil {
		return m.upsertOwnerOverride(ctx, arg)
	}
	return sqlc.OwnerOverride{}, nil
}
func (m *mockQuerier) DeleteOwnerOverride(ctx context.Context, arg sqlc.DeleteOwnerOverrideParams) (int64, error) {
	if m.deleteOwnerOverride != nil {
		return m.deleteOwnerOverride(ctx, arg)
	}
	return 0, nil
}
func (m *mockQuerier) GetOwnerOverride(ctx context.Context, arg sqlc.GetOwnerOverrideParams) (sqlc.OwnerOverride, error) {
	if m.getOwnerOverride != nil {
		return m.getOwnerOverride(ctx, arg)
	}
	return sqlc.OwnerOverride{}, nil
}
func (m *mockQuerier) ListTranscriptAssociationsByOwnerAndIDs(ctx context.Context, arg sqlc.ListTranscriptAssociationsByOwnerAndIDsParams) ([]sqlc.TranscriptAssociation, error) {
	if m.listTranscriptAssociationsByOwnerAndIDs != nil {
		return m.listTranscriptAssociationsByOwnerAndIDs(ctx, arg)
	}
	return nil, nil
}
func (m *mockQuerier) ListTranscriptAssociationsByOwnerTranscriptAndObservedCommitHashes(ctx context.Context, arg sqlc.ListTranscriptAssociationsByOwnerTranscriptAndObservedCommitHashesParams) ([]sqlc.TranscriptAssociation, error) {
	if m.listTranscriptAssociationsByOwnerTranscriptAndObservedCommitHashes != nil {
		return m.listTranscriptAssociationsByOwnerTranscriptAndObservedCommitHashes(ctx, arg)
	}
	return nil, nil
}
func (m *mockQuerier) InsertTranscriptAssociations(ctx context.Context, arg sqlc.InsertTranscriptAssociationsParams) error {
	if m.insertTranscriptAssociations != nil {
		return m.insertTranscriptAssociations(ctx, arg)
	}
	panic("InsertTranscriptAssociations: not stubbed")
}
func (m *mockQuerier) ListTranscriptAssociationIDsByOwner(ctx context.Context, arg sqlc.ListTranscriptAssociationIDsByOwnerParams) ([]string, error) {
	if m.listTranscriptAssociationIDsByOwner != nil {
		return m.listTranscriptAssociationIDsByOwner(ctx, arg)
	}
	return nil, nil
}
func (m *mockQuerier) SetTranscriptContentHash(ctx context.Context, arg sqlc.SetTranscriptContentHashParams) error {
	if m.setTranscriptContentHash != nil {
		return m.setTranscriptContentHash(ctx, arg)
	}
	return nil
}
func (m *mockQuerier) SetAcceptedRequestOperationFingerprint(ctx context.Context, arg sqlc.SetAcceptedRequestOperationFingerprintParams) error {
	if m.setAcceptedRequestOperationFingerprint != nil {
		return m.setAcceptedRequestOperationFingerprint(ctx, arg)
	}
	return nil
}

func (m *mockQuerier) ListTranscriptAssociationsByTranscript(ctx context.Context, transcriptID pgtype.UUID) ([]sqlc.TranscriptAssociation, error) {
	if m.listTranscriptAssociationsByTranscript != nil {
		return m.listTranscriptAssociationsByTranscript(ctx, transcriptID)
	}
	return nil, nil
}
func (m *mockQuerier) GetTranscriptContentHash(ctx context.Context, id pgtype.UUID) (pgtype.Text, error) {
	if m.getTranscriptContentHash != nil {
		return m.getTranscriptContentHash(ctx, id)
	}
	return pgtype.Text{}, nil
}
func (m *mockQuerier) ListPullableTranscripts(ctx context.Context, arg sqlc.ListPullableTranscriptsParams) ([]sqlc.ListPullableTranscriptsRow, error) {
	if m.listPullableTranscripts != nil {
		return m.listPullableTranscripts(ctx, arg)
	}
	return nil, nil
}
func (m *mockQuerier) ListPullableTranscriptsByIDs(ctx context.Context, arg sqlc.ListPullableTranscriptsByIDsParams) ([]sqlc.ListPullableTranscriptsByIDsRow, error) {
	if m.listPullableTranscriptsByIDs != nil {
		return m.listPullableTranscriptsByIDs(ctx, arg)
	}
	return nil, nil
}
func (m *mockQuerier) CountPullableTranscripts(ctx context.Context, userID pgtype.UUID) (int64, error) {
	if m.countPullableTranscripts != nil {
		return m.countPullableTranscripts(ctx, userID)
	}
	return 0, nil
}
func (m *mockQuerier) CountTranscriptAnnotations(ctx context.Context, transcriptID pgtype.UUID) (int64, error) {
	if m.countTranscriptAnnotations != nil {
		return m.countTranscriptAnnotations(ctx, transcriptID)
	}
	return 0, nil
}

func (m *mockQuerier) CompareAndSwapContentIdentity(context.Context, sqlc.CompareAndSwapContentIdentityParams) (int64, error) {
	return 0, nil
}

func (m *mockQuerier) CompareAndSwapTranscriptBlob(ctx context.Context, arg sqlc.CompareAndSwapTranscriptBlobParams) (sqlc.Transcript, error) {
	if m.compareAndSwapTranscriptBlob != nil {
		return m.compareAndSwapTranscriptBlob(ctx, arg)
	}
	return sqlc.Transcript{}, nil
}
func (m *mockQuerier) CompareAndSwapWrappedDataKey(context.Context, sqlc.CompareAndSwapWrappedDataKeyParams) (int64, error) {
	return 0, nil
}
func (m *mockQuerier) DeleteTranscriptReturningDescriptor(context.Context, pgtype.UUID) (sqlc.DeleteTranscriptReturningDescriptorRow, error) {
	return sqlc.DeleteTranscriptReturningDescriptorRow{}, nil
}
func (m *mockQuerier) ListTranscriptDescriptorsForRewrap(context.Context, sqlc.ListTranscriptDescriptorsForRewrapParams) ([]sqlc.ListTranscriptDescriptorsForRewrapRow, error) {
	return nil, nil
}
func (m *mockQuerier) ListApprovedTranscriptShareGroups(ctx context.Context, transcriptID pgtype.UUID) ([]pgtype.UUID, error) {
	if m.listApprovedTranscriptShareGroups != nil {
		return m.listApprovedTranscriptShareGroups(ctx, transcriptID)
	}
	return nil, nil
}
func (m *mockQuerier) GetOrCreateTag(ctx context.Context, name string) (sqlc.Tag, error) {
	if m.getOrCreateTag != nil {
		return m.getOrCreateTag(ctx, name)
	}
	return sqlc.Tag{}, nil
}
func (m *mockQuerier) GetTranscriptTags(ctx context.Context, transcriptID pgtype.UUID) ([]sqlc.Tag, error) {
	if m.getTranscriptTags != nil {
		return m.getTranscriptTags(ctx, transcriptID)
	}
	return nil, nil
}
func (m *mockQuerier) LinkTranscriptTag(ctx context.Context, arg sqlc.LinkTranscriptTagParams) error {
	if m.linkTranscriptTag != nil {
		return m.linkTranscriptTag(ctx, arg)
	}
	return nil
}
func (m *mockQuerier) UnlinkTranscriptTags(ctx context.Context, transcriptID pgtype.UUID) error {
	if m.unlinkTranscriptTags != nil {
		return m.unlinkTranscriptTags(ctx, transcriptID)
	}
	return nil
}
func (m *mockQuerier) ListTags(ctx context.Context) ([]sqlc.ListTagsRow, error) {
	return nil, nil
}
func (m *mockQuerier) ListPopularTags(ctx context.Context, limit int32) ([]sqlc.ListPopularTagsRow, error) {
	return nil, nil
}
func (m *mockQuerier) CreateGroup(ctx context.Context, arg sqlc.CreateGroupParams) (sqlc.Group, error) {
	panic("CreateGroup: not stubbed")
}
func (m *mockQuerier) GetGroupByID(ctx context.Context, id pgtype.UUID) (sqlc.Group, error) {
	panic("GetGroupByID: not stubbed")
}
func (m *mockQuerier) UpdateGroup(ctx context.Context, arg sqlc.UpdateGroupParams) (sqlc.Group, error) {
	panic("UpdateGroup: not stubbed")
}
func (m *mockQuerier) DeleteGroup(ctx context.Context, id pgtype.UUID) error {
	panic("DeleteGroup: not stubbed")
}
func (m *mockQuerier) ListUserGroups(ctx context.Context, userID pgtype.UUID) ([]sqlc.ListUserGroupsRow, error) {
	panic("ListUserGroups: not stubbed")
}
func (m *mockQuerier) AddGroupMember(ctx context.Context, arg sqlc.AddGroupMemberParams) error {
	panic("AddGroupMember: not stubbed")
}
func (m *mockQuerier) RemoveGroupMember(ctx context.Context, arg sqlc.RemoveGroupMemberParams) error {
	panic("RemoveGroupMember: not stubbed")
}
func (m *mockQuerier) GetGroupMember(ctx context.Context, arg sqlc.GetGroupMemberParams) (sqlc.GroupMember, error) {
	if m.getGroupMember != nil {
		return m.getGroupMember(ctx, arg)
	}
	panic("GetGroupMember: not stubbed")
}
func (m *mockQuerier) ListGroupMembers(ctx context.Context, arg sqlc.ListGroupMembersParams) ([]sqlc.ListGroupMembersRow, error) {
	panic("ListGroupMembers: not stubbed")
}
func (m *mockQuerier) ListGroupPendingMembers(ctx context.Context, groupID pgtype.UUID) ([]sqlc.ListGroupPendingMembersRow, error) {
	return nil, nil
}
func (m *mockQuerier) GetLatestShareAttempt(ctx context.Context, arg sqlc.GetLatestShareAttemptParams) (sqlc.TranscriptShareAttempt, error) {
	return sqlc.TranscriptShareAttempt{}, pgx.ErrNoRows
}
func (m *mockQuerier) ListShareAttempts(ctx context.Context, arg sqlc.ListShareAttemptsParams) ([]sqlc.TranscriptShareAttempt, error) {
	panic("ListShareAttempts: not stubbed")
}
func (m *mockQuerier) UnshareTranscript(ctx context.Context, arg sqlc.UnshareTranscriptParams) error {
	panic("UnshareTranscript: not stubbed")
}
func (m *mockQuerier) ListTranscriptShares(ctx context.Context, transcriptID pgtype.UUID) ([]sqlc.ListTranscriptSharesRow, error) {
	panic("ListTranscriptShares: not stubbed")
}
func (m *mockQuerier) ListGroupTranscripts(ctx context.Context, arg sqlc.ListGroupTranscriptsParams) ([]sqlc.ListGroupTranscriptsRow, error) {
	panic("ListGroupTranscripts: not stubbed")
}
func (m *mockQuerier) RemoveGroupTranscript(ctx context.Context, arg sqlc.RemoveGroupTranscriptParams) error {
	panic("RemoveGroupTranscript: not stubbed")
}

// ---- Share extended methods (signalling layer) ---------------------------

func (m *mockQuerier) ShareTranscriptWithStatus(ctx context.Context, arg sqlc.ShareTranscriptWithStatusParams) error {
	return nil
}
func (m *mockQuerier) ListSharesByTranscriptIDs(ctx context.Context, transcriptIds []pgtype.UUID) ([]sqlc.ListSharesByTranscriptIDsRow, error) {
	return nil, nil
}
func (m *mockQuerier) ListPendingGroupShares(ctx context.Context, groupID pgtype.UUID) ([]sqlc.ListPendingGroupSharesRow, error) {
	return nil, nil
}
func (m *mockQuerier) UpdateShareStatus(ctx context.Context, arg sqlc.UpdateShareStatusParams) error {
	return nil
}

// ---- GitHub org methods --------------------------------------------------

func (m *mockQuerier) UpsertUserGitHubOrg(ctx context.Context, arg sqlc.UpsertUserGitHubOrgParams) error {
	return nil
}
func (m *mockQuerier) DeleteStaleUserOrgs(ctx context.Context, arg sqlc.DeleteStaleUserOrgsParams) error {
	return nil
}
func (m *mockQuerier) ListUserVisibleOrgs(ctx context.Context, userID pgtype.UUID) ([]sqlc.ListUserVisibleOrgsRow, error) {
	return nil, nil
}
func (m *mockQuerier) ListUserAllOrgs(ctx context.Context, userID pgtype.UUID) ([]sqlc.ListUserAllOrgsRow, error) {
	return nil, nil
}
func (m *mockQuerier) SetOrgVisibility(ctx context.Context, arg sqlc.SetOrgVisibilityParams) error {
	return nil
}
func (m *mockQuerier) GetUserVisibleOrgsByUsername(ctx context.Context, githubUsername string) ([]sqlc.GetUserVisibleOrgsByUsernameRow, error) {
	return nil, nil
}
func (m *mockQuerier) ListVisibleOrgsByUserIDs(ctx context.Context, userIds []pgtype.UUID) ([]sqlc.ListVisibleOrgsByUserIDsRow, error) {
	return nil, nil
}
func (m *mockQuerier) SearchOrgs(ctx context.Context, arg sqlc.SearchOrgsParams) ([]sqlc.SearchOrgsRow, error) {
	return nil, nil
}
func (m *mockQuerier) GetOrgStats(ctx context.Context, login string) (sqlc.GetOrgStatsRow, error) {
	return sqlc.GetOrgStatsRow{}, nil
}
func (m *mockQuerier) ListOrgMembers(ctx context.Context, login string) ([]sqlc.ListOrgMembersRow, error) {
	return nil, nil
}

// ---- Attestation methods -------------------------------------------------

func (m *mockQuerier) CreateAttestation(ctx context.Context, arg sqlc.CreateAttestationParams) (sqlc.Attestation, error) {
	return sqlc.Attestation{}, nil
}
func (m *mockQuerier) DeleteAttestation(ctx context.Context, arg sqlc.DeleteAttestationParams) error {
	return nil
}
func (m *mockQuerier) ListTranscriptAttestations(ctx context.Context, transcriptID pgtype.UUID) ([]sqlc.ListTranscriptAttestationsRow, error) {
	return nil, nil
}
func (m *mockQuerier) ListAttestationsByTranscriptIDs(ctx context.Context, transcriptIds []pgtype.UUID) ([]sqlc.ListAttestationsByTranscriptIDsRow, error) {
	return nil, nil
}
func (m *mockQuerier) GetGroupTranscriptStats(ctx context.Context, groupID pgtype.UUID) (sqlc.GetGroupTranscriptStatsRow, error) {
	return sqlc.GetGroupTranscriptStatsRow{}, nil
}
func (m *mockQuerier) ListAllGroups(ctx context.Context) ([]sqlc.ListAllGroupsRow, error) {
	return nil, nil
}
func (m *mockQuerier) SearchCollectives(ctx context.Context, arg sqlc.SearchCollectivesParams) ([]sqlc.SearchCollectivesRow, error) {
	return nil, nil
}
func (m *mockQuerier) ListCollectivesByGitHubOrg(ctx context.Context, arg sqlc.ListCollectivesByGitHubOrgParams) ([]sqlc.ListCollectivesByGitHubOrgRow, error) {
	return nil, nil
}
func (m *mockQuerier) ListOwnerCollectiveContributions(ctx context.Context, ownerID pgtype.UUID) ([]sqlc.ListOwnerCollectiveContributionsRow, error) {
	if m.listOwnerCollectiveContributions != nil {
		return m.listOwnerCollectiveContributions(ctx, ownerID)
	}
	panic("ListOwnerCollectiveContributions: not stubbed")
}
func (m *mockQuerier) ListOwnerCollectiveSubmissions(ctx context.Context, arg sqlc.ListOwnerCollectiveSubmissionsParams) ([]sqlc.ListOwnerCollectiveSubmissionsRow, error) {
	if m.listOwnerCollectiveSubmissions != nil {
		return m.listOwnerCollectiveSubmissions(ctx, arg)
	}
	panic("ListOwnerCollectiveSubmissions: not stubbed")
}
func (m *mockQuerier) ListProjectCollectiveRollup(ctx context.Context, arg sqlc.ListProjectCollectiveRollupParams) ([]sqlc.ListProjectCollectiveRollupRow, error) {
	if m.listProjectCollectiveRollup != nil {
		return m.listProjectCollectiveRollup(ctx, arg)
	}
	panic("ListProjectCollectiveRollup: not stubbed")
}
func (m *mockQuerier) ListTranscriptCollectivesForViewer(ctx context.Context, arg sqlc.ListTranscriptCollectivesForViewerParams) ([]sqlc.ListTranscriptCollectivesForViewerRow, error) {
	if m.listTranscriptCollectivesForViewer != nil {
		return m.listTranscriptCollectivesForViewer(ctx, arg)
	}
	panic("ListTranscriptCollectivesForViewer: not stubbed")
}
func (m *mockQuerier) HasUserVisibleOrg(ctx context.Context, arg sqlc.HasUserVisibleOrgParams) (bool, error) {
	return false, nil
}
func (m *mockQuerier) UpdateMemberRole(ctx context.Context, arg sqlc.UpdateMemberRoleParams) error {
	return nil
}
func (m *mockQuerier) ListGroupModelBreakdown(ctx context.Context, groupID pgtype.UUID) ([]sqlc.ListGroupModelBreakdownRow, error) {
	return nil, nil
}
func (m *mockQuerier) ListGroupContributors(ctx context.Context, arg sqlc.ListGroupContributorsParams) ([]sqlc.ListGroupContributorsRow, error) {
	return nil, nil
}
func (m *mockQuerier) BulkUpsertAnnotations(ctx context.Context, arg sqlc.BulkUpsertAnnotationsParams) ([]sqlc.BulkUpsertAnnotationsRow, error) {
	if m.bulkUpsertAnnotations != nil {
		return m.bulkUpsertAnnotations(ctx, arg)
	}
	panic("BulkUpsertAnnotations: not stubbed")
}
func (m *mockQuerier) ListAnnotationContentHashesByOwner(ctx context.Context, ownerID pgtype.UUID) ([]string, error) {
	if m.listAnnotationContentHashesByOwner != nil {
		return m.listAnnotationContentHashesByOwner(ctx, ownerID)
	}
	panic("ListAnnotationContentHashesByOwner: not stubbed")
}
func (m *mockQuerier) ListOwnerAnnotationHashesForTranscriptIDs(ctx context.Context, arg sqlc.ListOwnerAnnotationHashesForTranscriptIDsParams) ([]sqlc.ListOwnerAnnotationHashesForTranscriptIDsRow, error) {
	if m.listOwnerAnnotationHashesForTranscriptIDs != nil {
		return m.listOwnerAnnotationHashesForTranscriptIDs(ctx, arg)
	}
	return nil, nil
}
func (m *mockQuerier) DeleteAnnotationByContentHash(ctx context.Context, arg sqlc.DeleteAnnotationByContentHashParams) error {
	if m.deleteAnnotationByContentHash != nil {
		return m.deleteAnnotationByContentHash(ctx, arg)
	}
	panic("DeleteAnnotationByContentHash: not stubbed")
}
func (m *mockQuerier) CreateManualAnnotation(ctx context.Context, arg sqlc.CreateManualAnnotationParams) (sqlc.Annotation, error) {
	if m.createManualAnnotation != nil {
		return m.createManualAnnotation(ctx, arg)
	}
	panic("CreateManualAnnotation: not stubbed")
}
func (m *mockQuerier) ListAnnotationsByTranscriptID(ctx context.Context, transcriptID pgtype.UUID) ([]sqlc.Annotation, error) {
	if m.listAnnotationsByTranscriptID != nil {
		return m.listAnnotationsByTranscriptID(ctx, transcriptID)
	}
	panic("ListAnnotationsByTranscriptID: not stubbed")
}
func (m *mockQuerier) ListUserSharesInGroup(ctx context.Context, arg sqlc.ListUserSharesInGroupParams) ([]sqlc.ListUserSharesInGroupRow, error) {
	return nil, nil
}
func (m *mockQuerier) RetractUserSharesInGroup(ctx context.Context, arg sqlc.RetractUserSharesInGroupParams) error {
	return nil
}
func (m *mockQuerier) ListGroupOwnersForTranscript(ctx context.Context, transcriptID pgtype.UUID) ([]pgtype.UUID, error) {
	return nil, nil
}

// ---- Collective repository / GitHub App methods --------------------------

func (m *mockQuerier) UpsertGitHubAppInstallation(ctx context.Context, arg sqlc.UpsertGitHubAppInstallationParams) error {
	if m.upsertGitHubAppInstallation != nil {
		return m.upsertGitHubAppInstallation(ctx, arg)
	}
	return nil
}
func (m *mockQuerier) GetGitHubAppInstallation(ctx context.Context, installationID int64) (sqlc.GithubAppInstallation, error) {
	if m.getGitHubAppInstallation != nil {
		return m.getGitHubAppInstallation(ctx, installationID)
	}
	panic("GetGitHubAppInstallation: not stubbed")
}
func (m *mockQuerier) LinkCollectiveRepository(ctx context.Context, arg sqlc.LinkCollectiveRepositoryParams) (sqlc.CollectiveRepository, error) {
	if m.linkCollectiveRepository != nil {
		return m.linkCollectiveRepository(ctx, arg)
	}
	panic("LinkCollectiveRepository: not stubbed")
}
func (m *mockQuerier) UnlinkCollectiveRepository(ctx context.Context, arg sqlc.UnlinkCollectiveRepositoryParams) (int64, error) {
	if m.unlinkCollectiveRepository != nil {
		return m.unlinkCollectiveRepository(ctx, arg)
	}
	panic("UnlinkCollectiveRepository: not stubbed")
}
func (m *mockQuerier) ListCollectiveRepositories(ctx context.Context, groupID pgtype.UUID) ([]sqlc.CollectiveRepository, error) {
	if m.listCollectiveRepositories != nil {
		return m.listCollectiveRepositories(ctx, groupID)
	}
	panic("ListCollectiveRepositories: not stubbed")
}
func (m *mockQuerier) GetCollectiveRepository(ctx context.Context, arg sqlc.GetCollectiveRepositoryParams) (sqlc.CollectiveRepository, error) {
	if m.getCollectiveRepository != nil {
		return m.getCollectiveRepository(ctx, arg)
	}
	panic("GetCollectiveRepository: not stubbed")
}
func (m *mockQuerier) UpdateCollectiveRepositorySync(ctx context.Context, arg sqlc.UpdateCollectiveRepositorySyncParams) error {
	if m.updateCollectiveRepositorySync != nil {
		return m.updateCollectiveRepositorySync(ctx, arg)
	}
	return nil
}
func (m *mockQuerier) UpsertRepositoryCommit(ctx context.Context, arg sqlc.UpsertRepositoryCommitParams) error {
	if m.upsertRepositoryCommit != nil {
		return m.upsertRepositoryCommit(ctx, arg)
	}
	return nil
}
func (m *mockQuerier) ListRepositoryCommits(ctx context.Context, arg sqlc.ListRepositoryCommitsParams) ([]sqlc.RepositoryCommit, error) {
	if m.listRepositoryCommits != nil {
		return m.listRepositoryCommits(ctx, arg)
	}
	panic("ListRepositoryCommits: not stubbed")
}
func (m *mockQuerier) CountRepositoryCommits(ctx context.Context, arg sqlc.CountRepositoryCommitsParams) (int64, error) {
	if m.countRepositoryCommits != nil {
		return m.countRepositoryCommits(ctx, arg)
	}
	return 0, nil
}

// ----------------------------------------------------------------------------
// mockTranscriptBlobStore records encrypted-store calls for handler tests.
// ----------------------------------------------------------------------------

type mockTranscriptBlobStore struct {
	uploadedKeys []string
	uploadErr    error
	deletedKeys  []string
	deleteErr    error
}

func (m *mockTranscriptBlobStore) Write(_ context.Context, _ uuid.UUID, content []byte) (storage.BlobDescriptor, storage.ContentIdentity, error) {
	if m.uploadErr != nil {
		return storage.BlobDescriptor{}, storage.ContentIdentity{}, m.uploadErr
	}
	key := "transcripts/" + uuid.NewString() + ".bin"
	m.uploadedKeys = append(m.uploadedKeys, key)
	descriptor, err := storage.NewBlobDescriptor(storage.ObjectKey(key), []byte("test-wrapped-key"), storage.EncryptionAES256GCMRandomNonceV1, 1)
	if err != nil {
		return storage.BlobDescriptor{}, storage.ContentIdentity{}, err
	}
	identity, err := storage.NewContentIdentity(schema.ComputeTranscriptHash(content), int64(len(content)))
	return descriptor, identity, err
}

func (*mockTranscriptBlobStore) Read(context.Context, uuid.UUID, storage.BlobDescriptor, storage.LoadedContentIdentity) ([]byte, storage.ContentIdentity, error) {
	return nil, storage.ContentIdentity{}, errors.New("mock transcript blob read not configured")
}
func (*mockTranscriptBlobStore) Rewrap(context.Context, uuid.UUID, storage.BlobDescriptor) (storage.BlobDescriptor, error) {
	return storage.BlobDescriptor{}, errors.New("mock transcript blob rewrap not configured")
}
func (m *mockTranscriptBlobStore) Delete(_ context.Context, descriptor storage.BlobDescriptor) error {
	m.deletedKeys = append(m.deletedKeys, string(descriptor.ObjectKey()))
	return m.deleteErr
}

// Compile-time assertion: mockQuerier must satisfy Querier.
var _ Querier = (*mockQuerier)(nil)

// ----------------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------------

// minimalConfig returns a config with enough fields populated that the
// handler constructor will not panic. GitHub OAuth flows are not exercised
// in unit tests, so those fields can be empty.
func minimalConfig() *config.Config {
	return &config.Config{
		BaseURL:     "https://example.com",
		FrontendURL: "https://app.example.com",
		JWTSecret:   "test-jwt-secret-unused-in-these-tests",
	}
}

// newTestHandler builds a *Handler whose DB access goes through the supplied
// mock. blobs is optional for tests that do not exercise transcript content.
func newTestHandler(q Querier, blobs storage.TranscriptBlobStore) *Handler {
	titles, err := redact.NewTitlePipeline()
	if err != nil {
		panic(err)
	}
	return &Handler{
		cfg:                   minimalConfig(),
		queries:               q,
		blobs:                 blobs,
		titles:                titles,
		preservationEvaluator: productionObservedModelPreservationEvaluator{},
		scanContent:           scanner.ScanForSecrets,
		// The same labeler production wires, so a unit test never observes a
		// resolver the served handler does not have.
		projectNames: projectname.Resolver{Label: schema.RemoteLabel},
	}
}

// decodeError reads the JSON {"error": "..."} body written by writeError.
func decodeError(t *testing.T, body []byte) string {
	t.Helper()
	var resp map[string]string
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to decode error body %q: %v", body, err)
	}
	return resp["error"]
}

// pgUUIDFrom converts a uuid.UUID into pgtype.UUID.
func pgUUIDFrom(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

// pgText creates a non-null pgtype.Text.
func pgText(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: true}
}

// ----------------------------------------------------------------------------
// TestCLILogin_PortValidation
// ----------------------------------------------------------------------------

func TestCLILogin_PortValidation(t *testing.T) {
	type testCase struct {
		name       string
		port       string
		state      string
		wantStatus int
		wantErrMsg string
	}

	cases := []testCase{
		{
			name:       "missing port",
			port:       "",
			state:      "abc",
			wantStatus: http.StatusBadRequest,
			wantErrMsg: "Missing port or state parameter",
		},
		{
			name:       "missing state",
			port:       "8080",
			state:      "",
			wantStatus: http.StatusBadRequest,
			wantErrMsg: "Missing port or state parameter",
		},
		{
			name:       "non-numeric port",
			port:       "not-a-port",
			state:      "abc",
			wantStatus: http.StatusBadRequest,
			wantErrMsg: "Invalid port parameter",
		},
		{
			name:       "port below minimum (0)",
			port:       "0",
			state:      "abc",
			wantStatus: http.StatusBadRequest,
			wantErrMsg: "Port must be between 1024 and 65535",
		},
		{
			name:       "port below minimum (1023)",
			port:       "1023",
			state:      "abc",
			wantStatus: http.StatusBadRequest,
			wantErrMsg: "Port must be between 1024 and 65535",
		},
		{
			name:       "port above maximum (65536)",
			port:       "65536",
			state:      "abc",
			wantStatus: http.StatusBadRequest,
			wantErrMsg: "Port must be between 1024 and 65535",
		},
		{
			name:       "port above maximum (99999)",
			port:       "99999",
			state:      "abc",
			wantStatus: http.StatusBadRequest,
			wantErrMsg: "Port must be between 1024 and 65535",
		},
	}

	// Valid-port cases require a DB insert; we stub InsertCLISession to
	// return success so we can verify the handler proceeds past validation
	// (it will try to redirect to GitHub, which is fine for this test).
	validPortCases := []testCase{
		{name: "minimum valid port (1024)", port: "1024", state: "abc", wantStatus: http.StatusTemporaryRedirect},
		{name: "common valid port (8080)", port: "8080", state: "abc", wantStatus: http.StatusTemporaryRedirect},
		{name: "maximum valid port (65535)", port: "65535", state: "abc", wantStatus: http.StatusTemporaryRedirect},
	}

	// Error path: no DB access needed because validation fires first.
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHandler(&mockQuerier{}, nil)

			target := "/api/v1/auth/cli/login"
			if tc.port != "" || tc.state != "" {
				target += "?"
				if tc.port != "" {
					target += "port=" + tc.port
				}
				if tc.port != "" && tc.state != "" {
					target += "&"
				}
				if tc.state != "" {
					target += "state=" + tc.state
				}
			}
			r := httptest.NewRequest(http.MethodGet, target, nil)
			w := httptest.NewRecorder()

			h.CLILogin(w, r)

			if w.Code != tc.wantStatus {
				t.Errorf("status: got %d, want %d", w.Code, tc.wantStatus)
			}
			if tc.wantErrMsg != "" {
				got := decodeError(t, w.Body.Bytes())
				if got != tc.wantErrMsg {
					t.Errorf("error message: got %q, want %q", got, tc.wantErrMsg)
				}
			}
		})
	}

	// Success path: stub the DB so InsertCLISession succeeds.
	for _, tc := range validPortCases {
		t.Run(tc.name, func(t *testing.T) {
			sessionID := pgUUIDFrom(uuid.New())
			q := &mockQuerier{
				insertCLISession: func(_ context.Context, arg sqlc.InsertCLISessionParams) (pgtype.UUID, error) {
					return sessionID, nil
				},
			}
			h := newTestHandler(q, nil)

			r := httptest.NewRequest(http.MethodGet,
				"/api/v1/auth/cli/login?port="+tc.port+"&state="+tc.state, nil)
			w := httptest.NewRecorder()

			h.CLILogin(w, r)

			if w.Code != tc.wantStatus {
				t.Errorf("status: got %d, want %d (body: %s)", w.Code, tc.wantStatus, w.Body.String())
			}
		})
	}
}

// ----------------------------------------------------------------------------
// TestCLIExchange — the high-risk endpoint that creates API keys
// ----------------------------------------------------------------------------

// validExchangeSession returns a CliAuthSession that represents a session
// ready to be exchanged: exchange_code set, user_id set, not yet exchanged.
func validExchangeSession(exchangeCode string, cliState string, userID uuid.UUID, username string) sqlc.CliAuthSession {
	return sqlc.CliAuthSession{
		ID:           pgUUIDFrom(uuid.New()),
		OauthState:   "some-oauth-state",
		CliPort:      9999,
		CliState:     cliState,
		ExchangeCode: pgText(exchangeCode),
		UserID:       pgUUIDFrom(userID),
		Username:     pgText(username),
		// ExchangedAt left zero (not yet exchanged)
	}
}

// validAPIKey returns an ApiKey row that CreateAPIKey would return.
func validAPIKey(userID uuid.UUID) sqlc.ApiKey {
	return sqlc.ApiKey{
		ID:        pgUUIDFrom(uuid.New()),
		UserID:    pgUUIDFrom(userID),
		KeyHash:   "fake-hash",
		KeyPrefix: "peasant_fa",
	}
}

// postCLIExchange builds and fires a POST /api/v1/auth/cli/exchange request.
func postCLIExchange(t *testing.T, h *Handler, code, state string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(CLIExchangeRequest{Code: code, State: state})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/cli/exchange", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.CLIExchange(w, r)
	return w
}

// TestCLIExchange_MissingFields verifies that omitting code or state yields 400.
func TestCLIExchange_MissingFields(t *testing.T) {
	cases := []struct {
		name  string
		code  string
		state string
	}{
		{"missing code", "", "some-state"},
		{"missing state", "some-code", ""},
		{"both missing", "", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHandler(&mockQuerier{}, nil)
			w := postCLIExchange(t, h, tc.code, tc.state)
			if w.Code != http.StatusBadRequest {
				t.Errorf("status: got %d, want %d", w.Code, http.StatusBadRequest)
			}
		})
	}
}

// TestCLIExchange_Success exercises the happy path: a valid code+state pair
// causes ExchangeCLISession to return a session, the handler issues an API
// key, and the response is 200 with the expected JSON fields.
func TestCLIExchange_Success(t *testing.T) {
	const exchangeCode = "abc123def456abc123def456abc123de"
	const cliState = "my-cli-state"
	userID := uuid.New()
	const username = "testuser"

	session := validExchangeSession(exchangeCode, cliState, userID, username)
	createdKey := validAPIKey(userID)

	q := &mockQuerier{
		exchangeCLISession: func(_ context.Context, arg sqlc.ExchangeCLISessionParams) (sqlc.CliAuthSession, error) {
			if arg.ExchangeCode.String != exchangeCode {
				t.Errorf("ExchangeCLISession: code mismatch: got %q, want %q", arg.ExchangeCode.String, exchangeCode)
			}
			if arg.CliState != cliState {
				t.Errorf("ExchangeCLISession: state mismatch: got %q, want %q", arg.CliState, cliState)
			}
			return session, nil
		},
		createAPIKey: func(_ context.Context, arg sqlc.CreateAPIKeyParams) (sqlc.ApiKey, error) {
			// Verify the key is labelled for the CLI and associated with the right user.
			if arg.Label.String != "peasant-cli" {
				t.Errorf("CreateAPIKey: label: got %q, want %q", arg.Label.String, "peasant-cli")
			}
			if arg.UserID != pgUUIDFrom(userID) {
				t.Errorf("CreateAPIKey: userID mismatch")
			}
			return createdKey, nil
		},
	}

	h := newTestHandler(q, nil)
	w := postCLIExchange(t, h, exchangeCode, cliState)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d (body: %s)", w.Code, http.StatusOK, w.Body.String())
	}

	var resp CLIExchangeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// APIKey must be non-empty and contain a plaintext key (not a hash).
	if resp.APIKey == "" {
		t.Error("APIKey in response is empty")
	}
	if resp.UserID != userID.String() {
		t.Errorf("UserID: got %q, want %q", resp.UserID, userID.String())
	}
	if resp.Username != username {
		t.Errorf("Username: got %q, want %q", resp.Username, username)
	}
	if resp.KeyID == "" {
		t.Error("KeyID in response is empty")
	}
}

// TestCLIExchange_InvalidCode checks that a non-existent code returns 401.
// This simulates ExchangeCLISession returning an error (no row found).
func TestCLIExchange_InvalidCode(t *testing.T) {
	q := &mockQuerier{
		exchangeCLISession: func(_ context.Context, arg sqlc.ExchangeCLISessionParams) (sqlc.CliAuthSession, error) {
			return sqlc.CliAuthSession{}, errors.New("no rows")
		},
	}

	h := newTestHandler(q, nil)
	w := postCLIExchange(t, h, "bad-code", "some-state")

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

// TestCLIExchange_AlreadyExchanged simulates a second exchange attempt.
// The DB UPDATE … WHERE exchanged_at IS NULL will find no rows on the
// second call, so ExchangeCLISession returns an error — same 401 path.
func TestCLIExchange_AlreadyExchanged(t *testing.T) {
	callCount := 0
	q := &mockQuerier{
		exchangeCLISession: func(_ context.Context, arg sqlc.ExchangeCLISessionParams) (sqlc.CliAuthSession, error) {
			callCount++
			// Simulate: first call succeeds (setup), second call fails
			// because exchanged_at is now set.
			if callCount > 1 {
				return sqlc.CliAuthSession{}, errors.New("no rows: session already exchanged")
			}
			// Should not reach here during the actual test request.
			return sqlc.CliAuthSession{}, errors.New("unexpected call")
		},
	}

	h := newTestHandler(q, nil)
	// Attempt exchange with any code — the mock always returns error.
	w := postCLIExchange(t, h, "some-code", "some-state")

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

// TestCLIExchange_ExpiredSession simulates an expired session (>5 min old).
// The DB UPDATE … WHERE created_at > now() - interval '5 minutes' will
// find no rows, so ExchangeCLISession returns an error.
func TestCLIExchange_ExpiredSession(t *testing.T) {
	q := &mockQuerier{
		exchangeCLISession: func(_ context.Context, arg sqlc.ExchangeCLISessionParams) (sqlc.CliAuthSession, error) {
			return sqlc.CliAuthSession{}, errors.New("no rows: session expired")
		},
	}

	h := newTestHandler(q, nil)
	w := postCLIExchange(t, h, "expired-code", "some-state")

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

// TestCLIExchange_MismatchedState verifies that when the DB returns a session
// whose ExchangeCode does NOT match the supplied code (mismatched state leads
// to a different row being returned), the constant-time compare rejects it.
//
// In practice the SQL query filters on cli_state too, so this also covers the
// case where an attacker supplies a valid code but wrong state.
func TestCLIExchange_MismatchedState(t *testing.T) {
	const realCode = "real-exchange-code-padded-to-32ch"
	const suppliedCode = "attacker-supplied-code-different!"

	// The DB returns no row when the state doesn't match, so we simulate
	// ExchangeCLISession failing (which is what happens when cli_state mismatches).
	q := &mockQuerier{
		exchangeCLISession: func(_ context.Context, arg sqlc.ExchangeCLISessionParams) (sqlc.CliAuthSession, error) {
			// The SQL WHERE clause includes cli_state = $2, so a wrong state
			// yields no row.
			return sqlc.CliAuthSession{}, errors.New("no rows: state mismatch")
		},
	}

	h := newTestHandler(q, nil)
	w := postCLIExchange(t, h, suppliedCode, "wrong-state")

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

// TestCLIExchange_ConstantTimeCompare verifies that even when ExchangeCLISession
// succeeds (returning a session), if the returned exchange_code does not byte-
// for-byte match the supplied code the handler rejects the request with 401.
//
// This covers the in-process constant-time guard:
//
//	subtle.ConstantTimeCompare([]byte(session.ExchangeCode.String), []byte(req.Code)) != 1
func TestCLIExchange_ConstantTimeCompare(t *testing.T) {
	const suppliedCode = "attacker-code-aaaaaaaaaaaaaaaa"
	// The DB returns a session whose exchange_code differs from the supplied one.
	// This could happen if, hypothetically, the DB query were somehow confused.
	// The in-process ConstantTimeCompare is the final defence.
	userID := uuid.New()

	q := &mockQuerier{
		exchangeCLISession: func(_ context.Context, arg sqlc.ExchangeCLISessionParams) (sqlc.CliAuthSession, error) {
			// Return a session with a DIFFERENT exchange_code than what was supplied.
			return sqlc.CliAuthSession{
				ID:           pgUUIDFrom(uuid.New()),
				OauthState:   "some-oauth-state",
				CliPort:      9999,
				CliState:     arg.CliState,
				ExchangeCode: pgText("legitimate-code-bbbbbbbbbbbbbbbb"),
				UserID:       pgUUIDFrom(userID),
				Username:     pgText("victim"),
			}, nil
		},
	}

	h := newTestHandler(q, nil)
	w := postCLIExchange(t, h, suppliedCode, "some-state")

	// The handler must reject this even though the DB row was found,
	// because the codes differ.
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want %d — constant-time compare should have rejected mismatched code",
			w.Code, http.StatusUnauthorized)
	}
}

// TestCLIExchange_CreateAPIKeyFailure verifies that a DB error during API key
// creation returns 500 rather than leaking a partial response.
func TestCLIExchange_CreateAPIKeyFailure(t *testing.T) {
	const exchangeCode = "good-code-aaaaaaaaaaaaaaaaaaaaaa"
	const cliState = "state-xyz"
	userID := uuid.New()

	session := validExchangeSession(exchangeCode, cliState, userID, "someuser")

	q := &mockQuerier{
		exchangeCLISession: func(_ context.Context, arg sqlc.ExchangeCLISessionParams) (sqlc.CliAuthSession, error) {
			return session, nil
		},
		createAPIKey: func(_ context.Context, arg sqlc.CreateAPIKeyParams) (sqlc.ApiKey, error) {
			return sqlc.ApiKey{}, errors.New("db: connection reset")
		},
	}

	h := newTestHandler(q, nil)
	w := postCLIExchange(t, h, exchangeCode, cliState)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

// TestCLIExchange_InvalidRequestBody verifies that a malformed JSON body
// yields 400 rather than a panic.
func TestCLIExchange_InvalidRequestBody(t *testing.T) {
	h := newTestHandler(&mockQuerier{}, nil)

	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/cli/exchange",
		bytes.NewBufferString("{not valid json"))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.CLIExchange(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusBadRequest)
	}
}
