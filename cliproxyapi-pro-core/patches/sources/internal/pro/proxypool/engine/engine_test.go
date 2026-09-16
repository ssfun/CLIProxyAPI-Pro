package engine

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	proxyconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/proxypool/config"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"
	proxy "golang.org/x/net/proxy"
)

type connectProxy struct {
	listener net.Listener
	count    atomic.Int64
	wg       sync.WaitGroup
}

func startConnectProxy(t *testing.T) *connectProxy {
	t.Helper()
	listener, errListen := net.Listen("tcp", "127.0.0.1:0")
	if errListen != nil {
		t.Fatal(errListen)
	}
	server := &connectProxy{listener: listener}
	server.wg.Add(1)
	go func() {
		defer server.wg.Done()
		for {
			client, errAccept := listener.Accept()
			if errAccept != nil {
				return
			}
			server.wg.Add(1)
			go func() {
				defer server.wg.Done()
				defer client.Close()
				reader := bufio.NewReader(client)
				request, errRead := http.ReadRequest(reader)
				if errRead != nil || request.Method != http.MethodConnect {
					return
				}
				upstream, errDial := net.Dial("tcp", request.Host)
				if errDial != nil {
					_, _ = io.WriteString(client, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
					return
				}
				defer upstream.Close()
				server.count.Add(1)
				_, _ = io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n")
				var relay sync.WaitGroup
				relay.Add(2)
				go func() {
					defer relay.Done()
					_, _ = io.Copy(upstream, reader)
					if tcp, ok := upstream.(*net.TCPConn); ok {
						_ = tcp.CloseWrite()
					}
				}()
				go func() {
					defer relay.Done()
					_, _ = io.Copy(client, upstream)
					if tcp, ok := client.(*net.TCPConn); ok {
						_ = tcp.CloseWrite()
					}
				}()
				relay.Wait()
			}()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		server.wg.Wait()
	})
	return server
}

func startEchoServer(t *testing.T) net.Listener {
	t.Helper()
	listener, errListen := net.Listen("tcp", "127.0.0.1:0")
	if errListen != nil {
		t.Fatal(errListen)
	}
	go func() {
		for {
			conn, errAccept := listener.Accept()
			if errAccept != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
	t.Cleanup(func() { _ = listener.Close() })
	return listener
}

func freeAddress(t *testing.T) string {
	t.Helper()
	listener, errListen := net.Listen("tcp", "127.0.0.1:0")
	if errListen != nil {
		t.Fatal(errListen)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	return address
}

func TestNormalizeProbeConcurrency(t *testing.T) {
	tests := []struct {
		input int
		want  int
	}{{0, DefaultProbeConcurrency}, {-1, DefaultProbeConcurrency}, {1, 1}, {8, 8}, {9, MaxProbeConcurrency}}
	for _, test := range tests {
		if got := NormalizeProbeConcurrency(test.input); got != test.want {
			t.Fatalf("NormalizeProbeConcurrency(%d) = %d, want %d", test.input, got, test.want)
		}
	}
}

func TestUnconfiguredEngineStatusDoesNotPublishMalformedProxyURL(t *testing.T) {
	status := New().Status()
	if status.ProxyURL != "" || status.Listen != "" || status.Strategy != "" || status.Ready {
		t.Fatalf("Status() = %+v", status)
	}
}

func TestEngineRoutesSOCKSConnectionsRoundRobin(t *testing.T) {
	proxyA := startConnectProxy(t)
	proxyB := startConnectProxy(t)
	echo := startEchoServer(t)
	listen := freeAddress(t)
	cfg := proxyconfig.Default()
	cfg.Listen = listen
	cfg.HealthCheck.Enabled = false
	cfg.MaxFailoverAttempts = 2
	cfg.Nodes = []proxyconfig.NodeConfig{
		{ID: "a", URL: "http://" + proxyA.listener.Addr().String(), Enabled: true, Weight: 1, Order: 10},
		{ID: "b", URL: "http://" + proxyB.listener.Addr().String(), Enabled: true, Weight: 1, Order: 20},
	}
	engine := New()
	if errApply := engine.ApplyConfig(cfg); errApply != nil {
		t.Fatalf("ApplyConfig() error = %v", errApply)
	}
	t.Cleanup(engine.Close)

	dialer, errDialer := proxy.SOCKS5("tcp", listen, nil, proxy.Direct)
	if errDialer != nil {
		t.Fatal(errDialer)
	}
	for index := range 2 {
		conn, errDial := dialer.Dial("tcp", echo.Addr().String())
		if errDial != nil {
			t.Fatalf("SOCKS5 Dial() #%d error = %v", index, errDial)
		}
		message := fmt.Sprintf("message-%d", index)
		if _, errWrite := io.WriteString(conn, message); errWrite != nil {
			t.Fatal(errWrite)
		}
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		buffer := make([]byte, len(message))
		if _, errRead := io.ReadFull(conn, buffer); errRead != nil {
			t.Fatal(errRead)
		}
		_ = conn.Close()
		if string(buffer) != message {
			t.Fatalf("echo = %q, want %q", buffer, message)
		}
	}
	if proxyA.count.Load() != 1 || proxyB.count.Load() != 1 {
		t.Fatalf("proxy counts = a:%d b:%d", proxyA.count.Load(), proxyB.count.Load())
	}
	status := engine.Status()
	if !status.Ready || status.TotalNodes != 2 || status.EligibleNodes != 2 || status.Generation != 1 {
		t.Fatalf("Status() = %+v", status)
	}
}

func TestEngineFailsOverToNextProxy(t *testing.T) {
	working := startConnectProxy(t)
	echo := startEchoServer(t)
	listen := freeAddress(t)
	closedAddress := freeAddress(t)
	cfg := proxyconfig.Default()
	cfg.Listen = listen
	cfg.HealthCheck.Enabled = false
	cfg.DialTimeout.Duration = 200 * time.Millisecond
	cfg.MaxFailoverAttempts = 2
	cfg.Nodes = []proxyconfig.NodeConfig{
		{ID: "broken", URL: "http://" + closedAddress, Enabled: true, Weight: 1, Order: 10},
		{ID: "working", URL: "http://" + working.listener.Addr().String(), Enabled: true, Weight: 1, Order: 20},
	}
	engine := New()
	if errApply := engine.ApplyConfig(cfg); errApply != nil {
		t.Fatal(errApply)
	}
	t.Cleanup(engine.Close)

	dialer, _ := proxy.SOCKS5("tcp", listen, nil, proxy.Direct)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	type dialResult struct {
		conn net.Conn
		err  error
	}
	done := make(chan dialResult, 1)
	go func() {
		conn, errDial := dialer.Dial("tcp", echo.Addr().String())
		done <- dialResult{conn: conn, err: errDial}
	}()
	select {
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	case result := <-done:
		if result.err != nil {
			t.Fatalf("Dial() error = %v", result.err)
		}
		_ = result.conn.Close()
	}
	if working.count.Load() != 1 {
		t.Fatalf("working proxy count = %d", working.count.Load())
	}
}

func TestProbeDraftDoesNotMutateSavedNodeRuntime(t *testing.T) {
	working := startConnectProxy(t)
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"ip":"203.0.113.8","country":"Testland"}`)
	}))
	t.Cleanup(target.Close)
	cfg := proxyconfig.Default()
	cfg.Listen = freeAddress(t)
	cfg.HealthCheck.Enabled = false
	cfg.Nodes = []proxyconfig.NodeConfig{
		{ID: "saved", URL: "http://127.0.0.1:1", Enabled: true, Weight: 1, Order: 10},
	}
	engine := New()
	if errApply := engine.ApplyConfig(cfg); errApply != nil {
		t.Fatal(errApply)
	}
	t.Cleanup(engine.Close)

	result := engine.ProbeDraft(
		context.Background(),
		"saved",
		"http://"+working.listener.Addr().String(),
		target.URL,
	)
	if !result.Success || result.ExitIP != "203.0.113.8" {
		t.Fatalf("ProbeDraft() = %+v", result)
	}
	snapshot := engine.Status().Nodes[0]
	if snapshot.State != "unknown" || snapshot.TotalConnects != 0 || snapshot.SuccessConnects != 0 {
		t.Fatalf("draft probe mutated saved runtime: %+v", snapshot)
	}
}

func TestProbeDraftWorksBeforeRuntimeConfigIsApplied(t *testing.T) {
	working := startConnectProxy(t)
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"ip":"203.0.113.9","country":"Testland"}`)
	}))
	t.Cleanup(target.Close)

	engine := New()
	t.Cleanup(engine.Close)
	result := engine.ProbeDraft(
		context.Background(),
		"draft",
		"http://"+working.listener.Addr().String(),
		target.URL,
	)
	if !result.Success || result.ExitIP != "203.0.113.9" {
		t.Fatalf("ProbeDraft() before ApplyConfig = %+v", result)
	}
}

