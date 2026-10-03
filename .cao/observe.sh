#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: .cao/observe.sh <run-id> [--interval seconds] [--once]

Poll a CAO workflow status using compact operational metadata only.
EOF
}

if [[ $# -lt 1 ]]; then
  usage >&2
  exit 2
fi

run_id="$1"
shift
interval=5
once=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --interval)
      [[ $# -ge 2 ]] || { usage >&2; exit 2; }
      interval="$2"
      shift 2
      ;;
    --once)
      once=true
      shift
      ;;
    *)
      usage >&2
      exit 2
      ;;
  esac
done

[[ "$interval" =~ ^[1-9][0-9]*$ ]] || {
  echo "interval must be a positive integer" >&2
  exit 2
}

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cao_db="${CAO_HOME_DIR:-$HOME/.aws/cli-agent-orchestrator}/db/cli-agent-orchestrator.db"

git_counts() {
  git -C "$repo_root" status --porcelain=v1 |
    awk '
      BEGIN { staged = 0; unstaged = 0; untracked = 0 }
      /^\?\?/ { untracked++; next }
      {
        if (substr($0, 1, 1) != " ") staged++
        if (substr($0, 2, 1) != " ") unstaged++
      }
      END {
        printf "staged=%d unstaged=%d untracked=%d", staged, unstaged, untracked
      }
    '
}

terminal_id_for_status() {
  python3 -c '
import json
import sys

status = json.load(sys.stdin)
current_step_id = status.get("current_step_id")
for step in status.get("steps") or []:
    if step.get("id") == current_step_id and step.get("terminal_id"):
        print(step["terminal_id"])
        raise SystemExit
for step in status.get("steps") or []:
    if step.get("state") in {"running", "pending"} and step.get("terminal_id"):
        print(step["terminal_id"])
        raise SystemExit
' 2>/dev/null
}

active_terminal() {
  local terminal_id="$1"
  local record tmux_session tmux_window provider title

  [[ -n "$terminal_id" && -f "$cao_db" ]] || return 0

  record="$(
    python3 - "$cao_db" "$terminal_id" <<'PY'
import sqlite3
import sys

with sqlite3.connect(sys.argv[1]) as connection:
    row = connection.execute(
        "SELECT tmux_session, tmux_window, provider FROM terminals WHERE id = ?",
        (sys.argv[2],),
    ).fetchone()

if row:
    print("\t".join("" if value is None else str(value) for value in row))
PY
  )"
  [[ -n "$record" ]] || return 0

  IFS=$'\t' read -r tmux_session tmux_window provider <<<"$record"
  tmux has-session -t "$tmux_session" 2>/dev/null || return 0
  title="$(tmux display-message -p -t "${tmux_session}:${tmux_window}" '#{pane_title}' 2>/dev/null || true)"
  printf 'terminal=%s provider=%s title=%s\n' "$terminal_id" "$provider" "${title:-unavailable}"
}

render_status() {
  local status_json="$1"
  local worktree_counts="$2"
  local terminal_summary="$3"

  python3 - "$worktree_counts" "$terminal_summary" 3<<<"$status_json" <<'PY'
import json
import os
import re
import sys

status = json.load(os.fdopen(3))


def label(step_id):
    exact = {
        "architect": "Architect",
        "developer-initial": "Developer (initial implementation)",
    }
    if step_id in exact:
        return exact[step_id]
    patterns = (
        (r"^developer-fix-verify-(\d+)$", "Developer (verification fix {0})"),
        (r"^reviewer-(\d+)$", "Adversarial Reviewer (review {0})"),
        (r"^developer-fix-review-(\d+)$", "Developer (review fix {0})"),
        (r"^developer-fix-rev-verify-(\d+)-(\d+)$", "Developer (review verification fix {0}.{1})"),
        (r"^release-gate-(\d+)$", "Release Gate (assessment {0})"),
        (r"^developer-fix-gate-(\d+)$", "Developer (gate fix {0})"),
    )
    for pattern, template in patterns:
        match = re.match(pattern, step_id)
        if match:
            return template.format(*match.groups())
    return f"Unknown/interstitial ({step_id})"


steps = status.get("steps") or []
completed = [label(step["id"]) for step in steps if step.get("state") == "completed"]
current_id = status.get("current_step_id")
current = next((step for step in steps if step.get("id") == current_id), None)
review_assessments = [step for step in steps if re.match(r"^reviewer-\d+$", step.get("id", ""))]
review_fixes = [step for step in steps if re.match(r"^developer-fix-review-\d+$", step.get("id", ""))]
gate_assessments = [step for step in steps if re.match(r"^release-gate-\d+$", step.get("id", ""))]
gate_fixes = [step for step in steps if re.match(r"^developer-fix-gate-\d+$", step.get("id", ""))]

print(f"Run: {status.get('run_id', 'unknown')}  State: {status.get('state', 'unknown')}")
print("Completed: " + (", ".join(completed) if completed else "none reported"))
if current:
    print(f"Current: {label(current_id)} [{current.get('state', 'unknown')}]")
elif current_id:
    print(f"Current: Unknown/interstitial ({current_id})")
else:
    print("Current: none reported by CAO")
print(
    "Iterations: "
    f"review assessments={len(review_assessments)} fixes={len(review_fixes)}; "
    f"gate assessments={len(gate_assessments)} fixes={len(gate_fixes)}"
)
print("Working tree: " + sys.argv[1])
print("Active CAO terminal: " + (sys.argv[2] or "unavailable"))
PY
}

while true; do
  status_json="$(cao workflow status "$run_id" --json)"
  terminal_id="$(printf '%s' "$status_json" | terminal_id_for_status)"
  terminal_summary="$(active_terminal "$terminal_id")"

  if [[ -t 1 ]]; then
    clear
  fi
  render_status "$status_json" "$(git_counts)" "$terminal_summary"

  state="$(
    printf '%s' "$status_json" |
      python3 -c 'import json, sys; print(json.load(sys.stdin).get("state", ""))'
  )"
  if [[ "$once" == true || "$state" == "completed" || "$state" == "failed" || "$state" == "cancelled" ]]; then
    exit 0
  fi
  sleep "$interval"
done
