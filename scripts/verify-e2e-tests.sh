#!/usr/bin/env bash
# verify-e2e-tests.sh — Run E2E tests twice per agent to verify a fix.
#
# Usage: verify-e2e-tests.sh <attempt> <output_file>
#   attempt     - attempt number (for log messages)
#   output_file - file to append test output to
#
# Required env vars:
#   FAILED_AGENTS - comma-separated list of agents to verify

set -euo pipefail

attempt="${1:?usage: verify-e2e-tests.sh <attempt> <output_file>}"
output_file="${2:?usage: verify-e2e-tests.sh <attempt> <output_file>}"

failed=""
for agent in $(echo "$FAILED_AGENTS" | tr ',' ' ' | xargs); do
  limit=""
  test_filter=""
  case "$agent" in
    gemini-cli) limit="6" ;;
    factoryai-droid) limit="1" ;;
    roger-roger) test_filter="TestExternalAgent" ;;
  esac
  export E2E_CONCURRENT_TEST_LIMIT="$limit"

  for run in 1 2; do
    echo "=== $agent: verification run $run/2 (attempt $attempt) ==="
    if ! mise run test:e2e --agent "$agent" ${test_filter:+"$test_filter"} 2>&1 | tee -a "$output_file"; then
      failed="$failed $agent(run$run)"
      echo "=== $agent: run $run FAILED ==="
      break
    fi
    echo "=== $agent: run $run passed ==="
  done
done

if [ -n "$failed" ]; then
  echo "FAILED:$failed" >> "$output_file"
  exit 1
fi
