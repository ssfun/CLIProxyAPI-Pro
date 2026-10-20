package executor

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestInspectionRefreshCodexForceBothLayers(t *testing.T) {
	for _, due := range []string{"outer", "siwc"} {
		for _, force := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/force=%v", due, force), func(t *testing.T) {
				proxyURL, outerCount, siwcCount, teardown := startDualCodexAndSIWCMockOAuthServer(t, makeTestCodexRefreshJWT("plus", "inspection-compat"), nil)
				defer teardown()
				now := time.Now()
				outerExpiry, siwcExpiry := now.Add(168*time.Hour), now.Add(168*time.Hour)
				if due == "outer" {
					outerExpiry = now.Add(-time.Hour)
				} else {
					siwcExpiry = now.Add(-time.Hour)
				}
				m := cliproxyauth.NewManager(nil, nil, nil)
				m.RegisterExecutor(NewCodexExecutor(&config.Config{}))
				a, err := m.Register(context.Background(), &cliproxyauth.Auth{ID: "inspection-dual", Provider: "codex", Status: cliproxyauth.StatusActive, ProxyURL: proxyURL, Metadata: map[string]any{
					"access_token": "outer-old", "refresh_token": "outer-refresh-old", "expired": outerExpiry.Format(time.RFC3339), "use_siwc": true,
					"siwc": map[string]any{"access_token": "siwc-old", "refresh_token": "siwc-refresh-old", "client_id": "oaiapp_test", "device_id": "e61bbe28-07ef-466d-8e5d-a344f94ab305", "expired": siwcExpiry.Format(time.RFC3339)},
				}})
				if err != nil {
					t.Fatal(err)
				}
				var refreshed bool
				if force {
					_, refreshed, err = m.ForceRefreshForInspection(context.Background(), a.ID)
				} else {
					_, refreshed, err = m.RefreshIfDueForInspection(context.Background(), a.ID)
				}
				if err != nil || !refreshed {
					t.Fatalf("refreshed=%v err=%v", refreshed, err)
				}
				wantOuter, wantSIWC := 0, 0
				outerToken, siwcToken := "outer-old", "siwc-old"
				if force || due == "outer" {
					wantOuter = 1
					outerToken = "outer-access-refreshed"
				}
				if force || due == "siwc" {
					wantSIWC = 1
					siwcToken = "siwc-access-refreshed"
				}
				if *outerCount != wantOuter || *siwcCount != wantSIWC {
					t.Errorf("OAuth calls outer=%d SIWC=%d want=%d/%d", *outerCount, *siwcCount, wantOuter, wantSIWC)
				}
				stored, _ := m.GetByID(a.ID)
				if stored.Metadata["access_token"] != outerToken || stored.Metadata["siwc"].(map[string]any)["access_token"] != siwcToken {
					t.Fatalf("committed tokens do not match selected refresh layers: %+v", stored.Metadata)
				}
			})
		}
	}
}
