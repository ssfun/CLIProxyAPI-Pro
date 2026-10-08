const PROVIDER_DISPLAY_LABELS: Record<string, string> = {
  antigravity: 'Antigravity',
  claude: 'Claude',
  codex: 'Codex',
  devin: 'Devin',
  gemini: 'Gemini',
  aistudio: 'AI Studio',
  kimi: 'Kimi',
  'kimi-ai': 'Kimi',
  'kimi.ai': 'Kimi',
  meta: 'Meta',
  vertex: 'Vertex',
  xai: 'xAI',
  iflow: 'iFlow',
  'openai-compatibility': 'OpenAI Compat',
};

export const resolveProviderDisplayLabel = (provider: string) => {
  const key = provider.trim().toLowerCase();
  return PROVIDER_DISPLAY_LABELS[key] ?? (key ? key.charAt(0).toUpperCase() + key.slice(1) : provider);
};
