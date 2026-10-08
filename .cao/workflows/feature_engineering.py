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

import hashlib
import json
import os
import re
import subprocess
import sys
import uuid
from pathlib import Path
from typing import Any, Dict, Tuple

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

FALLBACK_POLICY = {
    "enabled": False,
    "chains": {},
    "max_switches": 0,
}


def classify_failure(category: str) -> dict:
    infrastructure = {
        "provider_unavailable", "authentication_or_quota", "rate_limit", "session_startup",
    }
    return {
        "category": category if category in infrastructure | {
            "execution_uncertain", "engineering_findings", "verification_failure",
        } else "execution_uncertain",
        "fallback_candidate": category in infrastructure,
        "automatic_fallback": False,
        "requires_reconciliation": category not in infrastructure | {
            "engineering_findings", "verification_failure",
        },
    }


def git_bytes(repo: Path, *args: str) -> bytes:
    return subprocess.run(
        ["git", *args], cwd=repo, stdout=subprocess.PIPE,
        stderr=subprocess.PIPE, check=True,
    ).stdout


def repository_state(repo: Path) -> dict:
    head = git_bytes(repo, "rev-parse", "HEAD").decode().strip()
    parts = [git_bytes(repo, "ls-files", "--stage", "-z"),
             git_bytes(repo, "diff", "--binary", "--no-ext-diff", "--no-textconv")]
    paths = git_bytes(repo, "ls-files", "--others", "--exclude-standard", "-z").split(b"\0")
    for raw in sorted(path for path in paths if path):
        path = repo / os.fsdecode(raw)
        content = os.fsencode(os.readlink(path)) if path.is_symlink() else path.read_bytes()
        parts.extend([raw, str(path.lstat().st_mode).encode(), content])
    digest = hashlib.sha256()
    for part in parts:
        digest.update(len(part).to_bytes(8, "big"))
        digest.update(part)
    return {"head": head, "fingerprint": digest.hexdigest()}


class InvalidAssessment(ValueError):
    pass


def unique_json_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate JSON field")
        result[key] = value
    return result


def parse_assessment(output: str, marker: str) -> dict:
    if not isinstance(output, str) or not output.strip():
        raise InvalidAssessment("empty assessment")
    if len(output) > 20000:
        raise InvalidAssessment("assessment exceeds curated size limit")
    lines = output.strip().splitlines()
    if lines[-1] not in (f"{marker}: YES", f"{marker}: NO"):
        raise InvalidAssessment("expected one unquoted final outcome marker")
    findings = []
    for line in lines[:-1]:
        if not line.startswith("FINDING: "):
            raise InvalidAssessment("assessment must contain only curated findings and final marker")
        try:
            item = json.loads(line[len("FINDING: "):], object_pairs_hook=unique_json_object)
        except (ValueError, RecursionError) as exc:
            raise InvalidAssessment("invalid curated finding JSON") from exc
        if not isinstance(item, dict) or set(item) != {"id", "summary", "evidence", "requirement"}:
            raise InvalidAssessment("invalid curated finding fields")
        if any(not isinstance(v, str) or not v.strip() or len(v) > 240 for v in item.values()):
            raise InvalidAssessment("finding fields must be nonempty strings of at most 240 characters")
        findings.append(item)
    if len(findings) > 12 or len({f["id"] for f in findings}) != len(findings):
        raise InvalidAssessment("too many or duplicate findings")
    blocked = lines[-1].endswith("YES")
    if blocked != bool(findings):
        raise InvalidAssessment("outcome and curated findings disagree")
    return {"marker": lines[-1], "blocked": blocked, "findings": findings}


