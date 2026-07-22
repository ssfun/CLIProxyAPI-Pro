import unittest
from pathlib import Path


PAGE_PATH = (
    Path(__file__).resolve().parents[1]
    / 'overlay/src/pages/MonitoringCenterPage.tsx'
)
STYLE_PATH = PAGE_PATH.with_suffix('.module.scss')
REALTIME_HOOK_PATH = (
    Path(__file__).resolve().parents[1]
    / 'overlay/src/features/monitoring/hooks/useRealtimeLogData.ts'
)


class MonitoringToolbarCustomizationTest(unittest.TestCase):
    def test_monitoring_settings_button_keeps_a_stable_label_while_loading(self) -> None:
        source = PAGE_PATH.read_text()
        handler = 'onClick={() => void loadMonitoringSettings()}'
        start = source.index(handler)
        end = source.index('</button>', start)
        button = source[start:end]

        self.assertIn('disabled={isMonitoringSettingsLoading}', button)
        self.assertIn('aria-busy={isMonitoringSettingsLoading}', button)
        self.assertIn("{t('usage_stats.monitoring_settings')}", button)
        self.assertNotIn("isMonitoringSettingsLoading ? t('common.loading')", button)

    def test_realtime_logs_pause_auto_refresh_during_browsing(self) -> None:
        source = PAGE_PATH.read_text()
        hook_source = REALTIME_HOOK_PATH.read_text()

        self.assertIn("const autoRefreshPaused = page !== 1", hook_source)
        self.assertIn("|| !followEnabled", hook_source)
        self.assertIn("|| !atTop", hook_source)
        self.assertIn("|| detailsOpen", hook_source)
        self.assertIn("&& page === 1", hook_source)
        self.assertIn("&& !autoRefreshPaused", hook_source)
        self.assertIn("void refresh('top');", hook_source)
        self.assertIn("onScroll={handleRealtimeLogScroll}", source)

    def test_realtime_logs_restore_the_internal_scroll_anchor(self) -> None:
        source = PAGE_PATH.read_text()
        hook_source = REALTIME_HOOK_PATH.read_text()
        styles = STYLE_PATH.read_text()

        self.assertIn("data-realtime-row-id={row.id}", source)
        self.assertIn("pendingScrollSnapshotRef", hook_source)
        self.assertIn("anchor.getBoundingClientRect().top - wrapperRect.top - snapshot.anchorOffset", hook_source)
        self.assertIn("overflow-anchor: none;", styles)

    def test_realtime_follow_control_and_pending_update_action_are_present(self) -> None:
        source = PAGE_PATH.read_text()

        self.assertIn('role="switch"', source)
        self.assertIn("monitoring.request_events_live_follow", source)
        self.assertIn("monitoring.request_events_paused_hint", source)
        self.assertIn("monitoring.request_events_view_latest", source)

    def test_realtime_follow_refresh_does_not_change_outer_layout_height(self) -> None:
        source = PAGE_PATH.read_text()
        styles = STYLE_PATH.read_text()

        self.assertIn("pendingRealtimeEventCount > 0 && realtimeLogAutoRefreshPaused", source)
        self.assertIn("className={styles.realtimeTableShell}", source)
        self.assertIn("height: min(620px, 68vh);", styles)
        self.assertIn(".realtimeUpdateBar {\n  position: absolute;", styles)
        self.assertIn("flex-wrap: nowrap;", styles)


if __name__ == '__main__':
    unittest.main()
