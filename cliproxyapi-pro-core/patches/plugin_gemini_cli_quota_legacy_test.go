package pluginhost

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type legacyGeminiCLIQuotaTestExecutor struct {
	requests []*http.Request
	planBody string
	planCode int
}

func (e *legacyGeminiCLIQuotaTestExecutor) Identifier() string { return legacyGeminiCLIProvider }
func (e *legacyGeminiCLIQuotaTestExecutor) Execute(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (coreexecutor.Response, error) {
	return coreexecutor.Response{}, nil
}
func (e *legacyGeminiCLIQuotaTestExecutor) ExecuteStream(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (*coreexecutor.StreamResult, error) {
	return nil, nil
}
func (e *legacyGeminiCLIQuotaTestExecutor) Refresh(_ context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	return auth, nil
}
func (e *legacyGeminiCLIQuotaTestExecutor) CountTokens(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (coreexecutor.Response, error) {
	return coreexecutor.Response{}, nil
}
func (e *legacyGeminiCLIQuotaTestExecutor) HttpRequest(_ context.Context, _ *coreauth.Auth, req *http.Request) (*http.Response, error) {
	e.requests = append(e.requests, req)
	if strings.HasSuffix(req.URL.String(), ":retrieveUserQuota") {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(bytes.NewBufferString(`{"buckets":[
				{"modelId":"gemini-2.0-flash","remainingFraction":0.9},
				{"modelId":"gemini-4-pro-preview","remainingFraction":0.75,"remainingAmount":42},
				{"modelId":"gemini-3.1-pro-preview","remainingFraction":0.25,"remainingAmount":21}
			]}`)),
		}, nil
	}
	statusCode := e.planCode
	if statusCode == 0 {
		statusCode = http.StatusServiceUnavailable
	}
	responseBody := e.planBody
	if responseBody == "" {
		responseBody = `{"error":"temporary"}`
	}
	return &http.Response{
		StatusCode: statusCode,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewBufferString(responseBody)),
	}, nil
}

func TestLegacyGeminiCLIQuotaAdapterUsesRegisteredExecutorAndRetainsPlan(t *testing.T) {
	pluginExecutor := &fakeExecutor{identifier: legacyGeminiCLIProvider}
	host := newHostWithRecords(capabilityRecord{
		id: "geminicli",
		plugin: pluginapi.Plugin{Capabilities: pluginapi.Capabilities{
			Executor: pluginExecutor,
		}},
	})
	executor := &legacyGeminiCLIQuotaTestExecutor{}
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)
	host.SetAuthManager(manager)
	previous := &pluginapi.QuotaSnapshot{Plan: &pluginapi.QuotaPlan{ID: "g1-pro-tier", Kind: "pro", ObservedAtMS: 123}}
	auth := &coreauth.Auth{
		ID: "auth-1", Provider: legacyGeminiCLIProvider, FileName: "gemini.json",
		Metadata: map[string]any{"project_id": "project-a"},
	}

	result := host.FetchQuota(context.Background(), auth, previous)
	if !result.Handled || result.Err != nil || result.PluginID != "geminicli" {
		t.Fatalf("FetchQuota() = %#v", result)
	}
	if len(executor.requests) != 2 {
		t.Fatalf("executor requests = %d, want 2", len(executor.requests))
	}
	if len(result.Snapshot.Items) != 1 || result.Snapshot.Items[0].ID != "gemini-pro-series" {
		t.Fatalf("quota items = %#v", result.Snapshot.Items)
	}
	if remaining := result.Snapshot.Items[0].RemainingFraction; remaining == nil || *remaining != 0.25 {
		t.Fatalf("remaining fraction = %v, want conservative 0.25", remaining)
	}
	if result.Snapshot.Plan == nil || result.Snapshot.Plan.ID != "g1-pro-tier" || !result.Snapshot.Plan.Stale {
		t.Fatalf("retained plan = %#v", result.Snapshot.Plan)
	}
	if result.Snapshot.Metadata["quota_mode"] != "legacy-adapter" {
		t.Fatalf("snapshot metadata = %#v", result.Snapshot.Metadata)
	}
	plugins := host.RegisteredPlugins()
	if len(plugins) != 1 || !plugins[0].SupportsQuota || plugins[0].QuotaMode != "legacy-adapter" {
		t.Fatalf("registered plugin info = %#v", plugins)
	}
}

