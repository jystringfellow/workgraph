import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


SYNC = Path(__file__).resolve().parents[1] / 'sync.sh'


class SyncTest(unittest.TestCase):
    def run_sync(self, fail_install=False, zero_exit_failure=False):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        root = Path(temporary.name)
        rig = root / '.cao'
        (rig / 'profiles').mkdir(parents=True)
        (rig / 'workflows').mkdir()
        shutil.copyfile(SYNC, rig / 'sync.sh')
        (rig / 'profiles/wg-reviewer-smoke.md').write_text(
            '---\nname: wg-reviewer-smoke\nprovider: copilot_cli\n---\nReview.\n')
        (rig / 'profiles/wg-architect-smoke.md').write_text(
            '---\nname: wg-architect-smoke\nprovider: codex\n---\nPlan.\n')
        (rig / 'profiles/wg-adversarial-reviewer.md').write_text(
            '---\nname: wg-adversarial-reviewer\nprovider: copilot_cli\n---\nReview.\n')
        (rig / 'workflows/profile_smoke.py').write_text('INPUTS = {}\n')
        executable = root / 'bin'
        executable.mkdir()
        calls = root / 'calls.jsonl'
        mock = executable / 'cao'
        mock.write_text(
            '#!/usr/bin/env python3\n'
            'import json, os, pathlib, sys\n'
            'with open(os.environ["SYNC_TEST_CALLS"], "a") as log:\n'
            '    log.write(json.dumps(sys.argv[1:]) + "\\n")\n'
            'if sys.argv[1] == "install":\n'
            '    source = pathlib.Path(sys.argv[2])\n'
            '    assert source.is_file()\n'
            '    assert sys.argv[3:] == ["--provider", "copilot_cli"]\n'
            '    if os.environ["SYNC_TEST_FAIL"] == "1":\n'
            '        sys.exit(1)\n'
            '    if os.environ["SYNC_TEST_ZERO_FAIL"] == "1":\n'
            '        print("Error: Failed to install agent")\n'
            '        sys.exit(0)\n'
            '    destination = pathlib.Path(os.environ["SYNC_TEST_NATIVE"])\n'
            '    destination.mkdir()\n'
            '    (destination / (source.stem + ".agent.md")).write_text(source.read_text())\n'
            '    print("Agent " + repr(source.stem) + " installed successfully")\n')
        mock.chmod(0o755)
        result = subprocess.run(
            ['bash', str(rig / 'sync.sh')], capture_output=True, text=True,
            env={**os.environ, 'PATH': str(executable) + os.pathsep + os.environ['PATH'],
                 'CAO_HOME_DIR': str(root / 'cao'), 'SYNC_TEST_CALLS': str(calls),
                 'SYNC_TEST_NATIVE': str(root / 'native'),
                 'SYNC_TEST_ZERO_FAIL': '1' if zero_exit_failure else '0',
                 'SYNC_TEST_FAIL': '1' if fail_install else '0'})
        return root, result, [json.loads(line) for line in calls.read_text().splitlines()]

    def test_sync_installs_declared_copilot_profile_without_launching(self):
        root, result, calls = self.run_sync()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue((root / 'native/wg-reviewer-smoke.agent.md').is_file())
        self.assertEqual([call[0] for call in calls], ['profile', 'profile', 'profile', 'workflow', 'install'])
        self.assertEqual(calls[-1][1], str(root / 'cao/agent-store/wg-reviewer-smoke.md'))

    def test_sync_does_not_report_success_when_install_fails(self):
        _, result, _ = self.run_sync(fail_install=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn('Sync and Validation Complete', result.stdout)

    def test_zero_exit_install_error_stops_sync(self):
        _, result, _ = self.run_sync(zero_exit_failure=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn('Sync and Validation Complete', result.stdout)


if __name__ == '__main__':
    unittest.main()
