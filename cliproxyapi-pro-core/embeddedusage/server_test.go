package embeddedusage

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/embeddedusage/internalusage"
)

type failingImportReader struct{}

func (failingImportReader) Read([]byte) (int, error) {
	return 0, errors.New("forced import read failure")
}

func TestUsageStreamPushesInsertedEventsWithoutPollingDelay(t *testing.T) {
	store := openTestStore(t)
	server := httptest.NewServer(testUsageRouter(store))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/usage/stream", nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext() error = %v", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("stream request error = %v", err)
	}
	defer response.Body.Close()

	payloads := make(chan internalusage.Payload, 1)
	go func() {
		scanner := bufio.NewScanner(response.Body)
		isUsageEvent := false
		for scanner.Scan() {
			line := scanner.Text()
			if line == "event: usage" {
				isUsageEvent = true
				continue
			}
			if isUsageEvent && strings.HasPrefix(line, "data: ") {
				var payload internalusage.Payload
				if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &payload) == nil {
					payloads <- payload
				}
				return
			}
		}
	}()

	insertTestUsageEvents(t, store, testUsageEvent(0, false, 10))
	select {
	case payload := <-payloads:
		if payload.LatestID != 1 || payload.TotalRequests != 1 {
			t.Fatalf("stream payload = %+v, want inserted event", payload)
		}
	case <-ctx.Done():
		t.Fatal("stream did not push inserted event before polling interval")
	}
}

func testUsageRouter(store *Store) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	server := NewServer(Config{QueryLimit: 50000, BatchSize: 100}, store)
	group := router.Group("/usage")
	server.RegisterGinRoutes(group)
	return router
}

func decodeUsagePayload(t *testing.T, recorder *httptest.ResponseRecorder) internalusage.Payload {
	t.Helper()
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var payload internalusage.Payload
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; body=%s", err, recorder.Body.String())
	}
	return payload
}

func TestHandleUsageResetRequiresConfirmation(t *testing.T) {
	store := openTestStore(t)
	router := testUsageRouter(store)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/usage/reset", strings.NewReader(`{"confirm":false}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestUsageImportDoesNotWriteBeforeRequestIsFullyRead(t *testing.T) {
	store := openTestStore(t)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	server := NewServer(Config{QueryLimit: 50000, BatchSize: 1}, store)
	server.RegisterGinRoutes(router.Group("/usage"))
	event, err := json.Marshal(testUsageEvent(0, false, 10))
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	body := io.MultiReader(bytes.NewReader(append(event, '\n')), failingImportReader{})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/usage/import", body)
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
	events, _, err := store.Counts(context.Background())
	if err != nil || events != 0 {
		t.Fatalf("Counts() = %d, _, %v; import must not partially write", events, err)
	}
}

