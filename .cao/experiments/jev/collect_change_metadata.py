#!/usr/bin/env python3

"""Collect non-sensitive metadata about the current Workgraph change.

Produces JSON suitable for use as input to jev_shadow.py.

This script collects repository facts only. It does not read or emit source
contents, diffs, prompts, Workgraph memory, or environment variables.

Insertion and deletion counts describe tracked changes only. Untracked files
are counted and named, but their contents are not read.
"""

from __future__ import annotations

import json
import subprocess
import sys
from pathlib import Path


def git(*args: str) -> str:
    result = subprocess.run(
        ["git", *args],
        check=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    return result.stdout


def repository_root() -> Path:
    try:
        return Path(git("rev-parse", "--show-toplevel").strip())
    except subprocess.CalledProcessError as exc:
        print(
            f"unable to determine repository root: {exc.stderr.strip()}",
            file=sys.stderr,
        )
        raise SystemExit(1)


def tracked_changed_paths() -> list[str]:
    """Return tracked files changed relative to HEAD."""

    return sorted(
        path
        for path in git("diff", "--name-only", "HEAD").splitlines()
        if path
    )


def untracked_paths() -> list[str]:
    """Return untracked, non-ignored files without reading their contents."""

    return sorted(
        path
        for path in git(
            "ls-files",
            "--others",
            "--exclude-standard",
        ).splitlines()
        if path
    )


def tracked_diff_stats() -> tuple[int, int]:
    """Return insertion/deletion counts for tracked changes relative to HEAD."""

    insertions = 0
    deletions = 0

    for line in git("diff", "--numstat", "HEAD").splitlines():
        parts = line.split("\t", 2)
        if len(parts) < 2:
            continue

        added, removed = parts[0], parts[1]

        # Git represents binary-file counts as "-".
        if added.isdigit():
            insertions += int(added)

        if removed.isdigit():
            deletions += int(removed)

    return insertions, deletions


def main() -> None:
    # Validate that we're operating inside a Git repository.
    repository_root()

    try:
        tracked = tracked_changed_paths()
        untracked = untracked_paths()
        tracked_insertions, tracked_deletions = tracked_diff_stats()
    except subprocess.CalledProcessError as exc:
        print(
            f"unable to collect git metadata: {exc.stderr.strip()}",
            file=sys.stderr,
        )
        raise SystemExit(1)

    paths = sorted(set(tracked + untracked))

    tests_changed = any(
        path.endswith("_test.go")
        or path.startswith("test/")
        or path.startswith("tests/")
        for path in paths
    )

    specs_changed = any(
        path.startswith("specs/")
        for path in paths
    )

    result = {
        "change": {
            "files_changed": len(paths),
            "tracked_files_changed": len(tracked),
            "untracked_files": len(untracked),
            "tracked_insertions": tracked_insertions,
            "tracked_deletions": tracked_deletions,
            "paths": paths,
        },
        "signals": {
            "tests_changed": tests_changed,
            "specs_changed": specs_changed,
        },
    }

    print(json.dumps(result, indent=2, sort_keys=True))


if __name__ == "__main__":
    main()