"""Workgraph Feature Engineering Pod — CAO Workflow Script.

Implements the sequential multi-agent engineering workflow defined in
.cao/RIG.md and .cao/pods/feature-engineering.md:

    Architect (wg-architect, Codex / GPT-6 Astra)
      -> Developer (wg-developer, Codex / GPT-5.6 Sol)
      -> Deterministic Verification (verify.sh: go vet, go test)
         -> on failure: Developer fixes -> verify again
      -> Jev Shadow Evaluation (collect_change_metadata.py + jev_shadow.py)
         -> observational telemetry only; NEVER affects control flow
      -> Adversarial Reviewer (wg-adversarial-reviewer, antigravity_cli)
         -> findings: Developer fixes -> verify -> review again
      -> Release Gate (wg-release-gate, Codex / GPT-5.6 Sol)
         -> blockers: Developer fixes -> verify -> review -> gate again
      -> Done
"""

from __future__ import annotations

import json
import os
import re
import subprocess
import sys
from pathlib import Path
from typing import Any, Dict, Optional, Tuple

from cao_workflow import emit_output, get_inputs, step

# Module-level INPUTS declaration for CAO static loader.
# Field values must be AST literals.
INPUTS = {
    "task": {"type": "string", "required": True},
    "repo_path": {
        "type": "path",
        "required": False,
        "default": "/Volumes/Craig/Code/workgraph",
    },
    "max_verify_attempts": {"type": "int", "required": False, "default": 3},
    "max_review_attempts": {"type": "int", "required": False, "default": 3},
    "max_gate_attempts": {"type": "int", "required": False, "default": 2},
}

_FINDINGS_YES_RE = re.compile(r"\bFINDINGS:\s*YES\b", re.IGNORECASE)
_FINDINGS_NO_RE = re.compile(r"\bFINDINGS:\s*NO\b", re.IGNORECASE)

_BLOCKED_YES_RE = re.compile(r"\bRELEASE_BLOCKED:\s*YES\b", re.IGNORECASE)
_BLOCKED_NO_RE = re.compile(r"\bRELEASE_BLOCKED:\s*NO\b", re.IGNORECASE)


def run_verification(repo_path: Path) -> Tuple[bool, str]:
    """Execute deterministic verification outside LLM context.

    Runs .cao/verification/verify.sh in repo_path.
    Returns (passed, evidence).
    On pass: returns compact status lines.
    On failure: returns the concise failure output.
    """
    verify_script = repo_path / ".cao" / "verification" / "verify.sh"
    if not verify_script.exists():
        # Fallback to direct go vet & go test if verify.sh is missing
        cmd = ["bash", "-c", "unset WORKGRAPH_AI_SESSION_ID WORKGRAPH_AI_HOME WORKGRAPH_AI_DATABASE && go vet ./... && go test ./..."]
    else:
        cmd = [str(verify_script)]

    result = subprocess.run(
        cmd,
        cwd=str(repo_path),
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
    )
    output = result.stdout.strip()
    passed = (result.returncode == 0)

    if passed:
        evidence = "go_vet: pass\ngo_test: pass"
    else:
        # Failure evidence: return the relevant failure output
        evidence = output if output else f"verification failed with exit code {result.returncode}"

    return passed, evidence


def run_jev_shadow(repo_path: Path) -> Dict[str, Any]:
    """Execute non-authoritative Jev shadow telemetry.

    Collects metadata via collect_change_metadata.py and pipes to jev_shadow.py.
    This is observational telemetry only and MUST NEVER alter workflow control flow.
    """
    collector = repo_path / ".cao" / "verification" / "collect_change_metadata.py"
    evaluator = repo_path / ".cao" / "verification" / "jev_shadow.py"

    if not collector.exists() or not evaluator.exists():
        return {
            "mode": "shadow",
            "status": "unavailable",
            "reason": "verification scripts not found",
        }

    try:
        col_res = subprocess.run(
            [sys.executable, str(collector)],
            cwd=str(repo_path),
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            check=True,
        )
        metadata = json.loads(col_res.stdout)
    except Exception as exc:
        return {
            "mode": "shadow",
            "status": "unavailable",
            "error": f"metadata collection failed: {exc}",
        }

    try:
        eval_res = subprocess.run(
            [sys.executable, str(evaluator)],
            input=col_res.stdout,
            cwd=str(repo_path),
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            timeout=30,
        )
        if eval_res.returncode == 0:
            shadow_data = json.loads(eval_res.stdout)
            shadow_data["change_metadata"] = metadata
            return shadow_data
        else:
            return {
                "mode": "shadow",
                "status": "unavailable",
                "error": eval_res.stderr.strip() or f"exit code {eval_res.returncode}",
                "change_metadata": metadata,
            }
    except Exception as exc:
        return {
            "mode": "shadow",
            "status": "unavailable",
            "error": str(exc),
            "change_metadata": metadata,
        }


