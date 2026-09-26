package oauthpolicy

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	modelconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/oauthpolicy/config"
	modelengine "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/oauthpolicy/policy"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/pro/settings"
)

type memorySettingsStore struct {
	mu    sync.Mutex
	items map[string]settings.Item
}

type planSettingsStore struct {
	*memorySettingsStore
	raw          []byte
	observedAtMS int64
	provider     string
	fileName     string
	authIndex    string
}

type blockingPlanSettingsStore struct {
	*memorySettingsStore
	mu           sync.Mutex
	raw          []byte
	observedAtMS int64
	firstRead    chan struct{}
	releaseFirst chan struct{}
	reads        atomic.Int32
}

func (s *blockingPlanSettingsStore) GetPlanSnapshot(_ context.Context, _, _, _ string) (PlanSnapshot, bool, error) {
	s.mu.Lock()
	raw := append([]byte(nil), s.raw...)
	observedAtMS := s.observedAtMS
	s.mu.Unlock()
	if s.reads.Add(1) == 1 {
		close(s.firstRead)
		<-s.releaseFirst
	}
	return PlanSnapshot{Data: raw, ObservedAtMS: observedAtMS}, len(raw) > 0, nil
}

func (s *blockingPlanSettingsStore) setSnapshot(raw string, observedAtMS int64) {
	s.mu.Lock()
	s.raw = []byte(raw)
	s.observedAtMS = observedAtMS
	s.mu.Unlock()
}

func (s *planSettingsStore) GetPlanSnapshot(_ context.Context, provider, fileName, authIndex string) (PlanSnapshot, bool, error) {
	s.provider, s.fileName, s.authIndex = provider, fileName, authIndex
	if len(s.raw) == 0 {
		return PlanSnapshot{}, false, nil
	}
	return PlanSnapshot{Data: append([]byte(nil), s.raw...), ObservedAtMS: s.observedAtMS}, true, nil
}

func (s *memorySettingsStore) Get(_ context.Context, namespace string) (settings.Item, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, found := s.items[namespace]
	return item, found, nil
}

func (s *memorySettingsStore) Put(_ context.Context, item settings.Item) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	item.Settings = append(json.RawMessage(nil), item.Settings...)
	s.items[item.Namespace] = item
	return nil
}

func (s *memorySettingsStore) Delete(_ context.Context, namespace string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, namespace)
	return nil
}

func (*memorySettingsStore) Subscribe(string, func(context.Context, settings.Item) error) func() {
	return func() {}
}

