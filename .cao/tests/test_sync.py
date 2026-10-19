import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import sys
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

    def test_reference_codex_profiles_use_per_profile_config_files(self):
        for name, sandbox in (('cao_readonly', 'read-only'), ('cao_workspace', 'workspace-write')):
            config = tomllib.loads((SYNC.parent / f'codex/{name}.config.toml').read_text())
            self.assertEqual(config, {'sandbox_mode': sandbox})

    @unittest.skipUnless(shutil.which('codex'), 'codex CLI not installed')
    def test_installed_codex_accepts_reference_profiles_offline(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        home = Path(temporary.name)
        references = sorted((SYNC.parent / 'codex').glob('*.config.toml'))
        self.assertEqual([r.name for r in references], ['cao_readonly.config.toml', 'cao_workspace.config.toml'])
        for reference in references:
            shutil.copyfile(reference, home / reference.name)
        for name in ('cao_readonly', 'cao_workspace'):
            result = subprocess.run(['codex', '--profile', name, 'mcp', 'list'], cwd=home,
                                    capture_output=True, text=True, timeout=60,
                                    env={**os.environ, 'CODEX_HOME': str(home)})
            self.assertEqual(result.returncode, 0, result.stderr)

    def run_sync(self, fail_install=False, zero_exit_failure=False, arguments=(), invalid='',
                 existing_store=None, line_ending='\n', reviewer_provider='provider: copilot_cli',
                 lead_body_bytes=0, file_size_blocks=None, root=None):
        if root is None:
            temporary = tempfile.TemporaryDirectory()
            self.addCleanup(temporary.cleanup)
            root = Path(temporary.name)
            self.write_rig(root, existing_store, line_ending, reviewer_provider, lead_body_bytes)
        rig = root / '.cao'
        executable = root / 'bin'
        calls = root / 'calls.jsonl'
        prefix = ['bash', '-c', f'ulimit -f {file_size_blocks}; exec bash "$0"'] if file_size_blocks else ['bash']
        result = subprocess.run(
            [*prefix, str(rig / 'sync.sh'), *arguments], capture_output=True, text=True,
            env={**os.environ, 'PATH': str(executable) + os.pathsep + os.environ['PATH'],
                 'CAO_HOME_DIR': str(root / 'cao'), 'SYNC_TEST_CALLS': str(calls),
                 'CAO_PYTHON': sys.executable,
                 'SYNC_TEST_NATIVE': str(root / 'native'), 'SYNC_TEST_INVALID': invalid,
                 'SYNC_TEST_ZERO_FAIL': '1' if zero_exit_failure else '0',
                 'SYNC_TEST_FAIL': '1' if fail_install else '0'})
        return root, result, [json.loads(line) for line in calls.read_text().splitlines()] if calls.exists() else []

    def write_rig(self, root, existing_store, line_ending, reviewer_provider, lead_body_bytes):
        rig = root / '.cao'
        (rig / 'profiles').mkdir(parents=True)
        shutil.copyfile(SYNC, rig / 'sync.sh')
        for seat in ('lead', 'developer', 'reviewer', 'gate'):
            provider = reviewer_provider if seat == 'reviewer' else 'provider: codex'
            body = 'Pod seat.\n' + ('x' * 79 + '\n') * (lead_body_bytes // 80 if seat == 'lead' else 0)
            text = f'---\nname: wg-pod-{seat}\n{provider}\n---\n{body}provider: copilot_cli\n'
            (rig / f'profiles/wg-pod-{seat}.md').write_bytes(text.replace('\n', line_ending).encode())
        for name, content in (existing_store or {}).items():
            (root / 'cao/agent-store').mkdir(parents=True, exist_ok=True)
            (root / 'cao/agent-store' / name).write_text(content)
        executable = root / 'bin'
        executable.mkdir()
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

    def installed_agents(self, root):
        return sorted(p.name for p in (root / 'native').iterdir()) if (root / 'native').exists() else []

    def test_provider_tolerates_inline_comments_quoted_keys_and_folded_scalars(self):
        for line in ('provider: copilot_cli # review seat', '"provider": copilot_cli',
                     "'provider': 'copilot_cli'", 'provider: >-\n  copilot_cli'):
            with self.subTest(line=line):
                root, result, _ = self.run_sync(reviewer_provider=line)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(self.installed_agents(root), ['wg-pod-reviewer.agent.md'])

    def test_unparseable_front_matter_stops_before_copying(self):
        root, result, calls = self.run_sync(reviewer_provider='provider: [copilot_cli')
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((root / 'cao/agent-store').exists())
        self.assertNotIn('install', [call[0] for call in calls])

    def test_interrupted_copy_leaves_no_partial_profile_and_retry_succeeds(self):
        root, failed, _ = self.run_sync(lead_body_bytes=8000, file_size_blocks=4)
        self.assertNotEqual(failed.returncode, 0)
        store = root / 'cao/agent-store'
        self.assertFalse((store / 'wg-pod-lead.md').exists())
        self.assertEqual(sorted(p.name for p in store.iterdir()), ['wg-pod-developer.md', 'wg-pod-gate.md'])
        _, retried, _ = self.run_sync(root=root)
        self.assertEqual(retried.returncode, 0, retried.stderr)
        self.assertEqual((store / 'wg-pod-lead.md').read_bytes(),
                         (root / '.cao/profiles/wg-pod-lead.md').read_bytes())
        self.assertEqual(len(list(store.iterdir())), 4)

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
