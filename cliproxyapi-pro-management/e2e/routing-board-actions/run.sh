#!/usr/bin/env bash
set -euo pipefail

URL="${1:-http://127.0.0.1:4173/e2e/routing-board-actions/}"
ARTIFACT_DIR="${2:-/private/tmp/routing-board-actions-evidence}"
MODE="${3:-fixed}"
SPACE_ID="${4:-}"
KEEP_OPEN="${5:-false}"
if [[ "$MODE" != fixed && "$MODE" != before ]]; then
  echo 'mode must be fixed or before' >&2
  exit 2
fi
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONFIG="$(python3 - "$URL" "$ARTIFACT_DIR" "$MODE" "$SPACE_ID" "$KEEP_OPEN" <<'PY'
import json
import sys
config = {
    'url': sys.argv[1],
    'artifactDir': sys.argv[2],
    'expectBefore': sys.argv[3] == 'before',
    'keepOpen': sys.argv[5].lower() == 'true',
}
if sys.argv[4]:
    config['spaceId'] = int(sys.argv[4])
print(json.dumps(config))
PY
)"
{ printf 'const config = %s;\n' "$CONFIG"; sed -n '1,$p' "$SCRIPT_DIR/ego.mjs"; } | ego-browser nodejs
