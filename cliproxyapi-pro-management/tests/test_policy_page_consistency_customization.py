import json
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
PRO_ROOT = ROOT / 'overlay/src/pro/modules'
SHARED_ROOT = ROOT / 'overlay/src/pro/shared'
PRO_ICONS = ROOT / 'overlay/src/pro/icons.tsx'
LOCALES = ROOT / 'monitoring-locales.json'


class PolicyPageConsistencyCustomizationTest(unittest.TestCase):
    def test_optional_features_use_header_activation_actions(self) -> None:
        routing = (PRO_ROOT / 'routing/RoutingPolicyPage.tsx').read_text()
        account = (PRO_ROOT / 'oauthPolicy/OAuthPolicyPage.tsx').read_text()
        proxy_header = (PRO_ROOT / 'proxyPool/features/ProxyPoolHeader.tsx').read_text()

        self.assertNotIn('<ProFeatureHeader', routing)
        self.assertIn("navigate('/account-inspection'", routing)
        self.assertNotIn('routingPolicyApi.updateRequestProtection', routing)
        self.assertNotIn('routingPolicyApi.release', routing)
        self.assertIn('<ProFeatureHeader', account)
        self.assertIn('onToggle={toggleEnabled}', account)
        self.assertNotIn('className={styles.enabledField}', account)
        self.assertIn('<ProFeatureHeader', proxy_header)
        self.assertIn('onToggle={onTakeover}', proxy_header)

    def test_optional_feature_headers_share_copy_buttons_and_breakpoints(self) -> None:
        header = (SHARED_ROOT / 'ProFeatureHeader.tsx').read_text()
        styles = (SHARED_ROOT / 'ProFeatureHeader.module.scss').read_text()

        self.assertIn("variant={active ? 'danger' : 'primary'}", header)
        self.assertGreaterEqual(header.count('size="sm"'), 2)
        for key in ('active', 'inactive', 'start_takeover', 'stop_takeover'):
            self.assertIn(f'pro_feature_header.{key}', header)
        self.assertIn('@media (max-width: 720px)', styles)
        self.assertIn('grid-template-columns: repeat(2, minmax(0, 1fr));', styles)
        self.assertIn('@media (max-width: 480px)', styles)

    def test_optional_feature_tabs_share_proxy_management_style_and_behavior(self) -> None:
        routing = (PRO_ROOT / 'routing/RoutingPolicyPage.tsx').read_text()
        account = (PRO_ROOT / 'oauthPolicy/OAuthPolicyPage.tsx').read_text()
        proxy = (PRO_ROOT / 'proxyPool/ProxyPoolPage.tsx').read_text()
        tabs = (SHARED_ROOT / 'ProFeatureTabs.tsx').read_text()
        styles = (SHARED_ROOT / 'ProFeatureTabs.module.scss').read_text()

        for source in (routing, account, proxy):
            self.assertIn('<ProFeatureTabs', source)
        self.assertIn('role="tablist"', tabs)
        self.assertIn('role="tab"', tabs)
        self.assertIn('aria-selected={active}', tabs)
        self.assertIn('border-radius: 11px;', styles)
        self.assertIn('color: var(--primary-color);', styles)
        self.assertIn('background: color-mix(in srgb, var(--primary-color) 11%, transparent);', styles)
        self.assertIn('@media (max-width: 780px)', styles)
        self.assertIn('grid-auto-flow: column;', styles)
        self.assertIn('overflow-x: auto;', styles)
        self.assertIn('@media (max-width: 480px)', styles)

    def test_optional_feature_navigation_icons_have_distinct_product_semantics(self) -> None:
        routing = (PRO_ROOT / 'routing/manifest.tsx').read_text()
        account = (PRO_ROOT / 'oauthPolicy/manifest.tsx').read_text()
        proxy = (PRO_ROOT / 'proxyPool/manifest.tsx').read_text()
        icons = PRO_ICONS.read_text()

        self.assertIn('<IconSidebarRouting size={18} />', routing)
        self.assertIn('<IconSidebarAccountPolicy size={18} />', account)
        self.assertIn('<IconSidebarProxyPool size={18} />', proxy)
        self.assertIn("from '@/pro/icons'", routing)
        self.assertIn("from '@/pro/icons'", account)
        self.assertIn("from '@/pro/icons'", proxy)
        self.assertIn('M6 5h9l3 3-3 3H6', icons)
        self.assertIn('M5.5 16c.7-2 1.7-3 3-3s2.3 1 3 3', icons)
        self.assertIn('x="3" y="14" width="18" height="6"', icons)

    def test_floating_save_bars_only_render_for_modified_drafts(self) -> None:
        routing = (PRO_ROOT / 'routing/RoutingPolicyPage.tsx').read_text()
        account = (PRO_ROOT / 'oauthPolicy/OAuthPolicyPage.tsx').read_text()
        proxy_page = (PRO_ROOT / 'proxyPool/ProxyPoolPage.tsx').read_text()

        self.assertNotIn('isCurrentLayer && dirty', routing)
        self.assertIn('{dirty &&', account)
        self.assertIn('visible={isCurrentLayer && dirty}', proxy_page)

    def test_optional_feature_pages_guard_unsaved_proxy_drafts(self) -> None:
        proxy_page = (PRO_ROOT / 'proxyPool/ProxyPoolPage.tsx').read_text()

        self.assertIn('usePageTransitionLayer', proxy_page)
        self.assertIn('useUnsavedChangesGuard({', proxy_page)
        self.assertIn('enabled: isCurrentLayer', proxy_page)
        self.assertIn('shouldBlock: dirty', proxy_page)

    def test_status_overviews_keep_two_columns_on_narrow_screens(self) -> None:
        account_styles = (PRO_ROOT / 'oauthPolicy/OAuthPolicyPage.module.scss').read_text()
        proxy_styles = (PRO_ROOT / 'proxyPool/features/ProxyPool.module.scss').read_text()
        account_mobile = account_styles[account_styles.index('@media (max-width: 720px)'):]
        proxy_mobile = proxy_styles[proxy_styles.index('@media (max-width: 780px)'):]

        self.assertIn('grid-template-columns: repeat(2, minmax(0, 1fr));', account_mobile)
        self.assertIn('grid-template-columns: repeat(2, minmax(0, 1fr));', proxy_mobile)
        self.assertNotIn('.statusGrid > div {\n    border-right: 0;', account_mobile)

    def test_refresh_callbacks_preserve_modified_drafts_without_reload_loops(self) -> None:
        for relative in (
            'oauthPolicy/OAuthPolicyPage.tsx',
            'proxyPool/ProxyPoolPage.tsx',
        ):
            source = (PRO_ROOT / relative).read_text()
            self.assertIn('dirtyRef', source)

    def test_account_policy_provider_stats_and_tabs_use_distinct_semantics(self) -> None:
        account = (PRO_ROOT / 'oauthPolicy/OAuthPolicyPage.tsx').read_text()
        service = (PRO_ROOT / 'oauthPolicy/oauthPolicy.ts').read_text()

        self.assertIn('countOAuthPolicyProvidersWithRules(draft.providers)', account)
        self.assertIn('oauthModelProviderDefinitionsForAuthProviders(', account)
        self.assertIn('.filter((file) => !isRuntimeOnlyAuthFile(file))', account)
        self.assertIn('providerDefinitions.length === 0', account)
        self.assertIn('Object.values(provider.plans).some(({ configured }) => configured)', service)

    def test_routing_board_is_read_only(self) -> None:
        routing = (PRO_ROOT / 'routing/RoutingPolicyPage.tsx').read_text()
        service = (PRO_ROOT / 'routing/routingPolicy.ts').read_text()

        self.assertIn("navigate('/account-inspection'", routing)
        self.assertIn('buildInspectionFocusLocationState', routing)
        self.assertNotIn('updateRequestProtection', service)
        self.assertNotIn("post<SchedulingBoardRawResponse>('/routing-policy/release'", service)
        self.assertIn("get<SchedulingBoardRawResponse>('/routing-policy')", service)
        self.assertNotIn('<ProFeatureHeader', routing)
        self.assertNotIn('dirtyRef', routing)

    def test_header_and_discard_actions_cover_all_locales(self) -> None:
        locales = json.loads(LOCALES.read_text())
        routing = (PRO_ROOT / 'routing/RoutingPolicyPage.tsx').read_text()
        account = (PRO_ROOT / 'oauthPolicy/OAuthPolicyPage.tsx').read_text()
        required = {
            'routing_policy': {'title', 'subtitle', 'load_failed'},
            'oauth_policy': {
                'discard_changes',
                'providers_with_rules',
                'providers_with_rules_hint',
                'no_auth_providers',
                'no_auth_providers_hint',
            },
            'proxy_pool': {'discard_changes'},
            'pro_feature_header': {
                'active',
                'inactive',
                'start_takeover',
                'stop_takeover',
            },
        }

        for locale in ('en.json', 'ru.json', 'zh-CN.json', 'zh-TW.json'):
            for section, keys in required.items():
                self.assertTrue(keys.issubset(locales[locale][section]), f'{locale}: {section}')
            discard_labels = {
                locales[locale][section]['discard_changes']
                for section in ('oauth_policy', 'proxy_pool')
            }
            self.assertEqual(len(discard_labels), 1, f'{locale}: discard_changes')

        self.assertNotIn("t('common.disable'", routing)
        self.assertNotIn('t("common.disable"', account)
        self.assertNotIn("title={t('config_management.reload')}", routing)
        self.assertNotIn("routing_policy.discard_changes", routing)
        self.assertIn("oauth_policy.discard_changes", account)

    def test_policy_titles_match_the_product_navigation_names(self) -> None:
        locales = json.loads(LOCALES.read_text())
        expected = {
            'en.json': ('Scheduling Board', 'Account Policy'),
            'ru.json': ('Панель планирования', 'Политика аккаунтов'),
            'zh-CN.json': ('调度看板', '账号策略'),
            'zh-TW.json': ('調度看板', '帳號策略'),
        }

        for locale, (scheduling_title, account_title) in expected.items():
            self.assertEqual(scheduling_title, locales[locale]['nav']['routing_policy'])
            self.assertEqual(scheduling_title, locales[locale]['routing_policy']['title'])
            self.assertEqual(account_title, locales[locale]['oauth_policy']['title'])


if __name__ == '__main__':
    unittest.main()
