import type {
  ModelPriceRule,
  ModelPriceSyncChangeAction,
} from '@/utils/usage';

export type PriceTierDraft = {
  contextSize: string;
  input: string;
  output: string;
  cacheRead: string;
  cacheWrite: string;
};

export type PriceDraft = {
  input: string;
  output: string;
  cacheRead: string;
  cacheWrite: string;
  tiers: PriceTierDraft[];
};

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

export const formatDeltaPercent = (current: number, previous: number) => {
  const roundedCurrent = roundCurrency(current);
  const roundedPrevious = roundCurrency(previous);
  if (roundedPrevious <= 0) return roundedCurrent > 0 ? '+100.0%' : '0.0%';
  const delta = (roundedCurrent - roundedPrevious) / roundedPrevious;
  return `${delta >= 0 ? '+' : ''}${(delta * 100).toFixed(1)}%`;
};

export const createPriceDraft = (rule?: ModelPriceRule): PriceDraft => ({
  input: rule ? String(rule.base.input) : '',
  output: rule ? String(rule.base.output) : '',
  cacheRead: rule ? String(rule.base.cacheRead) : '',
  cacheWrite: rule ? String(rule.base.cacheWrite) : '',
  tiers: rule?.tiers?.map((tier) => ({
    contextSize: String(tier.contextSize),
    input: String(tier.input),
    output: String(tier.output),
    cacheRead: String(tier.cacheRead),
    cacheWrite: String(tier.cacheWrite),
  })) ?? [],
});

export const parsePriceValue = (value: string) => {
  const parsed = Number.parseFloat(value);
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : 0;
};

export const parsePriceContextSize = (value: string) => {
  const parsed = Number.parseInt(value, 10);
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : 0;
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
] as const;
