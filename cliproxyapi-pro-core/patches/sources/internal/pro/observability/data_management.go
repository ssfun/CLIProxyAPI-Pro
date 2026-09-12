package observability

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	probackup "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/backup"
)

const (
	dataOperationRunning = "running"
	dataOperationSuccess = "success"
	dataOperationFailed  = "failed"
)

type DataOperation struct {
	ID              int64          `json:"id"`
	Kind            string         `json:"kind"`
	Status          string         `json:"status"`
	Target          string         `json:"target"`
	FileName        string         `json:"fileName"`
	StartedAtMS     int64          `json:"startedAtMs"`
	FinishedAtMS    int64          `json:"finishedAtMs"`
	SizeBytes       int64          `json:"sizeBytes"`
	AffectedRecords int64          `json:"affectedRecords"`
	SecretClasses   []string       `json:"secretClasses"`
	Message         string         `json:"message"`
	Metadata        map[string]any `json:"metadata"`
}

type DataDomainInventory struct {
	ID               string   `json:"id"`
	Owner            string   `json:"owner"`
	SchemaVersion    int      `json:"schemaVersion"`
	Records          int64    `json:"records"`
	UpdatedAtMS      int64    `json:"updatedAtMs"`
	BackupIncluded   bool     `json:"backupIncluded"`
	RestoreMode      string   `json:"restoreMode"`
	CleanupSupported bool     `json:"cleanupSupported"`
	Sensitivity      string   `json:"sensitivity"`
	SecretClasses    []string `json:"secretClasses"`
	Available        bool     `json:"available"`
	Error            string   `json:"error,omitempty"`
}

// DataDomainContributor is the plugin-facing management contract for one Pro
// data domain. A contributor owns inventory, backup-record recognition and
// optional cleanup behavior for its domain; config.yaml API keys and auth files
// remain outside this protocol.
type DataDomainContributor interface {
	Inventory(context.Context, *Store) DataDomainInventory
	CountBackupRecord(context.Context, *Store, string, []byte) (int64, bool, error)
	SupportsCleanup() bool
	PreviewCleanup(context.Context, *Store, int64) (int64, error)
	ExecuteCleanup(context.Context, *Store, int64) (int64, error)
}

type DataDomainContributorFunc func(context.Context, *Store) DataDomainInventory

func (f DataDomainContributorFunc) Inventory(ctx context.Context, store *Store) DataDomainInventory {
	return f(ctx, store)
}

func (DataDomainContributorFunc) CountBackupRecord(context.Context, *Store, string, []byte) (int64, bool, error) {
	return 0, false, nil
}

func (DataDomainContributorFunc) SupportsCleanup() bool { return false }

func (DataDomainContributorFunc) PreviewCleanup(context.Context, *Store, int64) (int64, error) {
	return 0, fmt.Errorf("data-domain cleanup is not supported")
}

func (DataDomainContributorFunc) ExecuteCleanup(context.Context, *Store, int64) (int64, error) {
	return 0, fmt.Errorf("data-domain cleanup is not supported")
}

type DataDomainBackupRecordCounter func(context.Context, *Store, string, []byte) (int64, error)
type DataDomainBackupRecordImporter func(context.Context, *Store, string, []byte) error
type DataDomainCleanupHandler func(context.Context, *Store, int64) (int64, error)

// DataDomainBackupImporter is optional. Contributors that claim record types
// outside the host's built-in backup protocol implement it so preview and the
// transactional restore path use the same extension contract.
type DataDomainBackupImporter interface {
	CanImportBackupRecord(string) bool
	ImportBackupRecord(context.Context, *Store, string, []byte) error
}

// DataDomainContribution is the declarative adapter most plugins use. Backup
// record types are claimed explicitly; a nil counter counts one logical record.
// Cleanup is advertised only when both preview and execute handlers are set.
type DataDomainContribution struct {
	InventoryFunc     DataDomainContributorFunc
	BackupRecordTypes []string
	BackupCounter     DataDomainBackupRecordCounter
	BackupImporter    DataDomainBackupRecordImporter
	CleanupPreview    DataDomainCleanupHandler
	CleanupExecute    DataDomainCleanupHandler
}

func (c DataDomainContribution) CanImportBackupRecord(recordType string) bool {
	if c.BackupImporter == nil {
		return false
	}
	for _, candidate := range c.BackupRecordTypes {
		if candidate == recordType {
			return true
		}
	}
	return false
}

func (c DataDomainContribution) ImportBackupRecord(ctx context.Context, store *Store, recordType string, raw []byte) error {
	if !c.CanImportBackupRecord(recordType) {
		return fmt.Errorf("backup importer is unavailable for record type %q", recordType)
	}
	return c.BackupImporter(ctx, store, recordType, raw)
}

func (c DataDomainContribution) Inventory(ctx context.Context, store *Store) DataDomainInventory {
	if c.InventoryFunc == nil {
		return DataDomainInventory{Available: false, Error: "data-domain inventory is unavailable"}
	}
	return c.InventoryFunc(ctx, store)
}

func (c DataDomainContribution) CountBackupRecord(ctx context.Context, store *Store, recordType string, raw []byte) (int64, bool, error) {
	claimed := false
	for _, candidate := range c.BackupRecordTypes {
		if candidate == recordType {
			claimed = true
			break
		}
	}
	if !claimed {
		return 0, false, nil
	}
	if c.BackupCounter == nil {
		return 1, true, nil
	}
	count, err := c.BackupCounter(ctx, store, recordType, raw)
	return count, true, err
}

func (c DataDomainContribution) SupportsCleanup() bool {
	return c.CleanupPreview != nil && c.CleanupExecute != nil
}

func (c DataDomainContribution) PreviewCleanup(ctx context.Context, store *Store, cutoffMS int64) (int64, error) {
	if !c.SupportsCleanup() {
		return 0, fmt.Errorf("data-domain cleanup is not supported")
	}
	return c.CleanupPreview(ctx, store, cutoffMS)
}

func (c DataDomainContribution) ExecuteCleanup(ctx context.Context, store *Store, cutoffMS int64) (int64, error) {
	if !c.SupportsCleanup() {
		return 0, fmt.Errorf("data-domain cleanup is not supported")
	}
	return c.CleanupExecute(ctx, store, cutoffMS)
}

type dataDomainContributorRegistration struct {
	owner       uint64
	contributor DataDomainContributor
}

var dataDomainContributorRegistry = struct {
	sync.RWMutex
	nextOwner uint64
	owners    map[string][]dataDomainContributorRegistration
}{owners: make(map[string][]dataDomainContributorRegistration)}

// RegisterDataDomainContributor installs a lifecycle-bound data-domain
// provider. Registrations are owner-stacked so stopping an older plugin
// instance cannot unregister a newer replacement for the same domain.
func RegisterDataDomainContributor(id string, contributor DataDomainContributor) func() {
	id = strings.TrimSpace(id)
	if id == "" || contributor == nil {
		return func() {}
	}
	dataDomainContributorRegistry.Lock()
	dataDomainContributorRegistry.nextOwner++
	owner := dataDomainContributorRegistry.nextOwner
	dataDomainContributorRegistry.owners[id] = append(
		dataDomainContributorRegistry.owners[id],
		dataDomainContributorRegistration{owner: owner, contributor: contributor},
	)
	dataDomainContributorRegistry.Unlock()
	return func() {
		dataDomainContributorRegistry.Lock()
		defer dataDomainContributorRegistry.Unlock()
		registrations := dataDomainContributorRegistry.owners[id]
		for index := range registrations {
			if registrations[index].owner == owner {
				registrations = append(registrations[:index], registrations[index+1:]...)
				break
			}
		}
		if len(registrations) == 0 {
			delete(dataDomainContributorRegistry.owners, id)
			return
		}
		dataDomainContributorRegistry.owners[id] = registrations
	}
}

