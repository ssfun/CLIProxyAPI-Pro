import type {
  ModelPriceRate,
  ModelPriceRule,
  ModelPriceSyncChangeAction,
  UsageCostBreakdown,
} from '@/pro/modules/monitoring/features/usage';

export type PriceRateDraft = {
  input: string;
  output: string;
  cacheRead: string;
  cacheWrite: string;
  reasoning: string;
};

export type PriceTierDraft = PriceRateDraft & {
  contextSize: string;
};

export type ServiceTierDraft = PriceRateDraft & {
  name: string;
};

export type SpeedDraft = PriceRateDraft & {
  name: string;
};

export type PriceDraft = PriceRateDraft & {
  tiers: PriceTierDraft[];
  serviceTiers: ServiceTierDraft[];
  speeds: SpeedDraft[];
};

export type PriceDraftValidationError =
  | 'rate_required'
  | 'context_size_invalid'
  | 'context_size_duplicate'
  | 'service_tier_name_required'
  | 'service_tier_name_duplicate'
  | 'speed_name_required'
  | 'speed_name_duplicate';

export type ServiceTierChange = {
  name: string;
  action: 'added' | 'removed' | 'updated';
};

export type SpeedChange = ServiceTierChange;

export type ResolvedPricingMode = 'base' | 'context' | 'service_tier' | 'speed' | 'legacy_unknown';

export type PriceManagementView = 'rules' | 'sync';
export type PriceSyncChangeFilter = 'all' | ModelPriceSyncChangeAction;

export type PriceRuleTarget = {
  key: string;
  model: string;
  requests: number;
  lastSeenAtMs: number;
  rule?: ModelPriceRule;
};

const roundCurrency = (value: number) => Math.round(value * 100) / 100;

const PRICE_RATE_FIELDS = ['input', 'output', 'cacheRead', 'cacheWrite', 'reasoning'] as const;

export const normalizeServiceTierName = (value: string) => {
  const normalized = value.trim().toLowerCase();
  return normalized === 'priority' ? 'fast' : normalized;
};

const canonicalizeServiceTiers = (serviceTiers: ModelPriceRule['serviceTiers']) => {
  const normalized: NonNullable<ModelPriceRule['serviceTiers']> = {};
  Object.entries(serviceTiers ?? {}).forEach(([rawName, rate]) => {
    const name = normalizeServiceTierName(rawName);
    if (!name) return;
    if (rawName.trim().toLowerCase() === 'priority' && normalized.fast) return;
    normalized[name] = rate;
  });
  return normalized;
};

export const normalizeSpeedName = (value: string) => value.trim().toLowerCase();

const canonicalizeSpeeds = (speeds: ModelPriceRule['speeds']) => {
  const normalized: NonNullable<ModelPriceRule['speeds']> = {};
  Object.entries(speeds ?? {}).forEach(([rawName, rate]) => {
    const name = normalizeSpeedName(rawName);
    if (name) normalized[name] = rate;
  });
  return normalized;
};

const createPriceRateDraft = (rate?: ModelPriceRate): PriceRateDraft => ({
  input: rate ? String(rate.input) : '',
  output: rate ? String(rate.output) : '',
  cacheRead: rate ? String(rate.cacheRead) : '',
  cacheWrite: rate ? String(rate.cacheWrite) : '',
  reasoning: rate ? String(rate.reasoning ?? 0) : '',
});

const parsePriceRateDraft = (rate: PriceRateDraft): ModelPriceRate => ({
  input: parsePriceValue(rate.input),
  output: parsePriceValue(rate.output),
  cacheRead: parsePriceValue(rate.cacheRead),
  cacheWrite: parsePriceValue(rate.cacheWrite),
  reasoning: parsePriceValue(rate.reasoning),
});

const isValidPriceRateDraft = (rate: PriceRateDraft) => PRICE_RATE_FIELDS.every((field) => {
  if (rate[field].trim() === '') return false;
  const parsed = Number(rate[field]);
  return Number.isFinite(parsed) && parsed >= 0;
});

export const formatDeltaPercent = (current: number, previous: number) => {
  const roundedCurrent = roundCurrency(current);
  const roundedPrevious = roundCurrency(previous);
  if (roundedPrevious <= 0) return roundedCurrent > 0 ? '+100.0%' : '0.0%';
  const delta = (roundedCurrent - roundedPrevious) / roundedPrevious;
  return `${delta >= 0 ? '+' : ''}${(delta * 100).toFixed(1)}%`;
};

export const createPriceDraft = (rule?: ModelPriceRule): PriceDraft => ({
  ...createPriceRateDraft(rule?.base),
  tiers: rule?.tiers?.map((tier) => ({
    contextSize: String(tier.contextSize),
    ...createPriceRateDraft(tier),
  })) ?? [],
  serviceTiers: Object.entries(canonicalizeServiceTiers(rule?.serviceTiers))
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([name, rate]) => ({ name, ...createPriceRateDraft(rate) })),
  speeds: Object.entries(canonicalizeSpeeds(rule?.speeds))
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([name, rate]) => ({ name, ...createPriceRateDraft(rate) })),
});

export const createServiceTierDraft = (): ServiceTierDraft => ({
  name: '',
  input: '',
  output: '',
  cacheRead: '',
  cacheWrite: '',
  reasoning: '',
});

