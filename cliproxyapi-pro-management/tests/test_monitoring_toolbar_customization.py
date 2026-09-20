import json
import unittest
from pathlib import Path


PAGE_PATH = (
    Path(__file__).resolve().parents[1]
    / 'overlay/src/pro/modules/monitoring/MonitoringCenterPage.tsx'
)
DATA_PAGE_PATH = (
    Path(__file__).resolve().parents[1]
    / 'overlay/src/pro/modules/dataManagement/DataManagementPage.tsx'
)
STYLE_PATH = (
    Path(__file__).resolve().parents[1]
    / 'overlay/src/pro/modules/monitoring/features/monitoring.module.scss'
)
STYLE_DIR = STYLE_PATH.parent / 'styles'
BASE_STYLE_PATH = STYLE_DIR / '_base.scss'
REALTIME_HOOK_PATH = (
    Path(__file__).resolve().parents[1]
    / 'overlay/src/pro/modules/monitoring/features/hooks/useRealtimeLogData.ts'
)
REALTIME_PREFERENCES_PATH = (
    Path(__file__).resolve().parents[1]
    / 'overlay/src/pro/modules/monitoring/features/realtimeLogPreferences.ts'
)
WEBDAV_DIALOG_PATH = (
    Path(__file__).resolve().parents[1]
    / 'overlay/src/pro/modules/monitoring/features/components/WebDAVRestoreDialog.tsx'
)
ACCOUNT_PLAN_PATH = (
    Path(__file__).resolve().parents[1]
    / 'overlay/src/pro/modules/quota/accountPlan.ts'
)
COST_CELL_PATH = (
    Path(__file__).resolve().parents[1]
    / 'overlay/src/pro/modules/monitoring/features/components/RealtimeCostCell.tsx'
)
USAGE_MODEL_PATH = (
    Path(__file__).resolve().parents[1]
    / 'overlay/src/pro/modules/monitoring/features/usage.ts'
)
LOCALES_PATH = Path(__file__).resolve().parents[1] / 'monitoring-locales.json'


def read_monitoring_styles() -> str:
    return '\n'.join(path.read_text() for path in sorted(STYLE_DIR.glob('*.scss')))


