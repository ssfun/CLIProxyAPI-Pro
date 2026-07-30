import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Select } from '@/components/ui/Select';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/Table';
import {
  IconChevronDown,
  IconChevronUp,
  IconPencil,
  IconPlus,
  IconSearch,
  IconTrash2,
} from '@/components/ui/icons';
import type {
  ProxyPoolNodeConfig,
  ProxyPoolNodeStatus,
  ProxyPoolProbeResult,
} from '@/services/api/proxyPool';
import {
  formatProxyPoolSuccessRate,
  formatProxyPoolTime,
  maskProxyCredentials,
  proxyNodeKey,
  proxyPoolStateLabel,
  type ProxyPoolStatusFilter,
} from './proxyPoolUi';
import styles from './ProxyPool.module.scss';

export interface VisibleProxyPoolNode {
  node: ProxyPoolNodeConfig;
  index: number;
  runtime?: ProxyPoolNodeStatus;
  probe?: ProxyPoolProbeResult;
  state: ProxyPoolNodeStatus['state'];
  key: string;
}

interface ProxyPoolNodeManagerProps {
  nodes: ProxyPoolNodeConfig[];
  statusByID: Map<string, ProxyPoolNodeStatus>;
  probeResults: Record<string, ProxyPoolProbeResult>;
  query: string;
  statusFilter: ProxyPoolStatusFilter;
  selected: Set<string>;
  language: string;
  testing: boolean;
  onQueryChange: (value: string) => void;
  onStatusFilterChange: (value: ProxyPoolStatusFilter) => void;
  onSelectionChange: (value: Set<string>) => void;
  onEdit: (index: number) => void;
  onMove: (index: number, direction: -1 | 1) => void;
  onAdd: () => void;
  onImport: () => void;
  onBulkEnable: (enabled: boolean) => void;
  onBulkTest: () => void;
  onBulkDelete: () => void;
}

