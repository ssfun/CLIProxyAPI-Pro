package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	probackup "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/backup"
)

func TestDataCleanupRollsBackEarlierDomainsWhenLaterDomainFails(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	if _, err := store.StartDataOperation(ctx, DataOperation{Kind: "old", StartedAtMS: 1}); err != nil {
		t.Fatal(err)
	}
	unregister := RegisterDataDomainContributor("test-failing-cleanup", DataDomainContribution{
		CleanupPreview: func(context.Context, *Store, int64) (int64, error) { return 0, nil },
		CleanupExecute: func(context.Context, *Store, int64) (int64, error) { return 0, errors.New("forced cleanup failure") },
	})
	t.Cleanup(unregister)
	_, err := store.ExecuteDataCleanup(ctx, DataCleanupRequest{
		Domains: []string{"operation-log", "test-failing-cleanup"}, BeforeMS: 2,
		ExpectedRecords: map[string]int64{"operation-log": 1, "test-failing-cleanup": 0},
	}, time.UnixMilli(3))
	if err == nil {
		t.Fatal("cleanup unexpectedly succeeded")
	}
	operations, err := store.ListDataOperations(ctx, 10)
	if err != nil || len(operations) != 1 {
		t.Fatalf("operation log after rollback = %+v err=%v", operations, err)
	}
}

func TestDataCleanupRejectsRowsAddedAfterPreview(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	if _, err := store.StartDataOperation(ctx, DataOperation{Kind: "previewed", StartedAtMS: 1}); err != nil {
		t.Fatal(err)
	}
	preview, err := store.PreviewDataCleanup(ctx, DataCleanupRequest{Domains: []string{"operation-log"}, BeforeMS: 2}, time.UnixMilli(3))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartDataOperation(ctx, DataOperation{Kind: "added-later", StartedAtMS: 1}); err != nil {
		t.Fatal(err)
	}
	_, err = store.ExecuteDataCleanup(ctx, DataCleanupRequest{
		Domains: []string{"operation-log"}, BeforeMS: preview.CutoffMS,
		ExpectedRecords: map[string]int64{"operation-log": preview.TotalRecords},
	}, time.UnixMilli(3))
	if !errors.Is(err, errDataCleanupPreviewStale) {
		t.Fatalf("cleanup error = %v, want stale preview", err)
	}
	operations, listErr := store.ListDataOperations(ctx, 10)
	if listErr != nil || len(operations) != 2 {
		t.Fatalf("operation log after stale preview = %+v err=%v", operations, listErr)
	}
}

