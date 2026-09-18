import importlib.util
import tempfile
import unittest
from pathlib import Path


MODULE_PATH = Path(__file__).resolve().parents[1] / 'apply_customizations.py'
SPEC = importlib.util.spec_from_file_location('apply_customizations', MODULE_PATH)
assert SPEC and SPEC.loader
CUSTOMIZATIONS = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CUSTOMIZATIONS)


CONSTANTS_SOURCE = """export type QuotaProviderType = 'antigravity' | 'claude' | 'codex' | 'devin' | 'kimi' | 'xai';
export const QUOTA_PROVIDER_TYPES = new Set<QuotaProviderType>([
  'antigravity',
  'claude',
  'codex',
  'devin',
  'kimi',
  'xai',
]);
export const AUTH_FILE_MANUAL_REFRESH_PROVIDERS = new Set([
  'antigravity',
  'claude',
  'codex',
  'devin',
  'kimi',
  'xai',
]);
"""

CONSTANTS_SOURCE_WITH_META = """export type QuotaProviderType =
  'antigravity' | 'claude' | 'codex' | 'devin' | 'kimi' | 'xai' | 'meta';
export const QUOTA_PROVIDER_TYPES = new Set<QuotaProviderType>([
  'meta',
  'antigravity',
  'claude',
  'codex',
  'devin',
  'kimi',
  'xai',
]);
export const AUTH_FILE_MANUAL_REFRESH_PROVIDERS = new Set([
  'meta',
  'antigravity',
  'claude',
  'codex',
  'kimi',
  'xai',
]);
"""

QUOTA_SECTION_SOURCE = """
const quota = useQuotaStore((state) => {
    if (quotaType === 'codex') return state.codexQuota[cacheKey] as QuotaCardState | undefined;
    if (quotaType === 'devin') return state.devinQuota[cacheKey] as QuotaCardState | undefined;
    if (quotaType === 'kimi') return state.kimiQuota[cacheKey] as QuotaCardState | undefined;
});
"""


class GeminiAuthFileQuotaCustomizationTest(unittest.TestCase):
    def setUp(self) -> None:
        CUSTOMIZATIONS._writes.clear()

    def test_wires_gemini_quota_into_auth_file_cards(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            target = Path(temp_dir)
            components = target / 'src/features/authFiles/components'
            components.mkdir(parents=True)
            (target / 'src/features/authFiles/constants.ts').write_text(CONSTANTS_SOURCE)
            (components / 'AuthFileQuotaSection.tsx').write_text(QUOTA_SECTION_SOURCE)

            CUSTOMIZATIONS.patch_auth_files_gemini_quota_latest(target)
            CUSTOMIZATIONS.flush_writes()

            constants = (target / 'src/features/authFiles/constants.ts').read_text()
            section = (components / 'AuthFileQuotaSection.tsx').read_text()
            self.assertIn("'gemini-cli' | 'devin'", constants)
            self.assertEqual(constants.count("  'gemini-cli',"), 2)
            self.assertIn('state.geminiCliQuota[cacheKey]', section)

            CUSTOMIZATIONS.patch_auth_files_gemini_quota_latest(target)
            CUSTOMIZATIONS.flush_writes()
            self.assertEqual(constants, (target / 'src/features/authFiles/constants.ts').read_text())
            self.assertEqual(section, (components / 'AuthFileQuotaSection.tsx').read_text())

    def test_preserves_new_upstream_union_members_and_multiline_format(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            target = Path(temp_dir)
            components = target / 'src/features/authFiles/components'
            components.mkdir(parents=True)
            (target / 'src/features/authFiles/constants.ts').write_text(CONSTANTS_SOURCE_WITH_META)
            (components / 'AuthFileQuotaSection.tsx').write_text(QUOTA_SECTION_SOURCE)

            CUSTOMIZATIONS.patch_auth_files_gemini_quota_latest(target)
            CUSTOMIZATIONS.flush_writes()

            constants = (target / 'src/features/authFiles/constants.ts').read_text()
            self.assertIn("'codex' | 'gemini-cli' | 'devin'", constants)
            self.assertIn("| 'meta';", constants)
            self.assertEqual(constants.count("  'gemini-cli',"), 2)

            CUSTOMIZATIONS.patch_auth_files_gemini_quota_latest(target)
            CUSTOMIZATIONS.flush_writes()
            self.assertEqual(constants, (target / 'src/features/authFiles/constants.ts').read_text())


if __name__ == '__main__':
    unittest.main()
