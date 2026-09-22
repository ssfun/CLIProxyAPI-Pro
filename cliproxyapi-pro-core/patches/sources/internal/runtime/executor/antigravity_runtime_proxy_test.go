package executor

import (
	"context"
	"net/http"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"

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

func TestAntigravityClientPreservesRequestProxyWithRuntimeTakeover(t *testing.T) {
	proxyutil.ClearRuntimeProxyOverride()
	t.Cleanup(proxyutil.ClearRuntimeProxyOverride)
	ResetAntigravityTransports()
	t.Cleanup(ResetAntigravityTransports)
	const globalProxy = "http://global.example:8080"
	const requestProxy = "http://request.example:8081"
	const takeoverProxy = "http://pool.example:8082"
	cfg := &config.Config{}
	cfg.ProxyURL = globalProxy
	proxyutil.SetRuntimeProxyOverride(globalProxy, takeoverProxy)
	req, err := http.NewRequest(http.MethodGet, "https://upstream.example", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		ctx  context.Context
		auth *cliproxyauth.Auth
		want string
	}{
		{"request", cliproxyexecutor.WithRequestProxyURL(context.Background(), requestProxy), &cliproxyauth.Auth{ProxyURL: "http://auth.example:8083"}, requestProxy},
		{"global", context.Background(), &cliproxyauth.Auth{}, takeoverProxy},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := newAntigravityHTTPClient(test.ctx, cfg, test.auth, 0)
			transport, ok := client.Transport.(*http.Transport)
			if !ok || transport.Proxy == nil {
				t.Fatalf("transport = %T, want HTTP proxy transport", client.Transport)
			}
			got, err := transport.Proxy(req)
			if err != nil || got == nil || got.String() != test.want {
				t.Fatalf("proxy = %v, err = %v, want %s", got, err, test.want)
			}
		})
	}
}
