package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	codexlive "github.com/router-for-me/CLIProxyAPI/v7/internal/client/codex/live"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/pro/apikeypolicy"
	sdkaccess "github.com/router-for-me/CLIProxyAPI/v7/sdk/access"
)

type apiKeyPolicyAccessProvider struct {
	provider  string
	principal string
	authErr   *sdkaccess.AuthError
}

func (p apiKeyPolicyAccessProvider) Identifier() string { return p.provider }
func (p apiKeyPolicyAccessProvider) Authenticate(context.Context, *http.Request) (*sdkaccess.Result, *sdkaccess.AuthError) {
	if p.authErr != nil {
		return nil, p.authErr
	}
	return &sdkaccess.Result{Provider: p.provider, Principal: p.principal}, nil
}

func newAPIKeyPolicyMiddlewareService(t *testing.T) *apikeypolicy.Service {
	t.Helper()
	store, err := apikeypolicy.OpenStore(filepath.Join(t.TempDir(), "policy.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := apikeypolicy.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.SetTakeover(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	return service
}

func TestAuthMiddlewareCreatesIdentityOnlyForSuccessfulConfigInlineResult(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name         string
		provider     string
		wantIdentity bool
	}{
		{name: "config inline", provider: sdkaccess.DefaultAccessProviderName, wantIdentity: true},
		{name: "plugin access provider", provider: "plugin-access", wantIdentity: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := sdkaccess.NewManager()
			manager.SetProviders([]sdkaccess.Provider{apiKeyPolicyAccessProvider{provider: tc.provider, principal: "authenticated-key"}})
			service := newAPIKeyPolicyMiddlewareService(t)
			router := gin.New()
			router.Use(AuthMiddleware(manager, service))
			router.GET("/test", func(c *gin.Context) {
				identity, hasIdentity := apikeypolicy.IdentityFromContext(c.Request.Context())
				decision, hasDecision := apikeypolicy.DecisionFromContext(c.Request.Context())
				if hasIdentity != tc.wantIdentity || hasDecision != tc.wantIdentity {
					t.Fatalf("identity=%t decision=%t, want %t", hasIdentity, hasDecision, tc.wantIdentity)
				}
				if tc.wantIdentity && (!identity.Valid() || decision.Mode != apikeypolicy.ModePassthrough) {
					t.Fatalf("identity=%#v decision=%#v", identity, decision)
				}
				c.Status(http.StatusNoContent)
			})
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/test", nil))
			if recorder.Code != http.StatusNoContent {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestAuthMiddlewareNeverInjectsPolicyStateForNoAuthOrRejectedCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name      string
		providers []sdkaccess.Provider
		wantCode  int
	}{
		{name: "no auth configured", wantCode: http.StatusNoContent},
		{name: "missing credential", providers: []sdkaccess.Provider{apiKeyPolicyAccessProvider{provider: sdkaccess.DefaultAccessProviderName, authErr: sdkaccess.NewNoCredentialsError()}}, wantCode: http.StatusUnauthorized},
		{name: "invalid credential", providers: []sdkaccess.Provider{apiKeyPolicyAccessProvider{provider: sdkaccess.DefaultAccessProviderName, authErr: sdkaccess.NewInvalidCredentialError()}}, wantCode: http.StatusUnauthorized},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager := sdkaccess.NewManager()
			manager.SetProviders(test.providers)
			service := newAPIKeyPolicyMiddlewareService(t)
			router := gin.New()
			router.Use(AuthMiddleware(manager, service))
			router.GET("/test", func(c *gin.Context) {
				if _, ok := apikeypolicy.IdentityFromContext(c.Request.Context()); ok {
					t.Fatal("request obtained an API key identity without successful config-inline authentication")
				}
				if _, ok := apikeypolicy.DecisionFromContext(c.Request.Context()); ok {
					t.Fatal("request obtained a policy decision without successful config-inline authentication")
				}
				c.Status(http.StatusNoContent)
			})
			request := httptest.NewRequest(http.MethodGet, "/test?api_key_policy_id=forged", strings.NewReader(`{"profileId":"forged","allowedProviders":["forged"]}`))
			request.Header.Set("X-API-Key-Policy-ID", "forged")
			request.Header.Set("X-Profile-ID", "forged")
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			if recorder.Code != test.wantCode {
				t.Fatalf("status=%d body=%s; want %d", recorder.Code, recorder.Body.String(), test.wantCode)
			}
		})
	}
}

func TestAuthMiddlewareFailsClosedWhenPolicyIndexUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := sdkaccess.NewManager()
	manager.SetProviders([]sdkaccess.Provider{apiKeyPolicyAccessProvider{provider: sdkaccess.DefaultAccessProviderName, principal: "authenticated-key"}})
	service := newAPIKeyPolicyMiddlewareService(t)
	service.MarkUnavailable()
	router := gin.New()
	router.Use(AuthMiddleware(manager, service))
	router.GET("/test", func(c *gin.Context) { t.Fatal("handler executed with unavailable policy index") })
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/test", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestAPIKeyQuotaMiddlewareExemptsDiscoveryAndChargesConsumerRoutesKeyWide(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := sdkaccess.NewManager()
	manager.SetProviders([]sdkaccess.Provider{apiKeyPolicyAccessProvider{provider: sdkaccess.DefaultAccessProviderName, principal: "quota-route-key"}})
	service := newAPIKeyPolicyMiddlewareService(t)
	identity, err := apikeypolicy.NewAuthenticatedAPIKeyIdentity("quota-route-key")
	if err != nil {
		t.Fatal(err)
	}
	requestLimit := int64(1)
	if _, err = service.Create(context.Background(), identity, "Route quota", apikeypolicy.ProfileInput{Name: "default"}, &apikeypolicy.QuotaInput{Enabled: true, Requests: &requestLimit}); err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.Use(AuthMiddleware(manager, service), apiKeyQuotaMiddleware(service))
	for _, route := range []string{"/v1/models", "/v1beta/models", "/v1beta/models/gemini-3-flash", "/v1/live/call-1", "/v1/realtime"} {
		router.GET(route, func(c *gin.Context) { c.Status(http.StatusNoContent) })
	}
	router.POST("/v1/videos", func(c *gin.Context) {
		decision, ok := apikeypolicy.DecisionFromContext(c.Request.Context())
		if !ok {
			t.Fatal("consumer route has no quota decision")
		}
		if _, ok = decision.QuotaAttribution(); !ok {
			t.Fatal("consumer route has no quota admission")
		}
		c.Status(http.StatusNoContent)
	})
	router.POST("/backend-api/codex/responses", func(c *gin.Context) {
		t.Fatal("second consumer route executed after request quota exhaustion")
	})

	request := func(method, path string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(method, path, nil))
		return recorder
	}
	for _, route := range []string{"/v1/models", "/v1beta/models", "/v1beta/models/gemini-3-flash", "/v1/live/call-1", "/v1/realtime?call_id=call-1"} {
		if recorder := request(http.MethodGet, route); recorder.Code != http.StatusNoContent {
			t.Fatalf("exempt route %s status=%d body=%s", route, recorder.Code, recorder.Body.String())
		}
	}
	if recorder := request(http.MethodPost, "/v1/videos"); recorder.Code != http.StatusNoContent {
		t.Fatalf("first consumer status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder := request(http.MethodPost, "/backend-api/codex/responses"); recorder.Code != http.StatusTooManyRequests || !strings.Contains(recorder.Body.String(), `"code":"api_key_quota_exceeded"`) {
		t.Fatalf("second consumer status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestAPIKeyQuotaMiddlewareDefersWebsocketChargeToEachTurn(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := sdkaccess.NewManager()
	manager.SetProviders([]sdkaccess.Provider{apiKeyPolicyAccessProvider{provider: sdkaccess.DefaultAccessProviderName, principal: "websocket-quota-key"}})
	service := newAPIKeyPolicyMiddlewareService(t)
	identity, _ := apikeypolicy.NewAuthenticatedAPIKeyIdentity("websocket-quota-key")
	requestLimit := int64(1)
	if _, err := service.Create(context.Background(), identity, "WebSocket", apikeypolicy.ProfileInput{Name: "default"}, &apikeypolicy.QuotaInput{Enabled: true, Requests: &requestLimit}); err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.Use(AuthMiddleware(manager, service), apiKeyQuotaMiddleware(service))
	router.GET("/v1/responses", func(c *gin.Context) {
		decision, _ := apikeypolicy.DecisionFromContext(c.Request.Context())
		if _, charged := decision.QuotaAttribution(); charged {
			t.Fatal("websocket handshake consumed a request unit")
		}
		if _, err := apikeypolicy.AdmitQuotaTurn(c.Request.Context()); err != nil {
			t.Fatalf("first websocket turn admission: %v", err)
		}
		if _, err := apikeypolicy.AdmitQuotaTurn(c.Request.Context()); err == nil {
			t.Fatal("second websocket turn bypassed request quota")
		}
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestAPIKeyQuotaMiddlewareDefersWebRTCBootstrapUntilModelValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := sdkaccess.NewManager()
	manager.SetProviders([]sdkaccess.Provider{apiKeyPolicyAccessProvider{provider: sdkaccess.DefaultAccessProviderName, principal: "webrtc-quota-key"}})
	service := newAPIKeyPolicyMiddlewareService(t)
	identity, _ := apikeypolicy.NewAuthenticatedAPIKeyIdentity("webrtc-quota-key")
	requestLimit := int64(1)
	if _, err := service.Create(context.Background(), identity, "WebRTC", apikeypolicy.ProfileInput{Name: "default"}, &apikeypolicy.QuotaInput{Enabled: true, Requests: &requestLimit}); err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.Use(AuthMiddleware(manager, service), apiKeyQuotaMiddleware(service))
	router.POST("/v1/realtime/calls", func(c *gin.Context) {
		decision, _ := apikeypolicy.DecisionFromContext(c.Request.Context())
		if _, charged := decision.QuotaAttribution(); charged {
			t.Fatal("middleware charged WebRTC before model validation")
		}
		if _, err := apikeypolicy.AdmitQuotaTurn(c.Request.Context()); err != nil {
			t.Fatalf("deferred WebRTC admission: %v", err)
		}
		c.Status(http.StatusNoContent)
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/realtime/calls", nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestRealtimeClientSecretReDecidesAtEveryConnectionAndFreezesConnectedSnapshot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := newAPIKeyPolicyMiddlewareService(t)
	identity, err := apikeypolicy.NewAuthenticatedAPIKeyIdentity("realtime-policy-key")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := service.Create(context.Background(), identity, "Realtime", apikeypolicy.ProfileInput{
		Name: "first", Providers: []string{"codex"}, Models: []string{"first-model"},
	})
	if err != nil {
		t.Fatal(err)
	}
	policy, err = service.CreateProfile(context.Background(), policy.ID, policy.Version, apikeypolicy.ProfileInput{
		Name: "second", Providers: []string{"codex"}, Models: []string{"second-model"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var secondProfileID string
	for _, profile := range policy.Profiles {
		if profile.Name == "second" {
			secondProfileID = profile.ID
		}
	}
	if secondProfileID == "" {
		t.Fatal("second profile missing")
	}

	liveHandler := codexlive.NewHandler(nil, nil)
	t.Cleanup(liveHandler.Close)
	issuerDecision, err := service.Decide(identity)
	if err != nil {
		t.Fatal(err)
	}
	issuer := gin.New()
	issuer.Use(func(c *gin.Context) {
		ctx := apikeypolicy.WithIdentity(c.Request.Context(), identity)
		ctx = apikeypolicy.WithDecision(ctx, issuerDecision)
		c.Request = c.Request.WithContext(ctx)
		c.Set("userApiKey", "realtime-policy-key")
		c.Set("accessProvider", sdkaccess.DefaultAccessProviderName)
		c.Next()
	})
	issuer.POST("/secret", liveHandler.CreateClientSecret)
	secretRecorder := httptest.NewRecorder()
	issuer.ServeHTTP(secretRecorder, httptest.NewRequest(http.MethodPost, "/secret", strings.NewReader(`{"session":{"type":"realtime","model":"first-model"}}`)))
	if secretRecorder.Code != http.StatusOK {
		t.Fatalf("secret status=%d body=%s", secretRecorder.Code, secretRecorder.Body.String())
	}
	var secret struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(secretRecorder.Body.Bytes(), &secret); err != nil || secret.Value == "" {
		t.Fatalf("secret=%#v error=%v", secret, err)
	}

	policy, err = service.ActivateProfile(context.Background(), policy.ID, secondProfileID, policy.Version)
	if err != nil {
		t.Fatal(err)
	}
	var connected []apikeypolicy.RequestPolicyDecision
	connections := gin.New()
	connections.Use(realtimeAuthMiddleware(nil, service, liveHandler))
	connections.GET("/connect", func(c *gin.Context) {
		decision, ok := apikeypolicy.DecisionFromContext(c.Request.Context())
		if !ok {
			t.Fatal("connection has no policy decision")
		}
		connected = append(connected, decision)
		c.Status(http.StatusNoContent)
	})
	connect := func() *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/connect", nil)
		request.Header.Set("Authorization", "Bearer "+secret.Value)
		connections.ServeHTTP(recorder, request)
		return recorder
	}
	firstConnection := connect()
	if firstConnection.Code != http.StatusNoContent || len(connected) != 1 || connected[0].Snapshot == nil || connected[0].Snapshot.ProfileName != "second" {
		t.Fatalf("first connection status=%d decisions=%#v", firstConnection.Code, connected)
	}

	firstProfileID := ""
	for _, profile := range policy.Profiles {
		if profile.Name == "first" {
			firstProfileID = profile.ID
		}
	}
	if _, err = service.ActivateProfile(context.Background(), policy.ID, firstProfileID, policy.Version); err != nil {
		t.Fatal(err)
	}
	if _, err = connected[0].ApplyModel("second-model"); err != nil {
		t.Fatalf("established connection snapshot changed after active switch: %v", err)
	}
	if _, err = connected[0].ApplyModel("first-model"); err == nil {
		t.Fatal("established connection unexpectedly adopted the new active profile")
	}

	reconnected := connect()
	if reconnected.Code != http.StatusNoContent || len(connected) != 2 || connected[1].Snapshot == nil || connected[1].Snapshot.ProfileName != "first" {
		t.Fatalf("reconnect status=%d decisions=%#v", reconnected.Code, connected)
	}

	service.MarkUnavailable()
	unavailable := connect()
	if unavailable.Code != http.StatusServiceUnavailable || !strings.Contains(unavailable.Body.String(), `"code":"api_key_policy_unavailable"`) {
		t.Fatalf("unavailable status=%d body=%s", unavailable.Code, unavailable.Body.String())
	}
}

func TestDisabledAPIKeyEnforcementFollowsTakeover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := newAPIKeyPolicyMiddlewareService(t)
	identity, err := apikeypolicy.NewAuthenticatedAPIKeyIdentity("disabled-request-key")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SetTakeover(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	manager := sdkaccess.NewManager()
	manager.SetProviders([]sdkaccess.Provider{apiKeyPolicyAccessProvider{provider: sdkaccess.DefaultAccessProviderName, principal: "disabled-request-key"}})
	router := gin.New()
	router.Use(AuthMiddleware(manager, service))
	router.GET("/test", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	if err := service.SetKeyDisabled(context.Background(), identity, true, false); err != nil {
		t.Fatal(err)
	}
	for _, takeover := range []bool{false, true, false, true} {
		if err := service.SetTakeover(context.Background(), takeover); err != nil {
			t.Fatal(err)
		}
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/test", nil))
		if takeover {
			if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "api_key_disabled") {
				t.Fatalf("disabled response: %d %s", recorder.Code, recorder.Body.String())
			}
		} else if recorder.Code != http.StatusNoContent {
			t.Fatalf("stopped takeover response: %d", recorder.Code)
		}
	}
	if err := service.SetKeyDisabled(context.Background(), identity, false, true); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/test", nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("enabled response: %d", recorder.Code)
	}
}
