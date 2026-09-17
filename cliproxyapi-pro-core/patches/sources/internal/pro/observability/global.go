package observability

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	probackup "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/backup"
	prostate "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/state"
)

var globalService *Service
var proSettingConsumers = make(map[string][]proSettingConsumerRegistration)
var proSettingConsumerGeneration uint64
var globalStateMu sync.RWMutex
var proSettingApplyMu sync.Mutex
var globalStateWriter *prostate.Writer
var legacyInspectionDomainMu sync.Mutex
var legacyInspectionScheduleDomainUnregister func()
var legacyInspectionSnapshotDomainUnregister func()

func SetDefaultService(service *Service) {
	globalStateMu.Lock()
	if globalStateWriter != nil {
		globalStateWriter.Close()
	}
	globalStateWriter = nil
	globalService = service
	if service != nil && service.store != nil {
		globalStateWriter = prostate.NewWriter(service.store)
	}
	globalStateMu.Unlock()
}

// EstimateUsageCostMicros prices one terminal usage record against the active
// model-price snapshot. A missing service or rule is an error so cost quotas
// fail closed instead of silently treating unknown usage as free.
func EstimateUsageCostMicros(ctx context.Context, input UsageCostInput) (int64, error) {
	globalStateMu.RLock()
	service := globalService
	globalStateMu.RUnlock()
	if service == nil || service.store == nil {
		return 0, fmt.Errorf("model price service is unavailable")
	}
	return service.store.EstimateUsageCostMicros(ctx, input)
}

// MissingModelPriceRules returns the normalized requested models that do not
// have an active server-side price rule.
func MissingModelPriceRules(ctx context.Context, models []string) ([]string, error) {
	globalStateMu.RLock()
	service := globalService
	globalStateMu.RUnlock()
	if service == nil || service.store == nil {
		return nil, fmt.Errorf("model price service is unavailable")
	}
	rules, err := service.store.ActiveModelPriceRules(ctx)
	if err != nil {
		return nil, err
	}
	priced := make(map[string]struct{}, len(rules))
	for _, rule := range rules {
		if model := strings.TrimSpace(rule.Model); model != "" {
			priced[model] = struct{}{}
		}
	}
	missingSet := make(map[string]struct{})
	for _, raw := range models {
		model := strings.TrimSpace(raw)
		if model == "" {
			continue
		}
		if _, ok := priced[model]; !ok {
			missingSet[model] = struct{}{}
		}
	}
	missing := make([]string, 0, len(missingSet))
	for model := range missingSet {
		missing = append(missing, model)
	}
	sort.Strings(missing)
	return missing, nil
}

func stopRuntimeStateWriter(service *Service) {
	globalStateMu.Lock()
	if globalService != service {
		globalStateMu.Unlock()
		return
	}
	if globalStateWriter != nil {
		globalStateWriter.Close()
	}
	globalStateWriter = nil
	globalService = nil
	globalStateMu.Unlock()
}

func flushRuntimeStateWrites(ctx context.Context, store *Store) error {
	if ctx == nil {
		ctx = context.Background()
	}
	globalStateMu.RLock()
	defer globalStateMu.RUnlock()
	var writer *prostate.Writer
	if globalService != nil && globalService.store == store {
		writer = globalStateWriter
	}
	if writer == nil {
		return nil
	}
	return writer.Flush(ctx)
}

func SetAccountInspectionScheduleHandlers(exporter func() ([]byte, bool, error), importer func([]byte) error) {
	probackup.Default.SetInspectionSchedule(exporter, importer)
	legacyInspectionDomainMu.Lock()
	defer legacyInspectionDomainMu.Unlock()
	if legacyInspectionScheduleDomainUnregister != nil {
		legacyInspectionScheduleDomainUnregister()
		legacyInspectionScheduleDomainUnregister = nil
	}
	if exporter != nil {
		legacyInspectionScheduleDomainUnregister = RegisterDataDomainContributor("account-inspection-schedule", accountInspectionScheduleDataDomain(exporter))
	}
}

