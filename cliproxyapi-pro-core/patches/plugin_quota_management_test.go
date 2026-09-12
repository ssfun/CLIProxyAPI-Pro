package management

import (
	"context"
	"encoding/json"
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

func TestProQuotaEndpointPersistsUpstreamQuotaAndRetainsBothResponseShapes(t *testing.T) {
	t.Setenv("USAGE_DB_PATH", filepath.Join(t.TempDir(), "usage.sqlite"))
	t.Setenv("USAGE_SERVICE_ENABLED", "true")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service, err := embeddedusage.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	embeddedusage.SetDefaultService(service)
	t.Cleanup(func() { embeddedusage.SetDefaultService(nil) })
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

func TestProQuotaEndpointForwardsExplicitUpstreamSelection(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	auth := &coreauth.Auth{ID: "quota-auth", Provider: "other-provider"}
	auth.EnsureIndex()
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatal(err)
	}
	host := pluginhost.New()
	host.RegisterPluginForTest("selected-plugin", pluginapi.Plugin{Capabilities: pluginapi.Capabilities{QuotaProvider: &mockQuotaProvider{identifier: "opencode-go"}}})
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)
	h.SetPluginHost(host)
	for _, selection := range []string{`"plugin_id":"selected-plugin"`, `"provider":"opencode-go"`} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v0/management/quota/fetch", strings.NewReader(`{"AuthIndex":"`+auth.Index+`",`+selection+`}`))
		c.Request.Header.Set("Content-Type", "application/json")
		h.FetchProPluginQuota(c)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"groups"`) {
			t.Fatalf("selection %s response %d: %s", selection, rec.Code, rec.Body.String())
		}
	}
}
