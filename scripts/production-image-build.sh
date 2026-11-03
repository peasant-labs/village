#!/usr/bin/env bash

# Build through the guest's native client only when its profile is advertised.
production_image_build() {
  local target="$1"
  shift
  if [[ ! ${RUNNER_BUILD_SECCOMP_PROFILE+x} ]]; then
    docker build "$@"
    return
  fi
  if [[ -z "$RUNNER_BUILD_SECCOMP_PROFILE" ]]; then
    printf >&2 '%s\n' 'RUNNER_BUILD_SECCOMP_PROFILE is empty; configure a server-readable seccomp profile or unset it for Docker.'
    return 2
  fi

  local -a options=(--security-opt "seccomp=$RUNNER_BUILD_SECCOMP_PROFILE")
  case "$target" in
    backend)
      mkdir -p /var/cache/runner/gomod /var/cache/runner/gobuild || return
      options+=(--volume /var/cache/runner/gomod:/go/pkg/mod:rw
                --volume /var/cache/runner/gobuild:/root/.cache/go-build:rw)
      ;;
    frontend)
      mkdir -p /var/cache/runner/pnpm-store /var/cache/runner/pnpm-metadata || return
      options+=(--volume /var/cache/runner/pnpm-store:/pnpm/store:rw
                --volume /var/cache/runner/pnpm-metadata:/pnpm/cache:rw
                --build-arg PNPM_BUILD_STORE=/pnpm/store
                --build-arg PNPM_BUILD_CACHE=/pnpm/cache)
      ;;
    *) printf >&2 'Unknown production image target: %s\n' "$target"; return 2 ;;
  esac
  podman build "${options[@]}" "$@"
}
