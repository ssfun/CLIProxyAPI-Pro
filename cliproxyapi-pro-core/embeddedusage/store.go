package embeddedusage

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/embeddedusage/internalusage"
	_ "modernc.org/sqlite"
)

type InsertResult struct {
	Inserted int `json:"inserted"`
	Skipped  int `json:"skipped"`
}

type UsageSummary struct {
	TotalRequests int64
	SuccessCount  int64
	FailureCount  int64
	TotalTokens   int64
}

type UsageDatasetState struct {
	Generation int64 `json:"generation"`
	ResetAtMS  int64 `json:"resetAtMs"`
}

type UsageResetResult struct {
	DeletedEvents int64 `json:"deletedEvents"`
	Generation    int64 `json:"generation"`
	ResetAtMS     int64 `json:"resetAtMs"`
}

type UsageEventQueryOptions struct {
	SnapshotMaxID   int64
	BeforeTimestamp int64
	BeforeID        int64
	FromMS          int64
	ToMS            int64
	Provider        string
	Model           string
	AuthIndex       string
	APIKeyHash      string
	Failed          *bool
	Search          string
	Limit           int
	MatchedTotal    int64
	SkipCount       bool
}

type UsageEventQueryPage struct {
	Events       []internalusage.Event
	MatchedTotal int64
	HasMore      bool
}

type UsageAggregateBucket struct {
	BucketStartMS    int64   `json:"bucketStartMs"`
	BucketStart      string  `json:"bucketStart"`
	Provider         string  `json:"provider,omitempty"`
	Model            string  `json:"model,omitempty"`
	Endpoint         string  `json:"endpoint,omitempty"`
	AuthIndex        string  `json:"authIndex,omitempty"`
	APIKeyHash       string  `json:"apiKeyHash,omitempty"`
	LastSeenAtMS     int64   `json:"lastSeenAtMs"`
	TotalRequests    int64   `json:"totalRequests"`
	SuccessCount     int64   `json:"successCount"`
	FailureCount     int64   `json:"failureCount"`
	TotalTokens      int64   `json:"totalTokens"`
	InputTokens      int64   `json:"inputTokens"`
	OutputTokens     int64   `json:"outputTokens"`
	ReasoningTokens  int64   `json:"reasoningTokens"`
	CacheTokens      int64   `json:"cacheTokens"`
	CacheReadTokens  int64   `json:"cacheReadTokens"`
	CacheWriteTokens int64   `json:"cacheWriteTokens"`
	EstimatedCost    float64 `json:"estimatedCost"`
	AvgLatencyMS     *int64  `json:"avgLatencyMs,omitempty"`
	AvgTTFTMS        *int64  `json:"avgTtftMs,omitempty"`
}

type UsageAggregateOptions struct {
	FromMS                int64
	ToMS                  int64
	Interval              string
	GroupBy               []string
	Limit                 int
	APIKeyHash            string
	TimezoneOffsetMinutes int
}

type DeadLetterSample struct {
	ID          int64  `json:"id"`
	Error       string `json:"error"`
	Payload     string `json:"payload"`
	CreatedAtMS int64  `json:"createdAtMs"`
}

type usageSummarySnapshot struct {
	LatestID int64
	Summary  UsageSummary
	State    UsageDatasetState
}

type cachedUsageSummary struct {
	LatestID int64
	Summary  UsageSummary
}

type QuotaCacheEntry struct {
	ID                  string          `json:"id"`
	Provider            string          `json:"provider"`
	FileName            string          `json:"fileName"`
	AuthIndex           string          `json:"authIndex,omitempty"`
	IdentityFingerprint string          `json:"identityFingerprint,omitempty"`
	Data                json.RawMessage `json:"data"`
	CachedAt            int64           `json:"cachedAt"`
	AccessedAt          int64           `json:"accessedAt"`
	ObservedAt          int64           `json:"observedAt"`
	StoredAt            int64           `json:"storedAt"`
	Version             int             `json:"version"`
	Revision            int64           `json:"revision"`
}

type QuotaCacheStats struct {
	TotalEntries int64 `json:"totalEntries"`
	UpdatedAt    int64 `json:"updatedAt"`
	Generation   int64 `json:"generation"`
}

type RoutingCursorState struct {
	CursorKey   string `json:"cursorKey"`
	LastAuthID  string `json:"lastAuthId"`
	UpdatedAtMS int64  `json:"updatedAtMs"`
}

type RuntimeRequestBucket struct {
	BucketID int64 `json:"bucketId"`
	Success  int64 `json:"success"`
	Failed   int64 `json:"failed"`
}

type AuthRuntimeStats struct {
	AuthIndex           string                 `json:"authIndex"`
	AuthID              string                 `json:"authId"`
	FileName            string                 `json:"fileName,omitempty"`
	IdentityFingerprint string                 `json:"identityFingerprint,omitempty"`
	SelectedCount       int64                  `json:"selectedCount"`
	SuccessCount        int64                  `json:"successCount"`
	FailureCount        int64                  `json:"failureCount"`
	RecentBuckets       []RuntimeRequestBucket `json:"recentBuckets"`
	Generation          int64                  `json:"generation"`
	UpdatedAtMS         int64                  `json:"updatedAtMs"`
}

type MonitoringSettings struct {
	RetentionDays  int                          `json:"retentionDays"`
	WebDAV         MonitoringWebDAVBackupConfig `json:"webdav"`
	ModelPriceSync ModelPriceSyncConfig         `json:"modelPriceSync"`
}

type ModelPriceSyncConfig struct {
	Enabled         bool `json:"enabled"`
	IntervalMinutes int  `json:"intervalMinutes"`
}

type MonitoringWebDAVBackupConfig struct {
	Enabled         bool   `json:"enabled"`
	IntervalMinutes int    `json:"intervalMinutes"`
	RetentionDays   int    `json:"retentionDays"`
	URL             string `json:"url"`
	Username        string `json:"username"`
	Password        string `json:"password"`
}

type ModelPrice struct {
	Prompt     float64 `json:"prompt"`
	Completion float64 `json:"completion"`
	Cache      float64 `json:"cache"`
}

type modelPricesExportRecord struct {
	RecordType string                `json:"record_type"`
	Version    int                   `json:"version"`
	Prices     map[string]ModelPrice `json:"prices"`
	Rules      []ModelPriceRule      `json:"rules,omitempty"`
	ExportedAt int64                 `json:"exported_at_ms"`
}

type monitoringSettingsExportRecord struct {
	RecordType string             `json:"record_type"`
	Version    int                `json:"version"`
	Settings   MonitoringSettings `json:"settings"`
	ExportedAt int64              `json:"exported_at_ms"`
}

type quotaCacheExportRecord struct {
	RecordType string            `json:"record_type"`
	Version    int               `json:"version"`
	Entries    []QuotaCacheEntry `json:"entries"`
	ExportedAt int64             `json:"exported_at_ms"`
}

type routingCursorExportRecord struct {
	RecordType string               `json:"record_type"`
	Version    int                  `json:"version"`
	Items      []RoutingCursorState `json:"items"`
	ExportedAt int64                `json:"exported_at_ms"`
}

type authRuntimeStatsExportRecord struct {
	RecordType string             `json:"record_type"`
	Version    int                `json:"version"`
	Items      []AuthRuntimeStats `json:"items"`
	ExportedAt int64              `json:"exported_at_ms"`
}

const modelPricesExportRecordType = "model_prices"
const quotaCacheExportRecordType = "quota_cache"
const monitoringSettingsExportRecordType = "monitoring_settings"
const routingCursorExportRecordType = "routing_cursor_state"
const authRuntimeStatsExportRecordType = "auth_runtime_stats"

type Store struct {
	db           *sql.DB
	quotaCacheMu sync.Mutex
	usageWriteMu sync.Mutex
	summaryMu    sync.RWMutex
	summaryCache *cachedUsageSummary
	eventMu      sync.Mutex
	eventSignal  chan struct{}
	priceSyncMu  sync.Mutex
}

func OpenStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	store := &Store{db: db, eventSignal: make(chan struct{})}
	db.SetMaxOpenConns(1)
	if err := store.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	db := s.db
	s.db = nil
	return db.Close()
}

func (s *Store) invalidateUsageSummaryCache() {
	s.summaryMu.Lock()
	s.summaryCache = nil
	s.summaryMu.Unlock()
}

func (s *Store) EventSignal() <-chan struct{} {
	s.eventMu.Lock()
	defer s.eventMu.Unlock()
	return s.eventSignal
}

func (s *Store) notifyEventsChanged() {
	s.eventMu.Lock()
	close(s.eventSignal)
	s.eventSignal = make(chan struct{})
	s.eventMu.Unlock()
}

