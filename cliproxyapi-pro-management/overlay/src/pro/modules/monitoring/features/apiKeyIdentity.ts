import { sha256Hex } from './hash';

export type MonitoringApiKeyIdentity = {
  id: string;
  hash: string;
  masked: string;
  name?: string;
};

const maskConfiguredApiKey = (value: string): string => {
  const trimmed = value.trim();
  if (!trimmed || trimmed === '-') return '-';
  const visibleChars = trimmed.length < 4 ? 1 : 2;
  return `${trimmed.slice(0, visibleChars)}${'*'.repeat(Math.max(10 - visibleChars * 2, 1))}${trimmed.slice(-visibleChars)}`;
};

export const buildConfiguredApiKeyMap = (apiKeys: readonly string[] | undefined) => {
  const keys = (apiKeys || [])
    .map((key) => key.trim())
    .filter(Boolean)
    .map((key, index): MonitoringApiKeyIdentity => {
      const hash = sha256Hex(key);
      return {
        id: `clientApiKey:${hash || index}`,
        hash,
        masked: maskConfiguredApiKey(key),
      };
    });

  return {
    keys,
    byHash: new Map(keys.map((key) => [key.hash, key])),
  };
};

export function resolveConfiguredApiKeyLabel(
  apiKeyHash: string,
  configuredApiKeys: ReturnType<typeof buildConfiguredApiKeyMap>,
  unattributedLabel: string,
  unknownLabel: string
): string {
  const normalizedHash = apiKeyHash.trim();
  if (!normalizedHash) return unattributedLabel;
  return configuredApiKeys.byHash.get(normalizedHash)?.masked ?? unknownLabel;
}

export const formatMonitoringApiKeyLabel = (key: Pick<MonitoringApiKeyIdentity, 'masked' | 'name'>): string => {
  const name = key.name?.trim();
  return name ? `${name} (${key.masked})` : key.masked;
};

export const buildMonitoringApiKeyNames = (
  items: ReadonlyArray<{ apiKeyHash: string; displayName: string }> = []
): ReadonlyMap<string, string> => new Map(
  items.filter((item) => item.apiKeyHash.trim() && item.displayName.trim())
    .map((item) => [item.apiKeyHash.trim(), item.displayName.trim()])
);