def extract_acceptance_criteria(architect_text: str) -> str:
    """Extract acceptance criteria section if clearly marked, or return full plan."""
    marker = "acceptance criteria"
    lower = architect_text.lower()
    idx = lower.find(marker)
    if idx != -1:
        return architect_text[idx:].strip()
    return architect_text


def has_material_findings(review_text: str) -> bool:
    """Determine if reviewer reported material findings.

    Checks explicit markers first:
      FINDINGS: YES -> True
      FINDINGS: NO  -> False
    Fallback: check for non-empty finding headers or failure indications.
    """
    if _FINDINGS_YES_RE.search(review_text):
        return True
    if _FINDINGS_NO_RE.search(review_text):
        return False
    # If agent did not output explicit marker, check for obvious signals
    lower = review_text.lower()
    if "no material finding" in lower or "no material issues" in lower or "lgtm" in lower:
        return False
    return True


def is_release_blocked(gate_text: str) -> bool:
    """Determine if release gate blocked the release.

    Checks explicit markers:
      RELEASE_BLOCKED: YES -> True
      RELEASE_BLOCKED: NO  -> False
    """
    if _BLOCKED_YES_RE.search(gate_text):
        return True
    if _BLOCKED_NO_RE.search(gate_text):
        return False
    lower = gate_text.lower()
    if "release-ready" in lower and "not release-ready" not in lower:
        return False
    return True


