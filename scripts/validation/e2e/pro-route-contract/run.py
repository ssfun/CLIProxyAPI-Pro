#!/usr/bin/env python3
"""Check generated frontend Pro route ownership and restart hydration against real Core."""

import argparse
import hashlib
import http.client
import json
import os
from pathlib import Path
import shutil
import socket
import sqlite3
import subprocess
import threading
import time
import traceback
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlsplit


KEY = "pro-route-contract-synthetic-key"
HOP_HEADERS = {"connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
               "te", "trailer", "transfer-encoding", "upgrade", "content-length"}
NATIVE_PREFIX = "/v8/management"
PRO_PREFIX = "/v0/management"


def require(condition, message):
    if not condition:
        raise AssertionError(message)


def digest(path):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def free_port():
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return listener.getsockname()[1]


def request(port, path, method="GET", body=None):
    connection = http.client.HTTPConnection("127.0.0.1", port, timeout=5)
    try:
        connection.request(method, path, body=body, headers={
            "Authorization": "Bearer " + KEY,
            "Content-Type": "application/json",
        })
        response = connection.getresponse()
        return response.status, response.read()
    finally:
        connection.close()


class CoreProcess:
    def __init__(self, binary, root, port):
        self.binary = binary
        self.root = root
        self.port = port
        self.process = None
        self.log = None
        (root / "auth").mkdir()
        # Isolate storage and HOME; no copied user config, credentials or database.
        (root / "config.yaml").write_text(f'''config-version: 8
server:
  host: 127.0.0.1
  port: {port}
management:
  allow-remote: false
  secret-key: {KEY}
  disable-control-panel: true
  disable-auto-update-panel: true
oauth:
  auth-dir: {json.dumps(str(root / "auth"))}
plugins:
  enabled: false
''', encoding="utf-8")

    def start(self):
        require(self.process is None, "Core is already running")
        environment = {
            "PATH": os.environ.get("PATH", "/usr/bin:/bin"),
            "HOME": str(self.root), "TZ": "UTC", "LANG": "C.UTF-8",
            "USAGE_DB_PATH": str(self.root / "usage.sqlite"),
            "USAGE_SERVICE_ENABLED": "true",
        }
        self.log = (self.root / "server.log").open("a", encoding="utf-8")
        self.process = subprocess.Popen(
            [str(self.binary), "-config", str(self.root / "config.yaml"), "-local-model"],
            cwd=self.root, env=environment, stdout=self.log, stderr=subprocess.STDOUT,
        )
        deadline = time.monotonic() + 25
        while time.monotonic() < deadline:
            require(self.process.poll() is None, "Core exited before readiness; see server.log")
            try:
                status, _ = request(self.port, NATIVE_PREFIX + "/config")
                if status == 200:
                    return
            except (OSError, http.client.HTTPException):
                pass
            time.sleep(0.1)
        raise RuntimeError("Core readiness timed out; see server.log")

    def stop(self):
        if self.process is not None:
            try:
                if self.process.poll() is None:
                    self.process.terminate()
                    try:
                        self.process.wait(timeout=15)
                    except subprocess.TimeoutExpired:
                        self.process.kill()
                        self.process.wait(timeout=5)
                        raise RuntimeError("Core did not exit gracefully after SIGTERM")
            finally:
                self.process = None
                self.log.close()
                self.log = None


