import json
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
PRO_ROOT = ROOT / 'overlay/src/pro'
CUSTOMIZER = ROOT / 'apply_customizations.py'
SURFACE = PRO_ROOT / 'shared/ProSurface.tsx'
SURFACE_STYLE = PRO_ROOT / 'shared/ProSurface.module.scss'
SURFACE_STATE = PRO_ROOT / 'shared/useProSurfaceState.ts'
INFORMATION_DETAILS = PRO_ROOT / 'shared/ProInformationDetails.tsx'
INFORMATION_DETAILS_STYLE = PRO_ROOT / 'shared/ProInformationDetails.module.scss'
LOCALES = ROOT / 'monitoring-locales.json'


class ProSurfaceCustomizationTest(unittest.TestCase):
    def test_inspection_account_invalid_copy_covers_401_and_403_only(self) -> None:
        locales = json.loads(LOCALES.read_text())
        expected_labels = {
            'en.json': ('Authorization expired', 'Authentication invalid'),
            'ru.json': ('Срок авторизации истёк', 'Аутентификация недействительна'),
            'zh-CN.json': ('授权过期', '认证失效'),
            'zh-TW.json': ('授權過期', '認證失效'),
        }
        for locale_name, (authorization_expired, authentication_invalid) in expected_labels.items():
            monitoring = locales[locale_name]['monitoring']
            hint = monitoring['account_inspection_settings_auto_execute_account_invalid_action_hint']
            self.assertIn('401', hint)
            self.assertIn('403', hint)
            self.assertIn('400/404', hint)
            self.assertEqual(
                authorization_expired,
                monitoring['account_inspection_result_authorization_expired'],
            )
            self.assertEqual(
                authentication_invalid,
                monitoring['account_inspection_result_authentication_invalid'],
            )

    def test_business_surfaces_use_semantic_pro_components(self) -> None:
        direct_modal_users = []
        direct_sheet_users = []
        for path in PRO_ROOT.rglob('*.tsx'):
            if path == SURFACE:
                continue
            source = path.read_text()
            if "@/components/ui/Modal" in source or '<Modal' in source:
                direct_modal_users.append(path.relative_to(PRO_ROOT).as_posix())
            if "@/components/ui/Sheet" in source or '<Sheet' in source:
                direct_sheet_users.append(path.relative_to(PRO_ROOT).as_posix())
        self.assertEqual([], direct_modal_users)
        self.assertEqual([], direct_sheet_users)

    def test_surface_layer_defines_all_product_categories(self) -> None:
        source = SURFACE.read_text()
        state_source = SURFACE_STATE.read_text()
        styles = SURFACE_STYLE.read_text()
        for component in (
            'ProDetailDialog',
            'ProFormDialog',
            'ProTaskDialog',
            'ProWorkspaceDialog',
            'ProWorkspaceSheet',
            'ProSettingsSheet',
        ):
            self.assertIn(component, source)
        self.assertIn('useProSurfaceState', state_source)
        self.assertIn("@media (max-width: 720px)", styles)
        self.assertIn('height: 100dvh;', styles)

    def test_information_and_decision_dialogs_have_distinct_product_layouts(self) -> None:
        surface_styles = SURFACE_STYLE.read_text()
        details = INFORMATION_DETAILS.read_text()
        detail_styles = INFORMATION_DETAILS_STYLE.read_text()
        monitoring = (
            PRO_ROOT / 'modules/monitoring/features/components/RealtimeLogDetails.tsx'
        ).read_text()
        inspection_model = (
            PRO_ROOT / 'modules/inspection/features/accountInspectionPageModel.tsx'
        ).read_text()
        inspection_styles = (
            PRO_ROOT / 'modules/inspection/features/account-inspection-styles/_tables-dialogs.scss'
        ).read_text()
        routing = (PRO_ROOT / 'modules/routing/RoutingPolicyPage.tsx').read_text()

        self.assertIn('width: min(800px', surface_styles)
        self.assertIn('ProInformationDetails', details)
        self.assertIn('grid-template-columns: repeat(2, minmax(0, 1fr))', detail_styles)
        self.assertIn('ProInformationDetails', monitoring)
        self.assertIn('ProInformationDetails', inspection_model)
        self.assertIn('ProInformationDetails', routing)
        self.assertIn('className={styles.informationDetailsTheme}', monitoring)
        self.assertIn('className={styles.informationDetailsTheme}', inspection_model)
        delete_confirmation = inspection_model[
            inspection_model.index('export const buildDeleteConfirmationMessage'):inspection_model.index(
                'const withChanged'
            )
        ]
        self.assertIn('confirmationDecisionBody', delete_confirmation)
        self.assertIn('confirmationDecisionDanger', delete_confirmation)
        self.assertIn(':has(.confirmationDecisionBody)', inspection_styles)
        self.assertIn('--inspect-surface: var(--bg-primary);', inspection_styles)
        self.assertIn(':has(.confirmationDecisionDanger)', inspection_styles)
        self.assertIn('--decision-tone: var(--error-color);', inspection_styles)
        self.assertIn('width: min(760px', inspection_styles)
        self.assertIn('grid-auto-rows: max-content', inspection_styles)
        self.assertIn('scrollbar-gutter: stable', inspection_styles)
        self.assertNotIn('.slice(0, 5)', inspection_model)
        self.assertNotIn('-webkit-line-clamp', inspection_styles)

    def test_inspection_detail_omits_history_and_operation_modules(self) -> None:
        inspection = (PRO_ROOT / 'modules/inspection/AccountInspectionPage.tsx').read_text()
        api = (PRO_ROOT / 'modules/inspection/api.ts').read_text()
        records = PRO_ROOT / 'modules/inspection/InspectionRecordsPanel.tsx'
        self.assertFalse(records.exists())
        self.assertNotIn('InspectionRecordsPanel', inspection)
        self.assertNotIn('AccountInspectionHistoryResponse', api)
        self.assertNotIn('AccountInspectionOperationsResponse', api)
        self.assertNotIn('getHistory:', api)
        self.assertNotIn('getOperations:', api)

    def test_workspace_settings_use_sheets_and_shared_footer_contract(self) -> None:
        monitoring_settings = (PRO_ROOT / 'modules/monitoring/features/components/MonitoringSettingsModal.tsx').read_text()
        prices = (PRO_ROOT / 'modules/monitoring/features/components/ModelPriceManagerModal.tsx').read_text()
        inspection = (PRO_ROOT / 'modules/inspection/AccountInspectionPage.tsx').read_text()
        self.assertIn('<ProSettingsSheet', monitoring_settings)
        self.assertIn('<ProSettingsSheet', prices)
        self.assertIn('<ProSettingsSheet', inspection)
        self.assertNotIn('<ProWorkspaceDialog', monitoring_settings)
        self.assertNotIn('<ProWorkspaceDialog', prices)
        self.assertNotIn('<ProWorkspaceDialog', inspection)
        self.assertIn('dirty={monitoringSettingsDirty}', monitoring_settings)
        self.assertIn('dirty={settingsDirty}', inspection)
        self.assertNotIn('size="xl"', monitoring_settings)
        self.assertNotIn('size="xl"', prices)
        self.assertNotIn('size="xl"', inspection)

    def test_settings_sheets_use_native_confirm_close_contract(self) -> None:
        surface = SURFACE.read_text()
        monitoring_settings = (PRO_ROOT / 'modules/monitoring/features/components/MonitoringSettingsModal.tsx').read_text()
        prices = (PRO_ROOT / 'modules/monitoring/features/components/ModelPriceManagerModal.tsx').read_text()
        inspection = (PRO_ROOT / 'modules/inspection/AccountInspectionPage.tsx').read_text()
        self.assertIn('confirmClose={confirmSettingsClose}', surface)
        self.assertIn('onClick={() => void handleCancelClick()}', surface)
        self.assertIn('onClose={commitClose}', surface)
        self.assertIn('closeDisabled={busy || props.closeDisabled}', surface)
        self.assertIn('confirmClose={confirmMonitoringSettingsClose}', monitoring_settings)
        self.assertIn('onDiscard={discardMonitoringSettingsDraft}', monitoring_settings)
        self.assertIn('confirmClose={confirmPriceWorkspaceClose}', prices)
        self.assertIn('onDiscard={discardPriceWorkspaceDraft}', prices)
        self.assertIn('confirmClose={confirmSettingsModalClose}', inspection)
        self.assertIn('onDiscard={discardSettingsModalDraft}', inspection)
        self.assertIn('if (!accepted) resolve(false)', prices)
        self.assertIn('if (!accepted) resolve(false)', inspection)

    def test_workspace_sheet_layout_responds_to_sheet_width(self) -> None:
        surface_styles = SURFACE_STYLE.read_text()
        monitoring_styles = (PRO_ROOT / 'modules/monitoring/features/styles/_responsive.scss').read_text()
        inspection_styles = (PRO_ROOT / 'modules/inspection/features/account-inspection-styles/_responsive.scss').read_text()
        self.assertIn('container: pro-workspace-sheet / inline-size;', surface_styles)
        self.assertIn('@container pro-workspace-sheet (max-width: 1020px)', monitoring_styles)
        self.assertIn('@container pro-workspace-sheet (max-width: 720px)', monitoring_styles)
        self.assertIn('@container pro-workspace-sheet (max-width: 900px)', inspection_styles)

    def test_inspection_settings_sheet_uses_single_scroll_container(self) -> None:
        inspection = (PRO_ROOT / 'modules/inspection/AccountInspectionPage.tsx').read_text()
        inspection_styles = (
            PRO_ROOT / 'modules/inspection/features/account-inspection-styles/_analytics.scss'
        ).read_text()
        self.assertNotIn('className={styles.settingsSidebar}', inspection)
        self.assertNotIn('settingsMainRef', inspection)
        self.assertNotIn('settingsSectionRefs', inspection)
        self.assertNotIn('.settingsSidebar', inspection_styles)
        self.assertNotIn('max-height: 620px', inspection_styles)
        self.assertNotIn('settingsHeroPanel', inspection)
        self.assertNotIn('settingsSummaryGrid', inspection + inspection_styles)
        self.assertNotIn('account_inspection_settings_overview_', inspection)

    def test_monitoring_settings_sections_are_visually_distinct(self) -> None:
        settings = (
            PRO_ROOT / 'modules/monitoring/features/components/MonitoringSettingsModal.tsx'
        ).read_text()
        styles = (
            PRO_ROOT / 'modules/monitoring/features/styles/_management.scss'
        ).read_text()
        self.assertIn('styles.settingsRetentionSection', settings)
        self.assertIn('styles.settingsBackupSection', settings)
        self.assertIn('styles.settingsDangerSection', settings)
        self.assertIn('styles.resetStatisticsIcon', settings)
        self.assertIn('currentColor', styles)

    def test_model_price_sheet_uses_single_scroll_workspace(self) -> None:
        prices = (
            PRO_ROOT / 'modules/monitoring/features/components/ModelPriceManagerModal.tsx'
        ).read_text()
        styles = (
            PRO_ROOT / 'modules/monitoring/features/styles/_management.scss'
        ).read_text()
        responsive = (
            PRO_ROOT / 'modules/monitoring/features/styles/_responsive.scss'
        ).read_text()
        self.assertIn('className={styles.priceRulePicker}', prices)
        self.assertIn('className={styles.priceRuleEditorContent}', prices)
        self.assertIn('requestPriceManagementViewChange', prices)
        self.assertNotIn('priceRuleSidebar', prices)
        self.assertNotIn('priceRuleEditorScroll', prices)
        self.assertNotIn('.priceRuleSidebar', styles + responsive)
        self.assertNotIn('.priceRuleEditorScroll', styles + responsive)
        self.assertNotIn('max-height: min(64vh, 560px)', styles)
        self.assertIn('grid-row: 1 / span 2;', styles)
        self.assertIn('grid-column: -2 / -1;', styles)
        self.assertNotIn('.priceServiceTierRow {', styles)
        self.assertGreaterEqual(responsive.count('grid-row: auto;'), 2)

    def test_model_price_editor_prioritizes_status_and_inline_add_actions(self) -> None:
        prices = (
            PRO_ROOT / 'modules/monitoring/features/components/ModelPriceManagerModal.tsx'
        ).read_text()
        styles = (
            PRO_ROOT / 'modules/monitoring/features/styles/_management.scss'
        ).read_text()
        header_start = prices.index('<header className={styles.priceRuleEditorHeader}>')
        header = prices[header_start:prices.index('</header>', header_start)]
        self.assertNotIn('model_price_model_scope', header)
        self.assertLess(header.index('priceRuleEditorBadges'), header.index('priceRuleEditorActions'))
        self.assertIn('className={styles.priceRuleRestoreButton}', header)
        self.assertNotIn("<span>{t('common.delete')}</span>", header)
        self.assertNotIn('<details', prices)
        self.assertNotIn('model_price_advanced', prices)
        self.assertEqual(prices.count('styles.priceRuleCollectionSection'), 3)
        self.assertEqual(prices.count('styles.priceRuleCollectionContent'), 3)
        self.assertNotIn('priceAdvanced', prices + styles)
        for title, handler in (
            ('model_price_context_tier', 'addPriceTier'),
            ('model_price_service_tiers', 'addServiceTier'),
            ('model_price_speeds', 'addSpeed'),
        ):
            self.assertEqual(prices.count(f'onClick={{{handler}}}'), 1)
            title_index = prices.index(title)
            action_index = prices.index(f'onClick={{{handler}}}', title_index)
            content_index = prices.index('styles.priceRuleCollectionContent', action_index)
            self.assertLess(title_index, action_index)
            self.assertLess(action_index, content_index)
        actions_start = styles.index('.priceRuleEditorActions {')
        actions = styles[actions_start:styles.index('}', actions_start)]
        self.assertNotIn('grid-column: 1 / -1;', actions)
        self.assertNotIn('border-top:', actions)

    def test_model_price_editor_keeps_mobile_actions_and_base_rates_compact(self) -> None:
        responsive = (
            PRO_ROOT / 'modules/monitoring/features/styles/_responsive.scss'
        ).read_text()
        narrow_start = responsive.index('@media (max-width: 440px)')
        narrow = responsive[narrow_start:]
        self.assertIn('grid-template-columns: repeat(2, minmax(0, 1fr));', narrow)
        self.assertNotIn('grid-template-columns: 1fr;', narrow)
        self.assertNotIn('flex-direction: column;', narrow)
        self.assertIn('min-height: 40px;', narrow)
        self.assertIn('.priceRuleEditorActions :global(.btn:only-child)', narrow)
        self.assertNotIn('min-width: 76px;', narrow)
        self.assertNotIn('flex: 1 1 auto;', narrow)

    def test_settings_surfaces_freeze_all_actions_while_busy(self) -> None:
        surface = SURFACE.read_text()
        self.assertGreaterEqual(surface.count('disabled={busy}'), 3)
        self.assertIn(
            '<fieldset className={styles.settingsFooterStart} disabled={busy} aria-busy={busy}>',
            surface,
        )
        self.assertIn(
            '<fieldset className={styles.settingsFields} disabled={busy} aria-busy={busy}>',
            surface,
        )

    def test_price_workspace_preserves_dirty_state_across_tabs_and_model_changes(self) -> None:
        prices = (PRO_ROOT / 'modules/monitoring/features/components/ModelPriceManagerModal.tsx').read_text()
        self.assertIn('const workspaceDirty = priceEditorDirty || scheduleDirty;', prices)
        self.assertIn('const requestPriceTargetChange = (model: string) => {', prices)
        self.assertIn('if (!priceEditorDirty) {', prices)
        self.assertIn('dedupeKey: `model-price:discard:${priceModel}`', prices)
        self.assertIn('onConfirm: () => selectPriceTarget(model)', prices)
        self.assertIn('onClick={() => requestPriceTargetChange(item.model)}', prices)

    def test_models_dev_import_discards_stale_searches_and_hides_dead_end_actions(self) -> None:
        prices = (PRO_ROOT / 'modules/monitoring/features/components/ModelPriceManagerModal.tsx').read_text()
        self.assertIn('const modelsDevSearchRequestRef = useRef(0);', prices)
        self.assertIn("const modelsDevSearchTargetRef = useRef(selectedPriceTarget?.model ?? '');", prices)
        self.assertIn('modelsDevSearchRequestRef.current += 1;', prices)
        self.assertGreaterEqual(
            prices.count(
                'modelsDevSearchRequestRef.current !== requestID || modelsDevSearchTargetRef.current !== targetModel'
            ),
            2,
        )
        self.assertIn('const configuredRule = priceRuleTargetByModel.get(change.model)?.rule;', prices)
        self.assertIn('const configuredRule = priceRuleTargetByModel.get(item.model)?.rule;', prices)
        self.assertEqual(prices.count("'usage_stats.model_price_manual_configured'"), 2)
        self.assertNotIn('onClick={() => selectPriceTarget(item.model)}', prices)

    def test_models_dev_search_controls_align_and_results_scroll_inside_the_section(self) -> None:
        styles = (PRO_ROOT / 'modules/monitoring/features/styles/_management.scss').read_text()
        form_start = styles.index('.modelsDevSearchForm {')
        form = styles[form_start:styles.index('}', form_start)]
        results_start = styles.index('.modelsDevSearchResults {')
        results = styles[results_start:styles.index('}', results_start)]
        self.assertIn('align-items: stretch;', form)
        self.assertIn('.modelsDevSearchForm :global(.form-group)', styles)
        self.assertIn('margin: 0;', styles[styles.index('.modelsDevSearchForm :global(.form-group)'):results_start])
        self.assertIn('max-height: 304px;', results)
        self.assertIn('overflow-y: auto;', results)
        self.assertIn('overscroll-behavior: contain;', results)
        self.assertIn('scrollbar-gutter: stable;', results)

    def test_price_loading_reopens_the_surface_before_reusing_inflight_work(self) -> None:
        monitoring = (PRO_ROOT / 'modules/monitoring/MonitoringCenterPage.tsx').read_text()
        price_open = monitoring.index('setIsPriceModalOpen(true);')
        price_reuse = monitoring.index('if (priceManagementRequestRef.current)')
        self.assertLess(price_open, price_reuse)

    def test_pages_coordinate_one_primary_surface(self) -> None:
        monitoring = (PRO_ROOT / 'modules/monitoring/MonitoringCenterPage.tsx').read_text()
        inspection = (PRO_ROOT / 'modules/inspection/AccountInspectionPage.tsx').read_text()
        proxy_pool = (PRO_ROOT / 'modules/proxyPool/ProxyPoolPage.tsx').read_text()
        self.assertIn("useProSurfaceState<'realtime-detail' | 'price-management'>", monitoring)
        self.assertIn("useProSurfaceState<'settings' | 'detail' | 'recovery'>", inspection)
        self.assertIn("useProSurfaceState<'node' | 'import' | 'takeover'>", proxy_pool)

    def test_account_inspection_summary_merges_incomplete_results_and_deduplicates_auto_execution(self) -> None:
        inspection = (PRO_ROOT / 'modules/inspection/AccountInspectionPage.tsx').read_text()
        model = (PRO_ROOT / 'modules/inspection/features/accountInspectionPageModel.tsx').read_text()
        strategy_start = inspection.index('{hasAutoExecutionPolicy ? (')
        strategy_end = inspection.index(') : (', strategy_start)
        strategy = inspection[strategy_start:strategy_end]
        self.assertNotIn("showInspectionResults('unknown')", inspection)
        self.assertNotIn("value: 'unknown'", inspection)
        self.assertNotIn('autoExecutionResultLabel', inspection)
        self.assertNotIn('account_inspection_action_keep', strategy)
        self.assertIn("showInspectionResults('all')", strategy)
        self.assertNotIn("'recoverable' | 'unknown'", model)

    def test_detail_data_is_cleared_only_after_the_exit_animation(self) -> None:
        surface = SURFACE.read_text()
        monitoring = (PRO_ROOT / 'modules/monitoring/MonitoringCenterPage.tsx').read_text()
        inspection = (PRO_ROOT / 'modules/inspection/AccountInspectionPage.tsx').read_text()
        routing = (PRO_ROOT / 'modules/routing/RoutingPolicyPage.tsx').read_text()
        auth_test = (PRO_ROOT / 'authFiles/AuthFileConnectionTestModal.tsx').read_text()
        account_usage = (PRO_ROOT / 'modules/monitoring/features/components/AccountUsageModal.tsx').read_text()
        proxy_pool = (PRO_ROOT / 'modules/proxyPool/ProxyPoolPage.tsx').read_text()
        self.assertIn('onAfterClose?: () => void;', surface)
        self.assertIn('onAfterClose={onAfterClose}', surface)
        self.assertIn('onAfterClose={() => setSelectedRealtimeErrorRowState(null)}', monitoring)
        self.assertIn('onAfterClose={() => setSelectedDetailResultState(null)}', inspection)
        self.assertIn('onAfterClose={() => setSelectedAuthId(null)}', routing)
        self.assertIn('onAfterClose={() => setDisplayFile(null)}', auth_test)
        self.assertIn('onAfterClose={() => setDisplayFile(null)}', account_usage)
        self.assertIn('onAfterClose={clearClosedNodeSheet}', proxy_pool)

    def test_account_usage_workspace_uses_a_readable_desktop_width(self) -> None:
        account_usage = (
            PRO_ROOT / 'modules/monitoring/features/components/AccountUsageModal.tsx'
        ).read_text()
        account_usage_styles = (
            PRO_ROOT / 'modules/monitoring/features/components/AccountUsageModal.module.scss'
        ).read_text()
        self.assertIn('className={styles.modal}', account_usage)
        self.assertIn('@media (min-width: 721px)', account_usage_styles)
        self.assertIn('width: min(960px, calc(100vw - 32px)) !important;', account_usage_styles)

    def test_rich_account_confirmations_use_business_dedupe_keys(self) -> None:
        inspection = (PRO_ROOT / 'modules/inspection/AccountInspectionPage.tsx').read_text()
        self.assertGreaterEqual(
            inspection.count('dedupeKey: `account-inspection:execute:'),
            3,
        )
        self.assertIn('${target.key}:${target.action}', inspection)
        self.assertIn('${item.key}:${item.action}', inspection)

    def test_auth_surface_extensions_keep_one_external_active_surface(self) -> None:
        source = CUSTOMIZER.read_text()
        extensions = (PRO_ROOT / 'authFiles/AuthFileExtensions.tsx').read_text()
        self.assertIn(
            "'<AuthFileSurfaceExtensions />'",
            source,
        )
        self.assertIn('let activeSurface: AuthFileSurfaceState | null = null;', extensions)
        self.assertIn('useSyncExternalStore(', extensions)
        self.assertIn("openSurface('usage', file)", extensions)
        self.assertIn("openSurface('connection-test', file)", extensions)


if __name__ == '__main__':
    unittest.main()