func TestProbeDoesNotReenterPoolWhenNodeMatchesGlobalProxy(t *testing.T) {
	working := startConnectProxy(t)
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"ip":"203.0.113.10","country":"Testland"}`)
	}))
	t.Cleanup(target.Close)

	listen := freeAddress(t)
	nodeURL := "http://" + working.listener.Addr().String()
	cfg := proxyconfig.Default()
	cfg.Listen = listen
	cfg.HealthCheck.Enabled = false
	cfg.HealthCheck.Timeout.Duration = 500 * time.Millisecond
	cfg.Nodes = []proxyconfig.NodeConfig{{ID: "global", URL: nodeURL, Enabled: true, Weight: 1, Order: 10}}
	engine := New()
	if errApply := engine.ApplyConfig(cfg); errApply != nil {
		t.Fatal(errApply)
	}
	t.Cleanup(engine.Close)

	proxyutil.SetRuntimeProxyOverride(nodeURL, "socks5://"+listen)
	t.Cleanup(proxyutil.ClearRuntimeProxyOverride)
	result := engine.Probe(context.Background(), "global", target.URL)
	if !result.Success || result.ExitIP != "203.0.113.10" {
		t.Fatalf("Probe() with matching global proxy = %+v", result)
	}
	if working.count.Load() != 1 {
		t.Fatalf("pool node proxy connections = %d, want 1", working.count.Load())
	}
}
