package embeddedusage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const ProSettingNamespaceRoutingRequestProtection = "routing.request-protection"

// ProSetting stores one versioned Pro-owned configuration document outside upstream config.yaml.
type ProSetting struct {
	Namespace     string          `json:"namespace"`
	SchemaVersion int             `json:"schemaVersion"`
	Settings      json.RawMessage `json:"settings"`
	UpdatedAtMS   int64           `json:"updatedAtMs"`
}

func normalizeProSetting(item ProSetting) (ProSetting, error) {
	item.Namespace = strings.TrimSpace(item.Namespace)
	if item.Namespace == "" {
		return ProSetting{}, fmt.Errorf("pro setting namespace is required")
	}
	if item.SchemaVersion <= 0 {
		return ProSetting{}, fmt.Errorf("pro setting schema version must be positive")
	}
	if len(item.Settings) == 0 || !json.Valid(item.Settings) {
		return ProSetting{}, fmt.Errorf("pro setting %q contains invalid JSON", item.Namespace)
	}
	item.Settings = append(json.RawMessage(nil), item.Settings...)
	if item.UpdatedAtMS <= 0 {
		item.UpdatedAtMS = time.Now().UnixMilli()
	}
	return item, nil
}

func getProSettingFrom(ctx context.Context, queryer sqlQueryer, namespace string) (ProSetting, bool, error) {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return ProSetting{}, false, nil
	}
	var item ProSetting
	var raw string
	err := queryer.QueryRowContext(ctx, `select namespace, schema_version, settings_json, updated_at_ms from pro_settings where namespace = ?`, namespace).
		Scan(&item.Namespace, &item.SchemaVersion, &raw, &item.UpdatedAtMS)
	if err == sql.ErrNoRows {
		return ProSetting{}, false, nil
	}
	if err != nil {
		return ProSetting{}, false, err
	}
	item.Settings = json.RawMessage(raw)
	normalized, err := normalizeProSetting(item)
	if err != nil {
		return ProSetting{}, false, err
	}
	return normalized, true, nil
}

func listProSettingsFrom(ctx context.Context, queryer sqlQueryer) ([]ProSetting, error) {
	rows, err := queryer.QueryContext(ctx, `select namespace, schema_version, settings_json, updated_at_ms from pro_settings order by namespace`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]ProSetting, 0)
	for rows.Next() {
		var item ProSetting
		var raw string
		if err := rows.Scan(&item.Namespace, &item.SchemaVersion, &raw, &item.UpdatedAtMS); err != nil {
			return nil, err
		}
		item.Settings = json.RawMessage(raw)
		normalized, err := normalizeProSetting(item)
		if err != nil {
			return nil, err
		}
		items = append(items, normalized)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func setProSettingWith(ctx context.Context, execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, item ProSetting) error {
	normalized, err := normalizeProSetting(item)
	if err != nil {
		return err
	}
	_, err = execer.ExecContext(ctx, `insert into pro_settings(namespace, schema_version, settings_json, updated_at_ms) values(?, ?, ?, ?)
		on conflict(namespace) do update set schema_version = excluded.schema_version, settings_json = excluded.settings_json, updated_at_ms = excluded.updated_at_ms`,
		normalized.Namespace, normalized.SchemaVersion, string(normalized.Settings), normalized.UpdatedAtMS)
	return err
}

func (s *Store) GetProSetting(ctx context.Context, namespace string) (ProSetting, bool, error) {
	return getProSettingFrom(ctx, s.db, namespace)
}

func (s *Store) ListProSettings(ctx context.Context) ([]ProSetting, error) {
	return listProSettingsFrom(ctx, s.db)
}

func (s *Store) SetProSetting(ctx context.Context, item ProSetting) error {
	return setProSettingWith(ctx, s.db, item)
}

func (s *Store) ImportProSettings(ctx context.Context, items []ProSetting) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}
	normalized := make([]ProSetting, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		clean, err := normalizeProSetting(item)
		if err != nil {
			return 0, err
		}
		if _, ok := seen[clean.Namespace]; ok {
			return 0, fmt.Errorf("duplicate pro setting namespace %q", clean.Namespace)
		}
		seen[clean.Namespace] = struct{}{}
		normalized = append(normalized, clean)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	for _, item := range normalized {
		if err := setProSettingWith(ctx, tx, item); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(normalized), nil
}
