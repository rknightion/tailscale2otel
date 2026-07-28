#!/usr/bin/env bash
# Render-level regression tests for the tailscale2otel Helm chart.
#
# These are pure `helm template` assertions — no cluster, no `helm test` hooks —
# covering the two security contracts the chart owes operators:
#
#   SEC-07 (#470) a credential set inline under `config:` must never be rendered
#                 into a ConfigMap (or into any pod annotation, label or arg).
#                 The whole config.yaml moves to a Secret instead.
#   SEC-06 (#469) `rolloutTrigger` must reach the pod template so rotating an
#                 externally managed `existingSecret` has a documented way to
#                 force a rollout.
#   GHSA-825f-hph6-x65w  no pod-template annotation may ever carry a digest of
#                 secret material (an inline `secret:` value, a secret-backed
#                 config.yaml, or anything else credential-bearing) — that lets
#                 a workload-read-only principal verify offline guesses against
#                 it. `checksum/config` may hash the rendered config ONLY while
#                 it stays credential-free (ConfigMap-backed); rolloutTrigger /
#                 Reloader are the only supported rollout path once anything
#                 secret-bearing is in play.
#
# Requires: helm, yq (mikefarah). Run from anywhere:
#   deploy/helm/tests/render-tests.sh
set -uo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
CHART_DIR="${SCRIPT_DIR}/../tailscale2otel"
RELEASE=t
FULLNAME="t-tailscale2otel"
# A value that appears nowhere else in the chart, so a bare grep is conclusive.
SENTINEL="SENTINEL-c0ffee-do-not-leak"

pass=0
fail=0

ok()   { printf '  ok   %s\n' "$1"; pass=$((pass + 1)); }
bad()  { printf '  FAIL %s\n' "$1"; fail=$((fail + 1)); }
case_() { printf '\n== %s\n' "$1"; }

RENDER=""
RENDER_RC=0
render() { RENDER="$(helm template "$RELEASE" "$CHART_DIR" "$@" 2>&1)"; RENDER_RC=$?; }

# docs_of KIND -> the rendered documents of that kind
docs_of() { yq "select(.kind == \"$1\")" <<<"$RENDER"; }
# docs_not KIND -> every rendered document EXCEPT that kind
docs_not() { yq "select(.kind != \"$1\")" <<<"$RENDER"; }

assert_rc0() {
  if [[ $RENDER_RC -eq 0 ]]; then ok "$1"; else bad "$1 (helm exited $RENDER_RC)"; printf '%s\n' "$RENDER" | sed 's/^/       | /' | head -5; fi
}

# The core SEC-07 assertion: the sentinel may appear ONLY inside a Secret.
assert_secret_only() {
  local label="$1"
  if grep -q -- "$SENTINEL" <<<"$(docs_not Secret)"; then
    bad "$label: credential leaked outside a Secret"
    grep -n -- "$SENTINEL" <<<"$(docs_not Secret)" | sed 's/^/       | /' | head -3
  else
    ok "$label: no credential outside a Secret"
  fi
  if grep -q -- "$SENTINEL" <<<"$(docs_of Secret)"; then
    ok "$label: credential present in a Secret"
  else
    bad "$label: credential vanished (not in any Secret either)"
  fi
}

assert_no_configmap() {
  if [[ -z "$(docs_of ConfigMap | tr -d '[:space:]-')" ]]; then
    ok "$1: no ConfigMap rendered"
  else
    bad "$1: a ConfigMap was still rendered"
  fi
}

dep_field() { docs_of Deployment | yq "$1"; }

# --------------------------------------------------------------------------
case_ "A. credential-free config keeps the ConfigMap path"
render
assert_rc0 "A: renders"
[[ "$(docs_of ConfigMap | yq '.metadata.name')" == "$FULLNAME" ]] \
  && ok "A: ConfigMap $FULLNAME rendered" || bad "A: ConfigMap missing"
