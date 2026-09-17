package handlers

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/pro/apikeypolicy"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	coreusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"github.com/tidwall/gjson"
)

type apiKeyPolicyStatusError struct{ status int }

func (e apiKeyPolicyStatusError) Error() string   { return http.StatusText(e.status) }
func (e apiKeyPolicyStatusError) StatusCode() int { return e.status }

func profileDecisionForTest(providers, models []string, mappings map[string]string) apikeypolicy.RequestPolicyDecision {
	allowedProviders := make(map[string]struct{}, len(providers))
	for _, provider := range providers {
		allowedProviders[provider] = struct{}{}
	}
	allowedModels := make(map[string]struct{}, len(models))
	for _, model := range models {
		allowedModels[model] = struct{}{}
	}
	return apikeypolicy.RequestPolicyDecision{Mode: apikeypolicy.ModeProfile, Snapshot: &apikeypolicy.RequestPolicySnapshot{
		PolicyID: "policy", ProfileID: "profile", ProfileName: "restricted", Version: 1,
		ModelMappings: mappings, AllowedModels: allowedModels, AllowedProviders: allowedProviders,
	}}
}

func TestAPIKeyPolicyMapsBodyBeforeRoutingAndFiltersProviders(t *testing.T) {
	decision := profileDecisionForTest([]string{"codex"}, []string{"gpt-5"}, map[string]string{"smart": "gpt-5"})
	ctx := apikeypolicy.WithDecision(context.Background(), decision)
	ctx, model, body, errMsg := applyAPIKeyModelPolicy(nil, ctx, "smart", []byte(`{"model":"smart","input":"keep"}`))
	if errMsg != nil || model != "gpt-5" || gjson.GetBytes(body, "model").String() != "gpt-5" || gjson.GetBytes(body, "input").String() != "keep" {
		t.Fatalf("model=%q body=%s error=%#v", model, body, errMsg)
	}
	providers, errMsg := applyAPIKeyProviderPolicy(ctx, []string{"gemini", "codex", "claude"})
	if errMsg != nil || len(providers) != 1 || providers[0] != "codex" {
		t.Fatalf("providers=%v error=%#v", providers, errMsg)
	}
	frozen, ok := apikeypolicy.DecisionFromContext(ctx)
	if !ok || frozen.Snapshot.RequestedModel != "smart" || frozen.Snapshot.EffectiveModel != "gpt-5" {
		t.Fatalf("frozen decision=%#v", frozen)
	}
}

func TestAPIKeyPolicyExactAutoMappingWinsBeforeGlobalResolution(t *testing.T) {
	decision := profileDecisionForTest([]string{"codex"}, []string{"gpt-5"}, map[string]string{"auto": "gpt-5"})
	for requested, want := range map[string]string{"auto": "gpt-5", "auto(high)": "gpt-5(high)"} {
		ctx := apikeypolicy.WithDecision(context.Background(), decision)
		ctx, model, body, errMsg := applyAPIKeyModelPolicy(nil, ctx, requested, []byte(`{"model":"`+requested+`"}`))
		if errMsg != nil || model != want || gjson.GetBytes(body, "model").String() != want {
			t.Fatalf("requested=%q model=%q body=%s error=%#v", requested, model, body, errMsg)
		}
		frozen, ok := apikeypolicy.DecisionFromContext(ctx)
		if !ok || frozen.UsageAttribution().RequestedModel != requested || frozen.UsageAttribution().EffectiveModel != want {
			t.Fatalf("requested=%q frozen decision=%#v", requested, frozen)
		}
	}
}

