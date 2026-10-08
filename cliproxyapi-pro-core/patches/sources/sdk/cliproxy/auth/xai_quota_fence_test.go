package auth

import (
	"context"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/embeddedusage"
	proquota "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/quota"
)

func TestXAIQuotaCommitSerializesReplacement(t *testing.T) {
	ctx := context.Background()
	manager := NewManager(nil, nil, nil)
	old, err := manager.Register(ctx, &Auth{ID: "same.json", FileName: "same.json", Provider: "xai", Metadata: map[string]any{"sub": "old"}})
	if err != nil {
		t.Fatal(err)
	}
	entry := embeddedusage.QuotaCacheEntry{Provider: "xai", FileName: "same.json", IdentityFingerprint: proquota.XAIQuotaIdentityFingerprint("same.json", "old", "", "")}
	guard := func(entry embeddedusage.QuotaCacheEntry, write func() error) error { return write() }
	if runtimeGuard, ok := any(manager).(interface {
		WithCurrentXAIQuotaIdentity(embeddedusage.QuotaCacheEntry, func() error) error
	}); ok {
		guard = runtimeGuard.WithCurrentXAIQuotaIdentity
	}
	entered, release, written := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() { written <- guard(entry, func() error { close(entered); <-release; return nil }) }()
	<-entered
	started, replaced := make(chan struct{}), make(chan error, 1)
	go func() {
		close(started)
		fresh := old.Clone()
		fresh.Metadata["sub"] = "new"
		_, err := manager.Register(ctx, fresh)
		replaced <- err
	}()
	<-started
	premature := false
	select {
	case err = <-replaced:
		premature = true
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if writeErr := <-written; writeErr != nil {
		t.Fatal(writeErr)
	}
	if !premature {
		err = <-replaced
	}
	if err != nil {
		t.Fatal(err)
	}
	if premature {
		t.Fatal("registration changed while the quota commit callback was still running")
	}
	called := false
	err = guard(entry, func() error { called = true; return nil })
	if err == nil || called {
		t.Fatalf("stale identity commit ran: err=%v called=%v", err, called)
	}
	t.Log("replacement waited for quota commit; stale commit callback was rejected")
}