func registeredDataDomainContributors() map[string]DataDomainContributor {
	dataDomainContributorRegistry.RLock()
	defer dataDomainContributorRegistry.RUnlock()
	contributors := make(map[string]DataDomainContributor, len(dataDomainContributorRegistry.owners))
	for id, registrations := range dataDomainContributorRegistry.owners {
		if len(registrations) > 0 {
			contributors[id] = registrations[len(registrations)-1].contributor
		}
	}
	return contributors
}

type DataManagementOverview struct {
	Service           string                `json:"service"`
	DBPath            string                `json:"dbPath"`
	DBSizeBytes       int64                 `json:"dbSizeBytes"`
	WALSizeBytes      int64                 `json:"walSizeBytes"`
	Events            int64                 `json:"events"`
	DeadLetters       int64                 `json:"deadLetters"`
	LatestID          int64                 `json:"latestId"`
	LatestTimestampMS int64                 `json:"latestTimestampMs"`
	Generation        int64                 `json:"generation"`
	ResetAtMS         int64                 `json:"resetAtMs"`
	WebDAVEnabled     bool                  `json:"webdavEnabled"`
	WebDAVConfigured  bool                  `json:"webdavConfigured"`
	LastBackup        *DataOperation        `json:"lastBackup,omitempty"`
	Domains           []DataDomainInventory `json:"domains"`
	SecretClasses     []string              `json:"secretClasses"`
	UpdatedAtMS       int64                 `json:"updatedAtMs"`
}

type DataCleanupRequest struct {
	Domains         []string         `json:"domains"`
	BeforeMS        int64            `json:"beforeMs"`
	RetentionDays   int              `json:"retentionDays"`
	ExpectedRecords map[string]int64 `json:"expectedRecords,omitempty"`
}

type DataCleanupDomainPreview struct {
	ID          string `json:"id"`
	Records     int64  `json:"records"`
	CutoffMS    int64  `json:"cutoffMs"`
	Reclaimable bool   `json:"reclaimable"`
}

type DataCleanupPreview struct {
	Domains      []DataCleanupDomainPreview `json:"domains"`
	TotalRecords int64                      `json:"totalRecords"`
	CutoffMS     int64                      `json:"cutoffMs"`
}

var errDataCleanupPreviewStale = errors.New("cleanup preview is stale; preview the cleanup again")

type DataRestoreDomainPreview struct {
	ID             string   `json:"id"`
	Owner          string   `json:"owner"`
	CurrentRecords int64    `json:"currentRecords"`
	BackupRecords  int64    `json:"backupRecords"`
	Action         string   `json:"action"`
	Sensitivity    string   `json:"sensitivity"`
	SecretClasses  []string `json:"secretClasses"`
	Available      bool     `json:"available"`
	Error          string   `json:"error,omitempty"`
}

type DataRestorePreview struct {
	PolicyBackup       probackup.PolicyBackupPreview `json:"policyBackup"`
	BackupSHA256       string                        `json:"backupSha256"`
	LegacyBackup       bool                          `json:"legacyBackup"`
	IntegrityProtected bool                          `json:"integrityProtected"`
	Encrypted          bool                          `json:"encrypted"`
	RestoresAPIKeys    bool                          `json:"restoresAPIKeys"`
	Domains            []DataRestoreDomainPreview    `json:"domains"`
	SecretClasses      []string                      `json:"secretClasses"`
}

func normalizeJSONMap(raw string) map[string]any {
	value := map[string]any{}
	if strings.TrimSpace(raw) == "" || json.Unmarshal([]byte(raw), &value) != nil {
		return map[string]any{}
	}
	return value
}

func normalizeJSONStringSlice(raw string) []string {
	value := []string{}
	if strings.TrimSpace(raw) == "" || json.Unmarshal([]byte(raw), &value) != nil {
		return []string{}
	}
	return value
}

