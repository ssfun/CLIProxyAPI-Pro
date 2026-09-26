#!/usr/bin/env python3
"""Verify model-price CRUD and persistence against an isolated Core binary."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time
import urllib.error
import urllib.request


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', required=True)
    parser.add_argument('--port', type=int, default=18379)
    parser.add_argument('--artifact', required=True)
    args = parser.parse_args()
    binary = str(Path(args.binary).resolve())
    key = 'model-price-local-validation'
    base = f'http://127.0.0.1:{args.port}/v0/management/usage/model-price-rules'
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    checks = []

    def request(method='GET', payload=None, query=''):
        req = urllib.request.Request(base + query, method=method,
            data=None if payload is None else json.dumps(payload).encode(),
            headers={'Authorization': f'Bearer {key}', 'Content-Type': 'application/json'})
        with opener.open(req, timeout=3) as response:
            return json.load(response)

    with tempfile.TemporaryDirectory(prefix='model-price-smoke-') as directory:
        root = Path(directory)
        config = root / 'config.yaml'
        config.write_text(f'''host: "127.0.0.1"
port: {args.port}
auth-dir: "{root / 'auth'}"
remote-management:
  secret-key: "{key}"
  disable-control-panel: true
  disable-auto-update-panel: true
plugins:
  enabled: false
usage-statistics-enabled: true
''')
        env = {**os.environ, 'USAGE_DB_PATH': str(root / 'usage.sqlite'),
               'USAGE_SERVICE_ENABLED': 'true'}
        saved = None
        with (root / 'server.log').open('w+') as log:
            for phase in ('write', 'restart'):
                process = subprocess.Popen([binary, '-config', str(config), '-local-model'],
                                           env=env, stdout=log, stderr=subprocess.STDOUT)
                try:
                    deadline = time.monotonic() + 20
                    while True:
                        if process.poll() is not None:
                            raise RuntimeError('Core exited before readiness')
                        try:
                            current = request()
                            break
                        except (urllib.error.URLError, TimeoutError):
                            if time.monotonic() >= deadline:
                                raise RuntimeError('Core readiness timeout')
                            time.sleep(0.1)
                    if phase == 'write':
                        assert current['rules'] == [], current
                        rate = {'input': 16, 'output': 2, 'cacheRead': 0, 'cacheWrite': 0}
                        rule = {'model': ' price-smoke ', 'base': rate,
                                'tiers': [{'contextSize': 200000, **rate}],
                                'serviceTiers': {'priority': rate}}
                        first = request('PUT', {'rule': rule})
                        assert first['changed'] is True, first
                        saved = first['rule']
                        assert saved['model'] == 'price-smoke' and saved['locked'], saved
                        assert saved['tiers'][0]['contextSize'] == 200000, saved
                        assert saved['base']['input'] == 16, saved
                        assert 'fast' in saved['serviceTiers'], saved
                        assert 'provider' not in saved, saved
                        checks.append('create and canonicalize global locked rule')
                        second = request('PUT', {'rule': rule})
                        assert second['changed'] and second['rule']['version'] == saved['version'] + 1, second
                        saved = second['rule']
                        assert request()['rules'] == [saved]
                        checks.append('manual save version history and readback')
                    else:
                        assert current['rules'] == [saved], current
                        checks.append('SQLite persistence after restart')
                        assert request('DELETE', query='?model=price-smoke')['ok']
                        assert request()['rules'] == []
                        checks.append('delete active rule')
                except Exception:
                    log.flush()
                    print((root / 'server.log').read_text())
                    raise
                finally:
                    process.terminate()
                    try:
                        process.wait(timeout=10)
                    except subprocess.TimeoutExpired:
                        process.kill()
                        process.wait(timeout=5)
    artifact = Path(args.artifact)
    artifact.parent.mkdir(parents=True, exist_ok=True)
    artifact.write_text(json.dumps({'status': 'passed', 'checks': checks}, indent=2) + '\n')
    print(artifact.read_text())


if __name__ == '__main__':
    main()
