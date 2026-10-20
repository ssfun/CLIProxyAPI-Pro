package executor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

// Installed only by the runner's overlay, after the production hint read.
var proCreditsRecoveryObservedHint func()

func TestProCreditsRecoveryPublicationOrdering(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		fresh, failure, refresh bool
		balance                 string
	}{
		{name: "expired_refresh_available", refresh: true, balance: "2516"},
		{name: "failure_refresh_available", fresh: true, failure: true, refresh: true, balance: "2516"},
		{name: "expired_refresh_unavailable", refresh: true, balance: "25"},
		{name: "expired_without_refresh"},
		{name: "failure_without_refresh", fresh: true, failure: true},
		{name: "fresh_without_failure", fresh: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth := &cliproxyauth.Auth{ID: "pro-recovery-order-" + tc.name}
			updated := time.Now().Add(-cliproxyauth.AntigravityCreditsHintRefreshInterval - time.Minute)
			if tc.fresh {
				updated = time.Now()
			}
			old := cliproxyauth.AntigravityCreditsHint{Known: true, MinCreditAmount: 50, UpdatedAt: updated}
			cliproxyauth.SetAntigravityCreditsHint(auth.ID, old)
			if tc.failure {
				antigravityCreditsFailureByAuth.Store(auth.ID, antigravityCreditsFailureState{PermanentlyDisabled: true})
			}
			t.Cleanup(func() {
				proCreditsRecoveryObservedHint = nil
				antigravityCreditsFailureByAuth.Delete(auth.ID)
				antigravityCreditsBalanceByAuth.Delete(auth.ID)
				cliproxyauth.SetAntigravityCreditsHint(auth.ID, cliproxyauth.AntigravityCreditsHint{})
			})
			if tc.refresh {
				received, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/v1internal:loadCodeAssist" {
						t.Errorf("unexpected path: %s", r.URL.Path)
					}
					close(received)
					<-release
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"paidTier":{"id":"tier-1","availableCredits":[{"creditType":"GOOGLE_ONE_AI","creditAmount":"` + tc.balance + `","minimumCreditAmountForUsage":"50"}]}}`))
				}))
				defer server.Close()
				auth.Attributes = map[string]string{"base_url": server.URL}
				exec := NewAntigravityExecutor(&config.Config{})
				go func() { defer close(done); exec.updateAntigravityCreditsBalance(context.Background(), auth, "token") }()
				select {
				case <-received:
				case <-time.After(10 * time.Second):
					t.Fatal("refresh never reached HTTP server")
				}
				proCreditsRecoveryObservedHint = func() {
					close(release)
					select {
					case <-done:
					case <-time.After(10 * time.Second):
						t.Fatal("refresh did not complete")
					}
					t.Log("HTTP refresh published after recovery observed old hint")
				}
			}
			clearAntigravityCreditsFailureState(auth)
			proCreditsRecoveryObservedHint = nil
			got, ok := cliproxyauth.GetAntigravityCreditsHint(auth.ID)
			if !ok {
				t.Fatal("hint disappeared")
			}
			if tc.refresh {
				want := float64(2516)
				if tc.balance == "25" {
					want = 25
				}
				if !got.Known || got.CreditAmount != want || got.Available != (want >= 50) {
					t.Fatalf("new HTTP publication lost: got %+v; want known balance %v", got, want)
				}
			} else if tc.fresh && !tc.failure {
				if got != old {
					t.Fatalf("fresh hint changed: %+v", got)
				}
			} else if got.Known {
				t.Fatalf("old failure hint was not invalidated: %+v", got)
			}
			if _, exists := antigravityCreditsFailureByAuth.Load(auth.ID); exists {
				t.Fatal("failure marker remains")
			}
		})
	}
	clearAntigravityCreditsFailureState(nil)
	clearAntigravityCreditsFailureState(&cliproxyauth.Auth{ID: " "})
}
