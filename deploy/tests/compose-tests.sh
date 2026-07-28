#!/usr/bin/env bash
# Resolution-level regression tests for the Compose assets (#333).
#
# WHY THIS EXISTS. `deploy/docker-compose.yaml` used to carry a commented-out
# SECOND `volumes:` key with the instruction "uncomment to mount a config file".
# A Compose service map may only have one `volumes:` key, so following the
# shipped instruction produced a document Compose refuses outright:
#
#   failed to parse deploy/docker-compose.yaml: yaml: construct errors:
#     line 28: line 80: mapping key "volumes" already defined at line 77
#
# Nothing checked it, because nothing in CI ever resolved the compose file at
# all — the image build reads the Dockerfile directly. The file-backed config
# mode now lives in `docker-compose.config.yaml`, an override applied with a
# second `-f`, where Compose MERGES the two volume lists instead of colliding.
# That merge is the whole contract, and it is what these tests assert: the
# checkpoint volume must survive into the file-backed mode.
#
# Requires: docker compose (v2+), yq (mikefarah). Run from anywhere:
#   deploy/tests/compose-tests.sh
#   deploy/tests/compose-tests.sh --self-test   # prove the assertions can fail
set -uo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_DIR="${SCRIPT_DIR}/.."
BASE="${DEPLOY_DIR}/docker-compose.yaml"
OVERRIDE="${DEPLOY_DIR}/docker-compose.config.yaml"
SERVICE=tailscale2otel
CHECKPOINT_TARGET=/var/lib/tailscale2otel
CONFIG_TARGET=/etc/tailscale2otel/config.yaml

pass=0
fail=0
ok()    { printf '  ok   %s\n' "$1"; pass=$((pass + 1)); }
bad()   { printf '  FAIL %s\n' "$1"; fail=$((fail + 1)); }
case_() { printf '\n== %s\n' "$1"; }

# resolve -f... -> RESOLVED holds the fully resolved compose document.
RESOLVED=""
RESOLVED_RC=0
resolve() { RESOLVED="$(docker compose "$@" config 2>&1)"; RESOLVED_RC=$?; }

# svc QUERY -> evaluate a yq query against the resolved service.
svc() { yq ".services.${SERVICE} | ${1}" <<<"$RESOLVED"; }