func (s *Store) StartDataOperation(ctx context.Context, operation DataOperation) (int64, error) {
	if operation.StartedAtMS <= 0 {
		operation.StartedAtMS = time.Now().UnixMilli()
	}
	if strings.TrimSpace(operation.Status) == "" {
		operation.Status = dataOperationRunning
	}
	secretClasses, err := json.Marshal(operation.SecretClasses)
	if err != nil {
		return 0, err
	}
	metadata, err := json.Marshal(operation.Metadata)
	if err != nil {
		return 0, err
	}
	result, err := s.executor(ctx).ExecContext(ctx, `insert into data_operations(
		kind, status, target, file_name, started_at_ms, finished_at_ms, size_bytes,
		affected_records, secret_classes_json, message, metadata_json
	) values(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		operation.Kind, operation.Status, operation.Target, operation.FileName,
		operation.StartedAtMS, operation.FinishedAtMS, operation.SizeBytes,
		operation.AffectedRecords, string(secretClasses), operation.Message, string(metadata))
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (s *Store) FinishDataOperation(ctx context.Context, operation DataOperation) error {
	if operation.ID <= 0 {
		return fmt.Errorf("data operation id is required")
	}
	if operation.FinishedAtMS <= 0 {
		operation.FinishedAtMS = time.Now().UnixMilli()
	}
	secretClasses, err := json.Marshal(operation.SecretClasses)
	if err != nil {
		return err
	}
	metadata, err := json.Marshal(operation.Metadata)
	if err != nil {
		return err
	}
	_, err = s.executor(ctx).ExecContext(ctx, `update data_operations set
		status = ?, target = ?, file_name = ?, finished_at_ms = ?, size_bytes = ?,
		affected_records = ?, secret_classes_json = ?, message = ?, metadata_json = ?
		where id = ?`, operation.Status, operation.Target, operation.FileName,
		operation.FinishedAtMS, operation.SizeBytes, operation.AffectedRecords,
		string(secretClasses), operation.Message, string(metadata), operation.ID)
	return err
}

func scanDataOperation(scanner interface{ Scan(...any) error }) (DataOperation, error) {
	var operation DataOperation
	var secretClassesJSON string
	var metadataJSON string
	err := scanner.Scan(
		&operation.ID, &operation.Kind, &operation.Status, &operation.Target,
		&operation.FileName, &operation.StartedAtMS, &operation.FinishedAtMS,
		&operation.SizeBytes, &operation.AffectedRecords, &secretClassesJSON,
		&operation.Message, &metadataJSON,
	)
	if err != nil {
		return DataOperation{}, err
	}
	operation.SecretClasses = normalizeJSONStringSlice(secretClassesJSON)
	operation.Metadata = normalizeJSONMap(metadataJSON)
	return operation, nil
}

func (s *Store) ListDataOperations(ctx context.Context, limit int) ([]DataOperation, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.executor(ctx).QueryContext(ctx, `select
		id, kind, status, target, file_name, started_at_ms, finished_at_ms,
		size_bytes, affected_records, secret_classes_json, message, metadata_json
		from data_operations order by started_at_ms desc, id desc limit ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	operations := make([]DataOperation, 0)
	for rows.Next() {
		operation, err := scanDataOperation(rows)
		if err != nil {
			return nil, err
		}
		operations = append(operations, operation)
	}
	return operations, rows.Err()
}

func (s *Store) LastDataOperation(ctx context.Context, kind string) (*DataOperation, error) {
	row := s.executor(ctx).QueryRowContext(ctx, `select
		id, kind, status, target, file_name, started_at_ms, finished_at_ms,
		size_bytes, affected_records, secret_classes_json, message, metadata_json
		from data_operations where kind = ? order by started_at_ms desc, id desc limit 1`, kind)
	operation, err := scanDataOperation(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &operation, nil
}

func (s *Store) LastSuccessfulDataOperation(ctx context.Context, kind string) (*DataOperation, error) {
	row := s.executor(ctx).QueryRowContext(ctx, `select
		id, kind, status, target, file_name, started_at_ms, finished_at_ms,
		size_bytes, affected_records, secret_classes_json, message, metadata_json
		from data_operations where kind = ? and status = ? order by finished_at_ms desc, id desc limit 1`, kind, dataOperationSuccess)
	operation, err := scanDataOperation(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &operation, nil
}

type domainQuery struct {
	owner         string
	schemaVersion int
	countSQL      string
	updatedSQL    string
	restoreMode   string
	sensitivity   string
	secretClasses []string
}

type sqlDataDomainContributor struct {
	query             domainQuery
	backupRecordTypes []string
	backupCounter     DataDomainBackupRecordCounter
	cleanupPreviewSQL string
	cleanupExecute    DataDomainCleanupHandler
}

func (c sqlDataDomainContributor) Inventory(ctx context.Context, store *Store) DataDomainInventory {
	domain := DataDomainInventory{
		Owner: c.query.owner, SchemaVersion: c.query.schemaVersion,
		BackupIncluded: c.query.restoreMode != "excluded", RestoreMode: c.query.restoreMode,
		CleanupSupported: c.SupportsCleanup(), Sensitivity: c.query.sensitivity,
		SecretClasses: append([]string(nil), c.query.secretClasses...), Available: true,
	}
	if err := store.executor(ctx).QueryRowContext(ctx, c.query.countSQL).Scan(&domain.Records); err != nil {
		domain.Available, domain.Error = false, err.Error()
	} else if err := store.executor(ctx).QueryRowContext(ctx, c.query.updatedSQL).Scan(&domain.UpdatedAtMS); err != nil {
		domain.Available, domain.Error = false, err.Error()
	}
	return domain
}

func (c sqlDataDomainContributor) CountBackupRecord(ctx context.Context, store *Store, recordType string, raw []byte) (int64, bool, error) {
	return DataDomainContribution{BackupRecordTypes: c.backupRecordTypes, BackupCounter: c.backupCounter}.CountBackupRecord(ctx, store, recordType, raw)
}

func (c sqlDataDomainContributor) SupportsCleanup() bool {
	return c.cleanupPreviewSQL != "" && c.cleanupExecute != nil
}

func (c sqlDataDomainContributor) PreviewCleanup(ctx context.Context, store *Store, cutoffMS int64) (int64, error) {
	if !c.SupportsCleanup() {
		return 0, fmt.Errorf("data-domain cleanup is not supported")
	}
	var records int64
	err := store.executor(ctx).QueryRowContext(ctx, c.cleanupPreviewSQL, cutoffMS).Scan(&records)
	return records, err
}

func (c sqlDataDomainContributor) ExecuteCleanup(ctx context.Context, store *Store, cutoffMS int64) (int64, error) {
	if !c.SupportsCleanup() {
		return 0, fmt.Errorf("data-domain cleanup is not supported")
	}
	return c.cleanupExecute(ctx, store, cutoffMS)
}

func deleteDataDomainRows(query string) DataDomainCleanupHandler {
	return func(ctx context.Context, store *Store, cutoffMS int64) (int64, error) {
		result, err := store.executor(ctx).ExecContext(ctx, query, cutoffMS)
		if err != nil {
			return 0, err
		}
		return result.RowsAffected()
	}
}

func builtInDataDomainContributors() map[string]DataDomainContributor {
	return map[string]DataDomainContributor{
		"usage-events": sqlDataDomainContributor{
			query:             domainQuery{owner: "observability", schemaVersion: 1, countSQL: `select count(*) from usage_events`, updatedSQL: `select coalesce(max(timestamp_ms), 0) from usage_events`, restoreMode: "merge", sensitivity: "sensitive", secretClasses: []string{"request_metadata", "network_identifiers"}},
			backupRecordTypes: []string{""}, cleanupPreviewSQL: `select count(*) from usage_events where timestamp_ms < ?`,
			cleanupExecute: func(ctx context.Context, store *Store, cutoffMS int64) (int64, error) {
				return store.DeleteEventsBefore(ctx, cutoffMS)
			},
		},
		"dead-letters": sqlDataDomainContributor{
			query:             domainQuery{owner: "observability", schemaVersion: 1, countSQL: `select count(*) from dead_letter_events`, updatedSQL: `select coalesce(max(created_at_ms), 0) from dead_letter_events`, restoreMode: "excluded", sensitivity: "sensitive", secretClasses: []string{"diagnostic_payloads"}},
			cleanupPreviewSQL: `select count(*) from dead_letter_events where created_at_ms < ?`, cleanupExecute: deleteDataDomainRows(`delete from dead_letter_events where created_at_ms < ?`),
		},
		"quota-cache": sqlDataDomainContributor{
			query:             domainQuery{owner: "quota", schemaVersion: 1, countSQL: `select count(*) from quota_cache`, updatedSQL: `select coalesce(max(accessed_at_ms), 0) from quota_cache`, restoreMode: "merge", sensitivity: "sensitive", secretClasses: []string{"account_quota_metadata"}},
			backupRecordTypes: []string{quotaCacheExportRecordType}, backupCounter: func(_ context.Context, _ *Store, _ string, raw []byte) (int64, error) {
				items, err := parseQuotaCacheImportRecord(raw)
				return int64(len(items)), err
			},
			cleanupPreviewSQL: `select count(*) from quota_cache where accessed_at_ms < ?`, cleanupExecute: deleteDataDomainRows(`delete from quota_cache where accessed_at_ms < ?`),
		},
		"routing-runtime": sqlDataDomainContributor{
			query:             domainQuery{owner: "scheduler", schemaVersion: 1, countSQL: `select count(*) from routing_cursor_state`, updatedSQL: `select coalesce(max(updated_at_ms), 0) from routing_cursor_state`, restoreMode: "replace", sensitivity: "internal"},
			backupRecordTypes: []string{routingCursorExportRecordType}, backupCounter: func(_ context.Context, _ *Store, _ string, raw []byte) (int64, error) {
				items, err := parseRoutingCursorImportRecord(raw)
				return int64(len(items)), err
			},
		},
		"account-runtime": sqlDataDomainContributor{
			query:             domainQuery{owner: "scheduler", schemaVersion: 1, countSQL: `select count(*) from auth_runtime_stats`, updatedSQL: `select coalesce(max(updated_at_ms), 0) from auth_runtime_stats`, restoreMode: "replace", sensitivity: "sensitive", secretClasses: []string{"credential_identifiers"}},
			backupRecordTypes: []string{authRuntimeStatsExportRecordType}, backupCounter: func(_ context.Context, _ *Store, _ string, raw []byte) (int64, error) {
				items, err := parseAuthRuntimeStatsImportRecord(raw)
				return int64(len(items)), err
			},
		},
		"model-pricing": sqlDataDomainContributor{
			query:             domainQuery{owner: "observability", schemaVersion: 2, countSQL: `select (select count(*) from model_prices) + (select count(*) from model_price_rules) + (select count(*) from model_price_rule_versions)`, updatedSQL: `select max(value) from (select coalesce(max(updated_at_ms), 0) value from model_prices union all select coalesce(max(updated_at_ms), 0) from model_price_rules union all select coalesce(max(created_at_ms), 0) from model_price_rule_versions)`, restoreMode: "merge", sensitivity: "internal"},
			backupRecordTypes: []string{modelPricesExportRecordType}, backupCounter: func(_ context.Context, _ *Store, _ string, raw []byte) (int64, error) {
				prices, rules, err := parseModelPricesImportRecord(raw)
				return int64(len(prices) + len(rules)), err
			},
		},
		"data-settings": sqlDataDomainContributor{
			query:             domainQuery{owner: "data-management", schemaVersion: 1, countSQL: `select count(*) from monitoring_settings`, updatedSQL: `select coalesce(max(updated_at_ms), 0) from monitoring_settings`, restoreMode: "replace", sensitivity: "secret", secretClasses: []string{"connector_credentials"}},
			backupRecordTypes: []string{monitoringSettingsExportRecordType}, backupCounter: func(_ context.Context, _ *Store, _ string, raw []byte) (int64, error) {
				_, err := parseMonitoringSettingsImportRecord(raw)
				return 1, err
			},
		},
		"pro-settings": sqlDataDomainContributor{
			query:             domainQuery{owner: "pro", schemaVersion: 1, countSQL: `select count(*) from pro_settings`, updatedSQL: `select coalesce(max(updated_at_ms), 0) from pro_settings`, restoreMode: "replace", sensitivity: "secret", secretClasses: []string{"configuration_secrets"}},
			backupRecordTypes: []string{proSettingsExportRecordType}, backupCounter: func(_ context.Context, _ *Store, _ string, raw []byte) (int64, error) {
				items, err := parseProSettingsImportRecord(raw)
				return int64(len(items)), err
			},
		},
		"operation-log": sqlDataDomainContributor{
			query:             domainQuery{owner: "data-management", schemaVersion: 1, countSQL: `select count(*) from data_operations`, updatedSQL: `select coalesce(max(started_at_ms), 0) from data_operations`, restoreMode: "excluded", sensitivity: "internal"},
			cleanupPreviewSQL: `select count(*) from data_operations where started_at_ms < ?`, cleanupExecute: deleteDataDomainRows(`delete from data_operations where started_at_ms < ?`),
		},
	}
}

func allDataDomainContributors() map[string]DataDomainContributor {
	contributors := builtInDataDomainContributors()
	for id, contributor := range registeredDataDomainContributors() {
		contributors[id] = contributor
	}
	return contributors
}

func (s *Store) ListDataDomains(ctx context.Context) ([]DataDomainInventory, error) {
	return s.listDataDomains(ctx, allDataDomainContributors())
}

func (s *Store) listDataDomains(ctx context.Context, contributors map[string]DataDomainContributor) ([]DataDomainInventory, error) {
	domains := make([]DataDomainInventory, 0, len(contributors))
	ids := make([]string, 0, len(contributors))
	for id := range contributors {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		domain := contributors[id].Inventory(ctx, s)
		domain.ID = id
		if strings.TrimSpace(domain.Owner) == "" {
			domain.Owner = "plugin"
		}
		if domain.SchemaVersion <= 0 {
			domain.SchemaVersion = 1
		}
		domain.CleanupSupported = contributors[id].SupportsCleanup()
		if domain.SecretClasses == nil {
			domain.SecretClasses = []string{}
		}
		domains = append(domains, domain)
	}
	return domains, nil
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return 0
	}
	return info.Size()
}

func uniqueSecretClasses(domains []DataDomainInventory) []string {
	seen := map[string]struct{}{}
	values := make([]string, 0)
	for _, domain := range domains {
		if !domain.BackupIncluded {
			continue
		}
		for _, value := range domain.SecretClasses {
			if _, exists := seen[value]; exists {
				continue
			}
			seen[value] = struct{}{}
			values = append(values, value)
		}
	}
	sort.Strings(values)
	return values
}

func (s *Server) dataManagementOverview(ctx context.Context) (DataManagementOverview, error) {
	events, deadLetters, err := s.store.Counts(ctx)
	if err != nil {
		return DataManagementOverview{}, err
	}
	latestID, latestTimestamp, err := s.store.LatestCursor(ctx)
	if err != nil {
		return DataManagementOverview{}, err
	}
	state, err := s.store.UsageDatasetState(ctx)
	if err != nil {
		return DataManagementOverview{}, err
	}
	settings, err := s.store.GetMonitoringSettings(ctx)
	if err != nil {
		return DataManagementOverview{}, err
	}
	domains, err := s.store.ListDataDomains(ctx)
	if err != nil {
		return DataManagementOverview{}, err
	}
	lastBackup, err := s.store.LastDataOperation(ctx, "backup")
	if err != nil {
		return DataManagementOverview{}, err
	}
	return DataManagementOverview{
		Service: "pro-data-management", DBPath: s.cfg.DBPath,
		DBSizeBytes: fileSize(s.cfg.DBPath), WALSizeBytes: fileSize(s.cfg.DBPath + "-wal"),
		Events: events, DeadLetters: deadLetters, LatestID: latestID,
		LatestTimestampMS: latestTimestamp, Generation: state.Generation, ResetAtMS: state.ResetAtMS,
		WebDAVEnabled: settings.WebDAV.Enabled, WebDAVConfigured: strings.TrimSpace(settings.WebDAV.URL) != "",
		LastBackup: lastBackup, Domains: domains, SecretClasses: uniqueSecretClasses(domains),
		UpdatedAtMS: time.Now().UnixMilli(),
	}, nil
}

func (s *Store) PreviewDataCleanup(ctx context.Context, request DataCleanupRequest, now time.Time) (DataCleanupPreview, error) {
	return s.previewDataCleanup(ctx, request, now, allDataDomainContributors())
}

func (s *Store) previewDataCleanup(ctx context.Context, request DataCleanupRequest, now time.Time, contributors map[string]DataDomainContributor) (DataCleanupPreview, error) {
	cutoff := request.BeforeMS
	if cutoff <= 0 && request.RetentionDays > 0 {
		cutoff = now.AddDate(0, 0, -request.RetentionDays).UnixMilli()
	}
	if cutoff <= 0 {
		return DataCleanupPreview{}, fmt.Errorf("cleanup cutoff is required")
	}
	domains := request.Domains
	if len(domains) == 0 {
		domains = []string{"usage-events"}
	}
	preview := DataCleanupPreview{Domains: make([]DataCleanupDomainPreview, 0, len(domains)), CutoffMS: cutoff}
	seen := map[string]struct{}{}
	for _, id := range domains {
		id = strings.TrimSpace(id)
		contributor, ok := contributors[id]
		if !ok || !contributor.SupportsCleanup() {
			return DataCleanupPreview{}, fmt.Errorf("cleanup is not supported for data domain %q", id)
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		records, err := contributor.PreviewCleanup(ctx, s, cutoff)
		if err != nil {
			return DataCleanupPreview{}, err
		}
		preview.Domains = append(preview.Domains, DataCleanupDomainPreview{ID: id, Records: records, CutoffMS: cutoff, Reclaimable: true})
		preview.TotalRecords += records
	}
	return preview, nil
}

func (s *Store) ExecuteDataCleanup(ctx context.Context, request DataCleanupRequest, now time.Time) (DataCleanupPreview, error) {
	contributors := allDataDomainContributors()
	if len(request.ExpectedRecords) == 0 {
		return DataCleanupPreview{}, fmt.Errorf("%w: expected record counts are required", errDataCleanupPreviewStale)
	}
	var result DataCleanupPreview
	err := s.RunImportTransaction(ctx, func(transactionCtx context.Context) error {
		preview, err := s.previewDataCleanup(transactionCtx, request, now, contributors)
		if err != nil {
			return err
		}
		if len(request.ExpectedRecords) != len(preview.Domains) {
			return errDataCleanupPreviewStale
		}
		for _, domain := range preview.Domains {
			expected, ok := request.ExpectedRecords[domain.ID]
			if !ok || expected != domain.Records {
				return errDataCleanupPreviewStale
			}
		}
		for index := range preview.Domains {
			domain := &preview.Domains[index]
			contributor := contributors[domain.ID]
			domain.Records, err = contributor.ExecuteCleanup(transactionCtx, s, preview.CutoffMS)
			if err != nil {
				return err
			}
			if domain.Records != request.ExpectedRecords[domain.ID] {
				return errDataCleanupPreviewStale
			}
		}
		preview.TotalRecords = 0
		for _, domain := range preview.Domains {
			preview.TotalRecords += domain.Records
		}
		result = preview
		return nil
	})
	if err != nil {
		return DataCleanupPreview{}, err
	}
	return result, nil
}

func backupHasIntegrityManifest(data []byte) bool {
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		recordType, err := readImportRecordType(line)
		return err == nil && recordType == backupManifestRecordType
	}
	return false
}

func (s *Server) policyRestorePreview(ctx context.Context, data []byte, allowLegacy bool) (probackup.PolicyBackupPreview, bool, error) {
	payload, hasPolicies, err := probackup.ExtractAPIKeyPoliciesRecord(data, allowLegacy)
	if err != nil {
		return probackup.PolicyBackupPreview{}, false, err
	}
	if hasPolicies {
		preview, err := probackup.Default.PreviewAPIKeyPolicies(ctx, payload)
		return preview, false, err
	}
	current, ok, err := probackup.Default.ExportAPIKeyPolicies()
	if err != nil {
		return probackup.PolicyBackupPreview{}, true, err
	}
	preview := probackup.PolicyBackupPreview{}
	if ok {
		currentPreview, err := probackup.Default.PreviewAPIKeyPolicies(ctx, current)
		if err != nil {
			return probackup.PolicyBackupPreview{}, true, err
		}
		preview.PreservePolicies = currentPreview.TargetPolicies
		preview.PreserveProfiles = currentPreview.TargetProfiles
		preview.CurrentDisabledKeys = currentPreview.CurrentDisabledKeys
		preview.TargetDisabledKeys = currentPreview.CurrentDisabledKeys
		preview.CurrentConcurrencyKeys = currentPreview.CurrentConcurrencyKeys
		preview.TargetConcurrencyKeys = currentPreview.CurrentConcurrencyKeys
		preview.CurrentTakeoverEnabled = currentPreview.CurrentTakeoverEnabled
		preview.TargetTakeoverEnabled = currentPreview.CurrentTakeoverEnabled
	}
	return preview, true, nil
}

func (s *Store) backupDomainRecordCounts(ctx context.Context, data []byte) (map[string]int64, error) {
	return s.backupDomainRecordCountsWithContributors(ctx, data, allDataDomainContributors())
}

func (s *Store) backupDomainRecordCountsWithContributors(ctx context.Context, data []byte, contributors map[string]DataDomainContributor) (map[string]int64, error) {
	counts := map[string]int64{}
	ids := make([]string, 0, len(contributors))
	for id := range contributors {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		recordType, err := readImportRecordType(line)
		if err != nil {
			return nil, err
		}
		if recordType == backupManifestRecordType {
			continue
		}
		claimedBy := ""
		var recordCount int64
		for _, id := range ids {
			count, claimed, countErr := contributors[id].CountBackupRecord(ctx, s, recordType, line)
			if countErr != nil {
				return nil, fmt.Errorf("count backup records for data domain %q: %w", id, countErr)
			}
			if !claimed {
				continue
			}
			if claimedBy != "" {
				return nil, fmt.Errorf("backup record type %q is claimed by multiple data domains: %s and %s", recordType, claimedBy, id)
			}
			claimedBy, recordCount = id, count
		}
		if claimedBy == "" {
			return nil, fmt.Errorf("unsupported backup record type %q", recordType)
		}
		if !isHostBackupRecordType(recordType) {
			importer, ok := contributors[claimedBy].(DataDomainBackupImporter)
			if !ok || !importer.CanImportBackupRecord(recordType) {
				return nil, fmt.Errorf("data domain %q claims backup record type %q without a restore importer", claimedBy, recordType)
			}
		}
		counts[claimedBy] += recordCount
	}
	return counts, nil
}

func isHostBackupRecordType(recordType string) bool {
	switch recordType {
	case "", "api_key_policies", accountInspectionScheduleExportRecordType, accountInspectionSnapshotExportRecordType,
		modelPricesExportRecordType, monitoringSettingsExportRecordType, quotaCacheExportRecordType,
		routingCursorExportRecordType, authRuntimeStatsExportRecordType, proSettingsExportRecordType:
		return true
	default:
		return false
	}
}

func backupRecordImporterFor(ctx context.Context, store *Store, contributors map[string]DataDomainContributor, recordType string, raw []byte) (string, DataDomainBackupImporter, error) {
	ids := make([]string, 0, len(contributors))
	for id := range contributors {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	claimedBy := ""
	var importer DataDomainBackupImporter
	for _, id := range ids {
		_, claimed, err := contributors[id].CountBackupRecord(ctx, store, recordType, raw)
		if err != nil {
			return "", nil, fmt.Errorf("count backup records for data domain %q: %w", id, err)
		}
		if !claimed {
			continue
		}
		if claimedBy != "" {
			return "", nil, fmt.Errorf("backup record type %q is claimed by multiple data domains: %s and %s", recordType, claimedBy, id)
		}
		candidate, ok := contributors[id].(DataDomainBackupImporter)
		if !ok || !candidate.CanImportBackupRecord(recordType) {
			return "", nil, fmt.Errorf("data domain %q claims backup record type %q without a restore importer", id, recordType)
		}
		claimedBy, importer = id, candidate
	}
	if claimedBy == "" {
		return "", nil, fmt.Errorf("unsupported backup record type %q", recordType)
	}
	return claimedBy, importer, nil
}

func (s *Server) previewBackupData(ctx context.Context, data []byte, allowLegacy, encrypted bool, encryptedSecretClasses []string) (DataRestorePreview, error) {
	contributors := allDataDomainContributors()
	integrityProtected := backupHasIntegrityManifest(data)
	if !integrityProtected && !allowLegacy {
		return DataRestorePreview{}, fmt.Errorf("backup manifest is required; retry as a trusted legacy backup only when the source is known")
	}
	policyPreview, legacyPolicy, err := s.policyRestorePreview(ctx, data, allowLegacy)
	if err != nil {
		return DataRestorePreview{}, err
	}
	backupCounts, err := s.store.backupDomainRecordCountsWithContributors(ctx, data, contributors)
	if err != nil {
		return DataRestorePreview{}, err
	}
	if policyPreview.HasPolicies {
		backupCounts["api-key-policy"] = int64(policyPreview.TargetPolicies + policyPreview.TargetProfiles + policyPreview.TargetDisabledKeys + policyPreview.TargetConcurrencyKeys)
	}
	current, err := s.store.listDataDomains(ctx, contributors)
	if err != nil {
		return DataRestorePreview{}, err
	}
	domains := make([]DataRestoreDomainPreview, 0, len(current))
	for _, domain := range current {
		backupRecords, included := backupCounts[domain.ID]
		action := "preserve"
		if included {
			action = domain.RestoreMode
			if action == "excluded" || action == "" {
				action = "preserve"
			}
		}
		domains = append(domains, DataRestoreDomainPreview{
			ID: domain.ID, Owner: domain.Owner, CurrentRecords: domain.Records,
			BackupRecords: backupRecords, Action: action, Sensitivity: domain.Sensitivity,
			SecretClasses: append([]string(nil), domain.SecretClasses...),
			Available:     domain.Available, Error: domain.Error,
		})
	}
	secretClasses := uniqueSecretClasses(current)
	if encrypted && len(encryptedSecretClasses) > 0 {
		secretClasses = append([]string(nil), encryptedSecretClasses...)
		sort.Strings(secretClasses)
	}
	return DataRestorePreview{
		PolicyBackup: policyPreview, BackupSHA256: fmt.Sprintf("%x", sha256.Sum256(data)),
		LegacyBackup:       legacyPolicy || !integrityProtected,
		IntegrityProtected: integrityProtected, Encrypted: encrypted, RestoresAPIKeys: false,
		Domains: domains, SecretClasses: secretClasses,
	}, nil
}

func readDataManagementBackup(c *gin.Context) ([]byte, bool, []string, error) {
	data, err := io.ReadAll(io.LimitReader(c.Request.Body, 96*1024*1024+1))
	if err != nil {
		return nil, false, nil, err
	}
	if len(data) > 96*1024*1024 {
		return nil, false, nil, fmt.Errorf("backup exceeds 96 MiB encrypted restore limit")
	}
	return decryptBackup(data, c.GetHeader("X-CLIProxy-Backup-Passphrase"))
}

func RegisterDataManagementGinRoutes(group *gin.RouterGroup) {
	server := defaultServer()
	if server == nil {
		registerDataManagementUnavailableRoutes(group)
		return
	}
	server.RegisterDataManagementGinRoutes(group)
}

func registerDataManagementUnavailableRoutes(group *gin.RouterGroup) {
	unavailable := func(c *gin.Context) {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Pro data management service is not available"})
	}
	for _, route := range []string{"/overview", "/domains", "/operations", "/backups", "/backups/export", "/settings"} {
		group.GET(route, unavailable)
	}
	for _, route := range []string{"/backups/export", "/backups/preview", "/backups/restore", "/backups/webdav/preview", "/backups/webdav/restore", "/backups/now", "/backups/test", "/maintenance/preview", "/maintenance/execute", "/statistics/reset"} {
		group.POST(route, unavailable)
	}
	group.PUT("/settings", unavailable)
}

func (s *Server) RegisterDataManagementGinRoutes(group *gin.RouterGroup) {
	if s == nil {
		registerDataManagementUnavailableRoutes(group)
		return
	}
	group.GET("/overview", s.handleDataManagementOverview)
	group.GET("/domains", s.handleDataManagementDomains)
	group.GET("/operations", s.handleDataManagementOperations)
	group.GET("/backups", s.handleDataManagementBackups)
	group.GET("/backups/export", s.handleDataManagementBackupExport)
	group.POST("/backups/export", s.handleDataManagementEncryptedBackupExport)
	group.POST("/backups/preview", s.handleDataManagementBackupPreview)
	group.POST("/backups/restore", s.handleDataManagementBackupRestore)
	group.POST("/backups/webdav/preview", s.handleDataManagementWebDAVBackupPreview)
	group.POST("/backups/webdav/restore", s.handleDataManagementWebDAVBackupRestore)
	group.POST("/backups/now", s.handleDataManagementBackupNow)
	group.POST("/backups/test", s.handleDataManagementWebDAVTest)
	group.POST("/maintenance/preview", s.handleDataManagementCleanupPreview)
	group.POST("/maintenance/execute", withBackupWriteBarrier(s.handleDataManagementCleanupExecute))
	group.GET("/settings", s.handleMonitoringSettingsGet)
	group.PUT("/settings", withBackupWriteBarrier(s.handleMonitoringSettingsPut))
	group.POST("/statistics/reset", s.handleUsageReset)
}

func (s *Server) handleDataManagementOverview(c *gin.Context) {
	overview, err := s.dataManagementOverview(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, overview)
}

func (s *Server) handleDataManagementDomains(c *gin.Context) {
	domains, err := s.store.ListDataDomains(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"domains": domains, "secretClasses": uniqueSecretClasses(domains)})
}

func (s *Server) handleDataManagementOperations(c *gin.Context) {
	operations, err := s.store.ListDataOperations(c.Request.Context(), parseQueryInt(c, "limit", 50))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"operations": operations})
}