func TestAPIKeyPolicyForcedProviderHomeAndRouterTargetsUseSameProviderGate(t *testing.T) {
	decision := profileDecisionForTest([]string{"codex"}, []string{"gpt-5"}, nil)
	ctx := apikeypolicy.WithDecision(context.Background(), decision)
	handler := NewBaseAPIHandlers(nil, coreauth.NewManager(nil, nil, nil))

	providers, model, errMsg := handler.providersForExecution("gpt-5", "gpt-5", false, modelRouteDecision{}, modelExecutionOptions{ForcedProvider: "gemini"})
	if errMsg != nil || model != "gpt-5" || !reflect.DeepEqual(providers, []string{"gemini"}) {
		t.Fatalf("forced provider resolution = %v, %q, %#v", providers, model, errMsg)
	}
	if _, errMsg = applyAPIKeyProviderPolicy(ctx, providers); errMsg == nil || !strings.Contains(errMsg.Error.Error(), "profile_provider_forbidden") {
		t.Fatalf("forced provider bypassed profile: %#v", errMsg)
	}

	handler.AuthManager.SetConfig(&config.Config{Home: config.HomeConfig{Enabled: true}})
	providers, model, errMsg = handler.providersForExecution("gpt-5", "gpt-5", false, modelRouteDecision{}, modelExecutionOptions{})
	if errMsg != nil || model != "gpt-5" || !reflect.DeepEqual(providers, []string{"home"}) {
		t.Fatalf("Home provider resolution = %v, %q, %#v", providers, model, errMsg)
	}
	if _, errMsg = applyAPIKeyProviderPolicy(ctx, providers); errMsg == nil || !strings.Contains(errMsg.Error.Error(), "profile_provider_forbidden") {
		t.Fatalf("Home provider bypassed profile: %#v", errMsg)
	}

	providers, model, errMsg = handler.providersForExecution("gpt-5", "gpt-5", false, modelRouteDecision{Provider: "gemini", Model: "gpt-5"}, modelExecutionOptions{})
	if errMsg != nil || !reflect.DeepEqual(providers, []string{"gemini"}) {
		t.Fatalf("router provider resolution = %v, %q, %#v", providers, model, errMsg)
	}
	if _, errMsg = applyAPIKeyProviderPolicy(ctx, providers); errMsg == nil || !strings.Contains(errMsg.Error.Error(), "profile_provider_forbidden") {
		t.Fatalf("router provider bypassed profile: %#v", errMsg)
	}
}

type apiKeyPolicyAttemptExecutor struct {
	provider string
	mu       sync.Mutex
	execute  int
	stream   int
}

func (e *apiKeyPolicyAttemptExecutor) Identifier() string { return e.provider }
func (e *apiKeyPolicyAttemptExecutor) Execute(ctx context.Context, auth *coreauth.Auth, req coreexecutor.Request, _ coreexecutor.Options) (coreexecutor.Response, error) {
	e.mu.Lock()
	e.execute++
	call := e.execute
	e.mu.Unlock()
	reporter := helps.NewExecutorUsageReporter(ctx, e, req.Model, auth)
	if call == 1 {
		err := apiKeyPolicyStatusError{status: http.StatusBadGateway}
		reporter.PublishFailure(ctx, err)
		return coreexecutor.Response{}, err
	}
	reporter.Publish(ctx, coreusage.Detail{InputTokens: 1, OutputTokens: 1, TotalTokens: 2})
	return coreexecutor.Response{Payload: []byte(`{"id":"ok","choices":[]}`)}, nil
}
func (e *apiKeyPolicyAttemptExecutor) ExecuteStream(ctx context.Context, auth *coreauth.Auth, req coreexecutor.Request, _ coreexecutor.Options) (*coreexecutor.StreamResult, error) {
	e.mu.Lock()
	e.stream++
	call := e.stream
	e.mu.Unlock()
	reporter := helps.NewExecutorUsageReporter(ctx, e, req.Model, auth)
	if call == 1 {
		err := apiKeyPolicyStatusError{status: http.StatusBadGateway}
		reporter.PublishFailure(ctx, err)
		return nil, err
	}
	reporter.Publish(ctx, coreusage.Detail{InputTokens: 1, OutputTokens: 1, TotalTokens: 2})
	chunks := make(chan coreexecutor.StreamChunk, 1)
	chunks <- coreexecutor.StreamChunk{Payload: []byte("done")}
	close(chunks)
	return &coreexecutor.StreamResult{Chunks: chunks}, nil
}
func (e *apiKeyPolicyAttemptExecutor) Refresh(_ context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	return auth, nil
}
func (*apiKeyPolicyAttemptExecutor) CountTokens(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (coreexecutor.Response, error) {
	return coreexecutor.Response{}, errors.New("not used")
}
func (*apiKeyPolicyAttemptExecutor) HttpRequest(context.Context, *coreauth.Auth, *http.Request) (*http.Response, error) {
	return nil, errors.New("not used")
}

type apiKeyPolicyUsageCapture struct {
	records chan struct {
		decision apikeypolicy.RequestPolicyDecision
		record   coreusage.Record
	}
}

func (c *apiKeyPolicyUsageCapture) HandleUsage(ctx context.Context, record coreusage.Record) {
	decision, _ := apikeypolicy.DecisionFromContext(ctx)
	c.records <- struct {
		decision apikeypolicy.RequestPolicyDecision
		record   coreusage.Record
	}{decision: decision, record: record}
}

func registerAPIKeyPolicyAttemptAuths(t *testing.T, manager *coreauth.Manager, provider, model string) {
	t.Helper()
	for _, suffix := range []string{"a", "b"} {
		auth := &coreauth.Auth{ID: provider + "-policy-attempt-" + suffix, Provider: provider, Status: coreauth.StatusActive}
		if _, err := manager.Register(context.Background(), auth); err != nil {
			t.Fatal(err)
		}
		registry.GetGlobalRegistry().RegisterClient(auth.ID, provider, []*registry.ModelInfo{{ID: model}})
		t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(auth.ID) })
	}
}

