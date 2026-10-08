package management

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	devinauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/devin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/embeddedusage"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/pluginhost"
	proinspection "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/inspection"
	proobservability "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/observability"
	prorouting "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/routing"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

// Written before implementation: real HTTP, full inspection dispatch and SQLite.
func TestInspectionProviderCoverageHTTPAndSQLite(t *testing.T) {
	for _, tc := range []struct {
		name, provider, body, host, path, action                                string
		status                                                                  int
		used                                                                    *float64
		unknown, invalidDCA, invalidReset, cancelEOF, replaceOnParse, rotateDCA bool
	}{
		{name: "kimi-ai", provider: "kimi-ai", host: "api.kimi.ai", path: "/coding/v1/usages", body: `{"usage":{"used":95,"limit":100}}`, used: coveragePercent(95), action: "disable"},
		{name: "kimi-dot-ai", provider: "kimi.ai", host: "api.kimi.ai", path: "/coding/v1/usages", body: `{"usage":{"used":20,"limit":100}}`, used: coveragePercent(20), action: "keep"},
		{name: "gemini-probe", provider: "gemini-cli", host: "quota.fixture.invalid", path: "/quota", body: `{"groups":[{"displayName":"Gemini","buckets":[{"window":"daily","remainingFraction":0.05}]}],"auth_update":{"Metadata":{"token":"malicious"}}}`, used: coveragePercent(95), action: "disable"},
		{name: "devin-weekly", provider: "devin", host: "server.codeium.com", path: "/exa.seat_management_pb.SeatManagementService/GetUserStatus", body: `{"userStatus":{"planStatus":{"dailyQuotaRemainingPercent":75,"weeklyQuotaRemainingPercent":"5","weeklyQuotaResetAtUnix":"4097174400","planInfo":{"planName":"Pro"},"planStart":"2099-01-01T00:00:00Z","planEnd":"2099-12-01T00:00:00Z"}}}`, used: coveragePercent(95), action: "disable"},
		{name: "meta-window", provider: "meta", host: "api.meta.ai", path: "/muse-code/key", body: `{"api_key":"must-not-retain","email":"private@example.com","subs_tier_name":"Pro","is_subs_active":true,"subs_usage":{"window":{"used_percent":"95","resets_at":4097174400,"window_duration_mins":300},"weekly":{"used_percent":20}}}`, used: coveragePercent(95), action: "disable"},
		{name: "meta-unknown", provider: "meta", host: "api.meta.ai", path: "/muse-code/key", body: `{"api_key":"must-not-retain","subs_tier_name":"Pro"}`, unknown: true, action: "keep"},
		{name: "meta-no-dca", provider: "meta", invalidDCA: true, action: "keep"},
		{name: "devin-malformed", provider: "devin", host: "server.codeium.com", path: "/exa.seat_management_pb.SeatManagementService/GetUserStatus", body: `{"userStatus":{"planStatus":{"dailyQuotaRemainingPercent":"unknown"}}}`, unknown: true, action: "keep"},
		{name: "meta-malformed", provider: "meta", host: "api.meta.ai", path: "/muse-code/key", body: `[]`, unknown: true, action: "keep"},
		{name: "devin-401", provider: "devin", host: "server.codeium.com", path: "/exa.seat_management_pb.SeatManagementService/GetUserStatus", status: 401, body: `{"error":"invalid"}`, action: "disable"},
		{name: "meta-403", provider: "meta", host: "api.meta.ai", path: "/muse-code/key", status: 403, body: `{"api_key":"must-not-retain","error":"private@example.com"}`, action: "disable"},
		{name: "meta-429", provider: "meta", host: "api.meta.ai", path: "/muse-code/key", status: 429, body: `{"api_key":"must-not-retain"}`, action: "keep"},
		{name: "devin-reset-decimal", provider: "devin", host: "server.codeium.com", path: "/exa.seat_management_pb.SeatManagementService/GetUserStatus", body: `{"userStatus":{"planStatus":{"weeklyQuotaRemainingPercent":5,"weeklyQuotaResetAtUnix":"4097174400.0"}}}`, used: coveragePercent(95), invalidReset: true, action: "disable"},
		{name: "devin-reset-exponent", provider: "devin", host: "server.codeium.com", path: "/exa.seat_management_pb.SeatManagementService/GetUserStatus", body: `{"userStatus":{"planStatus":{"weeklyQuotaRemainingPercent":5,"weeklyQuotaResetAtUnix":"4.0971744e9"}}}`, used: coveragePercent(95), invalidReset: true, action: "disable"},
		{name: "meta-cancel-at-eof", provider: "meta", host: "api.meta.ai", path: "/muse-code/key", body: `{"subs_usage":{"window":{"used_percent":95}}}`, cancelEOF: true, unknown: true, action: "keep"},
		{name: "meta-replaced-after-response", provider: "meta", host: "api.meta.ai", path: "/muse-code/key", body: `{"subs_usage":{"window":{"used_percent":95}}}`, replaceOnParse: true, unknown: true, action: "keep"},
		{name: "meta-dca-rotated-after-response", provider: "meta", host: "api.meta.ai", path: "/muse-code/key", body: `{"subs_usage":{"window":{"used_percent":95}}}`, rotateDCA: true, unknown: true, action: "keep"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := startProQuotaTestService(t)
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			metadata := map[string]any{"access_token": "fixture-token", "api_key": "llm-key", "dca_token": "dca:fixture-dca"}
			if tc.invalidDCA {
				delete(metadata, "dca_token")
			}
			if tc.provider == "gemini-cli" {
				metadata["quota_probe"] = map[string]any{"url": "https://quota.fixture.invalid/quota"}
			}
			manager := coreauth.NewManager(nil, nil, nil)
			auth, err := manager.Register(ctx, &coreauth.Auth{ID: tc.name, FileName: tc.name + ".json", Provider: tc.provider, Metadata: metadata})
			if err != nil {
				t.Fatal(err)
			}
			requests := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Host != tc.host || r.URL.Path != tc.path {
					t.Errorf("unexpected request %s%s", r.Host, r.URL.Path)
				}
				if tc.provider == "meta" {
					if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer dca:fixture-dca" || r.Header.Get("x-api-version") != "1.0.0" {
						t.Errorf("invalid Meta request headers")
					}
				} else if tc.provider == "devin" {
					body, _ := io.ReadAll(r.Body)
					var data map[string]any
					if json.Unmarshal(body, &data) != nil || firstMap(data, "metadata")["apiKey"] != "fixture-token" || r.Method != "POST" || r.Header.Get("Connect-Protocol-Version") != "1" {
						t.Errorf("invalid Devin request: %s", body)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				status := tc.status
				if status == 0 {
					status = 200
				}
				w.WriteHeader(status)
				fmt.Fprint(w, tc.body)
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
			h := &Handler{authManager: manager, pluginHost: pluginhost.New()}
			s := &accountInspectionScheduler{h: h, quota: accountInspectionQuotaAdapter{h: h}}
			account := accountFromAuth(auth)
			if !shouldInspectAccount(account, "all") {
				t.Errorf("provider omitted from all inspection")
			}
			if strings.HasPrefix(tc.provider, "kimi") && !shouldInspectAccount(account, "kimi") {
				t.Errorf("Kimi alias omitted from canonical target")
			}
			settings := proinspection.DefaultSettings()
			settings.Timeout = 1000
			settings.Retries = 0
			settings.UsedPercentThreshold = 90
			var prior []byte
			if strings.HasSuffix(tc.name, "malformed") {
				if err := persistQuotaState(ctx, account, map[string]any{"status": "success", "sentinel": "retain previous"}); err != nil {
					t.Fatal(err)
				}
				priorEntries, _ := embeddedusage.GetQuotaCache(ctx, account.Provider, account.FileName)
				prior = priorEntries[0].Data
			}
			result := account.baseResult()
			if tc.cancelEOF || tc.replaceOnParse || tc.rotateDCA {
				decision, status, err := s.inspectExtendedProviderQuota(ctx, account, settings, "https://api.meta.ai/muse-code/key", map[string]string{"Authorization": "Bearer dca:fixture-dca", "x-api-version": "1.0.0"}, "{}", func(body string) (map[string]any, []map[string]any, *float64, error) {
					values, windows, used, err := proinspection.BuildMetaQuotaState(body)
					if tc.cancelEOF {
						cancel()
					}
					if tc.replaceOnParse {
						replacement := auth.Clone()
						replacement.Metadata["dca_token"] = "dca:replacement"
						if _, err := manager.Register(ctx, replacement); err != nil {
							t.Fatal(err)
						}
					}
					if tc.rotateDCA {
						replacement := auth.Clone()
						replacement.Metadata["dca_token"] = "dca:replacement"
						if _, err := manager.Update(ctx, replacement); err != nil {
							t.Fatal(err)
						}
					}
					return values, windows, used, err
				})
				if err == nil {
					result.Action = decision.Action
				}
				result.StatusCode = status
				result.UsedPercent = decision.UsedPercent
				result.IsQuota = decision.IsQuota
				result.QuotaKnown = decision.QuotaKnown
				if err != nil {
					result.Error = err.Error()
				}
			} else {
				result = s.inspectAccountObserved(ctx, account, settings)
			}
			if string(result.Action) != tc.action {
				t.Errorf("action=%v result=%+v", result.Action, result)
			}
			if tc.used != nil && (result.UsedPercent == nil || *result.UsedPercent != *tc.used || !result.QuotaKnown) {
				t.Errorf("quota=%+v want=%v", result, *tc.used)
			}
			if (tc.unknown || tc.invalidDCA) && (result.UsedPercent != nil || result.QuotaKnown || result.IsQuota) {
				t.Errorf("unknown quota treated as known: %+v", result)
			}
			if tc.invalidDCA && requests != 0 {
				t.Errorf("missing DCA made %d requests", requests)
			}
			if !tc.invalidDCA && requests != 1 {
				t.Errorf("requests=%d want 1", requests)
			}
			if tc.status > 0 && (result.StatusCode == nil || *result.StatusCode != tc.status) {
				t.Errorf("upstream status lost: %+v", result)
			}
			cacheProvider := tc.provider
			if strings.HasPrefix(cacheProvider, "kimi") {
				cacheProvider = "kimi"
			}
			entries, err := embeddedusage.GetQuotaCache(context.Background(), cacheProvider, auth.FileName)
			if err != nil {
				t.Fatal(err)
			}
			if tc.used != nil && len(entries) != 1 {
				t.Errorf("successful quota not persisted: %v", entries)
			}
			var persisted json.RawMessage
			if len(entries) > 0 {
				persisted = entries[0].Data
			}
			if (tc.cancelEOF || tc.replaceOnParse || tc.rotateDCA) && len(entries) != 0 {
				t.Error("canceled observation persisted")
			}
			if prior != nil && string(prior) != string(persisted) {
				t.Error("malformed observation replaced prior successful cache")
			}
			if tc.used != nil {
				snapshot, found, err := (proobservability.SettingsStore{}).GetPlanSnapshot(ctx, auth.Provider, auth.FileName, auth.Index)
				if err != nil || !found || len(snapshot.Data) == 0 {
					t.Errorf("policy cache reader omitted observation: found=%v err=%v", found, err)
				}
			}
			encoded, _ := json.Marshal(result)
			if strings.Contains(string(persisted)+string(encoded), "must-not-retain") || strings.Contains(string(persisted)+string(encoded), "private@example.com") {
				t.Errorf("upstream secrets leaked")
			}
			if tc.used != nil && *tc.used >= 90 {
				hold := newInspectionQuotaHold(auth, &result, settings)
				if hold == nil || !prorouting.ProtectionBlocks(*hold, "any-model", time.Now()) {
					t.Errorf("exhaustion did not establish account protection")
				}
				if (tc.provider == "meta" || tc.provider == "devin") && !tc.invalidReset {
					if result.QuotaResetAt != 4097174400000 {
						t.Errorf("reset=%d", result.QuotaResetAt)
					}
				}
			}
			if tc.invalidReset && (result.QuotaResetAt != 0 || strings.Contains(string(persisted), "4097174400000")) {
				t.Error("invalid reset interpreted as a trusted future deadline")
			}
			updated, _ := manager.GetByID(auth.ID)
			if updated.Metadata["access_token"] != "fixture-token" {
				t.Error("quota probe mutated auth")
			}
			if dir := os.Getenv("PROVIDER_COVERAGE_EVIDENCE_DIR"); dir != "" {
				if err := os.MkdirAll(dir, 0755); err != nil {
					t.Fatal(err)
				}
				evidence, _ := json.MarshalIndent(map[string]any{"requests": requests, "result": result, "sqlite": persisted}, "", "  ")
				if err := os.WriteFile(filepath.Join(dir, tc.name+".json"), evidence, 0600); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func coveragePercent(value float64) *float64 { return &value }

func TestInspectionRealDevinOAuthHTTPAndSQLite(t *testing.T) {
	ctx := startProQuotaTestService(t)
	var inspecting atomic.Bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v3/self" {
			fmt.Fprint(w, `{"user_name":"registered-user","user_id":"fixture-id","org_id":"fixture-org"}`)
			return
		}
		if r.URL.Path != devinauth.DevinGetUserStatusPath {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		if !inspecting.Load() {
			w.WriteHeader(503)
			return
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil || firstMap(body, "metadata")["apiKey"] != "devin-session-token$eyJ.fixture.token" {
			t.Error("real OAuth token not used for quota")
		}
		fmt.Fprint(w, `{"userStatus":{"planStatus":{"dailyQuotaRemainingPercent":5,"dailyQuotaResetAtUnix":"4097174400"}}}`)
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
	auth, err := devinauth.NewDevinAuthService(&http.Client{Transport: transport}).CreateAuthRecord(ctx, "eyJ.fixture.token")
	if err != nil {
		t.Fatal(err)
	}
	if auth.Attributes["auth_kind"] != "oauth" || auth.Attributes["api_key"] == "" || auth.Attributes["path"] != "" {
		t.Fatal("fixture did not produce the real initial OAuth record")
	}
	manager := coreauth.NewManager(nil, nil, nil)
	auth, err = manager.Register(ctx, auth)
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{authManager: manager}
	account := accountFromAuth(auth)
	if !shouldInspectAccount(account, "devin") {
		t.Error("initial real Devin OAuth record misclassified as API key")
	}
	for _, apiKey := range []*coreauth.Auth{
		{Provider: "devin", Attributes: map[string]string{"api_key": "config-key", "source": "config:devin", "auth_kind": "oauth"}},
		{Provider: "devin", Attributes: map[string]string{"api_key": "api-only"}},
	} {
		if shouldInspectAccount(accountFromAuth(apiKey), "all") {
			t.Error("genuine config/API key included in OAuth inspection")
		}
	}
	inspecting.Store(true)
	settings := proinspection.DefaultSettings()
	settings.Timeout = 1000
	settings.UsedPercentThreshold = 90
	result := (&accountInspectionScheduler{h: h}).inspectAccountObserved(ctx, account, settings)
	if result.UsedPercent == nil || *result.UsedPercent != 95 || !result.IsQuota {
		t.Errorf("real OAuth quota result: %+v", result)
	}
	entries, err := embeddedusage.GetQuotaCache(ctx, "devin", auth.FileName)
	if err != nil || len(entries) != 1 {
		t.Fatalf("missing real OAuth quota persistence: %v %v", entries, err)
	}
	if dir := os.Getenv("PROVIDER_COVERAGE_EVIDENCE_DIR"); dir != "" {
		evidence, _ := json.MarshalIndent(map[string]any{"result": result, "sqlite": json.RawMessage(entries[0].Data), "authKind": "oauth"}, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, "devin-real-oauth.json"), evidence, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
