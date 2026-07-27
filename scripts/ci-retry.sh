#!/usr/bin/env bash
# Bounded retry for a KNOWN-TRANSIENT network operation in CI (#439).
#
# Usage: scripts/ci-retry.sh <command> [args...]
#
# Runs the command up to CI_RETRY_ATTEMPTS times (default 3) with exponential
# backoff starting at CI_RETRY_DELAY seconds (default 5), and exits with the
# LAST attempt's exit status.
#
# Two things this deliberately is not:
#
#   * It is not a general "make CI green" wrapper. Wrap only operations whose
#     failure mode is genuinely transient — an artifact download, a registry
#     fetch. Wrapping a TEST or a DRIFT GATE would convert a real, reproducible
#     failure into a flaky one that passes on the second try, which is strictly
#     worse than failing: the signal disappears but the defect does not.
#
#   * It is not fail-open. When every attempt fails the script exits non-zero,
#     so a wrapped gate still blocks. This is asserted by a negative test
#     (TestCIRetryIsFailClosed) rather than assumed, because a retry helper that
#     silently succeeds after exhausting its attempts is invisible until the day
#     it matters.
#
# Each retry is announced on stderr so a run that only passed on attempt 3 says
# so in the log, rather than looking indistinguishable from a clean first pass.

set -uo pipefail

attempts="${CI_RETRY_ATTEMPTS:-3}"
delay="${CI_RETRY_DELAY:-5}"

if [ "$#" -eq 0 ]; then
	echo "ci-retry: no command given" >&2
	exit 2
fi

if ! [ "$attempts" -ge 1 ] 2>/dev/null; then
	echo "ci-retry: CI_RETRY_ATTEMPTS must be a positive integer, got '${attempts}'" >&2
	exit 2
fi

n=1
while true; do
	# Run and capture in two statements, NOT as `if "$@"; then ... fi` followed
	# by `status=$?`. In that shape `$?` reports the status of the `if`
	# construct, which is 0 whenever the else-branch runs — so the helper
	# reported success no matter how the command failed. It read correctly and
	# was fail-OPEN; TestCIRetryIsFailClosed is what caught it.
	"$@"
	status=$?
	if [ "$status" -eq 0 ]; then
		if [ "$n" -gt 1 ]; then
			echo "ci-retry: succeeded on attempt ${n}/${attempts}: $*" >&2
		fi
		exit 0
	fi
	if [ "$n" -ge "$attempts" ]; then
		echo "ci-retry: giving up after ${attempts} attempt(s), exit ${status}: $*" >&2
		exit "$status"
	fi
	echo "ci-retry: attempt ${n}/${attempts} failed (exit ${status}), retrying in ${delay}s: $*" >&2
	sleep "$delay"
	n=$((n + 1))
	delay=$((delay * 2))
done
