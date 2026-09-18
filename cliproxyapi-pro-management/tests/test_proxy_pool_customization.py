import json
import re
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
PAGE = ROOT / 'overlay/src/pro/modules/proxyPool/ProxyPoolPage.tsx'
FEATURE_DIR = ROOT / 'overlay/src/pro/modules/proxyPool/features'
SERVICE = ROOT / 'overlay/src/pro/modules/proxyPool/proxyPool.ts'
CUSTOMIZER = ROOT / 'apply_customizations.py'
REGISTRY = ROOT / 'overlay/src/pro/registry.tsx'
MANIFEST = ROOT / 'overlay/src/pro/modules/proxyPool/manifest.tsx'
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
        self.assertIn('proxy_pool.success_rate', features)
        self.assertIn('proxy_pool.active_tunnels', features)

    def test_page_guards_unknown_state_and_escapes_transition_transform(self) -> None:
        source = PAGE.read_text()
        features = self.feature_source()
        self.assertIn('useActionBarHeightVar(actionBarRef', features)
        self.assertIn('--proxy-pool-action-bar-height', features)
        self.assertIn('createPortal(content, document.body)', features)
        self.assertIn('@/pro/shared/FloatingActionBar.module.scss', features)
        self.assertIn('proxy_pool.load_unavailable', source)
        self.assertIn('actionDisabled={!snapshot}', features)
        self.assertIn('await load(true, savedRevision)', source)
        self.assertIn('key={key}', features)
        self.assertIn('proxy_pool.discard_changes', features)

    def test_page_exposes_complete_runtime_configuration(self) -> None:
        source = PAGE.read_text()
        features = self.feature_source()
        self.assertIn('proxy_pool.health_timeout', features)
        self.assertIn('proxy_pool.test_url', features)
        self.assertIn('proxy_pool.order', features)
        self.assertIn('parseLoopbackListener', source)
        self.assertIn('isLoopbackHostname', source)
        self.assertIn('proxy_pool.validation_recursive_url', source)
        self.assertIn('maskProxyCredentials(item.proxyUrl)', features)

    def test_page_silently_resolves_missing_saved_node_locations_once(self) -> None:
        source = PAGE.read_text()

        self.assertIn('automaticLocationAttemptsRef', source)
        self.assertIn("runtimeByID.get(node.id)?.location", source)
        self.assertIn("!node.enabled || !node.url.trim()", source)
        self.assertIn("proxyPoolApi.testNode(\n                node.id,\n                node.url,", source)
        self.assertIn('if (item?.result.success) next[item.key] = item.result', source)
        self.assertIn('automaticLocationAttemptsRef.current.clear()', source)

    def test_page_uses_node_first_operational_layout(self) -> None:
        source = PAGE.read_text()
        features = self.feature_source()
        styles = (FEATURE_DIR / 'ProxyPool.module.scss').read_text()
        self.assertRegex(source, r'useState<ProxyPoolView>\([\'\"]nodes[\'\"]\)')
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
        self.assertIn('<ProFeatureTabs', source)

    def test_new_node_is_only_added_after_sheet_confirmation(self) -> None:
        source = PAGE.read_text()
        begin_start = source.index('const beginAddNode = () => {')
        begin_end = source.index('\n  };', begin_start)
        begin_block = source[begin_start:begin_end]
        apply_start = source.index('onApply={(node) => {')
        apply_end = source.index('onTest=', apply_start)
        apply_block = source[apply_start:apply_end]

        self.assertIn('setPendingNode(createProxyPoolNode(draft.nodes.length))', begin_block)
        self.assertNotIn('updateDraft', begin_block)
        self.assertIn('if (pendingNode)', apply_block)
        self.assertIn('nodes: [...current.nodes, node]', apply_block)
        self.assertIn('onClose={closeNodeSheet}', source)

    def test_proxy_credentials_remain_hideable_after_reveal(self) -> None:
        sheet = (FEATURE_DIR / 'ProxyPoolNodeSheet.tsx').read_text()
        self.assertIn('hasProxyCredentials(value.url)', sheet)
        self.assertNotIn('const hasCredentials = displayUrl !== value.url', sheet)

    def test_proxy_endpoint_rejects_uninitialized_runtime_value(self) -> None:
        helper = (FEATURE_DIR / 'proxyPoolUi.ts').read_text()
        self.assertIn("endpoint !== 'socks5://'", helper)
        for name in (
            'ProxyPoolStatusOverview.tsx',
            'ProxyPoolDiagnostics.tsx',
            'ProxyPoolTakeoverDialog.tsx',
        ):
            self.assertIn('resolveProxyPoolEndpoint(', (FEATURE_DIR / name).read_text())

    def test_save_preserves_edits_made_while_request_is_in_flight(self) -> None:
        source = PAGE.read_text()
        self.assertIn('const draftRevisionRef = useRef(0)', source)
        self.assertIn('draftRevisionRef.current += 1', source)
        self.assertIn('draftRevisionRef.current === replaceDraftRevision', source)
        self.assertIn('await load(true, savedRevision)', source)
        self.assertNotIn('await load(true, true)', source)

    def test_loads_are_ordered_and_polling_is_single_flight(self) -> None:
        source = PAGE.read_text()
        self.assertIn('const loadSequenceRef = useRef(0)', source)
        self.assertIn('const appliedLoadSequenceRef = useRef(0)', source)
        self.assertIn('const pollInFlightRef = useRef(false)', source)
        self.assertIn('const loadSequence = ++loadSequenceRef.current', source)
        self.assertIn('loadSequence < appliedLoadSequenceRef.current', source)
        self.assertIn('if (pollInFlightRef.current) return', source)
        self.assertIn('void load(true).finally', source)

    def test_takeover_confirmation_previews_the_draft_being_applied(self) -> None:
        source = (FEATURE_DIR / 'ProxyPoolTakeoverDialog.tsx').read_text()
        self.assertIn('draft.nodes.filter((node) => node.enabled).length', source)
        self.assertIn('`socks5://${draft.listen.trim()}`', source)
        self.assertIn('proxy_pool.current_runtime', source)
        self.assertIn('proxy_pool.pending_enabled_nodes', source)
        self.assertIn('proxy_pool.pending_internal_endpoint', source)

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

    def test_service_persists_native_settings_without_writing_upstream_config(self) -> None:
        source = SERVICE.read_text()
        save_start = source.index('async save(config: ProxyPoolConfig)')
        activate_start = source.index('async activate(config: ProxyPoolConfig)')
        activate_end = source.index('async deactivate(config: ProxyPoolConfig)')
        save_block = source[save_start:activate_start]
        activate_block = source[activate_start:activate_end]

        self.assertIn('/pro/proxy-pool/config', save_block)
        self.assertIn('takeoverEnabled: true', activate_block)
        self.assertIn('takeoverEnabled: false', source)
        self.assertIn('isProxyPoolListenerUrl(item.proxyUrl, config.listen)', source)
        self.assertNotIn("apiClient.put(\"/proxy-url\"", source)
        self.assertNotIn('pluginsApi', source)
        self.assertNotIn('PLUGIN_ID', source)

    def test_routes_and_navigation_are_durable_customizer_edits(self) -> None:
        source = CUSTOMIZER.read_text()
        registry = REGISTRY.read_text()
        manifest = MANIFEST.read_text()
        self.assertIn("import { proxyPoolModule } from '@/pro/modules/proxyPool'", registry)
        self.assertIn('proxyPoolModule,', registry)
        self.assertIn("path: '/proxy-pool'", manifest)
        self.assertIn('<IconSidebarProxyPool size={18} />', manifest)
        self.assertIn("import { ProBootstrap } from '@/pro/ProBootstrap'", source)
        self.assertIn("import { proNavigationGroups } from '@/pro/registry'", source)
        self.assertIn('PROXY_POOL_NAV_LOCALE_KEYS', source)


if __name__ == '__main__':
    unittest.main()