def assessment_diagnostics(output: Any, marker: str) -> dict:
    if not isinstance(output, str):
        return {"output_kind": "non_text", "empty": output is None}
    lines = output.strip().splitlines()
    canonical = [line for line in lines if line in (f"{marker}: YES", f"{marker}: NO")]
    last = lines[-1] if lines else ""
    mentions = re.findall(rf"\b{marker}\s*:", output, re.IGNORECASE)
    if last in (f"{marker}: YES", f"{marker}: NO"):
        last_kind = "canonical_marker"
    elif re.fullmatch(rf"\s*(?:\*\*|`|>\s*){marker}: (?:YES|NO)(?:\*\*|`)?\s*", last):
        last_kind = "decorated_marker"
    elif re.search(rf"\b{marker}\s*:", last, re.IGNORECASE):
        last_kind = "contains_marker"
    else:
        last_kind = "other" if last else "empty"
    return {
        "output_kind": "text", "empty": not output.strip(),
        "characters": len(output), "bytes": len(output.encode("utf-8")), "lines": len(lines),
        "output_sha256": hashlib.sha256(output.encode("utf-8")).hexdigest(),
        "canonical_marker_count": len(canonical), "marker_mention_count": len(mentions),
        "canonical_marker_is_final": last_kind == "canonical_marker",
        "conflicting_canonical_markers": len(set(canonical)) > 1,
        "code_fence_present": any(line.lstrip().startswith(("```", "~~~")) for line in lines),
        "finding_line_count": sum(line.startswith("FINDING: ") for line in lines),
        "truncation_indicator": "truncated" in output.lower() or "cao_output_dropped" in output,
        "last_line_kind": last_kind,
    }


ASSESSMENT_FORMAT = (
    "Return only curated findings, then exactly one final outcome marker. "
    'Each finding is one FINDING: {"id":"stable-id","summary":"defect",'
    '"evidence":"repository location/test evidence","requirement":"violated requirement"} line. '
    "At most 12 findings, each field at most 240 characters. "
    "Exclude secrets, user content, source excerpts, prompts and transcripts. "
    "The very last line must be the exact standalone outcome marker specified above. "
    "For approval, return only the NO marker. Do not repeat the instructions or alternative "
    "marker. Do not quote markers, wrap them in Markdown, or put anything after the marker.\n"
)
CORRECTION_FORMAT = (
    "End with exactly one DISPOSITION: JSON line with fields status and evidence. "
    "Status is corrected, rejected, or unable. Evidence is a concise repository/spec/test "
    "reference (at most 480 characters); never include secrets or source excerpts. "
    "A rejection requires concrete evidence for every finding and independent reassessment; "
    "your assertion alone cannot resolve it.\n"
)


def parse_disposition(output: str) -> dict:
    if not isinstance(output, str) or not output.strip():
        raise ValueError("missing correction disposition")
    lines = output.strip().splitlines()
    if sum("DISPOSITION:" in line for line in lines) != 1 or not lines[-1].startswith("DISPOSITION: "):
        raise ValueError("expected one final correction disposition")
    item = json.loads(lines[-1][len("DISPOSITION: "):], object_pairs_hook=unique_json_object)
    if not isinstance(item, dict) or set(item) != {"status", "evidence"}:
        raise ValueError("invalid disposition fields")
    if not isinstance(item["status"], str) or item["status"] not in {"corrected", "rejected", "unable"}:
        raise ValueError("invalid disposition status")
    if not isinstance(item["evidence"], str) or not item["evidence"].strip() or len(item["evidence"]) > 480:
        raise ValueError("disposition requires concise concrete evidence")
    return item


class RunEvidence:
    def __init__(self, repo: Path):
        run_id = os.environ.get("CAO_WORKFLOW_RUN_ID", "local")
        if not re.fullmatch(r"[A-Za-z0-9_-]{1,100}", run_id):
            raise ValueError("invalid CAO run identity")
        root = Path(os.fsdecode(git_bytes(repo, "rev-parse", "--absolute-git-dir")).strip())
        self.path = root / "cao-evidence" / run_id / uuid.uuid4().hex
        self.path.mkdir(parents=True, mode=0o700)
        self.sequence = 0
        self.assessment = None
        self.invalid_assessment = None
        self.context = {}
        self.disputed = False
        self.review_corrections = 0
        self.gate_corrections = 0
        self.verification = {"passed": None}
        self.write("start", {"run_id": run_id, "fallback": FALLBACK_POLICY})

    def write(self, kind: str, record: dict) -> None:
        self.sequence += 1
        target = self.path / f"{self.sequence:04d}-{kind}.json"
        self.atomic_write(target, record)

    def atomic_write(self, target: Path, record: dict) -> None:
        temporary = target.with_suffix(".tmp")
        with temporary.open("x", encoding="utf-8") as stream:
            os.chmod(temporary, 0o600)
            json.dump(record, stream, sort_keys=True, indent=2)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, target)

    def fail(self, reason: str) -> None:
        record = {"reason": reason, "last_assessment": self.assessment,
                  "invalid_assessment": self.invalid_assessment,
                  "correction_context": self.context, "verification": self.verification,
                  "review_corrections": self.review_corrections,
                  "gate_corrections": self.gate_corrections}
        self.atomic_write(self.path / "failure.json", record)
        message = f"{reason}; failure evidence: {self.path / 'failure.json'}"
        print(f"ERROR: {message}", file=sys.stderr)
        emit_output({"status": "failed", "error": message})
        raise SystemExit(1)


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
    return parse_assessment(review_text, "FINDINGS")["blocked"]


