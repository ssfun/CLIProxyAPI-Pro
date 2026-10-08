import importlib.util
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location('customizations_v125', ROOT / 'apply_customizations.py')
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class ManagementV125CustomizationTests(unittest.TestCase):
    def tearDown(self):
        MODULE._writes.clear()

    def test_native_visual_config_is_not_projected_to_legacy_paths(self):
        source = (ROOT / 'apply_customizations.py').read_text()
        self.assertNotIn('patch_visual_config_layout', source)
        self.assertFalse((ROOT / 'overlay/src/utils/visualConfigLayout.ts').exists())
        contract = (ROOT.parent / 'scripts/validation/contracts/management-upstream-modified-files.txt').read_text()
        self.assertNotIn('src/hooks/useVisualConfig.ts', contract)

    def test_connection_probe_uses_pro_transport_without_downgrading_credentials(self):
        source = (ROOT / 'overlay/src/pro/authFiles/connectionTestApi.ts').read_text()
        self.assertIn("proApiClient.post<AuthFileConnectionTestResponse>('/auth-files/test'", source)
        self.assertNotIn("apiClient.post<AuthFileConnectionTestResponse>('/auth-files/test'", source)
        self.assertIn('/credentials/models?name=', source)
        self.assertIn('&purpose=connection-test', source)
        generator = (ROOT / 'apply_customizations.py').read_text()
        self.assertNotIn("target / 'src/services/api/authFiles.ts'", generator)
        contract = (ROOT.parent / 'scripts/validation/contracts/management-upstream-modified-files.txt').read_text()
        self.assertNotIn('src/services/api/authFiles.ts', contract)

    def test_cooldown_hint_assertion_is_escaped_and_idempotent(self):
        with tempfile.TemporaryDirectory() as directory:
            target = Path(directory)
            path = target / 'tests/authFileCooldowns.test.ts'
            path.parent.mkdir()
            path.write_text("expect(available).toContain(i18n.t('auth_files.cooldown_reset_hint'));\n")
            MODULE.patch_cooldown_hint_test(target)
            MODULE.flush_writes()
            first = path.read_text()
            self.assertIn('Bun.escapeHTML', first)
            MODULE.patch_cooldown_hint_test(target)
            MODULE.flush_writes()
            self.assertEqual(first, path.read_text())

    def test_cooldown_hint_patch_rejects_missing_or_duplicate_anchors(self):
        for count in (0, 2):
            with self.subTest(count=count), tempfile.TemporaryDirectory() as directory:
                target = Path(directory)
                path = target / 'tests/authFileCooldowns.test.ts'
                path.parent.mkdir()
                original = "expect(available).toContain(i18n.t('auth_files.cooldown_reset_hint'));\n" * count
                path.write_text(original)
                with self.assertRaisesRegex(RuntimeError, f'found {count}'):
                    MODULE.patch_cooldown_hint_test(target)
                self.assertEqual(original, path.read_text())
