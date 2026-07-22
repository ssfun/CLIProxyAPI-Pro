package embeddedusage

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/embeddedusage/internalusage"
)

var errTestParse = errors.New("parse failed")

func testUsageEvent(index int, failed bool, totalTokens int64) internalusage.Event {
	timestamp := time.Unix(1_700_000_000+int64(index), 0).UTC()
	latency := int64(100 + index)
	ttft := int64(20 + index)
	status := 200
	if failed {
		status = 429
	}
	return internalusage.Event{
		RequestID:         "request-" + string(rune('a'+index)),
		EventHash:         "event-hash-" + string(rune('a'+index)),
		TimestampMS:       timestamp.UnixMilli(),
		Timestamp:         timestamp.Format(time.RFC3339Nano),
		Provider:          "test",
		ExecutorType:      "TestExecutor",
		Model:             "model",
		Alias:             "client-model",
		Endpoint:          "POST /v1/test",
		Method:            "POST",
		Path:              "/v1/test",
		TotalTokens:       totalTokens,
		InputTokens:       totalTokens / 2,
		OutputTokens:      totalTokens - totalTokens/2,
		LatencyMS:         &latency,
		TTFTMS:            &ttft,
		StatusCode:        &status,
		UpstreamRequestID: "upstream-request",
		RetryAfter:        "30",
		Stream:            index%2 == 0,
		ReasoningEffort:   "medium",
		ServiceTier:       "default",
		Failed:            failed,
		CreatedAtMS:       timestamp.UnixMilli(),
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	return openTestStoreAt(t, filepath.Join(t.TempDir(), "usage.sqlite"))
}

func openTestStoreAt(t *testing.T, path string) *Store {
	t.Helper()
	store, err := OpenStore(path)
	if err != nil {
		t.Fatalf("OpenStore() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})
	return store
}

func insertTestUsageEvents(t *testing.T, store *Store, events ...internalusage.Event) {
	t.Helper()
	result, err := store.InsertEvents(context.Background(), events)
	if err != nil {
		t.Fatalf("InsertEvents() error = %v", err)
	}
	if result.Inserted != len(events) {
		t.Fatalf("InsertEvents() inserted = %d, want %d", result.Inserted, len(events))
	}
}

func TestInsertEventsNotifiesSubscribers(t *testing.T) {
	store := openTestStore(t)
	signal := store.EventSignal()

	insertTestUsageEvents(t, store, testUsageEvent(0, false, 10))

	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal("event signal was not closed after inserting usage events")
	}

	nextSignal := store.EventSignal()
	select {
	case <-nextSignal:
		t.Fatal("replacement event signal must remain open until the next insert")
	default:
	}
}

func TestUsageSummaryRespectsCursorLimit(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	insertTestUsageEvents(t, store,
		testUsageEvent(0, false, 10),
		testUsageEvent(1, true, 20),
		testUsageEvent(2, false, 30),
	)

	recent, err := store.RecentEvents(ctx, 1)
	if err != nil {
		t.Fatalf("RecentEvents() error = %v", err)
	}
	if len(recent) != 1 {
		t.Fatalf("RecentEvents() len = %d, want 1", len(recent))
	}

	latestID, _, err := store.LatestCursor(ctx)
	if err != nil {
		t.Fatalf("LatestCursor() error = %v", err)
	}
	summary, err := store.UsageSummary(ctx, latestID)
	if err != nil {
		t.Fatalf("UsageSummary() error = %v", err)
	}

	if summary.TotalRequests != 3 || summary.SuccessCount != 2 || summary.FailureCount != 1 || summary.TotalTokens != 60 {
		t.Fatalf("UsageSummary() = %+v, want total=3 success=2 failure=1 tokens=60", summary)
	}
}

func TestUsageSummaryStopsAtCursor(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	insertTestUsageEvents(t, store,
		testUsageEvent(0, false, 10),
		testUsageEvent(1, true, 20),
	)
	cursorID, _, err := store.LatestCursor(ctx)
	if err != nil {
		t.Fatalf("LatestCursor() error = %v", err)
	}

	insertTestUsageEvents(t, store, testUsageEvent(2, false, 30))
	summary, err := store.UsageSummary(ctx, cursorID)
	if err != nil {
		t.Fatalf("UsageSummary() error = %v", err)
	}

	if summary.TotalRequests != 2 || summary.SuccessCount != 1 || summary.FailureCount != 1 || summary.TotalTokens != 30 {
		t.Fatalf("UsageSummary() = %+v, want total=2 success=1 failure=1 tokens=30", summary)
	}
}

func TestUsageSummaryZeroCursorIsEmpty(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	insertTestUsageEvents(t, store, testUsageEvent(0, false, 10))
	summary, err := store.UsageSummary(ctx, 0)
	if err != nil {
		t.Fatalf("UsageSummary() error = %v", err)
	}

	if summary.TotalRequests != 0 || summary.SuccessCount != 0 || summary.FailureCount != 0 || summary.TotalTokens != 0 {
		t.Fatalf("UsageSummary() = %+v, want empty summary", summary)
	}
}

func TestEventsAfterAllowsSentinelLimit(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	events := make([]internalusage.Event, usageEventsSentinelLimit)
	for index := range events {
		events[index] = testUsageEvent(index, false, int64(index+1))
	}
	insertTestUsageEvents(t, store, events...)

	recent, err := store.EventsAfter(ctx, 0, usageEventsSentinelLimit)
	if err != nil {
		t.Fatalf("EventsAfter() error = %v", err)
	}
	if len(recent) != usageEventsSentinelLimit {
		t.Fatalf("EventsAfter() len = %d, want %d", len(recent), usageEventsSentinelLimit)
	}
}

func TestUsageSummaryCacheInvalidatesAfterInsert(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	insertTestUsageEvents(t, store, testUsageEvent(0, false, 10))
	latestID, _, err := store.LatestCursor(ctx)
	if err != nil {
		t.Fatalf("LatestCursor() error = %v", err)
	}
	firstSummary, err := store.UsageSummary(ctx, latestID)
	if err != nil {
		t.Fatalf("UsageSummary() first error = %v", err)
	}
	if firstSummary.TotalRequests != 1 || firstSummary.TotalTokens != 10 {
		t.Fatalf("first UsageSummary() = %+v, want total=1 tokens=10", firstSummary)
	}

	insertTestUsageEvents(t, store, testUsageEvent(1, true, 20))
	latestID, _, err = store.LatestCursor(ctx)
	if err != nil {
		t.Fatalf("LatestCursor() second error = %v", err)
	}
	secondSummary, err := store.UsageSummary(ctx, latestID)
	if err != nil {
		t.Fatalf("UsageSummary() second error = %v", err)
	}
	if secondSummary.TotalRequests != 2 || secondSummary.SuccessCount != 1 || secondSummary.FailureCount != 1 || secondSummary.TotalTokens != 30 {
		t.Fatalf("second UsageSummary() = %+v, want total=2 success=1 failure=1 tokens=30", secondSummary)
	}
}

func TestUsageSummaryPersistsAcrossStoreReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.sqlite")
	ctx := context.Background()

	store := openTestStoreAt(t, path)
	insertTestUsageEvents(t, store,
		testUsageEvent(0, false, 10),
		testUsageEvent(1, true, 20),
	)
	latestID, _, err := store.LatestCursor(ctx)
	if err != nil {
		t.Fatalf("LatestCursor() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened := openTestStoreAt(t, path)
	summary, err := reopened.UsageSummary(ctx, latestID)
	if err != nil {
		t.Fatalf("UsageSummary() after reopen error = %v", err)
	}
	if summary.TotalRequests != 2 || summary.SuccessCount != 1 || summary.FailureCount != 1 || summary.TotalTokens != 30 {
		t.Fatalf("UsageSummary() after reopen = %+v, want total=2 success=1 failure=1 tokens=30", summary)
	}

	var persistedRequests int64
	if err := reopened.db.QueryRowContext(ctx, `select total_requests from usage_summary where id = 1`).Scan(&persistedRequests); err != nil {
		t.Fatalf("usage_summary lookup error = %v", err)
	}
	if persistedRequests != 2 {
		t.Fatalf("usage_summary total_requests = %d, want 2", persistedRequests)
	}
}

func TestUsageSummaryUpdatesAfterDeleteEventsBefore(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	beforeState, err := store.UsageDatasetState(ctx)
	if err != nil {
		t.Fatalf("UsageDatasetState() before delete error = %v", err)
	}
	insertTestUsageEvents(t, store,
		testUsageEvent(0, false, 10),
		testUsageEvent(1, true, 20),
		testUsageEvent(2, false, 30),
	)
	signal := store.EventSignal()
	deleted, err := store.DeleteEventsBefore(ctx, testUsageEvent(2, false, 30).TimestampMS)
	if err != nil {
		t.Fatalf("DeleteEventsBefore() error = %v", err)
	}
	if deleted != 2 {
		t.Fatalf("DeleteEventsBefore() deleted = %d, want 2", deleted)
	}
	latestID, _, err := store.LatestCursor(ctx)
	if err != nil {
		t.Fatalf("LatestCursor() error = %v", err)
	}
	summary, err := store.UsageSummary(ctx, latestID)
	if err != nil {
		t.Fatalf("UsageSummary() error = %v", err)
	}
	if summary.TotalRequests != 1 || summary.SuccessCount != 1 || summary.FailureCount != 0 || summary.TotalTokens != 30 {
		t.Fatalf("UsageSummary() after delete = %+v, want total=1 success=1 failure=0 tokens=30", summary)
	}
	afterState, err := store.UsageDatasetState(ctx)
	if err != nil {
		t.Fatalf("UsageDatasetState() after delete error = %v", err)
	}
	if afterState.Generation != beforeState.Generation+1 {
		t.Fatalf("generation after delete = %d, want %d", afterState.Generation, beforeState.Generation+1)
	}
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal("retention delete did not notify subscribers")
	}
}

func TestResetUsageStatisticsClearsOnlyUsageEvents(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	settings := MonitoringSettings{RetentionDays: 30}
	if err := store.SetMonitoringSettings(ctx, settings); err != nil {
		t.Fatalf("SetMonitoringSettings() error = %v", err)
	}
	if err := store.AddDeadLetter(ctx, `{"authorization":"secret"}`, errTestParse); err != nil {
		t.Fatalf("AddDeadLetter() error = %v", err)
	}
	insertTestUsageEvents(t, store,
		testUsageEvent(0, false, 10),
		testUsageEvent(1, true, 20),
	)
	latestIDBefore, _, err := store.LatestCursor(ctx)
	if err != nil {
		t.Fatalf("LatestCursor() before reset error = %v", err)
	}
	stateBefore, err := store.UsageDatasetState(ctx)
	if err != nil {
		t.Fatalf("UsageDatasetState() before reset error = %v", err)
	}
	signal := store.EventSignal()

	result, err := store.ResetUsageStatistics(ctx)
	if err != nil {
		t.Fatalf("ResetUsageStatistics() error = %v", err)
	}
	if result.DeletedEvents != 2 {
		t.Fatalf("deleted events = %d, want 2", result.DeletedEvents)
	}
	if result.Generation != stateBefore.Generation+1 || result.ResetAtMS <= 0 {
		t.Fatalf("reset state = %+v, want generation %d and reset timestamp", result, stateBefore.Generation+1)
	}
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal("reset did not notify subscribers")
	}

	events, deadLetters, err := store.Counts(ctx)
	if err != nil {
		t.Fatalf("Counts() error = %v", err)
	}
	if events != 0 || deadLetters != 1 {
		t.Fatalf("counts after reset = events:%d deadLetters:%d, want 0/1", events, deadLetters)
	}
	latestID, _, err := store.LatestCursor(ctx)
	if err != nil {
		t.Fatalf("LatestCursor() after reset error = %v", err)
	}
	if latestID != 0 {
		t.Fatalf("latest id after reset = %d, want 0", latestID)
	}
	storedSettings, err := store.GetMonitoringSettings(ctx)
	if err != nil {
		t.Fatalf("GetMonitoringSettings() error = %v", err)
	}
	if storedSettings.RetentionDays != settings.RetentionDays {
		t.Fatalf("retention days after reset = %d, want %d", storedSettings.RetentionDays, settings.RetentionDays)
	}

	insertTestUsageEvents(t, store, testUsageEvent(2, false, 30))
	newEvents, err := store.EventsAfter(ctx, 0, 10)
	if err != nil {
		t.Fatalf("EventsAfter() error = %v", err)
	}
	if len(newEvents) != 1 || newEvents[0].ID <= latestIDBefore {
		t.Fatalf("new event ids = %+v, want one id greater than %d", newEvents, latestIDBefore)
	}
	stateAfter, err := store.UsageDatasetState(ctx)
	if err != nil {
		t.Fatalf("UsageDatasetState() after insert error = %v", err)
	}
	if stateAfter.Generation != result.Generation {
		t.Fatalf("generation after new insert = %d, want %d", stateAfter.Generation, result.Generation)
	}
}

func TestResetUsageStatisticsOnEmptyStoreIsNoop(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	stateBefore, err := store.UsageDatasetState(ctx)
	if err != nil {
		t.Fatalf("UsageDatasetState() error = %v", err)
	}
	signal := store.EventSignal()
	result, err := store.ResetUsageStatistics(ctx)
	if err != nil {
		t.Fatalf("ResetUsageStatistics() error = %v", err)
	}
	if result.DeletedEvents != 0 || result.Generation != stateBefore.Generation || result.ResetAtMS != stateBefore.ResetAtMS {
		t.Fatalf("empty reset result = %+v, want unchanged state %+v", result, stateBefore)
	}
	select {
	case <-signal:
		t.Fatal("empty reset must not notify subscribers")
	default:
	}
}

func TestInsertLiveEventsRejectsEventsFromBeforeReset(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	insertTestUsageEvents(t, store, testUsageEvent(0, false, 10))
	reset, err := store.ResetUsageStatistics(ctx)
	if err != nil {
		t.Fatalf("ResetUsageStatistics() error = %v", err)
	}

	stale := testUsageEvent(1, false, 20)
	result, err := store.InsertLiveEvents(ctx, []internalusage.Event{stale})
	if err != nil {
		t.Fatalf("InsertLiveEvents(stale) error = %v", err)
	}
	if result.Inserted != 0 || result.Skipped != 1 {
		t.Fatalf("InsertLiveEvents(stale) = %+v, want skipped", result)
	}

	fresh := testUsageEvent(2, false, 30)
	fresh.TimestampMS = reset.ResetAtMS + 1
	fresh.Timestamp = time.UnixMilli(fresh.TimestampMS).UTC().Format(time.RFC3339Nano)
	result, err = store.InsertLiveEvents(ctx, []internalusage.Event{fresh})
	if err != nil {
		t.Fatalf("InsertLiveEvents(fresh) error = %v", err)
	}
	if result.Inserted != 1 || result.Skipped != 0 {
		t.Fatalf("InsertLiveEvents(fresh) = %+v, want inserted", result)
	}
	events, _, err := store.Counts(ctx)
	if err != nil || events != 1 {
		t.Fatalf("Counts() = %d, _, %v; want one fresh event", events, err)
	}
}

func TestOpenStoreRebuildsStaleUsageSummary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.sqlite")
	ctx := context.Background()

	store := openTestStoreAt(t, path)
	insertTestUsageEvents(t, store,
		testUsageEvent(0, false, 10),
		testUsageEvent(1, true, 20),
	)
	if _, err := store.db.ExecContext(ctx, `update usage_summary set latest_event_id = 0, total_requests = 0, success_count = 0, failure_count = 0, total_tokens = 0 where id = 1`); err != nil {
		t.Fatalf("corrupt usage_summary error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened := openTestStoreAt(t, path)
	latestID, _, err := reopened.LatestCursor(ctx)
	if err != nil {
		t.Fatalf("LatestCursor() error = %v", err)
	}
	summary, err := reopened.UsageSummary(ctx, latestID)
	if err != nil {
		t.Fatalf("UsageSummary() error = %v", err)
	}
	if summary.TotalRequests != 2 || summary.SuccessCount != 1 || summary.FailureCount != 1 || summary.TotalTokens != 30 {
		t.Fatalf("rebuilt UsageSummary() = %+v, want total=2 success=1 failure=1 tokens=30", summary)
	}
}

func TestRecentEventsUsesRecentIndex(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	insertTestUsageEvents(t, store,
		testUsageEvent(0, false, 10),
		testUsageEvent(1, true, 20),
		testUsageEvent(2, false, 30),
	)

	rows, err := store.db.QueryContext(ctx, `explain query plan select
		id, request_id, event_hash, timestamp_ms, timestamp, provider, executor_type, model, alias, endpoint, method, path,
		auth_type, auth_index, source, source_hash, api_key_hash,
		input_tokens, output_tokens, reasoning_tokens, cached_tokens, cache_tokens, total_tokens,
		latency_ms, ttft_ms, status_code, error_code, error_message, upstream_request_id, retry_after, stream, reasoning_effort, service_tier,
		failed, raw_json, created_at_ms
		from usage_events indexed by idx_usage_events_recent
		order by timestamp_ms desc, id desc
		limit ?`, 2)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN error = %v", err)
	}
	defer rows.Close()

	planLines := []string{}
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatalf("scan query plan error = %v", err)
		}
		planLines = append(planLines, strings.ToLower(detail))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("query plan rows error = %v", err)
	}
	plan := strings.Join(planLines, "\n")
	if !strings.Contains(plan, "idx_usage_events_recent") {
		t.Fatalf("RecentEvents query plan = %q, want idx_usage_events_recent", plan)
	}
	if strings.Contains(plan, "temp b-tree") {
		t.Fatalf("RecentEvents query plan = %q, want no temp b-tree sort", plan)
	}
}

