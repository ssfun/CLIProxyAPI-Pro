package helps

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/pro/proxypool/socks5"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"
)

func TestNewProxyAwareHTTPClientRoutesEmptyGlobalProxyThroughRuntimeOverride(t *testing.T) {
	proxyutil.ClearRuntimeProxyOverride()
	t.Cleanup(proxyutil.ClearRuntimeProxyOverride)
	previousDefaultTransport := http.DefaultTransport
	http.DefaultTransport = &http.Transport{Proxy: nil}
	t.Cleanup(func() { http.DefaultTransport = previousDefaultTransport })

	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, "ok")
	}))
	t.Cleanup(target.Close)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var proxyDials atomic.Int64
	server, err := socks5.New(listener, func(ctx context.Context, target string) (socks5.DialResult, error) {
		proxyDials.Add(1)
		conn, errDial := (&net.Dialer{}).DialContext(ctx, "tcp", target)
		return socks5.DialResult{Conn: conn}, errDial
	})
	if err != nil {
		t.Fatal(err)
	}
	server.Start()
	t.Cleanup(server.Close)

	proxyutil.SetRuntimeProxyOverride("", "socks5://"+listener.Addr().String())
	client := NewProxyAwareHTTPClient(context.Background(), &config.Config{}, &cliproxyauth.Auth{}, 2*time.Second)
	response, err := client.Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if got := proxyDials.Load(); got != 1 {
		t.Fatalf("proxy dials = %d, want 1", got)
	}

	proxyutil.ClearRuntimeProxyOverride()
	directClient := NewProxyAwareHTTPClient(context.Background(), &config.Config{}, &cliproxyauth.Auth{}, 2*time.Second)
	response, err = directClient.Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if got := proxyDials.Load(); got != 1 {
		t.Fatalf("proxy dials after clear = %d, want 1", got)
	}
}

func TestHTTPClientsPreserveRequestProxyWithRuntimeTakeover(t *testing.T) {
	proxyutil.ClearRuntimeProxyOverride()
	t.Cleanup(proxyutil.ClearRuntimeProxyOverride)
	const globalProxy = "http://global.example:8080"
	const requestProxy = "http://request.example:8081"
	const takeoverProxy = "http://pool.example:8082"
	cfg := &config.Config{}
	cfg.ProxyURL = globalProxy
	proxyutil.SetRuntimeProxyOverride(globalProxy, takeoverProxy)
	ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", &http.Transport{})
	ctx = cliproxyexecutor.WithRequestProxyURL(ctx, requestProxy)
	req, err := http.NewRequest(http.MethodGet, "https://upstream.example", nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, build := range map[string]func(context.Context, *config.Config, *cliproxyauth.Auth, time.Duration) *http.Client{
		"generic": NewProxyAwareHTTPClient,
		"utls":    NewUtlsHTTPClient,
		"devin":   NewDevinHTTPClient,
	} {
		t.Run(name, func(t *testing.T) {
			for _, test := range []struct {
				ctx  context.Context
				auth *cliproxyauth.Auth
				want string
			}{
				{ctx, &cliproxyauth.Auth{ProxyURL: "http://auth.example:8083"}, requestProxy},
				{context.Background(), &cliproxyauth.Auth{}, takeoverProxy},
			} {
				client := build(test.ctx, cfg, test.auth, time.Second)
				route := client.Transport
				if fallback, ok := route.(*fallbackRoundTripper); ok {
					route = fallback.fallbackRoundTripper()
				}
				transport, ok := route.(*http.Transport)
				if !ok || transport.Proxy == nil {
					t.Fatalf("transport = %T, want HTTP proxy transport", client.Transport)
				}
				got, err := transport.Proxy(req)
				if err != nil || got == nil || got.String() != test.want {
					t.Fatalf("proxy = %v, err = %v, want %s", got, err, test.want)
				}
			}
		})
	}
}

// TestRuntimeProxyPoolHTTP exercises real HTTP connections across runtime takeover
// changes. Run with go test -v ./internal/runtime/executor/helps -run TestRuntimeProxyPoolHTTP.
func TestRuntimeProxyPoolHTTP(t *testing.T) {
	for name, build := range map[string]func(context.Context, *config.Config, *cliproxyauth.Auth, time.Duration) *http.Client{
		"standard": NewProxyAwareHTTPClient, "utls-fallback": NewUtlsHTTPClient,
	} {
		t.Run(name, func(t *testing.T) {
			proxyutil.ClearRuntimeProxyOverride()
			ResetProxyTransports()
			t.Cleanup(proxyutil.ClearRuntimeProxyOverride)
			t.Cleanup(ResetProxyTransports)
			serve := func(label string) *httptest.Server {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = io.WriteString(w, label+"|"+r.RemoteAddr)
				}))
				t.Cleanup(server.Close)
				return server
			}
			target, proxyA, proxyB := serve("direct"), serve("A"), serve("B")
			cfg := &config.Config{}
			first := &cliproxyauth.Auth{ID: "first", ProxyConnectionPool: &cliproxyauth.ProxyConnectionPoolOverride{}}
			second := &cliproxyauth.Auth{ID: "second", ProxyConnectionPool: &cliproxyauth.ProxyConnectionPoolOverride{}}
			request := func(client *http.Client, label string) string {
				t.Helper()
				response, err := client.Get(target.URL)
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(response.Body)
				_ = response.Body.Close()
				if err != nil {
					t.Fatal(err)
				}
				if !strings.HasPrefix(string(body), label+"|") {
					t.Fatalf("route = %q, want %s", body, label)
				}
				return string(body)
			}
			client := func(auth *cliproxyauth.Auth) *http.Client {
				return build(context.Background(), cfg, auth, 2*time.Second)
			}
			proxyutil.SetRuntimeProxyOverride("", proxyA.URL)
			a1 := request(client(first), "A")
			if a2 := request(client(first), "A"); a1 != a2 {
				t.Fatalf("pool did not reuse connection: %s != %s", a1, a2)
			}
			if other := request(client(second), "A"); other == a1 {
				t.Fatal("credentials shared a connection")
			}
			// Capture A before advancing; the lazy uTLS builder must not resolve again.
			delayed := client(first)
			proxyutil.SetRuntimeProxyOverride("", proxyB.URL)
			b1 := request(client(first), "B")
			if b2 := request(client(first), "B"); b1 != b2 {
				t.Fatalf("new pool did not reuse: %s != %s", b1, b2)
			}
			request(delayed, "A")
			proxyutil.SetRuntimeProxyOverride("", proxyA.URL)
			if fresh := request(client(first), "A"); fresh == a1 {
				t.Fatal("retired generation reused its idle connection")
			}
			proxyutil.ClearRuntimeProxyOverride()
			direct := request(client(first), "direct")
			if again := request(client(first), "direct"); direct != again {
				t.Fatal("empty-proxy fallback did not pool")
			}
			t.Logf("verified pooling, credential isolation, A -> B -> A -> direct, captured snapshot (%s)", name)
		})
	}
}
