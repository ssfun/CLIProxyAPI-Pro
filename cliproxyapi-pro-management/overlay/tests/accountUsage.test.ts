import { describe, expect, test } from 'bun:test';
import {
  buildAccountUsageLogPath,
  ratio,
  resolveAccountUsageLabel,
} from '../src/features/monitoring/accountUsage';
import {
  buildConfiguredApiKeyMap,
  resolveConfiguredApiKeyLabel,
} from '../src/features/monitoring/apiKeyIdentity';

describe('account usage helpers', () => {
  test('uses the full auth-file name when no account email is available', () => {
    expect(resolveAccountUsageLabel({
      name: 'xai-workspace_oauth_creds.json',
      account: 'workspace-user',
      label: 'xAI workspace',
    }, 'xai:opaque-account')).toBe('xai-workspace_oauth_creds.json');
  });

  test('prefers an available account email over the auth-file name', () => {
    expect(resolveAccountUsageLabel({
      name: 'codex-account.json',
      id_token: { email: 'owner@example.com' },
    }, 'codex:opaque-account')).toBe('owner@example.com');
  });

  test('builds an exact, encoded request-log scope', () => {
    expect(buildAccountUsageLogPath('codex:user+one@example.com', 100.4, 200.6)).toBe(
      '/monitoring?auth_index=codex%3Auser%2Bone%40example.com&from_ms=100&to_ms=201#request-events'
    );
  });

  test('resolves API key hashes through the configured keys used by request monitoring', () => {
    const configured = buildConfiguredApiKeyMap(['sk-live-1234567890']);
    const identity = configured.keys[0];

    expect(resolveConfiguredApiKeyLabel(identity.hash, configured, 'Unattributed', 'Unknown')).toBe(identity.masked);
    expect(identity.masked).toBe('sk******90');
    expect(identity.masked).not.toContain(identity.hash);
  });

  test('labels unattributed and unknown API key hashes without displaying hashes', () => {
    const configured = buildConfiguredApiKeyMap(['sk-live-1234567890']);

    expect(resolveConfiguredApiKeyLabel('', configured, 'Unattributed', 'Unknown')).toBe('Unattributed');
    expect(resolveConfiguredApiKeyLabel('deleted-key-hash', configured, 'Unattributed', 'Unknown')).toBe('Unknown');
  });

  test('clamps ratios and handles empty denominators', () => {
    expect(ratio(1, 4)).toBe(0.25);
    expect(ratio(3, 2)).toBe(1);
    expect(ratio(1, 0)).toBe(0);
  });
});
