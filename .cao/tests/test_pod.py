from contextlib import closing
import hashlib
import importlib.util
import json
import os
import shutil
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
    def test_handoff_stage_uses_latest_stage(self):
        self.assertEqual(pod.handoff_stage('Stage: fact development\nStage: stopped\n'),
                         'Stage: stopped')
        self.assertEqual(pod.handoff_stage('Stage: fact development\n  **Stage:** ready for human review.\n'),
                         'Stage: ready for human review.')

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
        patch.object(pod, 'api', side_effect=AssertionError('network forbidden')).start()
        self.native = self.repo / 'cao-home/copilot-agents'
        self.native.mkdir()
        patch.object(pod, 'COPILOT_AGENTS', self.native, create=True).start()

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

    def test_untracked_nested_repository_contents_affect_identity(self):
        nested = self.repo / 'sub'
        nested.mkdir()
        subprocess.check_call(['git', '-C', str(nested), 'init', '-q'])
        (nested / '.gitignore').write_text('ignored.txt\n')
        path = nested / 'file.txt'
        path.write_text('one')
        first = pod.state()
        path.write_text('two')
        self.assertNotEqual(first, pod.state())
        first = pod.state()
        (nested / 'ignored.txt').write_text('ignored input')
        self.assertEqual(first, pod.state())
        link = nested / 'link'
        link.symlink_to('file.txt')
        first = pod.state()
        link.unlink()
        link.symlink_to('missing.txt')
        self.assertNotEqual(first, pod.state())

    def test_ignored_files_are_outside_candidate_identity(self):
        first = pod.state()
        (self.home / 'ignored.txt').write_text('ignored input')
        self.assertEqual(first, pod.state())

    def test_prepare_is_private_unique_and_does_not_contact_cao(self):
        with patch.object(pod, 'api', side_effect=AssertionError('network forbidden')):
            first, second = self.prepare(), self.prepare()
        self.assertNotEqual(first, second)
        self.assertEqual(first.stat().st_mode & 0o777, 0o700)
        manifest = json.loads((first / 'manifest.json').read_text())
        self.assertEqual((manifest['max_worker_assignments'], manifest['max_check_requests']), (14, 12))
        self.assertFalse({'review_corrections', 'gate_corrections'} & set(manifest))
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
            receipt = pod.verify(root, 1, 'red', 'TestRegression', verifier=(root / 'verify.sh').read_bytes())
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
            receipt = pod.verify(root, 1, 'red', 'TestRegression', verifier=(root / 'verify.sh').read_bytes())
        self.assertFalse(receipt['expected_fact_failed'])

    def test_full_checks_use_the_prepared_verifier_after_source_is_edited(self):
        rig = self.repo / '.cao'
        (rig / 'verification').mkdir(parents=True)
        shutil.copytree(pod.RIG / 'profiles', rig / 'profiles')
        verifier = rig / 'verification/verify.sh'
        verifier.write_text('exit 7\n')
        with patch.object(pod, 'RIG', rig):
            root = self.prepare()
        verifier.write_text('exit 0\n')
        receipt = pod.verify(root, 0, verifier=(root / 'verify.sh').read_bytes())
        self.assertFalse(receipt['passed'])
        self.assertEqual(receipt['exit_code'], 7)

    def test_modified_verifier_snapshot_is_rejected_before_execution(self):
        root = self.prepare()
        (root / 'verify.sh').write_text('exit 0\n')
        with patch.object(pod, 'api') as api, patch.object(pod, 'verify') as verify:
            self.assertEqual(pod.run(root), 1)
        self.assertIn('Verifier snapshot changed', (root / 'result.json').read_text())
        api.assert_not_called()
        verify.assert_not_called()

    def test_test_success_is_recorded_when_vet_fails(self):
        root = self.prepare()
        child = Mock(returncode=1)
        child.communicate.return_value = ('go_vet: fail\ngo_test: pass\n', None)
        with self.check_process(child):
            receipt = pod.verify(root, 0, verifier=(root / 'verify.sh').read_bytes())
        self.assertFalse(receipt['passed'])
        self.assertEqual(receipt['go_vet'], 'fail')
        self.assertEqual(receipt['go_test'], 'pass')

    def timed_check(self, after_term, after_kill, time_limit=900, mode='full', fact=None):
        root = self.prepare()
        child = Mock(returncode=-9, pid=123456)
        clock = [0.0]
        kill = Mock()
        def communicate(timeout):
            clock[0] += timeout
            outcome = [None, after_term, after_kill][len(kill.call_args_list)]
            if outcome is None:
                raise subprocess.TimeoutExpired('go', timeout)
            return outcome
        child.communicate.side_effect = communicate
        with self.check_process(child), patch.object(pod.os, 'killpg', kill), \
                patch.object(pod.time, 'monotonic', side_effect=lambda: clock[0]):
            receipt = pod.verify(root, 1, mode, fact, verifier=(root / 'verify.sh').read_bytes(),
                                 time_limit=time_limit)
        return receipt, kill, clock[0]

    def test_check_timeout_terminates_only_its_process_group_and_is_not_red_evidence(self):
        import signal
        receipt, kill, _ = self.timed_check(None, ('--- FAIL: TestRegression (0s)\n', None),
                                            mode='red', fact='TestRegression')
        self.assertTrue(receipt['timed_out'])
        self.assertFalse(receipt['expected_fact_failed'])
        self.assertEqual([call.args for call in kill.call_args_list],
                         [(123456, signal.SIGTERM), (123456, signal.SIGKILL)])

    def test_unkillable_check_is_recorded_as_timed_out(self):
        receipt, kill, _ = self.timed_check(None, None)
        self.assertTrue(receipt['timed_out'])
        self.assertFalse(receipt['passed'])
        self.assertEqual(len(kill.call_args_list), 2)

    def test_check_time_limit_follows_remaining_supervision_time(self):
        receipt, kill, elapsed = self.timed_check(('', None), None, time_limit=40)
        self.assertTrue(receipt['timed_out'])
        self.assertEqual(len(kill.call_args_list), 1)
        self.assertEqual(elapsed, 55)

    def test_successful_check_finishing_after_its_time_limit_is_timed_out(self):
        root = self.prepare()
        child = Mock(returncode=0)
        clock = [0.0]
        def finish_late(timeout):
            clock[0] += 0.116
            return 'go_vet: pass\ngo_test: pass\n', None
        child.communicate.side_effect = finish_late
        with self.check_process(child), patch.object(pod.time, 'monotonic', side_effect=lambda: clock[0]):
            receipt = pod.verify(root, 1, verifier=(root / 'verify.sh').read_bytes(), time_limit=0.02)
        self.assertTrue(receipt['timed_out'])
        self.assertFalse(receipt['passed'])

    def test_each_check_wait_is_capped_by_the_remaining_time(self):
        root = self.prepare()
        child = Mock(returncode=0)
        clock = [0.0]
        waits = []
        def communicate(timeout):
            waits.append(timeout)
            clock[0] += timeout
            if len(waits) < 3:
                raise subprocess.TimeoutExpired('go', timeout)
            return 'go_vet: pass\ngo_test: pass\n', None
        child.communicate.side_effect = communicate
        with self.check_process(child), patch.object(pod.time, 'monotonic', side_effect=lambda: clock[0]):
            receipt = pod.verify(root, 1, verifier=(root / 'verify.sh').read_bytes(), time_limit=60)
        self.assertEqual(waits, [25, 25, 10])
        self.assertTrue(receipt['passed'])

    def test_edit_reverted_during_check_is_indistinguishable_from_no_edit(self):
        root = self.prepare()
        child = Mock(returncode=0)
        def edit_and_revert(**kwargs):
            original = (self.repo / 'candidate.txt').read_text()
            (self.repo / 'candidate.txt').write_text('transient edit')
            (self.repo / 'candidate.txt').write_text(original)
            return 'go_vet: pass\ngo_test: pass\n', None
        child.communicate.side_effect = edit_and_revert
        with self.check_process(child):
            receipt = pod.verify(root, 1, verifier=(root / 'verify.sh').read_bytes())
        self.assertTrue(receipt['passed'])
        self.assertFalse(receipt['changed_during_checks'])

    def test_prepared_profiles_bind_every_cao_mcp_client_to_the_runner_instance(self):
        import yaml
        root = self.prepare()
        for cfg in json.loads((root / 'seats.json').read_text()).values():
            header = (root / (cfg['name'] + '.md')).read_text().split('---', 2)[1]
            server = yaml.safe_load(header)['mcpServers']['cao-mcp-server']
            self.assertEqual(server.get('env'), {'CAO_HOME_DIR': str(self.home),
                                                 'CAO_API_HOST': pod.API_HOST, 'CAO_API_PORT': pod.API_PORT})

    def test_repository_changes_during_checks_invalidate_success(self):
        root = self.prepare()
        child = Mock(returncode=0)
        def finish(**kwargs):
            (self.repo / 'extra.txt').write_text('concurrent change')
            return 'go_vet: pass\ngo_test: pass\n', None
        child.communicate.side_effect = finish
        with self.check_process(child):
            receipt = pod.verify(root, 0, verifier=(root / 'verify.sh').read_bytes())
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
        receipt = {'passed': True, 'mode': 'full', 'candidate': candidate, 'inbox_rowid': 10}
        seats = {'reviewer': {'name': 'reviewer'}, 'gate': {'name': 'gate'}}
        actors = {'r': 'reviewer', 'g': 'gate', 'd': 'developer'}
        body = 'APPROVE ' + candidate['head'] + ' ' + candidate['fingerprint']
        callbacks = [(11, 'r', 'delivered', body), (12, 'g', 'delivered', body)]
        self.assertEqual(pod.review_evidence(receipt, candidate, callbacks, actors, seats),
                         {'reviewer': 'r', 'gate': 'g'})
        for bad in [[], callbacks[:1], [(11, 'r', 'delivered', body), (12, 'g', 'pending', body)],
                    [(11, 'd', 'delivered', body), callbacks[1]],
                    [(11, 'r', 'delivered', 'old candidate'), callbacks[1]],
                    [(10, 'r', 'delivered', body), callbacks[1]],
                    callbacks + [(13, 'g', 'pending', 'new assessment')]]:
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
        self.assertEqual(list((self.home / 'agent-store').glob('*.md')), [])

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

    def run_gate_scenario(self, assignment_limit=14, reject_delivery=False, stop_for_findings=False,
                          initial_stage=None, final_stage='Stage: ready for human review',
                          latest_gate_reply=None, callbacks_during_full=False,
                          busy_terminals=(), never_idle=False, fail_step=None, preexisting_native=False,
                          first_request='1 red TestRegression', red_succeeded=True, repeat_full_change=False,
                          unchanged_candidate=False, failed_full=False, confirming_red=False,
                          error_terminal=None, expire_while_waiting=False, ready_worker_busy=False,
                          delete_failure=None, verifier_script=None, tamper_step=None,
                          lead_provider=None, late_request=False, interrupt=False, register_lead=True,
                          commit_after_launch=False, duplicate_request=False, second_request=None):
        root = self.prepare()
        self.cli_commands = []
        self.check_options = {}
        manifest = json.loads((root / 'manifest.json').read_text())
        manifest['max_worker_assignments'] = assignment_limit
        pod.save(root / 'manifest.json', manifest)
        if verifier_script is not None:
            (root / 'verify.sh').write_bytes(verifier_script)
            manifest['verifier_sha256'] = hashlib.sha256(verifier_script).hexdigest()
            pod.save(root / 'manifest.json', manifest)
        seats = json.loads((root / 'seats.json').read_text())
        if lead_provider:
            seats['lead']['provider'] = lead_provider
            pod.save(root / 'seats.json', seats)
        if preexisting_native:
            (self.native / (seats['reviewer']['name'] + '.agent.md')).write_text('existing agent')
        (self.home / 'db').mkdir()
        database = self.home / 'db/cli-agent-orchestrator.db'
        with closing(sqlite3.connect(database)) as db, db:
            db.execute('CREATE TABLE terminals (id, agent_profile, provider, model, model_honored, tmux_session)')
            db.execute('CREATE TABLE inbox (sender_id, receiver_id, status, message)')
            for identifier, seat in [('lead', 'lead'), ('review1', 'reviewer'), ('gate1', 'gate'),
                                     ('developer', 'developer'), ('review2', 'reviewer'), ('gate2', 'gate')]:
                if seat == 'lead' and not register_lead:
                    continue
                cfg = seats[seat]
                db.execute('INSERT INTO terminals VALUES (?, ?, ?, ?, 1, ?)',
                           (identifier, cfg['name'], cfg['provider'], cfg['model'], 'cao-fake'))
            initial = pod.state()
            for sender in ('review1', 'gate1'):
                db.execute('INSERT INTO inbox VALUES (?, ?, ?, ?)',
                           (sender, 'lead', 'delivered', 'Old decision ' + json.dumps(initial)))
        stage = 'stopped' if stop_for_findings else 'gate correction fact added'
        (root / 'handoff.md').write_text((initial_stage or 'Stage: ' + stage) + '\nG1: unresolved defect\n')
        (root / 'verify.request').write_text(first_request)
        checks = []
        busy = {identifier: 0 for identifier in busy_terminals}

        clock = [100.0]
        ready = [False]
        real_verify = pod.verify

        def verification(directory, number, mode='full', fact=None, **options):
            if number and busy:
                raise AssertionError('Checks started while terminals were busy')
            checks.append((number, mode, fact))
            self.check_options[number] = options
            if verifier_script is not None and mode == 'full':
                return real_verify(directory, number, mode, fact, **options)
            passed = (mode == 'full' and not (failed_full and number == 2)) or (confirming_red and number == 2)
            receipt = {'passed': passed, 'mode': mode, 'candidate': pod.state(),
                       'expected_fact_failed': mode == 'red' and red_succeeded and number == 1}
            pod.save(directory / 'checks.json', receipt)
            if number == 2 and callbacks_during_full:
                with closing(sqlite3.connect(database)) as db, db:
                    for sender in ('review2', 'gate2'):
                        db.execute('INSERT INTO inbox VALUES (?, ?, ?, ?)',
                                   (sender, 'lead', 'delivered', 'APPROVE ' + json.dumps(pod.state())))
            return receipt

        real_run = subprocess.run
        def cli(command, **options):
            if command[0] == 'git':
                return real_run(command, **options)
            self.cli_commands.append(command)
            if 'launch' in command and commit_after_launch:
                self.git('commit', '-q', '--allow-empty', '-m', 'unexpected commit')
            if tamper_step in command:
                forged = b'printf "go_vet: pass\\ngo_test: pass\\n"\n'
                (root / 'verify.sh').write_bytes(forged)
                forged_manifest = json.loads((root / 'manifest.json').read_text())
                forged_manifest['verifier_sha256'] = hashlib.sha256(forged).hexdigest()
                pod.save(root / 'manifest.json', forged_manifest)
            if 'launch' in command and not unchanged_candidate:
                (self.repo / 'fact.txt').write_text('Regression assertion\n')
            if 'install' in command:
                (self.native / (seats['reviewer']['name'] + '.agent.md')).write_text('installed agent')
            if fail_step in command:
                return Mock(returncode=1, stdout='failed')
            output = ('Session created: cao-fake' if 'launch' in command else
                      f"Agent '{seats['reviewer']['name']}' installed successfully")
            return Mock(returncode=0, stdout=output)

        def api(method, path, **options):
            if method == 'DELETE':
                if delete_failure == 'exception':
                    raise RuntimeError('delete failed')
                return {'success': delete_failure != 'unconfirmed'}
            if method == 'POST':
                if reject_delivery:
                    return {'success': False}
                if 'request 1 ' in options['params']['message']:
                    if not unchanged_candidate:
                        (self.repo / 'candidate.txt').write_text('Corrected implementation\n')
                    following = second_request or ('2 red TestRegression' if confirming_red else '2 full')
                    if duplicate_request:
                        pending_request[0] = following
                    else:
                        (root / 'verify.request').write_text(following)
                elif (repeat_full_change or confirming_red) and 'request 2 ' in options['params']['message']:
                    if repeat_full_change:
                        (self.repo / 'candidate.txt').write_text('Another correction\n')
                    (root / 'verify.request').write_text('3 full')
                else:
                    ready[0] = True
                    (root / 'handoff.md').write_text(final_stage + '\nG1: closed\n')
                    current = pod.state()
                    with closing(sqlite3.connect(database)) as db, db:
                        for sender in (() if callbacks_during_full else ('review2', 'gate2')):
                            db.execute('INSERT INTO inbox VALUES (?, ?, ?, ?)',
                                       (sender, 'lead', 'delivered', 'APPROVE ' + json.dumps(current)))
                        if latest_gate_reply:
                            db.execute('INSERT INTO inbox VALUES (?, ?, ?, ?)',
                                       ('gate1', 'lead', 'delivered', latest_gate_reply))
                return {'success': True}
            identifier = path.rsplit('/', 1)[-1]
            if late_request and not late_clock_set[0] and path.startswith('/terminals/'):
                late_clock_set[0] = True
                clock[0] = 2440.0
            if identifier == error_terminal:
                return {'status': 'error'}
            if ready[0] and ready_worker_busy and identifier == 'developer':
                return {'status': 'processing'}
            if identifier in busy:
                if expire_while_waiting:
                    clock[0] = 2501.0
                busy[identifier] += 1
                if never_idle or busy[identifier] == 1:
                    return {'status': 'processing'}
                del busy[identifier]
            return {'status': 'ok' if path == '/health' else 'completed'}

        pending_request = [None]
        late_clock_set = [False]
        sleeps = [0]

        def sleep(seconds):
            if interrupt:
                raise KeyboardInterrupt
            clock[0] += 10
            sleeps[0] += 1
            if pending_request[0] and sleeps[0] >= 3:
                (root / 'verify.request').write_text(pending_request[0])
                pending_request[0] = None

        with patch.object(pod, 'api', side_effect=api), \
                patch.object(pod.subprocess, 'run', side_effect=cli), \
                patch.object(pod, 'verify', side_effect=verification), \
                patch.object(pod.time, 'sleep', side_effect=sleep), \
                patch.object(pod.time, 'monotonic', side_effect=lambda: clock[0]):
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

    def test_lead_launch_uses_the_lead_profile_provider(self):
        self.run_gate_scenario(lead_provider='claude_code')
        launch = next(command for command in self.cli_commands if 'launch' in command)
        self.assertEqual(launch[launch.index('--provider') + 1], 'claude_code')

    def test_host_checks_are_bounded_by_remaining_supervision_time(self):
        _, status, checks = self.run_gate_scenario(late_request=True)
        self.assertEqual(status, 0)
        self.assertEqual(self.check_options[0]['time_limit'], 900)
        self.assertEqual(self.check_options[1]['time_limit'], 60.0)

    def test_interrupt_records_interrupted_result(self):
        with self.assertRaises(KeyboardInterrupt):
            self.run_gate_scenario(interrupt=True)
        root, = (self.repo / '.cao/pod-runs').iterdir()
        result = json.loads((root / 'result.json').read_text())
        self.assertEqual(result['status'], 'interrupted')
        self.assertEqual(result['failure'], 'KeyboardInterrupt')

    def test_unregistered_lead_terminal_stops_before_checks(self):
        root, status, checks = self.run_gate_scenario(register_lead=False)
        self.assertEqual(status, 1)
        self.assertEqual(checks, [(0, 'full', None)])
        self.assertIn('Lead terminal is not registered', (root / 'result.json').read_text())

    def test_head_change_stops_the_run(self):
        root, status, checks = self.run_gate_scenario(commit_after_launch=True)
        self.assertEqual(status, 1)
        self.assertEqual(checks, [(0, 'full', None)])
        self.assertIn('Git HEAD changed', (root / 'result.json').read_text())

    def test_repeated_request_is_not_rerun(self):
        _, status, checks = self.run_gate_scenario(duplicate_request=True)
        self.assertEqual(status, 0)
        self.assertEqual(checks, [(0, 'full', None), (1, 'red', 'TestRegression'), (2, 'full', None)])

    def test_invalid_request_mid_run_stops_without_running_it(self):
        root, status, checks = self.run_gate_scenario(second_request='2 full --run everything')
        self.assertEqual(status, 1)
        self.assertEqual(checks, [(0, 'full', None), (1, 'red', 'TestRegression')])
        self.assertIn('Invalid host check request', (root / 'result.json').read_text())

    def test_lead_that_never_yields_stops_before_verification(self):
        root, status, checks = self.run_gate_scenario(busy_terminals=('lead',), never_idle=True)
        self.assertEqual(status, 1)
        self.assertEqual(checks, [(0, 'full', None)])
        self.assertIn('did not yield', (root / 'result.json').read_text())

    def test_overall_timeout_deletes_session_and_run_registrations(self):
        root, status, _ = self.run_gate_scenario(final_stage='Stage: awaiting gate')
        self.assertEqual(status, 1)
        result = json.loads((root / 'result.json').read_text())
        self.assertEqual(result['failure'], 'Overall pod time bound exhausted')
        self.assertEqual(result['bound_cleanup'], {'success': True})
        self.assertEqual(list((self.home / 'agent-store').glob('*.md')), [])
        self.assertEqual(list(self.native.iterdir()), [])

    def test_unchanged_candidate_can_repeat_full_without_red(self):
        _, status, checks = self.run_gate_scenario(first_request='1 full', unchanged_candidate=True)
        self.assertEqual(status, 0)
        self.assertEqual(checks, [(0, 'full', None), (1, 'full', None), (2, 'full', None)])

    def test_changed_candidate_full_is_left_to_lead_red_first_policy(self):
        _, status, checks = self.run_gate_scenario(first_request='1 full')
        self.assertEqual(status, 0)
        self.assertEqual(len(checks), 3)

    def test_nonfailing_red_does_not_block_full(self):
        _, status, checks = self.run_gate_scenario(red_succeeded=False)
        self.assertEqual(status, 0)
        self.assertEqual(len(checks), 3)

    def test_refactor_correction_can_repeat_full_without_new_red(self):
        _, status, checks = self.run_gate_scenario(repeat_full_change=True)
        self.assertEqual(status, 0)
        self.assertEqual(len(checks), 4)

    def test_failed_full_can_be_corrected_and_retried(self):
        _, status, checks = self.run_gate_scenario(repeat_full_change=True, failed_full=True)
        self.assertEqual(status, 0)
        self.assertEqual(checks[-2:], [(2, 'full', None), (3, 'full', None)])

    def test_confirming_green_red_does_not_block_full(self):
        _, status, checks = self.run_gate_scenario(confirming_red=True)
        self.assertEqual(status, 0)
        self.assertEqual(checks[-2:], [(2, 'red', 'TestRegression'), (3, 'full', None)])

    def test_snapshot_and_manifest_tampering_cannot_replace_full_verifier(self):
        root, status, checks = self.run_gate_scenario(
            verifier_script=b'if [ -f fact.txt ]; then exit 7; fi\n', tamper_step='launch')
        self.assertEqual(status, 1)
        self.assertEqual(len(checks), 3)
        receipt = json.loads((root / 'checks.json').read_text())
        self.assertFalse(receipt['passed'])
        self.assertEqual(receipt['exit_code'], 7)

    def test_verifier_is_pinned_before_registration_and_baseline(self):
        root, status, checks = self.run_gate_scenario(verifier_script=b'exit 7\n', tamper_step='validate')
        self.assertEqual(status, 1)
        self.assertEqual(checks, [(0, 'full', None)])
        self.assertIn('baseline verification failed', (root / 'result.json').read_text())

    def test_receipts_record_staged_unstaged_deleted_and_untracked_paths(self):
        root = self.prepare()
        (self.repo / 'candidate.txt').write_text('staged implementation')
        self.git('add', 'candidate.txt')
        (self.repo / 'plan.md').write_text('unstaged change')
        (self.repo / 'brief.md').unlink()
        (self.repo / 'fact.txt').write_text('regression assertion')
        child = Mock(returncode=1)
        child.communicate.return_value = ('--- FAIL: TestRegression (0s)\n', None)
        with self.check_process(child):
            receipt = pod.verify(root, 1, 'red', 'TestRegression', verifier=(root / 'verify.sh').read_bytes())
        self.assertEqual(receipt.get('changed_paths'), ['brief.md', 'candidate.txt', 'fact.txt', 'plan.md'])

    def test_changed_candidate_during_red_check_is_not_red_evidence(self):
        root = self.prepare()
        child = Mock(returncode=1)
        def finish(**kwargs):
            (self.repo / 'candidate.txt').write_text('concurrent edit')
            return '--- FAIL: TestRegression (0s)\n', None
        child.communicate.side_effect = finish
        with self.check_process(child):
            receipt = pod.verify(root, 1, 'red', 'TestRegression', verifier=(root / 'verify.sh').read_bytes())
        self.assertFalse(receipt['expected_fact_failed'])

    def test_retained_ready_session_keeps_registered_profiles_and_native_agent(self):
        root, status, _ = self.run_gate_scenario()
        self.assertEqual(status, 0)
        self.assertEqual(len(list((self.home / 'agent-store').glob('*.md'))), 4)
        self.assertEqual(len(list(self.native.iterdir())), 1)
        self.assertTrue((root / 'seats.json').is_file())
        self.assertTrue((root / 'handoff.md').is_file())

    def test_partial_native_install_is_cleaned_up(self):
        _, status, checks = self.run_gate_scenario(fail_step='install')
        self.assertEqual(status, 1)
        self.assertEqual(checks, [])
        self.assertEqual(list((self.home / 'agent-store').glob('*.md')), [])
        self.assertEqual(list(self.native.iterdir()), [])

    def test_launch_failure_cleans_up_run_registrations(self):
        _, status, _ = self.run_gate_scenario(fail_step='launch')
        self.assertEqual(status, 1)
        self.assertEqual(list((self.home / 'agent-store').glob('*.md')), [])
        self.assertEqual(list(self.native.iterdir()), [])

    def test_registration_cleanup_error_is_recorded_and_other_profiles_are_removed(self):
        real_unlink = Path.unlink
        def unlink(path, *args, **kwargs):
            if path.parent == self.native:
                raise PermissionError('cleanup denied')
            return real_unlink(path, *args, **kwargs)
        with patch.object(Path, 'unlink', autospec=True, side_effect=unlink):
            root, status, _ = self.run_gate_scenario(fail_step='install')
        self.assertEqual(status, 1)
        result = json.loads((root / 'result.json').read_text())
        self.assertEqual(result['registration_cleanup_errors'][0]['error'], 'PermissionError')
        self.assertEqual(list((self.home / 'agent-store').glob('*.md')), [])

    def test_existing_store_profile_is_preserved(self):
        root = self.prepare()
        seats = json.loads((root / 'seats.json').read_text())
        store = self.home / 'agent-store'
        store.mkdir()
        existing = store / (seats['lead']['name'] + '.md')
        existing.write_text('existing profile')
        with patch.object(pod, 'api', return_value={'status': 'ok'}), \
                patch.object(pod.subprocess, 'run') as cli:
            self.assertEqual(pod.run(root), 1)
        cli.assert_not_called()
        self.assertEqual(existing.read_text(), 'existing profile')

    def test_existing_native_agent_is_preserved(self):
        root, status, checks = self.run_gate_scenario(preexisting_native=True)
        self.assertEqual(status, 1)
        self.assertEqual(checks, [])
        self.assertIn('already exists', (root / 'result.json').read_text())
        self.assertEqual([p.read_text() for p in self.native.iterdir()], ['existing agent'])

    def test_checks_wait_for_lead_and_every_worker_to_yield(self):
        _, status, checks = self.run_gate_scenario(busy_terminals=('lead', 'developer', 'review1'))
        self.assertEqual(status, 0)
        self.assertEqual(len(checks), 3)

    def test_busy_worker_timeout_stops_before_verification(self):
        root, status, checks = self.run_gate_scenario(busy_terminals=('developer',), never_idle=True)
        self.assertEqual(status, 1)
        self.assertEqual(checks, [(0, 'full', None)])
        self.assertIn('did not yield', (root / 'result.json').read_text())

    def test_error_worker_has_yielded_for_checks(self):
        _, status, checks = self.run_gate_scenario(error_terminal='developer')
        self.assertEqual(status, 0)
        self.assertEqual(len(checks), 3)

    def test_overall_deadline_takes_precedence_over_yield_timeout(self):
        root, status, checks = self.run_gate_scenario(
            busy_terminals=('developer',), never_idle=True, expire_while_waiting=True)
        self.assertEqual(status, 1)
        self.assertEqual(checks, [(0, 'full', None)])
        result = json.loads((root / 'result.json').read_text())
        self.assertEqual(result['failure'], 'Overall pod time bound exhausted')
        self.assertEqual(result['bound_cleanup'], {'success': True})
        self.assertEqual(list((self.home / 'agent-store').glob('*.md')), [])
        self.assertEqual(list(self.native.iterdir()), [])

    def test_retained_stopped_session_keeps_profiles(self):
        _, status, _ = self.run_gate_scenario(stop_for_findings=True)
        self.assertEqual(status, 1)
        self.assertEqual(len(list((self.home / 'agent-store').glob('*.md'))), 4)
        self.assertEqual(len(list(self.native.iterdir())), 1)

    def assert_failed_deletion_keeps_profiles(self, failure):
        root, status, _ = self.run_gate_scenario(assignment_limit=1, delete_failure=failure)
        self.assertEqual(status, 1)
        seats = json.loads((root / 'seats.json').read_text())
        for cfg in seats.values():
            self.assertTrue((self.home / 'agent-store' / (cfg['name'] + '.md')).is_file())
        self.assertEqual(len(list(self.native.iterdir())), 1)

    def test_failed_session_deletion_keeps_profiles(self):
        self.assert_failed_deletion_keeps_profiles('exception')

    def test_unconfirmed_session_deletion_keeps_profiles(self):
        self.assert_failed_deletion_keeps_profiles('unconfirmed')

    def test_ready_waits_for_every_worker(self):
        root, status, _ = self.run_gate_scenario(ready_worker_busy=True)
        self.assertEqual(status, 1)
        self.assertIn('Overall pod time bound exhausted', (root / 'result.json').read_text())

    def test_qualified_ready_stage_does_not_accept_candidate(self):
        root, status, _ = self.run_gate_scenario(final_stage='Stage: ready for human review? not yet')
        self.assertEqual(status, 1)
        self.assertIn('Overall pod time bound exhausted', (root / 'result.json').read_text())

    def test_qualified_stopped_stage_does_not_stop_run(self):
        _, status, checks = self.run_gate_scenario(initial_stage='Stage: stopped? not yet')
        self.assertEqual(status, 0)
        self.assertEqual(len(checks), 3)

    def test_ready_stage_accepts_trailing_period(self):
        _, status, _ = self.run_gate_scenario(final_stage='Stage: ready for human review.')
        self.assertEqual(status, 0)

    def test_newer_gate_rejection_supersedes_older_candidate_callback(self):
        root, status, _ = self.run_gate_scenario(latest_gate_reply='Rejected: G1 remains unresolved')
        self.assertEqual(status, 1)
        self.assertIn('gate callback', (root / 'result.json').read_text())

    def test_callbacks_received_before_full_receipt_cannot_authorize_ready(self):
        root, status, _ = self.run_gate_scenario(callbacks_during_full=True)
        self.assertEqual(status, 1)
        self.assertIn('reviewer callback', (root / 'result.json').read_text())

    def test_fixing_failed_checks_does_not_stop_the_run(self):
        _, status, checks = self.run_gate_scenario(initial_stage='Stage: fixing failed full checks')
        self.assertEqual(status, 0)
        self.assertEqual(len(checks), 3)

    def test_negated_ready_stage_does_not_accept_candidate(self):
        root, status, _ = self.run_gate_scenario(final_stage='Stage: not ready for human review')
        self.assertEqual(status, 1)
        self.assertIn('Overall pod time bound exhausted', (root / 'result.json').read_text())

    def test_indented_bold_stage_is_recognized(self):
        _, status, _ = self.run_gate_scenario(final_stage='  **Stage:** ready for human review')
        self.assertEqual(status, 0)

    def test_indented_bold_stop_is_recognized(self):
        root, status, checks = self.run_gate_scenario(initial_stage='  **Stage:** stopped.')
        self.assertEqual(status, 1)
        self.assertEqual(len(checks), 1)
        self.assertIn('Lead stopped', (root / 'result.json').read_text())

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
