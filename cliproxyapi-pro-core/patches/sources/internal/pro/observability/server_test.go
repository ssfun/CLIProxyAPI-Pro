package observability

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/pro/observability/internalusage"
	probackup "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/backup"
)

type failingImportReader struct{}

func (failingImportReader) Read([]byte) (int, error) {
	return 0, errors.New("forced import read failure")
}

func registerTestAPIKeyPolicyDataDomain(t *testing.T) {
	t.Helper()
	unregister := RegisterDataDomainContributor("api-key-policy", DataDomainContribution{
		InventoryFunc: func(context.Context, *Store) DataDomainInventory {
			return DataDomainInventory{Owner: "api-key-policy", BackupIncluded: true, RestoreMode: "replace", Available: true}
		},
		BackupRecordTypes: []string{"api_key_policies"},
	})
	t.Cleanup(unregister)
}

func TestUsageImportDispatchesPluginDataDomainRecords(t *testing.T) {
	const recordType = "test_plugin_restore_record"
	called := false
	unregister := RegisterDataDomainContributor("test-plugin-restore", DataDomainContribution{
		BackupRecordTypes: []string{recordType},
		BackupImporter: func(_ context.Context, _ *Store, gotType string, raw []byte) error {
			called = gotType == recordType && bytes.Contains(raw, []byte(`"value":1`))
			return nil
		},
	})
	t.Cleanup(unregister)
	record := []byte(`{"record_type":"test_plugin_restore_record","version":1,"value":1}`)
	digest := sha256.Sum256(append(append([]byte(nil), record...), '\n'))
	manifest, err := json.Marshal(map[string]any{
		"record_type": "backup_manifest", "version": 1, "records": 1, "sha256": fmt.Sprintf("%x", digest),
	})
	if err != nil {
		t.Fatal(err)
	}
	backup := append(append(append([]byte(nil), manifest...), '\n'), append(record, '\n')...)
	server := NewServer(Config{Enabled: true, BatchSize: 100}, openTestStore(t))
	router := gin.New()
	router.POST("/import", server.handleUsageImport)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/import", bytes.NewReader(backup)))
	if recorder.Code != http.StatusOK || !called {
		t.Fatalf("plugin restore status=%d called=%v body=%s", recorder.Code, called, recorder.Body.String())
	}
}

