package management

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

type accountInspectionTestStorage struct {
	meta map[string]any
}

type accountInspectionAuthStore struct {
	path string
}

func (s *accountInspectionAuthStore) List(context.Context) ([]*coreauth.Auth, error) {
	return nil, nil
}

func (s *accountInspectionAuthStore) Save(_ context.Context, auth *coreauth.Auth) (string, error) {
	raw, err := json.Marshal(auth.Metadata)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(s.path, append(raw, '\n'), 0o600); err != nil {
		return "", err
	}
	return s.path, nil
}

func (s *accountInspectionAuthStore) Delete(context.Context, string) error {
	return nil
}

func (s *accountInspectionTestStorage) SetMetadata(meta map[string]any) {
	s.meta = meta
}

func (s *accountInspectionTestStorage) SaveTokenToFile(path string) error {
	raw, err := json.Marshal(s.meta)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}

func testInspectionResult(key string, action accountInspectionAction, disabled bool, statusCode *int, isQuota bool, err string) accountInspectionResult {
	return accountInspectionResult{
		Key:        key,
		Provider:   "test",
		FileName:   key + ".json",
		AuthIndex:  key,
		Action:     action,
		Disabled:   disabled,
		StatusCode: statusCode,
		IsQuota:    isQuota,
		Error:      err,
	}
}

func testInspectionProviderResult(key string, provider string, action accountInspectionAction, disabled bool, statusCode *int, isQuota bool, err string) accountInspectionResult {
	result := testInspectionResult(key, action, disabled, statusCode, isQuota, err)
	result.Provider = provider
	return result
}

func testStatusCode(value int) *int {
	return &value
}

func TestManagementHandlerShutdownReleasesBackgroundOwners(t *testing.T) {
	t.Setenv("ACCOUNT_INSPECTION_SCHEDULE_PATH", filepath.Join(t.TempDir(), "schedule.json"))
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, nil)
	scheduler := schedulerForHandler(h)
	if scheduler == nil {
		t.Fatal("scheduler was not registered")
	}
	if routingPolicyControllerForHandler(h) == nil {
		t.Fatal("routing policy controller was not registered")
	}

	h.Shutdown()
	h.Shutdown()

	if schedulerForHandler(h) != nil {
		t.Fatal("scheduler registration survived shutdown")
	}
	if routingPolicyControllerForHandler(h) != nil {
		t.Fatal("routing policy controller registration survived shutdown")
	}
	select {
	case <-h.lifecycleContext.Done():
	default:
		t.Fatal("handler lifecycle context was not canceled")
	}
	if err := scheduler.startRun(true); err == nil {
		t.Fatal("shut down scheduler accepted a new run")
	}
}

func TestPaginateAccountInspectionResultsReturnsRequestedPage(t *testing.T) {
	results := []accountInspectionResult{
		testInspectionResult("healthy-1", accountInspectionActionKeep, false, nil, false, ""),
		testInspectionResult("healthy-2", accountInspectionActionKeep, false, nil, false, ""),
		testInspectionResult("auth-1", accountInspectionActionDelete, false, nil, false, ""),
		testInspectionResult("auth-2", accountInspectionActionKeep, false, testStatusCode(401), false, ""),
	}

	page, info := paginateAccountInspectionResults(results, 2, 2, "", "")
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
		testInspectionResult("auth", accountInspectionActionDelete, false, nil, false, ""),
		testInspectionResult("quota", accountInspectionActionDisable, false, nil, false, ""),
		testInspectionResult("error", accountInspectionActionKeep, false, nil, false, "network error"),
		testInspectionResult("recoverable", accountInspectionActionEnable, true, nil, false, ""),
		testInspectionResult("disabled", accountInspectionActionKeep, true, nil, false, ""),
	}

	page, info := paginateAccountInspectionResults(results, 1, 10, "quotaExhausted", "")
	if info.Total != 1 || info.HasMore {
		t.Fatalf("quota page info = %+v, want total=1 hasMore=false", info)
	}
	if len(page) != 1 || page[0].Key != "quota" {
		t.Fatalf("quota page = %+v, want quota", page)
	}

	page, info = paginateAccountInspectionResults(results, 1, 10, "pending", "")
	if info.Total != 3 {
		t.Fatalf("pending page info = %+v, want total=3", info)
	}
	if len(page) != 3 || page[0].Key != "auth" || page[1].Key != "quota" || page[2].Key != "recoverable" {
		t.Fatalf("pending page = %+v, want auth/quota/recoverable", page)
	}
}

