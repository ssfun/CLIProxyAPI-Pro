package inspection

import (
	"strconv"
	"testing"
	"time"
)

func TestAntigravityParserBuildsCanonicalGroups(t *testing.T) {
	groups, err := BuildAntigravityGroups(`{
		"groups":[{"displayName":"Claude and GPT models","description":"premium models","buckets":[
			{"bucketId":"weekly","window":"weekly","remainingFraction":0.75,"resetTime":"2026-01-08T00:00:00Z"},
			{"bucketId":"five-hour","window":"5h","remainingFraction":0.25,"resetTime":"2026-01-01T05:00:00Z"}
		]}]
	}`)
	if err != nil || len(groups) != 1 || groups[0]["id"] != "claude-gpt" {
		t.Fatalf("groups/error = %+v / %v", groups, err)
	}
	used := AntigravityUsedPercent(groups, AntigravityQuotaModeClaudeGPT)
	if used == nil || *used != 75 {
		t.Fatalf("used percent = %v", used)
	}
	buckets := groups[0]["buckets"].([]map[string]any)
	if buckets[0]["resetAtMs"] != int64(1767830400000) || buckets[0]["periodHours"] != float64(168) {
		t.Fatalf("weekly timeline fields = %+v", buckets[0])
	}
	if buckets[1]["resetAtMs"] != int64(1767243600000) || buckets[1]["periodHours"] != float64(5) {
		t.Fatalf("five-hour timeline fields = %+v", buckets[1])
	}
}

func TestClaudeAndCodexWindowParsers(t *testing.T) {
	claude, extra, err := BuildClaudeWindows(`{
		"five_hour":{"utilization":25,"resets_at":"2026-01-01T00:00:00Z"},
		"seven_day":{"utilization":50,"resets_at":"2026-01-07T00:00:00Z"},
		"extra_usage":{"is_enabled":true}
	}`)
	if err != nil || len(claude) != 2 || extra == nil {
		t.Fatalf("claude windows/extra/error = %+v / %+v / %v", claude, extra, err)
	}
	if claude[0]["resetAtMs"] != int64(1767225600000) || claude[0]["periodHours"] != float64(5) {
		t.Fatalf("claude five-hour timeline fields = %+v", claude[0])
	}
	if claude[1]["resetAtMs"] != int64(1767744000000) || claude[1]["periodHours"] != float64(168) {
		t.Fatalf("claude weekly timeline fields = %+v", claude[1])
	}

	_, codex, used := BuildCodexWindows(`{
		"rate_limit":{"primary_window":{"limit_window_seconds":18000,"used_percent":12,"reset_at":1767225600},"secondary_window":{"limit_window_seconds":604800,"used_percent":42,"reset_at":1767744000}},
		"code_review_rate_limit":{"primary_window":{"limit_window_seconds":18000,"used_percent":5,"reset_at":1767243600}}
	}`)
	if len(codex) != 3 || used == nil || *used != 42 {
		t.Fatalf("codex windows/used = %+v / %v", codex, used)
	}
	if codex[0]["resetAtMs"] != int64(1767225600000) || codex[0]["periodHours"] != float64(5) {
		t.Fatalf("codex five-hour timeline fields = %+v", codex[0])
	}
	if codex[1]["resetAtMs"] != int64(1767744000000) || codex[1]["periodHours"] != float64(168) {
		t.Fatalf("codex weekly timeline fields = %+v", codex[1])
	}
}

func TestCodexSingleLongWindowIsNotDuplicatedAsFiveHour(t *testing.T) {
	tests := []struct {
		name       string
		windowSecs int
		wantID     string
	}{
		{name: "weekly", windowSecs: 604800, wantID: "weekly"},
		{name: "monthly", windowSecs: 2592000, wantID: "monthly"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := `{"rate_limit":{"primary_window":{"limit_window_seconds":` +
				strconv.Itoa(tt.windowSecs) + `,"used_percent":25,"reset_at":1767830400}}}`
			_, windows, used := BuildCodexWindows(body)
			if len(windows) != 1 || windows[0]["id"] != tt.wantID {
				t.Fatalf("windows = %+v, want only %q", windows, tt.wantID)
			}
			if used == nil || *used != 25 {
				t.Fatalf("used percent = %v, want 25", used)
			}
		})
	}
}

