#!/usr/bin/env python3
"""Black-box HTTP checks for inspection batch receipts and retained evidence."""

import json
import hashlib
import os
import shutil
import socket
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path


def free_ports():
    with socket.socket() as first, socket.socket() as second:
        first.bind(("127.0.0.1", 0))
        second.bind(("127.0.0.1", 0))
        return first.getsockname()[1], second.getsockname()[1]


root = Path(os.environ.get("INSPECTION_E2E_OUTPUT", "/private/tmp/inspection-batch-history-e2e"))
binary = Path(os.environ.get("INSPECTION_SERVER", ""))
if not binary.is_file():
    sys.exit("Set INSPECTION_SERVER to a built, patched Core server binary")
root.mkdir(parents=True, exist_ok=True)
server_port, provider_port = free_ports()
base = f"http://127.0.0.1:{server_port}/v0/management/account-inspection"
opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
receipts = []
coverage_gaps = []
requests = {}
request_lock = threading.Lock()
slow_entered = threading.Event()
slow_release = threading.Event()


class Provider(BaseHTTPRequestHandler):
    def do_POST(self):
        self.rfile.read(int(self.headers.get("Content-Length", "0")))
        letter = self.path.split("/")[1]
        with request_lock:
            requests[letter] = requests.get(letter, 0) + 1
        mode = json.loads((root / "modes.json").read_text()).get(letter, "healthy")
        if mode == "slow":
            slow_entered.set()
            slow_release.wait(timeout=45)
            mode = "healthy"
        status, payload = {
            "quota": (429, {"error": {"code": "quota_exhausted", "message": "quota exhausted"}}),
            "healthy": (200, {"choices": [{"message": {"content": "pong"}}]}),
            "unauthorized": (401, {"error": {"message": "authentication required"}}),
        }[mode]
        body = json.dumps(payload).encode()
        try:
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        except (BrokenPipeError, ConnectionResetError):
            pass

    def log_message(self, *_args):
        pass


def call(method, path, payload=None):
    data = None if payload is None else json.dumps(payload).encode()
    request = urllib.request.Request(base + path, data, method=method)
    request.add_header("Authorization", "Bearer inspection-e2e-only")
    if data is not None:
        request.add_header("Content-Type", "application/json")
    try:
        with opener.open(request, timeout=35) as response:
            return response.status, json.load(response)
    except urllib.error.HTTPError as exc:
        return exc.code, json.load(exc)


def ok(method, path, payload=None, expected=200):
    status, body = call(method, path, payload)
    assert status == expected, (method, path, status, body)
    return body


def note(step, **data):
    receipts.append({"step": step, **data})
    (root / "result.json").write_text(json.dumps({"passed": False, "receipts": receipts}, ensure_ascii=False, indent=2) + "\n")


def binary_sha256():
    digest = hashlib.sha256()
    with binary.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def item(row, action=None, suggested=False):
    return {key: row[key] for key in ("key", "provider", "fileName", "authIndex", "resultRef")} | {
        "action": action or row["action"], "suggested": suggested,
    }


def row(name):
    body = ok("GET", "/status?details=1")
    return next(entry for entry in body["status"]["results"] if entry["fileName"] == name)


def inspect(current):
    result = ok("POST", "/inspect-one?details=1", {"item": item(current)})
    return next(entry for entry in result["status"]["results"] if entry["key"] == current["key"])


def preflight(items, kind="action"):
    return ok("POST", "/batches/preflight", {"kind": kind, "scope": {"type": "selected", "items": items}})


def wait_until(description, reader, condition, seconds=35):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        value = reader()
        if condition(value):
            return value
        time.sleep(0.1)
    raise AssertionError(f"timed out waiting for {description}: {value}")


def wait_batch(operation_id):
    return wait_until("batch completion", lambda: ok("GET", f"/batches/{operation_id}"), lambda body: body["state"] == "completed")