func TestLegacyGeminiCLIQuotaPlanPrefersPaidTier(t *testing.T) {
	plan := legacyGeminiCLIQuotaPlan(map[string]any{
		"response": map[string]any{
			"currentTier": map[string]any{"id": "free-tier"},
			"paidTier": map[string]any{
				"id": "g1-ultra-tier", "name": "Google AI Ultra",
				"availableCredits": []any{map[string]any{"creditType": legacyGeminiCLIGoogleOneAI, "creditAmount": float64(12)}},
			},
		},
	}, 123)
	if plan == nil || plan.ID != "g1-ultra-tier" || plan.Kind != "ultra" || plan.CreditBalance == nil || *plan.CreditBalance != 12 {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestLegacyGeminiCLIQuotaPlanUnwrapsJSONStringBody(t *testing.T) {
	plan := legacyGeminiCLIQuotaPlan(map[string]any{
		"bodyText": `{"currentTier":{"id":"standard-tier","name":"Standard"}}`,
	}, 123)
	if plan == nil || plan.ID != "standard-tier" || plan.Kind != "standard" {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestLegacyGeminiCLIQuotaPlanSkipsUnrelatedWrapper(t *testing.T) {
	plan := legacyGeminiCLIQuotaPlan(map[string]any{
		"body":     map[string]any{"status": "ok"},
		"response": map[string]any{"currentTier": map[string]any{"id": "legacy-tier"}},
	}, 123)
	if plan == nil || plan.ID != "legacy-tier" || plan.Kind != "legacy" {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestLegacyGeminiCLIQuotaPlanUsesDefaultAllowedTier(t *testing.T) {
	plan := legacyGeminiCLIQuotaPlan(map[string]any{
		"allowedTiers": []any{
			map[string]any{"id": "legacy-tier", "isDefault": false},
			map[string]any{"id": "free-tier", "name": "Free", "isDefault": true},
		},
	}, 123)
	if plan == nil || plan.ID != "free-tier" || plan.Kind != "free" || plan.Label != "Free" {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestLegacyGeminiCLIQuotaAdapterRetainsPlanWhenTierPayloadIsUnsupported(t *testing.T) {
	pluginExecutor := &fakeExecutor{identifier: legacyGeminiCLIProvider}
	host := newHostWithRecords(capabilityRecord{
		id: "geminicli",
		plugin: pluginapi.Plugin{Capabilities: pluginapi.Capabilities{
			Executor: pluginExecutor,
		}},
	})
	executor := &legacyGeminiCLIQuotaTestExecutor{planCode: http.StatusOK, planBody: `{"allowedTiers":[]}`}
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)
	host.SetAuthManager(manager)
	previous := &pluginapi.QuotaSnapshot{Plan: &pluginapi.QuotaPlan{ID: "standard-tier", Kind: "standard", ObservedAtMS: 123}}
	auth := &coreauth.Auth{
		ID: "auth-1", Provider: legacyGeminiCLIProvider, FileName: "gemini.json",
		Metadata: map[string]any{"project_id": "project-a"},
	}

	result := host.FetchQuota(context.Background(), auth, previous)
	if !result.Handled || result.Err != nil {
		t.Fatalf("FetchQuota() = %#v", result)
	}
	if result.Snapshot.Plan == nil || result.Snapshot.Plan.ID != "standard-tier" || !result.Snapshot.Plan.Stale {
		t.Fatalf("retained plan = %#v", result.Snapshot.Plan)
	}
	if len(result.Snapshot.Warnings) != 1 || result.Snapshot.Warnings[0].Code != "plan_unavailable" {
		t.Fatalf("warnings = %#v", result.Snapshot.Warnings)
	}
}