func TestCodexLegacyUndatedWindowsKeepDistinctOrderFallback(t *testing.T) {
	_, windows, _ := BuildCodexWindows(`{
		"rate_limit":{
			"primary_window":{"used_percent":10,"reset_at":1767243600},
			"secondary_window":{"used_percent":20,"reset_at":1767830400}
		}
	}`)
	if len(windows) != 2 || windows[0]["id"] != "five-hour" || windows[1]["id"] != "weekly" {
		t.Fatalf("legacy windows = %+v, want distinct primary/secondary fallback", windows)
	}
}

func TestCodexAdditionalSingleMonthlyWindowUsesMonthlyLabel(t *testing.T) {
	_, windows, _ := BuildCodexWindows(`{
		"additional_rate_limits":[{
			"limit_name":"Credits",
			"rate_limit":{"primary_window":{"limit_window_seconds":2592000,"used_percent":30,"reset_at":1767830400}}
		}]
	}`)
	if len(windows) != 1 || windows[0]["id"] != "credits-monthly-0" {
		t.Fatalf("additional windows = %+v, want only monthly", windows)
	}
}

func TestKimiParserNormalizesLimits(t *testing.T) {
	rows, used, err := BuildKimiRows(`{
		"limits":[{"name":"Weekly","limit":100,"used":40,"reset_at":"2026-01-08T00:00:00Z","window":{"duration":1,"timeUnit":"WEEKS"}}]
	}`)
	if err != nil || len(rows) != 1 || rows[0]["limit"] != 100 || rows[0]["used"] != 40 {
		t.Fatalf("rows/error = %+v / %v", rows, err)
	}
	if used == nil || *used != 40 {
		t.Fatalf("used percent = %v", used)
	}
	if rows[0]["resetAtMs"] != int64(1767830400000) || rows[0]["periodHours"] != float64(168) {
		t.Fatalf("kimi timeline fields = %+v", rows[0])
	}
}

func TestKimiMonthlyUsageParticipatesInQuotaRecovery(t *testing.T) {
	rows, used, err := BuildKimiRows(`{
		"usage":{"used":20,"limit":100},
		"usages":{"limit_month_total":{"used_ratio":0.95,"reset_time":"2026-11-01T00:00:00Z"}}
	}`)
	if err != nil || len(rows) != 2 || used == nil || *used != 95 {
		t.Fatalf("rows/used/error = %+v / %v / %v", rows, used, err)
	}
	monthly := rows[1]
	if monthly["id"] != "monthly" || monthly["labelKey"] != "kimi_quota.monthly_limit" || monthly["used"] != 95 || monthly["limit"] != 100 || monthly["resetAtMs"] != int64(1793491200000) || monthly["periodHours"] != float64(720) {
		t.Fatalf("monthly row = %+v", monthly)
	}
	decision := WithQuotaWindows(Decision{UsedPercent: used, IsQuota: true}, rows, 95)
	if decision.QuotaResetAt != int64(1793491200000) || !decision.QuotaKnown {
		t.Fatalf("monthly quota recovery = %+v", decision)
	}
	if scope := QuotaModelScope("kimi", rows, 95); scope != "" {
		t.Fatalf("monthly quota must stay credential-wide, got %q", scope)
	}
}

