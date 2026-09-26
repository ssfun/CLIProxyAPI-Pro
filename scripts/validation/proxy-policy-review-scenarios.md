# Proxy policy review regression scenarios

Before implementation, the following failure cases define this review's scope:

1. A node URL omits the HTTP (80), HTTPS (443), or SOCKS (1080) default port,
   or pads its explicit port with zeros. Both configuration validation and the
   runtime recursion check must reject a loopback endpoint at the listener port.
   Different ports must remain usable. These parsing boundaries are checked in
   isolation because binding privileged/default ports is not portable.
2. A probe reaches a real HTTP target through a CONNECT proxy, then its caller
   cancels while waiting for headers or the response body. Cancellation must
   return an unsuccessful result without degrading or isolating the node.
   A probe's own timeout must still count as a failure.
3. Existing SOCKS round-robin, failover, draft probes, health counters, recovery,
   listener replacement and shutdown must keep working after simplification.

Repeat on a fresh upstream v7.3.17 checkout:

```sh
SRC_ROOT="$UPSTREAM" python3 cliproxyapi-pro-core/patches/apply_upstream_patches.py
cd "$UPSTREAM"
go test -race -json ./internal/pro/proxypool/... > proxy-policy-results.jsonl
```

The JSONL test log is the repeatable verification artifact. The network tests
use local real TCP listeners, HTTP CONNECT proxies and HTTP targets.
