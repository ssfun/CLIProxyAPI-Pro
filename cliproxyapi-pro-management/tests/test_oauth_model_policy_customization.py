import json
import re
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
PAGE = ROOT / 'overlay/src/pages/OAuthModelPolicyPage.tsx'
STYLE = ROOT / 'overlay/src/pages/OAuthModelPolicyPage.module.scss'
SERVICE = ROOT / 'overlay/src/services/api/oauthModelPolicy.ts'
CUSTOMIZER = ROOT / 'apply_customizations.py'
LOCALES = ROOT / 'monitoring-locales.json'


class OAuthModelPolicyCustomizationTest(unittest.TestCase):
    def test_page_exposes_plan_settings_rules_and_fallbacks(self) -> None:
        source = PAGE.read_text()
        self.assertIn('OAUTH_MODEL_PROVIDER_DEFINITIONS.map', source)
        self.assertIn('activePlanDefinitions.map', source)
        self.assertIn('normalizeOAuthModelPlanKey', source)
        self.assertIn('PatternEditor', source)
        self.assertIn('key === "_unknown"', source)
        self.assertIn("plans._default.configured", source)
        self.assertIn('isPositiveDuration', source)
        self.assertIn('DurationInput', source)
        self.assertIn('unit="m"', source)
        self.assertIn('unit="s"', source)
        self.assertIn('min="1"', source)
        self.assertIn('step="1"', source)
        self.assertNotIn('step="0.1"', source)
        self.assertIn('oauthModelPolicyApi.save', source)
        self.assertIn('configStyles.floatingActionContainer', source)
        self.assertIn('useActionBarHeightVar', source)
        self.assertIn('createPortal(', source)
        self.assertIn('document.body', source)

    def test_service_preserves_explicit_empty_rules_and_enables_runtime(self) -> None:
        source = SERVICE.read_text()
        for provider in ('xai', 'codex', 'claude', 'gemini-cli', 'antigravity', 'kimi'):
            self.assertIn(f'"{provider}"', source)
        self.assertIn('planDefinitionsForProvider', source)
        self.assertIn('plans[key] = {', source)
        self.assertIn('"excluded-models": normalizeModelPatterns', source)
        self.assertIn('if (!rule.configured) return', source)
        self.assertIn('pluginsApi.patchConfig', source)
        self.assertIn('pluginsApi.updateEnabled(OAUTH_MODEL_POLICY_PLUGIN_ID, true)', source)
        self.assertIn('document.setIn(["plugins", "enabled"], true)', source)
        self.assertIn('waitForRegistration', source)

    def test_routes_and_navigation_are_durable(self) -> None:
        source = CUSTOMIZER.read_text()
        self.assertIn("import { OAuthModelPolicyPage } from '@/pages/OAuthModelPolicyPage'", source)
        self.assertIn("path: '/oauth-model-policy'", source)
        self.assertIn('oauthModelPolicy: <IconModelCluster', source)
        self.assertIn('OAUTH_MODEL_POLICY_NAV_LOCALE_KEYS', source)
        self.assertIn("'zh-CN.json': {'label': '模型策略'", source)

    def test_page_is_responsive_and_localized(self) -> None:
        source = PAGE.read_text()
        styles = STYLE.read_text()
        locales = json.loads(LOCALES.read_text())
        expected = set(re.findall(r"oauth_model_policy\.([a-z0-9_]+)", source))
        expected.discard('plan_')
        expected.discard('provider_')
        for locale in ('en.json', 'zh-CN.json', 'zh-TW.json'):
            self.assertTrue(expected.issubset(locales[locale]['oauth_model_policy']))
        self.assertIn('@media (max-width: 720px)', styles)
        self.assertIn('.ruleGrid', styles)
        self.assertIn('.providerTabs', styles)
        self.assertIn('.customPlanRow', styles)
        self.assertIn('.durationControl', styles)
        self.assertIn('--oauth-model-policy-action-bar-height', styles)


if __name__ == '__main__':
    unittest.main()
