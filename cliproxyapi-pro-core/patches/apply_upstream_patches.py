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
    with tempfile.TemporaryDirectory(prefix='cliproxyapi-pro-gofmt-') as temp_dir:
        temp_root = Path(temp_dir)
        for relative_path in relative_paths:
            path = ROOT / relative_path
            if path not in _writes:
                raise SystemExit(f'gofmt target was not changed by the patch: {relative_path}')
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


def replace_all(path: Path, old: str, new: str, present=None) -> None:
    text = read(path)
    if (present or new) and (present or new) in text:
        return
    match_count = text.count(old)
    if match_count == 0:
        raise SystemExit(f'expected at least one pattern in {path}, found none: {old[:120]!r}')
    write(path, text.replace(old, new))


def insert_before(path: Path, marker: str, insertion: str, present: str) -> None:
    text = read(path)
    if present in text:
        return
    match_count = text.count(marker)
    if match_count != 1:
        raise SystemExit(f'expected one marker in {path}, found {match_count}: {marker[:120]!r}')
    write(path, text.replace(marker, insertion + marker, 1))


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
    'account_inspection_accounts.go',
    'account_inspection_transport.go',
    'account_inspection_quota.go',
    'account_inspection_runtime_test.go',
    'account_inspection_http_test.go',
    'account_inspection_accounts_test.go',
    'account_inspection_transport_test.go',
    'account_inspection_quota_test.go',
)
customization_sentinel = ROOT / 'internal/embeddedusage'
if customization_sentinel.exists():
    raise SystemExit(f'target already contains CLIProxyAPI Pro customizations: {customization_sentinel}')

new_customization_paths = (
    'internal/pro',
    'internal/api/api_key_policy_middleware_test.go',
	'internal/api/api_key_policy_models_test.go',
    'internal/api/handlers/management/account_inspection_host.go',
    'internal/api/handlers/management/api_key_policy.go',
    'internal/api/handlers/management/api_key_policy_test.go',
	'internal/api/handlers/management/auth_file_metadata.go',
    'internal/api/handlers/management/api_tools_executor_proxy_test.go',
    'internal/api/handlers/management/auth_file_connection.go',
    'internal/api/handlers/management/auth_file_connection_test.go',
    *[
        f'internal/api/handlers/management/{name}'
        for name in ACCOUNT_INSPECTION_SOURCE_FILES
    ],
    'internal/api/handlers/management/plugin_quota.go',
    'internal/api/handlers/management/plugin_quota_test.go',
    'internal/api/handlers/management/pro_auth_mutation.go',
    'internal/api/handlers/management/pro_features.go',
    'internal/api/handlers/management/pro_management_runtime.go',
    'internal/api/handlers/management/routing_policy.go',
    'internal/api/handlers/management/routing_policy_test.go',
    'internal/config/config_existing_updates.go',
    'internal/config/config_existing_updates_test.go',
    'internal/pluginhost/gemini_cli_quota_legacy.go',
    'internal/pluginhost/gemini_cli_quota_legacy_test.go',
    'internal/pluginhost/gemini_cli_storage_compat.go',
    'internal/pluginhost/gemini_cli_storage_compat_test.go',
    'internal/pluginhost/plugin_executor_usage_test.go',
	'internal/pluginhost/plugin_executor_usage.go',
    'internal/pluginhost/quota_provider.go',
    'internal/pluginhost/quota_provider_test.go',
    'internal/pluginstore/autoinstall.go',
    'internal/pluginstore/autoinstall_test.go',
    'internal/pluginstore/gitstore_auth_test.go',
    'internal/managementasset/gitstore_token_test.go',
    'internal/requestmeta/observer.go',
    'internal/requestmeta/observer_test.go',
	'internal/api/server_model_policy.go',
	'internal/runtime/executor/helps/quota_settlement_test.go',
	'internal/runtime/executor/helps/usage_pro_extensions.go',
	'internal/runtime/executor/helps/retry_after.go',
	'internal/runtime/executor/helps/retry_after_test.go',
	'internal/runtime/executor/codex_retry_after_test.go',
    'internal/runtime/executor/helps/usage_speed_test.go',
	'internal/runtime/executor/claude_usage_speed_test.go',
	'internal/runtime/executor/claude_stream_terminal.go',
	'internal/runtime/executor/api_key_policy_usage_test.go',
	'internal/runtime/executor/response_translation.go',
	'internal/pro/observability/config_test.go',
	'internal/redisqueue/speed_test.go',
	'internal/redisqueue/api_key_policy_usage_test.go',
	'sdk/api/handlers/handlers_speed_test.go',
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
	'sdk/cliproxy/auth/scheduler_runtime_state.go',
    'sdk/cliproxy/auth/inspection_refresh.go',
    'sdk/cliproxy/auth/pinned_execution.go',
    'sdk/cliproxy/pro_features_service_test.go',
)
for relative_path in new_customization_paths:
    target_path = ROOT / relative_path
    if target_path.exists():
        raise SystemExit(f'upstream path collides with a Pro customization: {target_path}')

queue_tree(PATCH_SOURCE_DIR / 'internal/pro', ROOT / 'internal/pro')
queue_tree(PATCH_SOURCE_DIR / 'sdk/proxyutil', ROOT / 'sdk/proxyutil')
queue_go_source('internal/api/api_key_policy_middleware_test.go')
queue_go_source('internal/api/api_key_policy_models_test.go')
queue_go_source('internal/api/handlers/management/api_key_policy.go')
queue_go_source('internal/api/handlers/management/api_key_policy_test.go')
queue_go_source('internal/api/handlers/management/auth_file_metadata.go')
queue_go_source('internal/api/server_model_policy.go')
queue_go_source('sdk/api/handlers/api_key_policy_test.go')
queue_go_source('sdk/api/handlers/api_key_policy_context_test.go')
queue_go_source('sdk/auth/filestore_identity_test.go')
queue_go_source('internal/runtime/executor/api_key_policy_usage_test.go')
queue_go_source('internal/runtime/executor/helps/quota_settlement_test.go')
queue_go_source('internal/runtime/executor/helps/usage_pro_extensions.go')
queue_go_source('internal/runtime/executor/helps/retry_after.go')
queue_go_source('internal/runtime/executor/helps/retry_after_test.go')
queue_go_source('internal/runtime/executor/codex_retry_after_test.go')
queue_go_source('internal/runtime/executor/response_translation.go')
queue_go_source('internal/pro/observability/config_test.go')
queue_go_source('sdk/cliproxy/usage/manager_pro_test.go')
queue_go_source('sdk/cliproxy/usage/manager_extensions.go')
queue_go_source('internal/pluginhost/plugin_executor_usage.go')
queue_go_source('sdk/auth/filestore_identity.go')
queue_go_source('sdk/cliproxy/auth/scheduler_runtime_state.go')
queue_go_source('internal/runtime/executor/claude_stream_terminal.go')
queue_go_source('sdk/cliproxy/auth/codex_retry_after_headers_test.go')

codex_terminal = ROOT / 'internal/runtime/executor/codex_executor_terminal.go'
replace_go_function(
    codex_terminal,
    'func newCodexStatusErr(statusCode int, body []byte) statusErr',
    '''func newCodexStatusErr(statusCode int, body []byte, responseHeaders ...http.Header) statusErr {
	errCode := statusCode
	if isCodexModelCapacityError(body) || isCodexUsageLimitError(body) {
		errCode = http.StatusTooManyRequests
	}
	body = classifyCodexStatusError(errCode, body)
	retryAfter := parseCodexRetryAfter(errCode, body, time.Now())
	if retryAfter == nil && len(responseHeaders) > 0 {
		retryAfter = helps.ParseRetryAfterHeader(responseHeaders[0].Get("Retry-After"), time.Now())
	}
	if retryAfter == nil && errCode == http.StatusTooManyRequests && !isCodexModelCapacityError(body) {
		fallback := 10 * time.Second
		retryAfter = &fallback
	}
	err := statusErr{code: errCode, msg: string(body), retryAfter: retryAfter}
	return err
}
''',
    'helps.ParseRetryAfterHeader(responseHeaders[0].Get("Retry-After"), time.Now())',
)

codex_execute = ROOT / 'internal/runtime/executor/codex_executor_execute.go'
replace_all(
    codex_execute,
    'newCodexStatusErr(httpResp.StatusCode, b)',
    'newCodexStatusErr(httpResp.StatusCode, b, httpResp.Header)',
    'newCodexStatusErr(httpResp.StatusCode, b, httpResp.Header)',
)

codex_images = ROOT / 'internal/runtime/executor/codex_openai_images.go'
replace_all(
    codex_images,
    'newCodexStatusErr(httpResp.StatusCode, data)',
    'newCodexStatusErr(httpResp.StatusCode, data, httpResp.Header)',
    'newCodexStatusErr(httpResp.StatusCode, data, httpResp.Header)',
)

codex_stream = ROOT / 'internal/runtime/executor/codex_executor_stream.go'
replace_once(
    codex_stream,
    'newCodexStatusErr(httpResp.StatusCode, data)',
    'newCodexStatusErr(httpResp.StatusCode, data, httpResp.Header)',
    'newCodexStatusErr(httpResp.StatusCode, data, httpResp.Header)',
)

codex_websocket_execute = ROOT / 'internal/runtime/executor/codex_websockets_execute.go'
replace_once(
    codex_websocket_execute,
    'newCodexStatusErr(respHS.StatusCode, bodyErr)',
    'newCodexStatusErr(respHS.StatusCode, bodyErr, respHS.Header)',
    'newCodexStatusErr(respHS.StatusCode, bodyErr, respHS.Header)',
)

codex_websocket_stream = ROOT / 'internal/runtime/executor/codex_websockets_stream.go'
replace_once(
    codex_websocket_stream,
    'newCodexStatusErr(respHS.StatusCode, bodyErr)',
    'newCodexStatusErr(respHS.StatusCode, bodyErr, respHS.Header)',
    'newCodexStatusErr(respHS.StatusCode, bodyErr, respHS.Header)',
)

home_concurrency = ROOT / 'sdk/cliproxy/auth/home_concurrency.go'
replace_go_function(
    home_concurrency,
    'func SafeResponseHeaders(err error) http.Header',
    '''func SafeResponseHeaders(err error) http.Header {
	var busy *HomeConcurrencyBusyError
	if errors.As(err, &busy) && busy != nil {
		return busy.SafeResponseHeaders()
	}
	var exhausted *homeRetryRoundExhaustedError
	if errors.As(err, &exhausted) && exhausted != nil {
		retryAfter := exhausted.RetryAfter()
		if retryAfter == nil {
			return nil
		}
		return safeRetryAfterHeader(*retryAfter)
	}
	var cooldown *homeDispatchRetryAfterError
	if errors.As(err, &cooldown) && cooldown != nil {
		retryAfter := cooldown.RetryAfter()
		if retryAfter == nil {
			return nil
		}
		return safeRetryAfterHeader(*retryAfter)
	}
	var modelCooldown *modelCooldownError
	if errors.As(err, &modelCooldown) && modelCooldown != nil {
		return modelCooldown.Headers()
	}
	var retryAfterStatusErr interface {
		StatusCode() int
		RetryAfter() *time.Duration
	}
	if errors.As(err, &retryAfterStatusErr) && retryAfterStatusErr != nil && retryAfterStatusErr.StatusCode() == http.StatusTooManyRequests {
		if retryAfter := retryAfterStatusErr.RetryAfter(); retryAfter != nil {
			return safeRetryAfterHeader(*retryAfter)
		}
	}
	return nil
}
''',
    'var retryAfterStatusErr interface',
)

