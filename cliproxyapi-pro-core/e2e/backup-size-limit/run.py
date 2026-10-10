#!/usr/bin/env python3
"""Exercise backup size policy through real HTTP servers; retain reproducible receipts."""
import argparse
import contextlib
import hashlib
import http.client
import http.server
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import threading
import time

DEFAULT = 256 * 1024 * 1024
KEY = "backup-limit-e2e-local-secret"
PREFIX = "/v0/management"
UPLOAD = ["/data/backups/preview", "/data/backups/restore", "/usage/import/preview", "/usage/import"]
WEBDAV = ["/data/backups/webdav/preview", "/data/backups/webdav/restore", "/usage/webdav/preview", "/usage/webdav/restore"]
NAME = "cliproxy-pro-backup-20261010_000000_000000001.jsonl"


def sha(data):
    return hashlib.sha256(data).hexdigest()


def backup(size, model):
    record = json.dumps({"record_type": "model_prices", "version": 1,
                         "prices": {model: {"prompt": 1.25, "completion": 2.5, "cache": 0.5}}}, separators=(",", ":")).encode() + b"\n"
    manifest = json.dumps({"record_type": "backup_manifest", "version": 1,
                           "records": 1, "sha256": sha(record)}).encode() + b"\n"
    data = manifest + record
    assert size >= len(data)
    count, remainder = divmod(size - len(data), 1024)
    return data + (b" " * 1023 + b"\n") * count + b" " * remainder


def port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


class DAV(http.server.BaseHTTPRequestHandler):
    body = b""
    status = 200
    def do_GET(self):
        self.send_response(type(self).status)
        # Deliberately omit Content-Length to exercise actual read limits.
        self.end_headers()
        try:
            self.wfile.write(type(self).body)
        except (BrokenPipeError, ConnectionResetError):
            pass
    def log_message(self, *args):
        pass


def request(server_port, route, body=None, method="POST", headers=None, chunked=False):
    connection = http.client.HTTPConnection("127.0.0.1", server_port, timeout=120)
    request_headers = {"Authorization": "Bearer " + KEY, "Content-Type": "application/octet-stream"}
    request_headers.update(headers or {})
    if isinstance(body, dict):
        body = json.dumps(body).encode()
        request_headers["Content-Type"] = "application/json"
    if chunked:
        payload = body
        body = (payload[i:i + 1024] for i in range(0, len(payload), 1024))
    try:
        connection.request(method, PREFIX + route, body=body, headers=request_headers, encode_chunked=chunked)
        response = connection.getresponse()
        return response.status, response.read()
    finally:
        connection.close()