func TestUsageImportDoesNotTreatUnknownRecordAsEvent(t *testing.T) {
	store := openTestStore(t)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/usage/import", strings.NewReader(`{"record_type":"future_record","model":"must-not-import"}`))
	testUsageRouter(store).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 legacy-compatible response; body=%s", recorder.Code, recorder.Body.String())
	}
	var result struct {
		Failed int `json:"failed"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil || result.Failed != 1 {
		t.Fatalf("import result = %+v, %v; want one failed record", result, err)
	}
	events, _, err := store.Counts(context.Background())
	if err != nil || events != 0 {
		t.Fatalf("Counts() = %d, _, %v; unknown record must not become an event", events, err)
	}
}

func TestUsageImportRejectsTamperedManifestBackupBeforeWriting(t *testing.T) {
	sourceStore := openTestStore(t)
	insertTestUsageEvents(t, sourceStore, testUsageEvent(0, false, 10))
	exportRecorder := httptest.NewRecorder()
	testUsageRouter(sourceStore).ServeHTTP(exportRecorder, httptest.NewRequest(http.MethodGet, "/usage/export", nil))
	if exportRecorder.Code != http.StatusOK {
		t.Fatalf("export status = %d; body=%s", exportRecorder.Code, exportRecorder.Body.String())
	}
	if !bytes.HasPrefix(exportRecorder.Body.Bytes(), []byte(`{"record_type":"backup_manifest"`)) {
		t.Fatalf("export is missing backup manifest: %s", exportRecorder.Body.String())
	}
	tampered := bytes.Replace(exportRecorder.Body.Bytes(), []byte(`"model":"model"`), []byte(`"model":"tampered"`), 1)
	if bytes.Equal(tampered, exportRecorder.Body.Bytes()) {
		t.Fatal("test backup event was not changed")
	}

	targetStore := openTestStore(t)
	importRecorder := httptest.NewRecorder()
	testUsageRouter(targetStore).ServeHTTP(importRecorder, httptest.NewRequest(http.MethodPost, "/usage/import", bytes.NewReader(tampered)))
	if importRecorder.Code != http.StatusBadRequest {
		t.Fatalf("import status = %d, want 400; body=%s", importRecorder.Code, importRecorder.Body.String())
	}
	events, _, err := targetStore.Counts(context.Background())
	if err != nil || events != 0 {
		t.Fatalf("Counts() = %d, _, %v; tampered backup must not write", events, err)
	}
}

func TestHandleUsageResetClearsStatisticsAndReturnsGeneration(t *testing.T) {
	store := openTestStore(t)
	insertTestUsageEvents(t, store,
		testUsageEvent(0, false, 10),
		testUsageEvent(1, true, 20),
	)
	router := testUsageRouter(store)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/usage/reset", strings.NewReader(`{"confirm":true}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var result UsageResetResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if result.DeletedEvents != 2 || result.Generation <= 1 || result.ResetAtMS <= 0 {
		t.Fatalf("reset result = %+v, want two deleted events and advanced state", result)
	}

	usageRecorder := httptest.NewRecorder()
	usageRequest := httptest.NewRequest(http.MethodGet, "/usage", nil)
	router.ServeHTTP(usageRecorder, usageRequest)
	payload := decodeUsagePayload(t, usageRecorder)
	if payload.TotalRequests != 0 || payload.LatestID != 0 || payload.Generation != result.Generation || payload.ResetAtMS != result.ResetAtMS {
		t.Fatalf("usage after reset = %+v, want empty generation %d", payload, result.Generation)
	}
}

func TestUsageStreamEmitsResetForStaleGeneration(t *testing.T) {
	store := openTestStore(t)
	insertTestUsageEvents(t, store, testUsageEvent(0, false, 10))
	result, err := store.ResetUsageStatistics(context.Background())
	if err != nil {
		t.Fatalf("ResetUsageStatistics() error = %v", err)
	}
	router := testUsageRouter(store)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/usage/stream?after_id=1&generation=1", nil)
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "event: reset\n") || strings.Contains(body, "event: ready\n") {
		t.Fatalf("stream body = %q, want reset event only", body)
	}
	var payload internalusage.Payload
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "data: ") {
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &payload); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			break
		}
	}
	if payload.Generation != result.Generation || payload.ResetAtMS != result.ResetAtMS {
		t.Fatalf("reset stream payload = %+v, want generation %d", payload, result.Generation)
	}
}

func usageDetailIDs(payload internalusage.Payload) []int64 {
	ids := make([]int64, 0, payload.DetailsCount)
	for _, api := range payload.APIs {
		for _, model := range api.Models {
			for _, detail := range model.Details {
				ids = append(ids, detail.ID)
			}
		}
	}
	return ids
}

func TestHandleUsageReturnsFullSummaryWithLimitedDetails(t *testing.T) {
	store := openTestStore(t)
	insertTestUsageEvents(t, store,
		testUsageEvent(0, false, 10),
		testUsageEvent(1, true, 20),
		testUsageEvent(2, false, 30),
	)
	router := testUsageRouter(store)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/usage?limit=1", nil)
	router.ServeHTTP(recorder, request)
	payload := decodeUsagePayload(t, recorder)

	if payload.TotalRequests != 3 || payload.SuccessCount != 2 || payload.FailureCount != 1 || payload.TotalTokens != 60 {
		t.Fatalf("summary = %+v, want total=3 success=2 failure=1 tokens=60", payload)
	}
	if payload.DetailsCount != 1 || payload.DetailsLimit != 1 || !payload.DetailsLimited {
		t.Fatalf("detail metadata = count:%d limit:%d limited:%v, want 1/1/true", payload.DetailsCount, payload.DetailsLimit, payload.DetailsLimited)
	}
}

