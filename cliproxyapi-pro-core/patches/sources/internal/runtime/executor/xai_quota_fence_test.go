package executor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/embeddedusage"
	proquota "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/quota"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/requestmeta"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

// The optional interface permits the identical regression source to run on
// the before-fix generated tree, where the binding method does not yet exist.
func TestXAIQuotaLateHTTPResponseFence(t *testing.T) {
	for _, scenario := range []string{"different subject", "removed", "same subject re-registration", "same subject refresh"} {
		t.Run(scenario, func(t *testing.T) {
			t.Setenv("USAGE_DB_PATH", filepath.Join(t.TempDir(), "usage.sqlite"))
			t.Setenv("USAGE_SERVICE_ENABLED", "false")
			ctx, cancel := context.WithCancel(context.Background())
			service, err := embeddedusage.Start(ctx)
			if err != nil {
				t.Fatal(err)
			}
			embeddedusage.SetDefaultService(service)
			t.Cleanup(func() { embeddedusage.SetDefaultService(nil); cancel() })
			manager := coreauth.NewManager(nil, nil, nil)
			if binder, ok := any(manager).(interface{ BindXAIQuotaCache() }); ok {
				binder.BindXAIQuotaCache()
			}
			old, err := manager.Register(ctx, &coreauth.Auth{ID: "same.json", FileName: "same.json", Provider: "xai", Index: "same-index", Metadata: map[string]any{"sub": "old", "access_token": "old-token"}, Attributes: map[string]string{"using_api": "false"}})
			if err != nil {
				t.Fatal(err)
			}
			observerCtx := withXAIQuotaObserver(ctx, old, "grok-free")
			arrived, release := make(chan struct{}), make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(arrived)
				<-release
				w.Header().Set("X-Ratelimit-Limit-Tokens", "100")
				w.Header().Set("X-Ratelimit-Remaining-Tokens", "0")
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()
			done := make(chan error, 1)
			go func() {
				req, _ := http.NewRequestWithContext(observerCtx, http.MethodGet, server.URL, nil)
				response, err := server.Client().Do(req)
				if err == nil {
					response.Body.Close()
					requestmeta.ObserveUpstreamResponse(observerCtx, response.StatusCode, response.Header, nil)
				}
				done <- err
			}()
			<-arrived
			current := old.Clone()
			switch scenario {
			case "removed":
				manager.Remove(ctx, old.ID)
			case "same subject refresh":
				current.Metadata["access_token"] = "refreshed-token"
				current, err = manager.Update(ctx, current)
			default:
				manager.Remove(ctx, old.ID)
				if scenario == "different subject" {
					current.Metadata["sub"] = "new"
				}
				current, err = manager.Register(ctx, current)
			}
			if err != nil {
				close(release)
				<-done
				t.Fatal(err)
			}
			fingerprint := proquota.XAIQuotaIdentityFingerprint(current.FileName, current.Metadata["sub"].(string), "", "")
			if scenario != "removed" {
				now := time.Now().UnixMilli()
				if err = embeddedusage.MergeXAIQuotaCache(ctx, embeddedusage.QuotaCacheEntry{Provider: "xai", FileName: current.FileName, AuthIndex: current.Index, IdentityFingerprint: fingerprint, Data: json.RawMessage(`{"billing":{"planLabel":"Current Plan","freeQuota":{"remainingTokens":80,"observedAt":1}}}`), CachedAt: now, ObservedAt: now}); err != nil {
					close(release)
					<-done
					t.Fatal(err)
				}
			}
			close(release)
			if err = <-done; err != nil {
				t.Fatal(err)
			}
			entries, err := embeddedusage.GetQuotaCache(ctx, "xai", "same.json")
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "removed" {
				if len(entries) != 0 {
					t.Fatalf("late response resurrected removed auth cache: %+v", entries)
				}
				return
			}
			if len(entries) != 1 {
				t.Fatalf("entries=%+v", entries)
			}
			var state struct {
				Billing struct {
					PlanLabel string
					FreeQuota struct{ RemainingTokens int }
				}
			}
			if err = json.Unmarshal(entries[0].Data, &state); err != nil {
				t.Fatal(err)
			}
			expected := 80
			if scenario == "same subject refresh" {
				expected = 0
			}
			t.Logf("sqlite fingerprint=%s data=%s", entries[0].IdentityFingerprint, entries[0].Data)
			if entries[0].IdentityFingerprint != fingerprint || state.Billing.PlanLabel != "Current Plan" || state.Billing.FreeQuota.RemainingTokens != expected {
				t.Fatalf("late observation crossed auth boundary: entry=%+v want remaining=%d", entries[0], expected)
			}
		})
	}
}