func (s *Store) init() error {
	statements := []string{
		`pragma journal_mode = WAL`,
		`pragma synchronous = FULL`,
		`pragma busy_timeout = 5000`,
		`create table if not exists usage_events (
			id integer primary key autoincrement,
			request_id text,
			event_hash text not null unique,
			timestamp_ms integer not null,
			timestamp text not null,
			provider text,
			executor_type text,
			model text not null,
			alias text,
			endpoint text,
			method text,
			path text,
			auth_type text,
			auth_index text,
			source text,
			source_hash text,
			api_key_hash text,
			input_tokens integer not null default 0,
			output_tokens integer not null default 0,
			reasoning_tokens integer not null default 0,
			cached_tokens integer not null default 0,
			cache_tokens integer not null default 0,
			cache_read_tokens integer not null default 0,
			cache_write_tokens integer not null default 0,
			total_tokens integer not null default 0,
			latency_ms integer,
			ttft_ms integer,
			status_code integer,
			error_code text,
			error_message text,
			upstream_request_id text,
			retry_after text,
			stream integer not null default 0,
			reasoning_effort text,
			service_tier text,
			estimated_cost real,
			price_rule_id integer,
			cost_breakdown_json text,
			failed integer not null default 0,
			raw_json text,
			created_at_ms integer not null
		)`,
		`create index if not exists idx_usage_events_timestamp on usage_events(timestamp_ms)`,
		`create index if not exists idx_usage_events_recent on usage_events(timestamp_ms desc, id desc)`,
		`create index if not exists idx_usage_events_request_id on usage_events(request_id)`,
		`create index if not exists idx_usage_events_model on usage_events(model)`,
		`create index if not exists idx_usage_events_provider_recent on usage_events(provider, timestamp_ms desc, id desc)`,
		`create index if not exists idx_usage_events_model_recent on usage_events(model, timestamp_ms desc, id desc)`,
		`create index if not exists idx_usage_events_failed_recent on usage_events(failed, timestamp_ms desc, id desc)`,
		`create index if not exists idx_usage_events_auth_index on usage_events(auth_index)`,
		`create index if not exists idx_usage_events_api_key_timestamp on usage_events(api_key_hash, timestamp_ms)`,
		`create index if not exists idx_usage_events_api_key_recent on usage_events(api_key_hash, timestamp_ms desc, id desc)`,
		`create table if not exists usage_summary (
			id integer primary key check (id = 1),
			latest_event_id integer not null default 0,
			total_requests integer not null default 0,
			success_count integer not null default 0,
			failure_count integer not null default 0,
			total_tokens integer not null default 0,
			generation integer not null default 1,
			reset_at_ms integer not null default 0,
			updated_at_ms integer not null
		)`,
		`create table if not exists dead_letter_events (
			id integer primary key autoincrement,
			payload text not null,
			error text not null,
			created_at_ms integer not null
		)`,
		`create table if not exists quota_cache (
			id text primary key,
			provider text not null,
			file_name text not null,
			data_json text not null,
			cached_at_ms integer not null,
			accessed_at_ms integer not null,
			version integer not null default 1
		)`,
		`create table if not exists runtime_state_meta (
			state_key text primary key,
			generation integer not null default 1,
			updated_at_ms integer not null default 0
		)`,
		`create table if not exists routing_cursor_state (
			cursor_key text primary key,
			last_auth_id text not null,
			updated_at_ms integer not null
		)`,
		`create table if not exists auth_runtime_stats (
			auth_index text primary key,
			auth_id text not null,
			file_name text not null default '',
			identity_fingerprint text not null default '',
			selected_count integer not null default 0,
			success_count integer not null default 0,
			failure_count integer not null default 0,
			recent_buckets_json text not null default '[]',
			generation integer not null default 1,
			updated_at_ms integer not null
		)`,
		`create index if not exists idx_auth_runtime_stats_auth_id on auth_runtime_stats(auth_id)`,
		`create index if not exists idx_quota_cache_provider on quota_cache(provider)`,
		`create index if not exists idx_quota_cache_accessed_at on quota_cache(accessed_at_ms)`,
		`create table if not exists model_prices (
				model text primary key,
				prompt_price real not null,
				completion_price real not null,
				cache_price real not null,
				updated_at_ms integer not null
			)`,
		`create index if not exists idx_model_prices_updated_at on model_prices(updated_at_ms)`,
		`create table if not exists model_price_rules (
			provider text not null,
			model text not null,
			active_version integer not null,
			source text not null,
			source_provider text not null default '',
			source_model text not null default '',
			locked integer not null default 0,
			fetched_at_ms integer not null default 0,
			upstream_updated text not null default '',
			updated_at_ms integer not null,
			primary key(provider, model)
		)`,
		`create table if not exists model_price_rule_versions (
			id integer primary key autoincrement,
			provider text not null,
			model text not null,
			version integer not null,
			rule_json text not null,
			effective_from_ms integer not null,
			created_at_ms integer not null,
			unique(provider, model, version)
		)`,
		`create table if not exists model_price_sync_state (
			id integer primary key check (id = 1),
			state_json text not null,
			updated_at_ms integer not null
		)`,
		`create table if not exists monitoring_settings (
			id integer primary key check (id = 1),
			settings_json text not null,
			updated_at_ms integer not null
		)`,
	}
	for _, statement := range statements {
		if _, err := s.db.Exec(statement); err != nil {
			return err
		}
	}
	for _, statement := range []string{
		`alter table usage_events add column ttft_ms integer`,
		`alter table usage_events add column status_code integer`,
		`alter table usage_events add column error_code text`,
		`alter table usage_events add column error_message text`,
		`alter table usage_events add column upstream_request_id text`,
		`alter table usage_events add column retry_after text`,
		`alter table usage_events add column stream integer not null default 0`,
		`alter table usage_events add column reasoning_effort text`,
		`alter table usage_events add column service_tier text`,
		`alter table usage_events add column executor_type text`,
		`alter table usage_events add column alias text`,
		`alter table usage_events add column cache_read_tokens integer not null default 0`,
		`alter table usage_events add column cache_write_tokens integer not null default 0`,
		`alter table usage_events add column estimated_cost real`,
		`alter table usage_events add column price_rule_id integer`,
		`alter table usage_events add column cost_breakdown_json text`,
		`alter table usage_summary add column generation integer not null default 1`,
		`alter table usage_summary add column reset_at_ms integer not null default 0`,
		`alter table quota_cache add column auth_index text not null default ''`,
		`alter table quota_cache add column identity_fingerprint text not null default ''`,
		`alter table quota_cache add column observed_at_ms integer not null default 0`,
		`alter table quota_cache add column stored_at_ms integer not null default 0`,
		`alter table quota_cache add column revision integer not null default 0`,
		`alter table auth_runtime_stats add column file_name text not null default ''`,
	} {
		if _, err := s.db.Exec(statement); err != nil && !isDuplicateColumnError(err) {
			return err
		}
	}
	if _, err := s.db.Exec(`insert or ignore into runtime_state_meta(state_key, generation, updated_at_ms) values
		('quota_cache', 1, 0), ('auth_runtime_stats', 1, 0), ('routing_cursor_state', 1, 0)`); err != nil {
		return err
	}
	if err := s.migrateLegacyModelPrices(context.Background()); err != nil {
		return err
	}
	if err := s.migrateProviderBoundModelPriceRules(context.Background()); err != nil {
		return err
	}
	return s.ensureUsageSummary()
}

func (s *Store) InsertEvents(ctx context.Context, events []internalusage.Event) (InsertResult, error) {
	s.usageWriteMu.Lock()
	defer s.usageWriteMu.Unlock()
	return s.insertEvents(ctx, events)
}

func (s *Store) InsertLiveEvents(ctx context.Context, events []internalusage.Event) (InsertResult, error) {
	s.usageWriteMu.Lock()
	defer s.usageWriteMu.Unlock()
	if len(events) == 0 {
		return InsertResult{}, nil
	}
	snapshot, err := s.readUsageSummarySnapshot(ctx)
	if err != nil {
		return InsertResult{}, err
	}
	filtered := make([]internalusage.Event, 0, len(events))
	skipped := 0
	for _, event := range events {
		if snapshot.State.ResetAtMS > 0 && event.TimestampMS <= snapshot.State.ResetAtMS {
			skipped++
			continue
		}
		filtered = append(filtered, event)
	}
	result, err := s.insertEvents(ctx, filtered)
	result.Skipped += skipped
	return result, err
}

