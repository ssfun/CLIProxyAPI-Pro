#!/usr/bin/env python3
"""Exercise v8/legacy configuration compatibility against a real Core process."""

import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import socket
import subprocess
import sys
import time
import traceback
import urllib.error
import urllib.request


MANAGEMENT_KEY = "v8-config-http-management-secret"
API_KEY_A = "v8-config-http-client-key-a"
API_KEY_B = "v8-config-http-client-key-b"
EXPECTED_POLICY_FEATURES = {"orphaned_purge_guard", "usage_key_target"}


def utc_now():
    return dt.datetime.now(dt.timezone.utc).isoformat()


def sha256_bytes(data):
    return hashlib.sha256(data).hexdigest()


def sha256_file(path):
    return sha256_bytes(path.read_bytes())


def free_port():
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as listener:
        listener.bind(("127.0.0.1", 0))
        return listener.getsockname()[1]


def body_summary(body):
    if body is None:
        return {"type": "empty"}
    if isinstance(body, dict):
        return {"type": "object", "keys": sorted(body)}
    if isinstance(body, list):
        return {"type": "array", "length": len(body)}
    return {"type": type(body).__name__, "value": body}


class HTTPClient:
    def __init__(self, base, key, transcript):
        self.base = base.rstrip("/")
        self.key = key
        self.transcript = transcript
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def request(self, method, path, body=None, timeout=5):
        payload = None if body is None else json.dumps(body).encode("utf-8")
        request = urllib.request.Request(self.base + path, data=payload, method=method)
        request.add_header("Authorization", "Bearer " + self.key)
        if payload is not None:
            request.add_header("Content-Type", "application/json")
        raw = b""
        try:
            with self.opener.open(request, timeout=timeout) as response:
                status = response.status
                raw = response.read()
        except urllib.error.HTTPError as error:
            status = error.code
            raw = error.read()
        content_type = ""
        parsed = None
        if raw:
            try:
                parsed = json.loads(raw)
                content_type = "json"
            except json.JSONDecodeError:
                parsed = raw.decode("utf-8", errors="replace")
                content_type = "text"
        self.transcript.append({
            "method": method,
            "path": path,
            "status": status,
            "responseType": content_type or "empty",
            "responseSha256": sha256_bytes(raw),
            "responseSummary": body_summary(parsed),
        })
        return status, parsed


def require(condition, message):
    if not condition:
        raise AssertionError(message)


def require_status(client, method, path, expected, body=None):
    status, response = client.request(method, path, body)
    require(status == expected, f"{method} {path}: status={status}, expected={expected}, body={response!r}")
    return response


def require_policy_capabilities(client):
    capabilities = require_status(client, "GET", "/v0/management/api-key-policy-capabilities", 200)
    require(isinstance(capabilities, dict), f"capabilities is not an object: {capabilities!r}")
    features = set(capabilities.get("features", []))
    require(EXPECTED_POLICY_FEATURES <= features, f"missing policy features: {sorted(EXPECTED_POLICY_FEATURES - features)}")
    require(capabilities.get("apiVersion", 0) >= 3, f"unexpected policy API version: {capabilities!r}")
    return {"apiVersion": capabilities["apiVersion"], "featuresChecked": sorted(EXPECTED_POLICY_FEATURES)}


def require_single_binding(client):
    bindings = require_status(client, "GET", "/v0/management/api-key-policy-bindings", 200)
    require(isinstance(bindings, dict), f"bindings is not an object: {bindings!r}")
    items = bindings.get("items", [])
    require(len(items) == 1, f"expected one API-key binding, got: {bindings!r}")
    require(items[0].get("keyRef"), f"binding has no keyRef: {bindings!r}")
    return bindings


def legacy_config(port, auth_dir, api_key=API_KEY_A):
    return f'''host: "127.0.0.1"
port: {port}
auth-dir: "{auth_dir}"
api-keys:
  - "{api_key}"
remote-management:
  allow-remote: false
  secret-key: "{MANAGEMENT_KEY}"
  disable-control-panel: true
  disable-auto-update-panel: true
plugins:
  enabled: false
'''


def v8_config(port, auth_dir, include_startup_fields=False, api_key=API_KEY_A):
    management_extra = ""
    observability = ""
    if include_startup_fields:
        management_extra = '  panel-github-repository: "https://example.invalid/v8-panel"\n'
        observability = '''observability:
  logs:
    debug: false
  usage:
    usage-statistics-enabled: false
'''
    return f'''config-version: 8
server:
  host: "127.0.0.1"
  port: {port}
management:
  allow-remote: false
  secret-key: "{MANAGEMENT_KEY}"
  disable-control-panel: true
  disable-auto-update-panel: true
{management_extra}access:
  api-keys:
    - "{api_key}"
oauth:
  auth-dir: "{auth_dir}"
routing:
  retry:
    request-retry: 3
{observability}plugins:
  enabled: false
'''


