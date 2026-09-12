package apikeypolicy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	prostorage "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/storage"
)

type Store struct {
	database *prostorage.Database
	db       *sql.DB
}

type quotaSummaryKey struct {
	policyID string
	epoch    int64
}

type quotaSummaryWindow struct {
	index      int
	key        quotaSummaryKey
	startMS    int64
	queryEndMS int64
}

type quotaSummaryAdmission struct{ occurredAt int64 }

type quotaSummaryEvent struct {
	totalTokens int64
	costMicros  int64
	occurredAt  int64
}

func OpenStore(path string) (*Store, error) {
	database, err := prostorage.OpenSQLite(path)
	if err != nil {
		return nil, err
	}
	store := &Store{database: database, db: database.SQL()}
	if err := store.init(context.Background()); err != nil {
		_ = database.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.database == nil {
		return nil
	}
	database := s.database
	s.database = nil
	s.db = nil
	return database.Close()
}

func (s *Store) init(ctx context.Context) error {
	return prostorage.ApplySchema(ctx, s.db, prostorage.Schema{Create: []string{
		`create table if not exists api_key_disabled_keys (api_key_hash text primary key)`,
		`create table if not exists api_key_concurrency_limits (api_key_hash text primary key, max_concurrent integer not null check(max_concurrent > 0 and max_concurrent <= 1000000))`,
		`create table if not exists api_key_policies (
			id text primary key,
			api_key_hash text not null unique,
			display_name text not null default '',
			profile_enabled integer check(profile_enabled in (0, 1)),
			active_profile_id text,
			version integer not null default 1 check(version > 0),
			created_at_ms integer not null,
			updated_at_ms integer not null,
			foreign key(active_profile_id) references api_key_profiles(id) deferrable initially deferred
		)`,
		`create table if not exists api_key_profiles (
			id text primary key,
			policy_id text not null,
			name text not null,
			created_at_ms integer not null,
			updated_at_ms integer not null,
			unique(policy_id, name),
			foreign key(policy_id) references api_key_policies(id) on delete cascade
		)`,
		`create table if not exists api_key_profile_providers (
			profile_id text not null,
			provider_id text not null,
			primary key(profile_id, provider_id),
			foreign key(profile_id) references api_key_profiles(id) on delete cascade
		)`,
		`create table if not exists api_key_profile_models (
			profile_id text not null,
			model_id text not null,
			primary key(profile_id, model_id),
			foreign key(profile_id) references api_key_profiles(id) on delete cascade
		)`,
		`create table if not exists api_key_profile_model_mappings (
			profile_id text not null,
			source_model text not null,
			target_model text not null,
			primary key(profile_id, source_model),
			foreign key(profile_id) references api_key_profiles(id) on delete cascade
		)`,
		`create table if not exists api_key_policy_audit (
			id integer primary key autoincrement,
			policy_id text not null,
			event_type text not null,
			details_json text not null default '{}',
			created_at_ms integer not null
		)`,
		`create table if not exists api_key_policy_settings (
			id integer primary key check(id = 1),
			takeover_enabled integer not null default 0 check(takeover_enabled in (0, 1))
		)`,
		`create table if not exists api_key_quota_generations (
			policy_id text primary key,
			generation integer not null check(generation > 0),
			foreign key(policy_id) references api_key_policies(id) on delete cascade
		)`,
		`create table if not exists api_key_policy_quotas (
			policy_id text primary key,
			enabled integer not null default 0 check(enabled in (0, 1)),
			request_limit integer check(request_limit is null or request_limit > 0),
			token_limit integer check(token_limit is null or token_limit > 0),
			cost_limit_micros integer check(cost_limit_micros is null or cost_limit_micros > 0),
			period_type text not null default 'all_time',
			period_value integer check(period_value is null or period_value > 0),
			period_unit text not null default '',
			period_timezone text not null default 'UTC',
			epoch integer not null default 1 check(epoch > 0),
			started_at_ms integer not null,
			requests_used integer not null default 0 check(requests_used >= 0),
			total_tokens_used integer not null default 0 check(total_tokens_used >= 0),
			cost_used_micros integer not null default 0 check(cost_used_micros >= 0),
			updated_at_ms integer not null,
			foreign key(policy_id) references api_key_policies(id) on delete cascade
		)`,
		`create table if not exists api_key_quota_admissions (
			admission_id text primary key,
			policy_id text not null,
			profile_id text not null,
			epoch integer not null,
			admitted_at_ms integer not null,
			foreign key(policy_id) references api_key_policies(id) on delete cascade
		)`,
		`create table if not exists api_key_quota_token_events (
			event_id text primary key,
			admission_id text not null,
			policy_id text not null,
			profile_id text not null,
			epoch integer not null,
			total_tokens integer not null check(total_tokens >= 0),
			cost_micros integer not null default 0 check(cost_micros >= 0),
			occurred_at_ms integer not null,
			foreign key(admission_id) references api_key_quota_admissions(admission_id) on delete cascade,
			foreign key(policy_id) references api_key_policies(id) on delete cascade
		)`,
		`create table if not exists api_key_quota_pending_settlements (
			event_id text primary key,
			admission_id text not null,
			policy_id text not null,
			profile_id text not null,
			epoch integer not null,
			usage_json text not null,
			require_cost integer not null check(require_cost in (0, 1)),
			quoted integer not null default 0 check(quoted in (0, 1)),
			cost_micros integer not null default 0 check(cost_micros >= 0),
			block_reason text not null,
			created_at_ms integer not null,
			updated_at_ms integer not null,
			foreign key(admission_id) references api_key_quota_admissions(admission_id) on delete cascade,
			foreign key(policy_id) references api_key_policies(id) on delete cascade
		)`,
		`insert into api_key_policy_settings(id, takeover_enabled) values(1, 0) on conflict(id) do nothing`,
		`create index if not exists idx_api_key_profiles_policy on api_key_profiles(policy_id, created_at_ms, id)`,
		`create index if not exists idx_api_key_policy_audit_policy on api_key_policy_audit(policy_id, created_at_ms)`,
		`create index if not exists idx_api_key_quota_admissions_policy on api_key_quota_admissions(policy_id, epoch)`,
		`create index if not exists idx_api_key_quota_tokens_policy on api_key_quota_token_events(policy_id, epoch)`,
		`create index if not exists idx_api_key_quota_admissions_window on api_key_quota_admissions(policy_id, epoch, admitted_at_ms)`,
		`create index if not exists idx_api_key_quota_tokens_window on api_key_quota_token_events(policy_id, epoch, occurred_at_ms)`,
		`create index if not exists idx_api_key_quota_pending_policy on api_key_quota_pending_settlements(policy_id, epoch)`,
	}, Alter: []string{
		`alter table api_key_policy_quotas add column cost_limit_micros integer check(cost_limit_micros is null or cost_limit_micros > 0)`,
		`alter table api_key_policy_quotas add column period_type text not null default 'all_time'`,
		`alter table api_key_policy_quotas add column period_value integer check(period_value is null or period_value > 0)`,
		`alter table api_key_policy_quotas add column period_unit text not null default ''`,
		`alter table api_key_policy_quotas add column period_timezone text not null default 'UTC'`,
		`alter table api_key_policy_quotas add column cost_used_micros integer not null default 0 check(cost_used_micros >= 0)`,
		`alter table api_key_quota_token_events add column cost_micros integer not null default 0 check(cost_micros >= 0)`,
		`alter table api_key_policies add column profile_enabled integer check(profile_enabled in (0, 1))`,
	}, Seed: []string{
		`update api_key_policies set profile_enabled = case when active_profile_id is null then 0 else 1 end where profile_enabled is null`,
		`update api_key_policies set active_profile_id = (
			select id from api_key_profiles where policy_id = api_key_policies.id order by created_at_ms, id limit 1
		) where profile_enabled = 0 and active_profile_id is null and exists (
			select 1 from api_key_profiles where policy_id = api_key_policies.id
		)`,
	}})
}

func (s *Store) TakeoverEnabled(ctx context.Context) (bool, error) {
	return takeoverEnabled(ctx, s.db)
}

func takeoverEnabled(ctx context.Context, queryer sqlQueryer) (bool, error) {
	var enabled bool
	err := queryer.QueryRowContext(ctx, `select takeover_enabled from api_key_policy_settings where id = 1`).Scan(&enabled)
	return enabled, err
}

type sqlQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *Store) List(ctx context.Context) ([]Policy, error) {
	return listPolicies(ctx, s.db)
}

