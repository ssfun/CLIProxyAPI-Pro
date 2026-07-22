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
    def test_patches_generation_guard_and_logout_client_clear(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            client = root / 'src/services/api/client.ts'
            client.parent.mkdir(parents=True)
            upstream_client = """import axios, { AxiosRequestConfig } from 'axios';
class ApiClient {
  private runtimeKind: ServerRuntimeKind = 'unknown';
  setConfig(config: ApiClientConfig): void {
    const connectionChanged = true;
    if (connectionChanged) {
      this.runtimeKind = 'unknown';
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
            client.write_text(upstream_client)
            auth_store = root / 'src/stores/useAuthStore.ts'
            auth_store.parent.mkdir(parents=True)
            auth_store.write_text("""logout: () => {
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
            self.assertIn('private connectionGeneration: number = 0;', first_client)
            self.assertIn('private connectionAbortController = new AbortController();', first_client)
            self.assertIn('this.connectionAbortController.abort();', first_client)
            self.assertIn('config.signal = this.combineRequestSignal(config.signal);', first_client)
            self.assertIn('__connectionGeneration = this.connectionGeneration;', first_client)
            self.assertIn('this.isStaleConnection(response.config)', first_client)
            self.assertIn("apiClient.setConfig({ apiBase: '', managementKey: '' });", first_store)

            MODULE._writes.clear()
            MODULE.patch_api_client_connection_isolation(root)
            MODULE.flush_writes()
            self.assertEqual(client.read_text(), first_client)
            self.assertEqual(auth_store.read_text(), first_store)


if __name__ == '__main__':
    unittest.main()
