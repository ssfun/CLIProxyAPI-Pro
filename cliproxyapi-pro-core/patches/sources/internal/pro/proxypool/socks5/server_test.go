package socks5

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

type recordingListener struct {
	accepts atomic.Int64
	closed  chan struct{}
}

func (l *recordingListener) Accept() (net.Conn, error) {
	l.accepts.Add(1)
	<-l.closed
	return nil, net.ErrClosed
}

func (l *recordingListener) Close() error {
	select {
	case <-l.closed:
	default:
		close(l.closed)
	}
	return nil
}

func (*recordingListener) Addr() net.Addr { return testAddress("127.0.0.1:8318") }

type testAddress string

func (address testAddress) Network() string { return "tcp" }
func (address testAddress) String() string  { return string(address) }

func TestNewDoesNotAcceptUntilStart(t *testing.T) {
	listener := &recordingListener{closed: make(chan struct{})}
	server, err := New(listener, func(context.Context, string) (DialResult, error) {
		return DialResult{}, errors.New("unused")
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Close)
	time.Sleep(10 * time.Millisecond)
	if got := listener.accepts.Load(); got != 0 {
		t.Fatalf("accepts before Start = %d", got)
	}
	server.Start()
	deadline := time.Now().Add(time.Second)
	for listener.accepts.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := listener.accepts.Load(); got != 1 {
		t.Fatalf("accepts after Start = %d", got)
	}
	server.StopAccepting()
}
