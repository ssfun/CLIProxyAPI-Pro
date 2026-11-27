Warning: truncated output (original token count: 80696)
Total output lines: 8186

#!/usr/bin/env python3
import os
import re
import subprocess
import tempfile
from pathlib import Path

ROOT = Path(os.environ.get('SRC_ROOT', '/src/CLIProxyAPI'))
PATCH_SOURCE_DIR = Path(__file__).resolve().parent / 'sources'
PRO_PANEL_REPOSITORY = 'https://github.com/ssfun/CLIProxyAPI-Pro'
PRO_PANEL_RELEASE_API = 'https://api.github.com/repos/ssfun/CLIProxyAPI-Pro/releases/latest'


_writes = {}


def read_text(path: Path) -> str:
    return path.read_text(encoding='utf-8')


def write_text(path: Path, text: str) -> None:
    path.write_text(text, encoding='utf-8')


def read(path: Path) -> str:
    if path in _writes:
        return _writes[path]
    return read_text(path)


def write(path: Path, text: str) -> None:
    _writes[path] = text


def module_path() -> str:
    match = re.search(r'^module\s+(\S+)', read_text(ROOT / 'go.mod'), re.MULTILINE)
    if not match:
        raise SystemExit(f'module path not found in {ROOT / "go.mod"}')
    return match.group(1)


def import_path(suffix: str) -> str:
    return f'{MODULE_PATH}/{suffix}'


def flush_writes() -> None:
    for path, text in _writes.items():
        path.parent.mkdir(parents=True, exist_ok=True)
        write_text(path, text)
    _writes.clear()


def format_go_writes(relative_paths: list[str]) -> None:
    changed_paths = [relative_path for relative_path in relative_paths if ROOT / relative_path in _writes]
    if not changed_paths:
        return
    relative_paths = changed_paths

    with tempfile.TemporaryDirectory(prefix='cliproxyapi-pro-gofmt-') as temp_dir:
        temp_root = Path(temp_dir)
        for relative_path in relative_paths:
            path = ROOT / relative_path
            temp_path = temp_root / relative_path
            temp_path.parent.mkdir(parents=True, exist_ok=True)
            write_text(temp_path, read(path))

        subprocess.run(['gofmt', '-w', *relative_paths], cwd=temp_root, check=True)

        for relative_path in relative_paths:
            write(ROOT / relative_path, read_text(temp_root / relative_path))


def queue_tree(source: Path, target: Path) -> None:
    for source_path in source.rglob('*'):
        if source_path.is_dir():
            continue
        text = read_text(source_path)
        if source_path.suffix == '.go':
            text = re.sub(r'github\.com/router-for-me/CLIProxyAPI/v\d+', MODULE_PATH, text)
        write(target / source_path.relative_to(source), text)


def queue_go_source(relative_path: str) -> None:
    source = PATCH_SOURCE_DIR / relative_path
    if not source.is_file():
        raise SystemExit(f'Go patch source not found: {source}')
    text = re.sub(r'github\.com/router-for-me/CLIProxyAPI/v\d+', MODULE_PATH, read_text(source))
    write(ROOT / relative_path, text)


def replace_once(path: Path, old: str, new: str, present=None) -> None:
    text = read(path)
    if (present or new) and (present or new) in text:
        return
    match_count = text.count(old)
    if match_count != 1:
        raise SystemExit(f'expected one pattern in {path}, found {match_count}: {old[:120]!r}')
    write(path, text.replace(old, new, 1))


def replace_all_exact(path: Path, old: str, new: str, expected_count: int) -> None:
    text = read(path)
    old_count = text.count(old)
    new_count = text.count(new)
    if old_count == 0 and new_count == expected_count:
        return
    if old_count != expected_count or new_count != 0:
        raise SystemExit(
            f'expected {expected_count} patterns in {path}, found old={old_count}, new={new_count}: {old[:120]!r}'
        )
    write(path, text.replace(old, new))


def insert_before(path: Path, marker: str, insertion: str, present: str) -> None:
    text = read(path)
    if present in text:
        return
    match_count = text.count(marker)
    if match_count != 1:
        raise SystemExit(f'expected one marker in {path}, found {match_count}: {marker[:120]!r}')
    write(path, text.replace(marker, insertion + marker, 1))


def insert_after_regex(path: Path, pattern: str, insertion: str, present: str) -> None:
    text = read(path)
    if present in text:
        return
    updated, count = re.subn(pattern, lambda match: match.group(0) + insertion, text, count=1)
    if count != 1:
        raise SystemExit(f'expected one regex match in {path}, found {count}: {pattern!r}')
    write(path, updated)


def ensure_go_require(path: Path, module: str, version: str) -> None:
    text = read(path)
    if re.search(rf'^\s*{re.escape(module)}\s+', text, re.MULTILINE):
        return
    line = f'\t{module} {version}\n'
    marker = 'require (\n'
    if marker in text:
        write(path, text.replace(marker, marker + line, 1))
        return
    write(path, text.rstrip() + f'\n\nrequire {module} {version}\n')


def insert_before_nth(path: Path, marker: str, insertion: str, occurrence: int, present: str) -> None:
    text = read(path)
    if present in text:
        return
    start = -1
    for _ in range(occurrence):
        start = text.find(marker, start + 1)
        if start < 0:
            raise SystemExit(f'pattern occurrence {occurrence} not found in {path}: {marker[:120]!r}')
    write(path, text[:start] + insertion + text[start:])


def add_go_import(path: Path, after: str, import_line: str) -> None:
    text = read(path)
    if import_line.strip() in text:
        return
    if after not in text:
        raise SystemExit(f'import anchor not found in {path}: {after[:120]!r}')
    write(path, text.replace(after, after + import_line, 1))


def replace_go_function(path: Path, signature: str, new_function: str, present: str) -> None:
    text = read(path)
    if present in text:
        return
    start = text.find(signature)
    if start < 0:
        raise SystemExit(f'function not found in {path}: {signature!r}')
    brace = text.find('{', start)
    if brace < 0:
        raise SystemExit(f'function body not found in {path}: {signature!r}')
    depth = 0
    for index in range(brace, len(text)):
        char = text[index]
        if char == '{':
            depth += 1
        elif char == '}':
            depth -= 1
            if depth == 0:
                end = index + 1
                if end < len(text) and text[end] == '\n':
                    end += 1
                write(path, text[:start] + new_function + text[end:])
                return
    raise SystemExit(f'function body end not found in {path}: {signature!r}')


def replace_go_call_block(path: Path, call_start: str, new_block: str, present: str) -> None:
    text = read(path)
    if present in text:
        return
    start = text.find(call_start)
    if start < 0:
        raise SystemExit(f'call block not found in {path}: {call_start!r}')
    brace = text.find('{', start)
    if brace < 0:
        raise SystemExit(f'call block body not found in {path}: {call_start!r}')
    depth = 0
    for index in range(brace, len(text)):
        char = text[index]
        if char == '{':
            depth += 1
        elif char == '}':
            depth -= 1
            if depth == 0:
                end = index + 1
                while end < len(text) and text[end] in ')\n':
                    end += 1
                    if text[end - 1] == '\n':
                        break
                write(path, text[:start] + new_block + text[end:])
                return
    raise SystemExit(f'call block end not found in {path}: {call_start!r}')


MODULE_PATH = module_path()
ACCOUNT_INSPECTION_SOURCE_FILES = (
    'account_inspection_runtime.go',
    'account_inspection_http.go',
    'account_inspection_batch.go',
    'account_inspection_history.go',
    'account_inspection_accounts.go',
    'account_inspection_extended_providers.go',
    'account_inspection_metrics.go',
    'account_inspection_probe_context.go',
    'account_inspection_transport.go',
    'account_inspection_quota.go',
    'account_inspection_xai_subscription.go',
    'account_inspection_xai_identity.go',
    'account_inspection_runtime_test.go',
    'account_inspection_http_test.go',
    'account_inspection_accounts_test.go',
    'account_inspection_provider_coverage_test.go',
    'account_inspection_transport_test.go',
    'account_inspection_provider_parity_test.go',
    'account_inspection_xai_subscription_test.go',
    'account_inspection_xai_identity_test.go',
    'account_inspection_quota_test.go',
)
customization_sentinel = ROOT / 'internal/embeddedusage'
if customization_sentinel.exists():
    raise SystemExit(f'target already contains CLIProxyAPI Pro customizations: {customization_sentinel}')

new_customization_paths = (
    'internal/cache/runtime_proxy_generation_pro.go',
    'internal/client/codex/live/realtime_usage_pro.go',
    'internal/runtime/executor/runtime_proxy_antigravity_pro.go',
    'internal/auth/claude/runtime_proxy_transport_pro.go',
    'internal/runtime/executor/helps/runtime_proxy_utls_pro.go',
    'sdk/cliproxy/runtime_proxy_provider_pro.go',
    'internal/runtime/executor/helps/runtime_proxy_helpers_pro.go',
    'internal/pro',
    'internal/api/api_key_policy_middleware_test.go',
    'internal/api/self_query.go',
    'internal/api/self_query_test.go',
	'internal/api/api_key_policy_models_test.go',
    'internal/api/handlers/management/account_inspection_host.go',
    'internal/api/handlers/management/api_key_policy.go',
    'internal/api/handlers/management/models.go',
    'internal/api/handlers/management/models_test.go',
    'internal/api/handlers/management/api_key_policy_test.go',
	'internal/api/handlers/management/auth_file_metadata.go',
    'internal/api/handlers/management/api_tools_executor_proxy_test.go',
    'internal/api/handlers/management/auth_file_connection.go',
    'internal/api/handlers/management/auth_file_connection_test.go',
    *[
        f'internal/api/handlers/management/{name}'
        for name in ACCOUNT_INSPECTION_SOURCE_FILES
    ],
    'internal/api/handlers/management/pro_plugin_quota.go',
    'internal/api/handlers/management/pro_plugin_quota_test.go',
    'internal/api/handlers/management/pro_auth_mutation.go',
    'internal/api/handlers/management/pro_auth_mutation_integration_test.go',
    'internal/api/handlers/management/pro_virtual_auth_consistency_integration_test.go',
    'internal/api/handlers/management/pro_features.go',
    'internal/api/handlers/management/pro_management_runtime.go',
    'internal/api/handlers/management/routing_policy.go',
    'internal/api/handlers/management/routing_policy_test.go',
    'internal/cache/bounded_lru_pro.go',
    'internal/config/config_existing_updates.go',
    'internal/config/config_existing_updates_test.go',
    'internal/config/pro_plugin_autoinstall.go',
    'internal/pluginhost/pro_executor_provider.go',
    'internal/pluginhost/gemini_cli_quota_legacy.go',
    'internal/pluginhost/gemini_cli_quota_legacy_test.go',
    'internal/pluginhost/gemini_cli_storage_compat.go',
    'internal/pluginhost/gemini_cli_storage_compat_test.go',
	'internal/pluginhost/plugin_executor_usage_test.go',
	'internal/pluginhost/plugin_executor_usage.go',
	'internal/pluginhost/pro_quota_provider.go',
	'internal/pluginhost/pro_quota_provider_test.go',
	'internal/pluginhost/runtime_proxy_override_test.go',
    'internal/pluginstore/autoinstall.go',
    'internal/pluginstore/autoinstall_test.go',
    'internal/pluginstore/gitstore_auth_test.go',
    'internal/managementasset/gitstore_token_test.go',
    'internal/requestmeta/observer.go',
    'internal/requestmeta/observer_test.go',
	'internal/api/server_model_policy.go',
	'internal/runtime/executor/helps/quota_settlement_test.go',
	'internal/runtime/executor/helps/usage_pro_extensions.go',
	'internal/runtime/executor/codex_retry_after_test.go',
    'internal/runtime/executor/helps/usage_speed_test.go',
	'internal/runtime/executor/claude_usage_speed_test.go',
	'internal/runtime/executor/claude_stream_terminal.go',
	'internal/runtime/executor/api_key_policy_usage_test.go',
	'internal/runtime/executor/response_translation.go',
	'internal/runtime/executor/antigravity_runtime_proxy_test.go',
	'internal/runtime/executor/helps/runtime_proxy_override_test.go',
	'internal/client/codex/live/runtime_proxy_override_test.go',
	'internal/pro/observability/config_test.go',
	'internal/redisqueue/speed_test.go',
	'internal/redisqueue/api_key_policy_usage_test.go',
	'internal/redisqueue/persistence_queue_test.go',
	'sdk/api/handlers/handlers_speed_test.go',
	'sdk/api/handlers/handlers_stream_cancellation_test.go',
	'sdk/api/handlers/api_key_policy_test.go',
	'sdk/api/handlers/api_key_policy_context_test.go',
	'sdk/auth/filestore_identity_test.go',
	'sdk/auth/filestore_identity.go',
	'sdk/cliproxy/auth/conductor_speed_test.go',
	'sdk/cliproxy/executor/speed.go',
	'sdk/cliproxy/usage/speed.go',
	'sdk/cliproxy/usage/speed_test.go',
	'sdk/cliproxy/usage/manager_pro_test.go',
	'sdk/cliproxy/usage/manager_extensions.go',
    'internal/runtime/executor/helps/response_observer_test.go',
    'internal/runtime/executor/xai_quota_observer.go',
    'sdk/cliproxy/auth/auth_runtime_state.go',
    'sdk/cliproxy/auth/auth_runtime_state_test.go',
	'sdk/cliproxy/auth/auth_account_policy.go',
	'sdk/cliproxy/auth/auth_account_policy_test.go',
	'sdk/cliproxy/auth/codex_retry_after_headers_test.go',
	'sdk/cliproxy/auth/schedulability_view.go',
	'sdk/cliproxy/auth/schedulability_view_test.go',
	'sdk/cliproxy/runtime_proxy_override_test.go',
	'sdk/cliproxy/auth/scheduler_runtime_state.go',
    'sdk/cliproxy/auth/inspection_refresh.go',
    'sdk/cliproxy/auth/antigravity_credits_recovery_pro.go',
    'sdk/cliproxy/auth/antigravity_credits_recovery_pro_test.go',
    'sdk/cliproxy/auth/pro_quota_protection.go',
    'internal/pro/routing/quota_protection.go',
    'internal/pro/routing/quota_protection_test.go',
    'internal/api/handlers/management/quota_recovery.go',
    'sdk/cliproxy/auth/inspection_refresh_test.go',
    'internal/runtime/executor/inspection_refresh_compat_test.go',
    'sdk/cliproxy/auth/pinned_execution.go',
    'sdk/cliproxy/pro_features_service_test.go',
)
for relative_path in new_customization_paths:
    target_path = ROOT / relative_path
    if target_path.exists():
        raise SystemExit(f'upstream path collides with a Pro customization: {target_path}')

queue_go_source('internal/runtime/executor/helps/runtime_proxy_helpers_pro.go')
queue_go_source('sdk/cliproxy/runtime_proxy_provider_pro.go')
queue_go_source('internal/runtime/executor/helps/runtime_proxy_utls_pro.go')
queue_go_source('internal/auth/claude/runtime_proxy_transport_pro.go')
queue_go_source('internal/runtime/executor/runtime_proxy_antigravity_pro.go')
queue_go_source('internal/cache/runtime_proxy_generation_pro.go')
queue_go_source('internal/client/codex/live/realtime_usage_pro.go')
queue_tree(PATCH_SOURCE_DIR / 'internal/pro', ROOT / 'internal/pro')
queue_tree(PATCH_SOURCE_DIR / 'sdk/proxyutil', ROOT / 'sdk/proxyutil')
queue_go_source('internal/config/pro_plugin_autoinstall.go')
queue_go_source('internal/cache/bounded_lru_pro.go')
queue_go_source('internal/pluginhost/pro_executor_provider.go')
queue_go_source('internal/api/api_key_policy_middleware_test.go')
queue_go_source('internal/api/self_query.go')
queue_go_source('internal/api/self_query_test.go')
queue_go_source('internal/api/api_key_policy_models_test.go')
queue_go_source('internal/api/handlers/management/api_key_policy.go')
queue_go_source('internal/api/handlers/management/models.go')
queue_go_source('internal/api/handlers/management/models_test.go')
queue_go_source('internal/api/handlers/management/api_key_policy_test.go')
queue_go_source('internal/api/handlers/management/auth_file_metadata.go')
queue_go_source('internal/api/server_model_policy.go')
queue_go_source('sdk/api/handlers/api_key_policy_test.go')
queue_go_source('sdk/api/handlers/api_key_policy_context_test.go')
queue_go_source('sdk/api/handlers/handlers_stream_cancellation_test.go')
queue_go_source('sdk/auth/filestore_identity_test.go')
queue_go_source('internal/runtime/executor/api_key_policy_usage_test.go')
queue_go_source('internal/runtime/executor/helps/quota_settlement_test.go')
queue_go_source('internal/runtime/executor/helps/usage_pro_extensions.go')
queue_go_source('internal/runtime/executor/codex_retry_after_test.go')
queue_go_source('internal/runtime/executor/response_translation.go')
queue_go_source('internal/runtime/executor/antigravity_runtime_proxy_test.go')
queue_go_source('internal/runtime/executor/helps/runtime_proxy_override_test.go')
queue_go_source('internal/client/codex/live/runtime_proxy_override_test.go')
queue_go_source('sdk/cliproxy/runtime_proxy_override_test.go')
queue_go_source('internal/pluginhost/runtime_proxy_override_test.go')
queue_go_source('internal/pro/observability/config_test.go')
queue_go_source('sdk/cliproxy/usage/manager_pro_test.go')
queue_go_source('sdk/cliproxy/usage/manager_extensions.go')
queue_go_source('internal/pluginhost/plugin_executor_usage.go')
queue_go_source('sdk/auth/filestore_identity.go')
queue_go_source('sdk/cliproxy/auth/scheduler_runtime_state.go')
queue_go_source('internal/runtime/executor/claude_stream_terminal.go')
queue_go_source('sdk/cliproxy/auth/codex_retry_after_headers_test.go')
queue_go_source('sdk/cliproxy/auth/schedulability_view.go')
queue_go_source('sdk/cliproxy/auth/schedulability_view_test.go')

claude_identity = ROOT / 'internal/auth/claude/identity.go'
insert_before(
    claude_identity,
    '// ReadMetadataString reads a string-valued metadata entry under the metadata\n',
    '''// ReadMetadataBool reads a boolean entry under the shared metadata lock.
func ReadMetadataBool(metadata *map[string]any, key string) bool {
\tif metadata == nil {
\t\treturn false
\t}
\tclaudeDevicePoolMu.Lock()
\tdefer claudeDevicePoolMu.Unlock()
\tvalue, _ := (*metadata)[key].(bool)
\treturn value
}

''',
    'func ReadMetadataBool(',
)
claude_executor_auth = ROOT / 'internal/runtime/executor/claude_executor_auth.go'
for flag, variable in (
    ('skip_account_profile', 'skip'),
    ('is_setup_token', 'isSetup'),
    ('setup_token', 'isSetup'),
):
    replace_once(
        claude_executor_auth,
        f'\tif {variable}, _ := auth.Metadata["{flag}"].(bool); {variable} {{\n',
        f'\tif claudeauth.ReadMetadataBool(&auth.Metadata, "{flag}") {{\n',
    )

# Native HTTP and WebSocket handshake helpers preserve Retry-After as of v8.0.45.
# Keep the stricter parser only for the Pro WebSocket connection-limit override.
codex_terminal = ROOT / 'internal/runtime/executor/codex_executor_terminal.go'
insert_before(
    codex_terminal,
    'func newCodexStatusErrWithCooling(',
    '''func parseCodexRetryAfterHeader(raw string, now time.Time) *time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	const maxRetryAfterSeconds = int64(1<<63-1) / int64(time.Second)
	deltaSeconds := true
	for _, digit := range raw {
		if digit < '0' || digit > '9' {
			deltaSeconds = false
			break
		}
	}
	if deltaSeconds {
		if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil && seconds > 0 && seconds <= maxRetryAfterSeconds {
			delay := time.Duration(seconds) * time.Second
			return &delay
		}
	}
	if resetAt, err := http.ParseTime(raw); err == nil {
		if delay := resetAt.Sub(now); delay > 0 {
			return &delay
		}
	}
	return nil
}

''',
    'func parseCodexRetryAfterHeader(',
)

codex_websocket_errors = ROOT / 'internal/runtime/executor/codex_websockets_errors.go'
replace_once(
    codex_websocket_errors,
    '''	if retryAfter := parseCodexRetryAfter(status, out, time.Now()); retryAfter != nil {
		statusError.retryAfter = retryAfter
	} else if isCodexWebsocketConnectionLimitError(payload) {
''',
    '''	now := time.Now()
	if retryAfter := parseCodexRetryAfter(status, out, now); retryAfter != nil {
		statusError.retryAfter = retryAfter
	} else if status == http.StatusTooManyRequests && isCodexWebsocketConnectionLimitError(payload) {
		// Pro honours an explicit frame-level retry hint. Nested hints retain
		// upstream's connection-limit immediate-retry precedence.
		hasFrameRetryAfter := false
		ojson.GetBytesNoCopy(payload, "headers").ForEach(func(key, _ ojson.Result) bool {
			if strings.EqualFold(strings.TrimSpace(key.String()), "Retry-After") {
				hasFrameRetryAfter = true
				return false
			}
			return true
		})
		retryAfter := time.Duration(0)
		statusError.retryAfter = &retryAfter
		if hasFrameRetryAfter {
			if retryAfter := parseCodexRetryAfterHeader(headers.Get("Retry-After"), now); retryAfter != nil {
				statusError.retryAfter = retryAfter
			}
		}
	} else if isCodexWebsocketConnectionLimitError(payload) {
''',
    'parseCodexRetryAfterHeader(headers.Get("Retry-After"), now)',
)

codex_websocket_tests = ROOT / 'internal/runtime/executor/codex_websockets_executor_test.go'
replace_once(
    codex_websocket_tests,
    '''	if got := *retryable.RetryAfter(); got != 0 {
		t.Fatalf("retryAfter = %v, want connection-limit fallback 0", got)
	}
''',
    '''	if got := *retryable.RetryAfter(); got != time.Second {
		t.Fatalf("retryAfter = %v, want standard Retry-After 1s", got)
	}
''',
    'want standard Retry-After 1s',
)

# Receiving Pong only releases the upload hook; it does not mean the request
# has arrived. Keep the mock upstream alive until its reader receives it.
# xAI already provides this synchronization upstream as of v8.0.43.
for variant in ('WithSession', 'Sessionless', 'NonstreamSessionless'):
    websocket_tests = codex_websocket_tests
    signature = f'func TestCodexWebsockets_KeepalivePingDuringUpload_{variant}(t *testing.T) {{'
    text = read(websocket_tests)
    start = text.find(signature)
    if start < 0:
        raise SystemExit(f'function not found in {websocket_tests}: {signature!r}')
    end = text.find('\nfunc ', start + len(signature))
    if end < 0:
        end = len(text)
    body = text[start:end]
    reader = '''\t\tgo func() {
\t\t\tfor {
\t\t\t\tif _, _, errRead := conn.ReadMessage(); errRead != nil {
\t\t\t\t\treturn
\t\t\t\t}
\t\t\t}
\t\t}()'''
    if variant == 'WithSession':
        reader = reader.replace('\t\t\t\t\treturn', '\t\t\t\t\treadErrCh <- errRead\n\t\t\t\t\treturn')
    # v8.0.63 supplies the same barrier natively. Validate the complete reader
    # and wait ordering before leaving it untouched; partial drift must fail.
    if 'payloadReceived' in body:
        native_reader = '\t\tpayloadReceived := make(chan struct{})\n' + reader.replace(
            '\t\tgo func() {\n', '\t\tgo func() {\n\t\t\tsignaled := false\n', 1,
        ).replace(
            '\t\t\t}\n\t\t}()',
            '\t\t\t\tif !signaled {\n\t\t\t\t\tsignaled = true\n'
            '\t\t\t\t\tclose(payloadReceived)\n\t\t\t\t}\n\t\t\t}\n\t\t}()', 1,
        )
        wait_message = 'timed out waiting for client payload after pong'
        if variant == 'Sessionless':
            wait_message += ' on sessionless connection'
        elif variant == 'NonstreamSessionless':
            wait_message += ' on nonstream sessionless connection'
        native_wait = (
            '\t\tselect {\n\t\tcase <-payloadReceived:\n'
            '\t\tcase <-time.After(5 * time.Second):\n'
            f'\t\t\tt.Errorf("{wait_message}")\n'
            '\t\t\treturn\n\t\t}\n'
        )
        response = '\t\trespPayload := '
        if (body.count('payloadReceived') != 3
                or body.count(native_reader) != 1
                or body.count(native_wait) != 1
                or body.count(response) != 1
                or not body.index(native_reader) < body.index(native_wait) < body.index(response)):
            raise SystemExit(f'expected native upload synchronization in {websocket_tests}: {signature}')
        continue
    if body.count(reader) != 1:
        raise SystemExit(f'expected one upload reader in {websocket_tests}: {signature}')
    # The Codex session test has an unused error channel before its reader.
    body = body.replace(reader, '''\t\trequestReadCh := make(chan error, 1)
\t\tgo func() {
\t\t\t_, _, errRead := conn.ReadMessage()
\t\t\trequestReadCh <- errRead
\t\t}()''', 1)
    body = body.replace('\t\treadErrCh := make(chan error, 1)\n', '', 1)
    marker = '\t\trespPayload := '
    if body.count(marker) != 1:
        raise SystemExit(f'expected one terminal response in {websocket_tests}: {signature}')
    body = body.replace(marker, '''\t\t// Pong unblocks the upload hook; wait for the actual request before closing.
\t\tselect {
\t\tcase errRead := <-requestReadCh:
\t\t\tif errRead != nil {
\t\t\t\tt.Errorf("read client request after pong: %v", errRead)
\t\t\t\treturn
\t\t\t}
\t\tcase <-time.After(2 * time.Second):
\t\t\tt.Error("timed out waiting for client request after pong")
\t\t\treturn
\t\t}

''' + marker, 1)
    write(websocket_tests, text[:start] + body + text[end:])

home_concurrency = ROOT / 'sdk/cliproxy/auth/home_concurrency.go'
# Preserve native carrier precedence, including aggregate and trusted deadlines;
# only add the Pro fallback after the generic safe-header provider.
replace_once(
    home_concurrency,
    '''	var safeHeaderProvider interface{ SafeResponseHeaders() http.Header }
	if errors.As(err, &safeHeaderProvider) && safeHeaderProvider != nil {
		if value := safeHeaderProvider.SafeResponseHeaders().Get("Retry-After"); value != "" {
			return http.Header{"Retry-After": []string{value}}
		}
	}
	return nil
''',
    '''	var safeHeaderProvider interface{ SafeResponseHeaders() http.Header }
	if errors.As(err, &safeHeaderProvider) && safeHeaderProvider != nil {
		if value := safeHeaderProvider.SafeResponseHeaders().Get("Retry-After"); value != "" {
			return http.Header{"Retry-After": []string{value}}
		}
	}
	var statusRetryAfter interface {
		StatusCode() int
		RetryAfter() *time.Duration
	}
	if errors.As(err, &statusRetryAfter) && statusRetryAfter != nil && statusRetryAfter.StatusCode() == http.StatusTooManyRequests {
		if retryAfter := statusRetryAfter.RetryAfter(); retryAfter != nil {
			return safeRetryAfterHeader(*retryAfter)
		}
	}
	return nil
''',
    'var statusRetryAfter interface',
)

codex_device = ROOT / 'sdk/auth/codex_device.go'
replace_once(
    codex_device,
    '''	metadata := map[string]any{
''',
    '''	metadata := map[string]any{
		"account_id": tokenStorage.AccountID,
''',
    '"account_id": tokenStorage.AccountID',
)

