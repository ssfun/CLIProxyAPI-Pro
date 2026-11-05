// Exercise the signal-composition fallback used by older browsers.
Object.defineProperty(AbortSignal, 'any', { configurable: true, value: undefined });
