import json
import re
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
PAGE = ROOT / 'overlay/src/pages/ProxyPoolPage.tsx'
FEATURE_DIR = ROOT / 'overlay/src/features/proxyPool'
SERVICE = ROOT / 'overlay/src/services/api/proxyPool.ts'
CUSTOMIZER = ROOT / 'apply_customizations.py'
LOCALES = ROOT / 'monitoring-locales.json'


class ProxyPoolCustomizationTest(unittest.TestCase):
    @staticmethod
    def feature_source() -> str:
        return '\n'.join(path.read_text() for path in sorted(FEATURE_DIR.glob('*.tsx')))

    def test_page_exposes_takeover_nodes_health_and_bypass_surfaces(self) -> None:
        source = PAGE.read_text()
        features = self.feature_source()
        self.assertIn('proxyPoolApi.activate', source)
        self.assertIn('proxyPoolApi.deactivate', source)
        self.assertIn('snapshot.bypassCredentials', features)
        self.assertIn('onTestAll', features)
        self.assertIn('parseProxyPoolImport', features)
        self.assertIn('proxyPoolApi.recoverNode', source)
        self.assertIn("t('proxy_pool.success_rate'", features)
        self.assertIn("t('proxy_pool.active_tunnels'", features)

    def test_page_guards_unknown_state_and_escapes_transition_transform(self) -> None:
        source = PAGE.read_text()
        features = self.feature_source()
        self.assertIn("useActionBarHeightVar(actionBarRef, '--proxy-pool-action-bar-height', visible)", features)
        self.assertIn('createPortal(content, document.body)', features)
        self.assertIn("import configStyles from '@/pages/ConfigPage.module.scss'", features)
        self.assertIn("t('proxy_pool.load_unavailable'", source)
        self.assertIn('disabled={loading || !snapshot?.pluginDiscovered}', features)
        self.assertIn('await load(true, true)', source)
        self.assertIn('key={key}', features)
        self.assertIn("t('proxy_pool.discard_changes'", features)

    def test_page_exposes_complete_runtime_configuration(self) -> None:
        source = PAGE.read_text()
        features = self.feature_source()
        self.assertIn("t('proxy_pool.health_timeout'", features)
        self.assertIn("t('proxy_pool.test_url'", features)
        self.assertIn("t('proxy_pool.order'", features)
        self.assertIn('parseLoopbackListener', source)
        self.assertIn("key: 'proxy_pool.validation_recursive_url'", source)
        self.assertIn('maskProxyCredentials(item.proxyUrl)', features)

    def test_page_uses_node_first_operational_layout(self) -> None:
        source = PAGE.read_text()
        features = self.feature_source()
        styles = (FEATURE_DIR / 'ProxyPool.module.scss').read_text()
        self.assertIn("useState<ProxyPoolView>('nodes')", source)
        self.assertIn('ProxyPoolNodeManager', source)
        self.assertIn('ProxyPoolNodeSheet', source)
        self.assertIn('ProxyPoolImportModal', source)
        self.assertIn('selected.size > 0', features)
        self.assertIn('confirm_import_count', features)
        self.assertIn('@media (max-width: 780px)', styles)
        self.assertIn('.mobileCards', styles)
        self.assertIn('.quickToggleControl', styles)
        self.assertIn('.durationControl', styles)
        self.assertIn('DurationInput', features)
        self.assertIn('formatProxyPoolSuccessRate', features)

    def test_new_node_is_only_added_after_sheet_confirmation(self) -> None:
        source = PAGE.read_text()
        begin_start = source.index('const beginAddNode = () => {')
        begin_end = source.index('\n  };', begin_start)
        begin_block = source[begin_start:begin_end]
        apply_start = source.index('onApply={(node) => {')
        apply_end = source.index('\n              }}', apply_start)
        apply_block = source[apply_start:apply_end]

        self.assertIn('setPendingNode(createProxyPoolNode(draft.nodes.length))', begin_block)
        self.assertNotIn('updateDraft', begin_block)
        self.assertIn('if (pendingNode)', apply_block)
        self.assertIn('nodes: [...current.nodes, node]', apply_block)
        self.assertIn('onClose={closeNodeSheet}', source)

    def test_proxy_pool_locales_cover_page_keys(self) -> None:
        locales = json.loads(LOCALES.read_text())
        source = PAGE.read_text() + self.feature_source()
        expected = set(re.findall(r"proxy_pool\.([a-z0-9_]+)", source))
        expected.discard('state_')
        expected.update({'state_unknown', 'state_healthy', 'state_degraded', 'state_isolated', 'state_disabled'})
        for locale in ('en.json', 'zh-CN.json', 'zh-TW.json'):
            self.assertTrue(expected.issubset(locales[locale]['proxy_pool']))

    def test_proxy_management_title_is_consistent_with_navigation(self) -> None:
        locales = json.loads(LOCALES.read_text())
        source = CUSTOMIZER.read_text()
        expected = {
            'en.json': 'Proxy Management',
            'zh-CN.json': '代理管理',
            'zh-TW.json': '代理管理',
        }
        for locale, title in expected.items():
            self.assertEqual(locales[locale]['proxy_pool']['title'], title)
            self.assertIn(f"'{locale}': {{'label': '{title}'", source)

    def test_service_enables_plugin_before_global_proxy_takeover(self) -> None:
        source = SERVICE.read_text()
        save_start = source.index('async save(config: ProxyPoolConfig, preserveTakeover = false)')
        activate_start = source.index('async activate(config: ProxyPoolConfig)')
        activate_end = source.index('async deactivate(config: ProxyPoolConfig)')
        save_block = source[save_start:activate_start]
        activate_block = source[activate_start:activate_end]

        self.assertIn("document.setIn(['plugins', 'enabled'], true)", source)
        self.assertIn('pluginsApi.updateEnabled(PROXY_POOL_PLUGIN_ID, true)', save_block)
        self.assertIn("if (preserveTakeover)", save_block)
        self.assertIn("apiClient.put('/proxy-url', { value: status.proxyUrl })", save_block)
        self.assertIn('isProxyPoolListenerUrl(item.proxyUrl, config.listen)', source)
        self.assertLess(activate_block.index('this.save(nextConfig)'), activate_block.index("apiClient.put('/proxy-url'"))
        self.assertIn("apiClient.put('/proxy-url', { value: config.restoreProxyUrl.trim() })", source)

    def test_routes_and_navigation_are_durable_customizer_edits(self) -> None:
        source = CUSTOMIZER.read_text()
        self.assertIn("import { ProxyPoolPage } from '@/pages/ProxyPoolPage'", source)
        self.assertIn("path: '/proxy-pool'", source)
        self.assertIn('IconSidebarProxyPool', source)
        self.assertIn('PROXY_POOL_NAV_LOCALE_KEYS', source)


if __name__ == '__main__':
    unittest.main()
