package management

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	proinspection "github.com/router-for-me/CLIProxyAPI/v6/internal/pro/inspection"
)

func TestPaginateAccountInspectionResultsReturnsRequestedPage(t *testing.T) {
	results := []accountInspectionResult{
		testInspectionResult("healthy-1", accountInspectionActionKeep, false, nil, false, ""),
		testInspectionResult("healthy-2", accountInspectionActionKeep, false, nil, false, ""),
		testInspectionResult("auth-1", accountInspectionActionDelete, false, nil, false, ""),
		testInspectionResult("auth-2", accountInspectionActionKeep, false, testStatusCode(401), false, ""),
	}

	page, info := paginateAccountInspectionResults(results, 2, 2, "", false, "", "")
	if info.Page != 2 || info.PageSize != 2 || info.Total != 4 || info.TotalPages != 2 || info.HasMore {
		t.Fatalf("page info = %+v, want page=2 size=2 total=4 totalPages=2 hasMore=false", info)
	}
	if len(page) != 2 || page[0].Key != "auth-1" || page[1].Key != "auth-2" {
		t.Fatalf("page = %+v, want auth-1/auth-2", page)
	}
}

func TestPaginateAccountInspectionResultsFiltersHealthBuckets(t *testing.T) {
	results := []accountInspectionResult{
		testInspectionResult("healthy", accountInspectionActionKeep, false, nil, false, ""),
		testInspectionAuthInvalidResult("auth", "test", accountInspectionActionDelete),
		testInspectionQuotaResult("quota", "test", accountInspectionActionDisable),
		testInspectionResult("error", accountInspectionActionKeep, false, nil, false, "network error"),
		testInspectionResult("recoverable", accountInspectionActionEnable, true, nil, false, ""),
		testInspectionResult("disabled", accountInspectionActionKeep, true, nil, false, ""),
	}

	page, info := paginateAccountInspectionResults(results, 1, 10, "quotaExhausted", false, "", "")
	if info.Total != 1 || info.HasMore {
		t.Fatalf("quota page info = %+v, want total=1 hasMore=false", info)
	}
	if len(page) != 1 || page[0].Key != "quota" {
		t.Fatalf("quota page = %+v, want quota", page)
	}

	page, info = paginateAccountInspectionResults(results, 1, 10, "pending", false, "", "")
	if info.Total != 3 {
		t.Fatalf("pending page info = %+v, want total=3", info)
	}
	if len(page) != 3 || page[0].Key != "auth" || page[1].Key != "quota" || page[2].Key != "recoverable" {
		t.Fatalf("pending page = %+v, want auth/quota/recoverable", page)
	}

	page, info = paginateAccountInspectionResults(results, 1, 10, "attention", false, "", "")
	if info.Total != 5 || len(page) != 5 {
		t.Fatalf("attention page info = %+v page=%+v, want all non-healthy results", info, page)
	}

	page, info = paginateAccountInspectionResults(results, 1, 10, "attention", true, "", "")
	if info.Total != 3 || len(page) != 3 || page[0].Key != "auth" || page[1].Key != "quota" || page[2].Key != "recoverable" {
		t.Fatalf("pending attention page = %+v info=%+v, want auth/quota/recoverable", page, info)
	}

	page, info = paginateAccountInspectionResults(results, 1, 10, "accountIssues", false, "", "")
	if info.Total != 2 || len(page) != 2 || page[0].Key != "auth" || page[1].Key != "error" {
		t.Fatalf("account issues page = %+v info=%+v, want auth/error", page, info)
	}

	page, info = paginateAccountInspectionResults(results, 1, 10, "quotaChanges", false, "", "")
	if info.Total != 2 || len(page) != 2 || page[0].Key != "quota" || page[1].Key != "recoverable" {
		t.Fatalf("quota changes page = %+v info=%+v, want quota/recoverable", page, info)
	}
}

func TestPaginateAccountInspectionResultsFiltersProvider(t *testing.T) {
	results := []accountInspectionResult{
		testInspectionProviderResult("codex-healthy", "codex", accountInspectionActionKeep, false, nil, false, ""),
		testInspectionProviderResult("claude-healthy", "claude", accountInspectionActionKeep, false, nil, false, ""),
		testInspectionAuthInvalidResult("codex-auth", "codex", accountInspectionActionDelete),
		testInspectionAuthInvalidResult("claude-auth", "claude", accountInspectionActionDelete),
	}

	page, info := paginateAccountInspectionResults(results, 1, 10, "pending", false, "codex", "")
	if info.Total != 1 || info.TotalPages != 1 || info.HasMore {
		t.Fatalf("codex pending page info = %+v, want total=1 totalPages=1 hasMore=false", info)
	}
	if len(page) != 1 || page[0].Key != "codex-auth" {
		t.Fatalf("codex pending page = %+v, want codex-auth", page)
	}

	page, info = paginateAccountInspectionResults(results, 1, 10, "healthy", false, "claude", "")
	if info.Total != 1 || len(page) != 1 || page[0].Key != "claude-healthy" {
		t.Fatalf("claude healthy page = %+v info=%+v, want claude-healthy", page, info)
	}
}

