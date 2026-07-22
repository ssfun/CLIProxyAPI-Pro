#!/usr/bin/env python3
import re
import sys
from pathlib import Path


USES_PATTERN = re.compile(r'^\s*uses:\s+([^\s#]+)')
COMMIT_SHA_PATTERN = re.compile(r'^[0-9a-f]{40}$')


def main() -> int:
    if len(sys.argv) != 2:
        print(f'Usage: {sys.argv[0]} /path/to/.github/workflows', file=sys.stderr)
        return 2

    workflows_dir = Path(sys.argv[1])
    failures = []
    for workflow in sorted(workflows_dir.glob('*.y*ml')):
        for line_number, line in enumerate(workflow.read_text(encoding='utf-8').splitlines(), 1):
            match = USES_PATTERN.match(line)
            if not match:
                continue
            action = match.group(1)
            if action.startswith('./') or action.startswith('docker://'):
                continue
            if '@' not in action:
                failures.append(f'{workflow}:{line_number}: action has no ref: {action}')
                continue
            ref = action.rsplit('@', 1)[1]
            if not COMMIT_SHA_PATTERN.fullmatch(ref):
                failures.append(f'{workflow}:{line_number}: action is not pinned to a commit SHA: {action}')

    if failures:
        print('\n'.join(failures), file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