func RegisterAccountInspectionScheduleHandlers(exporter func() ([]byte, bool, error), importer func([]byte) error) func() {
	backupUnregister := probackup.Default.RegisterInspectionSchedule(exporter, importer)
	domainUnregister := RegisterDataDomainContributor("account-inspection-schedule", accountInspectionScheduleDataDomain(exporter))
	return func() {
		domainUnregister()
		backupUnregister()
	}
}

func accountInspectionScheduleDataDomain(exporter func() ([]byte, bool, error)) DataDomainContribution {
	return DataDomainContribution{
		InventoryFunc: func(_ context.Context, _ *Store) DataDomainInventory {
			domain := DataDomainInventory{Owner: "inspection", SchemaVersion: 1, BackupIncluded: true, RestoreMode: "replace", Sensitivity: "internal", Available: true}
			if exporter == nil {
				domain.Available, domain.Error = false, "schedule contributor is unavailable"
				return domain
			}
			if raw, ok, err := exporter(); err != nil {
				domain.Available, domain.Error = false, err.Error()
			} else if ok && len(raw) > 0 {
				domain.Records = 1
			}
			return domain
		},
		BackupRecordTypes: []string{accountInspectionScheduleExportRecordType},
	}
}

func SetAccountInspectionSnapshotHandlers(exporter func() ([]byte, bool, error), importer func([]byte) error) {
	probackup.Default.SetInspectionSnapshot(exporter, importer)
	legacyInspectionDomainMu.Lock()
	defer legacyInspectionDomainMu.Unlock()
	if legacyInspectionSnapshotDomainUnregister != nil {
		legacyInspectionSnapshotDomainUnregister()
		legacyInspectionSnapshotDomainUnregister = nil
	}
	if exporter != nil {
		legacyInspectionSnapshotDomainUnregister = RegisterDataDomainContributor("account-inspection-snapshot", accountInspectionSnapshotDataDomain(exporter))
	}
}

func RegisterAccountInspectionSnapshotHandlers(exporter func() ([]byte, bool, error), importer func([]byte) error) func() {
	backupUnregister := probackup.Default.RegisterInspectionSnapshot(exporter, importer)
	domainUnregister := RegisterDataDomainContributor("account-inspection-snapshot", accountInspectionSnapshotDataDomain(exporter))
	return func() {
		domainUnregister()
		backupUnregister()
	}
}

func accountInspectionSnapshotDataDomain(exporter func() ([]byte, bool, error)) DataDomainContribution {
	return DataDomainContribution{
		InventoryFunc: func(_ context.Context, _ *Store) DataDomainInventory {
			domain := DataDomainInventory{Owner: "inspection", SchemaVersion: 1, BackupIncluded: true, RestoreMode: "replace", Sensitivity: "sensitive", SecretClasses: []string{"account_metadata"}, Available: true}
			if exporter == nil {
				domain.Available, domain.Error = false, "snapshot contributor is unavailable"
				return domain
			}
			if raw, ok, err := exporter(); err != nil {
				domain.Available, domain.Error = false, err.Error()
			} else if ok && len(raw) > 0 && string(raw) != "null" {
				domain.Records = 1
			}
			return domain
		},
		BackupRecordTypes: []string{accountInspectionSnapshotExportRecordType},
	}
}

func SetAuthRuntimeStateImportHandler(importer func([]RoutingCursorState, []AuthRuntimeStats) error) {
	probackup.Default.SetRuntimeStateImporter(importer)
}

func RegisterAuthRuntimeStateImportHandler(importer func([]RoutingCursorState, []AuthRuntimeStats) error) func() {
	return probackup.Default.RegisterRuntimeStateImporter(importer)
}

func SetLegacyQuotaCleanupHandler(cleanup func(context.Context) error) {
	probackup.Default.SetLegacyCleanup(cleanup)
}

func RegisterLegacyQuotaCleanupHandler(cleanup func(context.Context) error) func() {
	return probackup.Default.RegisterLegacyCleanup(cleanup)
}

type proSettingConsumerRegistration struct {
	generation uint64
	apply      func(context.Context, ProSetting) error
}

