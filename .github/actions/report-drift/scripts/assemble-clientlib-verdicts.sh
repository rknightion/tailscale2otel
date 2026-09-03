#!/usr/bin/env bash
# Assemble the client-library matrix's explicit pass/fail verdicts into one report.
set -euo pipefail

verdict_dir=""
legs_result=""
report_path=""
run_url=""

usage() {
  echo "usage: $0 --verdict-dir DIR --legs-result RESULT --report-path PATH --run-url URL" >&2
  exit 2
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --verdict-dir)
      verdict_dir=${2:-}
      shift 2
      ;;
    --legs-result)
      legs_result=${2:-}
      shift 2
      ;;
    --report-path)
      report_path=${2:-}
      shift 2
      ;;
    --run-url)
      run_url=${2:-}
      shift 2
      ;;
    *)
      usage
      ;;
  esac
done

if [ -z "$verdict_dir" ] || [ -z "$legs_result" ] || [ -z "$report_path" ] || [ -z "$run_url" ]; then
  usage
fi

if [ -z "${GITHUB_OUTPUT:-}" ]; then
  echo "GITHUB_OUTPUT must name the step output file" >&2
  exit 2
fi

failures=()
inconclusive=()
for ref in main latest; do
  verdict_file="${verdict_dir}/${ref}.md"
  if [ ! -r "$verdict_file" ]; then
    inconclusive+=("$ref (missing verdict)")
    continue
  fi

  case "$(head -n 1 "$verdict_file")" in
    verdict=pass)
      ;;
    verdict=fail)
      failures+=("$verdict_file")
      ;;
    verdict=inconclusive)
      inconclusive+=("$ref")
      ;;
    *)
      inconclusive+=("$ref (invalid verdict)")
      ;;
  esac
done

if [ "$legs_result" != "success" ] || [ "${#inconclusive[@]}" -ne 0 ]; then
  echo "client-lib matrix is inconclusive: legs result '${legs_result}', inconclusive verdicts: ${inconclusive[*]:-none}" >&2
  echo "outcome=inconclusive" >> "$GITHUB_OUTPUT"
  echo "failed=inconclusive" >> "$GITHUB_OUTPUT"
  exit 1
fi

if [ "${#failures[@]}" -eq 0 ]; then
  echo "outcome=green" >> "$GITHUB_OUTPUT"
  echo "failed=0" >> "$GITHUB_OUTPUT"
  echo "every client-lib matrix leg passed"
  exit 0
fi

{
  echo "## tailscale-client-go/v2 breaks our build"
  echo
  echo "${#failures[@]} of 2 matrix legs failed in [this run](${run_url})."
  echo
  for verdict_file in "${failures[@]}"; do
    sed '1d' "$verdict_file"
    echo
  done
} > "$report_path"

echo "outcome=failure" >> "$GITHUB_OUTPUT"
echo "failed=${#failures[@]}" >> "$GITHUB_OUTPUT"
