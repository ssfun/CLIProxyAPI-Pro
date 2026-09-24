import ast
import re
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
PATCHES = ROOT / 'cliproxyapi-pro-core/patches'
PRO = PATCHES / 'sources/internal/pro'
PRO_IMPORT_PATTERN = re.compile(
    r'github\.com/router-for-me/CLIProxyAPI/v\d+/internal/pro/([^/"\s]+)'
)


class CoreModuleBoundaryTests(unittest.TestCase):
    def test_go_formatter_batches_process_invocation(self):
        generator_path = PATCHES / 'apply_upstream_patches.py'
        generator_tree = ast.parse(generator_path.read_text(encoding='utf-8'))
        formatter = next(
            node
            for node in generator_tree.body
            if isinstance(node, ast.FunctionDef) and node.name == 'format_go_writes'
        )
        subprocess_runs = [
            node
            for node in ast.walk(formatter)
            if isinstance(node, ast.Call)
            and isinstance(node.func, ast.Attribute)
            and isinstance(node.func.value, ast.Name)
            and node.func.value.id == 'subprocess'
            and node.func.attr == 'run'
        ]

        self.assertEqual(1, len(subprocess_runs))
        run_call = subprocess_runs[0]
        loops = [
            node
            for node in ast.walk(formatter)
            if isinstance(node, (ast.For, ast.AsyncFor, ast.While))
        ]
        self.assertFalse(
            any(loop.lineno <= run_call.lineno <= loop.end_lineno for loop in loops),
            'gofmt must be invoked once outside per-file loops',
        )
        command = run_call.args[0]
        self.assertIsInstance(command, ast.List)
        self.assertTrue(
            any(
                isinstance(item, ast.Starred)
                and isinstance(item.value, ast.Name)
                and item.value.id == 'relative_paths'
                for item in command.elts
            ),
            'gofmt must receive all relative paths in one invocation',
        )

    def test_foundation_modules_remain_leaf_layers(self):
        allowed_dependencies = {
            'settings': set(),
            'state': set(),
            'storage': set(),
            'quota': set(),
            'routing': set(),
            'inspection': set(),
            'backup': {'state'},
        }
        violations = []
        for owner, allowed in allowed_dependencies.items():
            for path in sorted((PRO / owner).rglob('*.go')):
                if path.name.endswith('_test.go'):
                    continue
                source = path.read_text(encoding='utf-8')
                for dependency in PRO_IMPORT_PATTERN.findall(source):
                    if dependency != owner and dependency not in allowed:
                        violations.append(
                            f'{path.relative_to(ROOT)} imports internal/pro/{dependency}'
                        )
        self.assertEqual([], violations, '\n'.join(violations))

    def test_management_feature_files_use_explicit_host_adapters(self):
        inspection_files = tuple(PATCHES.glob('account_inspection_*.go'))
        production_files = {
            path.name for path in inspection_files
            if not path.name.endswith('_test.go') and path.name != 'account_inspection_host.go'
        }
        test_files = {
            path.name for path in inspection_files
            if path.name.endswith('_test.go')
        }
        expected_production_files = {
            'account_inspection_runtime.go',
            'account_inspection_http.go',
            'account_inspection_accounts.go',
            'account_inspection_transport.go',
            'account_inspection_quota.go',
            'account_inspection_batch.go',
            'account_inspection_history.go',
        }
        expected_test_files = {
            'account_inspection_runtime_test.go',
            'account_inspection_http_test.go',
            'account_inspection_accounts_test.go',
            'account_inspection_transport_test.go',
            'account_inspection_quota_test.go',
        }
        self.assertEqual(expected_production_files, production_files)
        self.assertEqual(expected_test_files, test_files)

        generator_path = PATCHES / 'apply_upstream_patches.py'
        generator_tree = ast.parse(generator_path.read_text(encoding='utf-8'))
        generated_source_files = None
        for node in generator_tree.body:
            if not isinstance(node, ast.Assign):
                continue
            if any(
                isinstance(target, ast.Name)
                and target.id == 'ACCOUNT_INSPECTION_SOURCE_FILES'
                for target in node.targets
            ):
                generated_source_files = ast.literal_eval(node.value)
                break
        self.assertIsNotNone(generated_source_files)
        self.assertEqual(
            expected_production_files | expected_test_files,
            set(generated_source_files),
        )
        self.assertEqual(len(generated_source_files), len(set(generated_source_files)))
        inspection = '\n'.join(
            path.read_text(encoding='utf-8')
            for path in inspection_files
            if not path.name.endswith('_test.go') and path.name != 'account_inspection_host.go'
        )
        inspection_runtime = (PATCHES / 'account_inspection_runtime.go').read_text(encoding='utf-8')
        inspection_http = (PATCHES / 'account_inspection_http.go').read_text(encoding='utf-8')
        inspection_transport = (PATCHES / 'account_inspection_transport.go').read_text(encoding='utf-8')
        routing = (PATCHES / 'routing_policy.go').read_text(encoding='utf-8')
        plugin_quota = (PATCHES / 'plugin_quota_management.go').read_text(encoding='utf-8')
        auth_adapter = (PATCHES / 'pro_auth_mutation.go').read_text(encoding='utf-8')
        runtime = (PATCHES / 'pro_management_runtime.go').read_text(encoding='utf-8')

        self.assertNotIn('startRoutingPolicyController(', inspection)
        self.assertNotIn('stopRoutingPolicyController(', inspection)
        self.assertNotIn('accountInspectionQuotaAdapter', inspection)
        self.assertNotIn('internal/pro/inspection', plugin_quota)
        self.assertNotIn('accountInspectionQuotaAdapter', plugin_quota)

        for declaration in (
            'func setProAuthDisabledState(',
            'func (h *Handler) updateProAuth(',
            'func stringFromAny(',
            'func clearRoutingProtectionOwnership(',
        ):
            self.assertIn(declaration, auth_adapter)
            self.assertNotIn(declaration, inspection)
            self.assertNotIn(declaration, routing)

        self.assertIn('h.startAccountInspectionScheduler(', runtime)
        self.assertIn('startRoutingPolicyController(h)', runtime)
        self.assertIn('stopRoutingPolicyController(h)', runtime)

        for alias in (
            'type accountInspectionResult = proinspection.Result',
            'type accountInspectionSummary = proinspection.Summary',
            'type accountInspectionHealthCounts = proinspection.HealthCounts',
            'type accountInspectionPageInfo = proinspection.PageInfo',
            'type accountInspectionResultSnapshot = proinspection.ResultSnapshot',
            'type accountInspectionRunState = proinspection.RunState',
            'type accountInspectionLogEntry = proinspection.LogEntry',
            'type accountInspectionProgress = proinspection.Progress',
            'type accountInspectionStatus = proinspection.Status',
            'type accountInspectionLogStreamMessage = proinspection.LogStreamMessage',
            'type accountInspectionActionItem = proinspection.ActionItem',
            'type accountInspectionActionOutcome = proinspection.ActionOutcome',
        ):
            self.assertIn(alias, inspection)

        self.assertNotIn('"net/http"', inspection_runtime)
        self.assertNotIn('github.com/gorilla/websocket', inspection_runtime)
        for declaration in (
            'var accountInspectionWebSocketUpgrader = websocket.Upgrader{',
            'type accountInspectionPageInfo = proinspection.PageInfo',
            'type accountInspectionSnapshotOptions = proinspection.SnapshotOptions',
        ):
            self.assertIn(declaration, inspection_http)
        for declaration in (
            'type accountInspectionHTTPResult struct {',
            'Header     http.Header',
            'func (r accountInspectionHTTPResult) probeResponse() proinspection.ProbeResponse {',
        ):
            self.assertIn(declaration, inspection_transport)

        results_module = (PRO / 'inspection/results.go').read_text(encoding='utf-8')
        for declaration in (
            'func HealthBucketOf(',
            'func PaginateResults(',
            'func AutoActionForResult(',
            'func SummarizeResults(',
        ):
            self.assertIn(declaration, results_module)

        providers_module = (PRO / 'inspection/providers.go').read_text(encoding='utf-8')
        for declaration in (
            'func BuildAntigravityGroups(',
            'func BuildClaudeWindows(',
            'func BuildCodexWindows(',
            'func BuildKimiRows(',
        ):
            self.assertIn(declaration, providers_module)

        probes_module = (PRO / 'inspection/probes.go').read_text(encoding='utf-8')
        self.assertNotIn('http.Header', probes_module)
        for declaration in (
            'type ProbeResponse struct',
            'func ShouldDeepProbe(',
            'func BuildAntigravityDeepProbeBody(',
            'func ClassifyAntigravityDeepProbeResponse(',
            'func BuildXAIDeepProbeBody(',
            'func ClassifyXAIDeepProbeResponse(',
            'func RunXAIDeepProbeWithRetry(',
            'func SummarizeHTTPBody(',
        ):
            self.assertIn(declaration, probes_module)

        for delegation in (
            'proinspection.BuildAntigravityDeepProbeBody(',
            'proinspection.ClassifyAntigravityDeepProbeResponse(',
            'proinspection.BuildXAIDeepProbeBody(',
            'proinspection.ClassifyXAIDeepProbeResponse(',
        ):
            self.assertIn(delegation, inspection)
        self.assertIn('proinspection.RunXAIDeepProbeWithRetry(', inspection)
        for removed_wrapper in (
            'func buildAntigravityDeepProbeBody(',
            'func buildXAIDeepProbeBody(',
            'func buildAntigravityGroups(',
            'func buildCodexWindows(',
            'func buildXAIBillingSummary(',
            'func sortAccountInspectionResults(',
        ):
            self.assertNotIn(removed_wrapper, inspection)

        actions_module = (PRO / 'inspection/actions.go').read_text(encoding='utf-8')
        for declaration in (
            'type ActionItem struct',
            'type ActionOutcome struct',
            'func AccountKey(',
            'func ActionItemFromResult(',
            'func DedupeActionItems(',
            'func SummarizeActionOutcomes(',
            'func MergeManualActionResult(',
        ):
            self.assertIn(declaration, actions_module)
        for delegation in (
            'proinspection.AccountKey(',
            'proinspection.ActionItemFromResult(',
            'proinspection.DedupeActionItems(',
            'proinspection.SummarizeActionOutcomes(',
            'proinspection.MergeManualActionResult(',
        ):
            self.assertIn(delegation, inspection)

        selection_module = (PRO / 'inspection/selection.go').read_text(encoding='utf-8')
        for declaration in (
            'func ShouldInspectCandidate(',
            'func Sample[',
        ):
            self.assertIn(declaration, selection_module)

        workers_module = (PRO / 'inspection/workers.go').read_text(encoding='utf-8')
        for declaration in (
            'type KeyedLimiter struct',
            'func (l *KeyedLimiter) Acquire(',
            'func RunWorkers(',
            'func RunKeyedWorkers(',
        ):
            self.assertIn(declaration, workers_module)

        policy_module = (PRO / 'inspection/policy.go').read_text(encoding='utf-8')
        for declaration in (
            'func CodexDecision(',
            'func ErrorCode(',
            'func DecisionErrorCode(',
        ):
            self.assertIn(declaration, policy_module)

        for delegation in (
            'return proinspection.ShouldInspectCandidate(',
            'return proinspection.Sample(',
            'return proinspection.CodexDecision(',
        ):
            self.assertIn(delegation, inspection)
        self.assertIn('proinspection.RunKeyedWorkers(', inspection)
        self.assertIn('.probeLimiter.Acquire(', inspection)
        self.assertIn('.actionLimiter.Acquire(', inspection)
        self.assertNotIn('actionMu', inspection)
        for delegation in (
            'proinspection.ErrorCode(',
            'proinspection.DecisionErrorCode(',
        ):
            self.assertIn(delegation, inspection)
        for removed_wrapper in (
            'func accountInspectionErrorCode(',
            'func accountInspectionDecisionErrorCode(',
        ):
            self.assertNotIn(removed_wrapper, inspection)

        snapshot_module = (PRO / 'inspection/snapshot.go').read_text(encoding='utf-8')
        for declaration in (
            'type RunState string',
            'type ResultSnapshot struct',
            'func NormalizeSnapshotState(',
            'func DecodeResultSnapshot(',
        ):
            self.assertIn(declaration, snapshot_module)
        self.assertIn('return proinspection.DecodeResultSnapshot(', inspection)

        status_module = (PRO / 'inspection/status.go').read_text(encoding='utf-8')
        for declaration in (
            'type LogEntry struct',
            'type Status struct',
            'type LogStreamMessage struct',
            'func PaginateLogs(',
            'func ProjectStatus(',
            'func MergeTokenRefreshResult(',
            'func MergeReinspectionResult(',
        ):
            self.assertIn(declaration, status_module)
        for delegation in (
            'return proinspection.PaginateLogs(',
            'return proinspection.ProjectStatus(',
            'return proinspection.MergeTokenRefreshResult(',
            'return proinspection.MergeReinspectionResult(',
        ):
            self.assertIn(delegation, inspection)

        xai_billing = (PRO / 'quota/xai_billing.go').read_text(encoding='utf-8')
        for declaration in (
            'func BuildXAIBillingSummary(',
            'func MergeXAIBillingSummaries(',
            'func XAISummaryUsedPercent(',
            'func XAIPlanTypeFromBillingBody(',
            'func XAIPaidHealthSummary(',
        ):
            self.assertIn(declaration, xai_billing)

        quota_snapshot = (PRO / 'quota/snapshot.go').read_text(encoding='utf-8')
        self.assertIn('func SnapshotMaxUsedPercent(', quota_snapshot)

        quota_cache = (PRO / 'quota/cache.go').read_text(encoding='utf-8')
        for declaration in (
            'func SuccessCacheState(',
            'func JSONShapeHash(',
            'func JSONShapeHashForBodies(',
        ):
            self.assertIn(declaration, quota_cache)

        for delegation in (
            'proquota.SnapshotMaxUsedPercent(',
            'proquota.SuccessCacheState(',
            'proquota.JSONShapeHash(',
            'proquota.JSONShapeHashForBodies(',
            'proinspection.XAIOfficialAPIQuotaDecision(',
        ):
            self.assertIn(delegation, inspection)


if __name__ == '__main__':
    unittest.main()