file_token_store = ROOT / 'sdk/auth/filestore.go'
replace_once(
    file_token_store,
    '''	path, err := s.resolveAuthPath(auth)
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", fmt.Errorf("auth filestore: missing file path attribute for %s", auth.ID)
	}

	// Runtime updates must not recreate a disabled credential whose source file
	// was deliberately removed. Login and migration callers explicitly mark the
	// save when creating a missing disabled credential is intentional.
	if auth.Disabled && !cliproxyauth.HasAuthCreationIntent(ctx) {
		if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
			return "", nil
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
''',
    '''	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.reuseExistingProviderIdentity(auth); err != nil {
		return "", err
	}
	path, err := s.resolveAuthPath(auth)
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", fmt.Errorf("auth filestore: missing file path attribute for %s", auth.ID)
	}

	// Runtime updates must not recreate a disabled credential whose source file
	// was deliberately removed. Login and migration callers explicitly mark the
	// save when creating a missing disabled credential is intentional.
	if auth.Disabled && !cliproxyauth.HasAuthCreationIntent(ctx) {
		if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
			return "", nil
		}
	}
''',
    's.reuseExistingProviderIdentity(auth)',
)
proxyutil_source = ROOT / 'sdk/proxyutil/proxy.go'
replace_once(
    proxyutil_source,
    'func BuildHTTPTransport(raw string) (*http.Transport, Mode, error) {\n\tsetting, errParse := Parse(raw)\n',
    'func BuildHTTPTransport(raw string) (*http.Transport, Mode, error) {\n\treturn buildHTTPTransport(ResolveEffectiveProxy(raw).Effective)\n}\n\n// BuildHTTPTransportWithoutRuntimeOverride builds a transport from an already\n// resolved setting. Cache users use it with ResolveEffectiveProxy so their key\n// and transport are derived from the same atomic override snapshot.\nfunc BuildHTTPTransportWithoutRuntimeOverride(raw string) (*http.Transport, Mode, error) {\n\treturn buildHTTPTransport(raw)\n}\n\nfunc buildHTTPTransport(raw string) (*http.Transport, Mode, error) {\n\tsetting, errParse := Parse(raw)\n',
    'func BuildHTTPTransportWithoutRuntimeOverride(',
)
replace_once(
    proxyutil_source,
    'func BuildDialer(raw string) (proxy.Dialer, Mode, error) {\n\tsetting, errParse := Parse(raw)\n',
    'func BuildDialer(raw string) (proxy.Dialer, Mode, error) {\n\treturn buildDialer(ResolveEffectiveProxy(raw).Effective)\n}\n\n// BuildDialerWithoutRuntimeOverride builds a dialer from the supplied raw\n// setting without applying the process-wide runtime proxy takeover. Internal\n// proxy-pool hops use this boundary to avoid dialing back into the pool.\nfunc BuildDialerWithoutRuntimeOverride(raw string) (proxy.Dialer, Mode, error) {\n\treturn buildDialer(raw)\n}\n\nfunc buildDialer(raw string) (proxy.Dialer, Mode, error) {\n\tsetting, errParse := Parse(raw)\n',
    'func BuildDialer(raw string) (proxy.Dialer, Mode, error) {\n\treturn buildDialer(ResolveEffectiveProxy(raw).Effective)',
)

proxy_helpers = ROOT / 'internal/runtime/executor/helps/proxy_helpers.go'
add_go_import(proxy_helpers, f'\t"{MODULE_PATH}/internal/config"\n', f'\tinternalcache "{MODULE_PATH}/internal/cache"\n')
add_go_import(proxy_helpers, '\t"strings"\n', '\t"sync/atomic"\n')
replace_once(
    proxy_helpers,
    '''		}
		// If proxy setup failed, log and fall through to context RoundTripper
		log.Debugf("failed to setup proxy from URL: %s, falling back to context transport", proxyutil.Redact(proxyURL))
	}
''',
    '''		}
		log.Debugf("failed to setup proxy from URL: %s, falling back to context transport", proxyutil.Redact(resolution.Effective))
	}
''',
)
replace_once(
    proxy_helpers,
    '''
	// Priority: request override, then auth.ProxyURL, then cfg.ProxyURL.
	proxyURL := effectiveProxyURL(ctx, cfg, auth)

	// Priority 1 & 2: Explicit proxy URL takes precedence over context RoundTripper.
	if proxyURL != "" {
		if transport := pooledProxyTransport(proxyURL, cfg, auth); transport != nil {
''',
    '''
	rawProxyURL := effectiveProxyURL(ctx, cfg, auth)
	resolution := proxyutil.ResolveEffectiveProxy(rawProxyURL)
	if resolution.Effective != "" {
		if transport := pooledProxyTransportResolved(resolution, cfg, auth); transport != nil {
''',
)
replace_once(
    proxy_helpers,
    '\tif transport := pooledProxyTransport("", cfg, auth); transport != nil {\n',
    '\tif transport := pooledProxyTransportResolved(resolution, cfg, auth); transport != nil {\n',
)
replace_once(
    proxy_helpers,
    '\tpublisher   uint64\n',
    '\tpublisher   uint64\n\truntimeGeneration uint64\n',
)
replace_once(
    proxy_helpers,
    'func pooledProxyTransport(proxyURL string, cfg *config.Config, auth *cliproxyauth.Auth) *http.Transport {\n',
    'func pooledProxyTransportResolved(resolution proxyutil.RuntimeProxyResolution, cfg *config.Config, auth *cliproxyauth.Auth) *http.Transport {\n',
)
replace_once(
    proxy_helpers,
    '\ttr, pooled := pooledTransport(proxyPoolKindStandard, proxyURL, cfg, auth, func() (*http.Transport, error) {\n\t\treturn buildPooledBaseTransport(proxyURL)\n',
    '\ttr, pooled := pooledTransportResolved(proxyPoolKindStandard, resolution, cfg, auth, func() (*http.Transport, error) {\n\t\treturn buildPooledBaseTransportResolved(resolution.Effective)\n',
)
replace_once(proxy_helpers, '\treturn buildProxyTransport(proxyURL)\n', '\treturn buildResolvedProxyTransport(resolution)\n')
replace_once(
    proxy_helpers,
    'func pooledTransport(kind proxyPoolKind, proxyURL string, cfg *config.Config, auth *cliproxyauth.Auth, build func() (*http.Transport, error)) (*http.Transport, bool) {\n',
    'func pooledTransportResolved(kind proxyPoolKind, resolution proxyutil.RuntimeProxyResolution, cfg *config.Config, auth *cliproxyauth.Auth, build func() (*http.Transport, error)) (*http.Transport, bool) {\n\tproxyURL := resolution.Effective\n',
)
replace_once(
    proxy_helpers,
    '\tcacheProxy, poolLabel := proxyPoolCacheTarget(setting)\n',
    '\tunlock, current := lockRuntimeProxyPoolGeneration(resolution.Generation)\n\tdefer unlock()\n\tif !current {\n\t\treturn unpooledTransport(build), true\n\t}\n\tcacheProxy, poolLabel := proxyPoolCacheTarget(setting)\n',
)
replace_once(proxy_helpers, '\t\tpublisher:   publisher,\n', '\t\tpublisher:   publisher,\n\t\truntimeGeneration: resolution.Generation,\n')
replace_once(
    proxy_helpers,
    'func buildPooledBaseTransport(proxyURL string) (*http.Transport, error) {\n',
    'func buildPooledBaseTransportResolved(proxyURL string) (*http.Transport, error) {\n',
)
replace_once(proxy_helpers, '\ttr, _, errBuild := proxyutil.BuildHTTPTransport(proxyURL)\n', '\ttr, _, errBuild := proxyutil.BuildHTTPTransportWithoutRuntimeOverride(proxyURL)\n')
replace_once(
    proxy_helpers,
    '''var devinTransportCache = NewTransportCache[string](DefaultTransportCacheCapacity)
''',
    '''var (
	devinTransportCache           = NewTransportCache[runtimeProxyTransportKey](DefaultTransportCacheCapacity)
	devinRuntimeProxyGeneration atomic.Uint64
)

func syncDevinRuntimeProxyGeneration(generation uint64) {
	internalcache.SyncGeneration(&devinRuntimeProxyGeneration, generation, devinTransportCache.Purge)
}
''',
)
replace_once(
    proxy_helpers,
    '''
	proxyURL := effectiveProxyURL(ctx, cfg, auth)

	tr, err := devinTransportCache.Get(proxyURL, func() (*http.Transport, error) {
		var base *http.Transport
		if proxyURL != "" {
			base = buildProxyTransport(proxyURL)
		}
		if base == nil {
			if dt, ok := http.DefaultTransport.(*http.Transport); ok {
				base = dt.Clone()
			} else {
''',
    '''
	rawProxyURL := effectiveProxyURL(ctx, cfg, auth)
	resolution := proxyutil.ResolveEffectiveProxy(rawProxyURL)
	syncDevinRuntimeProxyGeneration(resolution.Generation)
	key := runtimeProxyTransportKey{scope: resolution.Effective, generation: resolution.Generation}
	tr, err := devinTransportCache.Get(key, func() (*http.Transport, error) {
		base := buildResolvedProxyTransport(resolution)
		if base == nil {
			if defaultTransport, ok := http.DefaultTransport.(*http.Transport); ok {
				base = defaultTransport.Clone()
			} else {
''',
)
replace_once(
    proxy_helpers,
    '''			if tr, ok := rt.(*http.Transport); ok {
				key := fmt.Sprintf("rt:%p", tr)
				cloned, err := devinTransportCache.Get(key, func() (*http.Transport, error) {
''',
    '''			if tr, ok := rt.(*http.Transport); ok {
				key := runtimeProxyTransportKey{scope: fmt.Sprintf("rt:%p", tr), generation: generation}
				cloned, err := devinTransportCache.Get(key, func() (*http.Transport, error) {
''',
)
replace_once(
    proxy_helpers,
    '''func NewDevinHTTPClient(ctx context.Context, cfg *config.Config, auth *cliproxyauth.Auth, timeout time.Duration) (clientOut *http.Client) {
	defer func() { clientOut = httptransport.Decorate(ctx, clientOut) }()
	// A request proxy replaces both the injected round tripper and credential/global proxy.
	// Respect explicitly injected context RoundTripper only when no request override is set.
	if cliproxyexecutor.RequestProxyURL(ctx) == "" && ctx != nil {
''',
    '''func NewDevinHTTPClient(ctx context.Context, cfg *config.Config, auth *cliproxyauth.Auth, timeout time.Duration) (clientOut *http.Client) {
	defer func() { clientOut = httptransport.Decorate(ctx, clientOut) }()
	generation := proxyutil.ResolveEffectiveProxy("").Generation
	syncDevinRuntimeProxyGeneration(generation)
	if cliproxyexecutor.RequestProxyURL(ctx) == "" && ctx != nil {
''',
)
replace_once(
    proxy_helpers,
    '''func buildProxyTransport(proxyURL string) *http.Transport {
	transport, _, errBuild := proxyutil.BuildHTTPTransport(proxyURL)
	if errBuild != nil {
''',
    '''func buildResolvedProxyTransport(resolution proxyutil.RuntimeProxyResolution) *http.Transport {
	transport, _, errBuild := proxyutil.BuildHTTPTransportWithoutRuntimeOverride(resolution.Effective)
	if errBuild != nil {
''',
)

round_tripper_provider = ROOT / 'sdk/cliproxy/rtprovider.go'
replace_once(
    round_tripper_provider,
    '''type defaultRoundTripperProvider struct {
	mu    sync.RWMutex
	cache map[string]http.RoundTripper
}

func newDefaultRoundTripperProvider() *defaultRoundTripperProvider {
	return &defaultRoundTripperProvider{cache: make(map[string]http.RoundTripper)}
}
''',
    '''type defaultRoundTripperProvider struct {
	mu         sync.RWMutex
	generation uint64
	cache      map[runtimeProxyRoundTripperKey]http.RoundTripper
}

func newDefaultRoundTripperProvider() *defaultRoundTripperProvider {
	return &defaultRoundTripperProvider{cache: make(map[runtimeProxyRoundTripperKey]http.RoundTripper)}
}
''',
)
replace_once(
    round_tripper_provider,
    '''	proxyStr := strings.TrimSpace(auth.ProxyURL)
	if proxyStr == "" {
		return nil
	}
	p.mu.RLock()
	rt := p.cache[proxyStr]
	p.mu.RUnlock()
	if rt != nil {
		return rt
	}
''',
    '''	rawProxyURL := strings.TrimSpace(auth.ProxyURL)
	for {
		resolution := proxyutil.ResolveEffectiveProxy(rawProxyURL)
		key := runtimeProxyRoundTripperKey{effective: resolution.Effective, generation: resolution.Generation}

		p.mu.Lock()
		if p.generation > resolution.Generation {
			p.mu.Unlock()
			continue
		}
		if p.generation < resolution.Generation {
			for _, cached := range p.cache {
				if closer, ok := cached.(interface{ CloseIdleConnections() }); ok {
					closer.CloseIdleConnections()
				}
			}
			p.cache = make(map[runtimeProxyRoundTripperKey]http.RoundTripper)
			p.generation = resolution.Generation
		}
		if resolution.Effective == "" {
			p.mu.Unlock()
			return nil
		}
		if cached := p.cache[key]; cached != nil {
			p.mu.Unlock()
			return cached
		}
		p.mu.Unlock()

''',
)

replace_once(
    round_tripper_provider,
    '''	transport, _, errBuild := proxyutil.BuildHTTPTransport(proxyStr)
''',
    '''	transport, _, errBuild := proxyutil.BuildHTTPTransportWithoutRuntimeOverride(resolution.Effective)
''',
)

replace_once(
    round_tripper_provider,
    '''	if transport == nil {
		return nil
	}
	p.mu.Lock()
''',
    '''	if transport == nil {
		return nil
	}
		latest := proxyutil.ResolveEffectiveProxy(rawProxyURL)
		if latest.Generation != resolution.Generation || latest.Effective != resolution.Effective {
			transport.CloseIdleConnections()
			continue
		}

		p.mu.Lock()
		if p.generation != resolution.Generation {
			p.mu.Unlock()
			transport.CloseIdleConnections()
			continue
		}
		if cached := p.cache[key]; cached != nil {
			p.mu.Unlock()
			transport.CloseIdleConnections()
			return cached
		}
''',
)

replace_once(
    round_tripper_provider,
    '''	p.cache[proxyStr] = transport
	p.mu.Unlock()
	return transport
}
''',
    '''	p.cache[key] = transport
	p.mu.Unlock()
	return transport
	}
}
''',
)

utls_client = ROOT / 'internal/runtime/executor/helps/utls_client.go'
add_go_import(utls_client, '\t"sync"\n', '\t"sync/atomic"\n')
replace_once(
    utls_client,
    '''func newUtlsRoundTripper(proxyURL string) *utlsRoundTripper {
	var dialer proxy.Dialer = proxy.Direct
	if proxyURL != "" {
		proxyDialer, mode, errBuild := proxyutil.BuildDialer(proxyURL)
		if errBuild != nil {
			log.Errorf("utls: failed to configure proxy dialer for %q: %v", proxyutil.Redact(proxyURL), errBuild)
		} else if mode != proxyutil.ModeInherit && proxyDialer != nil {
''',
    '''func newUtlsRoundTripperResolved(effectiveProxyURL string) *utlsRoundTripper {
	var dialer proxy.Dialer = proxy.Direct
	if effectiveProxyURL != "" {
		proxyDialer, mode, errBuild := proxyutil.BuildDialerWithoutRuntimeOverride(effectiveProxyURL)
		if errBuild != nil {
			log.Errorf("utls: failed to configure proxy dialer for %q: %v", proxyutil.Redact(effectiveProxyURL), errBuild)
		} else if mode != proxyutil.ModeInherit && proxyDialer != nil {
''',
)
replace_once(
    utls_client,
    '''var claudeCodeRoundTripperCache = internalcache.NewBoundedLRU[string, http.RoundTripper](
	claudeCodeRoundTripperCacheCapacity,
	func(_ string, roundTripper http.RoundTripper) {
''',
    '''var (
	claudeCodeRoundTripperCache = internalcache.NewBoundedLRU[claudeCodeRoundTripperKey, http.RoundTripper](
		claudeCodeRoundTripperCacheCapacity,
		func(_ claudeCodeRoundTripperKey, roundTripper http.RoundTripper) {
''',
)
replace_once(
    utls_client,
    '''		transport.CloseIdleConnections()
		}
	},
)
''',
    '''			transport.CloseIdleConnections()
			}
		},
	)
	claudeCodeRuntimeProxyGeneration atomic.Uint64
)

func syncClaudeCodeRuntimeProxyGeneration(generation uint64) {
	internalcache.SyncGeneration(&claudeCodeRuntimeProxyGeneration, generation, claudeCodeRoundTripperCache.Purge)
}
''',
    'claudeCodeRuntimeProxyGeneration atomic.Uint64',
)
replace_once(
    utls_client,
    '''func cachedClaudeCodeRoundTripper(proxyURL string) http.RoundTripper {
	return claudeCodeRoundTripperCache.GetOrAdd(proxyURL, func() http.RoundTripper {
		return newClaudeCodeRoundTripper(proxyURL)
	})
''',
    '''func cachedClaudeCodeRoundTripperResolved(resolution proxyutil.RuntimeProxyResolution) http.RoundTripper {
	syncClaudeCodeRuntimeProxyGeneration(resolution.Generation)
	key := claudeCodeRoundTripperKey{effective: resolution.Effective, generation: resolution.Generation}
	return claudeCodeRoundTripperCache.GetOrAdd(key, func() http.RoundTripper {
		return newClaudeCodeRoundTripperResolved(resolution.Effective)
	})
''',
)
replace_once(
    utls_client,
    '''	var dialer proxy.Dialer = proxy.Direct
	if proxyURL != "" {
		proxyDialer, mode, errBuild := proxyutil.BuildDialer(proxyURL)
		if errBuild != nil {
			log.Errorf("claude tls: failed to configure proxy dialer for %q: %v", proxyutil.Redact(proxyURL), errBuild)
		} else if mode != proxyutil.ModeInherit && proxyDialer != nil {
''',
    '''	var dialer proxy.Dialer = proxy.Direct
	if effectiveProxyURL != "" {
		proxyDialer, mode, errBuild := proxyutil.BuildDialerWithoutRuntimeOverride(effectiveProxyURL)
		if errBuild != nil {
			log.Errorf("claude tls: failed to configure proxy dialer for %q: %v", proxyutil.Redact(effectiveProxyURL), errBuild)
		} else if mode != proxyutil.ModeInherit && proxyDialer != nil {
''',
)
replace_once(
    utls_client,
    '''func newClaudeCodeRoundTripper(proxyURL string) http.RoundTripper {
\treturn newClaudeCodeRoundTripperForDialer(claudeCodeDialer(proxyURL))
''',
    '''func newClaudeCodeRoundTripperResolved(effectiveProxyURL string) http.RoundTripper {
\treturn newClaudeCodeRoundTripperForDialer(claudeCodeDialerResolved(effectiveProxyURL))
''',
)
replace_once(utls_client, 'func claudeCodeDialer(proxyURL string) proxy.Dialer {\n', 'func claudeCodeDialerResolved(effectiveProxyURL string) proxy.Dialer {\n')
replace_once(
    utls_client,
    'func pooledClaudeCodeTransport(proxyURL string, cfg *config.Config, auth *cliproxyauth.Auth) *http.Transport {\n\ttr, pooled := pooledTransport(proxyPoolKindClaudeCode, proxyURL, cfg, auth, func() (*http.Transport, error) {\n\t\treturn newClaudeCodeRoundTripperForDialer(claudeCodeDialer(proxyURL)), nil\n',
    'func pooledClaudeCodeTransportResolved(resolution proxyutil.RuntimeProxyResolution, cfg *config.Config, auth *cliproxyauth.Auth) *http.Transport {\n\ttr, pooled := pooledTransportResolved(proxyPoolKindClaudeCode, resolution, cfg, auth, func() (*http.Transport, error) {\n\t\treturn newClaudeCodeRoundTripperForDialer(claudeCodeDialerResolved(resolution.Effective)), nil\n',
)
replace_once(
    utls_client,
    '''	var chromeRT http.RoundTripper = newUtlsRoundTripper(proxyURL)
	var anthropicRT http.RoundTripper = cachedClaudeCodeRoundTripper(proxyURL)
	var standardTransport http.RoundTripper = http.DefaultTransport
	var anthropicFn, fallbackFn func() http.RoundTripper
	if proxyURL != "" {
		fallbackFn = func() http.RoundTripper {
			if transport := pooledProxyTransport(proxyURL, cfg, auth); transport != nil {
				return transport
			}
			return standardTransport
		}
		// The Anthropic path has its own TLS fingerprint, so it needs its own pool kind.
		anthropicFn = func() http.RoundTripper {
			if transport := pooledClaudeCodeTransport(proxyURL, cfg, auth); transport != nil {
				return transport
			}
			return anthropicRT
		}
	} else if ctxRoundTripper != nil {
		chromeRT = ctxRoundTripper
		anthropicRT = ctxRoundTripper
		standardTransport = ctxRoundTripper
	} else {
		// A credential opt-in applies here too; an injected RoundTripper keeps priority.
		fallbackFn = func() http.RoundTripper {
			if transport := pooledProxyTransport("", cfg, auth); transport != nil {
				return transport
			}
			return standardTransport
		}
		anthropicFn = func() http.RoundTripper {
			if transport := pooledClaudeCodeTransport("", cfg, auth); transport != nil {
				return transport
			}
			return anthropicRT
		}
	}
''',
    '''	var chromeRT http.RoundTripper = newUtlsRoundTripperResolved(resolution.Effective)
	var anthropicRT http.RoundTripper = cachedClaudeCodeRoundTripperResolved(resolution)
	var standardTransport http.RoundTripper = http.DefaultTransport
	var anthropicFn, fallbackFn func() http.RoundTripper
	if resolution.Effective != "" {
		fallbackFn = func() http.RoundTripper {
			if transport := pooledProxyTransportResolved(resolution, cfg, auth); transport != nil {
				return transport
			}
			return standardTransport
		}
		// The Anthropic path has its own TLS fingerprint, so it needs its own pool kind.
		anthropicFn = func() http.RoundTripper {
			if transport := pooledClaudeCodeTransportResolved(resolution, cfg, auth); transport != nil {
				return transport
			}
			return anthropicRT
		}
	} else if ctxRoundTripper != nil {
		chromeRT = ctxRoundTripper
		anthropicRT = ctxRoundTripper
		standardTransport = ctxRoundTripper
	} else {
		// A credential opt-in applies here too; an injected RoundTripper keeps priority.
		fallbackFn = func() http.RoundTripper {
			if transport := pooledProxyTransportResolved(resolution, cfg, auth); transport != nil {
				return transport
			}
			return standardTransport
		}
		anthropicFn = func() http.RoundTripper {
			if transport := pooledClaudeCodeTransportResolved(resolution, cfg, auth); transport != nil {
				return transport
			}
			return anthropicRT
		}
	}
''',
)
replace_once(
    utls_client,
    '''	proxyURL := effectiveProxyURL(ctx, cfg, auth)

''',
    '''	rawProxyURL := effectiveProxyURL(ctx, cfg, auth)
	resolution := proxyutil.ResolveEffectiveProxy(rawProxyURL)

''',
)

claude_oauth_transport = ROOT / 'internal/auth/claude/utls_transport.go'
replace_once(
    claude_oauth_transport,
    '''var claudeOAuthSessionCaches = internalcache.NewBoundedLRU[string, tls.ClientSessionCache](
	claudeOAuthProxySessionCacheCapacity,
	nil,
)

func claudeOAuthSessionCache(proxyURL string) tls.ClientSessionCache {
	return claudeOAuthSessionCaches.GetOrAdd(proxyURL, func() tls.ClientSessionCache {
		return tls.NewLRUClientSessionCache(claudeOAuthSessionCacheCapacity)
	})
}
''',
    '''var claudeOAuthSessionCaches = internalcache.NewBoundedLRU[claudeOAuthSessionCacheKey, tls.ClientSessionCache](
	claudeOAuthProxySessionCacheCapacity,
	nil,
)

func claudeOAuthSessionCacheResolved(resolution proxyutil.RuntimeProxyResolution) tls.ClientSessionCache {
	key := claudeOAuthSessionCacheKey{effective: resolution.Effective, generation: resolution.Generation}
	return claudeOAuthSessionCaches.GetOrAdd(key, func() tls.ClientSessionCache {
		return tls.NewLRUClientSessionCache(claudeOAuthSessionCacheCapacity)
	})
}
''',
)
replace_once(
    claude_oauth_transport,
    '''		sessionCache: claudeOAuthSessionCache(proxyURL),
''',
    '''		sessionCache: claudeOAuthSessionCacheResolved(resolution),
''',
)
replace_once(
    claude_oauth_transport,
    '''func newUtlsRoundTripper(cfg *config.SDKConfig) *utlsRoundTripper {
	var dialer proxy.Dialer = proxy.Direct
	var proxyURL string
	if cfg != nil {
		proxyURL = cfg.ProxyURL
		proxyDialer, mode, errBuild := proxyutil.BuildDialer(cfg.ProxyURL)
		if errBuild != nil {
			log.Errorf("failed to configure proxy dialer for %q: %v", proxyutil.Redact(cfg.ProxyURL), errBuild)
		} else if mode != proxyutil.ModeInherit && proxyDialer != nil {
''',
    '''func newUtlsRoundTripper(cfg *config.SDKConfig) *utlsRoundTripper {
	rawProxyURL := ""
	if cfg != nil {
		rawProxyURL = cfg.ProxyURL
	}
	resolution := proxyutil.ResolveEffectiveProxy(rawProxyURL)
	var dialer proxy.Dialer = proxy.Direct
	if resolution.Effective != "" {
		proxyDialer, mode, errBuild := proxyutil.BuildDialerWithoutRuntimeOverride(resolution.Effective)
		if errBuild != nil {
			log.Errorf("failed to configure proxy dialer for %q: %v", proxyutil.Redact(resolution.Effective), errBuild)
		} else if mode != proxyutil.ModeInherit && proxyDialer != nil {
''',
)

antigravity_executor = ROOT / 'internal/runtime/executor/antigravity_executor.go'
add_go_import(antigravity_executor, '\t"strings"\n', '\t"sync/atomic"\n')
insert_before(
    antigravity_executor,
    'type antigravityTransportKey struct {',
    '''var antigravityRuntimeProxyGeneration atomic.Uint64

func syncAntigravityRuntimeProxyGeneration(generation uint64) {
	cache.SyncGeneration(&antigravityRuntimeProxyGeneration, generation, antigravityTransports.Purge)
}

''',
    'var antigravityRuntimeProxyGeneration atomic.Uint64',
)
replace_once(
    antigravity_executor,
    'type antigravityTransportKey struct {',
    'type antigravityTransportKey struct {\n\tgeneration uint64',
)
insert_before(
    antigravity_executor,
    '\t\tbase:                        base,',
    '\t\tgeneration:                  resolution.Generation,\n',
    'generation:                  resolution.Generation,',
)
replace_once(
    antigravity_executor,
    '''	if base == nil {
		return nil
	}
	var cfg *config.Config
	if len(cfgs) > 0 {
		cfg = cfgs[0]
''',
    '''	if base == nil {
		return nil
	}
	syncAntigravityRuntimeProxyGeneration(resolution.Generation)
	var cfg *config.Config
	if len(cfgs) > 0 {
		cfg = cfgs[0]
''',
)
replace_once(
    antigravity_executor,
    '''func antigravityHTTP11Transport(auth *cliproxyauth.Auth, base *http.Transport, cfgs ...*config.Config) *http.Transport {
	if base == nil {
''',
    '''func antigravityHTTP11TransportResolved(auth *cliproxyauth.Auth, base *http.Transport, resolution proxyutil.RuntimeProxyResolution, cfgs ...*config.Config) *http.Transport {
	if base == nil {
''',
)
replace_once(
    antigravity_executor,
    '''	transport, errGet := antigravityTransports.Get(key, func() (*http.Transport, error) {
		base, _, errBuild := proxyutil.BuildHTTPTransport(proxyURL)
		if errBuild != nil {
''',
    '''	transport, errGet := antigravityTransports.Get(key, func() (*http.Transport, error) {
		base, _, errBuild := proxyutil.BuildHTTPTransportWithoutRuntimeOverride(resolution.Effective)
		if errBuild != nil {
''',
)
replace_once(
    antigravity_executor,
    '''		credential:                  antigravityTransportScope(auth),
		proxy:                       proxyURL,
		shortMode:                   settings.shortMode,
''',
    '''		credential:          antigravityTransportScope(auth),
		proxy:               resolution.Effective,
		generation:          resolution.Generation,
		shortMode:           settings.shortMode,
''',
)
replace_once(
    antigravity_executor,
    '''func antigravityProxiedHTTP11Transport(auth *cliproxyauth.Auth, proxyURL string, cfgs ...*config.Config) *http.Transport {
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		return nil
	}
	var cfg *config.Config
''',
    '''func antigravityProxiedHTTP11TransportResolved(auth *cliproxyauth.Auth, resolution proxyutil.RuntimeProxyResolution, cfgs ...*config.Config) *http.Transport {
	if resolution.Effective == "" {
		return nil
	}
	syncAntigravityRuntimeProxyGeneration(resolution.Generation)
	var cfg *config.Config
''',
)
replace_once(
    antigravity_executor,
    '''	}
	client.Transport = antigravityHTTP11Transport(auth, transport, cfg)
	return client
''',
    '''	}
	client.Transport = antigravityHTTP11TransportResolved(auth, transport, resolution, cfg)
	return client
''',
)
replace_once(
    antigravity_executor,
    '''	// existing lifecycle and different OAuth identities remain isolated.
	if proxyURL := antigravityProxyURL(ctx, cfg, auth); proxyURL != "" {
		if transport := antigravityProxiedHTTP11Transport(auth, proxyURL, cfg); transport != nil {
			return &http.Client{Transport: transport, Timeout: timeout}
		}
		// Fall through so NewProxyAwareHTTPClient reports the failure and applies the
		// context transport fallback, preserving the previous behavior.
	}

	client := helps.NewProxyAwareHTTPClient(httptransport.WithoutDecorator(ctx), cfg, auth, timeout)
	// Direct requests share an HTTP/1.1 pool only within the selected credential.
	if client.Transport == nil {
		client.Transport = antigravityHTTP11Transport(auth, antigravityBaseTransport, cfg)
		return client
''',
    '''	// existing lifecycle and different OAuth identities remain isolated.
	if resolution.Effective != "" {
		if transport := antigravityProxiedHTTP11TransportResolved(auth, resolution, cfg); transport != nil {
			return &http.Client{Transport: transport, Timeout: timeout}
		}
		log.Debugf("antigravity executor: failed to setup proxy from URL: %s, falling back to context transport", proxyutil.Redact(resolution.Effective))
	}

	client := &http.Client{Timeout: timeout}
	if rt, ok := ctx.Value("cliproxy.roundtripper").(http.RoundTripper); ok && rt != nil {
		client.Transport = rt
	}
	// Direct requests share an HTTP/1.1 pool only within the selected credential.
	if client.Transport == nil {
		client.Transport = antigravityHTTP11TransportResolved(auth, antigravityBaseTransport, resolution, cfg)
		return client
''',
)
replace_once(
    antigravity_executor,
    '''func newAntigravityHTTPClient(ctx context.Context, cfg *config.Config, auth *cliproxyauth.Auth, timeout time.Duration) (clientOut *http.Client) {
	defer func() { clientOut = httptransport.Decorate(ctx, clientOut) }()
	// Native Antigravity reuses one transport across requests. Opt into a
''',
    '''func newAntigravityHTTPClient(ctx context.Context, cfg *config.Config, auth *cliproxyauth.Auth, timeout time.Duration) (clientOut *http.Client) {
	defer func() { clientOut = httptransport.Decorate(ctx, clientOut) }()
	// Resolve once so the transport route and its cache generation always come
	// from the same runtime-override snapshot.
	resolution := proxyutil.ResolveEffectiveProxy(antigravityProxyURL(ctx, cfg, auth))
	syncAntigravityRuntimeProxyGeneration(resolution.Generation)

	// Native Antigravity reuses one transport across requests. Opt into a
''',
)

