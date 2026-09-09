package management

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	proinspection "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/inspection"
	proquota "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/quota"
)

func TestAntigravityInspectionBindsPreparedTokenToAutomaticAction(t *testing.T) {
	for _, tc := range []struct {
		name             string
		remaining        float64
		disabled         bool
		validToken       bool
		tokenAliases     string
		refreshFails     bool
		reauthenticate   bool
		refreshMutation  string
		transientRefresh bool
		timeoutRefresh   bool
	}{
		{name: "weekly exhausted", remaining: 0},
		{name: "camelCase refreshed exhausted", remaining: 0, tokenAliases: "camel"},
		{name: "camelCase refreshed healthy", remaining: 0.9, tokenAliases: "camel"},
		{name: "camelCase refreshed recovery", remaining: 0.9, disabled: true, tokenAliases: "camel"},
		{name: "camelCase valid", remaining: 0, validToken: true, tokenAliases: "camel"},
		{name: "mixed aliases refreshed", remaining: 0, tokenAliases: "mixed"},
		{name: "mixed aliases valid", remaining: 0, validToken: true, tokenAliases: "mixed"},
		{name: "fractional remaining exceeds threshold", remaining: 0.0001},
		{name: "recovery with deep probe", remaining: 0.9, disabled: true},
		{name: "valid token needs no refresh", remaining: 0, validToken: true},
		{name: "refresh failure keeps account", remaining: 0, refreshFails: true},
		{name: "reauthentication during probe rejects action", remaining: 0, reauthenticate: true},
		{name: "credential upload during refresh", remaining: 0, refreshMutation: "credentials"},
		{name: "refresh token replacement during refresh", remaining: 0, refreshMutation: "refresh-token"},
		{name: "recreated account during refresh", remaining: 0, refreshMutation: "recreate"},
		{name: "concurrent note survives refresh", remaining: 0, refreshMutation: "note"},
		{name: "transient refresh is retried", remaining: 0.9, transientRefresh: true},
		{name: "refresh honors configured timeout", remaining: 0, timeoutRefresh: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			manager := coreauth.NewManager(nil, nil, nil)
			expiry := time.Now().Add(-time.Hour)
			if tc.validToken {
				expiry = time.Now().Add(time.Hour)
			}
			metadata := map[string]any{
				"access_token": "old-test-token", "refresh_token": "test-refresh-token",
				"expired": expiry.Format(time.RFC3339),
			}
			if tc.tokenAliases == "camel" {
				delete(metadata, "access_token")
				metadata["accessToken"] = "old-test-token"
			} else if tc.tokenAliases == "mixed" {
				metadata["accessToken"] = "stale-alias-test-token"
			}
			registered, err := manager.Register(ctx, &coreauth.Auth{
				ID: "antigravity-prepared-token", FileName: "prepared-token.json", Provider: "antigravity",
				Disabled: tc.disabled,
				Metadata: metadata,
			})
			if err != nil {
				t.Fatal(err)
			}
			var refreshes, probes, deepProbes atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/token" {
					n := refreshes.Add(1)
					if tc.refreshMutation != "" {
						current, _ := manager.GetByID(registered.ID)
						switch tc.refreshMutation {
						case "credentials", "recreate":
							current.Metadata["access_token"] = "uploaded-test-token"
							current.Metadata["refresh_token"] = "uploaded-refresh-token"
						case "refresh-token":
							current.Metadata["refresh_token"] = "uploaded-refresh-token"
						case "note":
							current.Metadata["note"] = "concurrent note"
						}
						if tc.refreshMutation == "recreate" {
							if _, err := manager.Register(ctx, current); err != nil {
								t.Error(err)
							}
						} else if err := (&Handler{authManager: manager}).upsertAuthRecord(ctx, current); err != nil {
							t.Error(err)
						}
					}
					if tc.transientRefresh && n == 1 {
						w.WriteHeader(http.StatusServiceUnavailable)
						_, _ = w.Write([]byte(`{"error":"temporarily_unavailable"}`))
						return
					}
					if tc.timeoutRefresh {
						select {
						case <-r.Context().Done():
							return
						case <-time.After(4 * time.Second):
						}
					}
					if tc.refreshFails {
						w.WriteHeader(http.StatusBadRequest)
						_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
						return
					}
					// The refreshed token is inside the resolver's refresh skew.
					// Later quota/subscription/deep-probe calls must not refresh it again.
					_, _ = w.Write([]byte(`{"access_token":"prepared-test-token","expires_in":1}`))
					return
				}
				probes.Add(1)
				wantToken := "Bearer prepared-test-token"
				if tc.validToken {
					wantToken = "Bearer old-test-token"
				}
				if r.Header.Get("Authorization") != wantToken {
					t.Error("request did not use the token bound to the observation")
					w.WriteHeader(http.StatusUnauthorized)
					_, _ = w.Write([]byte(`{"error":"invalid_token"}`))
					return
				}
				switch r.URL.Path {
				case "/v1internal:retrieveUserQuotaSummary":
					if tc.reauthenticate {
						current, _ := manager.GetByID(registered.ID)
						current.Metadata["access_token"] = "reauthenticated-test-token"
						if _, err := manager.Update(ctx, current); err != nil {
							t.Error(err)
						}
					}
					_, _ = fmt.Fprintf(w, `{"groups":[{"displayName":"GEMINI Models","buckets":[{"bucketId":"gemini-weekly","window":"weekly","remainingFraction":%g}]},{"displayName":"Claude and GPT models","buckets":[{"remainingFraction":0.99}]}]}`, tc.remaining)
				case "/v1internal:loadCodeAssist":
					_, _ = w.Write([]byte(`{"paidTier":{"id":"g1-pro-tier"}}`))
				case "/v1internal:generateContent":
					deepProbes.Add(1)
					_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"OK"}]}}]}`))
				default:
					t.Errorf("unexpected request path %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			transport := server.Client().Transport.(*http.Transport).Clone()
			transport.TLSClientConfig.ServerName = "example.com"
			transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}
			oldTransport, oldTokenURL := http.DefaultTransport, antigravityOAuthTokenURL
			http.DefaultTransport, antigravityOAuthTokenURL = transport, server.URL+"/token"
			defer func() {
				http.DefaultTransport, antigravityOAuthTokenURL = oldTransport, oldTokenURL
				transport.CloseIdleConnections()
			}()
			scheduler := &accountInspectionScheduler{h: &Handler{authManager: manager}}
			settings := proinspection.DefaultSettings()
			settings.UsedPercentThreshold = 95
			settings.AntigravityQuotaMode = accountInspectionAntigravityQuotaModeMaxUsed
			settings.AntigravityDeepProbeEnabled = true
			settings.AutoExecuteQuotaLimitDisable = true
			settings.AutoExecuteQuotaRecoveryEnable = true
			settings.AutoExecuteRequestErrorAction = accountInspectionActionDisable
			if tc.transientRefresh {
				settings.Retries = 1
			}
			if tc.timeoutRefresh {
				settings.Timeout = 3000
				settings.Retries = 0
			}
			started := time.Now()
			result := scheduler.inspectAccount(ctx, accountFromAuth(registered), settings)
			if tc.refreshMutation != "" && tc.refreshMutation != "note" {
				results := []accountInspectionResult{result}
				scheduler.applyAutomaticActions(ctx, results, settings)
				current, _ := manager.GetByID(registered.ID)
				if result.ErrorCode != "inspection_identity_changed" || probes.Load() != 0 || results[0].Executed || current.Disabled || current.Metadata["refresh_token"] != "uploaded-refresh-token" {
					t.Fatalf("credential replacement: code:%s probes:%d executed:%v disabled:%v", result.ErrorCode, probes.Load(), results[0].Executed, current.Disabled)
				}
				return
			}
			if tc.timeoutRefresh {
				if result.Error == "" || probes.Load() != 0 || time.Since(started) >= 4*time.Second || refreshes.Load() != 1 {
					t.Fatalf("timeout: error:%s probes:%d elapsed:%s attempts:%d", result.Error, probes.Load(), time.Since(started), refreshes.Load())
				}
				return
			}
			if tc.refreshFails {
				if result.TokenRefreshStatus != "failed" || result.ErrorCode != "token_refresh_error" || probes.Load() != 0 {
					t.Fatalf("refresh failure = status:%s code:%s probes:%d", result.TokenRefreshStatus, result.ErrorCode, probes.Load())
				}
				return
			}
			wantRefreshes := int32(1)
			if tc.transientRefresh {
				wantRefreshes = 2
			}
			if tc.validToken {
				wantRefreshes = 0
			}
			if refreshes.Load() != wantRefreshes || result.TokenRefreshTriggered != !tc.validToken {
				t.Fatalf("refreshes/triggered = %d/%v", refreshes.Load(), result.TokenRefreshTriggered)
			}
			if !tc.validToken && result.TokenRefreshStatus != "success" {
				t.Fatalf("refresh status = %s", result.TokenRefreshStatus)
			}
			if result.Error != "" || result.UsedPercent == nil || result.IsQuota != (tc.remaining <= 0.05) {
				t.Fatalf("unexpected quota result: used:%v quota:%v error:%s", result.UsedPercent, result.IsQuota, result.Error)
			}
			if tc.disabled && (deepProbes.Load() != 1 || result.DeepProbeStatus != "success") {
				t.Fatalf("deep probes/status = %d/%s", deepProbes.Load(), result.DeepProbeStatus)
			}
			results := []accountInspectionResult{result}
			scheduler.applyAutomaticActions(ctx, results, settings)
			current, _ := manager.GetByID(registered.ID)
			if tc.refreshMutation == "note" && current.Metadata["note"] != "concurrent note" {
				t.Fatal("concurrent note was overwritten")
			}
			if tc.reauthenticate {
				if results[0].Executed || results[0].ExecuteError != errAccountInspectionResultStale.Error() || current.Disabled {
					t.Fatalf("stale action: executed:%v error:%s disabled:%v", results[0].Executed, results[0].ExecuteError, current.Disabled)
				}
				return
			}
			if tc.remaining > 0.05 && !tc.disabled {
				if results[0].Executed || current.Disabled || result.Error != "" {
					t.Fatal("inspection disabled a healthy account")
				}
				return
			}
			if !results[0].Executed || results[0].ExecuteError != "" || current.Disabled != !tc.disabled {
				t.Fatalf("automatic action: executed:%v error:%s disabled:%v", results[0].Executed, results[0].ExecuteError, current.Disabled)
			}
		})
	}
}

func TestAccountInspectionDeepProbesUnknownXAIQuota(t *testing.T) {
	decision := accountInspectionDecision{Action: accountInspectionActionKeep}
	if !proinspection.ShouldDeepProbe(decision) {
		t.Fatal("unknown xAI quota should allow an explicitly enabled deep probe")
	}
}

func TestAntigravityQuotaURLsUseSummaryEndpoint(t *testing.T) {
	for _, url := range antigravityQuotaURLs() {
		if !strings.Contains(url, "retrieveUserQuotaSummary") {
			t.Fatalf("antigravity quota url = %q, want retrieveUserQuotaSummary", url)
		}
	}
}

func TestXAIRequestHeadersIncludeGrokClientAndUserID(t *testing.T) {
	auth := &coreauth.Auth{
		Provider:   "xai",
		Attributes: map[string]string{"using_api": "false"},
		Metadata:   map[string]any{"sub": "user-123"},
	}
	headers := xaiRequestHeaders(auth)
	if headers["X-Xai-Token-Auth"] != "xai-grok-cli" {
		t.Fatalf("x-xai-token-auth = %q", headers["X-Xai-Token-Auth"])
	}
	clientVersion := headers["X-Grok-Client-Version"]
	if clientVersion == "" {
		t.Fatal("x-grok-client-version is empty")
	}
	if headers["User-Agent"] != "xai-grok-workspace/"+clientVersion {
		t.Fatalf("User-Agent = %q", headers["User-Agent"])
	}
	if headers["x-userid"] != "user-123" {
		t.Fatalf("x-userid = %q, want user-123", headers["x-userid"])
	}
}

func TestXAIInspectionUsesExecutorHTTPRequest(t *testing.T) {
	if !accountInspectionShouldUseExecutorHTTPRequest(&coreauth.Auth{Provider: "xai"}) {
		t.Fatal("accountInspectionShouldUseExecutorHTTPRequest(xai) = false, want true")
	}
}

func TestXAIInspectionRoutesByUsingAPI(t *testing.T) {
	tests := []struct {
		name          string
		usingAPI      string
		wantURLs      []string
		forbiddenPath string
	}{
		{
			name:          "official api",
			usingAPI:      "true",
			wantURLs:      []string{"https://api.x.ai/v1/chat/completions"},
			forbiddenPath: "/billing",
		},
		{
			name:     "grok cli",
			usingAPI: "false",
			wantURLs: []string{
				"https://cli-chat-proxy.grok.com/v1/billing?format=credits",
				"https://cli-chat-proxy.grok.com/v1/billing",
				"https://cli-chat-proxy.grok.com/v1/responses",
			},
			forbiddenPath: "/chat/completions",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor := &xaiInspectionRoutingExecutor{}
			manager := coreauth.NewManager(nil, nil, nil)
			manager.RegisterExecutor(executor)
			scheduler := &accountInspectionScheduler{h: &Handler{authManager: manager}}
			auth := &coreauth.Auth{
				Provider: "xai",
				Attributes: map[string]string{
					"api_key":   "test-token",
					"base_url":  "https://api.x.ai/v1",
					"using_api": tt.usingAPI,
				},
			}
			decision, status, err := scheduler.inspectXAI(context.Background(), accountInspectionAccount{
				Auth:      auth,
				Provider:  "xai",
				FileName:  tt.name + ".json",
				AuthIndex: tt.name,
			}, accountInspectionSettings{Timeout: 3_000, UsedPercentThreshold: 100, XAIDeepProbeModel: "grok-4.5"})
			if err != nil || status == nil || *status != http.StatusOK || decision.Action != accountInspectionActionKeep {
				t.Fatalf("inspectXAI() = decision:%#v status:%v err:%v", decision, status, err)
			}
			gotURLs := make([]string, 0, len(executor.requests))
			for _, request := range executor.requests {
				gotURLs = append(gotURLs, request.URL.String())
				if strings.Contains(request.URL.String(), tt.forbiddenPath) {
					t.Fatalf("inspectXAI() requested forbidden URL %q", request.URL.String())
				}
			}
			if strings.Join(gotURLs, "\n") != strings.Join(tt.wantURLs, "\n") {
				t.Fatalf("inspectXAI() URLs = %#v, want %#v", gotURLs, tt.wantURLs)
			}
		})
	}
}

func TestXAICLIFreeQuotaProbeRunsWithoutDeepProbeAndAffectsSameRun(t *testing.T) {
	executor := &xaiInspectionRoutingExecutor{}
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)
	scheduler := &accountInspectionScheduler{h: &Handler{authManager: manager}}
	decision, status, err := scheduler.inspectXAI(context.Background(), accountInspectionAccount{
		Auth: &coreauth.Auth{Provider: "xai", Attributes: map[string]string{
			"api_key": "test-token", "using_api": "false",
		}},
		Provider: "xai", FileName: "free.json", AuthIndex: "free",
	}, accountInspectionSettings{
		Timeout: 3_000, UsedPercentThreshold: 25, XAIDeepProbeModel: "grok-4.5",
	})
	if err != nil || status == nil || *status != http.StatusOK {
		t.Fatalf("inspectXAI() = decision:%#v status:%v err:%v", decision, status, err)
	}
	if decision.Action != accountInspectionActionDisable || !decision.IsQuota || decision.UsedPercent == nil || *decision.UsedPercent != 30 {
		t.Fatalf("same-run free quota decision = %#v, want 30%% quota disable", decision)
	}
	if decision.DeepProbeStatus != "" {
		t.Fatalf("free quota sampling unexpectedly reported deep probe status %q", decision.DeepProbeStatus)
	}
	responses := 0
	for _, request := range executor.requests {
		if strings.HasSuffix(request.URL.Path, "/responses") {
			responses++
		}
	}
	if responses != 1 {
		t.Fatalf("responses requests = %d, want 1", responses)
	}
}

func TestXAICLIFreeQuotaProbeDoesNotRetryForDeepBodyWhenDeepProbeDisabled(t *testing.T) {
	executor := &xaiInspectionRoutingExecutor{
		responsesBody: `data: {"type":"response.created"}` + "\n\n",
	}
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)
	scheduler := &accountInspectionScheduler{h: &Handler{authManager: manager}}
	decision, _, err := scheduler.inspectXAI(context.Background(), accountInspectionAccount{
		Auth: &coreauth.Auth{Provider: "xai", Attributes: map[string]string{
			"api_key": "test-token", "using_api": "false",
		}},
		Provider: "xai", FileName: "free-nonterminal.json", AuthIndex: "free-nonterminal",
	}, accountInspectionSettings{
		Timeout: 3_000, UsedPercentThreshold: 100, XAIDeepProbeModel: "grok-4.5",
	})
	if err != nil || decision.UsedPercent == nil || *decision.UsedPercent != 30 {
		t.Fatalf("quota-only inspectXAI() = decision:%#v err:%v", decision, err)
	}
	responses := 0
	for _, request := range executor.requests {
		if strings.HasSuffix(request.URL.Path, "/responses") {
			responses++
		}
	}
	if responses != 1 {
		t.Fatalf("quota-only responses requests = %d, want 1 despite non-terminal body", responses)
	}
}

func TestXAICLIFreeQuotaProbeReusesResponseWhenDeepProbeEnabled(t *testing.T) {
	executor := &xaiInspectionRoutingExecutor{}
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)
	scheduler := &accountInspectionScheduler{h: &Handler{authManager: manager}}
	decision, _, err := scheduler.inspectXAI(context.Background(), accountInspectionAccount{
		Auth: &coreauth.Auth{Provider: "xai", Attributes: map[string]string{
			"api_key": "test-token", "using_api": "false",
		}},
		Provider: "xai", FileName: "free-deep.json", AuthIndex: "free-deep",
	}, accountInspectionSettings{
		Timeout: 3_000, UsedPercentThreshold: 100, XAIDeepProbeEnabled: true, XAIDeepProbeModel: "grok-4.5",
	})
	if err != nil || decision.DeepProbeStatus != accountInspectionDeepProbeSuccess {
		t.Fatalf("inspectXAI() = decision:%#v err:%v, want reused deep-probe success", decision, err)
	}
	responses := 0
	for _, request := range executor.requests {
		if strings.HasSuffix(request.URL.Path, "/responses") {
			responses++
		}
	}
	if responses != 1 {
		t.Fatalf("responses requests = %d, want one shared quota/deep probe", responses)
	}
}

func TestXAICLIFreeQuotaProbeAcceptsExhaustionWithoutDeepProbe(t *testing.T) {
	executor := &xaiInspectionRoutingExecutor{
		responsesStatus: http.StatusTooManyRequests,
		responsesBody:   `{"code":"subscription:free-usage-exhausted","error":"used all the included free usage for model grok-4.5, tokens (actual/limit): 1000/1000"}`,
	}
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)
	scheduler := &accountInspectionScheduler{h: &Handler{authManager: manager}}
	decision, status, err := scheduler.inspectXAI(context.Background(), accountInspectionAccount{
		Auth: &coreauth.Auth{Provider: "xai", Attributes: map[string]string{
			"api_key": "test-token", "using_api": "false",
		}},
		Provider: "xai", FileName: "exhausted.json", AuthIndex: "exhausted",
	}, accountInspectionSettings{Timeout: 3_000, UsedPercentThreshold: 100, XAIDeepProbeModel: "grok-4.5"})
	if err != nil || status == nil || *status != http.StatusTooManyRequests {
		t.Fatalf("inspectXAI() = decision:%#v status:%v err:%v", decision, status, err)
	}
	if decision.Action != accountInspectionActionDisable || !decision.IsQuota || decision.UsedPercent == nil || *decision.UsedPercent != 100 {
		t.Fatalf("exhausted free quota decision = %#v", decision)
	}
}

func TestXAIPlanTypePrefersAccessTokenTier(t *testing.T) {
	for tier, want := range map[int]string{
		0: "free", 1: "supergrok", 2: "x-basic", 3: "x-premium", 4: "x-premium-plus",
		5: "supergrok-heavy", 6: "supergrok-lite", 9: "paid-unknown",
	} {
		token := "header." + base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"tier":%d}`, tier))) + ".signature"
		plan, ok := xaiPlanTypeFromAccessToken(&coreauth.Auth{Metadata: map[string]any{"access_token": token}})
		if !ok || plan != want {
			t.Fatalf("xaiPlanTypeFromAccessToken(tier=%d) = %q, %v; want %q, true", tier, plan, ok, want)
		}
	}
}

