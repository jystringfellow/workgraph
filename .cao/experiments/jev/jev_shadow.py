#!/usr/bin/env python3

"""Run non-authoritative Jev shadow judgments for a Workgraph change.

This script NEVER controls workflow execution. Its output is telemetry for
evaluating whether Jev might be useful in future workflow decisions.

Input is JSON on stdin. Output is JSON on stdout.
"""

from __future__ import annotations

import json
import os
import sys
from typing import Any

from typesafe_sdk import Choice, Noul, TypeSafeClient


def fail(message: str) -> None:
    print(
        json.dumps(
            {
                "schema_version": 1,
                "mode": "shadow",
                "status": "error",
                "error": message,
            },
            indent=2,
            sort_keys=True,
        )
    )
    raise SystemExit(1)


def read_state() -> dict[str, Any]:
    try:
        state = json.load(sys.stdin)
    except json.JSONDecodeError as exc:
        fail(f"invalid JSON input: {exc}")

    if not isinstance(state, dict):
        fail("input must be a JSON object")

    return state


def main() -> None:
    if not os.environ.get("TYPESAFE_API_KEY"):
        fail("TYPESAFE_API_KEY is not set")

    state = read_state()

    questions = {
        "change_risk": Choice(
            instructions=(
                "Classify the engineering risk of this change from the supplied "
                "metadata. Consider the likely consequence of an implementation "
                "defect, not merely the size of the change."
            ),
            criteria={
                "low": (
                    "A localized change where a defect would likely have limited "
                    "impact and be straightforward to detect or recover from."
                ),
                "medium": (
                    "A change with meaningful behavioral, compatibility, or "
                    "maintenance consequences, but limited systemic risk."
                ),
                "high": (
                    "A change where a defect could materially affect privacy, "
                    "data integrity, compatibility, persisted state, recovery, "
                    "or important behavior across the system."
                ),
            },
        ),
        "review_focus": Choice(
            instructions=(
                "Choose the single most valuable primary focus for an independent "
                "substantive review of this change based only on the supplied "
                "metadata."
            ),
            criteria={
                "privacy": (
                    "Storage, disclosure, collection, retention, or handling of "
                    "user or agent information is the primary concern."
                ),
                "compatibility": (
                    "Backward compatibility, interoperability, external contracts, "
                    "or behavior relied upon by existing users is the primary concern."
                ),
                "data_integrity": (
                    "Correctness or durability of persisted or structured state is "
                    "the primary concern."
                ),
                "cli_behavior": (
                    "Command behavior, arguments, output, errors, or user-facing CLI "
                    "semantics are the primary concern."
                ),
                "architecture": (
                    "Component boundaries, responsibilities, coupling, or long-term "
                    "design coherence are the primary concern."
                ),
                "general": (
                    "No other category clearly dominates; a broad correctness review "
                    "is the most useful focus."
                ),
            },
        ),
        "needs_deep_review": Noul(
            instructions=(
                "Based only on the supplied metadata, does this change warrant "
                "deep independent substantive review beyond deterministic tests "
                "and static checks?"
            ),
        ),
    }

    try:
        with TypeSafeClient() as client:
            response = client.system_one(
                state=state,
                questions=questions,
            )
    except Exception as exc:
        fail(f"Jev request failed: {type(exc).__name__}")

    try:
        risk = response.answers["change_risk"]
        focus = response.answers["review_focus"]
        deep_review = response.answers["needs_deep_review"]
        judgments = {
            "change_risk": {
                "choice": risk.choice,
                "confidence": risk.confidence,
                "probabilities": risk.probabilities,
            },
            "review_focus": {
                "choice": focus.choice,
                "confidence": focus.confidence,
                "probabilities": focus.probabilities,
            },
            "needs_deep_review": {
                "probability": deep_review.noul,
            },
        }
    except (AttributeError, KeyError, TypeError) as exc:
        fail(f"Jev response incomplete: {type(exc).__name__}")

    result = {
        "schema_version": 1,
        "mode": "shadow",
        "status": "ok",
        "authoritative": False,
        "model": getattr(response, "model", None),
        "judgments": judgments,
    }

    print(json.dumps(result, indent=2, sort_keys=True))


if __name__ == "__main__":
    main()