export function ProxyPoolNodeManager({
  nodes,
  statusByID,
  probeResults,
  query,
  statusFilter,
  selected,
  language,
  testing,
  onQueryChange,
  onStatusFilterChange,
  onSelectionChange,
  onEdit,
  onMove,
  onAdd,
  onImport,
  onBulkEnable,
  onBulkTest,
  onBulkDelete,
}: ProxyPoolNodeManagerProps) {
  const { t } = useTranslation();
  const visible = useMemo<VisibleProxyPoolNode[]>(() => {
    const normalizedQuery = query.trim().toLowerCase();
    return nodes
      .map((node, index) => {
        const key = proxyNodeKey(node, index);
        const runtime = statusByID.get(node.id);
        const probe = probeResults[key];
        const state = runtime?.state ?? (node.enabled ? 'unknown' : 'disabled');
        return { node, index, key, runtime, probe, state };
      })
      .filter(({ node, state }) => {
        const queryMatches =
          !normalizedQuery ||
          [node.id, node.label, node.url].some((value) =>
            value.toLowerCase().includes(normalizedQuery)
          );
        return queryMatches && (statusFilter === 'all' || state === statusFilter);
      });
  }, [nodes, probeResults, query, statusByID, statusFilter]);

  const visibleKeys = visible.map((item) => item.key);
  const allVisibleSelected =
    visibleKeys.length > 0 && visibleKeys.every((key) => selected.has(key));
  const toggleVisible = () => {
    const next = new Set(selected);
    if (allVisibleSelected) visibleKeys.forEach((key) => next.delete(key));
    else visibleKeys.forEach((key) => next.add(key));
    onSelectionChange(next);
  };
  const toggleOne = (key: string) => {
    const next = new Set(selected);
    if (next.has(key)) next.delete(key);
    else next.add(key);
    onSelectionChange(next);
  };

  return (
    <section className={styles.nodePanel}>
      <div className={styles.panelHeading}>
        <div>
          <h2>{t('proxy_pool.nodes', { defaultValue: 'Proxy nodes' })}</h2>
          <p>
            {t('proxy_pool.nodes_operational_hint', {
              defaultValue: 'Select a node to edit it. Rotation follows the order shown below.',
            })}
          </p>
        </div>
        <div className={styles.panelActions}>
          <Button variant="ghost" size="sm" onClick={onImport}>
            {t('proxy_pool.batch_import', { defaultValue: 'Import' })}
          </Button>
          <Button variant="secondary" size="sm" onClick={onAdd}>
            <IconPlus size={16} />
            {t('proxy_pool.add_node', { defaultValue: 'Add node' })}
          </Button>
        </div>
      </div>

      <div className={styles.nodeFilters}>
        <label className={styles.searchField}>
          <IconSearch size={17} />
          <input
            value={query}
            onChange={(event) => onQueryChange(event.target.value)}
            placeholder={t('proxy_pool.search_nodes', { defaultValue: 'Search name, ID, or URL' })}
            aria-label={t('proxy_pool.search_nodes', { defaultValue: 'Search name, ID, or URL' })}
          />
        </label>
        <Select
          value={statusFilter}
          onChange={(value) => onStatusFilterChange(value as ProxyPoolStatusFilter)}
          size="sm"
          ariaLabel={t('proxy_pool.filter_status', { defaultValue: 'Filter by status' })}
          options={[
            { value: 'all', label: t('proxy_pool.status_all', { defaultValue: 'All statuses' }) },
            {
              value: 'healthy',
              label: t('proxy_pool.status_healthy', { defaultValue: 'Healthy' }),
            },
            {
              value: 'degraded',
              label: t('proxy_pool.status_degraded', { defaultValue: 'Degraded' }),
            },
            {
              value: 'isolated',
              label: t('proxy_pool.status_isolated', { defaultValue: 'Isolated' }),
            },
            {
              value: 'disabled',
              label: t('proxy_pool.status_disabled', { defaultValue: 'Disabled' }),
            },
          ]}
        />
        <span className={styles.filterCount}>
          {t('proxy_pool.node_count', {
            defaultValue: '{{visible}} of {{total}} nodes',
            visible: visible.length,
            total: nodes.length,
          })}
        </span>
      </div>

      {selected.size > 0 && (
        <div
          className={styles.bulkBar}
          role="toolbar"
          aria-label={t('proxy_pool.bulk_actions', { defaultValue: 'Selected node actions' })}
        >
          <strong>
            {t('proxy_pool.selected_count', {
              defaultValue: '{{count}} selected',
              count: selected.size,
            })}
          </strong>
          <div>
            <Button variant="ghost" size="sm" onClick={() => onBulkEnable(true)}>
              {t('proxy_pool.enable', { defaultValue: 'Enable' })}
            </Button>
            <Button variant="ghost" size="sm" onClick={() => onBulkEnable(false)}>
              {t('proxy_pool.disable', { defaultValue: 'Disable' })}
            </Button>
            <Button variant="secondary" size="sm" loading={testing} onClick={onBulkTest}>
              {t('proxy_pool.test_selected', { defaultValue: 'Test selected' })}
            </Button>
            <Button variant="danger" size="sm" onClick={onBulkDelete}>
              <IconTrash2 size={15} />
              {t('common.delete')}
            </Button>
          </div>
        </div>
      )}

      {visible.length === 0 ? (
        <div className={styles.emptyState}>
          <strong>
            {nodes.length === 0
              ? t('proxy_pool.no_nodes', { defaultValue: 'No proxy nodes yet' })
              : t('proxy_pool.no_matching_nodes', { defaultValue: 'No matching nodes' })}
          </strong>
          <p>
            {nodes.length === 0
              ? t('proxy_pool.no_nodes_hint', {
                  defaultValue: 'Add a node or import a proxy list to get started.',
                })
              : t('proxy_pool.no_matching_nodes_hint', {
                  defaultValue: 'Try a different search or status filter.',
                })}
          </p>
          {nodes.length === 0 && (
            <Button variant="secondary" size="sm" onClick={onAdd}>
              <IconPlus size={16} />
              {t('proxy_pool.add_first_node', { defaultValue: 'Add first node' })}
            </Button>
          )}
        </div>
      ) : (
        <>
          <div className={styles.desktopTable}>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className={styles.checkboxCell}>
                    <input
                      type="checkbox"
                      checked={allVisibleSelected}
                      onChange={toggleVisible}
                      aria-label={t('proxy_pool.select_visible', {
                        defaultValue: 'Select visible nodes',
                      })}
                    />
                  </TableHead>
                  <TableHead>{t('proxy_pool.status', { defaultValue: 'Status' })}</TableHead>
                  <TableHead>{t('proxy_pool.node', { defaultValue: 'Node' })}</TableHead>
                  <TableHead>
                    {t('proxy_pool.exit_location', { defaultValue: 'Exit / location' })}
                  </TableHead>
                  <TableHead>{t('proxy_pool.latency', { defaultValue: 'Latency' })}</TableHead>
                  <TableHead>{t('proxy_pool.success_rate', { defaultValue: 'Success' })}</TableHead>
                  <TableHead>
                    {t('proxy_pool.node_active_tunnels', { defaultValue: 'Active' })}
                  </TableHead>
                  <TableHead>
                    {t('proxy_pool.last_check', { defaultValue: 'Last check' })}
                  </TableHead>
                  <TableHead alignRight>
                    {t('proxy_pool.actions', { defaultValue: 'Actions' })}
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {visible.map(({ node, index, key, runtime, probe, state }) => (
                  <TableRow
                    key={key}
                    selected={selected.has(key)}
                    className={styles.nodeRow}
                    onDoubleClick={() => onEdit(index)}
                  >
                    <TableCell className={styles.checkboxCell}>
                      <input
                        type="checkbox"
                        checked={selected.has(key)}
                        onChange={() => toggleOne(key)}
                        aria-label={t('proxy_pool.select_node', {
                          defaultValue: 'Select {{name}}',
                          name: node.label || node.id,
                        })}
                      />
                    </TableCell>
                    <TableCell>
                      <span className={`${styles.stateBadge} ${styles[`state_${state}`]}`}>
                        {t(`proxy_pool.state_${state}`, {
                          defaultValue: proxyPoolStateLabel(state),
                        })}
                      </span>
                    </TableCell>
                    <TableCell className={styles.nodeIdentity}>
                      <strong>{node.label || node.id}</strong>
                      <code title={maskProxyCredentials(node.url)}>
                        {maskProxyCredentials(node.url)}
                      </code>
                    </TableCell>
                    <TableCell className={styles.exitCell}>
                      <strong>{probe?.exitIp || runtime?.exitIp || '-'}</strong>
                      <span>{probe?.location || runtime?.location || '-'}</span>
                    </TableCell>
                    <TableCell>
                      {probe?.latencyMs || runtime?.latencyMs
                        ? `${probe?.latencyMs || runtime?.latencyMs} ms`
                        : '-'}
                    </TableCell>
                    <TableCell>
                      {formatProxyPoolSuccessRate(
                        runtime?.successConnects ?? 0,
                        runtime?.totalConnects ?? 0
                      )}
                    </TableCell>
                    <TableCell>{runtime?.activeTunnels ?? 0}</TableCell>
                    <TableCell className={styles.timeCell}>
                      {formatProxyPoolTime(runtime?.lastCheck || probe?.checkedAt || '', language)}
                    </TableCell>
                    <TableCell alignRight>
                      <div className={styles.rowActions}>
                        <button
                          type="button"
                          onClick={() => onMove(index, -1)}
                          disabled={index === 0}
                          aria-label={t('proxy_pool.move_up', { defaultValue: 'Move node up' })}
                        >
                          <IconChevronUp size={16} />
                        </button>
                        <button
                          type="button"
                          onClick={() => onMove(index, 1)}
                          disabled={index === nodes.length - 1}
                          aria-label={t('proxy_pool.move_down', { defaultValue: 'Move node down' })}
                        >
                          <IconChevronDown size={16} />
                        </button>
                        <button
                          type="button"
                          onClick={() => onEdit(index)}
                          aria-label={t('proxy_pool.edit_node', { defaultValue: 'Edit node' })}
                        >
                          <IconPencil size={16} />
                        </button>
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>

          <div className={styles.mobileCards}>
            {visible.map(({ node, index, key, runtime, probe, state }) => (
              <article
                key={key}
                className={`${styles.mobileNodeCard} ${selected.has(key) ? styles.mobileNodeSelected : ''}`}
              >
                <div className={styles.mobileNodeTop}>
                  <input
                    type="checkbox"
                    checked={selected.has(key)}
                    onChange={() => toggleOne(key)}
                    aria-label={t('proxy_pool.select_node', {
                      defaultValue: 'Select {{name}}',
                      name: node.label || node.id,
                    })}
                  />
                  <div>
                    <strong>{node.label || node.id}</strong>
                    <code>{maskProxyCredentials(node.url)}</code>
                  </div>
                  <button
                    type="button"
                    className={styles.iconButton}
                    onClick={() => onEdit(index)}
                    aria-label={t('proxy_pool.edit_node', { defaultValue: 'Edit node' })}
                  >
                    <IconPencil size={17} />
                  </button>
                </div>
                <div className={styles.mobileNodeStats}>
                  <span className={`${styles.stateBadge} ${styles[`state_${state}`]}`}>
                    {t(`proxy_pool.state_${state}`, { defaultValue: proxyPoolStateLabel(state) })}
                  </span>
                  <span>
                    <small>{t('proxy_pool.latency', { defaultValue: 'Latency' })}</small>
                    <strong>
                      {probe?.latencyMs || runtime?.latencyMs
                        ? `${probe?.latencyMs || runtime?.latencyMs} ms`
                        : '-'}
                    </strong>
                  </span>
                  <span>
                    <small>{t('proxy_pool.location', { defaultValue: 'Location' })}</small>
                    <strong>
                      {probe?.location ||
                        runtime?.location ||
                        probe?.exitIp ||
                        runtime?.exitIp ||
                        '-'}
                    </strong>
                  </span>
                  <span>
                    <small>{t('proxy_pool.success_rate', { defaultValue: 'Success' })}</small>
                    <strong>
                      {formatProxyPoolSuccessRate(
                        runtime?.successConnects ?? 0,
                        runtime?.totalConnects ?? 0
                      )}
                    </strong>
                  </span>
                </div>
              </article>
            ))}
          </div>
        </>
      )}
    </section>
  );
}