func TestAPIKeyPolicyExecuteAndStreamRetriesStayInsideAllowedProvidersAndFreezeUsage(t *testing.T) {
	const model = "policy-retry-model"
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "execute", true: "stream"}[stream], func(t *testing.T) {
			manager := coreauth.NewManager(nil, nil, nil)
			allowed := &apiKeyPolicyAttemptExecutor{provider: "codex"}
			forbidden := &apiKeyPolicyAttemptExecutor{provider: "gemini"}
			manager.RegisterExecutor(allowed)
			manager.RegisterExecutor(forbidden)
			registerAPIKeyPolicyAttemptAuths(t, manager, "codex", model)
			registerAPIKeyPolicyAttemptAuths(t, manager, "gemini", model)
			decision := profileDecisionForTest([]string{"codex"}, []string{model}, nil).WithModels(model, model)
			ctx := apikeypolicy.WithDecision(context.Background(), decision)
			providers, errMsg := applyAPIKeyProviderPolicy(ctx, []string{"codex", "gemini"})
			if errMsg != nil || !reflect.DeepEqual(providers, []string{"codex"}) {
				t.Fatalf("provider intersection = %v, %#v", providers, errMsg)
			}
			capture := &apiKeyPolicyUsageCapture{records: make(chan struct {
				decision apikeypolicy.RequestPolicyDecision
				record   coreusage.Record
			}, 4)}
			pluginName := "api-key-policy-real-retry-" + map[bool]string{false: "execute", true: "stream"}[stream]
			coreusage.RegisterNamedPlugin(pluginName, capture)
			defer coreusage.UnregisterNamedPlugin(pluginName, capture)
			if stream {
				result, err := manager.ExecuteStream(ctx, providers, coreexecutor.Request{Model: model}, coreexecutor.Options{Stream: true})
				if err != nil {
					t.Fatal(err)
				}
				for range result.Chunks {
				}
			} else if _, err := manager.Execute(ctx, providers, coreexecutor.Request{Model: model}, coreexecutor.Options{}); err != nil {
				t.Fatal(err)
			}
			allowed.mu.Lock()
			allowedCalls := allowed.execute
			if stream {
				allowedCalls = allowed.stream
			}
			allowed.mu.Unlock()
			forbidden.mu.Lock()
			forbiddenCalls := forbidden.execute + forbidden.stream
			forbidden.mu.Unlock()
			if allowedCalls != 2 || forbiddenCalls != 0 {
				t.Fatalf("allowed calls=%d forbidden calls=%d", allowedCalls, forbiddenCalls)
			}
			for attempt := int64(0); attempt < 2; attempt++ {
				select {
				case captured := <-capture.records:
					attribution := captured.decision.UsageAttribution()
					if attribution.APIKeyPolicyID != "policy" || attribution.ProfileID != "profile" || attribution.RequestedModel != model || attribution.EffectiveModel != model {
						t.Fatalf("attempt attribution=%#v", attribution)
					}
					if captured.record.AttemptIndex == nil || *captured.record.AttemptIndex != attempt {
						t.Fatalf("attempt index=%v, want %d", captured.record.AttemptIndex, attempt)
					}
				case <-time.After(2 * time.Second):
					t.Fatalf("timed out waiting for attempt %d usage", attempt)
				}
			}
			select {
			case duplicate := <-capture.records:
				t.Fatalf("duplicate terminal usage=%#v", duplicate.record)
			case <-time.After(30 * time.Millisecond):
			}
		})
	}
}

