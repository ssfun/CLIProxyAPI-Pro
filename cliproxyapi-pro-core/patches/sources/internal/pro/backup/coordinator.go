// Package backup coordinates cross-module export/import hooks without making
// the observability transport own inspection or live routing state.
package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	prostate "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/state"
)

type JSONExporter func() ([]byte, bool, error)
type JSONImporter func([]byte) error
type ContextJSONImporter func(context.Context, []byte) error
type PolicyPreviewer func(context.Context, []byte) (PolicyBackupPreview, error)
type RuntimeStateImporter func([]prostate.RoutingCursor, []prostate.AuthRuntimeStats) error

type FlushFunc func(context.Context) error
type ExportFunc func(context.Context) ([]byte, error)

// PolicyBackupPreview is transport-neutral restore metadata. Counts describe
// the committed policy database that would be replaced (or preserved for a
// legacy backup) and the staged target's association with the current upstream
// API-key configuration. It never contains a key or fingerprint.
type PolicyBackupPreview struct {
	CurrentConcurrencyKeys      int `json:"currentConcurrencyKeys"`
	TargetConcurrencyKeys       int `json:"targetConcurrencyKeys"`
	ChangedConcurrencyKeys      int `json:"changedConcurrencyKeys"`
	EffectiveConcurrencyChanges int `json:"effectiveConcurrencyChanges"`
	CurrentDisabledKeys         int `json:"currentDisabledKeys"`
	TargetDisabledKeys          int `json:"targetDisabledKeys"`
	AddedDisabledKeys           int `json:"addedDisabledKeys"`
	RemovedDisabledKeys         int `json:"removedDisabledKeys"`
	NewlyBlockedKeys            int `json:"newlyBlockedKeys"`
	NewlyAllowedKeys            int `json:"newlyAllowedKeys"`

	HasPolicies            bool `json:"hasPolicies"`
	ReplacePolicies        int  `json:"replacePolicies"`
	PreservePolicies       int  `json:"preservePolicies"`
	ReplaceProfiles        int  `json:"replaceProfiles"`
	PreserveProfiles       int  `json:"preserveProfiles"`
	TargetPolicies         int  `json:"targetPolicies"`
	TargetProfiles         int  `json:"targetProfiles"`
	AssociatedPolicies     int  `json:"associatedPolicies"`
	OrphanedPolicies       int  `json:"orphanedPolicies"`
	CurrentTakeoverEnabled bool `json:"currentTakeoverEnabled"`
	TargetTakeoverEnabled  bool `json:"targetTakeoverEnabled"`
}

type transactionContextKey struct{}
type transactionState struct {
	tx          *sql.Tx
	afterCommit []func()
}

// WithTransaction lets backup domains that share the Pro SQLite file join the
// observability import transaction without importing each other.
func WithTransaction(ctx context.Context, tx *sql.Tx) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if tx == nil {
		return ctx
	}
	return context.WithValue(ctx, transactionContextKey{}, &transactionState{tx: tx})
}

func Transaction(ctx context.Context) *sql.Tx {
	if ctx == nil {
		return nil
	}
	state, _ := ctx.Value(transactionContextKey{}).(*transactionState)
	if state == nil {
		return nil
	}
	return state.tx
}

func AfterCommit(ctx context.Context, callback func()) bool {
	state, _ := ctx.Value(transactionContextKey{}).(*transactionState)
	if state == nil || callback == nil {
		return false
	}
	state.afterCommit = append(state.afterCommit, callback)
	return true
}

func RunAfterCommit(ctx context.Context) {
	state, _ := ctx.Value(transactionContextKey{}).(*transactionState)
	if state == nil {
		return
	}
	callbacks := state.afterCommit
	state.afterCommit = nil
	for _, callback := range callbacks {
		callback()
	}
}

// ImportPlan captures the cross-module restore barriers. Parsing remains at the
// HTTP boundary, while this coordinator owns lifecycle and application order.
type ImportPlan struct {
	FlushQueues         func(context.Context) error
	ImportDatabase      func(context.Context) error
	ReloadConfiguration func(context.Context) error
	ApplyRuntimeState   func(context.Context) error
	RestoreInspection   func(context.Context) error
	CleanupLegacy       func(context.Context) error
	Rollback            func(context.Context) error
}

type Lifecycle struct {
	Pause  func(context.Context) error
	Resume func(context.Context) error
}

