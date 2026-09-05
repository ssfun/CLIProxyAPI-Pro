package quota

import (
	"encoding/json"
	"fmt"
	"strings"
)

// PlanEvidence is the versioned business payload passed from persistence to
// account policy. Card rendering state and quota table rows never cross this
// boundary. Attributes retain provider evidence (including paid/current tiers)
// so policy can distinguish plans without flattening away provenance.
type PlanEvidence struct {
	Version    int             `json:"plan_evidence_version"`
	Provider   string          `json:"provider"`
	Attributes json.RawMessage `json:"attributes"`
	Error      string          `json:"error,omitempty"`
}

// NormalizePlanEvidence adapts historical card and quota-provider records on
// read. Persisted records and backup formats remain unchanged.
func NormalizePlanEvidence(provider string, raw []byte) ([]byte, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	evidence := PlanEvidence{Version: 1, Provider: strings.ToLower(strings.TrimSpace(provider))}
	var status string
	_ = json.Unmarshal(payload["status"], &status)
	if status != "" && status != "success" {
		evidence.Error = fmt.Sprintf("snapshot status is %s", status)
	}
	attributes := make(map[string]json.RawMessage)
	for _, key := range []string{
		"plan_type", "planType", "plan", "package", "tier_id", "tierId",
		"tier", "tier_label", "tierLabel", "subscription_type", "subscriptionType",
		"chatgpt_plan_type", "id_token", "idToken", "billing", "subscription",
		"paidTier", "paid_tier", "currentTier", "current_tier", "allowedTiers", "allowed_tiers",
		"has_claude_max", "has_claude_pro", "observed_at_ms", "observedAtMS", "cachedAt",
		"organization", "organizations", "account", "body", "bodyText", "data", "response", "result",
	} {
		if value, ok := payload[key]; ok {
			attributes[key] = value
		}
	}
	var err error
	evidence.Attributes, err = json.Marshal(attributes)
	if err != nil {
		return nil, err
	}
	return json.Marshal(evidence)
}

// PlanEvidenceAttributes accepts the new internal contract and legacy direct
// callers. Unknown versions/providers fail closed instead of selecting a plan.
func PlanEvidenceAttributes(provider string, raw []byte) ([]byte, error) {
	var evidence PlanEvidence
	if err := json.Unmarshal(raw, &evidence); err != nil {
		return nil, err
	}
	if evidence.Version == 0 {
		return raw, nil
	}
	if evidence.Version != 1 || evidence.Provider != strings.ToLower(strings.TrimSpace(provider)) {
		return nil, fmt.Errorf("unsupported plan evidence version or provider")
	}
	if evidence.Error != "" {
		return nil, fmt.Errorf("%s", evidence.Error)
	}
	return evidence.Attributes, nil
}