func TestAPIKeyPolicyReturnsStableForbiddenAndUnavailableErrors(t *testing.T) {
	decision := profileDecisionForTest([]string{"codex"}, []string{"gpt-5"}, nil)
	ctx := apikeypolicy.WithDecision(context.Background(), decision)
	_, _, _, errMsg := applyAPIKeyModelPolicy(nil, ctx, "claude-opus", []byte(`{"model":"claude-opus"}`))
	if errMsg == nil || errMsg.StatusCode != http.StatusForbidden || !gjson.Get(errMsg.Error.Error(), "error.code").Exists() || gjson.Get(errMsg.Error.Error(), "error.code").String() != "profile_model_forbidden" {
		t.Fatalf("model error=%#v", errMsg)
	}
	if errMsg = requireAPIKeyExecutionProvider(ctx, "gemini"); errMsg == nil || errMsg.StatusCode != http.StatusForbidden || gjson.Get(errMsg.Error.Error(), "error.code").String() != "profile_provider_forbidden" {
		t.Fatalf("provider error=%#v", errMsg)
	}
	quotaErr := &apikeypolicy.QuotaExceededError{Metric: "requests", Used: 1, Limit: 1}
	if errMsg = apiKeyPolicyExecutionError(quotaErr); errMsg == nil || errMsg.StatusCode != http.StatusTooManyRequests || gjson.Get(errMsg.Error.Error(), "error.code").String() != "api_key_quota_exceeded" {
		t.Fatalf("quota error=%#v", errMsg)
	}
	badCtx := apikeypolicy.WithDecision(context.Background(), apikeypolicy.RequestPolicyDecision{})
	_, _, _, errMsg = applyAPIKeyModelPolicy(nil, badCtx, "gpt-5", nil)
	if errMsg == nil || errMsg.StatusCode != http.StatusServiceUnavailable || gjson.Get(errMsg.Error.Error(), "error.code").String() != "api_key_policy_unavailable" {
		t.Fatalf("unavailable error=%#v", errMsg)
	}
}

func TestAPIKeyQuotaAdmissionRunsAfterPolicyValidationAndOnlyOnce(t *testing.T) {
	requestLimit := int64(1)
	decision := profileDecisionForTest([]string{"codex"}, []string{"gpt-5"}, nil)
	decision.Snapshot.Quota = &apikeypolicy.Quota{Enabled: true, Requests: &requestLimit, Epoch: 1}
	ctx := apikeypolicy.WithDecision(context.Background(), decision)
	var admissions int
	ctx = apikeypolicy.WithQuotaAdmission(ctx, func(_ context.Context, current apikeypolicy.RequestPolicyDecision) (apikeypolicy.RequestPolicyDecision, error) {
		admissions++
		admitted := current.Clone()
		admitted.Snapshot.QuotaAdmissionID = "quota_request_once"
		return admitted, nil
	})
	if _, _, _, errMsg := applyAPIKeyModelPolicy(nil, ctx, "forbidden", []byte(`{"model":"forbidden"}`)); errMsg == nil {
		t.Fatal("forbidden model unexpectedly passed validation")
	}
	if admissions != 0 {
		t.Fatalf("policy validation consumed request quota: %d", admissions)
	}
	policyCtx, _, _, errMsg := applyAPIKeyModelPolicy(nil, ctx, "gpt-5", []byte(`{"model":"gpt-5"}`))
	if errMsg != nil {
		t.Fatal(errMsg.Error)
	}
	policyCtx, errMsg = admitAPIKeyQuota(policyCtx)
	if errMsg != nil {
		t.Fatal(errMsg.Error)
	}
	policyCtx, errMsg = admitAPIKeyQuota(policyCtx)
	if errMsg != nil {
		t.Fatal(errMsg.Error)
	}
	if admissions != 1 {
		t.Fatalf("execution admissions = %d, want one", admissions)
	}
	admitted, ok := apikeypolicy.DecisionFromContext(policyCtx)
	if !ok {
		t.Fatal("admitted decision missing")
	}
	if attribution, ok := admitted.QuotaAttribution(); !ok || attribution.AdmissionID != "quota_request_once" {
		t.Fatalf("quota attribution = %#v, %t", attribution, ok)
	}
}

