import i18n from '@/i18n';
import { apiClient } from '@/services/api/client';
import { maskApiKey } from '@/utils/format';
import { normalizeAuthIndex } from '@/utils/authIndex';
import { parseTimestampMs } from '@/utils/timestamp';

export { normalizeAuthIndex };

export interface ModelPrice {
  prompt: number;
  completion: number;
  cache: number;
}

export interface ModelPriceRate {
  input: number;
  output: number;
  cacheRead: number;
  cacheWrite: number;
  reasoning?: number;
}

export interface ModelPriceTier extends ModelPriceRate {
  contextSize: number;
}

export interface ModelPriceRule {
  id?: number;
  model: string;
  base: ModelPriceRate;
  tiers?: ModelPriceTier[];
  serviceTiers?: Record<string, ModelPriceRate>;
  speeds?: Record<string, ModelPriceRate>;
  source?: string;
  sourceProvider?: string;
  sourceModel?: string;
  locked?: boolean;
  version?: number;
  effectiveFromMs?: number;
  fetchedAtMs?: number;
  upstreamUpdated?: string;
  updatedAtMs?: number;
}

export interface ObservedModelPriceTarget {
  provider: string;
  model: string;
  alias?: string;
  requests: number;
  lastSeenAtMs: number;
}

export interface ModelPriceSyncState {
  status: string;
  lastAttemptMs?: number;
  lastSuccessMs?: number;
  matched?: number;
  added?: number;
  updated?: number;
  unchanged?: number;
  locked?: number;
  unmatched?: number;
  unmatchedModels?: ObservedModelPriceTarget[];
  recalculated?: number;
  error?: string;
}

export interface ModelPriceSyncResult {
  dryRun: boolean;
  notModified: boolean;
  matched: number;
  added: number;
  updated: number;
  overridden: number;
  unchanged: number;
  locked: number;
  unmatched: ObservedModelPriceTarget[];
  changes: ModelPriceSyncChange[];
  recalculated: number;
}

export type ModelPriceSyncChangeAction = 'added' | 'updated' | 'overridden' | 'locked' | 'unmatched';

export interface ModelPriceSyncChange {
  action: ModelPriceSyncChangeAction;
  model: string;
  requests: number;
  sourceProvider?: string;
  sourceModel?: string;
  before?: ModelPriceRule;
  after?: ModelPriceRule;
}

export interface ModelsDevPriceSearchItem {
  provider: string;
  providerName?: string;
  model: string;
  modelName?: string;
  lastUpdated?: string;
  rule: ModelPriceRule;
}

export interface UsageTokens {
  input_tokens?: number;
  output_tokens?: number;
  reasoning_tokens?: number;
  cached_tokens?: number;
  cache_read_tokens?: number;
  cache_creation_tokens?: number;
  cache_tokens?: number;
  cache_write_tokens?: number;
  cache_input_tokens?: number;
  total_tokens?: number;
}

export interface UsageTokenBreakdown {
  schema_version: number;
  quality: string;
  total_tokens: number;
  input: {
    total_tokens: number;
    uncached_tokens: number;
    cache_read_tokens: number;
    cache_write_tokens: number;
  };
  output: {
    total_tokens: number;
    non_reasoning_tokens: number;
    reasoning_tokens: number;
  };
  unclassified_tokens: number;
}

export interface UsageCostBreakdown {
  ruleId: number;
  ruleVersion: number;
  provider: string;
  model: string;
  source: string;
  contextTokens: number;
  contextTierSize: number;
  serviceTier: string;
  requestedServiceTier: string;
  effectiveServiceTier: string;
  matchedServiceTier: string;
  serviceTierSource?: 'response' | 'request_fallback' | 'codex_oauth_request' | 'none';
  speed: string;
  requestedSpeed: string;
  effectiveSpeed: string;
  matchedSpeed: string;
  speedSource?: 'response' | 'request_fallback' | 'none';
  pricingMode?: 'base' | 'context' | 'service_tier' | 'speed';
  inputTokens: number;
  outputTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  reasoningTokens: number;
  inputCost: number;
  outputCost: number;
  cacheReadCost: number;
  cacheWriteCost: number;
  reasoningCost: number;
  totalCost: number;
}

export interface UsageDetail {
  timestamp: string;
  source: string;
  auth_index: string | number | null;
  api_key_hash?: string;
  api_key_policy_id?: string;
  profile_id?: string;
  profile_name_snapshot?: string;
  policy_mode?: string;
  requested_model?: string;
  effective_model?: string;
  upstream_model?: string;
  response_model?: string;
  model_match_status?: string;
  provider?: string;
  executor_type?: string;
  alias?: string;
  auth_type?: string;
  client_ip?: string;
  x_forwarded_for?: string;
  user_agent?: string;
  latency_ms?: number;
  ttft_ms?: number;
  status_code?: number;
  error_code?: string;
  error_message?: string;
  upstream_request_id?: string;
  retry_after?: string;
  attempt_index?: number;
  stream?: boolean;
  reasoning_effort?: string;
  service_tier?: string;
  effective_service_tier?: string;
  speed?: string;
  effective_speed?: string;
  estimated_cost?: number;
  price_rule_id?: number;
  cost_breakdown?: UsageCostBreakdown;
  accounting_version?: number;
  accounting_quality?: string;
  token_breakdown?: UsageTokenBreakdown;
  tokens: UsageTokens;
  failed: boolean;
  __modelName?: string;
  __timestampMs?: number;
}

