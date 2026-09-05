package quota

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPlanEvidenceRemovesCardStateAndPreservesTierProvenance(t *testing.T) {
	raw, err := NormalizePlanEvidence("gemini-cli", []byte(`{"status":"success","buckets":[{"remaining":0.5}],"paidTier":{"id":"g1-pro-tier"},"currentTier":{"id":"standard-tier"}}`))
	if err != nil {
		t.Fatal(err)
	}
	var evidence PlanEvidence
	if err := json.Unmarshal(raw, &evidence); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(evidence.Attributes), "buckets") || strings.Contains(string(evidence.Attributes), "status") {
		t.Fatal(string(raw))
	}
	attributes, err := PlanEvidenceAttributes("gemini-cli", raw)
	if err != nil || !strings.Contains(string(attributes), "paidTier") || !strings.Contains(string(attributes), "currentTier") {
		t.Fatalf("%s: %v", attributes, err)
	}
	if _, err := PlanEvidenceAttributes("codex", raw); err == nil {
		t.Fatal("accepted wrong provider")
	}
}

func TestPlanEvidencePreservesErrorsAndLegacyReads(t *testing.T) {
	raw, err := NormalizePlanEvidence("codex", []byte(`{"status":"error","planType":"plus"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PlanEvidenceAttributes("codex", raw); err == nil {
		t.Fatal("accepted failed observation")
	}
	legacy := []byte(`{"planType":"plus"}`)
	got, err := PlanEvidenceAttributes("codex", legacy)
	if err != nil || string(got) != string(legacy) {
		t.Fatalf("%s: %v", got, err)
	}
}
