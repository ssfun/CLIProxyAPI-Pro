package observability

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/redisqueue"
)

type webDAVRoundTripperFunc func(*http.Request) (*http.Response, error)

func (fn webDAVRoundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestWebDAVHTTPClientHasRequestTimeout(t *testing.T) {
	client := newWebDAVHTTPClient()
	if client.Timeout != webDAVRequestTimeout {
		t.Fatalf("newWebDAVHTTPClient() timeout = %v, want %v", client.Timeout, webDAVRequestTimeout)
	}
}

func TestWebDAVContextAddsBoundedDeadline(t *testing.T) {
	ctx, cancel := webDAVContext(context.Background())
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("webDAVContext() did not add a deadline")
	}
	if remaining := time.Until(deadline); remaining <= 0 || remaining > webDAVRequestTimeout {
		t.Fatalf("webDAVContext() remaining deadline = %v, want within %v", remaining, webDAVRequestTimeout)
	}
}

func TestWebDAVContextPreservesEarlierCallerDeadline(t *testing.T) {
	parent, parentCancel := context.WithTimeout(context.Background(), time.Second)
	defer parentCancel()
	want, _ := parent.Deadline()
	ctx, cancel := webDAVContext(parent)
	defer cancel()
	got, ok := ctx.Deadline()
	if !ok || !got.Equal(want) {
		t.Fatalf("webDAVContext() deadline = %v, %v; want %v, true", got, ok, want)
	}
}

func TestWebDAVBackupHonorsHTTPClientTimeout(t *testing.T) {
	store := openTestStore(t)
	client := &http.Client{
		Timeout: 20 * time.Millisecond,
		Transport: webDAVRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			<-req.Context().Done()
			return nil, req.Context().Err()
		}),
	}
	service := &Service{
		store:        store,
		server:       NewServer(Config{Enabled: true, BatchSize: 10}, store),
		webDAVClient: client,
	}
	startedAt := time.Now()
	err := service.backupToWebDAV(context.Background(), MonitoringWebDAVBackupConfig{
		Enabled: true,
		URL:     "https://webdav.example/backups",
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("backupToWebDAV() error = %v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("backupToWebDAV() elapsed = %v, want bounded request", elapsed)
	}
}

func TestLastWebDAVBackupIsRefreshedAndScopedToTarget(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	current := MonitoringWebDAVBackupConfig{URL: "https://webdav.example/current"}
	other := MonitoringWebDAVBackupConfig{URL: "https://webdav.example/other"}
	finishedAt := time.Now().Add(-time.Minute).UnixMilli()
	if _, err := store.StartDataOperation(ctx, DataOperation{
		Kind: "backup", Status: dataOperationSuccess, FinishedAtMS: finishedAt,
		Metadata: map[string]any{"trigger": "manual", "targetKey": webDAVTargetKey(current)},
	}); err != nil {
		t.Fatal(err)
	}
	service := &Service{store: store}
	got, err := service.lastWebDAVBackupForTarget(ctx, current)
	if err != nil || got.UnixMilli() != finishedAt {
		t.Fatalf("current target last backup=%v err=%v", got, err)
	}
	got, err = service.lastWebDAVBackupForTarget(ctx, other)
	if err != nil || !got.IsZero() {
		t.Fatalf("other target inherited last backup=%v err=%v", got, err)
	}
}

func TestCollectorRetriesPoppedBatchAfterSQLiteFailure(t *testing.T) {
	store := openTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	redisqueue.SetEnabled(false)
	redisqueue.SetEnabled(true)
	redisqueue.SetPersistenceQueueConfig(3600, 1000, 1024*1024)
	redisqueue.SetPersistenceEnabled(true)
	redisqueue.SetUsageStatisticsEnabled(true)
	t.Cleanup(func() {
		redisqueue.SetPersistenceEnabled(false)
		redisqueue.SetEnabled(false)
	})
	if _, err := store.db.ExecContext(ctx, `create trigger fail_usage_insert before insert on usage_events begin select raise(abort, 'forced usage write failure'); end`); err != nil {
		t.Fatalf("create trigger error = %v", err)
	}
	payload, err := json.Marshal(testUsageEvent(0, false, 10))
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	redisqueue.Enqueue(payload)
	service := &Service{ctx: ctx, cfg: Config{BatchSize: 10, PollInterval: 5 * time.Millisecond}, store: store}
	done := make(chan struct{})
	go func() {
		service.collect(ctx)
		close(done)
	}()

	// Allow several failed persistence attempts so the item has definitely left the upstream queue.
	time.Sleep(75 * time.Millisecond)
	if _, err := store.db.ExecContext(ctx, `drop trigger fail_usage_insert`); err != nil {
		t.Fatalf("drop trigger error = %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		events, _, countErr := store.Counts(ctx)
		if countErr == nil && events == 1 {
			cancel()
			<-done
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	t.Fatal("collector did not retry the popped batch after SQLite recovered")
}

func TestCollectorPersistsUsageWhileLegacySubscriberIsConnected(t *testing.T) {
	store := openTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	redisqueue.SetEnabled(false)
	redisqueue.SetEnabled(true)
	redisqueue.SetPersistenceQueueConfig(3600, 1000, 1024*1024)
	redisqueue.SetPersistenceEnabled(true)
	redisqueue.SetUsageStatisticsEnabled(true)
	t.Cleanup(func() {
		redisqueue.SetPersistenceEnabled(false)
		redisqueue.SetEnabled(false)
	})

	subscriber, unsubscribe := redisqueue.SubscribeUsage()
	defer unsubscribe()
	select {
	case <-subscriber:
	case <-time.After(time.Second):
		t.Fatal("usage subscriber did not receive initial capability payload")
	}

	service := &Service{ctx: ctx, cfg: Config{BatchSize: 10, PollInterval: 5 * time.Millisecond}, store: store}
	done := make(chan struct{})
	go func() {
		service.collect(ctx)
		close(done)
	}()

	payload, err := json.Marshal(testUsageEvent(0, false, 10))
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	redisqueue.Enqueue(payload)
	select {
	case got := <-subscriber:
		if string(got) != string(payload) {
			t.Fatalf("subscriber payload = %s, want %s", got, payload)
		}
	case <-time.After(time.Second):
		t.Fatal("legacy subscriber did not receive usage payload")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		events, _, countErr := store.Counts(ctx)
		if countErr == nil && events == 1 {
			cancel()
			<-done
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	t.Fatal("collector did not persist usage while legacy subscriber was connected")
}
