import importlib.util
import json
import re
import subprocess
import sys
import tempfile
import types
import unittest
from pathlib import Path
from unittest.mock import patch


CAO_ROOT = Path(__file__).parents[1]


def load_module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class ProfileSmokeTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.repo = Path(self.temp.name)
        profiles = self.repo / '.cao/profiles'
        profiles.mkdir(parents=True)
        for path in (CAO_ROOT / 'profiles').glob('wg-*.md'):
            (profiles / path.name).write_text(path.read_text())
        (self.repo / 'sentinel.txt').write_text('unchanged')
        for args in [('init', '-q'), ('config', 'user.email', 'test@example.invalid'),
                     ('config', 'user.name', 'Test'), ('add', '.'), ('commit', '-qm', 'initial')]:
            subprocess.run(['git', *args], cwd=self.repo, check=True, capture_output=True)
        self.calls = []
        self.outputs = []

    def run_smoke(self, replacements=None, fail=False, **inputs):
        responses = {
            'smoke-architect': 'Implement an inclusive adult threshold, covering ages 17, 18, 19.',
            'smoke-developer': '{"expression":"age >= 18"}',
            'smoke-review-bug': 'FINDING: ' + json.dumps({'id': 'boundary', 'summary': '18 is excluded',
                'evidence': 'candidate-bug.py:2 uses strict comparison at 18', 'requirement': 'inclusive threshold'}) + '\nFINDINGS: YES',
            'smoke-review-fixed': 'FINDINGS: NO',
            'smoke-gate': 'RELEASE_BLOCKED: NO',
        }
        responses.update(replacements or {})
        cao = types.ModuleType('cao_workflow')
        cao.get_inputs = lambda: {'repo_path': str(self.repo), **inputs}
        cao.emit_output = self.outputs.append
        def step(*args, **kwargs):
            self.calls.append((args, kwargs))
            output = responses[kwargs['step_id']]
            if callable(output):
                output = output()
            if isinstance(output, types.SimpleNamespace):
                return output
            return types.SimpleNamespace(output=output, status='completed', replayed=False)
        cao.step = step
        with patch.dict(sys.modules, {'cao_workflow': cao}):
            rig = load_module('smoke_rig_test', CAO_ROOT / 'workflows/feature_engineering.py')
            smoke = load_module('profile_smoke_test', CAO_ROOT / 'workflows/profile_smoke.py')
        with patch.dict(sys.modules, {'feature_engineering': rig}):
            if fail:
                with self.assertRaises(SystemExit):
                    smoke.main()
            else:
                smoke.main()
        return rig, smoke

    def ids(self):
        return [kwargs['step_id'] for _, kwargs in self.calls]

    def failure(self):
        path, = (self.repo / '.git/cao-evidence').glob('*/*/failure.json')
        return json.loads(path.read_text())

    def test_five_step_interoperability_and_no_astra(self):
        self.run_smoke()
        self.assertEqual(self.ids(), ['smoke-architect', 'smoke-developer', 'smoke-review-bug',
                                    'smoke-review-fixed', 'smoke-gate'])
        self.assertEqual(self.calls[0][0][:2], ('codex', 'wg-architect-smoke'))
        self.assertEqual(self.calls[2][0][:2], ('copilot_cli', 'wg-adversarial-reviewer'))
        self.assertEqual(self.outputs[-1]['status'], 'completed')
        self.assertEqual(self.outputs[-1]['requested_assignments']['architect']['model'], 'gpt-6.1-sol')
        self.assertIn('model: gpt-6-astra', (self.repo / '.cao/profiles/wg-architect.md').read_text())
        self.assertEqual((self.repo / 'sentinel.txt').read_text(), 'unchanged')
        self.assertEqual(subprocess.run(['git', 'status', '--porcelain'], cwd=self.repo,
                                       check=True, capture_output=True).stdout, b'')
        for _, kwargs in self.calls:
            self.assertEqual(kwargs['timeout'], 90)
            self.assertEqual(kwargs['recovery'], 'idempotent')

    def test_astra_preflight_blocks_every_seat_before_calls(self):
        profile = self.repo / '.cao/profiles/wg-developer.md'
        profile.write_text(re.sub(r'(?m)^model: .+$', 'model: gpt-6-astra', profile.read_text()))
        self.run_smoke(fail=True)
        self.assertEqual(self.calls, [])
        self.assertIn('non-Astra', self.failure()['reason'])

    def test_missing_profile_is_not_silently_substituted(self):
        (self.repo / '.cao/profiles/wg-architect-smoke.md').unlink()
        self.run_smoke(fail=True)
        self.assertEqual(self.calls, [])

    def test_reviewer_must_be_a_distinct_model(self):
        profile = self.repo / '.cao/profiles/wg-adversarial-reviewer.md'
        profile.write_text(profile.read_text().replace('claude-haiku-5.5', 'gpt-6.1-sol'))
        self.run_smoke(fail=True)
        self.assertEqual(self.calls, [])
        self.assertIn('different configured model', self.failure()['reason'])

    def test_reviewer_must_detect_bug_with_relevant_evidence(self):
        self.run_smoke({'smoke-review-bug': 'FINDINGS: NO'}, fail=True)
        self.assertEqual(self.ids()[-1], 'smoke-review-bug')
        self.assertIn('deterministic fixture expectations', self.failure()['reason'])

    def test_fixed_review_and_gate_must_approve_correct_fixture(self):
        finding = 'FINDING: ' + json.dumps({'id': 'irrelevant', 'summary': 'unrelated issue',
            'evidence': 'irrelevant', 'requirement': 'irrelevant'})
        for key, marker in [('smoke-review-fixed', 'FINDINGS'), ('smoke-gate', 'RELEASE_BLOCKED')]:
            with self.subTest(key=key):
                # Each execution is isolated; evidence never overwrites the other case.
                self.run_smoke({key: finding + '\n' + marker + ': YES'}, fail=True)
                self.assertEqual(self.ids()[-1], key)

    def test_incorrect_developer_expression_stops_before_review(self):
        self.run_smoke({'smoke-developer': '{"expression":"age > 18"}'}, fail=True)
        self.assertEqual(self.ids(), ['smoke-architect', 'smoke-developer'])
        self.assertIn('boundary verification', self.failure()['reason'])

    def test_one_format_retry_then_complete(self):
        self.run_smoke({'smoke-review-fixed': '**FINDINGS: NO**',
                        'smoke-review-fixed-format-1': 'FINDINGS: NO'})
        self.assertEqual(self.ids()[-3:], ['smoke-review-fixed', 'smoke-review-fixed-format-1', 'smoke-gate'])

    def test_two_invalid_outputs_stop_with_sanitized_diagnostics(self):
        self.run_smoke({'smoke-review-bug': 'private raw output',
                        'smoke-review-bug-format-1': ''}, fail=True)
        self.assertEqual(self.ids()[-1], 'smoke-review-bug-format-1')
        self.assertTrue(self.failure()['invalid_assessment']['diagnostics']['empty'])
        for path in (self.repo / '.git/cao-evidence').glob('*/*/*.json'):
            self.assertNotIn('private raw output', path.read_text())

    def test_repository_mutation_halts_immediately(self):
        def mutate():
            (self.repo / 'sentinel.txt').write_text('changed')
            return 'plan'
        self.run_smoke({'smoke-architect': mutate}, fail=True)
        self.assertEqual(self.ids(), ['smoke-architect'])
        self.assertIn('modified repository', self.failure()['reason'])

    def test_replayed_step_does_not_pass_as_live_connectivity(self):
        self.run_smoke({'smoke-architect': types.SimpleNamespace(output='plan', status='completed', replayed=True)}, fail=True)
        self.assertEqual(self.ids(), ['smoke-architect'])
        self.assertIn('fresh completed', self.failure()['reason'])
