import { describe, expect, test } from 'bun:test';
import {
  isProxyPoolListenerUrl,
  normalizeProxyPoolConfig,
  parseProxyPoolImport,
  serializeProxyPoolConfig,
} from '../src/services/api/proxyPool';
import {
  formatProxyPoolSuccessRate,
  proxyPoolDurationValue,
  serializeProxyPoolDuration,
} from '../src/features/proxyPool/proxyPoolUi';

describe('proxy pool service model', () => {
  test('recognizes credential routes that already point at the pool listener', () => {
    expect(isProxyPoolListenerUrl('socks5://127.0.0.1:8318', '127.0.0.1:8318')).toBe(true);
    expect(isProxyPoolListenerUrl('SOCKS5H://127.0.0.1:8318', '127.0.0.1:8318')).toBe(true);
    expect(isProxyPoolListenerUrl('socks5://127.0.0.1:1080', '127.0.0.1:8318')).toBe(false);
    expect(isProxyPoolListenerUrl('http://127.0.0.1:8318', '127.0.0.1:8318')).toBe(false);
  });

  test('round-trips health timeout, test URL, weight, and node order', () => {
    const normalized = normalizeProxyPoolConfig({
      listen: '127.0.0.1:8318',
      'health-check': { timeout: '11s', 'test-url': 'https://example.com/ip' },
      nodes: [{ id: 'primary', url: 'socks5://127.0.0.1:1080', weight: 4, order: 30 }],
    });
    const serialized = serializeProxyPoolConfig(normalized);

    expect(serialized['health-check']).toMatchObject({
      timeout: '11s',
      'test-url': 'https://example.com/ip',
    });
    expect(serialized.nodes).toEqual([
      {
        id: 'primary',
        label: '',
        url: 'socks5://127.0.0.1:1080',
        enabled: true,
        weight: 4,
        order: 30,
      },
    ]);
  });

  test('parses batch import formats and skips existing or repeated URLs', () => {
    const result = parseProxyPoolImport(
      [
        '# comment',
        'primary | socks5://user:pass@proxy.example:1080 | 3',
        'http://127.0.0.1:8080',
        'duplicate | http://127.0.0.1:8080',
        'broken line',
      ].join('\n'),
      [
        {
          id: 'proxy-primary',
          label: '',
          url: 'http://existing.example:80',
          enabled: true,
          weight: 1,
          order: 20,
        },
      ]
    );

    expect(result.nodes).toEqual([
      {
        id: 'proxy-primary-2',
        label: 'primary',
        url: 'socks5://user:pass@proxy.example:1080',
        enabled: true,
        weight: 3,
        order: 30,
      },
      {
        id: 'proxy-127-0-0-1',
        label: '',
        url: 'http://127.0.0.1:8080',
        enabled: true,
        weight: 1,
        order: 40,
      },
    ]);
    expect(result.duplicateCount).toBe(1);
    expect(result.errors).toEqual([{ line: 5, message: 'missing supported proxy URL' }]);
  });

  test('clamps inconsistent runtime success counters at 100 percent', () => {
    expect(formatProxyPoolSuccessRate(10, 9)).toBe('100%');
    expect(formatProxyPoolSuccessRate(8, 10)).toBe('80%');
    expect(formatProxyPoolSuccessRate(0, 0)).toBe('-');
  });

  test('converts configured durations to fixed UI units', () => {
    expect(proxyPoolDurationValue('1m30s', 's')).toBe(90);
    expect(proxyPoolDurationValue('90s', 'm')).toBe(1.5);
    expect(proxyPoolDurationValue('invalid', 's')).toBeNull();
    expect(serializeProxyPoolDuration(1.5, 'm')).toBe('1.5m');
  });
});
