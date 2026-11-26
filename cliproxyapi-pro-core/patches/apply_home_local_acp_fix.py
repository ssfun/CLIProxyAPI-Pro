#!/usr/bin/env python3
import os
import re
from pathlib import Path

root = Path(os.environ.get("SRC_ROOT", "/src/CLIProxyAPI"))
execution = root / "sdk/api/handlers/handlers_execution.go"
stream = root / "sdk/api/handlers/handlers_stream.go"

helper = """func normalizeHomeLocalPluginExecutorRequest(h *BaseAPIHandler, req coreexecutor.Request) coreexecutor.Request {
	if h == nil || h.AuthManager == nil || !h.AuthManager.HomeEnabled() || !strings.HasPrefix(req.Model, "acp/") {
		return req
	}
	req.Model = strings.TrimPrefix(req.Model, "acp/")
	if len(req.Payload) > 0 {
		if updated, errSet := ojson.SetBytes(req.Payload, "model", req.Model); errSet == nil {
			req.Payload = updated
		}
	}
	return req
}

"""

def patch(path: Path, pattern: str) -> None:
    text = path.read_text()
    if "normalizeHomeLocalPluginExecutorRequest" in text:
        return
    if path == execution:
        text = text.replace('import (\n', 'import (\n\t"github.com/MoeForge/moego/ojson"\n', 1)
        marker = '// ExecuteWithAuthManager executes a non-streaming request via the core auth manager.\n'
        if marker not in text:
            raise SystemExit(f"missing marker in {path}")
        text = text.replace(marker, helper + marker, 1)
    match = re.search(pattern, text, re.MULTILINE)
    if not match:
        raise SystemExit(f"missing plugin route call in {path}")
    end = match.end()
    text = text[:end] + '\n\treq = normalizeHomeLocalPluginExecutorRequest(h, req)' + text[end:]
    path.write_text(text)

patch(execution, r'^\treq, opts, interceptErr = h\.applyRequestInterceptorsAfterPluginExecutorRoute\(execCtx, host, executorPluginID, entryProtocol, originalRequestedModel, lifecycle\.requestID\(\), req, opts, execOptions\.SkipInterceptorPluginID\)$')
patch(stream, r'^\treq, opts, interceptErr = h\.applyRequestInterceptorsAfterPluginExecutorRoute\(execCtx, host, executorPluginID, entryProtocol, originalRequestedModel, lifecycle\.requestID\(\), req, opts, execOptions\.SkipInterceptorPluginID\)$')