func (s *Store) ListProfileCatalog(ctx context.Context) ([]ProfileCatalogItem, error) {
	rows, err := s.db.QueryContext(ctx, `select id, name, updated_at_ms from api_key_profiles order by id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ProfileCatalogItem, 0)
	for rows.Next() {
		var item ProfileCatalogItem
		if err := rows.Scan(&item.ID, &item.Name, &item.UpdatedAtMS); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) ListAudits(ctx context.Context) ([]AuditRecord, error) {
	return listAudits(ctx, s.db)
}

func listAudits(ctx context.Context, queryer sqlQueryer) ([]AuditRecord, error) {
	rows, err := queryer.QueryContext(ctx, `select id, policy_id, event_type, details_json, created_at_ms from api_key_policy_audit order by id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]AuditRecord, 0)
	for rows.Next() {
		var item AuditRecord
		var details string
		if err := rows.Scan(&item.ID, &item.PolicyID, &item.EventType, &details, &item.CreatedAtMS); err != nil {
			return nil, err
		}
		item.Details = json.RawMessage(details)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) ListMatchingHashes(ctx context.Context, hashes []string) ([]Policy, error) {
	if len(hashes) == 0 {
		return []Policy{}, nil
	}
	hashesJSON, err := json.Marshal(hashes)
	if err != nil {
		return nil, err
	}
	return queryPolicies(ctx, s.db, `select id, api_key_hash, display_name, coalesce(profile_enabled, case when active_profile_id is null then 0 else 1 end), coalesce(active_profile_id, ''), version, created_at_ms, updated_at_ms from api_key_policies where api_key_hash in (select value from json_each(?)) order by created_at_ms, id`, string(hashesJSON))
}

