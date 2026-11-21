package router

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/redact"
	schemaRoute "github.com/peasant-labs/schema/route"
	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/handler"
	"github.com/peasant-labs/village/backend/internal/middleware"
	"github.com/peasant-labs/village/backend/internal/storage"
)

// authRateLimitRequests is the maximum number of requests per IP per authRateLimitWindow
// for auth endpoints. This rate is intentionally strict to deter credential stuffing.
const (
	authRateLimitRequests = 5
	authRateLimitWindow   = time.Minute

	unsubscribeRateLimitRequests = 60
)

// New builds the routes. It starts no background work: it hands back no
// drain, so nothing it started could be stopped.
func New(cfg *config.Config, pool *pgxpool.Pool, blobs storage.TranscriptBlobStore, titles *redact.TitlePipeline) http.Handler {
	routes, _ := build(cfg, pool, blobs, titles)
	return routes
}

// NewWithDrain is New plus the background integrity check, started when it
// is configured, the content cache's idle-lifetime ticker, and the work a
// shutdown waits for after the server stops: the invitation emails still being
// sent after their responses, an integrity check still in flight, and a cache
// sweep still running.
func NewWithDrain(cfg *config.Config, pool *pgxpool.Pool, blobs storage.TranscriptBlobStore, titles *redact.TitlePipeline) (http.Handler, func(context.Context) error) {
	routes, h := build(cfg, pool, blobs, titles)
	h.StartIntegrityWorker()
	h.StartContentCacheSweeper()
	return routes, func(ctx context.Context) error {
		// The drains share the shutdown's one deadline.
		var integrityErr, cacheErr error
		var drains sync.WaitGroup
		drains.Add(2)
		go func() {
			defer drains.Done()
			integrityErr = h.DrainIntegrity(ctx)
		}()
		go func() {
			defer drains.Done()
			cacheErr = h.DrainContentCache(ctx)
		}()
		noticesErr := h.DrainNotices(ctx)
		drains.Wait()
		return errors.Join(noticesErr, integrityErr, cacheErr)
	}
}

