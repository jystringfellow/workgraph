import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import types
import unittest
from unittest.mock import MagicMock, Mock, patch


SCRIPT = Path(__file__).parents[1] / 'experiments/jev/jev_shadow.py'
COLLECTOR = Path(__file__).parents[1] / 'experiments/jev/collect_change_metadata.py'


class JevShadowTest(unittest.TestCase):
    def load(self):
        sdk = types.ModuleType('typesafe_sdk')
        sdk.Choice = Mock()
        sdk.Noul = Mock()
        sdk.TypeSafeClient = MagicMock()
        spec = importlib.util.spec_from_file_location('jev_shadow', SCRIPT)
        module = importlib.util.module_from_spec(spec)
        with patch.dict('sys.modules', {'typesafe_sdk': sdk}):
            spec.loader.exec_module(module)
        return module, sdk

    def test_missing_key_never_calls_sdk(self):
        module, sdk = self.load()
        output = io.StringIO()
        with patch.dict('os.environ', {}, clear=True), contextlib.redirect_stdout(output):
            with self.assertRaises(SystemExit):
                module.main()
        sdk.TypeSafeClient.assert_not_called()
        self.assertEqual(json.loads(output.getvalue())['status'], 'error')

    def test_shadow_output_is_non_authoritative(self):
        module, sdk = self.load()
        client = sdk.TypeSafeClient.return_value.__enter__.return_value
        answer = types.SimpleNamespace(choice='low', confidence=0.7,
                                       probabilities={'low': 0.7}, noul=0.2)
        client.system_one.return_value = types.SimpleNamespace(
            model='fake-model', answers={key: answer for key in
                                        ('change_risk', 'review_focus', 'needs_deep_review')})
        output = io.StringIO()
        state = {'change': {'files_changed': 1}}
        with patch.dict('os.environ', {'TYPESAFE_API_KEY': 'fake-test-key'}), \
                patch('sys.stdin', io.StringIO(json.dumps(state))), contextlib.redirect_stdout(output):
            module.main()
        result = json.loads(output.getvalue())
        self.assertFalse(result['authoritative'])
        self.assertEqual(result['mode'], 'shadow')
        self.assertEqual(client.system_one.call_args.kwargs['state'], state)
        self.assertNotIn('fake-test-key', output.getvalue())

    def test_provider_exception_does_not_echo_private_request_details(self):
        module, sdk = self.load()
        sdk.TypeSafeClient.return_value.__enter__.side_effect = RuntimeError('private request details')
        output = io.StringIO()
        with patch.dict('os.environ', {'TYPESAFE_API_KEY': 'fake-test-key'}), \
                patch('sys.stdin', io.StringIO('{}')), contextlib.redirect_stdout(output):
            with self.assertRaises(SystemExit):
                module.main()
        self.assertNotIn('private request details', output.getvalue())
        self.assertEqual(json.loads(output.getvalue())['error'], 'Jev request failed: RuntimeError')

    def test_incomplete_provider_response_fails_visibly(self):
        module, sdk = self.load()
        client = sdk.TypeSafeClient.return_value.__enter__.return_value
        client.system_one.return_value = types.SimpleNamespace(model='fake-model', answers={})
        output = io.StringIO()
        with patch.dict('os.environ', {'TYPESAFE_API_KEY': 'fake-test-key'}), \
                patch('sys.stdin', io.StringIO('{}')), contextlib.redirect_stdout(output):
            with self.assertRaises(SystemExit):
                module.main()
        result = json.loads(output.getvalue())
        self.assertEqual(result['status'], 'error')
        self.assertEqual(result['error'], 'Jev response incomplete: KeyError')


class CollectChangeMetadataTest(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.repo = Path(temporary.name).resolve()
        (self.repo / 'specs').mkdir()
        (self.repo / 'internal').mkdir()
        (self.repo / 'specs/feature.md').write_text('one\ntwo\n')
        (self.repo / 'internal/code.go').write_text('package internal\n')
        (self.repo / '.gitignore').write_text('ignored.txt\n')
        for args in [('init', '-q'), ('add', '.'),
                     ('-c', 'user.name=Test', '-c', 'user.email=test@example.invalid',
                      '-c', 'commit.gpgsign=false', 'commit', '-qm', 'initial')]:
            subprocess.run(['git', *args], cwd=self.repo, check=True)

    def collect(self, cwd):
        result = subprocess.run([sys.executable, '-I', str(COLLECTOR)], cwd=cwd,
                                capture_output=True, text=True, check=True)
        return json.loads(result.stdout)

    def test_metadata_covers_the_whole_repository_from_a_subdirectory(self):
        (self.repo / 'specs/feature.md').write_text('one\nchanged\nthree\n')
        (self.repo / 'internal/code_test.go').write_text('secret source contents\n')
        (self.repo / 'ignored.txt').write_text('ignored\n')
        change = self.collect(self.repo / 'internal')
        self.assertEqual(change['change']['paths'], ['internal/code_test.go', 'specs/feature.md'])
        self.assertEqual((change['change']['tracked_files_changed'], change['change']['untracked_files']), (1, 1))
        self.assertEqual((change['change']['tracked_insertions'], change['change']['tracked_deletions']), (2, 1))
        self.assertEqual(change['signals'], {'specs_changed': True, 'tests_changed': True})
        self.assertNotIn('secret source contents', json.dumps(change))

    def test_outside_a_repository_fails_without_output(self):
        outside = tempfile.TemporaryDirectory()
        self.addCleanup(outside.cleanup)
        result = subprocess.run([sys.executable, '-I', str(COLLECTOR)], cwd=outside.name,
                                capture_output=True, text=True, env={**os.environ, 'GIT_CEILING_DIRECTORIES': outside.name})
        self.assertEqual(result.returncode, 1)
        self.assertEqual(result.stdout, '')
        self.assertIn('unable to determine repository root', result.stderr)


if __name__ == '__main__':
    unittest.main()