export interface UsageDetailWithEndpoint extends UsageDetail {
  __endpoint: string;
  __endpointMethod?: string;
  __endpointPath?: string;
  __timestampMs: number;
}

export interface DurationFormatOptions {
  maxUnits?: number;
  invalidText?: string;
  secondDecimals?: number | 'auto';
  locale?: string;
}

const TOKENS_PER_PRICE_UNIT = 1_000_000;
const MODEL_PRICE_STORAGE_KEY = 'cli-proxy-model-prices-v2';
const MODEL_PRICE_API_PATH = '/usage/model-prices';
const USAGE_ENDPOINT_METHOD_REGEX = /^(GET|POST|PUT|PATCH|DELETE|OPTIONS|HEAD)\s+(\S+)/i;
const USAGE_SOURCE_PREFIX_KEY = 'k:';
const USAGE_SOURCE_PREFIX_MASKED = 'm:';
const USAGE_SOURCE_PREFIX_TEXT = 't:';
const KEY_LIKE_TOKEN_REGEX =
  /(sk-[A-Za-z0-9-_]{6,}|sk-ant-[A-Za-z0-9-_]{6,}|AIza[0-9A-Za-z-_]{8,}|AI[a-zA-Z0-9_-]{6,}|hf_[A-Za-z0-9]{6,}|pk_[A-Za-z0-9]{6,}|rk_[A-Za-z0-9]{6,})/;
const MASKED_TOKEN_HINT_REGEX = /^[^\s]{1,24}(\*{2,}|\.{3})[^\s]{1,24}$/;

const KEY_FINGERPRINT_CACHE_LIMIT = 2048;
const keyFingerprintCache = new Map<string, string>();
const usageDetailsWithEndpointCache = new WeakMap<object, UsageDetailWithEndpoint[]>();

const isRecord = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value);

const toFiniteNumber = (value: unknown): number => {
  const numberValue = typeof value === 'number' ? value : Number(value);
  return Number.isFinite(numberValue) ? numberValue : 0;
};

const getApisRecord = (usageData: unknown): Record<string, unknown> | null => {
  const usageRecord = isRecord(usageData) ? usageData : null;
  const apisRaw = usageRecord ? usageRecord.apis : null;
  return isRecord(apisRaw) ? apisRaw : null;
};

const fnv1a64Hex = (value: string): string => {
  const cached = keyFingerprintCache.get(value);
  if (cached) return cached;

  const FNV_OFFSET_BASIS = 0xcbf29ce484222325n;
  const FNV_PRIME = 0x100000001b3n;

  let hash = FNV_OFFSET_BASIS;
  for (let i = 0; i < value.length; i += 1) {
    hash ^= BigInt(value.charCodeAt(i));
    hash = (hash * FNV_PRIME) & 0xffffffffffffffffn;
  }

  const hex = hash.toString(16).padStart(16, '0');
  if (keyFingerprintCache.size >= KEY_FINGERPRINT_CACHE_LIMIT) {
    const oldestKey = keyFingerprintCache.keys().next().value;
    if (oldestKey) keyFingerprintCache.delete(oldestKey);
  }
  keyFingerprintCache.set(value, hex);
  return hex;
};

const looksLikeRawSecret = (text: string): boolean => {
  if (!text || /\s/.test(text)) return false;

  const lower = text.toLowerCase();
  if (lower.endsWith('.json')) return false;
  if (lower.startsWith('http://') || lower.startsWith('https://')) return false;
  if (/[\\/]/.test(text)) return false;
  if (KEY_LIKE_TOKEN_REGEX.test(text)) return true;
  if (text.length >= 32 && text.length <= 512) return true;
  if (text.length >= 16 && text.length < 32 && /^[A-Za-z0-9._=-]+$/.test(text)) {
    return /[A-Za-z]/.test(text) && /\d/.test(text);
  }
  return false;
};