def assert_legacy_layout(text):
    require(re.search(r"(?m)^host:", text), "legacy root host is missing")
    require(re.search(r"(?m)^port:", text), "legacy root port is missing")
    require(re.search(r"(?m)^api-keys:", text), "legacy root api-keys is missing")
    require(re.search(r"(?m)^remote-management:", text), "legacy root remote-management is missing")
    require(not re.search(r"(?m)^config-version:", text), "legacy startup unexpectedly migrated the file")


def assert_v8_layout(text):
    require(re.search(r"(?m)^config-version:\s*8\s*$", text), "config-version: 8 is missing")
    for key in ("server", "management", "access", "oauth", "routing"):
        require(re.search(rf"(?m)^{re.escape(key)}:\s*$", text), f"v8 root {key} is missing")
    for legacy in ("host", "port", "auth-dir", "remote-management", "request-retry"):
        require(not re.search(rf"(?m)^{re.escape(legacy)}:", text), f"legacy root {legacy} survived v8 persistence")
    require(not re.search(r"(?m)^api-keys:\s*$", text), "legacy root api-keys survived v8 persistence")


def assert_missing_startup_fields(text):
    require("usage-statistics-enabled:" not in text, "missing usage-statistics-enabled was materialized")
    require("panel-github-repository:" not in text, "missing panel-github-repository was materialized")


class ScenarioProcess:
    def __init__(self, binary, output_root, scenario_id, config_text, usage_enabled=False):
        self.binary = binary
        self.usage_enabled = usage_enabled
        self.root = output_root / scenario_id
        self.root.mkdir(parents=True, exist_ok=True)
        self.config = self.root / "config.yaml"
        self.initial = self.root / "config.initial.yaml"
        self.log_path = self.root / "server.log"
        self.transcript_path = self.root / "http-transcript.json"
        self.initial.write_text(config_text, encoding="utf-8")
        shutil.copyfile(self.initial, self.config)
        self.log = None
        self.process = None
        self.transcript = []

    def start(self, port):
        environment = os.environ.copy()
        environment.update({
            "USAGE_DB_PATH": str(self.root / "usage.sqlite"),
            "USAGE_SERVICE_ENABLED": str(self.usage_enabled).lower(),
        })
        self.log = self.log_path.open("w", encoding="utf-8")
        self.process = subprocess.Popen(
            [str(self.binary), "-config", str(self.config), "-local-model"],
            cwd=self.root,
            env=environment,
            stdout=self.log,
            stderr=subprocess.STDOUT,
            text=True,
        )
        client = HTTPClient(f"http://127.0.0.1:{port}", MANAGEMENT_KEY, self.transcript)
        deadline = time.monotonic() + 25
        latest = None
        while time.monotonic() < deadline:
            if self.process.poll() is not None:
                raise RuntimeError(f"server exited before readiness with status {self.process.returncode}")
            try:
                status, latest = client.request("GET", "/v0/management/config")
                if status == 200:
                    return client
            except (urllib.error.URLError, TimeoutError, ConnectionError) as error:
                latest = repr(error)
            time.sleep(0.1)
        raise RuntimeError(f"server readiness timeout; latest response={latest!r}")

    def stop(self):
        if self.process is not None and self.process.poll() is None:
            self.process.terminate()
            try:
                self.process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait(timeout=5)
        if self.log is not None:
            self.log.flush()
            self.log.close()
        self.transcript_path.write_text(json.dumps(self.transcript, indent=2, sort_keys=True) + "\n", encoding="utf-8")

    def artifacts(self):
        result = {
            "directory": str(self.root),
            "initialConfig": str(self.initial),
            "config": str(self.config),
            "serverLog": str(self.log_path),
            "httpTranscript": str(self.transcript_path),
        }
        for label, path in (("initialConfigSha256", self.initial), ("configSha256", self.config),
                            ("serverLogSha256", self.log_path), ("httpTranscriptSha256", self.transcript_path)):
            if path.exists():
                result[label] = sha256_file(path)
        return result