// RegisterProSettingConsumer installs one lifecycle-bound runtime consumer for a
// Pro setting namespace. The returned function unregisters only this exact
// registration, so a stopped owner cannot remove a newer replacement.
func RegisterProSettingConsumer(namespace string, apply func(context.Context, ProSetting) error) func() {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" || apply == nil {
		return func() {}
	}
	globalStateMu.Lock()
	proSettingConsumerGeneration++
	generation := proSettingConsumerGeneration
	registrations := proSettingConsumers[namespace]
	registrations = append(registrations, proSettingConsumerRegistration{generation: generation, apply: apply})
	proSettingConsumers[namespace] = registrations
	globalStateMu.Unlock()
	return func() {
		globalStateMu.Lock()
		registrations := proSettingConsumers[namespace]
		for index := range registrations {
			if registrations[index].generation == generation {
				registrations = append(registrations[:index], registrations[index+1:]...)
				break
			}
		}
		if len(registrations) == 0 {
			delete(proSettingConsumers, namespace)
		} else {
			proSettingConsumers[namespace] = registrations
		}
		globalStateMu.Unlock()
	}
}

func ApplyImportedProSettings(ctx context.Context, settings []ProSetting) error {
	settings = normalizeOAuthPolicySettings(settings)
	for _, item := range settings {
		globalStateMu.RLock()
		registrations := proSettingConsumers[item.Namespace]
		var consumer proSettingConsumerRegistration
		if count := len(registrations); count > 0 {
			consumer = registrations[count-1]
		}
		globalStateMu.RUnlock()
		if consumer.apply == nil {
			continue
		}
		if err := consumer.apply(ctx, item); err != nil {
			return err
		}
	}
	return nil
}

func defaultServer() *Server {
	globalStateMu.RLock()
	defer globalStateMu.RUnlock()
	if globalService == nil {
		return nil
	}
	return globalService.Server()
}

func SetQuotaCache(ctx context.Context, entry QuotaCacheEntry) error {
	return probackup.Default.ExecuteWrite(ctx, func(ctx context.Context) error {
		globalStateMu.RLock()
		defer globalStateMu.RUnlock()
		if globalService == nil || globalService.store == nil {
			return fmt.Errorf("usage service is not available")
		}
		return globalService.store.SetQuotaCache(ctx, entry)
	})
}

func GetQuotaCache(ctx context.Context, provider, fileName string) ([]QuotaCacheEntry, error) {
	globalStateMu.RLock()
	defer globalStateMu.RUnlock()
	if globalService == nil || globalService.store == nil {
		return nil, fmt.Errorf("usage service is not available")
	}
	return globalService.store.GetQuotaCache(ctx, provider, fileName)
}

func GetProSetting(ctx context.Context, namespace string) (ProSetting, bool, error) {
	globalStateMu.RLock()
	defer globalStateMu.RUnlock()
	if globalService == nil || globalService.store == nil {
		return ProSetting{}, false, nil
	}
	return globalService.store.GetProSetting(ctx, namespace)
}

func SetProSetting(ctx context.Context, item ProSetting) error {
	return probackup.Default.ExecuteWrite(ctx, func(ctx context.Context) error {
		return setProSetting(ctx, item)
	})
}

// SetProSettingAndApplyLatest keeps a live settings update inside the backup
// write barrier until the latest committed value has been applied. Concurrent
// live writers may still overlap, so each caller re-reads the durable winner
// instead of applying its own possibly stale draft.
func SetProSettingAndApplyLatest(ctx context.Context, item ProSetting, apply func(context.Context, ProSetting) error) error {
	return probackup.Default.ExecuteWrite(ctx, func(ctx context.Context) error {
		if err := setProSetting(ctx, item); err != nil {
			return err
		}
		if apply == nil {
			return nil
		}
		proSettingApplyMu.Lock()
		defer proSettingApplyMu.Unlock()
		latest, found, err := GetProSetting(ctx, item.Namespace)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("pro setting %q was not persisted", item.Namespace)
		}
		return apply(ctx, latest)
	})
}