type apiKeyPolicyCountExecutor struct {
	countCalls int
	model      string
}

func (e *apiKeyPolicyCountExecutor) Identifier() string { return "codex" }
func (e *apiKeyPolicyCountExecutor) Execute(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (coreexecutor.Response, error) {
	return coreexecutor.Response{}, nil
}
func (e *apiKeyPolicyCountExecutor) ExecuteStream(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (*coreexecutor.StreamResult, error) {
	return &coreexecutor.StreamResult{}, nil
}
func (e *apiKeyPolicyCountExecutor) Refresh(_ context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	return auth, nil
}
func (e *apiKeyPolicyCountExecutor) CountTokens(_ context.Context, _ *coreauth.Auth, req coreexecutor.Request, _ coreexecutor.Options) (coreexecutor.Response, error) {
	e.countCalls++
	e.model = req.Model
	return coreexecutor.Response{Payload: []byte("11")}, nil
}
func (e *apiKeyPolicyCountExecutor) HttpRequest(context.Context, *coreauth.Auth, *http.Request) (*http.Response, error) {
	return nil, nil
}

func TestAPIKeyPolicyCountAppliesModelMappingAndProviderIntersection(t *testing.T) {
	const model = "api-key-policy-count-model"
	executor := &apiKeyPolicyCountExecutor{}
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)
	auth := &coreauth.Auth{ID: "api-key-policy-count-auth", Provider: "codex", Status: coreauth.StatusActive}
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatal(err)
	}
	registry.GetGlobalRegistry().RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(auth.ID) })
	handler := NewBaseAPIHandlers(nil, manager)
	decision := profileDecisionForTest([]string{"codex"}, []string{model}, map[string]string{"smart-count": model})
	ctx := apikeypolicy.WithDecision(context.Background(), decision)
	body, _, errMsg := handler.ExecuteCountWithAuthManager(ctx, "openai", "smart-count", []byte(`{"model":"smart-count"}`), "")
	if errMsg != nil || string(body) != "11" || executor.countCalls != 1 || executor.model != model {
		t.Fatalf("body=%q calls=%d model=%q error=%#v", body, executor.countCalls, executor.model, errMsg)
	}

	forbidden := profileDecisionForTest([]string{"gemini"}, []string{model}, nil)
	ctx = apikeypolicy.WithDecision(context.Background(), forbidden)
	_, _, errMsg = handler.ExecuteCountWithAuthManager(ctx, "openai", model, []byte(`{"model":"`+model+`"}`), "")
	if errMsg == nil || errMsg.StatusCode != http.StatusForbidden || executor.countCalls != 1 || !strings.Contains(errMsg.Error.Error(), "profile_provider_forbidden") {
		t.Fatalf("calls=%d error=%#v", executor.countCalls, errMsg)
	}
}

type apiKeyPolicyPluginCountHost struct {
	executeCalls int
	streamCalls  int
	countCalls   int
}

func (*apiKeyPolicyPluginCountHost) RouteModel(context.Context, pluginapi.ModelRouteRequest) (pluginapi.ModelRouteResponse, bool) {
	return pluginapi.ModelRouteResponse{Handled: true, TargetKind: pluginapi.ModelRouteTargetExecutor, Target: "policy-plugin"}, true
}
func (*apiKeyPolicyPluginCountHost) HasModelRouters() bool { return true }
func (*apiKeyPolicyPluginCountHost) PluginExecutorProvider(string) (string, bool) {
	return "gemini", true
}
func (h *apiKeyPolicyPluginCountHost) ExecutePluginExecutor(context.Context, string, coreexecutor.Request, coreexecutor.Options) (coreexecutor.Response, error) {
	h.executeCalls++
	return coreexecutor.Response{Payload: []byte("plugin-ok")}, nil
}
func (h *apiKeyPolicyPluginCountHost) ExecutePluginExecutorStream(context.Context, string, coreexecutor.Request, coreexecutor.Options) (*coreexecutor.StreamResult, error) {
	h.streamCalls++
	chunks := make(chan coreexecutor.StreamChunk, 1)
	chunks <- coreexecutor.StreamChunk{Payload: []byte("plugin-stream-ok")}
	close(chunks)
	return &coreexecutor.StreamResult{Chunks: chunks}, nil
}

