# syntax=docker/dockerfile:1
FROM golang:1.25-alpine AS builder
RUN apk add --no-cache git
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /server ./cmd/server

FROM alpine:3.19
ARG RAILWAY_GIT_COMMIT_SHA
RUN case "$RAILWAY_GIT_COMMIT_SHA" in \
      ''|*[!0-9a-f]*) revision_valid=false ;; \
      *) revision_valid=true ;; \
    esac; \
    if [ "$revision_valid" != true ] || [ "${#RAILWAY_GIT_COMMIT_SHA}" -ne 40 ]; then \
      echo >&2 "backend/Dockerfile: RAILWAY_GIT_COMMIT_SHA must be the full 40-character lowercase Git revision. Railway supplies it for GitHub-triggered deployments; local builds must pass --build-arg RAILWAY_GIT_COMMIT_SHA=<full-git-revision>."; \
      exit 1; \
    fi; \
    printf 'Building Village revision: %s\n' "$RAILWAY_GIT_COMMIT_SHA"
LABEL org.opencontainers.image.source="https://github.com/peasant-labs/village" \
      org.opencontainers.image.revision="$RAILWAY_GIT_COMMIT_SHA"
ENV VILLAGE_BUILD_REVISION=${RAILWAY_GIT_COMMIT_SHA}
RUN apk add --no-cache ca-certificates
COPY --from=builder /server /server
# License obligations for the statically-linked binary this image ships, required
# when the image is published/redistributed: village's own Apache-2.0 license
# (Apache-2.0 section 4a) plus the dependency notices (see
# backend/scripts/gen-third-party-notices.sh).
COPY --from=builder /app/LICENSE /usr/share/doc/village/LICENSE
COPY --from=builder /app/THIRD_PARTY_NOTICES /usr/share/doc/village/THIRD_PARTY_NOTICES
EXPOSE 8080
CMD ["/server"]
