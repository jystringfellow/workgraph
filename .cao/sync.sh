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

profile_provider() {
  awk '
    { sub(/\r$/, "") }
    NR == 1 { if ($0 != "---") exit; next }
    $0 == "---" { exit }
    /^provider:/ { sub(/^provider:[ \t]*/, ""); sub(/[ \t]+$/, ""); gsub(/["\047]/, ""); print; exit }
  ' "$1"
}

echo "=== Validating with CAO ==="
for profile in "${profiles[@]}"; do
  cao profile validate "$profile"
done

for profile in "${profiles[@]}"; do
  stored="$AGENT_STORE/$(basename "$profile")"
  if [[ -e "$stored" ]] && ! cmp -s "$profile" "$stored"; then
    echo "Refusing to overwrite differing profile: $stored" >&2
    exit 1
  fi
done

echo "=== Syncing Workgraph CAO Profiles ==="
mkdir -p "$AGENT_STORE"
for profile in "${profiles[@]}"; do
  cp "$profile" "$AGENT_STORE/"
  echo "  Copied $(basename "$profile") -> $AGENT_STORE/"
done

echo "=== Installing Selected Copilot Custom Agents ==="
for profile in "${profiles[@]}"; do
  if [[ "$(profile_provider "$profile")" == "copilot_cli" ]]; then
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
