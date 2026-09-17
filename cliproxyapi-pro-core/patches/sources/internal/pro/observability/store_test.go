package observability

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	probackup "github.com/router-for-me/CLIProxyAPI/v6/internal/pro/backup"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/pro/observability/internalusage"
)

var errTestParse = errors.New("parse failed")

func TestUpdateMonitoringSettingsMergesSectionsAndDetectsConflicts(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	base := normalizeMonitoringSettings(MonitoringSettings{
		RetentionDays:  30,
		WebDAV:         MonitoringWebDAVBackupConfig{URL: "https://example.test/old", IntervalMinutes: 60},
		ModelPriceSync: ModelPriceSyncConfig{Enabled: false, IntervalMinutes: 1440},
	})
	if err := store.SetMonitoringSettings(ctx, base); err != nil {
		t.Fatal(err)
	}
	webDAVPatch := base
	webDAVPatch.WebDAV.URL = "https://example.test/new"
	if _, err := store.UpdateMonitoringSettings(ctx, webDAVPatch, &base, []string{"webdav"}); err != nil {
		t.Fatal(err)
	}
	pricePatch := base
	pricePatch.ModelPriceSync.Enabled = true
	merged, err := store.UpdateMonitoringSettings(ctx, pricePatch, &base, []string{"modelPriceSync"})
	if err != nil {
		t.Fatalf("disjoint stale section update failed: %v", err)
	}
	if merged.WebDAV.URL != webDAVPatch.WebDAV.URL || !merged.ModelPriceSync.Enabled {
		t.Fatalf("merged settings = %+v", merged)
	}
	conflicting := base
	conflicting.WebDAV.Username = "stale-writer"
	if _, err := store.UpdateMonitoringSettings(ctx, conflicting, &base, []string{"webdav"}); !errors.Is(err, ErrMonitoringSettingsConflict) {
		t.Fatalf("same-section conflict error = %v", err)
	}
}

func testUsageEvent(index int, failed bool, totalTokens int64) internalusage.Event {
	timestamp := time.Unix(1_700_000_000+int64(index), 0).UTC()
	latency := int64(100 + index)
	ttft := int64(20 + index)
	status := 200
	if failed {
		status = 429
	}
	return internalusage.Event{
		RequestID:            "request-" + string(rune('a'+index)),
		EventHash:            "event-hash-" + string(rune('a'+index)),
		TimestampMS:          timestamp.UnixMilli(),
		Timestamp:            timestamp.Format(time.RFC3339Nano),
		Provider:             "test",
		ExecutorType:         "TestExecutor",
		Model:                "model",
		Alias:                "client-model",
		Endpoint:             "POST /v1/test",
		Method:               "POST",
		Path:                 "/v1/test",
		APIKeyPolicyID:       "policy-a",
		ProfileID:            "profile-a",
		ProfileNameSnapshot:  "Production",
		PolicyMode:           "profile",
		RequestedModel:       "smart",
		EffectiveModel:       "model",
		ClientIP:             "192.0.2.10",
		XForwardedFor:        "203.0.113.5, 198.51.100.8",
		UserAgent:            "test-client/1.0",
		TotalTokens:          totalTokens,
		InputTokens:          totalTokens / 2,
		OutputTokens:         totalTokens - totalTokens/2,
		LatencyMS:            &latency,
		TTFTMS:               &ttft,
		StatusCode:           &status,
		UpstreamRequestID:    "upstream-request",
		RetryAfter:           "30",
		Stream:               index%2 == 0,
		ReasoningEffort:      "medium",
		ServiceTier:          "fast",
		EffectiveServiceTier: "default",
		Failed:               failed,
		CreatedAtMS:          timestamp.UnixMilli(),
	}
}

