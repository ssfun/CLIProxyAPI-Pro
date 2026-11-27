#!/usr/bin/env python3
import os
from pathlib import Path

root = Path(os.environ.get("SRC_ROOT", "/src/CLIProxyAPI"))
path = root / "sdk/api/handlers/handlers_routing.go"
text = path.read_text()
old = 'if h != nil && h.AuthManager != nil && h.AuthManager.HomeEnabled() {'
new = 'if h != nil && h.isHomeLocalExecutorModel(homeModel) {'
count = text.count(old)
if count == 0:
    raise SystemExit("Home ACP policy guard not found")
path.write_text(text.replace(old, new))
