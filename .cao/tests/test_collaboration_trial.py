import importlib.util
import json
import os
from pathlib import Path
import re
import shutil
import sys
import tempfile
import types
import unittest
from unittest.mock import patch


ROOT = Path(__file__).resolve().parents[1]


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class CollaborationTrialTest(unittest.TestCase):
    def setUp(self):
        from test_profile_smoke import ProfileSmokeTest
        fixture = ProfileSmokeTest()
        fixture.setUp()
        self.addCleanup(fixture.doCleanups)
        self.repo = fixture.repo
        self.calls = []
        self.outputs = []
        self.work = None

    def run_trial(self, overrides=None, gate_model='test-second-codex', fail=False):
        cao = types.ModuleType('cao_workflow')
        cao.get_inputs = lambda: {'repo_path': str(self.repo), 'gate_model': gate_model}
        cao.emit_output = self.outputs.append

        def call(provider, profile, prompt, **options):
            self.calls.append((provider, profile, prompt, options))
            work = Path(options['working_directory'])
            if self.work is None:
                self.work = work
                self.addCleanup(shutil.rmtree, work)
            key = options['step_id']
            action = (overrides or {}).get(key)
            if action is not None:
                action(work, prompt)
            elif 'developer' in key or key == 'trial-correction':
                (work / 'candidate.py').write_text('def is_adult(age):\n    return age >= 18\n')
            else:
                path = Path(re.search(r'Write your assessment to ([^;]+);', prompt)[1])
                marker = 'RELEASE_BLOCKED' if 'gate' in key else 'FINDINGS'
                path.write_text(f'{marker}: NO\n')
            return types.SimpleNamespace(status='completed', replayed=False, output='Ignored terminal chrome')

        cao.step = call
        with patch.dict(sys.modules, {'cao_workflow': cao}):
            rig = load('trial_rig', ROOT / 'workflows/feature_engineering.py')
            trial = load('collaboration_trial_test', ROOT / 'workflows/collaboration_trial.py')
        with patch.dict(sys.modules, {'feature_engineering': rig}):
            if fail:
                with self.assertRaises(SystemExit):
                    trial.main()
            else:
                trial.main()

    def failure(self):
        path = list((self.repo / '.git/cao-evidence').glob('*/*/failure.json'))[-1]
        return json.loads(path.read_text())

    def test_three_cli_providers_two_codex_models_and_local_default(self):
        self.run_trial()
        self.assertEqual([call[0] for call in self.calls], ['codex', 'copilot_cli', 'claude_code', 'codex'])
        self.assertEqual(self.calls[0][3]['model'], 'gpt-6.1-sol')
        self.assertEqual(self.calls[1][3]['model'], 'claude-haiku-5.5')
        self.assertNotIn('model', self.calls[2][3])
        self.assertEqual(self.calls[3][3]['model'], 'test-second-codex')
        self.assertTrue(all(call[3]['recovery'] == 'manual' for call in self.calls))
        self.assertNotIn('ARTIFACT_REQUEST', self.calls[0][2])
        self.assertEqual(self.outputs[-1]['status'], 'completed')

    def test_failed_tests_go_only_to_single_developer_correction(self):
        self.run_trial({'trial-developer': lambda work, prompt: None})
        keys = [call[3]['step_id'] for call in self.calls]
        self.assertEqual(keys[:2], ['trial-developer', 'trial-correction'])
        self.assertIn('age_18', self.calls[1][2])
        self.assertNotIn('age_18', self.calls[2][2])

    def test_noop_correction_stops_before_review(self):
        self.run_trial({'trial-developer': lambda work, prompt: None,
                        'trial-correction': lambda work, prompt: None}, fail=True)
        self.assertEqual(len(self.calls), 2)
        self.assertIn('No effective correction', self.failure()['reason'])

    def test_local_findings_are_advisory_and_curated(self):
        def local(work, prompt):
            path = Path(re.search(r'Write your assessment to ([^;]+);', prompt)[1])
            finding = {'id': 'local', 'summary': 'Question threshold', 'evidence': 'candidate.py:2', 'requirement': 'age'}
            path.write_text('FINDING: ' + json.dumps(finding) + '\nFINDINGS: YES\n')
        self.run_trial({'trial-local-1': local})
        self.assertEqual(len(self.calls), 4)
        self.assertEqual(self.outputs[-1]['status'], 'completed')

    def test_invalid_review_fails_without_retries_or_raw_retention(self):
        def invalid(work, prompt):
            path = Path(re.search(r'Write your assessment to ([^;]+);', prompt)[1])
            path.write_text('private raw output')
        self.run_trial({'trial-reviewer-1': invalid}, fail=True)
        self.assertEqual(len(self.calls), 2)
        self.assertFalse((self.work / 'reviewer-1.txt').exists())
        for path in (self.repo / '.git/cao-evidence').rglob('*.json'):
            self.assertNotIn('private raw output', path.read_text())

    def test_same_codex_model_and_astra_fail_before_any_call(self):
        for model in ('gpt-6.1-sol', 'gpt-6-astra'):
            self.run_trial(gate_model=model, fail=True)
        self.assertEqual(self.calls, [])

    def test_local_startup_failure_is_execution_failure(self):
        def fail(work, prompt):
            raise RuntimeError('private exception')
        self.run_trial({'trial-local-1': fail}, fail=True)
        self.assertEqual(self.failure()['execution_failure']['seat'], 'local')
        self.assertNotIn('private exception', json.dumps(self.failure()))

    def test_resume_stops_before_any_agent(self):
        with patch.dict(os.environ, {'CAO_WORKFLOW_RESUME': '1'}):
            self.run_trial(fail=True)
        self.assertEqual(self.calls, [])

    def test_reviewer_fix_runs_fresh_checks_and_both_reviews(self):
        def finding(work, prompt):
            path = Path(re.search(r'Write your assessment to ([^;]+);', prompt)[1])
            finding = {'id': 'threshold', 'summary': 'Inspect threshold', 'evidence': 'candidate.py:2', 'requirement': 'age'}
            path.write_text('FINDING: ' + json.dumps(finding) + '\nFINDINGS: YES\n')
        def fix(work, prompt):
            (work / 'candidate.py').write_text('def is_adult(age):\n    return (age >= 18)\n')
        self.run_trial({'trial-reviewer-1': finding, 'trial-correction': fix})
        self.assertEqual([call[3]['step_id'] for call in self.calls], [
            'trial-developer', 'trial-reviewer-1', 'trial-local-1', 'trial-correction',
            'trial-reviewer-2', 'trial-local-2', 'trial-gate-1'])

    def test_final_assessment_can_block_without_another_correction(self):
        def blocked(work, prompt):
            path = Path(re.search(r'Write your assessment to ([^;]+);', prompt)[1])
            finding = {'id': 'gate', 'summary': 'Material issue', 'evidence': 'candidate.py:2', 'requirement': 'age'}
            path.write_text('FINDING: ' + json.dumps(finding) + '\nRELEASE_BLOCKED: YES\n')
        self.run_trial({'trial-gate-1': blocked}, fail=True)
        self.assertEqual(len(self.calls), 4)
        self.assertEqual(self.failure()['last_assessment']['findings'][0]['id'], 'gate')

    def test_missing_candidate_has_actionable_failure_evidence(self):
        self.run_trial({'trial-developer': lambda work, prompt: (work / 'candidate.py').unlink()}, fail=True)
        self.assertIn('missing', self.failure()['reason'])
        self.assertEqual(len(self.calls), 1)


if __name__ == '__main__':
    unittest.main()