def main() -> None:
    inputs = get_inputs()
    task = inputs["task"]
    raw_repo = inputs.get("repo_path", "/Volumes/Craig/Code/workgraph")
    repo_path = Path(raw_repo).resolve()

    max_verify_attempts = int(inputs.get("max_verify_attempts", 3))
    max_review_attempts = int(inputs.get("max_review_attempts", 3))
    max_gate_attempts = int(inputs.get("max_gate_attempts", 2))

    working_dir_str = str(repo_path)

    # -------------------------------------------------------------------------
    # Stage 1: Architect (Understanding and Planning)
    # -------------------------------------------------------------------------
    architect_prompt = (
        f"You occupy the Architect seat in the Workgraph Feature Engineering pod.\n\n"
        f"Task:\n{task}\n\n"
        "Read `.cao/RIG.md` and `.cao/pods/feature-engineering.md`.\n"
        "Inspect relevant specifications under `specs/` and existing codebase.\n"
        "Produce a concise implementation contract containing:\n"
        "1. Goal\n"
        "2. Relevant existing behavior\n"
        "3. Proposed design\n"
        "4. Non-goals\n"
        "5. Risks and edge cases\n"
        "6. Concrete testable acceptance criteria\n"
        "7. Expected tests and documentation changes\n\n"
        "Prefer the smallest coherent solution.\n"
        "Do not modify the repository.\n"
    )
    architect_step = step(
        "codex",
        "wg-architect",
        architect_prompt,
        recovery="idempotent",
        step_id="architect",
        working_directory=working_dir_str,
    )
    architect_plan = architect_step.output
    acceptance_criteria = extract_acceptance_criteria(architect_plan)

    # -------------------------------------------------------------------------
    # Stage 2: Developer (Implementation)
    # -------------------------------------------------------------------------
    developer_prompt = (
        f"You occupy the Developer seat in the Workgraph Feature Engineering pod.\n\n"
        f"Task:\n{task}\n\n"
        f"Architect Implementation Contract:\n{architect_plan}\n\n"
        "Implement the requested work against the acceptance criteria.\n"
        "- Follow existing Workgraph conventions and privacy boundaries.\n"
        "- Add or update tests for changed behavior.\n"
        "- Update documentation/help if user-visible behavior changes.\n"
        "- Do not run `go vet ./...` or `go test ./...`; the workflow runs authoritative "
        "verification outside your sandbox.\n"
        "- You may run narrowly scoped tests or targeted checks while developing.\n"
        "Report implementation changes and targeted checks performed; the workflow will "
        "independently run authoritative verification.\n"
    )
    developer_step = step(
        "codex",
        "wg-developer",
        developer_prompt,
        recovery="manual",
        step_id="developer-initial",
        working_directory=working_dir_str,
    )

    # -------------------------------------------------------------------------
    # Stage 3: Deterministic Verification Loop
    # -------------------------------------------------------------------------
    verify_attempts = 0
    verification_passed = False
    verification_evidence = ""

    while verify_attempts < max_verify_attempts:
        verify_attempts += 1
        verification_passed, verification_evidence = run_verification(repo_path)
        if verification_passed:
            break

        # Verification failed -> ask Developer to fix
        fix_prompt = (
            f"Deterministic verification failed (attempt {verify_attempts}/{max_verify_attempts}):\n\n"
            f"{verification_evidence}\n\n"
            "Correct the code and tests so workflow-owned deterministic verification can pass.\n"
            "Do not run `go vet ./...` or `go test ./...`; you may run targeted checks.\n"
            "Never weaken a test or check merely to make it pass.\n"
            "Report what you corrected and any targeted checks performed.\n"
        )
        step(
            "codex",
            "wg-developer",
            fix_prompt,
            recovery="manual",
            step_id=f"developer-fix-verify-{verify_attempts}",
            working_directory=working_dir_str,
        )

    if not verification_passed:
        err_msg = (
            f"Deterministic verification failed after {max_verify_attempts} attempts:\n"
            f"{verification_evidence}"
        )
        print(f"ERROR: {err_msg}", file=sys.stderr)
        emit_output({
            "status": "failed",
            "stage": "verification",
            "error": err_msg,
            "evidence": verification_evidence,
        })
        sys.exit(1)

    # -------------------------------------------------------------------------
    # Stage 4: Jev Shadow Evaluation (Observational Telemetry Only)
    # -------------------------------------------------------------------------
    jev_telemetry = run_jev_shadow(repo_path)

    # -------------------------------------------------------------------------
    # Stage 5: Adversarial Reviewer Loop
    # -------------------------------------------------------------------------
    review_assessments = 0
    review_fixes = 0
    review_clean = False
    reviewer_output = ""

    while True:
        review_assessments += 1

        # NOTE: Reviewer independently assesses the change without seeing Jev telemetry.
        reviewer_prompt = (
            f"You occupy the Adversarial Reviewer seat in the Workgraph Feature Engineering pod.\n\n"
            f"Original Task:\n{task}\n\n"
            f"Architect Acceptance Criteria:\n{acceptance_criteria}\n\n"
            f"Deterministic Verification Evidence:\n{verification_evidence}\n\n"
            "Independently inspect the repository changes and tests.\n"
            "Do not assume the architect or developer was correct.\n"
            "Use the supplied deterministic verification evidence; do not rerun the "
            "repository-wide authoritative suite.\n"
            "Look for material issues involving correctness, architecture, edge cases,\n"
            "backward compatibility, privacy boundaries, data integrity, and tests.\n"
            "Do not report stylistic preference as a defect.\n\n"
            "Conclude your review with an explicit marker line:\n"
            "FINDINGS: NO\n"
            "(if there are no material findings, stating why it is sound)\n"
            "or:\n"
            "FINDINGS: YES\n"
            "(followed by each concrete finding, where it occurs, and violated requirement)\n"
        )

        reviewer_step = step(
            "antigravity_cli",
            "wg-adversarial-reviewer",
            reviewer_prompt,
            recovery="idempotent",
            step_id=f"reviewer-{review_assessments}",
            working_directory=working_dir_str,
        )
        reviewer_output = reviewer_step.output

        if not has_material_findings(reviewer_output):
            review_clean = True
            break

        if review_fixes >= max_review_attempts:
            break

        review_fixes += 1
        dev_review_fix_prompt = (
            f"The Adversarial Reviewer identified material findings (assessment {review_assessments}; "
            f"correction cycle {review_fixes}/{max_review_attempts}):\n\n"
            f"{reviewer_output}\n\n"
            "Make the smallest coherent correction addressing these findings.\n"
            "Preserve compatibility and privacy invariants.\n"
            "Do not run `go vet ./...` or `go test ./...`; the workflow will verify the "
            "correction outside your sandbox. You may run targeted checks.\n"
            "Report your changes and any targeted checks performed.\n"
        )
        step(
            "codex",
            "wg-developer",
            dev_review_fix_prompt,
            recovery="manual",
            step_id=f"developer-fix-review-{review_fixes}",
            working_directory=working_dir_str,
        )

        # Re-run verification after developer modifications
        v_passed, v_ev = run_verification(repo_path)
        if not v_passed:
            # Quick bounded re-verification loop
            for v_fix_i in range(1, max_verify_attempts + 1):
                step(
                    "codex",
                    "wg-developer",
                    f"Deterministic verification failed after review fix:\n{v_ev}\n"
                    "Fix the failure. Do not run `go vet ./...` or `go test ./...`; the "
                    "workflow will rerun authoritative verification. You may run targeted checks.",
                    recovery="manual",
                    step_id=f"developer-fix-rev-verify-{review_fixes}-{v_fix_i}",
                    working_directory=working_dir_str,
                )
                v_passed, v_ev = run_verification(repo_path)
                if v_passed:
                    break
            if not v_passed:
                err_msg = f"Deterministic verification failed after review fix: {v_ev}"
                print(f"ERROR: {err_msg}", file=sys.stderr)
                emit_output({
                    "status": "failed",
                    "stage": "verification_after_review",
                    "error": err_msg,
                })
                sys.exit(1)
        verification_evidence = v_ev

    if not review_clean:
        err_msg = (
            "Unresolved adversarial reviewer findings after "
            f"{max_review_attempts} correction cycles."
        )
        print(f"ERROR: {err_msg}", file=sys.stderr)
        emit_output({
            "status": "failed",
            "stage": "review",
            "error": err_msg,
            "last_review": reviewer_output,
        })
        sys.exit(1)

    # -------------------------------------------------------------------------
    # Stage 6: Release Gate
    # -------------------------------------------------------------------------
    gate_assessments = 0
    gate_fixes = 0
    gate_passed = False
    gate_output = ""

    while True:
        gate_assessments += 1

        gate_prompt = (
            f"You occupy the Release Gate seat in the Workgraph Feature Engineering pod.\n\n"
            f"Original Task:\n{task}\n\n"
            f"Architect Acceptance Criteria:\n{acceptance_criteria}\n\n"
            f"Deterministic Verification Evidence:\n{verification_evidence}\n\n"
            f"Adversarial Review Outcome:\n{reviewer_output}\n\n"
            "Perform an independent final release-readiness assessment against repository evidence.\n"
            "Use the supplied deterministic verification evidence; do not rerun the "
            "repository-wide authoritative suite.\n"
            "Verify:\n"
            "- Requested behavior is implemented and acceptance criteria are satisfied\n"
            "- Tests cover changed behavior\n"
            "- Documentation and help are sufficient\n"
            "- Privacy guarantees and backward compatibility hold\n"
            "- No unintended repository artifacts remain\n\n"
            "Distinguish release-blocking findings from non-blocking observations.\n"
            "Conclude your assessment with an explicit marker line:\n"
            "RELEASE_BLOCKED: NO\n"
            "(if the change is release-ready with no blocking defects)\n"
            "or:\n"
            "RELEASE_BLOCKED: YES\n"
            "(followed by each blocker, evidence, and unmet requirement)\n"
        )

        gate_step = step(
            "codex",
            "wg-release-gate",
            gate_prompt,
            recovery="idempotent",
            step_id=f"release-gate-{gate_assessments}",
            working_directory=working_dir_str,
        )
        gate_output = gate_step.output

        if not is_release_blocked(gate_output):
            gate_passed = True
            break

        if gate_fixes >= max_gate_attempts:
            break

        gate_fixes += 1
        dev_gate_fix_prompt = (
            f"The Release Gate identified release blockers (assessment {gate_assessments}; "
            f"correction cycle {gate_fixes}/{max_gate_attempts}):\n\n"
            f"{gate_output}\n\n"
            "Correct the release blockers.\n"
            "Do not run `go vet ./...` or `go test ./...`; the workflow will verify the "
            "correction outside your sandbox. You may run targeted checks.\n"
            "Report your changes and any targeted checks performed.\n"
        )
        step(
            "codex",
            "wg-developer",
            dev_gate_fix_prompt,
            recovery="manual",
            step_id=f"developer-fix-gate-{gate_fixes}",
            working_directory=working_dir_str,
        )

        # Re-run verification
        v_passed, v_ev = run_verification(repo_path)
        if not v_passed:
            err_msg = f"Deterministic verification failed after release gate fix: {v_ev}"
            print(f"ERROR: {err_msg}", file=sys.stderr)
            emit_output({
                "status": "failed",
                "stage": "verification_after_gate",
                "error": err_msg,
            })
            sys.exit(1)
        verification_evidence = v_ev

    if not gate_passed:
        err_msg = (
            "Release Gate found unresolved blockers after "
            f"{max_gate_attempts} correction cycles."
        )
        print(f"ERROR: {err_msg}", file=sys.stderr)
        emit_output({
            "status": "failed",
            "stage": "release_gate",
            "error": err_msg,
            "last_gate_output": gate_output,
        })
        sys.exit(1)

    # -------------------------------------------------------------------------
    # Stage 7: Done — Emit structured outcome
    # -------------------------------------------------------------------------
    emit_output({
        "status": "completed",
        "task": task,
        "repo_path": working_dir_str,
        "verification": verification_evidence,
        "jev_shadow": jev_telemetry,
        "architect_plan": architect_plan,
        "reviewer_summary": reviewer_output,
        "release_gate_summary": gate_output,
    })


if __name__ == "__main__":
    main()