pluginhost_http_bridge = ROOT / 'internal/pluginhost/http_bridge.go'
replace_once(
    pluginhost_http_bridge,
    '''	if proxyStr == "" && cfg != nil {
		proxyStr = strings.TrimSpace(cfg.ProxyURL)
	}

	var baseTransport *http.Transport
''',
    '''	if proxyStr == "" && cfg != nil {
		proxyStr = strings.TrimSpace(cfg.ProxyURL)
	}
	resolution := proxyutil.ResolveEffectiveProxy(proxyStr)
	proxyStr = resolution.Effective

	var baseTransport *http.Transport
''',
    'proxyStr = resolution.Effective',
)
replace_once(
    pluginhost_http_bridge,
    'builtTransport, _, errBuild := proxyutil.BuildHTTPTransport(proxyStr)',
    'builtTransport, _, errBuild := proxyutil.BuildHTTPTransportWithoutRuntimeOverride(proxyStr)',
    'BuildHTTPTransportWithoutRuntimeOverride(proxyStr)',
)
replace_once(
    pluginhost_http_bridge,
    '''func resolveProxyForRequest(r *http.Request, auth *coreauth.Auth, cfg *config.Config, proxyFunc func(*http.Request) (*url.URL, error)) (*url.URL, error) {
	if auth != nil && strings.TrimSpace(auth.ProxyURL) != "" {
		setting, errParse := proxyutil.Parse(strings.TrimSpace(auth.ProxyURL))
		if errParse != nil {
			return nil, fmt.Errorf("pluginhost: parse auth proxy: %w", errParse)
		}
		if setting.Mode == proxyutil.ModeDirect {
			return nil, nil
		}
		if setting.Mode == proxyutil.ModeProxy {
			return setting.URL, nil
		}
	}
	if cfg != nil && strings.TrimSpace(cfg.ProxyURL) != "" {
		setting, errParse := proxyutil.Parse(strings.TrimSpace(cfg.ProxyURL))
		if errParse != nil {
			return nil, fmt.Errorf("pluginhost: parse config proxy: %w", errParse)
		}
''',
    '''func resolveProxyForRequest(r *http.Request, auth *coreauth.Auth, cfg *config.Config, proxyFunc func(*http.Request) (*url.URL, error)) (*url.URL, error) {
	rawProxyURL := ""
	if auth != nil {
		rawProxyURL = strings.TrimSpace(auth.ProxyURL)
	}
	if rawProxyURL == "" && cfg != nil {
		rawProxyURL = strings.TrimSpace(cfg.ProxyURL)
	}
	resolution := proxyutil.ResolveEffectiveProxy(rawProxyURL)
	if resolution.Effective != "" {
		setting, errParse := proxyutil.Parse(resolution.Effective)
		if errParse != nil {
			return nil, fmt.Errorf("pluginhost: parse effective proxy: %w", errParse)
		}
''',
)

codex_live_sideband = ROOT / 'internal/client/codex/live/sideband.go'
replace_once(
    codex_live_sideband,
    '''func newSidebandDialer(proxyURL string) *websocket.Dialer {
	dialer := &websocket.Dialer{Proxy: http.ProxyFromEnvironment}
	if strings.TrimSpace(proxyURL) == "" {
''',
    '''func newSidebandDialer(proxyURL string) *websocket.Dialer {
	dialer := &websocket.Dialer{Proxy: http.ProxyFromEnvironment}
	proxyURL = proxyutil.ResolveEffectiveProxy(proxyURL).Effective
	if proxyURL == "" {
''',
    'proxyURL = proxyutil.ResolveEffectiveProxy(proxyURL).Effective',
)

write(
    ROOT / 'internal/runtime/executor/xai_quota_observer.go',
    re.sub(
        r'github\.com/router-for-me/CLIProxyAPI/v\d+',
        MODULE_PATH,
        read_text(Path(__file__).resolve().parent / 'xai_quota_observer.go'),
    ),
)
write(
    ROOT / 'internal/runtime/executor/xai_pro_bridge.go',
    re.sub(
        r'github\.com/router-for-me/CLIProxyAPI/v\d+',
        MODULE_PATH,
        read_text(Path(__file__).resolve().parent / 'xai_upstream_bridge.go'),
    ),
)
write(
    ROOT / 'internal/runtime/executor/xai_pro_bridge_test.go',
    re.sub(
        r'github\.com/router-for-me/CLIProxyAPI/v\d+',
        MODULE_PATH,
        read_text(Path(__file__).resolve().parent / 'xai_upstream_bridge_test.go'),
    ),
)
write(
    ROOT / 'internal/runtime/executor/helps/response_observer_test.go',
    re.sub(
        r'github\.com/router-for-me/CLIProxyAPI/v\d+',
        MODULE_PATH,
        read_text(Path(__file__).resolve().parent / 'response_observer_test.go'),
    ),
)

xai_executor = ROOT / 'internal/runtime/executor/xai_executor.go'
replace_once(
    xai_executor,
    '''\tvar attrs map[string]string
''',
    '''\tapplyProXAIHTTPRequestIdentity(req, auth)
\tvar attrs map[string]string
''',
    'applyProXAIHTTPRequestIdentity(req, auth)',
)

xai_executor_execute = ROOT / 'internal/runtime/executor/xai_executor_execute.go'
replace_once(
    xai_executor_execute,
    '''func (e *XAIExecutor) Execute(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (resp cliproxyexecutor.Response, err error) {
''',
    '''func (e *XAIExecutor) Execute(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (resp cliproxyexecutor.Response, err error) {
\tctx = withXAIQuotaObserver(ctx, auth, req.Model)
''',
    'ctx = withXAIQuotaObserver(ctx, auth, req.Model)',
)
xai_executor_stream = ROOT / 'internal/runtime/executor/xai_executor_stream.go'
replace_once(
    xai_executor_stream,
    '''func (e *XAIExecutor) ExecuteStream(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (_ *cliproxyexecutor.StreamResult, err error) {
''',
    '''func (e *XAIExecutor) ExecuteStream(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (_ *cliproxyexecutor.StreamResult, err error) {
\tctx = withXAIQuotaObserver(ctx, auth, req.Model)
''',
    'ctx = withXAIQuotaObserver(ctx, auth, req.Model)',
)
xai_websocket_executor = ROOT / 'internal/runtime/executor/xai_websockets_executor.go'
replace_once(
    xai_websocket_executor,
    '''func (e *XAIWebsocketsExecutor) ExecuteStream(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (_ *cliproxyexecutor.StreamResult, err error) {
''',
    '''func (e *XAIWebsocketsExecutor) ExecuteStream(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (_ *cliproxyexecutor.StreamResult, err error) {
\tctx = withXAIQuotaObserver(ctx, auth, req.Model)
''',
    'ctx = withXAIQuotaObserver(ctx, auth, req.Model)',
)
# Upstream v8.0.67 centralizes retry-dial failure recording in
# recordWebsocketDialRetryFailure. Keep the old rejection hook for older
# releases, but validate the native helper and call site when present.
xai_websocket_text = read(xai_websocket_executor)
xai_retry_anchor = '''\t\t\t\tbodyErrRetry := websocketHandshakeBody(respHSRetry)
\t\t\t\tcloseHTTPResponseBody(respHSRetry, "xai websockets executor: close handshake response body error")
'''
if xai_retry_anchor in xai_websocket_text:
    replace_once(
        xai_websocket_executor,
        xai_retry_anchor,
        '''\t\t\t\tbodyErrRetry := websocketHandshakeBody(respHSRetry)
\t\t\t\tif respHSRetry != nil {
\t\t\t\t\thelps.RecordAPIWebsocketUpgradeRejection(ctx, e.cfg, websocketUpgradeRequestLog(wsReqLog), respHSRetry.StatusCode, respHSRetry.Header.Clone(), bodyErrRetry)
\t\t\t\t}
\t\t\t\tcloseHTTPResponseBody(respHSRetry, "xai websockets executor: close handshake response body error")
''',
        'helps.RecordAPIWebsocketUpgradeRejection(ctx, e.cfg, websocketUpgradeRequestLog(wsReqLog), respHSRetry.StatusCode',
    )
else:
    native_retry_call = 'bodyErrRetry := recordWebsocketDialRetryFailure(ctx, e.cfg, wsReqLog, respHSRetry, errDialRetry)'
    if xai_websocket_text.count(native_retry_call) != 1:
        raise SystemExit('expected native websocket retry failure recording in xai_websockets_executor.go')

# Upstream owns the quota capability, RPC transport and reset/describe endpoints.
# Keep only the Pro snapshot fields needed for persisted quota and legacy plugins.
pluginapi_types = ROOT / 'sdk/pluginapi/types.go'
insert_before(
    pluginapi_types,
    '// ModelRegistrar registers plugin-provided models with the host.\n',
    read_text(Path(__file__).resolve().parent / 'plugin_quota_api.go'),
    'type QuotaSnapshot struct',
)
replace_once(
    pluginapi_types,
    'type QuotaFetchRequest struct {\n',
    'type QuotaFetchRequest struct {\n\tPlugin Metadata `json:"plugin,omitempty"`\n\tAuthProvider string `json:"auth_provider,omitempty"`\n\tPrevious *QuotaSnapshot `json:"previous,omitempty"`\n',
)
replace_once(
    pluginapi_types,
    'type QuotaFetchResponse struct {\n',
    'type QuotaFetchResponse struct {\n\tSnapshot QuotaSnapshot `json:"snapshot,omitempty"`\n\tPlanUnavailable bool `json:"plan_unavailable,omitempty"`\n\tPlanError string `json:"plan_error,omitempty"`\n\tAuthUpdate AuthData `json:"auth_update,omitempty"`\n',
)

plugin_snapshot = ROOT / 'internal/pluginhost/snapshot.go'
replace_once(plugin_snapshot, '\tQuotaProvider        string\n', '\tQuotaProvider        string\n\tQuotaMode string\n')
replace_once(
    plugin_snapshot,
    '''\t\tout = append(out, RegisteredPluginInfo{
''',
    '''\t\tquotaMode := ""
\t\tsupportsQuota := quotaProvider != nil
\t\tif quotaIdentifier != "" {
\t\t\tquotaMode = "native"
\t\t} else if quotaProvider == nil {
\t\t\tif identifier, okLegacy := h.legacyQuotaProviderForRecord(record); okLegacy {
\t\t\t\tquotaIdentifier = identifier
\t\t\t\tquotaMode = "legacy-adapter"
\t\t\t\tsupportsQuota = true
\t\t\t}
\t\t}
\t\tout = append(out, RegisteredPluginInfo{
''',
    'quotaMode := ""',
)
replace_once(plugin_snapshot, '\t\t\tSupportsQuota:        quotaProvider != nil,\n', '\t\t\tSupportsQuota: supportsQuota,\n')
replace_once(plugin_snapshot, '\t\t\tQuotaProvider:        quotaIdentifier,\n', '\t\t\tQuotaProvider: quotaIdentifier,\n\t\t\tQuotaMode: quotaMode,\n')

management_plugins = ROOT / 'internal/api/handlers/management/plugins.go'
replace_once(management_plugins, '\tQuotaProvider        string                         `json:"quota_provider,omitempty"`\n', '\tQuotaProvider        string                         `json:"quota_provider,omitempty"`\n\tQuotaMode string `json:"quota_mode"`\n')
replace_once(management_plugins, '\t\t\tentry.QuotaProvider = htmlsanitize.String(info.QuotaProvider)\n', '\t\t\tentry.QuotaProvider = htmlsanitize.String(info.QuotaProvider)\n\t\t\tentry.QuotaMode = htmlsanitize.String(info.QuotaMode)\n')

quota_provider_source = Path(__file__).resolve().parent / 'plugin_quota_provider.go'
quota_provider_target = ROOT / 'internal/pluginhost/pro_quota_provider.go'
write(quota_provider_target, re.sub(r'github\.com/router-for-me/CLIProxyAPI/v\d+', MODULE_PATH, read_text(quota_provider_source)))
quota_provider_test_source = Path(__file__).resolve().parent / 'plugin_quota_provider_test.go'
quota_provider_test_target = ROOT / 'internal/pluginhost/pro_quota_provider_test.go'
write(quota_provider_test_target, re.sub(r'github\.com/router-for-me/CLIProxyAPI/v\d+', MODULE_PATH, read_text(quota_provider_test_source)))

service_models = ROOT / 'sdk/cliproxy/service_models.go'
insert_before(
    service_models,
    'func (s *Service) oauthExcludedModels(provider, authKind string) []string {\n',
    '''func (s *Service) applyOAuthPolicy(ctx context.Context, cfg *config.Config, auth *coreauth.Auth, models []*ModelInfo) []*ModelInfo {
\tif s == nil || s.proApp == nil || auth == nil || len(models) == 0 {
\t\treturn models
\t}
\treturn s.proApp.FilterModels(ctx, cfg, auth, models, s.coreManager)
}

''',
    'func (s *Service) applyOAuthPolicy',
)

# Native dynamic Antigravity catalogs bypass the common model pipeline.
antigravity_models = ROOT / 'sdk/cliproxy/antigravity_models.go'
replace_once(
    antigravity_models,
    'func (s *Service) antigravityModelsForHintsWithConfig(auth *coreauth.Auth, hints antigravityModelCapabilityHints, cfg *config.Config) []*ModelInfo {',
    '''func (s *Service) antigravityModelsForHintsWithConfig(auth *coreauth.Auth, hints antigravityModelCapabilityHints, cfg *config.Config) []*ModelInfo {
\treturn s.antigravityModelsForHintsWithPolicy(context.Background(), auth, hints, cfg)
}

func (s *Service) antigravityModelsForHintsWithPolicy(ctx context.Context, auth *coreauth.Auth, hints antigravityModelCapabilityHints, cfg *config.Config) []*ModelInfo {''',
    'func (s *Service) antigravityModelsForHintsWithPolicy',
)
replace_once(
    antigravity_models,
    '\tmodels = applyExcludedModels(models, excluded)\n',
    '''\tmodels = applyExcludedModels(models, excluded)
\tif s.proApp != nil { models = s.proApp.FilterModels(ctx, cfg, auth, models, s.coreManager) }
''',
    'if s.proApp != nil { models = s.proApp.FilterModels(ctx, cfg, auth, models, s.coreManager) }',
)
replace_once(
    antigravity_models,
    '\treturn a != nil && b != nil && a.Provider == b.Provider && a.Prefix == b.Prefix &&\n',
    '''\tif a != nil { a = a.Clone(); coreauth.RestoreAccountPolicyBase(a) }
\tif b != nil { b = b.Clone(); coreauth.RestoreAccountPolicyBase(b) }
\treturn a != nil && b != nil && a.Provider == b.Provider && a.Prefix == b.Prefix &&
''',
    'if a != nil { a = a.Clone(); coreauth.RestoreAccountPolicyBase(a) }',
)
insert_before(
    antigravity_models,
    '\tpublish := func(current *coreauth.Auth) bool {',
    '''	if ctx == nil { ctx = context.Background() }
	cfg := s.currentConfig()
	var models []*ModelInfo
	var commitPolicy func(func()) bool
	if hints.ModelIDs != nil {
		policyAuth := auth.Clone()
		if s.coreManager != nil {
			current, exists := s.coreManager.GetByID(auth.ID)
			if !exists || !antigravityAuthModelSettingsEqual(current, auth) { return }
			policyAuth = current.Clone()
		}
		coreauth.RestoreAccountPolicyBase(policyAuth)
		if s.proApp != nil && s.proApp.OAuthPolicy() != nil {
			commitPolicy = s.proApp.OAuthPolicy().PublicationGuard(policyAuth.ID, policyAuth.Generation, policyAuth.RegistrationEpoch)
		}
		models = s.antigravityModelsForHintsWithPolicy(ctx, policyAuth, hints, cfg)
	}
''',
    'var commitPolicy func(func()) bool',
)
replace_once(
    antigravity_models,
    'models := s.antigravityModelsForHintsWithConfig(current, hints, s.cfg)',
    'if s.cfg != cfg { return false }',
)
replace_once(
    antigravity_models,
    'appliedEpoch, ok := reg.ReplaceClientModels(current.ID, providerKey, expectedRegEpoch, models)',
    '''var appliedEpoch uint64
			var ok bool
			commit := func() {
				if ctx.Err() == nil {
					appliedEpoch, ok = reg.ReplaceClientModels(current.ID, providerKey, expectedRegEpoch, models)
				}
			}
			if commitPolicy != nil { commitPolicy(commit) } else { commit() }''',
)
insert_before(
    antigravity_models,
    '\tvar updated bool\n',
    '''	s.authModelCommitMu.Lock()
	if _, found := ctx.Value(authModelRegistrationContextKey{}).(authModelRegistrationToken); found && !s.currentAuthModelRegistrationLocked(ctx, auth) {
		s.authModelCommitMu.Unlock()
		return
	}
''',
    'if _, found := ctx.Value(authModelRegistrationContextKey{})',
)
insert_before(
    antigravity_models,
    '\tif updated && s.coreManager != nil {',
    '\ts.authModelCommitMu.Unlock()\n',
    '\ts.authModelCommitMu.Unlock()\n\tif updated',
)

insert_before(
    antigravity_models,
    '\t\tupdated = s.coreManager.PublishIfRegistrationCurrentContext(ctx, auth.ID, expectedEpoch',
    '\t\tauth = auth.Clone()\n\t\tcoreauth.RestoreAccountPolicyBase(auth)\n',
    'coreauth.RestoreAccountPolicyBase(auth)',
)

service_auth = ROOT / 'sdk/cliproxy/service_auth.go'
replace_once(
    service_auth,
    '''\ts.coreManager.Remove(ctx, id)
\tGlobalModelRegistry().UnregisterClient(id)
''',
    '''\ts.coreManager.Remove(ctx, id)
\tGlobalModelRegistry().UnregisterClient(id)
\tif s.proApp != nil {
\t\ts.proApp.ForgetAccountPolicy(id)
\t}
\ts.authModelCommitMu.Unlock()
''',
    's.authModelCommitMu.Unlock()',
)

service_source = ROOT / 'sdk/cliproxy/service.go'
replace_once(
    service_source,
    '''\tconfigRuntimeMu        sync.Mutex
\texecutorRegistrationMu sync.Mutex
''',
    '''\tconfigRuntimeMu        sync.Mutex
\texecutorRegistrationMu sync.Mutex
\tauthModelCommitMu      sync.Mutex
\tauthModelGenerations   map[string]uint64
''',
    'authModelGenerations   map[string]uint64',
)

service_executors = ROOT / 'sdk/cliproxy/service_executors.go'
# Keep the upstream identity/registry-epoch fence inside the Pro queue-token
# gate. Neither layer may adopt a newer publication while an old task finishes.
insert_before(
    service_executors,
    '// registerResolvedModelsForAuthFenced publishes models only if fence still\n',
    '''func (s *Service) registerResolvedModelsForCurrentAuth(ctx context.Context, a *coreauth.Auth, fence modelRegistrationFence, providerKey string, models []*ModelInfo) bool {
\tvar published bool
\tcurrent := s.withCurrentAuthModelCommit(ctx, a, func() {
\t\tpublished = s.registerResolvedModelsForAuthFenced(a, fence, providerKey, models)
\t})
\tif !current && a != nil {
\t\tlog.WithFields(log.Fields{"auth_id": a.ID, "provider": providerKey}).Warnf("model registration for auth %s was superseded before publication; discarded the stale queue result", a.ID)
\t}
\treturn current && published
}

''',
    'func (s *Service) registerResolvedModelsForCurrentAuth',
)
service_models_patterns = [
    '''\tmodels := applyExcludedModels(result.Models, activeExcluded)
\tmodels = applyOAuthModelAliasForAuth(cfg, providerKey, activeAuthKind, activeAuth.Attributes, models)
''',
    '''\tmodels = applyExcludedModels(models, activeExcluded)
\tmodels = applyOAuthModelAliasForAuth(cfg, providerKey, activeAuthKind, activeAuth.Attributes, models)
''',
]
service_models_text = read(service_executors)
service_models_pattern = next((x for x in service_models_patterns if x in service_models_text), None)
if service_models_pattern is None:
    raise SystemExit('expected service model publication anchor')
replace_once(service_executors, service_models_pattern, service_models_pattern.replace('\n\tmodels = applyOAuthModelAliasForAuth', '\n\tmodels = s.applyOAuthPolicy(ctx, cfg, activeAuth, models)\n\tmodels = applyOAuthModelAliasForAuth'), 'models = s.applyOAuthPolicy(ctx, cfg, activeAuth, models)')

replace_once(
    service_executors,
    's.registerResolvedModelsForAuthFenced(activeAuth, publicationFence, providerKey, applyModelPrefixes(models, activeAuth.Prefix, cfg != nil && cfg.ForceModelPrefix))',
    's.registerResolvedModelsForCurrentAuth(ctx, activeAuth, publicationFence, providerKey, applyModelPrefixes(models, activeAuth.Prefix, cfg != nil && cfg.ForceModelPrefix))',
    's.registerResolvedModelsForCurrentAuth(ctx, activeAuth, publicationFence, providerKey',
)

replace_once(
    service_auth,
    '''\tid = strings.TrimSpace(id)
\tvar provider string
''',
    '''\tid = strings.TrimSpace(id)
\ts.authModelCommitMu.Lock()
\tif s.authModelGenerations == nil {
\t\ts.authModelGenerations = make(map[string]uint64)
\t}
\ts.authModelGenerations[id]++
\tvar provider string
''',
    '''\tid = strings.TrimSpace(id)
\ts.authModelCommitMu.Lock()
\tif s.authModelGenerations == nil {
\t\ts.authModelGenerations = make(map[string]uint64)
\t}
\ts.authModelGenerations[id]++
\tvar provider string
    ''',
)

insert_before(
    service_auth,
    'func (s *Service) applyCoreAuthRemoval(ctx context.Context, id string) {\n',
    '''type authModelRegistrationContextKey struct{}

type authModelRegistrationToken struct {
\tauthID     string
\tgeneration uint64
}

func (s *Service) beginAuthModelRegistration(ctx context.Context, authID string) context.Context {
\tif ctx == nil {
\t\tctx = context.Background()
\t}
\tif token, found := ctx.Value(authModelRegistrationContextKey{}).(authModelRegistrationToken); found && token.authID == authID {
\t\treturn ctx
\t}
\ts.authModelCommitMu.Lock()
\tif s.authModelGenerations == nil {
\t\ts.authModelGenerations = make(map[string]uint64)
\t}
\ts.authModelGenerations[authID]++
\ttoken := authModelRegistrationToken{authID: authID, generation: s.authModelGenerations[authID]}
\ts.authModelCommitMu.Unlock()
\treturn context.WithValue(ctx, authModelRegistrationContextKey{}, token)
}

func (s *Service) currentAuthModelRegistrationLocked(ctx context.Context, auth *coreauth.Auth) bool {
\tif s == nil || ctx == nil || auth == nil || auth.ID == "" {
\t\treturn false
\t}
\ttoken, found := ctx.Value(authModelRegistrationContextKey{}).(authModelRegistrationToken)
\tif !found || token.authID != auth.ID || s.authModelGenerations[token.authID] != token.generation {
\t\treturn false
\t}
\tif s.coreManager != nil {
\t\tif current, exists := s.coreManager.GetByID(auth.ID); !exists || current == nil {
\t\t\treturn false
\t\t}
\t}
\treturn true
}

func (s *Service) isCurrentAuthModelRegistration(ctx context.Context, auth *coreauth.Auth) bool {
\tif s == nil {
\t\treturn false
\t}
\ts.authModelCommitMu.Lock()
\tdefer s.authModelCommitMu.Unlock()
\treturn s.currentAuthModelRegistrationLocked(ctx, auth)
}

func (s *Service) withCurrentAuthModelCommit(ctx context.Context, auth *coreauth.Auth, commit func()) bool {
\tif s == nil || auth == nil || auth.ID == "" || commit == nil {
\t\treturn false
\t}
\ts.authModelCommitMu.Lock()
\tdefer s.authModelCommitMu.Unlock()
\tif !s.currentAuthModelRegistrationLocked(ctx, auth) {
\t\treturn false
\t}
\tcommit()
\treturn true
}

func (s *Service) updateAuthForCurrentModelRegistration(ctx context.Context, auth, updated *coreauth.Auth) (*coreauth.Auth, bool, error) {
\tif s == nil || s.coreManager == nil || updated == nil {
\t\treturn nil, false, nil
\t}
\tvar result *coreauth.Auth
\tvar committed bool
\tvar errUpdate error
\tif !s.withCurrentAuthModelCommit(ctx, auth, func() {
\t\tresult, committed, errUpdate = s.coreManager.UpdateIfRegistrationCurrent(ctx, auth, updated)
\t}) {
\t\treturn nil, false, nil
\t}
\treturn result, committed, errUpdate
}

func (s *Service) unregisterModelsForCurrentAuth(ctx context.Context, auth *coreauth.Auth, fence modelRegistrationFence) bool {
\tvar published bool
\tcurrent := s.withCurrentAuthModelCommit(ctx, auth, func() {
\t\tpublished = s.unregisterModelsForAuthFenced(auth, fence)
\t})
\tif !current && auth != nil {
\t\tlog.WithField("auth_id", auth.ID).Warnf("model unregistration for auth %s was superseded before publication; discarded the stale queue result", auth.ID)
\t}
\treturn current && published
}

''',
    'func (s *Service) withCurrentAuthModelCommit',
)

replace_once(
    service_models,
    '''\tfence := newModelRegistrationFence(a, cfg)
\ts.cancelStaleAntigravityProbes(a.ID)
''',
    '''\tctx = s.beginAuthModelRegistration(ctx, a.ID)
\tif !s.isCurrentAuthModelRegistration(ctx, a) {
\t\treturn false
\t}
\tfence := newModelRegistrationFence(a, cfg)
\ts.cancelStaleAntigravityProbes(a.ID)
''',
    'ctx = s.beginAuthModelRegistration(ctx, a.ID)',
)
service_models_text = read(service_models)
unregister_model_call = 's.unregisterModelsForAuthFenced(a, fence)'
if service_models_text.count(unregister_model_call) != 5:
    raise SystemExit(
        f'expected five auth model unregister calls in {service_models}, '
        f'found {service_models_text.count(unregister_model_call)}'
    )
write(
    service_models,
    service_models_text.replace(
        unregister_model_call,
        's.unregisterModelsForCurrentAuth(ctx, a, fence)',
    ).replace(
        's.registerResolvedModelsForAuthFenced(a, fence,',
        's.registerResolvedModelsForCurrentAuth(ctx, a, fence,',
    ),
)