def run_with_process(binary, output_root, scenario_id, config_factory, action):
    port = free_port()
    scenario_root = output_root / scenario_id
    config_text = config_factory(port, scenario_root / "auth")
    process = ScenarioProcess(binary, output_root, scenario_id, config_text)
    record = {"id": scenario_id, "status": "running", "port": port, "startedAt": utc_now(), "assertions": []}
    try:
        client = process.start(port)
        record["observations"] = action(process, client, record["assertions"])
        record["status"] = "passed"
    except Exception as error:
        record["status"] = "failed"
        record["error"] = {"type": type(error).__name__, "message": str(error), "traceback": traceback.format_exc()}
        raise
    finally:
        process.stop()
        record["finishedAt"] = utc_now()
        record["artifacts"] = process.artifacts()
    return record


def scenario_legacy_v0(process, client, assertions):
    require_status(client, "GET", "/v0/management/config", 200)
    capabilities = require_policy_capabilities(client)
    text = process.config.read_text(encoding="utf-8")
    assert_legacy_layout(text)
    assert_missing_startup_fields(text)
    assertions.extend([
        "legacy config starts and serves /v0/management/config",
        "Pro API-key policy capabilities remain available on /v0",
        "startup does not migrate legacy YAML or materialize absent usage/panel fields",
    ])
    return {"policy": capabilities, "persistedLayout": "legacy"}


def scenario_v8_coexistence(process, client, assertions):
    v8 = require_status(client, "GET", "/v8/management/config", 200)
    require_status(client, "GET", "/v0/management/config", 200)
    capabilities = require_policy_capabilities(client)
    bindings = require_single_binding(client)
    text = process.config.read_text(encoding="utf-8")
    assert_v8_layout(text)
    require('panel-github-repository: "https://github.com/ssfun/CLIProxyAPI-Pro"' in text,
            "existing v8 panel repository was not updated at its nested path")
    require(re.search(r"(?m)^\s{4}usage-statistics-enabled:\s*true\s*$", text),
            "existing v8 usage setting was not enabled at its nested path")
    require(isinstance(v8, dict) and v8.get("config-version") == 8, f"unexpected v8 config response: {v8!r}")
    assertions.extend([
        "/v8 and /v0 Management config routes coexist",
        "Pro API-key policy capabilities and one binding are available",
        "existing nested usage and panel settings receive the required Pro values after startup",
    ])
    return {"policy": capabilities, "bindingCount": 1, "configGeneration": bindings["configGeneration"]}


def scenario_v8_scalar_put(process, client, assertions):
    before = process.config.read_text(encoding="utf-8")
    assert_v8_layout(before)
    assert_missing_startup_fields(before)
    response = require_status(client, "PUT", "/v8/management/config/routing/retry/request-retry", 200, 1)
    require(isinstance(response, dict), f"unexpected save response type: {response!r}")
    require(response.get("status") == "ok", f"unexpected save status: {response!r}")
    require(response.get("config-version") == 8, f"unexpected saved config version: {response!r}")
    # v8.1.2 adds a server-id to successful writes; accept it while checking its shape.
    if "server-id" in response:
        require(isinstance(response["server-id"], str) and response["server-id"],
                f"unexpected server-id in save response: {response!r}")
    value = require_status(client, "GET", "/v8/management/config/routing/retry/request-retry", 200)
    require(value == 1, f"saved retry value is {value!r}, expected 1")
    after = process.config.read_text(encoding="utf-8")
    assert_v8_layout(after)
    assert_missing_startup_fields(after)
    assertions.extend([
        "v8 scalar PUT succeeds and reads back through the same nested path",
        "the persisted document remains v8",
        "the save does not materialize absent usage/panel fields",
    ])
    return {"savedPath": "routing.retry.request-retry", "savedValue": value}


def scenario_legacy_migration(process, client, assertions):
    before = process.config.read_text(encoding="utf-8")
    assert_legacy_layout(before)
    require_status(client, "PUT", "/v8/management/config/routing/retry/request-retry", 200, 2)
    after = process.config.read_text(encoding="utf-8")
    assert_v8_layout(after)
    require(API_KEY_A in after, "migration lost the configured client API key")
    keys = require_status(client, "GET", "/v0/management/api-keys", 200)
    require(keys == {"api-keys": [API_KEY_A]}, f"runtime API keys changed during migration: {keys!r}")
    capabilities = require_policy_capabilities(client)
    bindings = require_single_binding(client)
    assertions.extend([
        "the first successful v8 write migrates a legacy document to the v8 layout",
        "the client API key remains present on disk and through the v0 compatibility API",
        "Pro API-key capabilities and the migrated key binding remain available",
    ])
    return {"policy": capabilities, "bindingCount": 1, "configGeneration": bindings["configGeneration"]}


