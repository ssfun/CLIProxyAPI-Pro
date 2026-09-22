import { describe, expect, test } from 'bun:test';
import { createInstance } from 'i18next';
import { I18nextProvider } from 'react-i18next';
import { renderToStaticMarkup } from 'react-dom/server';
import { QuotaOverviewList } from '../src/pro/modules/apiKeyPolicy/QuotaOverviewList';
import { apiKeyPolicyLocales } from '../src/pro/apiKeyPolicyLocales';
import type {
  APIKeyPolicy,
  APIKeyQuota,
} from '../src/pro/modules/apiKeyPolicy/apiKeyPolicy';

const quota: APIKeyQuota = {
  enabled: true,
  cost: 25,
  epoch: 1,
  startedAtMs: 1,
  updatedAtMs: 1,
  period: { type: 'calendar_duration', unit: 'day', timezone: 'Asia/Shanghai' },
  usage: {
    requestsUsed: 146,
    totalTokensUsed: 8843000,
    costUsed: 9.890776,
    windowStartedAtMs: 1,
    windowEndsAtMs: Date.UTC(2026, 8, 22, 16),
    exhausted: [],
  },
};
async function render(
  value: APIKeyQuota | undefined,
  state = 'available',
  missing = false,
) {
  const i18n = createInstance();
  await i18n.init({
    lng: 'zh-CN',
    resources: { 'zh-CN': { translation: apiKeyPolicyLocales['zh-CN'] } },
    interpolation: { escapeValue: false },
  });
  const policy: APIKeyPolicy = {
    id: 'one',
    displayName: '市场部',
    state: 'configured',
    profileEnabled: false,
    activeProfileId: '',
    profiles: [],
    version: 1,
    createdAtMs: 1,
    updatedAtMs: 1,
    quota: value,
  };
  return renderToStaticMarkup(
    <I18nextProvider i18n={i18n}>
      <QuotaOverviewList
        rows={[
          {
            policy,
            binding: {
              maskedKey: 'sk***02',
              keyRef: 'ref',
              state: 'configured',
              weakKey: false,
            },
            summary: missing
              ? undefined
              : {
                  policyId: 'one',
                  policyVersion: 1,
                  quota: value,
                  admissionState: value?.enabled ? 'available' : 'disabled',
                },
            visualState: state,
          },
        ]}
        busy={false}
        revealDisabled={false}
        onEdit={() => {}}
        onReset={() => {}}
      />
    </I18nextProvider>,
  );
}

describe('quota overview display semantics', () => {
  test('emphasizes remaining budget, preserves exact usage and only meters configured limits', async () => {
    const html = await render(quota);
    expect(html).toContain('剩余 $15.11');
    expect(html).toContain('$9.890776');
    expect(html.match(/role="progressbar"/g)).toHaveLength(1);
    expect(html).toContain('Asia/Shanghai');
    expect(html).toContain('重置');
  });
  test('meters all configured limits and does not round a nonzero small cost to zero', async () => {
    const html = await render({
      ...quota,
      requests: 200,
      totalTokens: 10000000,
      usage: { ...quota.usage, costUsed: 0.001 },
    });
    expect(html.match(/role="progressbar"/g)).toHaveLength(3);
    expect(html).toContain('&lt; $0.01');
  });
  test('distinguishes disabled quotas from missing snapshots', async () => {
    const disabled = await render(undefined, 'disabled');
    expect(disabled).toContain('未启用（1）');
    expect(disabled).not.toContain('快照不可用');
    expect(disabled).not.toContain('role="progressbar"');
    const missing = await render(quota, 'unknown', true);
    expect(missing).toContain('快照不可用');
    expect(missing).not.toContain('role="progressbar"');
    expect(missing).not.toContain('剩余 $25');
  });
  test('places the most constrained metric first within a card', async () => {
    const html = await render({
      ...quota,
      requests: 150,
      totalTokens: 10000000,
    });
    expect(html.indexOf('剩余 4')).toBeLessThan(html.indexOf('剩余 115.7万'));
    expect(html.indexOf('剩余 115.7万')).toBeLessThan(
      html.indexOf('剩余 $15.11'),
    );
  });
  test('does not describe a rolling window endpoint as an automatic reset', async () => {
    const html = await render({
      ...quota,
      period: { type: 'past_duration', value: 1, unit: 'day' },
    });
    expect(html).toContain('过去 1');
    expect(html).not.toContain('2026');
  });
});