// ListExcludingHashes returns one stable database page of policies whose API
// key fingerprint is not present in the current upstream configuration.
func (s *Store) ListExcludingHashes(ctx context.Context, configuredHashes []string, afterCreatedAtMS int64, afterID string, limit int) ([]Policy, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	query := `select id, api_key_hash, display_name, coalesce(profile_enabled, case when active_profile_id is null then 0 else 1 end), coalesce(active_profile_id, ''), version, created_at_ms, updated_at_ms from api_key_policies where (created_at_ms > ? or (created_at_ms = ? and id > ?))`
	args := []any{afterCreatedAtMS, afterCreatedAtMS, strings.TrimSpace(afterID)}
	if len(configuredHashes) > 0 {
		hashesJSON, err := json.Marshal(configuredHashes)
		if err != nil {
			return nil, err
		}
		query += ` and api_key_hash not in (select value from json_each(?))`
		args = append(args, string(hashesJSON))
	}
	query += ` order by created_at_ms, id limit ?`
	args = append(args, limit)
	return queryPolicies(ctx, s.db, query, args...)
}

func listPolicies(ctx context.Context, queryer sqlQueryer) ([]Policy, error) {
	return queryPolicies(ctx, queryer, `select id, api_key_hash, display_name, coalesce(profile_enabled, case when active_profile_id is null then 0 else 1 end), coalesce(active_profile_id, ''), version, created_at_ms, updated_at_ms from api_key_policies order by created_at_ms, id`)
}

