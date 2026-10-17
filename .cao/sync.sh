#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORKGRAPH_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

CAO_HOME="${CAO_HOME_DIR:-$HOME/.aws/cli-agent-orchestrator}"
AGENT_STORE="$CAO_HOME/agent-store"
WORKFLOW_DIR="$CAO_HOME/workflows"

mkdir -p "$AGENT_STORE" "$WORKFLOW_DIR"

echo "=== Syncing Workgraph CAO Profiles ==="
for profile in "$SCRIPT_DIR"/profiles/wg-*.md; do
  if [[ -f "$profile" ]]; then
    cp "$profile" "$AGENT_STORE/"
    echo "  Copied $(basename "$profile") -> $AGENT_STORE/"
  fi
done

echo "=== Syncing Workgraph Workflows ==="
for wf in "$SCRIPT_DIR"/workflows/*.py; do
  if [[ -f "$wf" ]]; then
    cp "$wf" "$WORKFLOW_DIR/"
    echo "  Copied $(basename "$wf") -> $WORKFLOW_DIR/"
  fi
done

echo "=== Validating with CAO ==="
for profile in "$SCRIPT_DIR"/profiles/wg-*.md; do
  if [[ -f "$profile" ]]; then
    cao profile validate "$AGENT_STORE/$(basename "$profile")"
  fi
done

for wf in "$SCRIPT_DIR"/workflows/*.py; do
  if [[ -f "$wf" ]]; then
    cao workflow validate "$WORKFLOW_DIR/$(basename "$wf")"
  fi
done

echo "=== Installing Copilot Smoke Custom Agents ==="
for profile in "$SCRIPT_DIR"/profiles/wg-*-smoke.md; do
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
