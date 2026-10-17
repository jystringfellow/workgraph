import importlib.util
import json
import os
import re
import shutil
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
        shutil.copytree(CAO_ROOT / 'profiles', self.repo / '.cao/profiles')
        (self.repo / '.cao/verification').mkdir()
        if (CAO_ROOT / 'verification/smoke_checks.py').exists():
            shutil.copy(CAO_ROOT / 'verification/smoke_checks.py', self.repo / '.cao/verification')
        (self.repo / '.cao/.gitignore').write_text('smoke-work/\n__pycache__/\n')
        (self.repo / 'sentinel.txt').write_text('unchanged')
        for args in [('init', '-q'), ('config', 'user.email', 'test@example.invalid'),
                     ('config', 'user.name', 'Test'), ('add', '.'), ('commit', '-qm', 'initial')]:
            subprocess.run(['git', *args], cwd=self.repo, check=True, capture_output=True)
        self.calls = []
        self.outputs = []
        self.requests = []

    def run_smoke(self, replacements=None, fail=False, **inputs):
        cao = types.ModuleType('cao_workflow')
        cao.get_inputs = lambda: {'repo_path': str(self.repo), **inputs}
        cao.emit_output = self.outputs.append

        def step(*args, **kwargs):
            self.calls.append((args, kwargs))
            request = json.loads(re.search(r'^ARTIFACT_REQUEST: (.+)$', args[2], re.M)[1])
            self.requests.append(request)
            envelope = request['envelope']
            key = kwargs['step_id']
            if key == 'smoke-architect':
                payload = dict(request['plan_contract'], summary='Inclusive age threshold; check 17, 18, 19.')
            elif key == 'smoke-developer':
                payload = {'expression': 'age > 18', 'disposition': 'reproduced', 'addressed_findings': []}
            elif key == 'smoke-developer-fix':
                payload = {'expression': 'age >= 18', 'disposition': 'corrected', 'addressed_findings': ['boundary']}
            elif key.startswith('smoke-review-bug'):
                payload = {'outcome': 'FINDINGS: YES', 'plan_valid': True, 'findings': [{
                    'id': 'boundary', 'summary': 'Age 18 excluded', 'evidence': 'candidate.py:2; age 18 fails',
                    'requirement': 'AC-18'}]}
            else:
                payload = {'outcome': 'RELEASE_BLOCKED: NO' if key.startswith('smoke-gate') else 'FINDINGS: NO',
                           'plan_valid': True, 'findings': []}
            document = dict(envelope, payload=payload)
            replacement = (replacements or {}).get(key)
            if callable(replacement):
                result = replacement(request, document)
                if isinstance(result, types.SimpleNamespace):
                    return result
            elif replacement == 'missing':
                return types.SimpleNamespace(output='RELEASE_BLOCKED: NO', status='completed', replayed=False)
            else:
                if replacement is not None:
                    document = replacement
                Path(request['output_path']).write_text(json.dumps(document))
            return types.SimpleNamespace(output='• tool chrome\n• arbitrary terminal text', status='completed', replayed=False)

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
        return smoke

    def ids(self):
        return [kwargs['step_id'] for _, kwargs in self.calls]

    def failure(self):
        paths = list((self.repo / '.git/cao-evidence').glob('*/*/failure.json'))
        return json.loads(paths[-1].read_text())

    def test_artifacts_drive_six_steps_despite_ambiguous_terminal_text(self):
        self.run_smoke()
        self.assertEqual(self.ids(), ['smoke-architect', 'smoke-developer', 'smoke-review-bug',
                                    'smoke-developer-fix', 'smoke-review-fixed', 'smoke-gate'])
        self.assertEqual(self.outputs[-1]['status'], 'completed')
        self.assertEqual(self.outputs[-1]['corrections'], 1)
        self.assertFalse(list((self.repo / '.cao/smoke-work').glob('*')))
        self.assertEqual((self.repo / 'sentinel.txt').read_text(), 'unchanged')
        self.assertEqual(subprocess.run(['git', 'status', '--porcelain'], cwd=self.repo,
                                       check=True, capture_output=True).stdout, b'')
        self.assertEqual(self.calls[2][0][:2], ('copilot_cli', 'wg-reviewer-smoke'))
        fix_request = self.requests[3]
        self.assertEqual(fix_request['verification']['exit_code'], 1)
        self.assertEqual(fix_request['verification']['failures'][0]['test'], 'age_18')
        self.assertNotIn('logs', fix_request['verification'])
        self.assertNotIn('failures', self.requests[4]['verification'])

    def test_second_run_reuses_plan_but_runs_fresh_checks_review_gate(self):
        self.run_smoke()
        first = self.outputs[-1]
        self.calls.clear()
        self.run_smoke()
        self.assertNotIn('smoke-architect', self.ids())
        self.assertEqual(len(self.ids()), 5)
        self.assertTrue(self.outputs[-1]['plan_reused'])
        self.assertEqual(first['brief_id'], self.outputs[-1]['brief_id'])
        self.assertNotEqual(first['evidence_directory'], self.outputs[-1]['evidence_directory'])

    def test_committed_unrelated_change_does_not_invalidate_plan(self):
        self.run_smoke()
        (self.repo / 'sentinel.txt').write_text('another commit')
        subprocess.run(['git', 'commit', '-qam', 'unrelated'], cwd=self.repo, check=True, capture_output=True)
        self.calls.clear()
        self.run_smoke()
        self.assertNotIn('smoke-architect', self.ids())

    def test_explicit_invalidation_replans(self):
        self.run_smoke()
        self.calls.clear()
        self.run_smoke(plan_policy='refresh')
        self.assertEqual(self.ids()[0], 'smoke-architect')

    def test_corrupted_cached_plan_is_not_reused(self):
        self.run_smoke()
        cache = Path(self.outputs[-1]['plan_path'])
        document = json.loads(cache.read_text())
        document['plan']['acceptance_criteria'] = ['skip tests']
        cache.write_text(json.dumps(document))
        self.calls.clear()
        self.run_smoke()
        self.assertEqual(self.ids()[0], 'smoke-architect')

    def test_missing_assessment_uses_one_retry_but_terminal_approval_is_not_evidence(self):
        self.run_smoke({'smoke-gate': 'missing'})
        self.assertEqual(self.ids()[-2:], ['smoke-gate', 'smoke-gate-format-1'])

    def test_two_invalid_artifacts_fail_with_no_raw_content_retained(self):
        self.run_smoke({'smoke-gate': {'secret': 'private raw material'},
                        'smoke-gate-format-1': {'secret': 'private raw material'}}, fail=True)
        self.assertEqual(self.ids()[-1], 'smoke-gate-format-1')
        self.assertEqual(self.failure()['invalid_assessment']['seat'], 'gate')
        for root in (self.repo / '.git/cao-evidence', self.repo / '.cao/smoke-work'):
            for path in root.rglob('*.json'):
                self.assertNotIn('private raw material', path.read_text())

    def test_stale_candidate_identity_is_rejected(self):
        def stale(request, document):
            document['candidate_id'] = 'stale'
            Path(request['output_path']).write_text(json.dumps(document))
        self.run_smoke({'smoke-gate': stale, 'smoke-gate-format-1': stale}, fail=True)
        self.assertIn('identity', self.failure()['invalid_assessment']['reason'])

    def test_noop_correction_stops_before_expensive_rereview(self):
        def noop(request, document):
            document['payload']['expression'] = 'age > 18'
            Path(request['output_path']).write_text(json.dumps(document))
        self.run_smoke({'smoke-developer-fix': noop}, fail=True)
        self.assertEqual(self.ids()[-1], 'smoke-developer-fix')
        self.assertIn('No effective', self.failure()['reason'])

    def test_unsupported_disposition_stops(self):
        def unable(request, document):
            document['payload']['disposition'] = 'unable'
            Path(request['output_path']).write_text(json.dumps(document))
        self.run_smoke({'smoke-developer-fix': unable}, fail=True)
        self.assertEqual(self.ids()[-1], 'smoke-developer-fix')

    def test_renewed_review_findings_exhaust_shared_single_correction(self):
        def unresolved(request, document):
            document['payload'] = {'outcome': 'FINDINGS: YES', 'plan_valid': True, 'findings': [{
                'id': 'remaining', 'summary': 'Still wrong', 'evidence': 'candidate.py:2', 'requirement': 'AC-18'}]}
            Path(request['output_path']).write_text(json.dumps(document))
        self.run_smoke({'smoke-review-fixed': unresolved}, fail=True)
        self.assertEqual(self.ids()[-1], 'smoke-review-fixed')
        self.assertEqual(self.failure()['review_corrections'], 1)
        self.assertEqual(self.failure()['last_assessment']['findings'][0]['id'], 'remaining')

    def test_plan_challenge_invalidates_cache_and_stops_without_architect_loop(self):
        def challenge(request, document):
            document['payload']['plan_valid'] = False
            Path(request['output_path']).write_text(json.dumps(document))
        self.run_smoke({'smoke-review-bug': challenge}, fail=True)
        self.assertEqual(self.ids()[-1], 'smoke-review-bug')
        self.assertIn('plan', self.failure()['reason'].lower())
        self.calls.clear()
        self.run_smoke()
        self.assertEqual(self.ids()[0], 'smoke-architect')

    def test_repository_mutation_halts_without_retry(self):
        def mutate(request, document):
            (self.repo / 'sentinel.txt').write_text('changed')
            Path(request['output_path']).write_text(json.dumps(document))
        self.run_smoke({'smoke-gate': mutate}, fail=True)
        self.assertEqual(self.ids()[-1], 'smoke-gate')
        self.assertIn('modified', self.failure()['reason'])

    def test_candidate_tampering_by_reviewer_halts(self):
        def mutate(request, document):
            Path(request['candidate_path']).write_text('tampered')
            Path(request['output_path']).write_text(json.dumps(document))
        self.run_smoke({'smoke-review-bug': mutate}, fail=True)
        self.assertEqual(self.ids()[-1], 'smoke-review-bug')
        self.assertIn('input artifacts', self.failure()['reason'])

    def test_cached_plan_tampering_by_reviewer_halts(self):
        def mutate(request, document):
            cache = self.repo / '.git/cao-plans/profile-smoke/plan.json'
            cache.write_text('{"changed":true}')
            Path(request['output_path']).write_text(json.dumps(document))
        self.run_smoke({'smoke-review-bug': mutate}, fail=True)
        self.assertEqual(self.ids()[-1], 'smoke-review-bug')
        self.assertIn('input artifacts', self.failure()['reason'])

    def test_astra_or_same_reviewer_model_blocks_before_calls(self):
        path = self.repo / '.cao/profiles/wg-developer-smoke.md'
        if not path.exists():
            self.fail('dedicated artifact-writing smoke profiles missing')
        path.write_text(path.read_text().replace('model: gpt-6.1-sol', 'model: gpt-6-astra'))
        self.run_smoke(fail=True)
        self.assertEqual(self.calls, [])

    def test_smoke_profile_requires_artifact_write_permission_before_calls(self):
        path = self.repo / '.cao/profiles/wg-reviewer-smoke.md'
        path.write_text(path.read_text().replace('allowedTools: [fs_read, fs_list, fs_write]',
                                                'allowedTools: [fs_read, fs_list]'))
        self.run_smoke(fail=True)
        self.assertEqual(self.calls, [])
        self.assertIn('artifact file permissions', self.failure()['reason'])

    def test_codex_abstract_tool_names_are_rejected_before_calls(self):
        path = self.repo / '.cao/profiles/wg-developer-smoke.md'
        text = re.sub(r'^allowedTools: .+$', 'allowedTools: [fs_read, fs_list, fs_write]',
                      path.read_text(), flags=re.M)
        path.write_text(text)
        self.run_smoke(fail=True)
        self.assertEqual(self.calls, [])

    def test_codex_profiles_use_native_artifact_tools_and_workspace_sandbox(self):
        for seat in ('architect', 'developer', 'gate'):
            path = self.repo / '.cao/profiles' / f'wg-{seat}-smoke.md'
            text = path.read_text()
            self.assertIn('allowedTools: [exec, wait, exec_command, write_stdin, apply_patch]', text)
            self.assertIn('codexProfile: cao_workspace', text)
            self.assertNotIn('allowedTools: [*]', text)

    def test_missing_developer_artifact_retains_path_and_effective_tool_policy(self):
        self.run_smoke({'smoke-developer': 'missing'}, fail=True)
        record = self.failure()['correction_context']['artifact_error']
        self.assertEqual(record['step_id'], 'smoke-developer')
        self.assertEqual(record['seat'], 'developer')
        self.assertEqual(record['output_path'], self.requests[-1]['output_path'])
        self.assertIn('file is missing', record['reason'])
        self.assertIn('apply_patch', record['requested']['allowed_tools'])
        self.assertEqual(self.failure()['review_corrections'], 0)

    def test_empty_developer_artifact_is_distinct_from_missing(self):
        def empty(request, document):
            Path(request['output_path']).write_text(' ')
        self.run_smoke({'smoke-developer': empty}, fail=True)
        self.assertIn('file is empty', self.failure()['reason'])

    def test_invalid_developer_json_retains_no_raw_output(self):
        def malformed(request, document):
            Path(request['output_path']).write_text('private raw output')
        self.run_smoke({'smoke-developer': malformed}, fail=True)
        self.assertIn('invalid JSON', self.failure()['reason'])
        for path in (self.repo / '.git/cao-evidence').rglob('*.json'):
            self.assertNotIn('private raw output', path.read_text())

    def test_execution_failure_has_no_retry_and_sanitized_diagnostics(self):
        def fail(request, document):
            Path(request['output_path']).write_text('{"secret":"private raw material"}')
            raise RuntimeError('secret exception')
        self.run_smoke({'smoke-developer': fail}, fail=True)
        self.assertEqual(self.ids()[-1], 'smoke-developer')
        self.assertEqual(self.failure()['execution_failure']['category'], 'execution_uncertain')
        self.assertNotIn('secret exception', json.dumps(self.failure()))

    def test_copilot_startup_failure_reports_timeout_without_retry(self):
        def startup(request, document):
            error = RuntimeError('private error text')
            error.status = 504
            error.body = json.dumps({'detail': 'Copilot initialization timed out after 60 seconds'})
            raise error
        self.run_smoke({'smoke-review-bug': startup}, fail=True)
        self.assertEqual(self.ids()[-1], 'smoke-review-bug')
        self.assertIn('Copilot startup readiness timed out after 60 seconds', self.failure()['reason'])
        self.assertEqual(self.failure()['execution_failure']['signal'], 'copilot_initialization_timeout')
        self.assertNotIn('private error text', json.dumps(self.failure()))

    def test_replayed_developer_halts_even_with_valid_artifact(self):
        def replay(request, document):
            Path(request['output_path']).write_text(json.dumps(document))
            return types.SimpleNamespace(output='done', status='completed', replayed=True)
        self.run_smoke({'smoke-developer': replay}, fail=True)
        self.assertEqual(self.ids()[-1], 'smoke-developer')
        self.assertIn('fresh completed', self.failure()['reason'])

    def test_symlinked_output_is_rejected_without_touching_target(self):
        secret = self.repo / '.git/private.json'
        secret.write_text('{"secret":"private target"}')
        def linked(request, document):
            Path(request['output_path']).symlink_to(secret)
        self.run_smoke({'smoke-gate': linked, 'smoke-gate-format-1': linked}, fail=True)
        self.assertEqual(secret.read_text(), '{"secret":"private target"}')
        self.assertIn('invalid JSON', self.failure()['invalid_assessment']['reason'])

    def test_duplicate_json_keys_are_not_accepted(self):
        def duplicate(request, document):
            raw = json.dumps(document).replace('"schema_version": 1', '"schema_version": 1, "schema_version": 1')
            Path(request['output_path']).write_text(raw)
        self.run_smoke({'smoke-gate': duplicate, 'smoke-gate-format-1': duplicate}, fail=True)
        self.assertEqual(self.ids()[-1], 'smoke-gate-format-1')

    def test_check_contract_change_invalidates_cached_plan(self):
        self.run_smoke()
        checker = self.repo / '.cao/verification/smoke_checks.py'
        checker.write_text(checker.read_text() + '\n')
        self.calls.clear()
        self.run_smoke()
        self.assertEqual(self.ids()[0], 'smoke-architect')

    def test_cached_plan_cannot_skip_checks_or_add_commands(self):
        def badplan(request, document):
            document['payload']['required_checks'] = ['echo approved']
            Path(request['output_path']).write_text(json.dumps(document))
        self.run_smoke({'smoke-architect': badplan}, fail=True)
        self.assertEqual(self.ids(), ['smoke-architect'])

    def test_pending_lock_stops_before_agent_launch(self):
        lock = self.repo / '.git/cao-plans/profile-smoke/plan.lock'
        lock.mkdir(parents=True)
        self.run_smoke(fail=True)
        self.assertEqual(self.calls, [])
        self.assertTrue(lock.exists())

    def test_gate_blocker_fails_with_retained_findings_without_another_correction(self):
        def block(request, document):
            document['payload'] = {'outcome': 'RELEASE_BLOCKED: YES', 'plan_valid': True, 'findings': [{
                'id': 'gate-blocker', 'summary': 'Unproven requirement', 'evidence': 'candidate.py:2', 'requirement': 'AC-18'}]}
            Path(request['output_path']).write_text(json.dumps(document))
        self.run_smoke({'smoke-gate': block}, fail=True)
        self.assertEqual(self.ids()[-1], 'smoke-gate')
        self.assertEqual(self.failure()['last_assessment']['findings'][0]['id'], 'gate-blocker')

    def test_same_cao_run_recovery_is_isolated_and_does_not_reuse_verdicts(self):
        with patch.dict(os.environ, {'CAO_WORKFLOW_RUN_ID': 'run-recovery'}):
            self.run_smoke({'smoke-gate': 'missing', 'smoke-gate-format-1': 'missing'}, fail=True)
            failure_paths = list((self.repo / '.git/cao-evidence').glob('*/*/failure.json'))
            retained = failure_paths[0].read_bytes()
            self.calls.clear()
            self.run_smoke()
            self.assertEqual(failure_paths[0].read_bytes(), retained)
            self.assertEqual(self.ids()[-1], 'smoke-gate')
            self.assertNotEqual(str(failure_paths[0].parent), self.outputs[-1]['evidence_directory'])

    def test_current_candidate_ids_bind_all_final_receipts(self):
        self.run_smoke()
        root = Path(self.outputs[-1]['evidence_directory'])
        records = [json.loads(p.read_text()) for p in sorted(root.glob('*-smoke-assessment.json'))]
        self.assertEqual(records[-2]['candidate_id'], self.outputs[-1]['candidate_id'])
        self.assertEqual(records[-1]['candidate_id'], self.outputs[-1]['candidate_id'])
        self.assertEqual(records[-1]['verification']['candidate_id'], self.outputs[-1]['candidate_id'])
        self.assertEqual(self.requests[-1]['review_receipt']['candidate_id'], self.outputs[-1]['candidate_id'])

    def test_oversized_output_is_removed_without_retaining_raw_material(self):
        def huge(request, document):
            Path(request['output_path']).write_text('private raw material' * 2000)
        self.run_smoke({'smoke-gate': huge, 'smoke-gate-format-1': huge}, fail=True)
        self.assertIn('bounded regular file', self.failure()['invalid_assessment']['reason'])
        self.assertFalse(list((self.repo / '.cao/smoke-work').rglob('smoke-gate*.json')))

    def test_boolean_schema_version_is_rejected(self):
        def wrong(request, document):
            document['schema_version'] = True
            Path(request['output_path']).write_text(json.dumps(document))
        self.run_smoke({'smoke-gate': wrong, 'smoke-gate-format-1': wrong}, fail=True)
        self.assertIn('identity', self.failure()['invalid_assessment']['reason'])


if __name__ == '__main__':
    unittest.main()