codex_device = ROOT / 'sdk/auth/codex_device.go'
replace_once(
    codex_device,
    '''	metadata := map[string]any{
		"email": tokenStorage.Email,
	}
''',
    '''	metadata := map[string]any{
		"email":      tokenStorage.Email,
		"account_id": tokenStorage.AccountID,
	}
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

	if auth.Disabled {
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

	if auth.Disabled {
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
    'func BuildHTTPTransport(raw string) (*http.Transport, Mode, error) {\n\traw = resolveRuntimeProxyOverride(raw)\n\tsetting, errParse := Parse(raw)\n',
    'raw = resolveRuntimeProxyOverride(raw)',
)
replace_once(
    proxyutil_source,
    'func BuildDialer(raw string) (proxy.Dialer, Mode, error) {\n\tsetting, errParse := Parse(raw)\n',
    'func BuildDialer(raw string) (proxy.Dialer, Mode, error) {\n\traw = resolveRuntimeProxyOverride(raw)\n\tsetting, errParse := Parse(raw)\n',
    'func BuildDialer(raw string) (proxy.Dialer, Mode, error) {\n\traw = resolveRuntimeProxyOverride(raw)',
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
replace_once(
    xai_websocket_executor,
    '''\t\t\t\tbodyErrRetry := websocketHandshakeBody(respHSRetry)
\t\t\t\tcloseHTTPResponseBody(respHSRetry, "xai websockets executor: close handshake response body error")
''',
    '''\t\t\t\tbodyErrRetry := websocketHandshakeBody(respHSRetry)
\t\t\t\tif respHSRetry != nil {
\t\t\t\t\thelps.RecordAPIWebsocketUpgradeRejection(ctx, e.cfg, websocketUpgradeRequestLog(wsReqLog), respHSRetry.StatusCode, respHSRetry.Header.Clone(), bodyErrRetry)
\t\t\t\t}
\t\t\t\tcloseHTTPResponseBody(respHSRetry, "xai websockets executor: close handshake response body error")
''',
    'helps.RecordAPIWebsocketUpgradeRejection(ctx, e.cfg, websocketUpgradeRequestLog(wsReqLog), respHSRetry.StatusCode',
)

# Add the optional QuotaProvider capability without changing ABI/schema v1.
pluginapi_types = ROOT / 'sdk/pluginapi/types.go'
replace_once(
    pluginapi_types,
    '\t// FrontendAuthProvider authenticates frontend requests before proxy handling.\n',
    '\t// QuotaProvider fetches normalized per-auth quota and subscription snapshots.\n\tQuotaProvider QuotaProvider\n\t// FrontendAuthProvider authenticates frontend requests before proxy handling.\n',
    'QuotaProvider QuotaProvider',
)
insert_before(
    pluginapi_types,
    '// ModelRegistrar registers plugin-provided models with the host.\n',
    read_text(Path(__file__).resolve().parent / 'plugin_quota_api.go'),
    'type QuotaProvider interface',
)
pluginabi_types = ROOT / 'sdk/pluginabi/types.go'
replace_once(
    pluginabi_types,
    '\tMethodAuthRefresh    = "auth.refresh"\n',
    '\tMethodAuthRefresh    = "auth.refresh"\n\n\tMethodQuotaIdentifier = "quota.identifier"\n\tMethodQuotaFetch      = "quota.fetch"\n',
    'MethodQuotaIdentifier',
)

rpc_schema = ROOT / 'internal/pluginhost/rpc_schema.go'
replace_once(
    rpc_schema,
    '\tAuthProvider                  bool                         `json:"auth_provider"`\n',
    '\tAuthProvider                  bool                         `json:"auth_provider"`\n\tQuotaProvider                 bool                         `json:"quota_provider"`\n',
    'QuotaProvider                 bool',
)
insert_before(
    rpc_schema,
    'type rpcAuthModelRequest struct {\n',
    '''type rpcQuotaFetchRequest struct {
\tpluginapi.QuotaFetchRequest
\tHostCallbackID string `json:"host_callback_id,omitempty"`
}

''',
    'type rpcQuotaFetchRequest struct',
)
replace_once(
    rpc_schema,
    '\t\tAuthProvider:                  caps.AuthProvider != nil,\n',
    '\t\tAuthProvider:                  caps.AuthProvider != nil,\n\t\tQuotaProvider:                 caps.QuotaProvider != nil,\n',
    'QuotaProvider:                 caps.QuotaProvider != nil',
)

rpc_client = ROOT / 'internal/pluginhost/rpc_client.go'
insert_before(
    rpc_client,
    'type rpcFrontendAuthProvider struct {\n',
    '''type rpcQuotaProvider struct {
\t*rpcPluginAdapter
}

''',
    'type rpcQuotaProvider struct',
)
replace_once(
    rpc_client,
    '''\tif resp.Capabilities.FrontendAuthProvider {
\t\tplugin.Capabilities.FrontendAuthProvider = rpcFrontendAuthProvider{rpcPluginAdapter: adapter}
\t}
''',
    '''\tif resp.Capabilities.QuotaProvider {
\t\tplugin.Capabilities.QuotaProvider = rpcQuotaProvider{rpcPluginAdapter: adapter}
\t}
\tif resp.Capabilities.FrontendAuthProvider {
\t\tplugin.Capabilities.FrontendAuthProvider = rpcFrontendAuthProvider{rpcPluginAdapter: adapter}
\t}
''',
    'plugin.Capabilities.QuotaProvider = rpcQuotaProvider',
)
replace_once(
    rpc_client,
    '''\tcase pluginapi.AuthRefreshRequest:
\t\treq.HTTPClient = nil
\t\treturn req
''',
    '''\tcase pluginapi.AuthRefreshRequest:
\t\treq.HTTPClient = nil
\t\treturn req
\tcase pluginapi.QuotaFetchRequest:
\t\treq.HTTPClient = nil
\t\treturn req
\tcase rpcQuotaFetchRequest:
\t\treq.HTTPClient = nil
\t\treturn req
''',
    'case pluginapi.QuotaFetchRequest:',
)
insert_before(
    rpc_client,
    'func sanitizePluginMetadata(src map[string]any) map[string]any {\n',
    '''func (p rpcQuotaProvider) Identifier() string {
\treturn callPluginIdentifier(p.client, pluginabi.MethodQuotaIdentifier)
}

func (p rpcQuotaProvider) FetchQuota(ctx context.Context, req pluginapi.QuotaFetchRequest) (pluginapi.QuotaFetchResponse, error) {
\tcallbackID, closeCallback := p.openHostCallbackContext(ctx)
\tdefer closeCallback()
\treturn callPlugin[pluginapi.QuotaFetchResponse](ctx, p.client, pluginabi.MethodQuotaFetch, rpcQuotaFetchRequest{
\t\tQuotaFetchRequest: req,
\t\tHostCallbackID:    callbackID,
\t})
}

''',
    'func (p rpcQuotaProvider) FetchQuota',
)
plugin_host = ROOT / 'internal/pluginhost/host.go'
replace_once(
    plugin_host,
    '\t\tcaps.AuthProvider != nil ||\n',
    '\t\tcaps.AuthProvider != nil ||\n\t\tcaps.QuotaProvider != nil ||\n',
    'caps.QuotaProvider != nil',
)

plugin_snapshot = ROOT / 'internal/pluginhost/snapshot.go'
replace_once(
    plugin_snapshot,
    '\tOAuthProvider string\n',
    '\tOAuthProvider string\n\tSupportsQuota bool\n\tQuotaProvider string\n\tQuotaMode     string\n',
    'SupportsQuota bool',
)
replace_once(
    plugin_snapshot,
    '''\t\tout = append(out, RegisteredPluginInfo{
''',
    '''\t\tquotaProvider := record.plugin.Capabilities.QuotaProvider
\t\tquotaProviderID := ""
\t\tquotaMode := ""
\t\tsupportsQuota := quotaProvider != nil
\t\tif quotaProvider != nil && !h.isPluginFused(record.id) {
\t\t\tif identifier, okIdentifier := h.callQuotaProviderIdentifier(record.id, quotaProvider); okIdentifier {
\t\t\t\tquotaProviderID = identifier
\t\t\t\tquotaMode = "native"
\t\t\t}
\t\t} else if identifier, okLegacy := h.legacyQuotaProviderForRecord(record); okLegacy {
\t\t\tquotaProviderID = identifier
\t\t\tquotaMode = "legacy-adapter"
\t\t\tsupportsQuota = true
\t\t}
\t\tout = append(out, RegisteredPluginInfo{
''',
    'quotaProvider := record.plugin.Capabilities.QuotaProvider',
)
replace_once(
    plugin_snapshot,
    '\t\t\tOAuthProvider: oauthProvider,\n',
    '\t\t\tOAuthProvider: oauthProvider,\n\t\t\tSupportsQuota: supportsQuota,\n\t\t\tQuotaProvider: quotaProviderID,\n\t\t\tQuotaMode: quotaMode,\n',
    'QuotaProvider: quotaProviderID',
)

management_plugins = ROOT / 'internal/api/handlers/management/plugins.go'
replace_once(
    management_plugins,
    '\tOAuthProvider    string                  `json:"oauth_provider"`\n',
    '\tOAuthProvider    string                  `json:"oauth_provider"`\n\tSupportsQuota    bool                    `json:"supports_quota"`\n\tQuotaProvider    string                  `json:"quota_provider"`\n\tQuotaMode        string                  `json:"quota_mode"`\n',
    'SupportsQuota    bool',
)
replace_once(
    management_plugins,
    '\t\t\tentry.OAuthProvider = htmlsanitize.String(info.OAuthProvider)\n',
    '\t\t\tentry.OAuthProvider = htmlsanitize.String(info.OAuthProvider)\n\t\t\tentry.SupportsQuota = info.SupportsQuota\n\t\t\tentry.QuotaProvider = htmlsanitize.String(info.QuotaProvider)\n\t\t\tentry.QuotaMode = htmlsanitize.String(info.QuotaMode)\n',
    'entry.SupportsQuota = info.SupportsQuota',
)

quota_provider_source = Path(__file__).resolve().parent / 'plugin_quota_provider.go'
quota_provider_target = ROOT / 'internal/pluginhost/quota_provider.go'
write(quota_provider_target, re.sub(r'github\.com/router-for-me/CLIProxyAPI/v\d+', MODULE_PATH, read_text(quota_provider_source)))
quota_provider_test_source = Path(__file__).resolve().parent / 'plugin_quota_provider_test.go'
quota_provider_test_target = ROOT / 'internal/pluginhost/quota_provider_test.go'
write(quota_provider_test_target, re.sub(r'github\.com/router-for-me/CLIProxyAPI/v\d+', MODULE_PATH, read_text(quota_provider_test_source)))

service_models = ROOT / 'sdk/cliproxy/service_models.go'
replace_once(
    service_models,
    '''\tif ctx.Err() != nil {
\t\treturn
\t}
\tmodels = applyOAuthModelAliasForAuth(s.cfg, provider, authKind, a.Attributes, models)
''',
    '''\tif ctx.Err() != nil {
\t\treturn
\t}
\tmodels = s.applyOAuthPolicy(ctx, a, models)
\tmodels = applyOAuthModelAliasForAuth(s.cfg, provider, authKind, a.Attributes, models)
''',
    'models = s.applyOAuthPolicy(ctx, a, models)',
)
insert_before(
    service_models,
    'func (s *Service) oauthExcludedModels(provider, authKind string) []string {\n',
    '''func (s *Service) applyOAuthPolicy(ctx context.Context, auth *coreauth.Auth, models []*ModelInfo) []*ModelInfo {
\tif s == nil || s.proApp == nil || auth == nil || len(models) == 0 {
\t\treturn models
\t}
\treturn s.proApp.FilterModels(ctx, s.cfg, auth, models, s.coreManager)
}

''',
    'func (s *Service) applyOAuthPolicy',
)

service_auth = ROOT / 'sdk/cliproxy/service_auth.go'
replace_once(
    service_auth,
    '''\tGlobalModelRegistry().UnregisterClient(id)
\ts.coreManager.Remove(ctx, id)
''',
    '''\tGlobalModelRegistry().UnregisterClient(id)
\tif s.proApp != nil {
\t\ts.proApp.ForgetAccountPolicy(id)
\t}
\ts.coreManager.Remove(ctx, id)
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
replace_once(
    service_executors,
    '''func (s *Service) registerResolvedModelsForAuth(a *coreauth.Auth, providerKey string, models []*ModelInfo) {
\tif a == nil || a.ID == "" {
\t\treturn
\t}
''',
    '''func (s *Service) registerResolvedModelsForAuth(ctx context.Context, a *coreauth.Auth, providerKey string, models []*ModelInfo) {
\tif a == nil || a.ID == "" {
\t\treturn
\t}
\tif !s.withCurrentAuthModelCommit(ctx, a, func() {
\t\ts.registerResolvedModelsForCurrentAuth(a, providerKey, models)
\t}) {
\t\treturn
\t}
}

func (s *Service) registerResolvedModelsForCurrentAuth(a *coreauth.Auth, providerKey string, models []*ModelInfo) {
''',
    'func (s *Service) registerResolvedModelsForCurrentAuth',
)
replace_once(
    service_executors,
    '''\tmodels := applyExcludedModels(result.Models, activeExcluded)
\tmodels = applyOAuthModelAliasForAuth(s.cfg, providerKey, activeAuthKind, activeAuth.Attributes, models)
''',
    '''\tmodels := applyExcludedModels(result.Models, activeExcluded)
\tmodels = s.applyOAuthPolicy(ctx, activeAuth, models)
\tmodels = applyOAuthModelAliasForAuth(s.cfg, providerKey, activeAuthKind, activeAuth.Attributes, models)
''',
    'models = s.applyOAuthPolicy(ctx, activeAuth, models)',
)

replace_once(
    service_executors,
    's.registerResolvedModelsForAuth(activeAuth, providerKey, applyModelPrefixes(models, activeAuth.Prefix, s.cfg != nil && s.cfg.ForceModelPrefix))',
    's.registerResolvedModelsForAuth(ctx, activeAuth, providerKey, applyModelPrefixes(models, activeAuth.Prefix, s.cfg != nil && s.cfg.ForceModelPrefix))',
    's.registerResolvedModelsForAuth(ctx, activeAuth, providerKey',
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

func (s *Service) updateAuthForCurrentModelRegistration(ctx context.Context, auth, updated *coreauth.Auth) (*coreauth.Auth, bool) {
\tif s == nil || s.coreManager == nil || updated == nil {
\t\treturn nil, false
\t}
\tvar result *coreauth.Auth
\tvar errUpdate error
\tif !s.withCurrentAuthModelCommit(ctx, auth, func() {
\t\tresult, errUpdate = s.coreManager.Update(ctx, updated)
\t}) || errUpdate != nil || result == nil {
\t\treturn nil, false
\t}
\treturn result, true
}

func (s *Service) unregisterModelsForCurrentAuth(ctx context.Context, auth *coreauth.Auth) {
\t_ = s.withCurrentAuthModelCommit(ctx, auth, func() {
\t\tGlobalModelRegistry().UnregisterClient(auth.ID)
\t})
}

''',
    'func (s *Service) withCurrentAuthModelCommit',
)

service_models_text = read(service_models)
service_models_text = service_models_text.replace(
    '''\tif ctx.Err() != nil {
\t\treturn
\t}
\tif a.Disabled {
''',
    '''\tif ctx.Err() != nil {
\t\treturn
\t}
\tctx = s.beginAuthModelRegistration(ctx, a.ID)
\tif !s.isCurrentAuthModelRegistration(ctx, a) {
\t\treturn
\t}
\tif a.Disabled {
''',
    1,
)
unregister_model_call = '\tGlobalModelRegistry().UnregisterClient(a.ID)'
if service_models_text.count(unregister_model_call) != 5:
    raise SystemExit(
        f'expected five auth model unregister calls in {service_models}, '
        f'found {service_models_text.count(unregister_model_call)}'
    )
write(
    service_models,
    service_models_text.replace(
        unregister_model_call,
        '\ts.unregisterModelsForCurrentAuth(ctx, a)',
    ).replace(
        's.registerResolvedModelsForAuth(a,',
        's.registerResolvedModelsForAuth(ctx, a,',
    ),
)

replace_once(
    service_executors,
    '\tGlobalModelRegistry().UnregisterClient(activeAuth.ID)\n\treturn true\n',
    '\ts.unregisterModelsForCurrentAuth(ctx, activeAuth)\n\treturn true\n',
    's.unregisterModelsForCurrentAuth(ctx, activeAuth)',
)

replace_once(
    service_executors,
    '''\t\tif updated, errUpdate := s.coreManager.Update(ctx, result.Auth); errUpdate == nil && updated != nil {
\t\t\tactiveAuth = updated.Clone()
\t\t}
''',
    '''\t\tif updated, committed := s.updateAuthForCurrentModelRegistration(ctx, a, result.Auth); committed {
\t\t\tactiveAuth = updated.Clone()
\t\t}
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
\t\t\ttasks = append(tasks, modelRegistrationTask{
''',
    '''\t\t\tauthForRegistration := auth
\t\t\tauthRegistrationCtx := s.beginAuthModelRegistration(registrationCtx, authForRegistration.ID)
\t\t\ttasks = append(tasks, modelRegistrationTask{
''',
    'authRegistrationCtx := s.beginAuthModelRegistration(registrationCtx, authForRegistration.ID)',
)
replace_once(
    service_auth,
    's.completeModelRegistrationForAuthWithCache(registrationCtx, authForRegistration, compatCache)',
    's.completeModelRegistrationForAuthWithCache(authRegistrationCtx, authForRegistration, compatCache)',
    's.completeModelRegistrationForAuthWithCache(authRegistrationCtx, authForRegistration, compatCache)',
)

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

plugin_quota_management = ROOT / 'internal/api/handlers/management/plugin_quota.go'
write(plugin_quota_management, re.sub(r'github\.com/router-for-me/CLIProxyAPI/v\d+', MODULE_PATH, read_text(Path(__file__).resolve().parent / 'plugin_quota_management.go')))
plugin_quota_management_test = ROOT / 'internal/api/handlers/management/plugin_quota_test.go'
write(plugin_quota_management_test, read_text(Path(__file__).resolve().parent / 'plugin_quota_management_test.go'))

for source_name, target_name in (
    ('account_inspection_host.go', 'account_inspection_host.go'),
    ('auth_file_connection.go', 'auth_file_connection.go'),
    ('auth_file_connection_test.go', 'auth_file_connection_test.go'),
    ('pro_auth_mutation.go', 'pro_auth_mutation.go'),
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
    '\tappliedReloadGeneration uint64\n\tconfigGeneration        uint64\n',
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
replace_once(
    management_handler_source,
    '''\th.mu.Lock()
\th.cfg = cfg
\th.mu.Unlock()
''',
    '''\th.mu.Lock()
\th.cfg = cfg
\th.configGeneration++
\tapplication := h.proApp
\tvar apiKeys []string
\tif cfg != nil {
\t\tapiKeys = append(apiKeys, cfg.APIKeys...)
\t}
\th.mu.Unlock()
\tif application != nil && application.APIKeyPolicy() != nil {
\t\tapplication.APIKeyPolicy().SetConfiguredAPIKeys(apiKeys)
\t}
''',
    'application.APIKeyPolicy().SetConfiguredAPIKeys(apiKeys)',
)
replace_once(
    management_handler_source,
    '''\t\tallowed, statusCode, errMsg := h.AuthenticateManagementKey(clientIP, localClient, provided)
\t\tif !allowed {
''',
    '''\t\tallowed, statusCode, errMsg := h.AuthenticateManagementKey(clientIP, localClient, provided)
\t\tif !allowed {
''',
    'AuthenticateManagementKey(clientIP, localClient, provided)',
)
replace_once(
    management_handler_source,
    '''\t\t\tc.AbortWithStatusJSON(statusCode, gin.H{"error": errMsg})
\t\t\treturn
\t\t}
\t\tc.Next()
''',
    '''\t\t\tc.AbortWithStatusJSON(statusCode, gin.H{"error": errMsg})
\t\t\treturn
\t\t}
\t\tsessionSum := sha256.Sum256([]byte(clientIP + "\\x00" + provided))
\t\tc.Set(apiKeyPolicyManagementSessionContextKey, hex.EncodeToString(sessionSum[:]))
\t\tc.Next()
''',
    'apiKeyPolicyManagementSessionContextKey',
)

insert_before(
    management_handler_source,
    'type attemptInfo struct {\n',
    'const apiKeyPolicyManagementSessionContextKey = "apiKeyPolicyManagementSession"\n\n',
    'const apiKeyPolicyManagementSessionContextKey',
)

replace_once(
    management_handler_source,
    '''\tif err := config.SaveConfigPreserveComments(h.configFilePath, h.cfg); err != nil {
''',
    '''\tif err := config.SaveConfigPreserveComments(h.configFilePath, h.cfg); err != nil {
''',
    'config.SaveConfigPreserveComments(h.configFilePath, h.cfg)',
)
replace_once(
    management_handler_source,
    '''\t\treturn false
\t}
\tsnapshot := h.reloadSnapshotConfigLocked()
''',
    '''\t\treturn false
\t}
\th.configGeneration++
\tsnapshot := h.reloadSnapshotConfigLocked()
''',
    'h.configGeneration++\n\tsnapshot := h.reloadSnapshotConfigLocked()',
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
    '\ts.mgmt.SetPluginHost(optionState.pluginHost)\n\ts.mgmt.SetProApp(optionState.proApp)\n',
    's.mgmt.SetProApp(optionState.proApp)',
)
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
replace_once(
    server_middleware_source,
    '''func AuthMiddleware(manager *sdkaccess.Manager) gin.HandlerFunc {
\treturn accessAuthMiddleware(manager, false)
}

func realtimeStandardAuthMiddleware(manager *sdkaccess.Manager) gin.HandlerFunc {
\treturn accessAuthMiddleware(manager, true)
}

func accessAuthMiddleware(manager *sdkaccess.Manager, realtimeError bool) gin.HandlerFunc {
''',
    '''func AuthMiddleware(manager *sdkaccess.Manager, policy *apikeypolicy.Service) gin.HandlerFunc {
\treturn accessAuthMiddleware(manager, policy, false)
}

func realtimeStandardAuthMiddleware(manager *sdkaccess.Manager, policy *apikeypolicy.Service) gin.HandlerFunc {
\treturn accessAuthMiddleware(manager, policy, true)
}

func accessAuthMiddleware(manager *sdkaccess.Manager, policy *apikeypolicy.Service, realtimeError bool) gin.HandlerFunc {
''',
    'func AuthMiddleware(manager *sdkaccess.Manager, policy *apikeypolicy.Service)',
)
replace_once(
    server_middleware_source,
    '''\t\tif err == nil {
\t\t\tif result != nil {
\t\t\t\tc.Set("userApiKey", result.Principal)
\t\t\t\tc.Set("accessProvider", result.Provider)
\t\t\t\tif len(result.Metadata) > 0 {
\t\t\t\t\tc.Set("accessMetadata", result.Metadata)
\t\t\t\t}
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
\t\t\tc.Next()
\t\t\treturn
\t\t}
''',
    'requestCtx = apikeypolicy.WithDecision(requestCtx, decision)',
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
\t\t\t\twriteAPIKeyPolicyMiddlewareError(c, true, decisionErr)
\t\t\t\treturn
\t\t\t}
\t\t\trequestCtx := apikeypolicy.WithIdentity(c.Request.Context(), identity)
\t\t\trequestCtx = apikeypolicy.WithDecision(requestCtx, decision)
\t\t\tc.Request = c.Request.WithContext(requestCtx)
\t\t}
''',
    'authorization.IssuerAPIKeyIdentity',
)

server_routes_source = ROOT / 'internal/api/server_routes.go'
routes_text = read(server_routes_source)
routes_text = routes_text.replace('AuthMiddleware(s.accessManager)', 'AuthMiddleware(s.accessManager, s.apiKeyPolicy)')
routes_text = routes_text.replace('realtimeAuthMiddleware(s.accessManager, s.codexLiveHandler)', 'realtimeAuthMiddleware(s.accessManager, s.apiKeyPolicy, s.codexLiveHandler)')
routes_text = routes_text.replace('realtimeStandardAuthMiddleware(s.accessManager)', 'realtimeStandardAuthMiddleware(s.accessManager, s.apiKeyPolicy)')
write(server_routes_source, routes_text)

insert_before(
    server_middleware_source,
    '// corsMiddleware returns a Gin middleware handler',
    '''func apiKeyQuotaMiddleware(policy *apikeypolicy.Service) gin.HandlerFunc {
\treturn func(c *gin.Context) {
\t\tif policy == nil || c == nil || c.Request == nil {
\t\t\tc.Next()
\t\t\treturn
\t\t}
\t\tpath := c.Request.URL.Path
\t\tgeminiModelPath := strings.TrimPrefix(path, "/v1beta/models/")
\t\tgeminiModelDiscovery := geminiModelPath != path && geminiModelPath != "" && !strings.Contains(geminiModelPath, ":")
\t\tif c.Request.Method == http.MethodGet && (path == "/v1/models" || path == "/v1beta/models" || geminiModelDiscovery || strings.HasPrefix(path, "/v1/live/") || (path == "/v1/realtime" && strings.TrimSpace(c.Query("call_id")) != "")) {
\t\t\tc.Next()
\t\t\treturn
\t\t}
\t\tdecision, ok := apikeypolicy.DecisionFromContext(c.Request.Context())
\t\tif !ok {
\t\t\tc.Next()
\t\t\treturn
\t\t}
\t\tupgrade := strings.EqualFold(strings.TrimSpace(c.GetHeader("Upgrade")), "websocket")
\t\tdeferredRealtime := c.Request.Method == http.MethodPost && (path == "/v1/realtime" || path == "/v1/realtime/calls")
\t\tif upgrade && (path == "/v1/responses" || path == "/v1/realtime") || deferredRealtime {
\t\t\trequestCtx := apikeypolicy.WithQuotaAdmission(c.Request.Context(), policy.AdmitDecision)
\t\t\tc.Request = c.Request.WithContext(requestCtx)
\t\t\tc.Next()
\t\t\treturn
\t\t}
\t\tadmitted, err := policy.AdmitDecision(c.Request.Context(), decision)
\t\tif err != nil {
\t\t\twriteAPIKeyPolicyMiddlewareError(c, strings.HasPrefix(c.Request.URL.Path, "/v1/realtime"), err)
\t\t\treturn
\t\t}
\t\tc.Request = c.Request.WithContext(apikeypolicy.WithDecision(c.Request.Context(), admitted))
\t\tc.Next()
\t}
}

''',
    'func apiKeyQuotaMiddleware(policy *apikeypolicy.Service)',
)

routes_text = read(server_routes_source)
for auth_line in (
    'v1.Use(AuthMiddleware(s.accessManager, s.apiKeyPolicy))',
    'openaiV1.Use(AuthMiddleware(s.accessManager, s.apiKeyPolicy))',
    'codexDirect.Use(AuthMiddleware(s.accessManager, s.apiKeyPolicy))',
    'v1beta.Use(AuthMiddleware(s.accessManager, s.apiKeyPolicy))',
):
    if routes_text.count(auth_line) != 1:
        raise SystemExit(f'expected one quota group anchor: {auth_line}')
    group_name = auth_line.split('.')[0]
    routes_text = routes_text.replace(auth_line, auth_line + '\n\t' + group_name + '.Use(apiKeyQuotaMiddleware(s.apiKeyPolicy))', 1)
for route in (
    's.engine.GET("/v1/realtime", realtimeAuth, s.codexLiveHandler.HandleRealtimeWebsocket)',
    's.engine.POST("/v1/realtime", realtimeAuth, s.codexLiveHandler.Handle)',
    's.engine.POST("/v1/realtime/calls", realtimeAuth, s.codexLiveHandler.Handle)',
    's.engine.GET("/v1/realtime/translations", realtimeAuth, s.codexLiveHandler.HandleTranslation)',
    's.engine.POST("/v1/realtime/translations", realtimeAuth, s.codexLiveHandler.HandleTranslation)',
):
    if routes_text.count(route) != 1:
        raise SystemExit(f'expected one realtime quota route anchor: {route}')
    routes_text = routes_text.replace(route, route.replace(', realtimeAuth,', ', realtimeAuth, apiKeyQuotaMiddleware(s.apiKeyPolicy),'), 1)
write(server_routes_source, routes_text)

responses_websocket_source = ROOT / 'sdk/api/handlers/openai/openai_responses_websocket.go'
add_go_import(
    responses_websocket_source,
    f'\t"{MODULE_PATH}/internal/interfaces"\n',
    f'\tapikeypolicy "{MODULE_PATH}/internal/pro/apikeypolicy"\n',
)
replace_once(
    responses_websocket_source,
    '''\t\t\tcontinue
\t\t}

\t\tvar toolCacheTurn *responsesWebsocketToolCacheTurn
''',
    '''\t\t\tcontinue
\t\t}

\t\texecutionParent, errAdmission := apikeypolicy.AdmitQuotaTurn(executionParent)
\t\tif errAdmission != nil {
\t\t\terrMsg := handlers.ExecutionErrorMessage(errAdmission)
\t\t\tif errors.Is(errAdmission, apikeypolicy.ErrQuotaUnavailable) {
\t\t\t\terrMsg.StatusCode = http.StatusServiceUnavailable
\t\t\t}
\t\t\th.LoggingAPIResponseError(context.WithValue(context.Background(), "gin", c), errMsg)
\t\t\tmarkAPIResponseTimestamp(c)
\t\t\tif _, errWrite := writeResponsesWebsocketError(writer, wsTimelineLog, errMsg); errWrite != nil {
\t\t\t\twsTerminateErr = errWrite
\t\t\t\treturn
\t\t\t}
\t\t\tcontinue
\t\t}

\t\tvar toolCacheTurn *responsesWebsocketToolCacheTurn
''',
    'apikeypolicy.AdmitQuotaTurn(executionParent)',
)

realtime_websocket_source = ROOT / 'internal/client/codex/live/websocket.go'
add_go_import(realtime_websocket_source, '\t"encoding/json"\n', '\t"errors"\n')
add_go_import(realtime_websocket_source, '\t"errors"\n', '\t"fmt"\n')
add_go_import(realtime_websocket_source, '\t"strings"\n', '\t"sync"\n')
add_go_import(realtime_websocket_source, '\t"sync"\n', '\t"time"\n')
add_go_import(
    realtime_websocket_source,
    f'\t"{MODULE_PATH}/internal/logging"\n',
    f'\tapikeypolicy "{MODULE_PATH}/internal/pro/apikeypolicy"\n',
)
replace_once(
    realtime_websocket_source,
	'''\tif len(tokenSession) > 0 {
\t\ttokenModel := codexRealtimeModel(modelFromJSON(tokenSession))
\t\tif selectionModel != tokenModel {
\t\t\twriteRealtimeError(c, http.StatusForbidden, "Realtime client secret is not valid for the requested model", "invalid_request_error", "realtime_client_secret_scope_mismatch")
\t\t\treturn
\t\t}
\t}
\tctx := context.WithValue(c.Request.Context(), "gin", c)
''',
	'''\tpolicyCtx, effectiveModel, errPolicy := applyRealtimeAPIKeyPolicy(c.Request.Context(), requestedModel)
\tif errPolicy != nil {
\t\twriteRealtimeAPIKeyPolicyError(c, errPolicy)
\t\treturn
\t}
\trequestedModel = effectiveModel
\tselectionModel = codexRealtimeModel(requestedModel)
\tif len(tokenSession) > 0 {
\t\ttokenModel := codexRealtimeModel(modelFromJSON(tokenSession))
\t\tif selectionModel != tokenModel {
\t\t\twriteRealtimeError(c, http.StatusForbidden, "Realtime client secret is not valid for the requested model", "invalid_request_error", "realtime_client_secret_scope_mismatch")
\t\t\treturn
\t\t}
\t}
\tctx := context.WithValue(policyCtx, "gin", c)
''',
	'policyCtx, effectiveModel, errPolicy := applyRealtimeAPIKeyPolicy',
)
insert_before(
    realtime_websocket_source,
    'func realtimeSessionUpdate(session json.RawMessage) (json.RawMessage, error) {',
    r'''func applyRealtimeAPIKeyPolicy(ctx context.Context, requestedModel string) (context.Context, string, error) {
	decision, configured := apikeypolicy.DecisionFromContext(ctx)
	if !configured {
		return ctx, requestedModel, nil
	}
	effectiveModel, err := decision.ApplyModel(requestedModel)
	if err != nil {
		return ctx, "", err
	}
	if err = decision.AllowsProvider("codex"); err != nil {
		return ctx, "", err
	}
	settle := apikeypolicy.QuotaUsageSettlementFromContext(ctx)
	policyCtx := apikeypolicy.WithDecision(ctx, decision.WithModels(requestedModel, effectiveModel))
	return apikeypolicy.WithQuotaUsageSettlement(policyCtx, settle), effectiveModel, nil
}

func writeRealtimeAPIKeyPolicyError(c *gin.Context, err error) {
	status := http.StatusServiceUnavailable
	code := "api_key_policy_unavailable"
	message := "API key policy is unavailable"
	errorType := "server_error"
	if policyErr, ok := err.(*apikeypolicy.PolicyError); ok {
		status = http.StatusForbidden
		code = policyErr.Code
		message = policyErr.Message
		errorType = "permission_error"
	}
	writeRealtimeError(c, status, message, errorType, code)
}

func writeRealtimeQuotaAdmissionError(c *gin.Context, err error) {
	status := http.StatusServiceUnavailable
	code := "api_key_quota_unavailable"
	errorType := "server_error"
	if _, ok := err.(*apikeypolicy.QuotaExceededError); ok {
		status = http.StatusTooManyRequests
		code = "api_key_quota_exceeded"
		errorType = "rate_limit_error"
	}
	writeRealtimeError(c, status, err.Error(), errorType, code)
}

''',
    'func applyRealtimeAPIKeyPolicy(',
)
replace_once(
    realtime_websocket_source,
    '\tif errRelay := relayWebsockets(downstream, upstream); errRelay != nil && !isNormalWebsocketClose(errRelay) {\n',
    '\tif errRelay := relayRealtimeWebsockets(ctx, downstream, upstream, requestedModel); errRelay != nil && !isNormalWebsocketClose(errRelay) {\n',
    'relayRealtimeWebsockets(ctx, downstream, upstream, requestedModel)',
)
insert_before(
    realtime_websocket_source,
    'func realtimeSessionUpdate(session json.RawMessage) (json.RawMessage, error) {',
    r'''type realtimeQuotaTurn struct {
	ctx     context.Context
	eventID string
	model   string
}

type realtimeQuotaState struct {
	mu     sync.Mutex
	active *realtimeQuotaTurn
}

func relayRealtimeWebsockets(ctx context.Context, downstream, upstream *websocket.Conn, billingModel string) error {
	state := &realtimeQuotaState{}
	downstreamWriteMu := &sync.Mutex{}
	results := make(chan error, 2)
	go func() { results <- copyRealtimeDownstream(ctx, downstream, upstream, state, downstreamWriteMu, billingModel) }()
	go func() { results <- copyRealtimeUpstream(ctx, downstream, upstream, state, downstreamWriteMu) }()

	firstErr := <-results
	closeCode, closeReason := websocketCloseDetails(firstErr)
	payload := websocket.FormatCloseMessage(closeCode, closeReason)
	downstreamWriteMu.Lock()
	_ = downstream.WriteControl(websocket.CloseMessage, payload, time.Time{})
	downstreamWriteMu.Unlock()
	_ = upstream.WriteControl(websocket.CloseMessage, payload, time.Time{})
	_ = downstream.Close()
	_ = upstream.Close()
	<-results
	return firstErr
}

func copyRealtimeDownstream(ctx context.Context, downstream, upstream *websocket.Conn, state *realtimeQuotaState, downstreamWriteMu *sync.Mutex, billingModel string) error {
	for {
		messageType, payload, errRead := downstream.ReadMessage()
		if errRead != nil {
			return errRead
		}
		if messageType == websocket.TextMessage || messageType == websocket.BinaryMessage {
			var event struct{ Type string `json:"type"` }
			if json.Unmarshal(payload, &event) == nil && strings.TrimSpace(event.Type) == "response.create" {
				state.mu.Lock()
				busy := state.active != nil
				state.mu.Unlock()
				if busy {
					writeRealtimeQuotaError(downstream, downstreamWriteMu, errors.New("a realtime response is already active"), "realtime_response_in_progress")
					continue
				}
				turnCtx, errAdmission := apikeypolicy.AdmitQuotaTurn(ctx)
				if errAdmission != nil {
					writeRealtimeQuotaError(downstream, downstreamWriteMu, errAdmission, "api_key_quota_exceeded")
					continue
				}
				if decision, ok := apikeypolicy.DecisionFromContext(turnCtx); ok {
					if _, charged := decision.QuotaAttribution(); charged {
						state.mu.Lock()
						state.active = &realtimeQuotaTurn{ctx: turnCtx, eventID: fmt.Sprintf("realtime:%d", time.Now().UnixNano()), model: strings.TrimSpace(billingModel)}
						state.mu.Unlock()
					}
				}
			}
		}
		if errWrite := upstream.WriteMessage(messageType, payload); errWrite != nil {
			return errWrite
		}
	}
}

func copyRealtimeUpstream(ctx context.Context, downstream, upstream *websocket.Conn, state *realtimeQuotaState, downstreamWriteMu *sync.Mutex) error {
	for {
		messageType, payload, errRead := upstream.ReadMessage()
		if errRead != nil {
			return errRead
		}
		if messageType == websocket.TextMessage || messageType == websocket.BinaryMessage {
			var event struct {
				Type     string `json:"type"`
				Response struct{ ID string `json:"id"` } `json:"response"`
			}
			if json.Unmarshal(payload, &event) == nil && (event.Type == "response.done" || event.Type == "response.completed") {
				state.mu.Lock()
				turn := state.active
				state.active = nil
				state.mu.Unlock()
				if turn != nil {
					detail, ok := helps.ParseCodexUsage(payload)
					if !ok {
						detail = helps.ParseOpenAIUsage(payload)
					}
					totalTokens := detail.TokenBreakdown.TotalTokens
					if totalTokens == 0 {
						totalTokens = detail.TotalTokens
					}
					eventID := turn.eventID
					if responseID := strings.TrimSpace(event.Response.ID); responseID != "" {
						eventID += ":response=" + responseID
					}
					if errSettle := apikeypolicy.SettleQuotaUsage(turn.ctx, eventID, apikeypolicy.QuotaUsageDelta{
						Provider: "codex", Model: turn.model, InputTokens: detail.InputTokens,
						OutputTokens: detail.OutputTokens, ReasoningTokens: detail.ReasoningTokens,
						CachedTokens: detail.CachedTokens, CacheReadTokens: detail.CacheReadTokens,
						CacheWriteTokens: detail.CacheCreationTokens, TotalTokens: totalTokens,
						EffectiveServiceTier: detail.ResponseServiceTier, EffectiveSpeed: detail.ResponseSpeed,
					}); errSettle != nil {
						log.WithError(errSettle).Error("failed to settle realtime API key quota usage")
					}
				}
			}
		}
		downstreamWriteMu.Lock()
		errWrite := downstream.WriteMessage(messageType, payload)
		downstreamWriteMu.Unlock()
		if errWrite != nil {
			return errWrite
		}
	}
}

func writeRealtimeQuotaError(downstream *websocket.Conn, writeMu *sync.Mutex, err error, code string) {
	errorType := "rate_limit_error"
	if errors.Is(err, apikeypolicy.ErrQuotaUnavailable) {
		errorType = "server_error"
		code = "api_key_quota_unavailable"
	}
	payload, _ := json.Marshal(map[string]any{
		"type": "error",
		"error": map[string]any{"type": errorType, "code": code, "message": err.Error(), "param": nil},
	})
	writeMu.Lock()
	_ = downstream.WriteMessage(websocket.TextMessage, payload)
	writeMu.Unlock()
}

''',
    'func relayRealtimeWebsockets(',
)

realtime_live_source = ROOT / 'internal/client/codex/live/live.go'
add_go_import(
    realtime_live_source,
    f'\t"{MODULE_PATH}/internal/logging"\n',
    f'\tapikeypolicy "{MODULE_PATH}/internal/pro/apikeypolicy"\n',
)
replace_once(
    realtime_live_source,
    '''\tif errPayload == nil {
\t\tupstreamBody, model, errPayload = rewriteCallRequestModel(upstreamBody, upstreamContentType, model)
\t}
''',
    '''\tif errPayload == nil {
\t\tpolicyCtx, effectiveModel, errPolicy := applyRealtimeAPIKeyPolicy(c.Request.Context(), model)
\t\tif errPolicy != nil {
\t\t\twriteRealtimeAPIKeyPolicyError(c, errPolicy)
\t\t\treturn
\t\t}
\t\tmodel = effectiveModel
\t\tadmittedCtx, errAdmission := apikeypolicy.AdmitQuotaTurn(policyCtx)
\t\tif errAdmission != nil {
\t\t\twriteRealtimeQuotaAdmissionError(c, errAdmission)
\t\t\treturn
\t\t}
\t\tc.Request = c.Request.WithContext(admittedCtx)
\t}
\tquotaModel := model
\tif errPayload == nil {
\t\tupstreamBody, model, errPayload = rewriteCallRequestModel(upstreamBody, upstreamContentType, model)
\t}
''',
    'quotaModel := model',
)
replace_once(
    realtime_live_source,
    '''\t\t\tsession := liveSession{authID: selected.ID, model: model, media: mediaSession}
''',
    '''\t\t\tsession := liveSession{
\t\t\t\tauthID: selected.ID, model: model, media: mediaSession,
\t\t\t\tquotaModel: quotaModel, quotaSettlement: apikeypolicy.QuotaUsageSettlementFromContext(c.Request.Context()),
\t\t\t}
''',
    'quotaSettlement: apikeypolicy.QuotaUsageSettlementFromContext',
)

realtime_sideband_source = ROOT / 'internal/client/codex/live/sideband.go'
add_go_import(realtime_sideband_source, '\t"context"\n', '\t"encoding/json"\n')
add_go_import(realtime_sideband_source, '\t"context"\n', '\t"crypto/sha256"\n')
add_go_import(realtime_sideband_source, '\t"errors"\n', '\t"fmt"\n')
add_go_import(
    realtime_sideband_source,
    f'\t"{MODULE_PATH}/internal/logging"\n',
    f'\tapikeypolicy "{MODULE_PATH}/internal/pro/apikeypolicy"\n',
)
replace_once(
    realtime_sideband_source,
    '''\tclientSecretPrincipal string
\thomeSelection         *auth.HomeDispatchSelection
''',
    '''\tclientSecretPrincipal string
\tquotaModel           string
\tquotaSettlement      func(context.Context, string, apikeypolicy.QuotaUsageDelta) error
\thomeSelection         *auth.HomeDispatchSelection
''',
    'quotaSettlement func(context.Context, string, apikeypolicy.QuotaUsageDelta) error',
)
replace_once(
    realtime_sideband_source,
    '''\tif errRelay := relayWebsockets(downstream, upstream); errRelay != nil && !isNormalWebsocketClose(errRelay) {
''',
    '''\tif errRelay := relaySidebandQuotaWebsockets(ctx, downstream, upstream, session); errRelay != nil && !isNormalWebsocketClose(errRelay) {
''',
    'relaySidebandQuotaWebsockets(ctx, downstream, upstream, session)',
)
insert_before(
    realtime_sideband_source,
    'func sidebandTarget(c *gin.Context) (sidebandStyle, string, bool) {',
    r'''func relaySidebandQuotaWebsockets(ctx context.Context, downstream, upstream *websocket.Conn, session liveSession) error {
	if session.quotaSettlement == nil {
		return relayWebsockets(downstream, upstream)
	}
	results := make(chan error, 2)
	go func() { results <- copyWebsocket(upstream, downstream) }()
	go func() {
		for {
			messageType, payload, errRead := upstream.ReadMessage()
			if errRead != nil {
				results <- errRead
				return
			}
			var event struct {
				Type string `json:"type"`
				Response struct{ ID string `json:"id"` } `json:"response"`
			}
			if json.Unmarshal(payload, &event) == nil && (event.Type == "response.done" || event.Type == "response.completed") {
				detail, ok := helps.ParseCodexUsage(payload)
				if !ok {
					detail = helps.ParseOpenAIUsage(payload)
				}
				totalTokens := detail.TokenBreakdown.TotalTokens
				if totalTokens == 0 {
					totalTokens = detail.TotalTokens
				}
				eventID := "webrtc:" + session.callID + ":response=" + strings.TrimSpace(event.Response.ID)
				if strings.TrimSpace(event.Response.ID) == "" {
					eventID = fmt.Sprintf("webrtc:%s:payload=%x", session.callID, sha256.Sum256(payload))
				}
				if errSettle := session.quotaSettlement(context.WithoutCancel(ctx), eventID, apikeypolicy.QuotaUsageDelta{
					Provider: "codex", Model: session.quotaModel, InputTokens: detail.InputTokens,
					OutputTokens: detail.OutputTokens, ReasoningTokens: detail.ReasoningTokens,
					CachedTokens: detail.CachedTokens, CacheReadTokens: detail.CacheReadTokens,
					CacheWriteTokens: detail.CacheCreationTokens, TotalTokens: totalTokens,
					EffectiveServiceTier: detail.ResponseServiceTier, EffectiveSpeed: detail.ResponseSpeed,
				}); errSettle != nil {
					log.WithError(errSettle).Error("failed to settle WebRTC API key quota usage")
				}
			}
			if errWrite := downstream.WriteMessage(messageType, payload); errWrite != nil {
				results <- errWrite
				return
			}
		}
	}()
	firstErr := <-results
	closeCode, closeReason := websocketCloseDetails(firstErr)
	payload := websocket.FormatCloseMessage(closeCode, closeReason)
	_ = downstream.WriteControl(websocket.CloseMessage, payload, time.Time{})
	_ = upstream.WriteControl(websocket.CloseMessage, payload, time.Time{})
	_ = downstream.Close()
	_ = upstream.Close()
	<-results
	return firstErr
}

''',
    'func relaySidebandQuotaWebsockets(',
)

client_secret_source = ROOT / 'internal/client/codex/live/client_secret.go'
add_go_import(client_secret_source, '"github.com/gin-gonic/gin"\n', '\tapikeypolicy "' + import_path('internal/pro/apikeypolicy') + '"\n')
replace_once(
    client_secret_source,
    '''type ClientSecretAuthorization struct {
\tPrincipal       string
\tIssuerPrincipal string
\tIssuerProvider  string
\tSession         json.RawMessage
}
''',
    '''type ClientSecretAuthorization struct {
\tPrincipal           string
\tIssuerPrincipal     string
\tIssuerProvider      string
\tIssuerAPIKeyIdentity apikeypolicy.AuthenticatedAPIKeyIdentity
\tSession             json.RawMessage
}
''',
    'IssuerAPIKeyIdentity apikeypolicy.AuthenticatedAPIKeyIdentity',
)
replace_once(
    client_secret_source,
    'func (s *clientSecretStore) create(session json.RawMessage, lifetime time.Duration, issuerPrincipal, issuerProvider string) (string, ClientSecretAuthorization, time.Time, error) {\n',
    'func (s *clientSecretStore) create(session json.RawMessage, lifetime time.Duration, issuerPrincipal, issuerProvider string, issuerIdentities ...apikeypolicy.AuthenticatedAPIKeyIdentity) (string, ClientSecretAuthorization, time.Time, error) {\n',
    'issuerIdentities ...apikeypolicy.AuthenticatedAPIKeyIdentity',
)
replace_once(
    client_secret_source,
    '''\tauthorization := ClientSecretAuthorization{
''',
    '''\tissuerIdentity := apikeypolicy.AuthenticatedAPIKeyIdentity{}
\tif len(issuerIdentities) > 0 {
\t\tissuerIdentity = issuerIdentities[0]
\t}
\tauthorization := ClientSecretAuthorization{
''',
    'issuerIdentity = issuerIdentities[0]',
)
replace_once(
    client_secret_source,
    '''\t\tIssuerPrincipal: strings.TrimSpace(issuerPrincipal),
\t\tIssuerProvider:  strings.TrimSpace(issuerProvider),
\t\tSession:         append(json.RawMessage(nil), session...),
''',
    '''\t\tIssuerPrincipal:      strings.TrimSpace(issuerPrincipal),
\t\tIssuerProvider:       strings.TrimSpace(issuerProvider),
\t\tIssuerAPIKeyIdentity: issuerIdentity,
\t\tSession:              append(json.RawMessage(nil), session...),
''',
    'IssuerAPIKeyIdentity: issuerIdentity',
)
replace_once(
    client_secret_source,
    '''\tissuerPrincipalValue, _ := issuerPrincipal.(string)
\tissuerProviderValue, _ := issuerProvider.(string)
\ttoken, authorization, expiresAt, errCreate := h.clientSecrets.create(upstreamSession, lifetime, issuerPrincipalValue, issuerProviderValue)
''',
    '''\tissuerPrincipalValue, _ := issuerPrincipal.(string)
\tissuerProviderValue, _ := issuerProvider.(string)
\tissuerIdentity, _ := apikeypolicy.IdentityFromContext(c.Request.Context())
\ttoken, authorization, expiresAt, errCreate := h.clientSecrets.create(upstreamSession, lifetime, issuerPrincipalValue, issuerProviderValue, issuerIdentity)
''',
    'issuerIdentity, _ := apikeypolicy.IdentityFromContext',
)

handlers_routing_source = ROOT / 'sdk/api/handlers/handlers_routing.go'
add_go_import(handlers_routing_source, '"' + import_path('internal/interfaces') + '"\n', '\tapikeypolicy "' + import_path('internal/pro/apikeypolicy') + '"\n')
insert_before(
    handlers_routing_source,
    'func (h *BaseAPIHandler) getRequestDetails(modelName string) (providers []string, normalizedModel string, err *interfaces.ErrorMessage) {\n',
    '''func applyAPIKeyModelPolicy(h *BaseAPIHandler, ctx context.Context, modelName string, rawJSON []byte) (context.Context, string, []byte, *interfaces.ErrorMessage) {
\tdecision, configured := apikeypolicy.DecisionFromContext(ctx)
\tif !configured {
\t\treturn ctx, modelName, rawJSON, nil
\t}
\t// A Profile's exact alias contract wins over the host-wide auto resolver.
\t// Passthrough keeps the upstream order unchanged; profile requests first
\t// apply their exact mapping and only resolve an unmapped auto model.
\tparsed := thinking.ParseSuffix(modelName)
\tprofileOwnsMapping := decision.Mode == apikeypolicy.ModeProfile && (decision.HasExactModelMapping(modelName) || (parsed.HasSuffix && decision.HasExactModelMapping(parsed.ModelName)))
\tif profileOwnsMapping {
\t\teffectiveModel, errApply := decision.ApplyModel(modelName)
\t\tif errApply != nil {
\t\t\treturn ctx, "", nil, apiKeyPolicyExecutionError(errApply)
\t\t}
\t\tctx = apikeypolicy.WithDecision(ctx, decision.WithModels(modelName, effectiveModel))
\t\tif effectiveModel == modelName || len(rawJSON) == 0 {
\t\t\treturn ctx, effectiveModel, rawJSON, nil
\t\t}
\t\tupdated, errSet := sjson.SetBytes(rawJSON, "model", effectiveModel)
\t\tif errSet != nil {
\t\t\treturn ctx, "", nil, apiKeyPolicyExecutionError(apikeypolicy.ErrUnavailable)
\t\t}
\t\treturn ctx, effectiveModel, updated, nil
\t}
\tresolvedModel := modelName
\thomeEnabled := h != nil && h.AuthManager != nil && h.AuthManager.HomeEnabled()
\tif parsed.ModelName == "auto" && !homeEnabled {
\t\tresolvedBase := util.ResolveAutoModel(parsed.ModelName)
\t\tif parsed.HasSuffix {
\t\t\tresolvedModel = fmt.Sprintf("%s(%s)", resolvedBase, parsed.RawSuffix)
\t\t} else {
\t\t\tresolvedModel = resolvedBase
\t\t}
\t} else if !homeEnabled {
\t\tresolvedModel = util.ResolveAutoModel(modelName)
\t}
\teffectiveModel, errApply := decision.ApplyModel(resolvedModel)
\tif errApply != nil {
\t\treturn ctx, "", nil, apiKeyPolicyExecutionError(errApply)
\t}
\tctx = apikeypolicy.WithDecision(ctx, decision.WithModels(modelName, effectiveModel))
\tif effectiveModel == modelName || len(rawJSON) == 0 {
\t\treturn ctx, effectiveModel, rawJSON, nil
\t}
\tupdated, errSet := sjson.SetBytes(rawJSON, "model", effectiveModel)
\tif errSet != nil {
\t\treturn ctx, "", nil, apiKeyPolicyExecutionError(apikeypolicy.ErrUnavailable)
\t}
\treturn ctx, effectiveModel, updated, nil
}

func applyAPIKeyRoutedModelPolicy(ctx context.Context, modelName string, rawJSON []byte, routeDecision *modelRouteDecision) (context.Context, string, []byte, *interfaces.ErrorMessage) {
\tif routeDecision == nil || routeDecision.Provider == "" || strings.TrimSpace(routeDecision.Model) == "" {
\t\treturn ctx, modelName, rawJSON, nil
\t}
\tdecision, configured := apikeypolicy.DecisionFromContext(ctx)
\tif !configured {
\t\treturn ctx, modelName, rawJSON, nil
\t}
\teffectiveModel, errApply := decision.ValidateEffectiveModel(routeDecision.Model)
\tif errApply != nil {
\t\treturn ctx, "", nil, apiKeyPolicyExecutionError(errApply)
\t}
\trouteDecision.Model = effectiveModel
\tattribution := decision.UsageAttribution()
\tctx = apikeypolicy.WithDecision(ctx, decision.WithModels(attribution.RequestedModel, effectiveModel))
\tif len(rawJSON) == 0 {
\t\treturn ctx, modelName, rawJSON, nil
\t}
\tupdated, errSet := sjson.SetBytes(rawJSON, "model", effectiveModel)
\tif errSet != nil {
\t\treturn ctx, "", nil, apiKeyPolicyExecutionError(apikeypolicy.ErrUnavailable)
\t}
\treturn ctx, modelName, updated, nil
}

func applyAPIKeyProviderPolicy(ctx context.Context, providers []string) ([]string, *interfaces.ErrorMessage) {
\tdecision, configured := apikeypolicy.DecisionFromContext(ctx)
\tif !configured {
\t\treturn providers, nil
\t}
\tfiltered, errFilter := decision.FilterProviders(providers)
\tif errFilter != nil {
\t\treturn nil, apiKeyPolicyExecutionError(errFilter)
\t}
\treturn filtered, nil
}

func requireAPIKeyExecutionProvider(ctx context.Context, provider string) *interfaces.ErrorMessage {
\tdecision, configured := apikeypolicy.DecisionFromContext(ctx)
\tif !configured {
\t\treturn nil
\t}
\tif errAllowed := decision.AllowsProvider(provider); errAllowed != nil {
\t\treturn apiKeyPolicyExecutionError(errAllowed)
\t}
\treturn nil
}

func apiKeyPolicyExecutionError(err error) *interfaces.ErrorMessage {
\tstatus := http.StatusServiceUnavailable
\tcode := "api_key_policy_unavailable"
\tmessage := "API key policy is unavailable"
\terrorType := "server_error"
\tif policyErr, ok := err.(*apikeypolicy.PolicyError); ok {
\t\tstatus = http.StatusForbidden
\t\tcode = policyErr.Code
\t\tmessage = policyErr.Message
\t\terrorType = "permission_error"
\t}
\tbody := `{"error":{"message":"","type":"","code":""}}`
\tbody, _ = sjson.Set(body, "error.message", message)
\tbody, _ = sjson.Set(body, "error.type", errorType)
\tbody, _ = sjson.Set(body, "error.code", code)
\treturn &interfaces.ErrorMessage{StatusCode: status, Error: errors.New(body)}
}

''',
    'func applyAPIKeyModelPolicy(',
)

handlers_execution_source = ROOT / 'sdk/api/handlers/handlers_execution.go'
insert_before(
    handlers_execution_source,
    '// ExecuteWithAuthManager executes a non-streaming request via the core auth manager.\n',
    '''type pluginExecutorProviderResolver interface {
\tPluginExecutorProvider(string) (string, bool)
}

func (h *BaseAPIHandler) validateAPIKeyPluginExecutor(ctx context.Context, pluginID string) *interfaces.ErrorMessage {
\tdecision, configured := apikeypolicy.DecisionFromContext(ctx)
\tif !configured || decision.Mode == apikeypolicy.ModePassthrough {
\t\treturn nil
\t}
\thost := h.pluginExecutorHost()
\tresolver, ok := host.(pluginExecutorProviderResolver)
\tif !ok || resolver == nil {
\t\treturn apiKeyPolicyExecutionError(&apikeypolicy.PolicyError{Code: "profile_provider_forbidden", Message: "plugin execution provider cannot be resolved for the active API key profile"})
\t}
\tprovider, resolved := resolver.PluginExecutorProvider(pluginID)
\tif !resolved {
\t\treturn apiKeyPolicyExecutionError(&apikeypolicy.PolicyError{Code: "profile_provider_forbidden", Message: "plugin execution provider cannot be resolved for the active API key profile"})
\t}
\treturn requireAPIKeyExecutionProvider(ctx, provider)
}

''',
    'func (h *BaseAPIHandler) validateAPIKeyPluginExecutor(',
)
add_go_import(handlers_execution_source, '"' + import_path('internal/interfaces') + '"\n', '\tapikeypolicy "' + import_path('internal/pro/apikeypolicy') + '"\n')
replace_once(
    handlers_execution_source,
    '''\toriginalRequestedModel := modelName
\trouteDecision := h.applyModelRouter(ctx, entryProtocol, modelName, rawJSON, false, execOptions)
''',
    '''\toriginalRequestedModel := modelName
\tvar policyErr *interfaces.ErrorMessage
\tctx, modelName, rawJSON, policyErr = applyAPIKeyModelPolicy(h, ctx, modelName, rawJSON)
\tif policyErr != nil {
\t\treturn nil, nil, policyErr
\t}
\trouteDecision := h.applyModelRouter(ctx, entryProtocol, modelName, rawJSON, false, execOptions)
\tctx, modelName, rawJSON, policyErr = applyAPIKeyRoutedModelPolicy(ctx, modelName, rawJSON, &routeDecision)
\tif policyErr != nil {
\t\treturn nil, nil, policyErr
\t}
''',
    'ctx, modelName, rawJSON, policyErr = applyAPIKeyModelPolicy(h, ctx, modelName, rawJSON)',
)
replace_once(
    handlers_execution_source,
    '''\tif routeDecision.ExecutorPluginID != "" {
\t\treturn h.executeWithPluginExecutor(ctx, entryProtocol, responseProtocol, modelName, originalRequestedModel, rawJSON, alt, routeDecision.ExecutorPluginID, execOptions)
\t}
''',
    '''\tif routeDecision.ExecutorPluginID != "" {
\t\tif errMsg := h.validateAPIKeyPluginExecutor(ctx, routeDecision.ExecutorPluginID); errMsg != nil {
\t\t\treturn nil, nil, errMsg
\t\t}
\t\treturn h.executeWithPluginExecutor(ctx, entryProtocol, responseProtocol, modelName, originalRequestedModel, rawJSON, alt, routeDecision.ExecutorPluginID, execOptions)
\t}
''',
    'h.validateAPIKeyPluginExecutor(ctx, routeDecision.ExecutorPluginID)',
)
replace_once(
    handlers_execution_source,
    '''\tproviders = adjustExecutionProvidersForEntryProtocol(entryProtocol, providers)
\treqMeta := requestExecutionMetadata(ctx)
''',
    '''\tproviders = adjustExecutionProvidersForEntryProtocol(entryProtocol, providers)
\tproviders, errMsg = applyAPIKeyProviderPolicy(ctx, providers)
\tif errMsg != nil {
\t\treturn nil, nil, errMsg
\t}
\treqMeta := requestExecutionMetadata(ctx)
''',
    'providers, errMsg = applyAPIKeyProviderPolicy(ctx, providers)',
)
replace_once(
    handlers_execution_source,
    '''func (h *BaseAPIHandler) executeCountWithAuthManager(ctx context.Context, handlerType, modelName string, rawJSON []byte, alt string, execOptions modelExecutionOptions) ([]byte, http.Header, *interfaces.ErrorMessage) {
\toriginalRequestedModel := modelName
\trouteDecision := h.applyModelRouter(ctx, handlerType, modelName, rawJSON, false, execOptions)
''',
    '''func (h *BaseAPIHandler) executeCountWithAuthManager(ctx context.Context, handlerType, modelName string, rawJSON []byte, alt string, execOptions modelExecutionOptions) ([]byte, http.Header, *interfaces.ErrorMessage) {
\toriginalRequestedModel := modelName
\tvar policyErr *interfaces.ErrorMessage
\tctx, modelName, rawJSON, policyErr = applyAPIKeyModelPolicy(h, ctx, modelName, rawJSON)
\tif policyErr != nil {
\t\treturn nil, nil, policyErr
\t}
\trouteDecision := h.applyModelRouter(ctx, handlerType, modelName, rawJSON, false, execOptions)
\tctx, modelName, rawJSON, policyErr = applyAPIKeyRoutedModelPolicy(ctx, modelName, rawJSON, &routeDecision)
\tif policyErr != nil {
\t\treturn nil, nil, policyErr
\t}
''',
    'executeCountWithAuthManager(ctx context.Context, handlerType, modelName string, rawJSON []byte, alt string, execOptions modelExecutionOptions) ([]byte, http.Header, *interfaces.ErrorMessage) {\n\toriginalRequestedModel := modelName\n\tvar policyErr *interfaces.ErrorMessage',
)
replace_once(
    handlers_execution_source,
    '''\tif routeDecision.ExecutorPluginID != "" {
\t\treturn h.countWithPluginExecutor(ctx, handlerType, modelName, originalRequestedModel, rawJSON, alt, routeDecision.ExecutorPluginID, execOptions)
\t}
''',
    '''\tif routeDecision.ExecutorPluginID != "" {
\t\tif errMsg := h.validateAPIKeyPluginExecutor(ctx, routeDecision.ExecutorPluginID); errMsg != nil {
\t\t\treturn nil, nil, errMsg
\t\t}
\t\treturn h.countWithPluginExecutor(ctx, handlerType, modelName, originalRequestedModel, rawJSON, alt, routeDecision.ExecutorPluginID, execOptions)
\t}
''',
    'if errMsg := h.validateAPIKeyPluginExecutor(ctx, routeDecision.ExecutorPluginID); errMsg != nil {\n\t\t\treturn nil, nil, errMsg\n\t\t}\n\t\treturn h.countWithPluginExecutor',
)
replace_once(
    handlers_execution_source,
    '''\tproviders = adjustExecutionProvidersForEntryProtocol(handlerType, providers)
\treqMeta := requestExecutionMetadata(ctx)
''',
    '''\tproviders = adjustExecutionProvidersForEntryProtocol(handlerType, providers)
\tproviders, errMsg = applyAPIKeyProviderPolicy(ctx, providers)
\tif errMsg != nil {
\t\treturn nil, nil, errMsg
\t}
\treqMeta := requestExecutionMetadata(ctx)
''',
    'adjustExecutionProvidersForEntryProtocol(handlerType, providers)\n\tproviders, errMsg = applyAPIKeyProviderPolicy',
)

handlers_stream_source = ROOT / 'sdk/api/handlers/handlers_stream.go'
replace_once(
    handlers_stream_source,
    '''\toriginalRequestedModel := modelName
\trouteDecision, preparedRoute := preparedModelRouteFromContext(ctx, execOptions.SkipRouterPluginID)
''',
    '''\toriginalRequestedModel := modelName
\tvar policyErr *interfaces.ErrorMessage
\tctx, modelName, rawJSON, policyErr = applyAPIKeyModelPolicy(h, ctx, modelName, rawJSON)
\tif policyErr != nil {
\t\terrChan := make(chan *interfaces.ErrorMessage, 1)
\t\terrChan <- policyErr
\t\tclose(errChan)
\t\treturn nil, nil, errChan
\t}
\trouteDecision, preparedRoute := preparedModelRouteFromContext(ctx, execOptions.SkipRouterPluginID)
''',
    'ctx, modelName, rawJSON, policyErr = applyAPIKeyModelPolicy(h, ctx, modelName, rawJSON)',
)
replace_once(
    handlers_stream_source,
    '''\tif !preparedRoute {
\t\trouteDecision = h.applyModelRouter(ctx, entryProtocol, modelName, rawJSON, true, execOptions)
\t}
''',
    '''\tif !preparedRoute {
\t\trouteDecision = h.applyModelRouter(ctx, entryProtocol, modelName, rawJSON, true, execOptions)
\t}
\tctx, modelName, rawJSON, policyErr = applyAPIKeyRoutedModelPolicy(ctx, modelName, rawJSON, &routeDecision)
\tif policyErr != nil {
\t\terrChan := make(chan *interfaces.ErrorMessage, 1)
\t\terrChan <- policyErr
\t\tclose(errChan)
\t\treturn nil, nil, errChan
\t}
''',
    'applyAPIKeyRoutedModelPolicy(ctx, modelName, rawJSON, &routeDecision)',
)
replace_once(
    handlers_stream_source,
    '''\tif routeDecision.ExecutorPluginID != "" {
\t\treturn h.streamWithPluginExecutor(ctx, entryProtocol, responseProtocol, modelName, originalRequestedModel, rawJSON, alt, routeDecision.ExecutorPluginID, execOptions)
\t}
''',
    '''\tif routeDecision.ExecutorPluginID != "" {
\t\tif errMsg := h.validateAPIKeyPluginExecutor(ctx, routeDecision.ExecutorPluginID); errMsg != nil {
\t\t\terrChan := make(chan *interfaces.ErrorMessage, 1)
\t\t\terrChan <- errMsg
\t\t\tclose(errChan)
\t\t\treturn nil, nil, errChan
\t\t}
\t\treturn h.streamWithPluginExecutor(ctx, entryProtocol, responseProtocol, modelName, originalRequestedModel, rawJSON, alt, routeDecision.ExecutorPluginID, execOptions)
\t}
''',
    'h.validateAPIKeyPluginExecutor(ctx, routeDecision.ExecutorPluginID); errMsg != nil',
)
replace_once(
    handlers_stream_source,
    '''\tproviders = adjustExecutionProvidersForEntryProtocol(entryProtocol, providers)
\treqMeta := requestExecutionMetadata(ctx)
''',
    '''\tproviders = adjustExecutionProvidersForEntryProtocol(entryProtocol, providers)
\tproviders, errMsg = applyAPIKeyProviderPolicy(ctx, providers)
\tif errMsg != nil {
\t\terrChan := make(chan *interfaces.ErrorMessage, 1)
\t\terrChan <- errMsg
\t\tclose(errChan)
\t\treturn nil, nil, errChan
\t}
\treqMeta := requestExecutionMetadata(ctx)
''',
    'adjustExecutionProvidersForEntryProtocol(entryProtocol, providers)\n\tproviders, errMsg = applyAPIKeyProviderPolicy',
)

handlers_context_source = ROOT / 'sdk/api/handlers/handlers_context.go'
replace_once(
    handlers_context_source,
    '''\tdecision := h.applyModelRouter(ctx, handlerType, modelName, rawJSON, true, modelExecutionOptions{})
\tctx = context.WithValue(ctx, preparedModelRouteContextKey{}, decision)
''',
    '''\tpolicyCtx, effectiveModel, effectiveBody, policyErr := applyAPIKeyModelPolicy(h, ctx, modelName, rawJSON)
\tif policyErr != nil {
\t\tctx = context.WithValue(ctx, preparedModelRouteContextKey{}, modelRouteDecision{})
\t\treturn ctx, false
\t}
\tdecision := h.applyModelRouter(policyCtx, handlerType, effectiveModel, effectiveBody, true, modelExecutionOptions{})
\tctx = context.WithValue(policyCtx, preparedModelRouteContextKey{}, decision)
''',
    'policyCtx, effectiveModel, effectiveBody, policyErr := applyAPIKeyModelPolicy',
)

plugin_executor_route_source = ROOT / 'internal/pluginhost/executor_route.go'
insert_before(
    plugin_executor_route_source,
    '// ExecutePluginExecutor executes a request with the named plugin executor without changing the requested model.\n',
    '''// PluginExecutorProvider resolves the normalized execution provider declared by a plugin executor.
func (h *Host) PluginExecutorProvider(pluginID string) (string, bool) {
\tadapter, errAdapter := h.executorAdapterForPlugin(pluginID)
\tif errAdapter != nil || adapter == nil {
\t\treturn "", false
\t}
\tprovider := strings.ToLower(strings.TrimSpace(adapter.Identifier()))
\treturn provider, provider != ""
}

''',
    'func (h *Host) PluginExecutorProvider(',
)

handlers_source = ROOT / 'sdk/api/handlers/handlers.go'
insert_before(
    handlers_source,
    '// BaseAPIHandler contains the handlers for API endpoints.\n',
    '''type PolicyVisibleModel struct {
\tID          string
\tEffectiveID string
}

// FilterModelsForRequest applies the frozen API-key policy to one model catalog.
// providers resolves current provider carriage for canonical model IDs.
func FilterModelsForRequest(ctx context.Context, modelIDs []string, providers func(string) []string) ([]PolicyVisibleModel, *interfaces.ErrorMessage) {
\tdecision, configured := apikeypolicy.DecisionFromContext(ctx)
\tif !configured {
\t\tout := make([]PolicyVisibleModel, 0, len(modelIDs))
\t\tfor _, id := range modelIDs {
\t\t\tout = append(out, PolicyVisibleModel{ID: id, EffectiveID: id})
\t\t}
\t\treturn out, nil
\t}
\tcandidates := make([]apikeypolicy.ModelCandidate, 0, len(modelIDs))
\tfor _, id := range modelIDs {
\t\tvar carrying []string
\t\tif providers != nil {
\t\t\tcarrying = providers(id)
\t\t}
\t\tcandidates = append(candidates, apikeypolicy.ModelCandidate{ID: id, Providers: carrying})
\t}
\tvisible, errFilter := decision.FilterVisibleModels(candidates)
\tif errFilter != nil {
\t\treturn nil, apiKeyPolicyExecutionError(errFilter)
\t}
\tout := make([]PolicyVisibleModel, 0, len(visible))
\tfor _, model := range visible {
\t\tout = append(out, PolicyVisibleModel{ID: model.ID, EffectiveID: model.EffectiveID})
\t}
\treturn out, nil
}

// FilterModelMapsForRequest clones canonical model maps and duplicates mapped
// aliases without mutating registry-owned values.
func FilterModelMapsForRequest(ctx context.Context, models []map[string]any, idKey string, providers func(string) []string) ([]map[string]any, *interfaces.ErrorMessage) {
\tids := make([]string, 0, len(models))
\tbyID := make(map[string]map[string]any, len(models))
\tfor _, model := range models {
\t\tid, _ := model[idKey].(string)
\t\tid = strings.TrimPrefix(strings.TrimSpace(id), "models/")
\t\tif id == "" {
\t\t\tcontinue
\t\t}
\t\tids = append(ids, id)
\t\tbyID[id] = model
\t}
\tvisible, errMsg := FilterModelsForRequest(ctx, ids, providers)
\tif errMsg != nil {
\t\treturn nil, errMsg
\t}
\tout := make([]map[string]any, 0, len(visible))
\tfor _, item := range visible {
\t\tsource := byID[item.EffectiveID]
\t\tclone := make(map[string]any, len(source))
\t\tfor key, value := range source {
\t\t\tclone[key] = value
\t\t}
\t\tvalue := item.ID
\t\tif idKey == "name" {
\t\t\tvalue = "models/" + item.ID
\t\t}
\t\tclone[idKey] = value
\t\tout = append(out, clone)
\t}
\treturn out, nil
}

''',
    'func FilterModelsForRequest(',
)

openai_handlers_source = ROOT / 'sdk/api/handlers/openai/openai_handlers.go'
replace_once(
    openai_handlers_source,
    '''\tif _, ok := c.Request.URL.Query()["client_version"]; ok {
\t\tclientVersion := c.Query("client_version")
\t\tc.JSON(http.StatusOK, h.codexClientModelsResponse(clientVersion))
\t\treturn
\t}

\t// Get all available models
\tallModels := h.Models()
''',
    '''\tallModels, policyErr := handlers.FilterModelMapsForRequest(c.Request.Context(), h.Models(), "id", registry.GetGlobalRegistry().GetModelProviders)
\tif policyErr != nil {
\t\th.WriteErrorResponse(c, policyErr)
\t\treturn
\t}
\tif _, ok := c.Request.URL.Query()["client_version"]; ok {
\t\tclientVersion := c.Query("client_version")
\t\tc.JSON(http.StatusOK, codexmodels.BuildResponseForClient(allModels, registry.GetGlobalRegistry().GetModelProviders, h.Cfg != nil && h.Cfg.CodexOptimizeMultiAgentV2, clientVersion))
\t\treturn
\t}

\t// Get all models visible to the frozen request policy.
''',
    'allModels, policyErr := handlers.FilterModelMapsForRequest',
)
add_go_import(openai_handlers_source, '"' + import_path('internal/registry') + '"\n', '\tcodexmodels "' + import_path('internal/client/codex/models') + '"\n')

claude_handlers_source = ROOT / 'sdk/api/handlers/claude/code_handlers.go'
replace_once(
    claude_handlers_source,
    '''\t\t\t\tif m, ok := e["message"].(string); ok && strings.TrimSpace(m) != "" {
\t\t\t\t\tmessage = strings.TrimSpace(m)
\t\t\t\t} else if c, ok := e["code"].(string); ok && strings.TrimSpace(c) != "" {
\t\t\t\t\tmessage = strings.TrimSpace(c)
\t\t\t\t}
''',
    '''\t\t\t\tif m, ok := e["message"].(string); ok && strings.TrimSpace(m) != "" {
\t\t\t\t\tmessage = strings.TrimSpace(m)
\t\t\t\t\tif code, okCode := e["code"].(string); okCode && (strings.HasPrefix(code, "profile_") || strings.HasPrefix(code, "api_key_policy_")) {
\t\t\t\t\t\tmessage += " (" + strings.TrimSpace(code) + ")"
\t\t\t\t\t}
\t\t\t\t} else if c, ok := e["code"].(string); ok && strings.TrimSpace(c) != "" {
\t\t\t\t\tmessage = strings.TrimSpace(c)
\t\t\t\t}
''',
    'strings.HasPrefix(code, "profile_")',
)
replace_once(
    claude_handlers_source,
    '''func (h *ClaudeCodeAPIHandler) ClaudeModels(c *gin.Context) {
\tdisableCloaking := h.Cfg != nil && h.Cfg.ClaudeCode.DisableCloakingModelList
\tc.JSON(http.StatusOK, claudemodels.BuildResponse(h.Models(), disableCloaking))
}
''',
    '''func (h *ClaudeCodeAPIHandler) ClaudeModels(c *gin.Context) {
\trequestCtx := context.Background()
\tif c != nil && c.Request != nil {
\t\trequestCtx = c.Request.Context()
\t}
\tmodels, policyErr := handlers.FilterModelMapsForRequest(requestCtx, h.Models(), "id", registry.GetGlobalRegistry().GetModelProviders)
\tif policyErr != nil {
\t\th.WriteErrorResponse(c, policyErr)
\t\treturn
\t}
\tdisableCloaking := h.Cfg != nil && h.Cfg.ClaudeCode.DisableCloakingModelList
\tc.JSON(http.StatusOK, claudemodels.BuildResponse(models, disableCloaking))
}
''',
    'models, policyErr := handlers.FilterModelMapsForRequest',
)

gemini_handlers_source = ROOT / 'sdk/api/handlers/gemini/gemini_handlers.go'
add_go_import(gemini_handlers_source, '"context"\n', '"encoding/json"\n')
insert_before(
    gemini_handlers_source,
    'func (h *GeminiAPIHandler) GeminiModels(c *gin.Context) {\n',
    '''func (h *GeminiAPIHandler) WriteErrorResponse(c *gin.Context, msg *interfaces.ErrorMessage) {
\tstatus := http.StatusInternalServerError
\tif msg != nil && msg.StatusCode > 0 {
\t\tstatus = msg.StatusCode
\t}
\tvar policyEnvelope struct {
\t\tError struct {
\t\t\tMessage string `json:"message"`
\t\t\tCode    string `json:"code"`
\t\t} `json:"error"`
\t}
\tif msg != nil && msg.Error != nil && json.Unmarshal([]byte(msg.Error.Error()), &policyEnvelope) == nil &&
\t\t(strings.HasPrefix(policyEnvelope.Error.Code, "profile_") || strings.HasPrefix(policyEnvelope.Error.Code, "api_key_policy_")) {
\t\tstatusName := "UNAVAILABLE"
\t\tif status == http.StatusForbidden {
\t\t\tstatusName = "PERMISSION_DENIED"
\t\t}
\t\tc.JSON(status, gin.H{"error": gin.H{
\t\t\t"code": status, "message": policyEnvelope.Error.Message,
\t\t\t"status": statusName, "reason": policyEnvelope.Error.Code,
\t\t}})
\t\treturn
\t}
\th.BaseAPIHandler.WriteErrorResponse(c, msg)
}

''',
    'func (h *GeminiAPIHandler) WriteErrorResponse',
)
replace_once(
    gemini_handlers_source,
    '''func (h *GeminiAPIHandler) GeminiModels(c *gin.Context) {
\trawModels := h.Models()
''',
    '''func (h *GeminiAPIHandler) GeminiModels(c *gin.Context) {
\trequestCtx := context.Background()
\tif c != nil && c.Request != nil {
\t\trequestCtx = c.Request.Context()
\t}
\trawModels, policyErr := handlers.FilterModelMapsForRequest(requestCtx, h.Models(), "name", registry.GetGlobalRegistry().GetModelProviders)
\tif policyErr != nil {
\t\th.WriteErrorResponse(c, policyErr)
\t\treturn
\t}
''',
    'rawModels, policyErr := handlers.FilterModelMapsForRequest',
)
replace_once(
    gemini_handlers_source,
    '''\t// Get dynamic models from the global registry and find the matching one
\tavailableModels := h.Models()
''',
    '''\t// Get dynamic models visible to the frozen request policy.
\tavailableModels, policyErr := handlers.FilterModelMapsForRequest(c.Request.Context(), h.Models(), "name", registry.GetGlobalRegistry().GetModelProviders)
\tif policyErr != nil {
\t\th.WriteErrorResponse(c, policyErr)
\t\treturn
\t}
''',
    'availableModels, policyErr := handlers.FilterModelMapsForRequest',
)

replace_once(
    server_routes_source,
    '''\t} else {
\t\tmodels = grokModelsFromRegistryInfos(registry.GetGlobalRegistry().GetAvailableModelInfos())
\t}
\tc.JSON(http.StatusOK, grokbuild.BuildResponse(models))
''',
    '''\t} else {
\t\tvar ok bool
\t\tmodels, ok = s.filterRegistryGrokModels(c, registry.GetGlobalRegistry().GetAvailableModelInfos())
\t\tif !ok {
\t\t\treturn
\t\t}
\t}
\tc.JSON(http.StatusOK, grokbuild.BuildResponse(models))
''',
    'models, ok = s.filterRegistryGrokModels',
)
replace_once(
    server_routes_source,
    '''\treturn entries, true
}

func formatHomeGeminiModels(entries []homeModelEntry) []map[string]any {
''',
    '''\treturn s.filterHomeModelEntries(c, entries)
}

func formatHomeGeminiModels(entries []homeModelEntry) []map[string]any {
''',
    'return s.filterHomeModelEntries(c, entries)',
)

server_test_source = ROOT / 'internal/api/server_test.go'
replace_once(
    server_test_source,
    '''\tif legacyRR.Code != http.StatusNotFound {
\t\tt.Fatalf("legacy usage status = %d, want %d body=%s", legacyRR.Code, http.StatusNotFound, legacyRR.Body.String())
\t}
''',
    '''\tif legacyRR.Code != http.StatusServiceUnavailable {
\t\tt.Fatalf("unavailable usage status = %d, want %d body=%s", legacyRR.Code, http.StatusServiceUnavailable, legacyRR.Body.String())
\t}
\tif body := strings.TrimSpace(legacyRR.Body.String()); body != `{"error":"usage service is not available"}` {
\t\tt.Fatalf("unavailable usage body = %s", body)
\t}
''',
    'unavailable usage body = %s',
)

service_source = ROOT / 'sdk/cliproxy/service.go'
add_go_import(service_source, '"' + import_path('internal/pluginhost') + '"\n', '\tproapp "' + import_path('internal/pro/app') + '"\n')
replace_once(
    service_source,
    '\t// pluginHost owns dynamic plugin lifecycle and runtime capability adapters.\n\tpluginHost *pluginhost.Host\n',
    '\t// pluginHost owns dynamic plugin lifecycle and runtime capability adapters.\n\tpluginHost *pluginhost.Host\n\n\t// proApp owns and wires the statically linked Pro modules.\n\tproApp *proapp.App\n',
    'proApp *proapp.App',
)

builder_source = ROOT / 'sdk/cliproxy/builder.go'
add_go_import(builder_source, '"' + import_path('internal/pluginhost') + '"\n', '\tproapp "' + import_path('internal/pro/app') + '"\n')
replace_once(
    builder_source,
    '''\tconfigaccess.Register(&b.cfg.SDKConfig)
\tpluginHost := b.pluginHost
''',
    '''\tproApplication, errProApp := proapp.New(context.Background(), b.configPath, b.cfg.ProxyURL)
\tif errProApp != nil {
\t\treturn nil, fmt.Errorf("cliproxy: initialize Pro features: %w", errProApp)
\t}
\tb.cfg.ProxyURL = proApplication.BaseProxyURL()

\tconfigaccess.Register(&b.cfg.SDKConfig)
\tpluginHost := b.pluginHost
''',
    'proApplication, errProApp := proapp.New',
)
replace_once(
    builder_source,
    '\t\tpluginHost:          pluginHost,\n',
    '\t\tpluginHost:          pluginHost,\n\t\tproApp:         proApplication,\n',
    'proApp:         proApplication',
)
replace_once(
    builder_source,
    '''\tif b.postAuthHook != nil {
\t\tservice.serverOptions = append(service.serverOptions, api.WithPostAuthHook(b.postAuthHook))
\t}
''',
    '''\tproApplication.SetOAuthPolicyChangeHandler(func(ctx context.Context) {
\t\tif service.coreManager == nil {
\t\t\treturn
\t\t}
\t\tpolicyCtx := coreauth.WithSkipPersist(ctx)
\t\tservice.registerModelsForAuthBatch(policyCtx, service.coreManager.List())
\t})
\tservice.coreManager.SetAccountPolicyResolver(proApplication.ApplyCachedAccountPolicy)
\tif b.postAuthHook != nil {
\t\tservice.serverOptions = append(service.serverOptions, api.WithPostAuthHook(b.postAuthHook))
\t}
''',
    'proApplication.SetOAuthPolicyChangeHandler',
)
replace_once(
    builder_source,
    '\t\tapi.WithPluginHost(pluginHost),\n',
    '\t\tapi.WithPluginHost(pluginHost),\n\t\tapi.WithProApp(proApplication),\n',
    'api.WithProApp(proApplication)',
)

service_config_source = ROOT / 'sdk/cliproxy/service_config.go'
replace_once(
    service_config_source,
    '''\troutingState := normalizedRoutingRuntimeState(commit.cfg)
''',
    '''\tif s.proApp != nil {
\t\ts.proApp.SetBaseProxyURL(commit.cfg.ProxyURL)
\t}
\troutingState := normalizedRoutingRuntimeState(commit.cfg)
''',
    's.proApp.SetBaseProxyURL(commit.cfg.ProxyURL)',
)

service_lifecycle_source = ROOT / 'sdk/cliproxy/service_lifecycle.go'
replace_once(
    service_lifecycle_source,
    '\t\tusage.StopDefault()\n',
    '\t\tif s.proApp != nil {\n\t\t\ts.proApp.Close()\n\t\t}\n\t\tusage.StopDefault()\n',
    's.proApp.Close()',
)

for speed_source in (
    'internal/redisqueue/speed_test.go',
    'internal/runtime/executor/helps/usage_speed_test.go',
    'internal/runtime/executor/claude_usage_speed_test.go',
    'sdk/api/handlers/handlers_speed_test.go',
    'sdk/cliproxy/auth/conductor_speed_test.go',
    'sdk/cliproxy/executor/speed.go',
    'sdk/cliproxy/usage/speed.go',
    'sdk/cliproxy/usage/speed_test.go',
):
    queue_go_source(speed_source)
queue_go_source('internal/redisqueue/api_key_policy_usage_test.go')

handlers_source = ROOT / 'sdk/api/handlers/handlers.go'
add_go_import(handlers_source, '"' + import_path('internal/logging') + '"\n', '\tapikeypolicy "' + import_path('internal/pro/apikeypolicy') + '"\n')
replace_once(
    handlers_source,
    '''\tif requestCtx != nil && logging.GetRequestID(parentCtx) == "" {
''',
    '''\tif requestCtx != nil {
\t\tparentCtx = apikeypolicy.InheritContext(parentCtx, requestCtx)
\t}
\tif requestCtx != nil && logging.GetRequestID(parentCtx) == "" {
''',
    'parentCtx = apikeypolicy.InheritContext(parentCtx, requestCtx)',
)
replace_once(
    handlers_source,
    '''\tmeta[coreexecutor.ServiceTierMetadataKey] = serviceTier
}
''',
    '''\tmeta[coreexecutor.ServiceTierMetadataKey] = serviceTier
\tspeed := strings.TrimSpace(gjson.GetBytes(rawJSON, "speed").String())
\tif speed != "" {
\t\tmeta[coreexecutor.SpeedMetadataKey] = speed
\t}
}
''',
    'meta[coreexecutor.SpeedMetadataKey] = speed',
)

handlers_error_response_test_source = ROOT / 'sdk/api/handlers/handlers_error_response_test.go'
add_go_import(handlers_error_response_test_source, '"bytes"\n', '\t"encoding/json"\n')
insert_before(
    handlers_error_response_test_source,
    'func TestWriteErrorResponse_AddonHeadersDisabledByDefault(t *testing.T) {\n',
    '''func TestBuildErrorResponseBody_NormalizesNonCanonicalRateLimitJSON(t *testing.T) {
	body := BuildErrorResponseBody(http.StatusTooManyRequests, `{"detail":"Rate limit exceeded"}`)
	var payload struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("body is not valid JSON: %v; body=%s", err, body)
	}
	if payload.Error.Message != "Rate limit exceeded" {
		t.Fatalf("error.message = %q, want %q", payload.Error.Message, "Rate limit exceeded")
	}
	if payload.Error.Type != "rate_limit_error" {
		t.Fatalf("error.type = %q, want rate_limit_error", payload.Error.Type)
	}
	if payload.Error.Code != "rate_limit_exceeded" {
		t.Fatalf("error.code = %q, want rate_limit_exceeded", payload.Error.Code)
	}
}

func TestBuildErrorResponseBody_PreservesCanonicalRateLimitFields(t *testing.T) {
	body := BuildErrorResponseBody(http.StatusTooManyRequests, `{"error":{"type":"usage_limit_reached","code":"usage_limit","resets_in_seconds":120}}`)
	var payload struct {
		Error struct {
			Type            string `json:"type"`
			Code            string `json:"code"`
			ResetsInSeconds int    `json:"resets_in_seconds"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("body is not valid JSON: %v; body=%s", err, body)
	}
	if payload.Error.Type != "usage_limit_reached" || payload.Error.Code != "usage_limit" || payload.Error.ResetsInSeconds != 120 {
		t.Fatalf("canonical rate limit fields were not preserved: %+v", payload.Error)
	}
}

''',
    'func TestBuildErrorResponseBody_NormalizesNonCanonicalRateLimitJSON(',
)

replace_once(
    handlers_source,
    '// If errText is already valid JSON, it is returned as-is to preserve upstream error payloads.\n',
    '// Valid non-429 JSON is preserved. A 429 is normalized into an OpenAI-compatible\n// error envelope so Codex clients can recognize the rate-limit retry signal.\n',
    'A 429 is normalized into an OpenAI-compatible',
)

replace_go_function(
    handlers_source,
    'func BuildErrorResponseBody(status int, errText string) []byte',
    '''func BuildErrorResponseBody(status int, errText string) []byte {
	if status <= 0 {
		status = http.StatusInternalServerError
	}
	if strings.TrimSpace(errText) == "" {
		errText = http.StatusText(status)
	}

	trimmed := strings.TrimSpace(errText)
	if trimmed != "" && json.Valid([]byte(trimmed)) {
		if status == http.StatusTooManyRequests {
			if normalized := normalizeRateLimitErrorBody(trimmed); len(normalized) > 0 {
				return normalized
			}
		} else {
			return []byte(trimmed)
		}
	}

	errType := "invalid_request_error"
	var code string
	switch status {
	case http.StatusUnauthorized:
		errType = "authentication_error"
		code = "invalid_api_key"
	case http.StatusForbidden:
		errType = "permission_error"
		code = "insufficient_quota"
	case http.StatusTooManyRequests:
		errType = "rate_limit_error"
		code = "rate_limit_exceeded"
	case http.StatusNotFound:
		errType = "invalid_request_error"
		code = "model_not_found"
	default:
		if status >= http.StatusInternalServerError {
			errType = "server_error"
			code = "internal_server_error"
		}
	}

	payload, err := json.Marshal(ErrorResponse{
		Error: ErrorDetail{
			Message: errText,
			Type:    errType,
			Code:    code,
		},
	})
	if err != nil {
		return []byte(fmt.Sprintf(`{"error":{"message":%q,"type":"server_error","code":"internal_server_error"}}`, errText))
	}
	return payload
}
''',
    'func normalizeRateLimitErrorBody',
)

insert_before(
    handlers_source,
    '// StreamingKeepAliveInterval returns the SSE keep-alive interval for this server.\n',
    '''func normalizeRateLimitErrorBody(errText string) []byte {
	var payload map[string]any
	if err := json.Unmarshal([]byte(errText), &payload); err != nil {
		return nil
	}

	errorObject, _ := payload["error"].(map[string]any)
	if errorObject == nil {
		errorObject = make(map[string]any)
	}

	message, _ := errorObject["message"].(string)
	if strings.TrimSpace(message) == "" {
		if value, ok := payload["error"].(string); ok {
			message = value
		}
	}
	if strings.TrimSpace(message) == "" {
		for _, key := range []string{"message", "detail"} {
			if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
				message = value
				break
			}
		}
	}
	if strings.TrimSpace(message) == "" {
		message = http.StatusText(http.StatusTooManyRequests)
	}
	errorObject["message"] = message

	if value, ok := errorObject["type"].(string); !ok || strings.TrimSpace(value) == "" {
		errorObject["type"] = "rate_limit_error"
	}
	if value, ok := errorObject["code"].(string); !ok || strings.TrimSpace(value) == "" {
		errorObject["code"] = "rate_limit_exceeded"
	}
	payload["error"] = errorObject

	normalized, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	return normalized
}

''',
    'func normalizeRateLimitErrorBody',
)

codex_chat_completions_request = ROOT / 'internal/translator/codex/openai/chat-completions/codex_openai_request.go'
replace_once(
    codex_chat_completions_request,
    '''\t// Model
\tout, _ = sjson.SetBytes(out, "model", modelName)
''',
    '''\t// Model
\tout, _ = sjson.SetBytes(out, "model", modelName)

\tif serviceTier := root.Get("service_tier"); serviceTier.Type == gjson.String {
\t\tswitch strings.ToLower(strings.TrimSpace(serviceTier.String())) {
\t\tcase "fast", "priority":
\t\t\tout, _ = sjson.SetBytes(out, "service_tier", "priority")
\t\t}
\t}
''',
    'case "fast", "priority":',
)
queue_go_source('internal/translator/codex/openai/chat-completions/codex_fast_service_tier_test.go')

codex_responses_request = ROOT / 'internal/translator/codex/openai/responses/codex_openai-responses_request.go'
add_go_import(codex_responses_request, '\t"encoding/json"\n', '\t"strings"\n')
replace_once(
    codex_responses_request,
    '''\tif serviceTier := gjson.GetBytes(rawJSON, "service_tier"); serviceTier.Exists() && serviceTier.String() != "priority" {
\t\trawJSON = deleteCodexRequestFields(rawJSON, "service_tier")
\t}
''',
    '''\tif serviceTier := gjson.GetBytes(rawJSON, "service_tier"); serviceTier.Exists() {
\t\tswitch strings.ToLower(strings.TrimSpace(serviceTier.String())) {
\t\tcase "fast":
\t\t\trawJSON, _ = sjson.SetBytes(rawJSON, "service_tier", "priority")
\t\tcase "priority":
\t\t\tif serviceTier.String() != "priority" {
\t\t\t\trawJSON, _ = sjson.SetBytes(rawJSON, "service_tier", "priority")
\t\t\t}
\t\tdefault:
\t\t\trawJSON = deleteCodexRequestFields(rawJSON, "service_tier")
\t\t}
\t}
''',
    'case "fast":',
)
queue_go_source('internal/translator/codex/openai/responses/codex_fast_service_tier_test.go')

usage_manager = ROOT / 'sdk/cliproxy/usage/manager.go'
replace_once(
    usage_manager,
    '''\t// ResponseServiceTier stores the final tier reported by the upstream response.
\tResponseServiceTier string
\t// Generate reports whether the client requested actual generation.
''',
    '''\t// ResponseServiceTier stores the final tier reported by the upstream response.
\tResponseServiceTier string
\t// Speed stores the client-requested inference speed.
\tSpeed string
\t// ResponseSpeed stores the final inference speed reported by the upstream response.
\tResponseSpeed string
\t// Generate reports whether the client requested actual generation.
''',
    'ResponseSpeed string',
)
replace_once(
    usage_manager,
    '''\tTokenBreakdown      TokenBreakdown
\tResponseServiceTier string
}
''',
    '''\tTokenBreakdown      TokenBreakdown
\tResponseServiceTier string
\tResponseSpeed       string
}
''',
    'ResponseSpeed       string',
)
replace_once(
    usage_manager,
    '\tnamed     map[string]int\n',
    '\tnamed       map[string]int\n\tnamedOwners map[string][]Plugin\n',
    'namedOwners map[string][]Plugin',
)
replace_once(
    usage_manager,
    '''\tTTFT        time.Duration
\tFailed      bool
''',
    '''\tTTFT        time.Duration
\t// AttemptIndex is the zero-based upstream attempt number for this request.
\t// Nil means the caller did not instrument attempts.
\tAttemptIndex *int64
\tFailed       bool
''',
    'AttemptIndex *int64',
)
replace_once(
    usage_manager,
    '''\tif index, exists := m.named[name]; exists && index >= 0 && index < len(m.plugins) {
\t\tm.plugins[index] = plugin
\t\tm.pluginsMu.Unlock()
\t\treturn
\t}
''',
    '''\tif index, exists := m.named[name]; exists && index >= 0 && index < len(m.plugins) {
\t\tcurrent := m.plugins[index]
\t\tif samePlugin(current, plugin) {
\t\t\tm.pluginsMu.Unlock()
\t\t\treturn
\t\t}
\t\tif current != nil {
\t\t\tif m.namedOwners == nil {
\t\t\t\tm.namedOwners = make(map[string][]Plugin)
\t\t\t}
\t\t\tm.namedOwners[name] = append(m.namedOwners[name], current)
\t\t}
\t\tm.plugins[index] = plugin
\t\tm.pluginsMu.Unlock()
\t\treturn
\t}
''',
    'm.namedOwners[name] = append(m.namedOwners[name], current)',
)
replace_once(
    usage_manager,
    '''\tm.named[name] = len(m.plugins)
\tm.plugins = append(m.plugins, plugin)
\tm.pluginsMu.Unlock()
''',
    '''\tfor index, existing := range m.plugins {
\t\tif existing == nil {
\t\t\tm.named[name] = index
\t\t\tm.plugins[index] = plugin
\t\t\tm.pluginsMu.Unlock()
\t\t\treturn
\t\t}
\t}
\tm.named[name] = len(m.plugins)
\tm.plugins = append(m.plugins, plugin)
\tm.pluginsMu.Unlock()
''',
    'for index, existing := range m.plugins',
)
auth_conductor = ROOT / 'sdk/cliproxy/auth/conductor_execution.go'
replace_once(
    auth_conductor,
    '''\tserviceTier := serviceTierFromOptions(opts)
\tif serviceTier != "" {
\t\tctx = coreusage.WithServiceTier(ctx, serviceTier)
\t}
\tif generate, ok := generateFromOptions(opts); ok {
''',
    '''\tserviceTier := serviceTierFromOptions(opts)
\tif serviceTier != "" {
\t\tctx = coreusage.WithServiceTier(ctx, serviceTier)
\t}
\tif speed := speedFromOptions(opts); speed != "" {
\t\tctx = coreusage.WithSpeed(ctx, speed)
\t}
\tif generate, ok := generateFromOptions(opts); ok {
''',
    'ctx = coreusage.WithSpeed(ctx, speed)',
)
insert_before(
    auth_conductor,
    'func generateFromOptions(opts cliproxyexecutor.Options) (bool, bool) {\n',
    '''func speedFromOptions(opts cliproxyexecutor.Options) string {
\treturn stringMetadataValue(opts.Metadata, cliproxyexecutor.SpeedMetadataKey)
}

''',
    'func speedFromOptions(opts cliproxyexecutor.Options) string',
)

auth_conductor_base = ROOT / 'sdk/cliproxy/auth/conductor.go'
replace_once(
    auth_conductor_base,
    '''	// pluginScheduler runs outside m.mu before falling back to native selection.
	pluginScheduler PluginScheduler
''',
    '''	// pluginScheduler runs outside m.mu before falling back to native selection.
	pluginScheduler PluginScheduler
	// accountPolicyResolver derives execution-only OAuth policy overlays.
	accountPolicyResolver AccountPolicyResolver
''',
    'accountPolicyResolver AccountPolicyResolver',
)
replace_once(
    auth_conductor,
    '''func (m *Manager) Execute(ctx context.Context, providers []string, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
\treq, opts = cliproxysession.Enrich(req, opts)
\tnormalized := m.normalizeProviders(providers)
''',
    '''func (m *Manager) Execute(ctx context.Context, providers []string, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
\treq, opts = cliproxysession.Enrich(req, opts)
\tctx = coreusage.WithAttemptTracking(ctx)
\tnormalized := m.normalizeProviders(providers)
''',
    'ctx = coreusage.WithAttemptTracking(ctx)\n\tnormalized := m.normalizeProviders(providers)',
)
replace_once(
    auth_conductor,
    '''func (m *Manager) ExecuteCount(ctx context.Context, providers []string, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
\treq, opts = cliproxysession.Enrich(req, opts)
\tnormalized := m.normalizeProviders(providers)
''',
    '''func (m *Manager) ExecuteCount(ctx context.Context, providers []string, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
\treq, opts = cliproxysession.Enrich(req, opts)
\tctx = coreusage.WithAttemptTracking(ctx)
\tnormalized := m.normalizeProviders(providers)
''',
    'ExecuteCount(ctx context.Context, providers []string, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {\n\tctx = coreusage.WithAttemptTracking(ctx)',
)
replace_once(
    auth_conductor,
    '''func (m *Manager) ExecuteStream(ctx context.Context, providers []string, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
\treq, opts = cliproxysession.Enrich(req, opts)
\tif m.HomeEnabled() {
''',
    '''func (m *Manager) ExecuteStream(ctx context.Context, providers []string, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
\treq, opts = cliproxysession.Enrich(req, opts)
\tctx = coreusage.WithAttemptTracking(ctx)
\tif m.HomeEnabled() {
''',
    'ExecuteStream(ctx context.Context, providers []string, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {\n\tctx = coreusage.WithAttemptTracking(ctx)',
)

auth_conductor_stream = ROOT / 'sdk/cliproxy/auth/conductor_stream.go'
auth_conductor_home_execution = ROOT / 'sdk/cliproxy/auth/conductor_home_execution.go'
auth_conductor_home = ROOT / 'sdk/cliproxy/auth/conductor_home.go'

for target in (
    auth_conductor_stream,
    auth_conductor_home_execution,
    auth_conductor_home,
):
    add_go_import(
        target,
        f'\tcliproxyexecutor "{import_path("sdk/cliproxy/executor")}"\n',
        f'\tcoreusage "{import_path("sdk/cliproxy/usage")}"\n',
    )

for target, old_call, new_call, marker in (
    (
        auth_conductor_stream,
        '\t\tstreamResult, errStream := executor.ExecuteStream(ctx, auth, execReq, execOpts)\n',
        '\t\tctx = coreusage.NextAttemptContext(ctx)\n\t\tstreamResult, errStream := executor.ExecuteStream(ctx, auth, execReq, execOpts)\n',
        'ctx = coreusage.NextAttemptContext(ctx)\n\t\tstreamResult, errStream := executor.ExecuteStream',
    ),
    (
        auth_conductor_stream,
        '\t\t\t\t\tstreamResult, errStream = executor.ExecuteStream(ctx, auth, execReq, execOpts)\n',
        '\t\t\t\t\tctx = coreusage.NextAttemptContext(ctx)\n\t\t\t\t\tstreamResult, errStream = executor.ExecuteStream(ctx, auth, execReq, execOpts)\n',
        'ctx = coreusage.NextAttemptContext(ctx)\n\t\t\t\t\tstreamResult, errStream = executor.ExecuteStream',
    ),
    (
        auth_conductor_stream,
        '\t\t\t\t\tretryStream, retryErr := executor.ExecuteStream(ctx, auth, execReq, execOpts)\n',
        '\t\t\t\t\tctx = coreusage.NextAttemptContext(ctx)\n\t\t\t\t\tretryStream, retryErr := executor.ExecuteStream(ctx, auth, execReq, execOpts)\n',
        'ctx = coreusage.NextAttemptContext(ctx)\n\t\t\t\t\tretryStream, retryErr := executor.ExecuteStream',
    ),
    (
        auth_conductor,
        '\t\t\tresp, errExec := executor.Execute(execCtx, auth, execReq, execOpts)\n',
        '\t\t\texecCtx = coreusage.NextAttemptContext(execCtx)\n\t\t\tresp, errExec := executor.Execute(execCtx, auth, execReq, execOpts)\n',
        'execCtx = coreusage.NextAttemptContext(execCtx)\n\t\t\tresp, errExec := executor.Execute',
    ),
    (
        auth_conductor,
        '\t\t\t\t\tresp, errExec = executor.Execute(execCtx, auth, execReq, execOpts)\n',
        '\t\t\t\t\texecCtx = coreusage.NextAttemptContext(execCtx)\n\t\t\t\t\tresp, errExec = executor.Execute(execCtx, auth, execReq, execOpts)\n',
        'execCtx = coreusage.NextAttemptContext(execCtx)\n\t\t\t\t\tresp, errExec = executor.Execute',
    ),
    (
        auth_conductor,
        '\t\t\tresp, errExec := executor.CountTokens(execCtx, auth, execReq, execOpts)\n',
        '\t\t\texecCtx = coreusage.NextAttemptContext(execCtx)\n\t\t\tresp, errExec := executor.CountTokens(execCtx, auth, execReq, execOpts)\n',
        'execCtx = coreusage.NextAttemptContext(execCtx)\n\t\t\tresp, errExec := executor.CountTokens',
    ),
    (
        auth_conductor,
        '\t\t\t\t\tresp, errExec = executor.CountTokens(execCtx, auth, execReq, execOpts)\n',
        '\t\t\t\t\texecCtx = coreusage.NextAttemptContext(execCtx)\n\t\t\t\t\tresp, errExec = executor.CountTokens(execCtx, auth, execReq, execOpts)\n',
        'execCtx = coreusage.NextAttemptContext(execCtx)\n\t\t\t\t\tresp, errExec = executor.CountTokens',
    ),
    (
        auth_conductor_home,
        '\t\t\tresp, errExec := c.executor.Execute(creditsCtx, c.auth, execReq, creditsOpts)\n',
        '\t\t\tcreditsCtx = coreusage.NextAttemptContext(creditsCtx)\n\t\t\tresp, errExec := c.executor.Execute(creditsCtx, c.auth, execReq, creditsOpts)\n',
        'creditsCtx = coreusage.NextAttemptContext(creditsCtx)',
    ),
):
    replace_once(target, old_call, new_call, marker)

home_attempt_marker = '\t\t\t\tattemptCtx := coreusage.NextAttemptContext(execCtx)\n'
closure_execution = '''\t\t\texecutorCtx := execCtx
\t\t\tif countTokens {
\t\t\t\texecutorCtx = withAccessTokenFingerprintObserver(execCtx, setEffectiveAuth)
\t\t\t}
\t\t\texecute := func() (cliproxyexecutor.Response, error) {
\t\t\t\tif countTokens {
\t\t\t\t\treturn selection.Executor.CountTokens(executorCtx, preparedAuth, execReq, execOpts)
\t\t\t\t}
\t\t\t\treturn selection.Executor.Execute(execCtx, preparedAuth, execReq, execOpts)
\t\t\t}
'''
tracked_closure_execution = '''\t\t\texecute := func() (cliproxyexecutor.Response, error) {
\t\t\t\tattemptCtx := coreusage.NextAttemptContext(execCtx)
\t\t\t\tif countTokens {
\t\t\t\t\tattemptCtx = withAccessTokenFingerprintObserver(attemptCtx, setEffectiveAuth)
\t\t\t\t\treturn selection.Executor.CountTokens(attemptCtx, preparedAuth, execReq, execOpts)
\t\t\t\t}
\t\t\t\treturn selection.Executor.Execute(attemptCtx, preparedAuth, execReq, execOpts)
\t\t\t}
'''
replace_once(
    auth_conductor_home_execution,
    closure_execution,
    tracked_closure_execution,
    home_attempt_marker,
)
usage_helpers = ROOT / 'internal/runtime/executor/helps/usage_helpers.go'
replace_once(
    usage_helpers,
    '''\treasoning           string
\tserviceTier         string
\tgenerate            bool
''',
    '''\treasoning           string
\tserviceTier         string
\tspeed               string
\tgenerate            bool
''',
    '\tspeed           string\n',
)
replace_once(
    usage_helpers,
    '''\t\treasoning:   usage.ReasoningEffortFromContext(ctx),
\t\tserviceTier: usage.ServiceTierFromContext(ctx),
\t\tgenerate:    usage.GenerateFromContext(ctx),
''',
    '''\t\treasoning:   usage.ReasoningEffortFromContext(ctx),
\t\tserviceTier: usage.ServiceTierFromContext(ctx),
\t\tspeed:       usage.SpeedFromContext(ctx),
\t\tgenerate:    usage.GenerateFromContext(ctx),
''',
    'speed:       usage.SpeedFromContext(ctx)',
)
replace_once(
    usage_helpers,
    '''\t\tServiceTier:         r.serviceTier,
\t\tResponseServiceTier: strings.TrimSpace(detail.ResponseServiceTier),
\t\tGenerate:            usage.GenerateFlag(r.generate),
''',
    '''\t\tServiceTier:         r.serviceTier,
\t\tResponseServiceTier: strings.TrimSpace(detail.ResponseServiceTier),
\t\tSpeed:               r.speed,
\t\tResponseSpeed:       strings.TrimSpace(detail.ResponseSpeed),
\t\tGenerate:            usage.GenerateFlag(r.generate),
''',
    'ResponseSpeed:       strings.TrimSpace(detail.ResponseSpeed)',
)
replace_once(
    usage_helpers,
    '''\tusageNode := gjson.GetBytes(payload, "usage")
\tif !usageNode.Exists() {
\t\treturn usage.Detail{}, false
\t}
\treturn parseClaudeUsageNode(usageNode), true
''',
    '''\tusageNode := gjson.GetBytes(payload, "usage")
\tif !usageNode.Exists() {
\t\tusageNode = gjson.GetBytes(payload, "message.usage")
\t}
\tif !usageNode.Exists() {
\t\treturn usage.Detail{}, false
\t}
\treturn parseClaudeUsageNode(usageNode), true
''',
    'usageNode = gjson.GetBytes(payload, "message.usage")',
)
replace_once(
    usage_helpers,
    '''\t\tCacheReadTokens:     cacheReadTokens,
\t\tCacheCreationTokens: cacheCreationTokens,
\t}
''',
    '''\t\tCacheReadTokens:     cacheReadTokens,
\t\tCacheCreationTokens: cacheCreationTokens,
\t\tResponseSpeed:       strings.TrimSpace(usageNode.Get("speed").String()),
\t}
''',
    'ResponseSpeed:       strings.TrimSpace(usageNode.Get("speed").String())',
)
replace_once(
    usage_helpers,
    '''func (r *UsageReporter) PublishFailure(ctx context.Context, errs ...error) {
\tr.publishWithOutcome(ctx, usage.Detail{}, true, failFromErrors(errs...))
}
''',
    '''func (r *UsageReporter) PublishFailure(ctx context.Context, errs ...error) {
\tr.publishWithOutcome(ctx, usage.Detail{}, true, failFromErrors(errs...))
}

// PublishFailureWithDetail emits one failed record while preserving usage
// already observed before a streaming request terminated.
func (r *UsageReporter) PublishFailureWithDetail(ctx context.Context, detail usage.Detail, errs ...error) {
\tr.publishWithOutcome(ctx, detail, true, failFromErrors(errs...))
}
''',
    'func (r *UsageReporter) PublishFailureWithDetail(',
)
replace_once(
    usage_helpers,
    '''\tresponseServiceTier := strings.TrimSpace(detail.ResponseServiceTier)
\tif responseServiceTier == "" || hasNonZeroTokenUsage(detail) {
\t\tpreservedTier := b.detail.ResponseServiceTier
\t\tb.detail = detail
\t\tif b.detail.ResponseServiceTier == "" {
\t\t\tb.detail.ResponseServiceTier = preservedTier
\t\t}
\t} else {
\t\tb.detail.ResponseServiceTier = responseServiceTier
\t}
\tb.ok = true
}
''',
    '''\tresponseServiceTier := strings.TrimSpace(detail.ResponseServiceTier)
\tresponseSpeed := strings.TrimSpace(detail.ResponseSpeed)
\tif (responseServiceTier == "" && responseSpeed == "") || hasNonZeroTokenUsage(detail) {
\t\tpreservedTier := b.detail.ResponseServiceTier
\t\tpreservedSpeed := b.detail.ResponseSpeed
\t\tb.detail = detail
\t\tif b.detail.ResponseServiceTier == "" {
\t\t\tb.detail.ResponseServiceTier = preservedTier
\t\t}
\t\tif b.detail.ResponseSpeed == "" {
\t\t\tb.detail.ResponseSpeed = preservedSpeed
\t\t}
\t} else {
\t\tif responseServiceTier != "" {
\t\t\tb.detail.ResponseServiceTier = responseServiceTier
\t\t}
\t\tif responseSpeed != "" {
\t\t\tb.detail.ResponseSpeed = responseSpeed
\t\t}
\t}
\tb.ok = true
}

''',
    'responseSpeed := strings.TrimSpace(detail.ResponseSpeed)',
)

stream_usage_failure_signature = 'func (b *StreamUsageBuffer) PublishFailure(ctx context.Context, reporter *UsageReporter, errs ...error) bool'
stream_usage_failure_function = '''func (b *StreamUsageBuffer) PublishFailure(ctx context.Context, reporter *UsageReporter, errs ...error) bool {
\tif b == nil || !b.ok || reporter == nil {
\t\treturn false
\t}
\treporter.PublishFailureWithDetail(ctx, b.detail, errs...)
\treturn true
}
'''
replace_go_function(
    usage_helpers,
    stream_usage_failure_signature,
    stream_usage_failure_function,
    stream_usage_failure_signature + ' {\n\tif b == nil || !b.ok || reporter == nil {',
)

claude_execute = ROOT / 'internal/runtime/executor/claude_executor_execute.go'
replace_once(
    claude_execute,
    '''\tif upstreamStream {
\t\tif errValidate := validateClaudeStreamingResponse(data); errValidate != nil {
''',
    '''\tvar responseUsageBuffer helps.StreamUsageBuffer
\tif upstreamStream {
\t\tif errValidate := validateClaudeStreamingResponse(data); errValidate != nil {
''',
    'var responseUsageBuffer helps.StreamUsageBuffer',
)
replace_once(
    claude_execute,
    '''\t\tlines := bytes.Split(data, []byte("\\n"))
\t\tfor i, line := range lines {
\t\t\tif detail, ok := helps.ParseClaudeStreamUsage(line); ok {
\t\t\t\treporter.Publish(ctx, detail)
\t\t\t}
''',
    '''\t\tlines := bytes.Split(data, []byte("\\n"))
\t\tfor i, line := range lines {
\t\t\tresponseUsageBuffer.ObserveClaude(helps.ParseClaudeStreamUsage(line))
''',
    'responseUsageBuffer.ObserveClaude(',
)
replace_once(
    claude_execute,
    '''\t\t\trestoredLine, errRestore := restoreClaudeOAuthToolNamesFromStreamLine(line, oauthToolNamesReverseMap)
\t\t\tif errRestore != nil {
\t\t\t\terrRestore = fmt.Errorf("restore Claude OAuth tool name from streaming response: %w", errRestore)
\t\t\t\thelps.RecordAPIResponseError(ctx, e.cfg, errRestore)
\t\t\t\treturn resp, wrapClaudeFastRequestError(fastRequest, httpResp.StatusCode, errRestore)
\t\t\t}
''',
    '''\t\t\trestoredLine, errRestore := restoreClaudeOAuthToolNamesFromStreamLine(line, oauthToolNamesReverseMap)
\t\t\tif errRestore != nil {
\t\t\t\terrRestore = fmt.Errorf("restore Claude OAuth tool name from streaming response: %w", errRestore)
\t\t\t\thelps.RecordAPIResponseError(ctx, e.cfg, errRestore)
\t\t\t\terr = wrapClaudeFastRequestError(fastRequest, httpResp.StatusCode, errRestore)
\t\t\t\tif !responseUsageBuffer.PublishFailure(ctx, reporter, err) {
\t\t\t\t\treporter.PublishFailure(ctx, err)
\t\t\t\t}
\t\t\t\treturn resp, err
\t\t\t}
''',
)
replace_once(
    claude_execute,
    '''\t\tif errRestore != nil {
\t\t\terrRestore = fmt.Errorf("restore Claude OAuth tool name from response: %w", errRestore)
\t\t\thelps.RecordAPIResponseError(ctx, e.cfg, errRestore)
\t\t\treturn resp, wrapClaudeFastRequestError(fastRequest, httpResp.StatusCode, errRestore)
\t\t}
''',
    '''\t\tif errRestore != nil {
\t\t\terrRestore = fmt.Errorf("restore Claude OAuth tool name from response: %w", errRestore)
\t\t\thelps.RecordAPIResponseError(ctx, e.cfg, errRestore)
\t\t\terr = wrapClaudeFastRequestError(fastRequest, httpResp.StatusCode, errRestore)
\t\t\treporter.PublishFailureWithDetail(ctx, helps.ParseClaudeUsage(data), err)
\t\t\treturn resp, err
\t\t}
''',
)
replace_once(
    claude_execute,
    '''\t\t\tlines[i] = restoredLine
\t\t}
\t\tdata = bytes.Join(lines, []byte("\\n"))
''',
    '''\t\t\tlines[i] = restoredLine
\t\t}
\t\tdata = bytes.Join(lines, []byte("\\n"))
''',
    'responseUsageBuffer.ObserveClaude(',
)
replace_once(
    claude_execute,
    '''\t} else {
\t\tcommitClaudeDiagnostics(diagnosticsState, claudeMessageIDFromResponse(data))
\t\treporter.Publish(ctx, helps.ParseClaudeUsage(data))
\t\tvar errRestore error
''',
    '''\t} else {
\t\tcommitClaudeDiagnostics(diagnosticsState, claudeMessageIDFromResponse(data))
\t\tvar errRestore error
''',
)
# Keep the replacement scoped to the translator call. Upstream may insert
# response-format post-processing between translation and response assembly,
# and those transformations must remain authoritative.
replace_once(
    claude_execute,
    '''\tvar param any
\tout := sdktranslator.TranslateNonStream(
\t\tctx,
\t\tto,
\t\tresponseFormat,
\t\treq.Model,
\t\topts.OriginalRequest,
\t\tbodyForTranslation,
\t\tdata,
\t\t&param,
\t)
''',
    '''\tvar param any
\tout, errTranslate := translateNonStreamResponse(
\t\tctx,
\t\tto,
\t\tresponseFormat,
\t\treq.Model,
\t\topts.OriginalRequest,
\t\tbodyForTranslation,
\t\tdata,
\t\t&param,
\t)
\tif errTranslate != nil {
\t\terr = wrapClaudeFastRequestError(fastRequest, httpResp.StatusCode, errTranslate)
\t\tif upstreamStream {
\t\t\tif !responseUsageBuffer.PublishFailure(ctx, reporter, err) {
\t\t\t\treporter.PublishFailure(ctx, err)
\t\t\t}
\t\t} else {
\t\t\treporter.PublishFailureWithDetail(ctx, helps.ParseClaudeUsage(data), err)
\t\t}
\t\treturn resp, err
\t}
''',
    'translateNonStreamResponse(',
)
insert_before(
    claude_execute,
    '\tresp = cliproxyexecutor.Response{Payload: out, Headers: httpResp.Header.Clone()}\n',
    '''\tif upstreamStream {
\t\tresponseUsageBuffer.Publish(ctx, reporter)
\t} else {
\t\treporter.Publish(ctx, helps.ParseClaudeUsage(data))
\t}
\treporter.EnsurePublished(ctx)
''',
    '''\treporter.EnsurePublished(ctx)
\tresp = cliproxyexecutor.Response{Payload: out, Headers: httpResp.Header.Clone()}
''',
)

openai_compat_execute = ROOT / 'internal/runtime/executor/openai_compat_executor.go'
replace_once(
    openai_compat_execute,
    '''\thelps.AppendAPIResponseChunk(ctx, e.cfg, body)
\treporter.Publish(ctx, helps.ParseOpenAIUsage(body))
\t// Ensure we at least record the request even if upstream doesn't return usage
\treporter.EnsurePublished(ctx)
\t// Translate response back to source format when needed
''',
    '''\thelps.AppendAPIResponseChunk(ctx, e.cfg, body)
\t// Translate response back to source format before publishing success. A
\t// translator panic is an upstream-attempt failure and must win the one-shot
\t// terminal usage publication while retaining the parsed token detail.
''',
    'translator panic is an upstream-attempt failure',
)
replace_once(
    openai_compat_execute,
    '''\tvar param any
\tout := sdktranslator.TranslateNonStream(ctx, to, responseFormat, req.Model, opts.OriginalRequest, translated, body, &param)
''',
    '''\tvar param any
\tout, errTranslate := translateNonStreamResponse(ctx, to, responseFormat, req.Model, opts.OriginalRequest, translated, body, &param)
\tif errTranslate != nil {
\t\terr = errTranslate
\t\treporter.PublishFailureWithDetail(ctx, helps.ParseOpenAIUsage(body), err)
\t\treturn resp, err
\t}
''',
    'out, errTranslate := translateNonStreamResponse(ctx, to, responseFormat',
)
insert_before(
    openai_compat_execute,
    '\tresp = cliproxyexecutor.Response{Payload: out, Headers: httpResp.Header.Clone()}\n',
    '''\treporter.Publish(ctx, helps.ParseOpenAIUsage(body))
\t// Ensure we at least record the request even if upstream doesn't return usage.
\treporter.EnsurePublished(ctx)
''',
    '''\treporter.EnsurePublished(ctx)
\tresp = cliproxyexecutor.Response{Payload: out, Headers: httpResp.Header.Clone()}
''',
)
replace_once(
    openai_compat_execute,
    '''\t\tvar streamUsage helps.StreamUsageBuffer
\t\tvar seenDone bool
''',
    '''\t\tvar streamUsage helps.StreamUsageBuffer
\t\tvar seenDone bool
\t\tpublishStreamFailure := func(errStream error) {
\t\t\tif !streamUsage.PublishFailure(ctx, reporter, errStream) {
\t\t\t\treporter.PublishFailure(ctx, errStream)
\t\t\t}
\t\t}
''',
    'publishStreamFailure := func(errStream error)',
)
openai_stream_text = read(openai_compat_execute)
deferred_stream_publish = '''\t\tdefer streamUsage.Publish(ctx, reporter)
'''
if openai_stream_text.count(deferred_stream_publish) != 1:
    raise SystemExit('expected one deferred OpenAI-compatible stream usage publish')
openai_stream_text = openai_stream_text.replace(deferred_stream_publish, '', 1)
openai_stream_start = '''func (e *OpenAICompatExecutor) ExecuteStream'''
openai_stream_end = '''func (e *OpenAICompatExecutor) executeImagesStream'''
if openai_stream_text.count(openai_stream_start) != 1 or openai_stream_text.count(openai_stream_end) != 1:
    raise SystemExit('expected one OpenAI-compatible stream function boundary')
openai_stream_prefix, openai_stream_body = openai_stream_text.split(openai_stream_start, 1)
openai_stream_body, openai_stream_suffix = openai_stream_body.split(openai_stream_end, 1)
stream_failure_publish = 'reporter.PublishFailure(ctx, streamErr)'
stream_failure_count = openai_stream_body.count(stream_failure_publish)
if stream_failure_count != 1:
    raise SystemExit(
        f'expected one OpenAI-compatible stream error publication, found {stream_failure_count}'
    )
openai_stream_body = openai_stream_body.replace(
    stream_failure_publish,
    'publishStreamFailure(streamErr)',
    stream_failure_count,
)
logged_stream_failure = 'reporter.PublishFailure(ctx, loggedErr)'
logged_stream_failure_count = openai_stream_body.count(logged_stream_failure)
if logged_stream_failure_count != 1:
    raise SystemExit('expected one sanitized OpenAI-compatible stream error publication')
openai_stream_body = openai_stream_body.replace(
    logged_stream_failure,
    'publishStreamFailure(loggedErr)',
    logged_stream_failure_count,
)
stream_cancel_return = '''\t\t\t\tcase <-ctx.Done():
\t\t\t\t\treturn
'''
stream_cancel_count = openai_stream_body.count(stream_cancel_return)
if stream_cancel_count != 1:
    raise SystemExit(
        f'expected one OpenAI-compatible stream cancellation return, found {stream_cancel_count}'
    )
openai_stream_body = openai_stream_body.replace(
    stream_cancel_return,
    '''\t\t\t\tcase <-ctx.Done():
\t\t\t\t\tpublishStreamFailure(ctx.Err())
\t\t\t\t\treturn
''',
    stream_cancel_count,
)
stream_scan_failure = '''\t\t\treporter.PublishFailure(ctx, errScan)
'''
if openai_stream_body.count(stream_scan_failure) != 1:
    raise SystemExit('expected one OpenAI-compatible scanner failure publication')
openai_stream_body = openai_stream_body.replace(
    stream_scan_failure,
    '''\t\t\tpublishStreamFailure(errScan)
''',
    1,
)
openai_stream_text = (
    openai_stream_prefix
    + openai_stream_start
    + openai_stream_body
    + openai_stream_end
    + openai_stream_suffix
)
write(openai_compat_execute, openai_stream_text)
replace_once(
    openai_compat_execute,
    '''\t\tstreamUsage.Publish(ctx, reporter)
\t\treporter.EnsurePublished(ctx)
''',
    '''\t\tstreamUsage.Publish(ctx, reporter)
\t\treporter.EnsurePublished(ctx)
''',
    'publishStreamFailure := func(errStream error)',
)

claude_stream = ROOT / 'internal/runtime/executor/claude_executor_stream.go'
replace_once(
    claude_stream,
    '''\t\t\tvar event bytes.Buffer
\t\t\tvar upstreamMessageID string
''',
    '''\t\t\tvar event bytes.Buffer
\t\t\tvar usageBuffer helps.StreamUsageBuffer
\t\t\tvar upstreamMessageID string
''',
    'var usageBuffer helps.StreamUsageBuffer',
)
stream_publish_block = '''\t\t\t\tif detail, ok := helps.ParseClaudeStreamUsage(line); ok {
\t\t\t\t\treporter.Publish(ctx, detail)
\t\t\t\t}
'''
stream_observe_block = '''\t\t\t\tusageBuffer.ObserveClaude(helps.ParseClaudeStreamUsage(line))
'''
stream_text = read(claude_stream)
if stream_observe_block not in stream_text:
    if stream_text.count(stream_publish_block) != 1:
        raise SystemExit('expected one native Claude stream usage publish block')
    write(claude_stream, stream_text.replace(stream_publish_block, stream_observe_block, 1))
replace_once(
    claude_stream,
    '''\t\t\tif upstreamCompleted {
\t\t\t\tcommitClaudeDiagnostics(diagnosticsState, upstreamMessageID)
\t\t\t}
\t\t\treturn
''',
    '''\t\t\tif upstreamCompleted {
\t\t\t\tcommitClaudeDiagnostics(diagnosticsState, upstreamMessageID)
\t\t\t}
\t\t\tterminal.publishSuccess(&usageBuffer)
\t\t\treturn
''',
    'terminal.publishSuccess(&usageBuffer)\n\t\t\treturn',
)
replace_once(
    claude_stream,
    '''\t\tvar param any
\t\tvar upstreamMessageID string
''',
    '''\t\tvar param any
\t\tvar usageBuffer helps.StreamUsageBuffer
\t\tvar upstreamMessageID string
''',
    'var param any\n\t\tvar usageBuffer helps.StreamUsageBuffer',
)
stream_publish_block = '''\t\t\tif detail, ok := helps.ParseClaudeStreamUsage(line); ok {
\t\t\t\treporter.Publish(ctx, detail)
\t\t\t}
'''
stream_observe_block = '''\t\t\tusageBuffer.ObserveClaude(helps.ParseClaudeStreamUsage(line))
'''
stream_text = read(claude_stream)
if stream_publish_block in stream_text:
    if stream_text.count(stream_publish_block) != 1:
        raise SystemExit('expected one translated Claude stream usage publish block')
    write(claude_stream, stream_text.replace(stream_publish_block, stream_observe_block, 1))
elif stream_text.count(stream_observe_block) < 2:
    raise SystemExit('translated Claude stream usage buffer patch missing')
replace_once(
    claude_stream,
    '''\t\tif upstreamCompleted {
\t\t\tcommitClaudeDiagnostics(diagnosticsState, upstreamMessageID)
\t\t}
\t}()
''',
    '''\t\tif upstreamCompleted {
\t\t\tcommitClaudeDiagnostics(diagnosticsState, upstreamMessageID)
\t\t}
\t\tterminal.publishSuccess(&usageBuffer)
\t}()
''',
    'terminal.publishSuccess(&usageBuffer)\n\t}()',
)
claude_failure_helpers_old = '''\t\temitCancellation := func(cause error) bool {
\t\t\tcancelErr := newClaudeOAuthCancellationError(ctx, fp.OAuthCancellation, cause)
\t\t\tif cancelErr == nil {
\t\t\t\treturn false
\t\t\t}
\t\t\thelps.RecordAPIResponseError(ctx, e.cfg, cancelErr)
\t\t\treporter.PublishFailure(ctx, cancelErr)
\t\t\tselect {
\t\t\tcase out <- cliproxyexecutor.StreamChunk{Err: cancelErr}:
\t\t\tdefault:
\t\t\t}
\t\t\treturn true
\t\t}
\t\temitResponseError := func(errResponse error) {
\t\t\terrResponse = wrapClaudeFastRequestError(fastRequest, httpResp.StatusCode, errResponse)
\t\t\thelps.RecordAPIResponseError(ctx, e.cfg, errResponse)
\t\t\treporter.PublishFailure(ctx, errResponse)
\t\t\tselect {
\t\t\tcase out <- cliproxyexecutor.StreamChunk{Err: errResponse}:
\t\t\tcase <-ctx.Done():
\t\t\t}
\t\t}
'''
claude_terminal_init = '''\t\tterminal := claudeStreamTerminal{
\t\t\tctx: ctx, cfg: e.cfg, reporter: reporter, out: out,
\t\t\tstatusCode: httpResp.StatusCode, fastRequest: fastRequest,
\t\t\toauthCancellation: fp.OAuthCancellation,
\t\t}
'''
replace_once(
    claude_stream,
    claude_failure_helpers_old,
    claude_terminal_init,
    'terminal := claudeStreamTerminal{',
)

stream_text = read(claude_stream)
restore_failure_call = 'emitResponseError(fmt.Errorf("restore Claude OAuth tool name from streaming response: %w", errRestore))'
if stream_text.count(restore_failure_call) != 2:
    raise SystemExit('expected two Claude stream restore failure calls')
stream_text = stream_text.replace(
    restore_failure_call,
    'terminal.emitResponseError(&usageBuffer, fmt.Errorf("restore Claude OAuth tool name from streaming response: %w", errRestore))',
)
cancellation_block = '''\t\t\t\tif len(bytes.TrimSpace(line)) == 0 && !flushEvent() {
\t\t\t\t\temitCancellation(ctx.Err())
\t\t\t\t\treturn
\t\t\t\t}
'''
cancellation_replacement = '''\t\t\t\tif len(bytes.TrimSpace(line)) == 0 && !flushEvent() {
\t\t\t\t\tterminal.publishCancellation(&usageBuffer, ctx.Err())
\t\t\t\t\treturn
\t\t\t\t}
'''
if stream_text.count(cancellation_block) != 1:
    raise SystemExit('expected native Claude event flush cancellation block')
stream_text = stream_text.replace(cancellation_block, cancellation_replacement, 1)
final_flush_block = '''\t\t\tif !flushEvent() {
\t\t\t\temitCancellation(ctx.Err())
\t\t\t\treturn
\t\t\t}
'''
final_flush_replacement = '''\t\t\tif !flushEvent() {
\t\t\t\tterminal.publishCancellation(&usageBuffer, ctx.Err())
\t\t\t\treturn
\t\t\t}
'''
if stream_text.count(final_flush_block) != 1:
    raise SystemExit('expected native Claude final flush cancellation block')
stream_text = stream_text.replace(final_flush_block, final_flush_replacement, 1)
translated_cancellation_block = '''\t\t\t\tcase <-ctx.Done():
\t\t\t\t\temitCancellation(ctx.Err())
\t\t\t\t\treturn
'''
translated_cancellation_replacement = '''\t\t\t\tcase <-ctx.Done():
\t\t\t\t\tterminal.publishCancellation(&usageBuffer, ctx.Err())
\t\t\t\t\treturn
'''
if stream_text.count(translated_cancellation_block) != 1:
    raise SystemExit('expected translated Claude output cancellation block')
stream_text = stream_text.replace(translated_cancellation_block, translated_cancellation_replacement, 1)
native_scanner_outcome = '''\t\t\tif emitCancellation(scanner.Err()) {
\t\t\t\treturn
\t\t\t}
\t\t\tif errScan := scanner.Err(); errScan != nil {
\t\t\t\terrScan = wrapClaudeFastRequestError(fastRequest, httpResp.StatusCode, errScan)
\t\t\t\thelps.RecordAPIResponseError(ctx, e.cfg, errScan)
\t\t\t\treporter.PublishFailure(ctx, errScan)
\t\t\t\tselect {
\t\t\t\tcase out <- cliproxyexecutor.StreamChunk{Err: errScan}:
\t\t\t\tcase <-ctx.Done():
\t\t\t\t}
\t\t\t\treturn
\t\t\t}
'''
native_scanner_replacement = '''\t\t\tif terminal.finishScanner(&usageBuffer, scanner.Err()) {
\t\t\t\treturn
\t\t\t}
'''
if stream_text.count(native_scanner_outcome) != 1:
    raise SystemExit('expected native Claude scanner outcome block')
stream_text = stream_text.replace(native_scanner_outcome, native_scanner_replacement, 1)
translated_scanner_outcome = '''\t\tif emitCancellation(scanner.Err()) {
\t\t\treturn
\t\t}
\t\tif errScan := scanner.Err(); errScan != nil {
\t\t\terrScan = wrapClaudeFastRequestError(fastRequest, httpResp.StatusCode, errScan)
\t\t\thelps.RecordAPIResponseError(ctx, e.cfg, errScan)
\t\t\treporter.PublishFailure(ctx, errScan)
\t\t\tselect {
\t\t\tcase out <- cliproxyexecutor.StreamChunk{Err: errScan}:
\t\t\tcase <-ctx.Done():
\t\t\t}
\t\t\treturn
\t\t}
'''
translated_scanner_replacement = '''\t\tif terminal.finishScanner(&usageBuffer, scanner.Err()) {
\t\t\treturn
\t\t}
'''
if stream_text.count(translated_scanner_outcome) != 1:
    raise SystemExit('expected translated Claude scanner outcome block')
stream_text = stream_text.replace(translated_scanner_outcome, translated_scanner_replacement, 1)
write(claude_stream, stream_text)
replace_once(
    usage_helpers,
    '''func (r *UsageReporter) publishRecord(ctx context.Context, record usage.Record) {
\trecord.ResponseHeaders = internallogging.GetResponseHeaders(ctx)
\tusage.PublishRecord(ctx, record)
}
''',
    '''func (r *UsageReporter) publishRecord(ctx context.Context, record usage.Record) {
\tprepareUsageRecordForPublish(ctx, &record)
\tusage.PublishRecord(ctx, record)
}
''',
    'prepareUsageRecordForPublish(ctx, &record)',
)
replace_once(
    usage_helpers,
    '\tinternallogging "' + import_path('internal/logging') + '"\n',
    '',
)

config_defaults = ROOT / 'internal/config/config_defaults.go'
replace_once(
    config_defaults,
    'DefaultPanelGitHubRepository = "https://github.com/router-for-me/Cli-Proxy-API-Management-Center"',
    f'DefaultPanelGitHubRepository = "{PRO_PANEL_REPOSITORY}"',
)

config_existing_updates = ROOT / 'internal/config/config_existing_updates.go'
write(config_existing_updates, read_text(Path(__file__).resolve().parent / 'config_existing_updates.go'))
config_existing_updates_test = ROOT / 'internal/config/config_existing_updates_test.go'
write(config_existing_updates_test, read_text(Path(__file__).resolve().parent / 'config_existing_updates_test.go'))

config_example = ROOT / 'config.example.yaml'
replace_once(
    config_example,
    '  panel-github-repository: "https://github.com/router-for-me/Cli-Proxy-API-Management-Center"',
    f'  panel-github-repository: "{PRO_PANEL_REPOSITORY}"',
)
replace_once(
    config_example,
    '''      mode: "safe" # enum example: safe, fast

# When true, disable high-overhead request logging and HTTP middleware features to reduce per-request memory usage under high concurrency.
''',
    '''      mode: "safe" # enum example: safe, fast

    # Optional Pro plugin: subtract xAI OAuth models by detected account plan.
    # oauth-model-policy:
    #   enabled: true
    #   priority: 10
    #   cache-ttl: 30m
    #   resolve-timeout: 15s
    #   providers:
    #     xai:
    #       plans:
    #         free:
    #           excluded-models: ["grok-pro-*"]
    #         supergrok:
    #           excluded-models: ["grok-4.5-*"]
    #         _unknown:
    #           excluded-models: ["grok-pro-*"]

# When true, disable high-overhead request logging and HTTP middleware features to reduce per-request memory usage under high concurrency.
''',
    'Optional Pro plugin: subtract xAI OAuth models by detected account plan.',
)
config_yaml = ROOT / 'internal/config/config_yaml.go'
insert_before(
    config_yaml,
    '// NormalizeCommentIndentation removes indentation from standalone YAML comment lines to keep them left aligned.\n',
    '// SaveConfigPreserveCommentsUpdateNestedBoolScalar updates a nested bool scalar while preserving comments and positions.\nfunc SaveConfigPreserveCommentsUpdateNestedBoolScalar(configFile string, path []string, value bool) error {\n\tdata, err := os.ReadFile(configFile)\n\tif err != nil {\n\t\treturn err\n\t}\n\tvar root yaml.Node\n\tif err = yaml.Unmarshal(data, &root); err != nil {\n\t\treturn err\n\t}\n\tif root.Kind != yaml.DocumentNode || len(root.Content) == 0 {\n\t\treturn fmt.Errorf("invalid yaml document structure")\n\t}\n\tnode := root.Content[0]\n\tfor i, key := range path {\n\t\tif i == len(path)-1 {\n\t\t\tv := getOrCreateMapValue(node, key)\n\t\t\tv.Kind = yaml.ScalarNode\n\t\t\tv.Tag = "!!bool"\n\t\t\tif value {\n\t\t\t\tv.Value = "true"\n\t\t\t} else {\n\t\t\t\tv.Value = "false"\n\t\t\t}\n\t\t} else {\n\t\t\tnext := getOrCreateMapValue(node, key)\n\t\t\tif next.Kind != yaml.MappingNode {\n\t\t\t\tnext.Kind = yaml.MappingNode\n\t\t\t\tnext.Tag = "!!map"\n\t\t\t}\n\t\t\tnode = next\n\t\t}\n\t}\n\tf, err := os.Create(configFile)\n\tif err != nil {\n\t\treturn err\n\t}\n\tdefer func() { _ = f.Close() }()\n\tvar buf bytes.Buffer\n\tenc := yaml.NewEncoder(&buf)\n\tenc.SetIndent(2)\n\tif err = enc.Encode(&root); err != nil {\n\t\t_ = enc.Close()\n\t\treturn err\n\t}\n\tif err = enc.Close(); err != nil {\n\t\treturn err\n\t}\n\tdata = NormalizeCommentIndentation(buf.Bytes())\n\t_, err = f.Write(data)\n\treturn err\n}\n\n',
    'func SaveConfigPreserveCommentsUpdateNestedBoolScalar',
)
replace_go_function(
    config_yaml,
    'func SaveConfigPreserveCommentsUpdateNestedBoolScalar',
    '// SaveConfigPreserveCommentsUpdateNestedBoolScalar updates an existing bool scalar without creating missing keys.\nfunc SaveConfigPreserveCommentsUpdateNestedBoolScalar(configFile string, path []string, value bool) error {\n\t_, err := SaveConfigPreserveCommentsUpdateExistingScalars(configFile, []ExistingScalarUpdate{{Path: path, Value: value}})\n\treturn err\n}\n',
    'SaveConfigPreserveCommentsUpdateExistingScalars(configFile, []ExistingScalarUpdate',
)
insert_before(
    config_yaml,
    '// NormalizeCommentIndentation removes indentation from standalone YAML comment lines to keep them left aligned.\n',
    '// PluginAutoInstallProxyURL returns the proxy URL used by plugin store auto-install requests.\nfunc (cfg *Config) PluginAutoInstallProxyURL() string {\n\tif cfg == nil {\n\t\treturn ""\n\t}\n\treturn cfg.ProxyURL\n}\n\n// PluginAutoInstallEnabled reports whether dynamic plugins are enabled.\nfunc (cfg *Config) PluginAutoInstallEnabled() bool {\n\treturn cfg != nil && cfg.Plugins.Enabled\n}\n\n// PluginAutoInstallDir returns the normalized plugin discovery directory.\nfunc (cfg *Config) PluginAutoInstallDir() string {\n\tif cfg == nil {\n\t\treturn ""\n\t}\n\treturn cfg.Plugins.Dir\n}\n\n// PluginAutoInstallStoreSources returns configured third-party plugin registry URLs.\nfunc (cfg *Config) PluginAutoInstallStoreSources() []string {\n\tif cfg == nil || len(cfg.Plugins.StoreSources) == 0 {\n\t\treturn nil\n\t}\n\treturn append([]string(nil), cfg.Plugins.StoreSources...)\n}\n\n// PluginAutoInstallEnabledIDs returns configured plugin IDs that should be present at startup.\nfunc (cfg *Config) PluginAutoInstallEnabledIDs() []string {\n\tif cfg == nil || len(cfg.Plugins.Configs) == 0 {\n\t\treturn nil\n\t}\n\tids := make([]string, 0, len(cfg.Plugins.Configs))\n\tfor id, item := range cfg.Plugins.Configs {\n\t\tif item.Enabled == nil || !*item.Enabled {\n\t\t\tcontinue\n\t\t}\n\t\tids = append(ids, id)\n\t}\n\treturn ids\n}\n\n',
    'func (cfg *Config) PluginAutoInstallProxyURL',
)
config_normalization = ROOT / 'internal/config/config_normalization.go'
insert_before(
    config_normalization,
    '// SanitizeCodexHeaderDefaults trims surrounding whitespace from the\n',
    '''// PluginAutoInstallStoreAuth returns normalized plugin store authentication rules.
func (cfg *Config) PluginAutoInstallStoreAuth() []sdkpluginstore.AuthConfig {
\tif cfg == nil || len(cfg.Plugins.StoreAuth) == 0 {
\t\treturn nil
\t}
\treturn append([]sdkpluginstore.AuthConfig(nil), cfg.Plugins.StoreAuth...)
}

''',
    'func (cfg *Config) PluginAutoInstallStoreAuth()',
)

updater = ROOT / 'internal/managementasset/updater.go'
replace_once(
    updater,
    'defaultManagementReleaseURL  = "https://api.github.com/repos/router-for-me/Cli-Proxy-API-Management-Center/releases/latest"',
    f'defaultManagementReleaseURL  = "{PRO_PANEL_RELEASE_API}"',
)
replace_once(
    updater,
    '''\tif token := util.ResolveGitHubToken(); token != "" {
\t\theaders["Authorization"] = "Bearer " + token
\t}
''',
    '''\ttoken := strings.TrimSpace(os.Getenv("GITSTORE_GIT_TOKEN"))
\tif token != "" {
\t\tif isGitHubAPIURL(releaseURL) {
\t\t\theaders["Authorization"] = "Bearer " + token
\t\t}
\t} else {
\t\ttoken = util.ResolveGitHubToken()
\t\tif token != "" {
\t\t\theaders["Authorization"] = "Bearer " + token
\t\t}
\t}
''',
    'token = util.ResolveGitHubToken()',
)
insert_before(
    updater,
    'func fetchLatestAsset(ctx context.Context, client *http.Client, releaseURL string) (*releaseAsset, string, error) {\n',
    '''func isGitHubAPIURL(requestURL string) bool {
\tparsed, err := url.Parse(strings.TrimSpace(requestURL))
\tif err != nil || parsed.Host == "" || parsed.User != nil {
\t\treturn false
\t}
\treturn strings.EqualFold(parsed.Scheme, "https") && strings.EqualFold(parsed.Hostname(), "api.github.com")
}

''',
    'func isGitHubAPIURL(requestURL string) bool',
)
replace_once(
    updater,
    '\tName               string `json:"name"`\n',
    '\tAPIURL             string `json:"url"`\n\tName               string `json:"name"`\n',
    'APIURL             string `json:"url"`',
)
replace_once(
    updater,
    'downloadAsset(ctx, client, asset.BrowserDownloadURL)',
    'downloadReleaseAsset(ctx, client, asset)',
)
insert_before(
    updater,
    'func downloadAsset(ctx context.Context, client *http.Client, downloadURL string) ([]byte, string, error) {\n',
    '''func downloadReleaseAsset(ctx context.Context, client *http.Client, asset *releaseAsset) ([]byte, string, error) {
\tif asset == nil {
\t\treturn nil, "", fmt.Errorf("nil management release asset")
\t}
\tif tok := strings.TrimSpace(os.Getenv("GITSTORE_GIT_TOKEN")); tok != "" && isGitHubAPIURL(asset.APIURL) {
\t\tdownloadURL := strings.TrimSpace(asset.APIURL)
\t\theaders := map[string]string{
\t\t\t"Accept":        "application/octet-stream",
\t\t\t"Authorization": "Bearer " + tok,
\t\t\t"User-Agent":    httpUserAgent,
\t\t}

\t\tdata, err := httpfetch.GetBytes(ctx, client, downloadURL, headers, maxAssetDownloadSize)
\t\tif err != nil {
\t\t\treturn nil, "", fmt.Errorf("download asset: %w", err)
\t\t}

\t\tsum := sha256.Sum256(data)
\t\treturn data, hex.EncodeToString(sum[:]), nil
\t}
\treturn downloadAsset(ctx, client, asset.BrowserDownloadURL)
}

''',
    'func downloadReleaseAsset',
)
queue_go_source('internal/managementasset/gitstore_token_test.go')
queue_go_source('internal/api/handlers/management/management_panel.go')
queue_go_source('internal/api/handlers/management/management_panel_test.go')

pluginstore_auth = ROOT / 'internal/pluginstore/auth.go'
insert_before(
    pluginstore_auth,
    'func AuthConfigured(auth []AuthConfig, requestURL string, kind string) bool {\n',
    '''func gitStoreGitHubToken(requestURL string, kind string) (string, bool) {
\tswitch strings.ToLower(strings.TrimSpace(kind)) {
\tcase RequestKindMetadata, RequestKindArtifact:
\tdefault:
\t\treturn "", false
\t}
\tparsed, err := url.Parse(strings.TrimSpace(requestURL))
\tif err != nil || parsed.User != nil || !strings.EqualFold(parsed.Scheme, "https") || !strings.EqualFold(parsed.Hostname(), "api.github.com") {
\t\treturn "", false
\t}
\tpath := strings.ToLower(parsed.Path)
\tif !strings.HasPrefix(path, "/repos/") || !strings.Contains(path, "/releases/") {
\t\treturn "", false
\t}
\ttoken := strings.TrimSpace(os.Getenv("GITSTORE_GIT_TOKEN"))
\treturn token, token != ""
}

func gitStoreGitHubTokenConfigured(requestURL string, kind string) bool {
\t_, configured := gitStoreGitHubToken(requestURL, kind)
\treturn configured
}

''',
    'func gitStoreGitHubToken(requestURL string, kind string)',
)
replace_once(
    pluginstore_auth,
    '''func AuthConfigured(auth []AuthConfig, requestURL string, kind string) bool {
\titem, ok := matchingAuthConfig(auth, requestURL, kind)
\tif !ok {
\t\treturn false
\t}
''',
    '''func AuthConfigured(auth []AuthConfig, requestURL string, kind string) bool {
\titem, ok := matchingAuthConfig(auth, requestURL, kind)
\tif !ok {
\t\treturn gitStoreGitHubTokenConfigured(requestURL, kind)
\t}
''',
    'return gitStoreGitHubTokenConfigured(requestURL, kind)',
)
replace_once(
    pluginstore_auth,
    '''\titem, ok := matchingAuthConfig(auth, requestURL, kind)
\tif !ok {
\t\treturn false, nil
\t}
\tswitch strings.ToLower(strings.TrimSpace(item.Type)) {
''',
    '''\titem, ok := matchingAuthConfig(auth, requestURL, kind)
\tif !ok {
\t\tif token, configured := gitStoreGitHubToken(requestURL, kind); configured {
\t\t\theaders.Set("Authorization", "Bearer "+token)
\t\t\treturn true, nil
\t\t}
\t\treturn false, nil
\t}
\tswitch strings.ToLower(strings.TrimSpace(item.Type)) {
''',
    'if token, configured := gitStoreGitHubToken(requestURL, kind); configured',
)
queue_go_source('internal/pluginstore/gitstore_auth_test.go')

server_main = ROOT / 'cmd/server/main.go'
add_go_import(server_main, '"' + import_path('internal/pluginhost') + '"\n', '\t"' + import_path('internal/pluginstore') + '"\n')
replace_once(
    server_main,
    '''\tconfigaccess.Register(&cfg.SDKConfig)
\tpluginHost.ApplyConfig(context.Background(), cfg)
''',
    '''\tconfigaccess.Register(&cfg.SDKConfig)
\tpluginstore.EnsureConfiguredPluginsInstalled(context.Background(), cfg)
\tpluginHost.ApplyConfig(context.Background(), cfg)
''',
)

replace_once(
    ROOT / 'internal/pluginhost/host_callbacks.go',
    '''\tstreamCtx, cancel := context.WithCancel(ctx)
\tresp, errDo := h.newHTTPClient(nil).DoStream(streamCtx, httpReq)
\tif errDo != nil {
\t\tcancel()
\t\treturn nil, errDo
\t}
\tstreamID := ""
\tif h != nil && h.httpStreams != nil {
\t\tstreamID = h.httpStreams.open(resp.Chunks, cancel)
\t}
\tif streamID == "" {
\t\tcancel()
\t\treturn nil, fmt.Errorf("host http stream bridge is unavailable")
\t}
\treturn marshalRPCResult(rpcHostHTTPStreamResponse{
\t\tStatusCode: resp.StatusCode,
\t\tHeaders:    httpHeader(resp.Headers),
\t\tStreamID:   streamID,
\t})
''',
    '''\tstreamCtx, cancel := context.WithCancel(ctx)
\tcancelOwned := true
\tdefer func() {
\t\tif cancelOwned {
\t\t\tcancel()
\t\t}
\t}()
\tresp, errDo := h.newHTTPClient(nil).DoStream(streamCtx, httpReq)
\tif errDo != nil {
\t\treturn nil, errDo
\t}
\tstreamID := ""
\tif h != nil && h.httpStreams != nil {
\t\tstreamID = h.httpStreams.open(resp.Chunks, cancel)
\t}
\tif streamID == "" {
\t\treturn nil, fmt.Errorf("host http stream bridge is unavailable")
\t}
\trawResponse, errMarshal := marshalRPCResult(rpcHostHTTPStreamResponse{
\t\tStatusCode: resp.StatusCode,
\t\tHeaders:    httpHeader(resp.Headers),
\t\tStreamID:   streamID,
\t})
\tif errMarshal != nil {
\t\th.httpStreams.close(streamID)
\t\treturn nil, errMarshal
\t}
\tcancelOwned = false
\treturn rawResponse, nil
''',
    'rawResponse, errMarshal := marshalRPCResult(rpcHostHTTPStreamResponse{',
)

replace_once(
    ROOT / 'internal/pluginhost/host_model_stream_callbacks.go',
    '''\tstreamCtx, cancel := context.WithCancel(context.WithoutCancel(callbackCtx))
\tstream, errMsg := executor.ExecuteModelStream(streamCtx, modelExecutionRequestFromPlugin(req.HostModelExecutionRequest, skipPluginID))
\tif errMsg != nil {
\t\tcancel()
\t\treturn nil, modelExecutionError(errMsg)
\t}
\tstreamID := ""
\tif h.modelStreams != nil {
\t\tstreamID = h.modelStreams.open(req.HostCallbackID, stream.Chunks, cancel)
\t}
\tif streamID == "" {
\t\tcancel()
\t\treturn nil, fmt.Errorf("host model stream bridge is unavailable")
\t}
\tif req.HostCallbackID != "" {
\t\th.addCallbackCleanup(req.HostCallbackID, func() {
\t\t\th.modelStreams.close(streamID)
\t\t})
\t}
\treturn marshalRPCResult(pluginapi.HostModelStreamResponse{
\t\tStatusCode: stream.StatusCode,
\t\tHeaders:    cloneHeader(stream.Headers),
\t\tStreamID:   streamID,
\t})
''',
    '''\tstreamCtx, cancel := context.WithCancel(context.WithoutCancel(callbackCtx))
\tcancelOwned := true
\tdefer func() {
\t\tif cancelOwned {
\t\t\tcancel()
\t\t}
\t}()
\tstream, errMsg := executor.ExecuteModelStream(streamCtx, modelExecutionRequestFromPlugin(req.HostModelExecutionRequest, skipPluginID))
\tif errMsg != nil {
\t\treturn nil, modelExecutionError(errMsg)
\t}
\tstreamID := ""
\tif h.modelStreams != nil {
\t\tstreamID = h.modelStreams.open(req.HostCallbackID, stream.Chunks, cancel)
\t}
\tif streamID == "" {
\t\treturn nil, fmt.Errorf("host model stream bridge is unavailable")
\t}
\tif req.HostCallbackID != "" {
\t\th.addCallbackCleanup(req.HostCallbackID, func() {
\t\t\th.modelStreams.close(streamID)
\t\t})
\t}
\trawResponse, errMarshal := marshalRPCResult(pluginapi.HostModelStreamResponse{
\t\tStatusCode: stream.StatusCode,
\t\tHeaders:    cloneHeader(stream.Headers),
\t\tStreamID:   streamID,
\t})
\tif errMarshal != nil {
\t\th.modelStreams.close(streamID)
\t\treturn nil, errMarshal
\t}
\tcancelOwned = false
\treturn rawResponse, nil
''',
    'rawResponse, errMarshal := marshalRPCResult(pluginapi.HostModelStreamResponse{',
)

queue_go_source('internal/pluginstore/autoinstall.go')

queue_go_source('internal/pluginstore/autoinstall_test.go')

replace_once(
    ROOT / 'internal/pluginhost/auth_provider.go',
    '''\treq.RawJSON = bytes.Clone(req.RawJSON)
\tresp, errParse := provider.ParseAuth(ctx, req)
''',
    '''\treq.RawJSON = normalizePluginStorageJSON(req.Provider, bytes.Clone(req.RawJSON))
\tresp, errParse := provider.ParseAuth(ctx, req)
''',
)

replace_once(
    ROOT / 'internal/pluginhost/auth_provider.go',
    '''\tif provider != "" {
\t\tmetadata["type"] = provider
\t}
\tattributes := cloneStringMap(data.Attributes)
''',
    '''\tif provider != "" {
\t\tmetadata["type"] = provider
\t}
\tdisabled := data.Disabled || pluginAuthDisabledFromMetadata(metadata)
\tmetadata["disabled"] = disabled
\tattributes := cloneStringMap(data.Attributes)
''',
)

replace_once(
    ROOT / 'internal/pluginhost/auth_provider.go',
    '''\tstatus := coreauth.StatusActive
\tif data.Disabled {
\t\tstatus = coreauth.StatusDisabled
\t}
''',
    '''\tstatus := coreauth.StatusActive
\tif disabled {
\t\tstatus = coreauth.StatusDisabled
\t}
''',
)

replace_once(
    ROOT / 'internal/pluginhost/auth_provider.go',
    '''\t\tDisabled:         data.Disabled,
''',
    '''\t\tDisabled:         disabled,
''',
)

replace_once(
    ROOT / 'internal/pluginhost/adapters_executors.go',
    '''func storageJSONFromAuth(auth *coreauth.Auth) []byte {
\tif auth == nil {
\t\treturn nil
\t}
\tif rawProvider, okRaw := auth.Storage.(interface{ RawJSON() []byte }); okRaw {
\t\treturn bytes.Clone(rawProvider.RawJSON())
\t}
\tif len(auth.Metadata) == 0 {
\t\treturn nil
\t}
\tdata, errMarshal := json.Marshal(auth.Metadata)
\tif errMarshal != nil {
\t\treturn nil
\t}
\treturn data
}
''',
    '''func storageJSONFromAuth(auth *coreauth.Auth) []byte {
\tif auth == nil {
\t\treturn nil
\t}
\tif rawProvider, okRaw := auth.Storage.(interface{ RawJSON() []byte }); okRaw {
\t\treturn normalizePluginStorageJSON(auth.Provider, bytes.Clone(rawProvider.RawJSON()))
\t}
\tif len(auth.Metadata) == 0 {
\t\treturn nil
\t}
\tdata, errMarshal := json.Marshal(auth.Metadata)
\tif errMarshal != nil {
\t\treturn nil
\t}
\treturn normalizePluginStorageJSON(auth.Provider, data)
}
''',
)

replace_once(
    ROOT / 'internal/pluginhost/adapters_executors.go',
    '''\tif reporter != nil {
\t\treporter.RecordFirstPacket()
\t\tdetail := helps.ParsePluginExecutorResponseUsage(prepared.outputFormat.String(), pluginResp.Payload)
\t\treporter.Publish(ctx, detail)
\t\treporter.EnsurePublished(ctx)
\t}

\treturn coreexecutor.Response{
\t\tPayload:  a.translateExecutorResponse(ctx, prepared, pluginResp.Payload, false, nil),
\t\tMetadata: cloneAnyMap(pluginResp.Metadata),
\t\tHeaders:  cloneHeader(pluginResp.Headers),
\t}, nil
''',
    '''\tctx = pluginExecutorUsageContext(ctx, pluginResp.Headers)
\tresp = coreexecutor.Response{
\t\tPayload:  a.translateExecutorResponse(ctx, prepared, pluginResp.Payload, false, nil),
\t\tMetadata: cloneAnyMap(pluginResp.Metadata),
\t\tHeaders:  cloneHeader(pluginResp.Headers),
\t}
\tif reporter != nil {
\t\treporter.RecordFirstPacket()
\t\tif !publishPluginExecutorUsage(ctx, reporter, pluginExecutorUsageFormat(prepared), resp.Payload, false) {
\t\t\treporter.EnsurePublished(ctx)
\t\t}
\t}
\treturn resp, nil
''',
    'publishPluginExecutorUsage(ctx, reporter, pluginExecutorUsageFormat(prepared), resp.Payload, false)',
)

replace_once(
    ROOT / 'internal/pluginhost/adapters_executors.go',
    '''\tchunks := a.observeAndTranslateExecutorStream(ctx, prepared, pluginResp.Chunks, reporter)
\treturn &coreexecutor.StreamResult{
\t\tHeaders: cloneHeader(pluginResp.Headers),
\t\tChunks:  chunks,
\t}, nil
''',
    '''\tctx = pluginExecutorUsageContext(ctx, pluginResp.Headers)
\tchunks := a.observeAndTranslateExecutorStream(ctx, prepared, pluginResp.Chunks, reporter)
\treturn &coreexecutor.StreamResult{
\t\tHeaders: cloneHeader(pluginResp.Headers),
\t\tChunks:  chunks,
\t}, nil
''',
    'ctx = pluginExecutorUsageContext(ctx, pluginResp.Headers)\n\tchunks := a.observeAndTranslateExecutorStream',
)

replace_once(
    ROOT / 'internal/pluginhost/adapters_executors.go',
    '''\tif in == nil {
\t\tout := make(chan coreexecutor.StreamChunk)
\t\tclose(out)
\t\tif reporter != nil {
\t\t\treporter.EnsurePublished(ctx)
\t\t}
\t\treturn out
\t}
''',
    '''\tif in == nil {
\t\tout := make(chan coreexecutor.StreamChunk)
\t\tclose(out)
\t\tif reporter != nil {
\t\t\treporter.PublishFailure(ctx, pluginExecutorEmptyStreamError{})
\t\t}
\t\treturn out
\t}
''',
    'reporter.PublishFailure(ctx, pluginExecutorEmptyStreamError{})',
)

replace_once(
    ROOT / 'internal/pluginhost/adapters_executors.go',
    '''\tvar streamUsage helps.StreamUsageBuffer
\tvar streamErr error
\tvar publishOnce sync.Once
''',
    '''\tvar streamUsage helps.StreamUsageBuffer
\tvar streamErr error
\tvar sawPayload bool
\tvar publishOnce sync.Once
''',
    'var sawPayload bool',
)

replace_once(
    ROOT / 'internal/pluginhost/adapters_executors.go',
    '''\t\t\tif !streamUsage.Publish(ctx, reporter) {
\t\t\t\treporter.EnsurePublished(ctx)
\t\t\t}
''',
    '''\t\t\tif !sawPayload {
\t\t\t\treporter.PublishFailure(ctx, pluginExecutorEmptyStreamError{})
\t\t\t\treturn
\t\t\t}
\t\t\tif !streamUsage.Publish(ctx, reporter) {
\t\t\t\treporter.EnsurePublished(ctx)
\t\t\t}
''',
    'if !sawPayload {\n\t\t\t\treporter.PublishFailure(ctx, pluginExecutorEmptyStreamError{})',
)

replace_once(
    ROOT / 'internal/pluginhost/adapters_executors.go',
    '''\t\t\t\tif len(chunk.Payload) > 0 {
\t\t\t\t\thelps.ObservePluginExecutorStreamTTFT(prepared.outputFormat.String(), reporter, chunk.Payload)
''',
    '''\t\t\t\tif len(chunk.Payload) > 0 {
\t\t\t\t\tsawPayload = true
\t\t\t\t\thelps.ObservePluginExecutorStreamTTFT(prepared.outputFormat.String(), reporter, chunk.Payload)
''',
    'sawPayload = true\n\t\t\t\t\thelps.ObservePluginExecutorStreamTTFT',
)

queue_go_source('internal/pluginhost/gemini_cli_storage_compat.go')

queue_go_source('internal/pluginhost/gemini_cli_storage_compat_test.go')

queue_go_source('internal/pluginhost/plugin_executor_usage_test.go')

queue_go_source('internal/client/codex/live/api_key_quota_relay_test.go')

server = ROOT / 'internal/api/server.go'
server_routes = ROOT / 'internal/api/server_routes.go'
server_management = ROOT / 'internal/api/server_management.go'
replace_once(
    server_management,
    '\t\tmgmt.POST("/api-call", s.mgmt.APICall)\n',
    '\t\tmgmt.POST("/api-call", s.mgmt.APICall)\n\t\ts.mgmt.RegisterAPIKeyPolicyRoutes(mgmt)\n',
    's.mgmt.RegisterAPIKeyPolicyRoutes(mgmt)',
)
auth_files = ROOT / 'internal/api/handlers/management/auth_files.go'
auth_files_fields = ROOT / 'internal/api/handlers/management/auth_files_fields.go'
api_tools = ROOT / 'internal/api/handlers/management/api_tools.go'
api_tools_executor_proxy_test = ROOT / 'internal/api/handlers/management/api_tools_executor_proxy_test.go'
routing_policy = ROOT / 'internal/api/handlers/management/routing_policy.go'
routing_policy_test = ROOT / 'internal/api/handlers/management/routing_policy_test.go'
replace_once(
    server_management,
    '\t\tmgmt.GET("/latest-version", s.mgmt.GetLatestVersion)\n',
    '\t\tmgmt.GET("/latest-version", s.mgmt.GetLatestVersion)\n\t\tmgmt.POST("/management-panel/check-update", s.mgmt.PostCheckManagementPanelUpdate)\n',
)
for source_name in ACCOUNT_INSPECTION_SOURCE_FILES:
    source = Path(__file__).resolve().parent / source_name
    source_text = re.sub(r'github\.com/router-for-me/CLIProxyAPI/v\d+', MODULE_PATH, read_text(source))
    if source_name == 'account_inspection_transport.go':
        source_text = source_text.replace(
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
    '''		"unavailable":    auth.Unavailable,
		"runtime_only":   runtimeOnly,
''',
    '''		"unavailable":    auth.Unavailable,
		"last_error":     authFileLastError(auth),
		"runtime_only":   runtimeOnly,
''',
)
replace_once(
    auth_files,
    '''				typeValue := gjson.GetBytes(data, "type").String()
				emailValue := gjson.GetBytes(data, "email").String()
				fileData["type"] = typeValue
				fileData["email"] = emailValue
''',
    '''				typeValue := gjson.GetBytes(data, "type").String()
				emailValue := gjson.GetBytes(data, "email").String()
				fileData["type"] = typeValue
				fileData["email"] = emailValue
				if lastErrorRaw := gjson.GetBytes(data, "last_error"); lastErrorRaw.IsObject() {
					var lastError map[string]any
					if errUnmarshal := json.Unmarshal([]byte(lastErrorRaw.Raw), &lastError); errUnmarshal == nil && len(lastError) > 0 {
						fileData["last_error"] = lastError
					}
				}
				if strings.EqualFold(strings.TrimSpace(typeValue), "codex") {
					if claims := extractCodexIDTokenClaimsFromRaw(gjson.GetBytes(data, "id_token").String()); claims != nil {
						fileData["id_token"] = claims
					}
				}
				if strings.EqualFold(strings.TrimSpace(typeValue), "xai") {
					if plan := xaiAuthFilePlanTypeFromRaw(gjson.GetBytes(data, "access_token").String()); plan != "" {
						fileData["plan_type"] = plan
					}
				}
''',
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
replace_once(
    redisqueue_plugin,
    '''\t\tUserAgent:       clientRequestMetadata.UserAgent,
\t\tTokens:          tokens,
''',
    '''\t\tUserAgent:       clientRequestMetadata.UserAgent,
\t\tAttemptIndex:    record.AttemptIndex,
\t\tTokens:          tokens,
''',
    'AttemptIndex:    record.AttemptIndex',
)
replace_once(
    redisqueue_plugin,
    '''\tresponseServiceTier := strings.TrimSpace(record.ResponseServiceTier)
\tclientRequestMetadata := internallogging.GetClientRequestMetadata(ctx)
''',
    '''\tresponseServiceTier := strings.TrimSpace(record.ResponseServiceTier)
\tspeed := strings.TrimSpace(record.Speed)
\tif speed == "" {
\t\tspeed = coreusage.SpeedFromContext(ctx)
\t}
\tresponseSpeed := strings.TrimSpace(record.ResponseSpeed)
\tclientRequestMetadata := internallogging.GetClientRequestMetadata(ctx)
''',
    'responseSpeed := strings.TrimSpace(record.ResponseSpeed)',
)
replace_once(
    redisqueue_plugin,
    '''\tclientRequestMetadata := internallogging.GetClientRequestMetadata(ctx)

\tusageDetail := coreusage.EnsureTokenBreakdownForProvider''',
    '''\tclientRequestMetadata := internallogging.GetClientRequestMetadata(ctx)
\tpolicyDecision, hasPolicyDecision := apikeypolicy.DecisionFromContext(ctx)
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
replace_once(
    redisqueue_plugin,
    '''\t\tAPIKey:              apiKey,
\t\tRequestID:           requestID,
''',
    '''\t\tAPIKey:              apiKey,
\t\tAPIKeyPolicyID:      apiKeyPolicyID,
\t\tProfileID:           profileID,
\t\tProfileNameSnapshot: profileNameSnapshot,
\t\tPolicyMode:          policyMode,
\t\tRequestedModel:      requestedModel,
\t\tEffectiveModel:      effectiveModel,
\t\tRequestID:           requestID,
''',
    'APIKeyPolicyID:      apiKeyPolicyID',
)
replace_once(
    redisqueue_plugin,
    '''\tAPIKey              string                   `json:"api_key"`
\tRequestID           string                   `json:"request_id"`
''',
    '''\tAPIKey              string                   `json:"api_key"`
\tAPIKeyPolicyID      string                   `json:"api_key_policy_id,omitempty"`
\tProfileID           string                   `json:"profile_id,omitempty"`
\tProfileNameSnapshot string                   `json:"profile_name_snapshot,omitempty"`
\tPolicyMode          string                   `json:"policy_mode,omitempty"`
\tRequestedModel      string                   `json:"requested_model,omitempty"`
\tEffectiveModel      string                   `json:"effective_model,omitempty"`
\tRequestID           string                   `json:"request_id"`
''',
    '`json:"api_key_policy_id,omitempty"`',
)
replace_once(
    redisqueue_plugin,
    '''\t\tServiceTier:         serviceTier,
\t\tResponseServiceTier: responseServiceTier,
\t})
''',
    '''\t\tServiceTier:         serviceTier,
\t\tResponseServiceTier: responseServiceTier,
\t\tSpeed:               speed,
\t\tResponseSpeed:       responseSpeed,
\t})
''',
    'ResponseSpeed:       responseSpeed',
)
replace_once(
    redisqueue_plugin,
    '''\tServiceTier         string                   `json:"service_tier"`
\tResponseServiceTier string                   `json:"response_service_tier,omitempty"`
}
''',
    '''\tServiceTier         string                   `json:"service_tier"`
\tResponseServiceTier string                   `json:"response_service_tier,omitempty"`
\tSpeed               string                   `json:"speed,omitempty"`
\tResponseSpeed       string                   `json:"response_speed,omitempty"`
}
''',
    '`json:"response_speed,omitempty"`',
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
\t\tembeddedusage.RegisterGinRoutes(mgmt.Group("/usage"))
\t\tembeddedusage.RegisterDataManagementGinRoutes(mgmt.Group("/data"))

\t\tmgmt.GET("/config", s.mgmt.GetConfig)
''',
)
replace_once(
    server_management,
    '''\t\tmgmt.POST("/api-call", s.mgmt.APICall)\n''',
	'''\t\tmgmt.POST("/api-call", s.mgmt.APICall)\n\t\tmgmt.POST("/auth-files/test", s.mgmt.TestAuthFileConnection)\n\t\ts.mgmt.RegisterPluginQuotaRoutes(mgmt)\n\t\ts.mgmt.RegisterAccountInspectionRoutes(mgmt)\n\t\ts.mgmt.RegisterRoutingPolicyRoutes(mgmt)\n\t\ts.mgmt.RegisterProFeatureRoutes(mgmt)\n''',
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
\tif len(models) == 0 && selectedAuth != nil {
\t\tmodels = authFileManagementFallbackModels(selectedAuth)
\t}

\tresult := make([]gin.H, 0, len(models))
''',
)

handler = ROOT / 'internal/api/handlers/management/handler.go'
add_go_import(handler, '"net/http"\n', '\t"net/url"\n')
replace_once(
    handler,
    '''\tpluginReleaseCacheMu    sync.Mutex
\tpluginReleaseCache      map[string]pluginReleaseCacheEntry
}
''',
    '''\tpluginReleaseCacheMu    sync.Mutex
\tpluginReleaseCache      map[string]pluginReleaseCacheEntry
\tproAuthMutationMu       sync.Mutex
\tlifecycleContext        context.Context
\tlifecycleCancel         context.CancelFunc
\tlifecycleWG             sync.WaitGroup
\tshutdownOnce            sync.Once
}
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
    'func applyProRequiredStartupConfig(cfg *config.Config, configPath string) {\n\tif cfg == nil {\n\t\treturn\n\t}\n\tshouldPersistUsageStatistics := !cfg.UsageStatisticsEnabled\n\tshouldPersistPanelRepository := cfg.RemoteManagement.PanelGitHubRepository != config.DefaultPanelGitHubRepository\n\tcfg.UsageStatisticsEnabled = true\n\tcfg.RemoteManagement.PanelGitHubRepository = config.DefaultPanelGitHubRepository\n\tif configPath == "" {\n\t\treturn\n\t}\n\tif shouldPersistUsageStatistics {\n\t\tif _, err := config.SaveConfigPreserveCommentsUpdateExistingScalars(configPath, []config.ExistingScalarUpdate{{Path: []string{"usage-statistics-enabled"}, Value: true}}); err != nil {\n\t\t\tlog.Warnf("failed to update existing usage statistics config: %v", err)\n\t\t}\n\t}\n\tif shouldPersistPanelRepository {\n\t\tif _, err := config.SaveConfigPreserveCommentsUpdateExistingScalars(configPath, []config.ExistingScalarUpdate{{Path: []string{"remote-management", "panel-github-repository"}, Value: config.DefaultPanelGitHubRepository}}); err != nil {\n\t\t\tlog.Warnf("failed to update existing panel repository config: %v", err)\n\t\t}\n\t}\n}\n\n',
    'func applyProRequiredStartupConfig',
)
insert_before_nth(
    run,
    '''\tservice, err := builder.Build()
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
    2,
    'embeddedusage.StartForPath(ctx, configPath)',
)
insert_before_nth(
    run,
    '''\tservice, err := builder.Build()
''',
    '''\tusageService, err := embeddedusage.StartForPath(runCtx, configPath)
\tif err != nil {
\t\tlog.Errorf("failed to start embedded usage service: %v", err)
\t\treturn
\t}
\tembeddedusage.SetDefaultService(usageService)
\tapplyProRequiredStartupConfig(cfg, configPath)

''',
    1,
    'embeddedusage.StartForPath(runCtx, configPath)',
)

queue_go_source('sdk/cliproxy/auth/inspection_refresh.go')
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

auth_selector = ROOT / 'sdk/cliproxy/auth/selector.go'
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
    '''func (m *Manager) Update(ctx context.Context, auth *Auth) (*Auth, error) {
	if auth == nil || auth.ID == "" {
		return nil, nil
	}
''',
    '''func (m *Manager) Update(ctx context.Context, auth *Auth) (*Auth, error) {
	if auth == nil || auth.ID == "" {
		return nil, nil
	}
	RestoreAccountPolicyBase(auth)
''',
    '''func (m *Manager) Update(ctx context.Context, auth *Auth) (*Auth, error) {
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
lifecycle_scheduler_upsert = '''\tif m.scheduler != nil {
\t\tm.scheduler.upsertAuth(authClone.Clone())
\t}
'''
if auth_conductor_text.count(lifecycle_scheduler_upsert) != 2:
    raise SystemExit(
        f'expected two lifecycle scheduler upserts in {auth_conductor}, '
        f'found {auth_conductor_text.count(lifecycle_scheduler_upsert)}'
    )
write(
    auth_conductor,
    auth_conductor_text.replace(
        lifecycle_scheduler_upsert,
        '\tm.RefreshSchedulerEntry(authClone.ID)\n',
    ),
)

auth_conductor = ROOT / 'sdk/cliproxy/auth/conductor_cooldown.go'
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
\tif m.scheduler != nil && authSnapshot != nil {
\t\tm.scheduler.upsertAuth(authSnapshot)
\t}
''',
    '''\tm.mu.Unlock()
\tqueueAuthRuntimeStats(authSnapshot, authStatsObservedAt)
\tif authSnapshot != nil {
\t\tm.RefreshSchedulerEntry(authSnapshot.ID)
\t}
''',
    'queueAuthRuntimeStats(authSnapshot, authStatsObservedAt)',
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
replace_once(
    auth_conductor,
    '''	snapshot := auth.Clone()
	m.mu.RUnlock()
	m.scheduler.upsertAuth(snapshot)
''',
    '''	resolver := m.accountPolicyResolver
	snapshot := resolveAccountPolicy(auth, resolver)
	m.mu.RUnlock()
	m.scheduler.upsertAuth(snapshot)
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
auth_conductor_text = read(auth_conductor)
candidate_append = '\t\tcandidates = append(candidates, candidate)\n'
policy_candidate_append = '\t\tcandidates = append(candidates, resolveAccountPolicy(candidate, m.accountPolicyResolver))\n'
if policy_candidate_append not in auth_conductor_text:
    if auth_conductor_text.count(candidate_append) < 2:
        raise SystemExit(f'expected at least two legacy candidate appends in {auth_conductor}')
    write(auth_conductor, auth_conductor_text.replace(candidate_append, policy_candidate_append))

auth_conductor = ROOT / 'sdk/cliproxy/auth/conductor_refresh.go'
replace_once(
    auth_conductor,
    '''\t\t\tm.auths[id] = current
\t\t\tshouldReschedule = true
\t\t\tif m.scheduler != nil {
\t\t\t\tm.scheduler.upsertAuth(current.Clone())
\t\t\t}
\t\t}
\t\tm.mu.Unlock()
\t\tif shouldReschedule {
\t\t\tm.queueRefreshReschedule(id)
''',
    '''\t\t\tm.auths[id] = current
\t\t\tshouldReschedule = true
\t\t}
\t\tm.mu.Unlock()
\t\tif shouldReschedule {
\t\t\tm.RefreshSchedulerEntry(id)
\t\t\tm.queueRefreshReschedule(id)
''',
    'shouldReschedule {\n\t\t\tm.RefreshSchedulerEntry(id)',
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
    '''\tsavedPath, errSave := store.Save(ctx, record)
\tif errSave != nil {
\t\treturn savedPath, errSave
\t}
\tif h.postAuthPersistHook != nil {
''',
    '''\tsavedPath, errSave := store.Save(ctx, record)
\tif errSave != nil {
\t\treturn savedPath, errSave
\t}
\tif sdkAuth.TakeReusedExistingAuthIdentity(record) {
\t\t// The stable auth ID/index and runtime statistics remain attached to the
\t\t// existing file. Only quota is invalidated because credential or
\t\t// entitlement changes make the previous snapshot unsafe to reuse.
\t\t_ = embeddedusage.DeleteQuotaCache(ctx, record.Provider, record.FileName)
\t}
\tif h.postAuthPersistHook != nil {
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
\t\t\t\th.removeAuth(ctx, full)
''',
    '''\t\t\t\tdeleted++
\t\t\t\th.removeAuth(ctx, full)
\t\t\t\t_ = embeddedusage.DeleteAuthRuntimeState(ctx, "", "", name)
''',
    'DeleteAuthRuntimeState(ctx, "", "", name)',
)
replace_once(
    auth_files_crud_handler,
    '''\ttargetPath := filepath.Join(h.cfg.AuthDir, filepath.Base(name))
\ttargetID := ""
\tif targetAuth := h.findAuthForDelete(name); targetAuth != nil {
''',
    '''\ttargetPath := filepath.Join(h.cfg.AuthDir, filepath.Base(name))
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
    '''\th.removeAuthsForPath(ctx, targetPath, targetID)
\treturn filepath.Base(name), http.StatusOK, nil
''',
    '''\th.removeAuthsForPath(ctx, targetPath, targetID)
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
replace_once(
    auth_scheduler,
    '''func newAuthScheduler(selector Selector) *authScheduler {
\treturn &authScheduler{
\t\tstrategy:            selectorStrategy(selector),
\t\tproviders:           make(map[string]*providerScheduler),
\t\tauthProviders:       make(map[string]string),
\t\tauthGenerations:     make(map[string]scheduledGenerationMeta),
\t\tmixedCursors:        make(map[string]int),
\t\tmixedWeightedStates: make(map[string]*smoothWeightedState),
\t}
}''',
    '''func newAuthScheduler(selector Selector) *authScheduler {
\tpersistedCursors := loadRoutingCursorStates()
\treturn &authScheduler{
\t\tstrategy:            selectorStrategy(selector),
\t\tproviders:           make(map[string]*providerScheduler),
\t\tauthProviders:       make(map[string]string),
\t\tauthGenerations:     make(map[string]scheduledGenerationMeta),
\t\tmixedCursors:        make(map[string]int),
\t\tmixedWeightedStates: make(map[string]*smoothWeightedState),
\t\tmixedRestored:       make(map[string]bool),
\t\tpersistedCursors:    persistedCursors,
\t}
}''',
    'persistedCursors := loadRoutingCursorStates()',
)
replace_once(
    auth_scheduler,
    '''\ts.mixedCursors = make(map[string]int)
\ts.mixedWeightedStates = make(map[string]*smoothWeightedState)
\tnow := time.Now()''',
    '''\ts.mixedCursors = make(map[string]int)
\ts.mixedWeightedStates = make(map[string]*smoothWeightedState)
\ts.mixedRestored = make(map[string]bool)
\tnow := time.Now()''',
    's.mixedWeightedStates = make(map[string]*smoothWeightedState)\n\ts.mixedRestored = make(map[string]bool)\n\tnow := time.Now()',
)
replace_once(
    auth_scheduler,
    '''\ts.strategy = selectorStrategy(selector)
\tclear(s.mixedCursors)
\tclear(s.mixedWeightedStates)
''',
    '''\ts.strategy = selectorStrategy(selector)
\tclear(s.mixedCursors)
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

# Vertex requires multimodal tool results to live inside the corresponding
# functionResponse. Top-level sibling inline_data parts make the request end in
# an invalid model turn and are rejected with HTTP 400.
gemini_responses_request = ROOT / 'internal/translator/gemini/openai/responses/gemini_openai-responses_request.go'
replace_once(
    gemini_responses_request,
    '''\tparts := make([][]byte, 0, 1+len(imageParts))
\tparts = append(parts, functionResponse)
\tparts = append(parts, imageParts...)
\treturn parts
''',
    '''\tfor _, imagePart := range imageParts {
\t\tinlineData := []byte(`{"inlineData":{"mimeType":"","data":""}}`)
\t\tinlineData, _ = sjson.SetBytes(inlineData, "inlineData.mimeType", gjson.GetBytes(imagePart, "inline_data.mime_type").String())
\t\tinlineData, _ = sjson.SetBytes(inlineData, "inlineData.data", gjson.GetBytes(imagePart, "inline_data.data").String())
\t\tfunctionResponse, _ = sjson.SetRawBytes(functionResponse, "functionResponse.parts.-1", inlineData)
\t}
\treturn [][]byte{functionResponse}
''',
    'functionResponse.parts.-1',
)

gemini_responses_request_test = ROOT / 'internal/translator/gemini/openai/responses/gemini_openai-responses_request_test.go'
replace_once(
    gemini_responses_request_test,
    '''\tparts := userContent.Get("parts").Array()
\tif len(parts) < 2 {
\t\tt.Fatalf("expected at least 2 parts (functionResponse + inline_data), got %d; raw: %s", len(parts), userContent.Raw)
\t}''',
    '''\tparts := userContent.Get("parts").Array()
\tif len(parts) != 1 {
\t\tt.Fatalf("expected one top-level functionResponse part, got %d; raw: %s", len(parts), userContent.Raw)
\t}''',
    'expected one top-level functionResponse part',
)
replace_once(
    gemini_responses_request_test,
    '''\timg := parts[1].Get("inline_data")
\tif !img.Exists() {
\t\tt.Fatalf("expected second part to have inline_data, got %s", parts[1].Raw)
\t}
\tif got := img.Get("mime_type").String(); got != "image/png" {
\t\tt.Fatalf("expected mime_type = %q, got %q", "image/png", got)
\t}''',
    '''\timg := fr.Get("parts.0.inlineData")
\tif !img.Exists() {
\t\tt.Fatalf("expected image nested under functionResponse.parts, got %s", fr.Raw)
\t}
\tif got := img.Get("mimeType").String(); got != "image/png" {
\t\tt.Fatalf("expected mimeType = %q, got %q", "image/png", got)
\t}''',
    'expected image nested under functionResponse.parts',
)
replace_once(
    gemini_responses_request_test,
    '''\t\tif len(parts) != 2 {
\t\t\tt.Fatalf("expected 2 parts (functionResponse + inline_data), got %d; raw: %s", len(parts), userContent.Raw)
\t\t}
\t\tif got := parts[1].Get("inline_data.mime_type").String(); got != "image/png" {
\t\t\tt.Fatalf("expected mime_type 'image/png', got %q", got)
\t\t}
\t\tif got := parts[1].Get("inline_data.data").String(); got != "iVBORw0KGgoAAAANSUhEUg==" {''',
    '''\t\tif len(parts) != 1 {
\t\t\tt.Fatalf("expected one top-level functionResponse part, got %d; raw: %s", len(parts), userContent.Raw)
\t\t}
\t\tif got := parts[0].Get("functionResponse.parts.0.inlineData.mimeType").String(); got != "image/png" {
\t\t\tt.Fatalf("expected nested mimeType 'image/png', got %q", got)
\t\t}
\t\tif got := parts[0].Get("functionResponse.parts.0.inlineData.data").String(); got != "iVBORw0KGgoAAAANSUhEUg==" {''',
    'expected nested mimeType',
)
insert_before(
    gemini_responses_request_test,
    'func TestConvertOpenAIResponsesRequestToGemini_GroupsNonContiguousParallelToolOutputs(t *testing.T) {',
    '''func TestConvertOpenAIResponsesRequestToGemini_BindsImagesToParallelFunctionResponses(t *testing.T) {
\tinputJSON := `{
\t\t"model":"gemini-3.7-flash-high",
\t\t"input":[
\t\t\t{"type":"function_call","call_id":"call-1","name":"first_image","arguments":"{}"},
\t\t\t{"type":"function_call","call_id":"call-2","name":"second_image","arguments":"{}"},
\t\t\t{"type":"function_call_output","call_id":"call-1","output":[{"type":"input_image","image_url":"data:image/png;base64,AAAA"}]},
\t\t\t{"type":"function_call_output","call_id":"call-2","output":[{"type":"input_image","image_url":"data:image/jpeg;base64,BBBB"}]}
\t\t]
\t}`
\tresult := ConvertOpenAIResponsesRequestToGemini("gemini-3.7-flash-high", []byte(inputJSON), false)
\tresponses := gjson.GetBytes(result, "contents.1.parts").Array()
\tif len(responses) != 2 {
\t\tt.Fatalf("function response count = %d, want 2; result=%s", len(responses), result)
\t}
\twants := []struct {
\t\tid, mimeType, data string
\t}{
\t\t{"call-1", "image/png", "AAAA"},
\t\t{"call-2", "image/jpeg", "BBBB"},
\t}
\tfor index, want := range wants {
\t\tresponse := responses[index].Get("functionResponse")
\t\tif got := response.Get("id").String(); got != want.id {
\t\t\tt.Fatalf("function response %d id = %q, want %q; result=%s", index, got, want.id, result)
\t\t}
\t\tif got := response.Get("parts.0.inlineData.mimeType").String(); got != want.mimeType {
\t\t\tt.Fatalf("function response %d mime type = %q, want %q; result=%s", index, got, want.mimeType, result)
\t\t}
\t\tif got := response.Get("parts.0.inlineData.data").String(); got != want.data {
\t\t\tt.Fatalf("function response %d data = %q, want %q; result=%s", index, got, want.data, result)
\t\t}
\t}
}

''',
    'func TestConvertOpenAIResponsesRequestToGemini_BindsImagesToParallelFunctionResponses',
)

# Account policy fields are execution-only overlays. Every Manager-driven
# incremental scheduler refresh must therefore go through RefreshSchedulerEntry,
# whose single low-level upsert resolves the latest cached account policy.
auth_package = ROOT / 'sdk/cliproxy/auth'
auth_go_paths = set(auth_package.glob('*.go'))
auth_go_paths.update(
    path for path in _writes
    if path.parent == auth_package and path.suffix == '.go'
)
direct_scheduler_upserts = {
    path.relative_to(ROOT).as_posix(): read(path).count('m.scheduler.upsertAuth(')
    for path in auth_go_paths
    if read(path).count('m.scheduler.upsertAuth(') > 0
}
expected_direct_scheduler_upserts = {'sdk/cliproxy/auth/conductor_selection.go': 1}
if direct_scheduler_upserts != expected_direct_scheduler_upserts:
    raise SystemExit(
        'Manager scheduler upserts must be centralized in RefreshSchedulerEntry: '
        f'found {direct_scheduler_upserts}'
    )

format_go_writes([
    'cmd/server/main.go',
    'internal/api/server.go',
    'internal/api/api_key_policy_middleware_test.go',
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
    'internal/client/codex/live/websocket.go',
    'internal/client/codex/live/api_key_quota_relay_test.go',
    'internal/api/handlers/management/plugin_quota.go',
    'internal/api/handlers/management/plugin_quota_test.go',
    'internal/api/handlers/management/pro_auth_mutation.go',
    'internal/api/handlers/management/pro_features.go',
    'internal/api/handlers/management/pro_management_runtime.go',
    'internal/api/handlers/management/routing_policy.go',
    'internal/api/handlers/management/routing_policy_test.go',
    'internal/config/config_existing_updates.go',
    'internal/config/config_existing_updates_test.go',
    'internal/config/config_normalization.go',
    'internal/embeddedusage/facade.go',
    'internal/embeddedusage/internalusage/facade.go',
    'internal/pluginhost/gemini_cli_storage_compat.go',
    'internal/pluginhost/gemini_cli_storage_compat_test.go',
    'internal/pluginhost/gemini_cli_quota_legacy.go',
    'internal/pluginhost/gemini_cli_quota_legacy_test.go',
    'internal/pluginhost/quota_provider.go',
    'internal/pluginhost/quota_provider_test.go',
    'internal/pluginhost/rpc_client.go',
    'internal/pluginhost/rpc_schema.go',
    'internal/pluginhost/snapshot.go',
    'internal/pluginhost/executor_route.go',
    'internal/pluginhost/adapters_executors.go',
	'internal/pluginhost/plugin_executor_usage.go',
    'internal/pluginhost/plugin_executor_usage_test.go',
    'internal/pluginstore/autoinstall.go',
    'internal/pluginstore/autoinstall_test.go',
    'internal/pluginstore/auth.go',
    'internal/pluginstore/gitstore_auth_test.go',
    'internal/redisqueue/plugin.go',
    'internal/redisqueue/speed_test.go',
	'internal/redisqueue/api_key_policy_usage_test.go',
    'internal/requestmeta/observer.go',
    'internal/requestmeta/observer_test.go',
    'internal/runtime/executor/helps/logging_helpers.go',
	'internal/runtime/executor/helps/quota_settlement_test.go',
	'internal/runtime/executor/helps/usage_pro_extensions.go',
	'internal/runtime/executor/helps/retry_after.go',
	'internal/runtime/executor/helps/retry_after_test.go',
	'internal/runtime/executor/helps/response_observer_test.go',
    'internal/runtime/executor/helps/usage_helpers.go',
    'internal/runtime/executor/helps/usage_speed_test.go',
    'internal/runtime/executor/claude_usage_speed_test.go',
    'internal/runtime/executor/claude_executor_execute.go',
    'internal/runtime/executor/claude_executor_stream.go',
	'internal/runtime/executor/claude_stream_terminal.go',
	'internal/runtime/executor/codex_executor_terminal.go',
	'internal/runtime/executor/codex_executor_execute.go',
	'internal/runtime/executor/codex_openai_images.go',
	'internal/runtime/executor/codex_executor_stream.go',
	'internal/runtime/executor/codex_websockets_execute.go',
	'internal/runtime/executor/codex_websockets_stream.go',
	'internal/runtime/executor/codex_retry_after_test.go',
	'sdk/cliproxy/auth/home_concurrency.go',
	'sdk/cliproxy/auth/codex_retry_after_headers_test.go',
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
    'internal/runtime/executor/xai_executor.go',
    'internal/runtime/executor/xai_executor_execute.go',
    'internal/runtime/executor/xai_executor_stream.go',
    'internal/runtime/executor/xai_quota_observer.go',
    'internal/runtime/executor/xai_websockets_executor.go',
    'internal/translator/codex/openai/chat-completions/codex_fast_service_tier_test.go',
    'internal/translator/codex/openai/chat-completions/codex_openai_request.go',
    'internal/translator/codex/openai/responses/codex_fast_service_tier_test.go',
    'internal/translator/codex/openai/responses/codex_openai-responses_request.go',
    'internal/translator/gemini/openai/responses/gemini_openai-responses_request.go',
    'internal/translator/gemini/openai/responses/gemini_openai-responses_request_test.go',
    'sdk/auth/codex_device.go',
    'sdk/auth/filestore.go',
	'sdk/auth/filestore_identity.go',
	'sdk/auth/filestore_identity_test.go',
    'sdk/api/handlers/handlers.go',
    'sdk/api/handlers/handlers_error_response_test.go',
    'sdk/api/handlers/api_key_policy_test.go',
	'sdk/api/handlers/api_key_policy_context_test.go',
    'sdk/api/handlers/handlers_context.go',
    'sdk/api/handlers/handlers_execution.go',
    'sdk/api/handlers/handlers_routing.go',
    'sdk/api/handlers/handlers_stream.go',
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
    'sdk/pluginabi/types.go',
    'sdk/pluginapi/types.go',
    'sdk/proxyutil/proxy.go',
    'sdk/proxyutil/runtime_override.go',
    'sdk/proxyutil/runtime_override_test.go',
])
flush_writes()
