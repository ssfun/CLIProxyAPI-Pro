import { expect, test } from 'bun:test';

// Isolate React/module mocks and the fake clock from the rest of the test suite.
for (const scenario of [
  'idle',
  'coalesce',
  'manual',
  'disconnect',
  'generation',
  'unmount',
  'unmount-pending',
  'failure',
]) {
  test(`usage aggregates trailing summary refresh: ${scenario}`, () => {
    const result = Bun.spawnSync(
      [
        process.execPath,
        new URL('./fixtures/usageAggregatesLifecycle.ts', import.meta.url).pathname,
        scenario,
      ],
      { cwd: import.meta.dir }
    );
    expect(result.stderr.toString()).toBe('');
    expect(result.exitCode).toBe(0);
  });
}
