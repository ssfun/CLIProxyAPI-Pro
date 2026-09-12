package api

import (
	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/pro/apikeypolicy"
	sdkaccess "github.com/router-for-me/CLIProxyAPI/v7/sdk/access"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSelfQueryAuthRequiresExplicitConfiguredBearer(t *testing.T) {
	for _, tc := range []struct {
		name, bearer, provider, principal string
		noProviders                       bool
		want                              int
	}{
		{name: "own key", bearer: "Bearer own", provider: sdkaccess.DefaultAccessProviderName, principal: "own", want: 204},
		{name: "query key only", provider: sdkaccess.DefaultAccessProviderName, principal: "own", want: 401},
		{name: "other principal", bearer: "Bearer own", provider: sdkaccess.DefaultAccessProviderName, principal: "other", want: 401},
		{name: "plugin identity", bearer: "Bearer own", provider: "plugin", principal: "own", want: 401},
		{name: "open server", bearer: "Bearer own", noProviders: true, want: 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := sdkaccess.NewManager()
			if !tc.noProviders {
				manager.SetProviders([]sdkaccess.Provider{apiKeyPolicyAccessProvider{provider: tc.provider, principal: tc.principal}})
			}
			router := gin.New()
			router.Use(selfQueryAuth(manager))
			router.GET("/test", func(c *gin.Context) {
				identity, ok := apikeypolicy.IdentityFromContext(c.Request.Context())
				if !ok || !identity.Valid() {
					t.Fatal("missing identity")
				}
				if _, ok := apikeypolicy.DecisionFromContext(c.Request.Context()); ok {
					t.Fatal("query must not enforce generation policy or admit quota")
				}
				c.Status(http.StatusNoContent)
			})
			request := httptest.NewRequest("GET", "/test?key=own&api_key_hash=other", nil)
			request.Header.Set("Authorization", tc.bearer)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			if recorder.Code != tc.want {
				t.Fatalf("status %d want %d", recorder.Code, tc.want)
			}
			if recorder.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("private response is cacheable")
			}
		})
	}
}
