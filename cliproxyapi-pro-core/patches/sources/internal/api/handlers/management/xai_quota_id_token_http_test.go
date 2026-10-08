package management

import (
	"encoding/base64"
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

// HTTP upload, quota PUT/GET, and inspection observation share real SQLite.
func TestXAIQuotaIDTokenEmailHTTPAndSQLite(t *testing.T) {
	for _, encoding := range []string{"map", "json", "jwt"} {
		t.Run(encoding, func(t *testing.T) {
			ctx := startProQuotaTestService(t)
			manager := coreauth.NewManager(nil, nil, nil)
			if binder, ok := any(manager).(interface{ BindXAIQuotaCache() }); ok {
				binder.BindXAIQuotaCache()
			}
			h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)
			claims := map[string]any{"email": "id-only@example.test"}
			raw, _ := json.Marshal(claims)
			var token any = claims
			if encoding == "json" {
				token = string(raw)
			}
			if encoding == "jwt" {
				token = "header." + base64.RawURLEncoding.EncodeToString(raw) + ".signature"
			}
			body, _ := json.Marshal(map[string]any{"type": "xai", "access_token": "opaque", "id_token": token})
			upload := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(upload)
			c.Request = httptest.NewRequest(http.MethodPost, "/v0/management/auth-files?name=same.json", strings.NewReader(string(body))).WithContext(ctx)
			h.UploadAuthFile(c)
			if upload.Code != http.StatusOK {
				t.Fatalf("upload status=%d body=%s", upload.Code, upload.Body.String())
			}
			account := accountFromAuth(manager.List()[0])
			if err := persistQuotaState(ctx, account, quotaSuccessState(map[string]any{"billing": map[string]any{"planLabel": "Current Plan"}})); err != nil {
				t.Fatalf("valid id_token email inspection rejected: %v", err)
			}
			observeAccountXAIQuota(ctx, account, "grok-free", accountInspectionHTTPResult{StatusCode: http.StatusOK, Header: http.Header{"X-Ratelimit-Limit-Tokens": {"100"}, "X-Ratelimit-Remaining-Tokens": {"40"}}})
			entries, _ := embeddedusage.GetQuotaCache(ctx, "xai", "same.json")
			if len(entries) != 1 || !strings.Contains(string(entries[0].Data), "freeQuota") {
				t.Fatalf("id_token observation rejected: %+v", entries)
			}
			r := gin.New()
			embeddedusage.RegisterGinRoutes(r.Group("/v0/management/usage", h.BindQuotaCacheIdentity))
			raw, _ = json.Marshal(entries[0])
			put := httptest.NewRecorder()
			r.ServeHTTP(put, httptest.NewRequest(http.MethodPut, "/v0/management/usage/quota-cache", strings.NewReader(string(raw))).WithContext(ctx))
			get := httptest.NewRecorder()
			r.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/v0/management/usage/quota-cache?provider=xai", nil).WithContext(ctx))
			if put.Code != http.StatusOK || get.Code != http.StatusOK || !strings.Contains(get.Body.String(), "Current Plan") || !strings.Contains(get.Body.String(), "freeQuota") {
				t.Fatalf("put=%d %s get=%d %s", put.Code, put.Body.String(), get.Code, get.Body.String())
			}
			t.Logf("encoding=%s sqlite=%s HTTP GET=%s", encoding, entries[0].Data, get.Body.String())
		})
	}
}
