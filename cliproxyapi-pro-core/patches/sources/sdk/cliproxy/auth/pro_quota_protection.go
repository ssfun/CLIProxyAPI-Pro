package auth

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/embeddedusage"
	prorouting "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/routing"
	"time"
)

var ErrQuotaProtectionChanged = errors.New("quota protection or account changed")

type quotaProtectionRecord struct {
	Identity    string                                `json:"identity"`
	Protections map[string]prorouting.QuotaProtection `json:"protections"`
}

func decodeQuotaProtectionRecords(item embeddedusage.ProSetting) (map[string]quotaProtectionRecord, error) {
	records := make(map[string]quotaProtectionRecord)
	if len(item.Settings) == 0 {
		return records, nil
	}
	if item.SchemaVersion != 1 {
		return nil, errors.New("unsupported quota protection schema")
	}
	err := json.Unmarshal(item.Settings, &records)
	if records == nil {
		records = make(map[string]quotaProtectionRecord)
	}
	return records, err
}

func setQuotaProtections(auth *Auth, protections map[string]prorouting.QuotaProtection) {
	// Upstream Clone shares empty maps; always detach before mutating this field.
	metadata := make(map[string]any, len(auth.Metadata)+1)
	for key, value := range auth.Metadata {
		if key != prorouting.QuotaProtectionMetadataKey {
			metadata[key] = value
		}
	}
	if len(protections) != 0 {
		raw, _ := json.Marshal(protections)
		metadata[prorouting.QuotaProtectionMetadataKey] = string(raw)
	}
	if len(metadata) == 0 {
		metadata = nil
	}
	auth.Metadata = metadata
}

func restoreQuotaProtection(auth *Auth) {
	item, _, err := embeddedusage.GetProSetting(context.Background(), prorouting.QuotaProtectionNamespace)
	if err != nil {
		return
	}
	records, err := decodeQuotaProtectionRecords(item)
	if err != nil {
		return
	}
	dropLegacyRoutingQuotaRecords(records)
	applyQuotaProtectionRecord(auth, records[auth.ID])
}

func applyQuotaProtectionRecord(auth *Auth, record quotaProtectionRecord) {
	if record.Identity != authRuntimeIdentityFingerprint(auth) {
		setQuotaProtections(auth, nil)
		return
	}
	cleaned, _ := prorouting.WithoutLegacyRoutingQuotaProtections(record.Protections)
	setQuotaProtections(auth, cleaned)
}

// CAS on source revision prevents late probes from releasing newer restrictions.
// SQLite owns persistence, including plugin virtual and runtime-only accounts.
func (m *Manager) ChangeQuotaProtection(ctx context.Context, base *Auth, source string, expected int64, next *prorouting.QuotaProtection) error {
	if m == nil || base == nil || source == "" {
		return ErrQuotaProtectionChanged
	}
	err := embeddedusage.WithProSettingWriter(ctx, func(ctx context.Context, persist func(embeddedusage.ProSetting) error) error {
		m.mu.Lock()
		defer m.mu.Unlock()
		current := m.auths[base.ID]
		if !inspectionRefreshIdentityMatches(base, current) || current.Disabled != base.Disabled {
			return ErrQuotaProtectionChanged
		}
		if prorouting.IsLegacyRoutingQuotaSource(source) {
			next = nil
		}
		protections := prorouting.ParseQuotaProtections(current.Metadata)
		if protections[source].Revision != expected {
			return ErrQuotaProtectionChanged
		}
		if next != nil {
			if current.Disabled || current.Status == StatusDisabled {
				return ErrQuotaProtectionChanged
			}
			copy := *next
			copy.Source = source
			copy.Revision = time.Now().UnixNano()
			protections[source] = copy
		} else {
			delete(protections, source)
		}
		protections, _ = prorouting.WithoutLegacyRoutingQuotaProtections(protections)
		item, _, err := embeddedusage.GetProSetting(ctx, prorouting.QuotaProtectionNamespace)
		if err != nil {
			return err
		}
		records, err := decodeQuotaProtectionRecords(item)
		if err != nil {
			return err
		}
		if len(protections) == 0 {
			delete(records, current.ID)
		} else {
			records[current.ID] = quotaProtectionRecord{Identity: authRuntimeIdentityFingerprint(current), Protections: protections}
		}
		dropLegacyRoutingQuotaRecords(records)
		raw, err := json.Marshal(records)
		if err != nil {
			return err
		}
		if err = persist(embeddedusage.ProSetting{Namespace: prorouting.QuotaProtectionNamespace, SchemaVersion: 1, Settings: raw}); err != nil {
			return err
		}
		setQuotaProtections(current, protections)
		current.Generation++
		current.UpdatedAt = time.Now()
		return nil
	})
	if err == nil {
		m.RefreshSchedulerEntry(base.ID)
	}
	return err
}