func TestHandleUsageEventsDetailsLimitedTracksRemainingRows(t *testing.T) {
	store := openTestStore(t)
	insertTestUsageEvents(t, store,
		testUsageEvent(0, false, 10),
		testUsageEvent(1, true, 20),
		testUsageEvent(2, false, 30),
	)
	router := testUsageRouter(store)

	firstRecorder := httptest.NewRecorder()
	firstRequest := httptest.NewRequest(http.MethodGet, "/usage/events?after_id=0&limit=2", nil)
	router.ServeHTTP(firstRecorder, firstRequest)
	firstPayload := decodeUsagePayload(t, firstRecorder)

	if firstPayload.DetailsCount != 2 || firstPayload.DetailsLimit != 2 || !firstPayload.DetailsLimited {
		t.Fatalf("first page detail metadata = count:%d limit:%d limited:%v, want 2/2/true", firstPayload.DetailsCount, firstPayload.DetailsLimit, firstPayload.DetailsLimited)
	}

	secondRecorder := httptest.NewRecorder()
	secondRequest := httptest.NewRequest(http.MethodGet, "/usage/events?after_id=2&limit=2", nil)
	router.ServeHTTP(secondRecorder, secondRequest)
	secondPayload := decodeUsagePayload(t, secondRecorder)

	if secondPayload.DetailsCount != 1 || secondPayload.DetailsLimit != 2 || secondPayload.DetailsLimited {
		t.Fatalf("second page detail metadata = count:%d limit:%d limited:%v, want 1/2/false", secondPayload.DetailsCount, secondPayload.DetailsLimit, secondPayload.DetailsLimited)
	}
}

func TestHandleUsageEventsDoesNotMarkExactFinalPageLimited(t *testing.T) {
	store := openTestStore(t)
	insertTestUsageEvents(t, store,
		testUsageEvent(0, false, 10),
		testUsageEvent(1, true, 20),
	)
	router := testUsageRouter(store)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/usage/events?after_id=0&limit=2", nil)
	router.ServeHTTP(recorder, request)
	payload := decodeUsagePayload(t, recorder)

	if payload.DetailsCount != 2 || payload.DetailsLimit != 2 || payload.DetailsLimited {
		t.Fatalf("detail metadata = count:%d limit:%d limited:%v, want 2/2/false", payload.DetailsCount, payload.DetailsLimit, payload.DetailsLimited)
	}
	if payload.LatestID != 2 {
		t.Fatalf("latest_id = %d, want 2", payload.LatestID)
	}
}

func TestHandleUsageHistoryEventsUsesStableCursorSnapshot(t *testing.T) {
	store := openTestStore(t)
	insertTestUsageEvents(t, store,
		testUsageEvent(0, false, 10),
		testUsageEvent(1, true, 20),
		testUsageEvent(2, false, 30),
		testUsageEvent(3, false, 40),
		testUsageEvent(4, true, 50),
	)
	router := testUsageRouter(store)

	firstRecorder := httptest.NewRecorder()
	firstRequest := httptest.NewRequest(http.MethodGet, "/usage/events?direction=before&limit=2", nil)
	router.ServeHTTP(firstRecorder, firstRequest)
	firstPayload := decodeUsagePayload(t, firstRecorder)
	if got := usageDetailIDs(firstPayload); len(got) != 2 || got[0] != 5 || got[1] != 4 {
		t.Fatalf("first page ids = %v, want [5 4]", got)
	}
	if firstPayload.MatchedTotal != 5 || firstPayload.SnapshotMaxID != 5 || firstPayload.PageCursor == "" || !firstPayload.HasMore || firstPayload.NextCursor == "" {
		t.Fatalf("first page metadata = %+v, want matched=5 snapshot=5 more cursor", firstPayload)
	}

	insertTestUsageEvents(t, store, testUsageEvent(5, false, 60))
	secondRecorder := httptest.NewRecorder()
	secondRequest := httptest.NewRequest(http.MethodGet, "/usage/events?cursor="+firstPayload.NextCursor+"&limit=2", nil)
	router.ServeHTTP(secondRecorder, secondRequest)
	secondPayload := decodeUsagePayload(t, secondRecorder)
	if got := usageDetailIDs(secondPayload); len(got) != 2 || got[0] != 3 || got[1] != 2 {
		t.Fatalf("second page ids = %v, want [3 2]", got)
	}
	if secondPayload.MatchedTotal != 5 || secondPayload.SnapshotMaxID != 5 || !secondPayload.HasMore || secondPayload.NextCursor == "" {
		t.Fatalf("second page metadata = %+v, want original snapshot metadata", secondPayload)
	}

	returnRecorder := httptest.NewRecorder()
	returnRequest := httptest.NewRequest(http.MethodGet, "/usage/events?cursor="+firstPayload.PageCursor+"&limit=2", nil)
	router.ServeHTTP(returnRecorder, returnRequest)
	returnPayload := decodeUsagePayload(t, returnRecorder)
	if got := usageDetailIDs(returnPayload); len(got) != 2 || got[0] != 5 || got[1] != 4 {
		t.Fatalf("returned first page ids = %v, want stable [5 4]", got)
	}
	if returnPayload.MatchedTotal != 5 || returnPayload.SnapshotMaxID != 5 {
		t.Fatalf("returned first page metadata = %+v, want original snapshot", returnPayload)
	}

	thirdRecorder := httptest.NewRecorder()
	thirdRequest := httptest.NewRequest(http.MethodGet, "/usage/events?cursor="+secondPayload.NextCursor+"&limit=2", nil)
	router.ServeHTTP(thirdRecorder, thirdRequest)
	thirdPayload := decodeUsagePayload(t, thirdRecorder)
	if got := usageDetailIDs(thirdPayload); len(got) != 1 || got[0] != 1 {
		t.Fatalf("third page ids = %v, want [1]", got)
	}
	if thirdPayload.MatchedTotal != 5 || thirdPayload.SnapshotMaxID != 5 || thirdPayload.HasMore || thirdPayload.NextCursor != "" {
		t.Fatalf("third page metadata = %+v, want final original snapshot page", thirdPayload)
	}
}