@contextlib.contextmanager
def server(binary, root, label, limit):
    directory = root / label
    directory.mkdir()
    server_port = port()
    config = directory / "config.yaml"
    config.write_text(f'''host: "127.0.0.1"
port: {server_port}
auth-dir: "{directory / 'auth'}"
remote-management:
  allow-remote: false
  secret-key: "{KEY}"
  disable-control-panel: true
  disable-auto-update-panel: true
plugins:
  enabled: false
''')
    env = os.environ.copy()
    for name in list(env):
        if name.startswith(("USAGE_", "CLIPROXY_BACKUP_", "WEBDAV_", "ACCOUNT_INSPECTION_", "PGSTORE_", "GITSTORE_", "OBJECTSTORE_")) or name in ("PRO_BACKUP_MAX_BYTES", "MANAGEMENT_PASSWORD"):
            env.pop(name)
    env.update(USAGE_SERVICE_ENABLED="true", USAGE_DATA_DIR=str(directory / "usage"))
    if limit is not None:
        env["PRO_BACKUP_MAX_BYTES"] = limit
    with (directory / "server.log").open("w") as log:
        process = subprocess.Popen([str(binary), "-config", str(config)], env=env, cwd=directory, stdout=log, stderr=subprocess.STDOUT)
        try:
            deadline = time.monotonic() + 40
            while time.monotonic() < deadline:
                if process.poll() is not None:
                    raise RuntimeError(f"Core exited; see {directory / 'server.log'}")
                try:
                    if request(server_port, "/data/overview", method="GET")[0] == 200:
                        break
                except OSError:
                    pass
                time.sleep(0.1)
            else:
                raise RuntimeError("Core readiness timeout")
            yield server_port
        finally:
            process.terminate()
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", required=True, type=Path)
    parser.add_argument("--output", type=Path, default=Path("/private/tmp/backup-size-limit-e2e"))
    args = parser.parse_args()
    binary = args.binary.resolve()
    args.output.mkdir(parents=True, exist_ok=True)
    root = Path(tempfile.mkdtemp(prefix="run-", dir=args.output)).resolve()
    receipt = {"binary_sha256": sha(binary.read_bytes()), "script_sha256": sha(Path(__file__).read_bytes()), "checks": [], "passed": False}
    dav = http.server.ThreadingHTTPServer(("127.0.0.1", 0), DAV)
    thread = threading.Thread(target=dav.serve_forever, daemon=True)
    thread.start()

    def check(p, route, body, expected=200, limit=None, **kwargs):
        status, raw = request(p, route, body, **kwargs)
        entry = {"route": route, "expected_status": expected, "status": status}
        if isinstance(body, bytes):
            entry.update(bytes=len(body), sha256=sha(body))
        receipt["checks"].append(entry)
        assert status == expected, (entry, raw[:1000])
        if expected == 413:
            error = json.loads(raw)["error"]
            entry["error"] = error
            assert str(limit) in error and "PRO_BACKUP_MAX_BYTES" in error, error
        return raw

    def configure_dav(p):
        check(p, "/data/settings", {"settings": {"webdav": {"url": f"http://127.0.0.1:{dav.server_port}", "enabled": False}}}, method="PUT")

    def dav_request(data):
        DAV.body = data
        return {"fileName": NAME, "expectedSha256": sha(data)}

    def prices(p):
        data = check(p, "/data/backups/export", None, method="GET")
        result = {}
        for line in data.splitlines():
            record = json.loads(line)
            if record.get("record_type") == "model_prices":
                result.update(record["prices"])
        return result

    try:
        with server(binary, root, "custom", "4096") as p:
            configure_dav(p)
            for route in UPLOAD:
                for chunked in (False, True):
                    check(p, route, backup(4095, "accepted"), chunked=chunked)
                    check(p, route, backup(4096, "accepted"), chunked=chunked)
                    check(p, route, backup(4097, "rejected"), 413, limit=4096, chunked=chunked)
                    assert "rejected" not in prices(p), (route, chunked, "oversized import mutated prices")
            for route in WEBDAV:
                for size in (4095, 4096, 4097):
                    data = backup(size, "rejected" if size > 4096 else "accepted")
                    check(p, route, dav_request(data), 413 if size > 4096 else 200, limit=4096)
                    if size > 4096:
                        assert "rejected" not in prices(p), (route, "oversized import mutated prices")
            assert "rejected" not in prices(p), "oversized import mutated persistent prices"
            check(p, "/data/backups/restore", backup(4096, "recovered"))
            assert "recovered" in prices(p)
            receipt["checks"].append({"persistence": "oversized record absent; recovery record present"})
            corrupt = backup(4096, "corrupt").replace(b'"prompt":1.25', b'"prompt":9.25')
            check(p, "/data/backups/restore", corrupt, 400)
            DAV.status = 500
            check(p, WEBDAV[0], {"fileName": NAME}, 502)
            DAV.status = 200

        # Each environment case is a fresh process. Chunked body avoids trusting headers.
        for index, value in enumerate((None, "", "0", "-1", "invalid", "9223372036854775808", "536870912")):
            with server(binary, root, f"env-{index}", value) as p:
                effective = 536870912 if value == "536870912" else DEFAULT
                # A known excessive Content-Length should be rejected without buffering it.
                check(p, UPLOAD[0], b"", 413, limit=effective,
                      headers={"Content-Length": str(effective + 1)})
                check(p, UPLOAD[0], backup(8192, "config-valid"), chunked=True)
                receipt["checks"].append({"configured": value, "effective_limit": effective})

        with server(binary, root, "default-large", None) as p:
            configure_dav(p)
            large = backup(97 * 1024 * 1024, "large-backup")
            for route in UPLOAD[:2]:
                check(p, route, large)
            assert "large-backup" in prices(p)
            large = backup(97 * 1024 * 1024, "large-webdav")
            for route in WEBDAV[:2]:
                check(p, route, dav_request(large))
            assert "large-webdav" in prices(p)
            receipt["checks"].append({"persistence": "97 MiB backup restored through upload and WebDAV"})
            passphrase = "local-e2e-backup-passphrase"
            encrypted = check(p, "/data/backups/export", {"passphrase": passphrase})
            for route in UPLOAD[:2]:
                check(p, route, encrypted, headers={"X-CLIProxy-Backup-Passphrase": passphrase})
            check(p, UPLOAD[0], encrypted, 400, headers={"X-CLIProxy-Backup-Passphrase": "wrong"})
        with server(binary, root, "encrypted-over", str(len(encrypted) - 1)) as p:
            for route in UPLOAD[:2]:
                check(p, route, encrypted, 413, limit=len(encrypted) - 1,
                      headers={"X-CLIProxy-Backup-Passphrase": passphrase})
        receipt["passed"] = True
    finally:
        dav.shutdown()
        dav.server_close()
        (root / "result.json").write_text(json.dumps(receipt, indent=2) + "\n")
        print(root / "result.json", flush=True)


if __name__ == "__main__":
    main()
