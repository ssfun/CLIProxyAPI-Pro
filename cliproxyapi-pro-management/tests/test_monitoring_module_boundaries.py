import unittest
from pathlib import Path


OVERLAY_ROOT = Path(__file__).resolve().parents[1] / 'overlay/src'
PAGE_PATH = OVERLAY_ROOT / 'pages/MonitoringCenterPage.tsx'


class MonitoringModuleBoundariesTest(unittest.TestCase):
    def test_page_remains_a_controller_instead_of_reabsorbing_extracted_logic(self) -> None:
        source = PAGE_PATH.read_text()

        self.assertLess(len(source.splitlines()), 2300)
        self.assertNotIn('const buildUsageTrendAnalytics =', source)
        self.assertNotIn('const buildRealtimeLogPageRows =', source)
        self.assertNotIn('const normalizeRealtimeLogColumns =', source)
        self.assertNotIn('function RealtimeCostCell(', source)
        self.assertNotIn('function UsageTrendPanel(', source)
        self.assertNotIn('function MonitoringHealthStatusBar(', source)
        self.assertNotIn('function AccountStatsPanel(', source)
        self.assertNotIn('open={isMonitoringSettingsOpen}', source)
        self.assertNotIn('open={isPriceModalOpen}', source)

    def test_extracted_modules_own_their_domain_contracts(self) -> None:
        analytics = (OVERLAY_ROOT / 'features/monitoring/monitoringAnalytics.ts').read_text()
        realtime = (OVERLAY_ROOT / 'features/monitoring/realtimeLogPresentation.ts').read_text()
        preferences = (OVERLAY_ROOT / 'features/monitoring/realtimeLogPreferences.ts').read_text()
        health = (OVERLAY_ROOT / 'features/monitoring/accountHealth.ts').read_text()

        self.assertIn('export const buildUsageTrendAnalytics', analytics)
        self.assertIn('export const buildRealtimeLogPageRows', realtime)
        self.assertIn('export type RealtimeLogRow', realtime)
        self.assertIn('export const normalizeRealtimeLogColumns', preferences)
        self.assertIn('export const buildAccountStatusData', health)

    def test_monitoring_feature_owns_shared_styles(self) -> None:
        feature_root = OVERLAY_ROOT / 'features/monitoring'
        self.assertTrue((feature_root / 'monitoring.module.scss').is_file())
        self.assertFalse((OVERLAY_ROOT / 'pages/MonitoringCenterPage.module.scss').exists())
        for component in (feature_root / 'components').glob('*.tsx'):
            self.assertNotIn("@/pages/MonitoringCenterPage.module.scss", component.read_text())


if __name__ == '__main__':
    unittest.main()