func TestXAICLIPaidAccessTokenTierSkipsFreeQuotaProbe(t *testing.T) {
	token := "header." + base64.RawURLEncoding.EncodeToString([]byte(`{"tier":5}`)) + ".signature"
	executor := &xaiInspectionRoutingExecutor{}
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)
	scheduler := &accountInspectionScheduler{h: &Handler{authManager: manager}}
	decision, _, err := scheduler.inspectXAI(context.Background(), accountInspectionAccount{
		Auth: &coreauth.Auth{
			Provider:   "xai",
			Attributes: map[string]string{"using_api": "false"},
			Metadata:   map[string]any{"access_token": token},
		},
		Provider: "xai", FileName: "heavy.json", AuthIndex: "heavy",
	}, accountInspectionSettings{Timeout: 3_000, UsedPercentThreshold: 100, XAIDeepProbeModel: "grok-4.5"})
	if err != nil || decision.Action != accountInspectionActionKeep {
		t.Fatalf("paid inspectXAI() = decision:%#v err:%v", decision, err)
	}
	for _, request := range executor.requests {
		if strings.HasSuffix(request.URL.Path, "/responses") {
			t.Fatalf("paid tier unexpectedly requested free quota URL %q", request.URL.String())
		}
	}
}

func TestXAIPlanTierDoesNotMaskBillingUnauthorized(t *testing.T) {
	token := "header." + base64.RawURLEncoding.EncodeToString([]byte(`{"tier":5}`)) + ".signature"
	executor := &xaiInspectionRoutingExecutor{
		billingStatus: http.StatusUnauthorized,
		billingBody:   `{"error":"invalid token"}`,
	}
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)
	scheduler := &accountInspectionScheduler{h: &Handler{authManager: manager}}
	decision, status, err := scheduler.inspectXAI(context.Background(), accountInspectionAccount{
		Auth: &coreauth.Auth{
			Provider:   "xai",
			Attributes: map[string]string{"using_api": "false"},
			Metadata:   map[string]any{"access_token": token},
		},
		Provider: "xai", FileName: "unauthorized.json", AuthIndex: "unauthorized",
	}, accountInspectionSettings{Timeout: 3_000, UsedPercentThreshold: 100, XAIDeepProbeModel: "grok-4.5"})
	if err != nil || status == nil || *status != http.StatusUnauthorized {
		t.Fatalf("unauthorized inspectXAI() = decision:%#v status:%v err:%v", decision, status, err)
	}
	if decision.Action != accountInspectionActionDisable || decision.IsQuota {
		t.Fatalf("JWT tier masked billing authorization failure: %#v", decision)
	}
	for _, request := range executor.requests {
		if strings.HasSuffix(request.URL.Path, "/responses") {
			t.Fatalf("billing authorization failure unexpectedly continued to %q", request.URL.String())
		}
	}
}

