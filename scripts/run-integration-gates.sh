#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(git -C "$script_dir/.." rev-parse --show-toplevel)"
admin_url="${TEST_DATABASE_ADMIN_URL:-}"
url_template="${TEST_DATABASE_URL_TEMPLATE:-}"

# Test-only custody shared with the authority fixture. The integration gate
# deliberately owns this deterministic value so local and CI execution exercise
# mounted encryption instead of depending on an operator's environment.
export TRANSCRIPT_KEK_ACTIVE_VERSION="1"
export TRANSCRIPT_KEK_KEYRING='{"1":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="}'

if [[ -z "$admin_url" ]]; then
  printf >&2 '%s\n' "scripts/run-integration-gates.sh: TEST_DATABASE_ADMIN_URL is required to create isolated package databases. Set it to a reachable PostgreSQL maintenance database whose test role has CREATEDB."
  exit 2
fi
if [[ "$url_template" != *'{database}'* || "${url_template#*'{database}'}" == *'{database}'* ]]; then
  printf >&2 '%s\n' "scripts/run-integration-gates.sh: TEST_DATABASE_URL_TEMPLATE must contain exactly one literal {database} placeholder. Each package needs a distinct fresh database URL; for example postgres://test:test@localhost:5432/{database}?sslmode=disable."
  exit 2
fi

run_package() {
  local package="$1"
  local database="$2"
  local migrate_first="$3"
  local database_url="${url_template/'{database}'/$database}"
  local output
  output="$(mktemp)"

  printf '%s\n' "=== ISOLATED DATABASE: $database ($package)"
  (
    cd "$repo_root/backend"
    go run ../scripts/test-database-admin.go reset "$admin_url" "$database"
  )

  if [[ "$migrate_first" == true ]]; then
    printf '%s\n' "=== MIGRATE PACKAGE DATABASE: $database ($package)"
    (
      cd "$repo_root/backend"
      DATABASE_URL="$database_url" go run ./cmd/server -migrate-only
    )
  fi

  set +e
  (
    cd "$repo_root/backend"
    TEST_DATABASE_URL="$database_url" go test -json -tags=integration -race -count=1 "$package"
  ) 2>&1 | (
    cd "$repo_root/backend"
    go run ../scripts/check-go-test-events.go "$package"
  ) 2>&1 | tee "$output"
  local pipeline_statuses=("${PIPESTATUS[@]}")
  local test_status="${pipeline_statuses[0]}"
  local event_status="${pipeline_statuses[1]}"
  set -e

  if [[ "$test_status" -ne 0 || "$event_status" -ne 0 ]]; then
    printf >&2 '%s\n' "scripts/run-integration-gates.sh: $package failed or emitted a Go skip event against isolated database $database. Its database is retained for diagnosis; inspect the visible RUN/PASS/error evidence and repair the failing production path or unavailable service without weakening the writer fence or accepting a skipped test."
    rm -f "$output"
    if [[ "$test_status" -ne 0 ]]; then
      return "$test_status"
    fi
    return "$event_status"
  fi
  rm -f "$output"

  (
    cd "$repo_root/backend"
    go run ../scripts/test-database-admin.go drop "$admin_url" "$database"
  )
  printf '%s\n' "=== ISOLATED PACKAGE PASS: $package"
}

mapfile -t tagged_packages < <(cd "$repo_root/backend" && go list -tags=integration ./...)
integration_packages=()
for package in "${tagged_packages[@]}"; do
  baseline_files="$(cd "$repo_root/backend" && go list -f '{{join .TestGoFiles " "}}|{{join .XTestGoFiles " "}}' "$package")"
  tagged_files="$(cd "$repo_root/backend" && go list -tags=integration -f '{{join .TestGoFiles " "}}|{{join .XTestGoFiles " "}}' "$package")"
  if [[ "$baseline_files" != "$tagged_files" ]]; then
    integration_packages+=("$package")
  fi
done

if [[ "${#integration_packages[@]}" -eq 0 ]]; then
  printf >&2 '%s\n' "scripts/run-integration-gates.sh: no package gained test files under the integration build tag. The real-service suite would be empty; restore the tagged tests or fix Go package discovery."
  exit 1
fi

for package in "${integration_packages[@]}"; do
  migrate_first=false
  if [[ "$package" == */internal/database/sqlc ]]; then
    migrate_first=true
  fi
  run_package "$package" village_ci_package "$migrate_first"
done