type inspectionRegistration struct {
	owner    uint64
	exporter JSONExporter
	importer JSONImporter
}

type policyRegistration struct {
	owner     uint64
	exporter  JSONExporter
	importer  ContextJSONImporter
	previewer PolicyPreviewer
}

type runtimeRegistration struct {
	owner    uint64
	importer RuntimeStateImporter
}

type cleanupRegistration struct {
	owner   uint64
	cleanup func(context.Context) error
}

type lifecycleRegistration struct {
	owner     uint64
	lifecycle Lifecycle
}

type Coordinator struct {
	mu        sync.RWMutex
	importMu  sync.Mutex
	writeGate sync.RWMutex

	scheduleExporter JSONExporter
	scheduleImporter JSONImporter
	scheduleOwner    uint64
	scheduleOwners   []inspectionRegistration
	snapshotExporter JSONExporter
	snapshotImporter JSONImporter
	snapshotOwner    uint64
	snapshotOwners   []inspectionRegistration
	policyExporter   JSONExporter
	policyImporter   ContextJSONImporter
	policyPreviewer  PolicyPreviewer
	policyOwner      uint64
	policyOwners     []policyRegistration
	runtimeImporter  RuntimeStateImporter
	runtimeOwner     uint64
	runtimeOwners    []runtimeRegistration
	legacyCleanup    func(context.Context) error
	cleanupOwner     uint64
	cleanupOwners    []cleanupRegistration
	nextOwner        uint64
	lifecycles       []lifecycleRegistration
}

// RegisterAPIKeyPolicies installs the single policy backup domain used by
// JSONL and WebDAV. The owner stack prevents an older App shutdown from
// unregistering a newer replacement.
func (c *Coordinator) RegisterAPIKeyPolicies(exporter JSONExporter, importer ContextJSONImporter, previewer PolicyPreviewer) func() {
	if c == nil {
		return func() {}
	}
	c.mu.Lock()
	c.nextOwner++
	owner := c.nextOwner
	c.policyOwner = owner
	c.policyExporter, c.policyImporter, c.policyPreviewer = exporter, importer, previewer
	c.policyOwners = append(c.policyOwners, policyRegistration{owner: owner, exporter: exporter, importer: importer, previewer: previewer})
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		for index := range c.policyOwners {
			if c.policyOwners[index].owner == owner {
				c.policyOwners = append(c.policyOwners[:index], c.policyOwners[index+1:]...)
				break
			}
		}
		if c.policyOwner != owner {
			return
		}
		if count := len(c.policyOwners); count > 0 {
			current := c.policyOwners[count-1]
			c.policyOwner = current.owner
			c.policyExporter, c.policyImporter, c.policyPreviewer = current.exporter, current.importer, current.previewer
			return
		}
		c.policyOwner = 0
		c.policyExporter, c.policyImporter, c.policyPreviewer = nil, nil, nil
	}
}

func (c *Coordinator) ExportAPIKeyPolicies() ([]byte, bool, error) {
	if c == nil {
		return nil, false, nil
	}
	c.mu.RLock()
	exporter := c.policyExporter
	c.mu.RUnlock()
	if exporter == nil {
		return nil, false, nil
	}
	return exporter()
}

func (c *Coordinator) ImportAPIKeyPolicies(ctx context.Context, payload []byte) error {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	importer := c.policyImporter
	c.mu.RUnlock()
	if importer == nil {
		return fmt.Errorf("API key policy backup importer is unavailable")
	}
	return importer(ctx, payload)
}

func (c *Coordinator) PreviewAPIKeyPolicies(ctx context.Context, payload []byte) (PolicyBackupPreview, error) {
	if c == nil {
		return PolicyBackupPreview{}, nil
	}
	c.mu.RLock()
	previewer := c.policyPreviewer
	c.mu.RUnlock()
	if previewer == nil {
		return PolicyBackupPreview{}, fmt.Errorf("API key policy backup previewer is unavailable")
	}
	return previewer(ctx, payload)
}

func NewCoordinator() *Coordinator { return &Coordinator{} }

var Default = NewCoordinator()

// ExecuteWrite admits one ordinary state mutation while excluding backup
// imports. Callers should keep the operation bounded and honor ctx themselves.
func (c *Coordinator) ExecuteWrite(ctx context.Context, operation func(context.Context) error) error {
	if operation == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if c == nil {
		return operation(ctx)
	}
	c.writeGate.RLock()
	defer c.writeGate.RUnlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	return operation(ctx)
}