func TestXAIBillingURLMatchesUpstreamQuotaConfig(t *testing.T) {
	if got := xaiBillingURL(); got != "https://cli-chat-proxy.grok.com/v1/billing" {
		t.Fatalf("xaiBillingURL() = %q, want upstream billing endpoint", got)
	}
	if got := xaiBillingWeeklyURL(); got != "https://cli-chat-proxy.grok.com/v1/billing?format=credits" {
		t.Fatalf("xaiBillingWeeklyURL() = %q, want upstream weekly billing endpoint", got)
	}
}

func TestXAIDeepProbeDefaultsAndNormalization(t *testing.T) {
	defaults := proinspection.DefaultSettings()
	if defaults.XAIDeepProbeEnabled {
		t.Fatal("xAI deep probe should be disabled by default")
	}
	if defaults.XAIDeepProbeModel != "grok-4.5" {
		t.Fatalf("default xAI deep probe model = %q, want grok-4.5", defaults.XAIDeepProbeModel)
	}

	normalized := normalizeAccountInspectionSchedule(accountInspectionSchedule{Settings: accountInspectionSettings{
		XAIDeepProbeEnabled: true,
		XAIDeepProbeModel:   "   ",
	}})
	if !normalized.Settings.XAIDeepProbeEnabled || normalized.Settings.XAIDeepProbeModel != "grok-4.5" {
		t.Fatalf("normalized xAI deep probe settings = enabled:%v model:%q", normalized.Settings.XAIDeepProbeEnabled, normalized.Settings.XAIDeepProbeModel)
	}
}