func (s *Server) handleDataManagementBackups(c *gin.Context) {
	settings, err := s.store.GetMonitoringSettings(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	operations, err := s.store.ListDataOperations(c.Request.Context(), parseQueryInt(c, "limit", 50))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	backups := []WebDAVBackup{}
	if strings.TrimSpace(settings.WebDAV.URL) != "" {
		ctx, cancel := webDAVContext(c.Request.Context())
		defer cancel()
		backups, err = listWebDAVBackups(ctx, s.webDAVClient, settings.WebDAV.URL, settings.WebDAV)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error(), "operations": operations})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"backups": backups, "operations": operations})
}

func (s *Server) recordDownloadOperation(ctx context.Context, fileName string, data []byte, encrypted bool, secretClasses []string) {
	operation := DataOperation{
		Kind: "export", Status: dataOperationSuccess, Target: "download", FileName: fileName,
		StartedAtMS: time.Now().UnixMilli(), FinishedAtMS: time.Now().UnixMilli(),
		SizeBytes: int64(len(data)), SecretClasses: append([]string(nil), secretClasses...),
		Metadata: map[string]any{"encrypted": encrypted},
	}
	_, _ = s.store.StartDataOperation(context.WithoutCancel(ctx), operation)
}

func (s *Server) handleDataManagementBackupExport(c *gin.Context) {
	data, err := s.exportJSONL(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	domains, _ := s.store.ListDataDomains(c.Request.Context())
	secretClasses := uniqueSecretClasses(domains)
	passphrase := c.GetHeader("X-CLIProxy-Backup-Passphrase")
	encrypted := strings.TrimSpace(passphrase) != ""
	fileName := dataManagementBackupFileName(time.Now())
	contentType := "application/x-ndjson"
	if encrypted {
		data, err = encryptBackup(data, passphrase, secretClasses)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		fileName = strings.TrimSuffix(fileName, ".jsonl") + ".encrypted.json"
		contentType = "application/json"
	}
	s.recordDownloadOperation(c.Request.Context(), fileName, data, encrypted, secretClasses)
	c.Header("Content-Type", contentType)
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, fileName))
	_, _ = c.Writer.Write(data)
}

