# Peasant Village — Frontend

Next.js App Router frontend for the Peasant Village.

## Development

From the repo root, the easiest way to run everything together is:

```sh
make dev
```

To run only the frontend:

```sh
pnpm install
pnpm dev
```

This is a single-member pnpm workspace (see `../pnpm-workspace.yaml`):
`@peasant-labs/fairtrade` resolves from the registry (the pinned range in
`package.json`), so a plain
`pnpm install` works from any checkout — no sibling worktrees, no prebuild
step. To pick up a design-system change: publish/bump fairtrade, update the
range, and re-run `pnpm install`.

The dev server starts at `http://localhost:3000`. It expects the Go backend at `http://localhost:8080`.

## Build

```sh
pnpm build
```

## Railway deployment

The production image uses the Village repository root as its complete build
context. Configure the Railway frontend service with:

- **Root Directory:** keep this at the Village repository root (`.`).
- **Variable:** `RAILWAY_DOCKERFILE_PATH=/frontend/Dockerfile`
- **Build variable:** `NEXT_PUBLIC_API_URL=https://<api-origin>/api/v1`
- **Source:** connect the service to GitHub so Railway supplies `RAILWAY_GIT_COMMIT_SHA`.

`NEXT_PUBLIC_API_URL` is public configuration compiled into browser JavaScript,
not a secret or a runtime-only setting. Changing it requires a fresh frontend
build. The Docker build intentionally fails when it is absent.

`RAILWAY_GIT_COMMIT_SHA` must be a full 40-character lowercase revision. Railway
provides it automatically for GitHub-triggered builds; local builds pass the same
named build argument explicitly. The final image prints and records it as
`org.opencontainers.image.revision` and as the non-secret runtime value
`VILLAGE_BUILD_REVISION`. This makes local and live-container artifact inspection
use the same provenance value; it does not replace Railway's immutable image digest
and rollout checks. Do not create a custom `VCS_REF` variable.

Configure the Railway API/backend service with these runtime variables, then
restart or redeploy it:

- `FRONTEND_URL=https://<frontend-origin>`
- `BASE_URL=https://<api-origin>`

Both backend values must be origins only: scheme and public host, with no
`/api/v1`, credentials, trailing application path, localhost, container name,
or Railway private hostname. `BASE_URL` must be the origin that prefixes
`NEXT_PUBLIC_API_URL`.

For every enabled OAuth provider, register this callback URL in the provider's
dashboard:

```text
https://<api-origin>/api/v1/auth/<provider>/callback
```

The provider callback belongs to the API. After exchanging credentials, the API
redirects the browser to `https://<frontend-origin>/auth/callback`; do not
register that frontend URL as the provider callback.

### Deployment validation checklist

1. Before the frozen pnpm install begins, confirm the deployment log names
   `frontend/Dockerfile` as the selected Dockerfile. Then confirm the log shows
   the frozen install, the Next build, and final image creation from the
   repository root. Do not use `frontend/Dockerfile.dev` for Railway. Inspect
   the final image's `org.opencontainers.image.revision` label and require it to
   equal the intended full revision.
2. Load the final frontend in a browser and inspect a real API request. Its URL
   must start with the configured `NEXT_PUBLIC_API_URL`.
3. In browser DevTools, confirm the API response has
   `Access-Control-Allow-Origin: https://<frontend-origin>` and
   `Access-Control-Allow-Credentials: true`, with no CORS error. Check the same
   headers on any `OPTIONS` preflight.
4. Complete sign-in through every enabled OAuth provider using a validation
   account. Confirm the provider returns to the API callback above and the API
   then redirects to the final frontend `/auth/callback` route. Never copy the
   callback token into logs, screenshots, tickets, or shell history.
5. If no OAuth provider is enabled, record that fact rather than claiming an
   OAuth flow was tested.

## Key Directories

```
src/
├── app/                  # Pages (App Router)
│   ├── publish/          # Publishing dashboard (API keys + CLI instructions)
│   ├── transcripts/[id]/ # Transcript detail view
│   ├── me/transcripts/   # User's library
│   ├── groups/           # Collectives
│   └── users/[username]/ # Public profile
├── components/
│   ├── transcript/       # TranscriptCard, TranscriptViewer
│   └── ui/               # Button, Badge, etc.
├── lib/
│   ├── types.ts          # TypeScript interfaces
│   ├── api.ts            # API client
│   └── queries/          # React Query hooks (auth, transcripts, groups, tags)
└── providers/            # AuthProvider, QueryProvider
```

## Stack

- Next.js 16 with App Router
- TypeScript
- Tailwind CSS v4 (`@theme inline` in `globals.css`, no `tailwind.config.ts`)
- TanStack React Query for data fetching
