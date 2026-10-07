package management

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/embeddedusage"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	proinspection "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/inspection"
)

// Written before implementation. Failures: missing first observation; unchanged
// old plan after upgrade/downgrade; either endpoint unavailable or malformed;
// both unavailable/empty accidentally clearing a manually refreshed plan;
// optional auth errors contaminating billing health; serial/retried requests
// exceeding the bound; canceled or replaced credentials overwriting SQLite.
// The executor sends real HTTP to the fixture; assertions read persisted SQLite.
func TestXAIInspectionSubscriptionHTTPAndSQLite(t *testing.T) {
	for _, tc := range []struct {
		name, user, settings, label, tier                     string
		userStatus, settingsStatus                            int
		seed, exhausted, timeout, cancel, replacement, update bool
	}{
		{name: "first observation", user: `{"subscriptionTier":"SUPERGROK"}`, settings: `{"subscription_tier_display":"SuperGrok"}`, label: "SuperGrok", tier: "premium"},
		{name: "upgrade replaces manual plan", seed: true, user: `{"subscriptionTier":"SUPERGROK_HEAVY"}`, settings: `{"subscriptionTierDisplay":"SuperGrok Heavy"}`, label: "SuperGrok Heavy", tier: "elite"},
		{name: "downgrade replaces manual plan", seed: true, user: `{"subscription_tier":"FREE"}`, settings: `{}`, label: "FREE", tier: "standard"},
		{name: "display precedence and normalized heavy", user: `{"subscriptionTier":"super-grok_heavy"}`, settings: `{"subscription_tier_display":"  Workspace  "}`, label: "Workspace", tier: "elite"},
		{name: "premium fallback", user: `{"subscriptionTier":"premium_plus"}`, settings: `{}`, label: "premium_plus", tier: "premium"},
		{name: "numeric tier matches UI", user: `{"subscriptionTier":42}`, settings: `{}`, label: "42", tier: "standard"},
		{name: "user unauthorized keeps successful settings", userStatus: 401, settings: `{"subscription_tier_display":"SuperGrok Heavy"}`, label: "SuperGrok Heavy", tier: "elite"},
		{name: "settings forbidden keeps successful user", user: `{"subscription_tier":"SUPERGROK"}`, settingsStatus: 403, label: "SUPERGROK", tier: "premium"},
		{name: "malformed user keeps successful settings", user: `{`, settings: `{"subscription_tier_display":"SuperGrok"}`, label: "SuperGrok", tier: "premium"},
		{name: "array settings keeps successful user", user: `{"subscriptionTier":"standard"}`, settings: `[]`, label: "standard", tier: "standard"},
		{name: "both unavailable retain manual plan", seed: true, userStatus: 503, settingsStatus: 401, label: "Manual Premium", tier: "premium"},
		{name: "empty plans retain manual plan", seed: true, user: `{"subscriptionTier":" "}`, settings: `{"subscription_tier_display":null}`, label: "Manual Premium", tier: "premium"},
		{name: "timeout retain manual plan", seed: true, timeout: true, label: "Manual Premium", tier: "premium"},
		{name: "optional failure preserves exhaustion", seed: true, exhausted: true, userStatus: 503, settingsStatus: 503, label: "Manual Premium", tier: "premium"},
		{name: "replacement cannot overwrite cache", seed: true, replacement: true},
		{name: "token update cannot overwrite cache", seed: true, update: true},
		{name: "cancellation cannot overwrite cache", seed: true, cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := startProQuotaTestService(t)
			manager := coreauth.NewManager(nil, nil, nil)
			auth, err := manager.Register(ctx, &coreauth.Auth{ID: "xai-subscription", FileName: "xai-subscription.json", Provider: "xai", Attributes: map[string]string{"using_api": "false"}, Metadata: map[string]any{"access_token": "observed-token", "sub": "observed-user"}})
			if err != nil {
				t.Fatal(err)
			}
			account := accountFromAuth(auth)
			if tc.seed {
				if err := persistQuotaState(ctx, account, quotaSuccessState(map[string]any{"billing": map[string]any{"planLabel": "Manual Premium", "planTier": "premium"}})); err != nil {
					t.Fatal(err)
				}
			}
			before, err := embeddedusage.GetQuotaCache(ctx, "xai", account.FileName)
			if err != nil {
				t.Fatal(err)
			}
			probeCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			var calls atomic.Int32
			ready := make(chan struct{})
			var release sync.Once
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Header.Get("Authorization") != "Bearer observed-token" {
					t.Errorf("unexpected auth header: %q", r.Header.Get("Authorization"))
				}
				if r.URL.Path == "/v1/billing" {
					if r.URL.Query().Get("format") == "credits" {
						used := "20"
						if tc.exhausted {
							used = "100"
						}
						_, _ = io.WriteString(w, `{"config":{"credit_usage_percent":`+used+`}}`)
					} else {
						_, _ = io.WriteString(w, `{"config":{"monthly_limit":100,"used":20}}`)
					}
					return
				}
				if r.URL.Path == "/v1/responses" {
					w.Header().Set("x-ratelimit-limit-tokens", "1000")
					w.Header().Set("x-ratelimit-remaining-tokens", "700")
					_, _ = io.WriteString(w, `data: {"type":"response.completed"}`+"\n\n")
					return
				}
				if r.URL.Path != "/v1/user" && r.URL.Path != "/v1/settings" {
					t.Errorf("unexpected URL %s", r.URL)
					w.WriteHeader(404)
					return
				}
				if r.Method != http.MethodGet || (r.URL.Path == "/v1/user" && r.URL.Query().Get("include") != "subscription") {
					t.Errorf("subscription request: %s %s", r.Method, r.URL)
				}
				if calls.Add(1) == 2 {
					if tc.replacement || tc.update {
						changed := auth.Clone()
						changed.Metadata["access_token"] = "replacement-token"
						var updateErr error
						if tc.replacement {
							_, updateErr = manager.Register(ctx, changed)
						} else {
							_, updateErr = manager.Update(ctx, changed)
						}
						if updateErr != nil {
							t.Errorf("change credentials: %v", updateErr)
						}
					}
					if tc.cancel {
						cancel()
					}
					release.Do(func() { close(ready) })
				}
				select {
				case <-ready:
				case <-r.Context().Done():
					return
				}
				if tc.timeout || tc.cancel {
					<-r.Context().Done()
					return
				}
				status, body := tc.userStatus, tc.user
				if r.URL.Path == "/v1/settings" {
					status, body = tc.settingsStatus, tc.settings
				}
				if status == 0 {
					status = 200
				}
				if body == "" {
					body = `{}`
				}
				w.WriteHeader(status)
				_, _ = io.WriteString(w, body)
			}))
			defer server.Close()
			transport := server.Client().Transport.(*http.Transport).Clone()
			transport.TLSClientConfig.ServerName = "example.com"
			transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}
			defer transport.CloseIdleConnections()
			manager.RegisterExecutor(&xaiSubscriptionHTTPExecutor{xaiInspectionRoutingExecutor: &xaiInspectionRoutingExecutor{}, client: &http.Client{Transport: transport}})
			scheduler := &accountInspectionScheduler{h: &Handler{authManager: manager}}
			settings := proinspection.DefaultSettings()
			settings.Retries, settings.Timeout = 3, 300
			started := time.Now()
			decision, status, probeErr := scheduler.inspectXAICLI(probeCtx, account, settings)
			if time.Since(started) > 2*time.Second {
				t.Fatal("subscription requests exceeded bounded budget")
			}
			entries, err := embeddedusage.GetQuotaCache(ctx, "xai", account.FileName)
			if err != nil || len(entries) != 1 {
				t.Fatalf("cache read: %v %v", entries, err)
			}
			if calls.Load() != 2 {
				t.Fatalf("optional requests=%d, want 2 concurrent requests without retries", calls.Load())
			}
			if tc.cancel || tc.replacement || tc.update {
				if probeErr == nil || string(entries[0].Data) != string(before[0].Data) {
					t.Fatalf("stale/canceled observation accepted or persisted: err=%v before=%s after=%s", probeErr, before[0].Data, entries[0].Data)
				}
			} else {
				if probeErr != nil || status == nil || *status != 200 || decision.IsQuota != tc.exhausted {
					t.Fatalf("billing health changed: %+v status=%v err=%v", decision, status, probeErr)
				}
				var state map[string]any
				if err := json.Unmarshal(entries[0].Data, &state); err != nil {
					t.Fatal(err)
				}
				billing := state["billing"].(map[string]any)
				if billing["planLabel"] != tc.label || billing["planTier"] != tc.tier {
					t.Fatalf("plan label/tier=%v/%v, want %s/%s", billing["planLabel"], billing["planTier"], tc.label, tc.tier)
				}
			}
			if dir := os.Getenv("XAI_SUBSCRIPTION_EVIDENCE_DIR"); dir != "" {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, strings.ReplaceAll(tc.name, " ", "-")+".json"), entries[0].Data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

type xaiSubscriptionHTTPExecutor struct {
	*xaiInspectionRoutingExecutor
	client *http.Client
}

func (e *xaiSubscriptionHTTPExecutor) HttpRequest(ctx context.Context, _ *coreauth.Auth, req *http.Request) (*http.Response, error) {
	return e.client.Do(req.WithContext(ctx))
}