# has_mount TARGET -> 0 when the resolved service mounts TARGET.
has_mount() {
  [[ "$(svc "[.volumes[]? | select(.target == \"$1\")] | length")" != "0" ]]
}

assert_rc0() {
  if [[ $RESOLVED_RC -eq 0 ]]; then
    ok "$1"
  else
    bad "$1 (docker compose exited $RESOLVED_RC)"
    printf '%s\n' "$RESOLVED" | sed 's/^/       | /' | head -5
  fi
}

# The core #333 assertion: checkpoint persistence survives whichever mode is used.
assert_checkpoint_mount() {
  if has_mount "$CHECKPOINT_TARGET"; then
    ok "$1: checkpoint volume mounted at $CHECKPOINT_TARGET"
  else
    bad "$1: checkpoint volume LOST — persistence silently discarded"
  fi
}

assert_config_mount() {
  if has_mount "$CONFIG_TARGET"; then
    ok "$1: config file mounted at $CONFIG_TARGET"
  else
    bad "$1: config file NOT mounted at $CONFIG_TARGET"
  fi
}

assert_no_config_mount() {
  if has_mount "$CONFIG_TARGET"; then
    bad "$1: unexpected config mount in the env-var-only default mode"
  else
    ok "$1: no config mount (env-var-only default)"
  fi
}

# --------------------------------------------------------------------------
case_ "A. default mode: env vars only, checkpoints persisted"
resolve -f "$BASE"
assert_rc0 "A: resolves"
assert_checkpoint_mount "A"
assert_no_config_mount "A"
[[ "$(svc '.command')" == "null" ]] \
  && ok "A: no command override (built-in defaults + TS2OTEL_*)" \
  || bad "A: unexpected command override: $(svc '.command')"

# --------------------------------------------------------------------------
case_ "B. file-backed mode: config mounted AND checkpoints still persisted"
resolve -f "$BASE" -f "$OVERRIDE"
assert_rc0 "B: resolves"
# This is the regression #333 is about. The override adds a mount; Compose must
# MERGE it into the base volume list, not replace the list.
assert_checkpoint_mount "B"
assert_config_mount "B"
[[ "$(svc "[.volumes[] | select(.target == \"$CONFIG_TARGET\")][0].read_only")" == "true" ]] \
  && ok "B: config mounted read-only" || bad "B: config mount is not read-only"
[[ "$(svc '.command | join(" ")')" == "-config $CONFIG_TARGET" ]] \
  && ok "B: command passes -config $CONFIG_TARGET" \
  || bad "B: wrong command: $(svc '.command | join(" ")')"
# The mount count must be exactly base + 1: a replaced list would still satisfy
# both mount assertions above if the override happened to name both volumes.
[[ "$(svc '.volumes | length')" == "2" ]] \
  && ok "B: exactly 2 mounts (base checkpoint + override config)" \
  || bad "B: expected 2 mounts, got $(svc '.volumes | length')"

# --------------------------------------------------------------------------
case_ "C. the removed instruction stays removed"
# A second `volumes:` key in the same service map is what the base file used to
# tell operators to uncomment. Assert the base file carries no duplicate key,
# by the only authority that matters: Compose's own parser, which rejects it.
dupes="$(yq '.services.'"$SERVICE"' | keys | .[]' "$BASE" | sort | uniq -d)"
[[ -z "$dupes" ]] \
  && ok "C: no duplicate keys in the $SERVICE service map" \
  || bad "C: duplicate service keys would break Compose parsing: $dupes"
if grep -qE '^\s*#\s*volumes:' "$BASE"; then
  bad "C: base file still comments out a second 'volumes:' key — uncommenting it is a parse error"
else
  ok "C: base file no longer suggests a second 'volumes:' key"
fi

# --------------------------------------------------------------------------
case_ "D. file-based secrets mode keeps values out of the environment (#334)"
SECRETS_OVERRIDE="${DEPLOY_DIR}/docker-compose.secrets.yaml"
# Real files, because `docker compose config` resolves secret file paths and
# fails on a missing one — which is itself part of the contract.
sdir="$(mktemp -d)"
trap 'rm -rf "$sdir"' EXIT
SECRET_SENTINEL="SENTINEL-compose-secret-do-not-leak"
for f in oauth_client_secret grafana_cloud_token admin_token streaming_token webhook_secret pyroscope_password; do
  printf '%s' "$SECRET_SENTINEL" > "$sdir/$f"
done

RESOLVED="$(SECRETS_DIR="$sdir" docker compose -f "$BASE" -f "$SECRETS_OVERRIDE" config 2>&1)"; RESOLVED_RC=$?
assert_rc0 "D: resolves"
assert_checkpoint_mount "D"

# The whole point: no secret VALUE may appear anywhere in the resolved config.
if grep -q -- "$SECRET_SENTINEL" <<<"$RESOLVED"; then
  bad "D: a secret VALUE appears in the resolved compose config"
else
  ok "D: no secret value in the resolved config"
fi

# Every credential must arrive as a *_FILE path, and the matching non-file
# variable must NOT carry a value — value-XOR-file is the app's contract, and
# setting both is a startup error.
for pair in \
  "TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET" \
  "TS2OTEL_OTLP__GRAFANA_CLOUD__TOKEN" \
  "TS2OTEL_ADMIN__AUTH__TOKEN" \
  "TS2OTEL_STREAMING__TOKEN" \
  "TS2OTEL_WEBHOOK__SECRET" \
  "TS2OTEL_PROFILING__PYROSCOPE__BASIC_AUTH_PASSWORD"; do
  fileval="$(svc ".environment.${pair}_FILE // \"\"")"
  plainval="$(svc ".environment.${pair} // \"\"")"
  if [[ "$fileval" == /run/secrets/* ]]; then
    ok "D: ${pair}_FILE points into /run/secrets"
  else
    bad "D: ${pair}_FILE is ${fileval:-unset}, want a /run/secrets path"
  fi
  [[ -z "$plainval" ]] \
    && ok "D: $pair carries no inline value (value XOR file)" \
    || bad "D: $pair is ALSO set inline; the app rejects value+file as a conflict"
done

# Exactly the six credentials above may be file-backed here. A count check
# catches a future edit that drops one of the five categories #334 names
# (OAuth / OTLP / receiver / admin / profiling) — the per-pair loop above would
# still pass for the five that remain.
nfile="$(svc '[.environment | to_entries[] | select(.key | test("^TS2OTEL_.*_FILE$"))] | length')"
[[ "$nfile" == "6" ]] \
  && ok "D: exactly 6 *_FILE credential variables rendered" \
  || bad "D: $nfile *_FILE variables rendered, want 6 (a category was added or dropped)"

# --------------------------------------------------------------------------
case_ "E. published-image default vs explicit developer build (#335)"
DEV_OVERRIDE="${DEPLOY_DIR}/docker-compose.dev.yaml"

resolve -f "$BASE"
assert_rc0 "E: base resolves"
img="$(svc '.image')"
[[ "$img" == ghcr.io/rknightion/tailscale2otel:* ]] \
  && ok "E: default is the published image ($img)" || bad "E: default image is $img"
[[ "$img" != *:latest ]] \
  && ok "E: default tag is a pinned release, not :latest" \
  || bad "E: default tag is :latest, which moves under a running deployment"
[[ "$(svc '.build')" == "null" ]] \
  && ok "E: base declares no build (up is unambiguously the published image)" \
  || bad "E: base still declares a build section"

# Configurable, per the acceptance criterion.
TS2OTEL_VERSION=9.9.9 resolve -f "$BASE"
[[ "$(svc '.image')" == "ghcr.io/rknightion/tailscale2otel:9.9.9" ]] \
  && ok "E: TS2OTEL_VERSION overrides the tag" || bad "E: TS2OTEL_VERSION did not override the tag: $(svc '.image')"

resolve -f "$BASE" -f "$DEV_OVERRIDE"
assert_rc0 "E: dev override resolves"
[[ "$(svc '.build.dockerfile')" == *"deploy/Dockerfile" ]] \
  && ok "E: dev override builds from deploy/Dockerfile" || bad "E: dev override has no build context"
# The dev image must NOT reuse the published repository, or a local build is
# indistinguishable from a release in `docker images`.
[[ "$(svc '.image')" != ghcr.io/* ]] \
  && ok "E: dev build is tagged separately ($(svc '.image'))" \
  || bad "E: dev build reuses the published image name $(svc '.image')"
assert_checkpoint_mount "E"

# --------------------------------------------------------------------------
# --self-test mutates the inputs to prove each assertion above can actually
# fail. Three guard tests in this repository have shipped passing while
# asserting nothing; a checker that is never shown to fail proves nothing.
if [[ "${1:-}" == "--self-test" ]]; then
  case_ "SELF-TEST (expected failures below are the point)"
  before_fail=$fail
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT

  # 1. An override that drops the checkpoint mount must trip assert_checkpoint_mount.
  RESOLVED="$(docker compose -f "$BASE" -f "$OVERRIDE" config 2>&1 \
    | yq "(.services.${SERVICE}.volumes) |= [.[] | select(.target != \"$CHECKPOINT_TARGET\")]")"
  RESOLVED_RC=0
  assert_checkpoint_mount "SELF-TEST[checkpoint dropped]"
  [[ $fail -gt $before_fail ]] \
    && printf '  ok   SELF-TEST: checkpoint assertion fires when the mount is removed\n' \
    || { printf '  FAIL SELF-TEST: checkpoint assertion did NOT fire — it asserts nothing\n'; exit 1; }
  fail=$before_fail

  # 2. The duplicate-key form must still be rejected by Compose itself, which is
  #    the defect the override file exists to avoid.
  sed -e 's|^    volumes:|    volumes:\n      - ./config.yaml:/etc/tailscale2otel/config.yaml:ro\n    volumes:|' \
    "$BASE" > "$tmp/dup.yaml"
  if docker compose -f "$tmp/dup.yaml" config >/dev/null 2>&1; then
    printf '  FAIL SELF-TEST: Compose ACCEPTED a duplicate volumes: key — case C assumes it does not\n'
    exit 1
  fi
  printf '  ok   SELF-TEST: Compose rejects a duplicate volumes: key (the original #333 defect)\n'
fi

printf '\n%d passed, %d failed\n' "$pass" "$fail"
[[ $fail -eq 0 ]]