def start_server():
    output = (root / "server.log").open("a")
    process = subprocess.Popen([str(binary), "-config", str(root / "config.yaml")], stdout=output, stderr=subprocess.STDOUT)
    for _ in range(120):
        try:
            if call("GET", "/status")[0] == 200:
                return process, output
        except (urllib.error.URLError, TimeoutError, ConnectionError):
            pass
        if process.poll() is not None:
            raise AssertionError(f"server exited: {process.returncode}")
        time.sleep(0.1)
    raise AssertionError("server did not start")


def stop_server(process, output, kill=False):
    process.kill() if kill else process.terminate()
    try:
        process.wait(timeout=10)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait(timeout=10)
    output.close()


def disconnect_execute(operation_id):
    path = f"/v0/management/account-inspection/batches/{operation_id}/execute"
    request = (f"POST {path} HTTP/1.1\r\nHost: 127.0.0.1\r\n"
               "Authorization: Bearer inspection-e2e-only\r\n"
               "Content-Type: application/json\r\nContent-Length: 2\r\nConnection: close\r\n\r\n{}").encode()
    with socket.create_connection(("127.0.0.1", server_port), timeout=5) as sock:
        sock.sendall(request)
        sock.shutdown(socket.SHUT_RDWR)


shutil.rmtree(root / "usage", ignore_errors=True)
shutil.rmtree(root / "auth", ignore_errors=True)
(root / "auth").mkdir()
(root / "modes.json").write_text(json.dumps({"a": "quota", "b": "unauthorized", "c": "healthy", "d": "healthy"}))
for letter in "abcd":
    (root / "auth" / f"xai-{letter}.json").write_text(json.dumps({
        "api_key": f"secret-e2e-{letter}",
        "base_url": f"http://127.0.0.1:{provider_port}/{letter}/v1",
        "disabled": False,
        "email": f"inspection-{letter}@example.invalid",
        "type": "xai", "using_api": True,
    }))