def scenario_v8_api_key_generation(process, client, assertions):
    initial = require_single_binding(client)
    original_ref = initial["items"][0]["keyRef"]
    initial_generation = initial["configGeneration"]
    require_status(client, "PUT", "/v8/management/config/access/api-keys", 200, [API_KEY_B])
    require_status(client, "PUT", "/v8/management/config/access/api-keys", 200, [API_KEY_A])
    stale_status, stale_body = client.request("POST", "/v0/management/api-key-policy-key", {"keyRef": original_ref})
    require(stale_status == 409, f"old keyRef survived v8 A->B->A writes: status={stale_status}, body={stale_body!r}")
    require(isinstance(stale_body, dict) and stale_body.get("error", {}).get("code") == "api_key_reference_stale",
            f"old keyRef returned the wrong conflict: {stale_body!r}")
    final = require_single_binding(client)
    require(final["configGeneration"] > initial_generation,
            f"config generation did not advance: initial={initial_generation}, final={final['configGeneration']}")
    require(final["items"][0]["keyRef"] != original_ref, "fresh binding reused the stale keyRef")
    text = process.config.read_text(encoding="utf-8")
    assert_v8_layout(text)
    require(API_KEY_A in text and API_KEY_B not in text, "final persisted API-key set is not A")
    assertions.extend([
        "v8 generic config saves perform API-key A->B->A without an intervening policy read",
        "the pre-change keyRef is rejected with api_key_reference_stale",
        "configGeneration advances and a fresh binding receives a different keyRef",
    ])
    return {"initialConfigGeneration": initial_generation, "finalConfigGeneration": final["configGeneration"]}


SCENARIOS = {
    "legacy_v0_compatibility": (legacy_config, scenario_legacy_v0),
    "v8_management_coexistence": (lambda port, auth: v8_config(port, auth, True), scenario_v8_coexistence),
    "v8_scalar_put_preserves_layout": (v8_config, scenario_v8_scalar_put),
    "legacy_v8_migration_preserves_pro_keys": (legacy_config, scenario_legacy_migration),
    "v8_api_key_generation_fence": (v8_config, scenario_v8_api_key_generation),
}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True, help="patched cli-proxy-api binary")
    parser.add_argument("--output", required=True, help="artifact directory containing receipt.json")
    parser.add_argument("--scenario", action="append", choices=sorted(SCENARIOS),
                        help="run only this scenario; repeat to select multiple")
    args = parser.parse_args()

    binary = Path(args.binary).expanduser().resolve()
    require(binary.is_file(), f"binary does not exist: {binary}")
    require(os.access(binary, os.X_OK), f"binary is not executable: {binary}")
    output = Path(args.output).expanduser().resolve()
    output.mkdir(parents=True, exist_ok=True)
    selected = args.scenario or list(SCENARIOS)
    receipt = {
        "schemaVersion": 1,
        "status": "running",
        "startedAt": utc_now(),
        "binary": str(binary),
        "binarySha256": sha256_file(binary),
        "selectedScenarios": selected,
        "scenarios": [],
    }
    receipt_path = output / "receipt.json"
    failure = None
    try:
        for scenario_id in selected:
            factory, action = SCENARIOS[scenario_id]
            try:
                record = run_with_process(binary, output, scenario_id, factory, action)
            except Exception as error:
                failure = error
                # run_with_process populated artifacts in its local record before re-raising;
                # rebuild a compact failure record from the scenario directory and traceback.
                scenario_root = output / scenario_id
                record = {
                    "id": scenario_id,
                    "status": "failed",
                    "finishedAt": utc_now(),
                    "error": {"type": type(error).__name__, "message": str(error), "traceback": traceback.format_exc()},
                    "artifacts": {"directory": str(scenario_root)},
                }
                for name in ("config.initial.yaml", "config.yaml", "server.log", "http-transcript.json"):
                    path = scenario_root / name
                    if path.exists():
                        record["artifacts"][name + "Sha256"] = sha256_file(path)
                receipt["scenarios"].append(record)
                break
            receipt["scenarios"].append(record)
        receipt["status"] = "failed" if failure else "passed"
    finally:
        receipt["finishedAt"] = utc_now()
        receipt_path.write_text(json.dumps(receipt, indent=2, sort_keys=True) + "\n", encoding="utf-8")
        print(receipt_path.read_text(encoding="utf-8"))
    if failure:
        raise SystemExit(1)


if __name__ == "__main__":
    main()
