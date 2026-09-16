package inspection

import "testing"

func intStatus(value int) *int { return &value }

func TestQuotaDecisionHonorsDisabledAndThreshold(t *testing.T) {
	used := 100.0
	if got := QuotaDecision(false, &used, true, 100); got.Action != ActionDisable || !got.IsQuota {
		t.Fatalf("enabled exhausted decision = %+v", got)
	}
	if got := QuotaDecision(true, &used, true, 100); got.Action != ActionKeep || !got.IsQuota {
		t.Fatalf("disabled exhausted decision = %+v", got)
	}
}

func TestQuotaDecisionHonorsFractionalThreshold(t *testing.T) {
	below := 99.4
	if got := QuotaDecision(false, &below, true, 99.5); got.Action != ActionKeep || got.IsQuota {
		t.Fatalf("below-threshold decision = %+v", got)
	}
	at := 99.5
	if got := QuotaDecision(false, &at, true, 99.5); got.Action != ActionDisable || !got.IsQuota {
		t.Fatalf("at-threshold decision = %+v", got)
	}
}

func TestCodexDecisionAndErrorCodePrecedence(t *testing.T) {
	used := 100.0
	decision := CodexDecision(false, 401, &used, true, 95)
	if !decision.IsQuota || decision.Action != ActionDisable {
		t.Fatalf("quota decision = %#v", decision)
	}
	if code := DecisionErrorCode("codex", decision, intStatus(401)); code != "" {
		t.Fatalf("quota error code = %q", code)
	}

	decision = CodexDecision(false, 401, nil, false, 95)
	if decision.Action != ActionDelete {
		t.Fatalf("unauthorized decision = %#v", decision)
	}
	if code := DecisionErrorCode("codex", decision, intStatus(401)); code != "inspection_http_error" {
		t.Fatalf("unauthorized error code = %q", code)
	}
}

func TestDecisionErrorCodeUsesProviderSpecificDeepProbeCode(t *testing.T) {
	decision := Decision{DeepProbeStatus: DeepProbeTransientError}
	if code := DecisionErrorCode("xai", decision, intStatus(400)); code != "xai_deep_probe_error" {
		t.Fatalf("xai code = %q", code)
	}
	if code := DecisionErrorCode("antigravity", decision, intStatus(400)); code != "antigravity_deep_probe_error" {
		t.Fatalf("antigravity code = %q", code)
	}
}

func TestHealthyDecisionReenablesDisabledAccount(t *testing.T) {
	if got := HealthyDecision(true); got.Action != ActionEnable {
		t.Fatalf("healthy decision = %+v", got)
	}
}

func TestQuotaRecoveredHandlesLowThresholds(t *testing.T) {
	for _, threshold := range []float64{0, 1, 2} {
		if !QuotaRecovered(0, threshold) {
			t.Fatalf("zero usage did not recover at threshold %v", threshold)
		}
	}
	if QuotaRecovered(1, 2) {
		t.Fatal("non-zero usage recovered at a zero hysteresis boundary")
	}
	if !QuotaRecovered(92.9, 95) || QuotaRecovered(93, 95) {
		t.Fatal("normal hysteresis boundary changed")
	}
}