func TestUsageDiagnosticsRoundTripAndAggregates(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	event := testUsageEvent(0, true, 42)
	event.ErrorCode = "rate_limit"
	event.ErrorMessage = "too many requests"
	insertTestUsageEvents(t, store, event)

	recent, err := store.RecentEvents(ctx, 1)
	if err != nil {
		t.Fatalf("RecentEvents() error = %v", err)
	}
	if len(recent) != 1 {
		t.Fatalf("RecentEvents() len = %d, want 1", len(recent))
	}
	got := recent[0]
	if got.TTFTMS == nil || *got.TTFTMS != 20 || got.StatusCode == nil || *got.StatusCode != 429 {
		t.Fatalf("diagnostics = ttft:%v status:%v, want 20/429", got.TTFTMS, got.StatusCode)
	}
	if got.ErrorCode != "rate_limit" || got.ErrorMessage != "too many requests" || got.UpstreamRequestID != "upstream-request" || got.RetryAfter != "30" || !got.Stream || got.ReasoningEffort != "medium" || got.ServiceTier != "default" || got.ExecutorType != "TestExecutor" || got.Alias != "client-model" {
		t.Fatalf("diagnostic strings = %+v", got)
	}

	buckets, err := store.UsageAggregates(ctx, UsageAggregateOptions{Interval: "hour", GroupBy: []string{"provider", "model"}, Limit: 10})
	if err != nil {
		t.Fatalf("UsageAggregates() error = %v", err)
	}
	if len(buckets) != 1 {
		t.Fatalf("UsageAggregates() len = %d, want 1", len(buckets))
	}
	bucket := buckets[0]
	if bucket.Provider != "test" || bucket.Model != "model" || bucket.TotalRequests != 1 || bucket.FailureCount != 1 || bucket.TotalTokens != 42 {
		t.Fatalf("aggregate bucket = %+v, want provider/model failure tokens", bucket)
	}
	if bucket.AvgLatencyMS == nil || *bucket.AvgLatencyMS != 100 || bucket.AvgTTFTMS == nil || *bucket.AvgTTFTMS != 20 {
		t.Fatalf("aggregate latency = %+v/%+v, want 100/20", bucket.AvgLatencyMS, bucket.AvgTTFTMS)
	}
}

