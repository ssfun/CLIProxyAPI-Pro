package redisqueue

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/pro/apikeypolicy"
	coreusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
)

func apiKeyPolicyUsageContext(status int) context.Context {
	decision := apikeypolicy.RequestPolicyDecision{Mode: apikeypolicy.ModeProfile, Snapshot: &apikeypolicy.RequestPolicySnapshot{
		PolicyID: "policy-start", ProfileID: "profile-start", ProfileName: "Profile at request start",
		RequestedModel: "smart", EffectiveModel: "gpt-5",
	}}
	ctx := apikeypolicy.WithDecision(context.Background(), decision)
	ctx = logging.WithResponseStatusHolder(ctx)
	logging.SetResponseStatus(ctx, status)
	return ctx
}

func TestUsageQueuePluginSkipsMonitoringContext(t *testing.T) {
	withEnabledQueue(t, func() {
		ctx := coreusage.WithSkipMonitoring(context.Background())
		(&usageQueuePlugin{}).HandleUsage(ctx, coreusage.Record{
			Provider: "codex",
			Model:    "gpt-test",
			Detail:   coreusage.Detail{InputTokens: 1, TotalTokens: 1},
		})
		if items := PopOldest(10); len(items) != 0 {
			t.Fatalf("PopOldest() items = %d, want 0 for skipped monitoring", len(items))
		}
	})
}

func TestUsageQueuePolicyAttributionIsFrozenForEveryTerminalOutcome(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		record coreusage.Record
	}{
		{name: "success", status: http.StatusOK, record: coreusage.Record{Provider: "codex", Model: "gpt-5"}},
		{name: "ordinary failure", status: http.StatusBadGateway, record: coreusage.Record{Provider: "codex", Model: "gpt-5", Failed: true, Fail: coreusage.Failure{StatusCode: http.StatusBadGateway, Body: "upstream failed"}}},
		{name: "stream cancellation", status: 499, record: coreusage.Record{Provider: "codex", Model: "gpt-5", Failed: true, Fail: coreusage.Failure{StatusCode: 499, Body: "context canceled"}}},
		{name: "response translation failure", status: http.StatusBadGateway, record: coreusage.Record{Provider: "codex", Model: "gpt-5", Failed: true, Fail: coreusage.Failure{StatusCode: http.StatusBadGateway, Body: "response translation failed"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			withEnabledQueue(t, func() {
				ctx := apiKeyPolicyUsageContext(test.status)
				(&usageQueuePlugin{}).HandleUsage(ctx, test.record)
				payload := popSinglePayload(t)
				requireStringField(t, payload, "policy_mode", "profile")
				requireStringField(t, payload, "api_key_policy_id", "policy-start")
				requireStringField(t, payload, "profile_id", "profile-start")
				requireStringField(t, payload, "profile_name_snapshot", "Profile at request start")
				requireStringField(t, payload, "requested_model", "smart")
				requireStringField(t, payload, "effective_model", "gpt-5")
				if items := PopOldest(10); len(items) != 0 {
					t.Fatalf("terminal outcome emitted %d duplicate usage records", len(items))
				}
			})
		})
	}
}

func TestUsageQueueRetryAttemptsKeepOneFrozenProfileAttributionEach(t *testing.T) {
	withEnabledQueue(t, func() {
		ctx := apiKeyPolicyUsageContext(http.StatusOK)
		for attempt := int64(0); attempt < 2; attempt++ {
			current := attempt
			(&usageQueuePlugin{}).HandleUsage(ctx, coreusage.Record{
				Provider: "codex", Model: "gpt-5", AttemptIndex: &current,
				Failed: attempt == 0, Fail: coreusage.Failure{StatusCode: http.StatusBadGateway, Body: "retryable"},
			})
		}
		items := PopOldest(10)
		if len(items) != 2 {
			t.Fatalf("retry usage records=%d, want 2 attempts", len(items))
		}
		for _, item := range items {
			var payload map[string]json.RawMessage
			if err := json.Unmarshal(item, &payload); err != nil {
				t.Fatal(err)
			}
			requireStringField(t, payload, "api_key_policy_id", "policy-start")
			requireStringField(t, payload, "profile_id", "profile-start")
			requireStringField(t, payload, "profile_name_snapshot", "Profile at request start")
		}
	})
}

func TestUsageQueuePreservesModelAudit(t *testing.T) {
	withEnabledQueue(t, func() {
		(&usageQueuePlugin{}).HandleUsage(apiKeyPolicyUsageContext(http.StatusOK), coreusage.Record{
			Provider: "codex", Model: "billing-model", UpstreamModel: "gpt-6-astra", ResponseModel: "gpt-5.6-luna", ModelMatchStatus: "mismatch",
		})
		payload := popSinglePayload(t)
		requireStringField(t, payload, "model", "billing-model")
		requireStringField(t, payload, "requested_model", "smart")
		requireStringField(t, payload, "upstream_model", "gpt-6-astra")
		requireStringField(t, payload, "response_model", "gpt-5.6-luna")
		requireStringField(t, payload, "model_match_status", "mismatch")
	})
}
