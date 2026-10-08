package management

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/embeddedusage"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestXAIQuotaInspectionAndManualWriteFence(t *testing.T) {
	for _, writer := range []string{"inspection observation", "inspection billing", "manual PUT"} {
		t.Run(writer, func(t *testing.T) {
			ctx := startProQuotaTestService(t)
			manager := coreauth.NewManager(nil, nil, nil)
			if binder, ok := any(manager).(interface{ BindXAIQuotaCache() }); ok {
				binder.BindXAIQuotaCache()
			}
			h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)
			old, err := manager.Register(ctx, &coreauth.Auth{ID: "same.json", FileName: "same.json", Provider: "xai", Index: "same-index", Metadata: map[string]any{"sub": "old", "access_token": "old-token"}})
			if err != nil {
				t.Fatal(err)
			}
			account := accountFromAuth(old)
			if err = persistQuotaState(ctx, account, quotaSuccessState(map[string]any{"billing": map[string]any{"planLabel": "Old Plan"}})); err != nil {
				t.Fatal(err)
			}
			oldEntries, _ := embeddedusage.GetQuotaCache(ctx, "xai", "same.json")
			var recorder *httptest.ResponseRecorder
			var release chan struct{}
			var done chan struct{}
			if writer == "manual PUT" {
				arrived := make(chan struct{})
				release, done = make(chan struct{}), make(chan struct{})
				r := gin.New()
				group := r.Group("/v0/management/usage", h.BindQuotaCacheIdentity, func(c *gin.Context) { close(arrived); <-release; c.Next() })
				embeddedusage.RegisterGinRoutes(group)
				body, _ := json.Marshal(oldEntries[0])
				recorder = httptest.NewRecorder()
				go func() {
					defer close(done)
					r.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/v0/management/usage/quota-cache", strings.NewReader(string(body))).WithContext(ctx))
				}()
				<-arrived
			}
			manager.Remove(ctx, old.ID)
			fresh := old.Clone()
			fresh.Metadata["sub"] = "new"
			fresh, err = manager.Register(ctx, fresh)
			if err != nil {
				t.Fatal(err)
			}
			if err = persistQuotaState(ctx, accountFromAuth(fresh), quotaSuccessState(map[string]any{"billing": map[string]any{"planLabel": "Current Plan"}})); err != nil {
				t.Fatal(err)
			}
			switch writer {
			case "manual PUT":
				close(release)
				<-done
			case "inspection observation":
				observeAccountXAIQuota(ctx, account, "grok-free", accountInspectionHTTPResult{StatusCode: http.StatusOK, Header: http.Header{"X-Ratelimit-Limit-Tokens": {"100"}, "X-Ratelimit-Remaining-Tokens": {"0"}}})
			case "inspection billing":
				_ = persistQuotaState(ctx, account, quotaSuccessState(map[string]any{"billing": map[string]any{"planLabel": "Late Old Plan"}}))
			}
			entries, err := embeddedusage.GetQuotaCache(ctx, "xai", "same.json")
			if err != nil || len(entries) != 1 {
				t.Fatalf("entries=%+v err=%v", entries, err)
			}
			t.Logf("writer=%s sqlite fingerprint=%s data=%s", writer, entries[0].IdentityFingerprint, entries[0].Data)
			if entries[0].IdentityFingerprint != xaiAccountQuotaIdentityFingerprint(accountFromAuth(fresh)) || !strings.Contains(string(entries[0].Data), "Current Plan") {
				t.Fatalf("late %s overwrote new quota: %+v", writer, entries[0])
			}
			if writer == "manual PUT" && recorder.Code != http.StatusConflict {
				t.Fatalf("PUT status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}
