# Environment variables

This is the one reference for Village's `.env`: what each variable does, which
ones you actually need for local development, and how to obtain each value.
The server reads `.env` from the repository root. Start from the template:

```sh
cp .env.example .env
```

## What you actually need, by workflow

| Workflow | What you must set |
| --- | --- |
| Backend-only development: `make backend-dev` | Nothing. The target generates a worktree-local KEK and JWT secret and starts PostgreSQL, MinIO, and the hot-reloading backend. |
| Backend tests: `make backend-encrypted-test` | Nothing. The target provisions disposable PostgreSQL and MinIO. |
| Full stack in Docker: `make up` / `make dev` | `JWT_SECRET`, a real `TRANSCRIPT_KEK_KEYRING` value, and one sign-in provider pair (GitHub is the default) if you need to log in. Everything else has a working local default. |
| Production | Everything in the Required table, with real values. See `docs/railway-cloudflare-r2-activation.md`. |

Anonymous browsing works with no sign-in provider configured. A provider whose
ID or secret is missing returns `503 "<provider> sign-in is not configured"` on
its login route and nothing else breaks.

## Required for the full local stack

| Variable | Local default | Purpose | How to obtain |
| --- | --- | --- | --- |
| `JWT_SECRET` | none — startup fails | Signs session tokens. Must be 32+ characters and not a known-weak value (the template's `change-me-in-production` is rejected). | `openssl rand -base64 32` |
| `TRANSCRIPT_KEK_ACTIVE_VERSION` | `1` | Selects the active key in the keyring. | Keep `1` until you rotate. |
| `TRANSCRIPT_KEK_KEYRING` | placeholder — blob startup fails | JSON keyring of base64 key-encryption keys for encrypted transcript storage. | `openssl rand -base64 32`, then `{"1":"<value>"}`. |
| `DATABASE_URL` | compose PostgreSQL (`peasant:peasant@localhost:5432/peasant`) | PostgreSQL connection string. | Default works with `make up`. |
| `S3_ENDPOINT`, `S3_BUCKET`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`, `S3_USE_PATH_STYLE` | compose MinIO (`http://localhost:9000`, `peasant-transcripts`, `minioadmin`/`minioadmin`, `true`) | Object storage for encrypted transcript blobs. | Defaults work with `make up`. Production values come from R2 (see the activation runbook). |
| `PORT`, `BASE_URL`, `FRONTEND_URL` | `8080`, `https://localhost`, `https://localhost` | Listener port and the public URLs used for OAuth callbacks and links. | Defaults work locally. |
| `NEXT_PUBLIC_API_URL` | `https://localhost/api/v1` | The API base the Next.js frontend calls. | Default works with the compose Caddy setup. |

## Sign-in providers (each pair optional; unset = that provider returns 503)

Every callback URL is `<BASE_URL>/api/v1/auth/<provider>/callback`. Locally,
`<BASE_URL>` through Caddy is `https://localhost:8443`.

| Variables | Provider | Register at | Scopes |
| --- | --- | --- | --- |
| `GITHUB_CLIENT_ID` / `GITHUB_CLIENT_SECRET` | GitHub (the default provider; the template ships a client ID for local use, you supply the secret) | GitHub → Settings → Developer settings → OAuth Apps | default |
| `GITLAB_CLIENT_ID` / `GITLAB_CLIENT_SECRET` | GitLab | <https://gitlab.com/-/user_settings/applications> | `read_user` |
| `HUGGINGFACE_CLIENT_ID` / `HUGGINGFACE_CLIENT_SECRET` | Hugging Face | <https://huggingface.co/settings/applications/new> | `openid profile` |
| `CODEBERG_CLIENT_ID` / `CODEBERG_CLIENT_SECRET` | Codeberg (Forgejo/Gitea) | <https://codeberg.org/user/settings/applications> | `openid profile` |
| `SOURCEHUT_CLIENT_ID` / `SOURCEHUT_CLIENT_SECRET` | Sourcehut. Two quirks; follow [oauth-app-registration.md](oauth-app-registration.md). | <https://meta.sr.ht/oauth2> | `meta.sr.ht/PROFILE:RO` |

## Optional features

| Variables | Feature | When unset |
| --- | --- | --- |
| `GITHUB_APP_ID` / `GITHUB_APP_PRIVATE_KEY` | Collective repository linking and the commit overlay (a GitHub App, not the OAuth app: Contents read-only, Metadata read-only, installable on any account). The key accepts a multi-line PEM or one line with `\n` escapes. | The feature's endpoints return `501`; everything else works. |

## Failure behavior summary

| Missing or invalid | Effect |
| --- | --- |
| `JWT_SECRET` empty, short, or known-weak | The server refuses to start, with an actionable error. |
| `TRANSCRIPT_KEK_*` placeholder or malformed | Any mode that touches blob storage refuses to start. |
| `S3_*` incomplete | Same as above. |
| One OAuth pair incomplete | That provider's sign-in returns `503`; other providers and anonymous browsing are unaffected. |
| GitHub App pair incomplete | Repository-linking endpoints return `501`. |
