# Changelog

## 0.2.0 (2026-06-04)


### ⚠ BREAKING CHANGES

* node-metrics series now carry the scraped node's identity on the `tailscale_node` label instead of `instance` (which on Grafana Cloud always held the collector host); update node-metrics dashboards/queries accordingly. The per-device posture log now defaults to on-change; set

### Features

* **admin:** add status landing page, JSON API endpoint, and opt-in profiling (pprof + Pyroscope) ([282a333](https://github.com/rknightion/tailscale2otel/commit/282a33341b5c31da979b7c5da098640e5c1593b4))
* **admin:** authenticate status page + pprof with a shared token ([bbfea01](https://github.com/rknightion/tailscale2otel/commit/bbfea01032c68cdb718df2baca5a45da55676c0f))
* **app:** start the series.active cardinality reporter, gated by self-obs ([a9db840](https://github.com/rknightion/tailscale2otel/commit/a9db8407ea342e355841b3a39a19cda3244fcf28))
* **app:** wire dynamic node-metrics discovery from the devices API ([3900f89](https://github.com/rknightion/tailscale2otel/commit/3900f893169a53cc0c6fc72b5745b344e7cbda5a))
* **app:** wire node-metrics passthrough filters into nodeMetricsOptions ([1c42f81](https://github.com/rknightion/tailscale2otel/commit/1c42f812ad5711d5bde7f627e256b61f6de5d3c3))
* cardinality cap, stream feature.enabled, posture metric, node-label fix ([d3e5494](https://github.com/rknightion/tailscale2otel/commit/d3e54949df97be0c084aaa728fb8cfd3c1397e17))
* **cardinality:** per-entity gauge toggles for devices/users/keys ([389352f](https://github.com/rknightion/tailscale2otel/commit/389352fccb416fcb8c6c725e6d1892b80f74721b))
* **config:** add node_metrics.discovery schema ([7b29868](https://github.com/rknightion/tailscale2otel/commit/7b2986889cc6e8abc4b4dc585ea7785549e9d574))
* flow-log service-name mapping, independent port toggles, external reverse-DNS ([0835122](https://github.com/rknightion/tailscale2otel/commit/08351221c3d5e1f8aa42247ca0f7fc209330fa99))
* **nodemetrics:** add metric_allow/metric_deny/drop_labels passthrough filters ([603790c](https://github.com/rknightion/tailscale2otel/commit/603790c0ef8873ad7df77509b86604aa03546283))
* **nodemetrics:** emit discovery-health gauges ([cbb4831](https://github.com/rknightion/tailscale2otel/commit/cbb4831329c94a7d96e2f3d5fa98ad67dc00d632))
* **nodemetrics:** support dynamic target discovery ([1b86831](https://github.com/rknightion/tailscale2otel/commit/1b86831719463444c4cc492833c4665c34b380d8))
* **selfobs:** add runtime, dedup, and component-error self-observability metrics ([b0fa95f](https://github.com/rknightion/tailscale2otel/commit/b0fa95f6de9a9c9d0fc267d024f8a6094235637e))
* **telemetry:** add tailscale2otel.series.active cardinality self-metric ([918ca76](https://github.com/rknightion/tailscale2otel/commit/918ca76e654348b2e9333fa3b4abd84a8a810b26))
* **tsapi:** decode per-device tags from /devices?fields=all ([6e7906a](https://github.com/rknightion/tailscale2otel/commit/6e7906a24401a2cf4ce3fcf396823883ce007d54))


### Bug Fixes

* **ci:** authenticate cosign to ghcr.io before signing the chart ([c363142](https://github.com/rknightion/tailscale2otel/commit/c3631427cfebc9214b1ec15f9171d5a8cc03dda5))
* **ci:** bump Go to 1.26.4 to clear govulncheck stdlib findings ([5345bce](https://github.com/rknightion/tailscale2otel/commit/5345bcea1c809e38b5a7f508c1ebe7d363e97ce0))
* **ci:** bump tool modules to go 1.26.4 to match root module ([50cb7db](https://github.com/rknightion/tailscale2otel/commit/50cb7db6e0bd9d76419a560c0ddd22e58c01dcfe))
* **ci:** clear govulncheck stdlib findings + fix broken action versions ([62ace00](https://github.com/rknightion/tailscale2otel/commit/62ace0061a6e9b763e9b98c69e2cda33360168b7))
* **ci:** cosign snapshot image digest ([#12](https://github.com/rknightion/tailscale2otel/issues/12)) ([5bf2fa0](https://github.com/rknightion/tailscale2otel/commit/5bf2fa02f832e7b5cb80b9c466d565ddb74c24d4))
* **ci:** make snapshot chart prerelease version valid SemVer ([ba12049](https://github.com/rknightion/tailscale2otel/commit/ba12049863db3a10bdaa95e1ab2cdc2010734f36))
* **ci:** pin cosign installer action ([#10](https://github.com/rknightion/tailscale2otel/issues/10)) ([8ae03eb](https://github.com/rknightion/tailscale2otel/commit/8ae03ebb6a8012886016980a86d90e461bc0700a))
* **ci:** pin cosign-installer to [@v3](https://github.com/v3) (no moving v4 tag exists) ([37c9f7f](https://github.com/rknightion/tailscale2otel/commit/37c9f7ff414c1212b218a241b3fd070e7c7c01e8))
* **ci:** pin cosign-installer to [@v4](https://github.com/v4).1.2 (required for cosign v3+) ([0bf6156](https://github.com/rknightion/tailscale2otel/commit/0bf61560157e798cfb252841b2bb078bcf24bb17))
* **ci:** rename helm-values-schema-json input -&gt; values ([0a0b900](https://github.com/rknightion/tailscale2otel/commit/0a0b90098e5fb67b525a01f9dde62c285bcf140e))
* **ci:** use correct losisin/helm-docs-github-action@v2 repo ([2680758](https://github.com/rknightion/tailscale2otel/commit/26807585d24c8db31b917cc9d7a6852ff13c731b))
* **docker:** copy per-platform binary in dockers_v2 multi-arch build ([f780ca5](https://github.com/rknightion/tailscale2otel/commit/f780ca545507fdd9efa6551cb3558cb0c76da2ed))
* **docs:** redact live tailnet recon details from tracked files ([5ded0e6](https://github.com/rknightion/tailscale2otel/commit/5ded0e6918620a3f3376247952fc4a49116c7d11))
* guard main snapshot publishing ([44ee52e](https://github.com/rknightion/tailscale2otel/commit/44ee52e393a8a4fde1a8311d6497836b09489094))
* **nodemetrics:** bound discovered scrape work ([2770030](https://github.com/rknightion/tailscale2otel/commit/277003093a457d9fab9e96f3c8f6565199879f76))
* restrict main snapshot publishing to main ref ([1e58858](https://github.com/rknightion/tailscale2otel/commit/1e588584d88c7478829b0d45aaab3e47b934f1e5))
* **security:** harden receivers, scraper, TLS, and Helm from security review ([b743858](https://github.com/rknightion/tailscale2otel/commit/b743858103015ecc9d3e176fe820ed038b11235c))
* **selfobs:** guard cardinality reporter against non-positive interval to prevent panic ([cf1d7f4](https://github.com/rknightion/tailscale2otel/commit/cf1d7f4626c7f314994dd5318ff2c4aaec29583f))
* **webhook:** bound request bodies pre-auth and add server timeouts ([92348f4](https://github.com/rknightion/tailscale2otel/commit/92348f4517fac63295aa2ad2edeaaeb661f940bd))
* **webhook:** stop user cross-dedup over-suppressing distinct changes (D11) ([75a2c98](https://github.com/rknightion/tailscale2otel/commit/75a2c98ad245b4c87b449e03b5b8f006cb0de759))


### Miscellaneous

* **release:** make 0.2.0 the first complete release ([ec62fb1](https://github.com/rknightion/tailscale2otel/commit/ec62fb1b55cec270cd36c7def89f72e3c42687b5))
* **release:** set initial release version to 0.1.0 ([8f1a18e](https://github.com/rknightion/tailscale2otel/commit/8f1a18e1988a268e0996e992ea40b28c91f1b977))

## Changelog