func TestPaginateAccountInspectionResultsSearchesAccountIdentity(t *testing.T) {
	first := testInspectionProviderResult("first", "codex", accountInspectionActionKeep, false, nil, false, "")
	first.FileName = "codex-alice.json"
	first.DisplayName = "Alice Primary"
	first.Email = "alice@example.com"
	first.Name = "Alice"
	first.AuthIndex = "auth-alice"
	second := testInspectionProviderResult("second", "claude", accountInspectionActionKeep, false, nil, false, "")
	second.FileName = "claude-bob.json"
	second.Email = "bob@example.com"
	results := []accountInspectionResult{first, second}

	page, info := paginateAccountInspectionResults(results, 1, 10, "healthy", false, "codex", "ALICE@EXAMPLE")
	if info.Total != 1 || len(page) != 1 || page[0].Key != "first" {
		t.Fatalf("account search page = %+v info=%+v, want first result only", page, info)
	}

	page, info = paginateAccountInspectionResults(results, 1, 10, "healthy", false, "", "claude-bob")
	if info.Total != 1 || len(page) != 1 || page[0].Key != "second" {
		t.Fatalf("file-name search page = %+v info=%+v, want second result only", page, info)
	}
}

func TestStreamStatusLockedOmitsDetailsForLightSnapshots(t *testing.T) {
	scheduler := &accountInspectionScheduler{
		status: accountInspectionStatus{
			Results: []accountInspectionResult{
				testInspectionResult("healthy", accountInspectionActionKeep, false, nil, false, ""),
			},
			Logs: []accountInspectionLogEntry{{Time: 1, Level: "info", Message: "hello"}},
		},
	}

	status := scheduler.streamStatusLocked(accountInspectionSnapshotOptions{})
	if status.Results != nil || status.Logs != nil || status.HealthCounts != nil || status.ProviderHealthCounts != nil {
		t.Fatalf("streamStatusLocked(light) leaked details: results=%v logs=%v health=%v providerHealth=%v", status.Results, status.Logs, status.HealthCounts, status.ProviderHealthCounts)
	}
	if status.ResultsLimited || status.LogsLimited {
		t.Fatalf("streamStatusLocked(light) limited flags = results:%v logs:%v, want false", status.ResultsLimited, status.LogsLimited)
	}
	if status.ResultsPage != nil || status.LogsPage != nil {
		t.Fatalf("streamStatusLocked(light) leaked page info: results=%v logs=%v", status.ResultsPage, status.LogsPage)
	}
}

func TestStreamStatusIncludesLastRunSettings(t *testing.T) {
	scheduler := &accountInspectionScheduler{
		lastRunSettings: accountInspectionSettings{TargetType: "xai", UsedPercentThreshold: 73},
		status:          accountInspectionStatus{State: accountInspectionStateCompleted},
	}
	status := scheduler.streamStatusLocked(accountInspectionSnapshotOptions{})
	if status.RunSettings.TargetType != "xai" || status.RunSettings.UsedPercentThreshold != 73 {
		t.Fatalf("run settings = %#v", status.RunSettings)
	}
}

func TestInspectManyAccountsRejectsInvalidBatchWithoutScheduler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := &Handler{}
	scheduler := &accountInspectionScheduler{
		schedule: accountInspectionSchedule{}, status: accountInspectionStatus{},
		subscribers: make(map[chan accountInspectionLogStreamMessage]struct{}),
	}
	accountInspectionSchedulers.Store(handler, scheduler)
	t.Cleanup(func() { accountInspectionSchedulers.Delete(handler) })

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest("POST", "/account-inspection/inspect-many", strings.NewReader(`{"items":[]}`))
	context.Request.Header.Set("Content-Type", "application/json")
	handler.InspectManyAccounts(context)
	if recorder.Code != 400 {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestAccountInspectionHTTPStatusClassifiesExpectedControlErrors(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		{errAccountInspectionAlreadyRunning, http.StatusConflict},
		{errAccountInspectionResultStale, http.StatusConflict},
		{proinspection.ErrPaused, http.StatusLocked},
		{context.DeadlineExceeded, http.StatusGatewayTimeout},
		{context.Canceled, http.StatusRequestTimeout},
	}
	for _, test := range tests {
		if got := accountInspectionHTTPStatus(test.err); got != test.want {
			t.Errorf("accountInspectionHTTPStatus(%v) = %d, want %d", test.err, got, test.want)
		}
	}
}