def is_release_blocked(gate_text: str) -> bool:
    return parse_assessment(gate_text, "RELEASE_BLOCKED")["blocked"]


def main() -> None:
    inputs = get_inputs()
    task = inputs["task"]
    raw_repo = inputs.get("repo_path", "/Volumes/Craig/Code/workgraph")
    repo_path = Path(raw_repo).resolve()

    max_verify_attempts = int(inputs.get("max_verify_attempts", 3))
    max_review_attempts = int(inputs.get("max_review_attempts", 3))
    max_gate_attempts = int(inputs.get("max_gate_attempts", 2))

    if max_verify_attempts < 1 or not 0 <= max_review_attempts <= 3 or not 0 <= max_gate_attempts <= 2:
        raise ValueError("verification attempts must be positive; review/gate correction caps are 3/2")
    evidence = RunEvidence(repo_path)
    if FALLBACK_POLICY != {"enabled": False, "chains": {}, "max_switches": 0}:
        evidence.fail("Automatic fallback is unsupported; keep the configuration seam disabled")
    working_dir_str = str(repo_path)

    def verify():
        try:
            passed, output = run_verification(repo_path)
        except Exception as exc:
            evidence.write("verification-error", {"exception_type": type(exc).__name__})
            evidence.fail("Verification harness failed to execute; rerun .cao/verification/verify.sh")
        evidence.verification = {"passed": passed, "checks": {
            name: ("pass" if f"{name}: pass" in output else "fail" if f"{name}: fail" in output else "unknown")
            for name in ("go_vet", "go_test")}}
        evidence.verification["repository"] = repository_state(repo_path)
        evidence.write("verification", evidence.verification)
        return passed, output

    def agent_step(provider, profile, prompt, *, format_attempt=0, **opts):
        step_id = opts["step_id"]
        correction = step_id.startswith("developer-fix-")
        assessment = step_id.startswith(("reviewer-", "release-gate-"))
        before = repository_state(repo_path)
        if correction:
            prompt += "\n" + CORRECTION_FORMAT
        if assessment and format_attempt == 0:
            prompt += "\n" + ASSESSMENT_FORMAT
            prompt += "\nPrevious material findings and correction context (navigation only):\n"
            prompt += json.dumps(evidence.context, sort_keys=True)
            prompt += "\nIndependently inspect current primary sources and resolve each material finding; do not merely agree with the developer.\n"
        if assessment and evidence.verification.get("repository") != before:
            evidence.fail(f"Repository changed since authoritative verification before {step_id}")
        profile_path = repo_path / ".cao" / "profiles" / f"{profile}.md"
        configured = {}
        if profile_path.exists():
            frontmatter = profile_path.read_text().split("---", 2)
            if len(frontmatter) == 3:
                for key in ("provider", "model", "codexProfile"):
                    match = re.search(rf"^{key}: ([A-Za-z0-9_.-]{{1,100}})$", frontmatter[1], re.MULTILINE)
                    configured[key] = match.group(1) if match else None
        evidence.write("step-start", {"seat": profile, "step_id": step_id,
                       "requested_provider": provider, "requested_profile": profile,
                       "configured_profile": configured,
                       "actual_provider": None, "actual_cli": None, "actual_model": None,
                       "actual_profile": None,
                       "repository": before})
        try:
            result = step(provider, profile, prompt, recovery=opts.pop("recovery"), **opts)
        except Exception as exc:
            evidence.write("execution-failure", {"step_id": step_id, "exception_type": type(exc).__name__,
                           "classification": classify_failure("execution_uncertain"),
                           "before": before, "after": repository_state(repo_path)})
            evidence.fail(f"Agent execution failed at {step_id}; inspect CAO status and reconcile side effects before recovery")
        after = repository_state(repo_path)
        evidence.write("step-result", {"seat": profile, "step_id": step_id,
                       "completed": getattr(result, "status", None) == "completed",
                       "replayed": getattr(result, "replayed", False), "repository": after})
        if getattr(result, "status", None) != "completed":
            evidence.fail(f"Agent step {step_id} did not complete; side effects uncertain")
        if (correction or assessment) and getattr(result, "replayed", False):
            evidence.write("replay-halt", {"step_id": step_id, "repository": repository_state(repo_path)})
            evidence.fail(f"Replayed {step_id} cannot establish fresh repository evidence; reconcile and start a new CAO run")
        if profile == "wg-architect" and before != after:
            evidence.fail("Architect modified repository state")
        if correction:
            changed = before["fingerprint"] != after["fingerprint"]
            try:
                disposition = parse_disposition(result.output)
            except (ValueError, TypeError, RecursionError):
                disposition = {"status": "invalid", "evidence": "missing or invalid disposition"}
            record = {"step_id": step_id, "before": before, "after": after,
                      "changed": changed, "disposition": disposition,
                      "replayed": getattr(result, "replayed", False)}
            evidence.write("correction", record)
            evidence.context = {"previous_findings": (evidence.assessment or {}).get("findings", []), **record}
            if disposition["status"] in {"invalid", "unable"}:
                evidence.fail(f"Correction {step_id} has no supported resolution")
            if not changed and disposition["status"] != "rejected":
                evidence.fail(f"No effective repository change at {step_id}; findings remain unresolved")
            if not changed and "verify" in step_id:
                evidence.fail(f"Verification correction {step_id} requires a repository change")
            evidence.disputed = not changed
        if assessment:
            marker = "FINDINGS" if step_id.startswith("reviewer-") else "RELEASE_BLOCKED"
            assessment_number = int(step_id.split("-format-", 1)[0].rsplit("-", 1)[1])
            if before != after:
                evidence.fail(f"Read-only assessment {step_id} modified repository state")
            try:
                parsed = parse_assessment(result.output, marker)
            except InvalidAssessment as exc:
                evidence.invalid_assessment = {"seat": profile, "step_id": step_id,
                               "assessment": assessment_number, "format_attempt": format_attempt,
                               "marker": "INVALID", "verification": evidence.verification,
                               "review_corrections": evidence.review_corrections,
                               "gate_corrections": evidence.gate_corrections,
                               "repository": after, "reason": str(exc),
                               "diagnostics": assessment_diagnostics(result.output, marker)}
                evidence.write("invalid-assessment", evidence.invalid_assessment)
                if format_attempt == 0:
                    retry_prompt = (
                        prompt + "\nOne bounded formatting retry: the previous assessment was invalid.\n"
                        + f"Parser reason: {exc}\n"
                        + "Response-shape diagnostics (no raw output):\n"
                        + json.dumps(evidence.invalid_assessment["diagnostics"], sort_keys=True)
                        + "\nReturn a complete replacement assessment using the required curated format. "
                        "This is a new CAO step in the same seat; the prior response is not supplied. "
                        "Independently reconstruct any findings from the same task and verified repository. "
                        "Never suppress material findings or invent approval to satisfy formatting. "
                        "Do not modify the repository. This consumes no developer correction.\n"
                    )
                    return agent_step(provider, profile, retry_prompt, format_attempt=1,
                                      recovery="idempotent", **{**opts, "step_id": f"{step_id}-format-1"})
                evidence.fail(f"Invalid assessment at {step_id}: {exc}")
            evidence.assessment = {**parsed, "seat": profile, "assessment": assessment_number,
                "format_attempt": format_attempt,
                "repository": after, "verification": evidence.verification,
                "review_corrections": evidence.review_corrections, "gate_corrections": evidence.gate_corrections}
            evidence.write("assessment", evidence.assessment)
            if before != evidence.assessment["repository"]:
                evidence.fail(f"Read-only assessment {step_id} modified repository state")
            if evidence.disputed and parsed["blocked"]:
                evidence.fail(f"Independent assessment {step_id} did not accept the no-change dispute")
            evidence.disputed = False
        return result

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
    architect_step = agent_step(
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
    developer_step = agent_step(
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
        verification_passed, verification_evidence = verify()
        if verification_passed:
            break

        if verify_attempts >= max_verify_attempts:
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
        agent_step(
            "codex",
            "wg-developer",
            fix_prompt,
            recovery="manual",
            step_id=f"developer-fix-verify-{verify_attempts}",
            working_directory=working_dir_str,
        )

    if not verification_passed:
        err_msg = f"Deterministic verification failed after {max_verify_attempts} attempts; rerun .cao/verification/verify.sh for diagnostics"
        evidence.fail(err_msg)

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

    def assess_review():
        nonlocal review_assessments, review_fixes, review_clean, reviewer_output, verification_evidence
        review_clean = False
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
                "Use FINDINGS: YES for material blockers or FINDINGS: NO for approval.\n"
            )

            reviewer_step = agent_step(
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
            evidence.review_corrections = review_fixes
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
            agent_step(
                "codex",
                "wg-developer",
                dev_review_fix_prompt,
                recovery="manual",
                step_id=f"developer-fix-review-{review_fixes}",
                working_directory=working_dir_str,
            )

            # Re-run verification after developer modifications
            v_passed, v_ev = verify()
            if not v_passed:
                # Quick bounded re-verification loop
                for v_fix_i in range(1, max_verify_attempts + 1):
                    agent_step(
                        "codex",
                        "wg-developer",
                        f"Deterministic verification failed after review fix:\n{v_ev}\n"
                        "Fix the failure. Do not run `go vet ./...` or `go test ./...`; the "
                        "workflow will rerun authoritative verification. You may run targeted checks.",
                        recovery="manual",
                        step_id=f"developer-fix-rev-verify-{review_fixes}-{v_fix_i}",
                        working_directory=working_dir_str,
                    )
                    v_passed, v_ev = verify()
                    if v_passed:
                        break
                if not v_passed:
                    evidence.fail("Deterministic verification failed after review fix")
            verification_evidence = v_ev

        if not review_clean:
            err_msg = (
                "Unresolved adversarial reviewer findings after "
                f"{max_review_attempts} correction cycles."
            )
            evidence.fail(err_msg)

    assess_review()

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
            f"Previous gate blockers (independently reassess):\n{gate_output}\n\n"
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
            "Use RELEASE_BLOCKED: YES for material blockers or RELEASE_BLOCKED: NO for approval.\n"
        )

        gate_step = agent_step(
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
        evidence.gate_corrections = gate_fixes
        dev_gate_fix_prompt = (
            f"The Release Gate identified release blockers (assessment {gate_assessments}; "
            f"correction cycle {gate_fixes}/{max_gate_attempts}):\n\n"
            f"{gate_output}\n\n"
            "Correct the release blockers.\n"
            "Do not run `go vet ./...` or `go test ./...`; the workflow will verify the "
            "correction outside your sandbox. You may run targeted checks.\n"
            "Report your changes and any targeted checks performed.\n"
        )
        agent_step(
            "codex",
            "wg-developer",
            dev_gate_fix_prompt,
            recovery="manual",
            step_id=f"developer-fix-gate-{gate_fixes}",
            working_directory=working_dir_str,
        )

        # Re-run verification
        v_passed, v_ev = verify()
        if not v_passed:
            evidence.fail("Deterministic verification failed after release gate fix")
        verification_evidence = v_ev
        assess_review()

    if not gate_passed:
        err_msg = (
            "Release Gate found unresolved blockers after "
            f"{max_gate_attempts} correction cycles."
        )
        evidence.fail(err_msg)

    # -------------------------------------------------------------------------
    # Stage 7: Done — Emit structured outcome
    # -------------------------------------------------------------------------
    evidence.write("completed", {"verification": evidence.verification, "last_assessment": evidence.assessment})
    emit_output({
        "status": "completed",
        "evidence_directory": str(evidence.path),
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
