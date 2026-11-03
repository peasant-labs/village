#!/usr/bin/env bash
set -euo pipefail

mode="${FAKE_DOCKER_CASE:?FAKE_DOCKER_CASE is required}"
revision="${EXPECTED_REVISION:?EXPECTED_REVISION is required}"

if [[ "${1:-}" == "build" ]]; then
  cli="${0##*/}"
  if [[ "$mode" == native_* ]]; then
    [[ "$cli" == podman ]] || { echo >&2 'native build did not use podman'; exit 2; }
    args=" $* "
    [[ "$args" == *' --security-opt seccomp=/server-only/seccomp.json '* ]] || exit 2
    if [[ "$args" == *'frontend/Dockerfile '* ]]; then
      [[ "$args" == *' --volume /var/cache/runner/pnpm-store:/pnpm/store:rw '* && "$args" == *' --volume /var/cache/runner/pnpm-metadata:/pnpm/cache:rw '* && "$args" == *' --build-arg PNPM_BUILD_STORE=/pnpm/store '* && "$args" == *' --build-arg PNPM_BUILD_CACHE=/pnpm/cache '* ]] || exit 2
      if [[ "$mode" == native_frontend_failure ]]; then echo >&2 'native frontend build failed'; exit 42; fi
    else
      [[ "$args" == *' --volume /var/cache/runner/gomod:/go/pkg/mod:rw '* && "$args" == *' --volume /var/cache/runner/gobuild:/root/.cache/go-build:rw '* ]] || exit 2
    fi
    if [[ "$mode" == native_failure ]]; then echo >&2 'native build failed'; exit 42; fi
  else
    [[ "$cli" == docker ]] || { echo >&2 'fallback did not use docker'; exit 2; }
  fi
  if [[ "${FAKE_REVISION_GUARD:-}" == 1 ]]; then
    [[ " $* " == *' --build-arg RAILWAY_GIT_COMMIT_SHA='* ]] || { echo >&2 'revision argument missing'; exit 2; }
    echo >&2 'RAILWAY_GIT_COMMIT_SHA must be the full 40-character lowercase Git revision'
    exit 1
  fi
  found_revision=false
  prior=""
  for argument in "$@"; do
    build_arg=""
    if [[ "$prior" == "--build-arg" ]]; then
      build_arg="$argument"
    elif [[ "$argument" == --build-arg=* ]]; then
      build_arg="${argument#--build-arg=}"
    fi
    if [[ "$build_arg" == "RAILWAY_GIT_COMMIT_SHA=$revision" ]]; then
      found_revision=true
    fi
    if [[ "${build_arg%%=*}" == "VCS_REF" ]]; then
      printf >&2 '%s\n' 'fake docker rejected the retired VCS_REF build argument'
      exit 2
    fi
    prior="$argument"
  done
  if [[ "$found_revision" != "true" ]]; then
    printf >&2 'fake docker did not receive RAILWAY_GIT_COMMIT_SHA=%s\n' "$revision"
    exit 2
  fi
  exit 0
fi

if [[ "${FAKE_REVISION_GUARD:-}" == 1 && "${1:-}" == image && "${2:-}" == rm ]]; then
  exit 0
fi

# `docker run --rm --entrypoint cat <image> <path>` reads a license/notice file
# out of the built image. The success (and every non-notice) case returns the
# file; the missing_license_notices case simulates an image built without it.
if [[ "${1:-}" == "run" ]]; then
  path="${*: -1}"
  if [[ "$mode" == "missing_license_notices" ]]; then
    printf >&2 'fake docker: %s is not present in the image\n' "$path"
    exit 1
  fi
  printf 'fake license/notice content for %s\n' "$path"
  exit 0
fi

if [[ "$#" -ne 5 || "$1" != "image" || "$2" != "inspect" || "$3" != "--format" ]]; then
  printf >&2 'fake docker received unsupported arguments: %q ' "$@"
  printf >&2 '\n'
  exit 2
fi

format="$4"
image="$5"
if [[ "$format" == *'org.opencontainers.image.revision'* ]]; then
  if [[ "$mode" == "label_mismatch" ]]; then
    printf '%s\n' '0000000000000000000000000000000000000000'
  else
    printf '%s\n' "$revision"
  fi
  exit 0
fi

if [[ "$format" == *'.Config.Env'* ]]; then
  case "$mode" in
    missing_runtime_revision)
      printf '%s\n' 'PATH=/usr/bin'
      ;;
    runtime_revision_mismatch)
      printf '%s\n' 'VILLAGE_BUILD_REVISION=0000000000000000000000000000000000000000'
      ;;
    *)
      printf 'VILLAGE_BUILD_REVISION=%s\n' "$revision"
      ;;
  esac
  exit 0
fi

if [[ "$format" == *'.Config.Cmd'* ]]; then
  if [[ "$image" == village-backend:* ]]; then
    if [[ "$mode" == "backend_command_mismatch" ]]; then
      printf '%s\n' '["/wrong-server"]'
    else
      printf '%s\n' '["/server"]'
    fi
  else
    if [[ "$mode" == "frontend_command_mismatch" ]]; then
      printf '%s\n' '["node","wrong-server.js"]'
    else
      printf '%s\n' '["node","frontend/server.js"]'
    fi
  fi
  exit 0
fi

printf >&2 'fake docker received unsupported inspect format: %s\n' "$format"
exit 2
