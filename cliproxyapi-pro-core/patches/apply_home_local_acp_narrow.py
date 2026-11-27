#!/usr/bin/env python3
import os
from pathlib import Path

root = Path(os.environ.get("SRC_ROOT", "/src/CLIProxyAPI"))
path = root / "sdk/api/handlers/handlers_routing.go"
text = path.read_text()
old = 'if h != nil && h.AuthManager != nil && h.AuthManager.HomeEnabled() {'
new = 'if h != nil && h.isHomeLocalExecutorModel(homeModel) {'
if text.count(old) != 1:
    raise SystemExit(f"expected one Home ACP policy guard, found {text.count(old)}")
path.write_text(text.replace(old, new, 1))
