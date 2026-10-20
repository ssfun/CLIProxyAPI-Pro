#!/usr/bin/env python3
"""Replay an exact stale-read/HTTP-publication/invalidation ordering via Go overlay."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess

p = argparse.ArgumentParser()
p.add_argument('--src', type=Path, required=True)
p.add_argument('--output', type=Path, required=True)
p.add_argument('--count', type=int, default=1)
p.add_argument('--race', action='store_true')
a = p.parse_args()
src, out = a.src.resolve(), a.output.resolve()
out.mkdir(parents=True, exist_ok=True)
original = src / 'internal/runtime/executor/antigravity_executor_credits.go'
code = original.read_text()
anchor = '\tif hint, ok := cliproxyauth.GetAntigravityCreditsHint(authID); ok && hint.Known && !hint.Available {\n'
if code.count(anchor) != 1:
    raise SystemExit('expected exactly one recovery hint observation')
overlay_source = out / original.name
overlay_source.write_text(code.replace(anchor, anchor + '\t\tif proCreditsRecoveryObservedHint != nil { proCreditsRecoveryObservedHint() }\n'))
fixture = Path(__file__).with_name('recovery_test.go').resolve()
overlay = out / 'overlay.json'
overlay.write_text(json.dumps({'Replace': {str(original): str(overlay_source), str(src / 'internal/runtime/executor/pro_credits_recovery_order_test.go'): str(fixture)}}))
command = ['go', 'test', '-overlay', str(overlay), '-json', '-timeout=120s', '-count='+str(a.count), './internal/runtime/executor', '-run', '^(TestProCreditsRecoveryPublicationOrdering|TestAntigravityManagerExecuteAndStream_ToppedUpCreditsRecoverDuringQuotaCooldown)$']
if a.race:
    command.insert(2, '-race')
env = os.environ.copy()
env.setdefault('GOTOOLCHAIN', 'go1.26.0')
env.setdefault('GOCACHE', '/tmp/cliproxy-go-build-cache')
with (out / 'events.jsonl').open('w') as f:
    result = subprocess.run(command, cwd=src, env=env, stdout=f, stderr=subprocess.STDOUT)
failures=[]
for line in (out / 'events.jsonl').read_text().splitlines():
    try: event=json.loads(line)
    except ValueError: continue
    if event.get('Action')=='fail': failures.append(event.get('Test', event.get('Package')))
summary={'exit_code': result.returncode, 'source':str(src), 'source_sha256':hashlib.sha256(code.encode()).hexdigest(), 'fixture_sha256':hashlib.sha256(fixture.read_bytes()).hexdigest(), 'command':command, 'failures':failures}
(out / 'result.json').write_text(json.dumps(summary,indent=2)+'\n')
print(json.dumps(summary,indent=2))
raise SystemExit(result.returncode)
