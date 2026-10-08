package management

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/embeddedusage"
	proinspection "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/inspection"
	prorouting "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/routing"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

// Written before the fix: real HTTP transport, decision and persisted SQLite payload.
func TestOtherProviderQuotaParityHTTPAndSQLite(t *testing.T) {
	type testCase struct {
		name, provider, fileName, domain, baseURL, host, body, rowID, plan, scope string
		blocked                                                                   []string
		used                                                                      float64
		rows                                                                      map[string]map[string]any
	}
	tests := []testCase{
		{name: "kimi-ai-monthly", provider: "kimi", domain: "ai", host: "api.kimi.ai", body: `{"usage":{"used":20,"limit":100},"usages":{"limit_month_total":{"used_ratio":0.95,"reset_time":"2099-11-01T00:00:00Z"}}}`, rowID: "monthly", used: 95},
		{name: "kimi-ai-base-url", provider: "kimi", baseURL: "https://api.kimi.ai/coding", host: "api.kimi.ai", body: `{"usage":{"used":20,"limit":100}}`, rowID: "summary", used: 20},
		{name: "kimi-com-explicit", provider: "kimi", fileName: "kimi-ai-explicit.json", domain: "com", host: "api.kimi.com", body: `{"usage":{"used":20,"limit":100}}`, rowID: "summary", used: 20},
		{name: "kimi-arbitrary-base-url", provider: "kimi", baseURL: "https://example.invalid/steal", host: "api.kimi.com", body: `{"usage":{"used":20,"limit":100}}`, rowID: "summary", used: 20},
		{name: "kimi-monthly-only", provider: "kimi", host: "api.kimi.com", body: `{"usages":{"limit_month_total":{"used_ratio":0.95,"reset_time":"2099-11-01T00:00:00Z"}}}`, rowID: "monthly", used: 95},
		{name: "claude-fable-team", provider: "claude", host: "api.anthropic.com", body: `{"limits":[{"kind":"weekly_scoped","percent":95,"is_active":true,"scope":{"model":{"display_name":"Fable"}},"resets_at":"2099-11-01T00:00:00Z"}],"iguana_necktie":{"utilization":12,"resets_at":"2099-01-01T00:00:00Z"}}`, rowID: "seven-day-fable", used: 95, plan: "plan_team", scope: "claude-fable-*", blocked: []string{"claude-fable-5", "claude-fable-5-1"}},
		{name: "claude-fable-only", provider: "claude", host: "api.anthropic.com", body: `{"five_hour":{"utilization":10},"seven_day":{"utilization":20},"limits":[{"kind":"weekly_scoped","percent":95,"is_active":true,"scope":{"model":{"display_name":"Fable 5"}},"resets_at":"2099-11-01T00:00:00Z"}]}`, rowID: "seven-day-fable", used: 95, plan: "plan_team", scope: "claude-fable-*", blocked: []string{"claude-fable-5", "claude-fable-5-1"}},
		{name: "claude-fable-legacy", provider: "claude", host: "api.anthropic.com", body: `{"five_hour":{"utilization":10},"seven_day":{"utilization":20},"iguana_necktie":{"utilization":95,"resets_at":"2099-11-01T00:00:00Z"}}`, rowID: "seven-day-fable", used: 95, plan: "plan_team", scope: "claude-fable-*", blocked: []string{"claude-fable-5", "claude-fable-5-1"}},
		{name: "claude-fable-opus", provider: "claude", host: "api.anthropic.com", body: `{"five_hour":{"utilization":10},"seven_day":{"utilization":20},"limits":[{"kind":"weekly_scoped","percent":95,"is_active":true,"scope":{"model":{"display_name":"Fable 5"}},"resets_at":"2099-11-01T00:00:00Z"}],"seven_day_opus":{"utilization":95,"resets_at":"2099-11-01T00:00:00Z"}}`, rowID: "seven-day-fable", used: 95, plan: "plan_team", scope: "claude-opus-*,claude-fable-*", blocked: []string{"claude-fable-5", "claude-fable-5-1", "claude-opus-4-6"}},
		{name: "claude-fable-five-hour", provider: "claude", host: "api.anthropic.com", body: `{"five_hour":{"utilization":95},"seven_day":{"utilization":20},"limits":[{"kind":"weekly_scoped","percent":95,"is_active":true,"scope":{"model":{"display_name":"Fable 5"}},"resets_at":"2099-11-01T00:00:00Z"}]}`, rowID: "seven-day-fable", used: 95, plan: "plan_team", scope: "", blocked: []string{"claude-fable-5", "claude-fable-5-1", "claude-opus-4-6", "claude-sonnet-4-6"}},
		{name: "claude-fable-weekly", provider: "claude", host: "api.anthropic.com", body: `{"five_hour":{"utilization":10},"seven_day":{"utilization":95},"limits":[{"kind":"weekly_scoped","percent":95,"is_active":true,"scope":{"model":{"display_name":"Fable 5"}},"resets_at":"2099-11-01T00:00:00Z"}]}`, rowID: "seven-day-fable", used: 95, plan: "plan_team", scope: "", blocked: []string{"claude-fable-5", "claude-fable-5-1", "claude-opus-4-6", "claude-sonnet-4-6"}},
		{name: "claude-fable-below", provider: "claude", host: "api.anthropic.com", body: `{"five_hour":{"utilization":10},"seven_day":{"utilization":20},"limits":[{"kind":"weekly_scoped","percent":20,"is_active":true,"scope":{"model":{"display_name":"Fable 5"}},"resets_at":"2099-11-01T00:00:00Z"}]}`, rowID: "seven-day-fable", used: 20, plan: "plan_team", scope: "", blocked: []string{}},
	}
	allClaudeModels := []string{"claude-fable-5", "claude-fable-5-1", "claude-opus-4-6", "claude-sonnet-4-6"}
	for _, field := range []string{"limit_dollars", "used_dollars", "remaining_dollars"} {
		for _, value := range []string{`0`, `"10"`} {
			tests = append(tests, testCase{
				name:     "claude-credits-" + field + "-" + map[bool]string{true: "string", false: "zero"}[value == `"10"`],
				provider: "claude", host: "api.anthropic.com", plan: "plan_team", used: 95,
				body:  `{"iguana_necktie":{"utilization":95,"` + field + `":` + value + `,"resets_at":"2099-11-01T00:00:00Z"}}`,
				rowID: "cloud-session-credits", blocked: allClaudeModels,
				rows: map[string]map[string]any{"cloud-session-credits": {"periodHours": nil, "labelKey": "claude_quota.cloud_session_credits", "usedPercent": float64(95)}},
			})
		}
	}
	for _, creditsUsed := range []int{20, 95} {
		scope := "claude-fable-*"
		blocked := []string{"claude-fable-5", "claude-fable-5-1"}
		if creditsUsed >= 90 {
			scope, blocked = "", allClaudeModels
		}
		tests = append(tests, testCase{
			name: fmt.Sprintf("claude-credits-with-fable-%d", creditsUsed), provider: "claude", host: "api.anthropic.com", plan: "plan_team", used: 95,
			body:  fmt.Sprintf(`{"iguana_necktie":{"utilization":%d,"limit_dollars":10,"resets_at":"2099-11-01T00:00:00Z"},"limits":[{"kind":"weekly_scoped","percent":95,"is_active":true,"scope":{"model":{"display_name":"Fable 5"}},"resets_at":"2099-11-01T00:00:00Z"}]}`, creditsUsed),
			rowID: "cloud-session-credits", scope: scope, blocked: blocked,
			rows: map[string]map[string]any{
				"cloud-session-credits": {"periodHours": nil, "labelKey": "claude_quota.cloud_session_credits", "usedPercent": float64(creditsUsed)},
				"seven-day-fable":       {"periodHours": float64(168), "usedPercent": float64(95)},
			},
		})
	}
	for i, value := range []string{`null`, `""`, `"invalid"`, `"Infinity"`, `"NaN"`, `true`} {
		tests = append(tests, testCase{
			name: fmt.Sprintf("claude-invalid-dollar-%d", i), provider: "claude", host: "api.anthropic.com", plan: "plan_team", used: 95,
			body:  `{"iguana_necktie":{"utilization":95,"limit_dollars":` + value + `,"resets_at":"2099-11-01T00:00:00Z"}}`,
			rowID: "seven-day-fable", scope: "claude-fable-*", blocked: []string{"claude-fable-5", "claude-fable-5-1"},
			rows: map[string]map[string]any{"seven-day-fable": {"periodHours": float64(168)}},
		})
	}
	for _, unit := range []struct {
		name, token string
		hours       float64
	}{
		{"SECOND", "300s", float64(300) / 3600},
		{"MINUTE", "5h", 5},
		{"HOUR", "300h", 300},
		{"DAY", "300d", 7200},
		{"WEEK", "300w", 50400},
	} {
		for _, rawUnit := range []string{unit.name, unit.name + "S", "TIME_UNIT_" + unit.name, " time_unit_" + strings.ToLower(unit.name) + "s "} {
			body, err := json.Marshal(map[string]any{"limits": []any{map[string]any{"window": map[string]any{"duration": 300, "timeUnit": rawUnit}, "detail": map[string]any{"used": 40, "limit": 100, "reset_at": "2099-11-01T00:00:00Z"}}}})
			if err != nil {
				t.Fatal(err)
			}
			tests = append(tests, testCase{
				name: "kimi-unit-" + strings.TrimSpace(rawUnit), provider: "kimi", host: "api.kimi.com", body: string(body), rowID: "limit-0", used: 40,
				rows: map[string]map[string]any{"limit-0": {"periodHours": unit.hours, "labelParams": map[string]any{"duration": unit.token}, "used": float64(40), "limit": float64(100), "resetAtMs": float64(4097174400000)}},
			})
		}
	}
	for i, rawUnit := range []string{`null`, `""`, `"UNKNOWN"`, `"TIME_UNIT_UNKNOWN"`, `1`} {
		tests = append(tests, testCase{
			name: fmt.Sprintf("kimi-unit-fallback-%d", i), provider: "kimi", host: "api.kimi.com", rowID: "limit-0", used: 40,
			body: `{"limits":[{"window":{"duration":300,"timeUnit":` + rawUnit + `},"detail":{"used":40,"limit":100}}]}`,
			rows: map[string]map[string]any{"limit-0": {"periodHours": float64(5), "labelParams": map[string]any{"duration": "5h"}}},
		})
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := startProQuotaTestService(t)
			manager := coreauth.NewManager(nil, nil, nil)
			fileName := tc.fileName
			if fileName == "" {
				fileName = tc.name + ".json"
			}
			auth, err := manager.Register(ctx, &coreauth.Auth{ID: tc.name, FileName: fileName, Provider: tc.provider, Attributes: map[string]string{"domain": tc.domain, "base_url": tc.baseURL}, Metadata: map[string]any{"access_token": "fixture-token"}})
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Host != tc.host {
					t.Errorf("host=%s want %s", r.Host, tc.host)
				}
				if r.Header.Get("Authorization") != "Bearer fixture-token" {
					t.Error("missing fixture authorization")
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/coding/v1/usages", "/api/oauth/usage":
					fmt.Fprint(w, tc.body)
				case "/api/oauth/profile":
					fmt.Fprint(w, `{"organization":{"organization_type":"claude_team","subscription_status":"active"},"account":{"has_claude_max":true}}`)
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			transport := server.Client().Transport.(*http.Transport).Clone()
			transport.TLSClientConfig.ServerName = "example.com"
			transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}
			previous := http.DefaultTransport
			http.DefaultTransport = transport
			defer func() { http.DefaultTransport = previous; transport.CloseIdleConnections() }()
			scheduler := &accountInspectionScheduler{h: &Handler{authManager: manager}}
			account := accountFromAuth(auth)
			settings := proinspection.DefaultSettings()
			settings.Retries = 0
			settings.Timeout = 1000
			settings.UsedPercentThreshold = 90
			var decision accountInspectionDecision
			if tc.provider == "kimi" {
				decision, _, err = scheduler.inspectKimi(ctx, account, settings)
			} else {
				decision, _, err = scheduler.inspectClaude(ctx, account, settings)
			}
			if err != nil {
				t.Fatal(err)
			}
			if decision.UsedPercent == nil || *decision.UsedPercent != tc.used || decision.IsQuota != (tc.used >= 90) {
				t.Errorf("decision=%+v want used=%v", decision, tc.used)
			}
			var scopeEvidence map[string]any
			if tc.provider == "claude" {
				if decision.QuotaModel != tc.scope {
					t.Errorf("scope=%q want %q", decision.QuotaModel, tc.scope)
				}
				blocks := map[string]bool{}
				if decision.IsQuota {
					result := account.baseResult()
					result.QuotaModel = decision.QuotaModel
					result.QuotaResetAt = decision.QuotaResetAt
					hold := newInspectionQuotaHold(auth, &result, settings)
					if hold == nil {
						t.Fatal("missing protection")
					}
					for _, model := range []string{"claude-fable-5", "claude-fable-5-1", "claude-opus-4-6", "claude-sonnet-4-6"} {
						blocks[model] = prorouting.ProtectionBlocks(*hold, model, time.Now())
						if blocks[model] != slices.Contains(tc.blocked, model) {
							t.Errorf("model=%s blocked=%v want %v", model, blocks[model], slices.Contains(tc.blocked, model))
						}
					}
				}
				scopeEvidence = map[string]any{"scope": decision.QuotaModel, "isQuota": decision.IsQuota, "blocks": blocks}
			}
			entries, err := embeddedusage.GetQuotaCache(ctx, tc.provider, fileName)
			if err != nil || len(entries) != 1 {
				t.Fatalf("cache entries=%v err=%v", entries, err)
			}
			if dir := os.Getenv("OTHER_PROVIDER_QUOTA_EVIDENCE_DIR"); dir != "" {
				if err := os.MkdirAll(dir, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, tc.name+"-request.json"), []byte(tc.body), 0600); err != nil {
					t.Fatal(err)
				}
				if scopeEvidence != nil {
					raw, err := json.MarshalIndent(scopeEvidence, "", "  ")
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(dir, tc.name+"-routing.json"), raw, 0600); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(filepath.Join(dir, tc.name+".json"), entries[0].Data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			var state map[string]any
			if err := json.Unmarshal(entries[0].Data, &state); err != nil {
				t.Fatal(err)
			}
			key := "rows"
			if tc.provider == "claude" {
				key = "windows"
				if state["planType"] != tc.plan {
					t.Errorf("plan=%v want %s", state["planType"], tc.plan)
				}
			}
			found := false
			if tc.rows != nil && len(state[key].([]any)) != len(tc.rows) {
				t.Errorf("row count=%d want %d", len(state[key].([]any)), len(tc.rows))
			}
			for _, raw := range state[key].([]any) {
				row := raw.(map[string]any)
				if tc.rows != nil {
					id, _ := row["id"].(string)
					expected, ok := tc.rows[id]
					if !ok {
						t.Errorf("unexpected row %s", id)
					}
					for field, want := range expected {
						if actual, exists := row[field]; !exists || !reflect.DeepEqual(actual, want) {
							t.Errorf("row=%s field=%s got=%v want=%v", id, field, actual, want)
						}
					}
				}
				if row["id"] == tc.rowID {
					found = true
					if tc.used == 95 && row["resetAtMs"] != float64(4097174400000) {
						t.Errorf("reset=%v", row["resetAtMs"])
					}
				}
				if tc.provider == "claude" && row["id"] == "iguana-necktie" {
					t.Error("legacy duplicated Fable")
				}
			}
			if !found {
				t.Errorf("missing %s: %s", tc.rowID, entries[0].Data)
			}
		})
	}
}
