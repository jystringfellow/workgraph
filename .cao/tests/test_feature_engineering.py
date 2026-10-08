import importlib.util
import sys
import json
import subprocess
import tempfile
import types
import unittest
from pathlib import Path
from unittest.mock import patch


WORKFLOW_PATH = Path(__file__).parents[1] / "workflows" / "feature_engineering.py"


def load_workflow(inputs, responses):
    calls = []
    prompts = {}
    assignments = {}
    cao_workflow = types.ModuleType("cao_workflow")
    cao_workflow.get_inputs = lambda: inputs
    cao_workflow.emit_output = lambda output: None

    def step(*args, **kwargs):
        key = kwargs["step_id"]
        calls.append(key)
        prompts[key] = args[2]
        assignments[key] = (args[0], args[1])
        response = responses[key]
        if callable(response):
            response = response()
        return types.SimpleNamespace(output=response, status="completed", replayed=False)

    cao_workflow.step = step
    with patch.dict(sys.modules, {"cao_workflow": cao_workflow}):
        spec = importlib.util.spec_from_file_location("feature_engineering_under_test", WORKFLOW_PATH)
        workflow = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(workflow)
    workflow.prompts = prompts
    workflow.assignments = assignments
    return workflow, calls


def finding(marker="FINDINGS", summary="defect"):
    return "FINDING: " + json.dumps({"id": "F1", "summary": summary,
        "evidence": "test.txt:1", "requirement": "acceptance criterion"}) + "\n" + marker + ": YES"


def disposition(status="corrected"):
    return "DISPOSITION: " + json.dumps({"status": status, "evidence": "test.txt:1 and specs/contract.md"})


class FeatureEngineeringWorkflowTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.repo = Path(self.temp.name)
        self.git("init", "-q")
        self.git("config", "user.email", "test@example.invalid")
        self.git("config", "user.name", "Test")
        profiles = self.repo / ".cao" / "profiles"
        profiles.mkdir(parents=True)
        for path in (WORKFLOW_PATH.parents[1] / "profiles").glob("wg-*.md"):
            (profiles / path.name).write_text(path.read_text())
        (self.repo / "test.txt").write_text("initial\n")
        self.git("add", ".")
        self.git("commit", "-qm", "initial")
        self.change_count = 0

    def git(self, *args):
        return subprocess.run(["git", *args], cwd=self.repo, check=True,
                              stdout=subprocess.PIPE, stderr=subprocess.PIPE).stdout

    def correct(self):
        self.change_count += 1
        (self.repo / "test.txt").write_text(f"correction {self.change_count}\n")
        return disposition()

    def failure(self):
        paths = list((self.repo / ".git" / "cao-evidence").glob("*/*/failure.json"))
        self.assertEqual(len(paths), 1)
        return json.loads(paths[0].read_text())

    def test_prompts_delegate_repository_wide_verification_to_workflow(self):
        workflow_text = WORKFLOW_PATH.read_text()
        developer_profile = (WORKFLOW_PATH.parents[1] / "profiles" / "wg-developer.md").read_text()
        reviewer_profile = (WORKFLOW_PATH.parents[1] / "profiles" / "wg-adversarial-reviewer.md").read_text()
        gate_profile = (WORKFLOW_PATH.parents[1] / "profiles" / "wg-release-gate.md").read_text()

        self.assertIn("Do not run the repository-wide authoritative verification commands", developer_profile)
        self.assertIn("`go vet ./...` or `go test ./...`", developer_profile)
        self.assertIn("runs outside the agent sandbox", developer_profile)
        self.assertIn("do not rerun the\nrepository-wide authoritative suite", reviewer_profile)
        self.assertIn("Do not rerun\nthe repository-wide authoritative suite", gate_profile)
        self.assertRegex(reviewer_profile, r"(?m)^model: [A-Za-z0-9_.-]+$")
        self.assertRegex(reviewer_profile, r"(?m)^provider: [A-Za-z0-9_.-]+$")
        self.assertEqual(workflow_text.count("Do not run `go vet ./...` or `go test ./...`"), 5)

    def test_review_assesses_the_third_review_fix(self):
        workflow, calls = load_workflow(
            {
                "task": "test task",
                "repo_path": str(self.repo),
                "max_review_attempts": 3,
                "max_gate_attempts": 0,
            },
            {
                "architect": "Acceptance Criteria:\n- test",
                "developer-initial": "implemented",
                "reviewer-1": finding(summary="first"),
                "developer-fix-review-1": self.correct,
                "reviewer-2": finding(summary="second"),
                "developer-fix-review-2": self.correct,
                "reviewer-3": finding(summary="third"),
                "developer-fix-review-3": self.correct,
                "reviewer-4": "FINDINGS: NO",
                "release-gate-1": "RELEASE_BLOCKED: NO",
            },
        )

        with patch.object(workflow, "run_verification", return_value=(True, "go_test: pass")), patch.object(
            workflow, "run_jev_shadow", return_value={"mode": "shadow"}
        ):
            workflow.main()

        self.assertEqual(
            [call for call in calls if call.startswith("developer-fix-review-")],
            [
                "developer-fix-review-1",
                "developer-fix-review-2",
                "developer-fix-review-3",
            ],
        )
        self.assertEqual(
            [call for call in calls if call.startswith("reviewer-")],
            ["reviewer-1", "reviewer-2", "reviewer-3", "reviewer-4"],
        )

    def test_release_gate_assesses_the_final_gate_fix(self):
        workflow, calls = load_workflow(
            {
                "task": "test task",
                "repo_path": str(self.repo),
                "max_review_attempts": 1,
                "max_gate_attempts": 2,
            },
            {
                "architect": "Acceptance Criteria:\n- test",
                "developer-initial": "implemented",
                "reviewer-1": "FINDINGS: NO",
                "release-gate-1": finding("RELEASE_BLOCKED", "first"),
                "developer-fix-gate-1": self.correct,
                "reviewer-2": "FINDINGS: NO",
                "release-gate-2": finding("RELEASE_BLOCKED", "second"),
                "developer-fix-gate-2": self.correct,
                "reviewer-3": "FINDINGS: NO",
                "release-gate-3": "RELEASE_BLOCKED: NO",
            },
        )

        with patch.object(workflow, "run_verification", return_value=(True, "go_test: pass")), patch.object(
            workflow, "run_jev_shadow", return_value={"mode": "shadow"}
        ):
            workflow.main()

        self.assertEqual(
            [call for call in calls if call.startswith("developer-fix-gate-")],
            ["developer-fix-gate-1", "developer-fix-gate-2"],
        )
        self.assertEqual(
            [call for call in calls if call.startswith("release-gate-")],
            ["release-gate-1", "release-gate-2", "release-gate-3"],
        )

    def test_review_fails_unresolved_after_assessing_final_review_fix(self):
        workflow, calls = load_workflow(
            {
                "task": "test task",
                "repo_path": str(self.repo),
                "max_review_attempts": 1,
            },
            {
                "architect": "Acceptance Criteria:\n- test",
                "developer-initial": "implemented",
                "reviewer-1": finding(summary="first"),
                "developer-fix-review-1": self.correct,
                "reviewer-2": finding(summary="unresolved"),
            },
        )

        with patch.object(workflow, "run_verification", return_value=(True, "go_test: pass")), patch.object(
            workflow, "run_jev_shadow", return_value={"mode": "shadow"}
        ), self.assertRaises(SystemExit):
            workflow.main()

        self.assertEqual(
            [call for call in calls if call.startswith("developer-fix-review-")],
            ["developer-fix-review-1"],
        )
        self.assertEqual(
            [call for call in calls if call.startswith("reviewer-")],
            ["reviewer-1", "reviewer-2"],
        )

    def test_release_gate_fails_unresolved_after_assessing_final_gate_fix(self):
        workflow, calls = load_workflow(
            {
                "task": "test task",
                "repo_path": str(self.repo),
                "max_review_attempts": 1,
                "max_gate_attempts": 1,
            },
            {
                "architect": "Acceptance Criteria:\n- test",
                "developer-initial": "implemented",
                "reviewer-1": "FINDINGS: NO",
                "release-gate-1": finding("RELEASE_BLOCKED", "first"),
                "developer-fix-gate-1": self.correct,
                "reviewer-2": "FINDINGS: NO",
                "release-gate-2": finding("RELEASE_BLOCKED", "unresolved"),
            },
        )

        with patch.object(workflow, "run_verification", return_value=(True, "go_test: pass")), patch.object(
            workflow, "run_jev_shadow", return_value={"mode": "shadow"}
        ), self.assertRaises(SystemExit):
            workflow.main()

        self.assertEqual(
            [call for call in calls if call.startswith("developer-fix-gate-")],
            ["developer-fix-gate-1"],
        )
        self.assertEqual(
            [call for call in calls if call.startswith("release-gate-")],
            ["release-gate-1", "release-gate-2"],
        )

    def run_case(self, responses, fail=False, **inputs):
        defaults = {"architect": "Acceptance criteria: test", "developer-initial": "implemented",
                    "reviewer-1": finding(), "developer-fix-review-1": self.correct,
                    "reviewer-2": "FINDINGS: NO", "release-gate-1": "RELEASE_BLOCKED: NO",
                    "reviewer-1-format-1": "LGTM"}
        defaults.update(responses)
        workflow, calls = load_workflow({"task": "test", "repo_path": str(self.repo), **inputs}, defaults)
        with patch.object(workflow, "run_verification", return_value=(True, "go_vet: pass\ngo_test: pass")), patch.object(
            workflow, "run_jev_shadow", return_value={"mode": "shadow", "secret_shadow": "do not pass"}
        ):
            if fail:
                with self.assertRaises(SystemExit):
                    workflow.main()
            else:
                workflow.main()
        return workflow, calls

    def test_tracked_correction_context_and_fresh_verification(self):
        workflow, calls = self.run_case({})
        self.assertIn("reviewer-2", calls)
        prompt = workflow.prompts["reviewer-2"]
        self.assertIn('"changed": true', prompt)
        self.assertIn('"status": "corrected"', prompt)
        self.assertIn("acceptance criterion", prompt)
        self.assertIn("go_test: pass", prompt)
        self.assertNotIn("secret_shadow", prompt)

    def test_committed_correction(self):
        def commit():
            self.correct()
            self.git("add", "test.txt")
            self.git("commit", "-qm", "correction")
            return disposition()
        initial = self.git("rev-parse", "HEAD")
        workflow, _ = self.run_case({"developer-fix-review-1": commit})
        self.assertNotEqual(initial, self.git("rev-parse", "HEAD"))
        self.assertIn('"changed": true', workflow.prompts["reviewer-2"])

    def test_noop_and_changed_then_reverted_stop_without_review(self):
        for revert in (False, True):
            with self.subTest(revert=revert):
                def noop():
                    content = (self.repo / "test.txt").read_bytes()
                    if revert:
                        self.correct()
                        (self.repo / "test.txt").write_bytes(content)
                    return disposition()
                _, calls = self.run_case({"developer-fix-review-1": noop}, fail=True)
                self.assertNotIn("reviewer-2", calls)
        records = list((self.repo / ".git/cao-evidence").glob("*/*/failure.json"))
        self.assertEqual(len(records), 2)
        for path in records:
            data = json.loads(path.read_text())
            self.assertFalse(data["correction_context"]["changed"])
            self.assertEqual(data["last_assessment"]["findings"][0]["id"], "F1")

    def test_dispute_requires_independent_acceptance(self):
        workflow, calls = self.run_case({"developer-fix-review-1": disposition("rejected")})
        self.assertIn("reviewer-2", calls)
        self.assertIn('"status": "rejected"', workflow.prompts["reviewer-2"])
        _, calls = self.run_case({"developer-fix-review-1": disposition("rejected"),
                                 "reviewer-2": finding()}, fail=True)
        self.assertNotIn("developer-fix-review-2", calls)
        self.assertNotIn("release-gate-1", calls)

    def test_missing_unsupported_or_unable_disposition_stops(self):
        for response in ("fixed", disposition("unable"), 'DISPOSITION: {"status":"rejected","evidence":""}'):
            with self.subTest(response=response):
                _, calls = self.run_case({"developer-fix-review-1": response}, fail=True)
                self.assertNotIn("reviewer-2", calls)

    def test_invalid_markers_never_approve_or_consume_corrections(self):
        workflow, _ = load_workflow({}, {})
        for marker in ("FINDINGS", "RELEASE_BLOCKED"):
            for output in ("", "LGTM release-ready", f"> {marker}: NO", f"```\n{marker}: NO\n```",
                           f"{marker}: NO\n{marker}: YES", f"{marker}: MAYBE", f"{marker}: NO thanks",
                           f"Earlier: {marker}: YES\n{marker}: NO", f"{marker}: YES"):
                with self.subTest(output=output), self.assertRaises(workflow.InvalidAssessment):
                    workflow.parse_assessment(output, marker)
            self.assertFalse(workflow.parse_assessment(f"{marker}: NO", marker)["blocked"])
        _, calls = self.run_case({"reviewer-1": "LGTM"}, fail=True)
        self.assertNotIn("developer-fix-review-1", calls)
        data = self.failure()
        self.assertEqual(data["review_corrections"], 0)
        self.assertIn("Invalid assessment", data["reason"])

    def test_exhausted_budget_retains_final_findings_even_if_emit_fails(self):
        workflow, calls = load_workflow({"task": "test", "repo_path": str(self.repo), "max_review_attempts": 0},
            {"architect": "plan", "developer-initial": "done", "reviewer-1": finding(summary="final defect")})
        with patch.object(workflow, "run_verification", return_value=(True, "go_test: pass")), patch.object(
            workflow, "run_jev_shadow", return_value={}), patch.object(workflow, "emit_output", side_effect=RuntimeError("capture lost")):
            with self.assertRaises(RuntimeError):
                workflow.main()
        data = self.failure()
        self.assertEqual(data["last_assessment"]["findings"][0]["summary"], "final defect")
        self.assertNotIn("developer-fix-review-1", calls)

    def test_gate_fix_reverification_review_gate_order(self):
        _, calls = self.run_case({"reviewer-1": "FINDINGS: NO",
            "release-gate-1": finding("RELEASE_BLOCKED"), "developer-fix-gate-1": self.correct,
            "release-gate-2": "RELEASE_BLOCKED: NO"})
        self.assertEqual(calls[-3:], ["developer-fix-gate-1", "reviewer-2", "release-gate-2"])

    def test_gate_renewed_findings_share_global_review_budget(self):
        _, calls = self.run_case({"release-gate-1": finding("RELEASE_BLOCKED"),
            "developer-fix-gate-1": self.correct, "reviewer-3": finding(summary="new issue")},
            fail=True, max_review_attempts=1)
        self.assertNotIn("developer-fix-review-2", calls)
        self.assertNotIn("release-gate-2", calls)
        self.assertEqual(self.failure()["last_assessment"]["findings"][0]["summary"], "new issue")

    def test_gate_renewed_findings_can_converge_with_remaining_budget(self):
        _, calls = self.run_case({"reviewer-1": "FINDINGS: NO",
            "release-gate-1": finding("RELEASE_BLOCKED"), "developer-fix-gate-1": self.correct,
            "reviewer-2": finding(summary="new issue"), "developer-fix-review-1": self.correct,
            "reviewer-3": "FINDINGS: NO", "release-gate-2": "RELEASE_BLOCKED: NO"})
        self.assertEqual(calls[-4:], ["reviewer-2", "developer-fix-review-1", "reviewer-3", "release-gate-2"])

    def test_git_fingerprint_covers_staging_untracked_and_content(self):
        workflow, _ = load_workflow({}, {})
        initial = workflow.repository_state(self.repo)
        self.correct()
        unstaged = workflow.repository_state(self.repo)
        self.git("add", "test.txt")
        staged = workflow.repository_state(self.repo)
        self.assertNotEqual(initial, unstaged)
        self.assertNotEqual(unstaged, staged)
        (self.repo / "new.bin").write_bytes(b"first")
        untracked = workflow.repository_state(self.repo)
        (self.repo / "new.bin").write_bytes(b"other")
        self.assertNotEqual(staged, untracked)
        self.assertNotEqual(untracked, workflow.repository_state(self.repo))

    def test_run_artifacts_recovery_does_not_overwrite_other_executions(self):
        workflow, _ = load_workflow({}, {})
        with patch.dict("os.environ", {"CAO_WORKFLOW_RUN_ID": "run-test"}):
            first = workflow.RunEvidence(self.repo)
            first.write("assessment", {"findings": ["retained"]})
            prior = {path.name: path.read_bytes() for path in first.path.iterdir()}
            second = workflow.RunEvidence(self.repo)
            second.write("assessment", {"findings": []})
        self.assertNotEqual(first.path, second.path)
        self.assertEqual(prior, {path.name: path.read_bytes() for path in first.path.iterdir()})
        self.assertEqual(workflow.repository_state(self.repo)["head"], self.git("rev-parse", "HEAD").decode().strip())
        self.assertEqual(first.path.stat().st_mode & 0o777, 0o700)
        self.assertEqual(next(first.path.glob("*.json")).stat().st_mode & 0o777, 0o600)

    def test_provider_policy_separates_failures_and_is_disabled(self):
        workflow, _ = load_workflow({}, {})
        self.assertFalse(workflow.FALLBACK_POLICY["enabled"])
        self.assertEqual(workflow.FALLBACK_POLICY["chains"], {})
        for category in ("provider_unavailable", "authentication_or_quota", "rate_limit", "session_startup"):
            result = workflow.classify_failure(category)
            self.assertTrue(result["fallback_candidate"])
            self.assertFalse(result["automatic_fallback"])
        for category in ("engineering_findings", "verification_failure", "execution_uncertain", "unknown"):
            self.assertFalse(workflow.classify_failure(category)["fallback_candidate"])
        self.assertTrue(workflow.classify_failure("execution_uncertain")["requires_reconciliation"])

    def test_execution_failure_stops_without_retry_or_exception_text_retention(self):
        def fail():
            self.correct()
            raise RuntimeError("secret error transcript")
        _, calls = self.run_case({"developer-fix-review-1": fail}, fail=True)
        self.assertNotIn("reviewer-2", calls)
        paths = list((self.repo / ".git/cao-evidence").glob("*/*/*.json"))
        combined = "".join(path.read_text() for path in paths)
        self.assertNotIn("secret error transcript", combined)
        self.assertIn("execution_uncertain", combined)

    def test_failed_verification_cannot_advance_to_renewed_review_or_gate(self):
        workflow, calls = load_workflow({"task": "test", "repo_path": str(self.repo)}, {
            "architect": "plan", "developer-initial": "done", "reviewer-1": "FINDINGS: NO",
            "release-gate-1": finding("RELEASE_BLOCKED"), "developer-fix-gate-1": self.correct})
        with patch.object(workflow, "run_verification", side_effect=[(True, "go_test: pass"), (False, "go_test: fail")]), patch.object(
            workflow, "run_jev_shadow", return_value={}), self.assertRaises(SystemExit):
            workflow.main()
        self.assertNotIn("reviewer-2", calls)
        self.assertFalse(self.failure()["verification"]["passed"])

    def test_untracked_and_staged_corrections_receive_review(self):
        def untracked():
            (self.repo / "new.txt").write_text("correction")
            return disposition()
        def staged():
            self.correct()
            self.git("add", "test.txt")
            return disposition()
        for correction in (untracked, staged):
            with self.subTest(correction=correction.__name__):
                _, calls = self.run_case({"developer-fix-review-1": correction})
                self.assertIn("reviewer-2", calls)

    def test_replayed_correction_or_assessment_halts_safely(self):
        for key in ("developer-fix-review-1", "reviewer-1"):
            workflow, calls = load_workflow({"task": "test", "repo_path": str(self.repo)}, {})
            def replay_step(*args, **opts):
                calls.append(opts["step_id"])
                return types.SimpleNamespace(output=finding(), status="completed", replayed=opts["step_id"] == key)
            with patch.object(workflow, "step", side_effect=replay_step), patch.object(
                workflow, "run_verification", return_value=(True, "go_test: pass")), patch.object(
                workflow, "run_jev_shadow", return_value={}), self.assertRaises(SystemExit):
                workflow.main()
            self.assertNotIn("reviewer-2", calls)

    def test_fallback_cannot_be_enabled_without_implementation(self):
        workflow, calls = load_workflow({"task": "test", "repo_path": str(self.repo)}, {})
        with patch.dict(workflow.FALLBACK_POLICY, {"enabled": True}), self.assertRaises(SystemExit):
            workflow.main()
        self.assertEqual(calls, [])

    def test_curated_assessments_reject_prose_and_unclosed_code_fence(self):
        workflow, _ = load_workflow({}, {})
        for output in ("```\nFINDINGS: NO", "Instructions:\nFINDINGS: NO", "findings: yes\nFINDINGS: NO",
                       finding().replace('"summary": "defect"', '"summary": "' + 'x' * 241 + '"')):
            with self.subTest(output=output), self.assertRaises(workflow.InvalidAssessment):
                workflow.parse_assessment(output, "FINDINGS")

    def test_committed_and_reverted_correction_is_effective_noop(self):
        def reverted_commit():
            self.correct()
            self.git("add", "test.txt")
            self.git("commit", "-qm", "correction")
            self.git("revert", "--no-edit", "HEAD")
            return disposition()
        initial = self.git("rev-parse", "HEAD")
        _, calls = self.run_case({"developer-fix-review-1": reverted_commit}, fail=True)
        self.assertNotEqual(initial, self.git("rev-parse", "HEAD"))
        self.assertNotIn("reviewer-2", calls)
        self.assertFalse(self.failure()["correction_context"]["changed"])

    def test_final_failed_initial_verification_launches_no_unassessed_repair(self):
        workflow, calls = load_workflow({"task": "test", "repo_path": str(self.repo), "max_verify_attempts": 1},
            {"architect": "plan", "developer-initial": "done"})
        with patch.object(workflow, "run_verification", return_value=(False, "go_test: fail\nsecret failure log")), self.assertRaises(SystemExit):
            workflow.main()
        self.assertEqual(calls, ["architect", "developer-initial"])
        self.assertNotIn("secret failure log", json.dumps(self.failure()))

    def test_assessment_schema_and_duplicate_fields_fail_closed(self):
        workflow, _ = load_workflow({}, {})
        duplicate = 'FINDING: {"id":"F1","id":"F2","summary":"defect","evidence":"test:1","requirement":"contract"}\nFINDINGS: YES'
        for output in (duplicate, 'FINDING: []\nFINDINGS: YES', finding().replace('FINDINGS: YES', 'FINDINGS: NO'),
                       finding() + '\nFINDINGS: NO', 'FINDING: ' + '[' * 1200 + '0' + ']' * 1200 + '\nFINDINGS: YES'):
            with self.subTest(output=output[:80]), self.assertRaises(workflow.InvalidAssessment):
                workflow.parse_assessment(output, 'FINDINGS')

    def test_readonly_reviewer_modification_fails(self):
        def modify():
            self.correct()
            return "FINDINGS: NO"
        _, calls = self.run_case({"reviewer-1": modify}, fail=True)
        self.assertNotIn("release-gate-1", calls)
        self.assertIn("modified repository", self.failure()["reason"])

    def test_verification_execution_error_retains_safe_failure(self):
        workflow, calls = load_workflow({"task": "test", "repo_path": str(self.repo)},
            {"architect": "plan", "developer-initial": "done"})
        with patch.object(workflow, "run_verification", side_effect=OSError("secret harness error")), self.assertRaises(SystemExit):
            workflow.main()
        self.assertEqual(calls, ["architect", "developer-initial"])
        self.assertIn("harness failed", self.failure()["reason"])
        self.assertNotIn("secret harness error", json.dumps(self.failure()))

    def test_gate_evidence_records_verification_before_renewed_assessment(self):
        self.run_case({"reviewer-1": "FINDINGS: NO",
            "release-gate-1": finding("RELEASE_BLOCKED"), "developer-fix-gate-1": self.correct,
            "release-gate-2": "RELEASE_BLOCKED: NO"})
        records = sorted((self.repo / ".git/cao-evidence").glob("*/*/*.json"))
        stage_records = [path for path in records if path.stem.endswith(("-correction", "-verification", "-assessment"))]
        self.assertEqual([path.stem.split('-', 1)[1] for path in stage_records[-4:]],
                         ["correction", "verification", "assessment", "assessment"])
        self.assertEqual(json.loads(stage_records[-2].read_text())["seat"], "wg-adversarial-reviewer")
        self.assertEqual(json.loads(stage_records[-1].read_text())["seat"], "wg-release-gate")

    def test_invalid_review_gets_one_format_retry_without_developer_correction(self):
        workflow, calls = self.run_case({"reviewer-1": "**FINDINGS: NO**",
                                        "reviewer-1-format-1": "FINDINGS: NO"})
        self.assertEqual(calls, ["architect", "developer-initial", "reviewer-1",
                                 "reviewer-1-format-1", "release-gate-1"])
        retry_prompt = workflow.prompts["reviewer-1-format-1"]
        self.assertIn("formatting", retry_prompt.lower())
        self.assertIn("Do not modify the repository", retry_prompt)
        records = list((self.repo / ".git/cao-evidence").glob("*/*/*-assessment.json"))
        review = [json.loads(path.read_text()) for path in records if json.loads(path.read_text())["seat"] == "wg-adversarial-reviewer" and json.loads(path.read_text())["marker"] != "INVALID"]
        self.assertEqual(review[0]["assessment"], 1)
        self.assertEqual(review[0]["format_attempt"], 1)
        self.assertEqual(review[0]["review_corrections"], 0)

    def test_format_retry_material_findings_follow_normal_correction_loop(self):
        _, calls = self.run_case({"reviewer-1": "FINDINGS: YES\nmalformed finding",
                                 "reviewer-1-format-1": finding()})
        self.assertEqual(calls[2:5], ["reviewer-1", "reviewer-1-format-1", "developer-fix-review-1"])
        self.assertIn("reviewer-2", calls)

    def test_invalid_twice_retains_diagnostics_and_stops(self):
        _, calls = self.run_case({"reviewer-1": "secret output\n**FINDINGS: NO**",
                                 "reviewer-1-format-1": "```\nFINDINGS: NO\n```"}, fail=True)
        self.assertEqual(calls[-2:], ["reviewer-1", "reviewer-1-format-1"])
        data = self.failure()
        self.assertEqual(data["review_corrections"], 0)
        invalid = data["invalid_assessment"]
        self.assertEqual(invalid["format_attempt"], 1)
        self.assertTrue(invalid["diagnostics"]["code_fence_present"])
        self.assertEqual(invalid["diagnostics"]["canonical_marker_count"], 1)
        self.assertEqual(invalid["diagnostics"]["last_line_kind"], "other")
        for path in (self.repo / ".git/cao-evidence").glob("*/*/*.json"):
            self.assertNotIn("secret output", path.read_text())

    def test_gate_format_retry_preserves_seat_and_assessment_number(self):
        _, calls = self.run_case({"reviewer-1": "FINDINGS: NO", "release-gate-1": "release-ready",
                                 "release-gate-1-format-1": "RELEASE_BLOCKED: NO"})
        self.assertEqual(calls[-2:], ["release-gate-1", "release-gate-1-format-1"])
        records = list((self.repo / ".git/cao-evidence").glob("*/*/*-assessment.json"))
        gates = [json.loads(path.read_text()) for path in records if json.loads(path.read_text())["seat"] == "wg-release-gate" and json.loads(path.read_text())["marker"] != "INVALID"]
        self.assertEqual(gates[0]["assessment"], 1)
        self.assertEqual(gates[0]["gate_corrections"], 0)

    def test_invalid_review_that_modifies_repository_is_not_retried(self):
        def modify():
            self.correct()
            return "malformed"
        _, calls = self.run_case({"reviewer-1": modify}, fail=True)
        self.assertNotIn("reviewer-1-format-1", calls)
        self.assertIn("modified repository", self.failure()["reason"])

    def test_diagnostics_distinguish_empty_conflicting_and_markdown_without_text(self):
        workflow, _ = load_workflow({}, {})
        self.assertTrue(workflow.assessment_diagnostics(" \n", "FINDINGS")["empty"])
        self.assertEqual(workflow.assessment_diagnostics(None, "FINDINGS")["output_kind"], "non_text")
        conflicting = workflow.assessment_diagnostics("FINDINGS: YES\nFINDINGS: NO", "FINDINGS")
        self.assertTrue(conflicting["conflicting_canonical_markers"])
        self.assertEqual(conflicting["canonical_marker_count"], 2)
        markdown = workflow.assessment_diagnostics("secret text\n**FINDINGS: YES**", "FINDINGS")
        self.assertEqual(markdown["last_line_kind"], "decorated_marker")
        self.assertNotIn("secret text", json.dumps(markdown))
        not_final = workflow.assessment_diagnostics("FINDINGS: NO\nextra prose", "FINDINGS")
        self.assertFalse(not_final["canonical_marker_is_final"])

    def test_retry_prompt_preserves_literal_task_references(self):
        task = 'Continue `specs/ai-handoff.md` and `workgraph ai handoff`.'
        workflow, calls = self.run_case({"reviewer-1": "malformed", "reviewer-1-format-1": "FINDINGS: NO"}, task=task)
        for key in ("architect", "developer-initial", "reviewer-1", "reviewer-1-format-1", "release-gate-1"):
            self.assertIn(task, workflow.prompts[key])

    def test_reviewer_provider_comes_from_profile_including_format_retry(self):
        profile = self.repo / '.cao/profiles/wg-adversarial-reviewer.md'
        profile.write_text(profile.read_text().replace('provider: copilot_cli', 'provider: configured_cli'))
        workflow, _ = self.run_case({'reviewer-1': 'malformed', 'reviewer-1-format-1': 'FINDINGS: NO'})
        self.assertEqual(workflow.assignments['reviewer-1'], ('configured_cli', 'wg-adversarial-reviewer'))
        self.assertEqual(workflow.assignments['reviewer-1-format-1'], ('configured_cli', 'wg-adversarial-reviewer'))
