import json
import re
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
PAGE = ROOT / 'overlay/src/pro/modules/oauthPolicy/OAuthPolicyPage.tsx'
STYLE = ROOT / 'overlay/src/pro/modules/oauthPolicy/OAuthPolicyPage.module.scss'
FEATURE_HEADER = ROOT / 'overlay/src/pro/shared/ProFeatureHeader.tsx'
FEATURE_HEADER_STYLE = ROOT / 'overlay/src/pro/shared/ProFeatureHeader.module.scss'
ACTION_BAR_STYLE = ROOT / 'overlay/src/pro/shared/FloatingActionBar.module.scss'
SERVICE = ROOT / 'overlay/src/pro/modules/oauthPolicy/oauthPolicy.ts'
CUSTOMIZER = ROOT / 'apply_customizations.py'
REGISTRY = ROOT / 'overlay/src/pro/registry.tsx'
MANIFEST = ROOT / 'overlay/src/pro/modules/oauthPolicy/manifest.tsx'
LOCALES = ROOT / 'monitoring-locales.json'


class OAuthPolicyCustomizationTest(unittest.TestCase):
    def test_page_exposes_plan_settings_rules_and_fallbacks(self) -> None:
        source = PAGE.read_text()
        self.assertIn('oauthModelProviderDefinitionsForAuthProviders(', source)
        self.assertIn('providerDefinitions.map', source)
        self.assertIn('activePlanDefinitions.map', source)
        self.assertIn('normalizeOAuthModelPlanKey', source)
        self.assertIn('PatternEditor', source)
        self.assertIn('key === "_unknown"', source)
        self.assertIn("plans._default.configured", source)
        self.assertIn('isPositiveDuration', source)
        self.assertIn('DurationInput', source)
        self.assertIn('unit="m"', source)
        self.assertIn('unit="s"', source)
        self.assertIn('oauth_policy.max_stale', source)
        self.assertIn('min={1}', source)
        self.assertIn('step={1}', source)
        self.assertNotIn('step={0.1}', source)
        self.assertIn('oauthPolicyApi.save', source)
        self.assertIn('normalizeOAuthPolicyPrefix(event.target.value)', source)
        self.assertIn('configStyles.floatingActionContainer', source)
        self.assertIn('@/pro/shared/FloatingActionBar.module.scss', source)
        self.assertIn('useActionBarHeightVar', source)
        self.assertIn('createPortal(', source)
        self.assertIn('document.body', source)
        self.assertIn('filteredEffectivePolicies.map', source)
        self.assertIn('oauthPolicyApi.refreshPlanDetection', source)
        self.assertIn('next.status.refreshing', source)
        self.assertIn('item.planError', source)
        self.assertIn('effectiveProviderOptions', source)
        self.assertIn('effectivePlanOptions', source)
        self.assertIn('className={styles.patternInput}', source)
        self.assertIn('prefix_placeholder', source)
        self.assertIn('priority_placeholder', source)
        self.assertIn('weight_placeholder', source)

    def test_service_preserves_explicit_empty_rules_and_uses_native_runtime(self) -> None:
        source = SERVICE.read_text()
        for provider in ('xai', 'codex', 'claude', 'gemini-cli', 'antigravity', 'kimi'):
            self.assertIn(f'"{provider}"', source)
        self.assertIn('planDefinitionsForProvider', source)
        self.assertIn('plans[key] = {', source)
        self.assertIn('"excluded-models": normalizeModelPatterns', source)
        self.assertIn('if (!rule.configured) return', source)
        self.assertIn('/pro/oauth-policy/config', source)
        self.assertIn('/pro/oauth-policy/effective', source)
        self.assertIn('/pro/oauth-policy/refresh', source)
        self.assertIn('serializeOAuthPolicyConfig(config)', source)
        self.assertIn('apiClient.put(', source)
        self.assertNotIn('apiClient.patch(', source)
        self.assertNotIn('pluginsApi', source)
        self.assertNotIn('PLUGIN_ID', source)
        for field in ('prefix', 'priority', 'weight'):
            self.assertIn(field, source)

    def test_routes_and_navigation_are_durable(self) -> None:
        source = CUSTOMIZER.read_text()
        registry = REGISTRY.read_text()
        manifest = MANIFEST.read_text()
        self.assertIn("import { oauthPolicyModule } from '@/pro/modules/oauthPolicy'", registry)
        self.assertIn('oauthPolicyModule,', registry)
        self.assertIn("path: '/oauth-policy'", manifest)
        self.assertIn("path: '/oauth-model-policy'", manifest)
        self.assertIn('<IconSidebarAccountPolicy size={18} />', manifest)
        self.assertIn("import { proRoutes } from '@/pro/registry'", source)
        self.assertIn('...proNavigationGroups', source)
        self.assertIn('OAUTH_POLICY_NAV_LOCALE_KEYS', source)
        self.assertIn("'zh-CN.json': {'label': '账号策略'", source)

    def test_page_is_responsive_and_localized(self) -> None:
        source = PAGE.read_text()
        styles = STYLE.read_text()
        locales = json.loads(LOCALES.read_text())
        self.assertEqual('Account Policy', locales['en.json']['oauth_policy']['title'])
        self.assertEqual('账号策略', locales['zh-CN.json']['oauth_policy']['title'])
        self.assertEqual('帳號策略', locales['zh-TW.json']['oauth_policy']['title'])
        expected = set(re.findall(r"oauth_policy\.([a-z0-9_]+)", source))
        expected.discard('plan_')
        expected.discard('provider_')
        for locale in ('en.json', 'zh-CN.json', 'zh-TW.json'):
            self.assertTrue(expected.issubset(locales[locale]['oauth_policy']))
        self.assertIn('@media (max-width: 720px)', styles)
        self.assertIn('.ruleGrid', styles)
        self.assertIn('<ProFeatureTabs', source)
        self.assertIn('className={styles.providerTabBar}', source)
        self.assertIn('.customPlanRow', styles)
        self.assertIn('.durationControl', styles)
        self.assertIn('<ProFeatureHeader', source)
        self.assertIn('onToggle={toggleEnabled}', source)
        self.assertNotIn('className={styles.enabledField}', source)
        self.assertIn('grid-template-columns: repeat(2, minmax(0, 1fr))', styles)
        mobile = styles[styles.index('@media (max-width: 720px)'):]
        self.assertNotIn('.statusGrid > div {\n    border-right: 0;', mobile)
        self.assertIn('max-height: min(430px, 52vh)', styles)
        self.assertIn('position: sticky', styles)
        self.assertNotIn('grid-column: 1 / -1', styles)
        self.assertIn('repeat(auto-fit, minmax(min(220px, 100%), 1fr))', styles)
        self.assertIn('--oauth-policy-action-bar-height', styles)
        self.assertIn('.patternInput:focus', styles)

        header = FEATURE_HEADER.read_text()
        header_styles = FEATURE_HEADER_STYLE.read_text()
        self.assertIn("variant={active ? 'danger' : 'primary'}", header)
        self.assertIn('size="sm"', header)
        self.assertIn("pro_feature_header.start_takeover", header)
        self.assertIn("pro_feature_header.stop_takeover", header)
        self.assertIn('@media (max-width: 720px)', header_styles)
        self.assertIn('grid-template-columns: repeat(2, minmax(0, 1fr))', header_styles)
        self.assertIn('@media (max-width: 480px)', header_styles)

    def test_shared_action_bar_is_owned_by_the_pro_overlay(self) -> None:
        source = ACTION_BAR_STYLE.read_text()
        for class_name in (
            '.floatingActionContainer',
            '.floatingActionList',
            '.floatingStatus',
            '.floatingStatusCompact',
            '.floatingActionButton',
            '.dirtyDot',
            '.modified',
            '.saved',
            '.error',
        ):
            self.assertIn(class_name, source)

    def test_save_preserves_edits_made_while_request_is_in_flight(self) -> None:
        source = PAGE.read_text()
        self.assertIn('const draftRevisionRef = useRef(0)', source)
        self.assertIn('draftRevisionRef.current += 1', source)
        self.assertIn('const savedRevision = draftRevisionRef.current', source)
        self.assertIn('draftRevisionRef.current === savedRevision', source)


if __name__ == '__main__':
    unittest.main()
