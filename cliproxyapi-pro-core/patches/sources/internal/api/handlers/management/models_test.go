package management

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/pro/apikeypolicy"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
)

func TestManagementModelsRequireManagementAuthAndIgnoreBusinessPolicy(t *testing.T) {
	h, router := newAuthenticatedAPIKeyPolicyManagementHarness(t, []string{"restricted-business-key"})
	group := router.Group("/v0/management", h.Middleware())
	// Even an attached restrictive business decision must not narrow this catalog.
	group.Use(func(c *gin.Context) {
		decision := apikeypolicy.RequestPolicyDecision{Mode: apikeypolicy.ModeProfile, Snapshot: &apikeypolicy.RequestPolicySnapshot{AllowedModels: map[string]struct{}{"not-in-catalog": {}}, AllowedProviders: map[string]struct{}{"not-a-provider": {}}}}
		c.Request = c.Request.WithContext(apikeypolicy.WithDecision(c.Request.Context(), decision))
	})
	group.GET("/models", h.GetManagementModels)
	for _, key := range []string{"", "restricted-business-key"} {
		response := authenticatedPolicyRequest(t, router, http.MethodGet, "/v0/management/models", key, nil)
		if response.Code == http.StatusOK {
			t.Fatal("business key bypassed management authentication")
		}
	}
	identity, err := apikeypolicy.NewAuthenticatedAPIKeyIdentity("restricted-business-key")
	if err != nil {
		t.Fatal(err)
	}
	service := h.apiKeyPolicyService()
	if err := service.SetKeyDisabled(context.Background(), identity, true, false); err != nil {
		t.Fatal(err)
	}
	if err := service.SetTakeover(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	_, err = service.Decide(identity)
	var policyErr *apikeypolicy.PolicyError
	if !errors.As(err, &policyErr) || policyErr.Code != "api_key_disabled" {
		t.Fatalf("disabled key decision = %v", err)
	}
	response := authenticatedPolicyRequest(t, router, http.MethodGet, "/v0/management/models", "management-policy-test-secret", nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"claude-sonnet-4-6"`) {
		t.Fatalf("catalog = %d %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("catalog is cacheable")
	}
	// Catalog changes must be visible without changing any business API key.
	registry.GetGlobalRegistry().RegisterClient("management-models-test", "codex", []*registry.ModelInfo{{ID: "management-models-extra"}})
	t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient("management-models-test") })
	response = authenticatedPolicyRequest(t, router, http.MethodGet, "/v0/management/models", "management-policy-test-secret", nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"management-models-extra"`) {
		t.Fatalf("updated catalog = %d %s", response.Code, response.Body.String())
	}
}
