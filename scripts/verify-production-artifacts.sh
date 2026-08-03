#!/usr/bin/env bash
set -euo pipefail

revision="${1:-}"
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(git -C "$script_dir/.." rev-parse --show-toplevel)"
"$script_dir/check-artifact-source.sh" "$revision"
cd "$repo_root"

api_url="${NEXT_PUBLIC_API_URL:-}"
if [[ -z "$api_url" ]]; then
  printf >&2 '%s\n' "scripts/verify-production-artifacts.sh: NEXT_PUBLIC_API_URL is required because the production frontend compiles the API origin into browser assets. Set it to the intended deployment API URL."
  exit 2
fi

backend_image="village-backend:${revision}"
frontend_image="village-frontend:${revision}"

docker build --build-arg "RAILWAY_GIT_COMMIT_SHA=$revision" --tag "$backend_image" backend
docker build \
  --build-arg "RAILWAY_GIT_COMMIT_SHA=$revision" \
  --build-arg "NEXT_PUBLIC_API_URL=$api_url" \
  --tag "$frontend_image" \
  --file frontend/Dockerfile .

for image in "$backend_image" "$frontend_image"; do
  actual="$(docker image inspect --format '{{ index .Config.Labels "org.opencontainers.image.revision" }}' "$image")"
  if [[ "$actual" != "$revision" ]]; then
    printf >&2 '%s\n' "scripts/verify-production-artifacts.sh: image $image reports revision '$actual', expected '$revision'. Do not deploy it; rebuild from the intended checkout with RAILWAY_GIT_COMMIT_SHA set to that checkout's full revision."
    exit 1
  fi
  runtime_environment="$(docker image inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$image")"
  runtime_revision="$(grep '^VILLAGE_BUILD_REVISION=' <<<"$runtime_environment" | cut -d= -f2- || true)"
  if [[ "$runtime_revision" != "$revision" ]]; then
    printf >&2 '%s\n' "scripts/verify-production-artifacts.sh: image $image reports runtime revision '$runtime_revision', expected '$revision'. The runtime and OCI revision evidence do not identify the same source."
    exit 1
  fi
done

backend_command="$(docker image inspect --format '{{json .Config.Cmd}}' "$backend_image")"
frontend_command="$(docker image inspect --format '{{json .Config.Cmd}}' "$frontend_image")"
[[ "$backend_command" == '["/server"]' ]] || {
  printf >&2 '%s\n' "scripts/verify-production-artifacts.sh: backend production image command is $backend_command, expected [\"/server\"]. The verified label is not attached to the production server entry point."
  exit 1
}
[[ "$frontend_command" == '["node","frontend/server.js"]' ]] || {
  printf >&2 '%s\n' "scripts/verify-production-artifacts.sh: frontend production image command is $frontend_command, expected [\"node\",\"frontend/server.js\"]. The verified label is not attached to the standalone production entry point."
  exit 1
}

printf '%s\n' "Verified backend and frontend production image revision: $revision"