func TestNewMigratesLegacyNamespace(t *testing.T) {
	store := &memorySettingsStore{items: map[string]settings.Item{
		settings.LegacyNamespaceOAuthModelPolicy: {
			Namespace:     settings.LegacyNamespaceOAuthModelPolicy,
			SchemaVersion: 1,
			Settings:      json.RawMessage(`{"enabled":true,"providers":{"codex":{"plans":{"pro":{"excluded-models":[]}}}}}`),
		},
	}}
	service, err := New(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	service.Close()
	if _, found := store.items[settings.LegacyNamespaceOAuthModelPolicy]; found {
		t.Fatal("legacy namespace survived migration")
	}
	if _, found := store.items[settings.NamespaceOAuthPolicy]; !found {
		t.Fatal("new OAuth policy namespace was not written")
	}
}

func TestNewRejectsCorruptPersistedConfig(t *testing.T) {
	store := &memorySettingsStore{items: map[string]settings.Item{
		settings.NamespaceOAuthPolicy: {
			Namespace: settings.NamespaceOAuthPolicy, SchemaVersion: 1,
			Settings: json.RawMessage(`{"enabled":true,"providers":`),
		},
	}}
	if service, err := New(context.Background(), store); err == nil || service != nil {
		t.Fatalf("New() = %#v, %v; want startup error", service, err)
	}
}

// Failure cases: a failed current read must not migrate legacy data; invalid
// current or legacy settings must not delete the only recoverable legacy copy.
type failedCurrentReadStore struct{ *memorySettingsStore }

func (s *failedCurrentReadStore) Get(ctx context.Context, namespace string) (settings.Item, bool, error) {
	if namespace == settings.NamespaceOAuthPolicy {
		return settings.Item{}, false, fmt.Errorf("current read failed")
	}
	return s.memorySettingsStore.Get(ctx, namespace)
}

func TestNewDoesNotMutateSettingsAfterReadOrValidationFailure(t *testing.T) {
	for _, scenario := range []string{"read-error", "invalid-current", "invalid-legacy", "unsupported-legacy"} {
		t.Run(scenario, func(t *testing.T) {
			legacy := settings.Item{Namespace: settings.LegacyNamespaceOAuthModelPolicy, SchemaVersion: 1, Settings: json.RawMessage(`{"enabled":true}`)}
			memory := &memorySettingsStore{items: map[string]settings.Item{legacy.Namespace: legacy}}
			var store settings.Store = memory
			switch scenario {
			case "read-error":
				store = &failedCurrentReadStore{memory}
			case "invalid-current":
				memory.items[settings.NamespaceOAuthPolicy] = settings.Item{Namespace: settings.NamespaceOAuthPolicy, SchemaVersion: 1, Settings: json.RawMessage(`{"cache-ttl":"invalid"}`)}
			case "invalid-legacy":
				legacy.Settings = json.RawMessage(`{"cache-ttl":"invalid"}`)
				memory.items[legacy.Namespace] = legacy
			case "unsupported-legacy":
				legacy.SchemaVersion = 99
				memory.items[legacy.Namespace] = legacy
			}
			before, _ := json.Marshal(memory.items)
			service, err := New(context.Background(), store)
			if service != nil {
				service.Close()
			}
			if err == nil {
				t.Error("expected startup error")
			}
			after, _ := json.Marshal(memory.items)
			if string(before) != string(after) {
				t.Fatalf("failed startup mutated settings: before=%s after=%s", before, after)
			}
		})
	}
}

func TestFilterReadsFreshQuotaSnapshotByAuthIdentity(t *testing.T) {
	store := &planSettingsStore{
		memorySettingsStore: &memorySettingsStore{items: map[string]settings.Item{
			settings.NamespaceOAuthPolicy: {
				Namespace: settings.NamespaceOAuthPolicy, SchemaVersion: 1,
				Settings: json.RawMessage(`{"enabled":true,"providers":{"gemini-cli":{"plans":{"pro":{"priority":42}}}}}`),
			},
		}},
		raw:          []byte(`{"schema_version":1,"plan":{"id":"standard-tier","label":"Google AI Pro","kind":"standard"}}`),
		observedAtMS: time.Now().UnixMilli(),
	}
	service, err := New(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	result := service.Filter(context.Background(), modelengine.Input{
		AuthID: "gemini-1", AuthIndex: "auth-index-1", FileName: "gemini.json",
		AuthProvider: "gemini-cli", AuthKind: "oauth",
	})
	if !result.Handled || result.Annotations["plan_key"] != "pro" || result.Annotations["plan_source"] != "quota-provider" {
		t.Fatalf("Filter() = %#v", result)
	}
	if store.provider != "gemini-cli" || store.fileName != "gemini.json" || store.authIndex != "auth-index-1" {
		t.Fatalf("snapshot identity = %q, %q, %q", store.provider, store.fileName, store.authIndex)
	}
}

func TestFilterUsesAuthCardPreferredPlanSnapshot(t *testing.T) {
	now := time.Now().UnixMilli()
	store := &planSettingsStore{
		memorySettingsStore: &memorySettingsStore{items: map[string]settings.Item{
			settings.NamespaceOAuthPolicy: {
				Namespace: settings.NamespaceOAuthPolicy, SchemaVersion: 1,
				Settings: json.RawMessage(`{"enabled":true,"providers":{"antigravity":{"plans":{"ultra":{"priority":42},"_unknown":{"priority":1}}}}}`),
			},
		}},
		raw:          []byte(`{"status":"success","subscription":{"plan":"ultra"}}`),
		observedAtMS: now,
	}
	service, err := New(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	result := service.Filter(context.Background(), modelengine.Input{
		AuthID: "antigravity-1", AuthIndex: "idx", FileName: "account.json",
		AuthProvider: "antigravity", AuthKind: "oauth",
	})
	if !result.Handled || result.Annotations["plan_key"] != "ultra" || result.Annotations["plan_source"] != "quota-inspection" {
		t.Fatalf("Filter() = %#v", result)
	}
}

func TestUpdateConfigDoesNotWaitForAccountRefresh(t *testing.T) {
	store := &memorySettingsStore{items: map[string]settings.Item{}}
	service, err := New(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	service.SetChangeHandler(func(ctx context.Context) {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
		}
		close(finished)
	})
	cfg, err := modelconfig.Parse([]byte(`{"enabled":true,"providers":{"codex":{"plans":{"pro":{"priority":10}}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	returned := make(chan error, 1)
	go func() { returned <- service.UpdateConfig(context.Background(), cfg) }()
	select {
	case err = <-returned:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("UpdateConfig waited for account refresh")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("account refresh did not start")
	}
	close(release)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("account refresh did not finish")
	}
	service.Close()
}

func TestUpdateConfigCoalescesConcurrentAccountRefreshes(t *testing.T) {
	store := &memorySettingsStore{items: map[string]settings.Item{}}
	service, err := New(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondFinished := make(chan struct{})
	var calls atomic.Int32
	service.SetChangeHandler(func(ctx context.Context) {
		switch calls.Add(1) {
		case 1:
			close(firstStarted)
			select {
			case <-releaseFirst:
			case <-ctx.Done():
			}
		case 2:
			close(secondFinished)
		}
	})
	for priority := 1; priority <= 3; priority++ {
		raw := []byte(fmt.Sprintf(`{"enabled":true,"providers":{"codex":{"plans":{"pro":{"priority":%d}}}}}`, priority))
		cfg, errParse := modelconfig.Parse(raw)
		if errParse != nil {
			t.Fatal(errParse)
		}
		if err = service.UpdateConfig(context.Background(), cfg); err != nil {
			t.Fatal(err)
		}
		if priority == 1 {
			select {
			case <-firstStarted:
			case <-time.After(time.Second):
				t.Fatal("first account refresh did not start")
			}
		}
	}
	close(releaseFirst)
	select {
	case <-secondFinished:
	case <-time.After(time.Second):
		t.Fatal("coalesced account refresh did not finish")
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("account refresh calls = %d, want 2", got)
	}
}

func TestRefreshPlanDetectionCoalescesEngineInvalidation(t *testing.T) {
	store := &memorySettingsStore{items: map[string]settings.Item{}}
	service, err := New(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondFinished := make(chan struct{})
	var calls atomic.Int32
	service.SetChangeHandler(func(ctx context.Context) {
		switch calls.Add(1) {
		case 1:
			close(firstStarted)
			select {
			case <-releaseFirst:
			case <-ctx.Done():
			}
		case 2:
			close(secondFinished)
		}
	})
	initialRevision := service.revision
	service.RefreshPlanDetection()
	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first refresh did not start")
	}
	for range 3 {
		service.RefreshPlanDetection()
	}
	service.mu.RLock()
	revision := service.revision
	service.mu.RUnlock()
	if revision != initialRevision+1 {
		t.Fatalf("revision = %d, want %d", revision, initialRevision+1)
	}
	close(releaseFirst)
	select {
	case <-secondFinished:
	case <-time.After(time.Second):
		t.Fatal("coalesced follow-up refresh did not finish")
	}
	if calls.Load() != 2 {
		t.Fatalf("refresh calls = %d, want 2", calls.Load())
	}
	service.mu.RLock()
	revision = service.revision
	service.mu.RUnlock()
	if revision != initialRevision+2 {
		t.Fatalf("revision after follow-up = %d, want %d", revision, initialRevision+2)
	}
}

func TestFilterRetriesWithLatestConfigAfterConcurrentUpdate(t *testing.T) {
	store := &memorySettingsStore{items: map[string]settings.Item{}}
	service, err := New(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	enabled, _ := modelconfig.Parse([]byte(`{"enabled":true,"providers":{"claude":{"plans":{"pro":{"priority":99}}}}}`))
	if err = service.UpdateConfig(context.Background(), enabled); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan modelengine.Result, 1)
	var calls atomic.Int32
	storage, _ := json.Marshal(map[string]any{"access_token": "token"})
	go func() {
		finished <- service.Filter(context.Background(), modelengine.Input{
			AuthID: "claude-1", AuthProvider: "claude", AuthKind: "oauth", StorageJSON: storage,
			HTTPDo: func(context.Context, modelengine.HTTPRequest) (modelengine.HTTPResponse, error) {
				if calls.Add(1) == 1 {
					close(started)
					<-release
					return modelengine.HTTPResponse{StatusCode: 200, Body: []byte(`{"account":{"has_claude_pro":true}}`)}, nil
				}
				return modelengine.HTTPResponse{StatusCode: 200, Body: []byte(`{"account":{"has_claude_max":true}}`)}, nil
			},
		})
	}()
	<-started
	latest, _ := modelconfig.Parse([]byte(`{"enabled":true,"providers":{"claude":{"plans":{"max":{"priority":55}}}}}`))
	if err = service.UpdateConfig(context.Background(), latest); err != nil {
		t.Fatal(err)
	}
	close(release)
	if result := <-finished; !result.Handled || result.Annotations["plan_key"] != "max" || result.Priority == nil || *result.Priority != 55 {
		t.Fatalf("Filter() did not use latest config and probe result: %#v", result)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("provider lookup calls = %d, want 2", got)
	}
	if result, found := service.EffectivePolicy("claude-1"); !found || result.Annotations["plan_key"] != "max" {
		t.Fatalf("effective policy = %#v, found = %t", result, found)
	}
}

func TestFilterReloadsPlanSnapshotAfterConcurrentRefresh(t *testing.T) {
	now := time.Now().UnixMilli()
	store := &blockingPlanSettingsStore{
		memorySettingsStore: &memorySettingsStore{items: map[string]settings.Item{
			settings.NamespaceOAuthPolicy: {
				Namespace: settings.NamespaceOAuthPolicy, SchemaVersion: 1,
				Settings: json.RawMessage(`{"enabled":true,"providers":{"antigravity":{"plans":{"ultra":{"priority":90},"pro":{"priority":50}}}}}`),
			},
		}},
		firstRead: make(chan struct{}), releaseFirst: make(chan struct{}),
	}
	store.setSnapshot(`{"status":"success","subscription":{"plan":"ultra"}}`, now)
	service, err := New(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	finished := make(chan modelengine.Result, 1)
	go func() {
		finished <- service.Filter(context.Background(), modelengine.Input{
			AuthID: "antigravity-race", AuthGeneration: 1, AuthRegistrationEpoch: 1,
			CredentialFingerprint: "credential", AuthIndex: "idx", FileName: "account.json",
			AuthProvider: "antigravity", AuthKind: "oauth",
		})
	}()
	<-store.firstRead
	store.setSnapshot(`{"status":"success","subscription":{"plan":"pro"}}`, time.Now().UnixMilli())
	service.RefreshPlanDetection()
	close(store.releaseFirst)
	result := <-finished
	if !result.Handled || result.Annotations["plan_key"] != "pro" || result.Priority == nil || *result.Priority != 50 {
		t.Fatalf("Filter() retained stale snapshot: %#v", result)
	}
	if got, found := service.EffectivePolicy("antigravity-race"); !found || got.Annotations["plan_key"] != "pro" {
		t.Fatalf("effective policy = %#v, found = %t", got, found)
	}
	if got := store.reads.Load(); got < 2 {
		t.Fatalf("snapshot reads = %d, want at least 2", got)
	}
}

func TestNewerAuthVersionRejectsOlderInFlightDecision(t *testing.T) {
	store := &memorySettingsStore{items: map[string]settings.Item{
		settings.NamespaceOAuthPolicy: {
			Namespace: settings.NamespaceOAuthPolicy, SchemaVersion: 1,
			Settings: json.RawMessage(`{"enabled":true,"providers":{"claude":{"plans":{"pro":{"priority":90},"max":{"priority":50}}}}}`),
		},
	}}
	service, err := New(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	started := make(chan struct{})
	release := make(chan struct{})
	oldFinished := make(chan modelengine.Result, 1)
	go func() {
		oldFinished <- service.Filter(context.Background(), modelengine.Input{
			AuthID: "version-race", AuthGeneration: 1, AuthRegistrationEpoch: 1,
			CredentialFingerprint: "old", AuthProvider: "claude", AuthKind: "oauth",
			Metadata: map[string]any{"access_token": "old"},
			HTTPDo: func(context.Context, modelengine.HTTPRequest) (modelengine.HTTPResponse, error) {
				close(started)
				<-release
				return modelengine.HTTPResponse{StatusCode: 200, Body: []byte(`{"account":{"has_claude_pro":true}}`)}, nil
			},
		})
	}()
	<-started
	newResult := service.Filter(context.Background(), modelengine.Input{
		AuthID: "version-race", AuthGeneration: 2, AuthRegistrationEpoch: 1,
		CredentialFingerprint: "new", AuthProvider: "claude", AuthKind: "oauth",
		Metadata: map[string]any{"access_token": "new"},
		HTTPDo: func(context.Context, modelengine.HTTPRequest) (modelengine.HTTPResponse, error) {
			return modelengine.HTTPResponse{StatusCode: 200, Body: []byte(`{"account":{"has_claude_max":true}}`)}, nil
		},
	})
	close(release)
	if oldResult := <-oldFinished; oldResult.Handled {
		t.Fatalf("older auth version committed: %#v", oldResult)
	}
	if !newResult.Handled || newResult.Annotations["plan_key"] != "max" {
		t.Fatalf("new auth result = %#v", newResult)
	}
	if got, found := service.EffectivePolicy("version-race"); !found || got.Annotations["plan_key"] != "max" {
		t.Fatalf("effective policy = %#v, found = %t", got, found)
	}
}

func TestForgetAuthClearsStateAndRejectsInFlightResult(t *testing.T) {
	store := &memorySettingsStore{items: map[string]settings.Item{}}
	service, err := New(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	cfg, _ := modelconfig.Parse([]byte(`{"enabled":true,"providers":{"claude":{"plans":{"pro":{"priority":99}}}}}`))
	if err = service.UpdateConfig(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan modelengine.Result, 1)
	go func() {
		finished <- service.Filter(context.Background(), modelengine.Input{
			AuthID: "removed", AuthProvider: "claude", AuthKind: "oauth",
			Metadata: map[string]any{"access_token": "token"},
			HTTPDo: func(context.Context, modelengine.HTTPRequest) (modelengine.HTTPResponse, error) {
				close(started)
				<-release
				return modelengine.HTTPResponse{StatusCode: 200, Body: []byte(`{"account":{"has_claude_pro":true}}`)}, nil
			},
		})
	}()
	<-started
	service.ForgetAuth("removed")
	close(release)
	if result := <-finished; result.Handled {
		t.Fatalf("removed auth retained in-flight result: %#v", result)
	}
	if _, found := service.EffectivePolicy("removed"); found {
		t.Fatal("removed auth retained effective policy")
	}
}
