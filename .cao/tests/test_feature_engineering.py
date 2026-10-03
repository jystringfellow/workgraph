import importlib.util
import sys
import types
import unittest
from pathlib import Path
from unittest.mock import patch


WORKFLOW_PATH = Path(__file__).parents[1] / "workflows" / "feature_engineering.py"


def load_workflow(inputs, responses):
    calls = []
    cao_workflow = types.ModuleType("cao_workflow")
    cao_workflow.get_inputs = lambda: inputs
    cao_workflow.emit_output = lambda output: None

    def step(*args, **kwargs):
        calls.append(kwargs["step_id"])
        return types.SimpleNamespace(output=responses[kwargs["step_id"]])

    cao_workflow.step = step
    with patch.dict(sys.modules, {"cao_workflow": cao_workflow}):
        spec = importlib.util.spec_from_file_location("feature_engineering_under_test", WORKFLOW_PATH)
        workflow = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(workflow)
    return workflow, calls


class FeatureEngineeringWorkflowTest(unittest.TestCase):
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
        self.assertIn("model: claude-sonnet-4-6", reviewer_profile)
        self.assertEqual(workflow_text.count("Do not run `go vet ./...` or `go test ./...`"), 5)

    def test_review_assesses_the_third_review_fix(self):
        workflow, calls = load_workflow(
            {
                "task": "test task",
                "repo_path": str(WORKFLOW_PATH.parents[2]),
                "max_review_attempts": 3,
                "max_gate_attempts": 0,
            },
            {
                "architect": "Acceptance Criteria:\n- test",
                "developer-initial": "implemented",
                "reviewer-1": "FINDINGS: YES\nfirst",
                "developer-fix-review-1": "fixed",
                "reviewer-2": "FINDINGS: YES\nsecond",
                "developer-fix-review-2": "fixed",
                "reviewer-3": "FINDINGS: YES\nthird",
                "developer-fix-review-3": "fixed",
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
                "repo_path": str(WORKFLOW_PATH.parents[2]),
                "max_review_attempts": 1,
                "max_gate_attempts": 2,
            },
            {
                "architect": "Acceptance Criteria:\n- test",
                "developer-initial": "implemented",
                "reviewer-1": "FINDINGS: NO",
                "release-gate-1": "RELEASE_BLOCKED: YES\nfirst",
                "developer-fix-gate-1": "fixed",
                "release-gate-2": "RELEASE_BLOCKED: YES\nsecond",
                "developer-fix-gate-2": "fixed",
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
                "repo_path": str(WORKFLOW_PATH.parents[2]),
                "max_review_attempts": 1,
            },
            {
                "architect": "Acceptance Criteria:\n- test",
                "developer-initial": "implemented",
                "reviewer-1": "FINDINGS: YES\nfirst",
                "developer-fix-review-1": "fixed",
                "reviewer-2": "FINDINGS: YES\nunresolved",
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
                "repo_path": str(WORKFLOW_PATH.parents[2]),
                "max_review_attempts": 1,
                "max_gate_attempts": 1,
            },
            {
                "architect": "Acceptance Criteria:\n- test",
                "developer-initial": "implemented",
                "reviewer-1": "FINDINGS: NO",
                "release-gate-1": "RELEASE_BLOCKED: YES\nfirst",
                "developer-fix-gate-1": "fixed",
                "release-gate-2": "RELEASE_BLOCKED: YES\nunresolved",
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
