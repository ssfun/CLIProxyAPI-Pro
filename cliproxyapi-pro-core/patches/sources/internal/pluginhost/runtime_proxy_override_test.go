package pluginhost

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"
)

func TestWireProfileUsesRuntimeProxyOverrideWithEmptyBase(t *testing.T) {
	listener, errListen := net.Listen("tcp", "127.0.0.1:0")
	if errListen != nil {
		t.Fatal(errListen)
	}
	t.Cleanup(func() { _ = listener.Close() })

	accepted := make(chan struct{}, 1)
	go func() {
		conn, errAccept := listener.Accept()
		if errAccept != nil {
			return
		}
		accepted <- struct{}{}
		_ = conn.Close()
	}()

	proxyutil.ClearRuntimeProxyOverride()
	t.Cleanup(proxyutil.ClearRuntimeProxyOverride)
	proxyutil.SetRuntimeProxyOverride("", "socks5://"+listener.Addr().String())

	bridge := &hostHTTPClient{auth: &coreauth.Auth{}}
	request := httptest.NewRequest("GET", "http://127.0.0.1:1/", nil)
	client, cleanup, errClient := bridge.newHTTPClientForRequest(
		context.Background(),
		&config.Config{},
		pluginapi.HTTPRequest{WireProfile: &pluginapi.HTTPWireProfile{HTTP1Only: true}},
		request,
	)
	if errClient != nil {
		t.Fatal(errClient)
	}
	if cleanup != nil {
		defer cleanup()
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.DialContext == nil {
		t.Fatalf("transport %T does not expose DialContext", client.Transport)
	}
	dialCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, _ := transport.DialContext(dialCtx, "tcp", "127.0.0.1:1")
	if conn != nil {
		_ = conn.Close()
	}

	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("wire-profile transport bypassed the runtime SOCKS5 override")
	}
}