func setProSetting(ctx context.Context, item ProSetting) error {
	globalStateMu.RLock()
	defer globalStateMu.RUnlock()
	if globalService == nil || globalService.store == nil {
		return fmt.Errorf("usage service is not available")
	}
	return globalService.store.SetProSetting(ctx, item)
}

func DeleteProSetting(ctx context.Context, namespace string) error {
	return probackup.Default.ExecuteWrite(ctx, func(ctx context.Context) error {
		return deleteProSetting(ctx, namespace)
	})
}

func deleteProSetting(ctx context.Context, namespace string) error {
	globalStateMu.RLock()
	defer globalStateMu.RUnlock()
	if globalService == nil || globalService.store == nil {
		return fmt.Errorf("usage service is not available")
	}
	return globalService.store.DeleteProSetting(ctx, namespace)
}

func QueueRoutingCursorState(state RoutingCursorState) {
	probackup.Default.TryExecuteWrite(func() {
		globalStateMu.RLock()
		defer globalStateMu.RUnlock()
		if globalStateWriter != nil {
			globalStateWriter.QueueRoutingCursor(state)
		}
	})
}

func GetRoutingCursorState(ctx context.Context, cursorKey string) (RoutingCursorState, bool, error) {
	globalStateMu.RLock()
	defer globalStateMu.RUnlock()
	if globalService == nil || globalService.store == nil {
		return RoutingCursorState{}, false, nil
	}
	return globalService.store.GetRoutingCursorState(ctx, cursorKey)
}

func ListRoutingCursorStates(ctx context.Context) ([]RoutingCursorState, error) {
	globalStateMu.RLock()
	defer globalStateMu.RUnlock()
	if globalService == nil || globalService.store == nil {
		return nil, nil
	}
	return globalService.store.ListRoutingCursorStates(ctx)
}

func QueueAuthRuntimeStats(item AuthRuntimeStats) {
	probackup.Default.TryExecuteWrite(func() {
		globalStateMu.RLock()
		defer globalStateMu.RUnlock()
		if globalStateWriter != nil {
			globalStateWriter.QueueAuthRuntimeStats(item)
		}
	})
}

func GetAuthRuntimeStats(ctx context.Context, authIndex, authID string) (AuthRuntimeStats, bool, error) {
	globalStateMu.RLock()
	defer globalStateMu.RUnlock()
	if globalService == nil || globalService.store == nil {
		return AuthRuntimeStats{}, false, nil
	}
	return globalService.store.GetAuthRuntimeStats(ctx, authIndex, authID)
}

// DeleteQuotaCache invalidates only cached provider quota data. Credential
// replacement must retain auth runtime statistics and routing cursor ownership.
func DeleteQuotaCache(ctx context.Context, provider, fileName string) error {
	return probackup.Default.ExecuteWrite(ctx, func(ctx context.Context) error {
		globalStateMu.RLock()
		defer globalStateMu.RUnlock()
		if globalService == nil || globalService.store == nil {
			return nil
		}
		return globalService.store.DeleteQuotaCache(ctx, provider, fileName)
	})
}

func DeleteAuthRuntimeState(ctx context.Context, authID, authIndex, fileName string) error {
	return probackup.Default.ExecuteWrite(ctx, func(ctx context.Context) error {
		globalStateMu.RLock()
		defer globalStateMu.RUnlock()
		service := globalService
		writer := globalStateWriter
		if service == nil || service.store == nil {
			return nil
		}
		if writer == nil {
			return service.store.DeleteAuthRuntimeState(ctx, authID, authIndex, fileName)
		}
		return writer.Delete(ctx, authID, authIndex, fileName)
	})
}

// WithProSettingWriter keeps a guarded read/modify/write and its live mutation
// inside one backup barrier. The writer is valid only during this callback.
func WithProSettingWriter(ctx context.Context, update func(context.Context, func(ProSetting) error) error) error {
	return probackup.Default.ExecuteWrite(ctx, func(ctx context.Context) error {
		return update(ctx, func(item ProSetting) error { return setProSetting(ctx, item) })
	})
}
