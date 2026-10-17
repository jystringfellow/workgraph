import importlib.util
import re
import unittest
from pathlib import Path


spec = importlib.util.spec_from_file_location('copilot_compat', Path(__file__).parents[1] / 'verification/copilot_footer_compat.py')
compat = importlib.util.module_from_spec(spec)
spec.loader.exec_module(compat)


class CopilotFooterCompatibilityTest(unittest.TestCase):
    def test_download_banner_is_the_missing_prefix(self):
        self.assertIsNone(re.search(compat.OLD_PATTERN, compat.FOOTER.strip().lower()))
        self.assertIsNotNone(re.search(compat.NEW_PATTERN, compat.FOOTER.strip().lower()))
        self.assertIsNotNone(re.search(compat.NEW_PATTERN, '← open sidebar · autopilot · / commands'))
        self.assertIsNone(re.search(compat.NEW_PATTERN, 'unexpected output · / commands'))

    def test_patch_changes_only_the_supported_line_and_is_idempotent(self):
        source = 'before\nCOPILOT_HINT_BAR_PATTERN = r"' + compat.OLD_PATTERN + '"\nafter\n'
        updated = compat.patch_source(source)
        self.assertEqual(updated, 'before\nCOPILOT_HINT_BAR_PATTERN = r"' + compat.NEW_PATTERN + '"\nafter\n')
        self.assertEqual(compat.patch_source(updated), updated)

    def test_unexpected_or_duplicate_source_is_rejected(self):
        line = 'COPILOT_HINT_BAR_PATTERN = r"' + compat.OLD_PATTERN + '"\n'
        for source in ('unrelated source', line + line):
            with self.subTest(source=source), self.assertRaises(ValueError):
                compat.patch_source(source)