class MonitoringToolbarCustomizationTest(unittest.TestCase):
    def test_backup_and_restore_actions_move_to_data_management(self) -> None:
        source = PAGE_PATH.read_text()
        data_page = DATA_PAGE_PATH.read_text()

        self.assertNotIn('aria-controls="monitoring-import-menu"', source)
        self.assertNotIn('onClick={handleImportFromFileClick}', source)
        self.assertIn("navigate('/data-management')", source)
        self.assertIn('dataManagementApi.previewRestore', data_page)
        self.assertIn('dataManagementApi.restore', data_page)

    def test_restore_preview_is_a_data_management_workspace(self) -> None:
        data_page = DATA_PAGE_PATH.read_text()

        self.assertIn('<ProWorkspaceSheet', data_page)
        self.assertIn('restorePreview?.domains.map', data_page)
        self.assertIn('data_management.restore_no_api_keys', data_page)
        self.assertIn('data_management.integrity_verified', data_page)

    def test_realtime_duration_combines_ttft_and_total_latency(self) -> None:
        source = PAGE_PATH.read_text()

        self.assertNotIn("    ttft: {", source)
        self.assertIn("t('monitoring.realtime_duration_ttft')", source)
        self.assertIn("t('monitoring.realtime_duration_total')", source)
        self.assertIn("className={styles.realtimeDurationCell}", source)
        duration_cell = source[source.index("className={styles.realtimeDurationCell}"):]
        total_duration = duration_cell[duration_cell.index("t('monitoring.realtime_duration_total')"):]
        total_value_end = total_duration.index('formatDurationMs(row.latencyMs')
        self.assertLess(total_duration.index('<small className={'), total_value_end)
        self.assertNotIn('<strong className={', total_duration[:total_value_end])

    def test_simplified_chinese_inspection_duration_uses_concise_minute_unit(self) -> None:
        locales = json.loads(LOCALES_PATH.read_text())
        self.assertEqual(
            '{{count}}分',
            locales['zh-CN.json']['monitoring']['account_inspection_duration_minute'],
        )

    def test_monitoring_uses_a_stable_data_management_navigation_label(self) -> None:
        source = PAGE_PATH.read_text()
        handler = "onClick={() => navigate('/data-management')}"
        start = source.index(handler)
        end = source.index('</button>', start)
        button = source[start:end]

        self.assertIn("t('nav.data_management'", button)
        self.assertNotIn("t('common.loading')", button)

    def test_async_surface_openers_use_synchronous_promise_guards(self) -> None:
        source = PAGE_PATH.read_text()
        self.assertIn('priceManagementRequestRef.current', source)
        self.assertIn('if (priceManagementRequestRef.current) return', source)
        self.assertIn('disabled={isPriceLoading}', source)
        self.assertIn("openSurface('price-management')", source)

    def test_data_management_discards_stale_settings_and_restore_responses(self) -> None:
        source = DATA_PAGE_PATH.read_text()

        self.assertIn('settingsDraftRevisionRef.current += 1;', source)
        self.assertIn('draftRevision === settingsDraftRevisionRef.current', source)
        self.assertIn('submittedRevision === settingsDraftRevisionRef.current', source)
        self.assertIn('const fileSequence = ++restoreFileSequenceRef.current;', source)
        self.assertIn('if (sequence !== restorePreviewSequenceRef.current) return;', source)

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
        styles = read_monitoring_styles()

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
        styles = read_monitoring_styles()

        self.assertIn("pendingRealtimeEventCount > 0 && realtimeLogAutoRefreshPaused", source)
        self.assertIn("className={styles.realtimeTableShell}", source)
        self.assertIn("height: min(620px, 68vh);", styles)
        self.assertIn(".realtimeUpdateBar {\n  position: absolute;", styles)
        self.assertIn("flex-wrap: nowrap;", styles)

    def test_realtime_logs_merge_account_plan_into_account_type(self) -> None:
        source = PAGE_PATH.read_text()
        preferences = REALTIME_PREFERENCES_PATH.read_text()
        account_plan = ACCOUNT_PLAN_PATH.read_text()
        locales = json.loads(LOCALES_PATH.read_text())

        self.assertNotIn("'accountPlan'", preferences)
        self.assertNotIn("shouldMigrateAccountPlan", preferences)
        self.assertNotIn("label: t('monitoring.column_account_plan')", source)
        self.assertIn("authFileByAuthIndex.get(row.authIndex)", source)
        self.assertIn("accountPlan: resolveAccountPlanLabel({", source)
        self.assertIn("`${row.provider} · ${row.accountPlan}`", source)
        self.assertIn("className={styles.realtimeAccountTypeLine}", source)
        self.assertIn("quotaStore.antigravityQuota[fileName]", account_plan)
        self.assertIn("quotaStore.claudeQuota[fileName]", account_plan)
        self.assertIn("quotaStore.codexQuota[fileName]", account_plan)
        self.assertIn("quotaStore.geminiCliQuota[fileName]", account_plan)
        self.assertIn("quotaStore.kimiQuota[fileName]", account_plan)
        self.assertIn("quotaStore.xaiQuota[fileName]", account_plan)
        self.assertEqual('Account Type', locales['en.json']['monitoring']['column_type'])
        self.assertEqual('账号类型', locales['zh-CN.json']['monitoring']['column_type'])
        for locale_name in ('en.json', 'ru.json', 'zh-CN.json', 'zh-TW.json'):
            self.assertNotIn('column_account_plan', locales[locale_name]['monitoring'])

    def test_codex_oauth_request_authoritative_tier_is_preserved_and_explained(self) -> None:
        usage_model = USAGE_MODEL_PATH.read_text()
        cost_cell = COST_CELL_PATH.read_text()
        locales = json.loads(LOCALES_PATH.read_text())

        self.assertIn("'codex_oauth_request'", usage_model)
        self.assertIn("breakdown.serviceTierSource === 'codex_oauth_request'", cost_cell)
        self.assertIn("monitoring.cost_detail_codex_oauth_request", cost_cell)
        for locale_name in ('en.json', 'ru.json', 'zh-CN.json', 'zh-TW.json'):
            self.assertTrue(
                locales[locale_name]['monitoring']['cost_detail_codex_oauth_request'].strip(),
                locale_name,
            )


if __name__ == '__main__':
    unittest.main()
