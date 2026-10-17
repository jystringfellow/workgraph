#!/usr/bin/env python3
"""Check/apply a narrow installed-CAO Copilot update-banner compatibility fix."""

import argparse
import importlib.metadata
import os
from pathlib import Path

OLD_PATTERN = r"^←.*[·•]\s*/ commands\b"
NEW_PATTERN = r"^(?:v\d+\.\d+\.\d+ downloaded\s*[·•]\s*next launch or /restart\s*[·•]\s*)?←.*[·•]\s*/ commands\b"
FOOTER = (
    " v1.0.94 downloaded · next launch or /restart · ← open sidebar · Autopilot · "
    "Allow All · / commands · tab next tab    wg-adversarial-reviewer · Claude Haiku 5.5"
)


def patch_source(source):
    old = 'COPILOT_HINT_BAR_PATTERN = r"' + OLD_PATTERN + '"'
    new = 'COPILOT_HINT_BAR_PATTERN = r"' + NEW_PATTERN + '"'
    if source.count(new) == 1 and old not in source:
        return source
    if source.count(old) != 1 or new in source:
        raise ValueError("Installed CAO source differs from the supported patch; inspect rather than guessing")
    return source.replace(old, new, 1)


def check_runtime():
    from cli_agent_orchestrator.providers import copilot_cli
    provider = copilot_cli.CopilotCliProvider
    original = copilot_cli.COPILOT_HINT_BAR_PATTERN
    before = provider._has_idle_prompt_near_end(["❯", FOOTER])
    copilot_cli.COPILOT_HINT_BAR_PATTERN = NEW_PATTERN
    try:
        if not provider._has_idle_prompt_near_end(["❯", FOOTER]):
            raise ValueError("Update-banner footer remains unrecognized")
        if not provider._has_idle_prompt_near_end(["❯", "← open sidebar · Autopilot · / commands"]):
            raise ValueError("Existing footer behavior changed")
        for row in ("Error: model unavailable", "Waiting for login", "Do you trust the files in this folder?",
                    FOOTER + " · esc to cancel", "Unexpected text · / commands"):
            if provider._has_idle_prompt_near_end(["❯", row]):
                raise ValueError("Busy, authentication, or unknown output was incorrectly classified as idle")
    finally:
        copilot_cli.COPILOT_HINT_BAR_PATTERN = original
    return before


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--apply", action="store_true")
    args = parser.parse_args()
    distribution = importlib.metadata.distribution("cli-agent-orchestrator")
    target = Path(distribution.locate_file("cli_agent_orchestrator/providers/copilot_cli.py"))
    original = target.read_text()
    updated = patch_source(original)
    before = check_runtime()
    print(f"Installed CAO recognizes update-banner footer: {before}")
    print("Patched readiness checks: pass; no model calls")
    if not args.apply:
        print(f"Check only; proposed target: {target}")
        return
    if updated == original:
        print("Compatibility fix is already installed")
        return
    if target.read_text() != original:
        raise ValueError("CAO source changed during verification; refusing to overwrite it")
    backup = target.with_suffix(".py.workgraph-original")
    with backup.open("x", encoding="utf-8") as stream:
        os.chmod(backup, 0o600)
        stream.write(original)
    temporary = target.with_suffix(".py.workgraph-tmp")
    with temporary.open("x", encoding="utf-8") as stream:
        os.chmod(temporary, target.stat().st_mode & 0o777)
        stream.write(updated)
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(temporary, target)
    print(f"Applied one-line fix: {target}")
    print(f"Original preserved: {backup}")
    print("Restart the CAO server before testing; the running server retains its imported provider")


if __name__ == "__main__":
    main()