func TestXAIResponsesURLUsesConfiguredBaseURL(t *testing.T) {
	if got := xaiResponsesURL(nil); got != "https://api.x.ai/v1/responses" {
		t.Fatalf("xaiResponsesURL(nil) = %q", got)
	}
	oauth := &coreauth.Auth{Attributes: map[string]string{"base_url": "https://api.x.ai/v1", "auth_kind": "oauth"}}
	if got := xaiResponsesURL(oauth); got != "https://cli-chat-proxy.grok.com/v1/responses" {
		t.Fatalf("xaiResponsesURL(oauth) = %q", got)
	}
	api := &coreauth.Auth{Attributes: map[string]string{"base_url": "https://api.x.ai/v1", "using_api": "true"}}
	if got := xaiResponsesURL(api); got != "https://api.x.ai/v1/responses" {
		t.Fatalf("xaiResponsesURL(api) = %q", got)
	}
	if got := xaiOfficialChatURL(api); got != "https://api.x.ai/v1/chat/completions" {
		t.Fatalf("xaiOfficialChatURL(api) = %q", got)
	}
	metadataOAuth := &coreauth.Auth{Metadata: map[string]any{"base_url": "https://api.x.ai/v1", "using_api": false}}
	if got := xaiResponsesURL(metadataOAuth); got != "https://cli-chat-proxy.grok.com/v1/responses" {
		t.Fatalf("xaiResponsesURL(metadataOAuth) = %q", got)
	}
	defaultAPI := &coreauth.Auth{Attributes: map[string]string{"base_url": "https://api.x.ai/v1"}}
	if got := xaiResponsesURL(defaultAPI); got != "https://api.x.ai/v1/responses" {
		t.Fatalf("xaiResponsesURL(defaultAPI) = %q", got)
	}
	auth := &coreauth.Auth{Attributes: map[string]string{"base_url": "https://xai.example/v1/"}}
	if got := xaiResponsesURL(auth); got != "https://xai.example/v1/responses" {
		t.Fatalf("xaiResponsesURL(custom) = %q", got)
	}
	headers := xaiDeepProbeHeaders(oauth)
	if headers["X-Xai-Token-Auth"] != "xai-grok-cli" || headers["Accept"] != "text/event-stream" {
		t.Fatalf("xaiDeepProbeHeaders(oauth) = %#v", headers)
	}
	apiHeaders := xaiDeepProbeHeaders(api)
	if apiHeaders["X-Xai-Token-Auth"] != "" || apiHeaders["Authorization"] != "Bearer $TOKEN$" {
		t.Fatalf("xaiDeepProbeHeaders(api) = %#v", apiHeaders)
	}
	officialHeaders := xaiOfficialAPIHeaders(api)
	if officialHeaders["X-Xai-Token-Auth"] != "" || officialHeaders["Accept"] != "application/json" {
		t.Fatalf("xaiOfficialAPIHeaders() = %#v", officialHeaders)
	}
	customOAuth := &coreauth.Auth{Attributes: map[string]string{"base_url": "https://xai.example/v1", "using_api": "false"}}
	customHeaders := xaiDeepProbeHeaders(customOAuth)
	if customHeaders["X-Xai-Token-Auth"] != "" {
		t.Fatalf("xaiDeepProbeHeaders(customOAuth) = %#v", customHeaders)
	}
}

