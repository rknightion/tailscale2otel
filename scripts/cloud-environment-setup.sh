#!/usr/bin/env bash
# LOCAL AGENTS: do not execute this script unless you are running as a cloud agent.
#
# Bootstrap the manual Codex or Claude Code cloud environment for this repository.
# Configure the cloud environment setup command as:
#
#   scripts/cloud-environment-setup.sh
#
# Cloud setup runs before the agent starts. Durable executables are installed in
# ~/.local/bin and that directory is added to ~/.bashrc for later agent shells.
# Keep the pins below aligned with AGENTS.md and the GitHub Actions workflows.
set -euo pipefail

readonly BACKLOG_VERSION="1.50.1"
readonly GOLANGCI_LINT_VERSION="2.13.1"
readonly GOVULNCHECK_VERSION="1.3.0"
readonly GORELEASER_VERSION="2.16.0"
readonly HELM_VERSION="3.19.0"
readonly PROMETHEUS_VERSION="3.7.3"
readonly YQ_VERSION="4.47.2"

ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"

mkdir -p "$HOME/.local/bin"
# Put pinned tools ahead of universal-image defaults, which may contain older
# versions under mise or another system-level prefix.
export PATH="$HOME/.local/bin:$PATH"

# Exports from setup do not survive into a cloud task's agent shell. Make the
# user-local tools available there as well, without appending on every cache run.
if ! grep -Fq 'export PATH="$HOME/.local/bin:$PATH"' "$HOME/.bashrc" 2>/dev/null; then
  printf '\n# tailscale2otel cloud-agent tools\nexport PATH="$HOME/.local/bin:$PATH"\n' >>"$HOME/.bashrc"
fi

export GOBIN="$HOME/.local/bin"
# `go install pkg@version` otherwise selects the tool's minimum supported Go
# version. Build Go-based validators and generators with the repository's
# declared toolchain so cloud behavior stays aligned with CI.
export GOTOOLCHAIN="go$(awk '$1 == "go" { print $2; exit }' go.mod)"

echo "== installing task tracker"
# Backlog.md is the source of truth for project work. Pinning it to the version
# of the instructions embedded in AGENTS.md prevents CLI/instruction drift.
npm install --global --prefix "$HOME/.local" "backlog.md@$BACKLOG_VERSION"

echo "== installing Go validation tools"
go install "golang.org/x/vuln/cmd/govulncheck@v$GOVULNCHECK_VERSION"

install_release_tools() {
  local arch goreleaser_arch tmp
  case "$(uname -m)" in
    x86_64) arch=amd64; goreleaser_arch=x86_64 ;;
    aarch64 | arm64) arch=arm64; goreleaser_arch=arm64 ;;
    *) echo "unsupported architecture: $(uname -m)" >&2; return 1 ;;
  esac
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN

  if ! command -v golangci-lint >/dev/null 2>&1 ||
    ! golangci-lint version 2>&1 | grep -Fq "$GOLANGCI_LINT_VERSION"; then
    local lint_archive="golangci-lint-${GOLANGCI_LINT_VERSION}-linux-${arch}.tar.gz"
    local lint_base="https://github.com/golangci/golangci-lint/releases/download/v${GOLANGCI_LINT_VERSION}"
    scripts/ci-retry.sh curl -fsSL --max-time 120 -o "$tmp/$lint_archive" "$lint_base/$lint_archive"
    scripts/ci-retry.sh curl -fsSL --max-time 60 -o "$tmp/golangci-checksums.txt" "$lint_base/golangci-lint-${GOLANGCI_LINT_VERSION}-checksums.txt"
    (cd "$tmp" && grep -F "$lint_archive" golangci-checksums.txt | sha256sum --check)
    tar -xzf "$tmp/$lint_archive" -C "$tmp"
    install -m 0755 "$tmp/golangci-lint-${GOLANGCI_LINT_VERSION}-linux-${arch}/golangci-lint" \
      "$HOME/.local/bin/golangci-lint"
  fi

  if ! command -v goreleaser >/dev/null 2>&1 ||
    ! goreleaser --version 2>&1 | grep -Fq "$GORELEASER_VERSION"; then
    local gr_archive="goreleaser_Linux_${goreleaser_arch}.tar.gz"
    local gr_base="https://github.com/goreleaser/goreleaser/releases/download/v${GORELEASER_VERSION}"
    scripts/ci-retry.sh curl -fsSL --max-time 120 -o "$tmp/$gr_archive" "$gr_base/$gr_archive"
    scripts/ci-retry.sh curl -fsSL --max-time 60 -o "$tmp/goreleaser-checksums.txt" "$gr_base/checksums.txt"
    (cd "$tmp" && grep -F "$gr_archive" goreleaser-checksums.txt | sha256sum --check)
    tar -xzf "$tmp/$gr_archive" -C "$tmp" goreleaser
    install -m 0755 "$tmp/goreleaser" "$HOME/.local/bin/goreleaser"
  fi

  if ! command -v helm >/dev/null 2>&1 ||
    ! helm version --short 2>&1 | grep -Fq "v$HELM_VERSION"; then
    local helm_archive="helm-v${HELM_VERSION}-linux-${arch}.tar.gz"
    local helm_base="https://get.helm.sh"
    scripts/ci-retry.sh curl -fsSL --max-time 120 -o "$tmp/$helm_archive" "$helm_base/$helm_archive"
    scripts/ci-retry.sh curl -fsSL --max-time 60 -o "$tmp/$helm_archive.sha256" "$helm_base/$helm_archive.sha256sum"
    (cd "$tmp" && sha256sum --check "$helm_archive.sha256")
    tar -xzf "$tmp/$helm_archive" -C "$tmp"
    install -m 0755 "$tmp/linux-${arch}/helm" "$HOME/.local/bin/helm"
  fi

  if ! command -v yq >/dev/null 2>&1 ||
    ! yq --version 2>&1 | grep -Fq "v$YQ_VERSION"; then
    local yq_binary="yq_linux_${arch}"
    local yq_base="https://github.com/mikefarah/yq/releases/download/v${YQ_VERSION}"
    scripts/ci-retry.sh curl -fsSL --max-time 120 -o "$tmp/$yq_binary" "$yq_base/$yq_binary"
    scripts/ci-retry.sh curl -fsSL --max-time 60 -o "$tmp/yq-checksums" "$yq_base/checksums"
    local yq_hash
    # The release's documented hash order puts SHA-256 in column 19 (the
    # artifact name is column 1).
    yq_hash="$(awk -v name="$yq_binary" '$1 == name { print $19; exit }' "$tmp/yq-checksums")"
    test -n "$yq_hash"
    (cd "$tmp" && printf '%s  %s\n' "$yq_hash" "$yq_binary" | sha256sum --check)
    install -m 0755 "$tmp/$yq_binary" "$HOME/.local/bin/yq"
  fi
}

