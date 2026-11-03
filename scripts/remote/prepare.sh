#!/usr/bin/env bash
# Install pinned tools and download modules on a disposable build worker.
set -euo pipefail
source "$(dirname -- "${BASH_SOURCE[0]}")/common.sh"
remote_require_tools
profile="${1:-modern}"
case "$profile" in modern|legacy|all) ;; *) echo 'Usage: prepare.sh [modern|legacy|all]' >&2; exit 2 ;; esac
exec 9>"$remote_base/worker.lock"
flock -n 9 || { echo 'Another build/setup owns this worker cache.' >&2; exit 1; }
remote_budget
install_tmp="$(mktemp -d "$remote_base/tmp/install.XXXXXXXX")"
cleanup() {
  local status=$?
  if [[ "$(dirname -- "$install_tmp")" == "$remote_base/tmp" && "$(basename -- "$install_tmp")" == install.* && ! -L "$install_tmp" ]]; then
    rm -rf -- "$install_tmp"
  fi
  exit "$status"
}
trap cleanup EXIT

install_go() {
  local family="$1" version digest destination
  case "$family" in
    modern) version=1.26.10; digest=ccfe7ae822d2c2f41f4fe7c956199967a4dac22d0ee0d2b2bfdb440aab3b20c5 ;;
    legacy) version=1.24.4; digest=77e5da33bb72aeaef1ba4418b6fe511bc4d041873cbf82e5aa6318740df98717 ;;
  esac
  destination="$remote_base/toolchains/go$version"
  if [[ ! -e "$destination" ]]; then
    curl --fail --location --proto '=https' --tlsv1.2 --retry 3 \
      "https://go.dev/dl/go$version.linux-amd64.tar.gz" --output "$install_tmp/go.tar.gz"
    printf '%s  %s\n' "$digest" "$install_tmp/go.tar.gz" | sha256sum --check
    tar -xzf "$install_tmp/go.tar.gz" -C "$install_tmp"
    mv -- "$install_tmp/go" "$destination"
    rm -- "$install_tmp/go.tar.gz"
  fi
  [[ "$("$destination/bin/go" version)" == "go version go$version linux/amd64" ]] || { echo 'Wrong Go toolchain.' >&2; return 1; }
  remote_select_go "$family"
}

prepare_modules() {
  local family="$1" module="$2"
  install_go "$family"
  (
    cd "$module"
    go mod download
    go mod verify
    git diff --exit-code -- go.mod go.sum
  )
  remote_budget
}

if [[ "$profile" == modern || "$profile" == all ]]; then
  prepare_modules modern "$remote_root/network"
  if [[ ! -e "$SOLC" ]]; then
    curl --fail --location --proto '=https' --tlsv1.2 --retry 3 \
      'https://raw.githubusercontent.com/ethereum/solc-bin/gh-pages/linux-amd64/solc-linux-amd64-v0.8.20+commit.a1b79de6' \
      --output "$install_tmp/solc"
    printf '%s  %s\n' '0479d44fdf9c501c25337fdc540419f1593b884a87b47f023da4f1c700fda782' "$install_tmp/solc" | sha256sum --check
    chmod 755 "$install_tmp/solc"
    mv -- "$install_tmp/solc" "$SOLC"
  fi
  printf '%s  %s\n' '0479d44fdf9c501c25337fdc540419f1593b884a87b47f023da4f1c700fda782' "$SOLC" | sha256sum --check
fi
if [[ "$profile" == legacy || "$profile" == all ]]; then
  prepare_modules legacy "$remote_root"
fi
echo "Prepared $profile toolchains and dependencies. No daemon or test network was started."
