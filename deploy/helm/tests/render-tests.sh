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
#                 force a rollout, and an inline `secret:` change must still
#                 move `checksum/secret`.
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
case_ "D. inline secret: changes still move checksum/secret"
render --set secret.TS2OTEL_TAILSCALE__AUTH__APIKEY=v1
c1="$(dep_field '.spec.template.metadata.annotations."checksum/secret"')"
render --set secret.TS2OTEL_TAILSCALE__AUTH__APIKEY=v2
c2="$(dep_field '.spec.template.metadata.annotations."checksum/secret"')"
[[ -n "$c1" && "$c1" != "$c2" ]] && ok "D: checksum/secret changes with the inline value" \
  || bad "D: checksum/secret did not change ($c1 vs $c2)"

case_ "D2. inline config: changes still move checksum/config"
render --set config.log_level=debug
c3="$(dep_field '.spec.template.metadata.annotations."checksum/config"')"
render --set config.log_level=warn
c4="$(dep_field '.spec.template.metadata.annotations."checksum/config"')"
[[ -n "$c3" && "$c3" != "$c4" ]] && ok "D2: checksum/config changes with the config" \
  || bad "D2: checksum/config did not change"

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

printf '\n---\n%d passed, %d failed\n' "$pass" "$fail"
[[ $fail -eq 0 ]]
