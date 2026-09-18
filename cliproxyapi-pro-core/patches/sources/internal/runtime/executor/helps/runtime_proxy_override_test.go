package helps

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/pro/proxypool/socks5"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
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