func TestInspectManyAccountsReturnsConflictWhenInspectionIsRunning(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := &Handler{}
	scheduler := &accountInspectionScheduler{
		schedule: accountInspectionSchedule{}, status: accountInspectionStatus{State: accountInspectionStateRunning},
		subscribers: make(map[chan accountInspectionLogStreamMessage]struct{}),
	}
	accountInspectionSchedulers.Store(handler, scheduler)
	t.Cleanup(func() { accountInspectionSchedulers.Delete(handler) })

	recorder := httptest.NewRecorder()
	requestContext, _ := gin.CreateTestContext(recorder)
	requestContext.Request = httptest.NewRequest("POST", "/account-inspection/inspect-many", strings.NewReader(`{"items":[{"key":"account","fileName":"account.json","authIndex":"account"}]}`))
	requestContext.Request.Header.Set("Content-Type", "application/json")
	handler.InspectManyAccounts(requestContext)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s, want conflict", recorder.Code, recorder.Body.String())
	}
}

func TestAccountInspectionScheduleDoesNotRegisterPatchRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	handler := &Handler{}
	handler.RegisterAccountInspectionRoutes(engine.Group("/v0/management"))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPatch, "/v0/management/account-inspection/schedule", strings.NewReader(`{"enabled":true}`))
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("PATCH schedule status = %d body=%s, want not found", recorder.Code, recorder.Body.String())
	}
}

func TestAccountInspectionDoesNotRegisterDetailHistoryRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	handler := &Handler{}
	handler.RegisterAccountInspectionRoutes(engine.Group("/v0/management"))

	for _, path := range []string{
		"/v0/management/account-inspection/history",
		"/v0/management/account-inspection/operations",
	} {
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("GET %s status = %d body=%s, want not found", path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestInspectOneAccountReturnsConflictWhenInspectionIsRunning(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := &Handler{}
	scheduler := &accountInspectionScheduler{
		status:      accountInspectionStatus{State: accountInspectionStateRunning},
		subscribers: make(map[chan accountInspectionLogStreamMessage]struct{}),
	}
	accountInspectionSchedulers.Store(handler, scheduler)
	t.Cleanup(func() { accountInspectionSchedulers.Delete(handler) })

	recorder := httptest.NewRecorder()
	requestContext, _ := gin.CreateTestContext(recorder)
	requestContext.Request = httptest.NewRequest(http.MethodPost, "/account-inspection/inspect-one", strings.NewReader(`{"item":{"key":"account"}}`))
	requestContext.Request.Header.Set("Content-Type", "application/json")
	handler.InspectOneAccount(requestContext)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s, want conflict", recorder.Code, recorder.Body.String())
	}
}

func TestRefreshAccountInspectionTokenReturnsConflictWhenInspectionIsRunning(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := &Handler{}
	scheduler := &accountInspectionScheduler{
		status:      accountInspectionStatus{State: accountInspectionStateRunning},
		subscribers: make(map[chan accountInspectionLogStreamMessage]struct{}),
	}
	accountInspectionSchedulers.Store(handler, scheduler)
	t.Cleanup(func() { accountInspectionSchedulers.Delete(handler) })

	recorder := httptest.NewRecorder()
	requestContext, _ := gin.CreateTestContext(recorder)
	requestContext.Request = httptest.NewRequest(http.MethodPost, "/account-inspection/refresh-token", strings.NewReader(`{"item":{"key":"account"}}`))
	requestContext.Request.Header.Set("Content-Type", "application/json")
	handler.RefreshAccountInspectionToken(requestContext)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s, want conflict", recorder.Code, recorder.Body.String())
	}
}