// TryExecuteWrite admits a best-effort, non-blocking mutation. It is intended
// for high-frequency runtime snapshots produced while callers may hold their
// own locks. A false result means an import owns or is waiting on the barrier;
// the stale snapshot must be dropped rather than applied after the import.
func (c *Coordinator) TryExecuteWrite(operation func()) bool {
	if operation == nil {
		return true
	}
	if c == nil {
		operation()
		return true
	}
	if !c.writeGate.TryRLock() {
		return false
	}
	defer c.writeGate.RUnlock()
	operation()
	return true
}

func (c *Coordinator) SetInspectionSchedule(exporter JSONExporter, importer JSONImporter) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.nextOwner++
	c.scheduleOwner = c.nextOwner
	c.scheduleOwners = nil
	c.scheduleExporter, c.scheduleImporter = exporter, importer
	c.mu.Unlock()
}

// RegisterInspectionSchedule installs lifecycle-owned inspection hooks. The
// returned function only removes this exact registration, so an older owner
// cannot clear a newer replacement during shutdown.
func (c *Coordinator) RegisterInspectionSchedule(exporter JSONExporter, importer JSONImporter) func() {
	if c == nil {
		return func() {}
	}
	c.mu.Lock()
	c.nextOwner++
	owner := c.nextOwner
	c.scheduleOwner = owner
	c.scheduleExporter, c.scheduleImporter = exporter, importer
	c.scheduleOwners = append(c.scheduleOwners, inspectionRegistration{owner: owner, exporter: exporter, importer: importer})
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		if c.scheduleOwner == owner {
			c.scheduleOwners = removeInspectionRegistration(c.scheduleOwners, owner)
			if count := len(c.scheduleOwners); count > 0 {
				current := c.scheduleOwners[count-1]
				c.scheduleOwner = current.owner
				c.scheduleExporter, c.scheduleImporter = current.exporter, current.importer
			} else {
				c.scheduleExporter, c.scheduleImporter = nil, nil
				c.scheduleOwner = 0
			}
		} else {
			c.scheduleOwners = removeInspectionRegistration(c.scheduleOwners, owner)
		}
		c.mu.Unlock()
	}
}

func (c *Coordinator) SetInspectionSnapshot(exporter JSONExporter, importer JSONImporter) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.nextOwner++
	c.snapshotOwner = c.nextOwner
	c.snapshotOwners = nil
	c.snapshotExporter, c.snapshotImporter = exporter, importer
	c.mu.Unlock()
}

func (c *Coordinator) RegisterInspectionSnapshot(exporter JSONExporter, importer JSONImporter) func() {
	if c == nil {
		return func() {}
	}
	c.mu.Lock()
	c.nextOwner++
	owner := c.nextOwner
	c.snapshotOwner = owner
	c.snapshotExporter, c.snapshotImporter = exporter, importer
	c.snapshotOwners = append(c.snapshotOwners, inspectionRegistration{owner: owner, exporter: exporter, importer: importer})
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		if c.snapshotOwner == owner {
			c.snapshotOwners = removeInspectionRegistration(c.snapshotOwners, owner)
			if count := len(c.snapshotOwners); count > 0 {
				current := c.snapshotOwners[count-1]
				c.snapshotOwner = current.owner
				c.snapshotExporter, c.snapshotImporter = current.exporter, current.importer
			} else {
				c.snapshotExporter, c.snapshotImporter = nil, nil
				c.snapshotOwner = 0
			}
		} else {
			c.snapshotOwners = removeInspectionRegistration(c.snapshotOwners, owner)
		}
		c.mu.Unlock()
	}
}

func (c *Coordinator) SetRuntimeStateImporter(importer RuntimeStateImporter) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.nextOwner++
	c.runtimeOwner = c.nextOwner
	c.runtimeOwners = nil
	c.runtimeImporter = importer
	c.mu.Unlock()
}

func (c *Coordinator) RegisterRuntimeStateImporter(importer RuntimeStateImporter) func() {
	if c == nil {
		return func() {}
	}
	c.mu.Lock()
	c.nextOwner++
	owner := c.nextOwner
	c.runtimeOwner = owner
	c.runtimeImporter = importer
	c.runtimeOwners = append(c.runtimeOwners, runtimeRegistration{owner: owner, importer: importer})
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		if c.runtimeOwner == owner {
			c.runtimeOwners = removeRuntimeRegistration(c.runtimeOwners, owner)
			if count := len(c.runtimeOwners); count > 0 {
				current := c.runtimeOwners[count-1]
				c.runtimeOwner, c.runtimeImporter = current.owner, current.importer
			} else {
				c.runtimeImporter = nil
				c.runtimeOwner = 0
			}
		} else {
			c.runtimeOwners = removeRuntimeRegistration(c.runtimeOwners, owner)
		}
		c.mu.Unlock()
	}
}