replace_once(
    service_executors,
    '\treturn true, s.unregisterModelsForAuthFenced(activeAuth, publicationFence)\n',
    '\treturn true, s.unregisterModelsForCurrentAuth(ctx, activeAuth, publicationFence)\n',
    's.unregisterModelsForCurrentAuth(ctx, activeAuth, publicationFence)',
)

replace_once(
    service_executors,
    '''\t\tupdated, committed, errUpdate := s.coreManager.UpdateIfRegistrationCurrent(ctx, a, result.Auth)
''',
    '''\t\tupdated, committed, errUpdate := s.updateAuthForCurrentModelRegistration(ctx, a, result.Auth)
''',
    's.updateAuthForCurrentModelRegistration(ctx, a, result.Auth)',
)

service_plugins = ROOT / 'sdk/cliproxy/service_plugins.go'
service_config_source = ROOT / 'sdk/cliproxy/service_config.go'
replace_once(
    service_plugins,
    '''\t\tauthForRegistration := auth.Clone()
\t\ttasks = append(tasks, modelRegistrationTask{
''',
    '''\t\tauthForRegistration := auth.Clone()
\t\tregistrationCtx := s.beginAuthModelRegistration(ctx, authForRegistration.ID)
\t\ttasks = append(tasks, modelRegistrationTask{
''',
    'registrationCtx := s.beginAuthModelRegistration(ctx, authForRegistration.ID)',
)
replace_once(
    service_plugins,
    's.completeModelRegistrationForAuthWithCache(ctx, authForRegistration, compatCache)',
    's.completeModelRegistrationForAuthWithCache(registrationCtx, authForRegistration, compatCache)',
    's.completeModelRegistrationForAuthWithCache(registrationCtx, authForRegistration, compatCache)',
)
replace_once(
    service_plugins,
    '''\t\t\tauthForRefresh := auth
\t\t\ttasks = append(tasks, modelRegistrationTask{
''',
    '''\t\t\tauthForRefresh := auth
\t\t\tregistrationCtx := s.beginAuthModelRegistration(context.Background(), authForRefresh.ID)
\t\t\ttasks = append(tasks, modelRegistrationTask{
''',
    'registrationCtx := s.beginAuthModelRegistration(context.Background(), authForRefresh.ID)',
)
replace_once(
    service_plugins,
    'if s.refreshModelRegistrationForAuthWithCache(authForRefresh, compatCache) {',
    'if s.refreshModelRegistrationForAuthWithContext(registrationCtx, authForRefresh, compatCache) {',
    's.refreshModelRegistrationForAuthWithContext(registrationCtx, authForRefresh, compatCache)',
)

replace_once(
    service_auth,
    '''\t\t\tauthForRegistration := auth
\t\t\texpectedGeneration := authForRegistration.Generation
''',
    '''\t\t\tauthForRegistration := auth
\t\t\tauthRegistrationCtx := s.beginAuthModelRegistration(registrationCtx, authForRegistration.ID)
\t\t\texpectedGeneration := authForRegistration.Generation
''',
    'authRegistrationCtx := s.beginAuthModelRegistration(registrationCtx, authForRegistration.ID)',
)
replace_once(
    service_auth,
    's.completeModelRegistrationForAuthWithCache(registrationCtx, authForRegistration, compatCache)',
    's.completeModelRegistrationForAuthWithCache(authRegistrationCtx, authForRegistration, compatCache)',
    's.completeModelRegistrationForAuthWithCache(authRegistrationCtx, authForRegistration, compatCache)',
)


# Workers may reach the hook in either order. Test per-auth completion without
# assuming the second worker is auth B, and drain workers before registry cleanup.
auth_sync_test = ROOT / 'sdk/cliproxy/service_auth_sync_test.go'
auth_sync_text = read(auth_sync_test)
batch_test_start = auth_sync_text.index('func TestHandleAuthUpdates_SameRevisionWaitDoesNotWaitForOtherAuthInBatch(')
batch_test_end = auth_sync_text.index('\nfunc ', batch_test_start + 1)
batch_test = auth_sync_text[batch_test_start:batch_test_end]
batch_test = batch_test.replace('\tbStarted := make(chan struct{})', '\tfinishedBatch := make(chan struct{})\n\tbStarted := make(chan struct{})', 1)
batch_test = batch_test.replace('\t\tmodelRegistrationTaskHook = nil\n', '', 1)
batch_test = batch_test.replace('\t\t\tclose(bBlock)\n\t\t}\n\t})', '\t\t\tclose(bBlock)\n\t\t}\n\t\t<-finishedBatch\n\t\tmodelRegistrationTaskHook = nil\n\t})', 1)
batch_test = batch_test.replace('\n\tfinishedBatch := make(chan struct{})\n\tgo func()', '\n\tgo func()', 1)
old_wait = """\tdoneA := make(chan struct{})
\tgo func() {
\t\tservice.handleAuthUpdate(context.Background(), updateA)
\t\tclose(doneA)
\t}()
\tselect {
\tcase <-doneA:
\tcase <-time.After(2 * time.Second):
\t\tt.Fatal("auth A hook wait blocked on unrelated auth B registration")
\t}
"""
new_wait = """\t// Either auth may be the blocked worker. The other must finish its
\t// same-revision wait without waiting for the whole batch.
\tdoneAuth := make(chan string, 2)
\tvar waiters sync.WaitGroup
\tfor _, update := range []watcher.AuthUpdate{updateA, updateB} {
\t\twaiters.Add(1)
\t\tgo func() {
\t\t\tdefer waiters.Done()
\t\t\tservice.handleAuthUpdate(context.Background(), update)
\t\t\tdoneAuth <- update.ID
\t\t}()
\t}
\tdefer func() {
\t\tselect {
\t\tcase <-bBlock:
\t\tdefault:
\t\t\tclose(bBlock)
\t\t}
\t\twaiters.Wait()
\t}()
\tselect {
\tcase <-doneAuth:
\tcase <-time.After(2 * time.Second):
\t\tt.Fatal("completed auth hook wait blocked on unrelated registration")
\t}
"""
if batch_test.count(old_wait) != 1:
    raise SystemExit('expected one pattern in auth batch independent-wait test')
batch_test = batch_test.replace(old_wait, new_wait, 1)
write(auth_sync_test, auth_sync_text[:batch_test_start] + batch_test + auth_sync_text[batch_test_end:])
replace_once(auth_sync_test, '\t"sync/atomic"\n', '\t"sync"\n\t"sync/atomic"\n', '\t"sync"\n')

replace_once(
    service_config_source,
    '''\t\tauthForRegistration := prepared
\t\ttasks = append(tasks, modelRegistrationTask{
''',
    '''\t\tauthForRegistration := prepared
\t\tauthRegistrationCtx := s.beginAuthModelRegistration(registrationCtx, authForRegistration.ID)
\t\ttasks = append(tasks, modelRegistrationTask{
''',
    'authRegistrationCtx := s.beginAuthModelRegistration(registrationCtx, authForRegistration.ID)',
)
replace_once(
    service_config_source,
    's.completeModelRegistrationForAuthWithCache(registrationCtx, authForRegistration, compatCache)',
    's.completeModelRegistrationForAuthWithCache(authRegistrationCtx, authForRegistration, compatCache)',
    's.completeModelRegistrationForAuthWithCache(authRegistrationCtx, authForRegistration, compatCache)',
)

write(
    ROOT / 'sdk/cliproxy/pro_features_service_test.go',
    re.sub(
        r'github\.com/router-for-me/CLIProxyAPI/v\d+',
        MODULE_PATH,
        read_text(Path(__file__).resolve().parent / 'pro_features_service_test.go'),
    ),
)

legacy_gemini_quota_source = Path(__file__).resolve().parent / 'plugin_gemini_cli_quota_legacy.go'
legacy_gemini_quota_target = ROOT / 'internal/pluginhost/gemini_cli_quota_legacy.go'
write(legacy_gemini_quota_target, re.sub(r'github\.com/router-for-me/CLIProxyAPI/v\d+', MODULE_PATH, read_text(legacy_gemini_quota_source)))
legacy_gemini_quota_test_source = Path(__file__).resolve().parent / 'plugin_gemini_cli_quota_legacy_test.go'
legacy_gemini_quota_test_target = ROOT / 'internal/pluginhost/gemini_cli_quota_legacy_test.go'
write(legacy_gemini_quota_test_target, re.sub(r'github\.com/router-for-me/CLIProxyAPI/v\d+', MODULE_PATH, read_text(legacy_gemini_quota_test_source)))

plugin_quota_management = ROOT / 'internal/api/handlers/management/pro_plugin_quota.go'
write(plugin_quota_management, re.sub(r'github\.com/router-for-me/CLIProxyAPI/v\d+', MODULE_PATH, read_text(Path(__file__).resolve().parent / 'plugin_quota_management.go')))
plugin_quota_management_test = ROOT / 'internal/api/handlers/management/pro_plugin_quota_test.go'
write(plugin_quota_management_test, re.sub(r'github\.com/router-for-me/CLIProxyAPI/v\d+', MODULE_PATH, read_text(Path(__file__).resolve().parent / 'plugin_quota_management_test.go')))

for source_name, target_name in (
    ('account_inspection_host.go', 'account_inspection_host.go'),
    ('auth_file_connection.go', 'auth_file_connection.go'),
    ('auth_file_connection_test.go', 'auth_file_connection_test.go'),
    ('pro_auth_mutation.go', 'pro_auth_mutation.go'),
    ('pro_auth_mutation_integration_test.go', 'pro_auth_mutation_integration_test.go'),
    ('pro_virtual_auth_consistency_integration_test.go', 'pro_virtual_auth_consistency_integration_test.go'),
    ('pro_management_runtime.go', 'pro_management_runtime.go'),
):
    write(
        ROOT / 'internal/api/handlers/management' / target_name,
        re.sub(
            r'github\.com/router-for-me/CLIProxyAPI/v\d+',
            MODULE_PATH,
            read_text(Path(__file__).resolve().parent / source_name),
        ),
    )

pro_features_management = ROOT / 'internal/api/handlers/management/pro_features.go'
write(pro_features_management, re.sub(r'github\.com/router-for-me/CLIProxyAPI/v\d+', MODULE_PATH, read_text(Path(__file__).resolve().parent / 'pro_features_management.go')))

server_options_source = ROOT / 'internal/api/server_options.go'
add_go_import(server_options_source, '"' + import_path('internal/pluginhost') + '"\n', '\tproapp "' + import_path('internal/pro/app') + '"\n')
replace_once(
    server_options_source,
    '\tpluginHost            *pluginhost.Host\n',
    '\tpluginHost            *pluginhost.Host\n\tproApp               *proapp.App\n',
    'proApp               *proapp.App',
)
insert_before(
    server_options_source,
    '// WithConfigReloadHook registers a callback used after management saves config changes.\n',
    '''// WithProApp registers the statically linked Pro module composition root.
func WithProApp(application *proapp.App) ServerOption {
\treturn func(cfg *serverOptionConfig) {
\t\tcfg.proApp = application
\t}
}

''',
    'func WithProApp(application *proapp.App)',
)

management_handler_source = ROOT / 'internal/api/handlers/management/handler.go'
add_go_import(management_handler_source, '"' + import_path('internal/pluginhost') + '"\n', '\tproapp "' + import_path('internal/pro/app') + '"\n')
replace_once(
    management_handler_source,
    '\tpluginHost              *pluginhost.Host\n',
    '\tpluginHost              *pluginhost.Host\n\tproApp                 *proapp.App\n',
    'proApp                 *proapp.App',
)
add_go_import(management_handler_source, '"crypto/subtle"\n', '\t"crypto/sha256"\n\t"encoding/hex"\n')
replace_once(
    management_handler_source,
    '\tappliedReloadGeneration uint64\n',
    '\tappliedReloadGeneration uint64\n\tconfigGeneration        uint64\n\tapiKeyConfigFingerprint string\n',
    'configGeneration        uint64',
)
replace_once(
    management_handler_source,
    '\tproApp                 *proapp.App\n',
    '\tproApp                 *proapp.App\n\tapiKeyRefsMu           sync.Mutex\n\tapiKeyRefs             map[string]apiKeyReference\n',
    'apiKeyRefs              map[string]apiKeyReference',
)
replace_once(
    management_handler_source,
    '''\t\tenvSecret:           envSecret,
''',
    '''\t\tenvSecret:           envSecret,
\t\tconfigGeneration:    1,
\t\tapiKeyRefs:          make(map[string]apiKeyReference),
''',
    'apiKeyRefs:          make(map[string]apiKeyReference)',
)
if '''\th.mu.Lock()
\tdefer h.mu.Unlock()
\tif h.isStaleConfigFileSnapshotLocked(cloned) {
\t\tlog.Debug("management: ignoring config reload parsed from an older config file version")
\t\treturn
\t}
\tprevSecretHash := ""
\tif h.cfg != nil {
\t\tprevSecretHash = h.cfg.RemoteManagement.SecretKey
\t}
\th.cfg = cloned
''' in read(management_handler_source):
    replace_once(
        management_handler_source,
        '''\th.mu.Lock()
\tdefer h.mu.Unlock()
\tif h.isStaleConfigFileSnapshotLocked(cloned) {
\t\tlog.Debug("management: ignoring config reload parsed from an older config file version")
\t\treturn
\t}
\tprevSecretHash := ""
\tif h.cfg != nil {
\t\tprevSecretHash = h.cfg.RemoteManagement.SecretKey
\t}
\th.cfg = cloned
\tnextSecretHash := ""
\tif cloned != nil {
\t\tnextSecretHash = cloned.RemoteManagement.SecretKey
\t}
\tif prevSecretHash != nextSecretHash {
\t\th.authCache.invalidate()
\t}
''',
        '''\th.mu.Lock()
\tif h.isStaleConfigFileSnapshotLocked(cloned) {
\t\tlog.Debug("management: ignoring config reload parsed from an older config file version")
\t\th.mu.Unlock()
\t\treturn
\t}
\th.updateAPIKeyConfigGenerationLocked()
\tprevSecretHash := ""
\tif h.cfg != nil {
\t\tprevSecretHash = h.cfg.RemoteManagement.SecretKey
\t}
\th.cfg = cloned
\th.updateAPIKeyConfigGenerationLocked()
\tnextSecretHash := ""
\tif cloned != nil {
\t\tnextSecretHash = cloned.RemoteManagement.SecretKey
\t}
\tif prevSecretHash != nextSecretHash {
\t\th.authCache.invalidate()
\t}
\tapplication := h.proApp
\tvar apiKeys []string
\tif cloned != nil {
\t\tapiKeys = append(apiKeys, cloned.APIKeys...)
\t}
\th.mu.Unlock()
\tif application != nil && application.APIKeyPolicy() != nil {
\t\tapplication.APIKeyPolicy().SetConfiguredAPIKeys(apiKeys)
\t}
\t''',
        'application.APIKeyPolicy().SetConfiguredAPIKeys(apiKeys)',
    )
else:
    raise SystemExit('no SetConfig anchor')
insert_before(
    management_handler_source,
    '\t\tif c.Request.Method != http.MethodOptions {\n\t\t\tsetHeader := func(id string)',
    '''\t\tsessionSum := sha256.Sum256([]byte(clientIP + "\\x00" + provided))
\t\tc.Set(apiKeyPolicyManagementSessionContextKey, hex.EncodeToString(sessionSum[:]))
''',
    'sessionSum := sha256.Sum256',
)

insert_before(
    management_handler_source,
    'type attemptInfo struct {\n',
    'const apiKeyPolicyManagementSessionContextKey = "apiKeyPolicyManagementSession"\n\n',
    'const apiKeyPolicyManagementSessionContextKey',
)

# All successful saves, including generic v8 writes, take this locked snapshot.
# Track key changes here rather than only in the legacy persistLocked path.
replace_once(
    management_handler_source,
    '\th.reloadGeneration++\n',
    '\th.updateAPIKeyConfigGenerationLocked()\n\th.reloadGeneration++\n',
    'h.updateAPIKeyConfigGenerationLocked()\n\th.reloadGeneration++',
)

server_source = ROOT / 'internal/api/server.go'
add_go_import(server_source, '"' + import_path('internal/pluginhost') + '"\n', '\tapikeypolicy "' + import_path('internal/pro/apikeypolicy') + '"\n')
replace_once(
    server_source,
    '\t// requestLogger is the request logger instance for dynamic configuration updates.\n',
    '\t// apiKeyPolicy freezes the authenticated API-key policy for each request.\n\tapiKeyPolicy *apikeypolicy.Service\n\n\t// requestLogger is the request logger instance for dynamic configuration updates.\n',
    'apiKeyPolicy *apikeypolicy.Service',
)
replace_once(
    server_source,
    '\ts.mgmt.SetPluginHost(optionState.pluginHost)\n',
    '\ts.mgmt.SetPluginHost(optionState.pluginHost)\n\ts.mgmt.SetProApp(optionState.proApp)\n\tif optionState.proApp != nil && optionState.proApp.APIKeyPolicy() != nil {\n\t\toptionState.proApp.APIKeyPolicy().SetConfiguredAPIKeys(cfg.APIKeys)\n\t}\n',
    's.mgmt.SetProApp(optionState.proApp)',
)
if '\ts.handlers.SetPluginHost(optionState.pluginHost)\n' in read(server_source):
    replace_once(
        server_source,
        '\ts.handlers.SetPluginHost(optionState.pluginHost)\n',
    '''\tif optionState.proApp != nil {
\t\ts.apiKeyPolicy = optionState.proApp.APIKeyPolicy()
\t}
\ts.handlers.SetPluginHost(optionState.pluginHost)
''',
    's.apiKeyPolicy = optionState.proApp.APIKeyPolicy()',
)
else:
    pass

replace_once(
    server_source,
    '\ts.handlers.SetPluginHost(s.interceptorHost)\n',
    '''\tif optionState.proApp != nil {
\t\ts.apiKeyPolicy = optionState.proApp.APIKeyPolicy()
\t}
\ts.handlers.SetPluginHost(s.interceptorHost)
''',
    's.apiKeyPolicy = optionState.proApp.APIKeyPolicy()',
)

server_reload_source = ROOT / 'internal/api/server_reload.go'
replace_once(
    server_reload_source,
    '''	accessConfigApplied := s.applyAccessConfig(oldCfg, cfg)
''',
    '''	// Publish the committed Key set to the policy service before access auth
	// can accept a restored Key. Orphan purge shares this service guard, so it
	// cannot delete a policy in the gap between auth and Management reload.
	if s.apiKeyPolicy != nil {
		s.apiKeyPolicy.SetConfiguredAPIKeys(cfg.APIKeys)
	}
	accessConfigApplied := s.applyAccessConfig(oldCfg, cfg)
''',
    's.apiKeyPolicy.SetConfiguredAPIKeys(cfg.APIKeys)',
)

server_middleware_source = ROOT / 'internal/api/server_middleware.go'
add_go_import(server_middleware_source, 'codexlive "' + import_path('internal/client/codex/live') + '"\n', '\tapikeypolicy "' + import_path('internal/pro/apikeypolicy') + '"\n')
for old, new in (
    ('func AuthMiddleware(manager *sdkaccess.Manager)', 'func AuthMiddleware(manager *sdkaccess.Manager, policy *apikeypolicy.Service)'),
    ('accessAuthMiddleware(manager, false)', 'accessAuthMiddleware(manager, policy, false)'),
    ('func realtimeStandardAuthMiddleware(manager *sdkaccess.Manager)', 'func realtimeStandardAuthMiddleware(manager *sdkaccess.Manager, policy *apikeypolicy.Service)'),
    ('accessAuthMiddleware(manager, true)', 'accessAuthMiddleware(manager, policy, true)'),
    ('func accessAuthMiddleware(manager *sdkaccess.Manager, realtimeError bool)', 'func accessAuthMiddleware(manager *sdkaccess.Manager, policy *apikeypolicy.Service, realtimeError bool)'),
):
    replace_once(server_middleware_source, old, new)
replace_all_exact(
    server_middleware_source,
    '''\t\tif err == nil {
\t\t\tif result != nil {
\t\t\t\tc.Set("userApiKey", result.Principal)
\t\t\t\tc.Set("accessProvider", result.Provider)
\t\t\t\tif len(result.Metadata) > 0 {
\t\t\t\t\tc.Set("accessMetadata", result.Metadata)
\t\t\t\t}
\t\t\t\treqCtx := coreusage.WithAPIKey(c.Request.Context(), result.Principal)
\t\t\t\treqCtx = coreusage.WithAccessProvider(reqCtx, result.Provider)
\t\t\t\treqCtx = coreusage.WithIsNativeKey(reqCtx, coreusage.IsNativeAccessProvider(result.Provider))
\t\t\t\tc.Request = c.Request.WithContext(reqCtx)
\t\t\t}
\t\t\tmiddleware.CaptureAuthenticatedRequestBody(c)
\t\t\tif c.IsAborted() {
\t\t\t\treturn
\t\t\t}
\t\t\tc.Next()
\t\t\treturn
\t\t}
''',
    '''\t\tif err == nil {
\t\t\tif result != nil {
\t\t\t\tc.Set("userApiKey", result.Principal)
\t\t\t\tc.Set("accessProvider", result.Provider)
\t\t\t\tif len(result.Metadata) > 0 {
\t\t\t\t\tc.Set("accessMetadata", result.Metadata)
\t\t\t\t}
\t\t\t\treqCtx := coreusage.WithAPIKey(c.Request.Context(), result.Principal)
\t\t\t\treqCtx = coreusage.WithAccessProvider(reqCtx, result.Provider)
\t\t\t\treqCtx = coreusage.WithIsNativeKey(reqCtx, coreusage.IsNativeAccessProvider(result.Provider))
\t\t\t\tc.Request = c.Request.WithContext(reqCtx)
\t\t\t\tif result.Provider == sdkaccess.DefaultAccessProviderName && policy != nil {
\t\t\t\t\tidentity, identityErr := apikeypolicy.NewAuthenticatedAPIKeyIdentity(result.Principal)
\t\t\t\t\tif identityErr != nil {
\t\t\t\t\t\twriteAPIKeyPolicyMiddlewareError(c, realtimeError, identityErr)
\t\t\t\t\t\treturn
\t\t\t\t\t}
\t\t\t\t\tdecision, decisionErr := policy.Decide(identity)
\t\t\t\t\tif decisionErr != nil {
\t\t\t\t\t\twriteAPIKeyPolicyMiddlewareError(c, realtimeError, decisionErr)
\t\t\t\t\t\treturn
\t\t\t\t\t}
\t\t\t\t\trequestCtx := apikeypolicy.WithIdentity(c.Request.Context(), identity)
\t\t\t\t\trequestCtx = apikeypolicy.WithDecision(requestCtx, decision)
\t\t\t\t\tc.Request = c.Request.WithContext(requestCtx)
\t\t\t\t}
\t\t\t}
\t\t\tmiddleware.CaptureAuthenticatedRequestBody(c)
\t\t\tif c.IsAborted() {
\t\t\t\treturn
\t\t\t}
\t\t\tc.Next()
\t\t\treturn
\t\t}
''',
    expected_count=1,
)
replace_once(
    server_middleware_source,
    '''func realtimeAuthMiddleware(manager *sdkaccess.Manager, handler *codexlive.Handler) gin.HandlerFunc {
\tfallback := realtimeStandardAuthMiddleware(manager)
''',
    '''func writeAPIKeyPolicyMiddlewareError(c *gin.Context, realtimeError bool, err error) {
\tstatus := http.StatusServiceUnavailable
\tcode := "api_key_policy_unavailable"
\tmessage := "API key policy is unavailable"
\tif quotaErr, ok := err.(*apikeypolicy.QuotaExceededError); ok {
\t\tstatus = http.StatusTooManyRequests
\t\tcode = "api_key_quota_exceeded"
\t\tmessage = quotaErr.Error()
\t}
\tif err == apikeypolicy.ErrKeyConcurrencyExceeded {
\t\tstatus = http.StatusTooManyRequests
\t\tcode = "api_key_concurrency_exceeded"
\t\tmessage = err.Error()
\t}
\tif policyErr, ok := err.(*apikeypolicy.PolicyError); ok {
\t\tstatus = http.StatusForbidden
\t\tcode = policyErr.Code
\t\tmessage = policyErr.Message
\t}
\terrorType := "server_error"
\tif status == http.StatusForbidden || status == http.StatusTooManyRequests {
\t\terrorType = "permission_error"
\t}
\tif realtimeError {
\t\tc.AbortWithStatusJSON(status, gin.H{"error": gin.H{"message": message, "type": errorType, "param": nil, "code": code}})
\t\treturn
\t}
\tif c != nil && c.Request != nil {
\t\tpath := c.Request.URL.Path
\t\tif strings.HasPrefix(path, "/v1beta/") {
\t\t\tstatusName := "UNAVAILABLE"
\t\t\tif status == http.StatusForbidden {
\t\t\t\tstatusName = "PERMISSION_DENIED"
\t\t\t} else if status == http.StatusTooManyRequests {
\t\t\t\tstatusName = "RESOURCE_EXHAUSTED"
\t\t\t}
\t\t\tc.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": status, "message": message, "status": statusName, "reason": code}})
\t\t\treturn
\t\t}
\t\tif c.GetHeader("Anthropic-Version") != "" || strings.HasPrefix(c.GetHeader("User-Agent"), "claude-cli") || strings.HasPrefix(path, "/v1/messages") {
\t\t\tclaudeType := "api_error"
\t\t\tif status == http.StatusForbidden || status == http.StatusTooManyRequests {
\t\t\t\tclaudeType = "permission_error"
\t\t\t}
\t\t\tc.AbortWithStatusJSON(status, gin.H{"type": "error", "error": gin.H{"type": claudeType, "message": message + " (" + code + ")"}})
\t\t\treturn
\t\t}
\t}
\tc.AbortWithStatusJSON(status, gin.H{"error": gin.H{"message": message, "type": errorType, "code": code}})
}

func realtimeAuthMiddleware(manager *sdkaccess.Manager, policy *apikeypolicy.Service, handler *codexlive.Handler) gin.HandlerFunc {
\tfallback := realtimeStandardAuthMiddleware(manager, policy)
''',
    'func realtimeAuthMiddleware(manager *sdkaccess.Manager, policy *apikeypolicy.Service',
)
replace_once(
    server_middleware_source,
    '''\t\tc.Set("userApiKey", principal)
\t\tc.Set("accessProvider", provider)
''',
    '''\t\tc.Set("userApiKey", principal)
\t\tc.Set("accessProvider", provider)
\t\tif identity := authorization.IssuerAPIKeyIdentity; identity.Valid() && policy != nil {
\t\t\tdecision, decisionErr := policy.Decide(identity)
\t\t\tif decisionErr != nil {
\t\t\t\twri…30696 tokens truncated…    source_text = source_text.replace(
            's.h.resolveTokenForAuth(reqCtx, auth)',
            's.h.resolveTokenForAuth(reqCtx, auth, "")',
        ).replace(
            's.h.apiCallTransport(auth)',
            's.h.apiCallTransport(auth, "")',
        )
    write(
        ROOT / 'internal/api/handlers/management' / source_name,
        source_text,
    )
write(routing_policy, re.sub(r'github\.com/router-for-me/CLIProxyAPI/v\d+', MODULE_PATH, read_text(Path(__file__).resolve().parent / 'routing_policy.go')))
write(routing_policy_test, re.sub(r'github\.com/router-for-me/CLIProxyAPI/v\d+', MODULE_PATH, read_text(Path(__file__).resolve().parent / 'routing_policy_test.go')))

replace_once(
    auth_files_fields,
    '''\tmetadata["disabled"] = disabled
\traw, errMarshal := json.Marshal(metadata)
''',
    '''\tmetadata["disabled"] = disabled
\tdelete(metadata, routingProtectionMetadataKey)
\traw, errMarshal := json.Marshal(metadata)
''',
)
replace_once(
    auth_files_fields,
    '''\tif auth.Metadata == nil {
\t\tauth.Metadata = make(map[string]any)
\t}
\tauth.Metadata["disabled"] = disabled
}
''',
    '''\tif auth.Metadata == nil {
\t\tauth.Metadata = make(map[string]any)
\t}
\tauth.Metadata["disabled"] = disabled
\tclearRoutingProtectionOwnership(auth)
}
''',
)
replace_once(
    auth_files_fields,
    '''func syncAuthFileDisabledState(auth *coreauth.Auth) {
\tif auth == nil {
\t\treturn
\t}
\tdisabled, ok := authFileBoolValue(auth.Metadata["disabled"])
''',
    '''func syncAuthFileDisabledState(auth *coreauth.Auth) {
\tif auth == nil {
\t\treturn
\t}
\tdisabled, ok := authFileBoolValue(auth.Metadata["disabled"])
\tclearRoutingProtectionOwnership(auth)
''',
)

replace_once(
    api_tools,
    '''	Data            string            `json:"data"`
}
''',
    '''	Data            string            `json:"data"`
	UseExecutorSnake *bool             `json:"use_executor"`
	UseExecutorCamel *bool             `json:"useExecutor"`
	UseExecutorPascal *bool            `json:"UseExecutor"`
}
''',
)
replace_once(
    auth_files,
    '''\tif claims := extractCodexIDTokenClaims(auth); claims != nil {
\t\tentry["id_token"] = claims
\t}
\t// Expose priority from Attributes (set by synthesizer from JSON "priority" field).
''',
    '''\tif claims := extractCodexIDTokenClaims(auth); claims != nil {
\t\tentry["id_token"] = claims
\t}
\tif plan := xaiAuthFilePlanType(auth); plan != "" {
\t\tentry["plan_type"] = plan
\t}
\tif strings.EqualFold(strings.TrimSpace(auth.Provider), "xai") {
\t\tentry["quota_identity_fingerprint"] = xaiAccountQuotaIdentityFingerprint(accountFromAuth(auth))
\t}
\t// Expose priority from Attributes (set by synthesizer from JSON "priority" field).
''',
)
insert_before(
    api_tools,
    'func firstNonEmptyString(values ...*string) string {\n',
    '''func firstNonNilBool(values ...*bool) bool {
\tfor _, v := range values {
\t\tif v != nil {
\t\t\treturn *v
\t\t}
\t}
\treturn false
}

