"""Generator replay contracts authored before the v8.0.24 repair."""
import ast
import unittest
from pathlib import Path

PATCHER = Path(__file__).resolve().parents[2] / 'cliproxyapi-pro-core/patches/apply_upstream_patches.py'
USAGE = '\n'.join('\t\t\t\t' + line for line in [
    'reqCtx := coreusage.WithAPIKey(c.Request.Context(), result.Principal)',
    'reqCtx = coreusage.WithAccessProvider(reqCtx, result.Provider)',
    'reqCtx = coreusage.WithIsNativeKey(reqCtx, coreusage.IsNativeAccessProvider(result.Provider))',
    'c.Request = c.Request.WithContext(reqCtx)',
]) + '\n'
UPSTREAM = '''\t\tif err == nil {
\t\t\tif result != nil {
\t\t\t\tc.Set("userApiKey", result.Principal)
\t\t\t\tc.Set("accessProvider", result.Provider)
\t\t\t\tif len(result.Metadata) > 0 {
\t\t\t\t\tc.Set("accessMetadata", result.Metadata)
\t\t\t\t}
''' + USAGE + '''\t\t\t}
\t\t\tmiddleware.CaptureAuthenticatedRequestBody(c)
\t\t\tif c.IsAborted() {
\t\t\t\treturn
\t\t\t}
\t\t\tc.Next()
\t\t\treturn
\t\t}
'''


class AuthContextPatchContract(unittest.TestCase):
    def setUp(self):
        tree = ast.parse(PATCHER.read_text())
        helpers = [n for n in tree.body if isinstance(n, ast.FunctionDef)
                   and n.name in ('replace_once', 'replace_all_exact')]
        operations = [n for n in tree.body if isinstance(n, ast.Expr)
                      and isinstance(n.value, ast.Call)
                      and n.value.args and isinstance(n.value.args[0], ast.Name)
                      and n.value.args[0].id == 'server_middleware_source'
                      and isinstance(n.value.args[1], ast.Constant)
                      and n.value.args[1].value.startswith('\t\tif err == nil {')
                      and any(isinstance(a, ast.Constant) and isinstance(a.value, str)
                              and 'requestCtx := apikeypolicy.WithIdentity' in a.value
                              for a in n.value.args)]
        self.assertEqual(len(operations), 1)
        self.operation = compile(ast.Module(body=operations, type_ignores=[]), str(PATCHER), 'exec')
        self.source = UPSTREAM
        self.writes = []
        self.env = {'Path': Path, 'server_middleware_source': Path('server_middleware.go'),
                    'read': lambda _: self.source, 'write': self.write}
        exec(compile(ast.Module(body=helpers, type_ignores=[]), str(PATCHER), 'exec'), self.env)

    def write(self, path, text):
        self.writes.append(text)
        self.source = text

    def replay(self):
        exec(self.operation, self.env)

    def test_context_composition_and_idempotence(self):
        self.replay()
        patched = self.source
        self.assertEqual(patched.count(USAGE), 1)
        identity = 'requestCtx := apikeypolicy.WithIdentity(c.Request.Context(), identity)'
        self.assertEqual(patched.count(identity), 1)
        self.assertLess(patched.index(USAGE), patched.index(identity))
        self.assertIn('requestCtx = apikeypolicy.WithDecision(requestCtx, decision)', patched)
        self.assertIn('result.Provider == sdkaccess.DefaultAccessProviderName && policy != nil', patched)
        self.assertEqual(patched.count('writeAPIKeyPolicyMiddlewareError(c, realtimeError,'), 2)
        self.assertEqual(patched.count('c.Next()'), 1)
        self.replay()
        self.assertEqual(self.source, patched)
        self.assertEqual(len(self.writes), 1)

    def test_invalid_anchor_counts_fail_without_writes(self):
        self.replay()
        patched = self.source
        for malformed in ('', UPSTREAM + UPSTREAM, patched + patched, UPSTREAM + patched,
                          UPSTREAM.replace(USAGE, '')):
            with self.subTest(source=malformed[:50]):
                self.source = malformed
                self.writes.clear()
                with self.assertRaises(SystemExit):
                    self.replay()
                self.assertEqual(self.source, malformed)
                self.assertEqual(self.writes, [])


if __name__ == '__main__':
    unittest.main()
