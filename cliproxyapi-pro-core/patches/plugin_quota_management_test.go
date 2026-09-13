package management

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/embeddedusage"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/pluginhost"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestQuotaFileNameFallsBackToAuthIndex(t *testing.T) {
	if got := quotaFileName("", "gemini-cli:user@example.com:project-a"); got != "gemini-cli:user@example.com:project-a" {
		t.Fatalf("quotaFileName() = %q", got)
	}
	if got := quotaFileName("gemini.json", "ignored"); got != "gemini.json" {
		t.Fatalf("quotaFileName() = %q", got)
	}
}

func startProQuotaTestService(t *testing.T) context.Context {
	t.Helper()
	t.Setenv("USAGE_DB_PATH", filepath.Join(t.TempDir(), "usage.sqlite"))
	t.Setenv("USAGE_SERVICE_ENABLED", "true")
	ctx, cancel := context.WithCancel(context.Background())
	service, err := embeddedusage.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	embeddedusage.SetDefaultService(service)
	t.Cleanup(func() { embeddedusage.SetDefaultService(nil); cancel() })
	return ctx
}

func TestProQuotaEndpointPersistsUpstreamQuotaAndRetainsBothResponseShapes(t *testing.T) {
	ctx := startProQuotaTestService(t)
	manager := coreauth.NewManager(nil, nil, nil)
	auth := &coreauth.Auth{ID: "quota-auth", FileName: "quota.json", Provider: "opencode-go"}
	auth.EnsureIndex()
	if _, err := manager.Register(ctx, auth); err != nil {
		t.Fatal(err)
	}
	host := pluginhost.New()
	host.RegisterPluginForTest("quota-plugin", pluginapi.Plugin{Capabilities: pluginapi.Capabilities{QuotaProvider: &mockQuotaProvider{identifier: auth.Provider}}})
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)
	h.SetPluginHost(host)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v0/management/quota/fetch", strings.NewReader(`{"authIndex":"`+auth.Index+`"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	h.FetchProPluginQuota(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("quota response %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Snapshot     pluginapi.QuotaSnapshot      `json:"snapshot"`
		Groups       []pluginapi.QuotaGroup       `json:"groups"`
		Subscription *pluginapi.QuotaSubscription `json:"subscription"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Groups) != 1 || response.Subscription == nil || response.Subscription.TierID != "pro-tier" || len(response.Snapshot.Items) != 1 {
		t.Fatalf("missing upstream or Pro quota: %s", rec.Body.String())
	}
	saved := loadPluginQuotaSnapshot(ctx, auth.Provider, auth.FileName, auth.Index)
	if saved == nil || saved.Plan == nil || saved.Plan.ID != "pro-tier" || len(saved.Items) != 1 || *saved.Items[0].RemainingFraction != 0.9 {
		t.Fatalf("persisted upstream quota = %#v", saved)
	}
}

func TestProQuotaEndpointPersistsExplicitSelection(t *testing.T) {
	ctx := startProQuotaTestService(t)
	manager := coreauth.NewManager(nil, nil, nil)
	auth := &coreauth.Auth{ID: "quota-auth", FileName: "quota.json", Provider: "other-provider"}
	auth.EnsureIndex()
	if _, err := manager.Register(ctx, auth); err != nil {
		t.Fatal(err)
	}
	host := pluginhost.New()
	selectedProvider := ""
	failFetch := false
	calls := 0
	host.RegisterPluginForTest("selected-plugin", pluginapi.Plugin{Capabilities: pluginapi.Capabilities{QuotaProvider: &mockQuotaProvider{
		identifier: "opencode-go",
		fetchFn: func(_ context.Context, req pluginapi.QuotaFetchRequest) (pluginapi.QuotaFetchResponse, error) {
			calls++
			if failFetch {
				return pluginapi.QuotaFetchResponse{}, errors.New("quota request failed")
			}
			if req.Provider != selectedProvider || req.AuthProvider != auth.Provider || req.AuthID != auth.ID || req.Previous == nil || req.Previous.Plan == nil || req.Previous.Plan.ID != "old-tier" {
				t.Fatalf("selected quota request lost provider/auth/cache context: %#v", req)
			}
			return pluginapi.QuotaFetchResponse{
				Subscription: &pluginapi.QuotaSubscription{Plan: "pro", TierID: "new-tier"},
				Groups:       []pluginapi.QuotaGroup{{Buckets: []pluginapi.QuotaBucket{{Window: "daily", RemainingFraction: 0.5}}}},
				AuthUpdate:   pluginapi.AuthData{ID: "foreign-id", Provider: "foreign-provider", FileName: "foreign.json", Metadata: map[string]any{"token": "refreshed-test-token"}},
			}, nil
		},
	}}})
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)
	h.SetPluginHost(host)
	for _, tc := range []struct {
		selection, provider string
		fail                bool
		status, wantCalls   int
	}{
		{`"plugin_id":"selected-plugin"`, auth.Provider, false, 200, 1},
		{`"provider":"opencode-go"`, "opencode-go", false, 200, 1},
		{`"provider":"selected-plugin"`, "selected-plugin", false, 200, 1},
		{`"plugin_id":"selected-plugin","provider":"override"`, "override", false, 200, 1},
		{`"plugin_id":"missing","provider":"opencode-go"`, "opencode-go", false, 501, 0},
		{`"provider":"opencode-go"`, "opencode-go", true, 502, 1},
	} {
		t.Run(tc.selection+fmt.Sprint(tc.status), func(t *testing.T) {
			old := pluginapi.QuotaSnapshot{SchemaVersion: 1, Provider: auth.Provider, Plan: &pluginapi.QuotaPlan{ID: "old-tier"}}
			if err := persistPluginQuotaSnapshot(ctx, auth.Provider, auth.FileName, auth.Index, old); err != nil {
				t.Fatal(err)
			}
			selectedProvider, failFetch, calls = tc.provider, tc.fail, 0
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v0/management/quota/fetch", strings.NewReader(`{"AuthIndex":"`+auth.Index+`",`+tc.selection+`}`))
			c.Request.Header.Set("Content-Type", "application/json")
			h.FetchProPluginQuota(c)
			if rec.Code != tc.status || calls != tc.wantCalls {
				t.Fatalf("response %d, calls %d: %s", rec.Code, calls, rec.Body.String())
			}
			saved := loadPluginQuotaSnapshot(ctx, auth.Provider, auth.FileName, auth.Index)
			wantTier := "old-tier"
			if tc.status == 200 {
				wantTier = "new-tier"
				var response struct {
					Snapshot pluginapi.QuotaSnapshot `json:"snapshot"`
					PluginID string                  `json:"plugin_id"`
					Groups   []pluginapi.QuotaGroup  `json:"groups"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				if response.PluginID != "selected-plugin" || response.Snapshot.Provider != auth.Provider || len(response.Snapshot.Items) != 1 || len(response.Groups) != 1 {
					t.Fatalf("selected response lost normalized data or identity: %s", rec.Body.String())
				}
				if strings.Contains(rec.Body.String(), "auth_update") || strings.Contains(rec.Body.String(), "refreshed-test-token") {
					t.Fatalf("auth update leaked in quota response: %s", rec.Body.String())
				}
				updated, ok := manager.GetByID(auth.ID)
				if !ok || updated.Provider != auth.Provider || updated.FileName != auth.FileName || updated.Metadata["token"] != "refreshed-test-token" {
					t.Fatalf("auth update lost identity or metadata: %#v", updated)
				}
			}
			if saved == nil || saved.Plan == nil || saved.Plan.ID != wantTier {
				t.Fatalf("persisted quota = %#v, want %s", saved, wantTier)
			}
			if foreign := loadPluginQuotaSnapshot(ctx, tc.provider, auth.FileName, auth.Index); tc.provider != auth.Provider && foreign != nil {
				t.Fatalf("quota persisted under selector instead of auth identity: %#v", foreign)
			}
		})
	}
}

