#!/usr/bin/env bash
set -uo pipefail

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

vet_log="$tmpdir/vet.log"
test_log="$tmpdir/test.log"

vet_status="pass"
test_status="pass"

# Isolate verification from any active wrapped workgraph AI session so
# `workgraph ai run` tests don't falsely fail the nested-session guard.
unset WORKGRAPH_AI_SESSION_ID WORKGRAPH_AI_HOME WORKGRAPH_AI_DATABASE

if ! go vet ./... >"$vet_log" 2>&1; then
  vet_status="fail"
fi

if ! go test ./... >"$test_log" 2>&1; then
  test_status="fail"
fi

printf '=== WORKGRAPH VERIFICATION ===\n'
printf 'go_vet: %s\n' "$vet_status"
printf 'go_test: %s\n' "$test_status"

if [[ "$vet_status" == "fail" ]]; then
  printf '\n=== GO VET FAILURE ===\n'
  cat "$vet_log"
fi

if [[ "$test_status" == "fail" ]]; then
  printf '\n=== GO TEST FAILURE ===\n'
  cat "$test_log"
fi

if [[ "$vet_status" == "fail" || "$test_status" == "fail" ]]; then
  exit 1
fi

exit 0
