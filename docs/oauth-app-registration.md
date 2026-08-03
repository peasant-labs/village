# Registering the Sourcehut OAuth app

Exact steps to register Village's **Sourcehut** (`meta.sr.ht`) OAuth app and wire
it into `.env`. The other providers (GitHub, GitLab, Hugging Face, Codeberg) are
already configured — Sourcehut is the only one left.

## How the callback is built

The backend builds the callback from `BASE_URL` at runtime
(`auth_providers.go`: `fmt.Sprintf("%s/api/v1/auth/%s/callback", BaseURL, provider)`),
so the redirect URI differs per environment:

| Environment | `BASE_URL`                          | Sourcehut callback URL                                           |
| ----------- | ----------------------------------- | ---------------------------------------------------------------- |
| Local       | `https://localhost:8443` (compose)  | `https://localhost:8443/api/v1/auth/sourcehut/callback`          |
| Prod        | `https://village.peasantlabs.org`   | `https://village.peasantlabs.org/api/v1/auth/sourcehut/callback` |

> ⚠️ Match the path **exactly**: the `/api/v1/auth/` prefix, the slug
> `sourcehut` (one word), and a trailing `/callback` with no trailing slash.

## Sourcehut specifics

- **OIDC-ish OAuth2**, but with two quirks the code already handles (`oauth.go`):
  - **The redirect URI is baked into the app registration and is NOT sent on the
    authorize URL** — so the URL you register on the form is authoritative.
  - Scope is the literal grant string **`meta.sr.ht/PROFILE:RO`** — don't
    "simplify" it; that's the exact grant the GraphQL `me` query needs.
- Profile is read via a GraphQL `me` query at `https://meta.sr.ht/query`.
- **meta.sr.ht allows only ONE redirect URI per client**, so local and prod need
  **two separate apps** with **two separate ID/secret pairs**. (You can skip the
  local app if you don't need sr.ht sign-in locally.)

## Register the app(s)

1. Go to **https://meta.sr.ht/oauth2** (logged in) → **Register a new OAuth client**.
2. Register the **prod** client:
   - **Redirect URI:** `https://village.peasantlabs.org/api/v1/auth/sourcehut/callback`
   - **Client name / URL:** `Village` / optional.
   - Submit → copy the one-time **client ID** (UUID) and **client secret**.
3. (Optional) Register a **second** client for local:
   - **Redirect URI:** `https://localhost:8443/api/v1/auth/sourcehut/callback`
   - Submit → copy its **client ID** and **client secret**.

## Wire up `.env` and restart

Set the matching pair per environment (both ID **and** secret are required, or
the route returns `503 "sourcehut sign-in is not configured"`):

```env
# local .env  →  use the LOCAL app's credentials
SOURCEHUT_CLIENT_ID=<local client UUID>
SOURCEHUT_CLIENT_SECRET=<local client secret>

# prod environment  →  use the PROD app's credentials
SOURCEHUT_CLIENT_ID=<prod client UUID>
SOURCEHUT_CLIENT_SECRET=<prod client secret>
```

Then restart the backend so it re-reads env (frontend doesn't need it):

```sh
docker compose restart backend
```

Prod also needs `BASE_URL=https://village.peasantlabs.org` and
`FRONTEND_URL=https://village.peasantlabs.org` set, since the backend builds the
callback from `BASE_URL` and bounces back to `FRONTEND_URL` after login.

## Smoke test

At `https://localhost:8443/` → sign-in chevron dropdown → **Sign in with
SourceHut** (accept the self-signed cert). A correct flow ends at
`/auth/callback?token=...`. On first sign-in you're sent to **`/welcome`** to
choose a username, then into the app.

## Known gaps for sr.ht

- The `me` query doesn't expose an avatar, so `avatar_url` stays empty.
- CLI login is still GitHub-only, and `user_github_orgs` is empty for non-GitHub
  users, so organization filters are a no-op for those accounts.

## Troubleshooting

| Symptom | Likely cause |
| --- | --- |
| `503 "sourcehut sign-in is not configured"` | `SOURCEHUT_CLIENT_ID`/`SECRET` blank, or backend not restarted. |
| `Invalid OAuth state` | Stale tab / back-forward navigation — retry from the sign-in button. |
| `redirect_uri` / callback mismatch | Registered URI ≠ `<BASE_URL>/api/v1/auth/sourcehut/callback`. Remember sr.ht uses the **registered** URI, not a passed parameter. |
| `OAuth exchange failed` | Wrong client secret, or using the local app's credentials against prod (or vice versa). |
| `Sourcehut returned an empty profile` | Missing the `meta.sr.ht/PROFILE:RO` scope on the client. |
