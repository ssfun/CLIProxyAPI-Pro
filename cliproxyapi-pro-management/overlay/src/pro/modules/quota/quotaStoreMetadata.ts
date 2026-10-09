import { QUOTA_TAB_ORDER } from '@/features/quota/constants';
import type { QuotaProviderType, QuotaStore } from '@/features/quota/providers/types';

export type ProQuotaProviderType = QuotaProviderType;

type QuotaMapName = Extract<keyof QuotaStore, `${string}Quota`>;
type QuotaSetterName = Extract<keyof QuotaStore, `set${string}Quota`>;

type QuotaProviderMetadata = {
  quotaMapName: QuotaMapName;
  setterName: QuotaSetterName;
};

const QUOTA_PROVIDER_METADATA = {
  antigravity: { quotaMapName: 'antigravityQuota', setterName: 'setAntigravityQuota' },
  claude: { quotaMapName: 'claudeQuota', setterName: 'setClaudeQuota' },
  codex: { quotaMapName: 'codexQuota', setterName: 'setCodexQuota' },
  devin: { quotaMapName: 'devinQuota', setterName: 'setDevinQuota' },
  'gemini-cli': { quotaMapName: 'geminiCliQuota', setterName: 'setGeminiCliQuota' },
  kimi: { quotaMapName: 'kimiQuota', setterName: 'setKimiQuota' },
  meta: { quotaMapName: 'metaQuota', setterName: 'setMetaQuota' },
  plugin: { quotaMapName: 'pluginQuota', setterName: 'setPluginQuota' },
  xai: { quotaMapName: 'xaiQuota', setterName: 'setXaiQuota' },
} satisfies Record<QuotaProviderType, QuotaProviderMetadata>;

export const PRO_QUOTA_PROVIDER_TYPES: readonly ProQuotaProviderType[] = QUOTA_TAB_ORDER;

export const isProQuotaProviderType = (provider: string): provider is ProQuotaProviderType =>
  Object.prototype.hasOwnProperty.call(QUOTA_PROVIDER_METADATA, provider);

export const getQuotaProviderMapName = (provider: ProQuotaProviderType) =>
  QUOTA_PROVIDER_METADATA[provider].quotaMapName;

export const getQuotaProviderSetterName = (provider: ProQuotaProviderType) =>
  QUOTA_PROVIDER_METADATA[provider].setterName;
