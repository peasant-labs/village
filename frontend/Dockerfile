FROM node:26-alpine AS deps
RUN npm install -g pnpm@11.24.0
ENV CI=true
WORKDIR /app

# Keep dependency installation cacheable and locked to the committed workspace.
COPY package.json pnpm-workspace.yaml pnpm-lock.yaml ./
COPY frontend/package.json frontend/package.json
ARG PNPM_SHARED_CACHE=false
RUN case "$PNPM_SHARED_CACHE" in \
      false) pnpm install --frozen-lockfile ;; \
      true) pnpm install --frozen-lockfile --store-dir /app/.pnpm-cache --config.enable-global-virtual-store=true ;; \
      *) echo >&2 "frontend/Dockerfile: PNPM_SHARED_CACHE must be true or false; set it to false for the standard build or true for the shared cache layout."; exit 1 ;; \
    esac

FROM node:26-alpine AS builder
RUN npm install -g pnpm@11.24.0
ENV CI=true
ENV NEXT_TELEMETRY_DISABLED=1
WORKDIR /app
COPY --from=deps /app ./
COPY frontend frontend

# This value is compiled into browser assets by Next.js; setting it only when
# the container starts cannot change an already-built frontend.
ARG NEXT_PUBLIC_API_URL
RUN test -n "$NEXT_PUBLIC_API_URL" || { \
      echo >&2 "frontend/Dockerfile: NEXT_PUBLIC_API_URL is required during the Next.js build because browsers need the public API address. Without it, the deployed frontend cannot call the API. Set the Railway frontend build variable to https://<api-origin>/api/v1 and rebuild."; \
      exit 1; \
    }
ENV NEXT_PUBLIC_API_URL=${NEXT_PUBLIC_API_URL}
WORKDIR /app/frontend
ARG PNPM_SHARED_CACHE=false
RUN case "$PNPM_SHARED_CACHE" in \
      false) pnpm run build ;; \
      true) PNPM_CONFIG_ENABLE_GLOBAL_VIRTUAL_STORE=true PNPM_CONFIG_STORE_DIR=/app/.pnpm-cache pnpm run build ;; \
      *) echo >&2 "frontend/Dockerfile: PNPM_SHARED_CACHE must be true or false; set it to false for the standard build or true for the shared cache layout."; exit 1 ;; \
    esac

FROM node:26-alpine AS runner
ARG RAILWAY_GIT_COMMIT_SHA
RUN case "$RAILWAY_GIT_COMMIT_SHA" in \
      ''|*[!0-9a-f]*) revision_valid=false ;; \
      *) revision_valid=true ;; \
    esac; \
    if [ "$revision_valid" != true ] || [ "${#RAILWAY_GIT_COMMIT_SHA}" -ne 40 ]; then \
      echo >&2 "frontend/Dockerfile: RAILWAY_GIT_COMMIT_SHA must be the full 40-character lowercase Git revision. Railway supplies it for GitHub-triggered deployments; local builds must pass --build-arg RAILWAY_GIT_COMMIT_SHA=<full-git-revision>."; \
      exit 1; \
    fi; \
    printf 'Building Village revision: %s\n' "$RAILWAY_GIT_COMMIT_SHA"
LABEL org.opencontainers.image.source="https://github.com/peasant-labs/village" \
      org.opencontainers.image.revision="$RAILWAY_GIT_COMMIT_SHA"
ENV VILLAGE_BUILD_REVISION=${RAILWAY_GIT_COMMIT_SHA}
WORKDIR /app
ENV NODE_ENV=production
ENV NEXT_TELEMETRY_DISABLED=1
ENV HOSTNAME=0.0.0.0
ENV PORT=3000

# outputFileTracingRoot is the repository root, so retain the traced workspace
# layout and place application assets beside frontend/server.js.
COPY --from=builder /app/frontend/.next/standalone ./
COPY --from=builder /app/frontend/.next/static ./frontend/.next/static
COPY --from=builder /app/frontend/public ./frontend/public

EXPOSE 3000
CMD ["node", "frontend/server.js"]
