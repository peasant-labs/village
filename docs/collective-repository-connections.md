# Design: Connecting repositories to a collective

**Status:** Design only — not implemented. This doc scopes the work to let a
collective link real GitHub repositories so we can overlay its transcripts onto
a repo's commit/PR timeline — replicating what the local peasant app gets for
free from the working tree.

**Context / what already exists.** Pushed transcripts already carry git context
in their payload (`schema.GitContext`: `branch`, `remote`, `worktree`,
`tracking`, and — `v4+` — a `commits []CommitInfo` array with full SHAs, author
email, and commit time). Village persists `git_branch`, `git_remote`, and
`git_worktree` per transcript (`002_transcript_metadata_v2.up.sql`) and the
collective page now groups transcripts by `git_remote` client-side (the
"Repositories" view, Tier 1). What's missing to build a true commit-timeline
overlay is (a) the **commit SHAs** are dropped on ingest, and (b) we have no
**authenticated link** to the live repo to fetch its commits/PRs. This doc
covers both.

> Prerequisite & main blocker: a registered GitHub App plus its secrets is a
> human setup step (see [Auth mechanism](#auth-mechanism)). Nothing here ships
> until that exists.

---

## Auth mechanism

We need read access to a repo's commits and pull requests. Three options:

| Option | How it works | Pros | Cons |
| --- | --- | --- | --- |
| **GitHub App** (installation token) | Org/owner installs the app on selected repos; backend mints short-lived installation tokens from a private key (JWT → installation access token). | Fine-grained per-repo scope; org-admin consent model maps cleanly to "a collective owns repos"; 5,000 req/hr **per installation** (rate limit scales with adoption); tokens are short-lived (1h) so a leak is bounded; can subscribe to webhooks. | Requires registering one GitHub App + storing a private key (human setup); install flow is an extra step for repo owners. |
| **Personal access token (PAT)** | A collective owner pastes a fine-grained PAT. | Trivial to implement; no app registration. | Long-lived high-value secret in our DB; scoped to a *person*, not the collective (breaks when they leave / rotate); shared 5,000 req/hr across the whole token; no webhooks; users routinely over-scope PATs. |
| **OAuth (user) token** | Reuse the existing OAuth login to act as the signed-in user. | We already have GitHub OAuth wiring for sign-in. | Acts as a *user*, not an installation — same person-coupling problem as PATs; our current OAuth scope is identity-only (`read:user`), so we'd have to widen consent for *every* user just to let *some* link repos; rate limit is per-user. |

**Recommendation: GitHub App (installation tokens).** It is the only option
where the credential belongs to the *repository/org* rather than to an
individual, which matches the collective ownership model and survives member
churn. It gives per-repo scoping, short-lived tokens, higher aggregate rate
limits, and a webhook path for incremental sync. The cost is the one-time human
setup of registering the app and provisioning its secrets.

### Human setup (the blocker)

1. Register a **GitHub App** ("Village") under the peasant-labs org with:
   - **Permissions (read-only):** Repository → *Contents: Read*, *Metadata:
     Read*, *Pull requests: Read*. No write scopes.
   - **Callback URL:** `<BASE_URL>/api/v1/integrations/github/callback`
     (mirrors the existing OAuth callback convention in
     `auth_providers.go` / `docs/oauth-app-registration.md`).
   - **Setup URL** (post-install redirect) → the collective settings page.
   - **Webhook URL:** `<BASE_URL>/api/v1/integrations/github/webhook` with a
     webhook secret; events: `push`, `pull_request`. (Optional for v1 — polling
     works without it.)
2. Generate the App's **private key** (`.pem`) and note the **App ID** and
   **webhook secret**.
3. Provision secrets (same pattern as the OAuth apps — env vars passed through
   `docker-compose.yml`, real values in the prod secret store):
   ```env
   GITHUB_APP_ID=...
   GITHUB_APP_PRIVATE_KEY=...        # PEM, base64 or file-mounted
   GITHUB_APP_WEBHOOK_SECRET=...
   GITHUB_APP_CLIENT_ID=...          # for the install/identify handshake
   GITHUB_APP_CLIENT_SECRET=...
   ```
   The backend treats the integration as "configured" only when `GITHUB_APP_ID`
   and the private key are both present (otherwise the "Link repository" button
   503s, exactly like an unconfigured OAuth provider).

---

## Storage

Two new Postgres tables, following existing migration conventions
(`backend/internal/database/migrations/0NN_*.up.sql` + a matching `.down.sql`,
sqlc queries in `backend/internal/database/queries/`).

### `github_installations` — one row per GitHub App installation

```sql
CREATE TABLE github_installations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    installation_id BIGINT UNIQUE NOT NULL,   -- GitHub's installation id
    account_login   VARCHAR(255) NOT NULL,    -- org/user the app is installed on
    account_type    VARCHAR(20)  NOT NULL,    -- 'Organization' | 'User'
    installed_by    UUID REFERENCES users(id),
    created_at      TIMESTAMPTZ DEFAULT now(),
    suspended_at    TIMESTAMPTZ              -- set when GitHub suspends/uninstalls
);
```

### `collective_repositories` — links a collective to a specific repo

```sql
CREATE TABLE collective_repositories (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id            UUID REFERENCES groups(id) ON DELETE CASCADE NOT NULL,
    installation_id     BIGINT REFERENCES github_installations(installation_id)
                            ON DELETE SET NULL,
    repo_owner          VARCHAR(255) NOT NULL,   -- "peasant-labs"
    repo_name           VARCHAR(255) NOT NULL,   -- "village"
    repo_remote         TEXT NOT NULL,           -- normalized https remote, the join key to transcripts.git_remote
    github_repo_id      BIGINT,                  -- stable id (survives rename)
    default_branch      VARCHAR(255),
    linked_by           UUID REFERENCES users(id),
    last_synced_at      TIMESTAMPTZ,
    created_at          TIMESTAMPTZ DEFAULT now(),
    UNIQUE (group_id, repo_remote)
);
CREATE INDEX idx_collective_repositories_group ON collective_repositories(group_id);
CREATE INDEX idx_collective_repositories_remote ON collective_repositories(lower(repo_remote));
```

`repo_remote` is the bridge: it is normalized the same way Tier 1's
`remoteHref`/`extractRepoName` and the backend `extractRepoName` normalize a
remote, so it joins directly against `transcripts.git_remote` for the repos a
collective already has transcripts in.

### Cached commit/PR data

```sql
CREATE TABLE repository_commits (
    repo_id      UUID REFERENCES collective_repositories(id) ON DELETE CASCADE,
    sha          VARCHAR(40) NOT NULL,
    message      TEXT,
    author_login VARCHAR(255),
    authored_at  TIMESTAMPTZ,
    branch       VARCHAR(255),
    pr_number    INTEGER,                 -- nullable; set when associated to a PR
    PRIMARY KEY (repo_id, sha)
);

CREATE TABLE repository_pulls (
    repo_id    UUID REFERENCES collective_repositories(id) ON DELETE CASCADE,
    number     INTEGER NOT NULL,
    title      TEXT,
    state      VARCHAR(20),              -- open | closed | merged
    merged_sha VARCHAR(40),
    author     VARCHAR(255),
    opened_at  TIMESTAMPTZ,
    merged_at  TIMESTAMPTZ,
    PRIMARY KEY (repo_id, number)
);
```

### Secret handling

- The App **private key** and **webhook secret** are *deployment* secrets (env
  / prod secret store), never per-row in Postgres — same model as the OAuth
  client secrets today.
- We store only the **installation id** (a non-secret opaque integer), never an
  installation token. Tokens are minted on demand (App JWT → installation
  token, ~1h TTL), held in memory/short-lived cache, and never persisted.
- This keeps the DB free of long-lived high-value credentials; a DB compromise
  yields no usable GitHub access.

---

## Data + sync

### What to fetch

For each linked repo, the **default branch's commits** plus **pull requests**
(number, title, state, merge SHA, timestamps). v1 can scope to the default
branch; later expand to the branches that actually appear in transcripts'
`git_branch`.

### Capturing transcript commit SHAs (small ingest change, prerequisite)

The overlay's whole value is matching transcripts to commits. The peasant
payload already sends `git.commits[].hash`, but the publish mapper
(`schema_mapper.go`, ~line 39) drops them. Add a `transcript_commits` join
table populated on publish from `req.Git.Commits`:

```sql
CREATE TABLE transcript_commits (
    transcript_id UUID REFERENCES transcripts(id) ON DELETE CASCADE,
    sha           VARCHAR(40) NOT NULL,
    author_email  VARCHAR(320),
    committed_at  TIMESTAMPTZ,
    PRIMARY KEY (transcript_id, sha)
);
```

Then a transcript links to a commit by `transcript_commits.sha =
repository_commits.sha` for the repo whose `repo_remote = transcript.git_remote`.
SHAs may be abbreviated in the payload, so match on a SHA prefix (>= 7 chars)
and store the full SHA from the GitHub side as canonical.

### Caching + rate limits

- **Cache-first:** the overlay reads from `repository_commits` /
  `repository_pulls`, never live GitHub, so the UI is fast and resilient to
  GitHub outages.
- **Incremental sync:** persist `last_synced_at` per repo; poll
  `GET /repos/{o}/{r}/commits?since=<last_synced_at>` and the PRs list, upserting
  by `(repo_id, sha)` / `(repo_id, number)`. Conditional requests (`ETag` /
  `If-None-Match`) make unchanged polls free (a `304` doesn't count against the
  rate limit).
- **Rate-limit strategy:** installation tokens give 5,000 req/hr *per
  installation*, so the budget scales with adoption rather than being a single
  global ceiling. Read `X-RateLimit-Remaining`; on `403`/`429` back off using
  `Retry-After` / `X-RateLimit-Reset`. A single background sync worker with a
  per-installation token bucket keeps us well under budget.
- **Webhooks (optional, preferred at scale):** subscribing to `push` and
  `pull_request` lets us sync within seconds and drop most polling. Validate
  the `X-Hub-Signature-256` HMAC against `GITHUB_APP_WEBHOOK_SECRET`. Poll
  remains the fallback when webhooks aren't configured.

### Backend shape

New handlers mounted under the existing `/api/v1` group routes
(`router/routes.go`), e.g.:
- `POST /groups/{id}/repositories` — link a repo (owner-only).
- `GET  /groups/{id}/repositories` — list linked repos + sync status.
- `DELETE /groups/{id}/repositories/{repoID}` — unlink.
- `GET  /groups/{id}/repositories/{repoID}/timeline` — merged commits + PRs +
  the transcripts that touched each commit.
- `GET  /integrations/github/install` + `/callback` — App install handshake.
- `POST /integrations/github/webhook` — event ingestion (HMAC-verified).

A `internal/github` client package wraps JWT minting, token caching, and the
REST calls, mirroring how `internal/auth`/`oauth.go` isolate provider HTTP.

---

## UI

### "Linked repositories" in collective settings

Add a section to `frontend/src/app/groups/[id]/settings/page.tsx` (owner-only):

- **Connect GitHub** button → kicks off the App install flow when no
  installation covers the collective's org; otherwise lists installable repos.
- A table of linked repos: repo name/remote, default branch, last-synced time,
  a "Sync now" affordance, and unlink. Reuse the page's existing bordered
  `border-rule`/`bg-surface` primitives and the `Badge`/`Button` components for
  visual consistency with Tier 1's Repositories view.
- Empty/unconfigured state mirrors the OAuth-not-configured pattern: if the
  GitHub App isn't provisioned server-side, the button is disabled with a
  "GitHub integration not configured" note.

### Commit-timeline overlay (sketch)

On the collective detail page, when a repo group (Tier 1) is backed by a linked
repository, the repo card gains a **Timeline** tab:

```
repo: village                                         default: develop
 ──●────────●──────────●─────────────●────────────●──▶  (commits, left→right by date)
   │        │          │             │            │
  a1b2c3   d4e5f6     11aa22        88bb99        ff00cc
   example PR A   (2 transcripts)    example PR B  (1 transcript)
             ▲ click → transcript list
```

The labels and abbreviated hashes in this sketch are synthetic examples; they
do not refer to Village pull requests or repository history.

- Horizontal axis = time; markers = commits on the default branch, with PR
  bands spanning their commit range.
- A commit marker is **highlighted** when one or more transcripts reference its
  SHA (via `transcript_commits` ↔ `repository_commits`), and clicking it reveals
  those transcripts (linking to `/transcripts/{id}`) — the village analog of the
  local app's "this session produced these commits."
- Commits with no associated transcript render muted, so the overlay reads as
  "where did collective work land in the repo's history."
- Recharts is already a dependency (via `@peasant-labs/analytics`), so the
  timeline can be a thin custom chart or extend the shared package's
  `ChartCard`.

---

## Open decisions (for the user / team)

1. **App registration owner & scope.** Register the GitHub App under the
   `peasant-labs` org? Marketplace-public or private/internal only? (Public
   needs branding/screenshots review.)
2. **Who may link a repo.** Collective owner only, or any member with the right
   org role? Must the linker be an admin of the GitHub org, or do we trust the
   installation's repo selection alone?
3. **Private repos.** Support linking private repos at all? If yes, the overlay
   leaks commit messages/PR titles to anyone who can read the collective — is
   that acceptable, or should private-repo overlays be members-only regardless
   of `data_access`?
4. **Commit-SHA backfill.** The ingest change captures SHAs going forward.
   Backfill historical transcripts (re-read stored blobs for `git.commits`) or
   only enrich new pushes?
5. **Webhooks vs polling for v1.** Ship polling-only first (no webhook secret
   needed) and add webhooks later, or set up webhooks from day one?
6. **Non-GitHub remotes.** Transcripts already carry GitLab/Codeberg/Bitbucket
   remotes (those providers exist for sign-in). Is GitHub-only acceptable for
   v1, with the schema left provider-agnostic for later?
7. **Author attribution & privacy.** Commit `authorEmail` can deanonymize a
   contributor who opted out of discoverability. Do we match transcripts to
   commits by SHA only (safe) and never surface commit author emails in the UI?