func build(cfg *config.Config, pool *pgxpool.Pool, blobs storage.TranscriptBlobStore, titles *redact.TitlePipeline) (http.Handler, *handler.Handler) {
	r := chi.NewRouter()

	r.Use(chimw.RequestID)
	r.Use(middleware.Logger)
	r.Use(chimw.Recoverer)
	r.Use(middleware.CORS(cfg.FrontendURL))

	h := handler.NewWithTitlePipeline(cfg, pool, blobs, titles)

	// authLimiter enforces a strict per-IP rate limit on OAuth entry points to
	// deter abuse without constraining authenticated API traffic.
	authLimiter := httprate.LimitByIP(authRateLimitRequests, authRateLimitWindow)
	// unsubscribeLimiter bounds the signed-out unsubscribe target. It is looser
	// than authLimiter because mail providers send one-click unsubscribes for
	// many people from shared addresses.
	unsubscribeLimiter := httprate.LimitByIP(unsubscribeRateLimitRequests, authRateLimitWindow)

	r.Get(schemaRoute.VillageGetHealth.Pattern(), h.Health)

	// API routes take their patterns from the schema contract.
	r.Route(handler.APIPrefix, func(r chi.Router) {
		// Village API OpenAPI spec, served from the contract module
		// (schema.VillageAPISpecJSON) for client/tooling discovery; the publish path
		// enforces the same module's PublishRequest schema.
		r.Get(handler.MountPattern(schemaRoute.VillageGetVillageAPIDocument), h.ServeOpenAPI)
		// Auth (rate-limited entry points)
		r.With(authLimiter).Get(handler.MountPattern(schemaRoute.VillageStartGitHubSignIn), h.GitHubLogin)
		r.Get(handler.MountPattern(schemaRoute.VillageCompleteGitHubSignIn), h.GitHubCallback)
		r.With(authLimiter).Get(handler.MountPattern(schemaRoute.VillageStartGitLabSignIn), h.GitLabLogin)
		r.Get(handler.MountPattern(schemaRoute.VillageCompleteGitLabSignIn), h.GitLabCallback)
		r.With(authLimiter).Get(handler.MountPattern(schemaRoute.VillageStartHuggingFaceSignIn), h.HuggingFaceLogin)
		r.Get(handler.MountPattern(schemaRoute.VillageCompleteHuggingFaceSignIn), h.HuggingFaceCallback)
		r.With(authLimiter).Get(handler.MountPattern(schemaRoute.VillageStartCodebergSignIn), h.CodebergLogin)
		r.Get(handler.MountPattern(schemaRoute.VillageCompleteCodebergSignIn), h.CodebergCallback)
		r.With(authLimiter).Get(handler.MountPattern(schemaRoute.VillageStartSourceHutSignIn), h.SourceHutLogin)
		r.Get(handler.MountPattern(schemaRoute.VillageCompleteSourceHutSignIn), h.SourceHutCallback)
		r.With(authLimiter).Get(handler.MountPattern(schemaRoute.VillageCliLogin), h.CLILogin)
		r.With(authLimiter).Post(handler.MountPattern(schemaRoute.VillageCliExchangeCode), h.CLIExchange)
		r.Post(handler.MountPattern(schemaRoute.VillageLogOut), h.Logout)
		r.With(h.AuthRequired).Get(handler.MountPattern(schemaRoute.VillageGetAuthenticatedVillageUser), h.Me)
		r.With(h.AuthRequired).Delete(handler.MountPattern(schemaRoute.VillageDeleteMyAccount), h.DeleteAccount)
		r.With(h.AuthRequired).Patch(handler.MountPattern(schemaRoute.VillageSetMyDiscoverability), h.UpdateMySettings)
		r.With(h.AuthRequired).Patch(handler.MountPattern(schemaRoute.VillageSetMyUsername), h.SetMyUsername)

		// API keys
		r.With(h.AuthRequired).Post(handler.MountPattern(schemaRoute.VillageCreateMyAPIKey), h.CreateAPIKey)
		r.With(h.AuthRequired).Get(handler.MountPattern(schemaRoute.VillageListMyAPIKeys), h.ListAPIKeys)
		r.With(h.AuthRequired).Delete(handler.MountPattern(schemaRoute.VillageRevokeMyAPIKey), h.RevokeAPIKey)

		// User public profiles
		r.With(h.AuthOptional).Get(handler.MountPattern(schemaRoute.VillageGetUserProfile), h.GetUserPublicProfile)
		r.With(h.AuthOptional).Get(handler.MountPattern(schemaRoute.VillageListUserOrganizations), h.ListUserPublicOrgs)
		// One user's project. Keyed on the project HASH, which is the identity a
		// user's transcripts group by; AuthOptional, and it answers 404 for a
		// non-discoverable owner exactly as the profile route above does.
		r.With(h.AuthOptional).Get(handler.MountPattern(schemaRoute.VillageGetUserProject), h.GetUserProject)

		// Owner corrections to a project's rendered name. Both routes are keyed on
		// the project hash, never on a name: a name is what changes, so it cannot
		// also be the key. Neither route writes transcripts.project_name.
		r.With(h.AuthRequired).Patch(handler.MountPattern(schemaRoute.VillageSetMyProjectDisplayName), h.SetProjectDisplayName)
		r.With(h.AuthRequired).Delete(handler.MountPattern(schemaRoute.VillageClearMyProjectDisplayName), h.ClearProjectDisplayName)

		// Authenticated user org management
		r.With(h.AuthRequired).Get(handler.MountPattern(schemaRoute.VillageListAuthenticatedVillageOrganizations), h.ListMyOrgs)
		r.With(h.AuthRequired).Patch(handler.MountPattern(schemaRoute.VillageSetMyOrganizationVisibility), h.SetOrgVisibility)

		// Organizations (aggregated GitHub org affiliations)
		r.With(h.AuthOptional).Get(handler.MountPattern(schemaRoute.VillageSearchOrganizations), h.SearchOrgs)
		r.With(h.AuthOptional).Get(handler.MountPattern(schemaRoute.VillageGetVillageOrganizationCollectives), h.GetOrg)

		// Transcripts
		h.RegisterPublishRoutes(r)
		h.RegisterTranscriptBrowseRoutes(r)
		r.With(h.AuthOptional).Get(handler.MountPattern(schemaRoute.VillageGetTranscriptMetadata), h.GetTranscript)
		r.With(h.AuthOptional).Get(handler.MountPattern(schemaRoute.VillageGetTranscriptContent), h.GetTranscriptContent)
		r.With(h.AuthOptional).Get(handler.MountPattern(schemaRoute.VillageGetTranscriptSegments), h.GetTranscriptSegments)
		r.With(h.AuthOptional).Post(handler.MountPattern(schemaRoute.VillageLocateTranscriptEntry), h.LocateTranscriptEntry)
		r.With(h.AuthOptional).Get(handler.MountPattern(schemaRoute.VillageGetTranscriptSegment), h.GetTranscriptSegment)
		r.With(h.AuthOptional).Get(handler.MountPattern(schemaRoute.VillageGetTranscriptWindow), h.GetTranscriptWindow)
		r.With(h.AuthOptional).Get(handler.MountPattern(schemaRoute.VillageGetTranscriptHeader), h.GetTranscriptHeader)
		r.With(h.AuthRequired).Patch(handler.MountPattern(schemaRoute.VillageUpdateTranscript), h.UpdateTranscript)
		r.With(h.AuthRequired).Delete(handler.MountPattern(schemaRoute.VillageDeleteTranscript), h.DeleteTranscript)
		r.With(h.AuthRequired).Post(handler.MountPattern(schemaRoute.VillageShareTranscriptWithGroups), h.ShareTranscript)
		r.With(h.AuthRequired).Delete(handler.MountPattern(schemaRoute.VillageUnshareTranscriptFromGroup), h.UnshareTranscript)
		// Owner-only share-event history for one (transcript, collective) pair.
		// Deliberately no username parameter anywhere on this path: owner-only
		// is enforced by the ROUTE, not merely a predicate a caller could bypass
		// with someone else's identifier.
		r.With(h.AuthRequired).Get(handler.MountPattern(schemaRoute.VillageListShareEventHistory), h.ListShareEventHistory)

		// Every (transcript, collective) pair the caller has offered to one
		// collective, read from the attempt ledger rather than the derived
		// current-state row, so a contribution whose last event was a
		// withdrawal is still listed and its history is still reachable.
		// Owner-only by the same route discipline: no username segment exists,
		// and a caller with no pair here is answered 404, never 403.
		r.With(h.AuthRequired).Get(handler.MountPattern(schemaRoute.VillageListMyCollectiveSubmissions), h.ListMyCollectiveSubmissions)

		// Attestations
		r.With(h.AuthOptional).Get(handler.MountPattern(schemaRoute.VillageListTranscriptAttestations), h.ListTranscriptAttestations)
		r.With(h.AuthRequired).Post(handler.MountPattern(schemaRoute.VillageCreateTranscriptAttestation), h.CreateAttestation)
		r.With(h.AuthRequired).Delete(handler.MountPattern(schemaRoute.VillageDeleteTranscriptAttestation), h.DeleteAttestation)

		// Transcript annotations (read pushed labels; create manual per-turn labels)
		r.With(h.AuthOptional).Get(handler.MountPattern(schemaRoute.VillageListTranscriptAnnotations), h.ListTranscriptAnnotations)
		r.With(h.AuthRequired).Post(handler.MountPattern(schemaRoute.VillageCreateTranscriptAnnotation), h.CreateTranscriptAnnotation)

		// Transcript commits (read persisted git commits for the timeline overlay)
		r.With(h.AuthOptional).Get(handler.MountPattern(schemaRoute.VillageListTranscriptCommits), h.ListTranscriptCommits)

		// Collectives a transcript belongs to, and the caller's own contributions.
		// The contributions route takes the caller's id and has deliberately no
		// username variant: pending and refused counts are the contributor's alone.
		r.With(h.AuthOptional).Get(handler.MountPattern(schemaRoute.VillageListTranscriptCollectives), h.ListTranscriptCollectives)
		r.With(h.AuthRequired).Get(handler.MountPattern(schemaRoute.VillageListMyCollectiveContributions), h.ListMyCollectiveContributions)

		// Both resource families share handlers and auth through the complete
		// released compatibility window. Helper-group routes are unrelated.
		r.Get(handler.MountPattern(schemaRoute.VillageListPublicGroups), h.ListPublicGroups)
		r.Get(handler.MountPattern(schemaRoute.VillageListPublicGroupsDeprecated), h.ListPublicGroups)
		r.With(h.AuthOptional).Get(handler.MountPattern(schemaRoute.VillageSearchCollectives), h.SearchCollectives)
		r.With(h.AuthOptional).Get(handler.MountPattern(schemaRoute.VillageSearchCollectivesDeprecated), h.SearchCollectives)
		r.With(h.AuthRequired).Post(handler.MountPattern(schemaRoute.VillageCreateGroup), h.CreateGroup)
		r.With(h.AuthRequired).Post(handler.MountPattern(schemaRoute.VillageCreateGroupDeprecated), h.CreateGroup)
		r.With(h.AuthRequired).Get(handler.MountPattern(schemaRoute.VillageListGroups), h.ListGroups)
		r.With(h.AuthRequired).Get(handler.MountPattern(schemaRoute.VillageListGroupsDeprecated), h.ListGroups)
		// The collectives a person may SEE, as distinct from the ones they belong
		// to. Both routes exist because the browse surface and the contribute
		// picker ask different questions.
		r.With(h.AuthRequired).Get(handler.MountPattern(schemaRoute.VillageListVisibleGroups), h.ListVisibleGroups)
		r.With(h.AuthRequired).Get(handler.MountPattern(schemaRoute.VillageListVisibleGroupsDeprecated), h.ListVisibleGroups)
		r.With(h.AuthRequired).Patch(handler.MountPattern(schemaRoute.VillageUpdateGroup), h.UpdateGroup)
		r.With(h.AuthRequired).Patch(handler.MountPattern(schemaRoute.VillageUpdateGroupDeprecated), h.UpdateGroup)
		r.With(h.AuthRequired).Delete(handler.MountPattern(schemaRoute.VillageDeleteGroup), h.DeleteGroup)
		r.With(h.AuthRequired).Delete(handler.MountPattern(schemaRoute.VillageDeleteGroupDeprecated), h.DeleteGroup)
		r.With(h.AuthRequired).Post(handler.MountPattern(schemaRoute.VillageJoinGroup), h.JoinGroup)
		r.With(h.AuthRequired).Post(handler.MountPattern(schemaRoute.VillageJoinGroupDeprecated), h.JoinGroup)
		// An owner invites; the person becomes a member only by accepting.
		// No route makes someone a member without their consent.
		r.With(h.AuthRequired).Post(handler.MountPattern(schemaRoute.VillageCreateGroupInvitation), h.CreateGroupInvitation)
		r.With(h.AuthRequired).Post(handler.MountPattern(schemaRoute.VillageCreateGroupInvitationDeprecated), h.CreateGroupInvitation)
		r.With(h.AuthRequired).Get(handler.MountPattern(schemaRoute.VillageListGroupInvitations), h.ListGroupInvitations)
		r.With(h.AuthRequired).Get(handler.MountPattern(schemaRoute.VillageListGroupInvitationsDeprecated), h.ListGroupInvitations)
		r.With(h.AuthRequired).Delete(handler.MountPattern(schemaRoute.VillageRevokeGroupInvitation), h.RevokeGroupInvitation)
		r.With(h.AuthRequired).Delete(handler.MountPattern(schemaRoute.VillageRevokeGroupInvitationDeprecated), h.RevokeGroupInvitation)
		r.With(h.AuthRequired).Patch(handler.MountPattern(schemaRoute.VillageUpdateGroupMemberRole), h.PromoteMember)
		r.With(h.AuthRequired).Patch(handler.MountPattern(schemaRoute.VillageUpdateGroupMemberRoleDeprecated), h.PromoteMember)
		r.With(h.AuthRequired).Delete(handler.MountPattern(schemaRoute.VillageRemoveGroupMember), h.RemoveGroupMember)
		r.With(h.AuthRequired).Delete(handler.MountPattern(schemaRoute.VillageRemoveGroupMemberDeprecated), h.RemoveGroupMember)
		// Contributing a whole project: one read that says what the caller may
		// offer this collective, and one write that offers a project's
		// transcripts in a single transaction. Both are member-only.
		r.With(h.AuthRequired).Post(handler.MountPattern(schemaRoute.VillageBatchShareProject), h.BatchShareProject)
		r.With(h.AuthRequired).Post(handler.MountPattern(schemaRoute.VillageBatchShareProjectDeprecated), h.BatchShareProject)
		// Reviewing submissions: one route decides ONE submission, the other
		// decides a whole selection in one action. Both are owner-only and
		// neither touches transcript visibility.
		r.With(h.AuthRequired).Patch(handler.MountPattern(schemaRoute.VillageBatchReviewShares), h.BatchReviewShares)
		r.With(h.AuthRequired).Patch(handler.MountPattern(schemaRoute.VillageBatchReviewSharesDeprecated), h.BatchReviewShares)
		r.With(h.AuthRequired).Patch(handler.MountPattern(schemaRoute.VillageReviewShare), h.ReviewShare)
		r.With(h.AuthRequired).Patch(handler.MountPattern(schemaRoute.VillageReviewShareDeprecated), h.ReviewShare)
		r.With(h.AuthRequired).Delete(handler.MountPattern(schemaRoute.VillageRemoveGroupTranscript), h.RemoveGroupTranscript)
		r.With(h.AuthRequired).Delete(handler.MountPattern(schemaRoute.VillageRemoveGroupTranscriptDeprecated), h.RemoveGroupTranscript)

		// Collective linked repositories (GitHub App; config-gated -> 501 when off).
		// Writes (link/unlink/refresh) are owner-only; reads require membership.
		r.With(h.AuthRequired).Get(handler.MountPattern(schemaRoute.VillageListGroupRepositories), h.ListRepositories)
		r.With(h.AuthRequired).Get(handler.MountPattern(schemaRoute.VillageListGroupRepositoriesDeprecated), h.ListRepositories)
		r.With(h.AuthRequired).Post(handler.MountPattern(schemaRoute.VillageLinkGroupRepository), h.LinkRepository)
		r.With(h.AuthRequired).Post(handler.MountPattern(schemaRoute.VillageLinkGroupRepositoryDeprecated), h.LinkRepository)
		r.With(h.AuthRequired).Delete(handler.MountPattern(schemaRoute.VillageUnlinkGroupRepository), h.UnlinkRepository)
		r.With(h.AuthRequired).Delete(handler.MountPattern(schemaRoute.VillageUnlinkGroupRepositoryDeprecated), h.UnlinkRepository)
		r.With(h.AuthRequired).Get(handler.MountPattern(schemaRoute.VillageListGroupRepositoryCommits), h.ListRepositoryCommits)
		r.With(h.AuthRequired).Get(handler.MountPattern(schemaRoute.VillageListGroupRepositoryCommitsDeprecated), h.ListRepositoryCommits)
		// The repositories the App can offer this collective, for the picker. A
		// literal segment, so chi resolves it ahead of any {owner} pattern.
		r.With(h.AuthRequired).Get(handler.MountPattern(schemaRoute.VillageListAvailableGroupRepositories), h.ListAvailableRepositories)
		r.With(h.AuthRequired).Get(handler.MountPattern(schemaRoute.VillageListAvailableGroupRepositoriesDeprecated), h.ListAvailableRepositories)
		h.RegisterCollectiveBrowseRoutes(r)

		// Tags
		r.Get(handler.MountPattern(schemaRoute.VillageListTags), h.ListTags)
		r.Get(handler.MountPattern(schemaRoute.VillageListPopularTags), h.PopularTags)

		// Annotations
		r.With(h.AuthRequired).Post(handler.MountPattern(schemaRoute.VillagePushAnnotations), h.UploadAnnotations)
		// Server-authoritative skip-gate manifest: the set of content hashes the
		// village holds for the caller, so a push can skip
		// already-stored annotations. Additive, owner-scoped.
		r.With(h.AuthRequired).Get(handler.MountPattern(schemaRoute.VillageGetAnnotationManifest), h.GetAnnotationManifest)

		// Schema
		r.Get(handler.MountPattern(schemaRoute.VillageGetSchemaVersion), h.GetSchemaVersion)

		// Authenticated pull routes let the peasant CLI retrieve transcripts it
		// owns or that are group-shared with it. Deliberately separate from the web
		// read endpoints above
		// (canViewTranscript) so the web policy stays untouched and the pull policy
		// (canPullTranscript — own + group-shared; public + collective-preview
		// EXCLUDED) can diverge. AuthRequired on ALL four routes; every
		// not-pullable outcome is 404 (never 403) so existence never leaks.
		r.With(h.AuthRequired).Get(handler.MountPattern(schemaRoute.VillageListPullableTranscripts), h.ListPullableTranscripts)
		// Batch currency probe (skip-gate): transcript_id-keyed, pull-scoped
		// (non-pullable ids withheld by omission), owner-scoped annotation currency.
		// A static segment, so chi resolves it ahead of /pull/transcripts/{id}.
		r.With(h.AuthRequired).Post(handler.MountPattern(schemaRoute.VillagePullSkipGate), h.PullSkipGate)
		r.With(h.AuthRequired).Get(handler.MountPattern(schemaRoute.VillageGetPullTranscript), h.GetPullTranscript)
		r.With(h.AuthRequired).Get(handler.MountPattern(schemaRoute.VillageGetPullTranscriptContent), h.GetPullTranscriptContent)
		// The stored publish metadata: the owner and the owners of collectives
		// reviewing or holding the transcript only; every other caller gets 404.
		r.With(h.AuthRequired).Get(handler.MountPattern(schemaRoute.VillageGetPullTranscriptPublishMetadata), h.GetPullTranscriptPublishMetadata)
		r.With(h.AuthRequired).Get(handler.MountPattern(schemaRoute.VillageGetPullTranscriptAnnotations), h.GetPullTranscriptAnnotations)

		// Pull request prompt attachment. The served contract declares these
		// seven routes; each answers 501 until its handler lands, so the route
		// drift gate sees contract and server agree now. The webhook is
		// authenticated by GitHub's HMAC over the raw body, not by a session,
		// so it carries no auth middleware. The attachment read is open to
		// anonymous readers of a public repository, so auth is optional there.
		r.Post(handler.MountPattern(schemaRoute.VillageReceiveGitHubWebhook), h.ReceiveGitHubWebhook)
		r.With(h.AuthOptional).Get(handler.MountPattern(schemaRoute.VillageGetPullRequestAttachment), h.GetPullRequestAttachment)
		r.With(h.AuthRequired).Post(handler.MountPattern(schemaRoute.VillageConfirmPullRequestAttachment), h.ConfirmPullRequestAttachment)
		r.With(h.AuthRequired).Delete(handler.MountPattern(schemaRoute.VillageDetachPullRequestAttachment), h.DetachPullRequestAttachment)
		r.With(h.AuthRequired).Get(handler.MountPattern(schemaRoute.VillageListMyPromptRequests), h.ListMyPromptRequests)
		r.With(h.AuthRequired).Get(handler.MountPattern(schemaRoute.VillageGetMySettings), h.GetUserSettings)
		r.With(h.AuthRequired).Patch(handler.MountPattern(schemaRoute.VillageUpdateMySettings), h.UpdateUserSettings)

		// The caller's own invitations, and their notification address. The
		// address is returned only in the caller's own settings.
		r.With(h.AuthRequired).Get(handler.MountPattern(schemaRoute.VillageListMyInvitations), h.ListMyInvitations)
		r.With(h.AuthRequired).Post(handler.MountPattern(schemaRoute.VillageAcceptMyInvitation), h.AcceptMyInvitation)
		r.With(h.AuthRequired).Post(handler.MountPattern(schemaRoute.VillageDeclineMyInvitation), h.DeclineMyInvitation)
		r.With(h.AuthRequired).Put(handler.MountPattern(schemaRoute.VillageSetMyNotificationEmail), h.SetMyNotificationEmail)
		r.With(h.AuthRequired).Delete(handler.MountPattern(schemaRoute.VillageRemoveMyNotificationEmail), h.RemoveMyNotificationEmail)
		r.With(h.AuthRequired).Post(handler.MountPattern(schemaRoute.VillageVerifyMyNotificationEmail), h.VerifyMyNotificationEmail)
		// The one-click unsubscribe target works signed out: the signed token
		// it carries is the authority, and it can only turn invitation emails
		// off. Rate-limited per IP.
		r.With(unsubscribeLimiter).Post(handler.MountPattern(schemaRoute.VillageUnsubscribeInvitationEmails), h.UnsubscribeInvitationEmails)
		// A transcript's pull requests follow the transcript's own read: open to
		// an anonymous reader of a public transcript, 404 for anyone who may not
		// read it. The caller's totals are their own, so they need a session.
		r.With(h.AuthOptional).Get(handler.MountPattern(schemaRoute.VillageListTranscriptPullRequests), h.ListTranscriptPullRequests)
		r.With(h.AuthRequired).Get(handler.MountPattern(schemaRoute.VillageGetMyStats), h.GetMyStats)

		// GitHub App install and callback both require a session.
		r.With(h.AuthRequired).Get(handler.MountPattern(schemaRoute.VillageStartGitHubAppInstall), h.GitHubInstall)
		r.With(h.AuthRequired).Get(handler.MountPattern(schemaRoute.VillageCompleteGitHubAppInstall), h.GitHubInstallCallback)
	})

	ConfigureHealthRoutes(r, h)
	return r, h
}

// ConfigureHealthRoutes derives health capabilities from the completed router.
// Call during composition, before serving requests.
func ConfigureHealthRoutes(routes chi.Routes, h *handler.Handler) {
	directory, segment, window := false, false, false
	_ = chi.Walk(routes, func(method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if method != http.MethodGet {
			return nil
		}
		switch pattern {
		case schemaRoute.VillageGetTranscriptSegments.Pattern():
			directory = true
		case schemaRoute.VillageGetTranscriptSegment.Pattern():
			segment = true
		case schemaRoute.VillageGetTranscriptWindow.Pattern():
			window = true
		}
		return nil
	})
	h.SetSegmentedReadsMounted(directory && segment && window)
}