func TestUsagePolicyAttributionRoundTripAndFilters(t *testing.T) {
	store := openTestStore(t)
	profile := testUsageEvent(0, false, 10)
	passthrough := testUsageEvent(1, false, 20)
	passthrough.APIKeyPolicyID = ""
	passthrough.ProfileID = ""
	passthrough.ProfileNameSnapshot = ""
	passthrough.PolicyMode = "passthrough"
	passthrough.RequestedModel = "model"
	passthrough.EffectiveModel = "model"
	insertTestUsageEvents(t, store, profile, passthrough)

	page, err := store.QueryEvents(context.Background(), UsageEventQueryOptions{APIKeyPolicyID: "policy-a", ProfileID: "profile-a", PolicyMode: "profile", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 || page.Events[0].ProfileNameSnapshot != "Production" || page.Events[0].RequestedModel != "smart" || page.Events[0].EffectiveModel != "model" {
		t.Fatalf("profile events = %#v", page.Events)
	}
	passthroughPage, err := store.QueryEvents(context.Background(), UsageEventQueryOptions{PolicyMode: "passthrough", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(passthroughPage.Events) != 1 || passthroughPage.Events[0].ProfileID != "" {
		t.Fatalf("passthrough events = %#v", passthroughPage.Events)
	}
	buckets, err := store.UsageAggregates(context.Background(), UsageAggregateOptions{Interval: "all", GroupBy: []string{"api_key_policy_id", "profile_id", "policy_mode"}, APIKeyPolicyID: "policy-a", ProfileID: "profile-a", PolicyMode: "profile"})
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 1 || buckets[0].APIKeyPolicyID != "policy-a" || buckets[0].ProfileID != "profile-a" || buckets[0].PolicyMode != "profile" {
		t.Fatalf("policy buckets = %#v", buckets)
	}
}

func TestUsageUnknownPolicyModeMatchesOnlyPreMigrationRows(t *testing.T) {
	store := openTestStore(t)
	legacy := testUsageEvent(0, false, 10)
	legacy.PolicyMode = ""
	legacy.APIKeyPolicyID = ""
	legacy.ProfileID = ""
	profile := testUsageEvent(1, false, 20)
	insertTestUsageEvents(t, store, legacy, profile)

	page, err := store.QueryEvents(context.Background(), UsageEventQueryOptions{PolicyMode: "unknown", Limit: 10})
	if err != nil || len(page.Events) != 1 || page.Events[0].RequestID != legacy.RequestID {
		t.Fatalf("unknown events = %#v, %v", page.Events, err)
	}
	buckets, err := store.UsageAggregates(context.Background(), UsageAggregateOptions{Interval: "all", GroupBy: []string{"policy_mode"}, PolicyMode: "unknown", Limit: 10})
	if err != nil || len(buckets) != 1 || buckets[0].TotalRequests != 1 || buckets[0].PolicyMode != "" {
		t.Fatalf("unknown buckets = %#v, %v", buckets, err)
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

func TestResetUsageStatisticsClearsUsageAndAuthRuntimeStats(t *testing.T) {
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
	if err := store.SetRoutingCursorState(ctx, RoutingCursorState{
		CursorKey: "single|codex|gpt-5|0|all", LastAuthID: "auth-reset", UpdatedAtMS: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatalf("SetRoutingCursorState() error = %v", err)
	}
	if err := store.SetAuthRuntimeStats(ctx, AuthRuntimeStats{
		AuthIndex: "idx-reset", AuthID: "auth-reset", FileName: "reset.json",
		SelectedCount: 9, SuccessCount: 7, FailureCount: 2, UpdatedAtMS: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatalf("SetAuthRuntimeStats() error = %v", err)
	}
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
	if result.DeletedAuthRuntimeStats != 1 {
		t.Fatalf("deleted auth runtime stats = %d, want 1", result.DeletedAuthRuntimeStats)
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
	if stats, err := store.ListAuthRuntimeStats(ctx); err != nil || len(stats) != 0 {
		t.Fatalf("auth runtime stats after reset = %+v err:%v, want empty", stats, err)
	}
	cursors, err := store.ListRoutingCursorStates(ctx)
	if err != nil || len(cursors) != 1 || cursors[0].LastAuthID != "auth-reset" {
		t.Fatalf("routing cursors after reset = %+v err:%v, want preserved auth-reset cursor", cursors, err)
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

func TestResetUsageStatisticsClearsAuthStatsWithoutUsageEvents(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	if err := store.SetAuthRuntimeStats(ctx, AuthRuntimeStats{
		AuthIndex: "idx-only", AuthID: "auth-only", SelectedCount: 3, UpdatedAtMS: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatalf("SetAuthRuntimeStats() error = %v", err)
	}
	stateBefore, err := store.UsageDatasetState(ctx)
	if err != nil {
		t.Fatalf("UsageDatasetState() error = %v", err)
	}
	result, err := store.ResetUsageStatistics(ctx)
	if err != nil {
		t.Fatalf("ResetUsageStatistics() error = %v", err)
	}
	if result.DeletedEvents != 0 || result.DeletedAuthRuntimeStats != 1 || result.Generation != stateBefore.Generation+1 {
		t.Fatalf("reset result = %+v, want auth-only reset and advanced generation", result)
	}
	if stats, err := store.ListAuthRuntimeStats(ctx); err != nil || len(stats) != 0 {
		t.Fatalf("auth runtime stats after reset = %+v err:%v, want empty", stats, err)
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
	if got.ErrorCode != "rate_limit" || got.ErrorMessage != "too many requests" || got.UpstreamRequestID != "upstream-request" || got.RetryAfter != "30" || !got.Stream || got.ReasoningEffort != "medium" || got.ServiceTier != "fast" || got.EffectiveServiceTier != "default" || got.ExecutorType != "TestExecutor" || got.Alias != "client-model" {
		t.Fatalf("diagnostic strings = %+v", got)
	}
	if got.ClientIP != "192.0.2.10" || got.XForwardedFor != "203.0.113.5, 198.51.100.8" || got.UserAgent != "test-client/1.0" {
		t.Fatalf("client metadata = %q/%q/%q", got.ClientIP, got.XForwardedFor, got.UserAgent)
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

func TestClientRequestMetadataSearchAndExport(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	event := testUsageEvent(0, false, 1)
	insertTestUsageEvents(t, store, event)

	for _, search := range []string{event.ClientIP, "198.51.100.8", "test-client/1.0"} {
		page, err := store.QueryEvents(ctx, UsageEventQueryOptions{Search: search, Limit: 10})
		if err != nil {
			t.Fatalf("QueryEvents(%q) error = %v", search, err)
		}
		if len(page.Events) != 1 || page.Events[0].EventHash != event.EventHash {
			t.Fatalf("QueryEvents(%q) = %+v", search, page.Events)
		}
	}

	exported, err := store.ExportJSONL(ctx)
	if err != nil {
		t.Fatalf("ExportJSONL() error = %v", err)
	}
	for _, value := range []string{`"client_ip":"192.0.2.10"`, `"x_forwarded_for":"203.0.113.5, 198.51.100.8"`, `"user_agent":"test-client/1.0"`} {
		if !strings.Contains(string(exported), value) {
			t.Fatalf("export missing %s: %s", value, exported)
		}
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

func TestUsageAggregatesUseCanonicalCacheReadAcrossProviderSemantics(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	openAI := testUsageEvent(0, false, 130)
	openAI.Provider = "codex"
	openAI.ExecutorType = "CodexExecutor"
	openAI.InputTokens = 100
	openAI.OutputTokens = 30
	openAI.CachedTokens = 40
	openAI.CacheReadTokens = 40
	openAI.CacheWriteTokens = 10
	claude := testUsageEvent(1, false, 55)
	claude.Provider = "claude"
	claude.ExecutorType = "ClaudeExecutor"
	claude.InputTokens = 30
	claude.OutputTokens = 5
	claude.CachedTokens = 7
	claude.CacheReadTokens = 7
	claude.CacheWriteTokens = 13
	insertTestUsageEvents(t, store, openAI, claude)

	buckets, err := store.UsageAggregates(ctx, UsageAggregateOptions{Interval: "all", Limit: 10})
	if err != nil || len(buckets) != 1 {
		t.Fatalf("UsageAggregates() = %+v, %v", buckets, err)
	}
	bucket := buckets[0]
	if bucket.InputTokens != 150 || bucket.CacheInputTokens != 150 || bucket.CacheReadTokens != 47 ||
		bucket.CacheWriteTokens != 23 || bucket.CacheTokens != 47 {
		t.Fatalf("canonical aggregate = %+v, want input/cache-input/read/write/cache 150/150/47/23/47", bucket)
	}
	if bucket.CacheReadTokens > bucket.CacheInputTokens {
		t.Fatalf("cache hit ratio exceeds 100%%: %+v", bucket)
	}
}

func TestOpenStoreMigratesLegacyTokenAccountingFromRawPayload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-usage.sqlite")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatalf("OpenStore() error = %v", err)
	}
	raw := `{
		"timestamp":"2026-07-26T00:00:00Z","provider":"claude","executor_type":"ClaudeExecutor","model":"claude-test",
		"tokens":{"input_tokens":30,"output_tokens":5,"cached_tokens":7,"cache_read_tokens":7,"cache_creation_tokens":13,"total_tokens":55},
		"accounting_version":2,
		"token_breakdown":{"schema_version":2,"quality":"complete","total_tokens":55,"input":{"total_tokens":50,"uncached_tokens":30,"cache_read_tokens":7,"cache_write_tokens":13},"output":{"total_tokens":5,"non_reasoning_tokens":5,"reasoning_tokens":0},"unclassified_tokens":0}
	}`
	if _, err := store.db.Exec(`insert into usage_events(
		event_hash, timestamp_ms, timestamp, provider, executor_type, model,
		input_tokens, output_tokens, cached_tokens, cache_tokens, cache_read_tokens, cache_write_tokens, total_tokens,
		raw_json, created_at_ms
	) values(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"legacy-accounting-event", int64(1_785_024_000_000), "2026-07-26T00:00:00Z", "claude", "ClaudeExecutor", "claude-test",
		int64(30), int64(5), int64(7), int64(20), int64(7), int64(13), int64(55), raw, int64(1_785_024_000_000),
	); err != nil {
		t.Fatalf("insert legacy event: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	migrated, err := OpenStore(path)
	if err != nil {
		t.Fatalf("OpenStore(migrate) error = %v", err)
	}
	defer func() { _ = migrated.Close() }()
	events, err := migrated.RecentEvents(context.Background(), 1)
	if err != nil || len(events) != 1 {
		t.Fatalf("RecentEvents() = %+v, %v", events, err)
	}
	event := events[0]
	if event.AccountingVersion != 2 || event.AccountingQuality != "complete" || event.InputTokens != 50 ||
		event.UncachedInputTokens != 30 || event.CacheReadTokens != 7 || event.CacheWriteTokens != 13 || event.CachedTokens != 7 {
		t.Fatalf("migrated event = %+v", event)
	}
}

func TestOpenStoreMigratesAPIKeyPolicyColumnsBeforeIndexes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v7.2.130-usage.sqlite")
	legacy, err := OpenStore(path)
	if err != nil {
		t.Fatalf("OpenStore(seed) error = %v", err)
	}
	for _, statement := range []string{
		`drop index idx_usage_events_policy_recent`,
		`drop index idx_usage_events_profile_recent`,
		`drop index idx_usage_events_policy_mode_recent`,
		`alter table usage_events drop column api_key_policy_id`,
		`alter table usage_events drop column profile_id`,
		`alter table usage_events drop column profile_name_snapshot`,
		`alter table usage_events drop column policy_mode`,
		`alter table usage_events drop column requested_model`,
		`alter table usage_events drop column effective_model`,
	} {
		if _, err = legacy.db.Exec(statement); err != nil {
			t.Fatalf("prepare legacy schema with %q: %v", statement, err)
		}
	}
	if err = legacy.Close(); err != nil {
		t.Fatalf("Close(legacy) error = %v", err)
	}

	migrated, err := OpenStore(path)
	if err != nil {
		t.Fatalf("OpenStore(migrate v7.2.130 schema) error = %v", err)
	}
	defer func() { _ = migrated.Close() }()

	columns := map[string]bool{}
	rows, err := migrated.db.Query(`pragma table_info(usage_events)`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, dataType string
		var defaultValue any
		if err = rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		columns[name] = true
	}
	if err = rows.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"api_key_policy_id", "profile_id", "profile_name_snapshot", "policy_mode", "requested_model", "effective_model"} {
		if !columns[name] {
			t.Errorf("usage_events column %q was not migrated", name)
		}
	}
	for _, name := range []string{"idx_usage_events_policy_recent", "idx_usage_events_profile_recent", "idx_usage_events_policy_mode_recent"} {
		var count int
		if err = migrated.db.QueryRow(`select count(*) from sqlite_master where type = 'index' and name = ?`, name).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Errorf("usage_events index %q count = %d, want 1", name, count)
		}
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

func TestUsageAggregatesUsesIANATimezoneAcrossDST(t *testing.T) {
	store := openTestStore(t)
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("LoadLocation() error = %v", err)
	}
	eventTimes := []time.Time{
		time.Date(2026, 3, 8, 0, 30, 0, 0, location),
		time.Date(2026, 3, 9, 0, 30, 0, 0, location),
	}
	events := make([]internalusage.Event, 0, len(eventTimes))
	for index, eventTime := range eventTimes {
		event := testUsageEvent(index, false, 10)
		event.EventHash = fmt.Sprintf("dst-aggregate-%d", index)
		event.TimestampMS = eventTime.UnixMilli()
		event.Timestamp = eventTime.UTC().Format(time.RFC3339Nano)
		event.CreatedAtMS = event.TimestampMS
		events = append(events, event)
	}
	insertTestUsageEvents(t, store, events...)

	buckets, err := store.UsageAggregates(context.Background(), UsageAggregateOptions{
		FromMS:       eventTimes[0].Add(-time.Minute).UnixMilli(),
		ToMS:         eventTimes[1].Add(time.Minute).UnixMilli(),
		Interval:     "day",
		Limit:        10,
		TimezoneName: "America/New_York",
	})
	if err != nil {
		t.Fatalf("UsageAggregates() error = %v", err)
	}
	if len(buckets) != 2 {
		t.Fatalf("UsageAggregates() len = %d, want 2", len(buckets))
	}
	firstStart := time.Date(2026, 3, 8, 0, 0, 0, 0, location).UnixMilli()
	secondStart := time.Date(2026, 3, 9, 0, 0, 0, 0, location).UnixMilli()
	if buckets[0].BucketStartMS != firstStart || buckets[1].BucketStartMS != secondStart {
		t.Fatalf("DST bucket starts = %d, %d; want %d, %d", buckets[0].BucketStartMS, buckets[1].BucketStartMS, firstStart, secondStart)
	}
	if buckets[1].BucketStartMS-buckets[0].BucketStartMS != int64(23*time.Hour/time.Millisecond) {
		t.Fatalf("DST bucket distance = %d, want 23 hours", buckets[1].BucketStartMS-buckets[0].BucketStartMS)
	}
}

func TestAccountUsageAggregatesExactAuthIndexAndQualityMetrics(t *testing.T) {
	store := openTestStore(t)
	shanghai := time.FixedZone("Asia/Shanghai", 8*60*60)
	now := time.Date(2026, 7, 26, 15, 0, 0, 0, shanghai)
	todayStart := time.Date(2026, 7, 26, 0, 0, 0, 0, shanghai)
	attempt0 := int64(0)
	attempt1 := int64(1)
	makeEvent := func(index int, at time.Time, model, apiKey string, failed bool, cost float64, attempt *int64) internalusage.Event {
		event := testUsageEvent(index, failed, int64((index+1)*100))
		event.EventHash = fmt.Sprintf("account-event-%d", index)
		event.TimestampMS = at.UnixMilli()
		event.Timestamp = at.UTC().Format(time.RFC3339Nano)
		event.CreatedAtMS = at.UnixMilli()
		event.AuthIndex = "codex:account-a"
		event.Model = model
		event.APIKeyHash = apiKey
		event.EstimatedCost = &cost
		event.AttemptIndex = attempt
		event.CacheTokens = int64(index * 10)
		latency := int64((index + 1) * 1000)
		ttft := int64((index + 1) * 100)
		event.LatencyMS = &latency
		event.TTFTMS = &ttft
		return event
	}
	events := []internalusage.Event{
		makeEvent(0, todayStart.Add(time.Hour), "gpt-5", "key-a", false, 1.25, &attempt0),
		makeEvent(1, todayStart.AddDate(0, 0, -1).Add(time.Hour), "gpt-5", "key-a", true, 2.50, &attempt1),
		makeEvent(2, todayStart.AddDate(0, 0, -1).Add(2*time.Hour), "gpt-4.1", "", false, 3.75, nil),
		makeEvent(3, todayStart.AddDate(0, 0, -40), "old", "key-a", false, 9.99, &attempt0),
	}
	other := makeEvent(4, todayStart.Add(2*time.Hour), "other", "key-b", false, 8.88, &attempt0)
	other.AuthIndex = "codex:account-b"
	events = append(events, other)
	insertTestUsageEvents(t, store, events...)

	detail, err := store.AccountUsage(context.Background(), AccountUsageOptions{
		AuthIndex:             "codex:account-a",
		Days:                  30,
		TimezoneOffsetMinutes: 480,
		NowMS:                 now.UnixMilli(),
	})
	if err != nil {
		t.Fatalf("AccountUsage() error = %v", err)
	}
	if detail.TotalRequests != 3 || detail.SuccessCount != 2 || detail.FailureCount != 1 {
		t.Fatalf("request summary = %+v", detail)
	}
	if detail.ActiveDays != 2 || detail.Today.Requests != 1 || len(detail.History) != 2 {
		t.Fatalf("day summary = active:%d today:%+v history:%+v", detail.ActiveDays, detail.Today, detail.History)
	}
	if detail.EstimatedCost != 7.5 || detail.PricedRequests != 3 {
		t.Fatalf("cost summary = %.2f/%d", detail.EstimatedCost, detail.PricedRequests)
	}
	if detail.RetryAttempts != 1 || detail.RetrySamples != 2 {
		t.Fatalf("retry summary = %d/%d", detail.RetryAttempts, detail.RetrySamples)
	}
	if detail.AverageLatencyMS == nil || *detail.AverageLatencyMS != 2000 || detail.P95LatencyMS == nil || *detail.P95LatencyMS != 3000 {
		t.Fatalf("latency summary = avg:%v p95:%v", detail.AverageLatencyMS, detail.P95LatencyMS)
	}
	if detail.AverageTTFTMS == nil || *detail.AverageTTFTMS != 200 {
		t.Fatalf("TTFT average = %v", detail.AverageTTFTMS)
	}
	if len(detail.Models) != 2 || detail.Models[0].Model != "gpt-5" || detail.Models[0].Requests != 2 {
		t.Fatalf("models = %+v", detail.Models)
	}
	if len(detail.APIKeys) != 2 || detail.APIKeys[0].APIKeyHash != "key-a" || detail.APIKeys[0].Requests != 2 {
		t.Fatalf("api keys = %+v", detail.APIKeys)
	}
	if detail.HighestCostDay == nil || detail.HighestCostDay.EstimatedCost != 6.25 || detail.HighestRequestDay == nil || detail.HighestRequestDay.Requests != 2 {
		t.Fatalf("highlights = cost:%+v requests:%+v", detail.HighestCostDay, detail.HighestRequestDay)
	}

	todayDetail, err := store.AccountUsage(context.Background(), AccountUsageOptions{
		AuthIndex:             "codex:account-a",
		Days:                  1,
		TimezoneOffsetMinutes: 480,
		NowMS:                 now.UnixMilli(),
	})
	if err != nil {
		t.Fatalf("AccountUsage(today) error = %v", err)
	}
	if todayDetail.TotalRequests != 1 || todayDetail.Today.Requests != 1 || todayDetail.PeriodDays != 1 {
		t.Fatalf("today detail = requests:%d today:%+v days:%d", todayDetail.TotalRequests, todayDetail.Today, todayDetail.PeriodDays)
	}
}

func TestAccountUsageCustomRangeUsesExactMillisecondBounds(t *testing.T) {
	store := openTestStore(t)
	base := time.Date(2026, 8, 31, 8, 0, 0, 0, time.UTC).UnixMilli()
	events := make([]internalusage.Event, 0, 3)
	for index, timestampMS := range []int64{base + 999, base + 1000, base + 2000} {
		event := testUsageEvent(index, false, int64(index+1))
		event.EventHash = fmt.Sprintf("custom-range-event-%d", index)
		event.AuthIndex = "codex:custom-range"
		event.TimestampMS = timestampMS
		event.Timestamp = time.UnixMilli(timestampMS).UTC().Format(time.RFC3339Nano)
		event.CreatedAtMS = timestampMS
		events = append(events, event)
	}
	insertTestUsageEvents(t, store, events...)

	detail, err := store.AccountUsage(context.Background(), AccountUsageOptions{
		AuthIndex: "codex:custom-range",
		FromMS:    base + 1000,
		ToMS:      base + 1999,
		NowMS:     base + 5000,
	})
	if err != nil {
		t.Fatalf("AccountUsage() error = %v", err)
	}
	if detail.TotalRequests != 1 || detail.TotalTokens != 2 {
		t.Fatalf("custom range totals = requests:%d tokens:%d", detail.TotalRequests, detail.TotalTokens)
	}
	if detail.FromMS != base+1000 || detail.ToMS != base+1999 || detail.PeriodDays != 1 {
		t.Fatalf("custom range metadata = from:%d to:%d days:%d", detail.FromMS, detail.ToMS, detail.PeriodDays)
	}
}

func TestAccountUsageTodayUsesIANATimezoneAcrossDST(t *testing.T) {
	store := openTestStore(t)
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("LoadLocation() error = %v", err)
	}
	previousDay := time.Date(2026, 3, 7, 23, 30, 0, 0, location)
	today := time.Date(2026, 3, 8, 0, 30, 0, 0, location)
	events := make([]internalusage.Event, 0, 2)
	for index, eventTime := range []time.Time{previousDay, today} {
		event := testUsageEvent(index, false, 10)
		event.EventHash = fmt.Sprintf("dst-account-%d", index)
		event.AuthIndex = "codex:dst-account"
		event.TimestampMS = eventTime.UnixMilli()
		event.Timestamp = eventTime.UTC().Format(time.RFC3339Nano)
		event.CreatedAtMS = event.TimestampMS
		events = append(events, event)
	}
	insertTestUsageEvents(t, store, events...)

	detail, err := store.AccountUsage(context.Background(), AccountUsageOptions{
		AuthIndex:             "codex:dst-account",
		Days:                  1,
		TimezoneOffsetMinutes: -4 * 60,
		TimezoneName:          "America/New_York",
		NowMS:                 time.Date(2026, 3, 8, 12, 0, 0, 0, location).UnixMilli(),
	})
	if err != nil {
		t.Fatalf("AccountUsage() error = %v", err)
	}
	wantStart := time.Date(2026, 3, 8, 0, 0, 0, 0, location).UnixMilli()
	if detail.FromMS != wantStart || detail.TotalRequests != 1 || detail.Today.Requests != 1 {
		t.Fatalf("DST today detail = from:%d total:%d today:%+v; want from:%d total=1", detail.FromMS, detail.TotalRequests, detail.Today, wantStart)
	}

	customDetail, err := store.AccountUsage(context.Background(), AccountUsageOptions{
		AuthIndex:             "codex:dst-account",
		FromMS:                time.Date(2026, 3, 7, 0, 0, 0, 0, location).UnixMilli(),
		ToMS:                  time.Date(2026, 3, 9, 23, 59, 59, 999_000_000, location).UnixMilli(),
		TimezoneOffsetMinutes: -4 * 60,
		TimezoneName:          "America/New_York",
	})
	if err != nil {
		t.Fatalf("AccountUsage(custom DST range) error = %v", err)
	}
	if customDetail.PeriodDays != 3 {
		t.Fatalf("custom DST period days = %d, want 3", customDetail.PeriodDays)
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
	var storedPayload string
	if err := store.db.QueryRowContext(ctx, `select payload from dead_letter_events limit 1`).Scan(&storedPayload); err != nil {
		t.Fatalf("query stored dead letter error = %v", err)
	}
	if strings.Contains(storedPayload, "sk-secret") || !strings.Contains(storedPayload, "[redacted]") {
		t.Fatalf("dead letter was not redacted before storage: %s", storedPayload)
	}
}

func TestAddDeadLetterScrubsInvalidPayloadAndErrorBeforeStorage(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	if err := store.AddDeadLetter(
		ctx,
		"invalid payload x-api-key=plain-secret-value",
		errors.New("decode failed with Bearer abcdefghijklmnop"),
	); err != nil {
		t.Fatalf("AddDeadLetter() error = %v", err)
	}
	var payload, errorMessage string
	if err := store.db.QueryRowContext(ctx, `select payload, error from dead_letter_events limit 1`).Scan(&payload, &errorMessage); err != nil {
		t.Fatalf("query stored dead letter error = %v", err)
	}
	for _, secret := range []string{"plain-secret-value", "abcdefghijklmnop"} {
		if strings.Contains(payload, secret) || strings.Contains(errorMessage, secret) {
			t.Fatalf("dead letter secret %q reached storage: payload=%q error=%q", secret, payload, errorMessage)
		}
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

func TestUsageExportSnapshotKeepsAdjacentWritesOutOfEveryRecordType(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	initial := defaultMonitoringSettings()
	initial.RetentionDays = 7
	if err := store.SetMonitoringSettings(ctx, initial); err != nil {
		t.Fatalf("SetMonitoringSettings(initial) error = %v", err)
	}

	eventsRead := make(chan struct{})
	continueExport := make(chan struct{})
	snapshotResult := make(chan usageExportSnapshot, 1)
	snapshotError := make(chan error, 1)
	go func() {
		snapshot, err := store.readUsageExportSnapshot(ctx, func() {
			close(eventsRead)
			<-continueExport
		})
		snapshotResult <- snapshot
		snapshotError <- err
	}()
	<-eventsRead

	updated := initial
	updated.RetentionDays = 30
	writeStarted := make(chan struct{})
	writeDone := make(chan error, 1)
	go func() {
		close(writeStarted)
		writeDone <- store.SetMonitoringSettings(ctx, updated)
	}()
	<-writeStarted
	select {
	case err := <-writeDone:
		t.Fatalf("adjacent write completed inside export transaction: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(continueExport)

	snapshot := <-snapshotResult
	if err := <-snapshotError; err != nil {
		t.Fatalf("readUsageExportSnapshot() error = %v", err)
	}
	if snapshot.Settings.RetentionDays != initial.RetentionDays {
		t.Fatalf("snapshot retention = %d, want %d", snapshot.Settings.RetentionDays, initial.RetentionDays)
	}
	if err := <-writeDone; err != nil {
		t.Fatalf("SetMonitoringSettings(updated) error = %v", err)
	}
	current, err := store.GetMonitoringSettings(ctx)
	if err != nil || current.RetentionDays != updated.RetentionDays {
		t.Fatalf("current settings = %+v, %v; want updated retention", current, err)
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

func TestDeleteQuotaCacheKeepsAuthRuntimeStatsAndRoutingCursor(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	service := &Service{ctx: ctx, store: store}
	SetDefaultService(service)
	defer stopRuntimeStateWriter(service)

	stats := AuthRuntimeStats{
		AuthIndex: "idx-relogin", AuthID: "auth-relogin", SelectedCount: 4, UpdatedAtMS: 100,
	}
	if err := store.SetAuthRuntimeStats(ctx, stats); err != nil {
		t.Fatalf("SetAuthRuntimeStats() error = %v", err)
	}
	cursor := RoutingCursorState{CursorKey: "single|codex", LastAuthID: stats.AuthID, UpdatedAtMS: 100}
	if err := store.SetRoutingCursorState(ctx, cursor); err != nil {
		t.Fatalf("SetRoutingCursorState() error = %v", err)
	}
	if err := store.SetQuotaCache(ctx, QuotaCacheEntry{
		Provider: "codex", FileName: "account.json", Data: json.RawMessage(`{"plan":"free"}`), CachedAt: 100, ObservedAt: 100,
	}); err != nil {
		t.Fatalf("SetQuotaCache() error = %v", err)
	}

	if err := DeleteQuotaCache(ctx, "codex", "account.json"); err != nil {
		t.Fatalf("DeleteQuotaCache() error = %v", err)
	}
	if entries, err := store.GetQuotaCache(ctx, "codex", "account.json"); err != nil || len(entries) != 0 {
		t.Fatalf("GetQuotaCache() = %+v, %v; want empty", entries, err)
	}
	if got, ok, err := store.GetAuthRuntimeStats(ctx, stats.AuthIndex, stats.AuthID); err != nil || !ok || got.SelectedCount != stats.SelectedCount {
		t.Fatalf("GetAuthRuntimeStats() = %+v, %v, %v", got, ok, err)
	}
	if got, ok, err := store.GetRoutingCursorState(ctx, cursor.CursorKey); err != nil || !ok || got.LastAuthID != cursor.LastAuthID {
		t.Fatalf("GetRoutingCursorState() = %+v, %v, %v", got, ok, err)
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

	server := NewServer(Config{Enabled: true}, store)
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

func TestProSettingsRoundTripAndExport(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	want := ProSetting{
		Namespace:     ProSettingNamespaceRoutingRequestProtection,
		SchemaVersion: 1,
		Settings:      json.RawMessage(`{"enabled":true,"mode":"observe"}`),
		UpdatedAtMS:   123,
	}
	if err := store.SetProSetting(ctx, want); err != nil {
		t.Fatalf("SetProSetting() error = %v", err)
	}
	got, ok, err := store.GetProSetting(ctx, want.Namespace)
	if err != nil || !ok {
		t.Fatalf("GetProSetting() = %+v, %v, %v", got, ok, err)
	}
	if got.SchemaVersion != want.SchemaVersion || string(got.Settings) != string(want.Settings) || got.UpdatedAtMS != want.UpdatedAtMS {
		t.Fatalf("GetProSetting() = %+v, want %+v", got, want)
	}
	exported, err := store.ExportJSONL(ctx)
	if err != nil {
		t.Fatalf("ExportJSONL() error = %v", err)
	}
	if !strings.Contains(string(exported), `"record_type":"pro_settings"`) ||
		!strings.Contains(string(exported), `"namespace":"routing.request-protection"`) {
		t.Fatalf("export missing pro settings: %s", exported)
	}
}

func TestImportProSettingsIsAtomic(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	items := []ProSetting{
		{Namespace: "valid", SchemaVersion: 1, Settings: json.RawMessage(`{"value":1}`)},
		{Namespace: "invalid", SchemaVersion: 0, Settings: json.RawMessage(`{"value":2}`)},
	}
	if _, err := store.ImportProSettings(ctx, items); err == nil {
		t.Fatal("ImportProSettings() error = nil, want validation failure")
	}
	if _, ok, err := store.GetProSetting(ctx, "valid"); err != nil || ok {
		t.Fatalf("GetProSetting(valid) after failed import = _, %v, %v; want missing", ok, err)
	}
}

func TestRunImportTransactionRollsBackAllDomains(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	wantErr := errors.New("forced late import failure")
	err := store.RunImportTransaction(ctx, func(importCtx context.Context) error {
		if _, err := store.InsertEvents(importCtx, []internalusage.Event{testUsageEvent(0, false, 10)}); err != nil {
			return err
		}
		if _, _, err := store.ImportRuntimeState(importCtx,
			[]RoutingCursorState{{CursorKey: "single|codex", LastAuthID: "backup-auth", UpdatedAtMS: 100}}, nil,
		); err != nil {
			return err
		}
		if _, err := store.ImportProSettings(importCtx, []ProSetting{{
			Namespace: "test.atomic", SchemaVersion: 1, Settings: json.RawMessage(`{"enabled":true}`),
		}}); err != nil {
			return err
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("RunImportTransaction() error = %v, want %v", err, wantErr)
	}
	events, _, err := store.Counts(ctx)
	if err != nil || events != 0 {
		t.Fatalf("Counts() after rollback = %d, _, %v; want zero events", events, err)
	}
	if _, ok, err := store.GetRoutingCursorState(ctx, "single|codex"); err != nil || ok {
		t.Fatalf("routing cursor after rollback = _, %v, %v; want missing", ok, err)
	}
	if _, ok, err := store.GetProSetting(ctx, "test.atomic"); err != nil || ok {
		t.Fatalf("pro setting after rollback = _, %v, %v; want missing", ok, err)
	}
}

func TestSetProSettingAndApplyLatestBlocksBackupImportUntilRuntimeApplied(t *testing.T) {
	store := openTestStore(t)
	service := &Service{ctx: context.Background(), store: store}
	SetDefaultService(service)
	t.Cleanup(func() { SetDefaultService(nil) })

	liveApplied := make(chan struct{})
	releaseLiveApply := make(chan struct{})
	liveDone := make(chan error, 1)
	go func() {
		liveDone <- SetProSettingAndApplyLatest(context.Background(), ProSetting{
			Namespace: "test.live-apply", SchemaVersion: 1, Settings: json.RawMessage(`{"value":"live"}`),
		}, func(_ context.Context, item ProSetting) error {
			if string(item.Settings) != `{"value":"live"}` {
				return fmt.Errorf("applied setting = %s", item.Settings)
			}
			close(liveApplied)
			<-releaseLiveApply
			return nil
		})
	}()
	<-liveApplied

	importStarted := make(chan struct{})
	importDone := make(chan error, 1)
	go func() {
		importDone <- probackup.Default.ExecuteImport(context.Background(), probackup.ImportPlan{
			ImportDatabase: func(context.Context) error {
				close(importStarted)
				return nil
			},
		})
	}()
	select {
	case <-importStarted:
		t.Fatal("backup import entered while live runtime apply still held the write barrier")
	case err := <-importDone:
		t.Fatalf("backup import completed while live runtime apply was blocked: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(releaseLiveApply)
	if err := <-liveDone; err != nil {
		t.Fatal(err)
	}
	select {
	case <-importStarted:
	case <-time.After(time.Second):
		t.Fatal("backup import did not start after live runtime apply completed")
	}
	if err := <-importDone; err != nil {
		t.Fatal(err)
	}
}
