"""Opt-in, bounded live profile smoke test with synthetic boundary fixtures."""

from __future__ import annotations

import json
import re
import sys
from pathlib import Path

from cao_workflow import emit_output, get_inputs, step

INPUTS = {
    "repo_path": {"type": "path", "required": False, "default": "/Volumes/Craig/Code/workgraph"},
    "step_timeout_seconds": {"type": "int", "required": False, "default": 90},
}

PROFILES = {
    "architect": "wg-architect-smoke",
    "developer": "wg-developer",
    "reviewer": "wg-adversarial-reviewer",
    "gate": "wg-release-gate",
}
TASK = "Implement is_adult(age) for integer ages: return True exactly when age is at least 18."
SCOPE = (
    "This is an isolated synthetic PROFILE SMOKE test, not Workgraph feature engineering. "
    "Use only the supplied task and fixtures. Do not explore Workgraph source/specs or run "
    "repository-wide checks. Do not modify any file, stage, commit, or run shell writes. "
    "Keep the response short.\n"
)


def main() -> None:
    inputs = get_inputs()
    repo = Path(inputs.get("repo_path", "/Volumes/Craig/Code/workgraph")).resolve()
    timeout = int(inputs.get("step_timeout_seconds", 90))
    if not 1 <= timeout <= 120:
        raise ValueError("step timeout must be between 1 and 120 seconds")
    sys.dont_write_bytecode = True
    sys.path.insert(0, str(repo / ".cao" / "workflows"))
    try:
        import feature_engineering as rig
    finally:
        sys.path.pop(0)
    evidence = rig.RunEvidence(repo)
    configs = {}
    try:
        configs = {seat: rig.load_profile_config(repo, profile) for seat, profile in PROFILES.items()}
        if any("astra" in config["model"].lower() for config in configs.values()):
            raise ValueError("Astra is prohibited in profile_smoke")
    except (OSError, ValueError):
        evidence.fail("Smoke profile preflight failed: require explicit non-Astra provider/model assignments")
    if configs["reviewer"]["model"] in {configs["developer"]["model"], configs["gate"]["model"]}:
        evidence.fail("Smoke reviewer must use a different configured model from developer and gate")
    evidence.write("smoke-config", {"profiles": PROFILES, "requested": configs, "timeout_seconds": timeout})

    baseline = rig.repository_state(repo)

    def call(seat, step_id, prompt):
        before = rig.repository_state(repo)
        if before != baseline:
            evidence.fail(f"Repository changed between smoke steps before {step_id}")
        evidence.write("smoke-step-start", {"seat": seat, "step_id": step_id, "profile": PROFILES[seat],
                       "requested": configs[seat], "actual_provider": None, "actual_model": None,
                       "repository": before})
        try:
            result = step(configs[seat]["provider"], PROFILES[seat], SCOPE + prompt,
                          recovery="idempotent", step_id=step_id, working_directory=str(repo), timeout=timeout)
        except Exception as exc:
            evidence.write("smoke-execution-error", {"step_id": step_id, "exception_type": type(exc).__name__})
            evidence.fail(f"Smoke agent {step_id} failed; inspect CAO terminal state before another run")
        after = rig.repository_state(repo)
        evidence.write("smoke-step-result", {"step_id": step_id, "repository": after,
                       "completed": getattr(result, "status", None) == "completed",
                       "replayed": getattr(result, "replayed", False)})
        if before != after:
            evidence.fail(f"Smoke agent {step_id} modified repository state")
        if getattr(result, "status", None) != "completed" or getattr(result, "replayed", False):
            evidence.fail(f"Smoke agent {step_id} did not produce a fresh completed response")
        return result.output

    def assess(seat, step_id, prompt, marker, expected):
        contract = f"Final line is {marker}: YES for material findings or {marker}: NO for none.\n"
        full_prompt = prompt + contract + rig.ASSESSMENT_FORMAT
        for attempt in range(2):
            key = step_id if attempt == 0 else f"{step_id}-format-1"
            output = call(seat, key, full_prompt)
            try:
                parsed = rig.parse_assessment(output, marker)
            except rig.InvalidAssessment as exc:
                evidence.invalid_assessment = {"seat": seat, "step_id": key, "format_attempt": attempt,
                    "reason": str(exc), "diagnostics": rig.assessment_diagnostics(output, marker)}
                evidence.write("smoke-invalid-assessment", evidence.invalid_assessment)
                if attempt == 1:
                    evidence.fail(f"Smoke assessment {key} invalid after one formatting retry")
                full_prompt += "\nFormatting retry: independently reconstruct the same assessment; use the exact curated format.\n"
                continue
            evidence.assessment = {"seat": seat, "step_id": key, "format_attempt": attempt,
                **parsed, "verification": evidence.verification, "repository": rig.repository_state(repo)}
            evidence.write("smoke-assessment", evidence.assessment)
            if parsed["blocked"] != expected:
                evidence.fail(f"Smoke assessment {key} disagreed with deterministic fixture expectations")
            return parsed

    plan = call("architect", "smoke-architect", TASK + "\nReturn a contract under 100 words, including the boundary case.\n")
    if not isinstance(plan, str) or not plan.strip() or len(plan) > 1500:
        evidence.fail("Smoke architect contract was empty, non-text, or oversized")
    output = call("developer", "smoke-developer", TASK + "\nArchitect contract (verify independently):\n" + plan
                  + '\nReturn only JSON with one field expression: a Python comparison using age and 18. No Markdown.\n')
    try:
        implementation = json.loads(output, object_pairs_hook=rig.unique_json_object)
        if not isinstance(implementation, dict) or set(implementation) != {"expression"}:
            raise ValueError("invalid implementation fields")
        expression = implementation["expression"]
        if not isinstance(expression, str) or not re.fullmatch(r"age\s*>=\s*18", expression.strip()):
            raise ValueError("incorrect boundary expression")
    except (ValueError, TypeError, RecursionError):
        evidence.fail("Smoke developer output failed deterministic schema/boundary verification")
    expression = "age >= 18"
    bad = evidence.path / "candidate-bug.py"
    fixed = evidence.path / "candidate-fixed.py"
    for path, expr in ((bad, "age > 18"), (fixed, expression.strip())):
        with path.open("x", encoding="utf-8") as stream:
            stream.write(f"def is_adult(age):\n    return {expr}\n")
        path.chmod(0o600)
    evidence.verification = {"passed": True, "checks": {"developer_expression": "pass",
        "fixed_cases_17_18_19": "pass", "planted_bug_age_18": "expected_failure"}}
    evidence.write("smoke-verification", evidence.verification)
    bad_review = assess("reviewer", "smoke-review-bug", TASK + f"\nIndependently read and assess {bad}.\n"
                        "Workflow evidence: at age 18 this candidate returns False, expected True. "
                        "Identify the violated boundary, with candidate-bug.py and 18 in finding evidence.\n", "FINDINGS", True)
    if not any("candidate-bug.py" in item["evidence"] and "18" in item["evidence"] for item in bad_review["findings"]):
        evidence.fail("Smoke reviewer did not identify the planted boundary defect with concrete evidence")
    clean_review = assess("reviewer", "smoke-review-fixed", TASK + f"\nIndependently read and assess {fixed}.\n"
                          "Workflow verification: ages 17/18/19 satisfy the contract. Verify the source yourself.\n",
                          "FINDINGS", False)
    assess("gate", "smoke-gate", TASK + f"\nIndependently assess only {fixed} against this synthetic contract.\n"
           + "Workflow boundary verification: pass. Reviewer outcome:\n" + json.dumps(clean_review)
           + "\nJudge the fixture only; this is not a Workgraph release approval.\n", "RELEASE_BLOCKED", False)
    summary = {"status": "completed", "workflow": "profile_smoke", "evidence_directory": str(evidence.path),
               "requested_assignments": configs, "checks": evidence.verification["checks"],
               "scope": "synthetic profile interoperability; not product/release assurance"}
    evidence.write("smoke-completed", summary)
    emit_output(summary)


if __name__ == "__main__":
    main()