(root / "config.yaml").write_text(
    f'host: "127.0.0.1"\nport: {server_port}\nauth-dir: "{root / "auth"}"\n'
    'remote-management:\n  allow-remote: false\n'
    '  secret-key: "$2a$10$.PgSmWihpe3QCpGTzikH3.5kWgQP7dmZ3hxemXEn5pyajUnzuujQi"\n'
    '  disable-control-panel: true\n'
)
provider = ThreadingHTTPServer(("127.0.0.1", provider_port), Provider)
threading.Thread(target=provider.serve_forever, daemon=True).start()
process = output = None
try:
    process, output = start_server()
    schedule = ok("GET", "/status?details=1")["schedule"]
    schedule["settings"]["autoExecuteQuotaLimitDisable"] = False
    schedule["settings"]["autoExecuteQuotaRecoveryEnable"] = False
    schedule["settings"]["autoExecuteAccountInvalidAction"] = "none"
    schedule["settings"]["autoExecuteRequestErrorAction"] = "none"
    ok("PUT", "/schedule", schedule)
    ok("POST", "/run", {}, 202)
    wait_until("initial inspection", lambda: ok("GET", "/status?details=1"), lambda body: body["status"]["state"] == "completed")
    a, b, c, d = (row(f"xai-{letter}.json") for letter in "abcd")
    assert a["isQuota"] and b["statusCode"] == 401

    newer_c = inspect(c)
    assert newer_c["resultRef"] != c["resultRef"]
    old = preflight([item(c, "disable")])
    assert old["summary"]["stale"] == 1 and old["items"][0]["status"] == "stale", old
    note("old_result_ref_preflight_stale", oldRef=c["resultRef"], currentRef=newer_c["resultRef"])

    prepared = preflight([item(newer_c, "disable")])
    assert prepared["items"][0]["status"] == "ready" and prepared["items"][0]["effect"] == "admin_disable", prepared
    newest_c = inspect(newer_c)
    ok("POST", f'/batches/{prepared["operationId"]}/execute', {}, 202)
    stale = wait_batch(prepared["operationId"])
    assert stale["summary"]["stale"] == 1 and not row("xai-c.json")["disabled"], stale
    retry = ok("POST", f'/batches/{prepared["operationId"]}/retry', {})
    assert retry["operationId"] != prepared["operationId"] and retry["parentOperationId"] == prepared["operationId"]
    assert retry["items"][0]["status"] == "ready" and retry["items"][0]["item"]["resultRef"] == newest_c["resultRef"], retry
    ok("POST", f'/batches/{retry["operationId"]}/execute', {}, 202)
    assert wait_batch(retry["operationId"])["summary"]["succeeded"] == 1
    assert row("xai-c.json")["disabled"]
    note("execute_stale_and_retry_rebind", staleOperationId=prepared["operationId"], retryOperationId=retry["operationId"], newRef=newest_c["resultRef"])

    quota = preflight([item(a, "disable", True)])
    assert quota["items"][0]["effect"] == "quota_protection" and quota["summary"]["ready"] == 1, quota
    disconnect_execute(quota["operationId"])
    finished = wait_batch(quota["operationId"])
    assert finished["summary"]["succeeded"] == 1 and finished["items"][0]["effect"] == "quota_protection", finished
    note("disconnected_client_background_receipt", operationId=quota["operationId"], receipt=finished["summary"])

    (root / "modes.json").write_text(json.dumps({"a": "healthy", "b": "unauthorized", "c": "healthy", "d": "healthy"}))
    recovered_a = inspect(row("xai-a.json"))
    if recovered_a.get("quotaCooling") and recovered_a.get("action") == "enable":
        recovery = preflight([item(recovered_a, "enable", True)])
        assert recovery["items"][0]["effect"] == "quota_recovery" and recovery["summary"]["ready"] == 1, recovery
        ok("POST", f'/batches/{recovery["operationId"]}/execute', {}, 202)
        completed = wait_batch(recovery["operationId"])
        assert completed["summary"]["succeeded"] == 1, completed
        note("quota_recovery_effect", operationId=recovery["operationId"], receipt=completed["summary"])
    else:
        coverage_gaps.append("quota_recovery: official xAI 200 response has no usedPercent, so no suggested enable")

    manual = ok("POST", "/actions", {"items": [item(b, "disable")]})
    assert manual["summary"]["success"] == 1, manual
    b_operations = ok("GET", "/operations?key=" + urllib.parse.quote(b["key"]))["items"]
    direct = next(entry for entry in b_operations if entry["source"] == "manual" and entry["effect"] == "admin_disable")
    assert direct["status"] == "succeeded" and direct["before"]["statusCode"] == 401, direct
    for field in ("statusCode", "errorCode", "action", "resultRef", "observedAt"):
        assert direct["after"].get(field) == direct["before"].get(field), (field, direct)
    assert direct["after"]["executedEffect"] == "admin_disable" and direct["after"].get("executeError", "") == "", direct
    note("manual_action_after_preserves_diagnosis", operationId=direct["operationId"], statusCode=direct["after"]["statusCode"], errorCode=direct["after"]["errorCode"])

    delete = preflight([item(newest_c, "delete")])
    assert delete["items"][0]["effect"] == "delete", delete
    ok("POST", f'/batches/{delete["operationId"]}/execute', {}, 202)
    assert wait_batch(delete["operationId"])["summary"]["succeeded"] == 1
    assert not (root / "auth" / "xai-c.json").exists()
    history = ok("GET", "/history?key=" + urllib.parse.quote(c["key"]))
    operations = ok("GET", "/operations?key=" + urllib.parse.quote(c["key"]))
    assert any(entry["resultRef"] == newest_c["resultRef"] for entry in history["items"]), history
    assert any(entry["effect"] == "delete" and entry["before"]["resultRef"] == newest_c["resultRef"] for entry in operations["items"]), operations
    note("delete_retains_diagnostic_evidence", operationId=delete["operationId"], historyCount=history["pageInfo"]["total"])

    # The provider holds this HTTP probe until after the Core process is killed.
    slow = preflight([item(d)], "inspect")
    assert slow["summary"]["ready"] == 1, slow
    (root / "modes.json").write_text(json.dumps({"a": "healthy", "b": "unauthorized", "c": "healthy", "d": "slow"}))
    ok("POST", f'/batches/{slow["operationId"]}/execute', {}, 202)
    assert slow_entered.wait(timeout=10), "slow provider was not called"
    running = ok("GET", f'/batches/{slow["operationId"]}')
    assert running["state"] == "running" and running["items"][0]["status"] == "running", running
    with request_lock:
        calls_before_restart = requests["d"]
    stop_server(process, output, kill=True)
    process = output = None
    slow_release.set()
    process, output = start_server()
    interrupted = ok("GET", f'/batches/{slow["operationId"]}')
    assert interrupted["state"] == "interrupted" and interrupted["items"][0]["status"] == "interrupted", interrupted
    ok("POST", f'/batches/{slow["operationId"]}/execute', {}, 409)
    with request_lock:
        assert requests["d"] == calls_before_restart, requests
    note("running_restart_interrupted_no_replay", operationId=slow["operationId"], providerCalls=calls_before_restart)

    # Restarted snapshots are intentionally read-only until a fresh run.
    (root / "modes.json").write_text(json.dumps({"a": "healthy", "b": "unauthorized", "c": "healthy", "d": "healthy"}))
    ok("POST", "/run", {}, 202)
    wait_until("post-restart inspection", lambda: ok("GET", "/status?details=1"), lambda body: body["status"]["state"] == "completed" and not body["status"].get("restoredSnapshot", False))
    d = row("xai-d.json")

    # Make the evidence journal's final rename fail. The audit intent must be
    # durable before deletion, so this fault must leave the auth file intact.
    evidence_path = root / "usage" / "account-inspection-snapshot.json.evidence.json"
    evidence_backup = root / "usage" / "account-inspection-evidence.backup"
    before_count = ok("GET", "/operations?key=" + urllib.parse.quote(d["key"]))["pageInfo"]["total"]
    evidence_path.rename(evidence_backup)
    evidence_path.mkdir()
    try:
        denied = ok("POST", "/actions", {"items": [item(row("xai-d.json"), "delete")]}, 500)
        assert denied["summary"]["failed"] == 1 and (root / "auth" / "xai-d.json").exists(), denied
    finally:
        evidence_path.rmdir()
        evidence_backup.rename(evidence_path)
    assert ok("GET", "/operations?key=" + urllib.parse.quote(d["key"]))["pageInfo"]["total"] == before_count
    allowed = ok("POST", "/actions", {"items": [item(row("xai-d.json"), "delete")]})
    assert allowed["summary"]["success"] == 1 and not (root / "auth" / "xai-d.json").exists(), allowed
    assert ok("GET", "/operations?key=" + urllib.parse.quote(d["key"]))["pageInfo"]["total"] == before_count + 1
    note("audit_intent_write_failure_prevents_delete", firstOutcome=denied["summary"], secondOutcome=allowed["summary"])

    evidence_files = list(root.rglob("*.evidence.json"))
    assert evidence_files and all("secret-e2e-" not in file.read_text() and file.stat().st_mode & 0o777 == 0o600 for file in evidence_files)
    note("retained_evidence_redacted", files=[str(file.relative_to(root)) for file in evidence_files])
    artifact = {"passed": True, "binarySha256": binary_sha256(), "receipts": receipts, "coverageGaps": coverage_gaps}
    (root / "result.json").write_text(json.dumps(artifact, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps({"passed": True, "steps": [entry["step"] for entry in receipts], "coverageGaps": coverage_gaps, "artifact": str(root / "result.json")}, ensure_ascii=False))
except Exception as exc:
    (root / "result.json").write_text(json.dumps({"passed": False, "error": repr(exc), "receipts": receipts}, ensure_ascii=False, indent=2) + "\n")
    raise
finally:
    slow_release.set()
    if process is not None:
        stop_server(process, output)
    provider.shutdown()
