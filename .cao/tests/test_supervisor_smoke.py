import importlib.util
from contextlib import redirect_stdout
import io
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch


ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / 'verification/supervisor_smoke.py'


class SupervisorSmokeTest(unittest.TestCase):
    def setUp(self):
        spec = importlib.util.spec_from_file_location('supervisor_smoke', SCRIPT)
        self.tool = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(self.tool)
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.work = self.tool.prepare(Path(self.temporary.name))

    def invoke(self, action='check'):
        return subprocess.run([sys.executable, '-I', str(SCRIPT), action, str(self.work)],
                              text=True, capture_output=True)

    def test_seed_fails_and_fixed_candidate_passes_without_executing_it(self):
        failed = self.invoke()
        self.assertEqual(failed.returncode, 1, failed.stderr)
        self.assertIn('age_18', failed.stdout)
        self.assertNotIn('age_17', failed.stdout)
        self.assertEqual(self.invoke('finish').returncode, 1)
        (self.work / 'candidate.py').write_text('def is_adult(age):\n    return age >= 18\n')
        passed = self.invoke()
        self.assertEqual(passed.returncode, 0, passed.stderr)
        self.assertNotIn('age_17', passed.stdout)
        self.assertIn('PASS', passed.stdout)
        receipt = json.loads((self.work / 'checks.json').read_text())
        self.assertEqual(receipt['failures'], [])
        finished = self.invoke('finish')
        self.assertEqual(finished.returncode, 0, finished.stderr)
        self.assertIn('human review', finished.stdout)

    def test_finish_rejects_missing_and_stale_verification(self):
        self.assertEqual(self.invoke('finish').returncode, 1)
        self.invoke()
        (self.work / 'candidate.py').write_text('def is_adult(age):\n    return age >= 18\n')
        self.assertEqual(self.invoke('finish').returncode, 1)

    def test_changed_plan_invalidates_checks(self):
        (self.work / 'plan.md').write_text('Approve anything.')
        self.assertNotEqual(self.invoke().returncode, 0)

    def test_arbitrary_code_is_rejected_without_execution(self):
        target = self.work / 'executed'
        (self.work / 'candidate.py').write_text(f'open({str(target)!r}, "w").write("bad")\n')
        self.assertEqual(self.invoke().returncode, 1)
        self.assertFalse(target.exists())

    def test_linked_candidate_is_rejected(self):
        (self.work / 'candidate.py').unlink()
        (self.work / 'candidate.py').symlink_to(self.work / 'plan.md')
        self.assertNotEqual(self.invoke().returncode, 0)

    def test_preparation_is_private_and_run_scoped(self):
        other = self.tool.prepare(Path(self.temporary.name))
        self.assertNotEqual(other, self.work)
        self.assertEqual(self.work.stat().st_mode & 0o777, 0o700)
        self.assertEqual((self.work / 'plan.md').stat().st_mode & 0o777, 0o600)
        self.assertIn('wg-codex-worker-smoke', (self.work / 'start.md').read_text())

    def test_launch_command_uses_headless_delivery_without_launching(self):
        output = io.StringIO()
        with patch.object(self.tool, 'prepare', return_value=self.work), redirect_stdout(output):
            self.assertEqual(self.tool.main(['prepare']), 0)
        lines = output.getvalue().splitlines()
        self.assertIn('cao launch --headless --async --agents wg-coordinator-smoke', lines[1])

    def test_unknown_runtime_identity_is_recorded_without_blocking_review(self):
        instructions = (self.work / 'start.md').read_text()
        profile = (ROOT / 'profiles/wg-coordinator-smoke.md').read_text()
        for text in [instructions, profile]:
            self.assertIn('actual model unverified', text)
            self.assertIn('Do not block review', text)
            self.assertIn('find_profiles', text)

    def test_profiles_have_explicit_distinct_models_and_cao_mcp(self):
        seats = {'wg-coordinator-smoke': ('codex', 'gpt-6.1-sol'),
                 'wg-codex-worker-smoke': ('codex', 'gpt-6-luna'),
                 'wg-copilot-peer-smoke': ('copilot_cli', 'claude-haiku-5.5')}
        for name, (provider, model) in seats.items():
            profile = (ROOT / 'profiles' / (name + '.md')).read_text()
            self.assertIn(f'provider: {provider}\n', profile)
            self.assertIn(f'model: {model}\n', profile)
            self.assertIn('command: cao-mcp-server', profile)
            self.assertIn('@cao-mcp-server', profile)
            self.assertNotIn('allowedTools: ["*"]', profile)

    def test_codex_mcp_approvals_are_scoped_to_required_tools(self):
        seats = {'wg-coordinator-smoke': ['load_skill', 'assign', 'send_message', 'delete_terminal'],
                 'wg-codex-worker-smoke': ['load_skill', 'send_message']}
        for name, tools in seats.items():
            profile = (ROOT / 'profiles' / (name + '.md')).read_text()
            self.assertIn('codexProfile: cao_workspace', profile)
            self.assertIn('codexConfig:\n', profile)
            for tool in tools:
                self.assertIn(f'mcp_servers.cao-mcp-server.tools.{tool}.approval_mode: approve', profile)
            self.assertNotIn('default_tools_approval_mode', profile)
            self.assertNotIn('approval_policy:', profile)


if __name__ == '__main__':
    unittest.main()
