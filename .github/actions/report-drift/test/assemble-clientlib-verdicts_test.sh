#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)
assembler="${repo_root}/.github/actions/report-drift/scripts/assemble-clientlib-verdicts.sh"
tmpdir=$(mktemp -d)
trap 'rm -rf "${tmpdir}"' EXIT

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

assert_file_contains() {
  local file=$1
  local want=$2
  grep -Fqx "$want" "$file" || fail "${file} does not contain ${want}"
}

run_assembler() {
  local case_dir=$1
  local legs_result=$2
  GITHUB_OUTPUT="${case_dir}/output" bash "$assembler" \
    --verdict-dir "${case_dir}/verdicts" \
    --legs-result "$legs_result" \
    --report-path "${case_dir}/report.md" \
    --run-url "https://example.invalid/run"
}

write_verdict() {
  local case_dir=$1
  local ref=$2
  local verdict=$3
  mkdir -p "${case_dir}/verdicts"
  {
    echo "verdict=${verdict}"
    echo "### ${ref}"
  } > "${case_dir}/verdicts/${ref}.md"
}

clean_case="${tmpdir}/clean"
write_verdict "$clean_case" main pass
write_verdict "$clean_case" latest pass
run_assembler "$clean_case" success
assert_file_contains "${clean_case}/output" "outcome=green"
assert_file_contains "${clean_case}/output" "failed=0"
test ! -e "${clean_case}/report.md" || fail "clean run wrote a failure report"

failure_case="${tmpdir}/failure"
write_verdict "$failure_case" main pass
write_verdict "$failure_case" latest fail
run_assembler "$failure_case" success
assert_file_contains "${failure_case}/output" "outcome=failure"
assert_file_contains "${failure_case}/output" "failed=1"
grep -Fq "1 of 2 matrix legs failed" "${failure_case}/report.md" || fail "failure report lost count"
grep -Fq "### latest" "${failure_case}/report.md" || fail "failure report lost failing verdict"

missing_case="${tmpdir}/missing"
write_verdict "$missing_case" main pass
if run_assembler "$missing_case" success; then
  fail "missing verdict was accepted as green"
fi
assert_file_contains "${missing_case}/output" "outcome=inconclusive"
assert_file_contains "${missing_case}/output" "failed=inconclusive"
test ! -e "${missing_case}/report.md" || fail "missing verdict wrote a drift report"

unchanged_case="${tmpdir}/unchanged"
write_verdict "$unchanged_case" main pass
write_verdict "$unchanged_case" latest inconclusive
if run_assembler "$unchanged_case" success; then
  fail "failed baseline verification was reported as client-lib drift"
fi
assert_file_contains "${unchanged_case}/output" "outcome=inconclusive"
test ! -e "${unchanged_case}/report.md" || fail "unchanged candidate wrote a drift report"

echo "assemble-clientlib-verdicts tests passed"
