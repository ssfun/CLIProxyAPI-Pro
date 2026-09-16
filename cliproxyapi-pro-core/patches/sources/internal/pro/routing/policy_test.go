package routing

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestConfirmationTrackerResetsOutsideWindow(t *testing.T) {
	tracker := NewConfirmationTracker()
	policy := ProviderPolicy{Confirmations: 2, ConfirmationWindowSeconds: 60}
	now := time.Unix(1_700_000_000, 0)
	if confirmed, count, required := tracker.Confirm("auth", "xai", 429, policy, now); confirmed || count != 1 || required != 2 {
		t.Fatalf("first confirmation = %v, %d, %d", confirmed, count, required)
	}
	if confirmed, count, _ := tracker.Confirm("auth", "xai", 429, policy, now.Add(30*time.Second)); !confirmed || count != 2 {
		t.Fatalf("second confirmation = %v, %d", confirmed, count)
	}
	if confirmed, count, _ := tracker.Confirm("auth", "xai", 429, policy, now.Add(2*time.Minute)); confirmed || count != 1 {
		t.Fatalf("expired confirmation = %v, %d", confirmed, count)
	}
}

func TestConfirmationTrackerResetDiscardsPendingState(t *testing.T) {
	tracker := NewConfirmationTracker()
	policy := ProviderPolicy{Confirmations: 2, ConfirmationWindowSeconds: 60}
	now := time.Unix(1_700_000_000, 0)
	if confirmed, count, _ := tracker.Confirm("auth", "xai", 429, policy, now); confirmed || count != 1 {
		t.Fatalf("first confirmation = %v, %d", confirmed, count)
	}
	tracker.Reset()
	if confirmed, count, _ := tracker.Confirm("auth", "xai", 429, policy, now.Add(time.Second)); confirmed || count != 1 {
		t.Fatalf("confirmation after reset = %v, %d", confirmed, count)
	}
}

func TestProtectionEvidenceAndReleasePolicy(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	failure := UsageFailure{
		StatusCode: http.StatusTooManyRequests,
		Headers:    http.Header{"Retry-After": []string{"120"}},
		Body:       `{"error":{"type":"insufficient_quota"}}`,
	}
	if !HasQuotaEvidence(failure) {
		t.Fatal("expected quota evidence")
	}
	if got := ReleaseAt(failure, ProviderPolicy{AutoEnable: true}, now); !got.Equal(now.Add(2 * time.Minute)) {
		t.Fatalf("release at = %v", got)
	}
	if got := Reason(failure); got != failure.Body {
		t.Fatalf("reason = %q", got)
	}
}

func TestReasonRedactsSensitiveJSONAndText(t *testing.T) {
	jsonReason := Reason(UsageFailure{Body: `{"error":"quota exceeded","access_token":"sk-secret","x-api-key":"api-secret","nested":{"authorization":"Bearer private"}}`})
	if strings.Contains(jsonReason, "sk-secret") || strings.Contains(jsonReason, "api-secret") || strings.Contains(jsonReason, "Bearer private") {
		t.Fatalf("JSON reason exposed a secret: %q", jsonReason)
	}
	if !strings.Contains(jsonReason, "quota exceeded") || !strings.Contains(jsonReason, "[REDACTED]") {
		t.Fatalf("JSON reason lost useful detail: %q", jsonReason)
	}
	textReason := Reason(UsageFailure{Body: `quota exceeded; authorization=Bearer private-token; refresh_token=secret-token`})
	if strings.Contains(textReason, "private-token") || strings.Contains(textReason, "secret-token") {
		t.Fatalf("text reason exposed a secret: %q", textReason)
	}
}

func TestNormalizeConfigOwnsProviderPolicyBounds(t *testing.T) {
	got := NormalizeConfig(RequestProtectionConfig{
		Mode: "ENFORCE",
		Providers: map[string]ProviderPolicy{
			"xai": {StatusCodes: []int{999, 429, 429}, Confirmations: 99, ConfirmationWindowSeconds: 99_999},
		},
	}, []string{"xai", "codex"})
	if got.Mode != ModeEnforce || len(got.Providers) != 2 {
		t.Fatalf("normalized config = %+v", got)
	}
	xai := got.Providers["xai"]
	if len(xai.StatusCodes) != 1 || xai.StatusCodes[0] != 429 || xai.Confirmations != 5 || xai.ConfirmationWindowSeconds != 86400 {
		t.Fatalf("normalized xai policy = %+v", xai)
	}
	defaults := NormalizeConfig(RequestProtectionConfig{}, []string{"codex"}).Providers["codex"]
	if len(defaults.StatusCodes) != 2 || defaults.StatusCodes[0] != http.StatusPaymentRequired || defaults.StatusCodes[1] != http.StatusTooManyRequests {
		t.Fatalf("default status codes = %#v", defaults.StatusCodes)
	}
}
