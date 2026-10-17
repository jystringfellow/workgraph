import contextlib
import importlib.util
import io
import json
from pathlib import Path
import types
import unittest
from unittest.mock import MagicMock, Mock, patch


SCRIPT = Path(__file__).parents[1] / 'experiments/jev/jev_shadow.py'


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


if __name__ == '__main__':
    unittest.main()