func TestHandleUsageHistoryEventsSupportsStructuredFilters(t *testing.T) {
	store := openTestStore(t)
	first := testUsageEvent(0, false, 10)
	first.Provider = "alpha"
	first.Model = "model-a"
	first.APIKeyHash = "key-a"
	first.RequestID = "needle-request"
	second := testUsageEvent(1, true, 20)
	second.Provider = "alpha"
	second.Model = "model-a"
	second.APIKeyHash = "key-a"
	second.ErrorMessage = "needle failure"
	third := testUsageEvent(2, true, 30)
	third.Provider = "beta"
	third.Model = "model-b"
	third.APIKeyHash = "key-b"
	insertTestUsageEvents(t, store, first, second, third)
	router := testUsageRouter(store)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/usage/events?direction=before&limit=10&provider=alpha&model=model-a&api_key_hash=key-a&status=failed&search=needle", nil)
	router.ServeHTTP(recorder, request)
	payload := decodeUsagePayload(t, recorder)
	if got := usageDetailIDs(payload); len(got) != 1 || got[0] != 2 {
		t.Fatalf("filtered ids = %v, want [2]", got)
	}
	if payload.MatchedTotal != 1 || payload.HasMore {
		t.Fatalf("filtered metadata = %+v, want matched=1 final page", payload)
	}
}

func TestHandleUsageHistoryEventsSupportsAuthIndexFilter(t *testing.T) {
	store := openTestStore(t)
	first := testUsageEvent(0, false, 10)
	first.AuthIndex = "auth-a"
	second := testUsageEvent(1, false, 20)
	second.AuthIndex = "auth-b"
	insertTestUsageEvents(t, store, first, second)
	router := testUsageRouter(store)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/usage/events?direction=before&limit=10&auth_index=auth-a,auth-c", nil)
	router.ServeHTTP(recorder, request)
	payload := decodeUsagePayload(t, recorder)
	if got := usageDetailIDs(payload); len(got) != 1 || got[0] != 1 {
		t.Fatalf("auth filtered ids = %v, want [1]", got)
	}
}

