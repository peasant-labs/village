.PHONY: dev dev-backend dev-frontend setup setup-backend setup-frontend migrate sqlc seed test build up down backend-dev backend-dev-seed backend-dev-down backend-dev-reset backend-encrypted-test backend-encrypted-down third-party-notices third-party-notices-check

export PATH := $(PATH):$(shell go env GOPATH)/bin

# Local development (infra + app in Docker). All @peasant-labs/* packages
# resolve from the registry via the pnpm workspace (see pnpm-workspace.yaml);
# the vendored-tarball flow (frontend/vendor + vendor-shared) is gone.
dev:
	docker compose up --build

setup: setup-backend setup-frontend

setup-backend:
	@echo "Installing air..."
	go install github.com/air-verse/air@latest

setup-frontend:
	@echo "Installing frontend dependencies..."
	pnpm install

# Database
migrate:
	docker compose exec backend go run ./cmd/server -migrate-only

sqlc:
	@echo "Generating sqlc code..."
	sqlc generate

seed:
	@echo "Seeding database..."
	docker compose exec backend go run ./cmd/server -seed-core
	docker exec -i $$(docker compose ps -q postgres) psql -U peasant -d peasant < scripts/seed.sql
	docker compose exec backend go run ./cmd/server -seed-privacy
	docker exec -i $$(docker compose ps -q postgres) psql -U peasant -d peasant < scripts/seed-privacy-features.sql

# Testing
test:
	@echo "Running tests..."
	cd backend && go test ./...

# Dependency license notices shipped in the backend image (backend/Dockerfile).
third-party-notices:
	cd backend && ./scripts/gen-third-party-notices.sh

# CI gate: fail if the committed notices drift from the deps, and fail on any
# copyleft/unclassifiable license entering the backend binary's module set.
third-party-notices-check:
	cd backend && ./scripts/gen-third-party-notices.sh
	git diff --exit-code -- backend/THIRD_PARTY_NOTICES
	cd backend && ./scripts/check-dep-licenses.sh

# Disposable, isolated PostgreSQL + MinIO proof of the encrypted backend.
# The script always removes its project and volumes unless KEEP_ENCRYPTED_TEST_STACK=1.
backend-encrypted-test:
	./scripts/encrypted-backend-dev.sh test

backend-encrypted-down:
	./scripts/encrypted-backend-dev.sh down

# Persistent, worktree-isolated encrypted backend development.
backend-dev:
	./scripts/encrypted-backend-dev.sh dev

backend-dev-seed:
	./scripts/encrypted-backend-dev.sh seed

backend-dev-down:
	./scripts/encrypted-backend-dev.sh dev-down

backend-dev-reset:
	CONFIRM=$(CONFIRM) ./scripts/encrypted-backend-dev.sh reset

# Docker (full stack)
up:
	docker compose up --build -d

down:
	docker compose down

build:
	@echo "Building production binaries..."
	cd backend && go build -o ./bin/server ./cmd/server
	cd frontend && pnpm build