func TestKimiMonthlyRatioNormalization(t *testing.T) {
	tests := []struct {
		name  string
		value string
		count int
		used  int
	}{
		{name: "numeric-string-rounds", value: `"0.955"`, count: 1, used: 96},
		{name: "zero", value: `0`, count: 1},
		{name: "null-is-zero", value: `null`, count: 1},
		{name: "over-limit", value: `1.5`, count: 1, used: 150},
		{name: "negative-half-rounds-up", value: `-0.005`, count: 1},
		{name: "invalid", value: `"invalid"`},
		{name: "non-finite", value: `"Infinity"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows, used, err := BuildKimiRows(`{"usages":{"limit_month_total":{"used_ratio":` + tt.value + `}}}`)
			if err != nil || len(rows) != tt.count {
				t.Fatalf("rows/error = %+v / %v", rows, err)
			}
			if tt.count == 0 {
				if used != nil {
					t.Fatalf("invalid monthly usage = %v", used)
				}
				return
			}
			if rows[0]["used"] != tt.used || rows[0]["resetAtMs"] != nil || rows[0]["periodHours"] != float64(720) {
				t.Fatalf("monthly row = %+v", rows[0])
			}
			want := float64(tt.used)
			if want > 100 {
				want = 100
			}
			if used == nil || *used != want {
				t.Fatalf("used = %v, want %v", used, want)
			}
		})
	}
	rows, used, err := BuildKimiRows(`{"usages":{"limit_month_total":{"reset_time":"2026-11-01T00:00:00Z"}}}`)
	if err != nil || len(rows) != 0 || used != nil {
		t.Fatalf("missing ratio fabricated row: %+v / %v / %v", rows, used, err)
	}
}

func TestClaudeFableLimitsPreferActiveAndReplaceLegacyAlias(t *testing.T) {
	windows, _, err := BuildClaudeWindows(`{
		"five_hour":{"utilization":20},
		"iguana_necktie":{"utilization":12},
		"limits":[
			{"kind":"weekly_scoped","percent":70,"scope":{"model":{"display_name":"Fable"}}},
			{"kind":" WEEKLY_SCOPED ","percent":"95","is_active":true,"scope":{"model":{"display_name":" FABLE 5 "}},"resets_at":"2026-11-01T00:00:00Z"},
			{"kind":"weekly_scoped","percent":99,"is_active":true,"scope":{"model":{"display_name":"Fable"}}}
		]
	}`)
	if err != nil || len(windows) != 2 {
		t.Fatalf("windows/error = %+v / %v", windows, err)
	}
	fable := windows[1]
	if fable["id"] != "seven-day-fable" || fable["labelKey"] != "claude_quota.seven_day_fable" || fable["usedPercent"] != float64(95) || fable["resetAtMs"] != int64(1793491200000) || fable["periodHours"] != float64(168) {
		t.Fatalf("active fable row = %+v", fable)
	}
	used := MaxUsedPercentFromWindows(windows)
	if used == nil || *used != 95 || WithQuotaWindows(Decision{UsedPercent: used}, windows, 95).QuotaResetAt != int64(1793491200000) {
		t.Fatalf("fable did not participate in quota decision: %+v / %v", windows, used)
	}
}

func TestClaudeFableCandidateValidationAndLegacyFallback(t *testing.T) {
	tests := []struct {
		name   string
		limits string
		used   float64
	}{
		{name: "legacy-only", limits: `[]`, used: 12},
		{name: "wrong-kind", limits: `[{"kind":"daily_scoped","percent":99,"scope":{"model":{"display_name":"Fable"}}}]`, used: 12},
		{name: "wrong-model", limits: `[{"kind":"weekly_scoped","percent":99,"scope":{"model":{"display_name":"Fable 50"}}}]`, used: 12},
		{name: "invalid-percent", limits: `[{"kind":"weekly_scoped","percent":"Infinity","scope":{"model":{"display_name":"Fable"}}}]`, used: 12},
		{name: "first-valid-without-active", limits: `[{"kind":"weekly_scoped","percent":35,"scope":{"model":{"display_name":"Fable 5"}}},{"kind":"weekly_scoped","percent":99,"is_active":"true","scope":{"model":{"display_name":"Fable"}}}]`, used: 35},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			windows, _, err := BuildClaudeWindows(`{"iguana_necktie":{"utilization":12},"limits":` + tt.limits + `}`)
			if err != nil || len(windows) != 1 || windows[0]["id"] != "seven-day-fable" || windows[0]["labelKey"] != "claude_quota.seven_day_fable" || windows[0]["usedPercent"] != tt.used || windows[0]["resetAtMs"] != nil {
				t.Fatalf("windows/error = %+v / %v", windows, err)
			}
		})
	}
}

func TestResolveClaudePlanPrioritizesActiveTeam(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "active-team-over-max", body: `{"organization":{"organization_type":" CLAUDE_TEAM ","subscription_status":" ACTIVE "},"account":{"has_claude_max":true,"has_claude_pro":true}}`, want: "plan_team"},
		{name: "active-team-over-pro", body: `{"organization":{"organization_type":"claude_team","subscription_status":"active"},"account":{"has_claude_pro":"yes"}}`, want: "plan_team"},
		{name: "active-team-over-free", body: `{"organization":{"organization_type":"claude_team","subscription_status":"active"},"account":{"has_claude_max":false,"has_claude_pro":false}}`, want: "plan_team"},
		{name: "inactive-team-falls-back", body: `{"organization":{"organization_type":"claude_team","subscription_status":"inactive"},"account":{"has_claude_max":"on"}}`, want: "plan_max"},
		{name: "free", body: `{"account":{"has_claude_max":"off","has_claude_pro":0}}`, want: "plan_free"},
		{name: "unknown-is-not-free", body: `{"account":{"has_claude_max":false}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveClaudePlan(tt.body); got != tt.want {
				t.Fatalf("plan = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestQuotaRecoveryUsesOnlyBlockingWindows(t *testing.T) {
	now := time.Now()
	short := now.Add(time.Hour).UnixMilli()
	long := now.Add(7 * 24 * time.Hour).UnixMilli()
	used := 100.0
	windows := []map[string]any{{"usedPercent": 100.0, "resetAtMs": float64(short)}, {"usedPercent": 20.0, "resetAtMs": float64(long)}}
	decision := WithQuotaWindows(Decision{UsedPercent: &used, IsQuota: true}, windows, 95)
	if decision.QuotaResetAt != short {
		t.Fatalf("healthy weekly window extended cooldown: %d", decision.QuotaResetAt)
	}
	windows[1]["usedPercent"] = 100.0
	if got := WithQuotaWindows(Decision{}, windows, 95); got.QuotaResetAt != long {
		t.Fatal("did not wait for both exhausted windows")
	}
	delete(windows[1], "resetAtMs")
	if got := WithQuotaWindows(Decision{}, windows, 95); got.QuotaResetAt != 0 {
		t.Fatal("unknown reset treated as certain")
	}
	windows = []map[string]any{{"id": "seven-day-opus", "usedPercent": 100.0}, {"id": "five-hour", "usedPercent": 5.0}}
	if got := ClaudeQuotaModel(windows, 95); got != "claude-opus-*" {
		t.Fatalf("scope=%s", got)
	}
	windows[1]["usedPercent"] = 100.0
	if got := ClaudeQuotaModel(windows, 95); got != "" {
		t.Fatal("credential limit incorrectly scoped to one model")
	}
}

func TestQuotaModelScopeUsesProviderWindows(t *testing.T) {
	if got := QuotaModelScope("codex", []map[string]any{
		{"id": "five-hour", "usedPercent": 100.0},
		{"id": "gpt-5-codex-weekly-0", "labelParams": map[string]any{"name": "gpt-5-codex"}, "usedPercent": 100.0},
	}, 95); got != "" {
		t.Fatalf("shared five-hour plus model window must stay credential-wide, got %q", got)
	}
	if got := QuotaModelScope("codex", []map[string]any{
		{"id": "gpt-5-codex-weekly-0", "labelParams": map[string]any{"name": "gpt-5-codex"}, "usedPercent": 100.0},
	}, 95); got != "gpt-5-codex*" {
		t.Fatalf("codex additional window scope = %q", got)
	}
	if got := QuotaModelScope("kimi", []map[string]any{
		{"label": "kimi-k2.5", "used": 100, "limit": 100},
	}, 95); got != "kimi-k2.5*" {
		t.Fatalf("kimi model scope = %q", got)
	}
	if got := QuotaModelScope("kimi", []map[string]any{
		{"label": "Weekly", "used": 100, "limit": 100},
	}, 95); got != "" {
		t.Fatalf("kimi period window must stay credential-wide, got %q", got)
	}
	groups := []map[string]any{
		{"id": "claude-gpt", "buckets": []map[string]any{{"remainingFraction": 0.0}}},
		{"id": "gemini", "buckets": []map[string]any{{"remainingFraction": 0.8}}},
	}
	if got := AntigravityQuotaModel(groups, AntigravityQuotaModeClaudeGPT, 95); got != "claude-*,gpt-*" {
		t.Fatalf("antigravity claude-gpt scope = %q", got)
	}
	groups[1]["buckets"] = []map[string]any{{"remainingFraction": 0.0}}
	if got := AntigravityQuotaModel(groups, AntigravityQuotaModeMaxUsed, 95); got != "claude-*,gpt-*,gemini-*" {
		t.Fatalf("antigravity all groups scope = %q", got)
	}
}
