import { describe, expect, test } from 'bun:test';
import {
  buildModelPriceRule,
  collectSpeedChanges,
  collectServiceTierChanges,
  createServiceTierDraft,
  createSpeedDraft,
  createPriceDraft,
  formatDeltaPercent,
  normalizeServiceTierName,
  normalizeSpeedName,
  parsePriceValue,
  resolvePricingMode,
  validatePriceDraft,
} from '../src/pro/modules/monitoring/features/modelPricePresentation';

describe('model price presentation model', () => {
  test('creates editable strings without mutating the source rule', () => {
    const draft = createPriceDraft({
      id: 1,
      version: 2,
      model: 'gpt-test',
      source: 'manual',
      base: { input: 1, output: 2, cacheRead: 0.5, cacheWrite: 0.75, reasoning: 3 },
      tiers: [{ contextSize: 1000, input: 3, output: 4, cacheRead: 1, cacheWrite: 1.5, reasoning: 5 }],
      serviceTiers: {
        priority: { input: 6, output: 7, cacheRead: 0.6, cacheWrite: 0.7, reasoning: 8 },
      },
      speeds: {
        FAST: { input: 9, output: 10, cacheRead: 0.9, cacheWrite: 1.1, reasoning: 12 },
      },
    });

    expect(draft.input).toBe('1');
    expect(draft.reasoning).toBe('3');
    expect(draft.tiers[0]).toMatchObject({ contextSize: '1000', output: '4', reasoning: '5' });
    expect(draft.serviceTiers[0]).toMatchObject({ name: 'fast', output: '7', reasoning: '8' });
    expect(draft.speeds[0]).toMatchObject({ name: 'fast', output: '10', reasoning: '12' });

    const rebuilt = buildModelPriceRule('gpt-test', draft);
    expect(rebuilt.base.reasoning).toBe(3);
    expect(rebuilt.tiers?.[0].reasoning).toBe(5);
    expect(rebuilt.serviceTiers?.fast).toMatchObject({ output: 7, reasoning: 8 });
    expect(rebuilt.speeds?.fast).toMatchObject({ output: 10, reasoning: 12 });
  });

  test('keeps new advanced rates empty and clears removed overrides', () => {
    const draft = createPriceDraft({
      model: 'gpt-test',
      base: { input: 1, output: 2, cacheRead: 0.5, cacheWrite: 0.75, reasoning: 3 },
      serviceTiers: { priority: { input: 4, output: 5, cacheRead: 0.4, cacheWrite: 0.5 } },
    });
    expect(createServiceTierDraft()).toEqual({
      name: '', input: '', output: '', cacheRead: '', cacheWrite: '', reasoning: '',
    });
    expect(createSpeedDraft()).toEqual({
      name: '', input: '', output: '', cacheRead: '', cacheWrite: '', reasoning: '',
    });
    draft.serviceTiers = [];
    draft.speeds = [];
    expect(buildModelPriceRule('gpt-test', draft).serviceTiers).toEqual({});
    expect(buildModelPriceRule('gpt-test', draft).speeds).toEqual({});
  });

  test('keeps missing base rates empty when creating a service tier', () => {
    expect(createServiceTierDraft()).toEqual({
      name: '',
      input: '',
      output: '',
      cacheRead: '',
      cacheWrite: '',
      reasoning: '',
    });
  });

  test('validates normalized duplicate service tiers and invalid rates', () => {
    const draft = createPriceDraft({
      model: 'gpt-test',
      base: { input: 1, output: 2, cacheRead: 0.5, cacheWrite: 0.75 },
      serviceTiers: {
        priority: { input: 4, output: 5, cacheRead: 0.4, cacheWrite: 0.5 },
      },
    });
    draft.serviceTiers.push({ ...draft.serviceTiers[0], name: ' PRIORITY ' });
    expect(validatePriceDraft(draft)).toBe('service_tier_name_duplicate');
    draft.serviceTiers[1].name = 'flex';
    draft.serviceTiers[1].output = '';
    expect(validatePriceDraft(draft)).toBe('rate_required');
  });

  test('describes service-tier sync changes', () => {
    const rate = { input: 1, output: 2, cacheRead: 0.5, cacheWrite: 0.75 };
    expect(collectServiceTierChanges(
      { priority: rate, removed: rate },
      { fast: { ...rate, output: 3 }, flex: rate }
    )).toEqual([
      { name: 'fast', action: 'updated' },
      { name: 'flex', action: 'added' },
      { name: 'removed', action: 'removed' },
    ]);
  });

  test('validates and describes speed overrides independently', () => {
    const rate = { input: 1, output: 2, cacheRead: 0.5, cacheWrite: 0.75 };
    const draft = createPriceDraft({
      model: 'claude-test',
      base: rate,
      speeds: { fast: { ...rate, output: 4 } },
    });
    draft.speeds.push({ ...draft.speeds[0], name: ' FAST ' });
    expect(validatePriceDraft(draft)).toBe('speed_name_duplicate');
    draft.speeds[1].name = ' ';
    expect(validatePriceDraft(draft)).toBe('speed_name_required');
    expect(normalizeSpeedName(' FAST ')).toBe('fast');
    expect(collectSpeedChanges(
      { fast: rate, removed: rate },
      { FAST: { ...rate, output: 3 }, turbo: rate }
    )).toEqual([
      { name: 'fast', action: 'updated' },
      { name: 'removed', action: 'removed' },
      { name: 'turbo', action: 'added' },
    ]);
  });

  test('canonicalizes OpenAI priority as the fast compatibility alias', () => {
    expect(normalizeServiceTierName(' Priority ')).toBe('fast');
    expect(normalizeServiceTierName('FAST')).toBe('fast');
    expect(normalizeServiceTierName('flex')).toBe('flex');

    const draft = createPriceDraft({
      model: 'gpt-test',
      base: { input: 1, output: 2, cacheRead: 0.5, cacheWrite: 0.75 },
      serviceTiers: { fast: { input: 4, output: 5, cacheRead: 0.4, cacheWrite: 0.5 } },
    });
    draft.serviceTiers[0].name = ' Priority ';
    expect(createPriceDraft(buildModelPriceRule('gpt-test', draft)).serviceTiers[0].name).toBe('fast');
  });

  test('resolves explicit and legacy pricing modes without claiming a tier match', () => {
    expect(resolvePricingMode({ pricingMode: 'service_tier', contextTierSize: 0, serviceTier: 'priority' })).toBe('service_tier');
    expect(resolvePricingMode({ pricingMode: 'context', contextTierSize: 1000, serviceTier: 'flex' })).toBe('context');
    expect(resolvePricingMode({ pricingMode: 'speed', contextTierSize: 0, serviceTier: '' })).toBe('speed');
    expect(resolvePricingMode({ contextTierSize: 0, serviceTier: 'priority' })).toBe('legacy_unknown');
    expect(resolvePricingMode({ contextTierSize: 1000, serviceTier: 'priority' })).toBe('legacy_unknown');
    expect(resolvePricingMode({ contextTierSize: 1000, serviceTier: '' })).toBe('context');
    expect(resolvePricingMode({ contextTierSize: 0, serviceTier: '' })).toBe('base');
  });

  test('normalizes invalid rates and reports rounded deltas', () => {
    expect(parsePriceValue('-1')).toBe(0);
    expect(parsePriceValue('not-a-number')).toBe(0);
    expect(formatDeltaPercent(1.5, 1)).toBe('+50.0%');
    expect(formatDeltaPercent(0, 0)).toBe('0.0%');
  });
});


describe('price validation and serialization agree', () => {
  test('preserves accepted numeric notation when building the saved rule', () => {
    const draft = createPriceDraft({ model: 'test', base: { input: 1, output: 2, cacheRead: 0, cacheWrite: 0 } });
    draft.input = '0x10';
    draft.tiers = [{ ...draft, contextSize: '2e5' }];
    expect(validatePriceDraft(draft)).toBeNull();
    const rule = buildModelPriceRule('test', draft);
    expect(rule.base.input).toBe(16);
    expect(rule.tiers?.[0].contextSize).toBe(200000);
  });

  test('rejects unsafe context integers and partial price strings', () => {
    const draft = createPriceDraft({ model: 'test', base: { input: 1, output: 2, cacheRead: 0, cacheWrite: 0 } });
    draft.tiers = [{ ...draft, contextSize: '9007199254740992' }];
    expect(validatePriceDraft(draft)).toBe('context_size_invalid');
    expect(parsePriceValue('12invalid')).toBe(0);
  });
});