[[ -z "$(yq "select(.kind == \"Secret\" and .metadata.name == \"${FULLNAME}-config\")" <<<"$RENDER" | tr -d '[:space:]-')" ]] \
  && ok "A: no config Secret" || bad "A: unexpected config Secret"
[[ "$(dep_field '.spec.template.spec.volumes[] | select(.name == "config") | .configMap.name')" == "$FULLNAME" ]] \
  && ok "A: config volume is the ConfigMap" || bad "A: config volume is not the ConfigMap"

# --------------------------------------------------------------------------
case_ "B. every credential type set inline routes the config to a Secret"
creds=(
  "config.tailscale.auth.oauth.client_secret"
  "config.tailscale.auth.apikey"
  "config.headscale.api_key"
  "config.otlp.grafana_cloud.token"
  "config.otlp.headers.Authorization"
  "config.collectors.flowlogs.objectstore.access_key_id"
  "config.collectors.flowlogs.objectstore.secret_access_key"
  "config.collectors.flowlogs.objectstore.session_token"
  "config.collectors.auditlogs.objectstore.access_key_id"
  "config.collectors.auditlogs.objectstore.secret_access_key"
  "config.collectors.auditlogs.objectstore.session_token"
  "config.streaming.token"
  "config.webhook.secret"
  "config.prometheus.auth.token"
  "config.admin.auth.token"
  "config.profiling.pyroscope.basic_auth_password"
)
for key in "${creds[@]}"; do
  render --set "${key}=${SENTINEL}"
  assert_rc0 "B[$key]: renders"
  assert_secret_only "B[$key]"
  assert_no_configmap "B[$key]"
  [[ "$(dep_field '.spec.template.spec.volumes[] | select(.name == "config") | .secret.secretName')" == "${FULLNAME}-config" ]] \
    && ok "B[$key]: config volume is the Secret" || bad "B[$key]: config volume is not the Secret"
done

# Structural credential carriers (lists/maps, not scalar keys).
render --set-json "config.tailnets=[{\"name\":\"a.example.com\",\"auth\":{\"method\":\"apikey\",\"apikey\":\"${SENTINEL}\"}}]"
assert_rc0 "B[config.tailnets[]]: renders"
assert_secret_only "B[config.tailnets[]]"
assert_no_configmap "B[config.tailnets[]]"

render --set-json "config.collectors.node_metrics.targets=[{\"url\":\"http://10.0.0.1:5252/metrics\",\"instance\":\"n1\",\"bearer_token\":\"${SENTINEL}\"}]"
assert_rc0 "B[node_metrics.targets[].bearer_token]: renders"
assert_secret_only "B[node_metrics.targets[].bearer_token]"
assert_no_configmap "B[node_metrics.targets[].bearer_token]"

render --set-json "config.collectors.node_metrics.targets=[{\"url\":\"http://10.0.0.1:5252/metrics\",\"instance\":\"n1\",\"headers\":{\"Authorization\":\"${SENTINEL}\"}}]"
assert_rc0 "B[node_metrics.targets[].headers]: renders"
assert_secret_only "B[node_metrics.targets[].headers]"

# A credential supplied the supported way (env var from the chart Secret) must NOT
# drag the config off the ConfigMap path — only inline `config:` values do.
render --set "secret.TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET=${SENTINEL}"
assert_rc0 "B[secret.* env]: renders"
assert_secret_only "B[secret.* env]"
[[ "$(docs_of ConfigMap | yq '.metadata.name')" == "$FULLNAME" ]] \
  && ok "B[secret.* env]: ConfigMap path preserved" || bad "B[secret.* env]: ConfigMap path lost"

# --------------------------------------------------------------------------
case_ "C. rolloutTrigger reaches the pod template (existingSecret rotation)"
render --set existingSecret=ext-creds --set rolloutTrigger=rev-1
assert_rc0 "C: renders"
a1="$(dep_field '.spec.template.metadata.annotations')"
render --set existingSecret=ext-creds --set rolloutTrigger=rev-2
a2="$(dep_field '.spec.template.metadata.annotations')"
[[ "$a1" != "$a2" ]] && ok "C: changing rolloutTrigger changes the pod template" \
  || bad "C: pod template unchanged across rolloutTrigger values"
grep -q 'rollout-trigger' <<<"$a2" && ok "C: rollout-trigger annotation present" \
  || bad "C: rollout-trigger annotation missing"
grep -q 'rev-2' <<<"$a2" && ok "C: annotation carries the operator's opaque value" \
  || bad "C: annotation does not carry the value"

render --set existingSecret=ext-creds
grep -q 'rollout-trigger' <<<"$(dep_field '.spec.template.metadata.annotations')" \
  && bad "C: rollout-trigger annotation rendered when unset" \
  || ok "C: no rollout-trigger annotation when unset"

# --------------------------------------------------------------------------
case_ "D. GHSA-825f-hph6-x65w: no checksum/secret annotation ever, inline secret or not"
render --set secret.TS2OTEL_TAILSCALE__AUTH__APIKEY=v1
d1="$(dep_field '.spec.template.metadata.annotations."checksum/secret"')"
[[ -z "$d1" || "$d1" == "null" ]] && ok "D: no checksum/secret annotation with an inline secret set" \
  || bad "D: checksum/secret annotation present ($d1) — a digest of secret material must never be published"
render
d2="$(dep_field '.spec.template.metadata.annotations."checksum/secret"')"
[[ -z "$d2" || "$d2" == "null" ]] && ok "D: no checksum/secret annotation by default" \
  || bad "D: checksum/secret annotation present by default ($d2)"

# Two different offline-guessable secret values must never produce two different,
# individually derivable digests published anywhere in the pod template — that is
# the exact mechanism GHSA-825f-hph6-x65w closes. Assert the WHOLE annotations block
# is byte-identical across the two secret values (proves nothing secret-derived
# leaks into it at all, not just that one known key is absent).
render --set secret.TS2OTEL_TAILSCALE__AUTH__APIKEY=v1
d3="$(dep_field '.spec.template.metadata.annotations')"
render --set secret.TS2OTEL_TAILSCALE__AUTH__APIKEY=v2
d4="$(dep_field '.spec.template.metadata.annotations')"
[[ "$d3" == "$d4" ]] && ok "D: pod-template annotations are identical across two different secret values" \
  || bad "D: pod-template annotations changed with the secret value — something derived from it leaked"

case_ "D2. checksum/config only ever hashes a credential-free (ConfigMap-backed) config"
render --set config.log_level=debug
c3="$(dep_field '.spec.template.metadata.annotations."checksum/config"')"
render --set config.log_level=warn
c4="$(dep_field '.spec.template.metadata.annotations."checksum/config"')"
[[ -n "$c3" && "$c3" != "$c4" && "$c3" != "null" ]] \
  && ok "D2: checksum/config changes with the credential-free config" \
  || bad "D2: checksum/config did not change ($c3 vs $c4)"

# Once the config is secret-backed, checksum/config must be entirely absent — not
# hashed, not present, not derivable — same rationale as case D above.
render --set "config.tailscale.auth.apikey=${SENTINEL}"
d5="$(dep_field '.spec.template.metadata.annotations."checksum/config"')"
[[ -z "$d5" || "$d5" == "null" ]] && ok "D2: no checksum/config annotation once the config is secret-backed" \
  || bad "D2: checksum/config annotation present for a secret-backed config ($d5)"

# --------------------------------------------------------------------------
case_ "E. unsafe combination fails with an actionable message"
render --set configStorage.mode=configmap --set "config.admin.auth.token=${SENTINEL}"
[[ $RENDER_RC -ne 0 ]] && ok "E: helm template fails" || bad "E: helm template succeeded"
grep -q 'configStorage.mode' <<<"$RENDER" && ok "E: message names the offending value" \
  || bad "E: message does not name configStorage.mode"
grep -q 'config.admin.auth.token' <<<"$RENDER" && ok "E: message names the offending key" \
  || bad "E: message does not list the credential key"
grep -q -- "$SENTINEL" <<<"$RENDER" && bad "E: error message echoes the credential VALUE" \
  || ok "E: error message does not echo the credential value"

render --set configStorage.mode=nonsense
[[ $RENDER_RC -ne 0 ]] && ok "E2: unknown configStorage.mode rejected" || bad "E2: unknown mode accepted"

# --------------------------------------------------------------------------
case_ "F. configStorage.mode=secret forces the Secret path with no credentials"
render --set configStorage.mode=secret
assert_rc0 "F: renders"
assert_no_configmap "F"
[[ "$(dep_field '.spec.template.spec.volumes[] | select(.name == "config") | .secret.secretName')" == "${FULLNAME}-config" ]] \
  && ok "F: config volume is the Secret" || bad "F: config volume is not the Secret"

case_ "G. configStorage.mode=configmap is allowed when nothing is inline"
render --set configStorage.mode=configmap
assert_rc0 "G: renders"
[[ "$(docs_of ConfigMap | yq '.metadata.name')" == "$FULLNAME" ]] \
  && ok "G: ConfigMap rendered" || bad "G: ConfigMap missing"

# --------------------------------------------------------------------------
case_ "H. ingress WAL config renders with safe defaults and accepts overrides"
render
assert_rc0 "H: default renders"
default_config="$(docs_of ConfigMap | yq -r '.data."config.yaml"')"
[[ "$(yq '.ingress_wal.enabled' <<<"$default_config")" == "false" ]] \
  && ok "H: WAL disabled by default" || bad "H: WAL default enabled mismatch"
[[ "$(yq '.ingress_wal.directory' <<<"$default_config")" == "/var/lib/tailscale2otel/ingress-wal" ]] \
  && ok "H: WAL default directory rendered" || bad "H: WAL default directory mismatch"
[[ "$(yq '.ingress_wal.max_bytes' <<<"$default_config")" == "268435456" ]] \
  && ok "H: WAL default byte limit rendered" || bad "H: WAL default byte limit mismatch"
[[ "$(yq '.ingress_wal.max_entries' <<<"$default_config")" == "10000" ]] \
  && ok "H: WAL default entry limit rendered" || bad "H: WAL default entry limit mismatch"
[[ "$(yq '.ingress_wal.corruption' <<<"$default_config")" == "fail" ]] \
  && ok "H: WAL fail-closed corruption mode rendered" || bad "H: WAL corruption mode mismatch"

render \
  --set config.ingress_wal.enabled=true \
  --set config.ingress_wal.directory=/state/wal \
  --set config.ingress_wal.max_bytes=33554432 \
  --set config.ingress_wal.max_entries=321
assert_rc0 "H: override renders"
override_config="$(docs_of ConfigMap | yq -r '.data."config.yaml"')"
[[ "$(yq '.ingress_wal.enabled' <<<"$override_config")" == "true" ]] \
  && ok "H: WAL enabled override rendered" || bad "H: WAL enabled override missing"
[[ "$(yq '.ingress_wal.directory' <<<"$override_config")" == "/state/wal" ]] \
  && ok "H: WAL directory override rendered" || bad "H: WAL directory override missing"
[[ "$(yq '.ingress_wal.max_bytes' <<<"$override_config")" == "33554432" ]] \
  && ok "H: WAL byte override rendered" || bad "H: WAL byte override missing"
[[ "$(yq '.ingress_wal.max_entries' <<<"$override_config")" == "321" ]] \
  && ok "H: WAL entry override rendered" || bad "H: WAL entry override missing"

# --------------------------------------------------------------------------
case_ "I. state volume keeps the existing emptyDir/PVC and nonroot security contracts"
render
assert_rc0 "I: default renders"
[[ "$(dep_field '.spec.template.spec.containers[] | select(.name == "tailscale2otel") | .volumeMounts[] | select(.name == "checkpoints") | .mountPath')" == "/var/lib/tailscale2otel" ]] \
  && ok "I: state mount covers checkpoints and WAL" || bad "I: state mount path changed"
[[ "$(dep_field '.spec.template.spec.volumes[] | select(.name == "checkpoints") | has("emptyDir")')" == "true" ]] \
  && ok "I: default state volume remains emptyDir" || bad "I: default state volume is not emptyDir"
[[ "$(dep_field '.spec.template.spec.securityContext.fsGroup')" == "65532" ]] \
  && ok "I: pod fsGroup keeps state writable by uid 65532" || bad "I: pod fsGroup changed"
[[ "$(dep_field '.spec.template.spec.containers[] | select(.name == "tailscale2otel") | .securityContext.readOnlyRootFilesystem')" == "true" ]] \
  && ok "I: read-only root filesystem preserved" || bad "I: read-only root filesystem changed"

render --set persistence.enabled=true
assert_rc0 "I: managed PVC renders"
[[ "$(dep_field '.spec.template.spec.volumes[] | select(.name == "checkpoints") | .persistentVolumeClaim.claimName')" == "$FULLNAME" ]] \
  && ok "I: managed PVC backs the existing state volume" || bad "I: managed PVC claim mismatch"
[[ "$(docs_of PersistentVolumeClaim | yq '.spec.resources.requests.storage')" == "64Mi" ]] \
  && ok "I: existing 64Mi PVC default preserved" || bad "I: PVC default size changed"

render --set persistence.enabled=true --set persistence.existingClaim=durable-state
assert_rc0 "I: existing PVC renders"
[[ "$(dep_field '.spec.template.spec.volumes[] | select(.name == "checkpoints") | .persistentVolumeClaim.claimName')" == "durable-state" ]] \
  && ok "I: existingClaim backs the state volume" || bad "I: existingClaim not used"
[[ -z "$(docs_of PersistentVolumeClaim | tr -d '[:space:]-')" ]] \
  && ok "I: existingClaim does not create another PVC" || bad "I: unexpected managed PVC with existingClaim"

# --------------------------------------------------------------------------
case_ "J. probes follow admin TLS (#342)"
# The binary serves the admin server over HTTPS iff BOTH admin.tls files are set
# (internal/app/admin.go: ListenAndServeTLS when certFile != "" && keyFile != "").
# Probes with no httpGet.scheme default to HTTP, so enabling a supported chart
# setting made every probe fail the TLS handshake — the pod never becomes ready
# and the kubelet restarts it forever.
probe() { dep_field ".spec.template.spec.containers[] | select(.name == \"tailscale2otel\") | .${1}Probe.httpGet.scheme"; }

render
assert_rc0 "J: plain HTTP renders"
# Absent scheme means HTTP to Kubernetes; assert it is not HTTPS rather than
# demanding a literal, so either `null` or an explicit HTTP both pass.
[[ "$(probe liveness)" != "HTTPS" && "$(probe readiness)" != "HTTPS" ]] \
  && ok "J: no admin TLS -> probes are not HTTPS" \
  || bad "J: probes went HTTPS without admin TLS (liveness=$(probe liveness) readiness=$(probe readiness))"

render --set-string config.admin.tls.cert_file=/tls/tls.crt \
       --set-string config.admin.tls.key_file=/tls/tls.key
assert_rc0 "J: admin TLS renders"
[[ "$(probe liveness)" == "HTTPS" ]] \
  && ok "J: admin TLS -> liveness scheme HTTPS" || bad "J: liveness scheme is $(probe liveness), want HTTPS"
[[ "$(probe readiness)" == "HTTPS" ]] \
  && ok "J: admin TLS -> readiness scheme HTTPS" || bad "J: readiness scheme is $(probe readiness), want HTTPS"

# Only ONE of the pair set is not a TLS server — it is a config error the app
# rejects at startup (validateTLSFiles, #170). The chart must not pre-emptively
# flip the probes to HTTPS for a pod that will never serve TLS.
render --set-string config.admin.tls.cert_file=/tls/tls.crt
assert_rc0 "J: cert-only renders"
[[ "$(probe liveness)" != "HTTPS" ]] \
  && ok "J: cert_file alone does not flip the probe scheme" || bad "J: cert_file alone flipped the probe to HTTPS"
render --set-string config.admin.tls.key_file=/tls/tls.key
assert_rc0 "J: key-only renders"
[[ "$(probe liveness)" != "HTTPS" ]] \
  && ok "J: key_file alone does not flip the probe scheme" || bad "J: key_file alone flipped the probe to HTTPS"

# The workaround warning in values.yaml must not outlive the defect it warns
# about: it tells operators to patch the probes by hand, which is now wrong.
if grep -q 'patch the probes' "$CHART_DIR/values.yaml"; then
  bad "J: values.yaml still tells operators to patch the probes by hand"
else
  ok "J: stale probe workaround warning removed from values.yaml"
fi

# --------------------------------------------------------------------------
case_ "K. shutdown budget covers the staged drain (#332)"
render
assert_rc0 "K: default renders"
budget="$(dep_field '.spec.template.spec.terminationGracePeriodSeconds')"
[[ "$budget" == "45" ]] \
  && ok "K: default terminationGracePeriodSeconds is 45" \
  || bad "K: default terminationGracePeriodSeconds is $budget, want 45"

# An operator lowering this is making a data-durability decision. The render
# must fail and say so, rather than truncating a drain silently.
#
# TWO layers enforce it, on different inputs, and both are needed:
#   - values.schema.json `minimum: 45` catches any NUMBER below the floor, and
#     fires first, so the template guard never sees those.
#   - the template's `fail` catches null/absent, which JSON Schema `minimum`
#     does NOT reject and which would otherwise render a budget of ZERO —
#     an immediate SIGKILL, the worst possible outcome.
render --set terminationGracePeriodSeconds=20
if [[ $RENDER_RC -ne 0 ]] && grep -q 'want 45' <<<"$RENDER"; then
  ok "K: a numeric budget below the floor is rejected by the schema, naming 45"
else
  bad "K: terminationGracePeriodSeconds=20 rendered (rc=$RENDER_RC) instead of failing"
fi

# 30 is Kubernetes' own default and the exact drain — the specific value most
# likely to be chosen by someone "restoring the default", and the one with zero
# margin. It must fail too.
render --set terminationGracePeriodSeconds=30
[[ $RENDER_RC -ne 0 ]] \
  && ok "K: 30 (the k8s default, == the drain, no margin) also fails" \
  || bad "K: terminationGracePeriodSeconds=30 rendered; it leaves no headroom"

# null slips past the schema. Without the template guard this renders
# `terminationGracePeriodSeconds: 0` — a zero budget, killed instantly.
render --set terminationGracePeriodSeconds=null
if [[ $RENDER_RC -ne 0 ]] && grep -q 'terminationGracePeriodSeconds must be at least 45' <<<"$RENDER"; then
  ok "K: null is caught by the template guard, which explains the drain"
else
  bad "K: terminationGracePeriodSeconds=null rendered (rc=$RENDER_RC); a zero budget would ship"
fi
grep -q 'staged drain is 30s' <<<"$RENDER" \
  && ok "K: the failure explains WHY 45, not just that 45 is required" \
  || bad "K: failure message does not state the drain arithmetic"

render --set terminationGracePeriodSeconds=120
assert_rc0 "K: a larger budget renders"
[[ "$(dep_field '.spec.template.spec.terminationGracePeriodSeconds')" == "120" ]] \
  && ok "K: a larger budget is passed through" || bad "K: larger budget not honoured"

# --------------------------------------------------------------------------
case_ "L. the documented credential quick starts actually work (#341)"
# The README's preferred quick start, verbatim in shape: a pre-created Secret
# referenced by name. Assert it reaches the pod AND that the chart renders no
# Secret of its own — the whole point is that no credential passes through Helm.
render --set-string config.tailscale.tailnet=example.com \
       --set-string existingSecret=tailscale2otel-creds
assert_rc0 "L: existingSecret quick start renders"
[[ "$(dep_field '.spec.template.spec.containers[] | select(.name == "tailscale2otel") | [.envFrom[]?.secretRef.name] | join(",")')" == "tailscale2otel-creds" ]] \
  && ok "L: credentials injected via envFrom from the pre-created Secret" \
  || bad "L: envFrom does not reference tailscale2otel-creds"
[[ -z "$(yq "select(.kind == \"Secret\" and .metadata.name == \"${FULLNAME}\")" <<<"$RENDER" | tr -d '[:space:]-')" ]] \
  && ok "L: chart renders no Secret of its own (nothing passed through Helm)" \
  || bad "L: chart still rendered its own Secret alongside existingSecret"

# The -f secrets.yaml alternative: values under `secret:` must reach a Secret and
# nothing else. Reuses the SEC-07 assertion, so a leak fails the same way.
render --set-string "secret.TS2OTEL_OTLP__GRAFANA_CLOUD__TOKEN=${SENTINEL}"
assert_rc0 "L: chart-managed secret renders"
assert_secret_only "L[secret.* from a values file]"

# NOTES must be mode-aware: each mode names its own rotation path, and neither
# tells the operator to put a credential on the command line.
# `helm template` does NOT render NOTES.txt — it silently emits nothing, which
# made the two negative-shaped assertions below pass vacuously on first write.
# `helm install --dry-run=client` is what renders it, with no cluster contacted.
notes_of() {
  local out
  out="$(helm install "$RELEASE" "$CHART_DIR" --dry-run=client "$@" 2>&1 | sed -n '/^NOTES:/,$p')"
  if [[ -z "$out" ]]; then
    bad "L: NOTES rendered EMPTY — every assertion over it would pass vacuously"
    return 1
  fi
  printf '%s' "$out"
}
n_ext="$(notes_of --set-string existingSecret=tailscale2otel-creds)"
n_own="$(notes_of)"
grep -q 'tailscale2otel-creds' <<<"$n_ext" \
  && ok "L: NOTES names the existing Secret in existingSecret mode" \
  || bad "L: NOTES does not name the existing Secret"
grep -q 'Rendered into Secret' <<<"$n_own" \
  && ok "L: NOTES describes the chart-managed Secret in default mode" \
  || bad "L: NOTES does not describe the chart-managed Secret"
grep -q 'Rendered into Secret' <<<"$n_ext" \
  && bad "L: NOTES describes a chart-managed Secret while existingSecret is set" \
  || ok "L: NOTES is mode-aware (no chart-Secret text under existingSecret)"
# An inline `--set secret.KEY=` form in the printed output is the defect #341 is
# about — it is the single most-copied text the chart emits.
if grep -qE -- '--set(-string)? +"?secret\.[A-Za-z0-9_]+=' <<<"$n_own$n_ext"; then
  bad "L: NOTES still prints an inline --set secret.<KEY>=<value> command"
else
  ok "L: NOTES prints no inline credential command"
fi

# --------------------------------------------------------------------------
case_ "M. extraEnv / extraEnvFrom passthrough with a reserved-name policy (#348)"
cenv() { dep_field ".spec.template.spec.containers[] | select(.name == \"tailscale2otel\") | ${1}"; }

render
assert_rc0 "M: default renders"
[[ "$(cenv '[.env[]? | select(.name == "PROXY_TEST")] | length')" == "0" ]] \
  && ok "M: nothing extra by default" || bad "M: unexpected default extra env"
[[ "$(cenv '[.envFrom[]?] | length')" == "1" ]] \
  && ok "M: default envFrom is only the chart Secret" || bad "M: default envFrom changed"

render --set-string 'extraEnv[0].name=HTTPS_PROXY' --set-string 'extraEnv[0].value=http://proxy:3128'
assert_rc0 "M: plain value renders"
[[ "$(cenv '[.env[] | select(.name == "HTTPS_PROXY")][0].value')" == "http://proxy:3128" ]] \
  && ok "M: extraEnv value passthrough" || bad "M: extraEnv value not rendered"
# The chart's own GOGC must survive alongside it, not be replaced by the list.
[[ "$(cenv '[.env[] | select(.name == "GOGC")] | length')" == "1" ]] \
  && ok "M: chart-managed GOGC preserved next to extraEnv" || bad "M: chart-managed GOGC lost"

render --set-string 'extraEnv[0].name=POD_NAME' \
       --set-string 'extraEnv[0].valueFrom.fieldRef.fieldPath=metadata.name'
assert_rc0 "M: downward API renders"
[[ "$(cenv '[.env[] | select(.name == "POD_NAME")][0].valueFrom.fieldRef.fieldPath')" == "metadata.name" ]] \
  && ok "M: valueFrom fieldRef passthrough" || bad "M: valueFrom fieldRef not rendered"

render --set-string 'extraEnv[0].name=EXT_TOKEN' \
       --set-string 'extraEnv[0].valueFrom.secretKeyRef.name=ext' \
       --set-string 'extraEnv[0].valueFrom.secretKeyRef.key=token'
assert_rc0 "M: secretKeyRef renders"
[[ "$(cenv '[.env[] | select(.name == "EXT_TOKEN")][0].valueFrom.secretKeyRef.name')" == "ext" ]] \
  && ok "M: valueFrom secretKeyRef passthrough" || bad "M: valueFrom secretKeyRef not rendered"

render --set-string 'extraEnvFrom[0].configMapRef.name=proxy-cm' \
       --set-string 'extraEnvFrom[1].secretRef.name=ext-creds'
assert_rc0 "M: extraEnvFrom renders"
[[ "$(cenv '[.envFrom[]] | length')" == "3" ]] \
  && ok "M: extraEnvFrom appended to the chart Secret" || bad "M: envFrom count is $(cenv '[.envFrom[]] | length'), want 3"
# Order matters: later envFrom entries win in Kubernetes, so the chart's Secret
# must come FIRST for an operator's external source to be able to override it
# deliberately — and so the chart never silently overrides theirs.
[[ "$(cenv '.envFrom[0].secretRef.name')" == "$FULLNAME" ]] \
  && ok "M: chart Secret stays first in envFrom" || bad "M: chart Secret is no longer first in envFrom"

# --- the reserved-name policy -------------------------------------------------
# `env` beats `envFrom` in Kubernetes, so an extraEnv entry naming a key the
# chart injects from its Secret would SILENTLY replace a credential.
render --set-string "secret.TS2OTEL_OTLP__GRAFANA_CLOUD__TOKEN=${SENTINEL}" \
       --set-string 'extraEnv[0].name=TS2OTEL_OTLP__GRAFANA_CLOUD__TOKEN' \
       --set-string 'extraEnv[0].value=override'
if [[ $RENDER_RC -ne 0 ]] && grep -q 'TS2OTEL_OTLP__GRAFANA_CLOUD__TOKEN' <<<"$RENDER"; then
  ok "M: extraEnv shadowing a chart Secret key fails, naming the key"
else
  bad "M: extraEnv silently overrode a chart-managed credential (rc=$RENDER_RC)"
fi
grep -q -- "$SENTINEL" <<<"$RENDER" && bad "M: the failure echoed the credential VALUE" \
  || ok "M: failure names the key without echoing its value"

render --set-string 'extraEnv[0].name=GOGC' --set-string 'extraEnv[0].value=50'
[[ $RENDER_RC -ne 0 ]] \
  && ok "M: extraEnv shadowing chart-managed GOGC fails" \
  || bad "M: extraEnv silently overrode chart-managed GOGC"

# ...but only while the chart is actually setting it. Clearing goRuntime.gogc
# hands the name back to the operator.
render --set-string goRuntime.gogc= --set-string 'extraEnv[0].name=GOGC' --set-string 'extraEnv[0].value=50'
assert_rc0 "M: GOGC is settable via extraEnv once the chart stops setting it"
[[ "$(cenv '[.env[] | select(.name == "GOGC")][0].value')" == "50" ]] \
  && ok "M: operator GOGC applied when the chart yields the name" || bad "M: operator GOGC not applied"

render --set-string 'extraEnv[0].name=DUP' --set-string 'extraEnv[0].value=a' \
       --set-string 'extraEnv[1].name=DUP' --set-string 'extraEnv[1].value=b'
[[ $RENDER_RC -ne 0 ]] \
  && ok "M: duplicate extraEnv names fail" || bad "M: duplicate extraEnv names accepted (last silently wins)"

render --set-string 'extraEnv[0].value=orphan'
[[ $RENDER_RC -ne 0 ]] \
  && ok "M: an extraEnv entry with no name fails" || bad "M: nameless extraEnv entry accepted"

# Schema-level type checking, which the template `fail` cannot do: it runs after
# values validation, so a structurally wrong value never reaches it.
for bad_val in 'extraEnv=notalist' 'extraEnv[0].name.nested=x' 'extraEnv[0].value.nested=y' 'extraEnvFrom=notalist'; do
  render --set "$bad_val"
  [[ $RENDER_RC -ne 0 ]] \
    && ok "M: schema rejects malformed --set $bad_val" \
    || bad "M: schema accepted malformed --set $bad_val"
done

# --------------------------------------------------------------------------
case_ "N. default-off, per-listener Services (#344)"
svc_of() { yq "select(.kind == \"Service\" and .metadata.name == \"$1\")" <<<"$RENDER"; }
any_service() { docs_of Service | tr -d '[:space:]-'; }
# NOT `yq '[.] | length'`: that wraps EMPTY input to 1 and cheerfully reports
# one Service when none rendered, which passed vacuously on first write.
n_services() { grep -c '^kind: Service$' <<<"$RENDER" || true; }

render
assert_rc0 "N: default renders"
[[ -z "$(any_service)" ]] \
  && ok "N: default chart still renders NO Service" \
  || bad "N: a Service rendered by default"

# Each listener, enabled with its credential, gets exactly one Service mapping
# exactly its own port.
render --set config.prometheus.enabled=true \
       --set-string "config.prometheus.auth.token=${SENTINEL}" \
       --set service.prometheus.enabled=true
assert_rc0 "N: prometheus Service renders"
[[ "$(n_services)" == "1" ]] \
  && ok "N: exactly one Service" || bad "N: expected 1 Service, got $(n_services)"
[[ "$(svc_of "${FULLNAME}-prometheus" | yq '.spec.ports[0].port')" == "2112" ]] \
  && ok "N: prometheus Service maps 2112" || bad "N: wrong prometheus port"
[[ "$(svc_of "${FULLNAME}-prometheus" | yq '.spec.ports | length')" == "1" ]] \
  && ok "N: prometheus Service maps ONLY its own port" || bad "N: prometheus Service maps extra ports"
[[ "$(svc_of "${FULLNAME}-prometheus" | yq '.spec.type')" == "ClusterIP" ]] \
  && ok "N: default type is ClusterIP" || bad "N: default type is not ClusterIP"
# The credential must never be echoed into the Service.
grep -q -- "$SENTINEL" <<<"$(docs_of Service)" \
  && bad "N: a credential leaked into the Service" || ok "N: no credential in the Service"

render --set config.webhook.enabled=true \
       --set-string "config.webhook.secret=${SENTINEL}" \
       --set service.webhook.enabled=true
assert_rc0 "N: webhook Service renders"
[[ "$(svc_of "${FULLNAME}-webhook" | yq '.spec.ports[0].port')" == "8089" ]] \
  && ok "N: webhook Service maps 8089" || bad "N: wrong webhook port"

render --set config.streaming.enabled=true \
       --set-string "config.streaming.token=${SENTINEL}" \
       --set service.streaming.enabled=true
assert_rc0 "N: streaming Service renders"
[[ "$(svc_of "${FULLNAME}-streaming" | yq '.spec.ports[0].port')" == "8088" ]] \
  && ok "N: streaming Service maps 8088" || bad "N: wrong streaming port"

render --set-string "config.admin.auth.token=${SENTINEL}" --set service.admin.enabled=true
assert_rc0 "N: admin Service renders"
[[ "$(svc_of "${FULLNAME}-admin" | yq '.spec.ports[0].port')" == "9091" ]] \
  && ok "N: admin Service maps 9091" || bad "N: wrong admin port"

# --- the safety guards ------------------------------------------------------
# A Service for a listener that is switched OFF would publish a port nothing
# serves; worse, it hides the real mistake.
# The credential IS set here on purpose: otherwise the credential guard fires
# and this passes without the listener-enabled guard existing at all. It did
# exactly that on first write — removing the enabled check changed nothing.
render --set-string "config.prometheus.auth.token=${SENTINEL}" --set service.prometheus.enabled=true
if [[ $RENDER_RC -ne 0 ]] && grep -q 'requires config.prometheus.enabled' <<<"$RENDER"; then
  ok "N: Service for a disabled listener fails, naming the listener toggle"
else
  bad "N: rendered a Service for a disabled listener (rc=$RENDER_RC)"
fi

# The core contract from deploy/CLAUDE.md: never map a receiver port without its
# credential. Each of the four, individually.
render --set config.prometheus.enabled=true --set service.prometheus.enabled=true
[[ $RENDER_RC -ne 0 ]] && ok "N: prometheus Service without auth.token fails" \
  || bad "N: exposed prometheus with no token — it serves every series unauthenticated"
render --set config.webhook.enabled=true --set service.webhook.enabled=true
[[ $RENDER_RC -ne 0 ]] && ok "N: webhook Service without secret fails" \
  || bad "N: exposed webhook with no secret — HMAC verification is skipped"
render --set config.streaming.enabled=true --set service.streaming.enabled=true
[[ $RENDER_RC -ne 0 ]] && ok "N: streaming Service without token fails" \
  || bad "N: exposed streaming with no token"
render --set service.admin.enabled=true
[[ $RENDER_RC -ne 0 ]] && ok "N: admin Service without auth.token fails" \
  || bad "N: exposed admin with no token"

# A *_file credential is just as valid as an inline one; requiring the inline
# form would push operators back to the thing #341 just removed.
render --set config.prometheus.enabled=true \
       --set-string config.prometheus.auth.token_file=/run/secrets/promtok \
       --set service.prometheus.enabled=true
assert_rc0 "N: token_file satisfies the credential requirement"

# No aggregate Service, and LoadBalancer must never be a default.
render --set config.prometheus.enabled=true --set-string "config.prometheus.auth.token=${SENTINEL}" \
       --set service.prometheus.enabled=true \
       --set config.webhook.enabled=true --set-string "config.webhook.secret=${SENTINEL}" \
       --set service.webhook.enabled=true
assert_rc0 "N: two listeners render"
[[ "$(n_services)" == "2" ]] \
  && ok "N: one Service per listener, no aggregate" || bad "N: expected 2 discrete Services, got $(n_services)"
grep -q 'LoadBalancer' <<<"$RENDER" \
  && bad "N: a LoadBalancer Service rendered without being asked for" \
  || ok "N: no LoadBalancer anywhere by default"

# The pod must actually expose the ports the Services target.
[[ "$(dep_field '[.spec.template.spec.containers[] | select(.name == "tailscale2otel") | .ports[] | select(.name == "prometheus")] | length')" == "1" ]] \
  && ok "N: container declares a named prometheus port" || bad "N: no named prometheus containerPort"

# --------------------------------------------------------------------------
case_ "O. Prometheus Operator PodMonitor (#345)"
n_kind() { grep -c "^kind: $1\$" <<<"$RENDER" || true; }

render
assert_rc0 "O: default renders"
[[ "$(n_kind PodMonitor)" == "0" ]] \
  && ok "O: no PodMonitor by default (a cluster without the CRDs is unaffected)" \
  || bad "O: a PodMonitor rendered by default"

render --set config.prometheus.enabled=true --set metrics.podMonitor.enabled=true --set config.prometheus.auth.allow_unauthenticated=true
assert_rc0 "O: PodMonitor renders"
[[ "$(n_kind PodMonitor)" == "1" ]] && ok "O: exactly one PodMonitor" || bad "O: PodMonitor count is $(n_kind PodMonitor)"
pm() { yq 'select(.kind == "PodMonitor") | '"$1" <<<"$RENDER"; }
[[ "$(pm '.spec.podMetricsEndpoints[0].port')" == "prometheus" ]] \
  && ok "O: targets the named prometheus container port" || bad "O: wrong port name"
[[ "$(pm '.spec.podMetricsEndpoints[0].scheme')" == "http" ]] \
  && ok "O: scheme http without listener TLS" || bad "O: wrong default scheme"
# It must work with NO Service — that is the reason to prefer a PodMonitor.
[[ "$(n_kind Service)" == "0" ]] \
  && ok "O: PodMonitor works with no Service rendered" || bad "O: a Service was required"

# Follows the listener's TLS, same rule as the probes (#342).
render --set config.prometheus.enabled=true --set metrics.podMonitor.enabled=true --set config.prometheus.auth.allow_unauthenticated=true \
       --set-string config.prometheus.tls.cert_file=/tls/tls.crt \
       --set-string config.prometheus.tls.key_file=/tls/tls.key
assert_rc0 "O: TLS listener renders"
[[ "$(pm '.spec.podMetricsEndpoints[0].scheme')" == "https" ]] \
  && ok "O: scheme follows listener TLS" || bad "O: scheme did not follow listener TLS"

# Auth is REFERENCED, never embedded.
render --set config.prometheus.enabled=true --set metrics.podMonitor.enabled=true \
       --set-string "config.prometheus.auth.token=${SENTINEL}" \
       --set-string metrics.podMonitor.bearerTokenSecret.name=promtok \
       --set-string metrics.podMonitor.bearerTokenSecret.key=token
assert_rc0 "O: authenticated PodMonitor renders"
[[ "$(pm '.spec.podMetricsEndpoints[0].bearerTokenSecret.name')" == "promtok" ]] \
  && ok "O: bearerTokenSecret reference rendered" || bad "O: no bearerTokenSecret reference"
grep -q -- "$SENTINEL" <<<"$(yq 'select(.kind == "PodMonitor")' <<<"$RENDER")" \
  && bad "O: the token VALUE was embedded in the PodMonitor" \
  || ok "O: no token value in the PodMonitor (reference only)"

# A scrape that cannot authenticate does not fail loudly — it silently reports
# no data. That must be a render error.
render --set config.prometheus.enabled=true --set metrics.podMonitor.enabled=true \
       --set-string "config.prometheus.auth.token=${SENTINEL}"
[[ $RENDER_RC -ne 0 ]] \
  && ok "O: token set without bearerTokenSecret fails" \
  || bad "O: rendered a PodMonitor that would 401 and silently report no data"
grep -q -- "$SENTINEL" <<<"$RENDER" && bad "O: the failure echoed the token value" \
  || ok "O: failure names the setting without echoing the token"

render --set metrics.podMonitor.enabled=true --set config.prometheus.auth.allow_unauthenticated=true
[[ $RENDER_RC -ne 0 ]] \
  && ok "O: PodMonitor without the prometheus listener fails" || bad "O: PodMonitor rendered with no listener to scrape"

# --------------------------------------------------------------------------
case_ "P. Prometheus Operator ServiceMonitor (#458)"
sm() { yq 'select(.kind == "ServiceMonitor") | '"$1" <<<"$RENDER"; }

render
assert_rc0 "P: default renders"
[[ "$(n_kind ServiceMonitor)" == "0" ]] \
  && ok "P: no ServiceMonitor by default" || bad "P: a ServiceMonitor rendered by default"

render --set config.prometheus.enabled=true --set-string "config.prometheus.auth.token=${SENTINEL}" \
       --set service.prometheus.enabled=true --set metrics.serviceMonitor.enabled=true --set config.prometheus.auth.allow_unauthenticated=true \
       --set-string metrics.serviceMonitor.bearerTokenSecret.name=promtok \
       --set-string metrics.serviceMonitor.bearerTokenSecret.key=token
assert_rc0 "P: ServiceMonitor renders"
[[ "$(n_kind ServiceMonitor)" == "1" ]] && ok "P: exactly one ServiceMonitor" || bad "P: count is $(n_kind ServiceMonitor)"
[[ "$(sm '.spec.endpoints[0].port')" == "prometheus" ]] \
  && ok "P: targets the prometheus Service port by name" || bad "P: wrong port name"
# The selector must actually match the Service #344 renders, or it silently
# scrapes nothing. Compare against the real Service's labels.
[[ "$(sm '.spec.selector.matchLabels."app.kubernetes.io/component"')" == "prometheus" ]] \
  && ok "P: selector scopes to the prometheus Service component" || bad "P: selector does not scope by component"
[[ "$(yq "select(.kind == \"Service\") | .metadata.labels.\"app.kubernetes.io/component\"" <<<"$RENDER")" == "prometheus" ]] \
  && ok "P: the rendered Service carries the label the selector matches" \
  || bad "P: selector/Service label mismatch — the ServiceMonitor would match nothing"
grep -q -- "$SENTINEL" <<<"$(yq 'select(.kind == "ServiceMonitor")' <<<"$RENDER")" \
  && bad "P: the token VALUE was embedded in the ServiceMonitor" \
  || ok "P: no token value in the ServiceMonitor (reference only)"

# A ServiceMonitor with no Service matches nothing and reports no data.
render --set config.prometheus.enabled=true --set metrics.serviceMonitor.enabled=true --set config.prometheus.auth.allow_unauthenticated=true
[[ $RENDER_RC -ne 0 ]] && ok "P: ServiceMonitor without its Service fails" \
  || bad "P: rendered a ServiceMonitor that would match no Service"
grep -q 'podMonitor' <<<"$RENDER" \
  && ok "P: the failure points at the Service-free alternative" || bad "P: failure does not mention podMonitor"

render --set metrics.serviceMonitor.enabled=true --set service.prometheus.enabled=true --set config.prometheus.auth.allow_unauthenticated=true
[[ $RENDER_RC -ne 0 ]] && ok "P: ServiceMonitor without the listener fails" || bad "P: rendered with no listener"

# --------------------------------------------------------------------------
case_ "Q. externally managed config resources (#347)"
cfgvol() { dep_field '.spec.template.spec.volumes[] | select(.name == "config")'; }

render --set-string existingConfigMap=my-cfg
assert_rc0 "Q: existingConfigMap renders"
[[ "$(cfgvol | yq '.configMap.name')" == "my-cfg" ]] \
  && ok "Q: config volume is the operator's ConfigMap" || bad "Q: config volume is not my-cfg"
[[ "$(cfgvol | yq '.configMap.items[0].path')" == "config.yaml" ]] \
  && ok "Q: key projected to config.yaml (the -config path is unchanged)" || bad "Q: key not projected"
[[ "$(docs_of ConfigMap | tr -d '[:space:]-')" == "" ]] \
  && ok "Q: chart renders no ConfigMap of its own" || bad "Q: chart still rendered its own ConfigMap"
grep -q 'checksum/config' <<<"$RENDER" \
  && bad "Q: a checksum was emitted for a config the chart cannot read" \
  || ok "Q: no checksum/config for an operator-managed config"

render --set-string existingConfigSecret=my-cfg-secret
assert_rc0 "Q: existingConfigSecret renders"
[[ "$(cfgvol | yq '.secret.secretName')" == "my-cfg-secret" ]] \
  && ok "Q: config volume is the operator's Secret" || bad "Q: config volume is not my-cfg-secret"
# This is the multi-tailnet case the issue names: credentials reach the pod
# without ever entering Helm values.
[[ -z "$(yq "select(.kind == \"Secret\" and .metadata.name == \"${FULLNAME}-config\")" <<<"$RENDER" | tr -d '[:space:]-')" ]] \
  && ok "Q: chart renders no config Secret of its own" || bad "Q: chart still rendered its own config Secret"

render --set-string existingConfigKey=tailscale2otel.yml --set-string existingConfigMap=my-cfg
assert_rc0 "Q: custom key renders"
[[ "$(cfgvol | yq '.configMap.items[0].key')" == "tailscale2otel.yml" ]] \
  && ok "Q: existingConfigKey selects the source key" || bad "Q: custom key ignored"
[[ "$(cfgvol | yq '.configMap.items[0].path')" == "config.yaml" ]] \
  && ok "Q: a custom key is still projected to config.yaml" || bad "Q: custom key not projected to config.yaml"

render --set-string existingConfigMap=a --set-string existingConfigSecret=b
[[ $RENDER_RC -ne 0 ]] \
  && ok "Q: both external sources at once fails" || bad "Q: accepted two config sources"

render --set-string existingConfigMap=my-cfg --set-string existingConfigKey=
[[ $RENDER_RC -ne 0 ]] \
  && ok "Q: an empty existingConfigKey fails" || bad "Q: accepted an empty config key"

# Unchanged default: no external config means the chart's own objects.
render
[[ "$(cfgvol | yq '.configMap.name')" == "$FULLNAME" ]] \
  && ok "Q: default still uses the chart's own ConfigMap" || bad "Q: default config volume changed"
grep -q 'checksum/config' <<<"$RENDER" \
  && ok "Q: default still carries checksum/config" || bad "Q: default lost its checksum/config"

# --------------------------------------------------------------------------
case_ "R. projected Workload Identity Federation token (#343)"
WIF="--set workloadIdentity.enabled=true --set-string config.tailscale.auth.method=workload_identity --set-string config.tailscale.auth.workload_identity.client_id=abc123"
wifvol() { dep_field '.spec.template.spec.volumes[] | select(.name == "tailscale-wif-token")'; }

render
assert_rc0 "R: default renders"
[[ -z "$(wifvol | tr -d '[:space:]-')" ]] \
  && ok "R: no token volume by default" || bad "R: a WIF volume rendered by default"

# shellcheck disable=SC2086
render $WIF
assert_rc0 "R: WIF renders"
[[ "$(wifvol | yq '.projected.sources[0].serviceAccountToken.audience')" == "api.tailscale.com/abc123" ]] \
  && ok "R: audience derived from the client id" || bad "R: wrong audience"
[[ "$(wifvol | yq '[.projected.sources[]] | length')" == "1" ]] \
  && ok "R: exactly one projected source (nothing else smuggled in)" || bad "R: extra projected sources"
[[ "$(wifvol | yq '.projected.sources[0].serviceAccountToken.expirationSeconds')" == "3600" ]] \
  && ok "R: expirationSeconds projected" || bad "R: expirationSeconds missing"
# The critical negative: a projected AUDIENCE-SCOPED token, never the broad
# Kubernetes API token that automountServiceAccountToken would mount.
[[ "$(dep_field '.spec.template.spec.automountServiceAccountToken')" != "true" ]] \
  && ok "R: automountServiceAccountToken still not enabled (no broad k8s API token)" \
  || bad "R: a general-purpose Kubernetes API token is being mounted"
[[ "$(dep_field '.spec.template.spec.containers[] | select(.name == "tailscale2otel") | [.volumeMounts[] | select(.name == "tailscale-wif-token")][0].readOnly')" == "true" ]] \
  && ok "R: token mounted read-only" || bad "R: token mount is not read-only"
# The env var and the volume must agree, or the exporter reads a path that does
# not exist and reports an auth failure that looks like a bad client.
tokpath="$(dep_field '.spec.template.spec.containers[] | select(.name == "tailscale2otel") | [.env[] | select(.name == "TS2OTEL_TAILSCALE__AUTH__WORKLOAD_IDENTITY__ID_TOKEN_FILE")][0].value')"
mnt="$(dep_field '.spec.template.spec.containers[] | select(.name == "tailscale2otel") | [.volumeMounts[] | select(.name == "tailscale-wif-token")][0].mountPath')"
fn="$(wifvol | yq '.projected.sources[0].serviceAccountToken.path')"
[[ "$tokpath" == "${mnt}/${fn}" ]] \
  && ok "R: id_token_file path agrees with mountPath/fileName ($tokpath)" \
  || bad "R: token path $tokpath does not match ${mnt}/${fn}"

# A custom mount path must keep those three in agreement.
# shellcheck disable=SC2086
render $WIF --set-string workloadIdentity.mountPath=/etc/wif --set-string workloadIdentity.fileName=tok
assert_rc0 "R: custom path renders"
[[ "$(dep_field '.spec.template.spec.containers[] | select(.name == "tailscale2otel") | [.env[] | select(.name == "TS2OTEL_TAILSCALE__AUTH__WORKLOAD_IDENTITY__ID_TOKEN_FILE")][0].value')" == "/etc/wif/tok" ]] \
  && ok "R: custom mountPath/fileName flow through to id_token_file" || bad "R: custom path did not flow through"

# An explicit audience wins over the derived one.
# shellcheck disable=SC2086
render $WIF --set-string workloadIdentity.audience=custom.example/aud
[[ "$(wifvol | yq '.projected.sources[0].serviceAccountToken.audience')" == "custom.example/aud" ]] \
  && ok "R: explicit audience overrides the derived one" || bad "R: explicit audience ignored"

# --- guards ---
render --set workloadIdentity.enabled=true --set-string config.tailscale.auth.workload_identity.client_id=abc123
[[ $RENDER_RC -ne 0 ]] \
  && ok "R: WIF without auth.method=workload_identity fails" || bad "R: mounted a token the exporter would never use"
render --set workloadIdentity.enabled=true --set-string config.tailscale.auth.method=workload_identity
[[ $RENDER_RC -ne 0 ]] \
  && ok "R: WIF without a client_id fails (the audience derives from it)" || bad "R: rendered an unscoped audience"
# shellcheck disable=SC2086
render $WIF --set workloadIdentity.expirationSeconds=60
[[ $RENDER_RC -ne 0 ]] \
  && ok "R: expirationSeconds below the k8s floor fails" || bad "R: accepted a value Kubernetes silently clamps"
# extraEnv must not be able to shadow the chart-set token path.
# shellcheck disable=SC2086
render $WIF --set-string 'extraEnv[0].name=TS2OTEL_TAILSCALE__AUTH__WORKLOAD_IDENTITY__ID_TOKEN_FILE' \
       --set-string 'extraEnv[0].value=/tmp/elsewhere'
[[ $RENDER_RC -ne 0 ]] \
  && ok "R: extraEnv cannot silently repoint the token path" || bad "R: extraEnv silently repointed the token path"

# --------------------------------------------------------------------------
case_ "S. opt-in Ingress for the streaming/webhook receivers ONLY (#346)"
ing_of() { yq "select(.kind == \"Ingress\" and .metadata.name == \"$1\")" <<<"$RENDER"; }
n_ingress() { grep -c '^kind: Ingress$' <<<"$RENDER" || true; }

render
assert_rc0 "S: default renders"
[[ "$(n_ingress)" == "0" ]] \
  && ok "S: no Ingress by default" || bad "S: an Ingress rendered by default"

render --set config.streaming.enabled=true --set-string "config.streaming.token=${SENTINEL}" \
       --set service.streaming.enabled=true \
       --set ingress.streaming.enabled=true --set-string ingress.streaming.host=hec.example.com \
       --set-string ingress.streaming.tls.secretName=hec-tls
assert_rc0 "S: streaming Ingress renders"
[[ "$(n_ingress)" == "1" ]] && ok "S: exactly one Ingress" || bad "S: expected 1 Ingress, got $(n_ingress)"
[[ "$(ing_of "${FULLNAME}-streaming" | yq '.spec.rules[0].host')" == "hec.example.com" ]] \
  && ok "S: streaming Ingress host rendered" || bad "S: streaming Ingress host wrong"
[[ "$(ing_of "${FULLNAME}-streaming" | yq '.spec.rules[0].http.paths[0].path')" == "/" ]] \
  && ok "S: streaming Ingress path rendered" || bad "S: streaming Ingress path wrong"
[[ "$(ing_of "${FULLNAME}-streaming" | yq '.spec.rules[0].http.paths[0].pathType')" == "Prefix" ]] \
  && ok "S: streaming Ingress pathType rendered" || bad "S: streaming Ingress pathType wrong"
[[ "$(ing_of "${FULLNAME}-streaming" | yq '.spec.tls[0].secretName')" == "hec-tls" ]] \
  && ok "S: streaming Ingress tls.secretName rendered" || bad "S: streaming Ingress tls.secretName wrong"
[[ "$(ing_of "${FULLNAME}-streaming" | yq '.spec.rules[0].http.paths[0].backend.service.name')" == "${FULLNAME}-streaming" ]] \
  && ok "S: streaming Ingress backend points at the streaming Service" || bad "S: streaming Ingress backend wrong"
[[ "$(ing_of "${FULLNAME}-streaming" | yq '.spec.rules[0].http.paths[0].backend.service.port.number')" == "8088" ]] \
  && ok "S: streaming Ingress backend port matches config.streaming.listen" || bad "S: streaming Ingress backend port wrong"
grep -q -- "$SENTINEL" <<<"$(docs_of Ingress)" \
  && bad "S: a credential leaked into the streaming Ingress" || ok "S: no credential in the streaming Ingress"

render --set config.webhook.enabled=true --set-string "config.webhook.secret=${SENTINEL}" \
       --set service.webhook.enabled=true \
       --set ingress.webhook.enabled=true --set-string ingress.webhook.host=wh.example.com \
       --set-string 'ingress.webhook.annotations.cert-manager\.io/cluster-issuer'=letsencrypt
assert_rc0 "S: webhook Ingress renders"
[[ "$(n_ingress)" == "1" ]] && ok "S: exactly one Ingress (webhook)" || bad "S: expected 1 Ingress, got $(n_ingress)"
[[ "$(ing_of "${FULLNAME}-webhook" | yq '.spec.rules[0].host')" == "wh.example.com" ]] \
  && ok "S: webhook Ingress host rendered" || bad "S: webhook Ingress host wrong"
[[ "$(ing_of "${FULLNAME}-webhook" | yq '.spec.rules[0].http.paths[0].backend.service.name')" == "${FULLNAME}-webhook" ]] \
  && ok "S: webhook Ingress backend points at the webhook Service" || bad "S: webhook Ingress backend wrong"
[[ "$(ing_of "${FULLNAME}-webhook" | yq '.spec.rules[0].http.paths[0].backend.service.port.number')" == "8089" ]] \
  && ok "S: webhook Ingress backend port matches config.webhook.listen" || bad "S: webhook Ingress backend port wrong"
# tls.enabled default is true; satisfied here by the annotation alone (no secretName).
[[ "$(ing_of "${FULLNAME}-webhook" | yq '.spec.tls[0].hosts[0]')" == "wh.example.com" ]] \
  && ok "S: webhook Ingress tls block rendered from the annotation alone" || bad "S: webhook Ingress tls block missing"
[[ "$(ing_of "${FULLNAME}-webhook" | yq '.metadata.annotations."cert-manager.io/cluster-issuer"')" == "letsencrypt" ]] \
  && ok "S: webhook Ingress cert-manager annotation rendered" || bad "S: webhook Ingress annotation missing"
grep -q -- "$SENTINEL" <<<"$(docs_of Ingress)" \
  && bad "S: a credential leaked into the webhook Ingress" || ok "S: no credential in the webhook Ingress"

# --------------------------------------------------------------------------
case_ "T. Ingress guards, each isolated from every other precondition (#346)"
# Every guard test below satisfies every OTHER precondition on purpose, so
# only the guard under test can possibly fail — this repo has shipped a guard
# test that silently passed because a DIFFERENT guard fired first (see the
# credential case's note below, which documents exactly that for this feature).

# Guard 1: no backing Service.
render --set config.streaming.enabled=true --set-string "config.streaming.token=${SENTINEL}" \
       --set ingress.streaming.enabled=true --set-string ingress.streaming.host=hec.example.com \
       --set-string ingress.streaming.tls.secretName=hec-tls
if [[ $RENDER_RC -ne 0 ]] && grep -q 'ingress.streaming.enabled requires service.streaming.enabled' <<<"$RENDER"; then
  ok "T: Ingress without a backing Service fails, naming service.streaming.enabled"
else
  bad "T: rendered an Ingress with no backing Service (rc=$RENDER_RC)"
fi

# Guard 2: no credential. NOTE: with service.streaming.enabled=true, the
# Service template's OWN guard (#344, tailscale2otel.requireListenerCredential)
# fires first and aborts the render before ingress.yaml's reuse of the same
# helper is ever reached — the two guards are the same check on the same
# values key, so this is expected, not a test bug. It still proves the overall
# acceptance criterion: enabling exposure with no credential fails, naming the
# exact key.
render --set config.streaming.enabled=true \
       --set service.streaming.enabled=true \
       --set ingress.streaming.enabled=true --set-string ingress.streaming.host=hec.example.com \
       --set-string ingress.streaming.tls.secretName=hec-tls
if [[ $RENDER_RC -ne 0 ]] && grep -q 'config.streaming.token' <<<"$RENDER" && grep -q 'requires a credential' <<<"$RENDER"; then
  ok "T: exposing streaming with no credential fails, naming config.streaming.token"
else
  bad "T: exposed streaming with no credential (rc=$RENDER_RC) — HMAC/token verification would be skipped"
fi

# Guard 3: no host.
render --set config.streaming.enabled=true --set-string "config.streaming.token=${SENTINEL}" \
       --set service.streaming.enabled=true \
       --set ingress.streaming.enabled=true \
       --set-string ingress.streaming.tls.secretName=hec-tls
if [[ $RENDER_RC -ne 0 ]] && grep -q 'ingress.streaming.host must be set' <<<"$RENDER"; then
  ok "T: host-less Ingress fails, naming ingress.streaming.host"
else
  bad "T: rendered a host-less (catch-all) Ingress (rc=$RENDER_RC)"
fi

# Guard 4: tls.enabled with neither secretName nor an annotation.
render --set config.streaming.enabled=true --set-string "config.streaming.token=${SENTINEL}" \
       --set service.streaming.enabled=true \
       --set ingress.streaming.enabled=true --set-string ingress.streaming.host=hec.example.com
if [[ $RENDER_RC -ne 0 ]] && grep -q 'ingress.streaming.tls.enabled is true but neither' <<<"$RENDER"; then
  ok "T: tls.enabled with nothing to terminate it fails, naming the fix"
else
  bad "T: rendered an Ingress claiming TLS with nothing configured to terminate it (rc=$RENDER_RC)"
fi
# ... and disabling tls.enabled is the documented, allowed escape hatch.
render --set config.streaming.enabled=true --set-string "config.streaming.token=${SENTINEL}" \
       --set service.streaming.enabled=true \
       --set ingress.streaming.enabled=true --set-string ingress.streaming.host=hec.example.com \
       --set ingress.streaming.tls.enabled=false
assert_rc0 "T: tls.enabled=false renders with no secretName/annotation"
[[ "$(ing_of "${FULLNAME}-streaming" | yq '.spec | has("tls")')" == "false" ]] \
  && ok "T: no tls block when tls.enabled=false" || bad "T: a tls block rendered despite tls.enabled=false"

# --------------------------------------------------------------------------
case_ "U. opt-in Gateway API HTTPRoute for streaming/webhook ONLY (#346)"
route_of() { yq "select(.kind == \"HTTPRoute\" and .metadata.name == \"$1\")" <<<"$RENDER"; }
n_httproute() { grep -c '^kind: HTTPRoute$' <<<"$RENDER" || true; }

render
assert_rc0 "U: default renders"
[[ "$(n_httproute)" == "0" ]] \
  && ok "U: no HTTPRoute by default" || bad "U: an HTTPRoute rendered by default"

render --set config.webhook.enabled=true --set-string "config.webhook.secret=${SENTINEL}" \
       --set service.webhook.enabled=true \
       --set gateway.webhook.enabled=true \
       --set-json 'gateway.webhook.parentRefs=[{"name":"my-gateway","namespace":"gateway-system","sectionName":"https"}]' \
       --set-json 'gateway.webhook.hostnames=["wh.example.com"]'
assert_rc0 "U: webhook HTTPRoute renders"
[[ "$(n_httproute)" == "1" ]] && ok "U: exactly one HTTPRoute" || bad "U: expected 1 HTTPRoute, got $(n_httproute)"
[[ "$(route_of "${FULLNAME}-webhook" | yq '.spec.parentRefs[0].name')" == "my-gateway" ]] \
  && ok "U: HTTPRoute parentRefs rendered" || bad "U: HTTPRoute parentRefs wrong"
[[ "$(route_of "${FULLNAME}-webhook" | yq '.spec.hostnames[0]')" == "wh.example.com" ]] \
  && ok "U: HTTPRoute hostnames rendered" || bad "U: HTTPRoute hostnames wrong"
[[ "$(route_of "${FULLNAME}-webhook" | yq '.spec.rules[0].backendRefs[0].name')" == "${FULLNAME}-webhook" ]] \
  && ok "U: HTTPRoute backendRefs points at the webhook Service" || bad "U: HTTPRoute backendRefs wrong"
[[ "$(route_of "${FULLNAME}-webhook" | yq '.spec.rules[0].backendRefs[0].port')" == "8089" ]] \
  && ok "U: HTTPRoute backendRefs port matches config.webhook.listen" || bad "U: HTTPRoute backendRefs port wrong"
grep -q -- "$SENTINEL" <<<"$(docs_of HTTPRoute)" \
  && bad "U: a credential leaked into the HTTPRoute" || ok "U: no credential in the HTTPRoute"

# Guard: no backing Service.
render --set config.webhook.enabled=true --set-string "config.webhook.secret=${SENTINEL}" \
       --set gateway.webhook.enabled=true \
       --set-json 'gateway.webhook.parentRefs=[{"name":"my-gateway"}]'
if [[ $RENDER_RC -ne 0 ]] && grep -q 'gateway.webhook.enabled requires service.webhook.enabled' <<<"$RENDER"; then
  ok "U: HTTPRoute without a backing Service fails, naming service.webhook.enabled"
else
  bad "U: rendered an HTTPRoute with no backing Service (rc=$RENDER_RC)"
fi

# Guard: empty parentRefs (every other precondition satisfied).
render --set config.webhook.enabled=true --set-string "config.webhook.secret=${SENTINEL}" \
       --set service.webhook.enabled=true \
       --set gateway.webhook.enabled=true
if [[ $RENDER_RC -ne 0 ]] && grep -q 'gateway.webhook.enabled requires at least one entry in gateway.webhook.parentRefs' <<<"$RENDER"; then
  ok "U: empty parentRefs fails, naming gateway.webhook.parentRefs"
else
  bad "U: rendered an HTTPRoute with no parentRefs (attaches to nothing) (rc=$RENDER_RC)"
fi

# --------------------------------------------------------------------------
case_ "V. admin and prometheus can NEVER produce an Ingress or HTTPRoute (#346)"
# Turn up every other value as far as it goes (both listeners enabled+credentialed,
# both Services on, both PodMonitor/ServiceMonitor on) and confirm zero of each kind.
# There is deliberately no ingress.admin / ingress.prometheus / gateway.admin /
# gateway.prometheus key to even attempt setting.
render --set config.admin.enabled=true --set-string "config.admin.auth.token=${SENTINEL}" \
       --set service.admin.enabled=true \
       --set config.prometheus.enabled=true --set-string "config.prometheus.auth.token=${SENTINEL}" \
       --set service.prometheus.enabled=true \
       --set config.streaming.enabled=true --set-string "config.streaming.token=${SENTINEL}" \
       --set service.streaming.enabled=true \
       --set ingress.streaming.enabled=true --set-string ingress.streaming.host=hec.example.com \
       --set-string ingress.streaming.tls.secretName=hec-tls \
       --set config.webhook.enabled=true --set-string "config.webhook.secret=${SENTINEL}" \
       --set service.webhook.enabled=true \
       --set gateway.webhook.enabled=true \
       --set-json 'gateway.webhook.parentRefs=[{"name":"my-gateway"}]'
assert_rc0 "V: everything-on (admin/prometheus/streaming/webhook) renders"
[[ "$(n_ingress)" == "1" ]] && ok "V: only the streaming Ingress rendered" || bad "V: expected 1 Ingress total, got $(n_ingress)"
[[ "$(n_httproute)" == "1" ]] && ok "V: only the webhook HTTPRoute rendered" || bad "V: expected 1 HTTPRoute total, got $(n_httproute)"
[[ -z "$(ing_of "${FULLNAME}-admin" | tr -d '[:space:]-')" ]] \
  && ok "V: no admin Ingress" || bad "V: an admin Ingress rendered"
[[ -z "$(ing_of "${FULLNAME}-prometheus" | tr -d '[:space:]-')" ]] \
  && ok "V: no prometheus Ingress" || bad "V: a prometheus Ingress rendered"
[[ -z "$(route_of "${FULLNAME}-admin" | tr -d '[:space:]-')" ]] \
  && ok "V: no admin HTTPRoute" || bad "V: an admin HTTPRoute rendered"
[[ -z "$(route_of "${FULLNAME}-prometheus" | tr -d '[:space:]-')" ]] \
  && ok "V: no prometheus HTTPRoute" || bad "V: a prometheus HTTPRoute rendered"

# ingress.admin / ingress.prometheus / gateway.admin / gateway.prometheus are not
# real keys this chart defines or reads anywhere — templates/ingress.yaml and
# templates/httproute.yaml only ever range over a hardcoded streaming/webhook
# list. Since #304, values.schema.json now rejects them at the schema level
# (ingress:/gateway: are additionalProperties:false, and admin/prometheus are not
# among their declared properties) — a STRONGER guarantee than the old "silently a
# no-op": an operator who tries this now gets a hard failure naming the mistake,
# not silent nothing.
render --set ingress.admin.enabled=true --set-string ingress.admin.host=admin.example.com \
       --set-string ingress.admin.tls.secretName=admin-tls \
       --set ingress.prometheus.enabled=true --set-string ingress.prometheus.host=prom.example.com \
       --set-string ingress.prometheus.tls.secretName=prom-tls \
       --set gateway.admin.enabled=true --set-json 'gateway.admin.parentRefs=[{"name":"my-gateway"}]' \
       --set gateway.prometheus.enabled=true --set-json 'gateway.prometheus.parentRefs=[{"name":"my-gateway"}]'
if [[ $RENDER_RC -ne 0 ]] \
   && grep -q "at '/ingress'.*'admin'.*'prometheus'\|at '/ingress'.*'prometheus'.*'admin'" <<<"$RENDER" \
   && grep -q "at '/gateway'.*'admin'.*'prometheus'\|at '/gateway'.*'prometheus'.*'admin'" <<<"$RENDER"; then
  ok "V: ingress.admin/prometheus and gateway.admin/prometheus are rejected by the schema, naming both"
else
  bad "V: a forced admin/prometheus ingress/gateway attempt was NOT rejected by the schema (rc=$RENDER_RC)"
fi

# --------------------------------------------------------------------------
case_ "W. immutable image digest (#349)"
image_ref() { dep_field '.spec.template.spec.containers[] | select(.name == "tailscale2otel") | .image'; }

render
assert_rc0 "W: default renders"
[[ "$(image_ref)" == "ghcr.io/rknightion/tailscale2otel:3.0.0" ]] \
  && ok "W: default image is repository:appVersion" || bad "W: default image is $(image_ref)"

render --set-string image.tag=v9.9.9
assert_rc0 "W: tag override renders"
[[ "$(image_ref)" == "ghcr.io/rknightion/tailscale2otel:v9.9.9" ]] \
  && ok "W: tag override honoured" || bad "W: tag override is $(image_ref)"

DIGEST="sha256:$(printf '%064d' 1)"
render --set-string "image.digest=${DIGEST}"
assert_rc0 "W: digest alone renders"
[[ "$(image_ref)" == "ghcr.io/rknightion/tailscale2otel@${DIGEST}" ]] \
  && ok "W: digest produces repository@digest" || bad "W: digest reference is $(image_ref)"

# Digest wins deterministically over a tag left set alongside it — never repo:tag@digest.
render --set-string image.tag=v9.9.9 --set-string "image.digest=${DIGEST}"
assert_rc0 "W: digest+tag both set renders"
[[ "$(image_ref)" == "ghcr.io/rknightion/tailscale2otel@${DIGEST}" ]] \
  && ok "W: digest wins over a tag set alongside it" || bad "W: digest did not win, got $(image_ref)"
grep -q 'v9.9.9' <<<"$(image_ref)" \
  && bad "W: the ignored tag still leaked into the image reference" \
  || ok "W: the ignored tag does not leak into the image reference"

# Layer 1: values.schema.json's pattern rejects a malformed digest BEFORE templating.
render --set-string "image.digest=sha256:not-hex"
[[ $RENDER_RC -ne 0 ]] \
  && ok "W: schema rejects a malformed digest (bad hex)" \
  || bad "W: a malformed digest (bad hex) was accepted"
render --set-string "image.digest=not-even-sha256-shaped"
[[ $RENDER_RC -ne 0 ]] \
  && ok "W: schema rejects a digest with no sha256: prefix" \
  || bad "W: a digest with no sha256: prefix was accepted"
render --set-string "image.digest=sha256:$(printf '%063d' 1)"
[[ $RENDER_RC -ne 0 ]] \
  && ok "W: schema rejects a digest one hex character short" \
  || bad "W: a too-short digest was accepted"

# Layer 2: the template `fail` catches a malformed digest that reaches templating
# with no schema check at all (--skip-schema-validation, or any schema-less path).
render --set-string "image.digest=sha256:not-hex" --skip-schema-validation
if [[ $RENDER_RC -ne 0 ]] && grep -q 'image.digest' <<<"$RENDER"; then
  ok "W: template guard also rejects a malformed digest with schema validation skipped"
else
  bad "W: --skip-schema-validation let a malformed digest reach the container image (rc=$RENDER_RC)"
fi

# --------------------------------------------------------------------------
case_ "X. configurable probes + startup probe (#350)"
probe_field() { dep_field ".spec.template.spec.containers[] | select(.name == \"tailscale2otel\") | .${1}Probe.${2}"; }
has_probe() { dep_field ".spec.template.spec.containers[] | select(.name == \"tailscale2otel\") | has(\"${1}Probe\")"; }

render
assert_rc0 "X: default renders"
# The default rendered liveness/readiness timings must be BYTE-IDENTICAL to the
# hardcoded values this replaces — the only two fields the old template ever
# rendered were initialDelaySeconds/periodSeconds, so the new
# timeoutSeconds/failureThreshold/successThreshold fields must stay OMITTED at
# their own Kubernetes-API-default values (1/3/1) rather than appearing as new,
# functionally-inert lines in the manifest.
[[ "$(probe_field liveness initialDelaySeconds)" == "5" ]] \
  && ok "X: default liveness initialDelaySeconds unchanged (5)" || bad "X: liveness initialDelaySeconds is $(probe_field liveness initialDelaySeconds)"
[[ "$(probe_field liveness periodSeconds)" == "15" ]] \
  && ok "X: default liveness periodSeconds unchanged (15)" || bad "X: liveness periodSeconds is $(probe_field liveness periodSeconds)"
[[ "$(probe_field readiness initialDelaySeconds)" == "5" ]] \
  && ok "X: default readiness initialDelaySeconds unchanged (5)" || bad "X: readiness initialDelaySeconds is $(probe_field readiness initialDelaySeconds)"
[[ "$(probe_field readiness periodSeconds)" == "15" ]] \
  && ok "X: default readiness periodSeconds unchanged (15)" || bad "X: readiness periodSeconds is $(probe_field readiness periodSeconds)"
for kind in liveness readiness; do
  for field in timeoutSeconds failureThreshold successThreshold; do
    v="$(probe_field "$kind" "$field")"
    [[ -z "$v" || "$v" == "null" ]] \
      && ok "X: default ${kind}.${field} stays omitted (matches k8s API default)" \
      || bad "X: default ${kind}.${field} unexpectedly rendered ($v) — output is not byte-identical to before"
  done
done
[[ "$(has_probe startup)" != "true" ]] \
  && ok "X: no startupProbe by default" || bad "X: a startupProbe rendered by default"

# TLS scheme stays automatic and is NOT an exposed value (#342 behavior preserved).
render --set-string config.admin.tls.cert_file=/tls/tls.crt --set-string config.admin.tls.key_file=/tls/tls.key
assert_rc0 "X: admin TLS renders"
[[ "$(probe_field liveness httpGet.scheme)" == "HTTPS" ]] \
  && ok "X: liveness still follows admin TLS automatically" || bad "X: liveness scheme did not follow admin TLS"

# Each probe independently disableable.
render --set probes.liveness.enabled=false
assert_rc0 "X: liveness disabled renders"
[[ "$(has_probe liveness)" != "true" ]] \
  && ok "X: livenessProbe absent when disabled" || bad "X: livenessProbe still rendered when disabled"
[[ "$(has_probe readiness)" == "true" ]] \
  && ok "X: readinessProbe unaffected by disabling liveness" || bad "X: readinessProbe disappeared too"

render --set probes.readiness.enabled=false
assert_rc0 "X: readiness disabled renders"
[[ "$(has_probe readiness)" != "true" ]] \
  && ok "X: readinessProbe absent when disabled" || bad "X: readinessProbe still rendered when disabled"

# Startup probe: opt-in, targets /readyz, full timing set rendered.
render --set probes.startup.enabled=true
assert_rc0 "X: startup enabled renders"
[[ "$(has_probe startup)" == "true" ]] \
  && ok "X: startupProbe renders when enabled" || bad "X: startupProbe missing when enabled"
[[ "$(probe_field startup httpGet.path)" == "/readyz" ]] \
  && ok "X: startup probe targets /readyz" || bad "X: startup probe path is $(probe_field startup httpGet.path)"
[[ "$(probe_field startup periodSeconds)" == "10" ]] \
  && ok "X: startup default periodSeconds is 10" || bad "X: startup periodSeconds is $(probe_field startup periodSeconds)"
[[ "$(probe_field startup failureThreshold)" == "30" ]] \
  && ok "X: startup default failureThreshold is 30" || bad "X: startup failureThreshold is $(probe_field startup failureThreshold)"

# Overriding a non-default timing DOES render it explicitly.
render --set probes.liveness.timeoutSeconds=5
assert_rc0 "X: liveness timeoutSeconds override renders"
[[ "$(probe_field liveness timeoutSeconds)" == "5" ]] \
  && ok "X: overridden liveness timeoutSeconds renders explicitly" || bad "X: override not rendered"

# admin disabled -> no probe may render at all, whatever probes.* says.
render --set config.admin.enabled=false --set probes.startup.enabled=true
assert_rc0 "X: admin disabled renders"
[[ "$(has_probe liveness)" != "true" && "$(has_probe readiness)" != "true" && "$(has_probe startup)" != "true" ]] \
  && ok "X: no probes at all when config.admin.enabled=false" \
  || bad "X: a probe rendered despite config.admin.enabled=false"

# successThreshold must be 1 on liveness/startup — Kubernetes rejects anything else.
# Layer 1: schema `enum:[1]` rejects a numeric value other than 1 before templating.
render --set probes.liveness.successThreshold=2
[[ $RENDER_RC -ne 0 ]] \
  && ok "X: schema rejects liveness.successThreshold=2" || bad "X: liveness.successThreshold=2 was accepted"
render --set probes.startup.successThreshold=2 --set probes.startup.enabled=true
[[ $RENDER_RC -ne 0 ]] \
  && ok "X: schema rejects startup.successThreshold=2" || bad "X: startup.successThreshold=2 was accepted"
# readiness has no such Kubernetes restriction — any value >=1 is valid.
render --set probes.readiness.successThreshold=2
assert_rc0 "X: readiness.successThreshold=2 is valid and renders"
[[ "$(probe_field readiness successThreshold)" == "2" ]] \
  && ok "X: readiness successThreshold=2 rendered" || bad "X: readiness successThreshold=2 not rendered"

# Layer 2: the template guard catches null (schema minimum/enum do not reject null;
# an explicit --set to null bypasses schema validation for that field entirely).
render --set probes.liveness.successThreshold=null
if [[ $RENDER_RC -ne 0 ]] && grep -q 'probes.liveness.successThreshold must be 1' <<<"$RENDER"; then
  ok "X: template guard catches null liveness.successThreshold, naming the field"
else
  bad "X: null liveness.successThreshold was not caught (rc=$RENDER_RC)"
fi
render --set probes.liveness.periodSeconds=null
if [[ $RENDER_RC -ne 0 ]] && grep -q 'probes.liveness.periodSeconds must be at least 1' <<<"$RENDER"; then
  ok "X: template guard catches null liveness.periodSeconds, naming the field"
else
  bad "X: null liveness.periodSeconds was not caught (rc=$RENDER_RC)"
fi

# --------------------------------------------------------------------------
case_ "Y. opt-in NetworkPolicy baseline (#351)"
n_netpol() { grep -c '^kind: NetworkPolicy$' <<<"$RENDER" || true; }
# Filter to the NetworkPolicy document FIRST, then query it, like docs_of/dep_field
# do — NOT `select(...) | <query>` in one combined yq call: with an array/length
# query, yq's array constructor `[...]` still yields an empty `[]` (length 0) for
# every document select() has ALREADY excluded, so a single combined expression
# prints one real count plus a spurious `0` per other document instead of nothing.
netpol() { yq 'select(.kind == "NetworkPolicy")' <<<"$RENDER" | yq "$1"; }

render
assert_rc0 "Y: default renders"
[[ "$(n_netpol)" == "0" ]] \
  && ok "Y: default chart renders NO NetworkPolicy" || bad "Y: a NetworkPolicy rendered by default"

render --set networkPolicy.enabled=true
assert_rc0 "Y: bare enable renders"
[[ "$(n_netpol)" == "1" ]] && ok "Y: exactly one NetworkPolicy" || bad "Y: expected 1 NetworkPolicy, got $(n_netpol)"
[[ "$(netpol '.spec.podSelector.matchLabels."app.kubernetes.io/instance"')" == "$RELEASE" ]] \
  && ok "Y: podSelector matches this release's pods" || bad "Y: podSelector wrong"
[[ "$(netpol '.spec | has("egress")')" == "true" ]] \
  && ok "Y: egress section present (default allowAll)" || bad "Y: egress section missing"
[[ "$(netpol '.spec.egress[0]')" == "{}" ]] \
  && ok "Y: default egress.allowAll renders an open {} rule" || bad "Y: default egress is not the open {} rule"
# The default config has admin+its probes on, so bare-enable already opens
# exactly the admin ingress rule (proven properly, isolated, further down);
# here just confirm no OTHER listener leaks a rule with nothing else touched.
[[ "$(netpol '[.spec.ingress[]?] | length')" == "1" ]] \
  && ok "Y: bare enable opens only the (default-on) admin ingress rule" \
  || bad "Y: expected exactly 1 ingress rule (admin) with nothing else touched, got $(netpol '[.spec.ingress[]?] | length')"

# Ingress must match enabled Services/PodMonitor/probes exactly (acceptance: "Enabled
# Services/PodMonitor/probes have matching ingress").
render --set networkPolicy.enabled=true \
       --set config.prometheus.enabled=true --set-string "config.prometheus.auth.token=${SENTINEL}" \
       --set service.prometheus.enabled=true
assert_rc0 "Y: prometheus Service renders with policy on"
[[ "$(netpol '[.spec.ingress[] | select(.ports[0].port == 2112)] | length')" == "1" ]] \
  && ok "Y: prometheus Service enablement opens an ingress rule for its port" \
  || bad "Y: prometheus Service enabled but no matching ingress rule"

render --set networkPolicy.enabled=true \
       --set config.prometheus.enabled=true --set metrics.podMonitor.enabled=true --set config.prometheus.auth.allow_unauthenticated=true
assert_rc0 "Y: PodMonitor renders with policy on"
[[ "$(netpol '[.spec.ingress[] | select(.ports[0].port == 2112)] | length')" == "1" ]] \
  && ok "Y: PodMonitor enablement opens an ingress rule for the prometheus port" \
  || bad "Y: PodMonitor enabled but no matching ingress rule"

render --set networkPolicy.enabled=true --set config.prometheus.enabled=true
assert_rc0 "Y: prometheus listener alone (no consumer) renders with policy on"
[[ "$(netpol '[.spec.ingress[] | select(.ports[0].port == 2112)] | length')" == "0" ]] \
  && ok "Y: prometheus listener with NO Service/PodMonitor/ServiceMonitor consumer opens no ingress hole" \
  || bad "Y: an ingress rule opened for prometheus with no consumer at all"

# Admin/probes: the kubelet reaches the admin port directly (no Service in the
# path), so the rule tracks whether any probe is enabled, not service.admin.enabled.
render --set networkPolicy.enabled=true
assert_rc0 "Y: default admin probes render with policy on"
[[ "$(netpol '[.spec.ingress[] | select(.ports[0].port == 9091)] | length')" == "1" ]] \
  && ok "Y: default liveness/readiness probes open an ingress rule for the admin port" \
  || bad "Y: default probes enabled but no matching admin ingress rule"

render --set networkPolicy.enabled=true \
       --set probes.liveness.enabled=false --set probes.readiness.enabled=false
assert_rc0 "Y: all probes disabled renders with policy on"
[[ "$(netpol '[.spec.ingress[] | select(.ports[0].port == 9091)] | length')" == "0" ]] \
  && ok "Y: no admin ingress rule once every probe is disabled" \
  || bad "Y: an admin ingress rule opened despite every probe being disabled"

render --set networkPolicy.enabled=true --set config.admin.enabled=false \
       --set probes.liveness.enabled=false --set probes.readiness.enabled=false --set probes.startup.enabled=false
assert_rc0 "Y: admin listener off renders with policy on"
[[ "$(netpol '[.spec.ingress[] | select(.ports[0].port == 9091)] | length')" == "0" ]] \
  && ok "Y: no admin ingress rule when config.admin.enabled=false" \
  || bad "Y: an admin ingress rule opened for a disabled admin listener"

render --set networkPolicy.enabled=true \
       --set config.streaming.enabled=true --set-string "config.streaming.token=${SENTINEL}" \
       --set service.streaming.enabled=true
assert_rc0 "Y: streaming Service renders with policy on"
[[ "$(netpol '[.spec.ingress[] | select(.ports[0].port == 8088)] | length')" == "1" ]] \
  && ok "Y: streaming Service enablement opens an ingress rule for its port" \
  || bad "Y: streaming Service enabled but no matching ingress rule"

render --set networkPolicy.enabled=true --set config.streaming.enabled=true
assert_rc0 "Y: streaming listener alone (no Service) renders with policy on"
[[ "$(netpol '[.spec.ingress[] | select(.ports[0].port == 8088)] | length')" == "0" ]] \
  && ok "Y: streaming listener with no Service opens no ingress hole" \
  || bad "Y: an ingress rule opened for streaming with no backing Service"

render --set networkPolicy.enabled=true \
       --set config.webhook.enabled=true --set-string "config.webhook.secret=${SENTINEL}" \
       --set service.webhook.enabled=true
assert_rc0 "Y: webhook Service renders with policy on"
[[ "$(netpol '[.spec.ingress[] | select(.ports[0].port == 8089)] | length')" == "1" ]] \
  && ok "Y: webhook Service enablement opens an ingress rule for its port" \
  || bad "Y: webhook Service enabled but no matching ingress rule"

# Receiver ports remain closed unless explicitly selected — no credential leak either.
grep -q -- "$SENTINEL" <<<"$(docs_of NetworkPolicy)" \
  && bad "Y: a credential leaked into the NetworkPolicy" || ok "Y: no credential in the NetworkPolicy"

# egress.dns
render --set networkPolicy.enabled=true --set networkPolicy.egress.allowAll=false
assert_rc0 "Y: allowAll=false with dns default renders"
[[ "$(netpol '[.spec.egress[]? | select(.ports[]?.port == 53)] | length')" == "1" ]] \
  && ok "Y: DNS egress rule present when allowAll=false (dns default true)" \
  || bad "Y: DNS egress rule missing when allowAll=false"
[[ "$(netpol '[.spec.egress[]?] | length')" == "1" ]] \
  && ok "Y: allowAll=false with dns only and no extra yields exactly one egress rule" \
  || bad "Y: unexpected extra egress rules"

render --set networkPolicy.enabled=true --set networkPolicy.egress.allowAll=false --set networkPolicy.egress.dns=false
assert_rc0 "Y: allowAll=false, dns=false renders"
[[ "$(netpol '[.spec.egress[]?] | length')" == "0" ]] \
  && ok "Y: no egress rules at all with allowAll=false and dns=false and no extra" \
  || bad "Y: an unexpected egress rule rendered"

render --set networkPolicy.enabled=true --set networkPolicy.egress.allowAll=false \
       --set-json 'networkPolicy.egress.extra=[{"to":[{"ipBlock":{"cidr":"203.0.113.0/24"}}]}]'
assert_rc0 "Y: egress.extra passthrough renders"
[[ "$(netpol '[.spec.egress[]? | select(.to[0].ipBlock.cidr == "203.0.113.0/24")] | length')" == "1" ]] \
  && ok "Y: egress.extra rule passed through as-is" || bad "Y: egress.extra rule missing"

render --set networkPolicy.enabled=true \
       --set-json 'networkPolicy.ingress.extra=[{"from":[{"namespaceSelector":{}}],"ports":[{"protocol":"TCP","port":1234}]}]'
assert_rc0 "Y: ingress.extra passthrough renders"
[[ "$(netpol '[.spec.ingress[] | select(.ports[0].port == 1234)] | length')" == "1" ]] \
  && ok "Y: ingress.extra rule passed through as-is" || bad "Y: ingress.extra rule missing"

# --------------------------------------------------------------------------
case_ "Z. values.schema.json strictness (#304)"
# The generated schema (deploy/helm/tailscale2otel/values.schema.json, regenerated
# from values.yaml's `# @schema additionalProperties:false` annotations by
# `scripts/regen-generated.sh helm`) rejects an unknown key on every CHART-AUTHORED
# object, while intentionally free-form maps (labels/annotations/headers/tags/
# nodeSelector/resources.requests|limits/extraEnv valueFrom/...) stay open.

render
assert_rc0 "Z: known-good default render still succeeds"

# A typo'd nested config.* key fails (the issue's core acceptance case, one level
# down from the chart root: config.tailscale is additionalProperties:false).
render --set config.tailscale.bogus_key=1
[[ $RENDER_RC -ne 0 ]] && grep -q "additional properties 'bogus_key' not allowed" <<<"$RENDER" \
  && ok "Z: unknown config.tailscale.* key fails, naming the key" \
  || bad "Z: unknown config.tailscale.* key was accepted (rc=$RENDER_RC)"

# A near-miss typo of a real key (singular "device" vs the real "devices") fails
# the same way — this is exactly the class of mistake #304 was filed over.
render --set config.collectors.device.enabled=true
[[ $RENDER_RC -ne 0 ]] && grep -q "additional properties 'device' not allowed" <<<"$RENDER" \
  && ok "Z: near-miss config.collectors.device (vs devices) fails" \
  || bad "Z: near-miss config.collectors.device was accepted (rc=$RENDER_RC)"

# Another near-miss one level up: config.otlp.header (singular) instead of headers.
render --set config.otlp.header.X-Foo=bar
[[ $RENDER_RC -ne 0 ]] && grep -q "additional properties 'header' not allowed" <<<"$RENDER" \
  && ok "Z: near-miss config.otlp.header (vs headers) fails" \
  || bad "Z: near-miss config.otlp.header was accepted (rc=$RENDER_RC)"

# --- free-form maps must stay usable: one arbitrary key each, real render, no error ---
render --set podAnnotations.example\\.com/foo=bar
assert_rc0 "Z: podAnnotations accepts an arbitrary key"
[[ "$(dep_field '.spec.template.metadata.annotations."example.com/foo"')" == "bar" ]] \
  && ok "Z: podAnnotations arbitrary key rendered" || bad "Z: podAnnotations arbitrary key missing"

render --set podLabels.example\\.com/team=sre
assert_rc0 "Z: podLabels accepts an arbitrary key"
[[ "$(dep_field '.spec.template.metadata.labels."example.com/team"')" == "sre" ]] \
  && ok "Z: podLabels arbitrary key rendered" || bad "Z: podLabels arbitrary key missing"

render --set nodeSelector.disktype=ssd
assert_rc0 "Z: nodeSelector accepts an arbitrary key"
[[ "$(dep_field '.spec.template.spec.nodeSelector.disktype')" == "ssd" ]] \
  && ok "Z: nodeSelector arbitrary key rendered" || bad "Z: nodeSelector arbitrary key missing"

render --set serviceAccount.annotations.example\\.com/role-arn=arn:aws:iam::1:role/x
assert_rc0 "Z: serviceAccount.annotations accepts an arbitrary key"

render --set-string config.otlp.headers.X-Scope-OrgID=tenant-a
assert_rc0 "Z: config.otlp.headers accepts an arbitrary key"

render --set-string config.profiling.pyroscope.tags.env=prod
assert_rc0 "Z: config.profiling.pyroscope.tags accepts an arbitrary key"

render --set service.admin.enabled=true --set config.admin.auth.token=abc \
       --set service.admin.annotations.example\\.com/lb-type=internal
assert_rc0 "Z: service.admin.annotations accepts an arbitrary key"

render --set ingress.webhook.annotations.cert-manager\\.io/cluster-issuer=letsencrypt \
       --set config.webhook.enabled=true --set config.webhook.secret=abc \
       --set service.webhook.enabled=true --set ingress.webhook.enabled=true \
       --set ingress.webhook.host=example.com
assert_rc0 "Z: ingress.webhook.annotations accepts an arbitrary key"

render --set resources.requests.nvidia\\.com/gpu=1
assert_rc0 "Z: resources.requests accepts an arbitrary (custom) resource-name key"
[[ "$(dep_field '.spec.template.spec.containers[0].resources.requests."nvidia.com/gpu"')" == "1" ]] \
  && ok "Z: resources.requests custom key rendered" || bad "Z: resources.requests custom key missing"

render --set-json 'metrics.podMonitor.labels={"release":"prometheus"}' \
       --set metrics.podMonitor.enabled=true --set config.prometheus.enabled=true --set config.prometheus.auth.allow_unauthenticated=true
assert_rc0 "Z: metrics.podMonitor.labels accepts an arbitrary key"

# --- Root-level strictness. `--set secrets.foo=bar` is the issue's own repro: a
# plausible typo for a real key that Helm accepted happily, producing a release
# with no credential rendered and no error anywhere (#304).
#
# Root strictness is the one part of the schema that does NOT come from a values.yaml
# annotation — a `# @schema` comment at the top of the file attaches to the first KEY,
# not to the root mapping. It comes from --schema-root.additional-properties=false in
# scripts/regen-generated.sh and the matching `additionalProperties: false` input in
# .github/workflows/helm.yml. Those two must move together: drop either and this
# assertion is the thing that notices.
render --set secrets.foo=bar
[[ $RENDER_RC -ne 0 ]] \
  && ok "Z: root-level typo 'secrets.foo' is rejected" \
  || bad "Z: root-level typo 'secrets.foo' still renders"
grep -q "additional properties 'secrets' not allowed" <<<"$RENDER" \
  && ok "Z: rejection names the offending root key" \
  || bad "Z: rejection did not name 'secrets'"

# --- AA: a monitor that scrapes an unauthenticated, network-bound /metrics.
#
# config.prometheus.listen defaults to ":2112" — a WILDCARD bind — and the app
# refuses /metrics there with 403 when no token is set and the exposure is not
# acknowledged (#315). A PodMonitor/ServiceMonitor pointed at that listener scrapes
# a 403 forever and reports "no data", which looks like an exporter problem rather
# than a chart misconfiguration. The chart names it at render time instead.
render --set config.prometheus.enabled=true --set metrics.podMonitor.enabled=true
[[ $RENDER_RC -ne 0 ]] \
  && ok "AA: podMonitor over an unauthenticated network bind is rejected" \
  || bad "AA: podMonitor over an unauthenticated network bind rendered"
# Scoped to the guard's own wording. A bare grep for 'allow_unauthenticated' would
# also match the rendered ConfigMap on a SUCCESSFUL render, so it passed while the
# thing it guards was broken — verified during development.
grep -q 'REFUSES /metrics there with HTTP 403' <<<"$RENDER" \
  && ok "AA: the rejection explains the 403 the scrape would hit" \
  || bad "AA: the rejection does not explain the 403"
grep -q 'allow_unauthenticated=true to acknowledge' <<<"$RENDER" \
  && ok "AA: the rejection names the acknowledgement knob" \
  || bad "AA: the rejection does not name allow_unauthenticated"

render --set config.prometheus.enabled=true --set service.prometheus.enabled=true \
       --set metrics.serviceMonitor.enabled=true
[[ $RENDER_RC -ne 0 ]] \
  && ok "AA: serviceMonitor over an unauthenticated network bind is rejected" \
  || bad "AA: serviceMonitor over an unauthenticated network bind rendered"

# The three ways out, each of which must render.
render --set config.prometheus.enabled=true --set metrics.podMonitor.enabled=true \
       --set config.prometheus.auth.allow_unauthenticated=true
assert_rc0 "AA: acknowledging the exposure lets the podMonitor render"

render --set config.prometheus.enabled=true --set metrics.podMonitor.enabled=true \
       --set config.prometheus.auth.token=s3cret \
       --set-json 'metrics.podMonitor.bearerTokenSecret={"name":"t","key":"k"}'
assert_rc0 "AA: a token plus a matching bearerTokenSecret lets the podMonitor render"

# And the guard must not fire when nothing scrapes.
render --set config.prometheus.enabled=true
assert_rc0 "AA: /metrics alone, with no monitor, is unaffected"

printf '\n---\n%d passed, %d failed\n' "$pass" "$fail"
[[ $fail -eq 0 ]]
