#!/usr/bin/env bash
set -euo pipefail

mode="${FAKE_DOCKER_CASE:?FAKE_DOCKER_CASE is required}"
revision="${EXPECTED_REVISION:?EXPECTED_REVISION is required}"

if [[ "${1:-}" == "build" ]]; then
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
