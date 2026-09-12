package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
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

func TestAPIKeyConcurrencyMiddlewareHoldsRequestsAndReleasesOnExit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, method, path string
		panicExit          bool
	}{
		{"stream cancellation", http.MethodPost, "/v1/chat/completions", false},
		{"websocket connection", http.MethodGet, "/v1/responses", false},
		{"panic", http.MethodPost, "/v1/messages", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newAPIKeyPolicyMiddlewareService(t)
			identity, _ := apikeypolicy.NewAuthenticatedAPIKeyIdentity("middleware-concurrent-key")
			if err := s.SetKeyConcurrencyLimit(context.Background(), identity, 1, 0); err != nil {
				t.Fatal(err)
			}
			manager := sdkaccess.NewManager()
			manager.SetProviders([]sdkaccess.Provider{apiKeyPolicyAccessProvider{provider: sdkaccess.DefaultAccessProviderName, principal: "middleware-concurrent-key"}})
			entered, finished := make(chan struct{}), make(chan struct{})
			router := gin.New()
			router.Use(gin.Recovery(), AuthMiddleware(manager, s), apiKeyQuotaMiddleware(s))
			router.GET("/v1/models", func(c *gin.Context) { c.Status(200) })
			router.Handle(tc.method, tc.path, func(c *gin.Context) {
				if c.GetHeader("X-Test-Hold") == "true" {
					if !tc.panicExit {
						c.Header("Content-Type", "text/event-stream")
						c.Writer.Flush()
					}
					close(entered)
					<-c.Request.Context().Done()
					if tc.panicExit {
						panic("test handler failure")
					}
				}
				c.Status(200)
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			request := httptest.NewRequest(tc.method, tc.path, nil).WithContext(ctx)
			request.Header.Set("X-Test-Hold", "true")
			if tc.method == http.MethodGet {
				request.Header.Set("Upgrade", "websocket")
			}
			go func() { defer close(finished); router.ServeHTTP(httptest.NewRecorder(), request) }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("request did not enter")
			}
			blocked := httptest.NewRecorder()
			router.ServeHTTP(blocked, httptest.NewRequest(tc.method, tc.path, nil))
			if blocked.Code != 429 || !strings.Contains(blocked.Body.String(), "api_key_concurrency_exceeded") {
				t.Fatalf("limit: %d %s", blocked.Code, blocked.Body.String())
			}
			discovery := httptest.NewRecorder()
			router.ServeHTTP(discovery, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
			if discovery.Code != 200 {
				t.Fatalf("discovery consumed slot: %d", discovery.Code)
			}
			cancel()
			select {
			case <-finished:
			case <-time.After(5 * time.Second):
				t.Fatal("handler did not exit")
			}
			next := httptest.NewRecorder()
			router.ServeHTTP(next, httptest.NewRequest(tc.method, tc.path, nil))
			if next.Code != 200 {
				t.Fatalf("slot leaked: %d %s", next.Code, next.Body.String())
			}
		})
	}
}

func TestAPIKeyConcurrencyRejectsBeforeQuotaAndReleasesQuotaFailures(t *testing.T) {
	s := newAPIKeyPolicyMiddlewareService(t)
	identity, _ := apikeypolicy.NewAuthenticatedAPIKeyIdentity("concurrency-quota-key")
	budget := int64(1)
	_, err := s.Create(context.Background(), identity, "Quota", apikeypolicy.ProfileInput{Name: "Default"}, &apikeypolicy.QuotaInput{Enabled: true, Requests: &budget})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetKeyConcurrencyLimit(context.Background(), identity, 1, 0); err != nil {
		t.Fatal(err)
	}
	manager := sdkaccess.NewManager()
	manager.SetProviders([]sdkaccess.Provider{apiKeyPolicyAccessProvider{provider: sdkaccess.DefaultAccessProviderName, principal: "concurrency-quota-key"}})
	router := gin.New()
	router.Use(AuthMiddleware(manager, s), apiKeyQuotaMiddleware(s))
	router.POST("/v1/responses", func(c *gin.Context) { c.Status(200) })
	call := func() *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		router.ServeHTTP(r, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
		return r
	}
	release, err := s.AcquireKeyRequest(identity)
	if err != nil {
		t.Fatal(err)
	}
	if got := call(); got.Code != 429 || !strings.Contains(got.Body.String(), "api_key_concurrency_exceeded") {
		t.Fatalf("limit: %d %s", got.Code, got.Body.String())
	}
	release()
	if got := call(); got.Code != 200 {
		t.Fatalf("rejected request consumed quota: %d %s", got.Code, got.Body.String())
	}
	for i := 0; i < 2; i++ {
		if got := call(); got.Code != 429 || !strings.Contains(got.Body.String(), "api_key_quota_exceeded") {
			t.Fatalf("quota rejection leaked slot: %d %s", got.Code, got.Body.String())
		}
	}
}

func TestAPIKeyConcurrencyRealWebSocketCloseReleasesSlot(t *testing.T) {
	s := newAPIKeyPolicyMiddlewareService(t)
	identity, _ := apikeypolicy.NewAuthenticatedAPIKeyIdentity("concurrent-websocket-key")
	if err := s.SetKeyConcurrencyLimit(context.Background(), identity, 1, 0); err != nil {
		t.Fatal(err)
	}
	manager := sdkaccess.NewManager()
	manager.SetProviders([]sdkaccess.Provider{apiKeyPolicyAccessProvider{provider: sdkaccess.DefaultAccessProviderName, principal: "concurrent-websocket-key"}})
	router := gin.New()
	finished := make(chan struct{}, 2)
	// This outer middleware signals only after the quota/concurrency defer runs.
	router.Use(func(c *gin.Context) { c.Next(); finished <- struct{}{} }, AuthMiddleware(manager, s), apiKeyQuotaMiddleware(s))
	router.GET("/v1/responses", func(c *gin.Context) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})
	server := httptest.NewServer(router)
	defer server.Close()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
	first, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, response, err := websocket.DefaultDialer.Dial(url, nil)
	if second != nil {
		_ = second.Close()
	}
	if response != nil {
		defer response.Body.Close()
	}
	if err == nil || response == nil || response.StatusCode != 429 {
		t.Fatalf("saturated websocket: response=%v err=%v", response, err)
	}
	<-finished // Rejected handshake.
	_ = first.Close()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("socket handler did not release")
	}
	next, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = next.Close()
}
