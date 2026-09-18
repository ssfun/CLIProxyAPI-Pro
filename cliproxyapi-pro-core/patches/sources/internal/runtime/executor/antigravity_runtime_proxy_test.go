package executor

import (
	"testing"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"
)

func TestAntigravityTransportCacheTracksRuntimeProxyGeneration(t *testing.T) {
	proxyutil.ClearRuntimeProxyOverride()
	t.Cleanup(proxyutil.ClearRuntimeProxyOverride)
	ResetAntigravityTransports()
	auth := &cliproxyauth.Auth{ID: "runtime-proxy-cache"}
	const base = "http://127.0.0.1:18080"
	before := antigravityProxiedHTTP11Transport(auth, base)
	if before == nil {
		t.Fatal("base transport is nil")
	}
	proxyutil.SetRuntimeProxyOverride(base, "socks5://127.0.0.1:18318")
	during := antigravityProxiedHTTP11Transport(auth, base)
	if during == nil || during == before {
		t.Fatal("takeover reused pre-takeover transport")
	}
	proxyutil.ClearRuntimeProxyOverride()
	after := antigravityProxiedHTTP11Transport(auth, base)
	if after == nil || after == during || after == before {
		t.Fatal("clear reused transport from an earlier generation")
	}
}