func TestBuildXAIOfficialHealthRequestAndSummary(t *testing.T) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(proinspection.BuildXAIOfficialHealthBody(" grok-4.5 ")), &payload); err != nil {
		t.Fatalf("proinspection.BuildXAIOfficialHealthBody() JSON error = %v", err)
	}
	messages, _ := payload["messages"].([]any)
	if payload["model"] != "grok-4.5" || len(messages) != 1 || payload["stream"] != false || payload["max_tokens"] != float64(1) {
		t.Fatalf("official health payload = %#v", payload)
	}
	summary := proquota.XAIPaidHealthSummary()
	if summary["mode"] != "paid-health" || summary["planType"] != "paid" || summary["healthStatus"] != "chat-ok" {
		t.Fatalf("paid health summary = %#v", summary)
	}
	if _, exists := summary["freeQuota"]; exists {
		t.Fatalf("paid health summary contains free quota: %#v", summary)
	}
}

func TestXAIOfficialAPIQuotaDecision(t *testing.T) {
	active := xaiOfficialAPIQuotaDecision(accountInspectionAccount{}, `{"error":"credits exhausted"}`)
	if active.Action != accountInspectionActionDisable || !active.IsQuota || !strings.Contains(active.ErrorDetail, "credits exhausted") {
		t.Fatalf("active official quota decision = %#v", active)
	}
	disabled := xaiOfficialAPIQuotaDecision(accountInspectionAccount{Disabled: true}, `{"error":"credits exhausted"}`)
	if disabled.Action != accountInspectionActionKeep || !disabled.IsQuota {
		t.Fatalf("disabled official quota decision = %#v", disabled)
	}
}