export const createSpeedDraft = (): SpeedDraft => createServiceTierDraft();

export const validatePriceDraft = (draft: PriceDraft): PriceDraftValidationError | null => {
  if (!isValidPriceRateDraft(draft)) return 'rate_required';

  const contextSizes = new Set<number>();
  for (const tier of draft.tiers) {
    const contextSize = Number(tier.contextSize);
    if (!Number.isSafeInteger(contextSize) || contextSize <= 0) return 'context_size_invalid';
    if (contextSizes.has(contextSize)) return 'context_size_duplicate';
    contextSizes.add(contextSize);
    if (!isValidPriceRateDraft(tier)) return 'rate_required';
  }

  const serviceTierNames = new Set<string>();
  for (const tier of draft.serviceTiers) {
    const name = normalizeServiceTierName(tier.name);
    if (!name) return 'service_tier_name_required';
    if (serviceTierNames.has(name)) return 'service_tier_name_duplicate';
    serviceTierNames.add(name);
    if (!isValidPriceRateDraft(tier)) return 'rate_required';
  }
  const speedNames = new Set<string>();
  for (const speed of draft.speeds) {
    const name = normalizeSpeedName(speed.name);
    if (!name) return 'speed_name_required';
    if (speedNames.has(name)) return 'speed_name_duplicate';
    speedNames.add(name);
    if (!isValidPriceRateDraft(speed)) return 'rate_required';
  }
  return null;
};

export const buildModelPriceRule = (model: string, draft: PriceDraft): ModelPriceRule => {
  const serviceTiers = Object.fromEntries(draft.serviceTiers.map((tier) => (
    [normalizeServiceTierName(tier.name), parsePriceRateDraft(tier)]
  )));
  const speeds = Object.fromEntries(draft.speeds.map((speed) => (
    [normalizeSpeedName(speed.name), parsePriceRateDraft(speed)]
  )));
  return {
    model,
    base: parsePriceRateDraft(draft),
    tiers: draft.tiers.map((tier) => ({
      contextSize: parsePriceContextSize(tier.contextSize),
      ...parsePriceRateDraft(tier),
    })),
    serviceTiers,
    speeds,
  };
};

export const collectServiceTierChanges = (
  before: ModelPriceRule['serviceTiers'],
  after: ModelPriceRule['serviceTiers']
): ServiceTierChange[] => {
  const previous = canonicalizeServiceTiers(before);
  const next = canonicalizeServiceTiers(after);
  return Array.from(new Set([...Object.keys(previous), ...Object.keys(next)]))
    .sort((left, right) => left.localeCompare(right))
    .flatMap((name): ServiceTierChange[] => {
      if (!(name in previous)) return [{ name, action: 'added' }];
      if (!(name in next)) return [{ name, action: 'removed' }];
      return PRICE_RATE_FIELDS.some((field) => (previous[name][field] ?? 0) !== (next[name][field] ?? 0))
        ? [{ name, action: 'updated' }]
        : [];
    });
};

export const collectSpeedChanges = (
  before: ModelPriceRule['speeds'],
  after: ModelPriceRule['speeds']
): SpeedChange[] => {
  const previous = canonicalizeSpeeds(before);
  const next = canonicalizeSpeeds(after);
  return Array.from(new Set([...Object.keys(previous), ...Object.keys(next)]))
    .sort((left, right) => left.localeCompare(right))
    .flatMap((name): SpeedChange[] => {
      if (!(name in previous)) return [{ name, action: 'added' }];
      if (!(name in next)) return [{ name, action: 'removed' }];
      return PRICE_RATE_FIELDS.some((field) => (previous[name][field] ?? 0) !== (next[name][field] ?? 0))
        ? [{ name, action: 'updated' }]
        : [];
    });
};

export const resolvePricingMode = (
  breakdown: Pick<UsageCostBreakdown, 'pricingMode' | 'contextTierSize' | 'serviceTier'>
    & Partial<Pick<UsageCostBreakdown, 'speed'>>
): ResolvedPricingMode => {
  if (breakdown.pricingMode === 'base' || breakdown.pricingMode === 'context' || breakdown.pricingMode === 'service_tier' || breakdown.pricingMode === 'speed') {
    return breakdown.pricingMode;
  }
  if (breakdown.serviceTier || breakdown.speed) return 'legacy_unknown';
  if (breakdown.contextTierSize > 0) return 'context';
  return 'base';
};

export const parsePriceValue = (value: string) => {
  const parsed = Number(value);
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : 0;
};

export const parsePriceContextSize = (value: string) => {
  const parsed = Number(value);
  return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : 0;
};

export const formatModelPriceRate = (value: number | undefined) => {
  const normalized = Number(value) || 0;
  return `$${normalized.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 6 })}`;
};

export const MODEL_PRICE_SYNC_RATE_FIELDS = [
  ['input', 'usage_stats.model_price_input'],
  ['output', 'usage_stats.model_price_output'],
  ['cacheRead', 'usage_stats.model_price_cache_read'],
  ['cacheWrite', 'usage_stats.model_price_cache_write'],
  ['reasoning', 'usage_stats.model_price_reasoning'],
] as const;