func TestAPIKeyPolicyExecuteAndStreamValidatePluginExecutorProvider(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "execute", true: "stream"}[stream], func(t *testing.T) {
			host := &apiKeyPolicyPluginCountHost{}
			handler := NewBaseAPIHandlers(nil, nil)
			handler.SetModelRouterHost(host)
			call := func(provider string) *interfaces.ErrorMessage {
				ctx := apikeypolicy.WithDecision(context.Background(), profileDecisionForTest([]string{provider}, []string{"plugin-model"}, nil))
				if stream {
					data, _, errs := handler.ExecuteStreamWithAuthManager(ctx, "openai", "plugin-model", []byte(`{"model":"plugin-model"}`), "")
					if data == nil {
						return <-errs
					}
					for range data {
					}
					return <-errs
				}
				body, _, errMsg := handler.ExecuteWithAuthManager(ctx, "openai", "plugin-model", []byte(`{"model":"plugin-model"}`), "")
				if errMsg == nil && string(body) != "plugin-ok" {
					t.Fatalf("body=%q", body)
				}
				return errMsg
			}

			if errMsg := call("codex"); errMsg == nil || errMsg.StatusCode != http.StatusForbidden || !strings.Contains(errMsg.Error.Error(), "profile_provider_forbidden") {
				t.Fatalf("forbidden plugin error=%#v", errMsg)
			}
			if host.executeCalls != 0 || host.streamCalls != 0 {
				t.Fatalf("forbidden plugin executed: execute=%d stream=%d", host.executeCalls, host.streamCalls)
			}
			if errMsg := call("gemini"); errMsg != nil {
				t.Fatalf("allowed plugin error=%#v", errMsg)
			}
			if (!stream && host.executeCalls != 1) || (stream && host.streamCalls != 1) {
				t.Fatalf("allowed plugin calls: execute=%d stream=%d", host.executeCalls, host.streamCalls)
			}
		})
	}
}
func (h *apiKeyPolicyPluginCountHost) CountPluginExecutor(context.Context, string, coreexecutor.Request, coreexecutor.Options) (coreexecutor.Response, error) {
	h.countCalls++
	return coreexecutor.Response{Payload: []byte("5")}, nil
}

func TestAPIKeyPolicyCountValidatesPluginExecutorProvider(t *testing.T) {
	host := &apiKeyPolicyPluginCountHost{}
	handler := NewBaseAPIHandlers(nil, nil)
	handler.SetModelRouterHost(host)
	decision := profileDecisionForTest([]string{"codex"}, []string{"plugin-model"}, nil)
	ctx := apikeypolicy.WithDecision(context.Background(), decision)
	_, _, errMsg := handler.ExecuteCountWithAuthManager(ctx, "openai", "plugin-model", []byte(`{"model":"plugin-model"}`), "")
	if errMsg == nil || errMsg.StatusCode != http.StatusForbidden || host.countCalls != 0 || !strings.Contains(errMsg.Error.Error(), "profile_provider_forbidden") {
		t.Fatalf("calls=%d error=%#v", host.countCalls, errMsg)
	}

	decision = profileDecisionForTest([]string{"gemini"}, []string{"plugin-model"}, nil)
	ctx = apikeypolicy.WithDecision(context.Background(), decision)
	body, _, errMsg := handler.ExecuteCountWithAuthManager(ctx, "openai", "plugin-model", []byte(`{"model":"plugin-model"}`), "")
	if errMsg != nil || string(body) != "5" || host.countCalls != 1 {
		t.Fatalf("body=%q calls=%d error=%#v", body, host.countCalls, errMsg)
	}
}

