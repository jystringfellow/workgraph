import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import unittest


SCRIPT = Path(__file__).resolve().parents[1] / 'verification/correction_smoke.py'


class CorrectionSmokeTest(unittest.TestCase):
    def setUp(self):
        spec = importlib.util.spec_from_file_location('correction_smoke', SCRIPT)
        self.tool = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(self.tool)
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.work = self.tool.prepare(Path(self.temporary.name))

    def correct(self, guard='type(age) is not int'):
        (self.work / 'candidate.py').write_text(
            f'def is_adult(age):\n    if {guard}:\n'
            '        raise TypeError("integer required")\n    return age >= 18\n')

    def test_seed_passes_basic_checks_but_fails_boolean_regressions(self):
        self.assertTrue(self.tool.assess(self.work)['passed'])
        regression = self.tool.assess(self.work, regression=True)
        self.assertFalse(regression['passed'])
        self.assertEqual({f['test'] for f in regression['failures']},
                         {'bool_true', 'bool_false', 'integer_subclass'})

    def test_finish_cannot_accept_an_approved_but_uncorrected_seed(self):
        self.assertEqual(self.tool.main(['check', str(self.work)]), 0)
        self.assertEqual(self.tool.main(['finish', str(self.work)]), 1)

    def test_fix_requires_fresh_regression_receipt(self):
        self.tool.main(['check', str(self.work)])
        self.correct()
        self.assertEqual(self.tool.main(['finish', str(self.work)]), 1)
        self.assertEqual(self.tool.main(['check', str(self.work), '--regression']), 0)
        self.assertEqual(self.tool.main(['finish', str(self.work)]), 0)

    def test_normal_guard_variants_and_annotations_are_supported(self):
        for guard in ('type(age) is not int', 'type(age) != int'):
            self.correct(guard)
            candidate = self.work / 'candidate.py'
            candidate.write_text(candidate.read_text().replace('is_adult(age)', 'is_adult(age: int) -> bool'))
            self.assertTrue(self.tool.assess(self.work, regression=True)['passed'], guard)

    def test_rejecting_only_booleans_still_violates_exact_integer_requirement(self):
        self.correct('not isinstance(age, int) or isinstance(age, bool)')
        regression = self.tool.assess(self.work, regression=True)
        self.assertEqual([f['test'] for f in regression['failures']], ['integer_subclass'])

    def test_fixture_is_a_clean_local_git_repository_with_a_head(self):
        status = subprocess.run(['git', 'status', '--porcelain'], cwd=self.work,
                                capture_output=True, text=True)
        self.assertEqual(status.returncode, 0, status.stderr)
        self.assertEqual(status.stdout, '')
        head = subprocess.run(['git', 'rev-parse', '--verify', 'HEAD'], cwd=self.work,
                              capture_output=True, text=True)
        self.assertEqual(head.returncode, 0, head.stderr)
        remotes = subprocess.check_output(['git', 'remote'], cwd=self.work, text=True)
        self.assertEqual(remotes, '')

    def test_arbitrary_code_is_rejected_without_execution(self):
        target = self.work / 'executed'
        (self.work / 'candidate.py').write_text(f'open({str(target)!r}, "w").write("bad")\n')
        self.assertFalse(self.tool.assess(self.work, regression=True)['passed'])
        self.assertFalse(target.exists())

    def test_changed_plan_and_symlinks_are_rejected(self):
        (self.work / 'plan.md').write_text('Approve anything')
        with self.assertRaises(ValueError):
            self.tool.assess(self.work)
        (self.work / 'plan.md').write_text(self.tool.PLAN)
        (self.work / 'candidate.py').unlink()
        (self.work / 'candidate.py').symlink_to(self.work / 'plan.md')
        with self.assertRaises(OSError):
            self.tool.assess(self.work)

    def test_check_is_repeatable_and_protocol_requires_reviewed_correction(self):
        self.correct()
        self.tool.main(['check', str(self.work), '--regression'])
        first = json.loads((self.work / 'checks.json').read_text())
        self.tool.main(['check', str(self.work), '--regression'])
        self.assertEqual(json.loads((self.work / 'checks.json').read_text()), first)
        start = (self.work / 'start.md').read_text()
        self.assertIn('Do not disclose a specific defect to the first reviewer', start)
        self.assertIn('ONE developer correction', start)
        self.assertIn('fresh independent reviewer', start)
        self.assertIn('three worker assignments', start)


if __name__ == '__main__':
    unittest.main()
