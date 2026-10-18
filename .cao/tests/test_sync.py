import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import tomllib
import unittest


SYNC = Path(__file__).resolve().parents[1] / 'sync.sh'


class SyncTest(unittest.TestCase):
    def test_active_catalog_contains_only_current_pod_seats(self):
        profiles = SYNC.parent / 'profiles'
        self.assertEqual(sorted(path.stem for path in profiles.glob('wg-*.md')),
                         ['wg-pod-developer', 'wg-pod-gate', 'wg-pod-lead', 'wg-pod-reviewer'])

    def profile(self, seat):
        import yaml
        return yaml.safe_load((SYNC.parent / f'profiles/wg-pod-{seat}.md').read_text().split('---', 2)[1])

    def test_gate_seat_is_read_only_and_lead_cannot_auto_delete_terminals(self):
        gate = self.profile('gate')
        self.assertEqual(gate['codexProfile'], 'cao_readonly')
        self.assertFalse({'apply_patch', 'write_stdin'} & set(gate['allowedTools']))
        lead_approvals = self.profile('lead')['codexConfig']
        self.assertNotIn('mcp_servers.cao-mcp-server.tools.delete_terminal.approval_mode', lead_approvals)
        reference = tomllib.loads((SYNC.parent / 'codex-profiles.toml').read_text())['profiles']
        self.assertEqual(reference['cao_readonly']['sandbox_mode'], 'read-only')
        self.assertEqual(reference['cao_workspace']['sandbox_mode'], 'workspace-write')

    def run_sync(self, fail_install=False, zero_exit_failure=False, arguments=(), invalid='',
                 existing_store=None, line_ending='\n'):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        root = Path(temporary.name)
        rig = root / '.cao'
        (rig / 'profiles').mkdir(parents=True)
        shutil.copyfile(SYNC, rig / 'sync.sh')
        for seat in ('lead', 'developer', 'reviewer', 'gate'):
            provider = 'copilot_cli' if seat == 'reviewer' else 'codex'
            text = f'---\nname: wg-pod-{seat}\nprovider: {provider}\n---\nPod seat.\nprovider: copilot_cli\n'
            (rig / f'profiles/wg-pod-{seat}.md').write_bytes(text.replace('\n', line_ending).encode())
        for name, content in (existing_store or {}).items():
            (root / 'cao/agent-store').mkdir(parents=True, exist_ok=True)
            (root / 'cao/agent-store' / name).write_text(content)
        executable = root / 'bin'
        executable.mkdir()
        calls = root / 'calls.jsonl'
        mock = executable / 'cao'
        mock.write_text(
            '#!/usr/bin/env python3\n'
            'import json, os, pathlib, sys\n'
            'with open(os.environ["SYNC_TEST_CALLS"], "a") as log:\n'
            '    log.write(json.dumps(sys.argv[1:]) + "\\n")\n'
            'if sys.argv[1:3] == ["profile", "validate"]:\n'
            '    if pathlib.Path(sys.argv[3]).stem == os.environ["SYNC_TEST_INVALID"]:\n'
            '        sys.exit(1)\n'
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
            '    destination.mkdir(exist_ok=True)\n'
            '    (destination / (source.stem + ".agent.md")).write_text(source.read_text())\n'
            '    print("Agent " + repr(source.stem) + " installed successfully")\n')
        mock.chmod(0o755)
        result = subprocess.run(
            ['bash', str(rig / 'sync.sh'), *arguments], capture_output=True, text=True,
            env={**os.environ, 'PATH': str(executable) + os.pathsep + os.environ['PATH'],
                 'CAO_HOME_DIR': str(root / 'cao'), 'SYNC_TEST_CALLS': str(calls),
                 'SYNC_TEST_NATIVE': str(root / 'native'), 'SYNC_TEST_INVALID': invalid,
                 'SYNC_TEST_ZERO_FAIL': '1' if zero_exit_failure else '0',
                 'SYNC_TEST_FAIL': '1' if fail_install else '0'})
        return root, result, [json.loads(line) for line in calls.read_text().splitlines()] if calls.exists() else []

    def test_sync_installs_declared_copilot_profile_without_launching(self):
        root, result, calls = self.run_sync()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue((root / 'native/wg-pod-reviewer.agent.md').is_file())
        self.assertEqual([call[0] for call in calls], ['profile'] * 4 + ['install'])
        self.assertEqual(calls[-1][1], str(root / 'cao/agent-store/wg-pod-reviewer.md'))
        self.assertEqual(sorted(p.stem for p in (root / 'cao/agent-store').glob('*.md')),
                         ['wg-pod-developer', 'wg-pod-gate', 'wg-pod-lead', 'wg-pod-reviewer'])
        self.assertFalse((root / 'cao/workflows').exists())

    def test_invalid_profile_stops_before_copying_or_installing(self):
        root, result, calls = self.run_sync(invalid='wg-pod-gate')
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn('install', [call[0] for call in calls])
        self.assertFalse((root / 'cao/agent-store').exists())
        self.assertNotIn('Sync and Validation Complete', result.stdout)

    def test_differing_store_profile_is_not_overwritten(self):
        root, result, calls = self.run_sync(existing_store={'wg-pod-lead.md': 'operator edit\n'})
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual((root / 'cao/agent-store/wg-pod-lead.md').read_text(), 'operator edit\n')
        self.assertFalse((root / 'cao/agent-store/wg-pod-developer.md').exists())
        self.assertNotIn('install', [call[0] for call in calls])

    def test_identical_store_profile_is_accepted(self):
        identical = '---\nname: wg-pod-lead\nprovider: codex\n---\nPod seat.\nprovider: copilot_cli\n'
        _, result, _ = self.run_sync(existing_store={'wg-pod-lead.md': identical})
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_provider_is_read_from_crlf_front_matter_only(self):
        root, result, calls = self.run_sync(line_ending='\r\n')
        self.assertEqual(result.returncode, 0, result.stderr)
        installs = [call for call in calls if call[0] == 'install']
        self.assertEqual([Path(call[1]).stem for call in installs], ['wg-pod-reviewer'])

    def test_unknown_option_does_not_modify_installation(self):
        root, result, calls = self.run_sync(arguments=('--all',))
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(calls, [])
        self.assertFalse((root / 'cao').exists())

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