func (m *Manager) HasStoredQuotaProtections(ctx context.Context) bool {
	if m == nil {
		return false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	item, found, err := embeddedusage.GetProSetting(ctx, prorouting.QuotaProtectionNamespace)
	if err != nil || !found {
		return false
	}
	records, err := decodeQuotaProtectionRecords(item)
	return err == nil && len(records) > 0
}

func (m *Manager) SweepLegacyRoutingQuotaProtections(ctx context.Context) error {
	if m == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	item, _, err := embeddedusage.GetProSetting(ctx, prorouting.QuotaProtectionNamespace)
	if err != nil {
		return err
	}
	records, err := decodeQuotaProtectionRecords(item)
	if err != nil {
		return err
	}
	droppedRecords := dropLegacyRoutingQuotaRecords(records)
	m.mu.Lock()
	changedAuth := false
	for _, auth := range m.auths {
		if auth == nil {
			continue
		}
		cleaned, dropped := prorouting.WithoutLegacyRoutingQuotaProtections(prorouting.ParseQuotaProtections(auth.Metadata))
		if !dropped {
			continue
		}
		setQuotaProtections(auth, cleaned)
		auth.Generation++
		auth.UpdatedAt = time.Now()
		changedAuth = true
	}
	m.mu.Unlock()
	if droppedRecords {
		if err := persistQuotaProtectionRecords(ctx, records); err != nil {
			return err
		}
	}
	if changedAuth {
		m.RefreshSchedulerAll()
	}
	return nil
}

func (m *Manager) ApplyImportedQuotaProtection(ctx context.Context, item embeddedusage.ProSetting) error {
	if m == nil {
		return nil
	}
	records, err := decodeQuotaProtectionRecords(item)
	if err != nil {
		return err
	}
	dropped := dropLegacyRoutingQuotaRecords(records)
	m.mu.Lock()
	changedAuth := dropped
	for _, auth := range m.auths {
		if auth == nil {
			continue
		}
		next := importedQuotaProtections(auth, records[auth.ID])
		if quotaProtectionStateEqual(auth, next) {
			continue
		}
		setQuotaProtections(auth, next)
		auth.Generation++
		auth.UpdatedAt = time.Now()
		changedAuth = true
	}
	m.mu.Unlock()
	if changedAuth {
		m.RefreshSchedulerAll()
	}
	if !dropped {
		return nil
	}
	return persistQuotaProtectionRecords(ctx, records)
}

func importedQuotaProtections(auth *Auth, record quotaProtectionRecord) map[string]prorouting.QuotaProtection {
	if auth == nil || record.Identity != authRuntimeIdentityFingerprint(auth) {
		return nil
	}
	cleaned, _ := prorouting.WithoutLegacyRoutingQuotaProtections(record.Protections)
	if len(cleaned) == 0 {
		return nil
	}
	return cleaned
}

func quotaProtectionStateEqual(auth *Auth, next map[string]prorouting.QuotaProtection) bool {
	current := prorouting.ParseQuotaProtections(auth.Metadata)
	if len(current) == 0 && len(next) == 0 {
		return true
	}
	if len(current) != len(next) {
		return false
	}
	currentRaw, _ := json.Marshal(current)
	nextRaw, _ := json.Marshal(next)
	return string(currentRaw) == string(nextRaw)
}

func proQuotaProtectionBlocked(auth *Auth, model string, now time.Time) (bool, time.Time) {
	var next time.Time
	blocked := false
	for _, hold := range prorouting.QuotaProtections(auth.Metadata) {
		if !prorouting.ProtectionBlocks(hold, canonicalModelKey(model), now) {
			continue
		}
		blocked = true
		if hold.RetryAt == 0 {
			return true, time.Time{}
		}
		retry := time.UnixMilli(hold.RetryAt)
		if !retry.After(now) {
			retry = now.Add(30 * time.Second)
		}
		if retry.After(next) {
			next = retry
		}
	}
	return blocked, next
}

func persistQuotaProtectionRecords(ctx context.Context, records map[string]quotaProtectionRecord) error {
	raw, err := json.Marshal(records)
	if err != nil {
		return err
	}
	return embeddedusage.WithProSettingWriter(ctx, func(ctx context.Context, persist func(embeddedusage.ProSetting) error) error {
		return persist(embeddedusage.ProSetting{Namespace: prorouting.QuotaProtectionNamespace, SchemaVersion: 1, Settings: raw})
	})
}

func dropLegacyRoutingQuotaRecords(records map[string]quotaProtectionRecord) bool {
	droppedAny := false
	for id, record := range records {
		cleaned, dropped := prorouting.WithoutLegacyRoutingQuotaProtections(record.Protections)
		if !dropped {
			continue
		}
		droppedAny = true
		if len(cleaned) == 0 {
			delete(records, id)
			continue
		}
		record.Protections = cleaned
		records[id] = record
	}
	return droppedAny
}

func proroutingQuotaProtections(auth *Auth) map[string]prorouting.QuotaProtection {
	return prorouting.QuotaProtections(auth.Metadata)
}
