package cliproxy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/embeddedusage"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/pluginhost"
	proapp "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/app"
	policyconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/oauthpolicy/config"
	internalregistry "github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	runtimeexecutor "github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestBuiltInOAuthPolicyConstrainsRegistrationAndSelection(t *testing.T) {
	t.Setenv("USAGE_DB_PATH", filepath.Join(t.TempDir(), "usage.sqlite"))
	t.Setenv("USAGE_SERVICE_ENABLED", "true")
	ctx, cancel := context.WithCancel(context.Background())
	usageService, err := embeddedusage.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	embeddedusage.SetDefaultService(usageService)
	t.Cleanup(func() {
		embeddedusage.SetDefaultService(nil)
		cancel()
	})
	settings := json.RawMessage(`{
		"enabled": true,
		"cache-ttl": "30m",
		"resolve-timeout": "15s",
		"providers": {"xai": {"plans": {
			"free": {"excluded-models": ["grok-imagine-video"]},
			"supergrok": {"excluded-models": ["grok-imagine-image"]}
		}}}
	}`)
	if err := embeddedusage.SetProSetting(ctx, embeddedusage.ProSetting{
		Namespace: embeddedusage.ProSettingNamespaceOAuthPolicy, SchemaVersion: 1, Settings: settings,
	}); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.ParseConfigBytes([]byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := proapp.New(ctx, filepath.Join(t.TempDir(), "missing-config.yaml"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)

	manager := coreauth.NewManager(nil, nil, nil)
	service := &Service{cfg: cfg, proApp: runtime, coreManager: manager}
	freeAuth := &coreauth.Auth{
		ID: "xai-free-auth", Provider: "xai", Status: coreauth.StatusActive,
		Attributes: map[string]string{"auth_kind": "oauth"},
		Metadata:   map[string]any{"access_token": "header." + base64.RawURLEncoding.EncodeToString([]byte(`{"tier":0}`)) + ".signature"},
	}
	superGrokAuth := &coreauth.Auth{
		ID: "xai-supergrok-auth", Provider: "xai", Status: coreauth.StatusActive,
		Attributes: map[string]string{"auth_kind": "oauth"},
		Metadata:   map[string]any{"access_token": "header." + base64.RawURLEncoding.EncodeToString([]byte(`{"tier":1}`)) + ".signature"},
	}

	modelRegistry := internalregistry.GetGlobalRegistry()
	modelRegistry.UnregisterClient(freeAuth.ID)
	modelRegistry.UnregisterClient(superGrokAuth.ID)
	t.Cleanup(func() {
		modelRegistry.UnregisterClient(freeAuth.ID)
		modelRegistry.UnregisterClient(superGrokAuth.ID)
	})
	if _, err := manager.Register(ctx, freeAuth); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Register(ctx, superGrokAuth); err != nil {
		t.Fatal(err)
	}
	service.registerModelsForAuth(ctx, freeAuth)
	service.registerModelsForAuth(ctx, superGrokAuth)

	const freeOnlyModel = "grok-imagine-image"
	const superGrokOnlyModel = "grok-imagine-video"
	if !modelRegistry.ClientSupportsModel(freeAuth.ID, freeOnlyModel) || modelRegistry.ClientSupportsModel(freeAuth.ID, superGrokOnlyModel) {
		t.Fatalf("free auth model set was not filtered: %#v", modelRegistry.GetModelsForClient(freeAuth.ID))
	}
	if modelRegistry.ClientSupportsModel(superGrokAuth.ID, freeOnlyModel) || !modelRegistry.ClientSupportsModel(superGrokAuth.ID, superGrokOnlyModel) {
		t.Fatalf("SuperGrok auth model set was not filtered: %#v", modelRegistry.GetModelsForClient(superGrokAuth.ID))
	}
	service.registerExecutorForAuth(freeAuth, false)
	selected, err := manager.SelectAuth(ctx, "xai", superGrokOnlyModel, cliproxyexecutor.Options{})
	if err != nil || selected == nil || selected.ID != superGrokAuth.ID {
		t.Fatalf("selected auth = %#v, %v; want %s", selected, err, superGrokAuth.ID)
	}
	if _, found := runtime.OAuthPolicy().EffectivePolicy(freeAuth.ID); !found {
		t.Fatal("free auth account policy was not recorded before removal")
	}
	service.applyCoreAuthRemoval(ctx, freeAuth.ID)
	if _, found := runtime.OAuthPolicy().EffectivePolicy(freeAuth.ID); found {
		t.Fatal("removed auth retained its effective account policy")
	}
}

func TestOAuthPolicyInFlightRegistrationCannotRestoreRemovedAuthModels(t *testing.T) {
	t.Setenv("USAGE_DB_PATH", filepath.Join(t.TempDir(), "usage.sqlite"))
	t.Setenv("USAGE_SERVICE_ENABLED", "true")
	ctx, cancel := context.WithCancel(context.Background())
	usageService, err := embeddedusage.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	embeddedusage.SetDefaultService(usageService)
	t.Cleanup(func() {
		embeddedusage.SetDefaultService(nil)
		cancel()
	})
	settings := json.RawMessage(`{
		"enabled": true,
		"cache-ttl": "30m",
		"resolve-timeout": "15s",
		"providers": {"claude": {"plans": {
			"pro": {"excluded-models": ["claude-opus-4-1-20250805"]}
		}}}
	}`)
	if err = embeddedusage.SetProSetting(ctx, embeddedusage.ProSetting{
		Namespace: embeddedusage.ProSettingNamespaceOAuthPolicy, SchemaVersion: 1, Settings: settings,
	}); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.ParseConfigBytes([]byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := proapp.New(ctx, filepath.Join(t.TempDir(), "missing-config.yaml"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(runtimeexecutor.NewClaudeExecutor(cfg))
	service := &Service{cfg: cfg, proApp: runtime, coreManager: manager}
	auth := &coreauth.Auth{
		ID: "claude-in-flight-removal", FileName: "claude-in-flight-removal.json", Provider: "claude", Status: coreauth.StatusActive,
		Attributes: map[string]string{"auth_kind": "oauth"}, Metadata: map[string]any{"access_token": "claude-token"},
	}
	if _, err = manager.Register(ctx, auth); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	requestCtx := context.WithValue(ctx, "cliproxy.roundtripper", roundTripFunc(func(*http.Request) (*http.Response, error) {
		close(started)
		<-release
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"account":{"has_claude_pro":true}}`)),
		}, nil
	}))
	done := make(chan struct{})
	go func() {
		service.registerModelsForAuth(requestCtx, auth)
		close(done)
	}()
	<-started
	service.applyCoreAuthRemoval(ctx, auth.ID)
	close(release)
	<-done
	if _, found := manager.GetByID(auth.ID); found {
		t.Fatal("removed auth was restored to the manager")
	}
	if models := internalregistry.GetGlobalRegistry().GetModelsForClient(auth.ID); len(models) != 0 {
		t.Fatalf("removed auth models were restored by in-flight registration: %#v", models)
	}
}

func TestQueuedModelRegistrationCannotReplaceRecreatedAuthModels(t *testing.T) {
	ctx := context.Background()
	manager := coreauth.NewManager(nil, nil, nil)
	service := &Service{coreManager: manager}
	stale := &coreauth.Auth{ID: "reused-auth-id", Provider: "claude", Status: coreauth.StatusActive}
	if _, err := manager.Register(ctx, stale); err != nil {
		t.Fatal(err)
	}
	queuedCtx := service.beginAuthModelRegistration(ctx, stale.ID)
	service.applyCoreAuthRemoval(ctx, stale.ID)

	replacement := &coreauth.Auth{ID: stale.ID, Provider: "xai", Status: coreauth.StatusActive}
	if _, err := manager.Register(ctx, replacement); err != nil {
		t.Fatal(err)
	}
	modelRegistry := internalregistry.GetGlobalRegistry()
	t.Cleanup(func() { modelRegistry.UnregisterClient(stale.ID) })
	service.registerModelsForAuth(ctx, replacement)
	service.registerModelsForAuth(queuedCtx, stale)

	models := modelRegistry.GetModelsForClient(stale.ID)
	if len(models) == 0 {
		t.Fatal("recreated xAI auth has no registered models")
	}
	for _, model := range models {
		if model != nil && strings.HasPrefix(model.ID, "claude-") {
			t.Fatalf("queued stale snapshot replaced recreated auth models: %#v", models)
		}
	}
}

func TestStalePluginAuthUpdateCannotReplaceRecreatedAuth(t *testing.T) {
	ctx := context.Background()
	manager := coreauth.NewManager(nil, nil, nil)
	service := &Service{coreManager: manager}
	stale := &coreauth.Auth{ID: "reused-plugin-auth-id", Provider: "claude", Label: "old", Status: coreauth.StatusActive}
	if _, err := manager.Register(ctx, stale); err != nil {
		t.Fatal(err)
	}
	queuedCtx := service.beginAuthModelRegistration(ctx, stale.ID)
	service.applyCoreAuthRemoval(ctx, stale.ID)

	replacement := &coreauth.Auth{ID: stale.ID, Provider: "xai", Label: "replacement", Status: coreauth.StatusActive}
	if _, err := manager.Register(ctx, replacement); err != nil {
		t.Fatal(err)
	}
	staleUpdate := stale.Clone()
	staleUpdate.Label = "stale-plugin-update"
	if _, committed, _ := service.updateAuthForCurrentModelRegistration(queuedCtx, stale, staleUpdate); committed {
		t.Fatal("stale plugin auth update committed after the auth ID was recreated")
	}
	current, found := manager.GetByID(stale.ID)
	if !found || current.Provider != "xai" || current.Label != "replacement" {
		t.Fatalf("recreated auth was overwritten by stale plugin update: %#v", current)
	}
}

func TestCurrentRegistrationTokenCannotUpdateSupersededAuthInputs(t *testing.T) {
	ctx := context.Background()
	manager := coreauth.NewManager(nil, nil, nil)
	service := &Service{coreManager: manager}
	auth, err := manager.Register(ctx, &coreauth.Auth{ID: "current-token-stale-inputs", Provider: "claude", Prefix: "old", Status: coreauth.StatusActive})
	if err != nil {
		t.Fatal(err)
	}
	registrationCtx := service.beginAuthModelRegistration(ctx, auth.ID)
	current := auth.Clone()
	current.Prefix = "new"
	if _, err = manager.Update(ctx, current); err != nil {
		t.Fatal(err)
	}
	staleUpdate := auth.Clone()
	staleUpdate.Label = "stale-plugin-update"
	if _, committed, _ := service.updateAuthForCurrentModelRegistration(registrationCtx, auth, staleUpdate); committed {
		t.Fatal("current queue token allowed a plugin update from superseded registration inputs")
	}
	current, found := manager.GetByID(auth.ID)
	if !found || current.Prefix != "new" || current.Label == "stale-plugin-update" {
		t.Fatalf("superseded plugin update replaced current auth: %#v", current)
	}
}

func TestCurrentRegistrationTokenPreservesPublicationFence(t *testing.T) {
	for _, unregister := range []bool{false, true} {
		name := "publish"
		if unregister {
			name = "unregister"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			manager := coreauth.NewManager(nil, nil, nil)
			service := &Service{coreManager: manager}
			auth, err := manager.Register(ctx, &coreauth.Auth{ID: "current-token-stale-epoch-" + name, Provider: "claude", Status: coreauth.StatusActive})
			if err != nil {
				t.Fatal(err)
			}
			modelRegistry := GlobalModelRegistry()
			t.Cleanup(func() { modelRegistry.UnregisterClient(auth.ID) })
			registrationCtx := service.beginAuthModelRegistration(ctx, auth.ID)
			fence := newModelRegistrationFence(auth, service.currentConfig())
			modelRegistry.RegisterClient(auth.ID, "claude", []*ModelInfo{{ID: "newer-model"}})
			if unregister {
				service.unregisterModelsForCurrentAuth(registrationCtx, auth, fence)
			} else {
				service.registerResolvedModelsForCurrentAuth(registrationCtx, auth, fence, "claude", []*ModelInfo{{ID: "stale-model"}})
			}
			models := modelRegistry.GetModelsForClient(auth.ID)
			if len(models) != 1 || models[0].ID != "newer-model" {
				t.Fatalf("current queue token bypassed the original publication fence: %#v", models)
			}
		})
	}
}

func TestAntigravityOAuthPolicyDynamicLifecycle(t *testing.T) {
	resetAntigravityCapabilityCache()
	t.Cleanup(resetAntigravityCapabilityCache)
	t.Setenv("USAGE_DB_PATH", filepath.Join(t.TempDir(), "usage.sqlite"))
	t.Setenv("USAGE_SERVICE_ENABLED", "true")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	usage, err := embeddedusage.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	embeddedusage.SetDefaultService(usage)
	t.Cleanup(func() { embeddedusage.SetDefaultService(nil) })
	raw := []byte(`{"enabled":true,"providers":{"antigravity":{"plans":{"ultra":{"priority":10,"weight":7,"prefix":"ultra","excluded-models":["gemini-3-flash"]}}}}}`)
	if err := embeddedusage.SetProSetting(ctx, embeddedusage.ProSetting{Namespace: embeddedusage.ProSettingNamespaceOAuthPolicy, SchemaVersion: 1, Settings: raw}); err != nil {
		t.Fatal(err)
	}
	app, err := proapp.New(ctx, filepath.Join(t.TempDir(), "missing.yaml"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":{"gemini-3.1-flash-lite":{},"gemini-3-flash":{}}}`))
	}))
	defer server.Close()
	manager := coreauth.NewManager(nil, nil, nil)
	manager.SetAccountPolicyResolver(app.ApplyCachedAccountPolicy)
	hostConfig := &config.Config{}
	hostConfig.ForceModelPrefix = true
	svc := &Service{cfg: hostConfig, coreManager: manager, proApp: app}
	auth := antigravityTestAuth("pro-antigravity-policy-lifecycle", server.URL)
	auth.Prefix = "base"
	auth.Attributes["priority"] = "2"
	auth.Attributes[coreauth.AttributeWeight] = "3"
	auth.Metadata["currentTier"] = map[string]any{"id": "free-tier"}
	auth.Metadata["paidTier"] = map[string]any{"id": "g1-ultra-tier"}
	auth, err = manager.Register(ctx, auth)
	if err != nil {
		t.Fatal(err)
	}
	defer GlobalModelRegistry().UnregisterClient(auth.ID)
	check := func(stage string) {
		t.Helper()
		entries := app.OAuthPolicy().EffectivePolicies()
		if len(entries) != 1 || entries[0].PlanKey != "ultra" || entries[0].Priority != 10 || entries[0].Weight != 7 {
			t.Fatalf("%s effective=%+v", stage, entries)
		}
		models := GlobalModelRegistry().GetModelsForClient(auth.ID)
		if len(models) != 1 || models[0].ID != "ultra/gemini-3.1-flash-lite" {
			t.Fatalf("%s models=%+v", stage, models)
		}
		current, _ := manager.GetByID(auth.ID)
		current = app.ApplyCachedAccountPolicy(current)
		if current.Prefix != "ultra" || current.Attributes["priority"] != "10" || current.Attributes[coreauth.AttributeWeight] != "7" {
			t.Fatalf("%s current=%+v", stage, current)
		}
		t.Logf("%s: ultra priority=10 weight=7 prefix=ultra exclusion verified, capability calls=%d", stage, calls.Load())
	}
	svc.registerModelsForAuth(ctx, auth)
	svc.WaitAntigravityProbes()
	check("first HTTP publication")
	current, _ := manager.GetByID(auth.ID)
	current = app.ApplyCachedAccountPolicy(current)
	svc.registerModelsForAuth(ctx, current)
	svc.WaitAntigravityProbes()
	check("cached publication")
	app.OAuthPolicy().RefreshPlanDetection()
	svc.registerModelsForAuth(ctx, current)
	svc.WaitAntigravityProbes()
	check("refresh publication")
	if calls.Load() != 1 {
		t.Fatalf("cache missed: %d", calls.Load())
	}
	disabled, err := policyconfig.Parse([]byte(`{"enabled":false}`))
	if err != nil {
		t.Fatal(err)
	}
	if err = app.UpdateOAuthConfig(ctx, disabled); err != nil {
		t.Fatal(err)
	}
	svc.registerModelsForAuth(ctx, current)
	svc.WaitAntigravityProbes()
	if got := app.OAuthPolicy().EffectivePolicies(); len(got) != 0 {
		t.Fatalf("disabled effective=%+v", got)
	}
	models := GlobalModelRegistry().GetModelsForClient(auth.ID)
	if len(models) != 2 {
		t.Fatalf("disabled models=%+v", models)
	}
	for _, model := range models {
		if !strings.HasPrefix(model.ID, "base/") {
			t.Fatalf("disabled prefix=%s", model.ID)
		}
	}
	restored, _ := manager.GetByID(auth.ID)
	if restored.Prefix != "base" || restored.Attributes["priority"] != "2" || restored.Attributes[coreauth.AttributeWeight] != "3" {
		t.Fatalf("disabled base not restored: %+v", restored)
	}
	t.Log("disabled policy: original prefix/priority/weight and excluded model restored")

	started, release := make(chan struct{}), make(chan struct{})
	delayed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		_, _ = w.Write([]byte(`{"models":{"gemini-3-flash":{}}}`))
	}))
	defer delayed.Close()
	stale := restored.Clone()
	stale.Attributes["base_url"] = delayed.URL
	stale, err = manager.Update(ctx, stale)
	if err != nil {
		t.Fatal(err)
	}
	svc.registerModelsForAuth(ctx, stale)
	<-started
	enabled, err := policyconfig.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err = app.UpdateOAuthConfig(ctx, enabled); err != nil {
		t.Fatal(err)
	}
	latest := restored.Clone()
	latest.Attributes["base_url"] = server.URL
	latest, err = manager.Update(ctx, latest)
	if err != nil {
		t.Fatal(err)
	}
	svc.registerModelsForAuth(ctx, latest)
	close(release)
	svc.WaitAntigravityProbes()
	check("delayed replaced account and changed policy")
}