func TestUsageAggregatesSupportsAllIntervalAndAPIKeyFilter(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	first := testUsageEvent(0, false, 10)
	first.APIKeyHash = "key-a"
	second := testUsageEvent(1, true, 20)
	second.APIKeyHash = "key-b"
	insertTestUsageEvents(t, store, first, second)

	buckets, err := store.UsageAggregates(ctx, UsageAggregateOptions{
		FromMS:     first.TimestampMS - 1,
		Interval:   "all",
		GroupBy:    []string{"model"},
		APIKeyHash: "key-a",
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("UsageAggregates() error = %v", err)
	}
	if len(buckets) != 1 || buckets[0].TotalRequests != 1 || buckets[0].TotalTokens != 10 {
		t.Fatalf("all interval buckets = %+v, want one filtered request", buckets)
	}
}

func TestUsageAggregatesIncludesUnattributedAPIKeyBucket(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	attributed := testUsageEvent(0, false, 10)
	attributed.APIKeyHash = "key-a"
	unattributed := testUsageEvent(1, false, 20)
	insertTestUsageEvents(t, store, attributed, unattributed)

	buckets, err := store.UsageAggregates(ctx, UsageAggregateOptions{
		Interval: "all",
		GroupBy:  []string{"api_key_hash"},
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("UsageAggregates() error = %v", err)
	}
	if len(buckets) != 2 {
		t.Fatalf("UsageAggregates() len = %d, want attributed and unattributed buckets", len(buckets))
	}
	requestsByHash := make(map[string]int64, len(buckets))
	for _, bucket := range buckets {
		requestsByHash[bucket.APIKeyHash] += bucket.TotalRequests
	}
	if requestsByHash["key-a"] != 1 || requestsByHash[""] != 1 {
		t.Fatalf("requests by API key hash = %#v, want one attributed and one unattributed request", requestsByHash)
	}
}

func TestUsageAggregatesSupportsAuthIndexGroupingAndLastSeen(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	first := testUsageEvent(0, false, 10)
	first.AuthIndex = "auth-a"
	second := testUsageEvent(1, true, 20)
	second.AuthIndex = "auth-a"
	insertTestUsageEvents(t, store, first, second)

	buckets, err := store.UsageAggregates(ctx, UsageAggregateOptions{
		Interval: "all",
		GroupBy:  []string{"auth_index", "provider", "model"},
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("UsageAggregates() error = %v", err)
	}
	if len(buckets) != 1 {
		t.Fatalf("UsageAggregates() len = %d, want 1", len(buckets))
	}
	bucket := buckets[0]
	if bucket.AuthIndex != "auth-a" || bucket.TotalRequests != 2 || bucket.LastSeenAtMS != second.TimestampMS {
		t.Fatalf("aggregate bucket = %+v, want auth-a total=2 last_seen=%d", bucket, second.TimestampMS)
	}
}

func TestRecentDeadLettersLimitsPayload(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	payload := `{"api_key":"sk-secret","message":"` + strings.Repeat("x", 600) + `"}`
	if err := store.AddDeadLetter(ctx, payload, errTestParse); err != nil {
		t.Fatalf("AddDeadLetter() error = %v", err)
	}
	samples, err := store.RecentDeadLetters(ctx, 5)
	if err != nil {
		t.Fatalf("RecentDeadLetters() error = %v", err)
	}
	if len(samples) != 1 || len(samples[0].Payload) != 500 || samples[0].Error == "" {
		t.Fatalf("dead letter samples = %+v, want truncated payload and error", samples)
	}
	if strings.Contains(samples[0].Payload, "sk-secret") || !strings.Contains(samples[0].Payload, "[redacted]") {
		t.Fatalf("dead letter payload was not redacted: %s", samples[0].Payload)
	}
}

func TestQuotaCacheRejectsStaleWritesAndTracksGeneration(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	initial, err := store.QuotaCacheStats(ctx)
	if err != nil {
		t.Fatalf("QuotaCacheStats() error = %v", err)
	}
	newer := QuotaCacheEntry{Provider: "codex", FileName: "a.json", Data: json.RawMessage(`{"status":"success","value":2}`), CachedAt: 200, ObservedAt: 200}
	if err := store.SetQuotaCache(ctx, newer); err != nil {
		t.Fatalf("SetQuotaCache(newer) error = %v", err)
	}
	if err := store.SetQuotaCache(ctx, QuotaCacheEntry{Provider: "codex", FileName: "a.json", Data: json.RawMessage(`{"status":"success","value":1}`), CachedAt: 100, ObservedAt: 100}); err != nil {
		t.Fatalf("SetQuotaCache(stale) error = %v", err)
	}
	entries, err := store.GetQuotaCache(ctx, "codex", "a.json")
	if err != nil || len(entries) != 1 {
		t.Fatalf("GetQuotaCache() = %+v, %v", entries, err)
	}
	if !strings.Contains(string(entries[0].Data), `"value":2`) || entries[0].Revision != 1 {
		t.Fatalf("quota entry = %+v, want newer revision 1", entries[0])
	}
	afterSet, _ := store.QuotaCacheStats(ctx)
	if afterSet.Generation != initial.Generation+1 {
		t.Fatalf("generation after set = %d, want %d", afterSet.Generation, initial.Generation+1)
	}
	if err := store.DeleteQuotaCache(ctx, "codex", "a.json"); err != nil {
		t.Fatalf("DeleteQuotaCache() error = %v", err)
	}
	afterDelete, _ := store.QuotaCacheStats(ctx)
	if afterDelete.Generation != afterSet.Generation+1 {
		t.Fatalf("generation after delete = %d, want %d", afterDelete.Generation, afterSet.Generation+1)
	}
}

func TestRoutingCursorAndAuthRuntimeStatsRoundTrip(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	cursor := RoutingCursorState{CursorKey: "single|codex|gpt-5|0|all", LastAuthID: "auth-b", UpdatedAtMS: 123}
	if err := store.SetRoutingCursorState(ctx, cursor); err != nil {
		t.Fatalf("SetRoutingCursorState() error = %v", err)
	}
	gotCursor, ok, err := store.GetRoutingCursorState(ctx, cursor.CursorKey)
	if err != nil || !ok || gotCursor.LastAuthID != cursor.LastAuthID {
		t.Fatalf("GetRoutingCursorState() = %+v, %v, %v", gotCursor, ok, err)
	}
	stats := AuthRuntimeStats{
		AuthIndex: "idx-a", AuthID: "auth-a", IdentityFingerprint: "fp-a",
		SelectedCount: 7, SuccessCount: 5, FailureCount: 2, UpdatedAtMS: 456,
		RecentBuckets: []RuntimeRequestBucket{{BucketID: 100, Success: 5, Failed: 2}},
	}
	if err := store.SetAuthRuntimeStats(ctx, stats); err != nil {
		t.Fatalf("SetAuthRuntimeStats() error = %v", err)
	}
	gotStats, ok, err := store.GetAuthRuntimeStats(ctx, stats.AuthIndex, stats.AuthID)
	if err != nil || !ok || gotStats.SelectedCount != 7 || len(gotStats.RecentBuckets) != 1 {
		t.Fatalf("GetAuthRuntimeStats() = %+v, %v, %v", gotStats, ok, err)
	}
	exported, err := store.ExportJSONL(ctx)
	if err != nil {
		t.Fatalf("ExportJSONL() error = %v", err)
	}
	if !strings.Contains(string(exported), `"record_type":"routing_cursor_state"`) ||
		!strings.Contains(string(exported), `"record_type":"auth_runtime_stats"`) {
		t.Fatalf("export missing runtime state records: %s", exported)
	}
}

func TestQueuedAuthRuntimeDeleteCannotBeOverwrittenByPendingSnapshot(t *testing.T) {
	store := openTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service := &Service{ctx: ctx, store: store}
	SetDefaultService(service)
	defer stopRuntimeStateWriter(service)

	QueueAuthRuntimeStats(AuthRuntimeStats{
		AuthIndex: "idx-delete", AuthID: "auth-delete", SelectedCount: 3, UpdatedAtMS: time.Now().UnixMilli(),
	})
	if err := DeleteAuthRuntimeState(context.Background(), "auth-delete", "idx-delete", "delete.json"); err != nil {
		t.Fatalf("DeleteAuthRuntimeState() error = %v", err)
	}
	if stats, ok, err := store.GetAuthRuntimeStats(context.Background(), "idx-delete", "auth-delete"); err != nil || ok {
		t.Fatalf("GetAuthRuntimeStats() after delete = %+v, %v, %v; want missing", stats, ok, err)
	}
}

func TestRuntimeStateWriterRetainsSnapshotsAfterWriteFailure(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	if _, err := store.db.ExecContext(ctx, `create trigger fail_auth_runtime_insert before insert on auth_runtime_stats begin select raise(abort, 'forced runtime write failure'); end`); err != nil {
		t.Fatalf("create trigger error = %v", err)
	}
	service := &Service{ctx: ctx, store: store}
	SetDefaultService(service)
	defer stopRuntimeStateWriter(service)

	QueueAuthRuntimeStats(AuthRuntimeStats{
		AuthIndex: "idx-retry", AuthID: "auth-retry", SelectedCount: 7, UpdatedAtMS: 100,
	})
	if err := flushRuntimeStateWrites(ctx, store); err == nil {
		t.Fatal("flushRuntimeStateWrites() error = nil, want forced write failure")
	}
	if _, err := store.db.ExecContext(ctx, `drop trigger fail_auth_runtime_insert`); err != nil {
		t.Fatalf("drop trigger error = %v", err)
	}
	if err := flushRuntimeStateWrites(ctx, store); err != nil {
		t.Fatalf("flushRuntimeStateWrites() retry error = %v", err)
	}
	stats, ok, err := store.GetAuthRuntimeStats(ctx, "idx-retry", "auth-retry")
	if err != nil || !ok || stats.SelectedCount != 7 {
		t.Fatalf("GetAuthRuntimeStats() after retry = %+v, %v, %v", stats, ok, err)
	}
}

func TestRuntimeStateWriterCoalescesOverflowWithoutLosingLatestSnapshot(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	heldTx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	service := &Service{ctx: ctx, store: store}
	SetDefaultService(service)
	defer stopRuntimeStateWriter(service)
	QueueAuthRuntimeStats(AuthRuntimeStats{
		AuthIndex: "idx-overflow", AuthID: "auth-overflow", SelectedCount: 1, UpdatedAtMS: 1,
	})
	time.Sleep(300 * time.Millisecond)
	for index := 2; index <= 2200; index++ {
		QueueAuthRuntimeStats(AuthRuntimeStats{
			AuthIndex: "idx-overflow", AuthID: "auth-overflow", SelectedCount: int64(index), UpdatedAtMS: int64(index),
		})
	}
	if err := heldTx.Commit(); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if err := flushRuntimeStateWrites(ctx, store); err != nil {
		t.Fatalf("flushRuntimeStateWrites() error = %v", err)
	}
	stats, ok, err := store.GetAuthRuntimeStats(ctx, "idx-overflow", "auth-overflow")
	if err != nil || !ok || stats.SelectedCount != 2200 {
		t.Fatalf("GetAuthRuntimeStats() = %+v, %v, %v; want latest overflow snapshot", stats, ok, err)
	}
}

func TestRuntimeStateWriterRemainsAvailableUntilExplicitStop(t *testing.T) {
	store := openTestStore(t)
	serviceCtx, cancelService := context.WithCancel(context.Background())
	service := &Service{ctx: serviceCtx, store: store}
	SetDefaultService(service)
	defer stopRuntimeStateWriter(service)
	cancelService()
	time.Sleep(25 * time.Millisecond)

	QueueAuthRuntimeStats(AuthRuntimeStats{
		AuthIndex: "idx-shutdown", AuthID: "auth-shutdown", SelectedCount: 9, UpdatedAtMS: 900,
	})
	flushCtx, cancelFlush := context.WithTimeout(context.Background(), time.Second)
	defer cancelFlush()
	if err := flushRuntimeStateWrites(flushCtx, store); err != nil {
		t.Fatalf("flushRuntimeStateWrites() after service cancellation error = %v", err)
	}
	stats, ok, err := store.GetAuthRuntimeStats(context.Background(), "idx-shutdown", "auth-shutdown")
	if err != nil || !ok || stats.SelectedCount != 9 {
		t.Fatalf("GetAuthRuntimeStats() = %+v, %v, %v; writer stopped before explicit shutdown", stats, ok, err)
	}
}

func TestFailedRuntimeStateDeleteDoesNotSuppressLaterSnapshot(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	if err := store.SetAuthRuntimeStats(ctx, AuthRuntimeStats{
		AuthIndex: "idx-delete-failure", AuthID: "auth-delete-failure", SelectedCount: 1, UpdatedAtMS: 100,
	}); err != nil {
		t.Fatalf("SetAuthRuntimeStats() error = %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `create trigger fail_auth_runtime_delete before delete on auth_runtime_stats begin select raise(abort, 'forced runtime delete failure'); end`); err != nil {
		t.Fatalf("create trigger error = %v", err)
	}
	service := &Service{ctx: ctx, store: store}
	SetDefaultService(service)
	defer stopRuntimeStateWriter(service)

	if err := DeleteAuthRuntimeState(ctx, "auth-delete-failure", "idx-delete-failure", ""); err == nil {
		t.Fatal("DeleteAuthRuntimeState() error = nil, want forced delete failure")
	}
	if _, err := store.db.ExecContext(ctx, `drop trigger fail_auth_runtime_delete`); err != nil {
		t.Fatalf("drop trigger error = %v", err)
	}
	QueueAuthRuntimeStats(AuthRuntimeStats{
		AuthIndex: "idx-delete-failure", AuthID: "auth-delete-failure", SelectedCount: 9, UpdatedAtMS: 200,
	})
	if err := flushRuntimeStateWrites(ctx, store); err != nil {
		t.Fatalf("flushRuntimeStateWrites() error = %v", err)
	}
	stats, ok, err := store.GetAuthRuntimeStats(ctx, "idx-delete-failure", "auth-delete-failure")
	if err != nil || !ok || stats.SelectedCount != 9 {
		t.Fatalf("GetAuthRuntimeStats() = %+v, %v, %v; want later snapshot", stats, ok, err)
	}
}

func TestServerExportFlushesQueuedRuntimeState(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	service := &Service{ctx: ctx, store: store}
	SetDefaultService(service)
	defer stopRuntimeStateWriter(service)
	QueueRoutingCursorState(RoutingCursorState{
		CursorKey: "single|codex|gpt-5|0|all", LastAuthID: "auth-b", UpdatedAtMS: 123,
	})
	QueueAuthRuntimeStats(AuthRuntimeStats{
		AuthIndex: "idx-a", AuthID: "auth-a", SelectedCount: 7, SuccessCount: 5, FailureCount: 2, UpdatedAtMS: 456,
	})

	server := NewServer(Config{}, store)
	exported, err := server.exportJSONL(ctx)
	if err != nil {
		t.Fatalf("exportJSONL() error = %v", err)
	}
	if !strings.Contains(string(exported), `"lastAuthId":"auth-b"`) ||
		!strings.Contains(string(exported), `"selectedCount":7`) {
		t.Fatalf("export missing queued runtime state: %s", exported)
	}
}

func TestRuntimeStateImportUsesExplicitRestoreSemantics(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	if err := store.SetRoutingCursorState(ctx, RoutingCursorState{
		CursorKey: "single|codex|gpt-5|0|all", LastAuthID: "current-auth", UpdatedAtMS: 500,
	}); err != nil {
		t.Fatalf("SetRoutingCursorState() error = %v", err)
	}
	if err := store.SetAuthRuntimeStats(ctx, AuthRuntimeStats{
		AuthIndex: "idx-a", AuthID: "auth-a", SelectedCount: 1, SuccessCount: 1, UpdatedAtMS: 500,
	}); err != nil {
		t.Fatalf("SetAuthRuntimeStats() error = %v", err)
	}
	if imported, err := store.ImportRoutingCursorStates(ctx, []RoutingCursorState{{
		CursorKey: "single|codex|gpt-5|0|all", LastAuthID: "backup-auth", UpdatedAtMS: 100,
	}}); err != nil || imported != 1 {
		t.Fatalf("ImportRoutingCursorStates() = %d, %v", imported, err)
	}
	if imported, err := store.ImportAuthRuntimeStats(ctx, []AuthRuntimeStats{{
		AuthIndex: "idx-a", AuthID: "auth-a", SelectedCount: 9, SuccessCount: 7, FailureCount: 2, UpdatedAtMS: 100,
	}}); err != nil || imported != 1 {
		t.Fatalf("ImportAuthRuntimeStats() = %d, %v", imported, err)
	}
	cursor, ok, err := store.GetRoutingCursorState(ctx, "single|codex|gpt-5|0|all")
	if err != nil || !ok || cursor.LastAuthID != "backup-auth" {
		t.Fatalf("restored cursor = %+v, %v, %v", cursor, ok, err)
	}
	stats, ok, err := store.GetAuthRuntimeStats(ctx, "idx-a", "auth-a")
	if err != nil || !ok || stats.SelectedCount != 9 || stats.SuccessCount != 7 || stats.FailureCount != 2 {
		t.Fatalf("restored stats = %+v, %v, %v", stats, ok, err)
	}
}

func TestRuntimeStateImportRollsBackCursorWhenStatsFail(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	if _, err := store.db.ExecContext(ctx, `create trigger fail_auth_runtime_import before insert on auth_runtime_stats begin select raise(abort, 'forced runtime import failure'); end`); err != nil {
		t.Fatalf("create trigger error = %v", err)
	}
	_, _, err := store.ImportRuntimeState(ctx,
		[]RoutingCursorState{{CursorKey: "single|codex|gpt-5|0|all", LastAuthID: "backup-auth", UpdatedAtMS: 100}},
		[]AuthRuntimeStats{{AuthIndex: "idx-import", AuthID: "auth-import", SelectedCount: 2, UpdatedAtMS: 100}},
	)
	if err == nil {
		t.Fatal("ImportRuntimeState() error = nil, want forced stats failure")
	}
	if _, ok, err := store.GetRoutingCursorState(ctx, "single|codex|gpt-5|0|all"); err != nil || ok {
		t.Fatalf("GetRoutingCursorState() after rollback = _, %v, %v; want missing", ok, err)
	}
}