func TestReplaceRuntimeStateRemovesRowsMissingFromBackup(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	if err := store.SetRoutingCursorState(ctx, RoutingCursorState{CursorKey: "stale", LastAuthID: "old", UpdatedAtMS: 1}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ReplaceRuntimeState(ctx, []RoutingCursorState{{CursorKey: "restored", LastAuthID: "new", UpdatedAtMS: 2}}, nil, true, false); err != nil {
		t.Fatal(err)
	}
	if _, exists, err := store.GetRoutingCursorState(ctx, "stale"); err != nil || exists {
		t.Fatalf("stale cursor exists=%v err=%v", exists, err)
	}
}

func TestReplaceProSettingsRemovesNamespacesMissingFromBackup(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	if err := store.SetProSetting(ctx, ProSetting{Namespace: "stale", SchemaVersion: 1, Settings: []byte(`{"enabled":true}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceProSettings(ctx, []ProSetting{{Namespace: "restored", SchemaVersion: 1, Settings: []byte(`{"enabled":false}`)}}); err != nil {
		t.Fatal(err)
	}
	if _, exists, err := store.GetProSetting(ctx, "stale"); err != nil || exists {
		t.Fatalf("stale Pro setting exists=%v err=%v", exists, err)
	}
}

func TestLegacyInspectionSetHandlerRegistersPreviewDomain(t *testing.T) {
	previous := probackup.Default
	probackup.Default = probackup.NewCoordinator()
	t.Cleanup(func() { probackup.Default = previous })
	SetAccountInspectionSnapshotHandlers(func() ([]byte, bool, error) {
		return []byte(`{"rows":[1]}`), true, nil
	}, func([]byte) error { return nil })
	t.Cleanup(func() { SetAccountInspectionSnapshotHandlers(nil, nil) })
	backup, err := probackup.Default.ExportJSONL(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(Config{Enabled: true}, openTestStore(t))
	if _, err := server.previewBackupData(context.Background(), backup, false, false, nil); err != nil {
		t.Fatalf("preview backup from legacy Set handler: %v", err)
	}
}

func TestDataDomainContributorRegistrationKeepsNewestOwner(t *testing.T) {
	const id = "test-data-domain-owner-stack"
	older := RegisterDataDomainContributor(id, DataDomainContributorFunc(func(context.Context, *Store) DataDomainInventory {
		return DataDomainInventory{Owner: "older", Available: true}
	}))
	t.Cleanup(older)
	newer := RegisterDataDomainContributor(id, DataDomainContributorFunc(func(context.Context, *Store) DataDomainInventory {
		return DataDomainInventory{Owner: "newer", Available: true}
	}))
	t.Cleanup(newer)

	contributors := registeredDataDomainContributors()
	if got := contributors[id].Inventory(context.Background(), nil).Owner; got != "newer" {
		t.Fatalf("active contributor owner = %q, want newer", got)
	}

	older()
	contributors = registeredDataDomainContributors()
	if got := contributors[id].Inventory(context.Background(), nil).Owner; got != "newer" {
		t.Fatalf("older unregister replaced active contributor with %q", got)
	}

	newer()
	if _, exists := registeredDataDomainContributors()[id]; exists {
		t.Fatal("contributor remains registered after the active owner is removed")
	}
}

func TestDataDomainInventorySerializesEmptySecretClassesAsArray(t *testing.T) {
	domains, err := (&Store{}).listDataDomains(context.Background(), map[string]DataDomainContributor{
		"internal-domain": DataDomainContributorFunc(func(context.Context, *Store) DataDomainInventory {
			return DataDomainInventory{Owner: "test", Available: true}
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(domains) != 1 || domains[0].SecretClasses == nil {
		t.Fatalf("domains = %+v, want a non-nil empty secretClasses array", domains)
	}
	encoded, err := json.Marshal(domains[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"secretClasses":[]`)) {
		t.Fatalf("domain JSON = %s, want empty secretClasses array", encoded)
	}
}

func TestDataDomainContributionCountsClaimedBackupRecord(t *testing.T) {
	const (
		id         = "test-declarative-backup-domain"
		recordType = "test_declarative_backup_record"
	)
	unregister := RegisterDataDomainContributor(id, DataDomainContribution{
		BackupRecordTypes: []string{recordType},
		BackupCounter: func(_ context.Context, _ *Store, _ string, raw []byte) (int64, error) {
			if !bytes.Contains(raw, []byte(`"items"`)) {
				t.Fatal("backup counter did not receive the source record")
			}
			return 7, nil
		},
		BackupImporter: func(context.Context, *Store, string, []byte) error { return nil },
	})
	t.Cleanup(unregister)

	counts, err := (&Store{}).backupDomainRecordCounts(context.Background(), []byte(`{"record_type":"`+recordType+`","items":[]}`))
	if err != nil {
		t.Fatalf("count backup records: %v", err)
	}
	if got := counts[id]; got != 7 {
		t.Fatalf("backup record count = %d, want 7", got)
	}
}

func TestDataDomainContributionRejectsDuplicateBackupRecordClaims(t *testing.T) {
	const recordType = "test_duplicate_backup_record"
	unregisterA := RegisterDataDomainContributor("test-duplicate-backup-a", DataDomainContribution{BackupRecordTypes: []string{recordType}})
	t.Cleanup(unregisterA)
	unregisterB := RegisterDataDomainContributor("test-duplicate-backup-b", DataDomainContribution{BackupRecordTypes: []string{recordType}})
	t.Cleanup(unregisterB)

	_, err := (&Store{}).backupDomainRecordCounts(context.Background(), []byte(`{"record_type":"`+recordType+`"}`))
	if err == nil || !strings.Contains(err.Error(), "claimed by multiple data domains") {
		t.Fatalf("duplicate claim error = %v", err)
	}
}

func TestDataDomainContributionDelegatesCleanup(t *testing.T) {
	const id = "test-plugin-cleanup-domain"
	var previewCutoff, executeCutoff int64
	unregister := RegisterDataDomainContributor(id, DataDomainContribution{
		CleanupPreview: func(_ context.Context, _ *Store, cutoffMS int64) (int64, error) {
			previewCutoff = cutoffMS
			return 9, nil
		},
		CleanupExecute: func(_ context.Context, _ *Store, cutoffMS int64) (int64, error) {
			executeCutoff = cutoffMS
			return 9, nil
		},
	})
	t.Cleanup(unregister)

	const cutoffMS = int64(123456789)
	store := openTestStore(t)
	result, err := store.ExecuteDataCleanup(context.Background(), DataCleanupRequest{
		Domains: []string{id}, BeforeMS: cutoffMS, ExpectedRecords: map[string]int64{id: 9},
	}, time.UnixMilli(cutoffMS+1))
	if err != nil {
		t.Fatalf("execute plugin cleanup: %v", err)
	}
	if previewCutoff != cutoffMS || executeCutoff != cutoffMS {
		t.Fatalf("cleanup cutoffs preview=%d execute=%d, want %d", previewCutoff, executeCutoff, cutoffMS)
	}
	if result.TotalRecords != 9 || len(result.Domains) != 1 || result.Domains[0].Records != 9 {
		t.Fatalf("cleanup result = %+v, want one domain with 9 executed records", result)
	}
}

func TestBackupDomainRecordCountsAcceptsExistingRecordTypes(t *testing.T) {
	unregisterSchedule := RegisterDataDomainContributor("account-inspection-schedule", DataDomainContribution{BackupRecordTypes: []string{accountInspectionScheduleExportRecordType}})
	t.Cleanup(unregisterSchedule)
	unregisterSnapshot := RegisterDataDomainContributor("account-inspection-snapshot", DataDomainContribution{BackupRecordTypes: []string{accountInspectionSnapshotExportRecordType}})
	t.Cleanup(unregisterSnapshot)
	unregisterPolicy := RegisterDataDomainContributor("api-key-policy", DataDomainContribution{BackupRecordTypes: []string{"api_key_policies"}})
	t.Cleanup(unregisterPolicy)

	backup := strings.Join([]string{
		`{"record_type":"backup_manifest"}`,
		`{"model":"legacy-usage-event"}`,
		`{"record_type":"monitoring_settings","version":1,"settings":{}}`,
		`{"record_type":"model_prices","version":2,"prices":{},"rules":[]}`,
		`{"record_type":"quota_cache","version":2,"entries":[]}`,
		`{"record_type":"routing_cursor_state","version":1,"items":[]}`,
		`{"record_type":"auth_runtime_stats","version":1,"items":[]}`,
		`{"record_type":"pro_settings","version":1,"items":[]}`,
		`{"record_type":"account_inspection_schedule","version":1,"schedule":{}}`,
		`{"record_type":"account_inspection_snapshot","version":1,"snapshot":{}}`,
		`{"record_type":"api_key_policies","version":2,"policies":{}}`,
	}, "\n")
	counts, err := (&Store{}).backupDomainRecordCounts(context.Background(), []byte(backup))
	if err != nil {
		t.Fatalf("count existing backup record types: %v", err)
	}
	for _, id := range []string{"usage-events", "data-settings", "account-inspection-schedule", "account-inspection-snapshot", "api-key-policy"} {
		if counts[id] != 1 {
			t.Errorf("backup count for %s = %d, want 1", id, counts[id])
		}
	}
}

func TestEncryptedBackupRoundTripAndAuthentication(t *testing.T) {
	plaintext := []byte("{\"record_type\":\"backup_manifest\"}\n")
	secretClasses := []string{"connector_credentials", "credential_identifiers"}
	encrypted, err := encryptBackup(plaintext, "correct horse battery staple", secretClasses)
	if err != nil {
		t.Fatalf("encrypt backup: %v", err)
	}
	decrypted, protected, gotSecretClasses, err := decryptBackup(encrypted, "correct horse battery staple")
	if err != nil {
		t.Fatalf("decrypt backup: %v", err)
	}
	if !protected {
		t.Fatal("encrypted envelope was treated as plaintext")
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("decrypted payload = %q, want %q", decrypted, plaintext)
	}
	if len(gotSecretClasses) != len(secretClasses) {
		t.Fatalf("secret classes = %v, want %v", gotSecretClasses, secretClasses)
	}
	if _, protected, _, err := decryptBackup(encrypted, "incorrect passphrase"); !protected || err == nil {
		t.Fatalf("wrong passphrase result protected=%v err=%v", protected, err)
	}
	var envelope encryptedBackupEnvelope
	if err := json.Unmarshal(encrypted, &envelope); err != nil {
		t.Fatal(err)
	}
	envelope.SecretClasses = []string{"forged"}
	tampered, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if _, protected, _, err := decryptBackup(tampered, "correct horse battery staple"); !protected || err == nil {
		t.Fatalf("tampered secret classes protected=%v err=%v", protected, err)
	}
}

func TestDecryptBackupLeavesPlaintextUntouched(t *testing.T) {
	plaintext := []byte("{\"record_type\":\"backup_manifest\"}\n")
	decrypted, protected, secretClasses, err := decryptBackup(plaintext, "")
	if err != nil {
		t.Fatalf("decrypt plaintext backup: %v", err)
	}
	if protected || secretClasses != nil || !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("plaintext result protected=%v secretClasses=%v payload=%q", protected, secretClasses, decrypted)
	}
}

func TestDecryptBackupRemainsCompatibleWithVersionOneEnvelope(t *testing.T) {
	plaintext := []byte("legacy encrypted backup")
	encrypted, err := encryptBackupVersion(plaintext, "correct horse battery staple", []string{"legacy"}, encryptedBackupLegacyV1)
	if err != nil {
		t.Fatal(err)
	}
	decrypted, protected, secretClasses, err := decryptBackup(encrypted, "correct horse battery staple")
	if err != nil || !protected || !bytes.Equal(decrypted, plaintext) || secretClasses != nil {
		t.Fatalf("legacy decrypt protected=%v secretClasses=%v payload=%q err=%v", protected, secretClasses, decrypted, err)
	}
}

func TestDataManagementBackupNamesPreserveSubsecondPrecision(t *testing.T) {
	first := time.Date(2026, 9, 26, 12, 0, 0, 123456789, time.UTC)
	second := first.Add(time.Nanosecond)
	a, b := dataManagementBackupFileName(first), dataManagementBackupFileName(second)
	if a == b {
		t.Fatalf("distinct backup times overwrite the same WebDAV object: %s", a)
	}
	for _, name := range []string{a, b} {
		if !isKnownWebDAVBackupFileName(name) {
			t.Fatalf("backup is invisible to WebDAV listing/retention: %s", name)
		}
	}
}
