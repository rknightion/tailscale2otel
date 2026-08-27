# Changelog

## [4.0.1](https://github.com/rknightion/tailscale2otel/compare/v4.0.0...v4.0.1) (2026-08-27)


### Bug Fixes

* **acl:** persist revision provenance ([8b510f6](https://github.com/rknightion/tailscale2otel/commit/8b510f6864fe743e80b2ec50b4a634bff0c39e9b))
* **ci:** follow GitSync dashboard path ([e0d20a2](https://github.com/rknightion/tailscale2otel/commit/e0d20a2e3ae1f8c65d2af4cc757641abc17b93c3))
* **dashboards:** repair live signal accuracy ([d00c0ce](https://github.com/rknightion/tailscale2otel/commit/d00c0ced03c5c37bac59fe634d63e4aa8005ec0f))
* **release:** stream the latest-release response instead of buffering 64 KiB ([59bae17](https://github.com/rknightion/tailscale2otel/commit/59bae17befd156074d0ec0afc928d19bdf579c56))
* **state:** separate evidence durability from poll cursors ([66fcb05](https://github.com/rknightion/tailscale2otel/commit/66fcb05286092d841b8ffb86fd92594726b80595))

## [4.0.0](https://github.com/rknightion/tailscale2otel/compare/v3.0.0...v4.0.0) (2026-08-27)


### ⚠ BREAKING CHANGES

* improve first-run delivery experience
* move the Go module path to /v4 ahead of the 4.0.0 release
* **flowstore:** rename flows.store.path to directory and surface backend health

### Features

* **admin:** add a bounded audit and webhook event explorer ([8b655e9](https://github.com/rknightion/tailscale2otel/commit/8b655e97232a1b4eede7a3fa50dbc275375f8af5)), closes [#300](https://github.com/rknightion/tailscale2otel/issues/300)
* **admin:** add a per-tailnet selector to the status page ([306a161](https://github.com/rknightion/tailscale2otel/commit/306a161e031b7839d316b470bf6ca58a53beb074)), closes [#325](https://github.com/rknightion/tailscale2otel/issues/325)
* **admin:** add defensive HTTP headers to every admin response ([7d4aa8f](https://github.com/rknightion/tailscale2otel/commit/7d4aa8f0a695b7aca7168cfe3c8e87906588f51b)), closes [#322](https://github.com/rknightion/tailscale2otel/issues/322)
* **admin:** export the complete effective redacted configuration ([fffa889](https://github.com/rknightion/tailscale2otel/commit/fffa8897838225e9ce06dc61e016fb83f58a3078)), closes [#320](https://github.com/rknightion/tailscale2otel/issues/320)
* **admin:** generate a privacy-safe support bundle ([a7b49a6](https://github.com/rknightion/tailscale2otel/commit/a7b49a67c2edd35b736a47871c6da8a064d2ec3e)), closes [#321](https://github.com/rknightion/tailscale2otel/issues/321)
* **admin:** list active configuration advisories on the status page and API ([8491a31](https://github.com/rknightion/tailscale2otel/commit/8491a316d66ce375f6d49e9dd3ba8cdc44e16b10)), closes [#319](https://github.com/rknightion/tailscale2otel/issues/319)
* **admin:** make both embedded UIs keyboard-operable and screen-reader-legible ([66ac88d](https://github.com/rknightion/tailscale2otel/commit/66ac88dff9daa2c519d218189a23e32b9b3b4fa3)), closes [#327](https://github.com/rknightion/tailscale2otel/issues/327)
* **admin:** make UI polling single-flight, visibility-aware and backoff-capable ([1b3176a](https://github.com/rknightion/tailscale2otel/commit/1b3176a582ab012b6ebeb502ad964773086324a5)), closes [#328](https://github.com/rknightion/tailscale2otel/issues/328)
* **admin:** report actual OTLP delivery state instead of a collector proxy ([5c38fd2](https://github.com/rknightion/tailscale2otel/commit/5c38fd2906565d93832f8901d98b9a2012333ab3)), closes [#317](https://github.com/rknightion/tailscale2otel/issues/317)
* **admin:** show update availability on the status page ([e736ba2](https://github.com/rknightion/tailscale2otel/commit/e736ba219bc519b0802bf6c1dd6fdf877302a15f)), closes [#330](https://github.com/rknightion/tailscale2otel/issues/330)
* **alerts:** canonical recording rules, SLO burn-rate alerts, and approval-workflow alerts ([623240e](https://github.com/rknightion/tailscale2otel/commit/623240e8bf55b59b945cb0ec1e008e26062ce04a)), closes [#406](https://github.com/rknightion/tailscale2otel/issues/406) [#398](https://github.com/rknightion/tailscale2otel/issues/398) [#410](https://github.com/rknightion/tailscale2otel/issues/410)
* **alerts:** close the notification gap on object-store, receiver, drop and posture faults ([99c1226](https://github.com/rknightion/tailscale2otel/commit/99c12265d5d71f1cb97b3a98291043a5885826ec)), closes [#399](https://github.com/rknightion/tailscale2otel/issues/399) [#401](https://github.com/rknightion/tailscale2otel/issues/401) [#402](https://github.com/rknightion/tailscale2otel/issues/402) [#404](https://github.com/rknightion/tailscale2otel/issues/404) [#405](https://github.com/rknightion/tailscale2otel/issues/405)
* **alerts:** enable ConfigInvalid by default, and record standing Grafana push authorization ([fec022a](https://github.com/rknightion/tailscale2otel/commit/fec022ab6be0b2b1b95e77fafb4087c0788d031a))
* **alerts:** per-rule evaluation policy, runbook and panel links, and a real PromQL parser gate ([e54a4cd](https://github.com/rknightion/tailscale2otel/commit/e54a4cd2a8d83d6e9e58e0a135cd5d8b66aba9cc))
* **annotations:** publish curated tailnet events to Grafana as annotations ([8d2ab97](https://github.com/rknightion/tailscale2otel/commit/8d2ab9740ba1d5785c8e2cec3ee9c90d5358243c)), closes [#518](https://github.com/rknightion/tailscale2otel/issues/518)
* **api:** freeze availability-state, scope-class and entity-age seams ([da772f5](https://github.com/rknightion/tailscale2otel/commit/da772f5387207ead5ceab99b2b2394ef9564772e))
* **api:** restore dropped telemetry dimensions and classify API availability ([7445cc6](https://github.com/rknightion/tailscale2otel/commit/7445cc620acb049fbe02d142781a5bdd20fc5268)), closes [#411](https://github.com/rknightion/tailscale2otel/issues/411) [#412](https://github.com/rknightion/tailscale2otel/issues/412) [#413](https://github.com/rknightion/tailscale2otel/issues/413) [#414](https://github.com/rknightion/tailscale2otel/issues/414) [#415](https://github.com/rknightion/tailscale2otel/issues/415) [#416](https://github.com/rknightion/tailscale2otel/issues/416) [#417](https://github.com/rknightion/tailscale2otel/issues/417) [#418](https://github.com/rknightion/tailscale2otel/issues/418) [#419](https://github.com/rknightion/tailscale2otel/issues/419) [#420](https://github.com/rknightion/tailscale2otel/issues/420) [#421](https://github.com/rknightion/tailscale2otel/issues/421) [#425](https://github.com/rknightion/tailscale2otel/issues/425) [#426](https://github.com/rknightion/tailscale2otel/issues/426) [#427](https://github.com/rknightion/tailscale2otel/issues/427) [#428](https://github.com/rknightion/tailscale2otel/issues/428) [#429](https://github.com/rknightion/tailscale2otel/issues/429) [#430](https://github.com/rknightion/tailscale2otel/issues/430)
* **api:** version and publish the admin status/flows API contract ([fde409f](https://github.com/rknightion/tailscale2otel/commit/fde409f6c5a98bda35e2c5fe07e0d770f7facb47)), closes [#323](https://github.com/rknightion/tailscale2otel/issues/323)
* **auditlogs:** ingest the configuration-log S3 export ([2018f07](https://github.com/rknightion/tailscale2otel/commit/2018f0722731247626fa6a555187c49bd989afcf)), closes [#288](https://github.com/rknightion/tailscale2otel/issues/288)
* **catalog:** gate every signal on a declared dashboard/rule disposition ([1d2203b](https://github.com/rknightion/tailscale2otel/commit/1d2203bae9b7b0ae7c474cfbdb2cf25cfb343410)), closes [#390](https://github.com/rknightion/tailscale2otel/issues/390)
* **catalog:** gate that every emitted signal reaches a panel ([4617b10](https://github.com/rknightion/tailscale2otel/commit/4617b10ba8f36fa6eeeb4ba1ca71a74b008fd618)), closes [#526](https://github.com/rknightion/tailscale2otel/issues/526)
* **checkpoint:** use a platform-appropriate state path on native runs ([3ce3461](https://github.com/rknightion/tailscale2otel/commit/3ce346105b786d0c657cd5e3d4520c9879f51290)), closes [#336](https://github.com/rknightion/tailscale2otel/issues/336)
* **ci:** authenticate the live-contract lane with workload identity federation ([8de36d5](https://github.com/rknightion/tailscale2otel/commit/8de36d52f7c979e1e71f83d7b2997750d8eebcb1)), closes [#355](https://github.com/rknightion/tailscale2otel/issues/355)
* **ci:** check documented install commands against the real schemas ([123fe72](https://github.com/rknightion/tailscale2otel/commit/123fe7237f1f98bab4456092dbfb86bf1480e349)), closes [#445](https://github.com/rknightion/tailscale2otel/issues/445)
* **ci:** review the Tailscale changelog for new observability surfaces ([cfc60b9](https://github.com/rknightion/tailscale2otel/commit/cfc60b942bcf6fd3190c269d491ef31142eaa91f)), closes [#446](https://github.com/rknightion/tailscale2otel/issues/446)
* **ci:** watch the committed IANA service registry for staleness ([a0fdd75](https://github.com/rknightion/tailscale2otel/commit/a0fdd75835a47140686fa1412200cbcb7fb35621)), closes [#440](https://github.com/rknightion/tailscale2otel/issues/440)
* **cli:** add -preflight and -once single-cycle execution modes ([ee7e206](https://github.com/rknightion/tailscale2otel/commit/ee7e2067e84e454ede90f549938995d21519a3b0)), closes [#311](https://github.com/rknightion/tailscale2otel/issues/311)
* **cli:** add a native -healthcheck readiness probe ([4cc1bb3](https://github.com/rknightion/tailscale2otel/commit/4cc1bb318f80d9707990157ebf13e51d1468420b)), closes [#337](https://github.com/rknightion/tailscale2otel/issues/337)
* **cli:** print the effective redacted configuration and where each key came from ([355c8b1](https://github.com/rknightion/tailscale2otel/commit/355c8b147660aaa6dff4561b5af0bf9ef16225f5)), closes [#309](https://github.com/rknightion/tailscale2otel/issues/309)
* **config:** add log_format for one-record-per-line JSON logs ([c156ccd](https://github.com/rknightion/tailscale2otel/commit/c156ccdb785ed3fcb61b9f2998f65bfe8e00c8e2)), closes [#312](https://github.com/rknightion/tailscale2otel/issues/312)
* **config:** advise when a flow-cardinality key is inert under the current metrics_mode ([681fe1e](https://github.com/rknightion/tailscale2otel/commit/681fe1e355e460c62d481a4f487bfe0d3207b266)), closes [#525](https://github.com/rknightion/tailscale2otel/issues/525)
* **config:** generate a standalone JSON Schema for the application config ([b15156f](https://github.com/rknightion/tailscale2otel/commit/b15156fe46bc4889715d30c63c3fa494f2883ea2)), closes [#308](https://github.com/rknightion/tailscale2otel/issues/308)
* **config:** report every configuration diagnostic in one pass ([95da889](https://github.com/rknightion/tailscale2otel/commit/95da88933c00e363349921f64237f472fe409802)), closes [#307](https://github.com/rknightion/tailscale2otel/issues/307)
* **dashboards:** add the Kubernetes Audit tab ([0e02a05](https://github.com/rknightion/tailscale2otel/commit/0e02a05108f184e6a46a63440e0e77895f8d6717))
* **deploy:** add file-based Docker Compose secrets, and fix audit objectstore ([a544576](https://github.com/rknightion/tailscale2otel/commit/a5445768686a1439b03213bf5e6de0179e404b7a)), closes [#334](https://github.com/rknightion/tailscale2otel/issues/334)
* **deploy:** ship a validated Alloy gateway recipe ([5c422ab](https://github.com/rknightion/tailscale2otel/commit/5c422ab0de6b7f7dc68802d9243d7ea7de00574d)), closes [#363](https://github.com/rknightion/tailscale2otel/issues/363)
* **docs:** take the fleet project icon for the site logo and favicon ([a6db322](https://github.com/rknightion/tailscale2otel/commit/a6db3220c09aff21d9070ab84e505c1b84f73363))
* **docs:** take the fleet-generated social card ([4bffb6b](https://github.com/rknightion/tailscale2otel/commit/4bffb6b715b0a865312d38f874401a0485c07146))
* **enrichment:** add optional local GeoIP and ASN enrichment ([47d9e49](https://github.com/rknightion/tailscale2otel/commit/47d9e4963c7cce9de479d22b752b0afc9b30b108)), closes [#461](https://github.com/rknightion/tailscale2otel/issues/461)
* **flows:** export the filtered connection list as CSV and JSON ([cebb89d](https://github.com/rknightion/tailscale2otel/commit/cebb89dd44fa566d445ab1219f983e52357317ce)), closes [#299](https://github.com/rknightion/tailscale2otel/issues/299)
* **flows:** filter and paginate connections server-side ([86833de](https://github.com/rknightion/tailscale2otel/commit/86833de5351b1a382e34d702f96c7392fa671413)), closes [#296](https://github.com/rknightion/tailscale2otel/issues/296)
* **flows:** make flow-store capacity and memory policy configurable ([ead2f47](https://github.com/rknightion/tailscale2otel/commit/ead2f47cda562c35a13e4872ba367d68516c6d6d)), closes [#329](https://github.com/rknightion/tailscale2otel/issues/329)
* **flows:** put the flow view's state in shareable URLs ([1f32b15](https://github.com/rknightion/tailscale2otel/commit/1f32b152a99ffd3fb8497268dede61d1c0a3476f)), closes [#298](https://github.com/rknightion/tailscale2otel/issues/298)
* **flowstore:** optional SQLite persistence for multi-day /flows history ([4b25580](https://github.com/rknightion/tailscale2otel/commit/4b2558053d74d828e4dc663e19c66cb821d9175c)), closes [#294](https://github.com/rknightion/tailscale2otel/issues/294)
* **github:** replace free-form issue templates with structured forms ([ea9f9bf](https://github.com/rknightion/tailscale2otel/commit/ea9f9bf59800dffb34aebc43c5e3c9bd6356f283)), closes [#444](https://github.com/rknightion/tailscale2otel/issues/444)
* **grafana:** group the flagship's ten flat tabs into a two-level navigation ([e0e20b9](https://github.com/rknightion/tailscale2otel/commit/e0e20b9784baa4a1bf0db6eae8c56be86221268a)), closes [#495](https://github.com/rknightion/tailscale2otel/issues/495)
* **grafana:** rebuild the health dashboard around pipeline stages ([6a16821](https://github.com/rknightion/tailscale2otel/commit/6a16821ea7d687d3f087a8cb996d34029875e05b)), closes [#526](https://github.com/rknightion/tailscale2otel/issues/526)
* **grafana:** rebuild the product dashboard around domains and sub-tabs ([b3572e8](https://github.com/rknightion/tailscale2otel/commit/b3572e853dfcf2128d510a244b2db2567efd2330)), closes [#526](https://github.com/rknightion/tailscale2otel/issues/526)
* **helm:** add an opt-in Prometheus Operator PodMonitor ([e01d6c6](https://github.com/rknightion/tailscale2otel/commit/e01d6c696b7b43dd53c7bbc924f8d5ef2db9a15d)), closes [#345](https://github.com/rknightion/tailscale2otel/issues/345)
* **helm:** add an opt-in Prometheus ServiceMonitor ([9ade22f](https://github.com/rknightion/tailscale2otel/commit/9ade22f6f7ac0622d7cde6d05fa75c5fc23e1a33)), closes [#458](https://github.com/rknightion/tailscale2otel/issues/458)
* **helm:** add default-off, per-listener Services ([1e5d29a](https://github.com/rknightion/tailscale2otel/commit/1e5d29ab58b2eb8d1db4ced41806fa8080c60196)), closes [#344](https://github.com/rknightion/tailscale2otel/issues/344)
* **helm:** add first-class projected Workload Identity Federation tokens ([30589c4](https://github.com/rknightion/tailscale2otel/commit/30589c4f4026e23a8621312de6f53e6ef12432cf)), closes [#343](https://github.com/rknightion/tailscale2otel/issues/343)
* **helm:** add image digests, configurable probes, and a NetworkPolicy baseline ([15cd177](https://github.com/rknightion/tailscale2otel/commit/15cd1778fd765310ebc5947a7019027a31509262)), closes [#349](https://github.com/rknightion/tailscale2otel/issues/349) [#350](https://github.com/rknightion/tailscale2otel/issues/350) [#351](https://github.com/rknightion/tailscale2otel/issues/351)
* **helm:** add opt-in Ingress and Gateway API routes for the receivers ([9993b50](https://github.com/rknightion/tailscale2otel/commit/9993b50b0d079f7fc4c633082a5d423b81ec438d)), closes [#346](https://github.com/rknightion/tailscale2otel/issues/346)
* **helm:** add typed extraEnv and extraEnvFrom with a reserved-name policy ([b188b9a](https://github.com/rknightion/tailscale2otel/commit/b188b9aebeb40138a65df9d439c7cddd82357578)), closes [#348](https://github.com/rknightion/tailscale2otel/issues/348)
* **helm:** support externally managed application config resources ([098dbb3](https://github.com/rknightion/tailscale2otel/commit/098dbb3cc519d13e372ab6280b82dfcec54bddef)), closes [#347](https://github.com/rknightion/tailscale2otel/issues/347)
* improve first-run delivery experience ([2cf4644](https://github.com/rknightion/tailscale2otel/commit/2cf46446d5c6a7a30ea6f7d0c54d61ec9889d522))
* **ingest:** add optional durable receiver WAL ([04384a2](https://github.com/rknightion/tailscale2otel/commit/04384a20e6ae47f2ab08425907f729f4e026979b)), closes [#279](https://github.com/rknightion/tailscale2otel/issues/279)
* **ingest:** expose trust and harden receiver edges ([e88c1c5](https://github.com/rknightion/tailscale2otel/commit/e88c1c5076796672fe21cc364b2fff7ed78e84d4)), closes [#262](https://github.com/rknightion/tailscale2otel/issues/262) [#263](https://github.com/rknightion/tailscale2otel/issues/263) [#265](https://github.com/rknightion/tailscale2otel/issues/265) [#266](https://github.com/rknightion/tailscale2otel/issues/266) [#270](https://github.com/rknightion/tailscale2otel/issues/270) [#275](https://github.com/rknightion/tailscale2otel/issues/275) [#276](https://github.com/rknightion/tailscale2otel/issues/276) [#281](https://github.com/rknightion/tailscale2otel/issues/281)
* **ingest:** make event ingestion trustworthy ([6561eb7](https://github.com/rknightion/tailscale2otel/commit/6561eb75b5da6e0cbb158a6e8354c81f8b11510d)), closes [#258](https://github.com/rknightion/tailscale2otel/issues/258) [#259](https://github.com/rknightion/tailscale2otel/issues/259) [#260](https://github.com/rknightion/tailscale2otel/issues/260) [#261](https://github.com/rknightion/tailscale2otel/issues/261) [#264](https://github.com/rknightion/tailscale2otel/issues/264) [#267](https://github.com/rknightion/tailscale2otel/issues/267) [#268](https://github.com/rknightion/tailscale2otel/issues/268) [#269](https://github.com/rknightion/tailscale2otel/issues/269) [#272](https://github.com/rknightion/tailscale2otel/issues/272) [#273](https://github.com/rknightion/tailscale2otel/issues/273) [#274](https://github.com/rknightion/tailscale2otel/issues/274) [#278](https://github.com/rknightion/tailscale2otel/issues/278) [#280](https://github.com/rknightion/tailscale2otel/issues/280) [#282](https://github.com/rknightion/tailscale2otel/issues/282)
* **k8saudit:** export metrics and logs from tsrecorder Kubernetes audit data ([b61fe7a](https://github.com/rknightion/tailscale2otel/commit/b61fe7ae5e0f0c00d718f90bacd0e60cad7eb5f9)), closes [#462](https://github.com/rknightion/tailscale2otel/issues/462)
* mint release-please token from the OpenBao broker ([fec703d](https://github.com/rknightion/tailscale2otel/commit/fec703d18bf4ca042ccb93983fdc31562320c847))
* mint the docs-sync token from the OpenBao broker ([b618f04](https://github.com/rknightion/tailscale2otel/commit/b618f04885df958de551efb98e8435bc4034e972))
* **oas:** detect parameter, success-status and media-type drift ([ffb7a95](https://github.com/rknightion/tailscale2otel/commit/ffb7a9555472ecf52368dfb5330adf6cf6601279)), closes [#432](https://github.com/rknightion/tailscale2otel/issues/432)
* **objectstore:** expose provider request and cursor-health telemetry ([6709c54](https://github.com/rknightion/tailscale2otel/commit/6709c54d65968eb22dadec220cdde7e39ef3f97a)), closes [#451](https://github.com/rknightion/tailscale2otel/issues/451)
* **objectstore:** support flat copied-export layouts ([0794f61](https://github.com/rknightion/tailscale2otel/commit/0794f61a1658776888120d36a806200117aa5792)), closes [#293](https://github.com/rknightion/tailscale2otel/issues/293)
* **objectstore:** support per-tailnet storage destinations and credentials ([ddef918](https://github.com/rknightion/tailscale2otel/commit/ddef9181e92240a2e2fa783c9e89f1cb79dcd4e2)), closes [#284](https://github.com/rknightion/tailscale2otel/issues/284)
* **observability:** installable alert profiles, dashboard a11y and budget gates, and deployment drift verification ([ea53c30](https://github.com/rknightion/tailscale2otel/commit/ea53c30c42d0e1bd4ab9211f5bed61fd978d28ae)), closes [#389](https://github.com/rknightion/tailscale2otel/issues/389) [#396](https://github.com/rknightion/tailscale2otel/issues/396) [#397](https://github.com/rknightion/tailscale2otel/issues/397) [#408](https://github.com/rknightion/tailscale2otel/issues/408)
* **observability:** move alerts to gcx-native manifests and close the 71-signal coverage gap ([9a3674c](https://github.com/rknightion/tailscale2otel/commit/9a3674c4ffa2c700fbddb33a409b91578818412a))
* **observability:** stop rendering unchecked security signals as healthy zeros, and compare ingestion across sources ([db37106](https://github.com/rknightion/tailscale2otel/commit/db37106cb68679564a5506e52d539e07347f07a5)), closes [#395](https://github.com/rknightion/tailscale2otel/issues/395) [#400](https://github.com/rknightion/tailscale2otel/issues/400)
* **rdns:** serve a resolved PTR name past its TTL while refreshing ([e17a3cc](https://github.com/rknightion/tailscale2otel/commit/e17a3cca439463c1997b6d1e10edac58479913c2)), closes [#297](https://github.com/rknightion/tailscale2otel/issues/297)
* **release:** read the published release back and fail when it is incomplete ([3c413e1](https://github.com/rknightion/tailscale2otel/commit/3c413e14ff1cbb1d344971aae46ec5a54c3988d0)), closes [#442](https://github.com/rknightion/tailscale2otel/issues/442)
* **s3:** support AWS container credential providers ([26bb03c](https://github.com/rknightion/tailscale2otel/commit/26bb03ceaaf321e837b5753a0de6fd8e854200be)), closes [#452](https://github.com/rknightion/tailscale2otel/issues/452)
* **telemetry:** bound log records, trace dark clients, harden /metrics ([3858408](https://github.com/rknightion/tailscale2otel/commit/38584084b9f395a48f8444d369385682a023cda9)), closes [#356](https://github.com/rknightion/tailscale2otel/issues/356) [#366](https://github.com/rknightion/tailscale2otel/issues/366) [#367](https://github.com/rknightion/tailscale2otel/issues/367) [#369](https://github.com/rknightion/tailscale2otel/issues/369) [#371](https://github.com/rknightion/tailscale2otel/issues/371) [#374](https://github.com/rknightion/tailscale2otel/issues/374) [#375](https://github.com/rknightion/tailscale2otel/issues/375) [#376](https://github.com/rknightion/tailscale2otel/issues/376) [#377](https://github.com/rknightion/tailscale2otel/issues/377) [#378](https://github.com/rknightion/tailscale2otel/issues/378) [#379](https://github.com/rknightion/tailscale2otel/issues/379)
* **telemetry:** finish the OTLP exporter core, sampling and profiling work ([0084290](https://github.com/rknightion/tailscale2otel/commit/0084290102b215ca653ce006d7766ef5273a4f49)), closes [#357](https://github.com/rknightion/tailscale2otel/issues/357) [#358](https://github.com/rknightion/tailscale2otel/issues/358) [#359](https://github.com/rknightion/tailscale2otel/issues/359) [#360](https://github.com/rknightion/tailscale2otel/issues/360) [#361](https://github.com/rknightion/tailscale2otel/issues/361) [#362](https://github.com/rknightion/tailscale2otel/issues/362) [#365](https://github.com/rknightion/tailscale2otel/issues/365) [#370](https://github.com/rknightion/tailscale2otel/issues/370) [#372](https://github.com/rknightion/tailscale2otel/issues/372) [#373](https://github.com/rknightion/tailscale2otel/issues/373) [#380](https://github.com/rknightion/tailscale2otel/issues/380) [#383](https://github.com/rknightion/tailscale2otel/issues/383) [#384](https://github.com/rknightion/tailscale2otel/issues/384)
* **tls:** reload listener certificates in place and expose expiry health ([9b92b48](https://github.com/rknightion/tailscale2otel/commit/9b92b48d5154762f8d95fb2dcd20af05daac3f06)), closes [#316](https://github.com/rknightion/tailscale2otel/issues/316)


### Bug Fixes

* **acl:** send the policy to /acl/validate, and stop 4xx reading as transient ([1e488ef](https://github.com/rknightion/tailscale2otel/commit/1e488ef2de2a3909c6976d58fc099e016b9b87ee)), closes [#523](https://github.com/rknightion/tailscale2otel/issues/523)
* **admin:** derive the status verdict from the same component state as /readyz ([3aaab59](https://github.com/rknightion/tailscale2otel/commit/3aaab590568ba0faa963cac2541cad02bbbb9a65)), closes [#318](https://github.com/rknightion/tailscale2otel/issues/318)
* **admin:** make the /events explorer keyboard- and screen-reader-legible ([25e5b2b](https://github.com/rknightion/tailscale2otel/commit/25e5b2b5eac36f0408ab50c64527a3cb01dc50f4)), closes [#512](https://github.com/rknightion/tailscale2otel/issues/512)
* **admin:** make the default landing page safely usable ([3584827](https://github.com/rknightion/tailscale2otel/commit/3584827fa621de3b1cfdf67c6b550631fadc050e)), closes [#314](https://github.com/rknightion/tailscale2otel/issues/314)
* **alerts:** 10m pending window on exporter-down and config-invalid ([b399957](https://github.com/rknightion/tailscale2otel/commit/b399957106f307481d33c5fc30804483941bf72f)), closes [#529](https://github.com/rknightion/tailscale2otel/issues/529)
* **alerts:** correct the three stale rule counts that have had CI red ([b27ed99](https://github.com/rknightion/tailscale2otel/commit/b27ed99cc32f1c8449ed58b20c52bf538f7fe85d))
* **alerts:** execErrState spells its OK state "Ok", not "OK" — corrected by a real push ([5eead5b](https://github.com/rknightion/tailscale2otel/commit/5eead5b2d8df50788960019bafdb3e2cd66a11af))
* **alerts:** key credential and auth alerts off semantics, not raw counts ([2b48098](https://github.com/rknightion/tailscale2otel/commit/2b48098010ae1017e567404c66f00c90937bc6a7))
* **api:** complete API fidelity drift contracts ([fb4f877](https://github.com/rknightion/tailscale2otel/commit/fb4f877a5c6b0a48f6cfc67eecf09342804e96fd)), closes [#422](https://github.com/rknightion/tailscale2otel/issues/422) [#423](https://github.com/rknightion/tailscale2otel/issues/423) [#424](https://github.com/rknightion/tailscale2otel/issues/424)
* apply CodeRabbit auto-fixes ([b8634c4](https://github.com/rknightion/tailscale2otel/commit/b8634c4d4571bf588436798970625e43537858c0))
* author is Rob Knight, not Rob Knighton ([54ef3c7](https://github.com/rknightion/tailscale2otel/commit/54ef3c7260d82f815b5f11a99684e34af6b1eed1))
* **catalog:** a template variable is not a panel ([c54e0df](https://github.com/rknightion/tailscale2otel/commit/c54e0dfebcfad2909d45a5da55354882df55c467))
* **ci:** aggregate matrix failures before filing, so one defect files one issue ([b695f27](https://github.com/rknightion/tailscale2otel/commit/b695f270dda59bea65469162235bdb45429b9dc4)), closes [#435](https://github.com/rknightion/tailscale2otel/issues/435)
* **ci:** satisfy workflow contracts ([d8a56ad](https://github.com/rknightion/tailscale2otel/commit/d8a56ad0af5b9dacee1992b92ed4a92013a9bdd7))
* **ci:** stop two version/timing-fragile tests failing correct code ([cb07b5d](https://github.com/rknightion/tailscale2otel/commit/cb07b5ded33eff4f6559ec2250a74aa412e408d6)), closes [#521](https://github.com/rknightion/tailscale2otel/issues/521)
* **ci:** the dashboard sync reported success while publishing nothing ([f167a1c](https://github.com/rknightion/tailscale2otel/commit/f167a1ca35f086116c1712c7a02b3564657b2126)), closes [#526](https://github.com/rknightion/tailscale2otel/issues/526)
* **ci:** use a plain if in the live-contract negative case ([2aa2e03](https://github.com/rknightion/tailscale2otel/commit/2aa2e03bea60e5f5a508e0e778160c5d994d90ea))
* **config:** canonicalize listener addresses and feed bind failures to readiness ([6a113d9](https://github.com/rknightion/tailscale2otel/commit/6a113d928127cd316b8942787281673c86ac30c0)), closes [#306](https://github.com/rknightion/tailscale2otel/issues/306)
* **config:** only require a positive prometheus.max_requests_in_flight while the endpoint is on ([9df928e](https://github.com/rknightion/tailscale2otel/commit/9df928e6b1e1a8cc5b9fea3836b8d47d1c659947))
* **config:** protect object-store credentials ([3260963](https://github.com/rknightion/tailscale2otel/commit/32609631dd2d29bdce9a71a77097b0716e56fbab)), closes [#449](https://github.com/rknightion/tailscale2otel/issues/449)
* **config:** reject global objectstore in multi-tailnet mode ([c6f66b0](https://github.com/rknightion/tailscale2otel/commit/c6f66b015013b9ad11e80b85d27dc0f2b22ef135)), closes [#283](https://github.com/rknightion/tailscale2otel/issues/283)
* **config:** reject unknown YAML configuration keys ([96c5e9b](https://github.com/rknightion/tailscale2otel/commit/96c5e9b5ab3c01117d2922dfefc3d468a0e8b834)), closes [#303](https://github.com/rknightion/tailscale2otel/issues/303)
* **config:** resolve relative file paths against the config file's directory ([8154192](https://github.com/rknightion/tailscale2otel/commit/81541922a07cf59ad0437726b3a3a2d933d1874e)), closes [#310](https://github.com/rknightion/tailscale2otel/issues/310)
* **config:** validate complete, loadable TLS keypairs for every listener ([a3b96be](https://github.com/rknightion/tailscale2otel/commit/a3b96be4c7abcd7cf9f0d5cbd1d1c27f5eb7e14d)), closes [#305](https://github.com/rknightion/tailscale2otel/issues/305)
* **deploy:** align Compose and Kubernetes shutdown budgets with the staged drain ([ac58014](https://github.com/rknightion/tailscale2otel/commit/ac580149a20a0d78f77f9858a1ae7cf45a0b356b)), closes [#332](https://github.com/rknightion/tailscale2otel/issues/332)
* **deploy:** move the Compose config-file mount to an override file ([a9311a3](https://github.com/rknightion/tailscale2otel/commit/a9311a3c41dc8ca41488dfe04177a5504740c42d)), closes [#333](https://github.com/rknightion/tailscale2otel/issues/333)
* **deploy:** split the published-image Compose from the developer build ([1faaa5e](https://github.com/rknightion/tailscale2otel/commit/1faaa5eb32ae198100bb2e45f0b15fb40fddca8e)), closes [#335](https://github.com/rknightion/tailscale2otel/issues/335)
* **deps:** update github.com/tailscale/hujson digest to b80ff77 ([#508](https://github.com/rknightion/tailscale2otel/issues/508)) ([6ca7c86](https://github.com/rknightion/tailscale2otel/commit/6ca7c8665c7e46c0e0cae06da02842b20d620e73))
* **deps:** update go dependencies ([#572](https://github.com/rknightion/tailscale2otel/issues/572)) ([34589ff](https://github.com/rknightion/tailscale2otel/commit/34589ff4b48f6ca3fece57e417ff65e3ac644db4))
* **deps:** update module github.com/knadh/koanf/parsers/yaml to v1.1.1 ([#535](https://github.com/rknightion/tailscale2otel/issues/535)) ([7480415](https://github.com/rknightion/tailscale2otel/commit/7480415e08440ec09f4b49f9507e77c4ec0319e0))
* **deps:** update module github.com/knadh/koanf/providers/env/v2 to v2.0.1 ([#536](https://github.com/rknightion/tailscale2otel/issues/536)) ([1ecd7dd](https://github.com/rknightion/tailscale2otel/commit/1ecd7ddd52fa05e3703e7b84a1d367a622b447df))
* **deps:** update module github.com/knadh/koanf/providers/structs to v1.0.1 ([#537](https://github.com/rknightion/tailscale2otel/issues/537)) ([44f863d](https://github.com/rknightion/tailscale2otel/commit/44f863dee7f0c1fe2dcf0186d2e5061b20be0d79))
* **deps:** update module github.com/knadh/koanf/v2 to v2.3.6 ([#538](https://github.com/rknightion/tailscale2otel/issues/538)) ([66e0992](https://github.com/rknightion/tailscale2otel/commit/66e0992feccb43afb70782f341bd997795517292))
* **deps:** update module github.com/prometheus/prometheus to v0.313.2 ([#522](https://github.com/rknightion/tailscale2otel/issues/522)) ([2a032d4](https://github.com/rknightion/tailscale2otel/commit/2a032d41811aa3e1194784396bcc36f6e1aae9fa))
* **deps:** update module go.opentelemetry.io/proto/otlp to v1.11.0 ([#499](https://github.com/rknightion/tailscale2otel/issues/499)) ([f4bf065](https://github.com/rknightion/tailscale2otel/commit/f4bf065b25ade1f1ddb5fdce490beaf9c7020133))
* **deps:** update module google.golang.org/grpc to v1.83.0 ([#520](https://github.com/rknightion/tailscale2otel/issues/520)) ([924be79](https://github.com/rknightion/tailscale2otel/commit/924be79f514bc6a9930509d631c47efc5eb15bc8))
* **deps:** update module google.golang.org/grpc to v1.83.2 ([#576](https://github.com/rknightion/tailscale2otel/issues/576)) ([391dfa0](https://github.com/rknightion/tailscale2otel/commit/391dfa098d5952aefedbf19a426fe27bd08be829))
* **deps:** update module modernc.org/sqlite to v1.56.0 ([#530](https://github.com/rknightion/tailscale2otel/issues/530)) ([93c61f5](https://github.com/rknightion/tailscale2otel/commit/93c61f587a47c04c762e9cfc1f650fb4c3dbe64b))
* **deps:** update opentelemetry to v1.46.0 / log v0.22.0 ([613d0a3](https://github.com/rknightion/tailscale2otel/commit/613d0a3f90b6dd6b42f21261df98e1f608c22020))
* **dev:** make cloud setup cross-agent compatible ([34f8f78](https://github.com/rknightion/tailscale2otel/commit/34f8f7893ba6e8d43757f18746f5ecf13c904b18))
* **flowlogs:** preserve node identity through poll dedup ([622773c](https://github.com/rknightion/tailscale2otel/commit/622773cb53b27ab4b05ebc3383db71069d1bc5e4)), closes [#257](https://github.com/rknightion/tailscale2otel/issues/257)
* **flowstore:** give pre-4.0.0 flow databases a way to be adopted ([0741792](https://github.com/rknightion/tailscale2otel/commit/0741792f7a16a18acede3d786234cda5dff21a21))
* **flowstore:** order recent rows by event time and scope them to the query window ([3de4fc7](https://github.com/rknightion/tailscale2otel/commit/3de4fc71ad879aeb4201219281012acb77b09d64)), closes [#295](https://github.com/rknightion/tailscale2otel/issues/295) [#301](https://github.com/rknightion/tailscale2otel/issues/301)
* **flows:** version policy verdicts so retained traffic cannot join to the wrong rules ([a6d14c9](https://github.com/rknightion/tailscale2otel/commit/a6d14c9af44bdf66f9b7a4a5235ac08e26b33f33)), closes [#302](https://github.com/rknightion/tailscale2otel/issues/302)
* **grafana:** give boolean panels semantic polarity and stop zero-filling absence ([be4fc7d](https://github.com/rknightion/tailscale2otel/commit/be4fc7d141a466e22031bd35d440ec991cf938d9)), closes [#385](https://github.com/rknightion/tailscale2otel/issues/385)
* **grafana:** panels referenced variables that were not in scope ([c300199](https://github.com/rknightion/tailscale2otel/commit/c30019908a25d1369a4ee41d5462fa4cc7775732)), closes [#526](https://github.com/rknightion/tailscale2otel/issues/526)
* **grafana:** the degradation row rendered on a healthy exporter ([a273aa7](https://github.com/rknightion/tailscale2otel/commit/a273aa7d64b8f654daa464f5ec9828981b283e10)), closes [#526](https://github.com/rknightion/tailscale2otel/issues/526)
* **helm:** make liveness/readiness probes follow admin TLS ([82e43b5](https://github.com/rknightion/tailscale2otel/commit/82e43b50b4ee25a7f3a35a13f972733ad88ee3ba)), closes [#342](https://github.com/rknightion/tailscale2otel/issues/342)
* **helm:** reject unknown values with a strict chart schema ([69d7746](https://github.com/rknightion/tailscale2otel/commit/69d7746685c96aef4f6f8d1bb22db3310adb8b81)), closes [#304](https://github.com/rknightion/tailscale2otel/issues/304)
* **helm:** render every Secret-typed config field into a Secret, not a ConfigMap ([a51d901](https://github.com/rknightion/tailscale2otel/commit/a51d901b9b17e8920b66a737fe0711e682d7bf04)), closes [#518](https://github.com/rknightion/tailscale2otel/issues/518)
* **helm:** stop teaching credentials on the command line ([7cd9a84](https://github.com/rknightion/tailscale2otel/commit/7cd9a8495420f2f6b969b3728f345d9375626439)), closes [#341](https://github.com/rknightion/tailscale2otel/issues/341)
* **helm:** stop the chart README drifting on every release PR ([a78961e](https://github.com/rknightion/tailscale2otel/commit/a78961e80012dc8d5dcad14751c263423de6ef87)), closes [#521](https://github.com/rknightion/tailscale2otel/issues/521)
* **metrics:** make Prometheus authentication fail closed ([5a0be19](https://github.com/rknightion/tailscale2otel/commit/5a0be19c31392fed631aa097dfe95a7514b35aee)), closes [#315](https://github.com/rknightion/tailscale2otel/issues/315)
* **oas:** parse OpenAPI 3.1 type arrays and typed maps in drift analysis ([607b32a](https://github.com/rknightion/tailscale2otel/commit/607b32aaa8d000f8ded3f54dbc838df9fa0d64a7)), closes [#431](https://github.com/rknightion/tailscale2otel/issues/431)
* **objectstore:** accept .json.zstd and .json.gzip export keys ([3df1b32](https://github.com/rknightion/tailscale2otel/commit/3df1b32634a32d6116679db63f191f4c5ec6bd3f)), closes [#496](https://github.com/rknightion/tailscale2otel/issues/496)
* **objectstore:** bound decoded ingestion work ([f969025](https://github.com/rknightion/tailscale2otel/commit/f969025543446239b0a59af80ce36ca8d394c5a0)), closes [#292](https://github.com/rknightion/tailscale2otel/issues/292)
* **objectstore:** commit objects only after complete preparation ([f3e871e](https://github.com/rknightion/tailscale2otel/commit/f3e871e0a27c36e3b4fbf3febd5cb31aca431c64)), closes [#448](https://github.com/rknightion/tailscale2otel/issues/448)
* **objectstore:** fail an object whose every row fails to decode ([4f670ab](https://github.com/rknightion/tailscale2otel/commit/4f670abb636afeed079195b9a2634b0477476269)), closes [#497](https://github.com/rknightion/tailscale2otel/issues/497)
* **objectstore:** persist bounded listing progress ([8c951e2](https://github.com/rknightion/tailscale2otel/commit/8c951e2c4c8d57ad22f83de9fb11db3f7f58c532)), closes [#285](https://github.com/rknightion/tailscale2otel/issues/285)
* **objectstore:** persist failed ingestion gaps ([bf7e92d](https://github.com/rknightion/tailscale2otel/commit/bf7e92def8c5729ac749a7f2f5169635928c34a2)), closes [#286](https://github.com/rknightion/tailscale2otel/issues/286)
* **objectstore:** stop erasing durable listing progress under a leading-slash prefix ([2fb0c45](https://github.com/rknightion/tailscale2otel/commit/2fb0c4568570fcc638c148bf460407cfc399ab58)), closes [#498](https://github.com/rknightion/tailscale2otel/issues/498)
* **objectstore:** support official export keys ([9b9f7a3](https://github.com/rknightion/tailscale2otel/commit/9b9f7a3c24ba86fab75a182646ae9195f717f4e7)), closes [#447](https://github.com/rknightion/tailscale2otel/issues/447)
* pass the JWT role explicitly for docs-sync ([cb91b11](https://github.com/rknightion/tailscale2otel/commit/cb91b1127cdcf445450cb6aeca8d40fc9cfc813a))
* **receivers:** carry trace context onto streamed flow, audit and webhook logs ([0a3536e](https://github.com/rknightion/tailscale2otel/commit/0a3536e38e0ad7f1b846b6a0621b6af33d742504))
* **release:** wait for the module proxy before GoReleaser rebuilds from it ([f59c6f4](https://github.com/rknightion/tailscale2otel/commit/f59c6f4378dc6398049f592b6d08d3af5ba419be)), closes [#254](https://github.com/rknightion/tailscale2otel/issues/254)
* **s3:** preserve configured endpoint base paths ([1e716e1](https://github.com/rknightion/tailscale2otel/commit/1e716e11fa2d0aa23e22f4845b7073619cc99cda)), closes [#290](https://github.com/rknightion/tailscale2otel/issues/290)
* **s3:** stream list responses and refuse oversized ones explicitly ([7ebdb9c](https://github.com/rknightion/tailscale2otel/commit/7ebdb9c4f4408d096f05468c60e3b11f213af515)), closes [#291](https://github.com/rknightion/tailscale2otel/issues/291)
* **security:** close 14 advisories across credentials, admission control and cache trust ([6b65274](https://github.com/rknightion/tailscale2otel/commit/6b652741909106c21fbccafbf203d90a39337a1e))
* **security:** close the EPIC-10 follow-ups across decode budgets, retries, and checkpoints ([15b1038](https://github.com/rknightion/tailscale2otel/commit/15b1038205643528545dd2a77eacf46c2190c178)), closes [#488](https://github.com/rknightion/tailscale2otel/issues/488) [#489](https://github.com/rknightion/tailscale2otel/issues/489) [#490](https://github.com/rknightion/tailscale2otel/issues/490) [#491](https://github.com/rknightion/tailscale2otel/issues/491)
* **security:** close the public hardening backlog across privacy, credentials, and build context ([476f236](https://github.com/rknightion/tailscale2otel/commit/476f236d68423f3971b9ce56e2d6ea23c808267f)), closes [#464](https://github.com/rknightion/tailscale2otel/issues/464) [#465](https://github.com/rknightion/tailscale2otel/issues/465) [#466](https://github.com/rknightion/tailscale2otel/issues/466) [#467](https://github.com/rknightion/tailscale2otel/issues/467) [#468](https://github.com/rknightion/tailscale2otel/issues/468) [#469](https://github.com/rknightion/tailscale2otel/issues/469) [#470](https://github.com/rknightion/tailscale2otel/issues/470) [#471](https://github.com/rknightion/tailscale2otel/issues/471) [#472](https://github.com/rknightion/tailscale2otel/issues/472) [#473](https://github.com/rknightion/tailscale2otel/issues/473) [#474](https://github.com/rknightion/tailscale2otel/issues/474) [#475](https://github.com/rknightion/tailscale2otel/issues/475) [#476](https://github.com/rknightion/tailscale2otel/issues/476)
* **security:** remediate 19 scan findings ([45e489c](https://github.com/rknightion/tailscale2otel/commit/45e489c148de09c8b3aa34d7581f289712aca65f))
* **selfobs:** correct object-store ingest source and byte semantics ([cc6e498](https://github.com/rknightion/tailscale2otel/commit/cc6e4985af4fd4665cfeff8fcd51ac68fe54618d)), closes [#450](https://github.com/rknightion/tailscale2otel/issues/450)
* **status:** light up api-availability for every collector, and make the status page say why ([6acd70f](https://github.com/rknightion/tailscale2otel/commit/6acd70f9972797a77cc2a22894ceef8406336c0b)), closes [#524](https://github.com/rknightion/tailscale2otel/issues/524)
* **status:** show the state of collectors that have no matrix row of their own ([8b674af](https://github.com/rknightion/tailscale2otel/commit/8b674af7e0c61962b7b1c3ce3cffa41b369a3256)), closes [#524](https://github.com/rknightion/tailscale2otel/issues/524)
* **telemetry:** bound OTLP metric export batches ([6b1093a](https://github.com/rknightion/tailscale2otel/commit/6b1093a89fd60f959d35989abc80a9e2b66722f6)), closes [#494](https://github.com/rknightion/tailscale2otel/issues/494)
* **telemetry:** stop identical target_info duplicating on every Prometheus scrape ([844f267](https://github.com/rknightion/tailscale2otel/commit/844f267ac8943788b76217d0d3e05c727c00e650)), closes [#519](https://github.com/rknightion/tailscale2otel/issues/519)


### Refactoring

* **catalog:** delete pending_panel now that every signal has a panel ([e726aad](https://github.com/rknightion/tailscale2otel/commit/e726aad757856c422e729b1ea53dd356c984dbaf)), closes [#526](https://github.com/rknightion/tailscale2otel/issues/526)
* **config:** extract the effective-config projection to a leaf package ([0d31d9f](https://github.com/rknightion/tailscale2otel/commit/0d31d9fa4dddea0782b51db642ef5cb9778bbebd))
* **flowstore:** rename flows.store.path to directory and surface backend health ([5b25a5e](https://github.com/rknightion/tailscale2otel/commit/5b25a5e534513b9161a533f3cebb7fc291d5c8f3)), closes [#294](https://github.com/rknightion/tailscale2otel/issues/294)
* **grafana:** split the dashboard generator into builder, variables and tab modules ([8f36ffd](https://github.com/rknightion/tailscale2otel/commit/8f36ffd711b2c97c02716d1c7a7ee5ec239b1df2)), closes [#495](https://github.com/rknightion/tailscale2otel/issues/495)
* **grafana:** split the flagship dashboard into a tailnet and a health artifact ([372b169](https://github.com/rknightion/tailscale2otel/commit/372b169878a7fd64705b5bfb583f07655de69b80)), closes [#526](https://github.com/rknightion/tailscale2otel/issues/526)
* **objectstore:** add provider-neutral ingestion engine ([d701ebe](https://github.com/rknightion/tailscale2otel/commit/d701ebe45ffabe34b5ccf1d3923372ff38270759)), closes [#453](https://github.com/rknightion/tailscale2otel/issues/453)
* **telemetry:** freeze one Options seam per outstanding EPIC-04 child ([1e63883](https://github.com/rknightion/tailscale2otel/commit/1e63883f7d7d50017f101e24711090b59be515f4))


### Build System

* move the Go module path to /v4 ahead of the 4.0.0 release ([4374b06](https://github.com/rknightion/tailscale2otel/commit/4374b065b3a54ae4e1538393c07a2d21726418ad)), closes [#521](https://github.com/rknightion/tailscale2otel/issues/521)

## [3.0.0](https://github.com/rknightion/tailscale2otel/compare/v2.0.2...v3.0.0) (2026-07-25)


### ⚠ BREAKING CHANGES

* move the module path to /v3 and guard the next major
* **flowlog:** cardinality.flow.destination_service is removed and tailscale.dst.service is emitted unconditionally on both flow metric families. The rollup families already carried it ungated, so the toggle only ever governed the raw ones — the same dimension appeared or vanished with metrics_mode. Its value space is a fixed IANA registry rather than the ephemeral port range, so gating it bought little. An existing destination_service: in a config file is silently ignored (unknown YAML keys are); a stale TS2OTEL_CARDINALITY__FLOW__DESTINATION_SERVICE raises the unknown-variable advisory at startup. Deployments on metrics_mode all/both gain the label on tailscale.network.io/packets, which changes those series' identity.
* **admin:** pii_filter no longer applies to the /flows view or /api/flows.json. An operator who set e.g. pii_filter.emails: false previously saw no user attached to any flow on the admin page, and will now see them; the same holds for hostnames, tailscale_ips and external_ips. Exported telemetry is unchanged — the filter governs it exactly as before. The admin token is now the only control over who can read tailnet identity from this process: if the set of people holding it is wider than the set who may see your users' email addresses, set a narrower admin.auth.token or bind admin.listen to loopback.
* **admin:** with an empty admin.auth.token, the admin status page and its JSON APIs (/, /api/status.json, /api/cardinality.json, /api/config.json, /api/rdns/purge) now return HTTP 403 on any non-loopback admin.listen instead of serving unauthenticated. The default listen is the wildcard :9091, so a default deployment is affected. Set admin.auth.token (e.g. TS2OTEL_ADMIN__AUTH__TOKEN) or bind admin.listen to loopback. The process still starts, and /healthz and /readyz remain open on every bind, so container and Kubernetes probes are unaffected.
* **stream:** an enabled streaming receiver with an empty streaming.token on a non-loopback streaming.listen now refuses every request with HTTP 403 instead of accepting unauthenticated POSTs. Log ingestion stops until streaming.token is set (e.g. TS2OTEL_STREAMING__TOKEN) or the listener is moved to a loopback address. The default listen is the wildcard :8088, so a receiver previously running without a token is affected. A loud ERROR naming both remedies is logged at startup.
* **config:** a config with a polling flowlogs/auditlogs max_window that is positive and <= interval now fails startup validation instead of only warning.
* **nodemetrics:** two node_metrics static targets resolving to the same effective identity (same normalized URL and instance label) now fail config validation instead of being silently double-scraped.
* **stream:** a corrupt or partially undecodable stream batch is now rejected with a 4xx (sender retries) instead of being partially ingested and acknowledged 200. The valid-prefix salvage behaviour is removed.
* **helm:** Helm installs/upgrades with replicaCount != 1 now fail rendering instead of deploying multiple pollers.

### Features

* **aclpolicy:** compile and evaluate a tailnet policy against observed flows ([a0a986e](https://github.com/rknightion/tailscale2otel/commit/a0a986e55c9d0b80fbe7764e65cff65204480037)), closes [#237](https://github.com/rknightion/tailscale2otel/issues/237)
* **admin:** built-in network flow visualiser at /flows ([4427507](https://github.com/rknightion/tailscale2otel/commit/44275077d8f36f85e3a2069bbf17f0b277a856c6)), closes [#237](https://github.com/rknightion/tailscale2otel/issues/237)
* **admin:** emitted-throughput and collector-fleet trend charts ([a1c1fe9](https://github.com/rknightion/tailscale2otel/commit/a1c1fe973f44c8227d012bc41fc77beb0d6c40e9)), closes [#223](https://github.com/rknightion/tailscale2otel/issues/223)
* **admin:** enable admin server by default on :9091 ([730d467](https://github.com/rknightion/tailscale2otel/commit/730d4678b0cbc3910b8c20b13b8f939c0a380609))
* **admin:** identity views on /flows — tag matrix, split tags, identity on connections ([dd81b6b](https://github.com/rknightion/tailscale2otel/commit/dd81b6b8f7fc7ea73874c5ecdfeb46d3ba75a2cb)), closes [#237](https://github.com/rknightion/tailscale2otel/issues/237)
* **admin:** show identity in full on /flows, unfiltered by pii_filter ([b0616a9](https://github.com/rknightion/tailscale2otel/commit/b0616a9add64c95ed21c5185bb2b00c107828d7d)), closes [#241](https://github.com/rknightion/tailscale2otel/issues/241) [#237](https://github.com/rknightion/tailscale2otel/issues/237)
* **admin:** the path-quality section on /flows ([6037e1c](https://github.com/rknightion/tailscale2otel/commit/6037e1cd914c0257bf88fb5de1c5df9a781db395)), closes [#237](https://github.com/rknightion/tailscale2otel/issues/237)
* **admin:** the policy section on /flows ([51937e3](https://github.com/rknightion/tailscale2otel/commit/51937e31dc2322a1d4f6f8c71a1b9a23a142d1fc)), closes [#237](https://github.com/rknightion/tailscale2otel/issues/237)
* **app:** add read-only cardinality and config JSON export endpoints ([30d0672](https://github.com/rknightion/tailscale2otel/commit/30d0672131576cbcff6db80d9da34488d2781458))
* **app:** build cardinality suite, collector freshness and API auth panels ([372627f](https://github.com/rknightion/tailscale2otel/commit/372627f59c29cf9eabddf08a9d7de54b0409f848))
* **app:** extend status DTO for cardinality suite, freshness and API auth ([71449f3](https://github.com/rknightion/tailscale2otel/commit/71449f346ffdd0c458c8167957437fa45a17da4d))
* **app:** feed the ACL evaluator from the acl and users collectors ([5587695](https://github.com/rknightion/tailscale2otel/commit/5587695b8c240b1ba878f0338442bf6f391265cd)), closes [#237](https://github.com/rknightion/tailscale2otel/issues/237)
* **app:** retain per-metric active-series history for cardinality growth view ([d072aac](https://github.com/rknightion/tailscale2otel/commit/d072aaccfa50a69629ebaf6c86281d0a48b1bd89))
* **collector:** track last successful run time for status freshness ([13f60e4](https://github.com/rknightion/tailscale2otel/commit/13f60e420111802a9a7404300401fbb6c29a0607))
* **config:** add cardinality warning/critical thresholds and label-value sample cap ([00b0a65](https://github.com/rknightion/tailscale2otel/commit/00b0a65d116f42f685fd623d198bf71309dc4523))
* **config:** reject positive max_window &lt;= interval for polling log collectors ([376b47d](https://github.com/rknightion/tailscale2otel/commit/376b47dbeaf0374331005f98475625a964a5ff70)), closes [#202](https://github.com/rknightion/tailscale2otel/issues/202)
* **console:** configurable status-page refresh interval (default 5s) ([9b13a84](https://github.com/rknightion/tailscale2otel/commit/9b13a845d610ef82ebc23918d4c1d06bacc788c4)), closes [#215](https://github.com/rknightion/tailscale2otel/issues/215)
* **deploy:** expose cardinality threshold + label-cap knobs in Helm chart ([5991d21](https://github.com/rknightion/tailscale2otel/commit/5991d21195ace8cb0a554728e208f8c02778d882)), closes [#208](https://github.com/rknightion/tailscale2otel/issues/208)
* **flowlog:** carry path, DERP region and identity on the rollup families ([cb73daa](https://github.com/rknightion/tailscale2otel/commit/cb73daa74969e32619129d029bbb8cf8160d34ed)), closes [#243](https://github.com/rknightion/tailscale2otel/issues/243)
* **flowlog:** decode embedded node identity for self-enrichment ([8cb3f7b](https://github.com/rknightion/tailscale2otel/commit/8cb3f7b17e5d38c43c7377c26686363e47bff1d0)), closes [#235](https://github.com/rknightion/tailscale2otel/issues/235)
* **flowlog:** name IP protocol 99 tsmp and explain what a TSMP flow means ([0971527](https://github.com/rknightion/tailscale2otel/commit/097152733415cc2a9a90cf7cb4dc73235acbd16a)), closes [#249](https://github.com/rknightion/tailscale2otel/issues/249)
* **flowlog:** reconcile each connection against the tailnet policy ([c114f92](https://github.com/rknightion/tailscale2otel/commit/c114f9250d9df062b99ca6a029494dfc61e5d818)), closes [#237](https://github.com/rknightion/tailscale2otel/issues/237)
* **flowlogs:** read the S3 export as a third ingestion source ([a8ce1aa](https://github.com/rknightion/tailscale2otel/commit/a8ce1aaa91bd5d424a139e7e2f8cb86428f61d23)), closes [#238](https://github.com/rknightion/tailscale2otel/issues/238)
* **flowstore:** aggregate the policy verdict, unexplained relationships and rule use ([ce15572](https://github.com/rknightion/tailscale2otel/commit/ce155721e2d155ee5f09ce1c8ae68936e44d6fa3)), closes [#237](https://github.com/rknightion/tailscale2otel/issues/237)
* **flowstore:** bounded in-memory store of recent flow activity ([cb40c29](https://github.com/rknightion/tailscale2otel/commit/cb40c293beb1cfc4a44702406501913557807d4b)), closes [#237](https://github.com/rknightion/tailscale2otel/issues/237)
* **flowstore:** classify how each peer was actually reached on the wire ([08d1724](https://github.com/rknightion/tailscale2otel/commit/08d17247650fddb74a45e774736d4462f2367ae1)), closes [#237](https://github.com/rknightion/tailscale2otel/issues/237)
* **pyroscope:** collect all profile types by default incl. goroutine-leak ([fec8915](https://github.com/rknightion/tailscale2otel/commit/fec891504e081d32919b5adc935a637dbe0f7e9f)), closes [#185](https://github.com/rknightion/tailscale2otel/issues/185)
* **stream:** make HEC batch delivery atomic instead of ACKing partial loss ([cfa330a](https://github.com/rknightion/tailscale2otel/commit/cfa330a33d2959e101a02ae0f2342a72e8c729eb)), closes [#201](https://github.com/rknightion/tailscale2otel/issues/201)
* **telemetry:** capture bounded per-label distinct values and plumb the cap from config ([c301413](https://github.com/rknightion/tailscale2otel/commit/c301413e5e7cbccfd4cb34c741555aae303708aa))
* **ui:** tabbed layout, light/dark theme, real charts, cardinality suite ([55cc1e6](https://github.com/rknightion/tailscale2otel/commit/55cc1e6b41886046d1291e31b9570528fafe37f8)), closes [#208](https://github.com/rknightion/tailscale2otel/issues/208)


### Bug Fixes

* **admin:** record the fail-closed status page as a breaking change ([5656149](https://github.com/rknightion/tailscale2otel/commit/5656149cbe863c3def450155d85b8fb3dfb4abca))
* **admin:** refuse the status page on a network-reachable bind with no token ([effb816](https://github.com/rknightion/tailscale2otel/commit/effb816987ef9bb06f297c66aec350166f9c66a2)), closes [#227](https://github.com/rknightion/tailscale2otel/issues/227)
* **alerts:** page on tagged device key expiry only, not untagged user devices ([15bf0ba](https://github.com/rknightion/tailscale2otel/commit/15bf0bafc80d8af90cb585e5cfbd3a89abb4d18c)), closes [#250](https://github.com/rknightion/tailscale2otel/issues/250)
* **api:** bound successful JSON responses before decoding ([054ee09](https://github.com/rknightion/tailscale2otel/commit/054ee098720073a93037d3b0debc06556b0a8cca)), closes [#210](https://github.com/rknightion/tailscale2otel/issues/210) [#211](https://github.com/rknightion/tailscale2otel/issues/211)
* **deps:** update github.com/tailscale/hujson digest to 10d7940 ([#213](https://github.com/rknightion/tailscale2otel/issues/213)) ([2a278df](https://github.com/rknightion/tailscale2otel/commit/2a278dfb1b325f1bb94dfe324718491ffb4a7972))
* **deps:** update github.com/tailscale/hujson digest to 78b5b16 ([#225](https://github.com/rknightion/tailscale2otel/issues/225)) ([e38676c](https://github.com/rknightion/tailscale2otel/commit/e38676cb3fdb7f6f9d40a3d8404c4cc0f1c317c4))
* **deps:** update module github.com/klauspost/compress to v1.19.1 ([#216](https://github.com/rknightion/tailscale2otel/issues/216)) ([c8fe2f4](https://github.com/rknightion/tailscale2otel/commit/c8fe2f42d15f126787e5a9e7e00f0720190e26de))
* **deps:** update module github.com/prometheus/client_golang to v1.24.0 ([#217](https://github.com/rknightion/tailscale2otel/issues/217)) ([a22617a](https://github.com/rknightion/tailscale2otel/commit/a22617ab613de83060ce2beb0c65a0a5f3eb6400))
* **deps:** update module github.com/prometheus/client_golang to v1.24.1 ([#247](https://github.com/rknightion/tailscale2otel/issues/247)) ([10c5e01](https://github.com/rknightion/tailscale2otel/commit/10c5e01567798f67e17d5d264480539c4e760118))
* **deps:** update module github.com/rknightion/tailscale2otel/v2 to v2.0.2 ([#188](https://github.com/rknightion/tailscale2otel/issues/188)) ([9325849](https://github.com/rknightion/tailscale2otel/commit/9325849e8b1fceaadc2f07648b9aed878d3073eb))
* **deps:** update module google.golang.org/grpc to v1.82.1 ([#192](https://github.com/rknightion/tailscale2otel/issues/192)) ([6d9c74d](https://github.com/rknightion/tailscale2otel/commit/6d9c74d42ebfc8431269c904fe7b08bcc133829d))
* **flowlog:** omit absent flow endpoints instead of fabricating them ([b6629a2](https://github.com/rknightion/tailscale2otel/commit/b6629a28704d43711c906a038a575fd2ad9e12c3)), closes [#236](https://github.com/rknightion/tailscale2otel/issues/236)
* **flowlog:** read Tailscale's ":0" as the absent port it is ([7d36026](https://github.com/rknightion/tailscale2otel/commit/7d360263a6d161e2f0467693bb4eed8181d5d4a8)), closes [#248](https://github.com/rknightion/tailscale2otel/issues/248)
* **flowlog:** stop naming the DERP relay marker as a destination ([2004791](https://github.com/rknightion/tailscale2otel/commit/20047914c5fabcee0ed47ea43700c3e501d3e789)), closes [#245](https://github.com/rknightion/tailscale2otel/issues/245)
* **flowlog:** timestamp flow logs by window start, not capture time ([a0cce89](https://github.com/rknightion/tailscale2otel/commit/a0cce89d313f82ed43a1e7ba8233ff2c5e3609a7)), closes [#234](https://github.com/rknightion/tailscale2otel/issues/234)
* **flowstore:** keep underlay ports out of the destination-services table ([e8156bf](https://github.com/rknightion/tailscale2otel/commit/e8156bf0cf9638030b5e3f29732f405016c16857)), closes [#246](https://github.com/rknightion/tailscale2otel/issues/246)
* **helm:** re-sync the chart config block with the app's config surface ([c817573](https://github.com/rknightion/tailscale2otel/commit/c817573ab84a29f370972d6d151b10894b93e184)), closes [#242](https://github.com/rknightion/tailscale2otel/issues/242)
* **helm:** reject any replicaCount other than 1 ([93cb2d5](https://github.com/rknightion/tailscale2otel/commit/93cb2d58d01f3c1812eb7ef82aefe1ae86e6150d)), closes [#203](https://github.com/rknightion/tailscale2otel/issues/203)
* **nodemetrics:** key delta baselines by source series, not post-drop labels ([b3217f6](https://github.com/rknightion/tailscale2otel/commit/b3217f68c36169f69ac2180a46461d17d76807b5)), closes [#199](https://github.com/rknightion/tailscale2otel/issues/199)
* **pii:** classify host:port IP values before falling back to hostnames ([a206df4](https://github.com/rknightion/tailscale2otel/commit/a206df4ad9d6af4203011955170b92b35e7d1dee)), closes [#198](https://github.com/rknightion/tailscale2otel/issues/198)
* **stream:** bound record count, unwrap depth and concurrent bodies; fail closed without a token ([992d7f6](https://github.com/rknightion/tailscale2otel/commit/992d7f6a0573b87940d435fb1c5cfaf89887f626)), closes [#209](https://github.com/rknightion/tailscale2otel/issues/209) [#228](https://github.com/rknightion/tailscale2otel/issues/228) [#229](https://github.com/rknightion/tailscale2otel/issues/229)
* **stream:** derive the listener write window from the process deadline ([f8dab4b](https://github.com/rknightion/tailscale2otel/commit/f8dab4bda1ab6e6eea5082d2d6a3984cb0fda3ba)), closes [#232](https://github.com/rknightion/tailscale2otel/issues/232)
* **stream:** record fail-closed receiver auth as a breaking change ([cd90397](https://github.com/rknightion/tailscale2otel/commit/cd9039748f9a3c24719659346db9b097dc349172))
* **telemetry:** apply the PII filter to span attributes ([0550813](https://github.com/rknightion/tailscale2otel/commit/05508138a17d3b0fdd85395ba1e2bc75ecc19566)), closes [#212](https://github.com/rknightion/tailscale2otel/issues/212)
* **telemetry:** drop service.version from the metrics resource ([#187](https://github.com/rknightion/tailscale2otel/issues/187)) ([1658133](https://github.com/rknightion/tailscale2otel/commit/1658133eb90c4089920aec195f4c20e9dfc85309))
* **telemetry:** give telemetry pipelines independent shutdown budgets ([c7f4f1e](https://github.com/rknightion/tailscale2otel/commit/c7f4f1e6262e50ffc48efc661e54426dd755975e)), closes [#204](https://github.com/rknightion/tailscale2otel/issues/204)
* **telemetry:** redact PII from log bodies, not only attributes ([5441abf](https://github.com/rknightion/tailscale2otel/commit/5441abf0d349955e51627cffd51bb226772d7887)), closes [#197](https://github.com/rknightion/tailscale2otel/issues/197)
* **tsapi:** bound OAuth/workload-identity token fetches through the body read ([4d5afa1](https://github.com/rknightion/tailscale2otel/commit/4d5afa1a7ce3b6c0718829c4ce0cb62d87d363c3)), closes [#200](https://github.com/rknightion/tailscale2otel/issues/200)
* **tsapi:** cap Retry-After backoff at the configured max_delay ([d581993](https://github.com/rknightion/tailscale2otel/commit/d5819931c9ca9a352b0f3ad98038aed2ac3ae7d2)), closes [#206](https://github.com/rknightion/tailscale2otel/issues/206)
* **webhook:** reject signed timestamps too far in the future ([94774c7](https://github.com/rknightion/tailscale2otel/commit/94774c755abfac6d879d3c39cef1f6800407cf67)), closes [#205](https://github.com/rknightion/tailscale2otel/issues/205)


### Build System

* move the module path to /v3 and guard the next major ([1739277](https://github.com/rknightion/tailscale2otel/commit/17392772d79ead14e5b5906a6197aafb4668a071)), closes [#244](https://github.com/rknightion/tailscale2otel/issues/244)

## [2.0.2](https://github.com/rknightion/tailscale2otel/compare/v2.0.1...v2.0.2) (2026-07-14)


### Bug Fixes

* **deps:** update module github.com/rknightion/tailscale2otel/v2 to v2.0.1 ([#177](https://github.com/rknightion/tailscale2otel/issues/177)) ([ac0e152](https://github.com/rknightion/tailscale2otel/commit/ac0e152218c392e9a7316adaa2c793830219ff09))

## [2.0.1](https://github.com/rknightion/tailscale2otel/compare/v2.0.0...v2.0.1) (2026-07-13)


### Bug Fixes

* **build:** bump module path to /v2 so v2.x releases can build ([c252d50](https://github.com/rknightion/tailscale2otel/commit/c252d5031eb83429000a5f7fb314394b8a6afb7a)), closes [#174](https://github.com/rknightion/tailscale2otel/issues/174)

## [2.0.0](https://github.com/rknightion/tailscale2otel/compare/v1.0.0...v2.0.0) (2026-07-13)


### ⚠ BREAKING CHANGES

* the telemetry attributes above (and their Prometheus label normalizations) are renamed with no compatibility window; update external queries per the docs/upgrading.md v2.0.0 table.

### Features

* add -version and -validate flags to the release binary ([86c2e35](https://github.com/rknightion/tailscale2otel/commit/86c2e35ec1667ac2588ff0037bf7e8730744afce)), closes [#162](https://github.com/rknightion/tailscale2otel/issues/162)
* align telemetry attributes with OTel semantic conventions ([e59642d](https://github.com/rknightion/tailscale2otel/commit/e59642d49d278ae49e422218b04586f4124b9093)), closes [#161](https://github.com/rknightion/tailscale2otel/issues/161)
* **auth:** workload identity federation (auth.method: workload_identity) ([d55341a](https://github.com/rknightion/tailscale2otel/commit/d55341aaa482ec43d05bedeab39e98d49f74e451)), closes [#168](https://github.com/rknightion/tailscale2otel/issues/168)
* **ci:** report which live-contract ops actually ran ([550f84d](https://github.com/rknightion/tailscale2otel/commit/550f84d5023bf9d5f75235d9981c43ef82b7c0a8))
* **collector:** OAuth Apps collector + OpenAPI spec re-vendor ([8f2c3ca](https://github.com/rknightion/tailscale2otel/commit/8f2c3cacdd1c7b9da833c5d01ee9609363b740f1)), closes [#167](https://github.com/rknightion/tailscale2otel/issues/167)
* **config:** file-based secrets (*_file) for all credentials ([a474fb9](https://github.com/rknightion/tailscale2otel/commit/a474fb95dad7b884b878955ea84f5894a4f45c51)), closes [#169](https://github.com/rknightion/tailscale2otel/issues/169)
* **deploy:** alert + dashboard pack for the v2.0.0 program ([25aa62b](https://github.com/rknightion/tailscale2otel/commit/25aa62bb55dad0056e132f1222d56fe97fd9bb3e)), closes [#172](https://github.com/rknightion/tailscale2otel/issues/172)
* **devices:** emit multipleConnections, blocksIncomingConnections, postureIdentity.disabled ([e5551fc](https://github.com/rknightion/tailscale2otel/commit/e5551fce3112477ce0be5d5529f6f1fd53b1a45d)), closes [#163](https://github.com/rknightion/tailscale2otel/issues/163)
* **devices:** posture-attribute expiry telemetry ([de0c50c](https://github.com/rknightion/tailscale2otel/commit/de0c50c31458639053d1f5db280d139b40a96462)), closes [#164](https://github.com/rknightion/tailscale2otel/issues/164)
* **keys:** key owner (userId) and auto-applied device tags ([e548e5e](https://github.com/rknightion/tailscale2otel/commit/e548e5e54d7f66b74c30d7ab1226ecdd3baa1398)), closes [#165](https://github.com/rknightion/tailscale2otel/issues/165)
* **nodemetrics:** curate key tailscaled client metrics into the named catalog ([c362a92](https://github.com/rknightion/tailscale2otel/commit/c362a9258ae09bc14189a5824fea99ae97cf4bad)), closes [#171](https://github.com/rknightion/tailscale2otel/issues/171)
* **security:** fuzz the untrusted-input decoders, add a vuln-reporting policy ([ba82856](https://github.com/rknightion/tailscale2otel/commit/ba8285649315baae6979ae36ea40799b885bd167)), closes [#144](https://github.com/rknightion/tailscale2otel/issues/144)
* **services:** resolve service-VIP flow peers to service names ([f3669cd](https://github.com/rknightion/tailscale2otel/commit/f3669cd544024498a2a31f8d68884fb0dbd9a8ae)), closes [#166](https://github.com/rknightion/tailscale2otel/issues/166)
* TLS support for the admin and Prometheus listeners ([63c02a1](https://github.com/rknightion/tailscale2otel/commit/63c02a183ff623aa6b09b4dadba1b94e9a0359c5)), closes [#170](https://github.com/rknightion/tailscale2otel/issues/170)


### Bug Fixes

* **ci:** don't fail fuzz jobs on Go's benign -fuzztime shutdown race ([c42b28d](https://github.com/rknightion/tailscale2otel/commit/c42b28d0223fd6bdc56b6e06af346ee3dd7bcb7c))
* **ci:** run the live API contract lane on ubuntu-latest ([0acd799](https://github.com/rknightion/tailscale2otel/commit/0acd799a430d713b3583578657cbdfecd62d5498)), closes [#160](https://github.com/rknightion/tailscale2otel/issues/160)
* **deps:** update module github.com/grafana/pyroscope-go to v1.4.1 ([#142](https://github.com/rknightion/tailscale2otel/issues/142)) ([b7f83df](https://github.com/rknightion/tailscale2otel/commit/b7f83df71d69cae3b1b361538cc3527e007bbd58))
* **deps:** update module github.com/klauspost/compress to v1.19.0 ([#41](https://github.com/rknightion/tailscale2otel/issues/41)) ([15a754d](https://github.com/rknightion/tailscale2otel/commit/15a754d1f809bec9e5f01ad3e20df9289bb999ab))
* **deps:** update module google.golang.org/grpc to v1.82.0 ([#42](https://github.com/rknightion/tailscale2otel/issues/42)) ([8abf4ce](https://github.com/rknightion/tailscale2otel/commit/8abf4ce9c9fd1124a9ab7ef958c894365a85e7f1))
* **renovate:** stop raising invalid major bumps of tools/* indirect deps ([688a7a6](https://github.com/rknightion/tailscale2otel/commit/688a7a6271fe3afe146e6325c53ab395571b4a2f))

## [1.0.0](https://github.com/rknightion/tailscale2otel/compare/v0.6.0...v1.0.0) (2026-07-05)


### ⚠ BREAKING CHANGES

* v1.0.0 stabilises the public interface (config, metric names, HTTP endpoints, Helm values). There are no new breaking changes over 0.6.0; docs/upgrading.md carries the consolidated pre-1.0 to v1.0.0 migration notes for anyone upgrading from an earlier 0.x.

### Features

* declare stable v1.0.0 release ([eba5996](https://github.com/rknightion/tailscale2otel/commit/eba5996d51ff0013557ba450defa0257cea2f158))

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