func TestUsageImportPreviewReportsPolicyReplacementAssociationAndLegacyPreservation(t *testing.T) {
	defer func(previous *probackup.Coordinator) { probackup.Default = previous }(probackup.Default)
	coordinator := probackup.NewCoordinator()
	probackup.Default = coordinator
	registerTestAPIKeyPolicyDataDomain(t)
	current := []byte(`{"schema_version":2,"policies":[{"id":"current"}],"audits":[]}`)
	target := []byte(`{"schema_version":2,"policies":[{"id":"target-a"},{"id":"target-b"}],"audits":[]}`)
	coordinator.RegisterAPIKeyPolicies(
		func() ([]byte, bool, error) { return current, true, nil },
		func(context.Context, []byte) error { return nil },
		func(_ context.Context, payload []byte) (probackup.PolicyBackupPreview, error) {
			if bytes.Equal(payload, current) {
				return probackup.PolicyBackupPreview{CurrentDisabledKeys: 2, TargetDisabledKeys: 2, TargetPolicies: 1, TargetProfiles: 3, CurrentTakeoverEnabled: true, TargetTakeoverEnabled: true}, nil
			}
			if !bytes.Equal(payload, target) {
				t.Fatalf("preview payload = %s", payload)
			}
			return probackup.PolicyBackupPreview{HasPolicies: true, ReplacePolicies: 1, ReplaceProfiles: 3, TargetPolicies: 2, TargetProfiles: 4, AssociatedPolicies: 1, OrphanedPolicies: 1, CurrentTakeoverEnabled: true, TargetTakeoverEnabled: false}, nil
		},
	)
	backup, err := coordinator.ExportJSONL(context.Background(), nil, func(context.Context) ([]byte, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	// Replace the coordinator-produced current payload with a valid manifest for
	// the staged target so the transport verification is exercised too.
	line, _ := json.Marshal(map[string]any{"record_type": "api_key_policies", "version": 2, "policies": json.RawMessage(target), "exported_at_ms": 1})
	digest := sha256.Sum256(append(append([]byte(nil), line...), '\n'))
	manifest, _ := json.Marshal(map[string]any{"record_type": "backup_manifest", "version": 1, "records": 1, "sha256": fmt.Sprintf("%x", digest), "exported_at_ms": 1})
	backup = append(append(append([]byte(nil), manifest...), '\n'), append(line, '\n')...)

	previewRecorder := httptest.NewRecorder()
	testUsageRouter(openTestStore(t)).ServeHTTP(previewRecorder, httptest.NewRequest(http.MethodPost, "/usage/import/preview", bytes.NewReader(backup)))
	if previewRecorder.Code != http.StatusOK || !strings.Contains(previewRecorder.Body.String(), `"associatedPolicies":1`) || !strings.Contains(previewRecorder.Body.String(), `"orphanedPolicies":1`) || !strings.Contains(previewRecorder.Body.String(), `"currentTakeoverEnabled":true`) || !strings.Contains(previewRecorder.Body.String(), `"targetTakeoverEnabled":false`) || !strings.Contains(previewRecorder.Body.String(), `"restoresAPIKeys":false`) {
		t.Fatalf("preview response = %d %s", previewRecorder.Code, previewRecorder.Body.String())
	}

	legacyRecorder := httptest.NewRecorder()
	testUsageRouter(openTestStore(t)).ServeHTTP(legacyRecorder, httptest.NewRequest(http.MethodPost, "/usage/import/preview?allow_legacy=1", strings.NewReader(`{"model":"old"}`)))
	if legacyRecorder.Code != http.StatusOK || !strings.Contains(legacyRecorder.Body.String(), `"preservePolicies":1`) || !strings.Contains(legacyRecorder.Body.String(), `"preserveProfiles":3`) || !strings.Contains(legacyRecorder.Body.String(), `"currentDisabledKeys":2`) || !strings.Contains(legacyRecorder.Body.String(), `"targetDisabledKeys":2`) || !strings.Contains(legacyRecorder.Body.String(), `"removedDisabledKeys":0`) || !strings.Contains(legacyRecorder.Body.String(), `"currentTakeoverEnabled":true`) || !strings.Contains(legacyRecorder.Body.String(), `"targetTakeoverEnabled":true`) {
		t.Fatalf("legacy preview response = %d %s", legacyRecorder.Code, legacyRecorder.Body.String())
	}
}

func TestWebDAVPolicyBackupUsesSharedPreviewAndImportPipeline(t *testing.T) {
	defer func(previous *probackup.Coordinator) { probackup.Default = previous }(probackup.Default)
	coordinator := probackup.NewCoordinator()
	probackup.Default = coordinator
	registerTestAPIKeyPolicyDataDomain(t)
	policyPayload := []byte(`{"schema_version":2,"policies":[],"audits":[]}`)
	imported := false
	coordinator.RegisterAPIKeyPolicies(
		func() ([]byte, bool, error) { return policyPayload, true, nil },
		func(_ context.Context, payload []byte) error {
			if !bytes.Equal(payload, policyPayload) {
				t.Fatalf("imported policy payload = %s", payload)
			}
			imported = true
			return nil
		},
		func(context.Context, []byte) (probackup.PolicyBackupPreview, error) {
			return probackup.PolicyBackupPreview{HasPolicies: true, TargetPolicies: 2, TargetProfiles: 3, AssociatedPolicies: 1, OrphanedPolicies: 1}, nil
		},
	)
	backup, err := coordinator.ExportJSONL(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	webDAVBackup := backup
	store := openTestStore(t)
	if err := store.SetMonitoringSettings(context.Background(), MonitoringSettings{WebDAV: MonitoringWebDAVBackupConfig{URL: "https://dav.example/backups", Username: "operator", Password: "secret"}}); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: webDAVRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.String() != "https://dav.example/backups/cliproxy-pro-backup-20260814_120000_000.jsonl" {
			t.Fatalf("WebDAV request = %s %s", request.Method, request.URL)
		}
		user, password, ok := request.BasicAuth()
		if !ok || user != "operator" || password != "secret" {
			t.Fatalf("WebDAV auth = %q, %q, %v", user, password, ok)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(webDAVBackup)), Header: make(http.Header)}, nil
	})}
	server := NewServer(Config{Enabled: true, QueryLimit: 50000, BatchSize: 100}, store)
	server.webDAVClient = client
	router := gin.New()
	server.RegisterGinRoutes(router.Group("/usage"))
	server.RegisterDataManagementGinRoutes(router.Group("/data"))
	body := `{"fileName":"cliproxy-pro-backup-20260814_120000_000.jsonl"}`

	previewRecorder := httptest.NewRecorder()
	previewRequest := httptest.NewRequest(http.MethodPost, "/usage/webdav/preview", strings.NewReader(body))
	previewRequest.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(previewRecorder, previewRequest)
	if previewRecorder.Code != http.StatusOK || !strings.Contains(previewRecorder.Body.String(), `"targetPolicies":2`) || imported {
		t.Fatalf("WebDAV preview = %d %s imported=%v", previewRecorder.Code, previewRecorder.Body.String(), imported)
	}

	restoreRecorder := httptest.NewRecorder()
	restoreRequest := httptest.NewRequest(http.MethodPost, "/usage/webdav/restore", strings.NewReader(body))
	restoreRequest.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(restoreRecorder, restoreRequest)
	if restoreRecorder.Code != http.StatusOK || !imported || !strings.Contains(restoreRecorder.Body.String(), `"apiKeyPolicies":1`) {
		t.Fatalf("WebDAV restore = %d %s imported=%v", restoreRecorder.Code, restoreRecorder.Body.String(), imported)
	}

	imported = false
	dataPreviewRecorder := httptest.NewRecorder()
	dataPreviewRequest := httptest.NewRequest(http.MethodPost, "/data/backups/webdav/preview", strings.NewReader(body))
	dataPreviewRequest.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(dataPreviewRecorder, dataPreviewRequest)
	if dataPreviewRecorder.Code != http.StatusOK || !strings.Contains(dataPreviewRecorder.Body.String(), `"targetPolicies":2`) || imported {
		t.Fatalf("data-management WebDAV preview = %d %s imported=%v", dataPreviewRecorder.Code, dataPreviewRecorder.Body.String(), imported)
	}
	var dataPreview DataRestorePreview
	if err := json.Unmarshal(dataPreviewRecorder.Body.Bytes(), &dataPreview); err != nil {
		t.Fatal(err)
	}
	expectedSHA256 := fmt.Sprintf("%x", sha256.Sum256(backup))
	if dataPreview.BackupSHA256 != expectedSHA256 {
		t.Fatalf("data-management WebDAV preview SHA-256 = %q, want %q", dataPreview.BackupSHA256, expectedSHA256)
	}

	webDAVBackup = append(append([]byte(nil), backup...), '\n')
	changedRestoreRecorder := httptest.NewRecorder()
	changedRestoreRequest := httptest.NewRequest(http.MethodPost, "/data/backups/webdav/restore", strings.NewReader(fmt.Sprintf(`{"fileName":"cliproxy-pro-backup-20260814_120000_000.jsonl","allowLegacy":false,"expectedSha256":"%s"}`, expectedSHA256)))
	changedRestoreRequest.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(changedRestoreRecorder, changedRestoreRequest)
	if changedRestoreRecorder.Code != http.StatusConflict || imported || !strings.Contains(changedRestoreRecorder.Body.String(), "changed after preview") {
		t.Fatalf("changed data-management WebDAV restore = %d %s imported=%v", changedRestoreRecorder.Code, changedRestoreRecorder.Body.String(), imported)
	}

	webDAVBackup = backup
	dataRestoreRecorder := httptest.NewRecorder()
	dataRestoreRequest := httptest.NewRequest(http.MethodPost, "/data/backups/webdav/restore", strings.NewReader(fmt.Sprintf(`{"fileName":"cliproxy-pro-backup-20260814_120000_000.jsonl","allowLegacy":false,"expectedSha256":"%s"}`, expectedSHA256)))
	dataRestoreRequest.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(dataRestoreRecorder, dataRestoreRequest)
	if dataRestoreRecorder.Code != http.StatusOK || !imported || !strings.Contains(dataRestoreRecorder.Body.String(), `"apiKeyPolicies":1`) {
		t.Fatalf("data-management WebDAV restore = %d %s imported=%v", dataRestoreRecorder.Code, dataRestoreRecorder.Body.String(), imported)
	}
	operations, err := store.ListDataOperations(context.Background(), 10)
	if err != nil || len(operations) != 1 || operations[0].Target != "webdav" || operations[0].FileName != "cliproxy-pro-backup-20260814_120000_000.jsonl" || operations[0].Status != dataOperationSuccess {
		t.Fatalf("data-management WebDAV operations = %+v err=%v", operations, err)
	}
}

