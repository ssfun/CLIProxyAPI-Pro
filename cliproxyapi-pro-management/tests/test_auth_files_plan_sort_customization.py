import importlib.util
import json
import tempfile
import unittest
from pathlib import Path


MODULE_PATH = Path(__file__).resolve().parents[1] / 'apply_customizations.py'
SPEC = importlib.util.spec_from_file_location('apply_customizations', MODULE_PATH)
assert SPEC and SPEC.loader
CUSTOMIZATIONS = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CUSTOMIZATIONS)


AUTH_FILES_PAGE_SOURCE = """import {
  sortAuthFiles,
} from '@/features/authFiles/utils';
import { buildQuotaSearchValues, matchesQuotaSearch } from '@/pro/modules/quota';

export function AuthFilesPage() {
  const normalizedFilter = normalizeProviderKey(String(filter));
  const enabledOnly = statusFilterMode === 'enabled';

  const handleStatusFilterModeChange = useCallback((nextMode: AuthFilesStatusFilterMode) => {
    setStatusFilterMode(nextMode);
    setPage(1);
  }, []);

  const sortOptions = useMemo(
    () => [
      { value: 'default', label: t('auth_files.sort_default') },
      { value: 'az', label: t('auth_files.sort_az') },
      { value: 'priority', label: t('auth_files.sort_priority') },
    ],
    [t]
  );

  const sorted = useMemo(() => sortAuthFiles(filtered, sortMode), [filtered, sortMode]);

  return (
    <AuthFilesToolbar
          sortMode={sortMode}
          sortOptions={sortOptions}
    />
  );
}
"""


UI_STATE_SOURCE = """export const AUTH_FILES_SORT_MODES = ['default', 'az', 'priority'] as const;
export type AuthFilesSortMode = (typeof AUTH_FILES_SORT_MODES)[number];
"""


class AuthFilesSortingCustomizationTest(unittest.TestCase):
    def setUp(self) -> None:
        CUSTOMIZATIONS._writes.clear()

    def test_adds_provider_scoped_sorting_and_state_fallback(self) -> None:
        hook = (
            MODULE_PATH.parent / 'overlay/src/pro/authFiles/useAuthFilesQuotaExtensions.ts'
        ).read_text()
        customizer = MODULE_PATH.read_text()

        self.assertIn("['default', 'az', 'priority', 'plan', 'quota']", customizer)
        self.assertIn('compareAuthFilesByPlanDescending', hook)
        self.assertIn('compareAuthFilesByAvailableQuotaDescending', hook)
        self.assertIn('const planSortAvailable = isAuthFilePlanSortProvider(normalizedFilter)', hook)
        self.assertIn('const quotaSortAvailable = isAuthFileQuotaSortProvider(normalizedFilter)', hook)
        self.assertIn("options.push({ value: 'plan', label: t('auth_files.sort_plan_desc') })", hook)
        self.assertIn("options.push({ value: 'quota', label: t('auth_files.sort_quota_desc') })", hook)
        self.assertIn('if (selectedSortModeAvailable) return;', hook)
        self.assertIn("setSortMode('default');", hook)
        self.assertIn("selectedSortModeAvailable ? sortMode : 'default'", hook)
        self.assertIn('compareAuthFilesByPlanDescending(a, b, quotaSearchStore)', hook)
        self.assertIn('compareAuthFilesByAvailableQuotaDescending(a, b, quotaSearchStore)', hook)
        self.assertIn("'          sortMode={effectiveSortMode}\\n'", customizer)

    def test_replaces_upstream_sorting_and_is_idempotent(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            target = Path(temp_dir)
            feature_dir = target / 'src/features/authFiles'
            feature_dir.mkdir(parents=True)
            page_path = feature_dir / 'AuthFilesPage.tsx'
            ui_state_path = feature_dir / 'uiState.ts'
            page_path.write_text(AUTH_FILES_PAGE_SOURCE)
            ui_state_path.write_text(UI_STATE_SOURCE)

            CUSTOMIZATIONS.patch_auth_files_page_sorting_latest(target)
            CUSTOMIZATIONS.flush_writes()
            CUSTOMIZATIONS.patch_auth_files_page_sorting_latest(target)
            CUSTOMIZATIONS.flush_writes()

            page = page_path.read_text()
            self.assertNotIn('  sortAuthFiles,\n', page)
            self.assertIn('const sortOptions = quotaSortOptions;', page)
            self.assertIn('sortFilesWithQuota(filtered)', page)
            self.assertIn('sortMode={effectiveSortMode}', page)
            self.assertIn("'plan', 'quota'", ui_state_path.read_text())

    def test_adds_sort_locale_labels(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            target = Path(temp_dir)
            vietnamese_path = target / 'src/i18n/locales/vi.json'
            vietnamese_path.parent.mkdir(parents=True)
            vietnamese_path.write_text(
                '{\n  "claude_quota": {\n'
                '    "seven_day_fable": "Fable 5 · 7 ngày",\n'
                '    "extra_usage_label": "Sử dụng thêm"\n  }\n}\n'
            )

            CUSTOMIZATIONS.patch_locales(target)
            CUSTOMIZATIONS.flush_writes()

            expected = {
                'en': ('Plan: High to Low', 'Available Quota: High to Low'),
                'ru': ('Тариф: по убыванию', 'Доступная квота: по убыванию'),
                'zh-CN': ('套餐从高到低', '可用额度从高到低'),
                'zh-TW': ('套餐由高到低', '可用額度由高到低'),
            }
            generated = json.loads((target / 'src/pro/locales.generated.json').read_text())
            for name, labels in expected.items():
                data = generated[name]
                self.assertEqual(labels[0], data['auth_files']['sort_plan_desc'])
                self.assertEqual(labels[1], data['auth_files']['sort_quota_desc'])
                self.assertIn('plan_x_basic', data['xai_quota'])
                self.assertIn('plan_x_premium', data['xai_quota'])
                self.assertIn('plan_x_premium_plus', data['xai_quota'])
                self.assertIn('plan_supergrok_lite', data['xai_quota'])
                self.assertIn('plan_free', data['xai_quota'])
                self.assertIn('plan_paid_unknown', data['xai_quota'])
                self.assertIn('free_quota_window', data['xai_quota'])


if __name__ == '__main__':
    unittest.main()
