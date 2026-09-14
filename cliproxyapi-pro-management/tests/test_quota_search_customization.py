import importlib.util
import tempfile
import unittest
from pathlib import Path


MODULE_PATH = Path(__file__).resolve().parents[1] / 'apply_customizations.py'
SPEC = importlib.util.spec_from_file_location('apply_customizations', MODULE_PATH)
assert SPEC and SPEC.loader
CUSTOMIZATIONS = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CUSTOMIZATIONS)


AUTH_FILES_PAGE_SOURCE = """import { useAuthStore, useNotificationStore, useThemeStore } from '@/stores';

export function AuthFilesPage() {
  const statusBarCache = useAuthFilesStatusBarCache(files);
  const filtered = useMemo(
    () => filesMatchingStatusFilters.filter((item) => {
        const matchType = !normalizedFilter || normalizeProviderKey(item.type) === normalizedFilter;
        return matchType && matchesAuthFileSearch(item, normalizedSearch, wildcardSearch);
      }),
    [filesMatchingStatusFilters, normalizedFilter, normalizedSearch, wildcardSearch]
  );
}
"""

QUOTA_PAGE_SOURCE = """import { EmptyState } from '@/components/ui/EmptyState';
import { readQuotaUiState, writeQuotaUiState } from './uiState';

export function QuotaPage() {
  const { t } = useTranslation();
  useEffect(() => {
    void loadFiles();
    return () => {
      listRequestRef.current += 1;
    };
  }, [loadFiles]);

  const antigravityQuota = useQuotaStore((state) => state.antigravityQuota);
  const claudeQuota = useQuotaStore((state) => state.claudeQuota);
  const codexQuota = useQuotaStore((state) => state.codexQuota);
  const devinQuota = useQuotaStore((state) => state.devinQuota);
  const kimiQuota = useQuotaStore((state) => state.kimiQuota);
  const xaiQuota = useQuotaStore((state) => state.xaiQuota);

  const quotaByType = useMemo(
    () => ({
        antigravity: antigravityQuota,
        claude: claudeQuota,
        codex: codexQuota,
        devin: devinQuota,
        kimi: kimiQuota,
        xai: xaiQuota,
      }),
    [antigravityQuota, claudeQuota, codexQuota, devinQuota, kimiQuota, xaiQuota]
  );

  const getQuota = useCallback(
    (entry) => quotaByType[entry.type][entry.file.name],
    [quotaByType]
  );

  const entries = useMemo(() => classifyQuotaFiles(files), [files]);
  const tabCounts = useMemo(() => buildTabCounts(entries), [entries]);
  const filteredEntries = useMemo(() => filterEntriesByTab(entries, tab), [entries, tab]);
  const sortedEntries = useMemo(
    () => sortQuotaEntries(filteredEntries, sortMode, resolveNextRecovery),
    [filteredEntries, sortMode, resolveNextRecovery]
  );
  const { pageItems, currentPage, totalPages } = useMemo(
    () => paginate(sortedEntries, page, QUOTA_PAGE_SIZE),
    [sortedEntries, page]
  );

  return (
    <>
        {error && (
          <div>{error}</div>
        )}
    </>
  );
}
"""


class QuotaSearchCustomizationTest(unittest.TestCase):
    def setUp(self) -> None:
        CUSTOMIZATIONS._writes.clear()

    def test_adds_search_without_pruning_hidden_quota_state(self) -> None:
        hook = (
            MODULE_PATH.parent / 'overlay/src/pro/authFiles/useAuthFilesQuotaExtensions.ts'
        ).read_text()
        customizer = MODULE_PATH.read_text()

        self.assertIn('buildQuotaSearchValues', hook)
        self.assertIn('matchesQuotaSearch', hook)
        self.assertIn('const quotaSearchStore = useMemo(', hook)
        self.assertIn('state.geminiCliQuota', hook)
        self.assertIn('matchesQuotaSearch(buildQuotaSearchValues(file, quotaSearchStore, t)', hook)
        self.assertIn('matchesQuotaMetadata(item)', customizer)
        self.assertIn('matchesQuotaMetadata, normalizedFilter', customizer)

    def test_quota_page_search_preserves_upstream_sort_pipeline(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            target = Path(temp_dir)
            feature_dir = target / 'src/features/quota'
            feature_dir.mkdir(parents=True)
            page_path = feature_dir / 'QuotaPage.tsx'
            page_path.write_text(QUOTA_PAGE_SOURCE)

            CUSTOMIZATIONS.patch_quota_page_latest(target)
            CUSTOMIZATIONS.flush_writes()

            page = page_path.read_text()
            self.assertEqual(page.count('const searchedEntries = useMemo('), 1)
            self.assertEqual(page.count('const tabCounts = useMemo('), 1)
            self.assertEqual(page.count('const filteredEntries = useMemo('), 1)
            self.assertEqual(page.count('const { pageItems, currentPage, totalPages } = useMemo('), 1)
            self.assertIn('listRequestRef.current += 1;', page)
            self.assertIn('devin: devinQuota', page)
            self.assertIn('geminiCliQuota, devinQuota, kimiQuota', page)
            self.assertIn('buildTabCounts(searchedEntries)', page)
            self.assertIn('filterEntriesByTab(searchedEntries, tab)', page)
            self.assertIn('sortQuotaEntries(filteredEntries, sortMode, resolveNextRecovery)', page)
            self.assertIn('paginate(sortedEntries, page, QUOTA_PAGE_SIZE)', page)
            self.assertLess(page.index('const entries = useMemo('), page.index('const searchedEntries = useMemo('))
            self.assertLess(page.index('const searchedEntries = useMemo('), page.index('const filteredEntries = useMemo('))

            CUSTOMIZATIONS.patch_quota_page_latest(target)
            CUSTOMIZATIONS.flush_writes()
            self.assertEqual(page, page_path.read_text())

    def test_search_placeholders_are_concise_and_match_each_page(self) -> None:
        self.assertEqual(
            {
                'en.json': 'Search name, email, note, or plan',
                'ru.json': 'Поиск по имени, почте, заметке или тарифу',
                'zh-CN.json': '搜索名称、邮箱、备注或套餐',
                'zh-TW.json': '搜尋名稱、電子郵件、備註或套餐',
            },
            CUSTOMIZATIONS.AUTH_FILES_SEARCH_PLACEHOLDER_KEYS,
        )
        quota_placeholders = {
            locale: values['search_placeholder']
            for locale, values in CUSTOMIZATIONS.QUOTA_LOCALE_KEYS.items()
        }
        self.assertEqual(
            {
                'en.json': 'Search name, email, note, or plan',
                'ru.json': 'Поиск по имени, почте, заметке или тарифу',
                'zh-CN.json': '搜索名称、邮箱、备注或套餐',
                'zh-TW.json': '搜尋名稱、電子郵件、備註或套餐',
            },
            quota_placeholders,
        )

        placeholders = [
            *CUSTOMIZATIONS.AUTH_FILES_SEARCH_PLACEHOLDER_KEYS.values(),
            *quota_placeholders.values(),
        ]
        self.assertTrue(all('auth_index' not in placeholder for placeholder in placeholders))


if __name__ == '__main__':
    unittest.main()
