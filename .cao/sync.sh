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
  "${CAO_PYTHON:-python3}" - "$1" <<'PY'
import sys
import yaml

path = sys.argv[1]
lines = open(path, encoding="utf-8").read().splitlines()
if not lines or lines[0].strip() != "---" or "---" not in (line.strip() for line in lines[1:]):
    sys.exit(f"Missing YAML front matter: {path}")
end = next(i for i, line in enumerate(lines[1:], 1) if line.strip() == "---")
try:
    header = yaml.safe_load("\n".join(lines[1:end]))
except yaml.YAMLError as exc:
    sys.exit(f"Invalid YAML front matter in {path}: {exc}")
if not isinstance(header, dict):
    sys.exit(f"Front matter is not a mapping: {path}")
print(header.get("provider") or "")
PY
}

echo "=== Validating with CAO ==="
providers=()
for profile in "${profiles[@]}"; do
  cao profile validate "$profile"
  provider="$(profile_provider "$profile")"
  providers+=("$provider")
done

for profile in "${profiles[@]}"; do
  stored="$AGENT_STORE/$(basename "$profile")"
  if [[ -e "$stored" ]] && ! cmp -s "$profile" "$stored"; then
    echo "Refusing to overwrite differing profile: $stored" >&2
    exit 1
  fi
done

staged=""
trap 'if [[ -n "$staged" ]]; then rm -f "$staged"; fi' EXIT

echo "=== Syncing Workgraph CAO Profiles ==="
mkdir -p "$AGENT_STORE"
for profile in "${profiles[@]}"; do
  stored="$AGENT_STORE/$(basename "$profile")"
  if [[ -e "$stored" ]]; then
    echo "  Unchanged $(basename "$profile")"
    continue
  fi
  staged="$(mktemp "$AGENT_STORE/.$(basename "$profile").XXXXXX")"
  cp "$profile" "$staged"
  mv "$staged" "$stored"
  staged=""
  echo "  Copied $(basename "$profile") -> $AGENT_STORE/"
done

echo "=== Installing Selected Copilot Custom Agents ==="
for index in "${!profiles[@]}"; do
  profile="${profiles[$index]}"
  if [[ "${providers[$index]}" == "copilot_cli" ]]; then
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
