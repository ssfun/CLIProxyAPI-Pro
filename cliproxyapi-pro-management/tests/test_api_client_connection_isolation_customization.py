import importlib.util
import tempfile
import unittest
from pathlib import Path


MODULE_PATH = Path(__file__).resolve().parents[1] / 'apply_customizations.py'
SPEC = importlib.util.spec_from_file_location('apply_customizations', MODULE_PATH)
MODULE = importlib.util.module_from_spec(SPEC)
assert SPEC and SPEC.loader
SPEC.loader.exec_module(MODULE)


class ApiClientConnectionIsolationCustomizationTests(unittest.TestCase):
    def setUp(self):
        MODULE._writes.clear()

    def tearDown(self):
        MODULE._writes.clear()

    def apply_patch(self, root: Path, upstream_client: str) -> tuple[str, str]:
        client = root / 'src/services/api/client.ts'
        client.parent.mkdir(parents=True)
        if getattr(self, 'english_comments', False):
            upstream_client = upstream_client.replace('设置请求/响应拦截器', 'Set request/response interceptors').replace('设置 baseURL', 'Set baseURL')
        client.write_text(upstream_client)
        auth_store = root / 'src/stores/useAuthStore.ts'
        auth_store.parent.mkdir(parents=True)
        auth_store.write_text("""logout: () => {
        restoreSessionPromise = null;
        apiClient.setConfig({ apiBase: '', managementKey: '' });
        useQuotaStore.getState().clearQuotaCache();
        set({
          isAuthenticated: false,
        });
      },
""")

        MODULE.patch_api_client_connection_isolation(root)
        MODULE.flush_writes()
        first_client = client.read_text()
        first_store = auth_store.read_text()

        MODULE.patch_api_client_connection_isolation(root)
        MODULE.flush_writes()
        self.assertEqual(client.read_text(), first_client)
        self.assertEqual(auth_store.read_text(), first_store)
        if getattr(self, 'english_comments', False):
            self.assertIn('Set request/response interceptors', first_client)
            self.assertIn('// Set baseURL', first_client)
        return first_client, first_store

    def test_patches_latest_generation_guard_and_preserves_native_logout_reset(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            upstream_client = """import axios, { AxiosRequestConfig } from 'axios';
class ApiClient {
  private apiBase: string = '';
  private managementKey: string = '';
  setConfig(config: ApiClientConfig): void {
    const apiBase = computeApiUrl(config.apiBase);
    if (apiBase !== this.apiBase || config.managementKey !== this.managementKey) {
      this.connectionRevision += 1;
    }
    this.apiBase = apiBase;
    this.managementKey = config.managementKey;

    if (config.timeout) {
      this.instance.defaults.timeout = config.timeout;
    }
  }
  /**
   * 设置请求/响应拦截器
   */
  private setupInterceptors(): void {
    this.instance.interceptors.request.use(
      (config) => {
        // 设置 baseURL
        config.baseURL = this.apiBase;
      },
      (error) => Promise.reject(this.handleError(error))
    );
    this.instance.interceptors.response.use(
      (response) => {
        const headers = response.headers as Record<string, string | undefined>;
        return response;
      },
      (error) => Promise.reject(this.handleError(error))
    );
  }
}
"""
            first_client, first_store = self.apply_patch(root, upstream_client)
            self.assertIn('private connectionGeneration: number = 0;', first_client)
            self.assertIn('private connectionAbortController = new AbortController();', first_client)
            self.assertIn('const apiBase = computeApiUrl(config.apiBase);', first_client)
            self.assertIn('this.connectionRevision += 1;', first_client)
            self.assertIn('this.connectionAbortController.abort();', first_client)
            self.assertIn('config.signal = this.combineRequestSignal(config.signal);', first_client)
            self.assertIn('__connectionGeneration = this.connectionGeneration;', first_client)
            self.assertIn('this.isStaleConnection(response.config)', first_client)
            self.assertEqual(first_store.count("apiClient.setConfig({ apiBase: '', managementKey: '' });"), 1)

    def test_preserves_translated_interceptor_comments(self):
        self.english_comments = True
        self.test_patches_latest_generation_guard_and_preserves_native_logout_reset()

    def test_rejects_legacy_runtime_aware_connection_change(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            upstream_client = """import axios, { AxiosRequestConfig } from 'axios';
class ApiClient {
  private apiBase: string = '';
  private managementKey: string = '';
  private runtimeKind: ServerRuntimeKind = 'unknown';
  setConfig(config: ApiClientConfig): void {
    const nextApiBase = computeApiUrl(config.apiBase);
    const connectionChanged =
      this.apiBase !== nextApiBase || this.managementKey !== config.managementKey;
    this.apiBase = nextApiBase;
    this.managementKey = config.managementKey;
    if (connectionChanged) {
      this.runtimeKind = 'unknown';
    }

    if (config.timeout) {
      this.instance.defaults.timeout = config.timeout;
    }
  }
  /**
   * 设置请求/响应拦截器
   */
  private setupInterceptors(): void {
    this.instance.interceptors.request.use(
      (config) => {
        // 设置 baseURL
        config.baseURL = this.apiBase;
      },
      (error) => Promise.reject(this.handleError(error))
    );
    this.instance.interceptors.response.use(
      (response) => {
        const headers = response.headers as Record<string, string | undefined>;
        return response;
      },
      (error) => Promise.reject(this.handleError(error))
    );
  }
}
"""
            with self.assertRaisesRegex(RuntimeError, 'Expected latest connection-change shape'):
                self.apply_patch(root, upstream_client)


if __name__ == '__main__':
    unittest.main()
