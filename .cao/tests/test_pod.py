import importlib.util
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import tempfile
import unittest
from unittest.mock import Mock, patch


spec = importlib.util.spec_from_file_location('pod', Path(__file__).parents[1] / 'pod.py')
pod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(pod)


class PodTest(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.repo = Path(temporary.name).resolve()
        self.home = self.repo / 'cao-home'
        self.home.mkdir()
        (self.repo / '.gitignore').write_text('cao-home/\n.cao/pod-runs/\n')
        (self.repo / 'AGENTS.md').write_text('Follow the plan.\n')
        (self.repo / 'plan.md').write_text('Implement the requested behavior.\n')
        (self.repo / 'brief.md').write_text('Review the existing candidate against plan.md.\n')
        (self.repo / 'candidate.txt').write_text('initial implementation\n')
        for args in [('init', '-q'), ('config', 'user.name', 'Test'),
                     ('config', 'user.email', 'test@example.invalid'), ('add', '.'),
                     ('commit', '-qm', 'initial')]:
            self.git(*args)
        self.addCleanup(patch.stopall)
        patch.object(pod, 'REPO', self.repo).start()
        patch.object(pod, 'HOME', self.home).start()

    def git(self, *args):
        return subprocess.check_output(['git', '-c', 'core.hooksPath=/dev/null',
                                        '-c', 'commit.gpgsign=false', *args], cwd=self.repo)

    def prepare(self):
        return pod.prepare(self.repo / 'brief.md', ['plan.md'])

    def check_process(self, child):
        real_popen = subprocess.Popen
        return patch.object(pod.subprocess, 'Popen', side_effect=lambda command, **options:
                            child if command[0] in ('go', 'bash') else real_popen(command, **options))

    def test_state_captures_staged_unstaged_untracked_and_committed_changes(self):
        initial = pod.state()
        (self.repo / 'plan.md').write_text('Changed plan.\n')
        unstaged = pod.state()
        self.assertNotEqual(initial, unstaged)
        self.git('add', 'plan.md')
        staged = pod.state()
        self.assertNotEqual(unstaged, staged)
        (self.repo / 'new.txt').write_text('Untracked change.\n')
        self.assertNotEqual(staged, pod.state())
        self.git('add', '.')
        self.git('commit', '-qm', 'correction')
        self.assertNotEqual(initial['head'], pod.state()['head'])

    def test_noop_and_changed_then_reverted_have_identical_effective_state(self):
        initial = pod.state()
        self.assertEqual(initial, pod.state())
        path = self.repo / 'plan.md'
        original = path.read_bytes()
        path.write_text('Temporary change.\n')
        self.assertNotEqual(initial, pod.state())
        path.write_bytes(original)
        self.assertEqual(initial, pod.state())

    def test_untracked_content_and_symlink_targets_affect_identity(self):
        path = self.repo / 'extra.txt'
        path.write_text('one')
        first = pod.state()
        path.write_text('two')
        self.assertNotEqual(first, pod.state())
        link = self.repo / 'link'
        link.symlink_to('extra.txt')
        first = pod.state()
        link.unlink()
        link.symlink_to('plan.md')
        self.assertNotEqual(first, pod.state())

    def test_prepare_is_private_unique_and_does_not_contact_cao(self):
        with patch.object(pod, 'api', side_effect=AssertionError('network forbidden')):
            first, second = self.prepare(), self.prepare()
        self.assertNotEqual(first, second)
        self.assertEqual(first.stat().st_mode & 0o777, 0o700)
        self.assertEqual(json.loads((first / 'manifest.json').read_text())['review_corrections'], 3)
        self.assertEqual(json.loads((first / 'manifest.json').read_text())['gate_corrections'], 2)
        seats = json.loads((first / 'seats.json').read_text())
        self.assertEqual(set(seats), {'lead', 'developer', 'reviewer', 'gate'})
        self.assertNotIn('astra', json.dumps(seats))
        self.assertFalse((self.home / 'agent-store').exists())
        pod.check_plan(json.loads((first / 'plan-state.json').read_text())['sources'])

    def test_plan_changes_and_outside_repository_sources_are_rejected(self):
        root = self.prepare()
        (self.repo / 'plan.md').write_text('Different requirements.\n')
        with self.assertRaisesRegex(RuntimeError, 'Plan sources changed'):
            pod.check_plan(json.loads((root / 'plan-state.json').read_text())['sources'])
        with self.assertRaises(ValueError):
            pod.prepare(self.repo / 'brief.md', ['../outside.md'])

    def test_requests_are_allowlisted_sequential_and_bounded(self):
        self.assertEqual(pod.parse_request('1 red TestRegression', 0, 12), (1, 'red', 'TestRegression'))
        self.assertEqual(pod.parse_request('2 full', 1, 12), (2, 'full', None))
        for text, completed in [('1 full', 1), ('3 full', 0), ('13 full', 12),
                                ('1 red', 0), ('1 full TestOther', 0),
                                ('1 red TestX;touch file', 0), ('1 full\n2 full', 0)]:
            with self.subTest(text=text), self.assertRaises(ValueError):
                pod.parse_request(text, completed, 12)

    def test_verification_records_only_compact_failures_with_test_umask(self):
        root = self.prepare()
        child = Mock(returncode=1)
        child.communicate.return_value = ('--- FAIL: TestRegression (0s)\n    fact.go:42: private payload\n', None)
        with self.check_process(child) as launch:
            receipt = pod.verify(root, 1, 'red', 'TestRegression')
        self.assertTrue(receipt['expected_fact_failed'])
        self.assertEqual(receipt['failure_locations'], ['fact.go:42'])
        self.assertNotIn('private payload', (root / '01-checks.json').read_text())
        check = next(call for call in launch.call_args_list if call.args[0][0] == 'go')
        self.assertEqual(check.kwargs['umask'], 0o022)
        self.assertEqual(check.args[0][-1], '^TestRegression$')

    def test_build_failure_is_not_a_red_fact(self):
        root = self.prepare()
        child = Mock(returncode=1)
        child.communicate.return_value = ('FAIL package [build failed]\n', None)
        with self.check_process(child):
            self.assertFalse(pod.verify(root, 1, 'red', 'TestRegression')['expected_fact_failed'])

    def test_test_success_is_recorded_when_vet_fails(self):
        root = self.prepare()
        child = Mock(returncode=1)
        child.communicate.return_value = ('go_vet: fail\ngo_test: pass\n', None)
        with self.check_process(child):
            receipt = pod.verify(root, 0)
        self.assertFalse(receipt['passed'])
        self.assertEqual(receipt['go_vet'], 'fail')
        self.assertEqual(receipt['go_test'], 'pass')

    def test_check_timeout_terminates_only_its_process_group_and_is_not_red_evidence(self):
        import signal
        root = self.prepare()
        child = Mock(returncode=-signal.SIGKILL, pid=123456)
        child.communicate.side_effect = [subprocess.TimeoutExpired('go', 25),
                                         subprocess.TimeoutExpired('go', 15),
                                         ('--- FAIL: TestRegression (0s)\n', None)]
        with self.check_process(child), patch.object(pod.os, 'killpg') as kill, \
                patch.object(pod.time, 'monotonic', side_effect=[0, 901]):
            receipt = pod.verify(root, 1, 'red', 'TestRegression')
        self.assertTrue(receipt['timed_out'])
        self.assertFalse(receipt['expected_fact_failed'])
        self.assertEqual([call.args for call in kill.call_args_list],
                         [(123456, signal.SIGTERM), (123456, signal.SIGKILL)])

    def test_repository_changes_during_checks_invalidate_success(self):
        root = self.prepare()
        child = Mock(returncode=0)
        def finish(**kwargs):
            (self.repo / 'extra.txt').write_text('concurrent change')
            return 'go_vet: pass\ngo_test: pass\n', None
        child.communicate.side_effect = finish
        with self.check_process(child):
            receipt = pod.verify(root, 0)
        self.assertFalse(receipt['passed'])
        self.assertTrue(receipt['changed_during_checks'])

    def test_session_bindings_are_not_forwarded_to_checks(self):
        with patch.dict(os.environ, {key: 'private' for key in (
                'WORKGRAPH_AI_SESSION_ID', 'WORKGRAPH_AI_HOME', 'WORKGRAPH_AI_DATABASE', 'PYTHONPATH')}):
            env = pod.clean_env()
        for key in ('WORKGRAPH_AI_SESSION_ID', 'WORKGRAPH_AI_HOME', 'WORKGRAPH_AI_DATABASE', 'PYTHONPATH'):
            self.assertNotIn(key, env)

    def test_ready_requires_current_full_checks_and_both_independent_callbacks(self):
        candidate = pod.state()
        receipt = {'passed': True, 'mode': 'full', 'candidate': candidate}
        seats = {'reviewer': {'name': 'reviewer'}, 'gate': {'name': 'gate'}}
        actors = {'r': 'reviewer', 'g': 'gate', 'd': 'developer'}
        body = 'APPROVE ' + candidate['head'] + ' ' + candidate['fingerprint']
        callbacks = [('r', 'delivered', body), ('g', 'delivered', body)]
        self.assertEqual(pod.review_evidence(receipt, candidate, callbacks, actors, seats),
                         {'reviewer': 'r', 'gate': 'g'})
        for bad in [[], callbacks[:1], [('r', 'delivered', body), ('g', 'pending', body)],
                    [('d', 'delivered', body), callbacks[1]],
                    [('r', 'delivered', 'old candidate'), callbacks[1]]]:
            with self.subTest(callbacks=bad), self.assertRaises(RuntimeError):
                pod.review_evidence(receipt, candidate, bad, actors, seats)
        for bad_receipt in [dict(receipt, passed=False), dict(receipt, mode='red'),
                            dict(receipt, candidate={'head': 'old', 'fingerprint': 'old'})]:
            with self.assertRaises(RuntimeError):
                pod.review_evidence(bad_receipt, candidate, callbacks, actors, seats)

    def test_failed_preflight_retains_evidence_and_cannot_be_replayed(self):
        root = self.prepare()
        with patch.object(pod, 'api', side_effect=RuntimeError('CAO unavailable')):
            self.assertEqual(pod.run(root), 1)
        result = (root / 'result.json').read_text()
        self.assertEqual(json.loads(result)['failure'], 'CAO unavailable')
        with self.assertRaises(FileExistsError):
            pod.run(root)
        self.assertEqual(result, (root / 'result.json').read_text())

    def test_invalid_run_configuration_still_retains_failure_artifact(self):
        root = self.prepare()
        (root / 'manifest.json').write_text('invalid JSON')
        with patch.object(pod, 'api') as api:
            self.assertEqual(pod.run(root), 1)
        api.assert_not_called()
        self.assertEqual(json.loads((root / 'result.json').read_text())['status'], 'stopped')

    def test_provider_validation_failure_never_launches_or_retries(self):
        root = self.prepare()
        with patch.object(pod, 'api', return_value={'status': 'ok'}), \
                patch.object(pod.subprocess, 'run', return_value=Mock(returncode=1)) as calls, \
                patch.object(pod, 'verify') as verify:
            self.assertEqual(pod.run(root), 1)
        self.assertEqual(calls.call_count, 1)
        verify.assert_not_called()
        self.assertIn('Invalid profile for seat', (root / 'result.json').read_text())

    def test_engineering_failure_does_not_launch_or_fallback(self):
        root = self.prepare()
        success = Mock(returncode=0, stdout='')
        seats = json.loads((root / 'seats.json').read_text())
        success.stdout = f"Agent '{seats['reviewer']['name']}' installed successfully"
        with patch.object(pod, 'api', return_value={'status': 'ok'}), \
                patch.object(pod.subprocess, 'run', return_value=success) as calls, \
                patch.object(pod, 'verify', return_value={'passed': False}):
            self.assertEqual(pod.run(root), 1)
        self.assertFalse(any('launch' in call.args[0] for call in calls.call_args_list))
        self.assertIn('baseline verification failed', (root / 'result.json').read_text())

    def run_gate_scenario(self, assignment_limit=14, reject_delivery=False, stop_for_findings=False):
        root = self.prepare()
        manifest = json.loads((root / 'manifest.json').read_text())
        manifest['max_worker_assignments'] = assignment_limit
        pod.save(root / 'manifest.json', manifest)
        seats = json.loads((root / 'seats.json').read_text())
        (self.home / 'db').mkdir()
        database = self.home / 'db/cli-agent-orchestrator.db'
        with sqlite3.connect(database) as db:
            db.execute('CREATE TABLE terminals (id, agent_profile, provider, model, model_honored, tmux_session)')
            db.execute('CREATE TABLE inbox (sender_id, receiver_id, status, message)')
            for identifier, seat in [('lead', 'lead'), ('review1', 'reviewer'), ('gate1', 'gate'),
                                     ('developer', 'developer'), ('review2', 'reviewer'), ('gate2', 'gate')]:
                cfg = seats[seat]
                db.execute('INSERT INTO terminals VALUES (?, ?, ?, ?, 1, ?)',
                           (identifier, cfg['name'], cfg['provider'], cfg['model'], 'cao-fake'))
            initial = pod.state()
            for sender in ('review1', 'gate1'):
                db.execute('INSERT INTO inbox VALUES (?, ?, ?, ?)',
                           (sender, 'lead', 'delivered', 'Old decision ' + json.dumps(initial)))
        stage = 'stopped; correction budget exhausted' if stop_for_findings else 'gate correction fact added'
        (root / 'handoff.md').write_text('Stage: ' + stage + '\nG1: unresolved defect\n')
        (root / 'verify.request').write_text('1 red TestRegression')
        checks = []

        def verification(directory, number, mode='full', fact=None):
            checks.append((number, mode, fact))
            if number == 1:
                (self.repo / 'fact.txt').write_text('Regression assertion\n')
            if number == 2:
                (self.repo / 'candidate.txt').write_text('Corrected implementation\n')
            receipt = {'passed': mode == 'full', 'mode': mode, 'candidate': pod.state(),
                       'expected_fact_failed': mode == 'red'}
            pod.save(directory / 'checks.json', receipt)
            return receipt

        real_run = subprocess.run
        def cli(command, **options):
            if command[0] == 'git':
                return real_run(command, **options)
            output = ('Session created: cao-fake' if 'launch' in command else
                      f"Agent '{seats['reviewer']['name']}' installed successfully")
            return Mock(returncode=0, stdout=output)

        def api(method, path, **options):
            if method == 'POST':
                if reject_delivery:
                    return {'success': False}
                if 'request 1 ' in options['params']['message']:
                    (root / 'verify.request').write_text('2 full')
                else:
                    (root / 'handoff.md').write_text('Stage: ready for human review\nG1: closed\n')
                    current = pod.state()
                    with sqlite3.connect(database) as db:
                        for sender in ('review2', 'gate2'):
                            db.execute('INSERT INTO inbox VALUES (?, ?, ?, ?)',
                                       (sender, 'lead', 'delivered', 'APPROVE ' + json.dumps(current)))
                return {'success': True}
            return {'status': 'ok' if path == '/health' else 'completed'}

        with patch.object(pod, 'api', side_effect=api), \
                patch.object(pod.subprocess, 'run', side_effect=cli), \
                patch.object(pod, 'verify', side_effect=verification), \
                patch.object(pod.time, 'sleep'), \
                patch.object(pod.time, 'monotonic', side_effect=range(0, 1000, 10)):
            status = pod.run(root)
        return root, status, checks

    def test_gate_correction_gets_host_checks_and_fresh_candidate_bound_callbacks(self):
        root, status, checks = self.run_gate_scenario()
        self.assertEqual(status, 0)
        self.assertEqual(checks, [(0, 'full', None), (1, 'red', 'TestRegression'), (2, 'full', None)])
        result = json.loads((root / 'result.json').read_text())
        self.assertEqual(result['verdict_terminals'], {'reviewer': 'review2', 'gate': 'gate2'})
        self.assertTrue(result['verdicts_require_human_inspection'])
        self.assertEqual(result['candidate'], pod.state())

    def test_overall_assignment_bound_stops_before_another_check(self):
        root, status, checks = self.run_gate_scenario(assignment_limit=1)
        self.assertEqual(status, 1)
        self.assertEqual(checks, [(0, 'full', None)])
        self.assertIn('assignment bound exceeded', (root / 'result.json').read_text())
        self.assertIn('bound_cleanup', json.loads((root / 'result.json').read_text()))

    def test_unconfirmed_receipt_delivery_stops_without_retry(self):
        root, status, checks = self.run_gate_scenario(reject_delivery=True)
        self.assertEqual(status, 1)
        self.assertEqual(checks, [(0, 'full', None), (1, 'red', 'TestRegression')])
        self.assertIn('did not confirm host receipt delivery', (root / 'result.json').read_text())

    def test_exhausted_corrections_keep_final_findings(self):
        root, status, checks = self.run_gate_scenario(stop_for_findings=True)
        self.assertEqual(status, 1)
        self.assertEqual(checks, [(0, 'full', None)])
        self.assertIn('G1: unresolved defect', (root / 'handoff.md').read_text())
        self.assertIn('unresolved findings are in handoff.md', (root / 'result.json').read_text())


if __name__ == '__main__':
    unittest.main()