func TestHandleUsageHistoryEventsRejectsInvalidCursor(t *testing.T) {
	store := openTestStore(t)
	router := testUsageRouter(store)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/usage/events?cursor=not-a-cursor", nil)
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestHandleUsageEventsMaxLimitUsesSentinel(t *testing.T) {
	store := openTestStore(t)
	events := make([]internalusage.Event, usageEventsPageLimit+1)
	for index := range events {
		events[index] = testUsageEvent(index, index%2 == 0, int64(index+1))
	}
	insertTestUsageEvents(t, store, events...)
	router := testUsageRouter(store)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/usage/events?after_id=0&limit=5000", nil)
	router.ServeHTTP(recorder, request)
	payload := decodeUsagePayload(t, recorder)

	if payload.DetailsCount != int64(usageEventsPageLimit) || payload.DetailsLimit != int64(usageEventsPageLimit) || !payload.DetailsLimited {
		t.Fatalf("detail metadata = count:%d limit:%d limited:%v, want %d/%d/true", payload.DetailsCount, payload.DetailsLimit, payload.DetailsLimited, usageEventsPageLimit, usageEventsPageLimit)
	}
	if payload.LatestID != int64(usageEventsPageLimit) {
		t.Fatalf("latest_id = %d, want %d", payload.LatestID, usageEventsPageLimit)
	}
}

func TestLoadUsageEventPageUsesSentinelForStreamBatches(t *testing.T) {
	store := openTestStore(t)
	insertTestUsageEvents(t, store,
		testUsageEvent(0, false, 10),
		testUsageEvent(1, false, 20),
		testUsageEvent(2, false, 30),
	)
	server := NewServer(Config{QueryLimit: 50000, BatchSize: 2}, store)

	events, limit, detailsLimited, err := server.loadUsageEventPage(context.Background(), 0, server.cfg.BatchSize)
	if err != nil {
		t.Fatalf("loadUsageEventPage() error = %v", err)
	}

	if len(events) != 2 || limit != 2 || !detailsLimited {
		t.Fatalf("page = len:%d limit:%d limited:%v, want 2/2/true", len(events), limit, detailsLimited)
	}
	payload := usagePayloadWithDetailLimit(events, limit, detailsLimited)
	if payload.DetailsCount != 2 || payload.DetailsLimit != 2 || !payload.DetailsLimited || payload.LatestID != 2 {
		t.Fatalf("payload = %+v, want details 2/2/true latest_id=2", payload)
	}
}

func TestHandleUsageAggregatesReturnsBuckets(t *testing.T) {
	store := openTestStore(t)
	insertTestUsageEvents(t, store,
		testUsageEvent(0, false, 10),
		testUsageEvent(1, true, 20),
	)
	router := testUsageRouter(store)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/usage/aggregates?interval=hour&group_by=provider,model&limit=10", nil)
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Items        []UsageAggregateBucket `json:"items"`
		LatestID     int64                  `json:"latest_id"`
		SnapshotAtMS int64                  `json:"snapshot_at_ms"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(payload.Items) != 1 {
		t.Fatalf("aggregate items len = %d, want 1", len(payload.Items))
	}
	if payload.LatestID != 2 || payload.SnapshotAtMS <= 0 {
		t.Fatalf("aggregate metadata = latest:%d snapshot:%d, want latest=2 and timestamp", payload.LatestID, payload.SnapshotAtMS)
	}
	item := payload.Items[0]
	if item.Provider != "test" || item.Model != "model" || item.TotalRequests != 2 || item.FailureCount != 1 || item.TotalTokens != 30 {
		t.Fatalf("aggregate item = %+v, want totals by provider/model", item)
	}
}

func TestUsagePayloadDetailsIncludeEventID(t *testing.T) {
	store := openTestStore(t)
	insertTestUsageEvents(t, store, testUsageEvent(0, false, 10))
	router := testUsageRouter(store)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/usage/events?after_id=0&limit=1", nil)
	router.ServeHTTP(recorder, request)
	payload := decodeUsagePayload(t, recorder)

	for _, api := range payload.APIs {
		for _, model := range api.Models {
			if len(model.Details) != 1 || model.Details[0].ID != 1 {
				t.Fatalf("details = %+v, want event id 1", model.Details)
			}
			return
		}
	}
	t.Fatal("usage payload did not contain details")
}

func TestUsageExportImportPreservesAntigravitySubscriptionQuotaCache(t *testing.T) {
	sourceStore := openTestStore(t)
	sourceRouter := testUsageRouter(sourceStore)
	quotaState := map[string]any{
		"status":        "success",
		"schemaVersion": float64(2),
		"parserVersion": float64(3),
		"plan":          "ultra",
		"planType":      "ultra",
		"subscription": map[string]any{
			"plan":     "ultra",
			"tierId":   "g1-ultra-tier",
			"tierName": "Ultra",
			"availableCredits": []any{
				map[string]any{"creditType": "AI", "creditAmount": float64(20)},
			},
		},
		"groups": []any{
			map[string]any{
				"id": "claude-gpt",
				"buckets": []any{
					map[string]any{"id": "weekly", "remainingFraction": float64(0.5)},
				},
			},
		},
	}
	rawQuotaState, err := json.Marshal(quotaState)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if err := sourceStore.SetQuotaCache(context.Background(), QuotaCacheEntry{
		Provider:   "antigravity",
		FileName:   "antigravity-user.json",
		Data:       rawQuotaState,
		CachedAt:   1,
		AccessedAt: 1,
		Version:    1,
	}); err != nil {
		t.Fatalf("SetQuotaCache() error = %v", err)
	}

	exportRecorder := httptest.NewRecorder()
	exportRequest := httptest.NewRequest(http.MethodGet, "/usage/export", nil)
	sourceRouter.ServeHTTP(exportRecorder, exportRequest)
	if exportRecorder.Code != http.StatusOK {
		t.Fatalf("export status = %d, want 200; body=%s", exportRecorder.Code, exportRecorder.Body.String())
	}

	targetStore := openTestStore(t)
	targetRouter := testUsageRouter(targetStore)
	importRecorder := httptest.NewRecorder()
	importRequest := httptest.NewRequest(http.MethodPost, "/usage/import", bytes.NewReader(exportRecorder.Body.Bytes()))
	targetRouter.ServeHTTP(importRecorder, importRequest)
	if importRecorder.Code != http.StatusOK {
		t.Fatalf("import status = %d, want 200; body=%s", importRecorder.Code, importRecorder.Body.String())
	}

	entries, err := targetStore.GetQuotaCache(context.Background(), "antigravity", "antigravity-user.json")
	if err != nil {
		t.Fatalf("GetQuotaCache() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("quota cache entries len = %d, want 1", len(entries))
	}
	var restored map[string]any
	if err := json.Unmarshal(entries[0].Data, &restored); err != nil {
		t.Fatalf("json.Unmarshal(restored) error = %v", err)
	}
	subscription, ok := restored["subscription"].(map[string]any)
	if !ok {
		t.Fatalf("subscription = %#v, want object", restored["subscription"])
	}
	if restored["planType"] != "ultra" || subscription["plan"] != "ultra" || subscription["tierId"] != "g1-ultra-tier" {
		t.Fatalf("restored quota = %#v, want antigravity ultra subscription preserved", restored)
	}
}

func TestUsageExportImportPreservesRoutingCursorAndAuthRuntimeStats(t *testing.T) {
	defer SetAuthRuntimeStateImportHandler(nil)
	var appliedCursors []RoutingCursorState
	var appliedStats []AuthRuntimeStats
	SetAuthRuntimeStateImportHandler(func(cursors []RoutingCursorState, stats []AuthRuntimeStats) error {
		appliedCursors = append([]RoutingCursorState(nil), cursors...)
		appliedStats = append([]AuthRuntimeStats(nil), stats...)
		return nil
	})
	sourceStore := openTestStore(t)
	ctx := context.Background()
	if err := sourceStore.SetRoutingCursorState(ctx, RoutingCursorState{
		CursorKey: "single|codex|gpt-5|0|all", LastAuthID: "auth-b", UpdatedAtMS: 100,
	}); err != nil {
		t.Fatalf("SetRoutingCursorState() error = %v", err)
	}
	if err := sourceStore.SetAuthRuntimeStats(ctx, AuthRuntimeStats{
		AuthIndex: "idx-a", AuthID: "auth-a", SelectedCount: 9, SuccessCount: 7, FailureCount: 2,
		RecentBuckets: []RuntimeRequestBucket{{BucketID: 123, Success: 7, Failed: 2}}, UpdatedAtMS: 200,
	}); err != nil {
		t.Fatalf("SetAuthRuntimeStats() error = %v", err)
	}

	exportRecorder := httptest.NewRecorder()
	testUsageRouter(sourceStore).ServeHTTP(exportRecorder, httptest.NewRequest(http.MethodGet, "/usage/export", nil))
	if exportRecorder.Code != http.StatusOK {
		t.Fatalf("export status = %d; body=%s", exportRecorder.Code, exportRecorder.Body.String())
	}

	targetStore := openTestStore(t)
	importRecorder := httptest.NewRecorder()
	testUsageRouter(targetStore).ServeHTTP(importRecorder, httptest.NewRequest(http.MethodPost, "/usage/import", bytes.NewReader(exportRecorder.Body.Bytes())))
	if importRecorder.Code != http.StatusOK {
		t.Fatalf("import status = %d; body=%s", importRecorder.Code, importRecorder.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(importRecorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal(import response) error = %v", err)
	}
	if response["routingCursors"] != float64(1) || response["authRuntimeStats"] != float64(1) {
		t.Fatalf("import response = %+v", response)
	}
	if len(appliedCursors) != 1 || appliedCursors[0].LastAuthID != "auth-b" || len(appliedStats) != 1 || appliedStats[0].SelectedCount != 9 {
		t.Fatalf("runtime import callback cursors=%+v stats=%+v", appliedCursors, appliedStats)
	}
	cursor, ok, err := targetStore.GetRoutingCursorState(ctx, "single|codex|gpt-5|0|all")
	if err != nil || !ok || cursor.LastAuthID != "auth-b" {
		t.Fatalf("restored cursor = %+v, %v, %v", cursor, ok, err)
	}
	stats, ok, err := targetStore.GetAuthRuntimeStats(ctx, "idx-a", "auth-a")
	if err != nil || !ok || stats.SelectedCount != 9 || stats.SuccessCount != 7 || stats.FailureCount != 2 {
		t.Fatalf("restored stats = %+v, %v, %v", stats, ok, err)
	}
}

func TestUsageImportFlushesQueuedRuntimeStateBeforeExplicitRestore(t *testing.T) {
	sourceStore := openTestStore(t)
	ctx := context.Background()
	if err := sourceStore.SetRoutingCursorState(ctx, RoutingCursorState{
		CursorKey: "single|codex|gpt-5|0|all", LastAuthID: "backup-auth", UpdatedAtMS: 100,
	}); err != nil {
		t.Fatalf("SetRoutingCursorState() error = %v", err)
	}
	if err := sourceStore.SetAuthRuntimeStats(ctx, AuthRuntimeStats{
		AuthIndex: "idx-a", AuthID: "auth-a", SelectedCount: 9, SuccessCount: 7, FailureCount: 2, UpdatedAtMS: 100,
	}); err != nil {
		t.Fatalf("SetAuthRuntimeStats() error = %v", err)
	}
	exported, err := sourceStore.ExportJSONL(ctx)
	if err != nil {
		t.Fatalf("ExportJSONL() error = %v", err)
	}

	targetStore := openTestStore(t)
	service := &Service{ctx: ctx, store: targetStore}
	SetDefaultService(service)
	defer stopRuntimeStateWriter(service)
	QueueRoutingCursorState(RoutingCursorState{
		CursorKey: "single|codex|gpt-5|0|all", LastAuthID: "queued-current-auth", UpdatedAtMS: 500,
	})
	QueueAuthRuntimeStats(AuthRuntimeStats{
		AuthIndex: "idx-a", AuthID: "auth-a", SelectedCount: 1, SuccessCount: 1, UpdatedAtMS: 500,
	})

	router := testUsageRouter(targetStore)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/usage/import", bytes.NewReader(exported))
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if err := flushRuntimeStateWrites(ctx, targetStore); err != nil {
		t.Fatalf("flushRuntimeStateWrites() error = %v", err)
	}
	cursor, ok, err := targetStore.GetRoutingCursorState(ctx, "single|codex|gpt-5|0|all")
	if err != nil || !ok || cursor.LastAuthID != "backup-auth" {
		t.Fatalf("restored cursor = %+v, %v, %v", cursor, ok, err)
	}
	stats, ok, err := targetStore.GetAuthRuntimeStats(ctx, "idx-a", "auth-a")
	if err != nil || !ok || stats.SelectedCount != 9 || stats.SuccessCount != 7 || stats.FailureCount != 2 {
		t.Fatalf("restored stats = %+v, %v, %v", stats, ok, err)
	}
}

func TestUsageExportImportRestoresLatestAccountInspectionSnapshot(t *testing.T) {
	defer SetAccountInspectionSnapshotHandlers(nil, nil)

	sourceSnapshot := json.RawMessage(`{
		"version": 1,
		"state": "completed",
		"lastStartedAt": 1000,
		"lastFinishedAt": 2000,
		"settings": {"targetType": "xai", "workers": 4, "deleteWorkers": 4, "timeout": 15000},
		"summary": {"totalFiles": 1, "probeSetCount": 1, "sampledCount": 1},
		"healthCounts": {"total": 1, "inspectionError": 1},
		"results": [{"key": "xai.json::xai-1", "provider": "xai", "fileName": "xai.json", "authIndex": "xai-1", "action": "keep", "error": "upstream error", "errorDetail": "{\"error\":{\"message\":\"raw upstream response\"}}"}]
	}`)
	SetAccountInspectionSnapshotHandlers(func() ([]byte, bool, error) {
		return sourceSnapshot, true, nil
	}, nil)

	sourceStore := openTestStore(t)
	sourceRouter := testUsageRouter(sourceStore)
	exportRecorder := httptest.NewRecorder()
	exportRequest := httptest.NewRequest(http.MethodGet, "/usage/export", nil)
	sourceRouter.ServeHTTP(exportRecorder, exportRequest)
	if exportRecorder.Code != http.StatusOK {
		t.Fatalf("export status = %d, want 200; body=%s", exportRecorder.Code, exportRecorder.Body.String())
	}
	if !bytes.Contains(exportRecorder.Body.Bytes(), []byte(`"record_type":"account_inspection_snapshot"`)) {
		t.Fatalf("export body does not contain account inspection snapshot: %s", exportRecorder.Body.String())
	}

	var restoredSnapshot json.RawMessage
	SetAccountInspectionSnapshotHandlers(nil, func(raw []byte) error {
		restoredSnapshot = append(restoredSnapshot[:0], raw...)
		return nil
	})
	targetStore := openTestStore(t)
	targetRouter := testUsageRouter(targetStore)
	importRecorder := httptest.NewRecorder()
	importRequest := httptest.NewRequest(http.MethodPost, "/usage/import", bytes.NewReader(exportRecorder.Body.Bytes()))
	targetRouter.ServeHTTP(importRecorder, importRequest)
	if importRecorder.Code != http.StatusOK {
		t.Fatalf("import status = %d, want 200; body=%s", importRecorder.Code, importRecorder.Body.String())
	}
	var importResult struct {
		AccountInspectionSnapshot        bool `json:"accountInspectionSnapshot"`
		AccountInspectionSnapshotRecords int  `json:"accountInspectionSnapshotRecords"`
	}
	if err := json.Unmarshal(importRecorder.Body.Bytes(), &importResult); err != nil {
		t.Fatalf("json.Unmarshal(import result) error = %v", err)
	}
	if !importResult.AccountInspectionSnapshot || importResult.AccountInspectionSnapshotRecords != 1 {
		t.Fatalf("import result = %+v, want restored snapshot with one record", importResult)
	}
	var sourceValue map[string]any
	var restoredValue map[string]any
	if err := json.Unmarshal(sourceSnapshot, &sourceValue); err != nil {
		t.Fatalf("json.Unmarshal(source snapshot) error = %v", err)
	}
	if err := json.Unmarshal(restoredSnapshot, &restoredValue); err != nil {
		t.Fatalf("json.Unmarshal(restored snapshot) error = %v", err)
	}
	if !reflect.DeepEqual(restoredValue, sourceValue) {
		t.Fatalf("restored snapshot = %#v, want %#v", restoredValue, sourceValue)
	}
}

func TestUsageExportImportPreservesUpstreamDiagnostics(t *testing.T) {
	sourceStore := openTestStore(t)
	sourceRouter := testUsageRouter(sourceStore)
	event := testUsageEvent(0, true, 42)
	event.Provider = "antigravity"
	event.ExecutorType = "AntigravityExecutor"
	event.Model = "gemini-claude-opus-4-5-thinking"
	event.Alias = "claude-opus-4-5"
	event.ErrorCode = "rate_limit"
	event.ErrorMessage = "too many requests"
	event.UpstreamRequestID = "upstream-req-1"
	event.RetryAfter = "30"
	event.SourceHash = "source-hash"
	event.APIKeyHash = "api-key-hash"
	insertTestUsageEvents(t, sourceStore, event)

	exportRecorder := httptest.NewRecorder()
	exportRequest := httptest.NewRequest(http.MethodGet, "/usage/export", nil)
	sourceRouter.ServeHTTP(exportRecorder, exportRequest)
	if exportRecorder.Code != http.StatusOK {
		t.Fatalf("export status = %d, want 200; body=%s", exportRecorder.Code, exportRecorder.Body.String())
	}

	targetStore := openTestStore(t)
	targetRouter := testUsageRouter(targetStore)
	importRecorder := httptest.NewRecorder()
	importRequest := httptest.NewRequest(http.MethodPost, "/usage/import", bytes.NewReader(exportRecorder.Body.Bytes()))
	targetRouter.ServeHTTP(importRecorder, importRequest)
	if importRecorder.Code != http.StatusOK {
		t.Fatalf("import status = %d, want 200; body=%s", importRecorder.Code, importRecorder.Body.String())
	}

	recent, err := targetStore.RecentEvents(context.Background(), 1)
	if err != nil {
		t.Fatalf("RecentEvents() error = %v", err)
	}
	if len(recent) != 1 {
		t.Fatalf("RecentEvents() len = %d, want 1", len(recent))
	}
	got := recent[0]
	if got.Provider != "antigravity" || got.ExecutorType != "AntigravityExecutor" || got.Alias != "claude-opus-4-5" {
		t.Fatalf("provider metadata = provider:%q executor:%q alias:%q", got.Provider, got.ExecutorType, got.Alias)
	}
	if got.ErrorCode != "rate_limit" || got.ErrorMessage != "too many requests" || got.UpstreamRequestID != "upstream-req-1" || got.RetryAfter != "30" {
		t.Fatalf("diagnostics = code:%q message:%q rid:%q retry:%q", got.ErrorCode, got.ErrorMessage, got.UpstreamRequestID, got.RetryAfter)
	}
	if got.SourceHash != "source-hash" || got.APIKeyHash != "api-key-hash" {
		t.Fatalf("usage hashes = source:%q api-key:%q, want preserved export values", got.SourceHash, got.APIKeyHash)
	}
}

func TestHandleStatusIncludesDeadLetterSamples(t *testing.T) {
	store := openTestStore(t)
	if err := store.AddDeadLetter(context.Background(), "bad payload", errTestParse); err != nil {
		t.Fatalf("AddDeadLetter() error = %v", err)
	}
	router := testUsageRouter(store)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/usage/status", nil)
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		DeadLetters       int64              `json:"deadLetters"`
		DeadLetterSamples []DeadLetterSample `json:"deadLetterSamples"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if payload.DeadLetters != 1 || len(payload.DeadLetterSamples) != 1 || payload.DeadLetterSamples[0].Error == "" {
		t.Fatalf("status payload = %+v, want dead letter sample", payload)
	}
}