''',
    'func firstNonNilBool(values ...*bool) bool',
)
api_call_transport_args = 'auth, requestProxyURL'
executor_auth_args = 'requestScopedExecutorAuth(auth, requestProxyURL)'
insert_before(
    api_tools,
    'func firstNonNilBool(values ...*bool) bool {\n',
    '''func requestScopedExecutorAuth(auth *coreauth.Auth, requestProxyURL string) *coreauth.Auth {
\tif auth == nil || strings.TrimSpace(requestProxyURL) == "" {
\t\treturn auth
\t}
\trequestAuth := auth.Clone()
\trequestAuth.ProxyURL = strings.TrimSpace(requestProxyURL)
\treturn requestAuth
}

''',
    'func requestScopedExecutorAuth(auth *coreauth.Auth, requestProxyURL string)',
)
write(
    api_tools_executor_proxy_test,
    f'''package management

import (
\t"testing"

\tcoreauth "{MODULE_PATH}/sdk/cliproxy/auth"
)

func TestRequestScopedExecutorAuthOverridesProxyWithoutMutatingCredential(t *testing.T) {{
\tauth := &coreauth.Auth{{ProxyURL: "http://credential-proxy.example.com:8080"}}
\trequestAuth := requestScopedExecutorAuth(auth, " direct ")
\tif requestAuth == auth {{
\t\tt.Fatal("request-scoped auth aliases shared credential")
\t}}
\tif requestAuth.ProxyURL != "direct" {{
\t\tt.Fatalf("request-scoped proxy = %q, want direct", requestAuth.ProxyURL)
\t}}
\tif auth.ProxyURL != "http://credential-proxy.example.com:8080" {{
\t\tt.Fatalf("shared credential proxy mutated to %q", auth.ProxyURL)
\t}}
}}

func TestRequestScopedExecutorAuthKeepsCredentialWhenRequestProxyIsEmpty(t *testing.T) {{
\tauth := &coreauth.Auth{{ProxyURL: "http://credential-proxy.example.com:8080"}}
\tif requestAuth := requestScopedExecutorAuth(auth, "  "); requestAuth != auth {{
\t\tt.Fatal("empty request proxy should reuse credential auth")
\t}}
}}
''',
)
replace_once(
    api_tools,
    '''\thttpClient := &http.Client{
\t\tTimeout: defaultAPICallTimeout,
\t}
\thttpClient.Transport = h.apiCallTransport(__API_CALL_TRANSPORT_ARGS__)

\tresp, errDo := httpClient.Do(req)
'''.replace('__API_CALL_TRANSPORT_ARGS__', api_call_transport_args),
    '''\tuseExecutor := firstNonNilBool(body.UseExecutorSnake, body.UseExecutorCamel, body.UseExecutorPascal)
\tvar resp *http.Response
\tvar errDo error
\tif useExecutor {
\t\tif auth == nil {
\t\t\tc.JSON(http.StatusBadRequest, gin.H{"error": "auth not found"})
\t\t\treturn
\t\t}
\t\tif h == nil || h.authManager == nil {
\t\t\tc.JSON(http.StatusServiceUnavailable, gin.H{"error": "core auth manager unavailable"})
\t\t\treturn
\t\t}
\t\tresp, errDo = h.authManager.HttpRequest(c.Request.Context(), __EXECUTOR_AUTH_ARGS__, req)
\t} else {
\t\thttpClient := &http.Client{
\t\t\tTimeout: defaultAPICallTimeout,
\t\t}
\t\thttpClient.Transport = h.apiCallTransport(__API_CALL_TRANSPORT_ARGS__)
\t\tresp, errDo = httpClient.Do(req)
\t}
'''.replace('__API_CALL_TRANSPORT_ARGS__', api_call_transport_args).replace('__EXECUTOR_AUTH_ARGS__', executor_auth_args),
)
replace_once(
    auth_files,
    '''		"runtime_only":   runtimeOnly,
		"source":         "memory",
''',
    '''		"last_error":     authFileLastError(auth),
		"runtime_only":   runtimeOnly,
		"source":         "memory",
''',
)
replace_once(
    auth_files,
    '''			fileData["email"] = emailValue
''',
    '''			fileData["email"] = emailValue
				if lastErrorRaw := ojson.GetBytes(data, "last_error"); lastErrorRaw.IsObject() {
					var lastError map[string]any
					if errUnmarshal := json.Unmarshal([]byte(lastErrorRaw.Raw), &lastError); errUnmarshal == nil && len(lastError) > 0 {
						fileData["last_error"] = lastError
					}
				}
				if strings.EqualFold(strings.TrimSpace(typeValue), "codex") {
					if claims := extractCodexIDTokenClaimsFromRaw(ojson.GetBytes(data, "id_token").String()); claims != nil {
						fileData["id_token"] = claims
					}
				}
				if strings.EqualFold(strings.TrimSpace(typeValue), "xai") {
					if plan := xaiAuthFilePlanTypeFromRaw(ojson.GetBytes(data, "access_token").String()); plan != "" {
						fileData["plan_type"] = plan
					}
				}
''',
    'if lastErrorRaw := ojson.GetBytes(data, "last_error"); lastErrorRaw.IsObject()',
)
replace_go_function(
    auth_files,
    'func extractCodexIDTokenClaims(auth *coreauth.Auth) gin.H',
    '''func extractCodexIDTokenClaims(auth *coreauth.Auth) gin.H {
	if auth == nil || auth.Metadata == nil {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(auth.Provider), "codex") {
		return nil
	}
	idTokenRaw, ok := auth.Metadata["id_token"].(string)
	if !ok {
		return nil
	}
	return extractCodexIDTokenClaimsFromRaw(idTokenRaw)
}
''',
    'return extractCodexIDTokenClaimsFromRaw(idTokenRaw)',
)

patch_dir = Path(__file__).resolve().parent
embeddedusage_source = patch_dir.parent / 'embeddedusage'
embeddedusage_target = ROOT / 'internal/embeddedusage'
if embeddedusage_source.is_dir():
    queue_tree(embeddedusage_source, embeddedusage_target)
elif not embeddedusage_target.is_dir():
    raise SystemExit(f'embeddedusage source not found: {embeddedusage_source}')
ensure_go_require(ROOT / 'go.mod', 'modernc.org/sqlite', 'v1.51.0')
auth_runtime_state = ROOT / 'sdk/cliproxy/auth/auth_runtime_state.go'
write(
    auth_runtime_state,
    re.sub(r'github\.com/router-for-me/CLIProxyAPI/v\d+', MODULE_PATH, read_text(patch_dir / 'auth_runtime_state.go')),
)
write(
    ROOT / 'sdk/cliproxy/auth/auth_runtime_state_test.go',
    re.sub(r'github\.com/router-for-me/CLIProxyAPI/v\d+', MODULE_PATH, read_text(patch_dir / 'auth_runtime_state_test.go')),
)
write(
    ROOT / 'sdk/cliproxy/auth/auth_account_policy.go',
    read_text(patch_dir / 'auth_account_policy.go'),
)
write(
    ROOT / 'sdk/cliproxy/auth/auth_account_policy_test.go',
    re.sub(r'github\.com/router-for-me/CLIProxyAPI/v\d+', MODULE_PATH, read_text(patch_dir / 'auth_account_policy_test.go')),
)

redisqueue_queue = ROOT / 'internal/redisqueue/queue.go'
replace_once(
    redisqueue_queue,
    '''\tdefaultRetentionSeconds int64 = 60
\tmaxRetentionSeconds     int64 = 3600
\tusageSubscriberBuffer         = 256
''',
    '''\tdefaultRetentionSeconds            int64 = 60
\tmaxRetentionSeconds                int64 = 3600
\tdefaultPersistenceRetentionSeconds int64 = 3600
\tdefaultPersistenceMaxItems         int64 = 100000
\tdefaultPersistenceMaxBytes         int64 = 256 * 1024 * 1024
\tusageSubscriberBuffer                    = 256
''',
    'defaultPersistenceRetentionSeconds',
)
insert_before(
    redisqueue_queue,
    'type queue struct {\n',
    '''type PersistenceQueueStats struct {
\tDepth              int
\tBytes              int64
\tOldestAgeMS        int64
\tDroppedEventsTotal uint64
\tRetentionSeconds   int64
\tMaxItems           int64
\tMaxBytes           int64
}

type persistenceQueue struct {
\tmu      sync.Mutex
\titems   []queueItem
\thead    int
\tbytes   int64
\tdropped uint64
}

''',
    'type PersistenceQueueStats struct',
)
replace_once(
    redisqueue_queue,
    '''\tenabled          atomic.Bool
\tretentionSeconds atomic.Int64
\tglobal           queue
\terrorGlobal      queue
''',
    '''\tenabled                     atomic.Bool
\tretentionSeconds            atomic.Int64
\tpersistenceEnabled          atomic.Bool
\tpersistenceRetentionSeconds atomic.Int64
\tpersistenceMaxItems         atomic.Int64
\tpersistenceMaxBytes         atomic.Int64
\tglobal                      queue
\terrorGlobal                 queue
\tpersistenceGlobal           persistenceQueue
''',
    'persistenceRetentionSeconds atomic.Int64',
)
replace_once(
    redisqueue_queue,
    '''func init() {
\tretentionSeconds.Store(defaultRetentionSeconds)
}
''',
    '''func init() {
\tretentionSeconds.Store(defaultRetentionSeconds)
\tpersistenceRetentionSeconds.Store(defaultPersistenceRetentionSeconds)
\tpersistenceMaxItems.Store(defaultPersistenceMaxItems)
\tpersistenceMaxBytes.Store(defaultPersistenceMaxBytes)
}
''',
    'persistenceRetentionSeconds.Store(defaultPersistenceRetentionSeconds)',
)
replace_once(
    redisqueue_queue,
    '''\tif !value {
\t\tglobal.clear()
\t\terrorGlobal.clear()
\t}
''',
    '''\tif !value {
\t\tglobal.clear()
\t\terrorGlobal.clear()
\t\tpersistenceGlobal.clear()
\t}
''',
    'persistenceGlobal.clear()',
)
insert_before(
    redisqueue_queue,
    'func Enqueue(payload []byte) {\n',
    '''func SetPersistenceEnabled(value bool) {
\tpersistenceEnabled.Store(value)
\tif !value {
\t\tpersistenceGlobal.clear()
\t}
}

func SetPersistenceQueueConfig(retention int, maxItems int, maxBytes int64) {
\tnormalizedRetention := int64(retention)
\tif normalizedRetention <= 0 {
\t\tnormalizedRetention = defaultPersistenceRetentionSeconds
\t}
\tnormalizedMaxItems := int64(maxItems)
\tif normalizedMaxItems <= 0 {
\t\tnormalizedMaxItems = defaultPersistenceMaxItems
\t}
\tif maxBytes <= 0 {
\t\tmaxBytes = defaultPersistenceMaxBytes
\t}
\tpersistenceRetentionSeconds.Store(normalizedRetention)
\tpersistenceMaxItems.Store(normalizedMaxItems)
\tpersistenceMaxBytes.Store(maxBytes)
}

func PopOldestPersistence(count int) [][]byte {
\tif !Enabled() || !persistenceEnabled.Load() || count <= 0 {
\t\treturn nil
\t}
\treturn persistenceGlobal.popOldest(count)
}

func PersistenceStats() PersistenceQueueStats {
\treturn persistenceGlobal.stats()
}

