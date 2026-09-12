package observability

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/pro/apikeypolicy"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/pro/observability/internalusage"
)

func TestSelfQueryScopesHistoryAndStatsAndRedactsRecords(t *testing.T) {
	store := openTestStore(t)
	server := NewServer(Config{Enabled: true}, store)
	identity, _ := apikeypolicy.NewAuthenticatedAPIKeyIdentity("self-a")
	other, _ := apikeypolicy.NewAuthenticatedAPIKeyIdentity("self-b")
	now := time.Now().UnixMilli()
	rows := []internalusage.Event{}
	for i := 0; i < 55; i++ {
		rows = append(rows, internalusage.Event{EventHash: fmt.Sprint("own-", i), TimestampMS: now - int64(i), Timestamp: time.UnixMilli(now - int64(i)).UTC().Format(time.RFC3339Nano), APIKeyHash: identity.Hash(), Model: "model-a", TotalTokens: 2, Source: "PRIVATE_SOURCE", AuthIndex: "PRIVATE_AUTH", ErrorMessage: "PRIVATE_ERROR", ClientIP: "PRIVATE_IP", RawJSON: `{"secret":"PRIVATE_RAW"}`})
	}
	for i, hash := range []string{other.Hash(), ""} {
		rows = append(rows, internalusage.Event{EventHash: fmt.Sprint("foreign-", i), TimestampMS: now, Timestamp: time.UnixMilli(now).UTC().Format(time.RFC3339Nano), APIKeyHash: hash, Model: "PRIVATE_MODEL", TotalTokens: 9000})
	}
	if _, err := store.InsertEvents(context.Background(), rows); err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.GET("/:section", func(c *gin.Context) {
		c.Request = c.Request.WithContext(apikeypolicy.WithIdentity(c.Request.Context(), identity))
		server.handleSelfQuery(c)
	})
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}
	page := get("/events?api_key_hash=" + other.Hash() + "&auth_index=PRIVATE_AUTH&days=7")
	if page.Code != 200 {
		t.Fatalf("%d %s", page.Code, page.Body.String())
	}
	for _, forbidden := range []string{"PRIVATE", "apiKeyHash", "api_key_hash", "raw_json", "source", identity.Hash(), other.Hash()} {
		if strings.Contains(page.Body.String(), forbidden) {
			t.Fatalf("public data leaked %s", forbidden)
		}
	}
	var first struct {
		Items   []selfEvent `json:"items"`
		HasMore bool        `json:"hasMore"`
	}
	if err := json.Unmarshal(page.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 50 || !first.HasMore {
		t.Fatalf("bad first page: %s", page.Body.String())
	}
	last := first.Items[49]
	next := get(fmt.Sprintf("/events?before_ms=%d&before_id=%d", last.TimestampMS, last.ID))
	var second struct {
		Items   []selfEvent `json:"items"`
		HasMore bool        `json:"hasMore"`
	}
	if err := json.Unmarshal(next.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 5 || second.HasMore {
		t.Fatalf("bad second page: %s", next.Body.String())
	}
	stats := get("/stats?api_key_hash=" + other.Hash() + "&group_by=auth_index")
	var totals struct {
		Total UsageAggregateBucket `json:"total"`
	}
	if err := json.Unmarshal(stats.Body.Bytes(), &totals); err != nil {
		t.Fatal(err)
	}
	if stats.Code != 200 || totals.Total.TotalRequests != 55 || totals.Total.TotalTokens != 110 {
		t.Fatalf("unscoped totals: %s", stats.Body.String())
	}
	for _, path := range []string{"/events?days=0", "/stats?days=10000", "/events?before_id=2", "/events?before_ms=-1"} {
		if w := get(path); w.Code != 400 {
			t.Fatalf("%s status %d", path, w.Code)
		}
	}
}

func TestSelfQueryRejectsMissingIdentityAndRevokedStream(t *testing.T) {
	server := NewServer(Config{Enabled: true}, openTestStore(t))
	router := gin.New()
	router.GET("/:section", server.handleSelfQuery)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/stats", nil))
	if w.Code != 401 {
		t.Fatalf("unauthenticated status %d", w.Code)
	}
	identity, _ := apikeypolicy.NewAuthenticatedAPIKeyIdentity("revoked")
	router = gin.New()
	router.GET("/:section", func(c *gin.Context) {
		c.Request = c.Request.WithContext(apikeypolicy.WithIdentity(c.Request.Context(), identity))
		c.Set("selfQueryValidate", func() bool { return false })
		server.handleSelfQuery(c)
	})
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/stream", nil))
	if w.Body.Len() != 0 {
		t.Fatalf("revoked stream emitted %s", w.Body.String())
	}
}

func TestSelfUsageMetricsMatchMonitoringAccounting(t *testing.T) {
	for _, quality := range []string{"complete", "partial", ""} {
		t.Run(quality, func(t *testing.T) {
			event := internalusage.Event{Model: "model", ReasoningEffort: "xhigh", InputTokens: 1000, OutputTokens: 200, TotalTokens: 1200, ReasoningTokens: 80, CachedTokens: 600, CacheReadTokens: 600, CacheWriteTokens: 100, CacheTokens: 700, AccountingQuality: quality}
			projected := projectSelfEvent(event)
			management := internalusage.BuildPayload([]internalusage.Event{event}).APIs["-"].Models["model"].Details[0]
			if projected.ReasoningEffort != management.ReasoningEffort || projected.ReasoningTokens != management.Tokens.ReasoningTokens || projected.CacheReadTokens != management.Tokens.CacheReadTokens || projected.CachedTokens != management.Tokens.CachedTokens || projected.CacheInputTokens != management.Tokens.CacheInputTokens {
				t.Fatalf("public and monitoring metrics differ: public=%+v management=%+v", projected, management.Tokens)
			}
			if projected.ReasoningTokens != 80 || projected.CacheReadTokens != 600 || projected.CachedTokens != 600 {
				t.Fatalf("cache writes or reasoning were miscounted: %+v", projected)
			}
			wantDenominator := int64(0)
			if quality == "complete" {
				wantDenominator = 1000
			}
			if projected.CacheInputTokens != wantDenominator {
				t.Fatalf("cache denominator=%d want %d", projected.CacheInputTokens, wantDenominator)
			}
		})
	}
}

type selfStreamRecorder struct {
	*httptest.ResponseRecorder
	onFlush func()
}

func (w *selfStreamRecorder) Flush() {
	w.ResponseRecorder.Flush()
	w.onFlush()
}

func TestSelfStreamReportsResetWithoutPublishingOtherKeyActivity(t *testing.T) {
	store := openTestStore(t)
	server := NewServer(Config{Enabled: true}, store)
	identity, _ := apikeypolicy.NewAuthenticatedAPIKeyIdentity("stream-own")
	other, _ := apikeypolicy.NewAuthenticatedAPIKeyIdentity("stream-other")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	now := time.Now().UnixMilli()
	insert := func(hash, eventHash string) {
		_, err := store.InsertEvents(ctx, []internalusage.Event{{EventHash: eventHash, APIKeyHash: hash, TimestampMS: now, Timestamp: time.UnixMilli(now).Format(time.RFC3339Nano), Model: "private-model", TotalTokens: 1}})
		if err != nil {
			t.Fatal(err)
		}
	}
	insert(identity.Hash(), "own")
	state, err := server.usageDatasetState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ticks := make(chan time.Time, 1)
	recorder := &selfStreamRecorder{ResponseRecorder: httptest.NewRecorder()}
	flushes := 0
	var afterReset int64
	recorder.onFlush = func() {
		flushes++
		switch flushes {
		case 1:
			want := fmt.Sprintf("event: change\ndata: {\"generation\":%d}\n\n", state.Generation)
			if recorder.Body.String() != want {
				t.Fatalf("initial stream %q", recorder.Body.String())
			}
			insert(other.Hash(), "foreign")
			ticks <- time.Time{}
		case 2:
			if !strings.HasSuffix(recorder.Body.String(), ": keepalive\n\n") {
				t.Fatal("foreign activity changed public stream")
			}
			result, err := store.ResetUsageStatistics(ctx)
			if err != nil {
				t.Fatal(err)
			}
			afterReset = result.Generation
			ticks <- time.Time{}
		case 3:
			want := fmt.Sprintf("event: reset\ndata: {\"generation\":%d}\n\n", afterReset)
			if !strings.HasSuffix(recorder.Body.String(), want) {
				t.Fatalf("missing reset %q", recorder.Body.String())
			}
			cancel()
		default:
			t.Fatal("unexpected stream continuation")
		}
	}
	router := gin.New()
	router.GET("/stream", func(c *gin.Context) { server.streamSelfUsage(c, identity.Hash(), ticks) })
	router.ServeHTTP(recorder, httptest.NewRequest("GET", "/stream", nil).WithContext(ctx))
	if flushes != 3 {
		t.Fatalf("flushes %d", flushes)
	}
	if strings.Contains(recorder.Body.String(), "private-model") || strings.Contains(recorder.Body.String(), other.Hash()) {
		t.Fatal("stream leaked event fields")
	}
}

func TestSelfSnapshotRejectsDataCrossingResetBoundary(t *testing.T) {
	store := openTestStore(t)
	server := NewServer(Config{Enabled: true}, store)
	ctx := context.Background()
	_, err := store.InsertEvents(ctx, []internalusage.Event{{EventHash: "pre-reset", TimestampMS: time.Now().UnixMilli(), Model: "old-model"}})
	if err != nil {
		t.Fatal(err)
	}
	old, err := server.usageDatasetState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	reset, err := store.ResetUsageStatistics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.GET("/stats", func(c *gin.Context) { server.writeSelfQuerySnapshot(c, old, gin.H{"old": "must-not-escape"}) })
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest("GET", "/stats", nil))
	if recorder.Code != http.StatusConflict || strings.Contains(recorder.Body.String(), "must-not-escape") {
		t.Fatalf("stale snapshot returned: %d %s", recorder.Code, recorder.Body.String())
	}
	var result struct {
		Generation int64 `json:"generation"`
	}
	if err = json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Generation != reset.Generation {
		t.Fatalf("generation=%d want %d", result.Generation, reset.Generation)
	}
}
