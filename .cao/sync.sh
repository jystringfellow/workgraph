#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [[ $# -ne 0 ]]; then
  echo "Usage: $0" >&2
  exit 2
fi

profiles=("$SCRIPT_DIR"/profiles/wg-pod-*.md)

CAO_HOME="${CAO_HOME_DIR:-$HOME/.aws/cli-agent-orchestrator}"
AGENT_STORE="$CAO_HOME/agent-store"

mkdir -p "$AGENT_STORE"

echo "=== Syncing Workgraph CAO Profiles ==="
for profile in "${profiles[@]}"; do
  if [[ -f "$profile" ]]; then
    cp "$profile" "$AGENT_STORE/"
    echo "  Copied $(basename "$profile") -> $AGENT_STORE/"
  fi
done

echo "=== Validating with CAO ==="
for profile in "${profiles[@]}"; do
  if [[ -f "$profile" ]]; then
    cao profile validate "$AGENT_STORE/$(basename "$profile")"
  fi
done

echo "=== Installing Selected Copilot Custom Agents ==="
for profile in "${profiles[@]}"; do
  if [[ -f "$profile" ]] && grep -q '^provider: copilot_cli$' "$profile"; then
    install_result="$(cao install "$AGENT_STORE/$(basename "$profile")" --provider copilot_cli)"
    printf '%s\n' "$install_result"
    agent_name="$(basename "$profile" .md)"
    if ! grep -Fq "Agent '$agent_name' installed successfully" <<< "$install_result"; then
      echo "CAO did not confirm installation of $agent_name" >&2
      exit 1
    fi
  fi
done

echo "=== Sync and Validation Complete ==="