func queryPolicies(ctx context.Context, queryer sqlQueryer, query string, args ...any) ([]Policy, error) {
	rows, err := queryer.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	policies := make([]Policy, 0)
	for rows.Next() {
		var policy Policy
		if err := rows.Scan(&policy.ID, &policy.APIKeyHash, &policy.DisplayName, &policy.ProfileEnabled, &policy.ActiveProfileID, &policy.Version, &policy.CreatedAtMS, &policy.UpdatedAtMS); err != nil {
			_ = rows.Close()
			return nil, err
		}
		policies = append(policies, policy)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range policies {
		profiles, err := listProfiles(ctx, queryer, policies[i].ID)
		if err != nil {
			return nil, err
		}
		policies[i].Profiles = profiles
		quota, err := getPolicyQuota(ctx, queryer, policies[i].ID)
		if err != nil {
			return nil, err
		}
		policies[i].Quota = quota
	}
	return policies, nil
}

func (s *Store) Get(ctx context.Context, policyID string) (Policy, error) {
	var policy Policy
	err := s.db.QueryRowContext(ctx, `select id, api_key_hash, display_name, coalesce(profile_enabled, case when active_profile_id is null then 0 else 1 end), coalesce(active_profile_id, ''), version, created_at_ms, updated_at_ms from api_key_policies where id = ?`, strings.TrimSpace(policyID)).
		Scan(&policy.ID, &policy.APIKeyHash, &policy.DisplayName, &policy.ProfileEnabled, &policy.ActiveProfileID, &policy.Version, &policy.CreatedAtMS, &policy.UpdatedAtMS)
	if errors.Is(err, sql.ErrNoRows) {
		return Policy{}, ErrPolicyNotFound
	}
	if err != nil {
		return Policy{}, err
	}
	policy.Profiles, err = listProfiles(ctx, s.db, policy.ID)
	if err != nil {
		return Policy{}, err
	}
	policy.Quota, err = getPolicyQuota(ctx, s.db, policy.ID)
	return policy, err
}

func (s *Store) ListQuotaSummaries(ctx context.Context, nowMS int64) ([]QuotaSummary, error) {
	rows, err := s.db.QueryContext(ctx, `select p.id, p.version,
		q.policy_id, q.enabled, q.request_limit, q.token_limit, q.cost_limit_micros,
		q.period_type, q.period_value, q.period_unit, q.period_timezone, q.epoch, q.started_at_ms,
		q.requests_used, q.total_tokens_used, q.cost_used_micros, q.updated_at_ms
		from api_key_policies p
		left join api_key_policy_quotas q on q.policy_id = p.id
		order by p.created_at_ms, p.id`)
	if err != nil {
		return nil, err
	}
	summaries := make([]QuotaSummary, 0)
	windows := make([]quotaSummaryWindow, 0)
	windowByKey := make(map[quotaSummaryKey]int)
	minWindowStartMS := int64(math.MaxInt64)
	for rows.Next() {
		var summary QuotaSummary
		var quotaID, periodType, periodUnit, periodTimezone sql.NullString
		var enabled, requestLimit, tokenLimit, costLimitMicros, periodValue, epoch, startedAtMS, requestsUsed, totalTokensUsed, costUsedMicros, updatedAtMS sql.NullInt64
		if err = rows.Scan(
			&summary.PolicyID, &summary.PolicyVersion,
			&quotaID, &enabled, &requestLimit, &tokenLimit, &costLimitMicros,
			&periodType, &periodValue, &periodUnit, &periodTimezone, &epoch, &startedAtMS,
			&requestsUsed, &totalTokensUsed, &costUsedMicros, &updatedAtMS,
		); err != nil {
			_ = rows.Close()
			return nil, err
		}
		summary.AdmissionState = QuotaAdmissionDisabled
		if quotaID.Valid {
			quota := &Quota{
				Enabled: enabled.Valid && enabled.Int64 != 0,
				Period:  QuotaPeriod{Type: periodType.String, Unit: periodUnit.String, Timezone: periodTimezone.String},
				Usage: QuotaUsage{
					RequestsUsed:    requestsUsed.Int64,
					TotalTokensUsed: totalTokensUsed.Int64,
					CostUsed:        microsToUSD(costUsedMicros.Int64),
				},
			}
			if requestLimit.Valid {
				value := requestLimit.Int64
				quota.Requests = &value
			}
			if tokenLimit.Valid {
				value := tokenLimit.Int64
				quota.TotalTokens = &value
			}
			if costLimitMicros.Valid {
				value := microsToUSD(costLimitMicros.Int64)
				quota.Cost = &value
			}
			if periodValue.Valid {
				value := periodValue.Int64
				quota.Period.Value = &value
			}
			if epoch.Valid {
				quota.Epoch = epoch.Int64
			}
			if startedAtMS.Valid {
				quota.StartedAtMS = startedAtMS.Int64
			}
			if updatedAtMS.Valid {
				quota.UpdatedAtMS = updatedAtMS.Int64
			}
			quota.Period = normalizeQuotaPeriod(quota.Period)
			startMS, endMS := quotaPeriodBounds(quota.Period, quota.StartedAtMS, nowMS)
			quota.Usage.WindowStartedAtMS = startMS
			quota.Usage.WindowEndsAtMS = endMS
			summary.Quota = quota
			if quota.Period.Type != QuotaPeriodAllTime {
				queryEndMS := endMS
				if quota.Period.Type == QuotaPeriodPastDuration && queryEndMS < math.MaxInt64 {
					queryEndMS++
				}
				key := quotaSummaryKey{policyID: summary.PolicyID, epoch: quota.Epoch}
				windowByKey[key] = len(windows)
				windows = append(windows, quotaSummaryWindow{index: len(summaries), key: key, startMS: startMS, queryEndMS: queryEndMS})
				if startMS < minWindowStartMS {
					minWindowStartMS = startMS
				}
				// Rolling/calendar usage is recomputed from the event tables below.
				quota.Usage.RequestsUsed = 0
				quota.Usage.TotalTokensUsed = 0
				quota.Usage.CostUsed = 0
			}
		}
		summaries = append(summaries, summary)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	admissionsByKey := make(map[quotaSummaryKey][]quotaSummaryAdmission, len(windows))
	if len(windows) == 0 {
		for index := range summaries {
			if summaries[index].Quota != nil {
				summaries[index].Quota.Usage = quotaUsage(*summaries[index].Quota)
			}
		}
		return summaries, nil
	}
	admissionRows, err := s.db.QueryContext(ctx, `select a.policy_id, a.epoch, a.admitted_at_ms
		from api_key_quota_admissions a
		join api_key_policy_quotas q on q.policy_id = a.policy_id and q.epoch = a.epoch
		where q.period_type <> ? and a.admitted_at_ms >= ?
		order by a.policy_id, a.epoch, a.admitted_at_ms, a.admission_id`, QuotaPeriodAllTime, minWindowStartMS)
	if err != nil {
		return nil, err
	}
	for admissionRows.Next() {
		var key quotaSummaryKey
		var occurredAt int64
		if err = admissionRows.Scan(&key.policyID, &key.epoch, &occurredAt); err != nil {
			_ = admissionRows.Close()
			return nil, err
		}
		if windowIndex, ok := windowByKey[key]; ok {
			window := windows[windowIndex]
			if occurredAt < window.startMS {
				continue
			}
			admissionsByKey[key] = append(admissionsByKey[key], quotaSummaryAdmission{occurredAt: occurredAt})
			if occurredAt >= window.startMS && (window.queryEndMS == 0 || occurredAt < window.queryEndMS) {
				summaries[window.index].Quota.Usage.RequestsUsed++
			}
		}
	}
	if err = admissionRows.Err(); err != nil {
		_ = admissionRows.Close()
		return nil, err
	}
	if err = admissionRows.Close(); err != nil {
		return nil, err
	}

	eventsByKey := make(map[quotaSummaryKey][]quotaSummaryEvent, len(windows))
	eventRows, err := s.db.QueryContext(ctx, `select e.policy_id, e.epoch, e.total_tokens, e.cost_micros, e.occurred_at_ms
		from api_key_quota_token_events e
		join api_key_policy_quotas q on q.policy_id = e.policy_id and q.epoch = e.epoch
		where q.period_type <> ? and e.occurred_at_ms >= ?
		order by e.policy_id, e.epoch, e.occurred_at_ms, e.event_id`, QuotaPeriodAllTime, minWindowStartMS)
	if err != nil {
		return nil, err
	}
	for eventRows.Next() {
		var key quotaSummaryKey
		var row quotaSummaryEvent
		if err = eventRows.Scan(&key.policyID, &key.epoch, &row.totalTokens, &row.costMicros, &row.occurredAt); err != nil {
			_ = eventRows.Close()
			return nil, err
		}
		if windowIndex, ok := windowByKey[key]; ok {
			window := windows[windowIndex]
			if row.occurredAt < window.startMS {
				continue
			}
			eventsByKey[key] = append(eventsByKey[key], row)
			if row.occurredAt >= window.startMS && (window.queryEndMS == 0 || row.occurredAt < window.queryEndMS) {
				summaries[window.index].Quota.Usage.TotalTokensUsed += row.totalTokens
			}
		}
	}
	if err = eventRows.Err(); err != nil {
		_ = eventRows.Close()
		return nil, err
	}
	if err = eventRows.Close(); err != nil {
		return nil, err
	}
	for index := range summaries {
		if summaries[index].Quota != nil {
			summaries[index].Quota.Usage = quotaUsage(*summaries[index].Quota)
		}
	}
	for index := range windows {
		window := windows[index]
		quota := summaries[window.index].Quota
		if quota == nil {
			continue
		}
		// Re-sum cost in integer micros to avoid accumulating float conversion
		// error across a large event stream.
		costMicros := int64(0)
		for _, event := range eventsByKey[window.key] {
			if event.occurredAt >= window.startMS && (window.queryEndMS == 0 || event.occurredAt < window.queryEndMS) {
				costMicros += event.costMicros
			}
		}
		quota.Usage.CostUsed = microsToUSD(costMicros)
		quota.Usage = quotaUsage(*quota)
		if quota.Enabled {
			admissions := admissionsByKey[window.key]
			events := eventsByKey[window.key]
			summaries[window.index].NextRecoverAtMS = quotaNextRecoverAtFromRows(*quota, admissions, events)
		}
	}
	return summaries, nil
}

func quotaNextRecoverAtFromRows(quota Quota, admissions []quotaSummaryAdmission, events []quotaSummaryEvent) int64 {
	if len(quota.Usage.Exhausted) == 0 || quota.Period.Type == QuotaPeriodAllTime {
		return 0
	}
	if quota.Period.Type == QuotaPeriodCalendarDuration {
		return quota.Usage.WindowEndsAtMS
	}
	if quota.Period.Type != QuotaPeriodPastDuration || quota.Period.Value == nil {
		return 0
	}
	duration, ok := quotaPastDuration(*quota.Period.Value, quota.Period.Unit)
	if !ok {
		return 0
	}
	recoverAt := int64(0)
	if quota.Requests != nil && quota.Usage.RequestsUsed >= *quota.Requests {
		offset := quota.Usage.RequestsUsed - *quota.Requests
		if offset >= 0 && offset < int64(len(admissions)) {
			recoverAt = admissions[offset].occurredAt + duration.Milliseconds()
		}
	}
	if (quota.TotalTokens != nil && quota.Usage.TotalTokensUsed >= *quota.TotalTokens) ||
		(quota.Cost != nil && quota.Usage.CostUsed >= *quota.Cost) {
		remainingTokens := quota.Usage.TotalTokensUsed
		remainingCostMicros := int64(math.Round(quota.Usage.CostUsed * 1_000_000))
		tokenRecovered := quota.TotalTokens == nil || remainingTokens < *quota.TotalTokens
		costLimitMicros := int64(0)
		if quota.Cost != nil {
			costLimitMicros = int64(math.Round(*quota.Cost * 1_000_000))
		}
		costRecovered := quota.Cost == nil || remainingCostMicros < costLimitMicros
		for _, event := range events {
			remainingTokens -= event.totalTokens
			remainingCostMicros -= event.costMicros
			if !tokenRecovered && quota.TotalTokens != nil && remainingTokens < *quota.TotalTokens {
				tokenRecovered = true
				if candidate := event.occurredAt + duration.Milliseconds(); candidate > recoverAt {
					recoverAt = candidate
				}
			}
			if !costRecovered && quota.Cost != nil && remainingCostMicros < costLimitMicros {
				costRecovered = true
				if candidate := event.occurredAt + duration.Milliseconds(); candidate > recoverAt {
					recoverAt = candidate
				}
			}
			if tokenRecovered && costRecovered {
				break
			}
		}
	}
	return recoverAt
}

func quotaNextRecoverAt(ctx context.Context, queryer sqlQueryer, policyID string, quota Quota) (int64, error) {
	if len(quota.Usage.Exhausted) == 0 || quota.Period.Type == QuotaPeriodAllTime {
		return 0, nil
	}
	if quota.Period.Type == QuotaPeriodCalendarDuration {
		return quota.Usage.WindowEndsAtMS, nil
	}
	if quota.Period.Type != QuotaPeriodPastDuration || quota.Period.Value == nil {
		return 0, nil
	}
	duration, ok := quotaPastDuration(*quota.Period.Value, quota.Period.Unit)
	if !ok {
		return 0, nil
	}
	recoverAt := int64(0)
	if quota.Requests != nil && quota.Usage.RequestsUsed >= *quota.Requests {
		offset := quota.Usage.RequestsUsed - *quota.Requests
		var occurredAt int64
		err := queryer.QueryRowContext(ctx, `select admitted_at_ms from api_key_quota_admissions where policy_id = ? and epoch = ? and admitted_at_ms >= ? order by admitted_at_ms, admission_id limit 1 offset ?`, policyID, quota.Epoch, quota.Usage.WindowStartedAtMS, offset).Scan(&occurredAt)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
		if err == nil {
			recoverAt = occurredAt + duration.Milliseconds()
		}
	}
	if (quota.TotalTokens != nil && quota.Usage.TotalTokensUsed >= *quota.TotalTokens) ||
		(quota.Cost != nil && quota.Usage.CostUsed >= *quota.Cost) {
		rows, err := queryer.QueryContext(ctx, `select total_tokens, cost_micros, occurred_at_ms from api_key_quota_token_events where policy_id = ? and epoch = ? and occurred_at_ms >= ? order by occurred_at_ms, event_id`, policyID, quota.Epoch, quota.Usage.WindowStartedAtMS)
		if err != nil {
			return 0, err
		}
		remainingTokens := quota.Usage.TotalTokensUsed
		remainingCostMicros := int64(math.Round(quota.Usage.CostUsed * 1_000_000))
		tokenRecovered := quota.TotalTokens == nil || remainingTokens < *quota.TotalTokens
		costLimitMicros := int64(0)
		if quota.Cost != nil {
			costLimitMicros = int64(math.Round(*quota.Cost * 1_000_000))
		}
		costRecovered := quota.Cost == nil || remainingCostMicros < costLimitMicros
		for rows.Next() {
			var tokens, costMicros, occurredAt int64
			if err = rows.Scan(&tokens, &costMicros, &occurredAt); err != nil {
				_ = rows.Close()
				return 0, err
			}
			remainingTokens -= tokens
			remainingCostMicros -= costMicros
			if !tokenRecovered && quota.TotalTokens != nil && remainingTokens < *quota.TotalTokens {
				tokenRecovered = true
				if candidate := occurredAt + duration.Milliseconds(); candidate > recoverAt {
					recoverAt = candidate
				}
			}
			if !costRecovered && quota.Cost != nil && remainingCostMicros < costLimitMicros {
				costRecovered = true
				if candidate := occurredAt + duration.Milliseconds(); candidate > recoverAt {
					recoverAt = candidate
				}
			}
			if tokenRecovered && costRecovered {
				break
			}
		}
		if err = rows.Err(); err != nil {
			_ = rows.Close()
			return 0, err
		}
		if err = rows.Close(); err != nil {
			return 0, err
		}
	}
	return recoverAt, nil
}

func getPolicyQuota(ctx context.Context, queryer sqlQueryer, policyID string) (*Quota, error) {
	return getPolicyQuotaAt(ctx, queryer, policyID, time.Now().UnixMilli())
}

func getPolicyQuotaAt(ctx context.Context, queryer sqlQueryer, policyID string, nowMS int64) (*Quota, error) {
	var quota Quota
	var requestLimit, tokenLimit, costLimitMicros, periodValue sql.NullInt64
	var costUsedMicros int64
	err := queryer.QueryRowContext(ctx, `select enabled, request_limit, token_limit, cost_limit_micros, period_type, period_value, period_unit, period_timezone, epoch, started_at_ms, requests_used, total_tokens_used, cost_used_micros, updated_at_ms from api_key_policy_quotas where policy_id = ?`, policyID).
		Scan(&quota.Enabled, &requestLimit, &tokenLimit, &costLimitMicros, &quota.Period.Type, &periodValue, &quota.Period.Unit, &quota.Period.Timezone, &quota.Epoch, &quota.StartedAtMS, &quota.Usage.RequestsUsed, &quota.Usage.TotalTokensUsed, &costUsedMicros, &quota.UpdatedAtMS)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if requestLimit.Valid {
		value := requestLimit.Int64
		quota.Requests = &value
	}
	if tokenLimit.Valid {
		value := tokenLimit.Int64
		quota.TotalTokens = &value
	}
	if costLimitMicros.Valid {
		value := microsToUSD(costLimitMicros.Int64)
		quota.Cost = &value
	}
	if periodValue.Valid {
		value := periodValue.Int64
		quota.Period.Value = &value
	}
	quota.Period = normalizeQuotaPeriod(quota.Period)
	startMS, endMS := quotaPeriodBounds(quota.Period, quota.StartedAtMS, nowMS)
	quota.Usage.WindowStartedAtMS = startMS
	quota.Usage.WindowEndsAtMS = endMS
	if quota.Period.Type != QuotaPeriodAllTime {
		queryEndMS := endMS
		if quota.Period.Type == QuotaPeriodPastDuration && queryEndMS < math.MaxInt64 {
			// Include an admission or settlement written in the exact millisecond
			// used to evaluate a rolling window. Calendar bounds remain half-open.
			queryEndMS++
		}
		if err := queryer.QueryRowContext(ctx, `select count(*) from api_key_quota_admissions where policy_id = ? and epoch = ? and admitted_at_ms >= ? and (? = 0 or admitted_at_ms < ?)`, policyID, quota.Epoch, startMS, queryEndMS, queryEndMS).Scan(&quota.Usage.RequestsUsed); err != nil {
			return nil, err
		}
		if err := queryer.QueryRowContext(ctx, `select coalesce(sum(total_tokens), 0), coalesce(sum(cost_micros), 0) from api_key_quota_token_events where policy_id = ? and epoch = ? and occurred_at_ms >= ? and (? = 0 or occurred_at_ms < ?)`, policyID, quota.Epoch, startMS, queryEndMS, queryEndMS).Scan(&quota.Usage.TotalTokensUsed, &costUsedMicros); err != nil {
			return nil, err
		}
	}
	quota.Usage.CostUsed = microsToUSD(costUsedMicros)
	quota.Usage = quotaUsage(quota)
	return &quota, nil
}

func quotaUsage(quota Quota) QuotaUsage {
	usage := quota.Usage
	usage.Exhausted = make([]string, 0, 3)
	if quota.Requests != nil {
		remaining := *quota.Requests - usage.RequestsUsed
		if remaining < 0 {
			remaining = 0
		}
		usage.RequestsRemaining = &remaining
		if usage.RequestsUsed >= *quota.Requests {
			usage.Exhausted = append(usage.Exhausted, "requests")
		}
	}
	if quota.TotalTokens != nil {
		remaining := *quota.TotalTokens - usage.TotalTokensUsed
		if remaining < 0 {
			remaining = 0
		}
		usage.TokensRemaining = &remaining
		if usage.TotalTokensUsed >= *quota.TotalTokens {
			usage.Exhausted = append(usage.Exhausted, "total_tokens")
		}
	}
	if quota.Cost != nil {
		remaining := *quota.Cost - usage.CostUsed
		if remaining < 0 {
			remaining = 0
		}
		usage.CostRemaining = &remaining
		if usage.CostUsed >= *quota.Cost {
			usage.Exhausted = append(usage.Exhausted, "cost")
		}
	}
	return usage
}

func listProfiles(ctx context.Context, queryer sqlQueryer, policyID string) ([]Profile, error) {
	rows, err := queryer.QueryContext(ctx, `select id, policy_id, name, created_at_ms, updated_at_ms from api_key_profiles where policy_id = ? order by created_at_ms, id`, policyID)
	if err != nil {
		return nil, err
	}
	profiles := make([]Profile, 0)
	for rows.Next() {
		var profile Profile
		if err := rows.Scan(&profile.ID, &profile.PolicyID, &profile.Name, &profile.CreatedAtMS, &profile.UpdatedAtMS); err != nil {
			_ = rows.Close()
			return nil, err
		}
		profiles = append(profiles, profile)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range profiles {
		profile := &profiles[i]
		profile.Mappings = make([]ModelMapping, 0)
		profile.Providers, err = listStrings(ctx, queryer, `select provider_id from api_key_profile_providers where profile_id = ? order by provider_id`, profile.ID)
		if err != nil {
			return nil, err
		}
		profile.Models, err = listStrings(ctx, queryer, `select model_id from api_key_profile_models where profile_id = ? order by model_id`, profile.ID)
		if err != nil {
			return nil, err
		}
		mappingRows, err := queryer.QueryContext(ctx, `select source_model, target_model from api_key_profile_model_mappings where profile_id = ? order by source_model`, profile.ID)
		if err != nil {
			return nil, err
		}
		for mappingRows.Next() {
			var mapping ModelMapping
			if err := mappingRows.Scan(&mapping.Source, &mapping.Target); err != nil {
				_ = mappingRows.Close()
				return nil, err
			}
			profile.Mappings = append(profile.Mappings, mapping)
		}
		if err := mappingRows.Close(); err != nil {
			return nil, err
		}
	}
	return profiles, nil
}

func listStrings(ctx context.Context, queryer sqlQueryer, query, id string) ([]string, error) {
	rows, err := queryer.QueryContext(ctx, query, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

func insertProfile(ctx context.Context, tx *sql.Tx, policyID, profileID string, input ProfileInput, now int64) error {
	if _, err := tx.ExecContext(ctx, `insert into api_key_profiles(id, policy_id, name, created_at_ms, updated_at_ms) values(?, ?, ?, ?, ?)`, profileID, policyID, input.Name, now, now); err != nil {
		return err
	}
	return replaceProfileRules(ctx, tx, profileID, input)
}

func replaceProfileRules(ctx context.Context, tx *sql.Tx, profileID string, input ProfileInput) error {
	for _, table := range []string{"api_key_profile_providers", "api_key_profile_models", "api_key_profile_model_mappings"} {
		if _, err := tx.ExecContext(ctx, `delete from `+table+` where profile_id = ?`, profileID); err != nil {
			return err
		}
	}
	for _, provider := range input.Providers {
		if _, err := tx.ExecContext(ctx, `insert into api_key_profile_providers(profile_id, provider_id) values(?, ?)`, profileID, provider); err != nil {
			return err
		}
	}
	for _, model := range input.Models {
		if _, err := tx.ExecContext(ctx, `insert into api_key_profile_models(profile_id, model_id) values(?, ?)`, profileID, model); err != nil {
			return err
		}
	}
	for _, mapping := range input.Mappings {
		if _, err := tx.ExecContext(ctx, `insert into api_key_profile_model_mappings(profile_id, source_model, target_model) values(?, ?, ?)`, profileID, mapping.Source, mapping.Target); err != nil {
			return err
		}
	}
	return nil
}

func insertAudit(ctx context.Context, tx *sql.Tx, policyID, event string, details any, now int64) error {
	raw, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `insert into api_key_policy_audit(policy_id, event_type, details_json, created_at_ms) values(?, ?, ?, ?)`, policyID, event, string(raw), now)
	return err
}

func requireVersion(ctx context.Context, tx *sql.Tx, policyID string, version int64) error {
	var current int64
	if err := tx.QueryRowContext(ctx, `select version from api_key_policies where id = ?`, policyID).Scan(&current); errors.Is(err, sql.ErrNoRows) {
		return ErrPolicyNotFound
	} else if err != nil {
		return err
	}
	if current != version {
		return ErrVersionConflict
	}
	return nil
}

func sqliteConstraint(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("api key policy constraint: %w", err)
}