class RecordingProxy:
    """A loopback wire recorder, not an API mock: forwards bytes to real Core.

    It does not rewrite paths, responses, statuses, headers or authentication.
    Transport-only hop headers are removed as required for a forwarding proxy.
    """

    def __init__(self, core_port):
        self.phase = "startup"
        self.transcript = []
        outer = self

        class Handler(BaseHTTPRequestHandler):
            def handle_request(self):
                body = self.rfile.read(int(self.headers.get("Content-Length", "0")))
                connection = http.client.HTTPConnection("127.0.0.1", core_port, timeout=10)
                try:
                    headers = {key: value for key, value in self.headers.items()
                               if key.lower() not in HOP_HEADERS | {"host"}}
                    connection.request(self.command, self.path, body=body, headers=headers)
                    response = connection.getresponse()
                    payload = response.read()
                    outer.transcript.append({
                        "phase": outer.phase, "method": self.command,
                        "path": self.path, "status": response.status,
                        "authenticated": self.headers.get("Authorization") == "Bearer " + KEY,
                        "responseSha256": hashlib.sha256(payload).hexdigest(),
                    })
                    self.send_response_only(response.status)
                    for key, value in response.getheaders():
                        if key.lower() not in HOP_HEADERS:
                            self.send_header(key, value)
                    self.send_header("Content-Length", str(len(payload)))
                    self.end_headers()
                    self.wfile.write(payload)
                finally:
                    connection.close()

            do_GET = handle_request
            do_POST = handle_request
            do_PUT = handle_request

            def log_message(self, *_args):
                pass

        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()

    @property
    def base(self):
        return f"http://127.0.0.1:{self.server.server_port}"

    def stop(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=5)


def validate_wire(transcript, phase, write=False):
    rows = [row for row in transcript if row["phase"] == phase]
    expected = {
        ("GET", NATIVE_PREFIX + "/config"): 200,
        ("GET", NATIVE_PREFIX + "/credentials"): 200,
        ("POST", PRO_PREFIX + "/auth-files/test"): 400,
        ("GET", PRO_PREFIX + "/data/overview"): 200,
        ("GET", PRO_PREFIX + "/pro/proxy-pool/status"): 200,
        ("GET", PRO_PREFIX + "/usage/quota-cache"): 200,
    }
    if write:
        expected[("PUT", PRO_PREFIX + "/usage/quota-cache")] = 200
    seen = set()
    for row in rows:
        operation = row["method"], urlsplit(row["path"]).path
        require(operation in expected, f"Unexpected frontend request: {row}")
        require(row["authenticated"], f"Shared bearer authentication missing: {row}")
        require(row["status"] == expected[operation], f"Wrong route response: {row}")
        seen.add(operation)
    require(seen == expected.keys(), f"Missing wire operations: {expected.keys() - seen}")
    require(any(row["path"].endswith("/usage/quota-cache?stats=1") for row in rows),
            "Quota stats query did not reach Core")


