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
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/embeddedusage"
	proinspection "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/inspection"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

// Written before the fix: real HTTP transport, decision and persisted SQLite payload.
func TestOtherProviderQuotaParityHTTPAndSQLite(t *testing.T) {
	for _, tc := range []struct {
		name, provider, fileName, domain, baseURL, host, body, rowID, plan string
		used                                                               float64
	}{
		{name: "kimi-ai-monthly", provider: "kimi", domain: "ai", host: "api.kimi.ai", body: `{"usage":{"used":20,"limit":100},"usages":{"limit_month_total":{"used_ratio":0.95,"reset_time":"2099-11-01T00:00:00Z"}}}`, rowID: "monthly", used: 95},
		{name: "kimi-ai-base-url", provider: "kimi", baseURL: "https://api.kimi.ai/coding", host: "api.kimi.ai", body: `{"usage":{"used":20,"limit":100}}`, rowID: "summary", used: 20},
		{name: "kimi-com-explicit", provider: "kimi", fileName: "kimi-ai-explicit.json", domain: "com", host: "api.kimi.com", body: `{"usage":{"used":20,"limit":100}}`, rowID: "summary", used: 20},
		{name: "kimi-arbitrary-base-url", provider: "kimi", baseURL: "https://example.invalid/steal", host: "api.kimi.com", body: `{"usage":{"used":20,"limit":100}}`, rowID: "summary", used: 20},
		{name: "kimi-monthly-only", provider: "kimi", host: "api.kimi.com", body: `{"usages":{"limit_month_total":{"used_ratio":0.95,"reset_time":"2099-11-01T00:00:00Z"}}}`, rowID: "monthly", used: 95},
		{name: "claude-fable-team", provider: "claude", host: "api.anthropic.com", body: `{"limits":[{"kind":"weekly_scoped","percent":95,"is_active":true,"scope":{"model":{"display_name":"Fable"}},"resets_at":"2099-11-01T00:00:00Z"}],"iguana_necktie":{"utilization":12,"resets_at":"2099-01-01T00:00:00Z"}}`, rowID: "seven-day-fable", used: 95, plan: "plan_team"},
	} {
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
			entries, err := embeddedusage.GetQuotaCache(ctx, tc.provider, fileName)
			if err != nil || len(entries) != 1 {
				t.Fatalf("cache entries=%v err=%v", entries, err)
			}
			if dir := os.Getenv("OTHER_PROVIDER_QUOTA_EVIDENCE_DIR"); dir != "" {
				if err := os.MkdirAll(dir, 0755); err != nil {
					t.Fatal(err)
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
			for _, raw := range state[key].([]any) {
				row := raw.(map[string]any)
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
