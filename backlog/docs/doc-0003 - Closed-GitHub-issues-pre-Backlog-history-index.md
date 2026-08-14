---
id: doc-0003
title: Closed GitHub issues (pre-Backlog history index)
type: other
created_date: '2026-08-14 14:04'
updated_date: '2026-08-14 15:44'
---
> **Historical index of work tracked on GitHub Issues before this repo moved to Backlog.md on
> 2026-08-14.** The issues themselves were **deleted from GitHub** on that date, so `gh issue view
> <N>` will 404. Their full bodies and all 481 replies are archived in
> `archive/github-issues-2026-08-14.json` — that file is the record, this table is the index into it.
> The load-bearing detail (closing decisions, corrections, acceptance evidence) is in the comments,
> so read the archive, not just this table:
>
> ```sh
> jq '.[] | select(.number == 526)' archive/github-issues-2026-08-14.json
> ```
>
> The archive is redacted — host names, tailnet addresses and account identifiers were replaced with
> stable placeholders before it was committed. `archive/README.md` has the mapping.

**Why these were not imported as tasks.** Backlog IDs follow creation order, so an imported task
could never carry the number the history already cites. `CLAUDE.md`, the commit log and code
comments all reference this work as `#NNN`; keeping GitHub numbers as the only ID space over this
history is what keeps those references resolvable. Four hundred `Done` rows would also drown the
board's only real signal — what is left. Cite closed work as `#NNN`; cite new work as `tso-NNNN`.

**The commits column is every commit whose message cites the issue**, newest first, capped at
three. It is a lead, not a verdict: a commit may cite an issue it only touches, and a squashed or
un-citing commit leaves the column empty. 373 of 401 rows resolved to at least one commit.

`~` in the outcome column marks an issue closed as **not planned** (11 of 401) — it records a
decision *not* to do something, which is usually the more expensive fact to re-derive.