func (c *Coordinator) SetLegacyCleanup(cleanup func(context.Context) error) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.nextOwner++
	c.cleanupOwner = c.nextOwner
	c.cleanupOwners = nil
	c.legacyCleanup = cleanup
	c.mu.Unlock()
}

func (c *Coordinator) RegisterLegacyCleanup(cleanup func(context.Context) error) func() {
	if c == nil {
		return func() {}
	}
	c.mu.Lock()
	c.nextOwner++
	owner := c.nextOwner
	c.cleanupOwner = owner
	c.legacyCleanup = cleanup
	c.cleanupOwners = append(c.cleanupOwners, cleanupRegistration{owner: owner, cleanup: cleanup})
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		if c.cleanupOwner == owner {
			c.cleanupOwners = removeCleanupRegistration(c.cleanupOwners, owner)
			if count := len(c.cleanupOwners); count > 0 {
				current := c.cleanupOwners[count-1]
				c.cleanupOwner, c.legacyCleanup = current.owner, current.cleanup
			} else {
				c.legacyCleanup = nil
				c.cleanupOwner = 0
			}
		} else {
			c.cleanupOwners = removeCleanupRegistration(c.cleanupOwners, owner)
		}
		c.mu.Unlock()
	}
}

func removeInspectionRegistration(items []inspectionRegistration, owner uint64) []inspectionRegistration {
	for index := range items {
		if items[index].owner == owner {
			return append(items[:index], items[index+1:]...)
		}
	}
	return items
}

func removeRuntimeRegistration(items []runtimeRegistration, owner uint64) []runtimeRegistration {
	for index := range items {
		if items[index].owner == owner {
			return append(items[:index], items[index+1:]...)
		}
	}
	return items
}

func removeCleanupRegistration(items []cleanupRegistration, owner uint64) []cleanupRegistration {
	for index := range items {
		if items[index].owner == owner {
			return append(items[:index], items[index+1:]...)
		}
	}
	return items
}

func (c *Coordinator) CleanupLegacy(ctx context.Context) error {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	cleanup := c.legacyCleanup
	c.mu.RUnlock()
	if cleanup == nil {
		return nil
	}
	return cleanup(ctx)
}

func (c *Coordinator) RegisterLifecycle(lifecycle Lifecycle) func() {
	if c == nil || (lifecycle.Pause == nil && lifecycle.Resume == nil) {
		return func() {}
	}
	c.mu.Lock()
	c.nextOwner++
	owner := c.nextOwner
	c.lifecycles = append(c.lifecycles, lifecycleRegistration{owner: owner, lifecycle: lifecycle})
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		for index := range c.lifecycles {
			if c.lifecycles[index].owner == owner {
				c.lifecycles = append(c.lifecycles[:index], c.lifecycles[index+1:]...)
				break
			}
		}
		c.mu.Unlock()
	}
}

// ExecuteImport enforces the restore order shared by HTTP imports and future
// non-HTTP restore surfaces. Successfully paused modules are always resumed in
// reverse order, including when a later import phase fails.
func (c *Coordinator) ExecuteImport(ctx context.Context, plan ImportPlan) (err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if c == nil {
		return executeImportPhases(ctx, plan)
	}
	c.writeGate.Lock()
	defer c.writeGate.Unlock()
	c.importMu.Lock()
	defer c.importMu.Unlock()
	cleanupCtx := context.WithoutCancel(ctx)
	c.mu.RLock()
	registrations := append([]lifecycleRegistration(nil), c.lifecycles...)
	c.mu.RUnlock()
	lifecycles := make([]Lifecycle, 0, len(registrations))
	for _, registration := range registrations {
		lifecycles = append(lifecycles, registration.lifecycle)
	}
	paused := make([]Lifecycle, 0, len(lifecycles))
	for _, lifecycle := range lifecycles {
		if lifecycle.Pause == nil && lifecycle.Resume == nil {
			continue
		}
		if lifecycle.Pause != nil {
			if err := lifecycle.Pause(ctx); err != nil {
				// Pause implementations may have already closed their admission
				// gate before waiting for active work. Include the failing lifecycle
				// in cleanup and never use the canceled request context to resume.
				resumeLifecycles(cleanupCtx, append(paused, lifecycle))
				return err
			}
		}
		paused = append(paused, lifecycle)
	}
	defer func() {
		if resumeErr := resumeLifecycles(cleanupCtx, paused); err == nil {
			err = resumeErr
		}
	}()
	err = executeImportPhases(ctx, plan)
	if err != nil && plan.Rollback != nil {
		if rollbackErr := plan.Rollback(cleanupCtx); rollbackErr != nil {
			return fmt.Errorf("%w (rollback failed: %v)", err, rollbackErr)
		}
	}
	return err
}