func TestPaginateAccountInspectionResultsFiltersProvider(t *testing.T) {
	results := []accountInspectionResult{
		testInspectionProviderResult("codex-healthy", "codex", accountInspectionActionKeep, false, nil, false, ""),
		testInspectionProviderResult("claude-healthy", "claude", accountInspectionActionKeep, false, nil, false, ""),
		testInspectionProviderResult("codex-auth", "codex", accountInspectionActionDelete, false, nil, false, ""),
		testInspectionProviderResult("claude-auth", "claude", accountInspectionActionDelete, false, nil, false, ""),
	}

	page, info := paginateAccountInspectionResults(results, 1, 10, "pending", "codex")
	if info.Total != 1 || info.TotalPages != 1 || info.HasMore {
		t.Fatalf("codex pending page info = %+v, want total=1 totalPages=1 hasMore=false", info)
	}
	if len(page) != 1 || page[0].Key != "codex-auth" {
		t.Fatalf("codex pending page = %+v, want codex-auth", page)
	}

	page, info = paginateAccountInspectionResults(results, 1, 10, "healthy", "claude")
	if info.Total != 1 || len(page) != 1 || page[0].Key != "claude-healthy" {
		t.Fatalf("claude healthy page = %+v info=%+v, want claude-healthy", page, info)
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
	if status.Results != nil || status.Logs != nil || status.HealthCounts != nil {
		t.Fatalf("streamStatusLocked(light) leaked details: results=%v logs=%v health=%v", status.Results, status.Logs, status.HealthCounts)
	}
	if status.ResultsLimited || status.LogsLimited {
		t.Fatalf("streamStatusLocked(light) limited flags = results:%v logs:%v, want false", status.ResultsLimited, status.LogsLimited)
	}
	if status.ResultsPage != nil || status.LogsPage != nil {
		t.Fatalf("streamStatusLocked(light) leaked page info: results=%v logs=%v", status.ResultsPage, status.LogsPage)
	}
}

func TestStreamStatusLockedReturnsPagedDetailsWithFullHealthCounts(t *testing.T) {
	scheduler := &accountInspectionScheduler{
		status: accountInspectionStatus{
			Results: []accountInspectionResult{
				testInspectionResult("healthy-1", accountInspectionActionKeep, false, nil, false, ""),
				testInspectionResult("healthy-2", accountInspectionActionKeep, false, nil, false, ""),
				testInspectionResult("auth-1", accountInspectionActionDelete, false, nil, false, ""),
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
	page, info := paginateAccountInspectionResults(results, 1, accountInspectionMaxResultPageSize+100, "", "")
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
				testInspectionResult("auth", accountInspectionActionDelete, false, nil, false, ""),
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

func TestAccountInspectionResultSnapshotPersistsAndRestoresReadOnly(t *testing.T) {
	snapshotPath := filepath.Join(t.TempDir(), "account-inspection-snapshot.json")
	settings := defaultAccountInspectionSettings()
	settings.TargetType = "xai"
	result := testInspectionProviderResult("xai-1", "xai", accountInspectionActionKeep, false, testStatusCode(502), false, "upstream error")
	result.ErrorDetail = `{"error":{"message":"raw upstream response"}}`

	source := &accountInspectionScheduler{
		snapshotPath:    snapshotPath,
		lastRunSettings: settings,
		status: accountInspectionStatus{
			State:          accountInspectionStateCompleted,
			LastStartedAt:  1000,
			LastFinishedAt: 2000,
			Summary:        accountInspectionSummary{TotalFiles: 1, ProbeSetCount: 1, SampledCount: 1, ErrorCount: 1},
			Results:        []accountInspectionResult{result},
		},
	}
	if err := os.WriteFile(snapshotPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(existing snapshot) error = %v", err)
	}
	if err := source.saveResultSnapshotLocked(); err != nil {
		t.Fatalf("saveResultSnapshotLocked() error = %v", err)
	}
	info, err := os.Stat(snapshotPath)
	if err != nil {
		t.Fatalf("os.Stat(snapshot) error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("snapshot mode = %o, want 600", got)
	}

	restored := &accountInspectionScheduler{snapshotPath: snapshotPath}
	if err := restored.loadResultSnapshot(); err != nil {
		t.Fatalf("loadResultSnapshot() error = %v", err)
	}
	if !restored.status.RestoredSnapshot {
		t.Fatal("restored snapshot is not marked read-only")
	}
	if restored.lastRunSettings.TargetType != "xai" {
		t.Fatalf("restored target type = %q, want xai", restored.lastRunSettings.TargetType)
	}
	if len(restored.status.Results) != 1 || restored.status.Results[0].ErrorDetail != result.ErrorDetail {
		t.Fatalf("restored results = %+v, want original error detail", restored.status.Results)
	}
	if len(restored.status.Logs) != 0 {
		t.Fatalf("restored logs = %+v, want none", restored.status.Logs)
	}

	if _, err := restored.inspectOne(context.Background(), accountInspectionActionItem{}); !errors.Is(err, errAccountInspectionRestoredSnapshotReadOnly) {
		t.Fatalf("inspectOne() error = %v, want read-only error", err)
	}
	if _, err := restored.refreshTokenNow(context.Background(), accountInspectionActionItem{}); !errors.Is(err, errAccountInspectionRestoredSnapshotReadOnly) {
		t.Fatalf("refreshTokenNow() error = %v, want read-only error", err)
	}
	if _, err := restored.executeManualActions(context.Background(), nil); !errors.Is(err, errAccountInspectionRestoredSnapshotReadOnly) {
		t.Fatalf("executeManualActions() error = %v, want read-only error", err)
	}
}

func TestAccountInspectionSnapshotExportKeepsLastFinishedSnapshotDuringRun(t *testing.T) {
	snapshotPath := filepath.Join(t.TempDir(), "account-inspection-snapshot.json")
	scheduler := &accountInspectionScheduler{
		snapshotPath:    snapshotPath,
		lastRunSettings: defaultAccountInspectionSettings(),
		status: accountInspectionStatus{
			State:          accountInspectionStateCompleted,
			LastStartedAt:  1000,
			LastFinishedAt: 2000,
			Results:        []accountInspectionResult{testInspectionResult("finished", accountInspectionActionKeep, false, nil, false, "")},
		},
	}
	if err := scheduler.saveResultSnapshotLocked(); err != nil {
		t.Fatalf("saveResultSnapshotLocked() error = %v", err)
	}
	scheduler.status = accountInspectionStatus{State: accountInspectionStateRunning, LastStartedAt: 3000}

	raw, ok, err := scheduler.exportResultSnapshot()
	if err != nil {
		t.Fatalf("exportResultSnapshot() error = %v", err)
	}
	if !ok {
		t.Fatal("exportResultSnapshot() ok = false, want previous finished snapshot")
	}
	snapshot, err := decodeAccountInspectionResultSnapshot(raw)
	if err != nil {
		t.Fatalf("decodeAccountInspectionResultSnapshot() error = %v", err)
	}
	if snapshot.LastFinishedAt != 2000 || len(snapshot.Results) != 1 || snapshot.Results[0].Key != "finished" {
		t.Fatalf("exported snapshot = %+v, want previous finished run", snapshot)
	}
}

func TestHealthCountsCacheTracksResultUpdates(t *testing.T) {
	scheduler := &accountInspectionScheduler{}
	healthy := testInspectionResult("account", accountInspectionActionKeep, false, nil, false, "")
	if !scheduler.updateInspectionResultLocked(healthy, true, func(current accountInspectionResult) (accountInspectionResult, bool) {
		return current, true
	}) {
		t.Fatal("updateInspectionResultLocked() append healthy = false, want true")
	}
	if scheduler.healthCounts.Total != 1 || scheduler.healthCounts.Healthy != 1 {
		t.Fatalf("after append healthCounts = %+v, want total=1 healthy=1", scheduler.healthCounts)
	}

	authInvalid := healthy
	authInvalid.Action = accountInspectionActionDelete
	if !scheduler.updateInspectionResultLocked(authInvalid, true, func(current accountInspectionResult) (accountInspectionResult, bool) {
		return authInvalid, true
	}) {
		t.Fatal("updateInspectionResultLocked() replace auth invalid = false, want true")
	}
	if scheduler.healthCounts.Total != 1 || scheduler.healthCounts.Healthy != 0 || scheduler.healthCounts.AuthInvalid != 1 {
		t.Fatalf("after replace healthCounts = %+v, want total=1 healthy=0 authInvalid=1", scheduler.healthCounts)
	}

	if !scheduler.removeInspectionResultLocked(authInvalid) {
		t.Fatal("removeInspectionResultLocked() = false, want true")
	}
	if scheduler.healthCounts.Total != 0 || scheduler.healthCounts.AuthInvalid != 0 {
		t.Fatalf("after remove healthCounts = %+v, want empty", scheduler.healthCounts)
	}
}

func TestMergeTokenRefreshResultUpdatesErrorCodeAndHealthCounts(t *testing.T) {
	scheduler := &accountInspectionScheduler{}
	healthy := testInspectionResult("account", accountInspectionActionKeep, false, nil, false, "")
	healthy.Provider = "codex"
	scheduler.status.Results = []accountInspectionResult{healthy}
	scheduler.status.Summary = summarizeAccountInspection(1, 1, nil, scheduler.status.Results)
	scheduler.healthCounts = accountInspectionResultHealthCounts(scheduler.status.Results)

	failed := healthy
	failed.TokenRefreshTriggered = true
	failed.TokenRefreshStatus = "failed"
	failed.TokenRefreshError = "refresh failed"
	failed.Error = "refresh failed"
	failed.ErrorCode = "token_refresh_error"
	failed.ActionReason = "刷新令牌失败，保留账号"
	scheduler.mergeTokenRefreshResultLocked(failed)

	got := scheduler.status.Results[0]
	if got.ErrorCode != "token_refresh_error" || got.Error != "refresh failed" || got.TokenRefreshStatus != "failed" {
		t.Fatalf("merged failed refresh = %+v, want token_refresh_error", got)
	}
	if scheduler.healthCounts.InspectionError != 1 || scheduler.healthCounts.Healthy != 0 || scheduler.status.Summary.ErrorCount != 1 {
		t.Fatalf("after failed refresh health=%+v summary=%+v, want inspection error and summary error", scheduler.healthCounts, scheduler.status.Summary)
	}

	success := got
	success.TokenRefreshStatus = "success"
	success.TokenRefreshError = ""
	success.Error = ""
	success.ErrorCode = ""
	scheduler.mergeTokenRefreshResultLocked(success)

	got = scheduler.status.Results[0]
	if got.ErrorCode != "" || got.Error != "" || got.TokenRefreshStatus != "success" {
		t.Fatalf("merged successful refresh = %+v, want cleared token refresh error", got)
	}
	if scheduler.healthCounts.Healthy != 1 || scheduler.healthCounts.InspectionError != 0 || scheduler.status.Summary.ErrorCount != 0 {
		t.Fatalf("after successful refresh health=%+v summary=%+v, want healthy and no summary error", scheduler.healthCounts, scheduler.status.Summary)
	}
}

func TestSyncAuthInspectionLastErrorClearsMetadata(t *testing.T) {
	auth := &coreauth.Auth{
		LastError: &coreauth.Error{Code: "token_refresh_error", Message: "old refresh failed"},
		Metadata: map[string]any{
			"last_error": map[string]any{"code": "token_refresh_error", "message": "old refresh failed"},
			"email":      "user@example.com",
		},
	}

	syncAuthInspectionLastError(auth, nil)

	if auth.LastError != nil {
		t.Fatalf("LastError = %#v, want nil", auth.LastError)
	}
	if _, ok := auth.Metadata["last_error"]; ok {
		t.Fatalf("metadata last_error = %#v, want removed", auth.Metadata["last_error"])
	}
	if auth.Metadata["email"] != "user@example.com" {
		t.Fatalf("metadata email = %#v, want preserved", auth.Metadata["email"])
	}
}

func TestSyncInspectionAuthErrorPersistsLastErrorMetadata(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	registered, err := manager.Register(context.Background(), &coreauth.Auth{
		Provider: "codex",
		ID:       "codex-user",
		FileName: "codex-user.json",
		Metadata: map[string]any{"email": "user@example.com"},
	})
	if err != nil {
		t.Fatalf("Register auth error = %v", err)
	}

	scheduler := &accountInspectionScheduler{h: &Handler{authManager: manager}}
	scheduler.syncInspectionAuthError(context.Background(), accountFromAuth(registered), "token_refresh_error", "refresh failed", 0)

	var got *coreauth.Auth
	for _, auth := range manager.List() {
		if auth.ID == registered.ID {
			got = auth
			break
		}
	}
	if got == nil {
		t.Fatal("updated auth not found")
	}
	if got.Status != coreauth.StatusError || !got.Unavailable || got.StatusMessage != "refresh failed" {
		t.Fatalf("updated status = status:%q unavailable:%v message:%q, want error/unavailable/refresh failed", got.Status, got.Unavailable, got.StatusMessage)
	}
	if got.LastError == nil || got.LastError.Code != "token_refresh_error" || got.LastError.Message != "refresh failed" {
		t.Fatalf("LastError = %#v, want token_refresh_error/refresh failed", got.LastError)
	}
	lastError, ok := got.Metadata["last_error"].(map[string]any)
	if !ok {
		t.Fatalf("metadata last_error = %#v, want object", got.Metadata["last_error"])
	}
	if lastError["code"] != "token_refresh_error" || lastError["message"] != "refresh failed" {
		t.Fatalf("metadata last_error = %#v, want token_refresh_error/refresh failed", lastError)
	}
}

func TestCleanupLegacyQuotaCacheFromOrdinaryAuthPersistsJSONRemoval(t *testing.T) {
	authPath := filepath.Join(t.TempDir(), "codex-user.json")
	manager := coreauth.NewManager(&accountInspectionAuthStore{path: authPath}, nil, nil)
	registered, err := manager.Register(context.Background(), &coreauth.Auth{
		Provider: "codex",
		ID:       "codex-user",
		FileName: "codex-user.json",
		Metadata: map[string]any{
			"email":       "user@example.com",
			"quota_cache": map[string]any{"status": "success", "cachedAt": float64(123)},
		},
	})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	scheduler := &accountInspectionScheduler{h: &Handler{authManager: manager}}
	if err := scheduler.cleanupLegacyQuotaCacheFromAuth(context.Background(), accountFromAuth(registered)); err != nil {
		t.Fatalf("cleanupLegacyQuotaCacheFromAuth() error = %v", err)
	}
	raw, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var persisted map[string]any
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if _, ok := persisted["quota_cache"]; ok {
		t.Fatalf("persisted quota_cache = %#v, want removed", persisted["quota_cache"])
	}
	if persisted["email"] != "user@example.com" {
		t.Fatalf("persisted email = %#v, want preserved", persisted["email"])
	}
	got, ok := manager.GetByID(registered.ID)
	if !ok || got == nil {
		t.Fatal("updated auth not found")
	}
	if _, ok := got.Metadata["quota_cache"]; ok {
		t.Fatalf("runtime quota_cache = %#v, want removed", got.Metadata["quota_cache"])
	}
}

func TestCleanupLegacyQuotaCachesPreservesPluginVirtualSourceMetadata(t *testing.T) {
	authPath := filepath.Join(t.TempDir(), "gemini-cli.json")
	legacyQuota := map[string]any{"status": "success", "cachedAt": float64(123)}
	if err := os.WriteFile(authPath, []byte(`{"type":"gemini-cli","email":"user@example.com","project_id":"project-a","project_ids":["project-a","project-b"],"quota_cache":{"status":"success","cachedAt":123}}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	manager := coreauth.NewManager(nil, nil, nil)
	primary := &coreauth.Auth{
		Provider: "gemini-cli",
		ID:       "gemini-cli-primary",
		FileName: "gemini-cli.json",
		Metadata: map[string]any{
			"type":        "gemini-cli",
			"email":       "user@example.com",
			"project_id":  "project-a",
			"quota_cache": legacyQuota,
		},
		Attributes: map[string]string{"path": authPath, "project_id": "project-a"},
	}
	coreauth.MarkPluginVirtualAuth(primary, authPath, 0)
	secondary := &coreauth.Auth{
		Provider: "gemini-cli",
		ID:       "gemini-cli-project-b",
		FileName: "user-project-b.json",
		Metadata: map[string]any{
			"type":        "gemini-cli",
			"email":       "user@example.com",
			"project_id":  "project-b",
			"virtual":     true,
			"quota_cache": legacyQuota,
		},
		Attributes: map[string]string{"path": authPath, "project_id": "project-b", "runtime_only": "true"},
	}
	coreauth.MarkPluginVirtualAuth(secondary, authPath, 1)
	if _, err := manager.Register(context.Background(), secondary); err != nil {
		t.Fatalf("Register(secondary) error = %v", err)
	}
	if _, err := manager.Register(context.Background(), primary); err != nil {
		t.Fatalf("Register(primary) error = %v", err)
	}

	scheduler := &accountInspectionScheduler{h: &Handler{authManager: manager}}
	scheduler.cleanupLegacyQuotaCaches(context.Background())

	raw, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var persisted map[string]any
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if _, ok := persisted["quota_cache"]; ok {
		t.Fatalf("persisted quota_cache = %#v, want removed", persisted["quota_cache"])
	}
	if persisted["project_id"] != "project-a" {
		t.Fatalf("persisted project_id = %#v, want project-a", persisted["project_id"])
	}
	projectIDs, ok := persisted["project_ids"].([]any)
	if !ok || len(projectIDs) != 2 || projectIDs[0] != "project-a" || projectIDs[1] != "project-b" {
		t.Fatalf("persisted project_ids = %#v, want original project list", persisted["project_ids"])
	}
	for _, auth := range manager.List() {
		if auth != nil && auth.Metadata != nil {
			if _, ok := auth.Metadata["quota_cache"]; ok {
				t.Fatalf("runtime auth %q quota_cache = %#v, want removed", auth.ID, auth.Metadata["quota_cache"])
			}
		}
	}
}

func TestClearInspectionAuthErrorClearsMetadataOnlyError(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	registered, err := manager.Register(context.Background(), &coreauth.Auth{
		Provider:      "antigravity",
		ID:            "antigravity-user",
		FileName:      "antigravity-user.json",
		Status:        coreauth.StatusActive,
		StatusMessage: "",
		Unavailable:   false,
		Metadata: map[string]any{
			"email": "user@example.com",
			"last_error": map[string]any{
				"code":        "inspection_probe_error",
				"http_status": 0,
				"message":     "antigravity quota unavailable",
				"retryable":   false,
			},
		},
	})
	if err != nil {
		t.Fatalf("Register auth error = %v", err)
	}

	scheduler := &accountInspectionScheduler{h: &Handler{authManager: manager}}
	scheduler.clearInspectionAuthError(context.Background(), accountFromAuth(registered))

	var got *coreauth.Auth
	for _, auth := range manager.List() {
		if auth.ID == registered.ID {
			got = auth
			break
		}
	}
	if got == nil {
		t.Fatal("updated auth not found")
	}
	if got.LastError != nil {
		t.Fatalf("LastError = %#v, want nil", got.LastError)
	}
	if _, ok := got.Metadata["last_error"]; ok {
		t.Fatalf("metadata last_error = %#v, want removed", got.Metadata["last_error"])
	}
	if got.Status != coreauth.StatusActive || got.StatusMessage != "" || got.Unavailable {
		t.Fatalf("status = %q message=%q unavailable=%v, want active/empty/false", got.Status, got.StatusMessage, got.Unavailable)
	}
	if got.Metadata["email"] != "user@example.com" {
		t.Fatalf("metadata email = %#v, want preserved", got.Metadata["email"])
	}
}

func TestAutoActionConfirmationDelaysExecution(t *testing.T) {
	scheduler := &accountInspectionScheduler{}
	result := testInspectionResult("quota", accountInspectionActionDisable, false, nil, true, "")
	settings := defaultAccountInspectionSettings()
	settings.AutoExecuteConfirmations = 2
	settings.AutoExecuteQuotaLimitDisable = true

	action := autoActionForResult(result, settings)
	if action != accountInspectionActionDisable {
		t.Fatalf("autoActionForResult() = %q, want disable", action)
	}
	confirmed, count, required := scheduler.confirmAutoAction(result, action, settings.AutoExecuteConfirmations)
	if confirmed || count != 1 || required != 2 {
		t.Fatalf("first confirmation = confirmed:%v count:%d required:%d, want false/1/2", confirmed, count, required)
	}
	confirmed, count, required = scheduler.confirmAutoAction(result, action, settings.AutoExecuteConfirmations)
	if !confirmed || count != 2 || required != 2 {
		t.Fatalf("second confirmation = confirmed:%v count:%d required:%d, want true/2/2", confirmed, count, required)
	}
	scheduler.clearAutoActionConfirmation(result)
	if len(scheduler.autoActionConfirmations) != 0 {
		t.Fatalf("autoActionConfirmations = %+v, want empty after clear", scheduler.autoActionConfirmations)
	}
}

func TestExecuteActionDisablesGeminiCLIPluginVirtualSourceFile(t *testing.T) {
	authDir := t.TempDir()
	authPath := filepath.Join(authDir, "gemini-cli.json")
	if err := os.WriteFile(authPath, []byte(`{"type":"gemini-cli","email":"user@example.com","project_id":"project-a","project_ids":["project-a","project-b"]}`), 0o600); err != nil {
		t.Fatalf("WriteFile auth error = %v", err)
	}

	manager := coreauth.NewManager(nil, nil, nil)
	primary := &coreauth.Auth{
		Provider:   "gemini-cli",
		ID:         "gemini-cli-primary",
		FileName:   "gemini-cli.json",
		Metadata:   map[string]any{"type": "gemini-cli", "email": "user@example.com", "project_id": "project-a"},
		Attributes: map[string]string{"path": authPath, "project_id": "project-a"},
		Storage:    &accountInspectionTestStorage{},
	}
	coreauth.MarkPluginVirtualAuth(primary, authPath, 0)
	secondary := &coreauth.Auth{
		Provider:   "gemini-cli",
		ID:         "gemini-cli-project-b",
		FileName:   "user-project-b.json",
		Metadata:   map[string]any{"type": "gemini-cli", "email": "user@example.com", "project_id": "project-b", "virtual": true},
		Attributes: map[string]string{"path": authPath, "project_id": "project-b", "runtime_only": "true"},
		Storage:    &accountInspectionTestStorage{},
	}
	coreauth.MarkPluginVirtualAuth(secondary, authPath, 1)
	registeredSecondary, err := manager.Register(context.Background(), secondary)
	if err != nil {
		t.Fatalf("Register secondary error = %v", err)
	}
	if _, err = manager.Register(context.Background(), primary); err != nil {
		t.Fatalf("Register primary error = %v", err)
	}

	scheduler := &accountInspectionScheduler{h: &Handler{authManager: manager}}
	err = scheduler.executeAction(context.Background(), accountInspectionResult{AuthIndex: registeredSecondary.Index}, accountInspectionActionDisable)
	if err != nil {
		t.Fatalf("executeAction(disable) error = %v", err)
	}

	raw, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatalf("ReadFile auth error = %v", err)
	}
	var saved map[string]any
	if err = json.Unmarshal(raw, &saved); err != nil {
		t.Fatalf("saved auth invalid JSON: %v", err)
	}
	if saved["disabled"] != true {
		t.Fatalf("saved disabled = %#v, want true in source auth file", saved["disabled"])
	}
	if saved["project_id"] != "project-a" {
		t.Fatalf("saved project_id = %#v, want primary project preserved", saved["project_id"])
	}
	projectIDs, ok := saved["project_ids"].([]any)
	if !ok || len(projectIDs) != 2 || projectIDs[0] != "project-a" || projectIDs[1] != "project-b" {
		t.Fatalf("saved project_ids = %#v, want original source project_ids preserved", saved["project_ids"])
	}
}

func TestManualActionsBindIdentityToCurrentSnapshot(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	registeredA, err := manager.Register(context.Background(), &coreauth.Auth{Provider: "codex", ID: "auth-a", FileName: "a.json", Metadata: map[string]any{"email": "a@example.com"}})
	if err != nil {
		t.Fatalf("Register(auth-a) error = %v", err)
	}
	registeredB, err := manager.Register(context.Background(), &coreauth.Auth{Provider: "codex", ID: "auth-b", FileName: "b.json", Metadata: map[string]any{"email": "b@example.com"}})
	if err != nil {
		t.Fatalf("Register(auth-b) error = %v", err)
	}
	resultA := accountFromAuth(registeredA).baseResult()
	scheduler := &accountInspectionScheduler{
		h:      &Handler{authManager: manager},
		status: accountInspectionStatus{Results: []accountInspectionResult{resultA}},
	}
	outcomes, err := scheduler.executeManualActions(context.Background(), []accountInspectionActionItem{{
		Key:       resultA.Key,
		FileName:  registeredB.FileName,
		AuthIndex: registeredB.Index,
		Provider:  registeredB.Provider,
		Action:    accountInspectionActionDisable,
	}})
	if err != nil {
		t.Fatalf("executeManualActions() error = %v", err)
	}
	if len(outcomes) != 1 || !outcomes[0].Success || outcomes[0].AuthIndex != registeredA.Index || outcomes[0].FileName != registeredA.FileName {
		t.Fatalf("outcomes = %+v, want canonical auth-a identity", outcomes)
	}
	gotA, _ := manager.GetByID(registeredA.ID)
	gotB, _ := manager.GetByID(registeredB.ID)
	if gotA == nil || !gotA.Disabled {
		t.Fatalf("auth-a = %#v, want disabled", gotA)
	}
	if gotB == nil || gotB.Disabled {
		t.Fatalf("auth-b = %#v, want enabled", gotB)
	}
}

func TestExecuteActionRejectsStaleRuntimeIdentity(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	registeredA, _ := manager.Register(context.Background(), &coreauth.Auth{Provider: "codex", ID: "auth-a", FileName: "a.json", Metadata: map[string]any{}})
	registeredB, _ := manager.Register(context.Background(), &coreauth.Auth{Provider: "codex", ID: "auth-b", FileName: "b.json", Metadata: map[string]any{}})
	result := accountFromAuth(registeredA).baseResult()
	result.AuthIndex = registeredB.Index
	scheduler := &accountInspectionScheduler{h: &Handler{authManager: manager}}
	if err := scheduler.executeAction(context.Background(), result, accountInspectionActionDisable); !errors.Is(err, errAccountInspectionResultStale) {
		t.Fatalf("executeAction() error = %v, want stale result", err)
	}
	gotA, _ := manager.GetByID(registeredA.ID)
	gotB, _ := manager.GetByID(registeredB.ID)
	if gotA.Disabled || gotB.Disabled {
		t.Fatalf("auths mutated after stale action: a=%v b=%v", gotA.Disabled, gotB.Disabled)
	}
}

func TestExecuteActionRejectsSharedPluginVirtualSourceDelete(t *testing.T) {
	authPath := filepath.Join(t.TempDir(), "gemini-cli.json")
	if err := os.WriteFile(authPath, []byte(`{"type":"gemini-cli","project_ids":["project-a","project-b"]}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	manager := coreauth.NewManager(nil, nil, nil)
	primary := &coreauth.Auth{Provider: "gemini-cli", ID: "primary", FileName: "gemini-cli.json", Metadata: map[string]any{}, Attributes: map[string]string{"path": authPath}}
	secondary := &coreauth.Auth{Provider: "gemini-cli", ID: "secondary", FileName: "project-b.json", Metadata: map[string]any{}, Attributes: map[string]string{"path": authPath, "runtime_only": "true"}}
	coreauth.MarkPluginVirtualAuth(primary, authPath, 0)
	coreauth.MarkPluginVirtualAuth(secondary, authPath, 1)
	registeredPrimary, _ := manager.Register(context.Background(), primary)
	_, _ = manager.Register(context.Background(), secondary)
	scheduler := &accountInspectionScheduler{h: &Handler{authManager: manager}}
	if err := scheduler.executeAction(context.Background(), accountFromAuth(registeredPrimary).baseResult(), accountInspectionActionDelete); !errors.Is(err, errAccountInspectionSharedSourceDelete) {
		t.Fatalf("executeAction(delete) error = %v, want shared-source rejection", err)
	}
	if _, err := os.Stat(authPath); err != nil {
		t.Fatalf("shared source file was removed: %v", err)
	}
}

func TestPluginVirtualInspectionErrorStaysOnTargetIdentity(t *testing.T) {
	authPath := filepath.Join(t.TempDir(), "gemini-cli.json")
	if err := os.WriteFile(authPath, []byte(`{"type":"gemini-cli","project_ids":["project-a","project-b"]}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	manager := coreauth.NewManager(nil, nil, nil)
	primary := &coreauth.Auth{Provider: "gemini-cli", ID: "primary", FileName: "gemini-cli.json", Metadata: map[string]any{"project_id": "project-a"}, Attributes: map[string]string{"path": authPath}}
	secondary := &coreauth.Auth{Provider: "gemini-cli", ID: "secondary", FileName: "project-b.json", Metadata: map[string]any{"project_id": "project-b"}, Attributes: map[string]string{"path": authPath, "runtime_only": "true"}}
	coreauth.MarkPluginVirtualAuth(primary, authPath, 0)
	coreauth.MarkPluginVirtualAuth(secondary, authPath, 1)
	registeredPrimary, _ := manager.Register(context.Background(), primary)
	registeredSecondary, _ := manager.Register(context.Background(), secondary)
	scheduler := &accountInspectionScheduler{h: &Handler{authManager: manager}}
	scheduler.syncInspectionAuthError(context.Background(), accountFromAuth(registeredSecondary), "inspection_probe_error", "project-b failed", 0)

	gotPrimary, _ := manager.GetByID(registeredPrimary.ID)
	gotSecondary, _ := manager.GetByID(registeredSecondary.ID)
	if gotPrimary.LastError != nil || gotPrimary.Status == coreauth.StatusError {
		t.Fatalf("primary auth was polluted: %#v", gotPrimary)
	}
	if gotSecondary.LastError == nil || gotSecondary.LastError.Code != "inspection_probe_error" || gotSecondary.Status != coreauth.StatusError {
		t.Fatalf("secondary auth error = %#v, want scoped inspection error", gotSecondary)
	}
	raw, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.Contains(string(raw), "last_error") {
		t.Fatalf("shared source contains identity-local error: %s", raw)
	}
}

func TestEnablePreservesNonInspectionLastError(t *testing.T) {
	auth := &coreauth.Auth{
		Disabled:    true,
		Status:      coreauth.StatusDisabled,
		LastError:   &coreauth.Error{Code: "upstream_refresh_error", Message: "refresh failed"},
		Metadata:    map[string]any{"last_error": map[string]any{"code": "upstream_refresh_error", "message": "refresh failed"}},
		Unavailable: true,
	}
	setAuthInspectionDisabledState(auth, false)
	if auth.Disabled || auth.Status != coreauth.StatusError || !auth.Unavailable {
		t.Fatalf("enabled auth state = disabled:%v status:%q unavailable:%v", auth.Disabled, auth.Status, auth.Unavailable)
	}
	if auth.LastError == nil || auth.LastError.Code != "upstream_refresh_error" {
		t.Fatalf("LastError = %#v, want preserved upstream error", auth.LastError)
	}
	if _, ok := auth.Metadata["last_error"]; !ok {
		t.Fatal("metadata last_error was removed")
	}
}

func TestQuotaSuccessStateIncludesParserMetadata(t *testing.T) {
	state := quotaSuccessState(map[string]any{"rawShapeHash": jsonShapeHash(`{"a":1,"items":[{"b":true}]}`)})
	if state["schemaVersion"] != 2 || state["parserVersion"] != accountInspectionQuotaParserVersion || state["status"] != "success" {
		t.Fatalf("quota state metadata = %+v", state)
	}
	if state["rawShapeHash"] == "" {
		t.Fatalf("rawShapeHash = %q, want populated", state["rawShapeHash"])
	}
}

func TestAntigravityQuotaURLsUseSummaryEndpoint(t *testing.T) {
	for _, url := range antigravityQuotaURLs() {
		if !strings.Contains(url, "retrieveUserQuotaSummary") {
			t.Fatalf("antigravity quota url = %q, want retrieveUserQuotaSummary", url)
		}
	}
}

func TestListAuthFilesFromDiskIncludesInspectionAndCodexPlanMetadata(t *testing.T) {
	authDir := t.TempDir()
	fileName := "codex-user.json"
	idToken := testCodexIDToken(t, map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id":                       "acct-1",
			"chatgpt_plan_type":                        "pro",
			"chatgpt_subscription_active_until":        float64(1790000000),
			"chatgpt_subscription_active_start":        float64(1780000000),
			"chatgpt_subscription_last_checked":        "2026-06-22T00:00:00Z",
			"rate_limit_reset_credits_available_count": float64(3),
		},
	})
	content := map[string]any{
		"type":     "codex",
		"email":    "user@example.com",
		"id_token": idToken,
		"last_error": map[string]any{
			"code":        "token_refresh_error",
			"message":     "refresh failed",
			"http_status": float64(401),
		},
	}
	raw, err := json.Marshal(content)
	if err != nil {
		t.Fatalf("Marshal auth content error = %v", err)
	}
	if err = os.WriteFile(filepath.Join(authDir, fileName), raw, 0o600); err != nil {
		t.Fatalf("WriteFile auth content error = %v", err)
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, nil)
	entry := firstAuthFileEntry(t, h)

	lastError, ok := entry["last_error"].(map[string]any)
	if !ok {
		t.Fatalf("last_error = %#v, want object", entry["last_error"])
	}
	if lastError["code"] != "token_refresh_error" || lastError["message"] != "refresh failed" {
		t.Fatalf("last_error = %#v, want token_refresh_error/refresh failed", lastError)
	}
	idTokenEntry, ok := entry["id_token"].(map[string]any)
	if !ok {
		t.Fatalf("id_token = %#v, want object", entry["id_token"])
	}
	if idTokenEntry["plan_type"] != "pro" || idTokenEntry["chatgpt_account_id"] != "acct-1" || idTokenEntry["chatgpt_subscription_active_until"] != float64(1790000000) {
		t.Fatalf("id_token entry = %#v, want codex plan/subscription claims", idTokenEntry)
	}
}

func testCodexIDToken(t *testing.T, claims map[string]any) string {
	t.Helper()
	header := map[string]any{"alg": "none", "typ": "JWT"}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("Marshal JWT header error = %v", err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("Marshal JWT claims error = %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(claimsJSON) + "."
}

func TestBuildAntigravityGroupsSupportsSummaryBuckets(t *testing.T) {
	body := `{
		"groups": [{
			"displayName": "Claude/GPT",
			"description": "premium models",
			"buckets": [
				{"bucketId": "weekly", "displayName": "Weekly", "window": "weekly", "remainingFraction": 0.75, "resetTime": "2026-01-02T03:04:05Z"},
				{"bucket_id": "five-hour", "display_name": "Five hour", "window": "5h", "remaining_fraction": 0.25, "reset_time": "2026-01-01T03:04:05Z"}
			]
		}]
	}`

	groups, err := buildAntigravityGroups(body)
	if err != nil {
		t.Fatalf("buildAntigravityGroups() error = %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("groups len = %d, want 1", len(groups))
	}
	if groups[0]["id"] != "claude-gpt" {
		t.Fatalf("group id = %#v, want claude-gpt", groups[0]["id"])
	}
	buckets, ok := groups[0]["buckets"].([]map[string]any)
	if !ok || len(buckets) != 2 {
		t.Fatalf("buckets = %#v, want two parsed buckets", groups[0]["buckets"])
	}
	if _, ok := groups[0]["remainingFraction"]; ok {
		t.Fatalf("remainingFraction is present on group, want latest bucket-only shape")
	}
	if buckets[0]["id"] != "weekly" || buckets[1]["id"] != "five-hour" {
		t.Fatalf("bucket order = %q/%q, want weekly/five-hour", buckets[0]["id"], buckets[1]["id"])
	}
	used := antigravityGroupUsedPercent(map[string]any{"buckets": buckets})
	if used == nil || *used != 75 {
		t.Fatalf("used percent = %v, want 75", used)
	}
}

func TestBuildAntigravityGroupsCanonicalizesLatestGroups(t *testing.T) {
	body := `{
		"groups": [
			{
				"buckets": [
					{"bucketId": "gemini-weekly", "displayName": "Weekly Limit", "window": "weekly", "resetTime": "2026-06-20T00:39:10Z", "remainingFraction": 0.9997293},
					{"bucketId": "gemini-5h", "displayName": "Five Hour Limit", "window": "5h", "resetTime": "2026-06-17T15:04:15Z", "remainingFraction": 1}
				],
				"displayName": "Gemini Models",
				"description": "Models within this group: Gemini Flash, Gemini Pro"
			},
			{
				"buckets": [
					{"bucketId": "3p-weekly", "displayName": "Weekly Limit", "window": "weekly", "resetTime": "2026-06-24T04:38:44Z", "remainingFraction": 0.9914995},
					{"bucketId": "3p-5h", "displayName": "Five Hour Limit", "window": "5h", "resetTime": "2026-06-17T12:12:15Z", "remainingFraction": 0.999886}
				],
				"displayName": "Claude and GPT models",
				"description": "Models within this group: Claude Opus, Claude Sonnet, GPT-OSS"
			}
		]
	}`

	groups, err := buildAntigravityGroups(body)
	if err != nil {
		t.Fatalf("buildAntigravityGroups() error = %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("groups len = %d, want 2", len(groups))
	}
	if groups[0]["id"] != "gemini" || groups[1]["id"] != "claude-gpt" {
		t.Fatalf("group ids = %#v/%#v, want gemini/claude-gpt", groups[0]["id"], groups[1]["id"])
	}
	used := antigravityUsedPercent(groups, accountInspectionAntigravityQuotaModeClaudeGpt)
	if used == nil || math.Abs(*used-0.85005) > 0.0001 {
		t.Fatalf("claude-gpt used percent = %v, want about 0.85005", used)
	}
}

func TestBuildGeminiCLIQuotaBucketsGroupsModelFamiliesConservatively(t *testing.T) {
	body := `{
		"buckets": [
			{"modelId": "gemini-2.5-flash-lite_vertex", "tokenType": "input", "remainingFraction": 0.8, "remainingAmount": "80", "resetTime": "2026-06-22T01:00:00Z"},
			{"model_id": "gemini-4-flash-preview", "token_type": "input", "remaining_fraction": 0.6, "remaining_amount": 60, "reset_time": "2026-06-22T02:00:00Z"},
			{"modelId": "gemini-2.5-flash", "tokenType": "input", "remainingFraction": 0.4, "remainingAmount": 40, "resetTime": "2026-06-22T03:00:00Z"},
			{"modelId": "gemini-4-pro-preview", "tokenType": "output", "remainingFraction": 0.2, "remainingAmount": 20, "resetTime": "2026-06-22T04:00:00Z"},
			{"modelId": "gemini-2.0-flash", "tokenType": "input", "remainingFraction": 0}
		]
	}`

	buckets, used, err := buildGeminiCLIQuotaBuckets(body)
	if err != nil {
		t.Fatalf("buildGeminiCLIQuotaBuckets() error = %v", err)
	}
	if len(buckets) != 3 {
		t.Fatalf("buckets len = %d, want 3: %#v", len(buckets), buckets)
	}
	if buckets[0]["id"] != "gemini-flash-lite-series-input" || buckets[1]["id"] != "gemini-flash-series-input" || buckets[2]["id"] != "gemini-pro-series-output" {
		t.Fatalf("bucket order/ids = %#v", buckets)
	}
	if buckets[1]["remainingFraction"] != 0.4 || buckets[1]["remainingAmount"] != 40.0 {
		t.Fatalf("flash series remaining = %#v/%#v, want conservative 0.4/40", buckets[1]["remainingFraction"], buckets[1]["remainingAmount"])
	}
	modelIDs, ok := buckets[1]["modelIds"].([]string)
	if !ok || len(modelIDs) != 2 || modelIDs[0] != "gemini-4-flash-preview" || modelIDs[1] != "gemini-2.5-flash" {
		t.Fatalf("flash series modelIds = %#v", buckets[1]["modelIds"])
	}
	if used == nil || math.Abs(*used-80) > 0.000001 {
		t.Fatalf("used = %v, want 80", used)
	}
}

func TestBuildGeminiCLISubscriptionReadsPaidTierCredits(t *testing.T) {
	payload := map[string]any{
		"currentTier": map[string]any{
			"id":   "free-tier",
			"name": "Free",
		},
		"paidTier": map[string]any{
			"id": "g1-ultra-tier",
			"availableCredits": []any{
				map[string]any{"creditType": "OTHER", "creditAmount": float64(5)},
				map[string]any{"creditType": "GOOGLE_ONE_AI", "creditAmount": "123"},
			},
		},
	}

	subscription := buildGeminiCLISubscription(payload)
	if subscription["plan"] != "ultra" || subscription["tierId"] != "g1-ultra-tier" || subscription["tierLabel"] != "Google One AI Ultra" {
		t.Fatalf("subscription = %#v, want ultra tier", subscription)
	}
	if subscription["creditBalance"] != float64(123) {
		t.Fatalf("creditBalance = %#v, want 123", subscription["creditBalance"])
	}
}

func TestGeminiCLIProjectIDReadsPluginMetadata(t *testing.T) {
	auth := &coreauth.Auth{
		Provider: "gemini-cli",
		Attributes: map[string]string{
			"gemini_virtual_project": "project-a, project-b",
		},
	}

	if got := geminiCLIProjectID(auth); got != "project-a" {
		t.Fatalf("geminiCLIProjectID() = %q, want project-a", got)
	}
}

func TestBuildAntigravityGroupsSupportsWrappedBody(t *testing.T) {
	body := `{
		"body": "{\"groups\":[{\"displayName\":\"Claude/GPT\",\"buckets\":[{\"bucketId\":\"weekly\",\"displayName\":\"Weekly\",\"window\":\"weekly\",\"remainingFraction\":0.5}]}]}"
	}`

	groups, err := buildAntigravityGroups(body)
	if err != nil {
		t.Fatalf("buildAntigravityGroups() error = %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("groups len = %d, want 1", len(groups))
	}
	buckets, ok := groups[0]["buckets"].([]map[string]any)
	if !ok || len(buckets) != 1 || buckets[0]["remainingFraction"] != 0.5 {
		t.Fatalf("wrapped buckets = %#v, want one 0.5 bucket", groups[0]["buckets"])
	}
}

func TestBuildAntigravitySubscriptionMapsPaidTierPlan(t *testing.T) {
	payload := map[string]any{
		"currentTier": map[string]any{"id": "free-tier", "name": "Free"},
		"paidTier": map[string]any{
			"id":   "g1-ultra-tier",
			"name": "Ultra",
			"availableCredits": []any{
				map[string]any{
					"creditType":                  "AI",
					"creditAmount":                float64(20),
					"minimumCreditAmountForUsage": "1",
				},
			},
		},
	}

	subscription := buildAntigravitySubscription(payload)
	if subscription == nil {
		t.Fatal("buildAntigravitySubscription() = nil, want subscription")
	}
	if subscription["plan"] != "ultra" || subscription["tierId"] != "g1-ultra-tier" || subscription["source"] != "paid" {
		t.Fatalf("subscription = %#v, want ultra paid tier", subscription)
	}
	credits, ok := subscription["availableCredits"].([]map[string]any)
	if !ok || len(credits) != 1 || credits[0]["creditType"] != "AI" {
		t.Fatalf("availableCredits = %#v, want AI credit entry", subscription["availableCredits"])
	}
}

func TestAntigravityUsedPercentFallsBackWhenClaudeGroupNameChanges(t *testing.T) {
	groups := []map[string]any{{
		"id":    "quota-group-1",
		"label": "Premium Models",
		"buckets": []map[string]any{{
			"id":                "weekly",
			"label":             "Weekly",
			"remainingFraction": 0.35,
		}},
	}}

	used := antigravityUsedPercent(groups, accountInspectionAntigravityQuotaModeClaudeGpt)
	if used == nil || *used != 65 {
		t.Fatalf("used percent = %v, want 65 fallback from buckets", used)
	}
	if model := selectAntigravityDeepProbeModel(groups, ""); model != "claude-sonnet-4-6" {
		t.Fatalf("deep probe model = %q, want default claude-sonnet-4-6", model)
	}
}

func TestBuildAntigravityGroupsRejectsLegacyModelsShape(t *testing.T) {
	body := `{
		"models": {
			"claude-sonnet-4-6": {"quotaInfo": {"remainingFraction": 0.4, "resetTime": "2026-01-02T03:04:05Z"}},
			"gpt-oss-120b-medium": {"quota_info": {"remaining_fraction": 0.8}}
		}
	}`

	if _, err := buildAntigravityGroups(body); err == nil {
		t.Fatalf("buildAntigravityGroups() error = nil, want legacy models shape rejected")
	}
}

func TestBuildCodexWindowsClassifiesTeamMonthlyWindows(t *testing.T) {
	body := `{
		"rate_limit": {
			"primary_window": {"limit_window_seconds": 18000, "used_percent": 12.5, "reset_after_seconds": 60},
			"secondary_window": {"limit_window_seconds": 2592000, "used_percent": 42.5, "reset_after_seconds": 120},
			"allowed": true
		},
		"code_review_rate_limit": {
			"primary_window": {"limit_window_seconds": 18000, "used_percent": 5},
			"secondary_window": {"limit_window_seconds": 2419200, "used_percent": 88}
		},
		"additional_rate_limits": [{
			"limit_name": "Premium Tokens",
			"rate_limit": {
				"primary_window": {"limit_window_seconds": 18000, "used_percent": 11},
				"secondary_window": {"limit_window_seconds": 2678400, "used_percent": 22}
			}
		}]
	}`

	_, windows, used := buildCodexWindows(body)
	if used == nil || *used != 88 {
		t.Fatalf("used percent = %v, want 88", used)
	}
	labelsByID := make(map[string]string)
	for _, window := range windows {
		id, _ := window["id"].(string)
		labelKey, _ := window["labelKey"].(string)
		labelsByID[id] = labelKey
	}
	if labelsByID["monthly"] != "codex_quota.team_secondary_window" {
		t.Fatalf("monthly label = %q, want team secondary", labelsByID["monthly"])
	}
	if labelsByID["code-review-monthly"] != "codex_quota.code_review_team_secondary_window" {
		t.Fatalf("code review monthly label = %q, want code review team secondary", labelsByID["code-review-monthly"])
	}
	if labelsByID["premium-tokens-monthly-0"] != "codex_quota.additional_team_secondary_window" {
		t.Fatalf("additional monthly label = %q, want additional team secondary", labelsByID["premium-tokens-monthly-0"])
	}
}

func TestCodexQuotaStateValuesIncludesSubscriptionAndResetCredits(t *testing.T) {
	auth := &coreauth.Auth{
		Metadata: map[string]any{
			"id_token": map[string]any{
				"chatgpt_subscription_active_until": float64(1790000000),
			},
		},
		Attributes: map[string]string{
			"plan_type": "plus",
		},
	}
	payload := map[string]any{
		"rate_limit_reset_credits": map[string]any{
			"available_count": float64(2),
		},
	}
	windows := []map[string]any{{"id": "five-hour"}}

	values := codexQuotaStateValues(auth, payload, windows, `{"rate_limit":{}}`)
	if values["planType"] != "plus" {
		t.Fatalf("planType = %#v, want plus", values["planType"])
	}
	if values["subscriptionActiveUntil"] != float64(1790000000) {
		t.Fatalf("subscriptionActiveUntil = %#v, want id token timestamp", values["subscriptionActiveUntil"])
	}
	if values["rateLimitResetCreditsAvailableCount"] != float64(2) {
		t.Fatalf("rateLimitResetCreditsAvailableCount = %#v, want 2", values["rateLimitResetCreditsAvailableCount"])
	}
	if values["rawShapeHash"] == "" {
		t.Fatalf("rawShapeHash = %q, want populated", values["rawShapeHash"])
	}
}

func TestBuildXAIBillingSummaryParsesBillingConfig(t *testing.T) {
	body := `{
		"config": {
			"monthlyLimit": {"val": 10000},
			"used": {"val": 2500},
			"onDemandCap": {"val": 5000},
			"billingPeriodStart": "2026-06-01T00:00:00Z",
			"billingPeriodEnd": "2026-07-01T00:00:00Z"
		}
	}`

	billing, used, err := buildXAIBillingSummary(body)
	if err != nil {
		t.Fatalf("buildXAIBillingSummary() error = %v", err)
	}
	if used == nil || *used != 25 {
		t.Fatalf("used percent = %v, want 25", used)
	}
	if billing["monthlyLimitCents"] != 10000.0 || billing["usedCents"] != 2500.0 || billing["onDemandCapCents"] != 5000.0 {
		t.Fatalf("billing cents = %+v, want parsed cent values", billing)
	}
	if billing["billingPeriodEnd"] != "2026-07-01T00:00:00Z" {
		t.Fatalf("billing period end = %#v", billing["billingPeriodEnd"])
	}
	if billing["usedPercent"] != 25.0 {
		t.Fatalf("billing usedPercent = %#v, want 25", billing["usedPercent"])
	}
	if billing["includedUsedCents"] != 2500.0 {
		t.Fatalf("includedUsedCents = %#v, want 2500", billing["includedUsedCents"])
	}
	if billing["onDemandUsedCents"] != 0.0 {
		t.Fatalf("onDemandUsedCents = %#v, want 0", billing["onDemandUsedCents"])
	}
}

func TestBuildXAIBillingSummarySupportsSnakeCaseAndNumericValues(t *testing.T) {
	body := `{
		"config": {
			"monthly_limit": 8000,
			"used": 6000,
			"on_demand_cap": 12000,
			"billing_period_end": "2026-07-01T00:00:00Z"
		}
	}`

	billing, used, err := buildXAIBillingSummary(body)
	if err != nil {
		t.Fatalf("buildXAIBillingSummary() error = %v", err)
	}
	if used == nil || *used != 75 {
		t.Fatalf("used percent = %v, want 75", used)
	}
	if billing["monthlyLimitCents"] != 8000.0 || billing["usedCents"] != 6000.0 || billing["onDemandCapCents"] != 12000.0 {
		t.Fatalf("billing cents = %+v, want parsed snake_case numeric values", billing)
	}
}

func TestBuildXAIBillingSummarySupportsWeeklyCreditsShape(t *testing.T) {
	body := `{
		"config": {
			"currentPeriod": {
				"type": "weekly",
				"start": "2026-07-06T00:00:00Z",
				"end": "2026-07-13T00:00:00Z"
			},
			"creditUsagePercent": 64.5,
			"productUsage": [
				{"product": "Grok", "usagePercent": 50},
				{"product": "Think", "usage_percent": "82.25"}
			]
		}
	}`

	billing, used, err := buildXAIBillingSummary(body)
	if err != nil {
		t.Fatalf("buildXAIBillingSummary() error = %v", err)
	}
	if used == nil || *used != 64.5 {
		t.Fatalf("used percent = %v, want 64.5", used)
	}
	if billing["periodType"] != "weekly" || billing["usagePercent"] != 64.5 {
		t.Fatalf("weekly billing = %+v, want weekly usage percent", billing)
	}
	if billing["periodEnd"] != "2026-07-13T00:00:00Z" {
		t.Fatalf("periodEnd = %#v, want weekly period end", billing["periodEnd"])
	}
	usage, ok := billing["productUsage"].([]map[string]any)
	if !ok || len(usage) != 2 {
		t.Fatalf("productUsage = %#v, want 2 normalized items", billing["productUsage"])
	}
	if usage[1]["usagePercent"] != 82.25 {
		t.Fatalf("second usagePercent = %#v, want 82.25", usage[1]["usagePercent"])
	}
}

func TestMergeXAIBillingSummariesCombinesWeeklyAndMonthly(t *testing.T) {
	weekly, _, err := buildXAIBillingSummary(`{
		"config": {
			"current_period": {"type": "weekly", "end": "2026-07-13T00:00:00Z"},
			"credit_usage_percent": 10,
			"product_usage": [{"product": "Grok", "usage_percent": 10}]
		}
	}`)
	if err != nil {
		t.Fatalf("weekly build error = %v", err)
	}
	monthly, _, err := buildXAIBillingSummary(`{
		"config": {
			"monthly_limit": 150000,
			"used": 160000,
			"on_demand_cap": 20000,
			"billing_period_end": "2026-08-01T00:00:00Z"
		}
	}`)
	if err != nil {
		t.Fatalf("monthly build error = %v", err)
	}

	merged := mergeXAIBillingSummaries(weekly, monthly)
	if merged["periodType"] != "weekly" || merged["usagePercent"] != 10.0 {
		t.Fatalf("merged weekly fields = %+v", merged)
	}
	if merged["monthlyLimitCents"] != 150000.0 || merged["includedUsedCents"] != 150000.0 || merged["onDemandUsedCents"] != 10000.0 {
		t.Fatalf("merged monthly fields = %+v", merged)
	}
	if merged["usedPercent"] != 100.0 || merged["onDemandUsedPercent"] != 50.0 {
		t.Fatalf("merged percentages = %+v", merged)
	}
	usage, ok := merged["productUsage"].([]map[string]any)
	if !ok || len(usage) != 1 || usage[0]["product"] != "Grok" {
		t.Fatalf("merged productUsage = %#v, want weekly product usage", merged["productUsage"])
	}
}

func TestXAIRequestHeadersIncludeGrokClientAndUserID(t *testing.T) {
	auth := &coreauth.Auth{
		Provider: "xai",
		Metadata: map[string]any{"sub": "user-123"},
	}
	headers := xaiRequestHeaders(auth)
	if headers["x-xai-token-auth"] != "xai-grok-cli" {
		t.Fatalf("x-xai-token-auth = %q", headers["x-xai-token-auth"])
	}
	if headers["x-grok-client-version"] != "0.2.91" {
		t.Fatalf("x-grok-client-version = %q", headers["x-grok-client-version"])
	}
	if headers["x-userid"] != "user-123" {
		t.Fatalf("x-userid = %q, want user-123", headers["x-userid"])
	}
}

func TestXAIInspectionUsesExecutorHTTPRequest(t *testing.T) {
	if !accountInspectionShouldUseExecutorHTTPRequest(&coreauth.Auth{Provider: "xai"}) {
		t.Fatal("accountInspectionShouldUseExecutorHTTPRequest(xai) = false, want true")
	}
}

func TestXAIBillingURLMatchesUpstreamQuotaConfig(t *testing.T) {
	if got := xaiBillingURL(); got != "https://cli-chat-proxy.grok.com/v1/billing" {
		t.Fatalf("xaiBillingURL() = %q, want upstream billing endpoint", got)
	}
	if got := xaiBillingWeeklyURL(); got != "https://cli-chat-proxy.grok.com/v1/billing?format=credits" {
		t.Fatalf("xaiBillingWeeklyURL() = %q, want upstream weekly billing endpoint", got)
	}
}

func TestXAIDeepProbeDefaultsAndNormalization(t *testing.T) {
	defaults := defaultAccountInspectionSettings()
	if defaults.XAIDeepProbeEnabled {
		t.Fatal("xAI deep probe should be disabled by default")
	}
	if defaults.XAIDeepProbeModel != "grok-4.5" {
		t.Fatalf("default xAI deep probe model = %q, want grok-4.5", defaults.XAIDeepProbeModel)
	}

	normalized := normalizeAccountInspectionSchedule(accountInspectionSchedule{Settings: accountInspectionSettings{
		XAIDeepProbeEnabled: true,
		XAIDeepProbeModel:   "   ",
	}})
	if !normalized.Settings.XAIDeepProbeEnabled || normalized.Settings.XAIDeepProbeModel != "grok-4.5" {
		t.Fatalf("normalized xAI deep probe settings = enabled:%v model:%q", normalized.Settings.XAIDeepProbeEnabled, normalized.Settings.XAIDeepProbeModel)
	}
}

func TestXAIResponsesURLUsesConfiguredBaseURL(t *testing.T) {
	if got := xaiResponsesURL(nil); got != "https://api.x.ai/v1/responses" {
		t.Fatalf("xaiResponsesURL(nil) = %q", got)
	}
	auth := &coreauth.Auth{Attributes: map[string]string{"base_url": "https://xai.example/v1/"}}
	if got := xaiResponsesURL(auth); got != "https://xai.example/v1/responses" {
		t.Fatalf("xaiResponsesURL(custom) = %q", got)
	}
}

func TestBuildXAIDeepProbeBodyUsesMinimalResponsesRequest(t *testing.T) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(buildXAIDeepProbeBody(" grok-4.3 ")), &payload); err != nil {
		t.Fatalf("buildXAIDeepProbeBody() JSON error = %v", err)
	}
	if payload["model"] != "grok-4.3" || payload["input"] != "ping" || payload["stream"] != true || payload["max_output_tokens"] != float64(1) {
		t.Fatalf("deep probe payload = %#v", payload)
	}
}

func TestClassifyXAIDeepProbeResponse(t *testing.T) {
	tests := []struct {
		name string
		resp accountInspectionHTTPResult
		want accountInspectionDeepProbeStatus
	}{
		{
			name: "completed sse",
			resp: accountInspectionHTTPResult{StatusCode: http.StatusOK, Body: "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"},
			want: accountInspectionDeepProbeSuccess,
		},
		{
			name: "output capped after successful execution",
			resp: accountInspectionHTTPResult{StatusCode: http.StatusOK, Body: `data: {"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}` + "\n\n"},
			want: accountInspectionDeepProbeSuccess,
		},
		{
			name: "free usage exhausted",
			resp: accountInspectionHTTPResult{StatusCode: http.StatusTooManyRequests, Body: `{"code":"subscription:free-usage-exhausted","error":"You've used all the included free usage for now."}`},
			want: accountInspectionDeepProbeQuota,
		},
		{
			name: "unauthorized",
			resp: accountInspectionHTTPResult{StatusCode: http.StatusUnauthorized, Body: `{"error":{"message":"invalid token"}}`},
			want: accountInspectionDeepProbeAuthError,
		},
		{
			name: "content filter incomplete response",
			resp: accountInspectionHTTPResult{StatusCode: http.StatusOK, Body: `data: {"type":"response.incomplete","response":{"incomplete_details":{"reason":"content_filter"}}}` + "\n\n"},
			want: accountInspectionDeepProbeTransientError,
		},
		{
			name: "missing terminal response",
			resp: accountInspectionHTTPResult{StatusCode: http.StatusOK, Body: "data: {\"type\":\"response.created\"}\n\n"},
			want: accountInspectionDeepProbeTransientError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := classifyXAIDeepProbeResponse(tt.resp)
			if got != tt.want {
				t.Fatalf("classifyXAIDeepProbeResponse() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRunXAIDeepProbeWithRetryRecoversFromEmptyResponse(t *testing.T) {
	attempts := 0
	resp, status, message, err := runXAIDeepProbeWithRetry(context.Background(), 0, 0, func() (accountInspectionHTTPResult, error) {
		attempts++
		if attempts == 1 {
			return accountInspectionHTTPResult{StatusCode: http.StatusOK}, nil
		}
		return accountInspectionHTTPResult{
			StatusCode: http.StatusOK,
			Body:       `data: {"type":"response.completed","response":{"status":"completed"}}` + "\n\n",
		}, nil
	})
	if err != nil || status != accountInspectionDeepProbeSuccess || message != "" {
		t.Fatalf("runXAIDeepProbeWithRetry() = resp:%+v status:%q message:%q err:%v, want success", resp, status, message, err)
	}
	if attempts != 2 {
		t.Fatalf("runXAIDeepProbeWithRetry() attempts = %d, want 2", attempts)
	}
}

func TestRunXAIDeepProbeWithRetryRecoversFromTransportError(t *testing.T) {
	attempts := 0
	_, status, _, err := runXAIDeepProbeWithRetry(context.Background(), 0, 0, func() (accountInspectionHTTPResult, error) {
		attempts++
		if attempts == 1 {
			return accountInspectionHTTPResult{}, errors.New("temporary transport failure")
		}
		return accountInspectionHTTPResult{
			StatusCode: http.StatusOK,
			Body:       `data: {"type":"response.incomplete","response":{"incomplete_details":{"reason":"max_output_tokens"}}}` + "\n\n",
		}, nil
	})
	if err != nil || status != accountInspectionDeepProbeSuccess || attempts != 2 {
		t.Fatalf("runXAIDeepProbeWithRetry() = status:%q attempts:%d err:%v, want success after 2 attempts", status, attempts, err)
	}
}

func TestRunXAIDeepProbeWithRetryDoesNotRetryContentFilter(t *testing.T) {
	attempts := 0
	_, status, message, err := runXAIDeepProbeWithRetry(context.Background(), 0, 0, func() (accountInspectionHTTPResult, error) {
		attempts++
		return accountInspectionHTTPResult{
			StatusCode: http.StatusOK,
			Body:       `data: {"type":"response.incomplete","response":{"incomplete_details":{"reason":"content_filter"}}}` + "\n\n",
		}, nil
	})
	if err != nil || status != accountInspectionDeepProbeTransientError || !strings.Contains(message, "content_filter") {
		t.Fatalf("runXAIDeepProbeWithRetry() = status:%q message:%q err:%v, want content_filter transient error", status, message, err)
	}
	if attempts != 1 {
		t.Fatalf("runXAIDeepProbeWithRetry() attempts = %d, want 1", attempts)
	}
}

func TestAcquireXAIDeepProbeSerializesAndHonorsCancellation(t *testing.T) {
	scheduler := &accountInspectionScheduler{}
	releaseFirst, err := scheduler.acquireXAIDeepProbe(context.Background())
	if err != nil {
		t.Fatalf("first acquireXAIDeepProbe() error = %v", err)
	}

	secondAcquired := make(chan func(), 1)
	secondStarted := make(chan struct{})
	go func() {
		close(secondStarted)
		release, acquireErr := scheduler.acquireXAIDeepProbe(context.Background())
		if acquireErr == nil {
			secondAcquired <- release
		}
	}()
	<-secondStarted
	select {
	case release := <-secondAcquired:
		release()
		releaseFirst()
		t.Fatal("second xAI deep probe acquired before the first probe released")
	case <-time.After(50 * time.Millisecond):
	}

	canceledCtx, cancel := context.WithCancel(context.Background())
	canceledResult := make(chan error, 1)
	go func() {
		_, acquireErr := scheduler.acquireXAIDeepProbe(canceledCtx)
		canceledResult <- acquireErr
	}()
	cancel()
	select {
	case acquireErr := <-canceledResult:
		if !errors.Is(acquireErr, context.Canceled) {
			releaseFirst()
			t.Fatalf("canceled acquireXAIDeepProbe() error = %v, want context.Canceled", acquireErr)
		}
	case <-time.After(time.Second):
		releaseFirst()
		t.Fatal("canceled acquireXAIDeepProbe() did not return")
	}

	releaseFirst()
	select {
	case release := <-secondAcquired:
		release()
	case <-time.After(time.Second):
		t.Fatal("second xAI deep probe did not acquire after release")
	}
}

func TestSummarizeInspectionHTTPBodyExtractsCompleteNestedMessage(t *testing.T) {
	want := strings.TrimSpace(strings.Repeat("capacity unavailable ", 20))
	body, err := json.Marshal(map[string]any{
		"error": map[string]any{
			"code":    http.StatusServiceUnavailable,
			"message": want,
			"status":  "UNAVAILABLE",
		},
	})
	if err != nil {
		t.Fatalf("marshal error payload: %v", err)
	}
	if got := summarizeInspectionHTTPBody(string(body)); got != want {
		t.Fatalf("summarizeInspectionHTTPBody() = %q, want complete nested message %q", got, want)
	}
	if got := inspectionHTTPErrorDetail("  " + string(body) + "\n"); got != string(body) {
		t.Fatalf("inspectionHTTPErrorDetail() = %q, want complete body %q", got, string(body))
	}
}

func TestWithInspectionHTTPErrorDetailPreservesCompleteResponse(t *testing.T) {
	body := `{"error":{"code":"invalid_token","message":"credential rejected"},"request_id":"req-123"}`
	decision := withInspectionHTTPErrorDetail(
		authErrorDecision(accountInspectionAccount{}, http.StatusUnauthorized),
		"  "+body+"\n",
	)
	if decision.ErrorDetail != body {
		t.Fatalf("ErrorDetail = %q, want complete response %q", decision.ErrorDetail, body)
	}
	if decision.Action != accountInspectionActionDisable {
		t.Fatalf("Action = %q, want %q", decision.Action, accountInspectionActionDisable)
	}
}

func TestXAIDeepProbeErrorCodeCanBeCleared(t *testing.T) {
	decision := accountInspectionDecision{DeepProbeStatus: accountInspectionDeepProbeTransientError}
	if got := accountInspectionDecisionErrorCode("xai", decision, nil); got != "xai_deep_probe_error" {
		t.Fatalf("xAI deep probe error code = %q", got)
	}
	if !isInspectionAuthErrorCode("xai_deep_probe_error") {
		t.Fatal("xAI deep probe error code should be clearable after recovery")
	}
}