| # | closed | outcome | title | commits |
| --- | --- | --- | --- | --- |
| 552 | 2026-08-10 |  | Live Tailscale API contract failing | `47382f7` |
| 551 | 2026-08-08 |  | docs: migrate to the m7kni.io inverted docs model (content + manifest) | `02e30b3` |
| 539 | 2026-08-10 |  | New Tailscale changelog entries may be observability surfaces | `ac187aa`, `9cf94be` |
| 529 | 2026-08-01 |  | Alert rules trip on a firewall reboot: floor 'for' at 10m, and drop NoData on the burn-rate rules | `b399957` |
| 528 | 2026-08-02 |  | Live Tailscale API contract failing |  |
| 527 | 2026-07-31 |  | Coverage gate counts a template-variable query as "visualized" | `c54e0df` |
| 526 | 2026-07-31 |  | Dashboard re-architecture: split product from self-observability, section-scoped variables, hard coverage gate | `c54e0df`, `04808e8`, `c300199` +10 |
| 525 | 2026-07-31 |  | flow cardinality: source_port/destination_port and rollup_top_n are silently inert under the wrong metrics_mode | `681fe1e` |
| 524 | 2026-07-31 |  | status page: capability matrix cramped, State column mostly unknown, no why-not-polling reason, /flows and /events invisible and dead-ended | `681fe1e`, `42d559a`, `8b674af` +1 |
| 523 | 2026-07-31 |  | acl policy validation has never validated anything: empty-body POST 400s and misreports as transient_failure | `1e488ef` |
| 521 | 2026-07-30 |  | CI is red across main, the release PR and the Renovate queue (four independent causes) | `12a371b`, `a78961e`, `6ca7c86` +2 |
| 519 | 2026-07-29 |  | fix(telemetry): duplicate target_info makes every Prometheus scrape drop series (single-tailnet only) | `844f267` |
| 518 | 2026-07-29 |  | feat(annotations): publish curated tailnet events to Grafana as annotations | `a51d901`, `8d2ab97` |
| 517 | 2026-07-29 |  | Publish the dashboard and alert rules to the m7kni stack from CI, and fix the colliding dashboard UID | `ea04e0b` |
| 512 | 2026-07-28 |  | Complete accessibility and keyboard support for the /events explorer | `25e5b2b`, `66ac88d` |
| 511 | 2026-07-30 |  | docs: record why no log attribute here can be promoted to a Loki index label |  |
| 498 | 2026-07-27 |  | fix(objectstore): a leading-slash prefix erases durable listing progress every cycle | `2fb0c45` |
| 497 | 2026-07-27 |  | fix(objectstore): quarantine an object whose every row fails to decode | `4f670ab` |
| 496 | 2026-07-27 |  | fix(objectstore): accept .json.zstd and .json.gzip export keys | `3df1b32` |
| 495 | 2026-07-27 |  | P0: Make Grafana v2 dynamic-dashboard capabilities first-class before expanding coverage | `e0e20b9`, `9a3674c`, `8f36ffd` |
| 494 | 2026-07-27 |  | Prevent cumulative OTLP metric payloads exceeding backend request limits | `6b1093a` |
| 492 | 2026-07-28 | ~ | Config-reloader sidecar for existingSecret rotation, once hot reload lands | `15b1038` |
| 491 | 2026-07-25 |  | Checkpoint staging file is not swept after a hard process kill | `15b1038` |
| 490 | 2026-07-25 |  | Promote error.type to internal/semconv instead of three local literals | `15b1038` |
| 489 | 2026-07-25 |  | Token-endpoint 401/403 is still retried MaxAttempts times despite being classified non-retryable | `15b1038` |
| 488 | 2026-07-25 |  | internal/hsapi has the same post-hoc JSON decode cap as tsapi did | `15b1038` |
| 486 | 2026-07-25 |  | Tracker: public security, privacy, and secret-safety hardening | `15b1038`, `476f236` |
| 485 | 2026-07-29 |  | Tracker: ecosystem integration and experimental signal research |  |
| 484 | 2026-07-27 |  | Tracker: API drift, CI, generated artifacts, and release engineering |  |
| 483 | 2026-07-28 |  | Tracker: deployment, packaging, and Kubernetes operations |  |
| 482 | 2026-07-29 |  | Tracker: configuration, admin UI, and operator experience | `0d31d9f` |
| 481 | 2026-07-27 |  | Tracker: Grafana dashboards, rules, and exporter self-observability | `5841ebd`, `8f36ffd` |
| 480 | 2026-07-29 |  | Tracker: telemetry export, Prometheus, tracing, and profiling | `0084290`, `0a3536e`, `1e63883` +1 |
| 479 | 2026-07-26 |  | Tracker: Tailscale API and collector fidelity | `2b48098`, `7445cc6`, `da772f5` |
| 478 | 2026-07-27 |  | Tracker: durable object-store ingestion, backfill, and cloud providers |  |
| 477 | 2026-07-26 |  | Tracker: trustworthy flow, audit, webhook, and receiver semantics | `d5e643d` |
| 476 | 2026-07-25 |  | Exclude local secrets, captures, configs, and checkpoints from Docker build context | `476f236` |
| 475 | 2026-07-25 |  | Git-ignore the documented Compose credential file | `476f236` |
| 474 | 2026-07-25 |  | Bound API JSON decoding memory below supported deployment limits | `15b1038`, `476f236` |
| 473 | 2026-07-25 |  | Apply the free-text PII policy to span status descriptions | `476f236` |
| 472 | 2026-07-25 |  | Make log-body PII redaction deterministic for overlapping sensitive values | `476f236` |
| 471 | 2026-07-25 |  | Use randomized symlink-safe temporary files for checkpoint replacement | `15b1038`, `476f236` |
| 470 | 2026-07-25 |  | Prevent inline Helm configuration credentials from landing in ConfigMaps | `a51d901`, `e01d6c6`, `476f236` |
| 469 | 2026-07-25 |  | Make `existingSecret` rotation restart or reload affected pods | `476f236` |
| 468 | 2026-07-25 |  | Sanitize OAuth token error bodies before retry logging | `15b1038`, `476f236` |
| 467 | 2026-07-25 |  | Prevent workload-identity JWT replay across token-exchange redirects | `476f236` |
| 466 | 2026-07-25 |  | Keep Tailscale API bearer credentials on the configured origin across redirects | `476f236` |
| 465 | 2026-07-25 |  | Fail closed for unclassifiable semantic IP values and normalize IPv4-mapped addresses | `476f236` |
| 464 | 2026-07-25 |  | Reject future-dated object keys before advancing the durable cursor | `476f236` |
| 463 | 2026-07-27 |  | docs: reconcile ingestion-source, backfill, and acknowledgement claims with runtime behavior | `639ca34` |
| 462 | 2026-07-29 |  | research(tsrecorder): determine whether safe session metadata can be exported | `0e02a05`, `b61fe7a` |
| 461 | 2026-07-28 |  | feat(flowlogs): add optional local GeoIP and ASN enrichment | `47d9e49` |
| 460 | 2026-07-29 |  | research(aperture): ingest privacy-safe usage aggregates from Tailscale Aperture S3 exports |  |
| 459 | 2026-07-29 |  | docs(ecosystem): submit tailscale2otel to Tailscale Community Projects |  |
| 458 | 2026-07-28 |  | feat(helm): add an opt-in Prometheus ServiceMonitor | `9ade22f`, `e01d6c6` |
| 457 | 2026-07-28 |  | feat(otlp): make metric temporality selectable |  |
| 456 | 2026-07-26 | ~ | research(webhook): verify timestamp tolerance against 24-hour retries, then correct replay handling |  |
| 455 | 2026-07-26 |  | feat(objectstore): add an Azure Blob Storage backend |  |
| 454 | 2026-07-26 |  | feat(objectstore): add a native Google Cloud Storage backend |  |
| 453 | 2026-07-26 |  | refactor(objectstore): create a provider-neutral, multi-signal ingestion engine | `2fb0c45`, `2018f07`, `6709c54` +2 |
| 452 | 2026-07-27 |  | feat(s3): support AWS container credential providers | `26bb03c` |
| 451 | 2026-07-27 |  | feat(objectstore): expose provider request and cursor-health telemetry | `6709c54` |
| 450 | 2026-07-27 |  | fix(selfobs): correct object-store ingest source and byte semantics | `cc6e498` |
| 449 | 2026-07-26 |  | fix(config): protect object-store static credentials with Secret and _file semantics | `ddef918`, `3260963` |
| 448 | 2026-07-26 |  | fix(objectstore): make object processing atomic across parser failures | `f3e871e` |
| 447 | 2026-07-26 |  | fix(objectstore): validate and support the official Tailscale object-key contract | `9b9f7a3` |
| 446 | 2026-07-27 |  | Add an API/changelog opportunity review workflow | `cfc60b9` |
| 445 | 2026-07-27 |  | Add documentation-command and installation smoke tests | `7cd9a84`, `123fe72` |
| 444 | 2026-07-27 |  | Add structured GitHub issue forms | `ea9f9bf` |
| 443 | 2026-07-27 | ~ | Gate fixable high-severity container vulnerabilities before tag promotion |  |
| 442 | 2026-07-27 |  | Make releases atomically visible with a complete asset manifest | `3c413e1` |
| 441 | 2026-07-27 |  | Add cardinality and flow hot-path performance regression gates | `700e974` |
| 440 | 2026-07-27 |  | Schedule IANA service-registry freshness | `a0fdd75` |
| 439 | 2026-07-27 |  | Add workflow timeouts, concurrency, and bounded retry policy | `d3cd115` |
| 438 | 2026-07-27 |  | Gate every generated dashboard and rule artifact | `a855dc7`, `8ee2ac5` |
| 437 | 2026-07-27 |  | Make all four Go modules first-class verification units | `607b32a`, `dad27b1` |
| 436 | 2026-07-27 |  | Reconcile API-drift documentation and workflow reality | `a855dc7`, `54caa5e` |
| 435 | 2026-07-27 |  | Aggregate client-library compatibility failures before issue upsert | `b695f27` |
| 434 | 2026-07-27 |  | Add native fuzzing for webhook and OpenAPI parser boundaries | `6165a55` |
| 433 | 2026-07-27 |  | Add a valid-boundary fixture matrix for every consumed decoder | `f880ffe` |
| 432 | 2026-07-27 |  | Detect parameter, request, success-status, and media-type drift | `ffb7a95` |
| 431 | 2026-07-27 |  | Support OpenAPI 3.1 unions, typed maps, and composition in drift analysis | `607b32a` |
| 430 | 2026-07-26 |  | Add explicit configuration-to-capability status | `7445cc6`, `da772f5` |
| 429 | 2026-07-26 |  | Preserve documented node-metric drop and peer-relay labels | `2b48098`, `7445cc6`, `da772f5` |
| 428 | 2026-07-26 |  | Continuously validate the active ACL policy and embedded tests | `7445cc6` |
| 427 | 2026-07-26 |  | Add Linux distribution and codename inventory | `7445cc6` |
| 426 | 2026-07-26 |  | Add bounded entity lifecycle/age signals | `7445cc6`, `da772f5` |
| 425 | 2026-07-26 |  | Preflight requested OAuth scopes against enabled collectors | `7445cc6`, `da772f5` |
| 424 | 2026-07-26 |  | Exercise real path parameters in live contract CI | `fb4f877` |
| 423 | 2026-07-26 |  | Detect newly added upstream read operations automatically | `cfc60b9`, `fb4f877` |
| 422 | 2026-07-26 |  | Add a consumed-field disposition contract | `1d2203b`, `fb4f877` |
| 421 | 2026-07-26 |  | Expose per-device subrequest coverage and partial failure | `7445cc6`, `da772f5` |
| 420 | 2026-07-26 |  | Distinguish intentionally unavailable optional APIs from broken authentication | `2b48098`, `7445cc6`, `da772f5` |
| 419 | 2026-07-26 |  | Improve OAuth Apps lifecycle and posture telemetry | `7445cc6` |
| 418 | 2026-07-26 |  | Expose whether an external ACL policy link is configured | `7445cc6` |
| 417 | 2026-07-26 |  | Report webhook event-level subscription coverage | `7445cc6` |
| 416 | 2026-07-26 |  | Expose top-level trust-credential allowed-tag blast radius | `2b48098`, `7445cc6` |
| 415 | 2026-07-26 |  | Classify credential privilege by scope semantics | `b3572e8`, `2b48098`, `7445cc6` +1 |
| 414 | 2026-07-26 |  | Export Tailscale SSH and key-expiry-disabled posture | `7445cc6` |
| 413 | 2026-07-26 |  | Expose device-share invite age and resend state | `7445cc6` |
| 412 | 2026-07-26 |  | Expose user-invite age and delivery state | `7445cc6` |
| 411 | 2026-07-26 |  | Correct user-invite telemetry to match the open-invites endpoint | `7445cc6` |
| 410 | 2026-07-27 |  | Add approval-workflow alerts | `623240e`, `99c1226` |
| 409 | 2026-07-27 | ~ | Publish versioned dashboards to the Grafana dashboard catalog |  |
| 408 | 2026-07-27 |  | Add post-deployment dashboard/rule smoke verification | `ea53c30` |
| 407 | 2026-07-27 |  | Validate dashboard and rule queries with real parsers and synthetic fixtures | `e54a4cd`, `5841ebd` |
| 406 | 2026-07-27 |  | Expand and reuse canonical recording rules | `623240e` |
| 405 | 2026-07-27 |  | Cover receiver/rDNS loss and overflow signals | `99c1226`, `9a3674c` |
| 404 | 2026-07-27 |  | Add API rate-limiter wait visibility | `99c1226`, `9a3674c` |
| 403 | 2026-07-27 |  | Add OAuth app, webhook subscription, and log-stream inventory views | `9a3674c` |
| 402 | 2026-07-27 |  | Add node packet-drop and peer-relay diagnostics | `99c1226`, `9a3674c` |
| 401 | 2026-07-27 |  | Add device connectivity and security-posture views | `99c1226`, `9a3674c` |
| 400 | 2026-07-27 |  | Add cross-source ingestion freshness and delivery-delay views | `db37106` |
| 399 | 2026-07-27 |  | Add object-store ingestion health panels and alerts | `99c1226`, `9a3674c` |
| 398 | 2026-07-27 |  | Add exporter availability/freshness SLO recording and burn-rate rules | `623240e` |
| 397 | 2026-07-27 |  | Benchmark and reduce the 298-panel flagship query load | `e0e20b9`, `ea53c30` |
| 396 | 2026-07-27 |  | Add dashboard accessibility and visual-regression gates | `ea53c30` |
| 395 | 2026-07-27 |  | Add prerequisite-aware empty states throughout the flagship | `db37106` |
| 394 | 2026-07-27 |  | Define and enforce the lifecycle of flagship versus legacy dashboards |  |
| 393 | 2026-07-27 |  | Modernize audit/integration dashboards for multi-tailnet and optional-source semantics | `9a3674c` |
| 392 | 2026-07-27 |  | Modernize fleet dashboards for authorization, external-device, and multi-tailnet state | `9a3674c` |
| 391 | 2026-07-27 |  | Modernize the legacy network dashboard for rollup-first operation | `9a3674c` |
| 390 | 2026-07-27 |  | Add a catalog-to-dashboard/rule disposition manifest | `9a3674c`, `1d2203b` |
| 389 | 2026-07-27 |  | Generate installable alert profiles and threshold overrides | `fec022a`, `ea53c30` |
| 388 | 2026-07-27 |  | Replace the global fail-open alert evaluation policy with per-rule semantics | `b399957`, `ea53c30`, `9a3674c` +1 |
| 387 | 2026-07-27 |  | Add runbook and dashboard/panel links to every alert | `9a3674c`, `e54a4cd` |
| 386 | 2026-07-27 |  | Rebuild the dedicated exporter-health dashboard from the current self-observability catalog | `9a3674c` |
| 385 | 2026-07-27 |  | Correct semantic boolean maps and preserve unknown state | `be4fc7d` |
| 384 | 2026-07-29 |  | Make `stdout` a genuinely immediate debugging exporter | `0084290`, `1e63883` |
| 383 | 2026-07-29 |  | Repair trace-era telemetry documentation and stale comments | `0084290` |
| 382 | 2026-07-28 |  | Evaluate OTel Go’s experimental SDK/exporter self-observation | `0084290` |
| 381 | 2026-07-28 |  | Evaluate exponential/native histograms as a series-cost reduction |  |
| 380 | 2026-07-29 |  | Support controlled standard OTel Resource enrichment | `0084290`, `1e63883` |
| 379 | 2026-07-28 |  | Pin Prometheus translation semantics and test the complete name contract | `3858408` |
| 378 | 2026-07-28 |  | Support client-certificate authentication on `/metrics` | `3858408` |
| 377 | 2026-07-28 |  | Harden and instrument `/metrics` serving | `3858408` |
| 376 | 2026-07-28 |  | Add bounded collector labels to continuous profiles | `0084290`, `3858408` |
| 375 | 2026-07-28 |  | Add Pyroscope custom TLS and secret header controls | `3858408` |
| 374 | 2026-07-28 |  | Expose Pyroscope upload health and complete profile identity | `3858408` |
| 373 | 2026-07-29 |  | Add a trust policy for inbound `traceparent` sampling decisions | `0084290`, `1e63883` |
| 372 | 2026-07-29 |  | Add workload-specific head-sampling controls | `0084290`, `1e63883` |
| 371 | 2026-07-28 |  | Trace currently dark outbound HTTP dependencies | `3858408` |
| 370 | 2026-07-29 |  | Add opt-in Pyroscope span-profile correlation | `0084290`, `1e63883` |
| 369 | 2026-07-28 |  | Add a scrape-duration histogram with trace exemplars | `3858408` |
| 368 | 2026-07-28 |  | Negotiate OpenMetrics so pull-path exemplars survive |  |
| 367 | 2026-07-28 |  | Preserve trace context on logs and receiver metrics | `0a3536e`, `3858408` |
| 366 | 2026-07-28 |  | Bound individual OTLP log records and expose truncation | `3858408` |
| 365 | 2026-07-29 |  | Rate-limit OTLP outage diagnostics and emit recovery summaries | `0084290` |
| 364 | 2026-07-28 |  | Benchmark and reduce multi-tailnet exporter fan-out and synchronized bursts |  |
| 363 | 2026-07-28 |  | Ship production Alloy/Collector gateway recipes | `5c422ab` |
| 362 | 2026-07-29 |  | Reload outbound telemetry credentials and TLS material without restarting | `0084290` |
| 361 | 2026-07-29 |  | Support independent signal destinations and enablement | `0084290`, `1e63883` |
| 360 | 2026-07-29 |  | Expose deterministic OTLP compression, timeout, retry, and request-size controls | `0084290`, `1e63883` |
| 359 | 2026-07-29 |  | Complete exporter self-observability for traces, partial success, and local status | `0084290` |
| 358 | 2026-07-29 |  | Configure log/trace batch queues and expose saturation/drop telemetry | `0084290`, `1e63883` |
| 357 | 2026-07-29 |  | Add a real OTLP HTTP/gRPC wire-contract integration suite | `0084290` |
| 356 | 2026-07-28 |  | Pseudonymize per-tailnet `service.instance.id` when tailnet-name PII is disabled | `3858408` |
| 355 | 2026-07-28 |  | Exercise Workload Identity Federation in live-contract CI | `b75d3db`, `8de36d5` |
| 354 | 2026-07-28 | ~ | Research log-stream destination drift before implementing a gauge | `29f57c8` |
| 353 | 2026-07-28 | ~ | Add a Prometheus HTTP service-discovery endpoint for tailnet nodes |  |
| 352 | 2026-07-28 |  | Design HA semantics before adding shared checkpoint storage |  |
| 351 | 2026-07-28 |  | Add an optional NetworkPolicy baseline | `15cd177` |
| 350 | 2026-07-28 |  | Make probes configurable and add a startup probe | `15cd177` |
| 349 | 2026-07-28 |  | Support immutable image digests | `15cd177` |
| 348 | 2026-07-28 |  | Add typed `extraEnv` and `extraEnvFrom` | `b188b9a` |
| 347 | 2026-07-28 |  | Support externally managed application config resources | `098dbb3` |
| 346 | 2026-07-28 |  | Add secure receiver Ingress/Gateway examples or blocks | `9993b50` |
| 345 | 2026-07-28 |  | Add an opt-in Prometheus Operator PodMonitor | `9ade22f`, `e01d6c6`, `1e5d29a` |
| 344 | 2026-07-28 |  | Add default-off, per-listener Kubernetes Services | `9993b50`, `9ade22f`, `e01d6c6` +1 |
| 343 | 2026-07-28 |  | Add first-class projected Workload Identity Federation tokens | `30589c4` |
| 342 | 2026-07-28 |  | Make Helm probes follow admin TLS | `15cd177`, `e01d6c6`, `82e43b5` |
| 341 | 2026-07-28 |  | Replace unsafe Helm credential quick starts and fix the `secrets.*` typo | `1e5d29a`, `7cd9a84`, `123fe72` |
| 340 | 2026-07-28 |  | Publish a package-manager installation path | `c8802cd` |
| 339 | 2026-07-28 |  | Ship a hardened systemd deployment | `c8802cd` |
| 338 | 2026-07-28 |  | Document verified release-binary and image installation | `36a6083` |
| 337 | 2026-07-28 |  | Add a native healthcheck subcommand | `4cc1bb3`, `9b13a84` |
| 336 | 2026-07-28 |  | Use platform-appropriate native checkpoint paths | `3ce3461` |
| 335 | 2026-07-28 |  | Separate published-image Compose from developer builds | `1faaa5e` |
| 334 | 2026-07-28 |  | Provide first-class Docker Compose secrets | `a544576` |
| 333 | 2026-07-28 |  | Fix the Compose optional config-file mount | `a9311a3` |
| 332 | 2026-07-28 |  | Align Compose and Kubernetes shutdown budgets with staged draining | `7cd9a84`, `ac58014` |
| 331 | 2026-07-28 |  | Correct the operator documentation after recent security/UI changes | `c431611` |
| 330 | 2026-07-28 |  | Show update availability on the admin page | `e736ba2` |
| 329 | 2026-07-28 |  | Make flow-store capacity and memory policy configurable | `ead2f47` |
| 328 | 2026-07-28 |  | Make UI polling single-flight, visibility-aware, and backoff-capable | `1b3176a` |
| 327 | 2026-07-28 |  | Complete accessibility and keyboard support for both embedded UIs | `25e5b2b`, `66ac88d` |
| 326 | 2026-07-28 | ~ | Add authenticated per-collector “Run now” |  |
| 325 | 2026-07-28 |  | Add a per-tailnet selector to the admin page | `306a161` |
| 324 | 2026-07-28 |  | Add explicit browser login/logout for the admin page |  |
| 323 | 2026-07-28 |  | Version and publish the admin status/flows API contract | `fde409f` |
| 322 | 2026-07-28 |  | Add defensive HTTP headers to admin responses | `7d4aa8f` |
| 321 | 2026-07-28 |  | Generate a privacy-safe support bundle | `a7b49a6`, `0d31d9f` |
| 320 | 2026-07-28 |  | Export the complete effective redacted configuration | `8b655e9`, `0d31d9f`, `fffa889` |
| 319 | 2026-07-28 |  | Surface active configuration advisories in the admin UI/API | `8491a31`, `c431611` |
| 318 | 2026-07-28 |  | Unify component health, readiness, and status-page verdicts | `5b25a5e`, `3aaab59` |
| 317 | 2026-07-28 |  | Report actual OTLP delivery state in the admin page | `5c38fd2`, `3aaab59` |
| 316 | 2026-07-28 |  | Reload listener certificates and expose expiry health | `1b3176a`, `0d31d9f`, `9b92b48` |
| 315 | 2026-07-28 |  | Make Prometheus authentication fail closed | `c431611`, `5a0be19` |
| 314 | 2026-07-28 |  | Make the default admin landing page safely usable | `c431611`, `3584827` |
| 313 | 2026-07-28 | ~ | Add transactional configuration and secret hot reload |  |
| 312 | 2026-07-28 |  | Support structured JSON application logs | `c156ccd` |
| 311 | 2026-07-28 |  | Add safe preflight and single-cycle execution modes | `ee7e206` |
| 310 | 2026-07-28 |  | Resolve file paths relative to their configuration source | `5b25a5e`, `8154192` |
| 309 | 2026-07-28 |  | Add canonical redacted effective-config and provenance CLI output | `1b3176a`, `355c8b1`, `0d31d9f` |
| 308 | 2026-07-28 |  | Publish a standalone application configuration JSON Schema | `fde409f`, `355c8b1`, `b15156f` |
| 307 | 2026-07-28 |  | Report all configuration diagnostics in one pass | `95da889` |
| 306 | 2026-07-28 |  | Canonicalize listener addresses and fail startup on bind failure | `3aaab59`, `6a113d9` |
| 305 | 2026-07-28 |  | Validate complete, parseable TLS keypairs for every listener | `0084290`, `a3b96be` |
| 304 | 2026-07-28 |  | Make the Helm values schema reject unknown values | `69d7746` |
| 303 | 2026-07-28 |  | Reject unknown YAML configuration keys | `5b25a5e`, `96c5e9b` |
| 302 | 2026-07-28 |  | fix(flows): version policy verdicts so retained traffic cannot join to the wrong current rules | `a6d14c9` |
| 301 | 2026-07-28 |  | fix(flowstore): keep recent connections ordered by event time when late data arrives | `3de4fc7` |
| 300 | 2026-07-28 |  | feat(admin): add a bounded audit and webhook event explorer | `8b655e9` |
| 299 | 2026-07-28 |  | feat(flows): export filtered data as CSV and JSON | `cebb89d` |
| 298 | 2026-07-28 |  | feat(flows): persist view state in shareable URLs | `306a161`, `1f32b15` |
| 297 | 2026-07-28 |  | feat(rdns): serve stale positive PTR values while refreshing | `1b3176a`, `0d31d9f`, `e17a3cc` |
| 296 | 2026-07-28 |  | feat(flows): server-side filtering and cursor pagination | `86833de` |
| 295 | 2026-07-28 |  | fix(flows): historical pinned windows must return matching recent rows | `3de4fc7` |
| 294 | 2026-07-29 |  | feat(flowstore): optional SQLite persistence for multi-day /flows history | `5b25a5e`, `4b25580` |
| 293 | 2026-07-27 |  | fix(objectstore): support flat copied-export layouts as the parser claims | `0794f61` |
| 292 | 2026-07-26 |  | harden(objectstore): bound decompressed bytes and records per object | `f969025` |
| 291 | 2026-07-27 |  | fix(s3): detect or stream oversized ListObjects responses | `7ebdb9c` |
| 290 | 2026-07-27 |  | fix(s3): preserve configured endpoint base paths | `1e716e1` |
| 289 | 2026-07-26 |  | security(s3): require explicit opt-in for plaintext remote endpoints | `1528787` |
| 288 | 2026-07-27 |  | feat(auditlogs): ingest configuration-log S3 and S3-compatible exports | `639ca34`, `2018f07` |
| 287 | 2026-07-26 | ~ | feat(objectstore): resumable historical backfill beyond fourteen days |  |
| 286 | 2026-07-26 |  | fix(objectstore): persist failed-object gaps and correct the exactly-once contract | `bf7e92d` |
| 285 | 2026-07-26 |  | fix(objectstore): resume listings beyond already-seen keys | `6709c54`, `8c951e2` |
| 284 | 2026-07-27 |  | feat(objectstore): support per-tailnet storage destinations and credentials | `ddef918` |
| 283 | 2026-07-26 |  | fix(config): reject object-store ingestion in multi-tailnet mode to prevent cross-tailnet attribution | `c6f66b0` |
| 282 | 2026-07-26 |  | feat(ingest): expose per-source event age and accepted-data freshness | `6561eb7` |
| 281 | 2026-07-26 |  | fix(config): preflight streaming.public_url against Tailscale endpoint rules | `e88c1c5` |
| 280 | 2026-07-26 |  | feat(receivers): route HEC and webhook traffic in multi-tailnet mode | `6561eb7` |
| 279 | 2026-07-26 |  | feat(receivers): optional ingress WAL for crash-safe acknowledgement | `04384a2` |
| 278 | 2026-07-26 |  | fix(stream): stop all side effects after the request deadline returns 503 | `6561eb7` |
| 277 | 2026-07-26 | ~ | research(webhook): validate and instrument webhook-to-audit cross-source dedup |  |
| 276 | 2026-07-26 |  | research(webhook): verify secret-rotation signature overlap and correct local guidance | `e88c1c5` |
| 275 | 2026-07-26 |  | feat(webhook): add native TLS listener support | `e88c1c5` |
| 274 | 2026-07-26 |  | feat(webhook): suppress upstream retry duplicates for all event types | `6561eb7` |
| 273 | 2026-07-26 |  | security(webhook): fail closed on network-reachable binds with no secret | `6561eb7` |
| 272 | 2026-07-26 |  | feat(webhook): emit typed structured event data with PII controls | `6561eb7` |
| 271 | 2026-07-26 | ~ | fix(stream): classify explicit CONFIG audit records even when actor is absent |  |
| 270 | 2026-07-26 |  | feat(audit): emit bounded schema-drift signals for unknown values | `e88c1c5` |
| 269 | 2026-07-26 |  | feat(audit): expand the security and lifecycle change taxonomy from the current schema | `6561eb7` |
| 268 | 2026-07-26 |  | feat(audit): preserve deferred timing, event type, actor tags, and ephemeral targets | `6561eb7` |
| 267 | 2026-07-26 |  | fix(flowstore): reject future and expired observation timestamps | `6561eb7` |
| 266 | 2026-07-26 |  | feat(flowlog): distinguish Destination Logging privacy gaps from malformed records | `e88c1c5` |
| 265 | 2026-07-26 |  | feat(flowlog): expose reporter provenance and trust diagnostics | `e88c1c5` |
| 264 | 2026-07-26 |  | feat(flowlog): add shared semantic validation and data-quality telemetry | `6561eb7` |
| 263 | 2026-07-26 |  | fix(flowlog): canonicalize tag-set dimensions before emission and storage | `e88c1c5` |
| 262 | 2026-07-26 |  | fix(flowlog): omit network.type when neither endpoint is valid | `e88c1c5` |
| 261 | 2026-07-26 |  | fix(flowlogs): expose feature-probe failures in stream and object-store modes | `6561eb7` |
| 260 | 2026-07-26 |  | feat(flowlogs): replay a bounded overlap for late-arriving API records | `6561eb7` |
| 259 | 2026-07-26 |  | feat(flowlog): detect conflicting duplicate records instead of silently taking the first | `6561eb7` |
| 258 | 2026-07-26 |  | fix(flowlog): include traffic type in connection dedup identities | `6561eb7` |
| 257 | 2026-07-26 |  | fix(flowlogs): preserve embedded node identity through poll boundary dedup | `622773c` |
| 256 | 2026-07-25 |  | Deploy current dashboards and alerting rules to Grafana |  |
| 254 | 2026-07-25 |  | release: goreleaser races the Go module proxy on a freshly-cut tag | `9401181`, `f59c6f4` |
| 250 | 2026-07-24 |  | DeviceKeyExpiringCritical pages on untagged user devices where reauth is routine | `15bf0ba` |
| 249 | 2026-07-24 |  | flow logs: name IP proto 99 `tsmp`, and say what a TSMP flow means | `0971527` |
| 248 | 2026-07-24 |  | flow logs: `:0` is a "no port here" placeholder, and only one surface reads it as one | `7d36026` |
| 246 | 2026-07-24 |  | flow view: the ports breakdown counts underlay ports, including DERP region IDs | `e8156bf` |
| 245 | 2026-07-24 |  | flow metrics: stop labelling the DERP relay marker as a destination node | `2004791` |
| 244 | 2026-07-24 |  | release: bump the module path to /v3 before the 3.0.0 release PR merges | `1739277` |
| 243 | 2026-07-24 |  | flow metrics: carry path/DERP region and identity in the rollup, not just the raw families | `2004791`, `cb73daa` |
| 242 | 2026-07-24 |  | config sweep: realign the Helm chart and docs with the actual config surface | `c817573` |
| 241 | 2026-07-24 |  | fix(admin): /flows should show identity in full, not filtered by pii_filter | `4b25580`, `c431611`, `b0616a9` |
| 238 | 2026-07-24 |  | feat(flowlogs): S3-compatible object storage as a third ingestion source | `a8ce1aa` |
| 237 | 2026-07-24 |  | feat(admin): built-in network flow visualiser at /flows | `5b25a5e`, `4b25580`, `6037e1c` +11 |
| 236 | 2026-07-24 |  | fix(flowlog): exit traffic has no destination — stop emitting fabricated `dst_node` | `b6629a2` |
| 235 | 2026-07-24 |  | feat(flowlog): decode `srcNode`/`dstNodes` for self-enrichment and per-flow identity | `8cb3f7b` |
| 234 | 2026-07-24 |  | fix(flowlog): flow logs are timestamped by `logged`, misplacing them up to 14 minutes | `a0cce89` |
| 233 | 2026-07-24 |  | SEO & discoverability: repo metadata, README rewrite, docs backlinks | `3cd9355` |
| 232 | 2026-07-23 |  | Streaming receiver: the 30s processing deadline's 503 cannot reach a real client (WriteTimeout fires first) | `f8dab4b` |
| 229 | 2026-07-23 |  | Bound per-request record count in the streaming receiver: single-body memory amplification enables OOM DoS | `992d7f6` |
| 228 | 2026-07-23 |  | Bound envelope unwrapping in the streaming receiver: quadratic JSON re-parse enables single-request CPU DoS | `c431611`, `992d7f6` |
| 227 | 2026-07-23 |  | Admin status page and JSON APIs served unauthenticated by default on a wildcard bind | `c431611`, `4427507`, `effb816` |
| 223 | 2026-07-21 |  | admin: emitted-throughput and collector-fleet trend charts on the status page | `a1c1fe9` |
| 215 | 2026-07-20 |  | console: reference impl for fleet alignment — make refresh interval configurable (default 5s) | `9b13a84` |
| 212 | 2026-07-23 |  | Apply the PII filter to Tailscale API trace attributes | `0550813` |
| 211 | 2026-07-23 |  | Bound successful Headscale API JSON responses before decoding | `054ee09` |
| 210 | 2026-07-23 |  | Bound successful Tailscale API JSON responses before decoding | `054ee09` |
| 209 | 2026-07-23 |  | Add aggregate admission control to the streaming receiver body buffer | `992d7f6` |
| 208 | 2026-07-18 |  | Admin/status UI overhaul: tabbed layout, cardinality suite, real charts, theme refresh | `5991d21`, `55cc1e6` |
| 206 | 2026-07-17 |  | Cap Retry-After delays at the configured retry maximum | `15b1038`, `d581993` |
| 205 | 2026-07-17 |  | Reject webhook timestamps too far in the future | `94774c7` |
| 204 | 2026-07-17 |  | Give telemetry pipelines independent shutdown budgets | `c7f4f1e` |
| 203 | 2026-07-17 |  | Prevent Helm deployments from scaling the singleton poller | `93cb2d5` |
| 202 | 2026-07-17 |  | Reject log-poll windows that can never catch up | `376b47d` |
| 201 | 2026-07-17 |  | Decide stream batch durability before ACKing partial record loss | `cfa330a` |
| 200 | 2026-07-17 |  | Finish bounding OAuth token fetches after response headers | `15b1038`, `4d5afa1` |
| 199 | 2026-07-17 |  | Keep node-metric delta baselines separate from dropped labels and target identity | `b3217f6` |
| 198 | 2026-07-17 |  | Classify IP addresses with ports before applying PII filters | `a206df4` |
| 197 | 2026-07-17 |  | Redact PII from log bodies, not only log attributes | `5441abf` |
| 195 | 2026-07-17 |  | service_version still churns on Tempo-generated traces_spanmetrics_* (traces resource keeps it) | `bd93ee2` |
| 194 | 2026-07-17 |  | regen-generated.sh silently produced wrong helm artifacts (unpinned tools + helm-docs version ldflag) | `48b8ae5` |
| 187 | 2026-07-17 |  | Don't promote service.version to a per-series metric label (OTLP→Prometheus: version belongs on target_info/build_info) | `0084290`, `bd93ee2`, `1658133` |
| 185 | 2026-07-15 |  | Pyroscope: collect all profile types by default, incl. goroutine-leak (GOEXPERIMENT) | `fec8915` |
| 176 | 2026-07-13 |  | v2.0.1 provenance attestation failed: attest step handed the sigstore bundle instead of SHA256SUMS | `3c413e1`, `9401181`, `622ff44` |
| 174 | 2026-07-13 |  | release: v2.0.0 binaries job fails — module path must be /v2 for v2+ tags | `4374b06`, `3c413e1`, `1739277` +1 |
| 173 | 2026-07-13 |  | Tracker: 2026-07 feature program — semconv v2, API coverage, client-metric curation, operator polish |  |
| 172 | 2026-07-13 |  | feat(deploy): alert + dashboard pack (API permission failures, config changes, staleness, routes, client health) | `25aa62b` |
| 171 | 2026-07-13 |  | feat(nodemetrics): curate key tailscaled client metrics into the named catalog | `c362a92` |
| 170 | 2026-07-13 |  | feat: TLS support for the admin and Prometheus listeners | `82e43b5`, `63c02a1` |
| 169 | 2026-07-13 |  | feat(config): file-based secrets (*_file) for all credentials | `a474fb9` |
| 168 | 2026-07-13 |  | feat(auth): workload identity federation (auth.method: workload_identity) | `d55341a` |
| 167 | 2026-07-13 |  | feat(collector): OAuth Apps collector + spec re-vendor + api-drift lane check | `8f2c3ca`, `a474fb9` |
| 166 | 2026-07-13 |  | feat(services): verify collector against Services GA + flow-log service-VIP enrichment | `f3669cd` |
| 165 | 2026-07-13 |  | feat(keys): key owner (userId) and auto-applied device tags | `e548e5e` |
| 164 | 2026-07-13 |  | feat(devices): posture-attribute expiry telemetry | `de0c50c` |
| 163 | 2026-07-13 |  | feat(devices): emit multipleConnections, blocksIncomingConnections, postureIdentity.disabled | `e5551fc` |
| 162 | 2026-07-13 |  | feat: -version and -validate flags on the release binary + stale-doc fixes | `86c2e35` |
| 161 | 2026-07-13 |  | feat!: align telemetry attributes with OTel semantic conventions (breaking, v2.0.0) | `e59642d` |
| 160 | 2026-07-13 |  | Live API contract lane has never run: waits on a self-hosted runner that doesn't exist | `54caa5e`, `0acd799` |
| 147 | 2026-07-12 |  | Renovate leaves tools/* go.mod stale on shared-dep bumps, breaking their CI jobs | `f075931` |
| 145 | 2026-07-12 |  | docs: fix doc/code drift found by full documentation-accuracy audit | `26ac4a1` |
| 144 | 2026-07-12 |  | chore(security): raise OpenSSF Scorecard from 6.6 - fuzzing, security policy, release provenance | `ba82856` |
| 129 | 2026-07-05 |  | V1 LAUNCH — bug-bash execution plan & tracker | `edeec54`, `33942fe`, `413b314` +24 |
| 128 | 2026-07-03 |  | Decouple the IANA CSV re-download from `go generate ./...` bootstrap (rewrites committed data, fails offline) | `a0fdd75`, `4984ad6` |
| 127 | 2026-07-03 |  | Default tailnets[] OAuth scopes to all:read (least-privilege) when unset | `45d73b8` |
| 126 | 2026-07-03 |  | Add a shared catalog attribute-key drift guard usable by every package's catalog_test.go | `c421491` |
| 125 | 2026-07-03 |  | Attribute per-tailnet client construction failures to the offending tailnet | `f0e20d1` |
| 124 | 2026-07-03 |  | Document multi-tailnet (tailnets:) mode — absent from configuration.md, README, and all docs pages | `edeec54` |
| 123 | 2026-07-03 |  | Add exporter-liveness + baseline rules to the recommended Grafana-managed rules file | `250b4c5` |
| 122 | 2026-07-04 |  | Decide handling for deleted todos.txt that persists in public git history with real lab topology | `6687386`, `33942fe` |
| 121 | 2026-07-03 |  | Fix the rdns Cache Close()/LookupName WaitGroup race that can panic on receiver-driven shutdown | `e17a3cc`, `f0e20d1`, `ec63303` |
| 120 | 2026-07-03 |  | Fix dedup.evictions guidance — steady-state evictions are normal; document the real overflow criterion (or emit key residency) | `267db9d` |
| 119 | 2026-07-03 |  | Pick the PTR name deterministically instead of names[0] to avoid flow-metric label flap | `158b29f` |
| 118 | 2026-07-03 |  | Fix rdns cache exceeding max_entries by up to Concurrency and inflating the overflow counter at capacity | `e17a3cc`, `ec63303` |
| 117 | 2026-07-03 |  | Close the Headscale bypass of the streaming/webhook single-tailnet guard; add receivers to the headscale-unsupported warning | `45d73b8` |
| 116 | 2026-07-03 |  | Fix status page per-tailnet identity in list mode (auth method/secret flags/header read the unused tailscale block) | `796c75b` |
| 115 | 2026-07-03 |  | Fix the backwards PII-body limitation examples in docs/configuration.md | `edeec54` |
| 114 | 2026-07-03 |  | Add headscale, pii_filter, version_checks sections to the docs/configuration.md Contents TOC | `edeec54` |
| 113 | 2026-07-03 |  | Declare the missing attributes on the tailscale.key.scopes log event in the catalog | `c421491` |
| 112 | 2026-07-03 |  | Fix docs/configuration.md reverse_dns defaults (cache_ttl 24h, max_entries 50000) and tighten rdns miss/description wording | `edeec54` |
| 111 | 2026-07-03 |  | Fix Grafana-managed recording rules that use a raw range query with no reduce node | `08d0b87` |
| 110 | 2026-07-03 |  | Use sum not max over approval/configured buckets in ts2o-vip-service-no-ha | `08d0b87` |
| 109 | 2026-07-03 |  | Stop treating cumulative key-expiry histogram buckets as current device counts (alert latches, dashboard stats garbage) | `08d0b87` |
| 108 | 2026-07-03 |  | Emit enrich.cache_age from an async/observable gauge so staleness is detectable | `e3ef49e` |
| 107 | 2026-07-03 |  | Fix broken PromQL semantics in shipped alert rules and flagship dashboard panels | `08d0b87` |
| 106 | 2026-07-03 |  | Validate log_level against its documented enum | `4781a88` |
| 105 | 2026-07-03 |  | Handle checkpoint-key namespace changes on single↔multi transitions and tailnet renames | `38dfcaa` |
| 104 | 2026-07-03 |  | Apply tailscale.http defaults (esp. retry max_attempts) to tailnets[] entries | `1658133`, `4781a88` |
| 103 | 2026-07-03 |  | Fix /metrics HTTP 500 from duplicate series when pii_filter.tailnet_name=false (and single-mode self-obs overlap) | `844f267`, `3858408`, `16246da` |
| 102 | 2026-07-03 |  | Populate DeviceMeta.Tags in the devices collector's toMetas — status page device-tags column is always blank | `6035361` |
| 101 | 2026-07-03 |  | Retire the dead GoReleaser docker pipeline (dockers_v2/docker_signs/Dockerfile.goreleaser) and fix stale deploy/CLAUDE.md + .goreleaser.yaml header | `413b314` |
| 100 | 2026-07-03 |  | Reconcile report-drift's draft-PR capability with workflow permissions | `413b314` |
| 99 | 2026-07-03 |  | Decode posture integration status.error and stop describing last_sync as "successful" sync | `6035361` |
| 98 | 2026-07-03 |  | Stabilize discovered instance-label disambiguation across refresh cycles and static targets | `403dd98` |
| 97 | 2026-07-03 |  | Include action/target in the auditlogs grouped boundary-dedup key | `47f0eb1` |
| 96 | 2026-07-03 |  | Salvage the valid prefix in stream extractRecords instead of rejecting the whole concatenated HEC batch | `cfa330a`, `829c09a` |
| 95 | 2026-07-03 |  | Harden flowlogs error handling: errors.As-based 403 classification; avoid permanent wedge on oversized catch-up windows | `3b4cbbb` |
| 94 | 2026-07-03 |  | Fix otlp.tls.insecure docs (it means plaintext, not skip-verify) and add a real insecure_skip_verify knob | `edeec54`, `33942fe` |
| 93 | 2026-07-03 |  | Stop counting shutdown cancellation as a collector failure in scrape metrics/status/WARN | `0dc5ec0` |
| 92 | 2026-07-03 |  | Add emission-asserting tests for the shutdown rollup flush and the two untested ticker reporters | `25a2adc` |
| 91 | 2026-07-03 |  | Treat const-attr normalized names (tailscale_tailnet, tailscale2otel_provider) as reserved in the label-collision guard | `16246da` |
| 90 | 2026-07-03 |  | Update README.md's Collectors table to list all 14 registered collectors | `edeec54` |
| 89 | 2026-07-03 |  | Replace real lab/tailnet identifiers in tracked source/fixtures with synthetic values | `33942fe` |
| 88 | 2026-07-03 |  | Sync docs/dashboards.md tab list with the shipped 10-tab flagship dashboard | `edeec54` |
| 87 | 2026-07-03 |  | Default GOMEMLIMIT in the Helm chart to match the docker-compose backstop | `413b314` |
| 86 | 2026-07-03 |  | Add an allocation-free fast path to the emit-path label-collision guard | `4984ad6` |
| 85 | 2026-07-03 |  | Reuse the devices collector's cached inventory for node-metrics discovery | `5d43509` |
| 84 | 2026-07-03 |  | Bound the OAuth token fetch so a hung refresh can't stall every collector forever | `15b1038`, `4d5afa1`, `d55341a` +1 |
| 83 | 2026-07-03 |  | Add extraVolumes/extraVolumeMounts to the Helm chart (streaming TLS cert files are currently unmountable) | `250b4c5` |
| 82 | 2026-07-03 |  | Add fsGroup 65532 to podSecurityContext for the opt-in PVC persistence path | `250b4c5` |
| 81 | 2026-07-03 |  | Fix docs/architecture.md: wrong default checkpoint path; incomplete pprof gating (admin.auth.token) | `edeec54` |
| 80 | 2026-07-03 |  | Bound nodemetrics per-tick scrape time (serial loop, no per-tick deadline) | `479575e`, `ff47ce3` |
| 79 | 2026-07-03 |  | Give an actionable error when env vars index into list-of-structs config (node_metrics.targets is file-only) | `a544576`, `5d43509` |
| 78 | 2026-07-03 |  | Fix release-please 'deps' changelog section that never matches any commit | `413b314` |
| 77 | 2026-07-03 |  | Bound wire-derived metric attribute values on the stream path (audit action/origin, proto IANA clamp) | `742e6c1` |
| 76 | 2026-07-03 |  | Distinguish client-side rate-limiter wait from real API latency in api.duration | `15b1038`, `5d43509` |
| 75 | 2026-07-03 |  | Correct streaming.token auth-scheme docs (HTTP Basic, not "Authorization: Splunk") + stale MetricRejected comment | `edeec54` |
| 74 | 2026-07-03 |  | Add enduser.id / tailscale.user.login to the PII identityKeys set so per-user gauges suppress instead of collapsing | `7126f31` |
| 73 | 2026-07-03 |  | Type OTLP and node-metrics header values as config.Secret | `5d43509` |
| 72 | 2026-07-03 |  | Fix tsapi.ServiceHost.NodeID JSON tag (stableNodeID) and add listServiceHosts contract coverage | `bb4d575` |
| 71 | 2026-07-03 |  | Surface Headscale provider support in README/onboarding docs | `4984ad6` |
| 70 | 2026-07-03 |  | Wire a webhook.max_body_bytes config knob and lower the 64 MiB pre-auth default | `5d43509` |
| 69 | 2026-07-03 |  | Checkpoint store: degrade gracefully on corrupt file and report the EFFECTIVE store, not the config value | `38dfcaa` |
| 68 | 2026-07-03 |  | Correct metric catalog descriptions (device-invite host.id, logstream _ratio caveat, acl.last_changed restart semantics) | `33942fe` |
| 67 | 2026-07-03 |  | Count stream-receiver skipped/unclassifiable records in a metric (incl. unwrap drops) | `5d43509` |
| 66 | 2026-07-03 |  | Set a tailscale2otel/<version> User-Agent on raw tsapi requests | `5d43509` |
| 65 | 2026-07-03 |  | Fix stale in-code comments: providerset target_info claim, appcatalog _ratio claim, CLAUDE.md Emitter sketch, checkpoint persist-failure re-poll claim | `33942fe` |
| 64 | 2026-07-03 |  | Fix Headscale adapter fidelity: map preauth `used`, stop emitting fabricated zero/false values for absent fields | `3b4cbbb` |
| 63 | 2026-07-03 |  | Emit an identifiable per-domain signal for split-DNS zones with an empty resolver list | `5d43509` |
| 62 | 2026-07-03 |  | Stop counting non-export errors as OTLP export failures in InstallExportErrorHandler | `16246da` |
| 61 | 2026-07-03 |  | Prune the devices collector's lastPosture map to the current fleet each tick | `5d43509` |
| 60 | 2026-07-03 |  | Report dedup self-obs and status for all tailnet runtimes, not just runtimes[0] | `eb2700d` |
| 59 | 2026-07-03 |  | Fix per-tailnet duplication of static node_metrics targets in multi-tailnet mode | `f0e20d1` |
| 58 | 2026-07-03 |  | Tighten Registry.Register to require SnapshotCollector (compile-time check like RegisterWindow) | `38dfcaa` |
| 57 | 2026-07-03 |  | Make /readyz reflect actual readiness instead of aliasing /healthz | `3aaab59`, `6a113d9`, `5d43509` |
| 56 | 2026-07-03 |  | Route the tailscale.device.posture log's dynamic attributes through PII classification | `6035361` |
| 55 | 2026-07-04 |  | Address ghost cumulative gauge series after entity disappearance (stale re-export + cardinality-slot exhaustion) | `479575e`, `ac22409`, `c421491` |
| 54 | 2026-07-03 |  | Fix duplicated self-obs reporters under provider=headscale (export.* doubled, series.active corrupted) | `a1c1fe9`, `543b336` |
| 53 | 2026-07-03 |  | Join receiver goroutines on shutdown so in-flight stream/webhook records aren't ACKed-then-dropped | `f0e20d1`, `ec63303` |
| 52 | 2026-07-03 |  | Close config validation gaps that allow silent zero-ingestion, stalled window collectors, and dead listeners | `4781a88` |
| 51 | 2026-07-03 |  | Fix root CLAUDE.md's false claim that the live-contract lane uses GitHub OIDC | `33942fe` |
| 50 | 2026-07-03 |  | Install go-licenses before scripts/notices.sh in publish.yml's notices job | `413b314` |
| 49 | 2026-07-03 |  | Fix docs/node-metrics.md: forwarded node series carry `tailscale_node`, not `instance` | `edeec54` |
| 48 | 2026-07-03 |  | Fix DevicesRich decode failure on empty `created` timestamps from external devices | `bb4d575` |
| 46 | 2026-07-03 |  | Docs site: redesign & rebrand alignment + SEO/LLM discoverability | `77e7c4f` |
| 25 | 2026-06-26 |  | tailscale-client-go/v2 latest breaks our build | `4c83bf9` |