def run_frontend(bun, frontend, root, proxy, label, phase, base_suffix=""):
    proxy.phase = label
    base = proxy.base + base_suffix
    output = root / (label + ".json")
    command = [str(bun), "--tsconfig-override", str(frontend / "tsconfig.json"),
               str(Path(__file__).with_name("client.ts")),
               str(frontend), base, phase, str(output)]
    environment = {
        "PATH": os.environ.get("PATH", "/usr/bin:/bin"),
        "HOME": str(root), "TZ": "UTC", "LANG": "C.UTF-8", "NO_PROXY": "*",
    }
    completed = subprocess.run(command, cwd=frontend, env=environment,
                               capture_output=True, text=True, timeout=60)
    (root / (label + ".log")).write_text(completed.stdout + completed.stderr, encoding="utf-8")
    result = json.loads(output.read_text()) if output.exists() else {}
    require(completed.returncode == 0 and result.get("passed") is True,
            f"Frontend {label} failed: {result.get('error', completed.stderr)}")
    validate_wire(proxy.transcript, label, write=phase == "write")
    result["connectionBase"] = base_suffix or "bare"
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True, type=Path,
                        help="Real Core release/build executable; never downloaded by this harness")
    parser.add_argument("--management", required=True, type=Path,
                        help="Generated patched frontend with installed locked dependencies")
    parser.add_argument("--bun", default="bun", help="Bun 1.3.14 executable")
    parser.add_argument("--output", required=True, type=Path, help="New isolated evidence directory")
    parser.add_argument("--require-v8-pro-404", action="store_true",
                        help="Require current-release negative controls (future aliases may differ)")
    args = parser.parse_args()
    binary, frontend, root = args.binary.resolve(), args.management.resolve(), args.output.resolve()
    bun = Path(shutil.which(args.bun) or args.bun).resolve()
    require(binary.is_file() and os.access(binary, os.X_OK), "Core binary is not executable")
    require(bun.is_file(), "Bun is missing; supply --bun")
    require((frontend / "src/services/api/client.ts").is_file(), "Generated frontend is missing")
    require((frontend / "node_modules").is_dir(), "Frontend dependencies must already be installed")
    root.mkdir(parents=True, exist_ok=False)
    receipt = {
        "passed": False, "binarySha256": digest(binary),
        "frontendClientSha256": digest(frontend / "src/services/api/client.ts"),
        "frontendSourcesSha256": {
            path: digest(frontend / "src" / path) if (frontend / "src" / path).is_file() else None
            for path in [
                "services/api/client.ts", "services/api/config.ts", "services/api/authFiles.ts",
                "pro/shared/proManagementTransport.ts", "pro/shared/proManagementUrl.ts",
                "pro/modules/quota/extensions/sqliteQuotaCache.ts",
                "pro/modules/quota/extensions/persistenceMiddleware.ts",
                "pro/modules/dataManagement/dataManagement.ts",
                "pro/modules/proxyPool/proxyPool.ts", "stores/useQuotaStore.ts",
            ]
        },
        "bunVersion": subprocess.check_output([str(bun), "--version"], text=True).strip(),
        "phases": [], "negativeControls": [],
    }
    core = CoreProcess(binary, root, free_port())
    proxy = RecordingProxy(core.port)
    try:
        core.start()
        for path, method, body in [
            ("/usage/quota-cache", "GET", None),
            ("/data/overview", "GET", None),
            ("/pro/proxy-pool/status", "GET", None),
            ("/auth-files/test", "POST", b'{"name":"","model":""}'),
        ]:
            status, _ = request(core.port, NATIVE_PREFIX + path, method, body)
            receipt["negativeControls"].append({"method": method,
                                                "path": NATIVE_PREFIX + path, "status": status})
            if args.require_v8_pro_404:
                require(status == 404, f"Current-release v8 negative control returned {status}: {path}")

        receipt["phases"].append(run_frontend(bun, frontend, root, proxy, "bare-base-write", "write"))
        receipt["phases"].append(run_frontend(bun, frontend, root, proxy,
                                             "v8-base-read", "read", NATIVE_PREFIX + "/"))
        receipt["phases"].append(run_frontend(bun, frontend, root, proxy,
                                             "v0-base-read", "read", PRO_PREFIX + "/"))
        core.stop()
        require((root / "usage.sqlite").is_file(), "Core did not create isolated SQLite database")
        # Core is stopped: inspect its database without requiring
        # SQLite to create WAL sidecars for a read-only connection.
        with sqlite3.connect(f"file:{root / 'usage.sqlite'}?mode=ro&immutable=1", uri=True) as database:
            require(database.execute("PRAGMA quick_check").fetchone()[0] == "ok", "SQLite check failed")
        core.start()
        receipt["phases"].append(run_frontend(bun, frontend, root, proxy,
                                             "restart-hydrate", "hydrate", NATIVE_PREFIX + "/"))
        before, after = receipt["phases"][0]["quota"], receipt["phases"][-1]["quota"]
        require(before == after, f"Quota data/revision/generation changed over restart: {before}, {after}")
        require(receipt["phases"][0]["pluginQuota"] == receipt["phases"][-1]["pluginQuota"],
                "Plugin quota data/revision changed over restart")
        require(not any(row["method"] == "PUT" and row["phase"] == "restart-hydrate"
                        for row in proxy.transcript), "Hydration must not mirror data back to Core")
        receipt["passed"] = True
    except Exception:
        receipt["error"] = traceback.format_exc()
    finally:
        try:
            core.stop()
        except Exception:
            receipt["passed"] = False
            receipt["shutdownError"] = traceback.format_exc()
        proxy.stop()
        (root / "http-transcript.json").write_text(json.dumps(proxy.transcript, indent=2) + "\n")
        (root / "receipt.json").write_text(json.dumps(receipt, indent=2) + "\n")
    print(json.dumps({"passed": receipt["passed"], "receipt": str(root / "receipt.json")}))
    raise SystemExit(0 if receipt["passed"] else 1)


if __name__ == "__main__":
    main()