func TestBuildXAIDeepProbeBodyUsesMinimalResponsesRequest(t *testing.T) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(proinspection.BuildXAIDeepProbeBody(" grok-4.3 ")), &payload); err != nil {
		t.Fatalf("proinspection.BuildXAIDeepProbeBody() JSON error = %v", err)
	}
	input, _ := payload["input"].([]any)
	if payload["model"] != "grok-4.3" || len(input) != 1 || payload["stream"] != true || payload["store"] != false || payload["max_output_tokens"] != float64(1) {
		t.Fatalf("deep probe payload = %#v", payload)
	}
}

func TestClassifyXAIDeepProbeResponse(t *testing.T) {
	tests := []struct {
		name string
		resp accountInspectionHTTPResult
		want accountInspectionDeepProbeStatus
	}{
		{
			name: "completed sse",
			resp: accountInspectionHTTPResult{StatusCode: http.StatusOK, Body: "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"},
			want: accountInspectionDeepProbeSuccess,
		},
		{
			name: "output capped after successful execution",
			resp: accountInspectionHTTPResult{StatusCode: http.StatusOK, Body: `data: {"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}` + "\n\n"},
			want: accountInspectionDeepProbeSuccess,
		},
		{
			name: "free usage exhausted",
			resp: accountInspectionHTTPResult{StatusCode: http.StatusTooManyRequests, Body: `{"code":"subscription:free-usage-exhausted","error":"You've used all the included free usage for now."}`},
			want: accountInspectionDeepProbeQuota,
		},
		{
			name: "credits exhausted returned as forbidden",
			resp: accountInspectionHTTPResult{StatusCode: http.StatusForbidden, Body: `{"error":{"message":"You have run out of credits or need a Grok subscription. Add credits or upgrade to SuperGrok."}}`},
			want: accountInspectionDeepProbeQuota,
		},
		{
			name: "unauthorized",
			resp: accountInspectionHTTPResult{StatusCode: http.StatusUnauthorized, Body: `{"error":{"message":"invalid token"}}`},
			want: accountInspectionDeepProbeAuthError,
		},
		{
			name: "content filter incomplete response",
			resp: accountInspectionHTTPResult{StatusCode: http.StatusOK, Body: `data: {"type":"response.incomplete","response":{"incomplete_details":{"reason":"content_filter"}}}` + "\n\n"},
			want: accountInspectionDeepProbeTransientError,
		},
		{
			name: "missing terminal response",
			resp: accountInspectionHTTPResult{StatusCode: http.StatusOK, Body: "data: {\"type\":\"response.created\"}\n\n"},
			want: accountInspectionDeepProbeTransientError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := classifyXAIDeepProbeResponse(tt.resp)
			if got != tt.want {
				t.Fatalf("classifyXAIDeepProbeResponse() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClassifyAntigravityDeepProbePrefersQuotaEvidenceOverAuthStatus(t *testing.T) {
	resp := accountInspectionHTTPResult{
		StatusCode: http.StatusForbidden,
		Body:       `{"error":{"status":"RESOURCE_EXHAUSTED","message":"quota exhausted"}}`,
	}
	status, _ := classifyAntigravityDeepProbeResponse(resp)
	if status != accountInspectionDeepProbeQuota {
		t.Fatalf("classifyAntigravityDeepProbeResponse() = %q, want %q", status, accountInspectionDeepProbeQuota)
	}
}

func TestAntigravityDeepProbeFailoverStopsOnDeterministicClientErrors(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound} {
		if !shouldStopAntigravityDeepProbeFailover(status) {
			t.Fatalf("shouldStopAntigravityDeepProbeFailover(%d) = false, want true", status)
		}
	}
	for _, status := range []int{http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable} {
		if shouldStopAntigravityDeepProbeFailover(status) {
			t.Fatalf("shouldStopAntigravityDeepProbeFailover(%d) = true, want false", status)
		}
	}
}

func TestCodexDecisionPrefersQuotaEvidenceOverUnauthorizedStatus(t *testing.T) {
	decision := codexDecision(accountInspectionAccount{}, http.StatusUnauthorized, nil, true, 95)
	if !decision.IsQuota || decision.Action != accountInspectionActionDisable {
		t.Fatalf("codexDecision() = %#v, want quota disable decision", decision)
	}
	if got := proinspection.DecisionErrorCode("codex", decision, testStatusCode(http.StatusUnauthorized)); got != "" {
		t.Fatalf("quota decision error code = %q, want empty", got)
	}
}

func TestRunXAIDeepProbeWithRetryRecoversFromEmptyResponse(t *testing.T) {
	attempts := 0
	resp, status, message, err := runXAIDeepProbeWithRetry(context.Background(), 0, 0, func() (accountInspectionHTTPResult, error) {
		attempts++
		if attempts == 1 {
			return accountInspectionHTTPResult{StatusCode: http.StatusOK}, nil
		}
		return accountInspectionHTTPResult{
			StatusCode: http.StatusOK,
			Body:       `data: {"type":"response.completed","response":{"status":"completed"}}` + "\n\n",
		}, nil
	})
	if err != nil || status != accountInspectionDeepProbeSuccess || message != "" {
		t.Fatalf("runXAIDeepProbeWithRetry() = resp:%+v status:%q message:%q err:%v, want success", resp, status, message, err)
	}
	if attempts != 2 {
		t.Fatalf("runXAIDeepProbeWithRetry() attempts = %d, want 2", attempts)
	}
}

func TestRunXAIDeepProbeWithRetryRecoversFromTransportError(t *testing.T) {
	attempts := 0
	_, status, _, err := runXAIDeepProbeWithRetry(context.Background(), 0, 0, func() (accountInspectionHTTPResult, error) {
		attempts++
		if attempts == 1 {
			return accountInspectionHTTPResult{}, errors.New("temporary transport failure")
		}
		return accountInspectionHTTPResult{
			StatusCode: http.StatusOK,
			Body:       `data: {"type":"response.incomplete","response":{"incomplete_details":{"reason":"max_output_tokens"}}}` + "\n\n",
		}, nil
	})
	if err != nil || status != accountInspectionDeepProbeSuccess || attempts != 2 {
		t.Fatalf("runXAIDeepProbeWithRetry() = status:%q attempts:%d err:%v, want success after 2 attempts", status, attempts, err)
	}
}

func TestRunXAIDeepProbeWithRetryDoesNotRetryContentFilter(t *testing.T) {
	attempts := 0
	_, status, message, err := runXAIDeepProbeWithRetry(context.Background(), 0, 0, func() (accountInspectionHTTPResult, error) {
		attempts++
		return accountInspectionHTTPResult{
			StatusCode: http.StatusOK,
			Body:       `data: {"type":"response.incomplete","response":{"incomplete_details":{"reason":"content_filter"}}}` + "\n\n",
		}, nil
	})
	if err != nil || status != accountInspectionDeepProbeTransientError || !strings.Contains(message, "content_filter") {
		t.Fatalf("runXAIDeepProbeWithRetry() = status:%q message:%q err:%v, want content_filter transient error", status, message, err)
	}
	if attempts != 1 {
		t.Fatalf("runXAIDeepProbeWithRetry() attempts = %d, want 1", attempts)
	}
}

func TestSummarizeInspectionHTTPBodyExtractsCompleteNestedMessage(t *testing.T) {
	want := strings.TrimSpace(strings.Repeat("capacity unavailable ", 20))
	body, err := json.Marshal(map[string]any{
		"error": map[string]any{
			"code":    http.StatusServiceUnavailable,
			"message": want,
			"status":  "UNAVAILABLE",
		},
	})
	if err != nil {
		t.Fatalf("marshal error payload: %v", err)
	}
	if got := proinspection.SummarizeHTTPBody(string(body)); got != want {
		t.Fatalf("proinspection.SummarizeHTTPBody() = %q, want complete nested message %q", got, want)
	}
	if got := proinspection.HTTPErrorDetail("  " + string(body) + "\n"); got != string(body) {
		t.Fatalf("proinspection.HTTPErrorDetail() = %q, want complete body %q", got, string(body))
	}
}

func TestWithInspectionHTTPErrorDetailPreservesCompleteResponse(t *testing.T) {
	body := `{"error":{"code":"invalid_token","message":"credential rejected"},"request_id":"req-123"}`
	decision := proinspection.WithHTTPErrorDetail(
		authErrorDecision(accountInspectionAccount{}, http.StatusUnauthorized),
		"  "+body+"\n",
	)
	if decision.ErrorDetail != body {
		t.Fatalf("ErrorDetail = %q, want complete response %q", decision.ErrorDetail, body)
	}
	if decision.Action != accountInspectionActionDisable {
		t.Fatalf("Action = %q, want %q", decision.Action, accountInspectionActionDisable)
	}
}

func TestTransientDeepProbeErrorCodeTakesPriorityOverHTTPStatus(t *testing.T) {
	decision := accountInspectionDecision{DeepProbeStatus: accountInspectionDeepProbeTransientError}
	status := testStatusCode(http.StatusBadRequest)
	tests := []struct {
		provider string
		want     string
	}{
		{provider: "antigravity", want: "antigravity_deep_probe_error"},
		{provider: "xai", want: "xai_deep_probe_error"},
	}
	for _, tt := range tests {
		if got := proinspection.DecisionErrorCode(tt.provider, decision, status); got != tt.want {
			t.Fatalf("%s deep probe error code = %q, want %q", tt.provider, got, tt.want)
		}
		if !isInspectionAuthErrorCode(tt.want) {
			t.Fatalf("%s deep probe error code should be clearable after recovery", tt.provider)
		}
	}
}

type inspectionProbeRefreshDue bool

func (due inspectionProbeRefreshDue) ShouldRefresh(time.Time, *coreauth.Auth) bool { return bool(due) }

type inspectionProbeRefreshExecutor struct {
	coreauth.ProviderExecutor
	provider string
	refresh  func(*coreauth.Auth) (*coreauth.Auth, error)
}

func (e inspectionProbeRefreshExecutor) Identifier() string { return e.provider }
func (e inspectionProbeRefreshExecutor) Refresh(_ context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	return e.refresh(auth)
}

func TestOAuthInspectionUsesPreparedToken(t *testing.T) {
	for _, provider := range []string{"claude", "codex", "kimi"} {
		for _, alias := range []string{"canonical", "camel", "mixed"} {
			for _, refresh := range []bool{false, true} {
				for _, used := range []int{10, 100} {
					t.Run(fmt.Sprintf("%s/%s/refresh=%v/used=%d", provider, alias, refresh, used), func(t *testing.T) {
						ctx := context.Background()
						manager := coreauth.NewManager(nil, nil, nil)
						metadata := map[string]any{"access_token": "original-token", "refresh_token": "test-refresh", "account_id": "test-account"}
						if alias == "camel" {
							delete(metadata, "access_token")
							metadata["accessToken"] = "original-token"
						}
						if alias == "mixed" {
							metadata["accessToken"] = "stale-alias"
						}
						registered, err := manager.Register(ctx, &coreauth.Auth{ID: "probe-token", FileName: "probe-token.json", Provider: provider, Metadata: metadata, Runtime: inspectionProbeRefreshDue(refresh)})
						if err != nil {
							t.Fatal(err)
						}
						refreshes := 0
						manager.RegisterExecutor(inspectionProbeRefreshExecutor{provider: provider, refresh: func(auth *coreauth.Auth) (*coreauth.Auth, error) {
							refreshes++
							auth.Metadata["access_token"] = "refreshed-token"
							return auth, nil
						}})
						wantToken := "original-token"
						if refresh {
							wantToken = "refreshed-token"
						}
						var probes atomic.Int32
						server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							probes.Add(1)
							w.Header().Set("Content-Type", "application/json")
							if r.Header.Get("Authorization") != "Bearer "+wantToken {
								t.Error("probe used a stale token")
								w.WriteHeader(http.StatusUnauthorized)
								_, _ = w.Write([]byte(`{"error":"invalid_token"}`))
								return
							}
							switch r.URL.Path {
							case "/api/oauth/usage":
								_, _ = fmt.Fprintf(w, `{"five_hour":{"utilization":%d}}`, used)
							case "/api/oauth/profile":
								_, _ = w.Write([]byte(`{}`))
							case "/backend-api/wham/usage":
								_, _ = fmt.Fprintf(w, `{"rate_limit":{"primary_window":{"limit_window_seconds":18000,"used_percent":%d}}}`, used)
							case "/coding/v1/usages":
								_, _ = fmt.Fprintf(w, `{"limits":[{"name":"Weekly","limit":100,"used":%d}]}`, used)
							default:
								t.Errorf("unexpected path %s", r.URL.Path)
								w.WriteHeader(http.StatusNotFound)
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
						settings := proinspection.DefaultSettings()
						settings.UsedPercentThreshold = 95
						settings.AutoExecuteAccountInvalidAction = accountInspectionActionDisable
						settings.AutoExecuteRequestErrorAction = accountInspectionActionDisable
						settings.AutoExecuteQuotaLimitDisable = true
						result := scheduler.inspectAccount(ctx, accountFromAuth(registered), settings)
						results := []accountInspectionResult{result}
						scheduler.applyAutomaticActions(ctx, results, settings)
						current, _ := manager.GetByID(registered.ID)
						if result.Error != "" || result.UsedPercent == nil || *result.UsedPercent != float64(used) || result.IsQuota != (used >= 95) {
							t.Fatalf("quota result: used=%v quota=%v error=%s", result.UsedPercent, result.IsQuota, result.Error)
						}
						if current.Disabled != (used >= 95) || results[0].Executed != (used >= 95) {
							t.Fatalf("action: executed=%v disabled=%v", results[0].Executed, current.Disabled)
						}
						if result.AccessTokenSHA256 != coreauth.AccessTokenSHA256(current) || result.TokenRefreshTriggered != refresh {
							t.Fatal("result is not bound to the prepared token")
						}
						expectedRefreshes := 0
						if refresh {
							expectedRefreshes = 1
						}
						expectedProbes := int32(1)
						if provider == "claude" {
							expectedProbes = 2
						}
						if refreshes != expectedRefreshes || probes.Load() != expectedProbes {
							t.Fatalf("refreshes=%d probes=%d", refreshes, probes.Load())
						}
					})
				}
			}
		}
	}
}

func TestOAuthInspectionSkipsReauthenticatedAccount(t *testing.T) {
	for _, provider := range []string{"claude", "codex", "kimi", "gemini-cli", "xai"} {
		for _, fails := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/fails=%v", provider, fails), func(t *testing.T) {
				ctx := context.Background()
				manager := coreauth.NewManager(nil, nil, nil)
				registered, err := manager.Register(ctx, &coreauth.Auth{ID: "changed-auth", FileName: "changed.json", Provider: provider, Metadata: map[string]any{"access_token": "old-token"}, Runtime: inspectionProbeRefreshDue(true)})
				if err != nil {
					t.Fatal(err)
				}
				manager.RegisterExecutor(inspectionProbeRefreshExecutor{provider: provider, refresh: func(auth *coreauth.Auth) (*coreauth.Auth, error) {
					current, _ := manager.GetByID(auth.ID)
					current.Metadata["access_token"] = "new-login-token"
					if _, err := manager.Update(ctx, current); err != nil {
						t.Fatal(err)
					}
					auth.Metadata["access_token"] = "old-refresh-result"
					if fails {
						return nil, &coreauth.Error{HTTPStatus: 401, Message: "old refresh failed"}
					}
					return auth, nil
				}})
				scheduler := &accountInspectionScheduler{h: &Handler{authManager: manager}}
				settings := proinspection.DefaultSettings()
				settings.AutoExecuteAccountInvalidAction = accountInspectionActionDisable
				settings.AutoExecuteRequestErrorAction = accountInspectionActionDisable
				result := scheduler.inspectAccount(ctx, accountFromAuth(registered), settings)
				results := []accountInspectionResult{result}
				scheduler.applyAutomaticActions(ctx, results, settings)
				current, _ := manager.GetByID(registered.ID)
				if result.ErrorCode != "inspection_identity_changed" || results[0].Executed || current.Disabled || current.Unavailable || current.Metadata["access_token"] != "new-login-token" {
					t.Fatalf("identity change: code=%s executed=%v disabled=%v unavailable=%v", result.ErrorCode, results[0].Executed, current.Disabled, current.Unavailable)
				}
			})
		}
	}
}
