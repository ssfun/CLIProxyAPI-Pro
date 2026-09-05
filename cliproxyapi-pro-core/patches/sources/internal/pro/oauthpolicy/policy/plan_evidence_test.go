package policy

import (
	"fmt"
	"testing"

	proquota "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/quota"
)

func TestNormalizedPlanEvidencePreservesLegacyDecisions(t *testing.T) {
	for _, item := range []struct{ provider, raw string }{
		{"codex", `{"status":"success","windows":[],"planType":"plus","cachedAt":1234}`},
		{"claude", `{"status":"success","windows":[],"has_claude_max":true}`},
		{"claude", `{"status":"success","organization":{"organization_type":"claude_team","subscription_status":"active"}}`},
		{"antigravity", `{"status":"success","groups":[],"subscription":{"plan":"pro","tierId":"g1-pro-tier"}}`},
		{"gemini-cli", `{"items":[],"plan":{"id":"g1-pro-tier","kind":"pro"}}`},
		{"gemini-cli", `{"status":"success","paidTier":{"id":"g1-pro-tier"},"currentTier":{"id":"standard-tier"}}`},
		{"kimi", `{"status":"success","rows":[],"planType":"team"}`},
		{"xai", `{"status":"success","billing":{"monthlyLimitCents":3000}}`},
		{"codex", `{"status":"loading","planType":"plus"}`},
		{"gemini-cli", `{"items":[],"plan":{"kind":"pro","stale":true}}`},
	} {
		t.Run(item.provider+item.raw, func(t *testing.T) {
			raw := []byte(item.raw)
			normalized, err := proquota.NormalizePlanEvidence(item.provider, raw)
			if err != nil {
				t.Fatal(err)
			}
			legacyInput := Input{AuthProvider: item.provider, QuotaSnapshotJSON: raw}
			nextInput := Input{AuthProvider: item.provider, QuotaSnapshotJSON: normalized}
			wantPlan, wantSource, wantErr := planFromQuotaSnapshot(item.provider, legacyInput)
			gotPlan, gotSource, gotErr := planFromQuotaSnapshot(item.provider, nextInput)
			if gotPlan != wantPlan || gotSource != wantSource || fmt.Sprint(gotErr) != fmt.Sprint(wantErr) {
				t.Fatalf("normalized (%q, %q, %v), legacy (%q, %q, %v)", gotPlan, gotSource, gotErr, wantPlan, wantSource, wantErr)
			}
			if snapshotObservedAtMS(nextInput) != snapshotObservedAtMS(legacyInput) {
				t.Fatal("observation time changed")
			}
		})
	}
}
