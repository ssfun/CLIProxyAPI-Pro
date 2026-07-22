package embeddedusage

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/redisqueue"
)

func TestCollectorRetriesPoppedBatchAfterSQLiteFailure(t *testing.T) {
	store := openTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	redisqueue.SetEnabled(false)
	redisqueue.SetEnabled(true)
	redisqueue.SetUsageStatisticsEnabled(true)
	t.Cleanup(func() { redisqueue.SetEnabled(false) })
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