func (s *Server) handleDataManagementEncryptedBackupExport(c *gin.Context) {
	var request struct {
		Passphrase string `json:"passphrase"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	data, err := s.exportJSONL(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	domains, _ := s.store.ListDataDomains(c.Request.Context())
	secretClasses := uniqueSecretClasses(domains)
	encrypted, err := encryptBackup(data, request.Passphrase, secretClasses)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	fileName := strings.TrimSuffix(dataManagementBackupFileName(time.Now()), ".jsonl") + ".encrypted.json"
	s.recordDownloadOperation(c.Request.Context(), fileName, encrypted, true, secretClasses)
	c.Header("Content-Type", "application/json")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, fileName))
	_, _ = c.Writer.Write(encrypted)
}

func (s *Server) handleDataManagementBackupPreview(c *gin.Context) {
	data, encrypted, encryptedSecrets, err := readDataManagementBackup(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	preview, err := s.previewBackupData(c.Request.Context(), data, allowLegacyUsageImport(c), encrypted, encryptedSecrets)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, preview)
}

func (s *Server) handleDataManagementBackupRestore(c *gin.Context) {
	data, encrypted, encryptedSecrets, err := readDataManagementBackup(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s.restoreDataManagementBackup(c, data, encrypted, encryptedSecrets, "upload", "", allowLegacyUsageImport(c))
}

func (s *Server) restoreDataManagementBackup(c *gin.Context, data []byte, encrypted bool, encryptedSecrets []string, target, fileName string, allowLegacy bool) {
	secretClasses := append([]string(nil), encryptedSecrets...)
	if len(secretClasses) == 0 {
		if domains, listErr := s.store.ListDataDomains(c.Request.Context()); listErr == nil {
			secretClasses = uniqueSecretClasses(domains)
		}
	}
	operation := DataOperation{
		Kind: "restore", Status: dataOperationRunning, Target: target, FileName: fileName,
		StartedAtMS: time.Now().UnixMilli(), SizeBytes: int64(len(data)),
		SecretClasses: secretClasses, Metadata: map[string]any{"encrypted": encrypted},
	}
	operation.ID, _ = s.store.StartDataOperation(c.Request.Context(), operation)
	c.Request.Body = io.NopCloser(bytes.NewReader(data))
	query := c.Request.URL.Query()
	if allowLegacy {
		query.Set("allow_legacy", "1")
	} else {
		query.Del("allow_legacy")
	}
	c.Request.URL.RawQuery = query.Encode()
	s.handleUsageImport(c)
	operation.FinishedAtMS = time.Now().UnixMilli()
	if c.Writer.Status() >= 200 && c.Writer.Status() < 300 {
		operation.Status = dataOperationSuccess
	} else {
		operation.Status = dataOperationFailed
		operation.Message = fmt.Sprintf("restore request failed with status %d", c.Writer.Status())
	}
	_ = s.store.FinishDataOperation(context.WithoutCancel(c.Request.Context()), operation)
}

type dataManagementWebDAVRestoreRequest struct {
	FileName       string `json:"fileName"`
	AllowLegacy    bool   `json:"allowLegacy"`
	ExpectedSHA256 string `json:"expectedSha256"`
}

func dataManagementWebDAVRestoreRequestFrom(c *gin.Context) (dataManagementWebDAVRestoreRequest, error) {
	var request dataManagementWebDAVRestoreRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		return request, fmt.Errorf("invalid WebDAV restore request")
	}
	request.FileName = strings.TrimSpace(request.FileName)
	if request.FileName == "" {
		return request, fmt.Errorf("WebDAV backup file name is required")
	}
	return request, nil
}

func (s *Server) handleDataManagementWebDAVBackupPreview(c *gin.Context) {
	request, err := dataManagementWebDAVRestoreRequestFrom(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	data, err := s.fetchWebDAVBackup(c.Request.Context(), request.FileName)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	preview, err := s.previewBackupData(c.Request.Context(), data, true, false, nil)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, preview)
}

func (s *Server) handleDataManagementWebDAVBackupRestore(c *gin.Context) {
	request, err := dataManagementWebDAVRestoreRequestFrom(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	expectedSHA256 := strings.ToLower(strings.TrimSpace(request.ExpectedSHA256))
	if len(expectedSHA256) != sha256.Size*2 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "WebDAV restore preview SHA-256 is required"})
		return
	}
	if _, err = hex.DecodeString(expectedSHA256); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid WebDAV restore preview SHA-256"})
		return
	}
	data, err := s.fetchWebDAVBackup(c.Request.Context(), request.FileName)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	actualSHA256 := fmt.Sprintf("%x", sha256.Sum256(data))
	if actualSHA256 != expectedSHA256 {
		c.JSON(http.StatusConflict, gin.H{"error": "WebDAV backup changed after preview; preview it again before restoring"})
		return
	}
	s.restoreDataManagementBackup(c, data, false, nil, "webdav", request.FileName, request.AllowLegacy)
}

func (s *Server) handleDataManagementBackupNow(c *gin.Context) {
	backup, err := s.backupToWebDAVTracked(c.Request.Context(), "manual")
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"backup": backup})
}

func (s *Server) backupToWebDAVTracked(ctx context.Context, trigger string) (WebDAVBackup, error) {
	settings, err := s.store.GetMonitoringSettings(ctx)
	if err != nil {
		return WebDAVBackup{}, err
	}
	return s.backupToWebDAVWithConfigTracked(ctx, settings.WebDAV, trigger)
}

func (s *Server) backupToWebDAVWithConfigTracked(ctx context.Context, cfg MonitoringWebDAVBackupConfig, trigger string) (WebDAVBackup, error) {
	return s.backupToWebDAVWithConfigTrackedClient(ctx, cfg, trigger, s.webDAVClient)
}

func (s *Server) backupToWebDAVWithConfigTrackedClient(ctx context.Context, cfg MonitoringWebDAVBackupConfig, trigger string, webDAVClient *http.Client) (WebDAVBackup, error) {
	ctx, cancel := webDAVContext(ctx)
	defer cancel()
	cfg = normalizeMonitoringSettings(MonitoringSettings{WebDAV: cfg}).WebDAV
	if strings.TrimSpace(cfg.URL) == "" {
		return WebDAVBackup{}, fmt.Errorf("WebDAV backup URL is not configured")
	}
	domains, _ := s.store.ListDataDomains(ctx)
	now := time.Now().UTC()
	fileName := dataManagementBackupFileName(now)
	operation := DataOperation{
		Kind: "backup", Status: dataOperationRunning, Target: "webdav", FileName: fileName,
		StartedAtMS: now.UnixMilli(), SecretClasses: uniqueSecretClasses(domains),
		Metadata: map[string]any{"trigger": trigger, "encrypted": false, "targetKey": webDAVTargetKey(cfg)},
	}
	operation.ID, _ = s.store.StartDataOperation(ctx, operation)
	finishFailed := func(failure error) (WebDAVBackup, error) {
		operation.Status = dataOperationFailed
		operation.FinishedAtMS = time.Now().UnixMilli()
		operation.Message = failure.Error()
		_ = s.store.FinishDataOperation(context.WithoutCancel(ctx), operation)
		return WebDAVBackup{}, failure
	}
	data, err := s.exportJSONL(ctx)
	if err != nil {
		return finishFailed(err)
	}
	backupURL := dataManagementBackupPath(cfg.URL, now)
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, backupURL, bytes.NewReader(data))
	if err != nil {
		return finishFailed(err)
	}
	request.Header.Set("Content-Type", "application/x-ndjson")
	setWebDAVAuth(request, cfg)
	client := webDAVClient
	if client == nil {
		client = newWebDAVHTTPClient()
	}
	response, err := client.Do(request)
	if err != nil {
		return finishFailed(err)
	}
	response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return finishFailed(fmt.Errorf("webdav upload failed with status %d", response.StatusCode))
	}
	if cfg.RetentionDays > 0 {
		if _, pruneErr := pruneWebDAVBackups(ctx, client, strings.TrimRight(cfg.URL, "/"), cfg, now); pruneErr != nil {
			operation.Metadata["retentionWarning"] = pruneErr.Error()
		}
	}
	operation.Status = dataOperationSuccess
	operation.FinishedAtMS = time.Now().UnixMilli()
	operation.SizeBytes = int64(len(data))
	operation.AffectedRecords = int64(bytes.Count(data, []byte{'\n'}))
	_ = s.store.FinishDataOperation(context.WithoutCancel(ctx), operation)
	return WebDAVBackup{FileName: fileName, SizeBytes: int64(len(data)), LastModified: now.Format(http.TimeFormat), LastModifiedMS: now.UnixMilli()}, nil
}

type WebDAVConnectionTestResult struct {
	Connected bool  `json:"connected"`
	Writable  bool  `json:"writable"`
	Deletable bool  `json:"deletable"`
	LatencyMS int64 `json:"latencyMs"`
}

func testWebDAVConnection(ctx context.Context, client *http.Client, cfg MonitoringWebDAVBackupConfig) (WebDAVConnectionTestResult, error) {
	ctx, cancel := webDAVContext(ctx)
	defer cancel()
	cfg = normalizeMonitoringSettings(MonitoringSettings{WebDAV: cfg}).WebDAV
	if strings.TrimSpace(cfg.URL) == "" {
		return WebDAVConnectionTestResult{}, fmt.Errorf("WebDAV backup URL is not configured")
	}
	if client == nil {
		client = newWebDAVHTTPClient()
	}
	randomBytes := make([]byte, 8)
	if _, err := rand.Read(randomBytes); err != nil {
		return WebDAVConnectionTestResult{}, err
	}
	fileName := ".cliproxy-pro-write-test-" + hex.EncodeToString(randomBytes) + ".tmp"
	testURL := strings.TrimRight(cfg.URL, "/") + "/" + fileName
	startedAt := time.Now()
	putRequest, err := http.NewRequestWithContext(ctx, http.MethodPut, testURL, strings.NewReader("cliproxy-pro-webdav-test"))
	if err != nil {
		return WebDAVConnectionTestResult{}, err
	}
	putRequest.Header.Set("Content-Type", "application/octet-stream")
	setWebDAVAuth(putRequest, cfg)
	putResponse, err := client.Do(putRequest)
	if err != nil {
		return WebDAVConnectionTestResult{}, err
	}
	putResponse.Body.Close()
	if putResponse.StatusCode < 200 || putResponse.StatusCode >= 300 {
		return WebDAVConnectionTestResult{}, fmt.Errorf("webdav write test failed with status %d", putResponse.StatusCode)
	}
	result := WebDAVConnectionTestResult{Connected: true, Writable: true, LatencyMS: time.Since(startedAt).Milliseconds()}
	deleteRequest, err := http.NewRequestWithContext(ctx, http.MethodDelete, testURL, nil)
	if err != nil {
		return result, err
	}
	setWebDAVAuth(deleteRequest, cfg)
	deleteResponse, err := client.Do(deleteRequest)
	if err != nil {
		return result, fmt.Errorf("webdav write test succeeded but cleanup failed: %w", err)
	}
	deleteResponse.Body.Close()
	if deleteResponse.StatusCode < 200 || deleteResponse.StatusCode >= 300 {
		return result, fmt.Errorf("webdav write test succeeded but cleanup failed with status %d", deleteResponse.StatusCode)
	}
	result.Deletable = true
	result.LatencyMS = time.Since(startedAt).Milliseconds()
	return result, nil
}

func (s *Server) handleDataManagementWebDAVTest(c *gin.Context) {
	settings, err := s.store.GetMonitoringSettings(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	result, err := testWebDAVConnection(c.Request.Context(), s.webDAVClient, settings.WebDAV)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}

func (s *Server) handleDataManagementCleanupPreview(c *gin.Context) {
	var request DataCleanupRequest
	if err := json.NewDecoder(c.Request.Body).Decode(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	preview, err := s.store.PreviewDataCleanup(c.Request.Context(), request, time.Now())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, preview)
}

func (s *Server) handleDataManagementCleanupExecute(c *gin.Context) {
	var request DataCleanupRequest
	if err := json.NewDecoder(c.Request.Body).Decode(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	operation := DataOperation{Kind: "cleanup", Target: strings.Join(request.Domains, ","), SecretClasses: []string{}}
	operation.ID, _ = s.store.StartDataOperation(c.Request.Context(), operation)
	result, err := s.store.ExecuteDataCleanup(c.Request.Context(), request, time.Now())
	operation.FinishedAtMS = time.Now().UnixMilli()
	if err != nil {
		operation.Status = dataOperationFailed
		operation.Message = err.Error()
		_ = s.store.FinishDataOperation(context.WithoutCancel(c.Request.Context()), operation)
		status := http.StatusInternalServerError
		if errors.Is(err, errDataCleanupPreviewStale) {
			status = http.StatusConflict
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	operation.Status = dataOperationSuccess
	operation.AffectedRecords = result.TotalRecords
	operation.Metadata = map[string]any{"cutoffMs": result.CutoffMS}
	_ = s.store.FinishDataOperation(context.WithoutCancel(c.Request.Context()), operation)
	c.JSON(http.StatusOK, result)
}

func dataManagementBackupFileName(now time.Time) string {
	return fmt.Sprintf("cliproxy-pro-backup-%s.jsonl", now.UTC().Format("20060102_150405_000"))
}

func dataManagementBackupPath(baseURL string, now time.Time) string {
	return strings.TrimRight(baseURL, "/") + "/" + filepath.Base(dataManagementBackupFileName(now))
}