// The store blocks a newer persisted management update while the service reads
// the old manager snapshot and queues an unversioned watcher update behind it.
type proAuthUpdateGateStore struct{ entered, release chan struct{} }

func (s *proAuthUpdateGateStore) List(context.Context) ([]*coreauth.Auth, error) { return nil, nil }
func (s *proAuthUpdateGateStore) Delete(context.Context, string) error           { return nil }
func (s *proAuthUpdateGateStore) Save(_ context.Context, a *coreauth.Auth) (string, error) {
	if a.Prefix == "new" {
		close(s.entered)
		<-s.release
	}
	return "", nil
}

type proObservedMutationContext struct {
	context.Context
	observed chan struct{}
	once     sync.Once
}

func (c *proObservedMutationContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.observed) })
	return c.Context.Done()
}

func TestProWatcherUpdateCannotOverwriteConcurrentPersistedFields(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	service := &Service{cfg: &config.Config{}, coreManager: manager}
	initial, err := manager.Register(context.Background(), &coreauth.Auth{ID: t.Name(), Provider: "codex", Prefix: "old", Status: coreauth.StatusActive, Metadata: map[string]any{"prefix": "old"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { GlobalModelRegistry().UnregisterClient(initial.ID) })
	store := &proAuthUpdateGateStore{entered: make(chan struct{}), release: make(chan struct{})}
	manager.SetStore(store)
	newer := initial.Clone()
	newer.Prefix = "new"
	newer.Metadata["prefix"] = "new"
	persisted := make(chan error, 1)
	go func() { _, err := manager.Update(context.Background(), newer); persisted <- err }()
	<-store.entered
	stale := initial.Clone()
	stale.Generation = 0
	observed := &proObservedMutationContext{Context: context.Background(), observed: make(chan struct{})}
	finished := make(chan struct{})
	go func() { service.applyCoreAuthAddOrUpdate(coreauth.WithSkipPersist(observed), stale); close(finished) }()
	<-observed.observed
	close(store.release)
	if err := <-persisted; err != nil {
		t.Fatal(err)
	}
	<-finished
	current, ok := manager.GetByID(initial.ID)
	if !ok || current.Prefix != "new" || current.Metadata["prefix"] != "new" {
		t.Fatalf("watcher rolled back persisted fields: %+v", current)
	}
	if !GlobalModelRegistry().ClientSupportsModel(initial.ID, "new/gpt-6-astra") {
		t.Fatal("latest persisted prefix was not registered")
	}
}

func TestProWatcherSnapshotPreservesConcurrentRequestResult(t *testing.T) {
	ctx := context.Background()
	manager := coreauth.NewManager(nil, nil, nil)
	base, err := manager.Register(ctx, &coreauth.Auth{ID: t.Name(), Provider: "codex", Prefix: "old", Status: coreauth.StatusActive, Metadata: map[string]any{"prefix": "old"}})
	if err != nil {
		t.Fatal(err)
	}
	manager.MarkResult(ctx, coreauth.Result{AuthID: base.ID, Provider: "codex", Model: "gpt-6-astra", Success: false, Error: &coreauth.Error{HTTPStatus: 429, Message: "quota"}})
	before, _ := manager.GetByID(base.ID)
	if before.Generation <= base.Generation {
		t.Fatal("fixture did not advance runtime generation")
	}
	update := base.Clone()
	update.Generation = 0
	update.Prefix = "new"
	update.Metadata["prefix"] = "new"
	current, err := manager.UpdateFromSnapshot(coreauth.WithSkipPersist(ctx), base, update)
	if err != nil {
		t.Fatal(err)
	}
	if current == nil || current.Prefix != "new" {
		t.Fatalf("runtime result dropped legitimate watcher edit: %+v", current)
	}
	if current.Failed != before.Failed || !reflect.DeepEqual(current.ModelStates, before.ModelStates) {
		t.Fatal("watcher discarded concurrent request state")
	}
}

func TestProDelayedWatcherObservationReloadsPersistedFile(t *testing.T) {
	ctx := context.Background()
	manager := coreauth.NewManager(nil, nil, nil)
	current, err := manager.Register(ctx, &coreauth.Auth{ID: t.Name(), Provider: "codex", Prefix: "new", Status: coreauth.StatusActive, Attributes: map[string]string{"path": "/auth/account.json", "source_backend": "file"}, Metadata: map[string]any{"prefix": "new"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { GlobalModelRegistry().UnregisterClient(current.ID) })
	observed := false
	service := &Service{cfg: &config.Config{}, coreManager: manager, watcher: &WatcherWrapper{snapshotAuthFile: func(path string) ([]*coreauth.Auth, error) {
		observed = true
		fresh := current.Clone()
		fresh.Generation = 0
		return []*coreauth.Auth{fresh}, nil
	}}}
	stale := current.Clone()
	stale.Generation = 0
	stale.Prefix = "old"
	stale.Metadata["prefix"] = "old"
	service.applyCoreAuthAddOrUpdate(coreauth.WithSkipPersist(ctx), stale)
	got, _ := manager.GetByID(current.ID)
	if !observed || got.Prefix != "new" {
		t.Fatalf("delayed observation replayed stale fields: observed=%v prefix=%s", observed, got.Prefix)
	}
}

type acceptanceRegistrar struct{ provider string }

func (r acceptanceRegistrar) RegisterModels(context.Context, pluginapi.ModelRegistrationRequest) (pluginapi.ModelRegistrationResponse, error) {
	return pluginapi.ModelRegistrationResponse{Provider: r.provider, Models: []pluginapi.ModelInfo{{ID: "policy-blocked-plugin"}, {ID: "policy-allowed-plugin"}}}, nil
}
func TestAcceptanceOrdinaryProviderPluginPolicy(t *testing.T) {
	t.Setenv("USAGE_DB_PATH", filepath.Join(t.TempDir(), "usage.sqlite"))
	t.Setenv("USAGE_SERVICE_ENABLED", "true")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	usage, err := embeddedusage.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	embeddedusage.SetDefaultService(usage)
	defer embeddedusage.SetDefaultService(nil)
	err = embeddedusage.SetProSetting(ctx, embeddedusage.ProSetting{Namespace: embeddedusage.ProSettingNamespaceOAuthPolicy, SchemaVersion: 1, Settings: json.RawMessage(`{"enabled":true,"providers":{"xai":{"plans":{"free":{"excluded-models":["policy-blocked-plugin","grok-imagine-video"]}}}}}`)})
	if err != nil {
		t.Fatal(err)
	}
	app, err := proapp.New(ctx, filepath.Join(t.TempDir(), "missing.yaml"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	manager := coreauth.NewManager(nil, nil, nil)
	host := pluginhost.New()
	registry := internalregistry.GetGlobalRegistry()
	host.RegisterPluginForTest("acceptance-models", pluginapi.Plugin{Capabilities: pluginapi.Capabilities{ModelRegistrar: acceptanceRegistrar{provider: "xai"}}})
	host.RegisterModels(ctx, registry)
	host.RegisterExecutors(manager, registry)
	if got := host.ModelsForProvider("xai"); len(got) != 2 {
		t.Fatalf("fixture contributed %d models, want 2", len(got))
	}
	a := &coreauth.Auth{ID: "acceptance-plugin-xai", Provider: "xai", Status: coreauth.StatusActive, Attributes: map[string]string{"auth_kind": "oauth"}, Metadata: map[string]any{"access_token": "header." + base64.RawURLEncoding.EncodeToString([]byte(`{"tier":0}`)) + ".signature"}}
	defer registry.UnregisterClient(a.ID)
	if host.ModelsForAuth(ctx, a).Handled {
		t.Fatal("fixture unexpectedly handled per-auth models")
	}
	if _, err = manager.Register(ctx, a); err != nil {
		t.Fatal(err)
	}
	s := &Service{cfg: &config.Config{OAuthModelAlias: map[string][]config.OAuthModelAlias{"xai": {{Name: "grok-imagine-image", Alias: "native-allowed-alias"}, {Name: "policy-allowed-plugin", Alias: "plugin-alias-must-not-apply"}}}}, proApp: app, coreManager: manager, pluginHost: host}
	s.registerModelsForAuth(ctx, a)
	if !registry.ClientSupportsModel(a.ID, "policy-allowed-plugin") {
		t.Error("allowed plugin model missing")
	}
	if !registry.ClientSupportsModel(a.ID, "native-allowed-alias") {
		t.Error("native alias missing")
	}
	if registry.ClientSupportsModel(a.ID, "plugin-alias-must-not-apply") {
		t.Error("plugin alias changed prior behavior")
	}
	if registry.ClientSupportsModel(a.ID, "grok-imagine-video") {
		t.Error("excluded native model survived")
	}
	count := -1
	for _, effective := range app.OAuthPolicy().EffectivePolicies() {
		if effective.AuthID == a.ID {
			count = effective.ExcludedCount
		}
	}
	if count != 2 {
		t.Errorf("combined policy excluded count = %d, want 2", count)
	}
	if registry.ClientSupportsModel(a.ID, "policy-blocked-plugin") {
		t.Error("excluded plugin model was appended after OAuth policy filtering")
	}
}

func TestAcceptanceAntigravityProviderPluginPolicy(t *testing.T) {
	catalog := internalregistry.GetAntigravityModels()
	if len(catalog) < 2 {
		t.Fatal("need two native models")
	}
	blocked, allowed := catalog[0].ID, catalog[1].ID
	t.Setenv("USAGE_DB_PATH", filepath.Join(t.TempDir(), "usage.sqlite"))
	t.Setenv("USAGE_SERVICE_ENABLED", "true")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	usage, err := embeddedusage.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	embeddedusage.SetDefaultService(usage)
	defer embeddedusage.SetDefaultService(nil)
	err = embeddedusage.SetProSetting(ctx, embeddedusage.ProSetting{Namespace: embeddedusage.ProSettingNamespaceOAuthPolicy, SchemaVersion: 1, Settings: json.RawMessage(`{"enabled":true,"providers":{"antigravity":{"plans":{"pro":{"excluded-models":["policy-blocked-plugin","` + blocked + `"]}}}}}`)})
	if err != nil {
		t.Fatal(err)
	}
	app, err := proapp.New(ctx, filepath.Join(t.TempDir(), "missing.yaml"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	manager := coreauth.NewManager(nil, nil, nil)
	host := pluginhost.New()
	registry := internalregistry.GetGlobalRegistry()
	host.RegisterPluginForTest("acceptance-models", pluginapi.Plugin{Capabilities: pluginapi.Capabilities{ModelRegistrar: acceptanceRegistrar{provider: "antigravity"}}})
	host.RegisterModels(ctx, registry)
	host.RegisterExecutors(manager, registry)
	if got := host.ModelsForProvider("antigravity"); len(got) != 2 {
		t.Fatalf("fixture contributed %d models, want 2", len(got))
	}
	a := &coreauth.Auth{ID: "acceptance-plugin-antigravity", Provider: "antigravity", Status: coreauth.StatusActive, Attributes: map[string]string{"auth_kind": "oauth"}, Metadata: map[string]any{"paidTier": map[string]any{"id": "g1-pro-tier"}}}
	defer registry.UnregisterClient(a.ID)
	if host.ModelsForAuth(ctx, a).Handled {
		t.Fatal("fixture unexpectedly handled per-auth models")
	}
	if _, err = manager.Register(ctx, a); err != nil {
		t.Fatal(err)
	}
	s := &Service{cfg: &config.Config{OAuthModelAlias: map[string][]config.OAuthModelAlias{"antigravity": {{Name: allowed, Alias: "native-allowed-alias"}, {Name: "policy-allowed-plugin", Alias: "plugin-alias-must-not-apply"}}}}, proApp: app, coreManager: manager, pluginHost: host}
	models := s.antigravityModelsForHintsWithConfig(a, antigravityModelCapabilityHints{ModelIDs: map[string]struct{}{blocked: {}, allowed: {}}}, s.cfg)
	registry.RegisterClient(a.ID, a.Provider, models)
	if !registry.ClientSupportsModel(a.ID, "policy-allowed-plugin") {
		t.Error("allowed plugin model missing")
	}
	if !registry.ClientSupportsModel(a.ID, "native-allowed-alias") {
		t.Error("native alias missing")
	}
	if registry.ClientSupportsModel(a.ID, "plugin-alias-must-not-apply") {
		t.Error("plugin alias changed prior behavior")
	}
	if registry.ClientSupportsModel(a.ID, blocked) {
		t.Error("excluded native model survived")
	}
	count := -1
	for _, effective := range app.OAuthPolicy().EffectivePolicies() {
		if effective.AuthID == a.ID {
			count = effective.ExcludedCount
		}
	}
	if count != 2 {
		t.Errorf("combined policy excluded count = %d, want 2", count)
	}
	if registry.ClientSupportsModel(a.ID, "policy-blocked-plugin") {
		t.Error("excluded plugin model was appended after OAuth policy filtering")
	}
}
