#!/usr/bin/env bash
set -euo pipefail

# Start Vite in a clean, patched Management checkout, then pass its harness URL here.
URL="${1:-http://127.0.0.1:4173/e2e/recovery-dialog-race/}"
ARTIFACT_DIR="${2:-/private/tmp/recovery-dialog-race-evidence}"
MODE="${3:-fixed}"
if [[ "$MODE" != fixed && "$MODE" != before ]]; then
  echo 'mode must be fixed or before' >&2
  exit 2
fi
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONFIG="$(python3 - "$URL" "$ARTIFACT_DIR" "$MODE" <<'PY'
import json
import sys
print(json.dumps({'url': sys.argv[1], 'artifactDir': sys.argv[2], 'expectBug': sys.argv[3] == 'before'}))
PY
)"
{ printf 'const config = %s;\n' "$CONFIG"; cat "$SCRIPT_DIR/ego.mjs"; } | ego-browser nodejs
