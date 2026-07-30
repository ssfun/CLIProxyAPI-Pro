import { useMemo, type Dispatch, type SetStateAction } from 'react';
import type { TFunction } from 'i18next';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { Modal } from '@/components/ui/Modal';
import { IconRefreshCw, IconSearch, IconTrash2 } from '@/components/ui/icons';
import { formatShortDateTime } from '../hooks/useMonitoringData';
import {
  MODEL_PRICE_SYNC_RATE_FIELDS,
  createPriceDraft,
  formatModelPriceRate,
  type PriceDraft,
  type PriceManagementView,
  type PriceRuleTarget,
  type PriceSyncChangeFilter,
  type PriceTierDraft,
} from '../modelPricePresentation';
import type { MonitoringSettingsDraft } from '../monitoringSettings';
import {
  formatCompactNumber,
  type ModelPriceSyncResult,
  type ModelPriceSyncState,
} from '@/utils/usage';
import styles from '../monitoring.module.scss';

export function ModelPriceManagerModal({
  isPriceModalOpen,
  setIsPriceModalOpen,
  priceManagementView,
  setPriceManagementView,
  priceRuleTargets,
  priceRuleSearch,
  setPriceRuleSearch,
  priceModel,
  selectPriceTarget,
  isPriceLoading,
  priceDraft,
  setPriceDraft,
  handlePriceDraftChange,
  handlePriceTierChange,
  addPriceTier,
  removePriceTier,
  handleDeletePrice,
  handleSavePrice,
  isPriceSaving,
  priceSyncState,
  priceSyncResult,
  isPriceSyncing,
  handleSyncPrices,
  priceSyncLockedOverrides,
  setPriceSyncLockedOverrides,
  priceSyncChangeFilter,
  setPriceSyncChangeFilter,
  monitoringSettingsDraft,
  setMonitoringSettingsDraft,
  handleSaveMonitoringSettings,
  isMonitoringSettingsLoading,
  isMonitoringSettingsSaving,
  t,
}: {
  isPriceModalOpen: boolean;
  setIsPriceModalOpen: Dispatch<SetStateAction<boolean>>;
  priceManagementView: PriceManagementView;
  setPriceManagementView: Dispatch<SetStateAction<PriceManagementView>>;
  priceRuleTargets: PriceRuleTarget[];
  priceRuleSearch: string;
  setPriceRuleSearch: Dispatch<SetStateAction<string>>;
  priceModel: string;
  selectPriceTarget: (model: string) => void;
  isPriceLoading: boolean;
  priceDraft: PriceDraft;
  setPriceDraft: Dispatch<SetStateAction<PriceDraft>>;
  handlePriceDraftChange: (field: Exclude<keyof PriceDraft, 'tiers'>, value: string) => void;
  handlePriceTierChange: (index: number, field: keyof PriceTierDraft, value: string) => void;
  addPriceTier: () => void;
  removePriceTier: (index: number) => void;
  handleDeletePrice: (model: string) => void | Promise<void>;
  handleSavePrice: () => void | Promise<void>;
  isPriceSaving: boolean;
  priceSyncState: ModelPriceSyncState;
  priceSyncResult: ModelPriceSyncResult | null;
  isPriceSyncing: boolean;
  handleSyncPrices: (dryRun?: boolean) => void | Promise<void>;
  priceSyncLockedOverrides: string[];
  setPriceSyncLockedOverrides: Dispatch<SetStateAction<string[]>>;
  priceSyncChangeFilter: PriceSyncChangeFilter;
  setPriceSyncChangeFilter: Dispatch<SetStateAction<PriceSyncChangeFilter>>;
  monitoringSettingsDraft: MonitoringSettingsDraft;
  setMonitoringSettingsDraft: Dispatch<SetStateAction<MonitoringSettingsDraft>>;
  handleSaveMonitoringSettings: (closeModal?: boolean) => void | Promise<void>;
  isMonitoringSettingsLoading: boolean;
  isMonitoringSettingsSaving: boolean;
  t: TFunction;
}) {
  const filteredPriceRuleTargets = useMemo(() => {
    const query = priceRuleSearch.trim().toLowerCase();
    if (!query) return priceRuleTargets;
    return priceRuleTargets.filter((item) => item.model.toLowerCase().includes(query));
  }, [priceRuleSearch, priceRuleTargets]);
  const selectedPriceTarget = useMemo(
    () => priceRuleTargets.find((item) => item.model === priceModel),
    [priceModel, priceRuleTargets]
  );
  const configuredPriceRuleCount = priceRuleTargets.filter((item) => Boolean(item.rule)).length;
  const unconfiguredPriceRuleCount = priceRuleTargets.length - configuredPriceRuleCount;
  const priceSyncStatus = isPriceSyncing ? 'syncing' : priceSyncState.status;
  const unmatchedPriceModels = priceSyncResult?.unmatched ?? priceSyncState.unmatchedModels ?? [];
  const unmatchedPriceModelCount = priceSyncResult
    ? unmatchedPriceModels.length
    : (priceSyncState.unmatched ?? unmatchedPriceModels.length);
  const priceSyncChanges = useMemo(() => priceSyncResult?.changes ?? [], [priceSyncResult]);
  const priceSyncLockedOverrideSet = useMemo(
    () => new Set(priceSyncLockedOverrides),
    [priceSyncLockedOverrides]
  );
  const priceSyncChangeCounts = useMemo(() => {
    const counts = { added: 0, updated: 0, overridden: 0, locked: 0, unmatched: 0 };
    priceSyncChanges.forEach((change) => {
      const action = change.action === 'locked' && priceSyncLockedOverrideSet.has(change.model)
        ? 'overridden'
        : change.action;
      counts[action] += 1;
    });
    return counts;
  }, [priceSyncChanges, priceSyncLockedOverrideSet]);
  const filteredPriceSyncChanges = useMemo(
    () => priceSyncChangeFilter === 'all'
      ? priceSyncChanges
      : priceSyncChanges.filter((change) => {
        const action = change.action === 'locked' && priceSyncLockedOverrideSet.has(change.model)
          ? 'overridden'
          : change.action;
        return action === priceSyncChangeFilter;
      }),
    [priceSyncChangeFilter, priceSyncChanges, priceSyncLockedOverrideSet]
  );
  const lockedPriceSyncChanges = useMemo(
    () => priceSyncChanges.filter((change) => change.action === 'locked'),
    [priceSyncChanges]
  );
  const allLockedPriceSyncChangesSelected = lockedPriceSyncChanges.length > 0
    && lockedPriceSyncChanges.every((change) => priceSyncLockedOverrideSet.has(change.model));

  return (
      <Modal
        open={isPriceModalOpen}
        onClose={() => setIsPriceModalOpen(false)}
        title={t('usage_stats.model_price_settings')}
        width={960}
        className={`${styles.monitorModal} ${styles.priceManagerModal}`}
      >
        <div className={styles.priceManager}>
          <div className={styles.priceManagerTabs} role="tablist" aria-label={t('usage_stats.model_price_settings')}>
            <button
              type="button"
              role="tab"
              aria-selected={priceManagementView === 'rules'}
              className={`${styles.priceManagerTab} ${priceManagementView === 'rules' ? styles.priceManagerTabActive : ''}`}
              onClick={() => setPriceManagementView('rules')}
            >
              {t('usage_stats.model_price_tab_rules')}
              <span>{configuredPriceRuleCount}/{priceRuleTargets.length}</span>
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={priceManagementView === 'sync'}
              className={`${styles.priceManagerTab} ${priceManagementView === 'sync' ? styles.priceManagerTabActive : ''}`}
              onClick={() => setPriceManagementView('sync')}
            >
              {t('usage_stats.model_price_tab_sync')}
              {unconfiguredPriceRuleCount > 0 ? <span>{unconfiguredPriceRuleCount}</span> : null}
            </button>
          </div>

          {priceManagementView === 'rules' ? (
            <div className={styles.priceRuleWorkspace}>
              <aside className={styles.priceRuleSidebar}>
                <div className={styles.priceRuleSearch}>
                  <IconSearch size={15} />
                  <Input
                    value={priceRuleSearch}
                    onChange={(event) => setPriceRuleSearch(event.target.value)}
                    placeholder={t('usage_stats.model_price_search_placeholder')}
                  />
                </div>
                <div className={styles.priceRuleList}>
                  {isPriceLoading ? <div className={styles.priceRuleListEmpty}>{t('common.loading')}</div> : null}
                  {!isPriceLoading && filteredPriceRuleTargets.map((item) => {
                    const active = item.model === priceModel;
                    return (
                      <button
                        key={item.key}
                        type="button"
                        className={`${styles.priceRuleListItem} ${active ? styles.priceRuleListItemActive : ''}`}
                        onClick={() => selectPriceTarget(item.model)}
                      >
                        <span className={styles.priceRuleListIdentity}>
                          <strong title={item.model}>{item.model}</strong>
                        </span>
                        <span className={styles.priceRuleListMeta}>
                          <span className={item.rule ? styles.priceRuleConfigured : styles.priceRuleUnconfigured}>
                            {t(item.rule ? 'usage_stats.model_price_configured' : 'usage_stats.model_price_unconfigured')}
                          </span>
                          <small>{t('usage_stats.model_price_requests', { count: item.requests })}</small>
                        </span>
                      </button>
                    );
                  })}
                  {!isPriceLoading && filteredPriceRuleTargets.length === 0 ? (
                    <div className={styles.priceRuleListEmpty}>{t('usage_stats.model_price_search_empty')}</div>
                  ) : null}
                </div>
              </aside>

              <section className={styles.priceRuleEditorPane}>
                {selectedPriceTarget ? (
                  <>
                    <header className={styles.priceRuleEditorHeader}>
                      <div>
                        <h3 title={selectedPriceTarget.model}>{selectedPriceTarget.model}</h3>
                        <span>{t('usage_stats.model_price_model_scope')}</span>
                      </div>
                      <div className={styles.priceRuleEditorBadges}>
                        <span className={selectedPriceTarget.rule ? styles.priceRuleConfigured : styles.priceRuleUnconfigured}>
                          {t(selectedPriceTarget.rule ? 'usage_stats.model_price_configured' : 'usage_stats.model_price_unconfigured')}
                        </span>
                        {selectedPriceTarget.rule?.source ? <span>{selectedPriceTarget.rule.source}</span> : null}
                      </div>
                    </header>

                    <div className={styles.priceRuleEditorScroll}>
                      <section className={styles.priceRuleSection}>
                        <div className={styles.priceRuleSectionHeader}>
                          <h4>{t('usage_stats.model_price_base_rates')}</h4>
                          <span>USD / 1M</span>
                        </div>
                        <div className={styles.priceBaseGrid}>
                          {([
                            ['input', 'usage_stats.model_price_input'],
                            ['output', 'usage_stats.model_price_output'],
                            ['cacheRead', 'usage_stats.model_price_cache_read'],
                            ['cacheWrite', 'usage_stats.model_price_cache_write'],
                          ] as const).map(([field, label]) => (
                            <label className={styles.priceField} key={field}>
                              <span>{t(label)}</span>
                              <Input
                                type="number"
                                min="0"
                                step="0.0001"
                                value={priceDraft[field]}
                                onChange={(event) => handlePriceDraftChange(field, event.target.value)}
                                placeholder="0.0000"
                              />
                            </label>
                          ))}
                        </div>
                      </section>

                      <section className={styles.priceRuleSection}>
                        <div className={styles.priceRuleSectionHeader}>
                          <div>
                            <h4>{t('usage_stats.model_price_context_tier')}</h4>
                            <span>{t('usage_stats.model_price_tier_count', { count: priceDraft.tiers.length })}</span>
                          </div>
                          <Button variant="secondary" size="sm" onClick={addPriceTier}>
                            {t('usage_stats.model_price_tier_add')}
                          </Button>
                        </div>
                        <div className={styles.priceTierList}>
                          {priceDraft.tiers.map((tier, index) => (
                            <div className={styles.priceTierCompactRow} key={index}>
                              <span className={styles.priceTierIndex}>{index + 1}</span>
                              <label>
                                <span>{t('usage_stats.model_price_context_threshold')}</span>
                                <Input type="number" min="1" step="1" value={tier.contextSize} onChange={(event) => handlePriceTierChange(index, 'contextSize', event.target.value)} placeholder="272000" />
                              </label>
                              <label>
                                <span>{t('usage_stats.model_price_input')}</span>
                                <Input type="number" min="0" step="0.0001" value={tier.input} onChange={(event) => handlePriceTierChange(index, 'input', event.target.value)} placeholder="0.0000" />
                              </label>
                              <label>
                                <span>{t('usage_stats.model_price_output')}</span>
                                <Input type="number" min="0" step="0.0001" value={tier.output} onChange={(event) => handlePriceTierChange(index, 'output', event.target.value)} placeholder="0.0000" />
                              </label>
                              <label>
                                <span>{t('usage_stats.model_price_cache_read')}</span>
                                <Input type="number" min="0" step="0.0001" value={tier.cacheRead} onChange={(event) => handlePriceTierChange(index, 'cacheRead', event.target.value)} placeholder="0.0000" />
                              </label>
                              <label>
                                <span>{t('usage_stats.model_price_cache_write')}</span>
                                <Input type="number" min="0" step="0.0001" value={tier.cacheWrite} onChange={(event) => handlePriceTierChange(index, 'cacheWrite', event.target.value)} placeholder="0.0000" />
                              </label>
                              <button
                                type="button"
                                className={styles.priceTierRemoveButton}
                                onClick={() => removePriceTier(index)}
                                aria-label={t('usage_stats.model_price_tier_remove')}
                                title={t('usage_stats.model_price_tier_remove')}
                              >
                                <IconTrash2 size={15} />
                              </button>
                            </div>
                          ))}
                          {priceDraft.tiers.length === 0 ? (
                            <div className={styles.priceTierEmpty}>{t('usage_stats.model_price_tier_empty')}</div>
                          ) : null}
                        </div>
                      </section>
                    </div>

                    <footer className={styles.priceRuleEditorFooter}>
                      <div>
                        {selectedPriceTarget.rule ? (
                          <Button variant="secondary" size="sm" onClick={() => void handleDeletePrice(selectedPriceTarget.model)}>
                            {t('common.delete')}
                          </Button>
                        ) : null}
                      </div>
                      <div>
                        <Button variant="secondary" size="sm" onClick={() => setPriceDraft(createPriceDraft(selectedPriceTarget.rule))}>
                          {t('usage_stats.model_price_reset_changes')}
                        </Button>
                        <Button variant="primary" size="sm" onClick={() => void handleSavePrice()} disabled={isPriceSaving}>
                          {isPriceSaving ? t('common.loading') : t('common.save')}
                        </Button>
                      </div>
                    </footer>
                  </>
                ) : (
                  <div className={styles.priceRuleEditorEmpty}>{t('usage_stats.model_price_select_empty')}</div>
                )}
              </section>
            </div>
          ) : (
            <div className={styles.priceSyncView}>
              <header className={styles.priceSyncHeader}>
                <div>
                  <span className={`${styles.priceSyncStatusDot} ${styles[`priceSyncStatus${priceSyncStatus}`] ?? ''}`} />
                  <div>
                    <h3>{t(`usage_stats.model_price_sync_state_${priceSyncStatus}`, { defaultValue: priceSyncStatus })}</h3>
                    <span>
                      {priceSyncState.lastSuccessMs
                        ? t('usage_stats.model_price_last_sync', { value: formatShortDateTime(priceSyncState.lastSuccessMs) })
                        : t('usage_stats.model_price_sync_never')}
                    </span>
                  </div>
                </div>
                <div className={styles.priceSyncActions}>
                  <Button variant="secondary" size="sm" onClick={() => void handleSyncPrices(true)} disabled={isPriceSyncing || isPriceLoading}>
                    {t('usage_stats.model_price_sync_preview')}
                  </Button>
                  <Button
                    variant="primary"
                    size="sm"
                    className={styles.priceSyncApplyButton}
                    onClick={() => void handleSyncPrices(false)}
                    disabled={isPriceSyncing || isPriceLoading}
                  >
                    <IconRefreshCw size={14} className={styles.priceSyncApplyIcon} />
                    {isPriceSyncing
                      ? t('common.loading')
                      : priceSyncLockedOverrides.length > 0
                        ? t('usage_stats.model_price_sync_with_overrides', { count: priceSyncLockedOverrides.length })
                        : t('usage_stats.model_price_sync')}
                  </Button>
                </div>
              </header>

              <div className={styles.priceSyncMetrics}>
                {([
                  ['matched', priceSyncResult?.matched ?? priceSyncState.matched ?? 0],
                  ['added', priceSyncResult?.added ?? priceSyncState.added ?? 0],
                  ['updated', priceSyncResult?.updated ?? priceSyncState.updated ?? 0],
                  ['unmatched', unmatchedPriceModelCount],
                ] as const).map(([key, value]) => (
                  <div key={key} className={`${styles.priceSyncMetric} ${styles[`priceSyncMetric${key}`] ?? ''}`}>
                    <span>{t(`usage_stats.model_price_sync_metric_${key}`)}</span>
                    <strong>{formatCompactNumber(value)}</strong>
                  </div>
                ))}
              </div>

              {priceSyncState.error ? <div className={styles.priceSyncError}>{priceSyncState.error}</div> : null}

              {priceSyncResult ? (
                <section className={styles.priceSyncChangesSection}>
                  <div className={styles.priceSyncChangesHeader}>
                    <div>
                      <h4>{t(priceSyncResult.dryRun ? 'usage_stats.model_price_sync_preview_details' : 'usage_stats.model_price_sync_applied_details')}</h4>
                      <span>{t('usage_stats.model_price_sync_change_summary', {
                        added: priceSyncChangeCounts.added,
                        updated: priceSyncChangeCounts.updated,
                        overridden: priceSyncChangeCounts.overridden,
                        locked: priceSyncChangeCounts.locked,
                        unmatched: unmatchedPriceModelCount,
                      })}</span>
                    </div>
                    <div className={styles.priceSyncChangesToolbar}>
                      {lockedPriceSyncChanges.length > 0 ? (
                        <label className={styles.priceSyncOverrideAll}>
                          <input
                            type="checkbox"
                            checked={allLockedPriceSyncChangesSelected}
                            onChange={(event) => setPriceSyncLockedOverrides(event.target.checked ? lockedPriceSyncChanges.map((change) => change.model) : [])}
                          />
                          <span>{t('usage_stats.model_price_sync_override_all', { count: lockedPriceSyncChanges.length })}</span>
                        </label>
                      ) : null}
                      <div className={styles.priceSyncChangeFilters}>
                        {(['all', 'added', 'updated', 'overridden', 'locked', 'unmatched'] as const).map((filter) => {
                          const count = filter === 'all' ? priceSyncChanges.length : priceSyncChangeCounts[filter];
                          if (filter !== 'all' && count === 0) return null;
                          const filterLabel = filter === 'overridden' && priceSyncResult.dryRun
                            ? t('usage_stats.model_price_sync_override_selected')
                            : t(`usage_stats.model_price_sync_change_${filter}`);
                          return (
                            <button
                              type="button"
                              key={filter}
                              className={priceSyncChangeFilter === filter ? styles.priceSyncChangeFilterActive : ''}
                              onClick={() => setPriceSyncChangeFilter(filter)}
                            >
                              <span className={styles.priceSyncChangeFilterLabel}>{filterLabel}</span>
                              <span className={styles.priceSyncChangeFilterCount}>{count}</span>
                            </button>
                          );
                        })}
                      </div>
                    </div>
                  </div>

                  <div className={styles.priceSyncChangeList}>
                    {filteredPriceSyncChanges.map((change) => {
                      const rateChanges = MODEL_PRICE_SYNC_RATE_FIELDS.filter(([field]) => (
                        change.after && (!change.before || change.before.base[field] !== change.after.base[field])
                      ));
                      const beforeTierCount = change.before?.tiers?.length ?? 0;
                      const afterTierCount = change.after?.tiers?.length ?? 0;
                      const overrideSelected = change.action === 'locked' && priceSyncLockedOverrideSet.has(change.model);
                      const displayedAction = overrideSelected ? 'overridden' : change.action;
                      return (
                        <article className={styles.priceSyncChangeRow} key={`${change.action}/${change.model}`}>
                          <div className={styles.priceSyncChangeIdentity}>
                            <span className={`${styles.priceSyncChangeBadge} ${styles[`priceSyncChange${displayedAction}`] ?? ''}`}>
                              {overrideSelected
                                ? t('usage_stats.model_price_sync_override_selected')
                                : t(`usage_stats.model_price_sync_change_${displayedAction}`)}
                            </span>
                            <div>
                              <strong title={change.model}>{change.model}</strong>
                              <small>
                                {change.sourceProvider
                                  ? `${change.sourceProvider}/${change.sourceModel || change.model}`
                                  : t('usage_stats.model_price_sync_change_no_source')}
                                {' · '}
                                {t('usage_stats.model_price_requests', { count: change.requests })}
                              </small>
                            </div>
                          </div>

                          {change.action !== 'unmatched' ? (
                            <div className={styles.priceSyncRateChanges}>
                              {rateChanges.map(([field, label]) => (
                                <div key={field}>
                                  <span>{t(label)}</span>
                                  <div>
                                    {change.before ? <del>{formatModelPriceRate(change.before.base[field])}</del> : null}
                                    {change.before ? <span aria-hidden="true">-&gt;</span> : null}
                                    <strong>{formatModelPriceRate(change.after?.base[field])}</strong>
                                  </div>
                                </div>
                              ))}
                              {beforeTierCount !== afterTierCount ? (
                                <div>
                                  <span>{t('usage_stats.model_price_context_tier')}</span>
                                  <div>
                                    <del>{beforeTierCount}</del>
                                    <span aria-hidden="true">-&gt;</span>
                                    <strong>{afterTierCount}</strong>
                                  </div>
                                </div>
                              ) : null}
                              {rateChanges.length === 0 && beforeTierCount === afterTierCount ? (
                                <small>{t(change.action === 'locked'
                                  ? overrideSelected
                                    ? 'usage_stats.model_price_sync_override_selected_hint'
                                    : 'usage_stats.model_price_sync_change_locked_hint'
                                  : 'usage_stats.model_price_sync_change_metadata_hint')}</small>
                              ) : null}
                              {change.action === 'locked' ? (
                                <label className={styles.priceSyncOverrideOption}>
                                  <input
                                    type="checkbox"
                                    checked={overrideSelected}
                                    onChange={(event) => setPriceSyncLockedOverrides((previous) => (
                                      event.target.checked
                                        ? Array.from(new Set([...previous, change.model]))
                                        : previous.filter((model) => model !== change.model)
                                    ))}
                                  />
                                  <span>{t(overrideSelected
                                    ? 'usage_stats.model_price_sync_override_selected'
                                    : 'usage_stats.model_price_sync_override_option')}</span>
                                </label>
                              ) : null}
                            </div>
                          ) : (
                            <span className={styles.priceSyncChangeHint}>{t('usage_stats.model_price_sync_change_unmatched_hint')}</span>
                          )}
                        </article>
                      );
                    })}
                    {filteredPriceSyncChanges.length === 0 ? (
                      <div className={styles.priceSyncChangesEmpty}>{t('usage_stats.model_price_sync_no_changes')}</div>
                    ) : null}
                  </div>
                </section>
              ) : unmatchedPriceModelCount > 0 ? (
                <section className={styles.priceSyncResultSection}>
                  <div className={styles.priceRuleSectionHeader}>
                    <div>
                      <h4>{t('usage_stats.model_price_sync_unmatched')}</h4>
                      <span>{unmatchedPriceModelCount}</span>
                    </div>
                  </div>
                  <div className={styles.priceUnmatchedList}>
                  {unmatchedPriceModels.map((item) => (
                    <div key={item.model}>
                      <span>
                        <strong title={item.model}>{item.model}</strong>
                        {item.alias ? <small title={item.alias}>{item.alias}</small> : null}
                      </span>
                      <small>{t('usage_stats.model_price_requests', { count: item.requests })}</small>
                    </div>
                  ))}
                  </div>
                </section>
              ) : null}

              <section className={styles.priceSyncSchedule}>
                <div>
                  <strong>{t('usage_stats.model_price_sync_schedule_title')}</strong>
                  <span>{t('usage_stats.model_price_sync_schedule_desc')}</span>
                </div>
                <div className={styles.priceSyncScheduleControls}>
                  <label className={styles.priceSyncScheduleToggle}>
                    <input
                      type="checkbox"
                      checked={monitoringSettingsDraft.modelPriceSyncEnabled}
                      onChange={(event) => setMonitoringSettingsDraft((previous) => ({ ...previous, modelPriceSyncEnabled: event.target.checked }))}
                    />
                    <span>{t('usage_stats.model_price_sync_schedule_enabled')}</span>
                  </label>
                  <label className={styles.priceSyncScheduleInterval}>
                    <span>{t('usage_stats.model_price_sync_schedule_interval')}</span>
                    <Input
                      type="number"
                      min="60"
                      step="60"
                      value={monitoringSettingsDraft.modelPriceSyncIntervalMinutes}
                      onChange={(event) => setMonitoringSettingsDraft((previous) => ({ ...previous, modelPriceSyncIntervalMinutes: event.target.value }))}
                      placeholder="1440"
                      disabled={!monitoringSettingsDraft.modelPriceSyncEnabled}
                    />
                  </label>
                  <Button
                    variant="secondary"
                    size="sm"
                    onClick={() => void handleSaveMonitoringSettings(false)}
                    disabled={isMonitoringSettingsLoading || isMonitoringSettingsSaving}
                  >
                    {isMonitoringSettingsSaving ? t('common.loading') : t('common.save')}
                  </Button>
                </div>
              </section>
            </div>
          )}
        </div>
      </Modal>
  );
}
