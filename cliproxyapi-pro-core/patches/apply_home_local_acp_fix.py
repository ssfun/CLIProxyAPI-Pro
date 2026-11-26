#!/usr/bin/env python3
import os
import re
from pathlib import Path

root = Path(os.environ.get("SRC_ROOT", "/src/CLIProxyAPI"))
execution = root / "sdk/api/handlers/handlers_execution.go"
stream = root / "sdk/api/handlers/handlers_stream.go"
routing = root / "sdk/api/handlers/handlers_routing.go"

# Keep the original ACP request visible to all interceptors and policy logic.
text = routing.read_text()
text, n = re.subn(
    r'\n\tif strings\.HasPrefix\(modelName, "acp/"\) \{.*?\n\t\}\n\tdecision, configured :=',
    '\n\tdecision, configured :=',
    text,
    count=1,
    flags=re.S,
)
if n != 1:
    raise SystemExit("ACP policy block not found")
routing.write_text(text)

helper = """func normalizeHomeLocalPluginExecutorRequest(h *BaseAPIHandler, req coreexecutor.Request) coreexecutor.Request {
\tif h == nil || h.AuthManager == nil || !h.AuthManager.HomeEnabled() || !strings.HasPrefix(req.Model, "acp/") {
\t\treturn req
\t}
\treq.Model = strings.TrimPrefix(req.Model, "acp/")
\tif len(req.Payload) > 0 {
\t\tif updated, errSet := ojson.SetBytes(req.Payload, "model", req.Model); errSet == nil {
\t\t\treq.Payload = updated
\t\t}
\t}
\treturn req
}

"""
text = execution.read_text()
if "func normalizeHomeLocalPluginExecutorRequest(" not in text:
    text = text.replace('import (\n', 'import (\n\t"github.com/MoeForge/moego/ojson"\n', 1)
    marker = '// ExecuteWithAuthManager executes a non-streaming request via the core auth manager.\n'
    if marker not in text:
        raise SystemExit("execution marker not found")
    text = text.replace(marker, helper + marker, 1)
execution.write_text(text)

patterns = {
    execution: [
        r'^\treq, opts, interceptErr = h\.applyRequestInterceptorsAfterPluginExecutorRoute\(execCtx, host, executorPluginID, entryProtocol, originalRequestedModel, lifecycle\.requestID\(\), req, opts, execOptions\.SkipInterceptorPluginID\)$',
        r'^\treq, opts, interceptErr = h\.applyRequestInterceptorsAfterPluginExecutorRoute\(ctx, host, executorPluginID, handlerType, originalRequestedModel, lifecycle\.requestID\(\), req, opts, execOptions\.SkipInterceptorPluginID\)$',
    ],
    stream: [
        r'^\treq, opts, interceptErr = h\.applyRequestInterceptorsAfterPluginExecutorRoute\(execCtx, host, executorPluginID, entryProtocol, originalRequestedModel, lifecycle\.requestID\(\), req, opts, execOptions\.SkipInterceptorPluginID\)$',
    ],
}
for path, pats in patterns.items():
    text = path.read_text()
    for pat in pats:
        if "normalizeHomeLocalPluginExecutorRequest(h, req)" in text:
            # Do not skip other paths just because one path is already patched.
            pass
        text, n = re.subn(pat, lambda m: m.group(0) + '\n\treq = normalizeHomeLocalPluginExecutorRequest(h, req)', text, count=1, flags=re.M)
        if n != 1 and "normalizeHomeLocalPluginExecutorRequest(h, req)" not in text:
            raise SystemExit(f"plugin route call not found in {path}")
    path.write_text(text)
