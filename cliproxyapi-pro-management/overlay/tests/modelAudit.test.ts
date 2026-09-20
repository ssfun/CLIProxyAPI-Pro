import { describe, expect, test } from 'bun:test';
import type { TFunction } from 'i18next';
import { modelAuditItems, resolveModelAudit } from '../src/pro/modules/monitoring/features/modelAudit';
import { collectUsageDetailsWithEndpoint } from '../src/pro/modules/monitoring/features/usage';

const row = {
  model: 'gpt-6-astra', modelAlias: '', requestedModel: 'gpt-6-astra', effectiveModel: '',
  upstreamModel: 'gpt-6-astra', responseModel: 'gpt-6-astra', modelMatchStatus: 'match',
};

describe('model audit presentation', () => {
  test('keeps identical models compact', () => {
    expect(resolveModelAudit(row)).toMatchObject({ showSent: false, showResponse: false, status: 'match' });
  });
  test('shows a normal Profile mapping without marking it as a mismatch', () => {
    expect(resolveModelAudit({ ...row, requestedModel: 'smart' })).toMatchObject({ requested: 'smart', showSent: true, showResponse: false, status: 'match' });
  });
  test('shows substitution and variants with the server classification', () => {
    expect(resolveModelAudit({ ...row, responseModel: 'gpt-5.6-luna', modelMatchStatus: 'mismatch' })).toMatchObject({ showResponse: true, status: 'mismatch' });
    expect(resolveModelAudit({ ...row, responseModel: 'gpt-6-astra-2026-09-01', modelMatchStatus: 'variant' })).toMatchObject({ showResponse: true, status: 'variant' });
  });
  test('missing evidence stays unknown, while old aliases remain visible', () => {
    expect(resolveModelAudit({ ...row, responseModel: '' }).status).toBe('unknown');
    expect(resolveModelAudit({ ...row, requestedModel: '', modelAlias: 'smart', upstreamModel: '', responseModel: '', modelMatchStatus: '' })).toMatchObject({ requested: 'smart', legacyModel: 'gpt-6-astra', status: 'unknown' });
    expect(resolveModelAudit({ ...row, modelMatchStatus: 'unexpected' }).status).toBe('unknown');
  });
  test('normalizes audit fields on the shared snapshot, page and SSE payload path', () => {
    const details = collectUsageDetailsWithEndpoint({ apis: { '/v1/responses': { models: {
      'billing-model': { details: [{ timestamp: '2026-09-20T00:00:00Z', source: '', auth_index: '1',
        requested_model: 'smart', upstream_model: ' gpt-6-astra ', response_model: ' gpt-5.6-luna ', model_match_status: 'mismatch', tokens: { total_tokens: 1 } }] },
    } } } });
    expect(details[0]).toMatchObject({ requested_model: 'smart', upstream_model: 'gpt-6-astra', response_model: 'gpt-5.6-luna', model_match_status: 'mismatch', __modelName: 'billing-model' });
  });
});

 test('highlights name differences without inventing an upstream verdict', () => {
   const unknown = { ...row, requestedModel: 'codex-auto-review', upstreamModel: '', responseModel: 'gpt-5.6-luna' };
   expect(resolveModelAudit(unknown)).toMatchObject({ responseTone: 'different', status: 'unknown', unknownReason: 'missing_sent' });
   expect(resolveModelAudit({ ...row, requestedModel: 'smart' })).toMatchObject({ responseTone: 'different', status: 'match' });
   expect(resolveModelAudit({ ...row, responseModel: '' })).toMatchObject({ responseTone: '', unknownReason: 'missing_response' });
   expect(resolveModelAudit(row).responseTone).toBe('');
   expect(resolveModelAudit({ ...row, responseModel: 'other', modelMatchStatus: 'mismatch' }).responseTone).toBe('mismatch');
   expect(resolveModelAudit({ ...row, responseModel: 'variant', modelMatchStatus: 'variant' }).responseTone).toBe('variant');
   const items = modelAuditItems(unknown, ((key: string) => key) as TFunction);
   expect(items.find((item) => item.key === 'status')?.value).toBe('monitoring.model_unknown_missing_sent');
   expect(items.every((item) => typeof item.value === 'string')).toBe(true);
 });
