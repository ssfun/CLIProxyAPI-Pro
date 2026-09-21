import importlib.util
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
MODULE_PATH = ROOT / 'apply_customizations.py'
SPEC = importlib.util.spec_from_file_location('apply_customizations', MODULE_PATH)
assert SPEC and SPEC.loader
CUSTOMIZATIONS = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CUSTOMIZATIONS)


API_CALL_SOURCE = """export interface ApiCallRequest {
  authIndex?: string;
  method: string;
  url: string;
  header?: Record<string, string>;
  data?: string;
}

export interface ApiCallResult<T = unknown> {
  statusCode: number;
  header: Record<string, string[]>;
  bodyText: string;
  body: T | null;
}

export const apiCallApi = {
  request: async (payload: ApiCallRequest): Promise<ApiCallResult> => {
    const response = await apiClient.post<Record<string, unknown>>('/api-call', payload);
    const statusCode = Number(response?.status_code ?? 0);
    const header = (response?.header ?? {}) as Record<string, string[]>;
    const { bodyText, body } = normalizeBody(response?.body);

    return {
      statusCode,
      header,
      bodyText,
      body,
    };
  },
};
"""


CODEX_RESOLVER_SOURCE = """export function extractCodexChatgptAccountId(value: unknown): string | null {
  const payload = parseIdTokenPayload(value);
  if (!payload) return null;
  return normalizeStringValue(payload.chatgpt_account_id ?? payload.chatgptAccountId);
}

export function resolveCodexChatgptAccountId(file: AuthFileItem): string | null {
  const metadata = file.metadata as Record<string, unknown> | null;
  const attributes = file.attributes as Record<string, unknown> | null;

  const candidates = [file.id_token, metadata?.id_token, attributes?.id_token];

  for (const candidate of candidates) {
    const id = extractCodexChatgptAccountId(candidate);
    if (id) return id;
  }

  return null;
}
"""


class NarrowUpstreamCustomizationsTest(unittest.TestCase):
    def setUp(self) -> None:
        CUSTOMIZATIONS._writes.clear()

    def tearDown(self) -> None:
        CUSTOMIZATIONS._writes.clear()

    def test_api_call_contract_is_narrow_and_idempotent(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            target = Path(temp_dir)
            path = target / 'src/services/api/apiCall.ts'
            path.parent.mkdir(parents=True)
            path.write_text(API_CALL_SOURCE)

            CUSTOMIZATIONS.patch_api_call_executor_contract(target)
            CUSTOMIZATIONS.flush_writes()
            first = path.read_text()

            self.assertIn('useExecutor?: boolean;', first)
            self.assertIn('use_executor?: boolean;', first)
            self.assertIn('hasStatusCode: boolean;', first)
            self.assertIn('response?.status_code ?? response?.statusCode', first)
            self.assertIn('response?.header ?? response?.headers ?? {}', first)

            CUSTOMIZATIONS.patch_api_call_executor_contract(target)
            CUSTOMIZATIONS.flush_writes()
            self.assertEqual(first, path.read_text())

    def test_codex_account_id_patch_preserves_upstream_direct_candidates(self) -> None:
        direct = "  const directCandidates = [file.chatgpt_account_id];\n  for (const candidate of directCandidates) {\n    const id = normalizeStringValue(candidate);\n    if (id) return id;\n  }\n"
        source = CODEX_RESOLVER_SOURCE.replace('const candidates = [file.id_token', direct + '  const tokenCandidates = [file.id_token').replace('of candidates)', 'of tokenCandidates)')
        with tempfile.TemporaryDirectory() as temp_dir:
            target = Path(temp_dir)
            path = target / 'src/utils/quota/resolvers.ts'
            path.parent.mkdir(parents=True)
            path.write_text(source)
            CUSTOMIZATIONS.patch_codex_account_id_resolver(target)
            CUSTOMIZATIONS.flush_writes()
            first = path.read_text()
            self.assertIn(direct, first)
            self.assertIn('const tokenCandidates = [\n    file,\n    metadata,\n    attributes,', first)
            self.assertIn('for (const candidate of tokenCandidates)', first)
            CUSTOMIZATIONS.patch_codex_account_id_resolver(target)
            CUSTOMIZATIONS.flush_writes()
            self.assertEqual(first, path.read_text())

    def test_codex_account_id_patch_extends_only_account_id_resolution(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            target = Path(temp_dir)
            path = target / 'src/utils/quota/resolvers.ts'
            path.parent.mkdir(parents=True)
            path.write_text(CODEX_RESOLVER_SOURCE)

            CUSTOMIZATIONS.patch_codex_account_id_resolver(target)
            CUSTOMIZATIONS.flush_writes()
            first = path.read_text()

            self.assertIn('const resolveAccountIdCandidate', first)
            self.assertIn('record.account_id', first)
            self.assertIn('record.accountId', first)
            self.assertIn('for (const candidate of candidates)', first)
            self.assertIn('if (accountId) return accountId;', first)
            self.assertIn('    file,\n    metadata,\n    attributes,', first)
            self.assertEqual(1, first.count('export function resolveCodexChatgptAccountId'))

            CUSTOMIZATIONS.patch_codex_account_id_resolver(target)
            CUSTOMIZATIONS.flush_writes()
            self.assertEqual(first, path.read_text())

    def test_generated_locales_are_registered_without_replacing_upstream_locales(self) -> None:
        bootstrap = (ROOT / 'overlay/src/pro/ProBootstrap.tsx').read_text()
        registration = (ROOT / 'overlay/src/pro/registerLocales.ts').read_text()
        manifest = (ROOT / 'overlay-replacements.json').read_text()

        self.assertIn("import '@/pro/registerLocales';", bootstrap)
        self.assertIn("i18n.addResourceBundle(language, 'translation', resources, true, true)", registration)
        self.assertIn('"replacements": []', manifest)


if __name__ == '__main__':
    unittest.main()