const extractRawSecretFromText = (text: string): string | null => {
  if (!text) return null;
  if (looksLikeRawSecret(text)) return text;

  const keyLikeMatch = text.match(KEY_LIKE_TOKEN_REGEX);
  if (keyLikeMatch?.[0]) return keyLikeMatch[0];

  const queryMatch = text.match(
    /(?:[?&])(api[-_]?key|key|token|access_token|authorization)=([^&#\s]+)/i
  );
  const queryValue = queryMatch?.[2];
  if (queryValue && looksLikeRawSecret(queryValue)) return queryValue;

  const headerMatch = text.match(
    /(api[-_]?key|key|token|access[-_]?token|authorization)\s*[:=]\s*([A-Za-z0-9._=-]+)/i
  );
  const headerValue = headerMatch?.[2];
  if (headerValue && looksLikeRawSecret(headerValue)) return headerValue;

  const bearerMatch = text.match(/\bBearer\s+([A-Za-z0-9._=-]{6,})/i);
  const bearerValue = bearerMatch?.[1];
  return bearerValue && looksLikeRawSecret(bearerValue) ? bearerValue : null;
};

function normalizeUsageSourceId(
  value: unknown,
  masker: (val: string) => string = maskApiKey
): string {
  const raw =
    typeof value === 'string' ? value : value === null || value === undefined ? '' : String(value);
  const trimmed = raw.trim();
  if (!trimmed) return '';

  if (trimmed.startsWith(USAGE_SOURCE_PREFIX_MASKED)) {
    return `${USAGE_SOURCE_PREFIX_MASKED}${trimmed.slice(USAGE_SOURCE_PREFIX_MASKED.length)}`;
  }

  const extracted = extractRawSecretFromText(trimmed);
  if (extracted) return `${USAGE_SOURCE_PREFIX_KEY}${fnv1a64Hex(extracted)}`;
  if (MASKED_TOKEN_HINT_REGEX.test(trimmed)) {
    return `${USAGE_SOURCE_PREFIX_MASKED}${masker(trimmed)}`;
  }
  return `${USAGE_SOURCE_PREFIX_TEXT}${trimmed}`;
}

export function buildCandidateUsageSourceIds(input: {
  apiKey?: string;
  prefix?: string;
}): string[] {
  const result: string[] = [];
  const prefix = input.prefix?.trim();
  if (prefix) result.push(`${USAGE_SOURCE_PREFIX_TEXT}${prefix}`);

  const apiKey = input.apiKey?.trim();
  if (apiKey) {
    result.push(normalizeUsageSourceId(apiKey));
    result.push(`${USAGE_SOURCE_PREFIX_TEXT}${maskApiKey(apiKey)}`);
  }

  return Array.from(new Set(result.filter(Boolean)));
}

function extractLatencyMs(detail: unknown): number | null {
  return extractNonNegativeNumberField(detail, ['latency_ms']);
}

const extractNonNegativeNumberField = (detail: unknown, keys: string[]): number | null => {
  const record = isRecord(detail) ? detail : null;
  const rawValue = keys.reduce<unknown>((found, key) => found ?? record?.[key], undefined);
  if (
    rawValue === null ||
    rawValue === undefined ||
    (typeof rawValue === 'string' && rawValue.trim() === '')
  ) {
    return null;
  }

  const parsed = Number(rawValue);
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : null;
};

const readTokens = (detail: Record<string, unknown>): UsageTokens => {
  const tokensRaw = isRecord(detail.tokens) ? detail.tokens : {};
  const cacheReadTokens = toFiniteNumber(tokensRaw.cache_read_tokens);
  const cacheCreationTokens = toFiniteNumber(tokensRaw.cache_creation_tokens);
  return {
    input_tokens: toFiniteNumber(tokensRaw.input_tokens),
    output_tokens: toFiniteNumber(tokensRaw.output_tokens),
    reasoning_tokens: toFiniteNumber(tokensRaw.reasoning_tokens),
    cached_tokens: toFiniteNumber(tokensRaw.cached_tokens),
    cache_read_tokens: cacheReadTokens,
    cache_creation_tokens: cacheCreationTokens,
    cache_tokens: toFiniteNumber(tokensRaw.cache_tokens) || cacheReadTokens + cacheCreationTokens,
    cache_write_tokens: toFiniteNumber(tokensRaw.cache_write_tokens),
    cache_input_tokens: toFiniteNumber(tokensRaw.cache_input_tokens),
    total_tokens: toFiniteNumber(tokensRaw.total_tokens),
  };
};

const normalizeUsageTokenBreakdown = (value: unknown): UsageTokenBreakdown | undefined => {
  if (!isRecord(value)) return undefined;
  const input = isRecord(value.input) ? value.input : {};
  const output = isRecord(value.output) ? value.output : {};
  const number = (snakeKey: string, camelKey: string, source: Record<string, unknown> = value) =>
    toFiniteNumber(source[snakeKey] ?? source[camelKey]);
  const schemaVersion = number('schema_version', 'schemaVersion');
  const qualityRaw = value.quality;
  const quality = typeof qualityRaw === 'string' ? qualityRaw.trim() : '';
  if (schemaVersion <= 0 && !quality) return undefined;
  return {
    schema_version: schemaVersion,
    quality,
    total_tokens: number('total_tokens', 'totalTokens'),
    input: {
      total_tokens: number('total_tokens', 'totalTokens', input),
      uncached_tokens: number('uncached_tokens', 'uncachedTokens', input),
      cache_read_tokens: number('cache_read_tokens', 'cacheReadTokens', input),
      cache_write_tokens: number('cache_write_tokens', 'cacheWriteTokens', input),
    },
    output: {
      total_tokens: number('total_tokens', 'totalTokens', output),
      non_reasoning_tokens: number('non_reasoning_tokens', 'nonReasoningTokens', output),
      reasoning_tokens: number('reasoning_tokens', 'reasoningTokens', output),
    },
    unclassified_tokens: number('unclassified_tokens', 'unclassifiedTokens'),
  };
};

const normalizeUsageCostBreakdown = (value: unknown): UsageCostBreakdown | undefined => {
  let raw = value;
  if (typeof raw === 'string') {
    try {
      raw = JSON.parse(raw);
    } catch {
      return undefined;
    }
  }
  if (!isRecord(raw)) return undefined;

  const readNumber = (camelKey: string, snakeKey: string) => toFiniteNumber(raw[camelKey] ?? raw[snakeKey]);
  const readString = (camelKey: string, snakeKey: string) => {
    const candidate = raw[camelKey] ?? raw[snakeKey];
    return typeof candidate === 'string' ? candidate : '';
  };
  const hasCostFields = ['totalCost', 'total_cost', 'inputCost', 'input_cost', 'outputCost', 'output_cost']
    .some((key) => raw[key] !== undefined);
  if (!hasCostFields) return undefined;
  const rawPricingMode = readString('pricingMode', 'pricing_mode');
  const pricingMode = rawPricingMode === 'base' || rawPricingMode === 'context' || rawPricingMode === 'service_tier' || rawPricingMode === 'speed'
    ? rawPricingMode
    : undefined;

  return {
    ruleId: readNumber('ruleId', 'rule_id'),
    ruleVersion: readNumber('ruleVersion', 'rule_version'),
    provider: readString('provider', 'provider'),
    model: readString('model', 'model'),
    source: readString('source', 'source'),
    contextTokens: readNumber('contextTokens', 'context_tokens'),
    contextTierSize: readNumber('contextTierSize', 'context_tier_size'),
    serviceTier: readString('serviceTier', 'service_tier'),
    requestedServiceTier: readString('requestedServiceTier', 'requested_service_tier') || readString('serviceTier', 'service_tier'),
    effectiveServiceTier: readString('effectiveServiceTier', 'effective_service_tier'),
    matchedServiceTier: readString('matchedServiceTier', 'matched_service_tier'),
    serviceTierSource: (() => {
      const source = readString('serviceTierSource', 'service_tier_source');
      return source === 'response' || source === 'request_fallback' || source === 'codex_oauth_request' || source === 'none' ? source : undefined;
    })(),
    speed: readString('speed', 'speed'),
    requestedSpeed: readString('requestedSpeed', 'requested_speed') || readString('speed', 'speed'),
    effectiveSpeed: readString('effectiveSpeed', 'effective_speed'),
    matchedSpeed: readString('matchedSpeed', 'matched_speed'),
    speedSource: (() => {
      const source = readString('speedSource', 'speed_source');
      return source === 'response' || source === 'request_fallback' || source === 'none' ? source : undefined;
    })(),
    pricingMode,
    inputTokens: readNumber('inputTokens', 'input_tokens'),
    outputTokens: readNumber('outputTokens', 'output_tokens'),
    cacheReadTokens: readNumber('cacheReadTokens', 'cache_read_tokens'),
    cacheWriteTokens: readNumber('cacheWriteTokens', 'cache_write_tokens'),
    reasoningTokens: readNumber('reasoningTokens', 'reasoning_tokens'),
    inputCost: readNumber('inputCost', 'input_cost'),
    outputCost: readNumber('outputCost', 'output_cost'),
    cacheReadCost: readNumber('cacheReadCost', 'cache_read_cost'),
    cacheWriteCost: readNumber('cacheWriteCost', 'cache_write_cost'),
    reasoningCost: readNumber('reasoningCost', 'reasoning_cost'),
    totalCost: readNumber('totalCost', 'total_cost'),
  };
};

const normalizeSourceWithCache = (sourceCache: Map<string, string>, value: unknown): string => {
  const raw =
    typeof value === 'string'
      ? value
      : value === null || value === undefined
        ? ''
        : String(value);
  const trimmed = raw.trim();
  if (!trimmed) return '';

  const cached = sourceCache.get(trimmed);
  if (cached !== undefined) return cached;

  const normalized = normalizeUsageSourceId(trimmed);
  sourceCache.set(trimmed, normalized);
  return normalized;
};

const buildUsageDetail = (
  detailRaw: unknown,
  modelName: string,
  sourceCache: Map<string, string>
): UsageDetail | null => {
  if (!isRecord(detailRaw) || typeof detailRaw.timestamp !== 'string') return null;

  const timestamp = detailRaw.timestamp;
  const timestampMs = parseTimestampMs(timestamp);
  const latencyMs = extractLatencyMs(detailRaw);
  const ttftMs = extractNonNegativeNumberField(detailRaw, ['ttft_ms']);
  const statusCode = extractNonNegativeNumberField(detailRaw, ['status_code']);
  const estimatedCost = extractNonNegativeNumberField(detailRaw, ['estimated_cost', 'estimatedCost']);
  const priceRuleID = extractNonNegativeNumberField(detailRaw, ['price_rule_id', 'priceRuleId']);
  const attemptIndex = extractNonNegativeNumberField(detailRaw, ['attempt_index', 'attemptIndex']);
  const accountingVersion = extractNonNegativeNumberField(detailRaw, ['accounting_version', 'accountingVersion']);

  const provider = typeof detailRaw.provider === 'string' ? detailRaw.provider.trim() : undefined;
  const executorType = typeof detailRaw.executor_type === 'string'
    ? detailRaw.executor_type.trim()
    : typeof detailRaw.executorType === 'string'
      ? detailRaw.executorType.trim()
      : undefined;
  const alias = typeof detailRaw.alias === 'string' ? detailRaw.alias.trim() : undefined;
  const authType = typeof detailRaw.auth_type === 'string'
    ? detailRaw.auth_type.trim()
    : typeof detailRaw.authType === 'string'
      ? (detailRaw.authType as string).trim()
      : undefined;

  return {
    timestamp,
    source: normalizeSourceWithCache(sourceCache, detailRaw.source),
    auth_index: (detailRaw.auth_index ??
      detailRaw.authIndex ??
      detailRaw.AuthIndex ??
      null) as UsageDetail['auth_index'],
    api_key_hash: typeof detailRaw.api_key_hash === 'string'
      ? detailRaw.api_key_hash
      : typeof detailRaw.apiKeyHash === 'string'
        ? detailRaw.apiKeyHash
        : undefined,
    api_key_policy_id: typeof detailRaw.api_key_policy_id === 'string'
      ? detailRaw.api_key_policy_id.trim()
      : typeof detailRaw.apiKeyPolicyId === 'string'
        ? detailRaw.apiKeyPolicyId.trim()
        : undefined,
    profile_id: typeof detailRaw.profile_id === 'string'
      ? detailRaw.profile_id.trim()
      : typeof detailRaw.profileId === 'string'
        ? detailRaw.profileId.trim()
        : undefined,
    profile_name_snapshot: typeof detailRaw.profile_name_snapshot === 'string'
      ? detailRaw.profile_name_snapshot.trim()
      : typeof detailRaw.profileNameSnapshot === 'string'
        ? detailRaw.profileNameSnapshot.trim()
        : undefined,
    policy_mode: typeof detailRaw.policy_mode === 'string'
      ? detailRaw.policy_mode.trim()
      : typeof detailRaw.policyMode === 'string'
        ? detailRaw.policyMode.trim()
        : undefined,
    requested_model: typeof detailRaw.requested_model === 'string'
      ? detailRaw.requested_model.trim()
      : typeof detailRaw.requestedModel === 'string'
        ? detailRaw.requestedModel.trim()
        : undefined,
    effective_model: typeof detailRaw.effective_model === 'string'
      ? detailRaw.effective_model.trim()
      : typeof detailRaw.effectiveModel === 'string'
        ? detailRaw.effectiveModel.trim()
        : undefined,
    upstream_model: typeof detailRaw.upstream_model === 'string' ? detailRaw.upstream_model.trim() : undefined,
    response_model: typeof detailRaw.response_model === 'string' ? detailRaw.response_model.trim() : undefined,
    model_match_status: typeof detailRaw.model_match_status === 'string' ? detailRaw.model_match_status : undefined,
    provider: provider || undefined,
    executor_type: executorType || undefined,
    alias: alias || undefined,
    auth_type: authType || undefined,
    client_ip: typeof detailRaw.client_ip === 'string'
      ? detailRaw.client_ip.trim()
      : typeof detailRaw.clientIp === 'string'
        ? detailRaw.clientIp.trim()
        : undefined,
    x_forwarded_for: typeof detailRaw.x_forwarded_for === 'string'
      ? detailRaw.x_forwarded_for.trim()
      : typeof detailRaw.xForwardedFor === 'string'
        ? detailRaw.xForwardedFor.trim()
        : undefined,
    user_agent: typeof detailRaw.user_agent === 'string'
      ? detailRaw.user_agent.trim()
      : typeof detailRaw.userAgent === 'string'
        ? detailRaw.userAgent.trim()
        : undefined,
    latency_ms: latencyMs ?? undefined,
    ttft_ms: ttftMs ?? undefined,
    status_code: statusCode ?? undefined,
    error_code: typeof detailRaw.error_code === 'string'
      ? detailRaw.error_code
      : typeof detailRaw.errorCode === 'string'
        ? detailRaw.errorCode
        : undefined,
    error_message: typeof detailRaw.error_message === 'string'
      ? detailRaw.error_message
      : typeof detailRaw.errorMessage === 'string'
        ? detailRaw.errorMessage
        : undefined,
    upstream_request_id: typeof detailRaw.upstream_request_id === 'string'
      ? detailRaw.upstream_request_id
      : typeof detailRaw.upstreamRequestId === 'string'
        ? detailRaw.upstreamRequestId
        : undefined,
    retry_after: typeof detailRaw.retry_after === 'string'
      ? detailRaw.retry_after
      : typeof detailRaw.retryAfter === 'string'
        ? detailRaw.retryAfter
        : undefined,
    attempt_index: attemptIndex ?? undefined,
    stream: detailRaw.stream === true,
    reasoning_effort: typeof detailRaw.reasoning_effort === 'string'
      ? detailRaw.reasoning_effort
      : typeof detailRaw.reasoningEffort === 'string'
        ? detailRaw.reasoningEffort
        : undefined,
    service_tier: typeof detailRaw.service_tier === 'string'
      ? detailRaw.service_tier
      : typeof detailRaw.serviceTier === 'string'
        ? detailRaw.serviceTier
        : undefined,
    effective_service_tier: typeof detailRaw.effective_service_tier === 'string'
      ? detailRaw.effective_service_tier
      : typeof detailRaw.effectiveServiceTier === 'string'
        ? detailRaw.effectiveServiceTier
        : undefined,
    speed: typeof detailRaw.speed === 'string'
      ? detailRaw.speed
      : typeof detailRaw.requestSpeed === 'string'
        ? detailRaw.requestSpeed
        : undefined,
    effective_speed: typeof detailRaw.effective_speed === 'string'
      ? detailRaw.effective_speed
      : typeof detailRaw.effectiveSpeed === 'string'
        ? detailRaw.effectiveSpeed
        : undefined,
    estimated_cost: estimatedCost ?? undefined,
    price_rule_id: priceRuleID ?? undefined,
    cost_breakdown: normalizeUsageCostBreakdown(detailRaw.cost_breakdown ?? detailRaw.costBreakdown),
    accounting_version: accountingVersion ?? undefined,
    accounting_quality: typeof detailRaw.accounting_quality === 'string'
      ? detailRaw.accounting_quality
      : typeof detailRaw.accountingQuality === 'string'
        ? detailRaw.accountingQuality
        : undefined,
    token_breakdown: normalizeUsageTokenBreakdown(
      detailRaw.token_breakdown ?? detailRaw.tokenBreakdown
    ),
    tokens: readTokens(detailRaw),
    failed: detailRaw.failed === true,
    __modelName: modelName,
    __timestampMs: Number.isNaN(timestampMs) ? 0 : timestampMs,
  };
};

export function collectUsageDetailsWithEndpoint(usageData: unknown): UsageDetailWithEndpoint[] {
  const cacheKey = isRecord(usageData) ? (usageData as object) : null;
  if (cacheKey) {
    const cached = usageDetailsWithEndpointCache.get(cacheKey);
    if (cached) return cached;
  }

  const apis = getApisRecord(usageData);
  if (!apis) return [];

  const details: UsageDetailWithEndpoint[] = [];
  const sourceCache = new Map<string, string>();
  let previousTimestampMs = Number.POSITIVE_INFINITY;
  let isDescending = true;

  Object.entries(apis).forEach(([endpoint, apiEntry]) => {
    if (!isRecord(apiEntry)) return;
    const models = isRecord(apiEntry.models) ? apiEntry.models : null;
    if (!models) return;

    const endpointMatch = endpoint.match(USAGE_ENDPOINT_METHOD_REGEX);
    const endpointMethod = endpointMatch?.[1]?.toUpperCase();
    const endpointPath = endpointMatch?.[2];

    Object.entries(models).forEach(([modelName, modelEntry]) => {
      if (!isRecord(modelEntry)) return;
      const modelDetails = Array.isArray(modelEntry.details) ? modelEntry.details : [];

      modelDetails.forEach((detailRaw) => {
        const detail = buildUsageDetail(detailRaw, modelName, sourceCache);
        if (!detail) return;
        const timestampMs = detail.__timestampMs ?? 0;
        if (timestampMs > previousTimestampMs) {
          isDescending = false;
        }
        previousTimestampMs = timestampMs;
        details.push({
          ...detail,
          __endpoint: endpoint,
          __endpointMethod: endpointMethod,
          __endpointPath: endpointPath,
          __timestampMs: timestampMs,
        });
      });
    });
  });

  if (!isDescending) {
    details.sort((left, right) => right.__timestampMs - left.__timestampMs);
  }
  if (cacheKey) usageDetailsWithEndpointCache.set(cacheKey, details);
  return details;
}

export function extractTotalTokens(detail: unknown): number {
  const record = isRecord(detail) ? detail : null;
  const tokens = record && isRecord(record.tokens) ? record.tokens : {};
  const explicitTotal = toFiniteNumber(tokens.total_tokens);
  if (explicitTotal > 0) return explicitTotal;

  const inputTokens = toFiniteNumber(tokens.input_tokens);
  const outputTokens = toFiniteNumber(tokens.output_tokens);
  const reasoningTokens = toFiniteNumber(tokens.reasoning_tokens);
  const cacheReadTokens = toFiniteNumber(tokens.cache_read_tokens);
  const cacheCreationTokens = toFiniteNumber(tokens.cache_creation_tokens);
  const cachedTokens = Math.max(
    toFiniteNumber(tokens.cached_tokens),
    toFiniteNumber(tokens.cache_tokens) || cacheReadTokens + cacheCreationTokens
  );

  return inputTokens + outputTokens + reasoningTokens + cachedTokens;
}

export function calculateCost(
	 detail: Pick<UsageDetail, 'tokens' | '__modelName' | 'estimated_cost'>,
	 modelPrices: Record<string, ModelPrice>
): number {
	 const backendCost = Number(detail.estimated_cost);
	 if (Number.isFinite(backendCost) && backendCost >= 0) return backendCost;
  const modelName = detail.__modelName || '';
  const price = modelPrices[modelName];
  if (!price) return 0;

  const inputTokens = Math.max(toFiniteNumber(detail.tokens.input_tokens), 0);
  const completionTokens = Math.max(toFiniteNumber(detail.tokens.output_tokens), 0);
  const cachedTokens = Math.max(
    Math.max(toFiniteNumber(detail.tokens.cached_tokens), 0),
    Math.max(toFiniteNumber(detail.tokens.cache_tokens), 0)
  );
  const promptTokens = Math.max(inputTokens - cachedTokens, 0);
  const promptCost = (promptTokens / TOKENS_PER_PRICE_UNIT) * (Number(price.prompt) || 0);
  const cachedCost = (cachedTokens / TOKENS_PER_PRICE_UNIT) * (Number(price.cache) || 0);
  const completionCost =
    (completionTokens / TOKENS_PER_PRICE_UNIT) * (Number(price.completion) || 0);
  const total = promptCost + cachedCost + completionCost;
  return Number.isFinite(total) && total > 0 ? total : 0;
}

const normalizeModelPrices = (value: unknown): Record<string, ModelPrice> => {
  if (!isRecord(value)) return {};

  const normalized: Record<string, ModelPrice> = {};
  Object.entries(value).forEach(([model, price]) => {
    if (!model || !isRecord(price)) return;

    const prompt = toFiniteNumber(price.prompt);
    const completion = toFiniteNumber(price.completion);
    const cacheRaw = Number(price.cache);
    const cache = Number.isFinite(cacheRaw) && cacheRaw >= 0 ? cacheRaw : prompt;

    if (prompt < 0 || completion < 0 || cache < 0) return;
    normalized[model] = { prompt, completion, cache };
  });

  return normalized;
};

export function loadLegacyModelPrices(): Record<string, ModelPrice> {
  try {
    if (typeof localStorage === 'undefined') return {};
    const raw = localStorage.getItem(MODEL_PRICE_STORAGE_KEY);
    if (!raw) return {};

    return normalizeModelPrices(JSON.parse(raw));
  } catch {
    return {};
  }
}

export async function loadModelPricesFromSqlite(): Promise<Record<string, ModelPrice>> {
  const payload = await apiClient.get<{ prices?: unknown }>(MODEL_PRICE_API_PATH);
  return normalizeModelPrices(payload?.prices);
}

export async function saveModelPricesToSqlite(prices: Record<string, ModelPrice>): Promise<void> {
  await apiClient.put(MODEL_PRICE_API_PATH, { prices: normalizeModelPrices(prices) });
}

export async function loadModelPriceRules(): Promise<{
  rules: ModelPriceRule[];
  observedModels: ObservedModelPriceTarget[];
}> {
  const payload = await apiClient.get<{ rules?: ModelPriceRule[]; observedModels?: ObservedModelPriceTarget[] }>('/usage/model-price-rules');
  return {
    rules: Array.isArray(payload?.rules) ? payload.rules : [],
    observedModels: Array.isArray(payload?.observedModels) ? payload.observedModels : [],
  };
}

export async function saveModelPriceRule(rule: ModelPriceRule): Promise<ModelPriceRule> {
  const payload = await apiClient.put<{ rule: ModelPriceRule }>('/usage/model-price-rules', { rule });
  return payload.rule;
}

export async function deleteModelPriceRule(model: string): Promise<void> {
  await apiClient.delete('/usage/model-price-rules', { params: { model } });
}

export async function syncModelPricesFromModelsDev(dryRun = false, overrideLockedModels: string[] = []): Promise<ModelPriceSyncResult> {
  const payload = await apiClient.post<ModelPriceSyncResult>('/usage/model-prices/sync', {
    dryRun,
    recalculateUnpriced: !dryRun,
    overrideLockedModels,
  });
  return {
    ...payload,
    overridden: Number(payload?.overridden) || 0,
    unmatched: Array.isArray(payload?.unmatched) ? payload.unmatched : [],
    changes: Array.isArray(payload?.changes) ? payload.changes : [],
  };
}

export async function loadModelPriceSyncState(): Promise<ModelPriceSyncState> {
  const payload = await apiClient.get<{ state?: ModelPriceSyncState }>('/usage/model-prices/sync-status');
  const state = payload?.state ?? { status: 'idle' };
  return {
    ...state,
    unmatchedModels: Array.isArray(state.unmatchedModels) ? state.unmatchedModels : [],
  };
}

export async function searchModelsDevPrices(query: string, limit = 20): Promise<ModelsDevPriceSearchItem[]> {
  const payload = await apiClient.get<{ items?: ModelsDevPriceSearchItem[] }>('/usage/model-prices/models-dev/search', {
    params: { q: query.trim(), limit },
  });
  return Array.isArray(payload?.items) ? payload.items : [];
}

export async function recalculateModelPriceHistory(all = false): Promise<number> {
  const payload = await apiClient.post<{ updated?: number }>('/usage/model-prices/recalculate', { all });
  return Number(payload?.updated) || 0;
}

export function formatCompactNumber(value: number): string {
  const num = Number(value);
  if (!Number.isFinite(num)) return '0';

  const abs = Math.abs(num);
  if (abs === 0) return '0';
  if (abs >= 1_000_000) return `${(num / 1_000_000).toFixed(1)}M`;
  if (abs >= 1_000) return `${(num / 1_000).toFixed(1)}K`;
  return abs >= 1 ? num.toFixed(0) : num.toFixed(2);
}

export function formatUsd(value: number): string {
  const num = Number(value);
  if (!Number.isFinite(num)) return '$0.00';

  const fixed = num.toFixed(2);
  const parts = Number(fixed).toLocaleString(undefined, {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  });
  return `$${parts}`;
}

export function formatUsdPrecise(value: number): string {
  const num = Number(value);
  if (!Number.isFinite(num)) return '$0.000000';

  const decimals = num !== 0 && Math.abs(num) < 0.000001 ? 8 : 6;
  return `$${num.toLocaleString(undefined, {
    minimumFractionDigits: decimals,
    maximumFractionDigits: decimals,
  })}`;
}

const resolveDurationLocale = (locale?: string): string | undefined =>
  locale?.trim() || i18n.resolvedLanguage || i18n.language || undefined;

const formatDurationNumber = (
  value: number,
  locale: string | undefined,
  options: Intl.NumberFormatOptions = {}
): string => {
  try {
    return new Intl.NumberFormat(locale, {
      useGrouping: false,
      ...options,
    }).format(value);
  } catch {
    return String(value);
  }
};

const getDurationUnitLabel = (unit: 'd' | 'h' | 'm' | 's' | 'ms'): string =>
  i18n.t(`usage_stats.duration_unit_${unit}`, { defaultValue: unit });

const formatDurationPart = (
  value: number,
  unit: 'd' | 'h' | 'm' | 's' | 'ms',
  locale: string | undefined,
  options: Intl.NumberFormatOptions = {}
): string => `${formatDurationNumber(value, locale, options)}${getDurationUnitLabel(unit)}`;

const normalizeDurationMaxUnits = (value: number | undefined): number => {
  const parsed = Number(value);
  if (!Number.isFinite(parsed) || parsed <= 0) return 2;
  return Math.min(Math.floor(parsed), 4);
};

const resolveSecondDecimalPlaces = (
  seconds: number,
  secondDecimals: number | 'auto' | undefined
): number => {
  if (secondDecimals === 'auto' || secondDecimals === undefined) return seconds < 10 ? 2 : 1;

  const parsed = Math.floor(Number(secondDecimals));
  if (!Number.isFinite(parsed) || parsed < 0) return seconds < 10 ? 2 : 1;
  return Math.min(parsed, 3);
};

export function formatDurationMs(
  value: number | null | undefined,
  options: DurationFormatOptions = {}
): string {
  const invalidText = options.invalidText ?? '--';
  if (value === null || value === undefined) return invalidText;

  const parsed = Number(value);
  if (!Number.isFinite(parsed) || parsed < 0) return invalidText;

  const locale = resolveDurationLocale(options.locale);
  if (parsed < 1000) return formatDurationPart(Math.round(parsed), 'ms', locale);

  const seconds = parsed / 1000;
  if (seconds < 60) {
    const secondDecimalPlaces = resolveSecondDecimalPlaces(seconds, options.secondDecimals);
    return formatDurationPart(seconds, 's', locale, {
      minimumFractionDigits: 0,
      maximumFractionDigits: secondDecimalPlaces,
    });
  }

  const totalSeconds = Math.floor(seconds);
  let remainingSeconds = totalSeconds;
  const days = Math.floor(remainingSeconds / 86_400);
  remainingSeconds -= days * 86_400;
  const hours = Math.floor(remainingSeconds / 3_600);
  remainingSeconds -= hours * 3_600;
  const minutes = Math.floor(remainingSeconds / 60);
  remainingSeconds -= minutes * 60;

  const parts = [
    { unit: 'd' as const, value: days },
    { unit: 'h' as const, value: hours },
    { unit: 'm' as const, value: minutes },
    { unit: 's' as const, value: remainingSeconds },
  ].filter((part) => part.value > 0);

  if (!parts.length) return formatDurationPart(0, 's', locale);

  return parts
    .slice(0, normalizeDurationMaxUnits(options.maxUnits))
    .map((part, index) =>
      formatDurationPart(part.value, part.unit, locale, {
        minimumIntegerDigits: index > 0 && (part.unit === 'm' || part.unit === 's') ? 2 : 1,
        maximumFractionDigits: 0,
      })
    )
    .join(' ');
}