echo "== installing release and chart tools"
install_release_tools

echo "== installing pinned generated-artifact tools"
# This repository helper supplies the nonstandard helm-docs version ldflag and
# installs helm-values-schema-json at the exact versions used by CI.
#
# Called as a SCRIPT, not as `just gen-tools`: this environment has no `just` on
# PATH (nothing above installs one), so the `tools` entry point has to stay
# script-callable. Every other generator is reached through the justfile.
scripts/regen-generated.sh tools

install_promtool() {
  local os arch archive base tmp
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  case "$(uname -m)" in
    x86_64) arch=amd64 ;;
    aarch64 | arm64) arch=arm64 ;;
    *) echo "unsupported architecture for promtool: $(uname -m)" >&2; return 1 ;;
  esac
  archive="prometheus-${PROMETHEUS_VERSION}.${os}-${arch}.tar.gz"
  base="https://github.com/prometheus/prometheus/releases/download/v${PROMETHEUS_VERSION}"
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN
  scripts/ci-retry.sh curl -fsSL --max-time 120 -o "$tmp/$archive" "$base/$archive"
  scripts/ci-retry.sh curl -fsSL --max-time 60 -o "$tmp/sha256sums.txt" "$base/sha256sums.txt"
  (cd "$tmp" && grep -F "$archive" sha256sums.txt | sha256sum --check)
  tar -xzf "$tmp/$archive" -C "$tmp"
  install -m 0755 "$tmp/prometheus-${PROMETHEUS_VERSION}.${os}-${arch}/promtool" "$HOME/.local/bin/promtool"
}

if ! command -v promtool >/dev/null 2>&1 ||
  ! promtool --version 2>&1 | grep -Fq "$PROMETHEUS_VERSION"; then
  echo "== installing promtool $PROMETHEUS_VERSION"
  install_promtool
fi

echo "== warming dependency caches for every Go module"
while IFS= read -r modfile; do
  (cd "$(dirname "$modfile")" && go mod download)
done < <(find . -name go.mod -not -path './.git/*' -not -path './.capture/*' \
  -not -path './.claude/*' | sort)

# Install the checked-in pre-commit hook. It regenerates only artifacts affected
# by staged inputs and remains advisory when an optional generator is absent.
scripts/setup.sh

echo "== setup smoke check"
backlog --version
go version
golangci-lint version
govulncheck -version
goreleaser --version
helm version --short
yq --version
promtool --version

echo "Cloud agent environment is ready."