func TestStreamStatusLockedReturnsPagedDetailsWithFullHealthCounts(t *testing.T) {
	scheduler := &accountInspectionScheduler{
		status: accountInspectionStatus{
			Results: []accountInspectionResult{
				testInspectionResult("healthy-1", accountInspectionActionKeep, false, nil, false, ""),
				testInspectionResult("healthy-2", accountInspectionActionKeep, false, nil, false, ""),
				testInspectionAuthInvalidResult("auth-1", "test", accountInspectionActionDelete),
				testInspectionResult("auth-2", accountInspectionActionKeep, false, testStatusCode(401), false, ""),
			},
			Logs: []accountInspectionLogEntry{
				{Time: 1, Level: "info", Message: "one"},
				{Time: 2, Level: "info", Message: "two"},
				{Time: 3, Level: "info", Message: "three"},
			},
		},
	}

	status := scheduler.streamStatusLocked(accountInspectionSnapshotOptions{
		IncludeDetails: true,
		ResultPage:     2,
		ResultPageSize: 2,
		LogPage:        1,
		LogPageSize:    2,
	})

	if status.HealthCounts == nil {
		t.Fatal("streamStatusLocked(details) HealthCounts = nil")
	}
	if status.HealthCounts.Total != 4 || status.HealthCounts.Healthy != 2 || status.HealthCounts.AuthInvalid != 2 {
		t.Fatalf("HealthCounts = %+v, want total=4 healthy=2 authInvalid=2", *status.HealthCounts)
	}
	providerCounts := status.ProviderHealthCounts["test"]
	if providerCounts.Total != 4 || providerCounts.Healthy != 2 || providerCounts.AuthInvalid != 2 {
		t.Fatalf("ProviderHealthCounts[test] = %+v, want total=4 healthy=2 authInvalid=2", providerCounts)
	}
	if status.ResultsPage == nil || status.ResultsPage.Total != 4 || status.ResultsPage.Page != 2 || status.ResultsPage.PageSize != 2 {
		t.Fatalf("ResultsPage = %+v, want page=2 size=2 total=4", status.ResultsPage)
	}
	if status.LogsPage == nil || status.LogsPage.Total != 3 || status.LogsPage.Page != 1 || status.LogsPage.PageSize != 2 || !status.LogsPage.HasMore {
		t.Fatalf("LogsPage = %+v, want page=1 size=2 total=3 hasMore=true", status.LogsPage)
	}
	if len(status.Results) != 2 {
		t.Fatalf("paged results len = %d, want 2", len(status.Results))
	}
	if status.Results[0].Key != "auth-1" || status.Results[1].Key != "auth-2" {
		t.Fatalf("paged results = %+v, want auth rows", status.Results)
	}
	if len(status.Logs) != 2 || status.Logs[0].Time != 2 || status.Logs[1].Time != 3 {
		t.Fatalf("paged logs = %+v, want last two log entries", status.Logs)
	}
}

func TestPaginateAccountInspectionPageSizeCapsAtServerMax(t *testing.T) {
	results := make([]accountInspectionResult, accountInspectionMaxResultPageSize+5)
	for index := range results {
		results[index] = testInspectionResult("result", accountInspectionActionKeep, false, nil, false, "")
	}
	page, info := paginateAccountInspectionResults(results, 1, accountInspectionMaxResultPageSize+100, "", false, "", "")
	if info.PageSize != accountInspectionMaxResultPageSize {
		t.Fatalf("result page size = %d, want capped %d", info.PageSize, accountInspectionMaxResultPageSize)
	}
	if len(page) != accountInspectionMaxResultPageSize {
		t.Fatalf("result page len = %d, want %d", len(page), accountInspectionMaxResultPageSize)
	}

	logs := make([]accountInspectionLogEntry, accountInspectionMaxLogPageSize+5)
	for index := range logs {
		logs[index] = accountInspectionLogEntry{Time: int64(index + 1), Level: "info", Message: "log"}
	}
	logPage, logInfo := paginateAccountInspectionLogs(logs, 1, accountInspectionMaxLogPageSize+100, "")
	if logInfo.PageSize != accountInspectionMaxLogPageSize {
		t.Fatalf("log page size = %d, want capped %d", logInfo.PageSize, accountInspectionMaxLogPageSize)
	}
	if len(logPage) != accountInspectionMaxLogPageSize {
		t.Fatalf("log page len = %d, want %d", len(logPage), accountInspectionMaxLogPageSize)
	}
}

func TestHealthCountsLockedRebuildsStaleCache(t *testing.T) {
	scheduler := &accountInspectionScheduler{
		status: accountInspectionStatus{
			Results: []accountInspectionResult{
				testInspectionResult("healthy", accountInspectionActionKeep, false, nil, false, ""),
				testInspectionAuthInvalidResult("auth", "test", accountInspectionActionDelete),
			},
		},
	}

	counts := scheduler.healthCountsLocked()
	if counts.Total != 2 || counts.Healthy != 1 || counts.AuthInvalid != 1 {
		t.Fatalf("healthCountsLocked() = %+v, want total=2 healthy=1 authInvalid=1", counts)
	}
	if scheduler.healthCounts != counts {
		t.Fatalf("scheduler healthCounts cache = %+v, want %+v", scheduler.healthCounts, counts)
	}
}
