# Licensing

The default and only license for this repository is the **Apache License 2.0**
(`Apache-2.0`). The full text is in [LICENSE](./LICENSE).

## Third-party dependencies

Third-party dependencies in the Go module cache remain under their own upstream licenses. Their
licenses are not superseded by the Apache-2.0 license of this repository; the combined binary is
distributed under Apache-2.0 while each dependency retains its original terms.

### Notices & SBOMs (release artifacts)

Third-party attribution and software bills of materials are generated from the **actual import
graph** of `./cmd/tailscale2otel` (not from `go.mod`, which carries indirect/test-only deps that
never ship), using [`go-licenses`](https://github.com/google/go-licenses) and
[`syft`](https://github.com/anchore/syft):

- **`scripts/notices.sh`** → `THIRD_PARTY_NOTICES.md` — every linked module's `LICENSE` text, plus
  its `NOTICE` file where one exists (Apache-2.0 §4(d)). The container images bake this into
  `/licenses/THIRD_PARTY_NOTICES.md` (alongside `/licenses/LICENSE`); the release pipeline also
  attaches it to each GitHub Release.
- **`scripts/sbom.sh`** → `dist/sbom/tailscale2otel.spdx.json` (SPDX 2.3) +
  `dist/sbom/tailscale2otel.cdx.json` (CycloneDX 1.6), attached to each GitHub Release. (GoReleaser
  additionally emits per-archive SBOMs and an image SBOM attestation.)

These are **regenerated at release time, not committed** — they change on every dependency bump, so
committing and gating them would block hosted-Renovate automerge. They are therefore deliberately
**not** part of any CI gate. The images and the release assets always reflect exactly what shipped.

Regenerate locally (requires the pinned tools on `PATH`):

```sh
go install github.com/google/go-licenses@v1.6.0
go install github.com/anchore/syft/cmd/syft@v1.18.1
bash scripts/notices.sh                                    # -> THIRD_PARTY_NOTICES.md
go build -o bin/tailscale2otel ./cmd/tailscale2otel && bash scripts/sbom.sh   # -> dist/sbom/
```
