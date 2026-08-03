package router

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/redact"
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
)

func New(cfg *config.Config, pool *pgxpool.Pool, blobs storage.TranscriptBlobStore, titles *redact.TitlePipeline) http.Handler {
	r := chi.NewRouter()

	r.Use(chimw.RequestID)
	r.Use(middleware.Logger)
	r.Use(chimw.Recoverer)
	r.Use(middleware.CORS(cfg.FrontendURL))

	h := handler.NewWithTitlePipeline(cfg, pool, blobs, titles)

	// authLimiter enforces a strict per-IP rate limit on OAuth entry points to
	// deter abuse without constraining authenticated API traffic.
	authLimiter := httprate.LimitByIP(authRateLimitRequests, authRateLimitWindow)

	r.Get("/health", h.Health)

	r.Route("/api/v1", func(r chi.Router) {
		// Village API OpenAPI spec, served from the contract module
		// (schema.VillageAPISpecJSON) for client/tooling discovery; the publish path
		// enforces the same module's PublishRequest schema.
		r.Get("/openapi.json", h.ServeOpenAPI)
		// Auth (rate-limited entry points)
		r.With(authLimiter).Get("/auth/github", h.GitHubLogin)
		r.Get("/auth/github/callback", h.GitHubCallback)
		r.With(authLimiter).Get("/auth/gitlab", h.GitLabLogin)
		r.Get("/auth/gitlab/callback", h.GitLabCallback)
		r.With(authLimiter).Get("/auth/huggingface", h.HuggingFaceLogin)
		r.Get("/auth/huggingface/callback", h.HuggingFaceCallback)
		r.With(authLimiter).Get("/auth/codeberg", h.CodebergLogin)
		r.Get("/auth/codeberg/callback", h.CodebergCallback)
		r.With(authLimiter).Get("/auth/sourcehut", h.SourceHutLogin)
		r.Get("/auth/sourcehut/callback", h.SourceHutCallback)
		r.With(authLimiter).Get("/auth/cli/login", h.CLILogin)
		r.With(authLimiter).Post("/auth/cli/exchange", h.CLIExchange)
		r.Post("/auth/logout", h.Logout)
		r.With(h.AuthRequired).Get("/auth/me", h.Me)
		r.With(h.AuthRequired).Delete("/auth/me", h.DeleteAccount)
		r.With(h.AuthRequired).Patch("/auth/me/settings", h.UpdateMySettings)
		r.With(h.AuthRequired).Patch("/auth/me/username", h.SetMyUsername)

		// API keys
		r.With(h.AuthRequired).Post("/auth/api-keys", h.CreateAPIKey)
		r.With(h.AuthRequired).Get("/auth/api-keys", h.ListAPIKeys)
		r.With(h.AuthRequired).Delete("/auth/api-keys/{id}", h.RevokeAPIKey)

		// User public profiles
		r.With(h.AuthOptional).Get("/users/{username}", h.GetUserPublicProfile)
		r.With(h.AuthOptional).Get("/users/{username}/orgs", h.ListUserPublicOrgs)

		// Authenticated user org management
		r.With(h.AuthRequired).Get("/auth/orgs", h.ListMyOrgs)
		r.With(h.AuthRequired).Patch("/auth/orgs/{orgLogin}/visibility", h.SetOrgVisibility)

		// Organizations (aggregated GitHub org affiliations)
		r.With(h.AuthOptional).Get("/orgs/search", h.SearchOrgs)
		r.With(h.AuthOptional).Get("/orgs/{login}", h.GetOrg)

		// Transcripts
		r.With(h.AuthRequired).Post("/transcripts/publish", h.PublishTranscript)
		r.With(h.AuthRequired).Post("/transcripts/publish/batch", h.PublishBatch)
		r.With(h.AuthOptional).Get("/transcripts", h.ListTranscripts)
		r.With(h.AuthOptional).Get("/transcripts/{id}", h.GetTranscript)
		r.With(h.AuthOptional).Get("/transcripts/{id}/content", h.GetTranscriptContent)
		r.With(h.AuthRequired).Patch("/transcripts/{id}", h.UpdateTranscript)
		r.With(h.AuthRequired).Delete("/transcripts/{id}", h.DeleteTranscript)
		r.With(h.AuthRequired).Patch("/users/me/projects/rename", h.RenameUserProject)
		r.With(h.AuthRequired).Post("/transcripts/{id}/share", h.ShareTranscript)
		r.With(h.AuthRequired).Delete("/transcripts/{id}/share/{groupID}", h.UnshareTranscript)

		// Attestations
		r.With(h.AuthOptional).Get("/transcripts/{id}/attestations", h.ListTranscriptAttestations)
		r.With(h.AuthRequired).Post("/transcripts/{id}/attestations", h.CreateAttestation)
		r.With(h.AuthRequired).Delete("/transcripts/{id}/attestations/{attestationID}", h.DeleteAttestation)

		// Transcript annotations (read pushed labels; create manual per-turn labels)
		r.With(h.AuthOptional).Get("/transcripts/{id}/annotations", h.ListTranscriptAnnotations)
		r.With(h.AuthRequired).Post("/transcripts/{id}/annotations", h.CreateTranscriptAnnotation)

		// Transcript commits (read persisted git commits for the timeline overlay)
		r.With(h.AuthOptional).Get("/transcripts/{id}/commits", h.ListTranscriptCommits)

		// Groups
		r.Get("/groups/public", h.ListPublicGroups)
		r.With(h.AuthOptional).Get("/groups/search", h.SearchCollectives)
		r.With(h.AuthRequired).Post("/groups", h.CreateGroup)
		r.With(h.AuthRequired).Get("/groups", h.ListGroups)
		r.With(h.AuthOptional).Get("/groups/{id}", h.GetGroup)
		r.With(h.AuthRequired).Patch("/groups/{id}", h.UpdateGroup)
		r.With(h.AuthRequired).Delete("/groups/{id}", h.DeleteGroup)
		r.With(h.AuthRequired).Post("/groups/{id}/join", h.JoinGroup)
		r.With(h.AuthRequired).Post("/groups/{id}/members", h.AddGroupMember)
		r.With(h.AuthRequired).Patch("/groups/{id}/members/{userID}/role", h.PromoteMember)
		r.With(h.AuthRequired).Delete("/groups/{id}/members/{userID}", h.RemoveGroupMember)
		r.With(h.AuthRequired).Get("/groups/{id}/pending", h.ListPendingShares)
		r.With(h.AuthRequired).Get("/groups/{id}/my-shares", h.ListMyGroupShares)
		r.With(h.AuthRequired).Patch("/groups/{id}/shares/{transcriptID}", h.ReviewShare)
		r.With(h.AuthRequired).Delete("/groups/{id}/transcripts/{transcriptID}", h.RemoveGroupTranscript)

		// Collective linked repositories (GitHub App; config-gated -> 501 when off).
		// Writes (link/unlink/refresh) are owner-only; reads require membership.
		r.With(h.AuthRequired).Get("/groups/{id}/repositories", h.ListRepositories)
		r.With(h.AuthRequired).Post("/groups/{id}/repositories", h.LinkRepository)
		r.With(h.AuthRequired).Delete("/groups/{id}/repositories/{owner}/{name}", h.UnlinkRepository)
		r.With(h.AuthRequired).Get("/groups/{id}/repositories/{owner}/{name}/commits", h.ListRepositoryCommits)

		// Tags
		r.Get("/tags", h.ListTags)
		r.Get("/tags/popular", h.PopularTags)

		// Annotations
		r.With(h.AuthRequired).Post("/annotations", h.UploadAnnotations)
		// Server-authoritative skip-gate manifest: the set of content hashes the
		// village holds for the caller, so a push can skip
		// already-stored annotations. Additive, owner-scoped.
		r.With(h.AuthRequired).Get("/annotations/manifest", h.GetAnnotationManifest)

		// Schema
		r.Get("/schema/version", h.GetSchemaVersion)

		// Authenticated pull routes let the peasant CLI retrieve transcripts it
		// owns or that are group-shared with it. Deliberately separate from the web
		// read endpoints above
		// (canViewTranscript) so the web policy stays untouched and the pull policy
		// (canPullTranscript — own + group-shared; public + collective-preview
		// EXCLUDED) can diverge. AuthRequired on ALL four routes; every
		// not-pullable outcome is 404 (never 403) so existence never leaks.
		r.With(h.AuthRequired).Get("/pull/transcripts", h.ListPullableTranscripts)
		// Batch currency probe (skip-gate): transcript_id-keyed, pull-scoped
		// (non-pullable ids withheld by omission), owner-scoped annotation currency.
		// A static segment, so chi resolves it ahead of /pull/transcripts/{id}.
		r.With(h.AuthRequired).Post("/pull/transcripts/skip-gate", h.PullSkipGate)
		r.With(h.AuthRequired).Get("/pull/transcripts/{id}", h.GetPullTranscript)
		r.With(h.AuthRequired).Get("/pull/transcripts/{id}/content", h.GetPullTranscriptContent)
		r.With(h.AuthRequired).Get("/pull/transcripts/{id}/annotations", h.GetPullTranscriptAnnotations)
	})

	return r
}