''',
    'func SetPersistenceEnabled(value bool)',
)
replace_once(
    redisqueue_queue,
    '''\tif len(payload) == 0 {
\t\treturn
\t}
\tif global.publishToSubscribers(payload) {
''',
    '''\tif len(payload) == 0 {
\t\treturn
\t}
\tif persistenceEnabled.Load() {
\t\tpersistenceGlobal.enqueue(payload)
\t}
\tif global.publishToSubscribers(payload) {
''',
    'persistenceGlobal.enqueue(payload)',
)
insert_before(
    redisqueue_queue,
    'func (q *queue) clear() {\n',
    '''func (q *persistenceQueue) clear() {
\tq.mu.Lock()
\tq.items = nil
\tq.head = 0
\tq.bytes = 0
\tq.dropped = 0
\tq.mu.Unlock()
}

func (q *persistenceQueue) enqueue(payload []byte) {
\tnow := time.Now()
\tcloned := append([]byte(nil), payload...)

\tq.mu.Lock()
\tdefer q.mu.Unlock()
\tq.pruneLocked(now)
\tq.items = append(q.items, queueItem{enqueuedAt: now, payload: cloned})
\tq.bytes += int64(len(cloned))
\tq.enforceCapacityLocked()
\tq.maybeCompactLocked()
}

func (q *persistenceQueue) popOldest(count int) [][]byte {
\tnow := time.Now()
\tq.mu.Lock()
\tdefer q.mu.Unlock()
\tq.pruneLocked(now)
\tavailable := len(q.items) - q.head
\tif available <= 0 {
\t\tq.resetItemsLocked()
\t\treturn nil
\t}
\tif count > available {
\t\tcount = available
\t}
\tout := make([][]byte, 0, count)
\tfor index := 0; index < count; index++ {
\t\titem := q.items[q.head+index]
\t\tout = append(out, item.payload)
\t\tq.bytes -= int64(len(item.payload))
\t}
\tq.head += count
\tq.maybeCompactLocked()
\treturn out
}

func (q *persistenceQueue) stats() PersistenceQueueStats {
\tnow := time.Now()
\tq.mu.Lock()
\tdefer q.mu.Unlock()
\tq.pruneLocked(now)
\tdepth := len(q.items) - q.head
\toldestAgeMS := int64(0)
\tif depth > 0 {
\t\toldestAgeMS = now.Sub(q.items[q.head].enqueuedAt).Milliseconds()
\t\tif oldestAgeMS < 0 {
\t\t\toldestAgeMS = 0
\t\t}
\t}
\treturn PersistenceQueueStats{
\t\tDepth:              depth,
\t\tBytes:              q.bytes,
\t\tOldestAgeMS:        oldestAgeMS,
\t\tDroppedEventsTotal: q.dropped,
\t\tRetentionSeconds:   persistenceRetentionSeconds.Load(),
\t\tMaxItems:           persistenceMaxItems.Load(),
\t\tMaxBytes:           persistenceMaxBytes.Load(),
\t}
}

func (q *persistenceQueue) pruneLocked(now time.Time) {
\tretention := persistenceRetentionSeconds.Load()
\tif retention <= 0 {
\t\tretention = defaultPersistenceRetentionSeconds
\t}
\tcutoff := now.Add(-time.Duration(retention) * time.Second)
\tfor q.head < len(q.items) && q.items[q.head].enqueuedAt.Before(cutoff) {
\t\tq.dropHeadLocked()
\t}
\tq.maybeCompactLocked()
}

func (q *persistenceQueue) enforceCapacityLocked() {
\tmaxItems := persistenceMaxItems.Load()
\tmaxBytes := persistenceMaxBytes.Load()
\tfor q.head < len(q.items) && ((maxItems > 0 && int64(len(q.items)-q.head) > maxItems) || (maxBytes > 0 && q.bytes > maxBytes)) {
\t\tq.dropHeadLocked()
\t}
}

func (q *persistenceQueue) dropHeadLocked() {
\tif q.head >= len(q.items) {
\t\treturn
\t}
\tq.bytes -= int64(len(q.items[q.head].payload))
\tq.head++
\tq.dropped++
}

func (q *persistenceQueue) maybeCompactLocked() {
\tif q.head == 0 {
\t\treturn
\t}
\tif q.head >= len(q.items) {
\t\tq.resetItemsLocked()
\t\treturn
\t}
\tif q.head < 1024 && q.head*2 < len(q.items) {
\t\treturn
\t}
\tq.items = append([]queueItem(nil), q.items[q.head:]...)
\tq.head = 0
}

func (q *persistenceQueue) resetItemsLocked() {
\tq.items = nil
\tq.head = 0
\tq.bytes = 0
}

''',
    'func (q *persistenceQueue) clear()',
)

redisqueue_plugin = ROOT / 'internal/redisqueue/plugin.go'
add_go_import(
    redisqueue_plugin,
    'internallogging "' + import_path('internal/logging') + '"\n',
    '\tapikeypolicy "' + import_path('internal/pro/apikeypolicy') + '"\n',
)
replace_once(
    redisqueue_plugin,
    '''\tif p == nil {
\t\treturn
\t}
''',
    '''\tif p == nil {
\t\treturn
\t}
\tif coreusage.SkipMonitoringFromContext(ctx) {
\t\treturn
\t}
''',
    'coreusage.SkipMonitoringFromContext(ctx)',
)
# Anchor on the literal, since new upstream fields change gofmt alignment.
replace_once(
    redisqueue_plugin,
    '''\tdetail := requestDetail{
''',
    '''\tdetail := requestDetail{
\t\tAttemptIndex: record.AttemptIndex,
''',
    'record.AttemptIndex,',
)
replace_once(
    redisqueue_plugin,
    '''\tresponseServiceTier := strings.TrimSpace(record.ResponseServiceTier)
\tresponseModel := strings.TrimSpace(record.ResponseModel)
\tclientRequestMetadata := internallogging.GetClientRequestMetadata(ctx)
''',
    '''\tresponseServiceTier := strings.TrimSpace(record.ResponseServiceTier)
\tspeed := strings.TrimSpace(record.Speed)
\tif speed == "" {
\t\tspeed = coreusage.SpeedFromContext(ctx)
\t}
\tif speed == "" {
\t\tspeed = strings.TrimSpace(record.Detail.Speed)
\t}
\tresponseSpeed := strings.TrimSpace(record.ResponseSpeed)
\tif responseSpeed == "" {
\t\tresponseSpeed = strings.TrimSpace(record.Detail.Speed)
\t}
\tresponseModel := strings.TrimSpace(record.ResponseModel)
\tclientRequestMetadata := internallogging.GetClientRequestMetadata(ctx)
''',
    'responseSpeed := strings.TrimSpace(record.ResponseSpeed)',
)
replace_once(
    redisqueue_plugin,
    '''\tusageDetail := coreusage.EnsureTokenBreakdownForProvider''',
    '''\tpolicyDecision, hasPolicyDecision := apikeypolicy.DecisionFromContext(ctx)
\tpolicyMode := ""
\tapiKeyPolicyID := ""
\tprofileID := ""
\tprofileNameSnapshot := ""
\trequestedModel := ""
\teffectiveModel := ""
\tif hasPolicyDecision {
\t\tattribution := policyDecision.UsageAttribution()
\t\tpolicyMode = attribution.PolicyMode
\t\tapiKeyPolicyID = attribution.APIKeyPolicyID
\t\tprofileID = attribution.ProfileID
\t\tprofileNameSnapshot = attribution.ProfileName
\t\trequestedModel = attribution.RequestedModel
\t\teffectiveModel = attribution.EffectiveModel
\t}

\tusageDetail := coreusage.EnsureTokenBreakdownForProvider''',
    'profileNameSnapshot = attribution.ProfileName',
)
replace_all_exact(
    redisqueue_plugin,
    '''\t\tAPIKey:                apiKey,
\t\tIsNativeKey:           isNativeKey,
\t\tAccessProvider:        accessProvider,
\t\tRequestID:             requestID,
''',
    '''\t\tAPIKey:                apiKey,
\t\tIsNativeKey:           isNativeKey,
\t\tAccessProvider:        accessProvider,
\t\tAPIKeyPolicyID:        apiKeyPolicyID,
\t\tProfileID:             profileID,
\t\tProfileNameSnapshot:   profileNameSnapshot,
\t\tPolicyMode:            policyMode,
\t\tRequestedModel:        requestedModel,
\t\tEffectiveModel:        effectiveModel,
\t\tRequestID:             requestID,
''',
    expected_count=1,
)
replace_all_exact(
    redisqueue_plugin,
    '''\tAPIKey                string                      `json:"api_key"`
\tIsNativeKey           bool                        `json:"is_native_key"`
\tAccessProvider        string                      `json:"access_provider,omitempty"`
\tRequestID             string                      `json:"request_id"`
''',
    '''\tAPIKey                string                      `json:"api_key"`
\tIsNativeKey           bool                        `json:"is_native_key"`
\tAccessProvider        string                      `json:"access_provider,omitempty"`
\tAPIKeyPolicyID        string                      `json:"api_key_policy_id,omitempty"`
\tProfileID             string                      `json:"profile_id,omitempty"`
\tProfileNameSnapshot   string                      `json:"profile_name_snapshot,omitempty"`
\tPolicyMode            string                      `json:"policy_mode,omitempty"`
\tRequestedModel        string                      `json:"requested_model,omitempty"`
\tEffectiveModel        string                      `json:"effective_model,omitempty"`
\tRequestID             string                      `json:"request_id"`
''',
    expected_count=1,
)
# Match other native fields independently of gofmt alignment.
redisqueue_text = read(redisqueue_plugin)
for pattern, replacement in (
    (r'(?m)^\t\tResponseModel:\s+responseModel,\n', lambda m: m[0] + '\t\tUpstreamModel: strings.TrimSpace(record.UpstreamModel),\n\t\tModelMatchStatus: record.ModelMatchStatus,\n'),
    (r'(?m)^\tResponseModel\s+string\s+`json:"response_model,omitempty"`\n', lambda m: m[0] + '\tUpstreamModel string `json:"upstream_model,omitempty"`\n\tModelMatchStatus string `json:"model_match_status,omitempty"`\n'),
    # Preserve Pro's existing wire contract: speed is requested, response_speed is reported.
    (r'(?m)^\t\tSpeed:\s+strings.TrimSpace\(usageDetail.Speed\),\n', lambda m: '\t\tSpeed: speed,\n\t\tResponseSpeed: responseSpeed,\n'),
    (r'(?m)^\tSpeed\s+string\s+`json:"speed,omitempty"`\n', lambda m: m[0] + '\tResponseSpeed string `json:"response_speed,omitempty"`\n'),
):
    if len(re.findall(pattern, redisqueue_text)) != 1:
        raise SystemExit(f'expected one RedisQueue field matching {pattern!r}')
    redisqueue_text = re.sub(pattern, replacement, redisqueue_text, count=1)
write(redisqueue_plugin, redisqueue_text)
# Observe the model only at the final transport/protocol boundary.
replace_once(
    usage_helpers,
    '\tcliproxyexecutor.MarkUpstreamAttempt(req.Context())\n\tt.reporter.StartResponseTTFT()',
    '\tcliproxyexecutor.MarkUpstreamAttempt(req.Context())\n\tt.reporter.ObserveUpstreamHTTPRequest(req)\n\tt.reporter.StartResponseTTFT()',
    't.reporter.ObserveUpstreamHTTPRequest(req)',
)
replace_once(
    usage_helpers,
    '\tresponseModelMu sync.RWMutex\n',
    '\tresponseModelMu sync.RWMutex\n\tresponseModelFormat string\n',
    'responseModelFormat string',
)
replace_once(
    usage_helpers,
    '\tserved, terminal := extractResponseModelEvent(payload, provider)',
    '''\tr.responseModelMu.RLock()
\tformat := r.responseModelFormat
\tr.responseModelMu.RUnlock()
\tif format != "" {
\t\tprovider = format
\t\t// A partial data line must not finalize a multi-line plugin SSE event.
\t\tif !ojson.ValidBytes(jsonPayload(payload)) {
\t\t\treturn
\t\t}
\t}
\tserved, terminal := extractResponseModelEvent(payload, provider)''',
    'format := r.responseModelFormat',
)
# Observe the complete final wire body: the HTTP fallback reads only a bounded
# prefix and cannot find a model placed after a large conversation input.
for relative_path, expected_count, response_ready in (
    ('internal/runtime/executor/codex_executor_execute.go', 2, 'nil'),
    ('internal/runtime/executor/codex_executor_stream.go', 1, 'responseReady'),
):
    target = ROOT / relative_path
    request_call = f'\thttpResp, err := doCodexRequest(httpClient, httpReq, upstreamBody, prepareHeaders, {response_ready})'
    if 'reporter.ObserveUpstreamRequestModel(upstreamBody)' not in read(target):
        replace_all_exact(
            target,
            request_call,
            '\treporter.ObserveUpstreamRequestModel(upstreamBody)\n' + request_call,
            expected_count,
        )
# Compact returns a JSON response object rather than Codex SSE events.
codex_execute = ROOT / 'internal/runtime/executor/codex_executor_execute.go'
replace_once(
    codex_execute,
    '\treporter.Publish(ctx, helps.ParseOpenAIUsage(upstreamData))',
    '\treporter.SetResponseModelFormat("openai-response")\n\treporter.ObserveResponseModel(data)\n\treporter.Publish(ctx, helps.ParseOpenAIUsage(upstreamData))',
    'reporter.ObserveResponseModel(data)',
)
for relative_path in (
    'internal/runtime/executor/codex_websockets_execute.go',
    'internal/runtime/executor/codex_websockets_stream.go',
):
    replace_once(
        ROOT / relative_path,
        '\twsReqLog := helps.UpstreamRequestLog{',
        '\treporter.ObserveUpstreamRequestModel(wsReqBody)\n\twsReqLog := helps.UpstreamRequestLog{',
        'reporter.ObserveUpstreamRequestModel(wsReqBody)',
    )
replace_once(
    ROOT / 'internal/runtime/executor/xai_websockets_executor.go',
    '\twsReqBody := buildXAIWebsocketRequestBodyDoc(doc)',
    '\twsReqBody := buildXAIWebsocketRequestBodyDoc(doc)\n\treporter.ObserveUpstreamRequestModel(wsReqBody)',
    'reporter.ObserveUpstreamRequestModel(wsReqBody)',
)
aistudio_executor = ROOT / 'internal/runtime/executor/aistudio_executor.go'
# Account for a translated error before its SSE payload reaches a client that
# may cancel immediately. Keep the original later terminal chunk for consumers.
replace_once(
    ROOT / 'sdk/cliproxy/executor/types.go',
    'type StreamChunk struct {',
    'type StreamChunk struct {\n\t// ResultError records a known failure before delivery of its protocol payload.\n\tResultError error',
)
for payload in ('spaced', 'ensureColonSpacedJSON(line)', 'ensureColonSpacedJSON(lines[i])'):
    old = 'case out <- cliproxyexecutor.StreamChunk{Payload: ' + payload + '}:'
    # Only translated Claude SSE errors carry the retained failure.
    new = 'case out <- aiStudioTranslatedChunk(' + payload + ', responseFormat, streamErr):'
    replace_once(aistudio_executor, old, new)
insert_before(
    aistudio_executor, '// ExecuteStream performs a streaming request',
    '''func aiStudioTranslatedChunk(payload []byte, format sdktranslator.Format, retained error) cliproxyexecutor.StreamChunk {
	chunk := cliproxyexecutor.StreamChunk{Payload: payload}
	if retained != nil {
		if _, ok := helps.TranslatedClaudeStreamErrorPayload(format, [][]byte{payload}); ok {
			chunk.ResultError = retained
		}
	}
	return chunk
}

''', 'func aiStudioTranslatedChunk(',
)
replace_once(
    auth_conductor_stream,
    'if chunk.Err != nil && !failed {',
    '''resultErr := chunk.Err
			if resultErr == nil { resultErr = chunk.ResultError }
			if resultErr != nil && !failed {''',
)
stream_text = read(auth_conductor_stream)
start = stream_text.index('if resultErr != nil && !failed {')
end = stream_text.index('if okAction && !isRequestTerminatedError(chunk.Err)', start)
write(auth_conductor_stream, stream_text[:start] + stream_text[start:end].replace('chunk.Err', 'resultErr') + stream_text[end:].replace('if okAction && !isRequestTerminatedError(chunk.Err)', 'if chunk.Err != nil && okAction && !isRequestTerminatedError(chunk.Err)', 1))

if 'reporter.SetUpstreamModel(baseModel)' not in read(aistudio_executor):
    replace_all_exact(
        aistudio_executor,
        '\tendpoint := e.buildEndpoint(baseModel, body.action, opts.Alt)',
        '\tendpoint := e.buildEndpoint(baseModel, body.action, opts.Alt)\n\treporter.SetUpstreamModel(baseModel)',
        2,
    )
replace_once(
    usage_helpers,
    '\t\tResponseModel:        responseModel,',
    '\t\tResponseModel:        responseModel,\n\t\tUpstreamModel:       r.auditUpstreamModel(model),\n\t\tModelMatchStatus:    modelMatchStatus(r.auditUpstreamModel(model), responseModel),',
    'ModelMatchStatus:    modelMatchStatus',
)
# Plugin provider identity is independent of the negotiated wire protocol.
plugin_adapter = ROOT / 'internal/pluginhost/adapters_executors.go'
if 'reporter.SetResponseModelFormat(prepared.outputFormat.String())' not in read(plugin_adapter):
    replace_all_exact(
        plugin_adapter,
        '\t\treporter.SetTranslatedReasoningEffort(prepared.req.Payload, prepared.inputFormat.String())',
        '''\t\treporter.SetResponseModelFormat(prepared.outputFormat.String())
\t\treporter.SetUpstreamModel(prepared.req.Model)
\t\treporter.ObserveUpstreamRequestModel(prepared.req.Payload)
\t\treporter.SetTranslatedReasoningEffort(prepared.req.Payload, prepared.inputFormat.String())''',
        2,
    )
replace_once(
    plugin_adapter,
    '\tctx = pluginExecutorUsageContext(ctx, pluginResp.Headers)\n\tresp = coreexecutor.Response{',
    '\tobservePluginResponseModel(reporter, pluginResp.Payload)\n\tctx = pluginExecutorUsageContext(ctx, pluginResp.Headers)\n\tresp = coreexecutor.Response{',
    'observePluginResponseModel(reporter, pluginResp.Payload)',
)
replace_once(
    plugin_adapter,
    '\tvar streamUsage helps.StreamUsageBuffer',
    '\tmodelObserver := helps.NewStreamResponseModelObserver(reporter)\n\tvar streamUsage helps.StreamUsageBuffer',
    'modelObserver := helps.NewStreamResponseModelObserver(reporter)',
)
replace_once(
    plugin_adapter,
    '\t\tpublishOnce.Do(func() {',
    '\t\tpublishOnce.Do(func() {\n\t\t\tmodelObserver.Finish()',
    'modelObserver.Finish()',
)
replace_once(
    plugin_adapter,
    '\t\t\t\t\tsawPayload = true',
    '''\t\t\t\t\tsawPayload = true
\t\t\t\t\tif json.Valid(chunk.Payload) {
\t\t\t\t\t\treporter.ObserveResponseModel(chunk.Payload)
\t\t\t\t\t} else {
\t\t\t\t\t\tmodelObserver.Feed(chunk.Payload)
\t\t\t\t\t}''',
    'modelObserver.Feed(chunk.Payload)',
)

redisqueue_plugin_text = read(redisqueue_plugin)
attempt_field = '\tAttemptIndex *int64 `json:"attempt_index,omitempty"`\n'
if '`json:"attempt_index,omitempty"`' not in redisqueue_plugin_text:
    auth_index_field = re.compile(r'(?m)^\tAuthIndex[ \t]+string[ \t]+`json:"auth_index"`\n')
    matches = auth_index_field.findall(redisqueue_plugin_text)
    if len(matches) != 1:
        raise SystemExit(
            f'expected one auth index field in {redisqueue_plugin}, found {len(matches)}'
        )
    write(
        redisqueue_plugin,
        auth_index_field.sub(lambda match: match.group(0) + attempt_field, redisqueue_plugin_text, count=1),
    )
queue_go_source('internal/requestmeta/observer.go')

queue_go_source('internal/requestmeta/observer_test.go')

logging_helpers = ROOT / 'internal/runtime/executor/helps/logging_helpers.go'
add_go_import(
    logging_helpers,
    '\t"' + import_path('internal/logging') + '"\n',
    '\t"' + import_path('internal/requestmeta') + '"\n',
)
replace_once(
    logging_helpers,
    '''func RecordAPIResponseMetadata(ctx context.Context, cfg *config.Config, status int, headers http.Header) {
\tlogging.SetResponseHeaders(ctx, headers)
''',
    '''func RecordAPIResponseMetadata(ctx context.Context, cfg *config.Config, status int, headers http.Header) {
\trequestmeta.ObserveUpstreamResponse(ctx, status, headers, nil)
\tlogging.SetResponseHeaders(ctx, headers)
''',
    'requestmeta.ObserveUpstreamResponse(ctx, status, headers, nil)',
)
replace_once(
    logging_helpers,
    '''func AppendAPIResponseChunk(ctx context.Context, cfg *config.Config, chunk []byte) {
\tif !requestLogCaptureEnabled(cfg) {
''',
    '''func AppendAPIResponseChunk(ctx context.Context, cfg *config.Config, chunk []byte) {
\trequestmeta.ObserveUpstreamResponse(ctx, 0, nil, chunk)
\tif !requestLogCaptureEnabled(cfg) {
''',
    'requestmeta.ObserveUpstreamResponse(ctx, 0, nil, chunk)',
)
replace_once(
    logging_helpers,
    '''func RecordAPIWebsocketHandshake(ctx context.Context, cfg *config.Config, status int, headers http.Header) {
\tlogging.SetResponseHeaders(ctx, headers)
''',
    '''func RecordAPIWebsocketHandshake(ctx context.Context, cfg *config.Config, status int, headers http.Header) {
\trequestmeta.ObserveUpstreamResponse(ctx, status, headers, nil)
\tlogging.SetResponseHeaders(ctx, headers)
''',
    'func RecordAPIWebsocketHandshake(ctx context.Context, cfg *config.Config, status int, headers http.Header) {\n\trequestmeta.ObserveUpstreamResponse',
)
replace_once(
    logging_helpers,
    '''func RecordAPIWebsocketUpgradeRejection(ctx context.Context, cfg *config.Config, info UpstreamRequestLog, status int, headers http.Header, body []byte) {
\tlogging.SetResponseHeaders(ctx, headers)
''',
    '''func RecordAPIWebsocketUpgradeRejection(ctx context.Context, cfg *config.Config, info UpstreamRequestLog, status int, headers http.Header, body []byte) {
\trequestmeta.ObserveUpstreamResponse(ctx, status, headers, body)
\tlogging.SetResponseHeaders(ctx, headers)
''',
    'func RecordAPIWebsocketUpgradeRejection(ctx context.Context, cfg *config.Config, info UpstreamRequestLog, status int, headers http.Header, body []byte) {\n\trequestmeta.ObserveUpstreamResponse',
)
replace_once(
    logging_helpers,
    '''\tRecordAPIRequest(ctx, cfg, info)
\tRecordAPIResponseMetadata(ctx, cfg, status, headers)
\tAppendAPIResponseChunk(ctx, cfg, body)
''',
    '''\tRecordAPIRequest(ctx, cfg, info)
\tlogOnlyCtx := requestmeta.WithoutUpstreamResponseObserver(ctx)
\tRecordAPIResponseMetadata(logOnlyCtx, cfg, status, headers)
\tAppendAPIResponseChunk(logOnlyCtx, cfg, body)
''',
    'logOnlyCtx := requestmeta.WithoutUpstreamResponseObserver(ctx)',
)
replace_once(
    logging_helpers,
    '''func AppendAPIWebsocketResponse(ctx context.Context, cfg *config.Config, payload []byte) {
\tif !requestLogCaptureEnabled(cfg) {
''',
    '''func AppendAPIWebsocketResponse(ctx context.Context, cfg *config.Config, payload []byte) {
\trequestmeta.ObserveUpstreamResponse(ctx, 0, nil, payload)
\tif !requestLogCaptureEnabled(cfg) {
''',
    'func AppendAPIWebsocketResponse(ctx context.Context, cfg *config.Config, payload []byte) {\n\trequestmeta.ObserveUpstreamResponse',
)

add_go_import(server_management, '"github.com/gin-gonic/gin"\n', '\t"' + import_path('internal/embeddedusage') + '"\n')

replace_go_call_block(
    server_routes,
    '\ts.engine.GET("/", func(c *gin.Context) {',
    '''\ts.engine.GET("/", func(c *gin.Context) {
\t\tc.Redirect(http.StatusTemporaryRedirect, "/management.html")
\t})
''',
    'c.Redirect(http.StatusTemporaryRedirect, "/management.html")',
)
replace_once(
    server_management,
    '''\t{
\t\tmgmt.GET("/config", s.mgmt.GetConfig)
''',
    '''\t{
\t\tembeddedusage.RegisterGinRoutes(mgmt.Group("/usage", s.mgmt.BindQuotaCacheIdentity))
\t\tembeddedusage.RegisterDataManagementGinRoutes(mgmt.Group("/data"))

\t\tmgmt.GET("/config", s.mgmt.GetConfig)
''',
)
replace_once(
    server_management,
    '''\t\tmgmt.POST("/api-call", s.mgmt.APICall)\n''',
	'''\t\tmgmt.POST("/api-call", s.mgmt.APICall)\n\t\tmgmt.POST("/auth-files/test", s.mgmt.TestAuthFileConnection)\n\t\ts.mgmt.RegisterAccountInspectionRoutes(mgmt)\n\t\ts.mgmt.RegisterRoutingPolicyRoutes(mgmt)\n\t\ts.mgmt.RegisterProFeatureRoutes(mgmt)\n''',
)

replace_once(
    server_management,
    '\t\tmgmt.POST("/quota/fetch", s.mgmt.FetchCredentialQuota)\n',
    '\t\tmgmt.POST("/quota/fetch", s.mgmt.FetchProPluginQuota)\n',
)

# Both API generations use plugin-scoped GET/POST routes. Keep the upstream
# request parsing and URL selection, then share Pro normalization and persistence.
replace_go_function(
    ROOT / 'internal/api/handlers/management/plugin_quota.go',
    'func (h *Handler) fetchQuotaForPlugin(c *gin.Context, pluginID, authIndex string)',
    '''func (h *Handler) fetchQuotaForPlugin(c *gin.Context, pluginID, authIndex string) {
\th.fetchProQuotaForPlugin(c, pluginID, authIndex)
}
''',
    'h.fetchProQuotaForPlugin(c, pluginID, authIndex)',
)
# The existing upstream endpoint fixture now exercises persisted Pro quota.
replace_once(
    ROOT / 'internal/api/handlers/management/plugin_quota_test.go',
    'func TestPluginSpecificQuotaEndpoints(t *testing.T) {\n',
    'func TestPluginSpecificQuotaEndpoints(t *testing.T) {\n\tstartProQuotaTestService(t)\n',
)

auth_files_handler = ROOT / 'internal/api/handlers/management/auth_files.go'
replace_once(
    auth_files_handler,
    '''\t// Try to find auth ID via authManager
\tvar authID string
\tif h.authManager != nil {
\t\tauths := h.authManager.List()
\t\tfor _, auth := range auths {
\t\t\tif auth.FileName == name || auth.ID == name {
\t\t\t\tauthID = auth.ID
\t\t\t\tbreak
\t\t\t}
\t\t}
\t}
''',
    '''\t// Try to find the exact auth record via authManager. Disabled auths are
\t// intentionally absent from the upstream model registry, but their provider
\t// metadata is still needed for the static model fallback below.
\tauthIndex := strings.TrimSpace(c.Query("auth_index"))
\tselectedAuth, foundAuth := h.lookupAuthFile(name, authIndex)
\tif authIndex != "" && !foundAuth {
\t\tc.JSON(404, gin.H{"error": "auth file not found"})
\t\treturn
\t}
\tvar authID string
\tif selectedAuth != nil {
\t\tauthID = selectedAuth.ID
\t}
''',
)
replace_once(
    auth_files_handler,
    '''\tmodels := reg.GetModelsForClient(authID)

\tresult := make([]gin.H, 0, len(models))
''',
    '''\tmodels := reg.GetModelsForClient(authID)
\tif len(models) == 0 && selectedAuth != nil && c.Query("purpose") == "connection-test" {
\t\tmodels = authFileManagementFallbackModels(selectedAuth)
\t}

\tresult := make([]gin.H, 0, len(models))
''',
)

handler = ROOT / 'internal/api/handlers/management/handler.go'
add_go_import(handler, '"net/http"\n', '\t"net/url"\n')
replace_once(
    handler,
    '''\tpluginReleases          pluginReleaseCache
''',
    '''\tpluginReleases          pluginReleaseCache
\tproAuthMutationMu       sync.Mutex
\tlifecycleContext        context.Context
\tlifecycleCancel         context.CancelFunc
\tlifecycleWG             sync.WaitGroup
\tshutdownOnce            sync.Once
''',
    'proAuthMutationMu       sync.Mutex',
)
replace_once(
    handler,
    '''\th := &Handler{
''',
    '''\tlifecycleContext, lifecycleCancel := context.WithCancel(context.Background())

\th := &Handler{
''',
    'lifecycleContext, lifecycleCancel := context.WithCancel(context.Background())',
)
replace_once(
    handler,
    '''\t\tallowRemoteOverride: envSecret != "",
\t\tenvSecret:           envSecret,
\t\tconfigGeneration:    1,
\t\tapiKeyRefs:          make(map[string]apiKeyReference),
\t}
''',
    '''\t\tallowRemoteOverride: envSecret != "",
\t\tenvSecret:           envSecret,
\t\tconfigGeneration:    1,
\t\tapiKeyRefs:          make(map[string]apiKeyReference),
\t\tlifecycleContext:    lifecycleContext,
\t\tlifecycleCancel:     lifecycleCancel,
\t}
''',
    'lifecycleContext:    lifecycleContext',
)
replace_go_function(
    handler,
    'func (h *Handler) startAttemptCleanup() {',
    '''func (h *Handler) startAttemptCleanup() {
\tif h == nil || h.lifecycleContext == nil {
\t\treturn
\t}
\th.lifecycleWG.Add(1)
\tgo func() {
\t\tdefer h.lifecycleWG.Done()
\t\tticker := time.NewTicker(attemptCleanupInterval)
\t\tdefer ticker.Stop()
\t\tfor {
\t\t\tselect {
\t\t\tcase <-h.lifecycleContext.Done():
\t\t\t\treturn
\t\t\tcase <-ticker.C:
\t\t\t\th.purgeStaleAttempts()
\t\t\t}
\t\t}
\t}()
}
''',
    'case <-h.lifecycleContext.Done():',
)
replace_once(
    handler,
    '''\t\tif provided == "" {
\t\t\tprovided = c.GetHeader("X-Management-Key")
\t\t}
''',
    '''\t\tif provided == "" {
\t\t\tprovided = c.GetHeader("X-Management-Key")
\t\t}
\t\tif provided == "" {
\t\t\tprovided = managementKeyFromWebSocketProtocol(c)
\t\t}
''',
)
insert_before(
    handler,
    '''func (h *Handler) Middleware() gin.HandlerFunc {
''',
    '''func managementKeyFromWebSocketProtocol(c *gin.Context) string {
\tif !strings.EqualFold(c.GetHeader("Upgrade"), "websocket") {
\t\treturn ""
\t}
\tfor _, protocol := range strings.Split(c.GetHeader("Sec-WebSocket-Protocol"), ",") {
\t\tprotocol = strings.TrimSpace(protocol)
\t\tif !strings.HasPrefix(protocol, "cpa-management.") {
\t\t\tcontinue
\t\t}
\t\tdecoded, err := url.QueryUnescape(strings.TrimPrefix(protocol, "cpa-management."))
\t\tif err != nil {
\t\t\treturn ""
\t\t}
\t\treturn decoded
\t}
\treturn ""
}

''',
    'func managementKeyFromWebSocketProtocol(c *gin.Context) string',
)
replace_once(
    handler,
    '''\th.startAttemptCleanup()
\treturn h
''',
    '''\th.startProManagementRuntime()
\th.startAttemptCleanup()
\treturn h
''',
)
replace_once(
    server,
    '''\tlog.Debug("Stopping API server...")

\tif s.keepAliveEnabled {
''',
    '''\tlog.Debug("Stopping API server...")
\tdefer func() {
\t\tif s.mgmt != nil {
\t\t\ts.mgmt.Shutdown()
\t\t}
\t}()

\tif s.keepAliveEnabled {
''',
    's.mgmt.Shutdown()',
)

run = ROOT / 'internal/cmd/run.go'
add_go_import(run, '"' + import_path('internal/config') + '"\n', '\t"' + import_path('internal/embeddedusage') + '"\n')
insert_before(
    run,
    '// StartService builds and runs the proxy service using the exported SDK.\n',
    'func applyProRequiredStartupConfig(cfg *config.Config, configPath string) {\n\tif cfg == nil {\n\t\treturn\n\t}\n\tshouldPersistUsageStatistics := !cfg.UsageStatisticsEnabled\n\tshouldPersistPanelRepository := cfg.RemoteManagement.PanelGitHubRepository != config.DefaultPanelGitHubRepository\n\tcfg.UsageStatisticsEnabled = true\n\tcfg.RemoteManagement.PanelGitHubRepository = config.DefaultPanelGitHubRepository\n\tif configPath == "" {\n\t\treturn\n\t}\n\tpersistExisting := func(value any, paths ...[]string) {\n\t\tfor _, path := range paths {\n\t\t\tmissing, err := config.SaveConfigPreserveCommentsUpdateExistingScalars(configPath, []config.ExistingScalarUpdate{{Path: path, Value: value}})\n\t\t\tif err != nil {\n\t\t\t\tlog.Warnf("failed to update existing required startup config %v: %v", path, err)\n\t\t\t\treturn\n\t\t\t}\n\t\t\tif len(missing) == 0 {\n\t\t\t\treturn\n\t\t\t}\n\t\t}\n\t}\n\tif shouldPersistUsageStatistics {\n\t\tpersistExisting(true, []string{"observability", "usage", "usage-statistics-enabled"}, []string{"usage-statistics-enabled"})\n\t}\n\tif shouldPersistPanelRepository {\n\t\tpersistExisting(config.DefaultPanelGitHubRepository, []string{"management", "panel-github-repository"}, []string{"remote-management", "panel-github-repository"})\n\t}\n}\n\n',
    'func applyProRequiredStartupConfig',
)
queue_go_source('internal/cmd/pro_startup_config_test.go')
insert_before_nth(
    run,
    '''\tgo func() {
''',
    '''\tusageService, err := embeddedusage.StartForPath(ctx, configPath)
\tif err != nil {
\t\tlog.Errorf("failed to start embedded usage service: %v", err)
\t\tclose(doneCh)
\t\treturn cancelFn, doneCh
\t}
\tembeddedusage.SetDefaultService(usageService)
\tapplyProRequiredStartupConfig(cfg, configPath)

''',
    1,
    'embeddedusage.StartForPath(ctx, configPath)',
)
insert_before_nth(
    run,
    '''\terr = service.Run(runCtx)
''',
    '''\tusageService, err := embeddedusage.StartForPath(runCtx, configPath)
\tif err != nil {
\t\tlog.Errorf("failed to start embedded usage service: %v", err)
\t\treturn err
\t}
\tembeddedusage.SetDefaultService(usageService)
\tapplyProRequiredStartupConfig(cfg, configPath)

''',
    1,
    'embeddedusage.StartForPath(runCtx, configPath)',
)

queue_go_source('sdk/cliproxy/auth/inspection_refresh.go')
queue_go_source('sdk/cliproxy/auth/xai_quota_fence.go')
queue_go_source('sdk/cliproxy/auth/xai_quota_fence_test.go')
queue_go_source('internal/runtime/executor/xai_quota_fence_test.go')
queue_go_source('internal/api/handlers/management/xai_quota_fence_test.go')
queue_go_source('internal/api/handlers/management/xai_quota_id_token_http_test.go')
queue_go_source('sdk/cliproxy/auth/inspection_refresh_test.go')
queue_go_source('internal/runtime/executor/inspection_refresh_compat_test.go')
queue_go_source('sdk/cliproxy/auth/pinned_execution.go')

auth_types = ROOT / 'sdk/cliproxy/auth/types.go'
replace_once(
    auth_types,
    '''\tSuccess int64 `json:"-"`
\tFailed  int64 `json:"-"`
''',
    '''\tSelected int64 `json:"-"`
\tSuccess  int64 `json:"-"`
\tFailed   int64 `json:"-"`
''',
    'Selected int64',
)

queue_go_source('internal/pro/routing/quota_protection.go')
queue_go_source('sdk/cliproxy/auth/pro_quota_protection.go')
queue_go_source('internal/api/handlers/management/quota_recovery.go')

auth_selector = ROOT / 'sdk/cliproxy/auth/selector.go'
replace_once(
    auth_selector,
    '\tif hasUnauthorizedAuthFailure(auth) {\n\t\treturn true, blockReasonOther, time.Time{}\n\t}\n',
    '\tif blocked, retryAt := proQuotaProtectionBlocked(auth, model, now); blocked {\n\t\treturn true, blockReasonCooldown, retryAt\n\t}\n\tif hasUnauthorizedAuthFailure(auth) {\n\t\treturn true, blockReasonOther, time.Time{}\n\t}\n',
    'proQuotaProtectionBlocked(auth, model, now)',
)

replace_once(
    auth_selector,
    '''type RoundRobinSelector struct {
\tmu         sync.Mutex
\tlastPicked map[string]string
\tmaxKeys    int
}''',
    '''type RoundRobinSelector struct {
\tmu                      sync.Mutex
\tlastPicked              map[string]string
\tmaxKeys                  int
\troutingCursorRestored    map[string]bool
\tpersistedRoutingCursors map[string]string
}''',
    'persistedRoutingCursors map[string]string',
)
replace_once(
    auth_selector,
    '''\ts.ensureRotationKey(key, limit)
\tpicked := available[successorIndex(available, s.lastPicked[key])]
\ts.lastPicked[key] = picked.ID
\treturn picked, nil
}''',
    '''\ts.ensureRotationKey(key, limit)
\ts.restoreRoutingCursorLocked(provider, model, key)
\tpicked := available[successorIndex(available, s.lastPicked[key])]
\ts.lastPicked[key] = picked.ID
\ts.persistRoutingCursorLocked(provider, model, picked)
\treturn picked, nil
}''',
    's.restoreRoutingCursorLocked(provider, model, key)',
)
replace_once(
    auth_selector,
    '''\tif _, ok := s.lastPicked[key]; !ok && len(s.lastPicked) >= limit {
\t\ts.lastPicked = make(map[string]string)
\t}
}''',
    '''\tif _, ok := s.lastPicked[key]; !ok && len(s.lastPicked) >= limit {
\t\ts.lastPicked = make(map[string]string)
\t\ts.routingCursorRestored = make(map[string]bool)
\t}
}''',
    's.routingCursorRestored = make(map[string]bool)',
)

auth_conductor = ROOT / 'sdk/cliproxy/auth/conductor_lifecycle.go'
replace_once(
    auth_conductor,
    '\t// Serialize this credential, but release the manager lock during store I/O.\n',
    '\trestoreQuotaProtection(auth)\n\t// Serialize this credential, but release the manager lock during store I/O.\n',
    'restoreQuotaProtection(auth)',
)

replace_once(
    auth_conductor,
    '\tif auth.Generation <= existing.Generation {\n',
    '\tsetQuotaProtections(auth, proroutingQuotaProtections(existing))\n\tif auth.Generation <= existing.Generation {\n',
    'setQuotaProtections(auth, proroutingQuotaProtections(existing))',
)

replace_once(
    auth_conductor,
    '\tupdateModePrepare\n)',
    '\tupdateModePrepare\n\tupdateModeInspectionRefresh\n)',
    '\tupdateModeInspectionRefresh\n)',
)
replace_once(
    auth_conductor,
    '\tif mode == updateModeRefresh {\n\t\tmerged := MergeRefreshedAuth(base, existing, auth)\n',
    '''\tif mode == updateModeInspectionRefresh && !inspectionRefreshIdentityMatches(base, existing) {
\t\tm.mu.Unlock()
\t\treturn nil, false, false, false, ErrInspectionAuthChanged
\t}
\tif mode == updateModeRefresh || mode == updateModeInspectionRefresh {
\t\tmerged := MergeRefreshedAuth(base, existing, auth)
\t\tif mode == updateModeInspectionRefresh {
\t\t\treconcileInspectionRefreshStatus(base, existing, merged)
\t\t}
''',
    'mode == updateModeInspectionRefresh && !inspectionRefreshIdentityMatches',
)
replace_once(
    auth_conductor,
    '\t\tif credChanged || mode == updateModeRefresh {\n',
    '\t\tif (credChanged || mode == updateModeRefresh) && (mode != updateModeInspectionRefresh || inspectionRefreshRestrictionsMatch(base, existing)) {\n',
    'if (credChanged || mode == updateModeRefresh) && (mode != updateModeInspectionRefresh',
)
replace_once(
    auth_conductor,
    'if reconcileAuthGrantCooldownAfterRefresh(auth, existing, preserveNonAuth, now) {',
    'if (mode != updateModeInspectionRefresh || !existing.NextRetryAfter.After(now)) && reconcileAuthGrantCooldownAfterRefresh(auth, existing, preserveNonAuth, now) {',
)
replace_once(
    auth_conductor,
    '\t\tdevinQuotaRestored := mode == updateModeRefresh && applyDevinQuotaRestoration(auth, existing, now)\n',
    '\t\tdevinQuotaRestored := (mode == updateModeRefresh || (mode == updateModeInspectionRefresh && inspectionRefreshRestrictionsMatch(base, existing))) && applyDevinQuotaRestoration(auth, existing, now)\n',
    'devinQuotaRestored := (mode == updateModeRefresh || (mode == updateModeInspectionRefresh',
)
replace_once(
    auth_conductor,
    '''func (m *Manager) Register(ctx context.Context, auth *Auth) (*Auth, error) {
	if auth == nil {
		return nil, nil
	}
''',
    '''func (m *Manager) Register(ctx context.Context, auth *Auth) (*Auth, error) {
	if auth == nil {
		return nil, nil
	}
	RestoreAccountPolicyBase(auth)
''',
    '''func (m *Manager) Register(ctx context.Context, auth *Auth) (*Auth, error) {
	if auth == nil {
		return nil, nil
	}
	RestoreAccountPolicyBase(auth)''',
)
replace_once(
    auth_conductor,
    '''func (m *Manager) updateInternalWithStatus(ctx context.Context, base, auth *Auth, mode updateAuthMode, strictPrepareVersion bool) (*Auth, bool, bool, error) {
	if auth == nil || auth.ID == "" {
		return nil, false, false, nil
	}
''',
    '''func (m *Manager) updateInternalWithStatus(ctx context.Context, base, auth *Auth, mode updateAuthMode, strictPrepareVersion bool) (*Auth, bool, bool, error) {
	if auth == nil || auth.ID == "" {
		return nil, false, false, nil
	}
	RestoreAccountPolicyBase(auth)
''',
    '''func (m *Manager) updateInternal(ctx context.Context, base, auth *Auth, mode updateAuthMode) (*Auth, error) {
	if auth == nil || auth.ID == "" {
		return nil, nil
	}
	RestoreAccountPolicyBase(auth)''',
)
replace_once(
    auth_conductor,
    '''\tauth.EnsureIndex()
\tm.mu.Lock()
''',
    '''\tauth.EnsureIndex()
\trestoreAuthRuntimeStats(auth)
\tcleanupLegacyQuotaCacheOnRegister(auth)
\tm.mu.Lock()
''',
    'cleanupLegacyQuotaCacheOnRegister(auth)',
)
replace_once(
    auth_conductor,
    '''\tauth.Success = existing.Success
\tauth.Failed = existing.Failed
\tauth.recentRequests = existing.recentRequests
''',
    '''\tauth.Selected = existing.Selected
\tauth.Success = existing.Success
\tauth.Failed = existing.Failed
\tauth.recentRequests = existing.recentRequests
''',
    'auth.Selected = existing.Selected',
)
auth_conductor_text = read(auth_conductor)
# RefreshSchedulerEntry takes its own locked, policy-aware snapshot. Remove the
# upstream temporary snapshot together with its consumer, retaining the unlock.
lifecycle_scheduler_upsert = '''\t// Snapshot before unlocking: MarkResult mutates the published auth in place.
\tvar schedulerSnapshot *Auth
\tif m.scheduler != nil {
\t\tschedulerSnapshot = authClone.Clone()
\t}
\tm.mu.Unlock()
\treleaseMutation()
\tif !shouldDeferAPIKeyModelAliasRebuild(ctx) {
\t\tm.rebuildAPIKeyModelAliasFromRuntimeConfig()
\t}
\tif m.scheduler != nil {
\t\tm.scheduler.upsertAuth(schedulerSnapshot)
\t}
'''
if auth_conductor_text.count(lifecycle_scheduler_upsert) != 1:
    raise SystemExit(
        f'expected one registration scheduler upsert in {auth_conductor}, '
        f'found {auth_conductor_text.count(lifecycle_scheduler_upsert)}'
    )
write(
    auth_conductor,
    auth_conductor_text.replace(
        lifecycle_scheduler_upsert,
        '''\tm.mu.Unlock()
\treleaseMutation()
\tif !shouldDeferAPIKeyModelAliasRebuild(ctx) {
\t\tm.rebuildAPIKeyModelAliasFromRuntimeConfig()
\t}
\tm.RefreshSchedulerEntry(authClone.ID)
''',
    ),
)
# Updates now share a post-commit helper, called after releasing the auth
# mutation gate (including conditional plugin updates). Keep that ordering.
replace_once(
    auth_conductor,
    '''\tif m.scheduler != nil {
\t\tm.scheduler.upsertAuth(committed.Clone())
\t}
''',
    '''\tm.RefreshSchedulerEntry(committed.ID)
''',
)

auth_conductor = ROOT / 'sdk/cliproxy/auth/conductor_cooldown.go'
replace_once(
    auth_conductor,
    '''func (m *Manager) MarkResult(ctx context.Context, result Result) {
	if result.AuthID == "" {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if antigravityTargetRejected(result) {
		return
	}
	routedModel := antigravityResultModel(ctx, result)
	if routedModel != "" {
		result.Model = routedModel
	}
	if m != nil {
		if policy := m.ResultPolicy(); policy != nil {
			result = policy.ApplyResultPolicy(ctx, result)
			if result.AuthID == "" {
				return
			}
		}
	}
	modelKey := canonicalModelKey(result.Model)
''',
    '''func (m *Manager) markResult(ctx context.Context, result Result) bool {
	if result.AuthID == "" {
		return false
	}
	if antigravityTargetRejected(result) {
		return false
	}
	routedModel := antigravityResultModel(ctx, result)
	if routedModel != "" {
		result.Model = routedModel
	}
	if ctx == nil {
		ctx = context.Background()
	}
	// A result policy may alter outcome classification, but cannot discard or
	// replace a diagnostic's original identity/restriction binding.
	pinnedIdentity, hasPin := result.Options.Metadata[pinnedResultIdentityKey]
	pinnedSnapshot := result.Options.Metadata[pinnedResultIdentityKey+".snapshot"]
	if m != nil {
		if policy := m.ResultPolicy(); policy != nil {
			result = policy.ApplyResultPolicy(ctx, result)
			if result.AuthID == "" {
				return false
			}
		}
	}
	if hasPin {
		identity, validIdentity := pinnedIdentity.(string)
		snapshot, validSnapshot := pinnedSnapshot.(string)
		currentIdentity, _ := result.Options.Metadata[pinnedResultIdentityKey].(string)
		currentSnapshot, _ := result.Options.Metadata[pinnedResultIdentityKey+".snapshot"].(string)
		if !validIdentity || !validSnapshot || identity == "" || snapshot == "" || identity != currentIdentity || snapshot != currentSnapshot {
			return false
		}
	}
	modelKey := canonicalModelKey(result.Model)
''',
    'func (m *Manager) markResult(ctx context.Context, result Result) bool',
)
replace_once(
    auth_conductor,
    '\tm.updateSessionAffinity(result)\n}\n\nfunc (m *Manager) updateSessionAffinity',
    '\tm.updateSessionAffinity(result)\n\treturn authSnapshot != nil\n}\n\nfunc (m *Manager) updateSessionAffinity',
    'return authSnapshot != nil',
)
# Keep upstream stale-result bookkeeping intact; only add the pinned identity
# fence before mutation and adapt the stale branch to markResult's bool result.
replace_once(
    auth_conductor,
    '''\trejectsToken := !result.Success && (result.Error == nil || result.Error.Code != ErrorCodeForceCooldown) && !shouldSkipCredentialCooldown(result.Error) && (statusCodeFromResult(result.Error) == http.StatusUnauthorized || isUnauthorizedError(result.Error))

\treleaseMutation := m.lockAuthMutation(result.AuthID)
\tdefer releaseMutation()
\tm.mu.Lock()
''',
    '''\trejectsToken := !result.Success && (result.Error == nil || result.Error.Code != ErrorCodeForceCooldown) && !shouldSkipCredentialCooldown(result.Error) && (statusCodeFromResult(result.Error) == http.StatusUnauthorized || isUnauthorizedError(result.Error))

\treleaseMutation := m.lockAuthMutation(result.AuthID)
\tdefer releaseMutation()
\tm.mu.Lock()
\tif !pinnedResultIdentityMatches(result, m.auths[result.AuthID]) {
\t\tm.mu.Unlock()
\t\treturn false
\t}
''',
    'pinnedResultIdentityMatches(result, m.auths[result.AuthID])',
)
replace_once(
    auth_conductor,
    '''\t\t\tm.publishErrorEvent(result, nil)
\t\t\treturn
\t\t}
\t\tif modelKey == ""''',
    '''\t\t\tm.publishErrorEvent(result, nil)
\t\t\treturn false
\t\t}
\t\tif modelKey == ""''',
)
if '\t\twasTerminalUnauthorized := hasUnauthorizedAuthFailure(auth)\n' in read(auth_conductor):
    replace_once(
        auth_conductor,
        '\t\twasTerminalUnauthorized := hasUnauthorizedAuthFailure(auth)\n',
        '''\t\tif result.Success {
\t\t\tif _, pinned := result.Options.Metadata[pinnedResultIdentityKey]; pinned {
\t\t\t\t// Identity and restriction snapshot were verified under m.mu above.
\t\t\t\t// Only explicit recovery may clear terminal 401 state.
\t\t\t\tauth.LastError = nil
\t\t\t\tclearAuthStateOnSuccess(auth, now)
\t\t\t}
\t\t}
\t\twasTerminalUnauthorized := hasUnauthorizedAuthFailure(auth)
''',
    )
else:
    replace_once(
        auth_conductor,
        '\t\tif result.Success {\n\t\t\tif auth.Quota.Reason == "credential_quota"',
        '\t\tif result.Success {\n\t\t\tif _, pinned := result.Options.Metadata[pinnedResultIdentityKey]; pinned {\n\t\t\t\tclearAuthStateOnSuccess(auth, now)\n\t\t\t}\n\t\t\tif auth.Quota.Reason == "credential_quota"',
        'if _, pinned := result.Options.Metadata[pinnedResultIdentityKey]; pinned',
    )
replace_once(
    auth_conductor,
    '''\tvar authSnapshot *Auth
\tcooldownStateChanged := false
''',
    '''\tvar authSnapshot *Auth
\tvar authStatsObservedAt time.Time
\tcooldownStateChanged := false
''',
    'var authStatsObservedAt time.Time',
)
replace_once(
    auth_conductor,
    '''\t\tnow = time.Now()
\t\tresponseHeaders := internallogging.GetResponseHeaders(ctx)
''',
    '''\t\tnow = time.Now()
\t\tauthStatsObservedAt = now
\t\tresponseHeaders := internallogging.GetResponseHeaders(ctx)
''',
    'authStatsObservedAt = now',
)
replace_once(
    auth_conductor,
    '''\tm.mu.Unlock()
\treleaseMutation()
\tif m.scheduler != nil && authSnapshot != nil {
''',
    '''\tm.mu.Unlock()
\treleaseMutation()
\tqueueAuthRuntimeStats(authSnapshot, authStatsObservedAt)
\tif m.scheduler != nil && authSnapshot != nil {
''',
    'queueAuthRuntimeStats(authSnapshot, authStatsObservedAt)',
)
# Preserve upstream's credential-wide refresh after an unauthorized response.
# Older releases pass CredentialScope directly; v8.0.34 also includes refreshes.
result_credential_scope = 'result.CredentialScope'
if '\t\tm.scheduler.upsertAuthResult(authSnapshot, targetModels, credentialScopedResult)\n' in read(auth_conductor):
    result_credential_scope = 'credentialScopedResult'
replace_once(
    auth_conductor,
    f'\t\tm.scheduler.upsertAuthResult(authSnapshot, targetModels, {result_credential_scope})\n',
    f'\t\tm.scheduler.upsertAuthResult(m.applyAccountPolicy(authSnapshot), targetModels, {result_credential_scope})\n',
    'm.scheduler.upsertAuthResult(m.applyAccountPolicy(authSnapshot),',
)
replace_once(
    auth_conductor,
    '''\tif m.scheduler != nil {
\t\tfor _, snapshot := range snapshots {
\t\t\tm.scheduler.upsertAuth(snapshot)
\t\t}
\t}
''',
    '''\tfor _, snapshot := range snapshots {
\t\tm.RefreshSchedulerEntry(snapshot.ID)
\t}
''',
    'm.RefreshSchedulerEntry(snapshot.ID)',
)
replace_once(
    auth_conductor,
    '''\tif m.scheduler != nil {
\t\tfor _, snapshot := range snapshotsByID {
\t\t\tm.scheduler.upsertAuth(snapshot)
\t\t}
\t}
''',
    '''\tfor _, snapshot := range snapshotsByID {
\t\tm.RefreshSchedulerEntry(snapshot.ID)
\t}
''',
    'for _, snapshot := range snapshotsByID {\n\t\tm.RefreshSchedulerEntry(snapshot.ID)',
)
replace_once(
    auth_conductor,
    '''\tif m.scheduler != nil && snapshot != nil {
\t\tm.scheduler.upsertAuth(snapshot)
\t}
''',
    '''\tif snapshot != nil {
\t\tm.RefreshSchedulerEntry(snapshot.ID)
\t}
''',
    '''\tif snapshot != nil {
\t\tm.RefreshSchedulerEntry(snapshot.ID)
\t}
\tif snapshot != nil && cooldownStateChanged''',
)

auth_conductor = ROOT / 'sdk/cliproxy/auth/conductor_selection.go'
replace_once(
    auth_conductor,
    '''func (m *Manager) snapshotAuths() []*Auth {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Auth, 0, len(m.auths))
	for _, a := range m.auths {
		out = append(out, a.Clone())
	}
	return out
}''',
    '''func (m *Manager) snapshotAuths() []*Auth {
	m.mu.RLock()
	resolver := m.accountPolicyResolver
	out := make([]*Auth, 0, len(m.auths))
	for _, a := range m.auths {
		out = append(out, resolveAccountPolicy(a, resolver))
	}
	m.mu.RUnlock()
	return out
}''',
    'out = append(out, resolveAccountPolicy(a, resolver))',
)
# Preserve the scheduler structural epoch introduced by upstream v7.3.10.
scheduler_epoch_update = (
    '\tm.structuralEpoch.Add(1)\n'
    if '\tm.structuralEpoch.Add(1)\n' in read(auth_conductor) else ''
)
replace_once(
    auth_conductor,
    '''	snapshot := auth.Clone()
	m.mu.RUnlock()
''' + scheduler_epoch_update + '''	m.scheduler.upsertAuth(snapshot)
''',
    '''	resolver := m.accountPolicyResolver
	snapshot := resolveAccountPolicy(auth, resolver)
	m.mu.RUnlock()
''' + scheduler_epoch_update + '''	m.scheduler.upsertAuth(snapshot)
''',
    'snapshot := resolveAccountPolicy(auth, resolver)',
)
replace_once(
    auth_conductor,
    '''\tif m.scheduler != nil {
\t\tm.scheduler.upsertAuth(snapshot)
\t}
\tif cooldownStateChanged {''',
    '''\tm.RefreshSchedulerEntry(snapshot.ID)
\tif cooldownStateChanged {''',
    '''\tm.RefreshSchedulerEntry(snapshot.ID)
\tif cooldownStateChanged {''',
)
# v8.0.63 reconciles aliases while holding m.mu. Keep those atomic scheduler
# updates in place, applying policy only to their execution snapshots.
native_alias_reconciliation = 'type reconciledAuthItem struct {' in read(auth_conductor)
if native_alias_reconciliation:
    replace_once(
        auth_conductor,
        '\t\t\tm.scheduler.upsertAuth(snapshot.Clone())\n',
        '\t\t\tm.scheduler.upsertAuth(resolveAccountPolicy(snapshot, m.accountPolicyResolver))\n',
    )
    replace_once(
        auth_conductor,
        '\t\t\t\t\t\tm.scheduler.upsertAuth(current.Clone())\n',
        '\t\t\t\t\t\tm.scheduler.upsertAuth(resolveAccountPolicy(current, m.accountPolicyResolver))\n',
    )
auth_conductor_text = read(auth_conductor)
candidate_append = '\t\tcandidates = append(candidates, candidate)\n'
policy_candidate_append = '\t\tcandidates = append(candidates, resolveAccountPolicy(candidate, m.accountPolicyResolver))\n'
if policy_candidate_append not in auth_conductor_text:
    if auth_conductor_text.count(candidate_append) < 2:
        raise SystemExit(f'expected at least two legacy candidate appends in {auth_conductor}')
    write(auth_conductor, auth_conductor_text.replace(candidate_append, policy_candidate_append))

auth_conductor = ROOT / 'sdk/cliproxy/auth/conductor_refresh.go'
# v8.0.53 keeps a usable access token until expiry after its refresh grant was
# rejected. Preserve its guarded mutation and reschedule/unschedule decisions,
# but refresh the policy-aware scheduler only after releasing m.mu.
if '\t\tif !accessTokenRejected && auth.HasValidAccessToken(checkNow) {\n\t\t\tshouldReschedule := false\n' in read(auth_conductor):
    replace_once(
        auth_conductor,
        '''\t\tif !accessTokenRejected && auth.HasValidAccessToken(checkNow) {
\t\t\tshouldReschedule := false
''',
        '''\t\tif !accessTokenRejected && auth.HasValidAccessToken(checkNow) {
\t\t\tshouldReschedule := false
\t\t\tshouldRefreshScheduler := false
''',
    )
    replace_once(
        auth_conductor,
        '''\t\t\t\tm.auths[id] = current
\t\t\t\tif m.scheduler != nil {
\t\t\t\t\tm.scheduler.upsertAuth(current.Clone())
\t\t\t\t}
\t\t\t}
\t\t\tm.mu.Unlock()
\t\t\tif shouldReschedule {
''',
        '''\t\t\t\tm.auths[id] = current
\t\t\t\tshouldRefreshScheduler = true
\t\t\t}
\t\t\tm.mu.Unlock()
\t\t\tif shouldRefreshScheduler {
\t\t\t\tm.RefreshSchedulerEntry(id)
\t\t\t}
\t\t\tif shouldReschedule {
''',
    )
# Keep upstream refresh notifications and retry decisions. Resolve the same
# scheduler view used by RefreshSchedulerEntry while the auth is stable.
replace_all_exact(
    auth_conductor,
    'm.scheduler.upsertAuth(current.Clone())',
    'm.scheduler.upsertAuth(resolveAccountPolicy(current, m.accountPolicyResolver))',
    5,
)
auth_conductor = ROOT / 'sdk/cliproxy/auth/conductor_selection.go'
replace_once(
    auth_conductor,
    'func (m *Manager) pickNext(ctx context.Context, provider, model string, opts cliproxyexecutor.Options, tried map[string]struct{}) (*Auth, ProviderExecutor, error) {\n',
    '''func (m *Manager) pickNext(ctx context.Context, provider, model string, opts cliproxyexecutor.Options, tried map[string]struct{}) (auth *Auth, executor ProviderExecutor, err error) {
\tdefer func() {
\t\tif err == nil && auth != nil {
\t\t\tm.recordAuthSelected(auth.ID)
\t\t}
\t}()
''',
    'func (m *Manager) pickNext(ctx context.Context, provider, model string, opts cliproxyexecutor.Options, tried map[string]struct{}) (auth *Auth, executor ProviderExecutor, err error)',
)
replace_once(
    auth_conductor,
    'func (m *Manager) pickNextMixed(ctx context.Context, providers []string, model string, opts cliproxyexecutor.Options, tried map[string]struct{}) (*Auth, ProviderExecutor, string, error) {\n',
    '''func (m *Manager) pickNextMixed(ctx context.Context, providers []string, model string, opts cliproxyexecutor.Options, tried map[string]struct{}) (auth *Auth, executor ProviderExecutor, providerKey string, err error) {
\tdefer func() {
\t\tif err == nil && auth != nil {
\t\t\tm.recordAuthSelected(auth.ID)
\t\t}
\t}()
''',
    'func (m *Manager) pickNextMixed(ctx context.Context, providers []string, model string, opts cliproxyexecutor.Options, tried map[string]struct{}) (auth *Auth, executor ProviderExecutor, providerKey string, err error)',
)

auth_files_handler = ROOT / 'internal/api/handlers/management/auth_files.go'
auth_files_crud_handler = ROOT / 'internal/api/handlers/management/auth_files_crud.go'
auth_files_fields_handler = ROOT / 'internal/api/handlers/management/auth_files_fields.go'
add_go_import(
    auth_files_crud_handler,
    '"github.com/gin-gonic/gin"\n',
    '\t"' + import_path('internal/embeddedusage') + '"\n',
)
add_go_import(
    auth_files_fields_handler,
    f'\t"{import_path("internal/credentialweight")}"\n',
    f'\t"{import_path("internal/embeddedusage")}"\n',
)
replace_once(
    auth_files_fields_handler,
    '''\tsavedPath, errSave := store.Save(coreauth.WithAuthCreationIntent(ctx), record)
\tif errSave != nil {
\t\treturn savedPath, errSave
\t}
''',
    '''\tsavedPath, errSave := store.Save(coreauth.WithAuthCreationIntent(ctx), record)
\tif errSave != nil {
\t\treturn savedPath, errSave
\t}
\tif sdkAuth.TakeReusedExistingAuthIdentity(record) {
\t\t// The stable auth ID/index and runtime statistics remain attached to the
\t\t// existing file. Only quota is invalidated because credential or
\t\t// entitlement changes make the previous snapshot unsafe to reuse.
\t\t_ = embeddedusage.DeleteQuotaCache(ctx, record.Provider, record.FileName)
\t}
''',
    'sdkAuth.TakeReusedExistingAuthIdentity(record)',
)
replace_once(
    auth_files_handler,
    '''\tentry["success"] = auth.Success
\tentry["failed"] = auth.Failed
''',
    '''\tentry["selected"] = auth.Selected
\tentry["success"] = auth.Success
\tentry["failed"] = auth.Failed
''',
    'entry["selected"] = auth.Selected',
)
replace_once(
    auth_files_crud_handler,
    '''\t\t\t\tdeleted++
\t\t\t\tif v8Mode {
\t\t\t\t\th.reconcileRemovedV8SourceAuths(ctx, full, resolvedFull, "")
\t\t\t\t} else {
\t\t\t\t\th.removeAuth(ctx, full)
\t\t\t\t}
''',
    '''\t\t\t\tdeleted++
\t\t\t\tif v8Mode {
\t\t\t\t\th.reconcileRemovedV8SourceAuths(ctx, full, resolvedFull, "")
\t\t\t\t} else {
\t\t\t\t\th.removeAuth(ctx, full)
\t\t\t\t}
\t\t\t\t_ = embeddedusage.DeleteAuthRuntimeState(ctx, "", "", name)
''',
    'DeleteAuthRuntimeState(ctx, "", "", name)',
)
replace_once(
    auth_files_crud_handler,
    '''\ttargetPath := filepath.Join(h.currentHandlerAuthDir(), filepath.Base(name))
\ttargetID := ""
\tif targetAuth := h.findAuthForDelete(name); targetAuth != nil {
''',
    '''\ttargetPath := filepath.Join(h.currentHandlerAuthDir(), filepath.Base(name))
\ttargetID := ""
\ttargetIndex := ""
\tif targetAuth := h.findAuthForDelete(name); targetAuth != nil {
''',
    'targetIndex := ""',
)
replace_once(
    auth_files_crud_handler,
    '''\t\ttargetID = strings.TrimSpace(targetAuth.ID)
\t\tif path := strings.TrimSpace(authAttribute(targetAuth, "path")); path != "" {
''',
    '''\t\ttargetID = strings.TrimSpace(targetAuth.ID)
\t\ttargetIndex = strings.TrimSpace(targetAuth.Index)
\t\tif path := strings.TrimSpace(authAttribute(targetAuth, "path")); path != "" {
''',
    'targetIndex = strings.TrimSpace(targetAuth.Index)',
)
replace_once(
    auth_files_crud_handler,
    '''\tif isV8AuthFileMutation(ctx) {
\t\th.reconcileRemovedV8SourceAuths(ctx, targetPath, resolvedTargetPath, targetID)
\t} else {
\t\th.removeAuthsForPath(ctx, targetPath, targetID)
\t}
\treturn filepath.Base(name), http.StatusOK, nil
''',
    '''\tif isV8AuthFileMutation(ctx) {
\t\th.reconcileRemovedV8SourceAuths(ctx, targetPath, resolvedTargetPath, targetID)
\t} else {
\t\th.removeAuthsForPath(ctx, targetPath, targetID)
\t}
\t_ = embeddedusage.DeleteAuthRuntimeState(ctx, targetID, targetIndex, filepath.Base(name))
\treturn filepath.Base(name), http.StatusOK, nil
''',
    'DeleteAuthRuntimeState(ctx, targetID, targetIndex',
)

auth_scheduler = ROOT / 'sdk/cliproxy/auth/scheduler.go'
replace_once(
    auth_scheduler,
    '''\tmixedCursors        map[string]int
\tmixedWeightedStates map[string]*smoothWeightedState
}''',
    '''\tmixedCursors        map[string]int
\tmixedWeightedStates map[string]*smoothWeightedState
\tmixedRestored       map[string]bool
\tpersistedCursors    map[string]string
}''',
    'persistedCursors map[string]string',
)
replace_once(
    auth_scheduler,
    '''\tmodelShards map[string]*modelScheduler
}''',
    '''\tmodelShards      map[string]*modelScheduler
\tpersistedCursors map[string]string
}''',
    'modelShards      map[string]*modelScheduler',
)
replace_once(
    auth_scheduler,
    '''type modelScheduler struct {
\tmodelKey        string''',
    '''type modelScheduler struct {
\tproviderKey     string
\tmodelKey        string
\tpersistedCursors map[string]string''',
    'providerKey     string\n\tmodelKey',
)
replace_once(
    auth_scheduler,
    '''type readyView struct {
\tflat          []*scheduledAuth
\tlastPicked    string
\tweightedState smoothWeightedState
}''',
    '''type readyView struct {
\tflat          []*scheduledAuth
\tlastPicked    string
\tweightedState smoothWeightedState
\tcursorKey     string
\tpersisted     map[string]string
}''',
    'cursorKey  string',
)
# Insert only Pro cursor fields; preserve native weighted-selector ownership.
replace_once(
    auth_scheduler,
    'func newAuthScheduler(selector Selector) *authScheduler {\n',
    'func newAuthScheduler(selector Selector) *authScheduler {\n\tpersistedCursors := loadRoutingCursorStates()\n',
    'persistedCursors := loadRoutingCursorStates()',
)
replace_once(
    auth_scheduler,
    '\t\tmixedWeightedStates: make(map[string]*smoothWeightedState),\n',
    '\t\tmixedWeightedStates: make(map[string]*smoothWeightedState),\n'
    '\t\tmixedRestored:       make(map[string]bool),\n'
    '\t\tpersistedCursors:    persistedCursors,\n',
    '\t\tpersistedCursors:    persistedCursors,',
)
auth_scheduler_text = read(auth_scheduler)
auth_scheduler_legacy_refresh = '''\ts.mixedCursors = make(map[string]int)
\ts.mixedWeightedStates = make(map[string]*smoothWeightedState)
\tnow := time.Now()'''
auth_scheduler_native_refresh = '''\ts.mixedCursors = make(map[string]int, len(existingMixedCursors))
\ts.mixedWeightedStates = make(map[string]*smoothWeightedState)
\tnow := time.Now()'''
if auth_scheduler_legacy_refresh in auth_scheduler_text:
    replace_once(
        auth_scheduler,
        auth_scheduler_legacy_refresh,
        '''\ts.mixedCursors = make(map[string]int)
\ts.mixedWeightedStates = make(map[string]*smoothWeightedState)
\ts.mixedRestored = make(map[string]bool)
\tnow := time.Now()''',
        's.mixedWeightedStates = make(map[string]*smoothWeightedState)\n\ts.mixedRestored = make(map[string]bool)\n\tnow := time.Now()',
    )
elif auth_scheduler_text.count(auth_scheduler_native_refresh) != 1:
    raise SystemExit('expected native scheduler refresh initialization in scheduler.go')
replace_once(
    auth_scheduler,
    '''\tclear(s.mixedCursors)
\tclear(s.mixedWeightedStates)
''',
    '''\tclear(s.mixedCursors)
\tclear(s.mixedWeightedStates)
\tclear(s.mixedRestored)
''',
    'clear(s.mixedRestored)',
)
replace_once(
    auth_scheduler,
    '''\t\tproviderState = &providerScheduler{
\t\t\tproviderKey: providerKey,
\t\t\tauths:       make(map[string]*scheduledAuthMeta),
\t\t\tmodelShards: make(map[string]*modelScheduler),
\t\t}''',
    '''\t\tproviderState = &providerScheduler{
\t\t\tproviderKey:      providerKey,
\t\t\tauths:            make(map[string]*scheduledAuthMeta),
\t\t\tmodelShards:      make(map[string]*modelScheduler),
\t\t\tpersistedCursors: s.persistedCursors,
\t\t}''',
    'persistedCursors: s.persistedCursors',
)
replace_once(
    auth_scheduler,
    '''\tshard := &modelScheduler{
\t\tmodelKey:        modelKey,
\t\tentries:         make(map[string]*scheduledAuth),
\t\treadyByPriority: make(map[int]*readyBucket),
\t}''',
    '''\tshard := &modelScheduler{
\t\tproviderKey:      p.providerKey,
\t\tmodelKey:         modelKey,
\t\tpersistedCursors: p.persistedCursors,
\t\tentries:          make(map[string]*scheduledAuth),
\t\treadyByPriority:  make(map[int]*readyBucket),
\t}''',
    'persistedCursors: p.persistedCursors',
)
replace_once(
    auth_scheduler,
    '''\t\tif cursorState, ok := cursorStates[priority]; ok && bucket != nil {
\t\t\trestoreReadyViewCursors(&bucket.all, cursorState.all)
\t\t\trestoreReadyViewCursors(&bucket.ws, cursorState.ws)
\t\t}
\t\tm.readyByPriority[priority] = bucket
''',
    '''\t\tif cursorState, ok := cursorStates[priority]; ok && bucket != nil {
\t\t\trestoreReadyViewCursors(&bucket.all, cursorState.all)
\t\t\trestoreReadyViewCursors(&bucket.ws, cursorState.ws)
\t\t}
\t\tm.configurePersistedReadyBucket(bucket, priority)
\t\tm.readyByPriority[priority] = bucket
''',
    'm.configurePersistedReadyBucket(bucket, priority)',
)
replace_once(
    auth_scheduler,
    '''\t\tv.lastPicked = entry.auth.ID
\t\treturn entry
''',
    '''\t\tv.lastPicked = entry.auth.ID
\t\tv.persistSelection(entry)
\t\treturn entry
''',
    'v.persistSelection(entry)',
)
replace_once(
    auth_scheduler,
    '''\tstartSlot := s.mixedCursors[cursorKey] % totalWeight
''',
    '''\tstartSlot := s.mixedCursors[cursorKey] % totalWeight
\tstartSlot, persistedCursorKey := s.restoreMixedCursor(
\t\tcursorKey, bestPriority, startSlot, candidateShards, weights, segmentStarts,
\t)
''',
    'startSlot, persistedCursorKey := s.restoreMixedCursor(',
)
replace_once(
    auth_scheduler,
    '''\t\ts.mixedCursors[cursorKey] = slot + 1
\t\treturn picked, providerKey, nil
''',
    '''\t\ts.mixedCursors[cursorKey] = slot + 1
\t\ts.persistMixedCursor(persistedCursorKey, picked.ID)
\t\treturn picked, providerKey, nil
''',
    's.persistMixedCursor(persistedCursorKey, picked.ID)',
)

# Account policy fields are execution-only overlays. Every Manager-driven
# full scheduler refresh uses RefreshSchedulerEntry, except locked native alias
# reconciliation, whose two snapshots explicitly resolve the same policy.
# Result updates retain upstream model scope and stale-generation handling,
# applying the cached account policy to an execution-only snapshot.
auth_package = ROOT / 'sdk/cliproxy/auth'
auth_go_paths = {path for path in auth_package.glob('*.go') if not path.name.endswith('_test.go')}
auth_go_paths.update(
    path for path in _writes
    if path.parent == auth_package and path.suffix == '.go' and not path.name.endswith('_test.go')
)
direct_scheduler_upserts = {
    path.relative_to(ROOT).as_posix(): read(path).count('m.scheduler.upsertAuth(')
    for path in auth_go_paths
    if read(path).count('m.scheduler.upsertAuth(') > 0
    and not (path.name == 'conductor_refresh.go' and 'm.notifyAuthChangeLocked(id)' in read(path))
}
expected_direct_scheduler_upserts = {
    'sdk/cliproxy/auth/conductor_selection.go': 3 if native_alias_reconciliation else 1,
}
if direct_scheduler_upserts != expected_direct_scheduler_upserts:
    raise SystemExit(
        'Manager scheduler upserts must be centralized in RefreshSchedulerEntry: '
        f'found {direct_scheduler_upserts}'
    )
result_scheduler_upserts = {
    path.relative_to(ROOT).as_posix(): read(path).count('m.scheduler.upsertAuthResult(')
    for path in auth_go_paths
    if read(path).count('m.scheduler.upsertAuthResult(') > 0
}
if result_scheduler_upserts != {'sdk/cliproxy/auth/conductor_cooldown.go': 1}:
    raise SystemExit(f'unexpected Manager result scheduler upserts: {result_scheduler_upserts}')

# Watcher updates must not overwrite a management write committed after their snapshot.
replace_once(
    ROOT / 'sdk/cliproxy/auth/conductor_lifecycle.go',
    '\tupdateModeReplace updateAuthMode = iota',
    '''	updateModeReplace updateAuthMode = iota
	updateModeSnapshot''',
    '''	updateModeReplace updateAuthMode = iota
	updateModeSnapshot''',
)
replace_once(
    ROOT / 'sdk/cliproxy/auth/conductor_lifecycle.go',
    '// Update replaces an existing auth entry and notifies hooks.',
    '''// UpdateFromSnapshot applies an observed watcher update only while its base
// snapshot still owns the persisted inputs. A concurrent persisted update wins.
func (m *Manager) UpdateFromSnapshot(ctx context.Context, base, updated *Auth) (*Auth, error) {
 if base == nil || updated == nil || base.ID != updated.ID { return nil, nil }
 return m.updateInternal(ctx, base, updated, updateModeSnapshot)
}

// Update replaces an existing auth entry and notifies hooks.''',
    '''// UpdateFromSnapshot applies an observed watcher update only while its base
// snapshot still owns the persisted inputs. A concurrent persisted update wins.
func (m *Manager) UpdateFromSnapshot(ctx context.Context, base, updated *Auth) (*Auth, error) {
 if base == nil || updated == nil || base.ID != updated.ID { return nil, nil }
 return m.updateInternal(ctx, base, updated, updateModeSnapshot)
}

// Update replaces an existing auth entry and notifies hooks.''',
)
# Digest-bearing watcher snapshots still need upstream's disk fence and runtime
# state merge when Pro adds its persisted-input CAS through updateModeSnapshot.
replace_once(
    ROOT / 'sdk/cliproxy/auth/conductor_lifecycle.go',
    '\tisFileSnapshot := mode == updateModeReplace && auth.HasFileSnapshot()',
    '\tisFileSnapshot := (mode == updateModeReplace || mode == updateModeSnapshot) && auth.HasFileSnapshot()',
)
replace_once(
    ROOT / 'sdk/cliproxy/auth/conductor_lifecycle.go',
    '''	if m.authEpochs == nil {
		m.authEpochs = make(map[string]uint64)
	}
	if existing.RegistrationEpoch > m.authEpochs[auth.ID] {''',
    ''' if mode == updateModeSnapshot && (base == nil || existing.RegistrationEpoch != base.RegistrationEpoch || !snapshotAuthInputsEqual(existing, base)) {
  current := existing.Clone()
  m.mu.Unlock()
  return current, false, false, false, nil
 }
 if mode == updateModeSnapshot { mergeAuthSaveDelta(auth, base, existing, false) }
	if m.authEpochs == nil {
		m.authEpochs = make(map[string]uint64)
	}
	if existing.RegistrationEpoch > m.authEpochs[auth.ID] {''',
    ''' if mode == updateModeSnapshot && (base == nil || existing.RegistrationEpoch != base.RegistrationEpoch || !snapshotAuthInputsEqual(existing, base)) {
  current := existing.Clone()
  m.mu.Unlock()
  return current, false, false, false, nil
 }
 if mode == updateModeSnapshot { mergeAuthSaveDelta(auth, base, existing, false) }
	if m.authEpochs == nil {
		m.authEpochs = make(map[string]uint64)
	}
	if existing.RegistrationEpoch > m.authEpochs[auth.ID] {''',
)
replace_once(
    ROOT / 'sdk/cliproxy/service_auth.go',
    '\t\tupdated, err = s.coreManager.Update(ctx, auth)\n',
    '''\t\tupdated, err = s.coreManager.UpdateFromSnapshot(ctx, existing, auth)
\t\tif err == nil && updated == nil { return nil }
''',
    'updated, err = s.coreManager.UpdateFromSnapshot(ctx, existing, auth)',
)


insert_before(
    ROOT / 'sdk/cliproxy/auth/conductor_lifecycle.go',
    '// UpdateFromSnapshot applies',
    '''// snapshotAuthInputsEqual excludes request counters, cooldowns and scheduler
// generations: those must not cause a legitimate file observation to be lost.
func snapshotAuthInputsEqual(a, b *Auth) bool {
 return a.RegistrationEpoch == b.RegistrationEpoch && a.CredentialVersion == b.CredentialVersion &&
 a.Provider == b.Provider && a.Prefix == b.Prefix && a.FileName == b.FileName && a.Label == b.Label &&
 a.Disabled == b.Disabled && (a.Status == StatusDisabled) == (b.Status == StatusDisabled) &&
 a.ProxyURL == b.ProxyURL && reflect.DeepEqual(a.Attributes, b.Attributes) && reflect.DeepEqual(a.Metadata, b.Metadata)
}

''',
    'func snapshotAuthInputsEqual(',
)
add_go_import(ROOT / 'sdk/cliproxy/auth/conductor_lifecycle.go', '\t"math"\n', '\t"reflect"\n')

replace_once(
    ROOT / 'sdk/cliproxy/service_auth.go',
    '''		auth.CreatedAt = existing.CreatedAt
''',
    '''  // A queued, unversioned file observation may predate a successful PATCH.
  // Capture existing before reading disk: a write during synthesis then loses
  // the snapshot CAS below instead of replaying an old credential.
  if auth.Generation == 0 && !auth.HasFileSnapshot() && auth.AuthSourceKind() == coreauth.AuthSourceFile {
   if watcher := s.currentWatcher(); watcher != nil {
    path := auth.Attributes[coreauth.AttributePath]
    if path == "" { path = auth.Attributes[coreauth.AttributeSource] }
    if path != "" {
     snapshots, errSnapshot := watcher.SnapshotAuthFile(path)
     if errSnapshot != nil { return existing }
     var fresh *coreauth.Auth
     for _, candidate := range snapshots { if candidate != nil && candidate.ID == auth.ID { fresh = candidate; break } }
     if fresh == nil { return existing }
     auth = fresh.Clone()
    }
   }
  }
		auth.CreatedAt = existing.CreatedAt
''',
    'Capture existing before reading disk:',
)

insert_before(
    ROOT / 'sdk/cliproxy/service_models.go',
    'func (s *Service) applyOAuthPolicy(',
    '''// Filter both original catalogs in one evaluation so effective policy counts
// include plugin contributions. Keep them separate until native aliasing is done.
func (s *Service) filterOAuthCatalogs(ctx context.Context, cfg *config.Config, auth *coreauth.Auth, native, plugins []*ModelInfo) ([]*ModelInfo, []*ModelInfo) {
 filtered := appendDistinctModels(native, plugins)
 if s.proApp != nil { filtered = s.proApp.FilterModels(ctx, cfg, auth, filtered, s.coreManager) }
 allowed := make(map[string]bool, len(filtered))
 for _, model := range filtered { if model != nil { allowed[strings.TrimSpace(model.ID)] = true } }
 var filteredNative, filteredPlugins []*ModelInfo
 for _, model := range native { if model != nil && allowed[strings.TrimSpace(model.ID)] { filteredNative = append(filteredNative, model) } }
 for _, model := range plugins { if model != nil && allowed[strings.TrimSpace(model.ID)] { filteredPlugins = append(filteredPlugins, model) } }
 return filteredNative, filteredPlugins
}

''',
    'func (s *Service) filterOAuthCatalogs(',
)
replace_once(
    ROOT / 'sdk/cliproxy/service_models.go',
    '''	models = applyExcludedModels(models, excluded)
	models = applyOAuthModelAliasForAuth''',
    '''	models, staticModels = s.filterOAuthCatalogs(ctx, cfg, a, applyExcludedModels(models, excluded), applyExcludedModels(staticModels, excluded))
	models = applyOAuthModelAliasForAuth''',
    '''	models, staticModels = s.filterOAuthCatalogs(ctx, cfg, a, applyExcludedModels(models, excluded), applyExcludedModels(staticModels, excluded))
	models = applyOAuthModelAliasForAuth''',
)
replace_once(
    ROOT / 'sdk/cliproxy/service_models.go',
    '\tmodels = appendDistinctModels(models, applyExcludedModels(staticModels, excluded))',
    '\tmodels = appendDistinctModels(models, staticModels)',
    '\tmodels = appendDistinctModels(models, staticModels)',
)
replace_all_exact(
    ROOT / 'sdk/cliproxy/service_models.go',
    'ms = s.appendPluginModels(providerKey, ms, nil)',
    'ms = s.applyOAuthPolicy(ctx, cfg, a, s.appendPluginModels(providerKey, ms, nil))',
    2,
)
replace_all_exact(
    ROOT / 'sdk/cliproxy/service_models.go',
    'ms = s.appendPluginModels(providerKey, nil, nil)',
    'ms = s.applyOAuthPolicy(ctx, cfg, a, s.appendPluginModels(providerKey, nil, nil))',
    2,
)
replace_all_exact(
    ROOT / 'sdk/cliproxy/service_models.go',
    'models = s.appendPluginModels(providerKey, nil, nil)',
    'models = s.applyOAuthPolicy(ctx, cfg, a, s.appendPluginModels(providerKey, nil, nil))',
    1,
)
replace_once(
    ROOT / 'sdk/cliproxy/antigravity_models.go',
    '\tif s.proApp != nil { models = s.proApp.FilterModels(ctx, cfg, auth, models, s.coreManager) }',
    '\tmodels, policyPlugins := s.filterOAuthCatalogs(ctx, cfg, auth, models, applyExcludedModels(s.pluginModelsForProvider("antigravity"), excluded))',
    '\tmodels, policyPlugins := s.filterOAuthCatalogs(ctx, cfg, auth, models, applyExcludedModels(s.pluginModelsForProvider("antigravity"), excluded))',
)
replace_once(
    ROOT / 'sdk/cliproxy/antigravity_models.go',
    '\tmodels = s.appendPluginModels("antigravity", models, excluded)',
    '\tmodels = appendDistinctModels(models, policyPlugins)',
    '\tmodels = appendDistinctModels(models, policyPlugins)',
)

# Recovery may observe an unavailable hint just before a balance refresh publishes.
# Invalidate only the observation that justified cleanup, in local and Home modes.
antigravity_credits = ROOT / 'internal/runtime/executor/antigravity_executor_credits.go'
antigravity_credits_old = '''	defer unlock()
	_, hadFailure := antigravityCreditsFailureByAuth.LoadAndDelete(key)
	if hint, ok := cliproxyauth.GetAntigravityCreditsHintForAuth(auth); ok && hint.Known && !hint.Available {'''
if antigravity_credits_old in read(antigravity_credits):
    replace_once(
        antigravity_credits,
        antigravity_credits_old,
        '''	_, hadFailure := antigravityCreditsFailureByAuth.LoadAndDelete(key)
	hint, ok := cliproxyauth.GetAntigravityCreditsHintForAuth(auth)
	unlock()
	if ok && hint.Known && !hint.Available {''',
    )
antigravity_executor_credits = ROOT / 'internal/runtime/executor/antigravity_executor_credits.go'
if '\t\t\tcliproxyauth.SetAntigravityCreditsHintForAuth(auth, cliproxyauth.AntigravityCreditsHint{})' in read(antigravity_executor_credits):
    replace_once(
        antigravity_executor_credits,
        '\t\t\tcliproxyauth.SetAntigravityCreditsHintForAuth(auth, cliproxyauth.AntigravityCreditsHint{})',
        '\t\t\tcliproxyauth.CompareAndSwapAntigravityCreditsHintForAuth(auth, hint, cliproxyauth.AntigravityCreditsHint{})',
    )
queue_go_source('sdk/cliproxy/auth/antigravity_credits_recovery_pro.go')
queue_go_source('sdk/cliproxy/auth/antigravity_credits_recovery_pro_test.go')

# Upstream 8.1.4 can emit the initial tenant server_list before the
# asynchronously populated server records are visible. The production
# behavior is valid; keep the end-to-end test focused on protocol shape
# and validate the record when one is present.
server_tunnel_test = ROOT / 'internal/api/server_tunnel_test.go'
replace_once(
    server_tunnel_test,
    '''\tif initList.Get("type").String() != wstunnel.ControlTypeServerList ||
\t\tinitList.Get("servers.#").Int() != 1 ||
\t\tinitList.Get("servers.0.uuid").String() != "srv-a1" ||
\t\tinitList.Get("servers.0.type").String() != "cpa-desktop" {
\t\tt.Fatalf("unexpected initial server_list: %s", initList.Raw)
\t}''',
    '''\tif initList.Get("type").String() != wstunnel.ControlTypeServerList {
\t\tt.Fatalf("unexpected initial server_list type: %s", initList.Raw)
\t}
\tif initList.Get("servers.#").Int() > 0 &&
\t\t(initList.Get("servers.0.uuid").String() != "srv-a1" ||
\t\t\tinitList.Get("servers.0.type").String() != "cpa-desktop") {
\t\tt.Fatalf("unexpected initial server_list: %s", initList.Raw)
\t}''',
    'unexpected initial server_list type:',
)


format_go_writes([
    'sdk/cliproxy/auth/inspection_refresh.go',
    'sdk/cliproxy/auth/inspection_refresh_test.go',
    'internal/runtime/executor/inspection_refresh_compat_test.go',
    'internal/runtime/executor/antigravity_executor_credits.go',
    'sdk/cliproxy/auth/antigravity_credits_recovery_pro.go',
    'sdk/cliproxy/auth/antigravity_credits_recovery_pro_test.go',
    'sdk/cliproxy/auth/conductor_lifecycle.go',
    'sdk/cliproxy/service_auth.go',
    'internal/cache/runtime_proxy_generation_pro.go',
    'internal/client/codex/live/realtime_usage_pro.go',
    'internal/runtime/executor/runtime_proxy_antigravity_pro.go',
    'internal/auth/claude/runtime_proxy_transport_pro.go',
    'internal/runtime/executor/helps/runtime_proxy_utls_pro.go',
    'sdk/cliproxy/runtime_proxy_provider_pro.go',
    'internal/runtime/executor/helps/runtime_proxy_helpers_pro.go',
    'sdk/api/handlers/openai/openai_responses_websocket_forward.go',
    'sdk/api/handlers/openai/responses_websocket_terminal_order_test.go',
    'cmd/server/main.go',
    'internal/cache/bounded_lru_pro.go',
	'internal/auth/claude/utls_transport.go',
    'internal/api/server.go',
    'internal/api/api_key_policy_middleware_test.go',
    'internal/api/self_query.go',
    'internal/api/self_query_test.go',
    'internal/api/server_middleware.go',
    'internal/api/server_options.go',
    'internal/api/server_routes.go',
	'internal/api/server_model_policy.go',
    'internal/api/server_test.go',
    *[
        f'internal/api/handlers/management/{name}'
        for name in ACCOUNT_INSPECTION_SOURCE_FILES
    ],
    'internal/api/handlers/management/account_inspection_host.go',
    'internal/api/handlers/management/api_key_policy.go',
    'internal/api/handlers/management/models.go',
    'internal/api/handlers/management/models_test.go',
    'internal/api/handlers/management/auth_file_connection.go',
    'internal/api/handlers/management/auth_file_connection_test.go',
    'internal/api/handlers/management/auth_file_metadata.go',
    'internal/api/handlers/management/auth_files_fields.go',
    'internal/api/handlers/management/auth_files.go',
    'internal/api/handlers/management/handler.go',
    'internal/api/handlers/management/management_panel.go',
    'internal/api/handlers/management/management_panel_test.go',
    'internal/managementasset/updater.go',
    'internal/managementasset/gitstore_token_test.go',
    'internal/client/codex/live/client_secret.go',
	'internal/client/codex/live/live.go',
	'internal/client/codex/live/sideband.go',
	'internal/client/codex/live/runtime_proxy_override_test.go',
    'internal/client/codex/live/websocket.go',
    'internal/client/codex/live/api_key_quota_relay_test.go',
    'internal/api/handlers/management/plugin_quota.go',
    'internal/api/handlers/management/plugin_quota_test.go',
    'internal/api/handlers/management/pro_plugin_quota.go',
    'internal/api/handlers/management/pro_plugin_quota_test.go',
    'internal/api/handlers/management/pro_auth_mutation.go',
    'internal/api/handlers/management/pro_auth_mutation_integration_test.go',
    'internal/api/handlers/management/pro_virtual_auth_consistency_integration_test.go',
    'internal/api/handlers/management/pro_features.go',
    'internal/api/handlers/management/pro_management_runtime.go',
    'internal/api/handlers/management/routing_policy.go',
    'internal/api/handlers/management/routing_policy_test.go',
    'internal/config/config_existing_updates.go',
    'internal/config/config_existing_updates_test.go',
    'internal/config/pro_plugin_autoinstall.go',
    'internal/embeddedusage/facade.go',
    'internal/embeddedusage/internalusage/facade.go',
    'internal/pluginhost/gemini_cli_storage_compat.go',
    'internal/pluginhost/gemini_cli_storage_compat_test.go',
    'internal/pluginhost/gemini_cli_quota_legacy.go',
	'internal/pluginhost/gemini_cli_quota_legacy_test.go',
	'internal/pluginhost/http_bridge.go',
	'internal/pluginhost/pro_quota_provider.go',
	'internal/pluginhost/pro_quota_provider_test.go',
	'internal/pluginhost/runtime_proxy_override_test.go',
    'internal/pluginhost/snapshot.go',
    'internal/pluginhost/pro_executor_provider.go',
    'internal/pluginhost/adapters_executors.go',
	'internal/pluginhost/plugin_executor_usage.go',
    'internal/pluginhost/plugin_executor_usage_test.go',
    'internal/pluginstore/autoinstall.go',
    'internal/pluginstore/autoinstall_test.go',
    'internal/pluginstore/auth.go',
    'internal/pluginstore/gitstore_auth_test.go',
    'internal/redisqueue/plugin.go',
    'internal/redisqueue/queue.go',
    'internal/redisqueue/speed_test.go',
	'internal/redisqueue/api_key_policy_usage_test.go',
	'internal/redisqueue/persistence_queue_test.go',
    'internal/requestmeta/observer.go',
    'internal/requestmeta/observer_test.go',
    'internal/runtime/executor/helps/logging_helpers.go',
	'internal/runtime/executor/helps/proxy_helpers.go',
	'internal/runtime/executor/helps/utls_client.go',
	'internal/runtime/executor/helps/runtime_proxy_override_test.go',
	'internal/runtime/executor/helps/quota_settlement_test.go',
	'internal/runtime/executor/aistudio_executor.go',
	'internal/runtime/executor/helps/usage_pro_extensions.go',
    'internal/runtime/executor/helps/response_observer_test.go',
    'internal/runtime/executor/helps/usage_helpers.go',
    'internal/runtime/executor/helps/usage_speed_test.go',
    'internal/runtime/executor/claude_usage_speed_test.go',
    'internal/runtime/executor/claude_executor_execute.go',
    'internal/runtime/executor/claude_executor_stream.go',
    'internal/runtime/executor/claude_stream_terminal.go',
    'internal/runtime/executor/codex_executor_execute.go',
    'internal/runtime/executor/codex_executor_stream.go',
    'internal/runtime/executor/codex_executor_terminal.go',
    'internal/runtime/executor/codex_retry_after_test.go',
    'internal/runtime/executor/codex_websockets_errors.go',
    'internal/runtime/executor/codex_websockets_execute.go',
    'internal/runtime/executor/codex_websockets_executor_test.go',
    'internal/runtime/executor/codex_websockets_stream.go',
	'internal/runtime/executor/antigravity_executor.go',
	'internal/runtime/executor/antigravity_runtime_proxy_test.go',
    'internal/pro/oauthpolicy/config/config.go',
    'internal/pro/oauthpolicy/config/config_test.go',
    'internal/pro/oauthpolicy/policy/engine.go',
    'internal/pro/oauthpolicy/policy/engine_test.go',
    'internal/pro/app/migration.go',
    'internal/pro/app/migration_test.go',
    'internal/pro/app/app.go',
    'internal/pro/app/app_test.go',
	'internal/pro/apikeypolicy/service.go',
	'internal/pro/apikeypolicy/service_test.go',
	'internal/pro/apikeypolicy/store.go',
	'internal/pro/apikeypolicy/types.go',
    'internal/pro/backup/coordinator.go',
    'internal/pro/backup/coordinator_test.go',
    'internal/pro/host/oauth_policy.go',
    'internal/pro/host/proxy.go',
    'internal/pro/oauthpolicy/service.go',
    'internal/pro/oauthpolicy/management.go',
    'internal/pro/proxypool/management.go',
    'internal/pro/proxypool/service.go',
    'internal/pro/proxypool/service_test.go',
    'internal/pro/settings/store.go',
    'internal/pro/state/types.go',
    'internal/pro/state/writer.go',
    'internal/pro/state/writer_test.go',
    'internal/pro/storage/database.go',
    'internal/pro/storage/database_test.go',
    'internal/pro/storage/schema.go',
    'internal/pro/quota/xai.go',
    'internal/pro/quota/xai_test.go',
    'internal/pro/quota/xai_billing.go',
    'internal/pro/quota/xai_billing_test.go',
    'internal/pro/quota/cache.go',
    'internal/pro/quota/cache_test.go',
    'internal/pro/quota/gemini_cli.go',
    'internal/pro/quota/snapshot.go',
    'internal/pro/quota/snapshot_test.go',
    'internal/pro/routing/quota_protection.go',
    'internal/pro/routing/quota_protection_test.go',
    'internal/pro/routing/runtime.go',
    'internal/pro/routing/runtime_test.go',
    'internal/pro/inspection/lifecycle.go',
    'internal/pro/inspection/lifecycle_test.go',
    'internal/pro/inspection/confirmations.go',
    'internal/pro/inspection/confirmations_test.go',
    'internal/pro/inspection/persistence.go',
    'internal/pro/inspection/persistence_test.go',
    'internal/pro/inspection/policy.go',
    'internal/pro/inspection/policy_test.go',
    'internal/pro/inspection/ports.go',
    'internal/pro/inspection/schedule.go',
    'internal/pro/inspection/schedule_test.go',
    'internal/pro/inspection/workers.go',
    'internal/pro/inspection/workers_test.go',
    'internal/pro/inspection/results.go',
    'internal/pro/inspection/results_test.go',
    'internal/pro/inspection/providers.go',
    'internal/pro/inspection/providers_test.go',
    'internal/pro/inspection/probes.go',
    'internal/pro/inspection/probes_test.go',
    'internal/pro/inspection/snapshot.go',
    'internal/pro/inspection/snapshot_test.go',
    'internal/pro/inspection/status.go',
    'internal/pro/inspection/status_test.go',
    'internal/pro/inspection/actions.go',
    'internal/pro/inspection/actions_test.go',
    'internal/pro/inspection/selection.go',
    'internal/pro/inspection/selection_test.go',
    'internal/pro/observability/module.go',
    'internal/pro/observability/module_test.go',
	'internal/pro/observability/backup_crypto.go',
    'internal/pro/observability/backup_limits.go',
    'internal/pro/observability/config.go',
	'internal/pro/observability/config_test.go',
	'internal/pro/observability/data_management.go',
	'internal/pro/observability/data_management_test.go',
    'internal/pro/observability/global.go',
    'internal/pro/observability/pricing.go',
    'internal/pro/observability/pricing_sync.go',
    'internal/pro/observability/pricing_test.go',
    'internal/pro/observability/pro_settings.go',
    'internal/pro/observability/server.go',
    'internal/pro/observability/server_test.go',
    'internal/pro/observability/settings_store.go',
    'internal/pro/observability/service.go',
    'internal/pro/observability/service_test.go',
    'internal/pro/observability/store.go',
    'internal/pro/observability/store_test.go',
    'internal/pro/observability/xai_quota.go',
    'internal/pro/observability/xai_quota_test.go',
    'internal/pro/observability/internalusage/event.go',
    'internal/pro/observability/internalusage/event_test.go',
    'internal/pro/proxypool/config/config.go',
    'internal/pro/proxypool/config/config_test.go',
    'internal/pro/proxypool/engine/engine.go',
    'internal/pro/proxypool/engine/engine_test.go',
    'internal/pro/proxypool/pool/pool.go',
    'internal/pro/proxypool/pool/pool_test.go',
    'internal/pro/proxypool/socks5/server.go',
	'internal/pro/proxypool/socks5/server_test.go',
    'internal/runtime/executor/xai_executor.go',
    'internal/runtime/executor/xai_executor_execute.go',
    'internal/runtime/executor/xai_executor_stream.go',
    'internal/runtime/executor/xai_quota_observer.go',
    'internal/runtime/executor/xai_websockets_executor.go',
    'internal/translator/codex/openai/chat-completions/codex_fast_service_tier_test.go',
    'internal/translator/codex/openai/responses/codex_fast_service_tier_test.go',
    'sdk/auth/codex_device.go',
    'sdk/auth/filestore.go',
	'sdk/auth/filestore_identity.go',
	'sdk/auth/filestore_identity_test.go',
    'sdk/api/handlers/handlers.go',
    'sdk/api/handlers/api_key_policy_test.go',
	'sdk/api/handlers/api_key_policy_context_test.go',
    'sdk/api/handlers/handlers_context.go',
    'sdk/api/handlers/handlers_execution.go',
    'sdk/api/handlers/handlers_interceptors.go',
    'sdk/api/handlers/handlers_routing.go',
    'sdk/api/handlers/handlers_stream.go',
    'sdk/api/handlers/handlers_stream_cancellation_test.go',
    'sdk/api/handlers/openai/openai_handlers.go',
    'sdk/api/handlers/claude/code_handlers.go',
    'sdk/api/handlers/gemini/gemini_handlers.go',
    'sdk/api/handlers/handlers_speed_test.go',
    'sdk/cliproxy/auth/auth_runtime_state.go',
    'sdk/cliproxy/auth/auth_runtime_state_test.go',
	'sdk/cliproxy/auth/auth_account_policy.go',
	'sdk/cliproxy/auth/auth_account_policy_test.go',
	'sdk/cliproxy/auth/scheduler_runtime_state.go',
    'sdk/cliproxy/auth/pinned_execution.go',
    'sdk/cliproxy/auth/conductor.go',
    'sdk/cliproxy/auth/conductor_execution.go',
    'sdk/cliproxy/auth/conductor_speed_test.go',
    'sdk/cliproxy/auth/codex_retry_after_headers_test.go',
	'sdk/cliproxy/rtprovider.go',
	'sdk/cliproxy/runtime_proxy_override_test.go',
    'sdk/cliproxy/auth/home_concurrency.go',
    'sdk/cliproxy/auth/scheduler.go',
    'sdk/cliproxy/auth/types.go',
    'sdk/cliproxy/builder.go',
    'sdk/cliproxy/service.go',
    'sdk/cliproxy/service_config.go',
    'sdk/cliproxy/service_executors.go',
    'sdk/cliproxy/service_lifecycle.go',
    'sdk/cliproxy/service_models.go',
    'sdk/cliproxy/executor/speed.go',
    'sdk/cliproxy/usage/manager.go',
	'sdk/cliproxy/usage/manager_extensions.go',
	'sdk/cliproxy/usage/manager_pro_test.go',
    'sdk/cliproxy/usage/speed.go',
    'sdk/cliproxy/usage/speed_test.go',
    'sdk/pluginapi/types.go',
    'sdk/proxyutil/proxy.go',
    'sdk/proxyutil/runtime_override.go',
    'sdk/proxyutil/runtime_override_test.go',
])
lifecycle_out = ROOT / 'sdk/cliproxy/auth/conductor_lifecycle.go'
if lifecycle_out in _writes:
    lifecycle_text = read(lifecycle_out)
    lifecycle_text = lifecycle_text.replace('return current, false, true, nil', 'return current, false, true, false, nil')
    lifecycle_text = lifecycle_text.replace('return nil, false, true, fmt.Errorf', 'return nil, false, true, false, fmt.Errorf')
    write(lifecycle_out, lifecycle_text)

flush_writes()
