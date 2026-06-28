#!/usr/bin/env bash
# sbom.sh — generate SPDX + CycloneDX SBOMs for the shipped artifact.
#
# Writes two SBOMs (standard formats, both JSON) into dist/sbom/:
#   <name>.spdx.json   SPDX 2.3
#   <name>.cdx.json    CycloneDX 1.6
#
# Default target is the built binary (bin/tailscale2otel), which embeds the Go module
# build info — i.e. exactly the modules linked into the release. Set SBOM_TARGET to scan
# something else (e.g. an image ref `ghcr.io/rknightion/tailscale2otel:vX.Y.Z` or `dir:.`).
# These are RELEASE ARTIFACTS (timestamps/UUIDs make them non-deterministic), so they are
# attached to the GitHub Release rather than committed.
#
# Env:
#   SYFT          syft binary (default: syft on PATH)
#   SBOM_TARGET   what syft scans (default: bin/tailscale2otel)
#   OUT_DIR       output directory (default: dist/sbom)
#   SBOM_NAME     output basename (default: tailscale2otel)
set -euo pipefail

SYFT="${SYFT:-syft}"
SBOM_TARGET="${SBOM_TARGET:-bin/tailscale2otel}"
OUT_DIR="${OUT_DIR:-dist/sbom}"
SBOM_NAME="${SBOM_NAME:-tailscale2otel}"

command -v "$SYFT" >/dev/null 2>&1 || {
  echo "sbom: syft not found ('$SYFT') — install with 'go install github.com/anchore/syft/cmd/syft@v1.18.1'" >&2
  exit 1
}

mkdir -p "$OUT_DIR"
echo "sbom: scanning $SBOM_TARGET"
"$SYFT" "$SBOM_TARGET" -q \
  -o "spdx-json=$OUT_DIR/$SBOM_NAME.spdx.json" \
  -o "cyclonedx-json=$OUT_DIR/$SBOM_NAME.cdx.json"

echo "sbom: wrote $OUT_DIR/$SBOM_NAME.spdx.json + $OUT_DIR/$SBOM_NAME.cdx.json"