func TestAPIKeyPolicyRejectsForbiddenModelRouterTarget(t *testing.T) {
	decision := profileDecisionForTest([]string{"codex"}, []string{"allowed-target"}, nil)
	ctx := apikeypolicy.WithDecision(context.Background(), decision.WithModels("alias", "allowed-target"))
	route := modelRouteDecision{Provider: "codex", Model: "forbidden-target(high)"}
	_, _, _, errMsg := applyAPIKeyRoutedModelPolicy(ctx, "allowed-target", []byte(`{"model":"allowed-target"}`), &route)
	if errMsg == nil || errMsg.StatusCode != http.StatusForbidden || !strings.Contains(errMsg.Error.Error(), "profile_model_forbidden") {
		t.Fatalf("route=%#v error=%#v", route, errMsg)
	}
}

func TestAPIKeyPolicyVisibleModelsPreservePassthroughAndCloneMappedMetadata(t *testing.T) {
	models := []map[string]any{
		{"id": "shared-model", "object": "model", "owned_by": "catalog-owner", "context_window": float64(128000)},
		{"id": "forbidden-model", "object": "model", "owned_by": "other-owner"},
	}
	providers := func(model string) []string {
		switch model {
		case "shared-model":
			return []string{"codex", "gemini"}
		case "forbidden-model":
			return []string{"codex"}
		default:
			return nil
		}
	}

	passthrough, errMsg := FilterModelMapsForRequest(context.Background(), models, "id", providers)
	if errMsg != nil || !reflect.DeepEqual(passthrough, models) {
		t.Fatalf("passthrough = %#v, error=%#v", passthrough, errMsg)
	}

	decision := profileDecisionForTest([]string{"gemini"}, []string{"shared-model"}, map[string]string{"smart": "shared-model"})
	ctx := apikeypolicy.WithDecision(context.Background(), decision)
	visible, errMsg := FilterModelMapsForRequest(ctx, models, "id", providers)
	if errMsg != nil || len(visible) != 2 {
		t.Fatalf("visible = %#v, error=%#v", visible, errMsg)
	}
	if visible[0]["id"] != "shared-model" || visible[1]["id"] != "smart" {
		t.Fatalf("visible IDs = %#v", visible)
	}
	for _, model := range visible {
		if model["object"] != "model" || model["owned_by"] != "catalog-owner" || model["context_window"] != float64(128000) {
			t.Fatalf("mapped metadata was not preserved: %#v", model)
		}
	}
	if models[0]["id"] != "shared-model" || len(models[0]) != 4 {
		t.Fatalf("registry-owned source mutated: %#v", models[0])
	}
}

func TestAPIKeyPolicyVisibleModelsRequireOneAllowedCarryingProvider(t *testing.T) {
	decision := profileDecisionForTest([]string{"gemini"}, []string{"shared-model", "codex-only", "unregistered"}, nil)
	ctx := apikeypolicy.WithDecision(context.Background(), decision)
	visible, errMsg := FilterModelsForRequest(ctx, []string{"shared-model", "codex-only", "unregistered"}, func(model string) []string {
		switch model {
		case "shared-model":
			return []string{"codex", "gemini"}
		case "codex-only":
			return []string{"codex"}
		default:
			return nil
		}
	})
	if errMsg != nil || !reflect.DeepEqual(visible, []PolicyVisibleModel{{ID: "shared-model", EffectiveID: "shared-model"}}) {
		t.Fatalf("visible = %#v, error=%#v", visible, errMsg)
	}
}

func TestAPIKeyPolicyVisibleModelsFailClosedForInvalidSnapshot(t *testing.T) {
	ctx := apikeypolicy.WithDecision(context.Background(), apikeypolicy.RequestPolicyDecision{})
	visible, errMsg := FilterModelsForRequest(ctx, []string{"must-not-leak"}, func(string) []string { return []string{"codex"} })
	if visible != nil || errMsg == nil || errMsg.StatusCode != http.StatusServiceUnavailable || !strings.Contains(errMsg.Error.Error(), "api_key_policy_unavailable") {
		t.Fatalf("visible = %#v, error=%#v", visible, errMsg)
	}
}
