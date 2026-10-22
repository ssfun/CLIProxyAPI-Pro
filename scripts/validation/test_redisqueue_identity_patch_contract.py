"""Redis queue replay contracts authored before the v8.0.24 repair."""
import ast
import unittest
from pathlib import Path

PATCHER = Path(__file__).resolve().parents[2] / 'cliproxyapi-pro-core/patches/apply_upstream_patches.py'
CASES = [
    ('\t\tAPIKey:               apiKey,\n',
     '\t\tIsNativeKey:          isNativeKey,\n\t\tAccessProvider:       accessProvider,\n',
     '\t\tRequestID:            requestID,\n', 'APIKeyPolicyID:       apiKeyPolicyID'),
    ('\tAPIKey               string                   `json:"api_key"`\n',
     '\tIsNativeKey          bool                     `json:"is_native_key"`\n\tAccessProvider       string                   `json:"access_provider,omitempty"`\n',
     '\tRequestID            string                   `json:"request_id"`\n', '`json:"api_key_policy_id,omitempty"`'),
]


class RedisqueueIdentityPatchContract(unittest.TestCase):
    def test_payload_and_type_preserve_upstream_identity(self):
        tree = ast.parse(PATCHER.read_text())
        helpers = [n for n in tree.body if isinstance(n, ast.FunctionDef)
                   and n.name in ('replace_once', 'replace_all_exact')]
        for prefix, identity, suffix, marker in CASES:
            with self.subTest(marker=marker):
                ops = [n for n in tree.body if isinstance(n, ast.Expr)
                       and isinstance(n.value, ast.Call) and len(n.value.args) >= 3
                       and isinstance(n.value.args[0], ast.Name) and n.value.args[0].id == 'redisqueue_plugin'
                       and isinstance(n.value.args[1], ast.Constant) and n.value.args[1].value.startswith(prefix)]
                self.assertEqual(len(ops), 1)
                source = prefix + identity + suffix
                state = [source]
                writes = []
                def write(path, text):
                    state[0] = text
                    writes.append(text)
                env = {'Path': Path, 'redisqueue_plugin': Path('plugin.go'),
                       'read': lambda _: state[0], 'write': write}
                exec(compile(ast.Module(body=helpers, type_ignores=[]), str(PATCHER), 'exec'), env)
                operation = compile(ast.Module(body=ops, type_ignores=[]), str(PATCHER), 'exec')
                exec(operation, env)
                patched = state[0]
                self.assertEqual(patched.count(identity), 1)
                for field in ('APIKeyPolicyID', 'ProfileID', 'ProfileNameSnapshot', 'PolicyMode', 'RequestedModel', 'EffectiveModel'):
                    self.assertEqual(patched.count(field), 1)
                self.assertEqual(patched.count(marker), 1)
                exec(operation, env)
                self.assertEqual(state[0], patched)
                self.assertEqual(len(writes), 1)
                for malformed in ('', source + source, patched + patched, source + patched, prefix + suffix):
                    state[0] = malformed
                    writes.clear()
                    with self.assertRaises(SystemExit):
                        exec(operation, env)
                    self.assertEqual(state[0], malformed)
                    self.assertEqual(writes, [])


if __name__ == '__main__':
    unittest.main()
