#!/usr/bin/env bash
# Shared configuration for disposable Linux build workers.
set -euo pipefail

remote_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
remote_base="${MCND_REMOTE_BASE:-${HOME}/.cache/mintcandy-remote}"
remote_base="$(realpath -m -- "$remote_base")"
if [[ "$(basename -- "$remote_base")" != mintcandy-remote || "$remote_base" == "$remote_root"/* ]]; then
  echo 'MCND_REMOTE_BASE must be a dedicated mintcandy-remote directory outside the checkout.' >&2
  exit 2
fi
if [[ "$(uname -s)" != Linux || "$(uname -m)" != x86_64 || -n "${WSL_DISTRO_NAME:-}" ]]; then
  echo 'Use a disposable Linux amd64 worker, not the operator Windows/WSL environment.' >&2
  exit 2
fi
mkdir -p "$remote_base"/{tools,toolchains,modules,cache,tmp,results}
export GOENV=off GOWORK=off GOTOOLCHAIN=local GOFLAGS= GOAMD64=v1
export GOOS=linux GOARCH=amd64 CGO_ENABLED=1 CC=gcc GOMAXPROCS=2
export GOPROXY=https://proxy.golang.org GOSUMDB=sum.golang.org
unset GOEXPERIMENT CGO_CFLAGS CGO_CPPFLAGS CGO_CXXFLAGS CGO_FFLAGS CGO_LDFLAGS
export GOMODCACHE="$remote_base/modules"
export SOLC="$remote_base/tools/solc-0.8.20"

remote_select_go() {
  case "$1" in modern) remote_go_version=1.26.11 ;; legacy) remote_go_version=1.24.4 ;; *) return 2 ;; esac
  export PATH="$remote_base/toolchains/go$remote_go_version/bin:$PATH"
  export GOCACHE="$remote_base/cache/go$remote_go_version"
  mkdir -p "$GOCACHE"
}

remote_budget() {
  local free_kib cache_kib
  free_kib="$(df -Pk -- "$remote_base" | awk 'END {print $4}')"
  cache_kib="$(du -sk -- "$remote_base/cache" | awk '{print $1}')"
  if (( free_kib < 20 * 1024 * 1024 )); then
    echo 'Stop: worker has less than 20 GiB free. Do not remove node data to proceed.' >&2
    return 1
  fi
  if (( cache_kib > 30 * 1024 * 1024 )); then
    echo 'Stop: build caches exceed the 30 GiB soft budget. Clean this worker cache between runs.' >&2
    return 1
  fi
}

remote_require_tools() {
  local command
  for command in git curl sha256sum tar gcc g++ make python3 flock; do
    command -v "$command" >/dev/null || { echo "Missing dependency: $command" >&2; return 1; }
  done
}
