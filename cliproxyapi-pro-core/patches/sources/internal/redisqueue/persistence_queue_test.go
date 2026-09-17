package redisqueue

import (
	"testing"
	"time"
)

func withPersistenceQueue(t *testing.T, fn func()) {
	t.Helper()
	previousEnabled := Enabled()
	SetEnabled(false)
	SetEnabled(true)
	SetPersistenceQueueConfig(3600, 100000, 256*1024*1024)
	SetPersistenceEnabled(true)
	t.Cleanup(func() {
		SetPersistenceEnabled(false)
		SetEnabled(false)
		SetEnabled(previousEnabled)
	})
	fn()
}

func TestPersistenceQueueReceivesUsageWhileSubscriberIsConnected(t *testing.T) {
	withPersistenceQueue(t, func() {
		subscriber, unsubscribe := SubscribeUsage()
		defer unsubscribe()
		requireUsageSubscriberPayload(t, subscriber, usageSupportRefreshPayload)

		Enqueue([]byte("usage-record"))

		requireUsageSubscriberPayload(t, subscriber, "usage-record")
		items := PopOldestPersistence(1)
		if len(items) != 1 || string(items[0]) != "usage-record" {
			t.Fatalf("PopOldestPersistence() items = %q, want persisted usage record", items)
		}
		if items := PopOldest(1); len(items) != 0 {
			t.Fatalf("legacy PopOldest() items = %q, want subscriber broadcast semantics unchanged", items)
		}
	})
}

func TestPersistenceQueueBoundsDropsAndReportsStats(t *testing.T) {
	withPersistenceQueue(t, func() {
		SetPersistenceQueueConfig(3600, 2, 7)
		Enqueue([]byte("one"))
		Enqueue([]byte("two"))
		Enqueue([]byte("three"))

		stats := PersistenceStats()
		if stats.Depth != 1 || stats.Bytes != 5 || stats.DroppedEventsTotal != 2 {
			t.Fatalf("PersistenceStats() = %+v, want depth=1 bytes=5 dropped=2", stats)
		}
		items := PopOldestPersistence(10)
		if len(items) != 1 || string(items[0]) != "three" {
			t.Fatalf("PopOldestPersistence() items = %q, want newest bounded item", items)
		}
	})
}

func TestPersistenceQueueRetentionDropsAreObservable(t *testing.T) {
	withPersistenceQueue(t, func() {
		SetPersistenceQueueConfig(1, 100, 1024)
		Enqueue([]byte("expired"))

		persistenceGlobal.mu.Lock()
		persistenceGlobal.items[persistenceGlobal.head].enqueuedAt = time.Now().Add(-2 * time.Second)
		persistenceGlobal.mu.Unlock()

		stats := PersistenceStats()
		if stats.Depth != 0 || stats.DroppedEventsTotal != 1 {
			t.Fatalf("PersistenceStats() = %+v, want expired event reported as dropped", stats)
		}
	})
}
