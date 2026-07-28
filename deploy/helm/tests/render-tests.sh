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

printf '\n---\n%d passed, %d failed\n' "$pass" "$fail"
[[ $fail -eq 0 ]]