func TestWebDAVBackupListingFiltersAndSortsKnownFiles(t *testing.T) {
	store := openTestStore(t)
	if err := store.SetMonitoringSettings(context.Background(), MonitoringSettings{WebDAV: MonitoringWebDAVBackupConfig{URL: "https://dav.example/backups", Username: "operator", Password: "secret"}}); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: webDAVRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != "PROPFIND" || request.URL.String() != "https://dav.example/backups/" || request.Header.Get("Depth") != "1" {
			t.Fatalf("WebDAV request = %s %s depth=%s", request.Method, request.URL, request.Header.Get("Depth"))
		}
		if user, password, ok := request.BasicAuth(); !ok || user != "operator" || password != "secret" {
			t.Fatalf("WebDAV auth = %q, %q, %v", user, password, ok)
		}
		body := `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:">
			<d:response><d:href>/backups/usage-export-20260813_120000.jsonl</d:href><d:propstat><d:prop><d:getcontentlength>12</d:getcontentlength><d:getlastmodified>Thu, 13 Aug 2026 12:00:00 GMT</d:getlastmodified></d:prop></d:propstat></d:response>
			<d:response><d:href>/backups/notes.txt</d:href><d:propstat><d:prop><d:getcontentlength>99</d:getcontentlength></d:prop></d:propstat></d:response>
			<d:response><d:href>/backups/usage-export-20260814_120000.jsonl</d:href><d:propstat><d:prop><d:getcontentlength>34</d:getcontentlength><d:getlastmodified>Fri, 14 Aug 2026 12:00:00 GMT</d:getlastmodified></d:prop></d:propstat></d:response>
		</d:multistatus>`
		return &http.Response{StatusCode: http.StatusMultiStatus, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	server := NewServer(Config{Enabled: true, QueryLimit: 50000, BatchSize: 100}, store)
	server.webDAVClient = client
	router := gin.New()
	server.RegisterGinRoutes(router.Group("/usage"))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/usage/webdav/backups", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Backups []WebDAVBackup `json:"backups"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Backups) != 2 || response.Backups[0].FileName != "usage-export-20260814_120000.jsonl" || response.Backups[0].SizeBytes != 34 || response.Backups[1].FileName != "usage-export-20260813_120000.jsonl" {
		t.Fatalf("backups=%#v", response.Backups)
	}
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
	server := NewServer(Config{Enabled: true, QueryLimit: 50000, BatchSize: 100}, store)
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
	server := NewServer(Config{Enabled: true, QueryLimit: 50000, BatchSize: 1}, store)
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
	request := httptest.NewRequest(http.MethodPost, "/usage/import?allow_legacy=1", strings.NewReader(`{"record_type":"future_record","model":"must-not-import"}`))
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

func TestUsageImportRequiresManifestUnlessLegacyIsExplicitlyAllowed(t *testing.T) {
	event, err := json.Marshal(testUsageEvent(0, false, 10))
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	store := openTestStore(t)
	router := testUsageRouter(store)

	rejected := httptest.NewRecorder()
	router.ServeHTTP(rejected, httptest.NewRequest(http.MethodPost, "/usage/import", bytes.NewReader(event)))
	if rejected.Code != http.StatusBadRequest || !strings.Contains(rejected.Body.String(), "backup manifest is required") {
		t.Fatalf("default import status = %d, body=%s", rejected.Code, rejected.Body.String())
	}
	events, _, err := store.Counts(context.Background())
	if err != nil || events != 0 {
		t.Fatalf("Counts() after rejected legacy import = %d, _, %v", events, err)
	}

	allowed := httptest.NewRecorder()
	router.ServeHTTP(allowed, httptest.NewRequest(http.MethodPost, "/usage/import?allow_legacy=1", bytes.NewReader(event)))
	if allowed.Code != http.StatusOK {
		t.Fatalf("explicit legacy import status = %d, body=%s", allowed.Code, allowed.Body.String())
	}
	var result struct {
		LegacyBackup bool `json:"legacyBackup"`
		Added        int  `json:"added"`
	}
	if err := json.Unmarshal(allowed.Body.Bytes(), &result); err != nil || !result.LegacyBackup || result.Added != 1 {
		t.Fatalf("legacy import result = %+v, %v", result, err)
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

func TestUsageImportRollsBackEarlierDomainsWhenLateDatabaseWriteFails(t *testing.T) {
	ctx := context.Background()
	sourceStore := openTestStore(t)
	insertTestUsageEvents(t, sourceStore, testUsageEvent(0, false, 10))
	if err := sourceStore.SetProSetting(ctx, ProSetting{
		Namespace: "test.rollback", SchemaVersion: 1, Settings: json.RawMessage(`{"enabled":true}`),
	}); err != nil {
		t.Fatal(err)
	}
	exportRecorder := httptest.NewRecorder()
	testUsageRouter(sourceStore).ServeHTTP(exportRecorder, httptest.NewRequest(http.MethodGet, "/usage/export", nil))
	if exportRecorder.Code != http.StatusOK {
		t.Fatalf("export status = %d; body=%s", exportRecorder.Code, exportRecorder.Body.String())
	}

	targetStore := openTestStore(t)
	if _, err := targetStore.db.ExecContext(ctx, `create trigger fail_late_backup_import before insert on pro_settings begin select raise(abort, 'forced late backup import failure'); end`); err != nil {
		t.Fatal(err)
	}
	importRecorder := httptest.NewRecorder()
	testUsageRouter(targetStore).ServeHTTP(importRecorder, httptest.NewRequest(http.MethodPost, "/usage/import", bytes.NewReader(exportRecorder.Body.Bytes())))
	if importRecorder.Code != http.StatusInternalServerError {
		t.Fatalf("import status = %d, want 500; body=%s", importRecorder.Code, importRecorder.Body.String())
	}
	events, _, err := targetStore.Counts(ctx)
	if err != nil || events != 0 {
		t.Fatalf("Counts() after failed import = %d, _, %v; want rollback", events, err)
	}
	if _, ok, err := targetStore.GetProSetting(ctx, "test.rollback"); err != nil || ok {
		t.Fatalf("pro setting after failed import = _, %v, %v; want missing", ok, err)
	}
}

func TestUsageImportRollsBackDatabaseAndRuntimeWhenConfigurationApplyFails(t *testing.T) {
	const namespace = "test.import-runtime-rollback"
	ctx := context.Background()
	sourceStore := openTestStore(t)
	if err := sourceStore.SetProSetting(ctx, ProSetting{
		Namespace: namespace, SchemaVersion: 1,
		Settings: json.RawMessage(`{"enabled":true}`),
	}); err != nil {
		t.Fatal(err)
	}
	exportRecorder := httptest.NewRecorder()
	testUsageRouter(sourceStore).ServeHTTP(exportRecorder, httptest.NewRequest(http.MethodGet, "/usage/export", nil))
	if exportRecorder.Code != http.StatusOK {
		t.Fatalf("export status = %d; body=%s", exportRecorder.Code, exportRecorder.Body.String())
	}

	var applied []string
	unregister := RegisterProSettingConsumer(namespace, func(_ context.Context, item ProSetting) error {
		applied = append(applied, string(item.Settings))
		if strings.Contains(string(item.Settings), `"enabled":true`) {
			return errors.New("forced runtime apply failure")
		}
		return nil
	})
	defer unregister()
	targetStore := openTestStore(t)
	importRecorder := httptest.NewRecorder()
	testUsageRouter(targetStore).ServeHTTP(importRecorder, httptest.NewRequest(http.MethodPost, "/usage/import", bytes.NewReader(exportRecorder.Body.Bytes())))
	if importRecorder.Code != http.StatusInternalServerError {
		t.Fatalf("import status = %d, want 500; body=%s", importRecorder.Code, importRecorder.Body.String())
	}
	if _, ok, err := targetStore.GetProSetting(ctx, namespace); err != nil || ok {
		t.Fatalf("persisted setting after runtime failure = _, %v, %v; want missing", ok, err)
	}
	if len(applied) != 2 || !strings.Contains(applied[0], `"enabled":true`) || applied[1] != `{}` {
		t.Fatalf("runtime apply/rollback calls = %#v", applied)
	}
}

func TestUsageBackupRestoresAllNamespacedProSettingsAndConsumers(t *testing.T) {
	ctx := context.Background()
	sourceStore := openTestStore(t)
	wants := []ProSetting{
		{Namespace: ProSettingNamespaceRoutingRequestProtection, SchemaVersion: 1, Settings: json.RawMessage(`{"enabled":true,"mode":"enforce"}`), UpdatedAtMS: 123},
		{Namespace: ProSettingNamespaceProxyPool, SchemaVersion: 1, Settings: json.RawMessage(`{"enabled":false}`), UpdatedAtMS: 124},
		{Namespace: ProSettingNamespaceOAuthPolicy, SchemaVersion: 1, Settings: json.RawMessage(`{"enabled":true}`), UpdatedAtMS: 125},
	}
	for _, want := range wants {
		if err := sourceStore.SetProSetting(ctx, want); err != nil {
			t.Fatal(err)
		}
	}
	exportRecorder := httptest.NewRecorder()
	testUsageRouter(sourceStore).ServeHTTP(exportRecorder, httptest.NewRequest(http.MethodGet, "/usage/export", nil))
	if exportRecorder.Code != http.StatusOK {
		t.Fatalf("export status = %d; body=%s", exportRecorder.Code, exportRecorder.Body.String())
	}

	targetStore := openTestStore(t)
	applied := make(chan ProSetting, len(wants))
	for _, want := range wants {
		unregister := RegisterProSettingConsumer(want.Namespace, func(_ context.Context, item ProSetting) error {
			applied <- item
			return nil
		})
		defer unregister()
	}
	importRecorder := httptest.NewRecorder()
	testUsageRouter(targetStore).ServeHTTP(importRecorder, httptest.NewRequest(http.MethodPost, "/usage/import", bytes.NewReader(exportRecorder.Body.Bytes())))
	if importRecorder.Code != http.StatusOK {
		t.Fatalf("import status = %d; body=%s", importRecorder.Code, importRecorder.Body.String())
	}
	for _, want := range wants {
		got, ok, err := targetStore.GetProSetting(ctx, want.Namespace)
		if err != nil || !ok || string(got.Settings) != string(want.Settings) {
			t.Fatalf("restored pro setting %q = %+v, %v, %v", want.Namespace, got, ok, err)
		}
	}
	seen := make(map[string]ProSetting, len(wants))
	for range wants {
		select {
		case item := <-applied:
			seen[item.Namespace] = item
		default:
			t.Fatal("a namespaced Pro settings consumer was not called")
		}
	}
	for _, want := range wants {
		if item, ok := seen[want.Namespace]; !ok || string(item.Settings) != string(want.Settings) {
			t.Fatalf("applied pro setting %q = %+v, found:%v", want.Namespace, item, ok)
		}
	}
}

func TestNormalizeOAuthPolicySettingsPrefersCurrentNamespace(t *testing.T) {
	items := normalizeOAuthPolicySettings([]ProSetting{
		{Namespace: LegacyProSettingNamespaceOAuthModelPolicy, SchemaVersion: 1, Settings: json.RawMessage(`{"legacy":true}`)},
		{Namespace: ProSettingNamespaceOAuthPolicy, SchemaVersion: 1, Settings: json.RawMessage(`{"current":true}`)},
	})
	if len(items) != 1 || items[0].Namespace != ProSettingNamespaceOAuthPolicy || !strings.Contains(string(items[0].Settings), "current") {
		t.Fatalf("normalized settings = %#v", items)
	}
	legacyOnly := normalizeOAuthPolicySettings([]ProSetting{{
		Namespace: LegacyProSettingNamespaceOAuthModelPolicy, SchemaVersion: 1, Settings: json.RawMessage(`{"enabled":true}`),
	}})
	if len(legacyOnly) != 1 || legacyOnly[0].Namespace != ProSettingNamespaceOAuthPolicy {
		t.Fatalf("legacy settings = %#v", legacyOnly)
	}
}

func TestProSettingConsumerRestoresOlderLiveOwner(t *testing.T) {
	const namespace = "test.owner-stack"
	var calls []string
	unregisterOld := RegisterProSettingConsumer(namespace, func(context.Context, ProSetting) error {
		calls = append(calls, "old")
		return nil
	})
	unregisterNew := RegisterProSettingConsumer(namespace, func(context.Context, ProSetting) error {
		calls = append(calls, "new")
		return nil
	})
	t.Cleanup(unregisterOld)

	item := ProSetting{Namespace: namespace, SchemaVersion: 1, Settings: json.RawMessage(`{}`)}
	if err := ApplyImportedProSettings(context.Background(), []ProSetting{item}); err != nil {
		t.Fatal(err)
	}
	unregisterNew()
	if err := ApplyImportedProSettings(context.Background(), []ProSetting{item}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(calls, ","); got != "new,old" {
		t.Fatalf("consumer calls = %q, want new,old", got)
	}
}

func TestUsageImportRestoresModelPriceRuleWhenOnlyNewerHistoryRemains(t *testing.T) {
	ctx := context.Background()
	sourceStore := openTestStore(t)
	sourceRule := testGPT56PriceRule()
	sourceRule.Base.Input = 1.25
	fastRate := sourceRule.ServiceTiers["fast"]
	fastRate.Reasoning = 7.5
	sourceRule.ServiceTiers["fast"] = fastRate
	if _, changed, err := sourceStore.UpsertModelPriceRule(ctx, sourceRule, true); err != nil || !changed {
		t.Fatalf("source UpsertModelPriceRule() = changed:%v err:%v", changed, err)
	}
	exportRecorder := httptest.NewRecorder()
	testUsageRouter(sourceStore).ServeHTTP(exportRecorder, httptest.NewRequest(http.MethodGet, "/usage/export", nil))
	if exportRecorder.Code != http.StatusOK {
		t.Fatalf("export status = %d; body=%s", exportRecorder.Code, exportRecorder.Body.String())
	}

	targetStore := openTestStore(t)
	targetRule := testGPT56PriceRule()
	if _, changed, err := targetStore.UpsertModelPriceRule(ctx, targetRule, true); err != nil || !changed {
		t.Fatalf("target first UpsertModelPriceRule() = changed:%v err:%v", changed, err)
	}
	targetRule.Base.Input = 9.99
	if _, changed, err := targetStore.UpsertModelPriceRule(ctx, targetRule, true); err != nil || !changed {
		t.Fatalf("target second UpsertModelPriceRule() = changed:%v err:%v", changed, err)
	}
	if err := targetStore.DeleteModelPriceRule(ctx, targetRule.Model); err != nil {
		t.Fatalf("DeleteModelPriceRule() error = %v", err)
	}

	importRecorder := httptest.NewRecorder()
	testUsageRouter(targetStore).ServeHTTP(importRecorder, httptest.NewRequest(http.MethodPost, "/usage/import", bytes.NewReader(exportRecorder.Body.Bytes())))
	if importRecorder.Code != http.StatusOK {
		t.Fatalf("import status = %d, want 200; body=%s", importRecorder.Code, importRecorder.Body.String())
	}
	rules, err := targetStore.ActiveModelPriceRules(ctx)
	var fast ModelPriceRate
	var hasFast bool
	if len(rules) == 1 {
		fast, hasFast = rules[0].ServiceTiers["fast"]
	}
	if err != nil || len(rules) != 1 || rules[0].Model != sourceRule.Model || rules[0].Base.Input != sourceRule.Base.Input ||
		!hasFast || fast.Reasoning != sourceRule.ServiceTiers["fast"].Reasoning || rules[0].Version <= 2 {
		t.Fatalf("restored rules = %+v err:%v; want imported rule newer than retained version 2", rules, err)
	}
}

func TestHandleUsageResetClearsStatisticsAndReturnsGeneration(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	insertTestUsageEvents(t, store,
		testUsageEvent(0, false, 10),
		testUsageEvent(1, true, 20),
	)
	cursor := RoutingCursorState{
		CursorKey: "single|codex|gpt-5|0|all", LastAuthID: "auth-reset", UpdatedAtMS: time.Now().UnixMilli(),
	}
	if err := store.SetRoutingCursorState(ctx, cursor); err != nil {
		t.Fatalf("SetRoutingCursorState() error = %v", err)
	}
	if err := store.SetAuthRuntimeStats(ctx, AuthRuntimeStats{
		AuthIndex: "idx-reset", AuthID: cursor.LastAuthID, SelectedCount: 4, SuccessCount: 3, FailureCount: 1,
		UpdatedAtMS: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatalf("SetAuthRuntimeStats() error = %v", err)
	}
	var appliedCursors []RoutingCursorState
	var appliedStats []AuthRuntimeStats
	SetAuthRuntimeStateImportHandler(func(cursors []RoutingCursorState, stats []AuthRuntimeStats) error {
		appliedCursors = append([]RoutingCursorState(nil), cursors...)
		appliedStats = append([]AuthRuntimeStats(nil), stats...)
		return nil
	})
	defer SetAuthRuntimeStateImportHandler(nil)
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
	if result.DeletedEvents != 2 || result.DeletedAuthRuntimeStats != 1 || result.Generation <= 1 || result.ResetAtMS <= 0 {
		t.Fatalf("reset result = %+v, want usage and auth runtime statistics cleared", result)
	}
	if len(appliedCursors) != 1 || appliedCursors[0].CursorKey != cursor.CursorKey || appliedCursors[0].LastAuthID != cursor.LastAuthID {
		t.Fatalf("applied cursors = %+v, want preserved cursor %+v", appliedCursors, cursor)
	}
	if len(appliedStats) != 0 {
		t.Fatalf("applied auth runtime stats = %+v, want authoritative empty snapshot", appliedStats)
	}
	if storedStats, err := store.ListAuthRuntimeStats(ctx); err != nil || len(storedStats) != 0 {
		t.Fatalf("stored auth runtime stats after reset = %+v err:%v, want empty", storedStats, err)
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
	fourth := testUsageEvent(3, true, 40)
	fourth.Provider = "alpha"
	fourth.Model = "model-a"
	fourth.APIKeyHash = "key-a"
	fourth.AuthIndex = "auth-meta"
	insertTestUsageEvents(t, store, first, second, third, fourth)
	router := testUsageRouter(store)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/usage/events?direction=before&limit=1&provider=alpha&model=model-a&api_key_hash=key-a&status=failed&search=needle&search_auth_indexes=auth-meta", nil)
	router.ServeHTTP(recorder, request)
	payload := decodeUsagePayload(t, recorder)
	if got := usageDetailIDs(payload); len(got) != 1 || got[0] != 4 {
		t.Fatalf("first filtered page ids = %v, want [4]", got)
	}
	if payload.MatchedTotal != 2 || !payload.HasMore || payload.NextCursor == "" {
		t.Fatalf("first filtered page metadata = %+v, want matched=2 and next cursor", payload)
	}

	nextRecorder := httptest.NewRecorder()
	nextRequest := httptest.NewRequest(http.MethodGet, "/usage/events?cursor="+payload.NextCursor+"&limit=1", nil)
	router.ServeHTTP(nextRecorder, nextRequest)
	nextPayload := decodeUsagePayload(t, nextRecorder)
	if got := usageDetailIDs(nextPayload); len(got) != 1 || got[0] != 2 {
		t.Fatalf("second filtered page ids = %v, want [2]", got)
	}
	if nextPayload.MatchedTotal != 2 || nextPayload.HasMore {
		t.Fatalf("second filtered page metadata = %+v, want matched=2 final page", nextPayload)
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
	server := NewServer(Config{Enabled: true, QueryLimit: 50000, BatchSize: 2}, store)

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

	invalidTimezoneRecorder := httptest.NewRecorder()
	invalidTimezoneRequest := httptest.NewRequest(http.MethodGet, "/usage/aggregates?interval=day&timezone=Not%2FA_Timezone", nil)
	router.ServeHTTP(invalidTimezoneRecorder, invalidTimezoneRequest)
	if invalidTimezoneRecorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid timezone status = %d, want 400", invalidTimezoneRecorder.Code)
	}
}

func TestHandleAccountUsageValidatesScopeAndReturnsDatasetState(t *testing.T) {
	store := openTestStore(t)
	event := testUsageEvent(0, false, 10)
	event.AuthIndex = "codex:account"
	insertTestUsageEvents(t, store, event)
	router := testUsageRouter(store)

	invalidRecorder := httptest.NewRecorder()
	router.ServeHTTP(invalidRecorder, httptest.NewRequest(http.MethodGet, "/usage/account?days=30", nil))
	if invalidRecorder.Code != http.StatusBadRequest {
		t.Fatalf("missing auth_index status = %d, want 400", invalidRecorder.Code)
	}
	invalidRangeRecorder := httptest.NewRecorder()
	router.ServeHTTP(invalidRangeRecorder, httptest.NewRequest(http.MethodGet, "/usage/account?auth_index=codex%3Aaccount&from_ms=1000", nil))
	if invalidRangeRecorder.Code != http.StatusBadRequest {
		t.Fatalf("partial custom range status = %d, want 400", invalidRangeRecorder.Code)
	}
	invalidTimezoneRecorder := httptest.NewRecorder()
	router.ServeHTTP(invalidTimezoneRecorder, httptest.NewRequest(http.MethodGet, "/usage/account?auth_index=codex%3Aaccount&days=1&timezone=Not%2FA_Timezone", nil))
	if invalidTimezoneRecorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid timezone status = %d, want 400", invalidTimezoneRecorder.Code)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/usage/account?auth_index=codex%3Aaccount&days=0&timezone_offset_minutes=480", nil)
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Detail     AccountUsageDetail `json:"detail"`
		LatestID   int64              `json:"latest_id"`
		Generation int64              `json:"generation"`
		SnapshotAt int64              `json:"snapshot_at_ms"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if payload.Detail.AuthIndex != "codex:account" || payload.Detail.TotalRequests != 1 {
		t.Fatalf("detail = %+v", payload.Detail)
	}
	if payload.LatestID != 1 || payload.Generation < 1 || payload.SnapshotAt <= 0 {
		t.Fatalf("dataset state = latest:%d generation:%d snapshot:%d", payload.LatestID, payload.Generation, payload.SnapshotAt)
	}

	customRecorder := httptest.NewRecorder()
	customPath := fmt.Sprintf("/usage/account?auth_index=codex%%3Aaccount&from_ms=%d&to_ms=%d", event.TimestampMS, event.TimestampMS+999)
	router.ServeHTTP(customRecorder, httptest.NewRequest(http.MethodGet, customPath, nil))
	if customRecorder.Code != http.StatusOK {
		t.Fatalf("custom range status = %d, want 200; body=%s", customRecorder.Code, customRecorder.Body.String())
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

func TestUsageImportWithoutProSettingsContinuesIntoRuntimeRestore(t *testing.T) {
	defer SetAuthRuntimeStateImportHandler(nil)
	called := false
	SetAuthRuntimeStateImportHandler(func(cursors []RoutingCursorState, stats []AuthRuntimeStats) error {
		called = true
		if len(cursors) != 1 || cursors[0].LastAuthID != "auth-without-settings" || len(stats) != 0 {
			t.Fatalf("runtime import callback cursors=%+v stats=%+v", cursors, stats)
		}
		return nil
	})

	sourceStore := openTestStore(t)
	if err := sourceStore.SetRoutingCursorState(context.Background(), RoutingCursorState{
		CursorKey: "single|codex|gpt-5|0|all", LastAuthID: "auth-without-settings", UpdatedAtMS: 100,
	}); err != nil {
		t.Fatal(err)
	}
	exportRecorder := httptest.NewRecorder()
	testUsageRouter(sourceStore).ServeHTTP(exportRecorder, httptest.NewRequest(http.MethodGet, "/usage/export", nil))
	if exportRecorder.Code != http.StatusOK || bytes.Contains(exportRecorder.Body.Bytes(), []byte(`"record_type":"pro_settings"`)) {
		t.Fatalf("export = %d %s, want backup without Pro settings", exportRecorder.Code, exportRecorder.Body.String())
	}

	importRecorder := httptest.NewRecorder()
	testUsageRouter(openTestStore(t)).ServeHTTP(importRecorder, httptest.NewRequest(http.MethodPost, "/usage/import", bytes.NewReader(exportRecorder.Body.Bytes())))
	if importRecorder.Code != http.StatusOK {
		t.Fatalf("import = %d %s", importRecorder.Code, importRecorder.Body.String())
	}
	if !called {
		t.Fatal("runtime restore was skipped after importing zero Pro settings")
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
	request := httptest.NewRequest(http.MethodPost, "/usage/import?allow_legacy=1", bytes.NewReader(exported))
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
		"settings": {"targetType": "xai", "workers": 4, "providerWorkers": 2, "deleteWorkers": 4, "timeout": 15000},
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