func executeImportPhases(ctx context.Context, plan ImportPlan) error {
	for _, phase := range []func(context.Context) error{
		plan.FlushQueues,
		plan.ImportDatabase,
		plan.ReloadConfiguration,
		plan.ApplyRuntimeState,
		plan.RestoreInspection,
		plan.CleanupLegacy,
	} {
		if phase != nil {
			if err := phase(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

func resumeLifecycles(ctx context.Context, lifecycles []Lifecycle) error {
	var firstErr error
	for index := len(lifecycles) - 1; index >= 0; index-- {
		if lifecycles[index].Resume == nil {
			continue
		}
		if err := lifecycles[index].Resume(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

type inspectionScheduleRecord struct {
	RecordType string          `json:"record_type"`
	Version    int             `json:"version"`
	Schedule   json.RawMessage `json:"schedule"`
	ExportedAt int64           `json:"exported_at_ms"`
}

type inspectionSnapshotRecord struct {
	RecordType string          `json:"record_type"`
	Version    int             `json:"version"`
	Snapshot   json.RawMessage `json:"snapshot"`
	ExportedAt int64           `json:"exported_at_ms"`
}

type apiKeyPoliciesRecord struct {
	RecordType string          `json:"record_type"`
	Version    int             `json:"version"`
	Policies   json.RawMessage `json:"policies"`
	ExportedAt int64           `json:"exported_at_ms"`
}

// ExtractAPIKeyPoliciesRecord verifies the manifest when present and returns
// the single policy domain payload. A missing record is a valid legacy backup.
func ExtractAPIKeyPoliciesRecord(data []byte, allowLegacy bool) ([]byte, bool, error) {
	lines := bytes.Split(data, []byte{'\n'})
	var manifest *manifestRecord
	var payload []byte
	policyRecords := 0
	var hashed []byte
	nonEmpty := 0
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var header struct {
			RecordType string `json:"record_type"`
		}
		if err := json.Unmarshal(line, &header); err != nil {
			return nil, false, err
		}
		if header.RecordType == "backup_manifest" {
			if nonEmpty != 0 || manifest != nil {
				return nil, false, fmt.Errorf("backup manifest must be the first and only manifest record")
			}
			var parsed manifestRecord
			if err := json.Unmarshal(line, &parsed); err != nil || parsed.Version != 1 || parsed.Records < 0 || len(parsed.SHA256) != sha256.Size*2 {
				return nil, false, fmt.Errorf("invalid backup manifest")
			}
			manifest = &parsed
			nonEmpty++
			continue
		}
		nonEmpty++
		if manifest != nil {
			hashed = append(hashed, line...)
			hashed = append(hashed, '\n')
		}
		if header.RecordType != "api_key_policies" {
			continue
		}
		var record apiKeyPoliciesRecord
		if err := json.Unmarshal(line, &record); err != nil || (record.Version != 1 && record.Version != 2) || !json.Valid(record.Policies) {
			return nil, false, fmt.Errorf("invalid API key policy backup record")
		}
		payload = append(payload[:0], record.Policies...)
		policyRecords++
	}
	if manifest == nil && !allowLegacy {
		return nil, false, fmt.Errorf("backup manifest is required")
	}
	if manifest != nil {
		digest := sha256.Sum256(hashed)
		if manifest.Records != bytes.Count(hashed, []byte{'\n'}) || manifest.SHA256 != fmt.Sprintf("%x", digest) {
			return nil, false, fmt.Errorf("backup manifest verification failed")
		}
	}
	if policyRecords > 1 {
		return nil, false, fmt.Errorf("backup contains multiple API key policy records")
	}
	return payload, policyRecords == 1, nil
}

type manifestRecord struct {
	RecordType string `json:"record_type"`
	Version    int    `json:"version"`
	Records    int    `json:"records"`
	SHA256     string `json:"sha256"`
	ExportedAt int64  `json:"exported_at_ms"`
}

// ExportJSONL owns the flush/snapshot/manifest sequence while preserving the
// existing line format byte-for-byte.
func (c *Coordinator) ExportJSONL(ctx context.Context, flush FlushFunc, export ExportFunc) ([]byte, error) {
	if flush != nil {
		if err := flush(ctx); err != nil {
			return nil, err
		}
	}
	if c != nil {
		c.writeGate.RLock()
		defer c.writeGate.RUnlock()
	}
	var data []byte
	var err error
	if export != nil {
		data, err = export(ctx)
		if err != nil {
			return nil, err
		}
	}
	appendRecord := func(record any) error {
		line, err := json.Marshal(record)
		if err != nil {
			return err
		}
		data = append(data, line...)
		data = append(data, '\n')
		return nil
	}
	if schedule, ok, err := c.ExportInspectionSchedule(); ok || err != nil {
		if err != nil {
			return nil, err
		}
		if ok {
			if err := appendRecord(inspectionScheduleRecord{
				RecordType: "account_inspection_schedule", Version: 1,
				Schedule: schedule, ExportedAt: time.Now().UnixMilli(),
			}); err != nil {
				return nil, err
			}
		}
	}
	if snapshot, ok, err := c.ExportInspectionSnapshot(); ok || err != nil {
		if err != nil {
			return nil, err
		}
		if ok {
			if err := appendRecord(inspectionSnapshotRecord{
				RecordType: "account_inspection_snapshot", Version: 1,
				Snapshot: snapshot, ExportedAt: time.Now().UnixMilli(),
			}); err != nil {
				return nil, err
			}
		}
	}
	if policies, ok, err := c.ExportAPIKeyPolicies(); ok || err != nil {
		if err != nil {
			return nil, err
		}
		if ok {
			if !json.Valid(policies) {
				return nil, fmt.Errorf("API key policy exporter returned invalid JSON")
			}
			if err := appendRecord(apiKeyPoliciesRecord{
				RecordType: "api_key_policies", Version: 2,
				Policies: policies, ExportedAt: time.Now().UnixMilli(),
			}); err != nil {
				return nil, err
			}
		}
	}
	digest := sha256.Sum256(data)
	manifest, err := json.Marshal(manifestRecord{
		RecordType: "backup_manifest", Version: 1,
		Records: bytes.Count(data, []byte{'\n'}), SHA256: fmt.Sprintf("%x", digest),
		ExportedAt: time.Now().UnixMilli(),
	})
	if err != nil {
		return nil, err
	}
	output := make([]byte, 0, len(manifest)+1+len(data))
	output = append(output, manifest...)
	output = append(output, '\n')
	output = append(output, data...)
	return output, nil
}

func (c *Coordinator) ExportInspectionSchedule() ([]byte, bool, error) {
	if c == nil {
		return nil, false, nil
	}
	c.mu.RLock()
	exporter := c.scheduleExporter
	c.mu.RUnlock()
	if exporter == nil {
		return nil, false, nil
	}
	return exporter()
}

func (c *Coordinator) ExportInspectionSnapshot() ([]byte, bool, error) {
	if c == nil {
		return nil, false, nil
	}
	c.mu.RLock()
	exporter := c.snapshotExporter
	c.mu.RUnlock()
	if exporter == nil {
		return nil, false, nil
	}
	return exporter()
}

func (c *Coordinator) ImportInspectionSchedule(raw []byte) error {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	importer := c.scheduleImporter
	c.mu.RUnlock()
	if importer == nil {
		return nil
	}
	return importer(raw)
}

func (c *Coordinator) ImportInspectionSnapshot(raw []byte) error {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	importer := c.snapshotImporter
	c.mu.RUnlock()
	if importer == nil {
		return nil
	}
	return importer(raw)
}

func (c *Coordinator) HasRuntimeStateImporter() bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.runtimeImporter != nil
}

func (c *Coordinator) ImportRuntimeState(cursors []prostate.RoutingCursor, stats []prostate.AuthRuntimeStats) error {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	importer := c.runtimeImporter
	c.mu.RUnlock()
	if importer == nil {
		return nil
	}
	return importer(cursors, stats)
}
