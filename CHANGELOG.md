# Changelog

## [0.6.0](https://github.com/rknightion/tailscale2otel/compare/v0.5.0...v0.6.0) (2026-07-04)


### Features

* **app,config:** readiness, cache reuse, User-Agent, rate-limit visibility, secret headers + collector fixes ([5d43509](https://github.com/rknightion/tailscale2otel/commit/5d4350941fb20ebbd2b9e271c12a4eb91b6cd63f)), closes [#66](https://github.com/rknightion/tailscale2otel/issues/66) [#76](https://github.com/rknightion/tailscale2otel/issues/76) [#57](https://github.com/rknightion/tailscale2otel/issues/57) [#85](https://github.com/rknightion/tailscale2otel/issues/85) [#70](https://github.com/rknightion/tailscale2otel/issues/70) [#79](https://github.com/rknightion/tailscale2otel/issues/79) [#73](https://github.com/rknightion/tailscale2otel/issues/73) [#61](https://github.com/rknightion/tailscale2otel/issues/61) [#63](https://github.com/rknightion/tailscale2otel/issues/63) [#67](https://github.com/rknightion/tailscale2otel/issues/67)
* **deploy:** add fsGroup, extra volumes/mounts, and baseline alert rules ([250b4c5](https://github.com/rknightion/tailscale2otel/commit/250b4c577bd3bf46c109c7d443b5b11eadf860cc)), closes [#82](https://github.com/rknightion/tailscale2otel/issues/82) [#83](https://github.com/rknightion/tailscale2otel/issues/83) [#123](https://github.com/rknightion/tailscale2otel/issues/123)
* **docs:** align docs site with m7kni.io brand + server-side SEO/LLM metadata ([77e7c4f](https://github.com/rknightion/tailscale2otel/commit/77e7c4f99f916b9bcc531af835726d2d6e1ce31f)), closes [#46](https://github.com/rknightion/tailscale2otel/issues/46)
* **helm:** add conditional liveness/readiness probes to deployment ([3210c9b](https://github.com/rknightion/tailscale2otel/commit/3210c9b6e320d01101831907969fce007822e686))
* **telemetry:** add GaugeSnapshot observable-gauge facade for churn-safe per-entity gauges ([ac22409](https://github.com/rknightion/tailscale2otel/commit/ac224092370efb8ff90840249727e7b5fb0164fb)), closes [#55](https://github.com/rknightion/tailscale2otel/issues/55)


### Bug Fixes

* **app:** emit enrich.cache_age at export time so staleness is detectable ([e3ef49e](https://github.com/rknightion/tailscale2otel/commit/e3ef49e5cc5f5b1ab8d2d9135aa7f2f54f2cb946)), closes [#108](https://github.com/rknightion/tailscale2otel/issues/108)
* **app:** join receivers before shutdown; attribute tailnet client failures; static node_metrics once ([f0e20d1](https://github.com/rknightion/tailscale2otel/commit/f0e20d116a4f2ce6daa07d192eabdf10b75c060e)), closes [#53](https://github.com/rknightion/tailscale2otel/issues/53) [#125](https://github.com/rknightion/tailscale2otel/issues/125) [#59](https://github.com/rknightion/tailscale2otel/issues/59)
* **app:** report dedup self-obs for every tailnet runtime, not just runtimes[0] ([eb2700d](https://github.com/rknightion/tailscale2otel/commit/eb2700d619212badec2eda143921f337e697b385)), closes [#60](https://github.com/rknightion/tailscale2otel/issues/60)
* **app:** status page reads per-tailnet identity in multi-tailnet (list) mode ([796c75b](https://github.com/rknightion/tailscale2otel/commit/796c75b442438bc79aca3ec9ab4956526785aca2)), closes [#116](https://github.com/rknightion/tailscale2otel/issues/116)
* **app:** stop double-counting self-obs under provider=headscale ([543b336](https://github.com/rknightion/tailscale2otel/commit/543b336414a376f58d76234a797f61b45e2045cb)), closes [#54](https://github.com/rknightion/tailscale2otel/issues/54)
* **auditlogs:** include action/target in the grouped boundary-dedup key ([47f0eb1](https://github.com/rknightion/tailscale2otel/commit/47f0eb1c207bb9392206678df4a0ffea4c35945e)), closes [#97](https://github.com/rknightion/tailscale2otel/issues/97)
* **collector:** checkpoint resilience + effective-store reporting + compile-time registry check ([38dfcaa](https://github.com/rknightion/tailscale2otel/commit/38dfcaa9e73f9d8ecd1b585c236f54831993cb68)), closes [#69](https://github.com/rknightion/tailscale2otel/issues/69) [#105](https://github.com/rknightion/tailscale2otel/issues/105) [#58](https://github.com/rknightion/tailscale2otel/issues/58)
* **collector:** don't count shutdown cancellation as a collector scrape failure ([0dc5ec0](https://github.com/rknightion/tailscale2otel/commit/0dc5ec00063cd568b4e78bb4aa4efa7d9f2cb6a0)), closes [#93](https://github.com/rknightion/tailscale2otel/issues/93)
* **collector:** errors.As 403 classification (flowlogs) + Headscale adapter fidelity ([3b4cbbb](https://github.com/rknightion/tailscale2otel/commit/3b4cbbbb44b9cbfd265bef1b864e7724ef1bc8b5)), closes [#95](https://github.com/rknightion/tailscale2otel/issues/95) [#64](https://github.com/rknightion/tailscale2otel/issues/64)
* **collector:** posture-log PII routing, posture status.error signal, device tags on status page ([6035361](https://github.com/rknightion/tailscale2otel/commit/603536148b40dce87fb99eff53143401bf224dfd)), closes [#56](https://github.com/rknightion/tailscale2otel/issues/56) [#99](https://github.com/rknightion/tailscale2otel/issues/99) [#102](https://github.com/rknightion/tailscale2otel/issues/102)
* **collectors:** migrate churning per-entity gauges to observable snapshots (fixes ghost series) ([479575e](https://github.com/rknightion/tailscale2otel/commit/479575e1894dcd4809012f093154e8f7a79fb6bb)), closes [#55](https://github.com/rknightion/tailscale2otel/issues/55)
* **config:** close validation gaps + backfill tailnet HTTP defaults ([4781a88](https://github.com/rknightion/tailscale2otel/commit/4781a88b2e662f5a94bf82a9a5b1758157cb77ed)), closes [#52](https://github.com/rknightion/tailscale2otel/issues/52) [#104](https://github.com/rknightion/tailscale2otel/issues/104) [#106](https://github.com/rknightion/tailscale2otel/issues/106)
* **config:** headscale receiver guard/warnings + least-privilege tailnet OAuth scopes ([45d73b8](https://github.com/rknightion/tailscale2otel/commit/45d73b81d22adafafa97b8bcfe0914bce0732ae3)), closes [#117](https://github.com/rknightion/tailscale2otel/issues/117) [#127](https://github.com/rknightion/tailscale2otel/issues/127)
* **config:** validate receiver paths, grpc endpoint shape, required tailnet; align Helm defaults ([030b185](https://github.com/rknightion/tailscale2otel/commit/030b185cb08c10a46e97066bae62ce108ea5e07f))
* **deploy:** correct broken PromQL in shipped alerts + flagship dashboard ([08d0b87](https://github.com/rknightion/tailscale2otel/commit/08d0b87db939eec4fb23b87ccc0f5762abeb0a59)), closes [#107](https://github.com/rknightion/tailscale2otel/issues/107) [#109](https://github.com/rknightion/tailscale2otel/issues/109) [#110](https://github.com/rknightion/tailscale2otel/issues/110) [#111](https://github.com/rknightion/tailscale2otel/issues/111)
* **deps:** update module github.com/grafana/pyroscope-go to v1.4.0 ([#45](https://github.com/rknightion/tailscale2otel/issues/45)) ([be6e1d9](https://github.com/rknightion/tailscale2otel/commit/be6e1d98cbbcdcf89ca2fd9976d6c79ab6f1a141))
* **ingestion:** bound wire-derived metric attribute values (audit action/origin, flow proto) ([742e6c1](https://github.com/rknightion/tailscale2otel/commit/742e6c1d4713fca71956b1ca97325d46354203f1)), closes [#77](https://github.com/rknightion/tailscale2otel/issues/77)
* **nodemetrics:** bound per-tick scrape time (worker pool + interval deadline) ([ff47ce3](https://github.com/rknightion/tailscale2otel/commit/ff47ce33dd2b01aa890cc56d0560ed6d2a815b0a)), closes [#80](https://github.com/rknightion/tailscale2otel/issues/80)
* **nodemetrics:** stable, batch-independent instance-label disambiguation ([403dd98](https://github.com/rknightion/tailscale2otel/commit/403dd9842f5fde543ed217255a1d5a5be75462ca)), closes [#98](https://github.com/rknightion/tailscale2otel/issues/98)
* **pii:** register user identity keys so per-user gauges suppress instead of collapsing ([7126f31](https://github.com/rknightion/tailscale2otel/commit/7126f3177fa09c5d2ad28947e9567be5c18510c5)), closes [#74](https://github.com/rknightion/tailscale2otel/issues/74)
* **rdns:** enforce cache bound under concurrency + fix Close()/LookupName WaitGroup race ([ec63303](https://github.com/rknightion/tailscale2otel/commit/ec63303f0a2c4ac6556475cc05e8490b94bbc9a9)), closes [#118](https://github.com/rknightion/tailscale2otel/issues/118) [#121](https://github.com/rknightion/tailscale2otel/issues/121)
* **rdns:** pick PTR name deterministically to stop flow-metric label flap ([158b29f](https://github.com/rknightion/tailscale2otel/commit/158b29fe65e18d0905204b42bdadc9557154e405)), closes [#119](https://github.com/rknightion/tailscale2otel/issues/119)
* **security/telemetry:** OTLP skip-verify knob, scrub lab identifiers, correct stale comments/descriptions ([33942fe](https://github.com/rknightion/tailscale2otel/commit/33942fe46cfcf51a112a4c5b0041a59c0ffd97a3)), closes [#94](https://github.com/rknightion/tailscale2otel/issues/94) [#89](https://github.com/rknightion/tailscale2otel/issues/89) [#51](https://github.com/rknightion/tailscale2otel/issues/51) [#65](https://github.com/rknightion/tailscale2otel/issues/65) [#68](https://github.com/rknightion/tailscale2otel/issues/68)
* **stream:** salvage the valid decoded prefix from a corrupted HEC batch ([829c09a](https://github.com/rknightion/tailscale2otel/commit/829c09a4ae0fbf8b036c5c1c53d68002933abb88)), closes [#96](https://github.com/rknightion/tailscale2otel/issues/96)
* **telemetry:** stop /metrics 500 on tailnet_name=false; classify instrument-name errors; guard const-attr collisions ([16246da](https://github.com/rknightion/tailscale2otel/commit/16246dab808c167546c384a8f941397cc5e3ce66)), closes [#103](https://github.com/rknightion/tailscale2otel/issues/103) [#91](https://github.com/rknightion/tailscale2otel/issues/91) [#62](https://github.com/rknightion/tailscale2otel/issues/62)
* **tsapi:** bound the OAuth token fetch so a hung refresh can't stall collectors forever ([086c9b5](https://github.com/rknightion/tailscale2otel/commit/086c9b5d383bcacb9e2a2251e49e4d671fc132ee)), closes [#84](https://github.com/rknightion/tailscale2otel/issues/84)
* **tsapi:** tolerate empty device timestamps; fix ServiceHost stableNodeID tag ([bb4d575](https://github.com/rknightion/tailscale2otel/commit/bb4d57534313a58915fecf4bd70f5d8cdfb64a1b)), closes [#48](https://github.com/rknightion/tailscale2otel/issues/48) [#72](https://github.com/rknightion/tailscale2otel/issues/72)


### Performance

* **telemetry:** alloc-free label-collision fast path; harden portservice gen; document Headscale ([4984ad6](https://github.com/rknightion/tailscale2otel/commit/4984ad6ea8cc16e67aa5f49b5c548f56530d5da4)), closes [#86](https://github.com/rknightion/tailscale2otel/issues/86) [#128](https://github.com/rknightion/tailscale2otel/issues/128) [#71](https://github.com/rknightion/tailscale2otel/issues/71)

## [0.5.0](https://github.com/rknightion/tailscale2otel/compare/v0.4.0...v0.5.0) (2026-06-26)


### Features

* **app:** auto-resolve tailnet name from the "-" placeholder ([f660faa](https://github.com/rknightion/tailscale2otel/commit/f660faa0292db3f60a5fa3c9055616d163a85599))
* **ci:** adversarial Tailscale API drift detection suite ([e1e5e20](https://github.com/rknightion/tailscale2otel/commit/e1e5e200d1e7c94e068c63e5a813d4f0c8998573))
* **metrics:** add opt-in Prometheus /metrics pull endpoint ([e63e4b0](https://github.com/rknightion/tailscale2otel/commit/e63e4b0473485202bbb90019948ca5777c77d63f))
* **telemetry:** emit tailnet/provider as signal attributes, off the Resource ([6cfbb52](https://github.com/rknightion/tailscale2otel/commit/6cfbb5272f8ac2b11b58444af0453f395dc83cf4))


### Bug Fixes

* **app:** hostname-free instance ID under pii_filter; validate and clamp OTLP metric interval ([81accad](https://github.com/rknightion/tailscale2otel/commit/81accad19f77c727d285c1814c70d96addf506d5))
* **audit:** bound audit-change metric labels; keep identifiers out of ACL/key log bodies ([21cd0f9](https://github.com/rknightion/tailscale2otel/commit/21cd0f9be73292fc51a131e03d3a9df282b4522c))
* **ci:** vendor OpenAPI spec to committed spec/ baseline (drift lanes need it in CI) ([4f5f665](https://github.com/rknightion/tailscale2otel/commit/4f5f6656437f2523a1cef3d23ef3ddae9bbeee06))
* **config:** redact Secret under JSON/YAML marshaling + permissions/cardinality advisories ([82d620d](https://github.com/rknightion/tailscale2otel/commit/82d620df25a2f55c9eac59a9c2723a0f463885d9))
* **grafana:** query tailnet/provider as direct labels, drop target_info joins ([78b80a0](https://github.com/rknightion/tailscale2otel/commit/78b80a0ba9315bbdda2940599aceeb5590ac09cd))
* **helm:** never render empty secret keys; serve multi-tailnet config from a Secret (chart 0.7.3) ([bffe3f1](https://github.com/rknightion/tailscale2otel/commit/bffe3f170b1cebde2fb1ad919dd7bda47dcd042b))
* **http:** bound request/response lifetimes on the admin and metrics servers ([f5a3340](https://github.com/rknightion/tailscale2otel/commit/f5a334050718fa2fa2e2cfaa5e5735a16e7584e4))
* **lint:** suppress SA5011 false-positive on t.Fatal nil-guards in tests ([15a3096](https://github.com/rknightion/tailscale2otel/commit/15a3096daf57e61563a560e9dd79edfd5325f197))
* **nodemetrics:** restrict discovery to Tailscale address ranges and cap delta baselines ([cc990fb](https://github.com/rknightion/tailscale2otel/commit/cc990fb6ab62f2b70b0a49f245162da6719a5ecf))
* **telemetry:** respect pii_filter.tailnet_name on universal const attrs; cap collision diagnostics ([79a35a7](https://github.com/rknightion/tailscale2otel/commit/79a35a71becef7a09c4d187fcf9ea06e55e312c3))
* **tsapi:** elide variable path segments from API endpoint labels ([91ef35a](https://github.com/rknightion/tailscale2otel/commit/91ef35a9a95d6526ee0894e7240753acd38a39fa))
* **tsapi:** keep OAuth token-endpoint response bodies out of traces and status ([d4276a5](https://github.com/rknightion/tailscale2otel/commit/d4276a5bbbdb169266042d928ac97adb3300f4e0))


### Performance

* **telemetry:** drop exemplar reservoirs for synchronous non-histogram instruments ([c31d7ba](https://github.com/rknightion/tailscale2otel/commit/c31d7bae4e4c160bf966a4b27f0898cd1c61d80c))

## [0.4.0](https://github.com/rknightion/tailscale2otel/compare/v0.3.0...v0.4.0) (2026-06-08)


### Features

* **app:** multi-tailnet/MSP via per-tailnet OTEL providers ([6648327](https://github.com/rknightion/tailscale2otel/commit/664832749621ce19f25a4dfb0f6fb0dcf54769bb))
* **pii:** configurable redaction filter + identifier backfill (J+K) ([8ed3ede](https://github.com/rknightion/tailscale2otel/commit/8ed3edea24f691e50a367251256d501e74a882b8))
* **provider:** add Headscale control-plane provider ([a5e48a4](https://github.com/rknightion/tailscale2otel/commit/a5e48a4a1da86f64f74cfc86070a536eac087acc))
* **telemetry:** OTLP export-duration self-observability histogram (C2) ([0ff4437](https://github.com/rknightion/tailscale2otel/commit/0ff4437602e80a62ea34224fcad8692c2e93ffa0))
* **viz:** drain dashboard/alert backlog — panels, PII rendering, multi-tailnet, taxonomy ([096ebec](https://github.com/rknightion/tailscale2otel/commit/096ebec576a03d870f0800a3fd038eb3e0934134))


### Refactoring

* **logs:** keep redactable PII identifiers out of log bodies ([dfe001a](https://github.com/rknightion/tailscale2otel/commit/dfe001a216c254ac04938f9dd656bfada36d3be7))

## [0.3.0](https://github.com/rknightion/tailscale2otel/compare/v0.2.0...v0.3.0) (2026-06-07)


### Features

* **acl:** policy risk-scoring gauges (wildcard/unrestricted/autoapprover/ssh/posture) ([de8100c](https://github.com/rknightion/tailscale2otel/commit/de8100cebbc89ea7c1024254f786b88a4704c29b))
* **audit:** curated security/lifecycle change counter + device churn ([98bcc48](https://github.com/rknightion/tailscale2otel/commit/98bcc4819be966e1699a07ffadb3792c4430f3f9))
* **collector:** per-collector scrape staleness + budget-headroom gauges ([f43fdd8](https://github.com/rknightion/tailscale2otel/commit/f43fdd84315130a28abc99a0fc1d9f637f920f2b))
* **devices,flowlogs:** connectivity quality + exit-node/subnet-router analytics (B3+B4) ([f58b73b](https://github.com/rknightion/tailscale2otel/commit/f58b73bc3ace0c3a45ad998a7475f1093cd8dfe5))
* **devices:** fleet hygiene roll-ups (untagged/ephemeral/version/tag distributions + key-expiry histogram) ([da158e3](https://github.com/rknightion/tailscale2otel/commit/da158e3a53eef66e1465997a23cc5eec0f292c12))
* **devices:** inventory outstanding device-share invites ([5bdfa85](https://github.com/rknightion/tailscale2otel/commit/5bdfa855b831af724d431b1dc048df52a62dbb03))
* **dns:** unified DNS configuration with override-local + per-resolver exit-node visibility ([b51b5dc](https://github.com/rknightion/tailscale2otel/commit/b51b5dcd62105d878763eb0d534f3a58b09a4ec3))
* **keys:** inventory OAuth clients & API tokens via the unified key model ([4a8a3f7](https://github.com/rknightion/tailscale2otel/commit/4a8a3f70a81e20f719d2fc7fbf7c2e196f7c0529))
* **selfobs:** API request latency histogram (api.duration) ([756b042](https://github.com/rknightion/tailscale2otel/commit/756b042b5d5ea879f2f4f2845c84b694b6990260))
* **selfobs:** ingestion volume + OTLP export-cost self-observability (C8) ([d517fa0](https://github.com/rknightion/tailscale2otel/commit/d517fa0d7dbc93b8014ad1eb020e78b3b2ddb539))
* **selfobs:** receiver in-flight/duration, dedup hits, checkpoint/process/config health (C6/C7/C9) ([e09ffcc](https://github.com/rknightion/tailscale2otel/commit/e09ffccd1c40dfc6f647dc1f30db5f112409712b))
* **telemetry:** cardinality headroom — series.limit + series.overflowing self-obs gauges ([be7be05](https://github.com/rknightion/tailscale2otel/commit/be7be052c1274eb4da990f4790c141db66d1e111))
* **telemetry:** OTEL traces pillar — scrape/API/receiver spans + exemplars ([e8b78ee](https://github.com/rknightion/tailscale2otel/commit/e8b78eebd5dc01bef5ce5a765be57b5f888294df))
* **version:** self update-available + device version-skew via shared release fetcher ([02a47d1](https://github.com/rknightion/tailscale2otel/commit/02a47d1fff96959a7f734cfa514e7bb5df1e2bfb))


### Bug Fixes

* **docs:** remove glightbox slide_effect option (rejected by zensical 0.0.44) ([5083835](https://github.com/rknightion/tailscale2otel/commit/50838350c1486e6b895ef2f880ea18a27ebba99c))
* **keys:** correct stale docs and keys-by-type dashboard aggregation ([b9420c9](https://github.com/rknightion/tailscale2otel/commit/b9420c971522eaf55f00318b721b80815e364dc3))
* **selfobs:** drop unnecessary int64 conversion in tvToSeconds (unconvert) ([012307f](https://github.com/rknightion/tailscale2otel/commit/012307fc72330320f32188ec2764a6ff41c22090))

## 0.2.0 (2026-06-06)


### ⚠ BREAKING CHANGES

* **config:** restructure schema, env-driven loader, generated env-var reference
* node-metrics series now carry the scraped node's identity on the `tailscale_node` label instead of `instance` (which on Grafana Cloud always held the collector host); update node-metrics dashboards/queries accordingly. The per-device posture log now defaults to on-change; set

### Features

* **admin:** add status landing page, JSON API endpoint, and opt-in profiling (pprof + Pyroscope) ([282a333](https://github.com/rknightion/tailscale2otel/commit/282a33341b5c31da979b7c5da098640e5c1593b4))
* **admin:** authenticate status page + pprof with a shared token ([bbfea01](https://github.com/rknightion/tailscale2otel/commit/bbfea01032c68cdb718df2baca5a45da55676c0f))
* **admin:** per-collector info tooltip on status page ([5bfd025](https://github.com/rknightion/tailscale2otel/commit/5bfd025087e4fdb6b60f2c9c1536b24545e324c7))
* **alerts:** add Grafana-managed alert + recording rules ([a49dab0](https://github.com/rknightion/tailscale2otel/commit/a49dab0b4f8ef58f4ea2aa07ba2637431cc8c60f))
* **app:** derive overall health + enrich collector status rows ([e3f86b8](https://github.com/rknightion/tailscale2otel/commit/e3f86b8ff3cf0867ed69b70f620bfa27fce99266))
* **app:** redesign admin status page — health, sparklines, API panel, live tables ([e7a26d5](https://github.com/rknightion/tailscale2otel/commit/e7a26d5b0c963ef0e97ee42bedf9789920bf7bc3))
* **app:** sample runtime/cardinality trends for status sparklines ([b03d4a1](https://github.com/rknightion/tailscale2otel/commit/b03d4a154adad17ad6565550ae944ca08a24f5ac))
* **app:** start the series.active cardinality reporter, gated by self-obs ([a9db840](https://github.com/rknightion/tailscale2otel/commit/a9db8407ea342e355841b3a39a19cda3244fcf28))
* **app:** surface per-endpoint API health and window checkpoint state ([66359f6](https://github.com/rknightion/tailscale2otel/commit/66359f602908d51ea1819d473c521df2c89e8b45))
* **app:** tag subsystem loggers with component for per-subsystem filtering ([da75818](https://github.com/rknightion/tailscale2otel/commit/da75818451d5c2887de8c02cb3c2b10fb4ee7f48))
* **app:** wire dynamic node-metrics discovery from the devices API ([3900f89](https://github.com/rknightion/tailscale2otel/commit/3900f893169a53cc0c6fc72b5745b344e7cbda5a))
* **app:** wire node-metrics passthrough filters into nodeMetricsOptions ([1c42f81](https://github.com/rknightion/tailscale2otel/commit/1c42f812ad5711d5bde7f627e256b61f6de5d3c3))
* bounded top-N flow-metric rollups (default) with __other__ + unique counts ([d8bcbb8](https://github.com/rknightion/tailscale2otel/commit/d8bcbb884d3dc0cce7e63892377f4d92f6e3dc68))
* cardinality cap, stream feature.enabled, posture metric, node-label fix ([d3e5494](https://github.com/rknightion/tailscale2otel/commit/d3e54949df97be0c084aaa728fb8cfd3c1397e17))
* **cardinality:** per-entity gauge toggles for devices/users/keys ([389352f](https://github.com/rknightion/tailscale2otel/commit/389352fccb416fcb8c6c725e6d1892b80f74721b))
* **collector:** track per-collector run history and consecutive failures ([4f7e5ca](https://github.com/rknightion/tailscale2otel/commit/4f7e5ca46f2d4531199ca73df4f152fcbedf9bd7))
* **config:** add node_metrics.discovery schema ([7b29868](https://github.com/rknightion/tailscale2otel/commit/7b2986889cc6e8abc4b4dc585ea7785549e9d574))
* **config:** document new collectors + cardinality toggles (config + Helm chart) ([fb55c8c](https://github.com/rknightion/tailscale2otel/commit/fb55c8cd1c2999f486804fd1f5b3313560fe2ffe))
* **config:** redact credential fields via a Secret type ([987de8f](https://github.com/rknightion/tailscale2otel/commit/987de8fa1f7202567858246e8447392b41da3454))
* **config:** restructure schema, env-driven loader, generated env-var reference ([0891d26](https://github.com/rknightion/tailscale2otel/commit/0891d26133881fb39f351c116d74e4a104b6fd67))
* **config:** warn on undefined ${ENV} references at load ([d10b3cb](https://github.com/rknightion/tailscale2otel/commit/d10b3cbba4f8d005e90555033ecf568dd5b945b3))
* **contacts:** add tailnet contact verification collector ([9ddbc66](https://github.com/rknightion/tailscale2otel/commit/9ddbc66ec9ca72d57606e92219be36e98e8765bd))
* **devices:** add tailnet-lock errors + per-DERP-region rollup ([dbbcd19](https://github.com/rknightion/tailscale2otel/commit/dbbcd19461a725328931a6ba0c77f0ce7ddcb1d3))
* **devices:** add tailscale.tags label to per-device gauges ([3c8c5d1](https://github.com/rknightion/tailscale2otel/commit/3c8c5d18ce55448d71185c66ef42b082e5765ade))
* **devices:** expose MDM/posture attributes as queryable metrics ([e3eb199](https://github.com/rknightion/tailscale2otel/commit/e3eb199a31a1182e92976feac5773a8689fce942))
* flow-log service-name mapping, independent port toggles, external reverse-DNS ([0835122](https://github.com/rknightion/tailscale2otel/commit/08351221c3d5e1f8aa42247ca0f7fc209330fa99))
* **grafana:** add Cardinality & Cost tab ([1a93a1e](https://github.com/rknightion/tailscale2otel/commit/1a93a1e455d3c84056dcef8384aa22667189d178))
* **grafana:** add comprehensive v2-schema multi-tab dashboard (generated) ([843f1e0](https://github.com/rknightion/tailscale2otel/commit/843f1e05140e58b769c6bd731c7ef1f7c5002845))
* **grafana:** add DERP-vs-direct connection-path row to Node Metrics tab ([0a47685](https://github.com/rknightion/tailscale2otel/commit/0a4768507419d56b2d3f0eb4dd90dc9498a55ad8))
* **grafana:** add Security & Audit tab ([027c9fb](https://github.com/rknightion/tailscale2otel/commit/027c9fb8cfe2c4d043226edc77a88387ef452671))
* **grafana:** add tag filter and Devices-by-tag panel to Fleet tab ([ce86f71](https://github.com/rknightion/tailscale2otel/commit/ce86f71f6239b00493032ae72c8d091f9cbff04b))
* **grafana:** dashboard coverage for new collectors (3131e672+) ([ec527f6](https://github.com/rknightion/tailscale2otel/commit/ec527f632e60e17ac89d45f83ee70eabe06b5fd1))
* **grafana:** surface alloc churn, heap objects, GC next-target in Diagnostics ([e4c52f1](https://github.com/rknightion/tailscale2otel/commit/e4c52f1a40eda10a3e7c2d11891a80d6d1352933))
* **helm:** expose collectors.devices.attribute_namespaces ([1dfa89e](https://github.com/rknightion/tailscale2otel/commit/1dfa89e6478e65e78b704e24ee3bf68b66fed6a9))
* **logstream:** add log-stream delivery-health collector ([a0b259b](https://github.com/rknightion/tailscale2otel/commit/a0b259bf77d0aa811293bf382dce12735ff55422))
* **nodemetrics:** add metric_allow/metric_deny/drop_labels passthrough filters ([603790c](https://github.com/rknightion/tailscale2otel/commit/603790c0ef8873ad7df77509b86604aa03546283))
* **nodemetrics:** emit discovery-health gauges ([cbb4831](https://github.com/rknightion/tailscale2otel/commit/cbb4831329c94a7d96e2f3d5fa98ad67dc00d632))
* **nodemetrics:** support dynamic target discovery ([1b86831](https://github.com/rknightion/tailscale2otel/commit/1b86831719463444c4cc492833c4665c34b380d8))
* **posture:** add device-posture integration sync-health collector ([3131e67](https://github.com/rknightion/tailscale2otel/commit/3131e6728ca81f8424d179f7157ad428f0cadbfe))
* **rdns:** observability, purge control, and larger defaults for the PTR cache ([a8b8867](https://github.com/rknightion/tailscale2otel/commit/a8b88677de64ae70a6ddd9f07ff68e82569363a5))
* **ringbuf:** add generic thread-safe bounded ring buffer ([14c01c7](https://github.com/rknightion/tailscale2otel/commit/14c01c7047ba44a9efe4ed3018e38fadec33498e))
* **selfobs:** add runtime, dedup, and component-error self-observability metrics ([b0fa95f](https://github.com/rknightion/tailscale2otel/commit/b0fa95f6de9a9c9d0fc267d024f8a6094235637e))
* **services:** add Tailscale Services (VIP) collector ([30900f4](https://github.com/rknightion/tailscale2otel/commit/30900f41a0222c7e1331be84db0674420dc9f005))
* **settings:** surface httpsEnabled, aclsExternallyManaged & external-tailnets role ([667e4e7](https://github.com/rknightion/tailscale2otel/commit/667e4e787c522e6994bb97ef9dcc3a81039e4148))
* **telemetry:** add tailscale2otel.series.active cardinality self-metric ([918ca76](https://github.com/rknightion/tailscale2otel/commit/918ca76e654348b2e9333fa3b4abd84a8a810b26))
* **tsapi:** add equal-jitter to retry backoff ([62f73ca](https://github.com/rknightion/tailscale2otel/commit/62f73cad8d719c16d87f71680936469278f017c3))
* **tsapi:** decode per-device tags from /devices?fields=all ([6e7906a](https://github.com/rknightion/tailscale2otel/commit/6e7906a24401a2cf4ce3fcf396823883ce007d54))
* **tsapi:** honor HTTP-date form of Retry-After ([8e0ce6e](https://github.com/rknightion/tailscale2otel/commit/8e0ce6e9af69fa3f0bfd6bedf2a1bb6618c02523))
* **tsapi:** per-attempt timeout so long Retry-After is honored ([85c3584](https://github.com/rknightion/tailscale2otel/commit/85c35846ae6304226d46dcd9ea7f2d26ecf51d6d))
* **tsapi:** rate-limit retries, not just first attempt ([87107a1](https://github.com/rknightion/tailscale2otel/commit/87107a1bb2b7608952bbcbd29bf957b54cbee863))
* **tsapi:** status-aware retry logging (429 INFO, 5xx DEBUG, 401 ERROR) ([65403c8](https://github.com/rknightion/tailscale2otel/commit/65403c83e69e5a5d11b94b89f39bd6f4d033f348))
* **tsapi:** widen request hook to RequestInfo (latency + error) ([4d89430](https://github.com/rknightion/tailscale2otel/commit/4d89430541effdd0b5ad0fc23f15ba44bec11779))
* **webhooks:** add webhook-endpoint inventory collector ([8931eb9](https://github.com/rknightion/tailscale2otel/commit/8931eb96dd7e823e532b16ec03283ee3e1612cbe))


### Bug Fixes

* **app:** don't log receiver clean shutdown as ERROR ([0db54c8](https://github.com/rknightion/tailscale2otel/commit/0db54c8a093239b713e63d5507c47721b0a07158))
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
* **collector:** run first collector tick promptly at startup ([2c72ee3](https://github.com/rknightion/tailscale2otel/commit/2c72ee3296720cf0f9a5728c28d4c62c046da1c0))
* **config:** scope the undefined-${ENV} advisory to active config values ([d6809f8](https://github.com/rknightion/tailscale2otel/commit/d6809f8dc5491938b4e860089a18a50185cfb9e6))
* **deps:** update github.com/tailscale/hujson digest to ecc657c ([d9843a0](https://github.com/rknightion/tailscale2otel/commit/d9843a0c6eee1b5b02e2e23759ca48d3b32649b6))
* **docker:** copy per-platform binary in dockers_v2 multi-arch build ([f780ca5](https://github.com/rknightion/tailscale2otel/commit/f780ca545507fdd9efa6551cb3558cb0c76da2ed))
* **docs:** redact live tailnet recon details from tracked files ([5ded0e6](https://github.com/rknightion/tailscale2otel/commit/5ded0e6918620a3f3376247952fc4a49116c7d11))
* **flowlog:** bound rollup accumulator memory between flushes ([629b111](https://github.com/rknightion/tailscale2otel/commit/629b1112858e54de9f78481c16bf77775b5a3a8f))
* **grafana:** correct policy/config, network & diagnostics panels ([4bfd717](https://github.com/rknightion/tailscale2otel/commit/4bfd7178a0041bfd36312bda10b10297e32bf914))
* **grafana:** show 0 not "No data" for empty audit-count stats ([a0c26a2](https://github.com/rknightion/tailscale2otel/commit/a0c26a2d1b11a623cce84ba2c1a9ab61e9ab04c1))
* **grafana:** stabilize panels across redeploys (strip service_version) ([2224bce](https://github.com/rknightion/tailscale2otel/commit/2224bcee8cfe3a4bc18751ee5711cfe16f7d2811))
* guard main snapshot publishing ([44ee52e](https://github.com/rknightion/tailscale2otel/commit/44ee52e393a8a4fde1a8311d6497836b09489094))
* **helm:** disable ServiceAccount token automount by default ([289a0fd](https://github.com/rknightion/tailscale2otel/commit/289a0fdc696886910d217c3b40486505da931bed))
* **nodemetrics:** bound discovered scrape work ([2770030](https://github.com/rknightion/tailscale2otel/commit/277003093a457d9fab9e96f3c8f6565199879f76))
* **nodemetrics:** unique short MagicDNS instance labels + collision guard ([f578e54](https://github.com/rknightion/tailscale2otel/commit/f578e549f42f3450fa296e582edaa65d251b7a10))
* reserve node metrics identity label ([#16](https://github.com/rknightion/tailscale2otel/issues/16)) ([d439c38](https://github.com/rknightion/tailscale2otel/commit/d439c38ca9924c8369fe1e1cfe30ef16c2ec4067))
* restrict main snapshot publishing to main ref ([1e58858](https://github.com/rknightion/tailscale2otel/commit/1e588584d88c7478829b0d45aaab3e47b934f1e5))
* **security:** harden receivers, scraper, TLS, and Helm from security review ([b743858](https://github.com/rknightion/tailscale2otel/commit/b743858103015ecc9d3e176fe820ed038b11235c))
* **selfobs:** guard cardinality reporter against non-positive interval to prevent panic ([cf1d7f4](https://github.com/rknightion/tailscale2otel/commit/cf1d7f4626c7f314994dd5318ff2c4aaec29583f))
* **stream:** cap zstd decoder back-reference window at the body limit ([bfde16b](https://github.com/rknightion/tailscale2otel/commit/bfde16bd55fbab295650062c4b4e056cc9ef9473))
* **telemetry:** drop OTLP→Prometheus colliding labels and log export errors ([874cf1b](https://github.com/rknightion/tailscale2otel/commit/874cf1bb4f2fa1c6ae9fa0c9f00a4441641eafe0))
* **telemetry:** stop emitting redundant service.version on build_info ([d82d71d](https://github.com/rknightion/tailscale2otel/commit/d82d71d72084788942213149d61d3eabe7cd50e2))
* **webhook:** bound request bodies pre-auth and add server timeouts ([92348f4](https://github.com/rknightion/tailscale2otel/commit/92348f4517fac63295aa2ad2edeaaeb661f940bd))
* **webhook:** stop user cross-dedup over-suppressing distinct changes (D11) ([75a2c98](https://github.com/rknightion/tailscale2otel/commit/75a2c98ad245b4c87b449e03b5b8f006cb0de759))
* **webhook:** wire replay-protection tolerance from config (default 5m) ([7ce9cf6](https://github.com/rknightion/tailscale2otel/commit/7ce9cf66cc0e7a1484ad851451abf553dc45c8dc))


### Performance

* **telemetry:** disable unused metric exemplars, add GC tuning knobs ([5e6fce3](https://github.com/rknightion/tailscale2otel/commit/5e6fce32f1c7c1763f08749ea0273e06515e1a9b))


### Refactoring

* **config:** remove dead oauth token_url field ([d21f11c](https://github.com/rknightion/tailscale2otel/commit/d21f11c5b306696e89d97bb2ea874d0281f061e7))
* **config:** remove legacy cardinality.flow_include_ports toggle ([6bc1a56](https://github.com/rknightion/tailscale2otel/commit/6bc1a5647ceb1082123f32e1675de605ab68cade))
* **tsapi:** use min() in computeBackoff ([3e58f5f](https://github.com/rknightion/tailscale2otel/commit/3e58f5f34440eb1f7057e79b02410bab028e87fd))


### Miscellaneous

* **release:** make 0.2.0 the first complete release ([ec62fb1](https://github.com/rknightion/tailscale2otel/commit/ec62fb1b55cec270cd36c7def89f72e3c42687b5))
* **release:** set initial release version to 0.1.0 ([8f1a18e](https://github.com/rknightion/tailscale2otel/commit/8f1a18e1988a268e0996e992ea40b28c91f1b977))
