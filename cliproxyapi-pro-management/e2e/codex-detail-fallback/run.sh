#!/usr/bin/env bash
set -euo pipefail
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
config="$(python3 - "$@" <<'PY'
import json, sys
print(json.dumps(dict(spaceId=int(sys.argv[1]), url=sys.argv[2], artifactDir=sys.argv[3], before=len(sys.argv)>4 and sys.argv[4]=='before')))
PY
)"
{ printf 'const config = %s;\n' "$config"; cat "$script_dir/ego.mjs"; } | ego-browser nodejs