func TestProQuotaEndpointPersistsDeclarativeProbeAndPreservesCacheOnFailure(t *testing.T) {
	ctx := startProQuotaTestService(t)
	probeStatus := http.StatusOK
	probe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(probeStatus)
		_, _ = w.Write([]byte(`{"subscription":{"plan":"pro","tierId":"probe-tier"},"groups":[{"displayName":"Gemini","buckets":[{"window":"daily","remainingFraction":0.75}]}],"auth_update":{"Metadata":{"token":"untrusted-probe-token"}}}`))
	}))
	defer probe.Close()
	manager := coreauth.NewManager(nil, nil, nil)
	auth := &coreauth.Auth{ID: "probe-auth", FileName: "probe.json", Provider: "gemini-cli", Metadata: map[string]any{"quota_probe": map[string]any{"url": probe.URL}, "token": "original-test-token"}}
	auth.EnsureIndex()
	if _, err := manager.Register(ctx, auth); err != nil {
		t.Fatal(err)
	}
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)
	for _, status := range []int{http.StatusOK, http.StatusBadGateway} {
		probeStatus = status
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v0/management/quota/fetch", strings.NewReader(`{"auth_index":"`+auth.Index+`"}`))
		c.Request.Header.Set("Content-Type", "application/json")
		h.FetchProPluginQuota(c)
		if rec.Code != status {
			t.Fatalf("probe response %d: %s", rec.Code, rec.Body.String())
		}
		if status == http.StatusOK {
			var response struct {
				Snapshot pluginapi.QuotaSnapshot `json:"snapshot"`
				Groups   []pluginapi.QuotaGroup  `json:"groups"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if len(response.Snapshot.Items) != 1 || *response.Snapshot.Items[0].RemainingFraction != 0.75 || len(response.Groups) != 1 {
				t.Fatalf("Gemini UI cannot hydrate successful probe: %s", rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "auth_update") || strings.Contains(rec.Body.String(), "untrusted-probe-token") {
				t.Fatalf("probe auth payload leaked: %s", rec.Body.String())
			}
		}
		saved := loadPluginQuotaSnapshot(ctx, auth.Provider, auth.FileName, auth.Index)
		if saved == nil || saved.Plan == nil || saved.Plan.ID != "probe-tier" || len(saved.Items) != 1 {
			t.Fatalf("probe snapshot missing or overwritten by error: %#v", saved)
		}
		updated, _ := manager.GetByID(auth.ID)
		if updated.Metadata["token"] != "original-test-token" {
			t.Fatal("declarative probe changed credentials")
		}
	}
}