func (s *Store) insertEvents(ctx context.Context, events []internalusage.Event) (InsertResult, error) {
	if len(events) == 0 {
		return InsertResult{}, nil
	}
	rules, err := s.activeModelPriceRuleMap(ctx)
	if err != nil {
		return InsertResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return InsertResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `insert or ignore into usage_events (
		request_id, event_hash, timestamp_ms, timestamp, provider, executor_type, model, alias, endpoint, method, path,
		auth_type, auth_index, source, source_hash, api_key_hash,
		input_tokens, output_tokens, reasoning_tokens, cached_tokens, cache_tokens, cache_read_tokens, cache_write_tokens, total_tokens,
		latency_ms, ttft_ms, status_code, error_code, error_message, upstream_request_id, retry_after, stream, reasoning_effort, service_tier,
		estimated_cost, price_rule_id, cost_breakdown_json, failed, raw_json, created_at_ms
	) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return InsertResult{}, err
	}
	defer stmt.Close()

	result := InsertResult{}
	summaryDelta := UsageSummary{}
	latestInsertedID := int64(0)
	for _, event := range events {
		if event.EstimatedCost == nil {
			if rule, ok := findModelPriceRule(rules, event.Provider, event.Model); ok {
				cost, breakdown := evaluateEventCost(event, rule)
				event.EstimatedCost = &cost
				event.PriceRuleID = rule.ID
				if raw, marshalErr := json.Marshal(breakdown); marshalErr == nil {
					event.CostBreakdownJSON = string(raw)
				}
			}
		}
		failed := 0
		if event.Failed {
			failed = 1
		}
		res, err := stmt.ExecContext(ctx,
			nullString(event.RequestID), event.EventHash, event.TimestampMS, event.Timestamp,
			nullString(event.Provider), nullString(event.ExecutorType), event.Model, nullString(event.Alias), nullString(event.Endpoint), nullString(event.Method), nullString(event.Path),
			nullString(event.AuthType), nullString(event.AuthIndex), nullString(event.Source), nullString(event.SourceHash), nullString(event.APIKeyHash),
			event.InputTokens, event.OutputTokens, event.ReasoningTokens, event.CachedTokens, event.CacheTokens, event.CacheReadTokens, event.CacheWriteTokens, event.TotalTokens,
			nullInt64(event.LatencyMS), nullInt64(event.TTFTMS), nullInt(event.StatusCode), nullString(event.ErrorCode), nullString(event.ErrorMessage), nullString(event.UpstreamRequestID), nullString(event.RetryAfter), boolToInt(event.Stream), nullString(event.ReasoningEffort), nullString(event.ServiceTier),
			nullFloat64(event.EstimatedCost), nullPositiveInt64(event.PriceRuleID), nullString(event.CostBreakdownJSON), failed, nullString(event.RawJSON), event.CreatedAtMS,
		)
		if err != nil {
			return InsertResult{}, err
		}
		affected, _ := res.RowsAffected()
		if affected > 0 {
			result.Inserted++
			summaryDelta.TotalRequests++
			if event.Failed {
				summaryDelta.FailureCount++
			} else {
				summaryDelta.SuccessCount++
			}
			summaryDelta.TotalTokens += event.TotalTokens
			if insertedID, err := res.LastInsertId(); err == nil && insertedID > latestInsertedID {
				latestInsertedID = insertedID
			}
		} else {
			result.Skipped++
		}
	}
	if result.Inserted > 0 {
		if latestInsertedID <= 0 {
			if err := tx.QueryRowContext(ctx, `select coalesce(max(id), 0) from usage_events`).Scan(&latestInsertedID); err != nil {
				return InsertResult{}, err
			}
		}
		if err := s.applyUsageSummaryDelta(ctx, tx, latestInsertedID, summaryDelta); err != nil {
			return InsertResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return InsertResult{}, err
	}
	if result.Inserted > 0 {
		s.invalidateUsageSummaryCache()
		s.notifyEventsChanged()
	}
	return result, nil
}

func (s *Store) ensureUsageSummary() error {
	snapshot, err := s.readUsageSummarySnapshot(context.Background())
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	latestID, _, cursorErr := s.LatestCursor(context.Background())
	if cursorErr != nil {
		return cursorErr
	}
	if err == sql.ErrNoRows || snapshot.LatestID != latestID {
		return s.rebuildUsageSummary(context.Background())
	}
	return nil
}

func (s *Store) readUsageSummarySnapshot(ctx context.Context) (usageSummarySnapshot, error) {
	var snapshot usageSummarySnapshot
	err := s.db.QueryRowContext(ctx, `select
		latest_event_id,
		total_requests,
		success_count,
		failure_count,
		total_tokens,
		generation,
		reset_at_ms
		from usage_summary
		where id = 1`).Scan(
		&snapshot.LatestID,
		&snapshot.Summary.TotalRequests,
		&snapshot.Summary.SuccessCount,
		&snapshot.Summary.FailureCount,
		&snapshot.Summary.TotalTokens,
		&snapshot.State.Generation,
		&snapshot.State.ResetAtMS,
	)
	return snapshot, err
}

func (s *Store) UsageDatasetState(ctx context.Context) (UsageDatasetState, error) {
	snapshot, err := s.readUsageSummarySnapshot(ctx)
	if err == sql.ErrNoRows {
		if rebuildErr := s.rebuildUsageSummary(ctx); rebuildErr != nil {
			return UsageDatasetState{}, rebuildErr
		}
		snapshot, err = s.readUsageSummarySnapshot(ctx)
	}
	if err != nil {
		return UsageDatasetState{}, err
	}
	if snapshot.State.Generation <= 0 {
		snapshot.State.Generation = 1
	}
	return snapshot.State, nil
}

func (s *Store) applyUsageSummaryDelta(ctx context.Context, tx *sql.Tx, latestID int64, delta UsageSummary) error {
	_, err := tx.ExecContext(ctx, `insert into usage_summary(
		id,
		latest_event_id,
		total_requests,
		success_count,
		failure_count,
		total_tokens,
		generation,
		reset_at_ms,
		updated_at_ms
	) values(1, ?, ?, ?, ?, ?, 1, 0, ?)
	on conflict(id) do update set
		latest_event_id = max(usage_summary.latest_event_id, excluded.latest_event_id),
		total_requests = usage_summary.total_requests + excluded.total_requests,
		success_count = usage_summary.success_count + excluded.success_count,
		failure_count = usage_summary.failure_count + excluded.failure_count,
		total_tokens = usage_summary.total_tokens + excluded.total_tokens,
		updated_at_ms = excluded.updated_at_ms`,
		latestID,
		delta.TotalRequests,
		delta.SuccessCount,
		delta.FailureCount,
		delta.TotalTokens,
		time.Now().UnixMilli(),
	)
	return err
}

func (s *Store) rebuildUsageSummary(ctx context.Context) error {
	var latestID sql.NullInt64
	var totalRequests, successCount, failureCount, totalTokens sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `select
		coalesce(max(id), 0),
		count(*),
		coalesce(sum(case when failed = 0 then 1 else 0 end), 0),
		coalesce(sum(case when failed != 0 then 1 else 0 end), 0),
		coalesce(sum(total_tokens), 0)
		from usage_events`).Scan(&latestID, &totalRequests, &successCount, &failureCount, &totalTokens); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `insert into usage_summary(
		id,
		latest_event_id,
		total_requests,
		success_count,
		failure_count,
		total_tokens,
		generation,
		reset_at_ms,
		updated_at_ms
	) values(1, ?, ?, ?, ?, ?, 1, 0, ?)
	on conflict(id) do update set
		latest_event_id = excluded.latest_event_id,
		total_requests = excluded.total_requests,
		success_count = excluded.success_count,
		failure_count = excluded.failure_count,
		total_tokens = excluded.total_tokens,
		updated_at_ms = excluded.updated_at_ms`,
		latestID.Int64,
		totalRequests.Int64,
		successCount.Int64,
		failureCount.Int64,
		totalTokens.Int64,
		time.Now().UnixMilli(),
	)
	if err == nil {
		s.invalidateUsageSummaryCache()
	}
	return err
}

func (s *Store) AddDeadLetter(ctx context.Context, payload string, parseErr error) error {
	_, err := s.db.ExecContext(ctx,
		`insert into dead_letter_events(payload, error, created_at_ms) values(?, ?, ?)`,
		payload, parseErr.Error(), time.Now().UnixMilli(),
	)
	return err
}

func (s *Store) scanEvents(rows *sql.Rows) ([]internalusage.Event, error) {
	events := make([]internalusage.Event, 0)
	for rows.Next() {
		var event internalusage.Event
		var requestID, provider, executorType, alias, endpoint, method, path, authType, authIndex, source, sourceHash, apiKeyHash, rawJSON sql.NullString
		var latency, ttft sql.NullInt64
		var statusCode sql.NullInt64
		var errorCode, errorMessage, upstreamRequestID, retryAfter, reasoningEffort, serviceTier, costBreakdown sql.NullString
		var estimatedCost sql.NullFloat64
		var priceRuleID sql.NullInt64
		var stream, failed int
		if err := rows.Scan(
			&event.ID, &requestID, &event.EventHash, &event.TimestampMS, &event.Timestamp, &provider, &executorType, &event.Model,
			&alias, &endpoint, &method, &path, &authType, &authIndex, &source, &sourceHash, &apiKeyHash,
			&event.InputTokens, &event.OutputTokens, &event.ReasoningTokens, &event.CachedTokens, &event.CacheTokens, &event.CacheReadTokens, &event.CacheWriteTokens, &event.TotalTokens,
			&latency, &ttft, &statusCode, &errorCode, &errorMessage, &upstreamRequestID, &retryAfter, &stream, &reasoningEffort, &serviceTier,
			&estimatedCost, &priceRuleID, &costBreakdown, &failed, &rawJSON, &event.CreatedAtMS,
		); err != nil {
			return nil, err
		}
		event.RequestID = requestID.String
		event.Provider = provider.String
		event.ExecutorType = executorType.String
		event.Alias = alias.String
		event.Endpoint = endpoint.String
		event.Method = method.String
		event.Path = path.String
		event.AuthType = authType.String
		event.AuthIndex = authIndex.String
		event.Source = source.String
		event.SourceHash = sourceHash.String
		event.APIKeyHash = apiKeyHash.String
		event.RawJSON = rawJSON.String
		event.Failed = failed != 0
		if latency.Valid {
			value := latency.Int64
			event.LatencyMS = &value
		}
		if ttft.Valid {
			value := ttft.Int64
			event.TTFTMS = &value
		}
		if statusCode.Valid {
			value := int(statusCode.Int64)
			event.StatusCode = &value
		}
		event.ErrorCode = errorCode.String
		event.ErrorMessage = errorMessage.String
		event.UpstreamRequestID = upstreamRequestID.String
		event.RetryAfter = retryAfter.String
		event.Stream = stream != 0
		event.ReasoningEffort = reasoningEffort.String
		event.ServiceTier = serviceTier.String
		if estimatedCost.Valid {
			value := estimatedCost.Float64
			event.EstimatedCost = &value
		}
		event.PriceRuleID = priceRuleID.Int64
		event.CostBreakdownJSON = costBreakdown.String
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) RecentEvents(ctx context.Context, limit int) ([]internalusage.Event, error) {
	if limit <= 0 {
		limit = 50000
	}
	rows, err := s.db.QueryContext(ctx, `select
		id, request_id, event_hash, timestamp_ms, timestamp, provider, executor_type, model, alias, endpoint, method, path,
		auth_type, auth_index, source, source_hash, api_key_hash,
		input_tokens, output_tokens, reasoning_tokens, cached_tokens, cache_tokens, cache_read_tokens, cache_write_tokens, total_tokens,
		latency_ms, ttft_ms, status_code, error_code, error_message, upstream_request_id, retry_after, stream, reasoning_effort, service_tier,
		estimated_cost, price_rule_id, cost_breakdown_json, failed, raw_json, created_at_ms
		from usage_events indexed by idx_usage_events_recent
		order by timestamp_ms desc, id desc
		limit ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.scanEvents(rows)
}

func (s *Store) EventsAfter(ctx context.Context, afterID int64, limit int) ([]internalusage.Event, error) {
	if limit <= 0 || limit > usageEventsSentinelLimit {
		limit = usageEventsSentinelLimit
	}
	rows, err := s.db.QueryContext(ctx, `select
		id, request_id, event_hash, timestamp_ms, timestamp, provider, executor_type, model, alias, endpoint, method, path,
		auth_type, auth_index, source, source_hash, api_key_hash,
		input_tokens, output_tokens, reasoning_tokens, cached_tokens, cache_tokens, cache_read_tokens, cache_write_tokens, total_tokens,
		latency_ms, ttft_ms, status_code, error_code, error_message, upstream_request_id, retry_after, stream, reasoning_effort, service_tier,
		estimated_cost, price_rule_id, cost_breakdown_json, failed, raw_json, created_at_ms
		from usage_events
		where id > ?
		order by id asc
		limit ?`, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.scanEvents(rows)
}

func appendUsageEventQueryFilters(options UsageEventQueryOptions, includeCursor bool) ([]string, []any) {
	wheres := make([]string, 0, 10)
	args := make([]any, 0, 12)
	if options.SnapshotMaxID > 0 {
		wheres = append(wheres, `id <= ?`)
		args = append(args, options.SnapshotMaxID)
	}
	if includeCursor && options.BeforeTimestamp > 0 && options.BeforeID > 0 {
		wheres = append(wheres, `(timestamp_ms < ? or (timestamp_ms = ? and id < ?))`)
		args = append(args, options.BeforeTimestamp, options.BeforeTimestamp, options.BeforeID)
	}
	if options.FromMS > 0 {
		wheres = append(wheres, `timestamp_ms >= ?`)
		args = append(args, options.FromMS)
	}
	if options.ToMS > 0 {
		wheres = append(wheres, `timestamp_ms <= ?`)
		args = append(args, options.ToMS)
	}
	if value := strings.TrimSpace(options.Provider); value != "" {
		wheres = append(wheres, `provider = ?`)
		args = append(args, value)
	}
	if value := strings.TrimSpace(options.Model); value != "" {
		wheres = append(wheres, `model = ?`)
		args = append(args, value)
	}
	if values := splitUsageEventFilterValues(options.AuthIndex, 100); len(values) > 0 {
		wheres = append(wheres, `auth_index in (`+strings.TrimRight(strings.Repeat("?,", len(values)), ",")+`)`)
		for _, value := range values {
			args = append(args, value)
		}
	}
	if value := strings.TrimSpace(options.APIKeyHash); value != "" {
		wheres = append(wheres, `api_key_hash = ?`)
		args = append(args, value)
	}
	if options.Failed != nil {
		failed := 0
		if *options.Failed {
			failed = 1
		}
		wheres = append(wheres, `failed = ?`)
		args = append(args, failed)
	}
	if value := strings.ToLower(strings.TrimSpace(options.Search)); value != "" {
		searchRunes := []rune(value)
		if len(searchRunes) > 200 {
			value = string(searchRunes[:200])
		}
		wheres = append(wheres, `instr(lower(
			coalesce(request_id, '') || char(10) || coalesce(provider, '') || char(10) ||
			coalesce(executor_type, '') || char(10) || coalesce(model, '') || char(10) ||
			coalesce(alias, '') || char(10) || coalesce(endpoint, '') || char(10) ||
			coalesce(method, '') || char(10) || coalesce(path, '') || char(10) ||
			coalesce(auth_type, '') || char(10) || coalesce(auth_index, '') || char(10) ||
			coalesce(source, '') || char(10) || coalesce(source_hash, '') || char(10) ||
			coalesce(api_key_hash, '') || char(10) || coalesce(error_code, '') || char(10) ||
			coalesce(error_message, '') || char(10) || coalesce(upstream_request_id, '')
		), ?) > 0`)
		args = append(args, value)
	}
	return wheres, args
}

func splitUsageEventFilterValues(value string, limit int) []string {
	out := make([]string, 0)
	seen := map[string]struct{}{}
	for _, item := range strings.Split(value, ",") {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func usageEventQueryWhere(wheres []string) string {
	if len(wheres) == 0 {
		return ""
	}
	return ` where ` + strings.Join(wheres, ` and `)
}

func (s *Store) QueryEvents(ctx context.Context, options UsageEventQueryOptions) (UsageEventQueryPage, error) {
	limit := options.Limit
	if limit <= 0 || limit > usageEventsPageLimit {
		limit = usageEventsPageLimit
	}

	matchedTotal := options.MatchedTotal
	if !options.SkipCount {
		countWheres, countArgs := appendUsageEventQueryFilters(options, false)
		if err := s.db.QueryRowContext(ctx, `select count(*) from usage_events`+usageEventQueryWhere(countWheres), countArgs...).Scan(&matchedTotal); err != nil {
			return UsageEventQueryPage{}, err
		}
	}

	queryWheres, queryArgs := appendUsageEventQueryFilters(options, true)
	query := `select
		id, request_id, event_hash, timestamp_ms, timestamp, provider, executor_type, model, alias, endpoint, method, path,
		auth_type, auth_index, source, source_hash, api_key_hash,
		input_tokens, output_tokens, reasoning_tokens, cached_tokens, cache_tokens, cache_read_tokens, cache_write_tokens, total_tokens,
		latency_ms, ttft_ms, status_code, error_code, error_message, upstream_request_id, retry_after, stream, reasoning_effort, service_tier,
		estimated_cost, price_rule_id, cost_breakdown_json, failed, raw_json, created_at_ms
		from usage_events` + usageEventQueryWhere(queryWheres) + `
		order by timestamp_ms desc, id desc
		limit ?`
	queryArgs = append(queryArgs, limit+1)
	rows, err := s.db.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return UsageEventQueryPage{}, err
	}
	defer rows.Close()
	events, err := s.scanEvents(rows)
	if err != nil {
		return UsageEventQueryPage{}, err
	}
	hasMore := len(events) > limit
	if hasMore {
		events = events[:limit]
	}
	return UsageEventQueryPage{Events: events, MatchedTotal: matchedTotal, HasMore: hasMore}, nil
}

func (s *Store) LatestCursor(ctx context.Context) (int64, int64, error) {
	var id sql.NullInt64
	var timestamp sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `select id, timestamp_ms from usage_events order by id desc limit 1`).Scan(&id, &timestamp); err != nil {
		if err == sql.ErrNoRows {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	latestID := int64(0)
	latestTimestamp := int64(0)
	if id.Valid {
		latestID = id.Int64
	}
	if timestamp.Valid {
		latestTimestamp = timestamp.Int64
	}
	return latestID, latestTimestamp, nil
}

func (s *Store) UsageSummary(ctx context.Context, maxID int64) (UsageSummary, error) {
	if maxID <= 0 {
		return UsageSummary{}, nil
	}
	latestID, _, err := s.LatestCursor(ctx)
	if err != nil {
		return UsageSummary{}, err
	}
	if maxID == latestID {
		s.summaryMu.RLock()
		if s.summaryCache != nil && s.summaryCache.LatestID == maxID {
			summary := s.summaryCache.Summary
			s.summaryMu.RUnlock()
			return summary, nil
		}
		s.summaryMu.RUnlock()

		snapshot, err := s.readUsageSummarySnapshot(ctx)
		if err != nil {
			if err != sql.ErrNoRows {
				return UsageSummary{}, err
			}
		} else if snapshot.LatestID == maxID {
			s.summaryMu.Lock()
			s.summaryCache = &cachedUsageSummary{LatestID: maxID, Summary: snapshot.Summary}
			s.summaryMu.Unlock()
			return snapshot.Summary, nil
		}
		if err := s.rebuildUsageSummary(ctx); err != nil {
			return UsageSummary{}, err
		}
		snapshot, err = s.readUsageSummarySnapshot(ctx)
		if err != nil {
			return UsageSummary{}, err
		}
		if snapshot.LatestID == maxID {
			s.summaryMu.Lock()
			s.summaryCache = &cachedUsageSummary{LatestID: maxID, Summary: snapshot.Summary}
			s.summaryMu.Unlock()
			return snapshot.Summary, nil
		}
	}

	var summary UsageSummary
	var totalRequests, successCount, failureCount, totalTokens sql.NullInt64
	query := `select
		count(*),
		coalesce(sum(case when failed = 0 then 1 else 0 end), 0),
		coalesce(sum(case when failed != 0 then 1 else 0 end), 0),
		coalesce(sum(total_tokens), 0)
		from usage_events`
	args := []any{}
	query += ` where id <= ?`
	args = append(args, maxID)
	err = s.db.QueryRowContext(ctx, query, args...).Scan(&totalRequests, &successCount, &failureCount, &totalTokens)
	if err != nil {
		return summary, err
	}
	if totalRequests.Valid {
		summary.TotalRequests = totalRequests.Int64
	}
	if successCount.Valid {
		summary.SuccessCount = successCount.Int64
	}
	if failureCount.Valid {
		summary.FailureCount = failureCount.Int64
	}
	if totalTokens.Valid {
		summary.TotalTokens = totalTokens.Int64
	}
	if maxID == latestID {
		s.summaryMu.Lock()
		s.summaryCache = &cachedUsageSummary{LatestID: maxID, Summary: summary}
		s.summaryMu.Unlock()
	}
	return summary, nil
}

func (s *Store) Counts(ctx context.Context) (events int64, deadLetters int64, err error) {
	if err = s.db.QueryRowContext(ctx, `select count(*) from usage_events`).Scan(&events); err != nil {
		return 0, 0, err
	}
	if err = s.db.QueryRowContext(ctx, `select count(*) from dead_letter_events`).Scan(&deadLetters); err != nil {
		return 0, 0, err
	}
	return events, deadLetters, nil
}

func (s *Store) RecentDeadLetters(ctx context.Context, limit int) ([]DeadLetterSample, error) {
	if limit <= 0 || limit > 20 {
		limit = 5
	}
	rows, err := s.db.QueryContext(ctx, `select id, error, payload, created_at_ms from dead_letter_events order by id desc limit ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	samples := make([]DeadLetterSample, 0)
	for rows.Next() {
		var sample DeadLetterSample
		if err := rows.Scan(&sample.ID, &sample.Error, &sample.Payload, &sample.CreatedAtMS); err != nil {
			return nil, err
		}
		sample.Payload = redactDeadLetterPayload(sample.Payload)
		if len(sample.Payload) > 500 {
			sample.Payload = sample.Payload[:500]
		}
		samples = append(samples, sample)
	}
	return samples, rows.Err()
}

func redactDeadLetterPayload(payload string) string {
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return ""
	}
	var value any
	if err := json.Unmarshal([]byte(payload), &value); err != nil {
		return payload
	}
	redacted, err := json.Marshal(redactDeadLetterValue(value))
	if err != nil {
		return payload
	}
	return string(redacted)
}

func redactDeadLetterValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			if isDeadLetterSecretKey(key) {
				out[key] = "[redacted]"
			} else {
				out[key] = redactDeadLetterValue(child)
			}
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, child := range typed {
			out = append(out, redactDeadLetterValue(child))
		}
		return out
	default:
		return value
	}
}

func isDeadLetterSecretKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
	return normalized == "api_key" ||
		normalized == "apikey" ||
		normalized == "authorization" ||
		normalized == "cookie" ||
		normalized == "set_cookie" ||
		normalized == "access_token" ||
		normalized == "refresh_token" ||
		normalized == "token" ||
		strings.Contains(normalized, "secret")
}

func (s *Store) UsageAggregates(ctx context.Context, options UsageAggregateOptions) ([]UsageAggregateBucket, error) {
	intervalMs := aggregateIntervalMS(options.Interval)
	if intervalMs < 0 {
		intervalMs = int64(time.Hour / time.Millisecond)
	}
	if options.Limit <= 0 || options.Limit > 10000 {
		options.Limit = 1000
	}
	selects := []string{}
	groups := []string{`bucket_start_ms`}
	args := []any{}
	if intervalMs == 0 {
		selects = append(selects, `? as bucket_start_ms`)
		args = append(args, options.FromMS)
	} else {
		offsetMs := int64(options.TimezoneOffsetMinutes) * int64(time.Minute/time.Millisecond)
		selects = append(selects, `((timestamp_ms + ?) / ?) * ? - ? as bucket_start_ms`)
		args = append(args, offsetMs, intervalMs, intervalMs, offsetMs)
	}
	for _, group := range normalizeAggregateGroups(options.GroupBy) {
		selects = append(selects, group)
		groups = append(groups, group)
	}
	selects = append(selects,
		`count(*) as total_requests`,
		`coalesce(sum(case when failed = 0 then 1 else 0 end), 0) as success_count`,
		`coalesce(sum(case when failed != 0 then 1 else 0 end), 0) as failure_count`,
		`coalesce(sum(total_tokens), 0) as total_tokens`,
		`coalesce(sum(input_tokens), 0) as input_tokens`,
		`coalesce(sum(output_tokens), 0) as output_tokens`,
		`coalesce(sum(reasoning_tokens), 0) as reasoning_tokens`,
		`coalesce(sum(max(cached_tokens, cache_tokens)), 0) as cache_tokens`,
		`coalesce(sum(cache_read_tokens), 0) as cache_read_tokens`,
		`coalesce(sum(cache_write_tokens), 0) as cache_write_tokens`,
		`coalesce(sum(estimated_cost), 0) as estimated_cost`,
		`cast(avg(latency_ms) as integer) as avg_latency_ms`,
		`cast(avg(ttft_ms) as integer) as avg_ttft_ms`,
		`coalesce(max(timestamp_ms), 0) as last_seen_at_ms`,
	)
	query := `select ` + strings.Join(selects, ", ") + ` from usage_events`
	wheres := []string{}
	if options.FromMS > 0 {
		wheres = append(wheres, `timestamp_ms >= ?`)
		args = append(args, options.FromMS)
	}
	if options.ToMS > 0 {
		wheres = append(wheres, `timestamp_ms <= ?`)
		args = append(args, options.ToMS)
	}
	if strings.TrimSpace(options.APIKeyHash) != "" {
		wheres = append(wheres, `api_key_hash = ?`)
		args = append(args, strings.TrimSpace(options.APIKeyHash))
	}
	if len(wheres) > 0 {
		query += ` where ` + strings.Join(wheres, ` and `)
	}
	query += ` group by ` + strings.Join(groups, ", ") + ` order by bucket_start_ms asc limit ?`
	args = append(args, options.Limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	buckets := make([]UsageAggregateBucket, 0)
	groupColumns := normalizeAggregateGroups(options.GroupBy)
	for rows.Next() {
		var bucket UsageAggregateBucket
		dest := []any{&bucket.BucketStartMS}
		groupValues := make([]sql.NullString, len(groupColumns))
		for index, group := range groupColumns {
			switch group {
			case "provider", "model", "endpoint", "auth_index", "api_key_hash":
				dest = append(dest, &groupValues[index])
			}
		}
		var avgLatency, avgTTFT sql.NullInt64
		dest = append(dest, &bucket.TotalRequests, &bucket.SuccessCount, &bucket.FailureCount, &bucket.TotalTokens, &bucket.InputTokens, &bucket.OutputTokens, &bucket.ReasoningTokens, &bucket.CacheTokens, &bucket.CacheReadTokens, &bucket.CacheWriteTokens, &bucket.EstimatedCost, &avgLatency, &avgTTFT, &bucket.LastSeenAtMS)
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		for index, group := range groupColumns {
			value := groupValues[index].String
			switch group {
			case "provider":
				bucket.Provider = value
			case "model":
				bucket.Model = value
			case "endpoint":
				bucket.Endpoint = value
			case "auth_index":
				bucket.AuthIndex = value
			case "api_key_hash":
				bucket.APIKeyHash = value
			}
		}
		bucket.BucketStart = time.UnixMilli(bucket.BucketStartMS).UTC().Format(time.RFC3339Nano)
		if avgLatency.Valid {
			value := avgLatency.Int64
			bucket.AvgLatencyMS = &value
		}
		if avgTTFT.Valid {
			value := avgTTFT.Int64
			bucket.AvgTTFTMS = &value
		}
		buckets = append(buckets, bucket)
	}
	return buckets, rows.Err()
}

func aggregateIntervalMS(interval string) int64 {
	switch strings.ToLower(strings.TrimSpace(interval)) {
	case "all", "total":
		return 0
	case "minute", "1m":
		return int64(time.Minute / time.Millisecond)
	case "day", "1d":
		return int64(24 * time.Hour / time.Millisecond)
	default:
		return -1
	}
}

func normalizeAggregateGroups(groups []string) []string {
	allowed := map[string]string{
		"provider":     "provider",
		"model":        "model",
		"endpoint":     "endpoint",
		"auth_index":   "auth_index",
		"authIndex":    "auth_index",
		"api_key_hash": "api_key_hash",
		"apiKeyHash":   "api_key_hash",
	}
	out := make([]string, 0, len(groups))
	seen := map[string]struct{}{}
	for _, group := range groups {
		normalized, ok := allowed[strings.TrimSpace(group)]
		if !ok {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
}

func isDuplicateColumnError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "duplicate column")
}

func (s *Store) ExportJSONL(ctx context.Context) ([]byte, error) {
	events, err := s.RecentEvents(ctx, 0)
	if err != nil {
		return nil, err
	}
	prices, err := s.getModelPrices(ctx, false)
	if err != nil {
		return nil, err
	}
	priceRules, err := s.ActiveModelPriceRules(ctx)
	if err != nil {
		return nil, err
	}
	quotaEntries, err := s.GetQuotaCache(ctx, "", "")
	if err != nil {
		return nil, err
	}
	routingCursors, err := s.ListRoutingCursorStates(ctx)
	if err != nil {
		return nil, err
	}
	authRuntimeStats, err := s.ListAuthRuntimeStats(ctx)
	if err != nil {
		return nil, err
	}
	settings, err := s.GetMonitoringSettings(ctx)
	if err != nil {
		return nil, err
	}

	output := make([]byte, 0)
	line, err := json.Marshal(monitoringSettingsExportRecord{
		RecordType: monitoringSettingsExportRecordType,
		Version:    1,
		Settings:   settings,
		ExportedAt: time.Now().UnixMilli(),
	})
	if err != nil {
		return nil, err
	}
	output = append(output, line...)
	output = append(output, '\n')
	if len(prices) > 0 || len(priceRules) > 0 {
		line, err := json.Marshal(modelPricesExportRecord{
			RecordType: modelPricesExportRecordType,
			Version:    2,
			Prices:     prices,
			Rules:      priceRules,
			ExportedAt: time.Now().UnixMilli(),
		})
		if err != nil {
			return nil, err
		}
		output = append(output, line...)
		output = append(output, '\n')
	}
	if len(quotaEntries) > 0 {
		line, err := json.Marshal(quotaCacheExportRecord{
			RecordType: quotaCacheExportRecordType,
			Version:    2,
			Entries:    quotaEntries,
			ExportedAt: time.Now().UnixMilli(),
		})
		if err != nil {
			return nil, err
		}
		output = append(output, line...)
		output = append(output, '\n')
	}
	if len(routingCursors) > 0 {
		line, err := json.Marshal(routingCursorExportRecord{
			RecordType: routingCursorExportRecordType,
			Version:    1,
			Items:      routingCursors,
			ExportedAt: time.Now().UnixMilli(),
		})
		if err != nil {
			return nil, err
		}
		output = append(output, line...)
		output = append(output, '\n')
	}
	if len(authRuntimeStats) > 0 {
		line, err := json.Marshal(authRuntimeStatsExportRecord{
			RecordType: authRuntimeStatsExportRecordType,
			Version:    1,
			Items:      authRuntimeStats,
			ExportedAt: time.Now().UnixMilli(),
		})
		if err != nil {
			return nil, err
		}
		output = append(output, line...)
		output = append(output, '\n')
	}
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		event.RawJSON = ""
		line, err := json.Marshal(event)
		if err != nil {
			return nil, err
		}
		output = append(output, line...)
		output = append(output, '\n')
	}
	return output, nil
}

func defaultMonitoringSettings() MonitoringSettings {
	return MonitoringSettings{
		RetentionDays: 0,
		WebDAV: MonitoringWebDAVBackupConfig{
			Enabled:         false,
			IntervalMinutes: 1440,
		},
		ModelPriceSync: ModelPriceSyncConfig{Enabled: false, IntervalMinutes: 1440},
	}
}

func normalizeMonitoringSettings(settings MonitoringSettings) MonitoringSettings {
	if settings.RetentionDays < 0 {
		settings.RetentionDays = 0
	}
	if settings.WebDAV.IntervalMinutes <= 0 {
		settings.WebDAV.IntervalMinutes = 1440
	}
	if settings.WebDAV.RetentionDays < 0 {
		settings.WebDAV.RetentionDays = 0
	}
	settings.WebDAV.URL = strings.TrimSpace(settings.WebDAV.URL)
	settings.WebDAV.Username = strings.TrimSpace(settings.WebDAV.Username)
	if settings.ModelPriceSync.IntervalMinutes < 60 {
		settings.ModelPriceSync.IntervalMinutes = 1440
	}
	return settings
}

func (s *Store) GetMonitoringSettings(ctx context.Context) (MonitoringSettings, error) {
	settings := defaultMonitoringSettings()
	var raw string
	if err := s.db.QueryRowContext(ctx, `select settings_json from monitoring_settings where id = 1`).Scan(&raw); err != nil {
		if err == sql.ErrNoRows {
			return settings, nil
		}
		return settings, err
	}
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		return defaultMonitoringSettings(), err
	}
	return normalizeMonitoringSettings(settings), nil
}

func (s *Store) SetMonitoringSettings(ctx context.Context, settings MonitoringSettings) error {
	settings = normalizeMonitoringSettings(settings)
	raw, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `insert into monitoring_settings(id, settings_json, updated_at_ms) values(1, ?, ?)
		on conflict(id) do update set settings_json = excluded.settings_json, updated_at_ms = excluded.updated_at_ms`, string(raw), time.Now().UnixMilli())
	return err
}

func (s *Store) DeleteEventsBefore(ctx context.Context, beforeMs int64) (int64, error) {
	if beforeMs <= 0 {
		return 0, nil
	}
	s.usageWriteMu.Lock()
	defer s.usageWriteMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	var deleted UsageSummary
	if err := tx.QueryRowContext(ctx, `select
		count(*),
		coalesce(sum(case when failed = 0 then 1 else 0 end), 0),
		coalesce(sum(case when failed != 0 then 1 else 0 end), 0),
		coalesce(sum(total_tokens), 0)
		from usage_events
		where timestamp_ms < ?`, beforeMs).Scan(&deleted.TotalRequests, &deleted.SuccessCount, &deleted.FailureCount, &deleted.TotalTokens); err != nil {
		return 0, err
	}
	if deleted.TotalRequests == 0 {
		return 0, nil
	}

	result, err := tx.ExecContext(ctx, `delete from usage_events where timestamp_ms < ?`, beforeMs)
	if err != nil {
		return 0, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if affected > 0 {
		var latestID int64
		if err := tx.QueryRowContext(ctx, `select coalesce(max(id), 0) from usage_events`).Scan(&latestID); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `update usage_summary set
			latest_event_id = ?,
			total_requests = max(total_requests - ?, 0),
			success_count = max(success_count - ?, 0),
			failure_count = max(failure_count - ?, 0),
			total_tokens = max(total_tokens - ?, 0),
			generation = generation + 1,
			updated_at_ms = ?
			where id = 1`,
			latestID,
			deleted.TotalRequests,
			deleted.SuccessCount,
			deleted.FailureCount,
			deleted.TotalTokens,
			time.Now().UnixMilli(),
		); err != nil {
			return 0, err
		}
		if err := tx.Commit(); err != nil {
			return 0, err
		}
		s.invalidateUsageSummaryCache()
		s.notifyEventsChanged()
	}
	return affected, nil
}

func (s *Store) ResetUsageStatistics(ctx context.Context) (UsageResetResult, error) {
	s.usageWriteMu.Lock()
	defer s.usageWriteMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return UsageResetResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var deletedEvents int64
	if err := tx.QueryRowContext(ctx, `select count(*) from usage_events`).Scan(&deletedEvents); err != nil {
		return UsageResetResult{}, err
	}

	var generation, resetAtMS int64
	if err := tx.QueryRowContext(ctx, `select generation, reset_at_ms from usage_summary where id = 1`).Scan(&generation, &resetAtMS); err != nil {
		return UsageResetResult{}, err
	}
	if generation <= 0 {
		generation = 1
	}

	if deletedEvents > 0 {
		if _, err := tx.ExecContext(ctx, `delete from usage_events`); err != nil {
			return UsageResetResult{}, err
		}
		generation++
		resetAtMS = time.Now().UnixMilli()
		if _, err := tx.ExecContext(ctx, `update usage_summary set
			latest_event_id = 0,
			total_requests = 0,
			success_count = 0,
			failure_count = 0,
			total_tokens = 0,
			generation = ?,
			reset_at_ms = ?,
			updated_at_ms = ?
			where id = 1`, generation, resetAtMS, resetAtMS); err != nil {
			return UsageResetResult{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return UsageResetResult{}, err
	}
	if deletedEvents > 0 {
		s.invalidateUsageSummaryCache()
		s.notifyEventsChanged()
	}
	return UsageResetResult{DeletedEvents: deletedEvents, Generation: generation, ResetAtMS: resetAtMS}, nil
}

func (s *Store) ApplyRetention(ctx context.Context, now time.Time) (int64, error) {
	settings, err := s.GetMonitoringSettings(ctx)
	if err != nil {
		return 0, err
	}
	if settings.RetentionDays <= 0 {
		return 0, nil
	}
	return s.DeleteEventsBefore(ctx, now.AddDate(0, 0, -settings.RetentionDays).UnixMilli())
}

func (s *Store) GetQuotaCache(ctx context.Context, provider string, fileName string) ([]QuotaCacheEntry, error) {
	query := `select id, provider, file_name, auth_index, identity_fingerprint, data_json,
		cached_at_ms, accessed_at_ms, observed_at_ms, stored_at_ms, version, revision from quota_cache`
	args := []any{}
	switch {
	case provider != "" && fileName != "":
		query += ` where provider = ? and file_name = ?`
		args = append(args, provider, fileName)
	case provider != "":
		query += ` where provider = ?`
		args = append(args, provider)
	case fileName != "":
		query += ` where file_name = ?`
		args = append(args, fileName)
	}
	query += " order by cached_at_ms desc"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := make([]QuotaCacheEntry, 0)
	for rows.Next() {
		var entry QuotaCacheEntry
		var raw string
		if err := rows.Scan(&entry.ID, &entry.Provider, &entry.FileName, &entry.AuthIndex, &entry.IdentityFingerprint,
			&raw, &entry.CachedAt, &entry.AccessedAt, &entry.ObservedAt, &entry.StoredAt, &entry.Version, &entry.Revision); err != nil {
			return nil, err
		}
		entry.Data = json.RawMessage(raw)
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if provider != "" || fileName != "" {
		_ = s.touchQuotaCache(ctx, provider, fileName, time.Now().UnixMilli())
	}
	return entries, nil
}

func (s *Store) touchQuotaCache(ctx context.Context, provider string, fileName string, accessedAt int64) error {
	switch {
	case provider != "" && fileName != "":
		_, err := s.db.ExecContext(ctx, `update quota_cache set accessed_at_ms = ? where provider = ? and file_name = ?`, accessedAt, provider, fileName)
		return err
	case provider != "":
		_, err := s.db.ExecContext(ctx, `update quota_cache set accessed_at_ms = ? where provider = ?`, accessedAt, provider)
		return err
	case fileName != "":
		_, err := s.db.ExecContext(ctx, `update quota_cache set accessed_at_ms = ? where file_name = ?`, accessedAt, fileName)
		return err
	default:
		return nil
	}
}

func (s *Store) SetQuotaCache(ctx context.Context, entry QuotaCacheEntry) error {
	if entry.Provider == "" || entry.FileName == "" || len(entry.Data) == 0 || !json.Valid(entry.Data) {
		return sql.ErrNoRows
	}
	if entry.ID == "" {
		entry.ID = entry.Provider + ":" + entry.FileName
	}
	now := time.Now().UnixMilli()
	if entry.CachedAt <= 0 {
		entry.CachedAt = now
	}
	if entry.ObservedAt <= 0 || entry.ObservedAt > now+5*60*1000 {
		entry.ObservedAt = entry.CachedAt
	}
	if entry.ObservedAt > now+5*60*1000 {
		entry.ObservedAt = now
	}
	if entry.AccessedAt <= 0 {
		entry.AccessedAt = now
	}
	if entry.Version <= 0 {
		entry.Version = 1
	}
	s.quotaCacheMu.Lock()
	defer s.quotaCacheMu.Unlock()

	return retrySQLiteBusy(ctx, func() error {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		changed, err := upsertQuotaCacheTx(ctx, tx, entry, false, now)
		if err != nil {
			return err
		}
		if changed {
			if err := bumpRuntimeGenerationTx(ctx, tx, "quota_cache", now); err != nil {
				return err
			}
		}
		return tx.Commit()
	})
}

func upsertQuotaCacheTx(ctx context.Context, tx *sql.Tx, entry QuotaCacheEntry, preserveRevision bool, now int64) (bool, error) {
	var currentObserved, currentRevision int64
	err := tx.QueryRowContext(ctx, `select observed_at_ms, revision from quota_cache where id = ?`, entry.ID).Scan(&currentObserved, &currentRevision)
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	if err == nil {
		if preserveRevision {
			if entry.Revision > 0 && entry.Revision <= currentRevision {
				return false, nil
			}
			if entry.Revision <= 0 && entry.ObservedAt <= currentObserved {
				return false, nil
			}
		} else if entry.ObservedAt < currentObserved {
			return false, nil
		}
	}
	if entry.Revision <= currentRevision || !preserveRevision {
		entry.Revision = currentRevision + 1
	}
	entry.StoredAt = now
	_, err = tx.ExecContext(ctx, `insert into quota_cache(
		id, provider, file_name, auth_index, identity_fingerprint, data_json, cached_at_ms, accessed_at_ms,
		observed_at_ms, stored_at_ms, version, revision)
		values(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		on conflict(id) do update set
			provider = excluded.provider,
			file_name = excluded.file_name,
			auth_index = excluded.auth_index,
			identity_fingerprint = excluded.identity_fingerprint,
			data_json = excluded.data_json,
			cached_at_ms = excluded.cached_at_ms,
			accessed_at_ms = excluded.accessed_at_ms,
			observed_at_ms = excluded.observed_at_ms,
			stored_at_ms = excluded.stored_at_ms,
			version = excluded.version,
			revision = excluded.revision`, entry.ID, entry.Provider, entry.FileName, entry.AuthIndex,
		entry.IdentityFingerprint, string(entry.Data), entry.CachedAt, entry.AccessedAt, entry.ObservedAt,
		entry.StoredAt, entry.Version, entry.Revision)
	return err == nil, err
}

func bumpRuntimeGenerationTx(ctx context.Context, tx *sql.Tx, stateKey string, now int64) error {
	_, err := tx.ExecContext(ctx, `insert into runtime_state_meta(state_key, generation, updated_at_ms) values(?, 2, ?)
		on conflict(state_key) do update set generation = runtime_state_meta.generation + 1, updated_at_ms = excluded.updated_at_ms`, stateKey, now)
	return err
}

func (s *Store) ImportQuotaCache(ctx context.Context, entries []QuotaCacheEntry) (int, error) {
	if len(entries) == 0 {
		return 0, nil
	}
	s.quotaCacheMu.Lock()
	defer s.quotaCacheMu.Unlock()
	imported := 0
	err := retrySQLiteBusy(ctx, func() error {
		attemptImported := 0
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		now := time.Now().UnixMilli()
		for _, entry := range entries {
			if entry.Provider == "" || entry.FileName == "" || len(entry.Data) == 0 || !json.Valid(entry.Data) {
				continue
			}
			if entry.ID == "" {
				entry.ID = entry.Provider + ":" + entry.FileName
			}
			if entry.Version <= 0 {
				entry.Version = 1
			}
			if entry.ObservedAt <= 0 {
				entry.ObservedAt = entry.CachedAt
			}
			changed, err := upsertQuotaCacheTx(ctx, tx, entry, true, now)
			if err != nil {
				return err
			}
			if changed {
				attemptImported++
			}
		}
		if attemptImported > 0 {
			if err := bumpRuntimeGenerationTx(ctx, tx, "quota_cache", now); err != nil {
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		imported = attemptImported
		return nil
	})
	return imported, err
}

func retrySQLiteBusy(ctx context.Context, operation func() error) error {
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		err = operation()
		if !isSQLiteBusy(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 100 * time.Millisecond):
		}
	}
	return err
}

func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "sqlite_busy") || strings.Contains(message, "database is locked") || strings.Contains(message, "database table is locked")
}

func (s *Store) DeleteQuotaCache(ctx context.Context, provider string, fileName string) error {
	s.quotaCacheMu.Lock()
	defer s.quotaCacheMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var result sql.Result
	switch {
	case provider == "" && fileName == "":
		result, err = tx.ExecContext(ctx, `delete from quota_cache`)
	case provider != "" && fileName != "":
		result, err = tx.ExecContext(ctx, `delete from quota_cache where provider = ? and file_name = ?`, provider, fileName)
	case provider != "":
		result, err = tx.ExecContext(ctx, `delete from quota_cache where provider = ?`, provider)
	default:
		result, err = tx.ExecContext(ctx, `delete from quota_cache where file_name = ?`, fileName)
	}
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected > 0 {
		if err := bumpRuntimeGenerationTx(ctx, tx, "quota_cache", time.Now().UnixMilli()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) QuotaCacheStats(ctx context.Context) (QuotaCacheStats, error) {
	var stats QuotaCacheStats
	err := s.db.QueryRowContext(ctx, `select count(*), coalesce(max(stored_at_ms), 0),
		coalesce((select generation from runtime_state_meta where state_key = 'quota_cache'), 1) from quota_cache`).Scan(&stats.TotalEntries, &stats.UpdatedAt, &stats.Generation)
	return stats, err
}

func (s *Store) GetRoutingCursorState(ctx context.Context, cursorKey string) (RoutingCursorState, bool, error) {
	var state RoutingCursorState
	err := s.db.QueryRowContext(ctx, `select cursor_key, last_auth_id, updated_at_ms from routing_cursor_state where cursor_key = ?`, cursorKey).
		Scan(&state.CursorKey, &state.LastAuthID, &state.UpdatedAtMS)
	if err == sql.ErrNoRows {
		return RoutingCursorState{}, false, nil
	}
	return state, err == nil, err
}

func (s *Store) ListRoutingCursorStates(ctx context.Context) ([]RoutingCursorState, error) {
	rows, err := s.db.QueryContext(ctx, `select cursor_key, last_auth_id, updated_at_ms from routing_cursor_state order by cursor_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]RoutingCursorState, 0)
	for rows.Next() {
		var item RoutingCursorState
		if err := rows.Scan(&item.CursorKey, &item.LastAuthID, &item.UpdatedAtMS); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) SetRoutingCursorState(ctx context.Context, state RoutingCursorState) error {
	state.CursorKey = strings.TrimSpace(state.CursorKey)
	state.LastAuthID = strings.TrimSpace(state.LastAuthID)
	if state.CursorKey == "" || state.LastAuthID == "" {
		return sql.ErrNoRows
	}
	if state.UpdatedAtMS <= 0 {
		state.UpdatedAtMS = time.Now().UnixMilli()
	}
	_, err := s.db.ExecContext(ctx, `insert into routing_cursor_state(cursor_key, last_auth_id, updated_at_ms) values(?, ?, ?)
		on conflict(cursor_key) do update set last_auth_id = excluded.last_auth_id, updated_at_ms = excluded.updated_at_ms
		where excluded.updated_at_ms >= routing_cursor_state.updated_at_ms`, state.CursorKey, state.LastAuthID, state.UpdatedAtMS)
	return err
}

func (s *Store) ImportRoutingCursorStates(ctx context.Context, items []RoutingCursorState) (int, error) {
	imported, _, err := s.ImportRuntimeState(ctx, items, nil)
	return imported, err
}

func (s *Store) ImportRuntimeState(ctx context.Context, cursors []RoutingCursorState, stats []AuthRuntimeStats) (int, int, error) {
	if len(cursors) == 0 && len(stats) == 0 {
		return 0, 0, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = tx.Rollback() }()
	importedCursors := 0
	for _, item := range cursors {
		item.CursorKey = strings.TrimSpace(item.CursorKey)
		item.LastAuthID = strings.TrimSpace(item.LastAuthID)
		if item.CursorKey == "" || item.LastAuthID == "" {
			continue
		}
		if item.UpdatedAtMS <= 0 {
			item.UpdatedAtMS = time.Now().UnixMilli()
		}
		result, err := tx.ExecContext(ctx, `insert into routing_cursor_state(cursor_key, last_auth_id, updated_at_ms) values(?, ?, ?)
			on conflict(cursor_key) do update set last_auth_id = excluded.last_auth_id, updated_at_ms = excluded.updated_at_ms`,
			item.CursorKey, item.LastAuthID, item.UpdatedAtMS)
		if err != nil {
			return 0, 0, err
		}
		if affected, _ := result.RowsAffected(); affected > 0 {
			importedCursors++
		}
	}
	if importedCursors > 0 {
		if err := bumpRuntimeGenerationTx(ctx, tx, "routing_cursor_state", time.Now().UnixMilli()); err != nil {
			return 0, 0, err
		}
	}
	importedStats := 0
	for _, item := range stats {
		changed, err := setAuthRuntimeStatsTx(ctx, tx, item, true)
		if err != nil {
			return 0, 0, err
		}
		if changed {
			importedStats++
		}
	}
	if importedStats > 0 {
		if err := bumpRuntimeGenerationTx(ctx, tx, "auth_runtime_stats", time.Now().UnixMilli()); err != nil {
			return 0, 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return importedCursors, importedStats, nil
}

func (s *Store) GetAuthRuntimeStats(ctx context.Context, authIndex, authID string) (AuthRuntimeStats, bool, error) {
	query := `select auth_index, auth_id, file_name, identity_fingerprint, selected_count, success_count, failure_count,
		recent_buckets_json, generation, updated_at_ms from auth_runtime_stats where auth_index = ?`
	arg := strings.TrimSpace(authIndex)
	if arg == "" {
		query = strings.Replace(query, "auth_index = ?", "auth_id = ?", 1)
		arg = strings.TrimSpace(authID)
	}
	var item AuthRuntimeStats
	var buckets string
	err := s.db.QueryRowContext(ctx, query, arg).Scan(&item.AuthIndex, &item.AuthID, &item.FileName, &item.IdentityFingerprint,
		&item.SelectedCount, &item.SuccessCount, &item.FailureCount, &buckets, &item.Generation, &item.UpdatedAtMS)
	if err == sql.ErrNoRows {
		return AuthRuntimeStats{}, false, nil
	}
	if err != nil {
		return AuthRuntimeStats{}, false, err
	}
	if err := json.Unmarshal([]byte(buckets), &item.RecentBuckets); err != nil {
		return AuthRuntimeStats{}, false, err
	}
	return item, true, nil
}

func (s *Store) ListAuthRuntimeStats(ctx context.Context) ([]AuthRuntimeStats, error) {
	rows, err := s.db.QueryContext(ctx, `select auth_index, auth_id, file_name, identity_fingerprint, selected_count, success_count,
		failure_count, recent_buckets_json, generation, updated_at_ms from auth_runtime_stats order by auth_index`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]AuthRuntimeStats, 0)
	for rows.Next() {
		var item AuthRuntimeStats
		var buckets string
		if err := rows.Scan(&item.AuthIndex, &item.AuthID, &item.FileName, &item.IdentityFingerprint, &item.SelectedCount,
			&item.SuccessCount, &item.FailureCount, &buckets, &item.Generation, &item.UpdatedAtMS); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(buckets), &item.RecentBuckets); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func normalizeRuntimeBuckets(items []RuntimeRequestBucket) []RuntimeRequestBucket {
	if len(items) > 20 {
		items = items[len(items)-20:]
	}
	out := make([]RuntimeRequestBucket, 0, len(items))
	for _, item := range items {
		if item.BucketID <= 0 || item.Success < 0 || item.Failed < 0 {
			continue
		}
		out = append(out, item)
	}
	return out
}

func setAuthRuntimeStatsTx(ctx context.Context, tx *sql.Tx, item AuthRuntimeStats, force bool) (bool, error) {
	item.AuthIndex = strings.TrimSpace(item.AuthIndex)
	item.AuthID = strings.TrimSpace(item.AuthID)
	if item.AuthIndex == "" || item.AuthID == "" || item.SelectedCount < 0 || item.SuccessCount < 0 || item.FailureCount < 0 {
		return false, nil
	}
	item.RecentBuckets = normalizeRuntimeBuckets(item.RecentBuckets)
	buckets, err := json.Marshal(item.RecentBuckets)
	if err != nil {
		return false, err
	}
	if item.Generation <= 0 {
		item.Generation = 1
	}
	if item.UpdatedAtMS <= 0 {
		item.UpdatedAtMS = time.Now().UnixMilli()
	}
	query := `insert into auth_runtime_stats(auth_index, auth_id, file_name, identity_fingerprint,
		selected_count, success_count, failure_count, recent_buckets_json, generation, updated_at_ms)
		values(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		on conflict(auth_index) do update set
			auth_id = excluded.auth_id,
			file_name = excluded.file_name,
			identity_fingerprint = excluded.identity_fingerprint,
			selected_count = excluded.selected_count,
			success_count = excluded.success_count,
			failure_count = excluded.failure_count,
			recent_buckets_json = excluded.recent_buckets_json,
			generation = auth_runtime_stats.generation + 1,
			updated_at_ms = excluded.updated_at_ms`
	if !force {
		query += ` where excluded.updated_at_ms >= auth_runtime_stats.updated_at_ms`
	}
	result, err := tx.ExecContext(ctx, query, item.AuthIndex, item.AuthID, item.FileName,
		item.IdentityFingerprint, item.SelectedCount, item.SuccessCount, item.FailureCount, string(buckets),
		item.Generation, item.UpdatedAtMS)
	if err != nil {
		return false, err
	}
	affected, _ := result.RowsAffected()
	return affected > 0, nil
}

func (s *Store) SetAuthRuntimeStats(ctx context.Context, item AuthRuntimeStats) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	changed, err := setAuthRuntimeStatsTx(ctx, tx, item, false)
	if err != nil {
		return err
	}
	if changed {
		if err := bumpRuntimeGenerationTx(ctx, tx, "auth_runtime_stats", time.Now().UnixMilli()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ImportAuthRuntimeStats(ctx context.Context, items []AuthRuntimeStats) (int, error) {
	_, imported, err := s.ImportRuntimeState(ctx, nil, items)
	return imported, err
}

func (s *Store) DeleteAuthRuntimeState(ctx context.Context, authID, authIndex, fileName string) error {
	s.quotaCacheMu.Lock()
	defer s.quotaCacheMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `delete from auth_runtime_stats where
		(? != '' and auth_id = ?) or (? != '' and auth_index = ?) or (? != '' and file_name = ?)`,
		authID, authID, authIndex, authIndex, fileName, fileName)
	if err != nil {
		return err
	}
	quotaResult, err := tx.ExecContext(ctx, `delete from quota_cache where
		(? != '' and auth_index = ?) or (? != '' and file_name = ?)`, authIndex, authIndex, fileName, fileName)
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	if affected, _ := result.RowsAffected(); affected > 0 {
		if err := bumpRuntimeGenerationTx(ctx, tx, "auth_runtime_stats", now); err != nil {
			return err
		}
	}
	if affected, _ := quotaResult.RowsAffected(); affected > 0 {
		if err := bumpRuntimeGenerationTx(ctx, tx, "quota_cache", now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) GetModelPrices(ctx context.Context) (map[string]ModelPrice, error) {
	return s.getModelPrices(ctx, true)
}

func (s *Store) getModelPrices(ctx context.Context, includeProviderRules bool) (map[string]ModelPrice, error) {
	query := `select case when a.provider = '' then a.model else a.provider || '/' || a.model end,
		json_extract(v.rule_json, '$.base.input'),
		json_extract(v.rule_json, '$.base.output'), json_extract(v.rule_json, '$.base.cacheRead')
		from model_price_rules a join model_price_rule_versions v
		on v.provider = a.provider and v.model = a.model and v.version = a.active_version`
	if !includeProviderRules {
		query += ` where a.provider = ''`
	}
	query += ` order by a.provider, a.model`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	prices := make(map[string]ModelPrice)
	for rows.Next() {
		var model string
		var price ModelPrice
		if err := rows.Scan(&model, &price.Prompt, &price.Completion, &price.Cache); err != nil {
			return nil, err
		}
		prices[model] = price
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return prices, nil
}

func (s *Store) SetModelPrices(ctx context.Context, prices map[string]ModelPrice) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `delete from model_prices`); err != nil {
		return err
	}

	now := time.Now().UnixMilli()
	stmt, err := tx.PrepareContext(ctx, `insert into model_prices(model, prompt_price, completion_price, cache_price, updated_at_ms) values(?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for model, price := range prices {
		if model == "" {
			continue
		}
		if _, err := stmt.ExecContext(ctx, model, price.Prompt, price.Completion, price.Cache, now); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `delete from model_price_rules where provider = ''`); err != nil {
		return err
	}
	for model, price := range prices {
		if strings.TrimSpace(model) == "" {
			continue
		}
		if _, _, err := s.UpsertModelPriceRule(ctx, ModelPriceRule{
			Model:  model,
			Base:   ModelPriceRate{Input: price.Prompt, Output: price.Completion, CacheRead: price.Cache},
			Source: modelPriceSourceManual,
			Locked: true,
		}, true); err != nil {
			return err
		}
	}
	return nil
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullFloat64(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullPositiveInt64(value int64) any {
	if value <= 0 {
		return nil
	}
	return value
}

func nullInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}
