package management

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

type proMutationStore struct {
	dir     string
	mu      sync.Mutex
	entered chan struct{}
	release chan struct{}
	fail    bool
	saves   int
}

func (s *proMutationStore) List(context.Context) ([]*coreauth.Auth, error) { return nil, nil }
func (s *proMutationStore) Delete(context.Context, string) error           { return nil }
func (s *proMutationStore) Save(ctx context.Context, a *coreauth.Auth) (string, error) {
	s.mu.Lock()
	entered, release, fail := s.entered, s.release, s.fail
	s.entered, s.release = nil, nil
	s.saves++
	s.mu.Unlock()
	if entered != nil {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if fail {
		return "", errors.New("injected disk failure")
	}
	raw, err := json.Marshal(a)
	if err != nil {
		return "", err
	}
	path := filepath.Join(s.dir, a.ID+".json")
	return path, os.WriteFile(path, raw, 0600)
}
func (s *proMutationStore) gate() (<-chan struct{}, chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entered, s.release = make(chan struct{}), make(chan struct{})
	return s.entered, s.release
}
func (s *proMutationStore) saved(t *testing.T, id string) *coreauth.Auth {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(s.dir, id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var a coreauth.Auth
	if err = json.Unmarshal(raw, &a); err != nil {
		t.Fatal(err)
	}
	return &a
}
func proMutationFixture(t *testing.T) (*Handler, *coreauth.Auth, *proMutationStore) {
	t.Helper()
	store := &proMutationStore{dir: t.TempDir()}
	manager := coreauth.NewManager(store, nil, nil)
	auth, err := manager.Register(context.Background(), &coreauth.Auth{ID: "mutation", FileName: "mutation.json", Provider: "codex", Metadata: map[string]any{"access_token": "old-access", "refresh_token": "old-refresh", "quota_cache": map[string]any{"old": true}}})
	if err != nil {
		t.Fatal(err)
	}
	return &Handler{authManager: manager}, auth, store
}
func proMutationWait(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("mutation timed out")
		return nil
	}
}

func TestProNativeMutationPreservesNativeCommit(t *testing.T) {
	for _, refresh := range []bool{false, true} {
		name := "native-edit"
		if refresh {
			name = "token-refresh"
		}
		t.Run(name, func(t *testing.T) {
			h, base, store := proMutationFixture(t)
			entered, release := store.gate()
			nativeDone := make(chan error, 1)
			refreshedAt := time.Now().UTC().Truncate(time.Second)
			go func() {
				if refresh {
					a := base.Clone()
					a.Metadata["access_token"] = "new-access"
					a.Metadata["refresh_token"] = "new-refresh"
					a.LastRefreshedAt = refreshedAt
					_, err := h.authManager.UpdateRefreshedAuth(context.Background(), base, a)
					nativeDone <- err
					return
				}
				_, _, err := h.authManager.Modify(context.Background(), base.ID, base.RegistrationEpoch, func(a *coreauth.Auth) error {
					a.ProxyURL = "http://native.proxy"
					a.Prefix = "native-prefix"
					a.Metadata["native-note"] = "preserved"
					return nil
				})
				nativeDone <- err
			}()
			<-entered
			proDone := make(chan error, 1)
			waitCtx := proMutationGateContext(context.Background())
			go func() {
				proDone <- h.updateProAuth(waitCtx, base.Index, func(a *coreauth.Auth) { setProAuthDisabledState(a, true) })
			}()
			proMutationAwaitGate(t, waitCtx)
			close(release)
			if err := proMutationWait(t, nativeDone); err != nil {
				t.Fatal(err)
			}
			if err := proMutationWait(t, proDone); err != nil {
				t.Fatal(err)
			}
			runtime, _ := h.authManager.GetByID(base.ID)
			for where, a := range map[string]*coreauth.Auth{"runtime": runtime, "disk": store.saved(t, base.ID)} {
				if !a.Disabled {
					t.Errorf("%s: disable was lost", where)
				}
				if refresh {
					if a.Metadata["access_token"] != "new-access" || a.Metadata["refresh_token"] != "new-refresh" || !a.LastRefreshedAt.Equal(refreshedAt) {
						t.Errorf("%s: refresh overwritten: %#v refreshed=%v", where, a.Metadata, a.LastRefreshedAt)
					}
				} else if a.ProxyURL != "http://native.proxy" || a.Prefix != "native-prefix" || a.Metadata["native-note"] != "preserved" {
					t.Errorf("%s: native fields overwritten: proxy=%q prefix=%q metadata=%#v", where, a.ProxyURL, a.Prefix, a.Metadata)
				}
			}
		})
	}
}

func TestProNativeMutationRejectsOldInspectionEpoch(t *testing.T) {
	for _, cleanup := range []bool{false, true} {
		name := "clear-error"
		if cleanup {
			name = "quota-cache"
		}
		t.Run(name, func(t *testing.T) {
			h, old, store := proMutationFixture(t)
			account := accountFromAuth(old)
			h.authManager.Remove(context.Background(), old.ID)
			replacement := old.Clone()
			replacement.RegistrationEpoch = 0
			replacement.Generation = 0
			replacement.LastError = &coreauth.Error{Code: "inspection_probe_error", Message: "replacement failure"}
			replacement.Unavailable = true
			replacement.Status = coreauth.StatusError
			fresh, err := h.authManager.Register(context.Background(), replacement)
			if err != nil {
				t.Fatal(err)
			}
			if fresh.RegistrationEpoch == old.RegistrationEpoch {
				t.Fatal("epoch did not advance")
			}
			// Registration itself strips legacy quota_cache. Reintroduce it via
			// the native update path to model an existing legacy cache accurately.
			if cleanup {
				_, _, err = h.authManager.Modify(context.Background(), fresh.ID, fresh.RegistrationEpoch, func(a *coreauth.Auth) error {
					a.Metadata["quota_cache"] = map[string]any{"replacement": true}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				before, _ := h.authManager.GetByID(fresh.ID)
				if _, ok := before.Metadata["quota_cache"]; !ok {
					t.Fatal("legacy cache fixture missing")
				}
			}
			scheduler := &accountInspectionScheduler{h: h}
			if cleanup {
				_ = scheduler.cleanupLegacyQuotaCacheFromAuth(context.Background(), account)
			} else {
				scheduler.clearInspectionAuthError(context.Background(), account)
			}
			runtime, _ := h.authManager.GetByID(old.ID)
			for where, a := range map[string]*coreauth.Auth{"runtime": runtime, "disk": store.saved(t, old.ID)} {
				if cleanup {
					if _, ok := a.Metadata["quota_cache"]; !ok {
						t.Errorf("%s: stale inspection removed replacement quota cache", where)
					}
				} else if a.LastError == nil || a.LastError.Message != "replacement failure" {
					t.Errorf("%s: stale inspection cleared replacement error", where)
				}
			}
		})
	}
}

func TestProNativeMutationCancellationAndNil(t *testing.T) {
	h, base, store := proMutationFixture(t)
	if err := h.updateProAuth(context.Background(), base.Index, nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	if err := h.updateProAuth(ctx, base.Index, func(a *coreauth.Auth) { called = true; setProAuthDisabledState(a, true) }); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled error=%v", err)
	}
	if called {
		t.Error("canceled operation invoked callback")
	}
	runtime, _ := h.authManager.GetByID(base.ID)
	if runtime.Disabled || store.saved(t, base.ID).Disabled {
		t.Fatal("canceled mutation changed auth")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.saves != 1 {
		t.Fatalf("nil/canceled callbacks persisted %d times", store.saves)
	}
}

func TestProNativeMutationRejectsIdentityChange(t *testing.T) {
	h, base, store := proMutationFixture(t)
	other, err := h.authManager.Register(context.Background(), &coreauth.Auth{ID: "other", Provider: "codex", Metadata: map[string]any{"access_token": "other-token"}})
	if err != nil {
		t.Fatal(err)
	}
	err = h.updateProAuth(context.Background(), base.Index, func(a *coreauth.Auth) { a.ID = other.ID; a.RegistrationEpoch = 0; setProAuthDisabledState(a, true) })
	if err == nil {
		t.Error("identity-changing callback reported success")
	}
	runtime, _ := h.authManager.GetByID(other.ID)
	if runtime.Disabled || store.saved(t, other.ID).Disabled {
		t.Fatal("callback altered a different identity")
	}
}

func TestProNativeMutationNonfatalStoreFailure(t *testing.T) {
	h, base, store := proMutationFixture(t)
	store.mu.Lock()
	store.fail = true
	store.mu.Unlock()
	if err := h.updateProAuth(context.Background(), base.Index, func(a *coreauth.Auth) { setProAuthDisabledState(a, true) }); err != nil {
		t.Fatalf("native ordinary persistence contract changed: %v", err)
	}
	runtime, _ := h.authManager.GetByID(base.ID)
	if !runtime.Disabled {
		t.Fatal("upstream nonfatal save failure did not commit runtime")
	}
	if store.saved(t, base.ID).Disabled {
		t.Fatal("failed save changed disk")
	}
}

func TestProNativeMutationRecoveryRechecksCurrentError(t *testing.T) {
	h, base, store := proMutationFixture(t)
	_, _, err := h.authManager.Modify(context.Background(), base.ID, base.RegistrationEpoch, func(a *coreauth.Auth) error {
		syncAuthInspectionLastError(a, &coreauth.Error{Code: "inspection_probe_error", Message: "old probe"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	observed, _ := h.authManager.GetByID(base.ID)
	account := accountFromAuth(observed)
	entered, release := store.gate()
	nativeDone := make(chan error, 1)
	go func() {
		_, _, err := h.authManager.Modify(context.Background(), base.ID, base.RegistrationEpoch, func(a *coreauth.Auth) error {
			a.LastError = &coreauth.Error{Code: "native-new-error", Message: "native failure"}
			a.Unavailable = true
			return nil
		})
		nativeDone <- err
	}()
	<-entered
	waitCtx := proMutationGateContext(context.Background())
	done := make(chan error, 1)
	go func() {
		(&accountInspectionScheduler{h: h}).clearInspectionAuthError(waitCtx, account)
		done <- nil
	}()
	proMutationAwaitGate(t, waitCtx)
	close(release)
	if err := proMutationWait(t, nativeDone); err != nil {
		t.Fatal(err)
	}
	proMutationWait(t, done)
	runtime, _ := h.authManager.GetByID(base.ID)
	if runtime.LastError == nil || runtime.LastError.Code != "native-new-error" {
		t.Fatal("old recovery erased newer native failure")
	}
	if a := store.saved(t, base.ID); a.LastError == nil || a.LastError.Code != "native-new-error" {
		t.Fatal("new native error lost on disk")
	}
}

func TestProNativeMutationSharedVirtualSourcePreserved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shared.json")
	raw := []byte(`{"type":"gemini-cli","access_token":"source-token","project_id":"a","project_ids":["a","b"],"custom":"retained"}`)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	manager := coreauth.NewManager(nil, nil, nil)
	var trigger *coreauth.Auth
	for i, id := range []string{"project-a", "project-b"} {
		a := &coreauth.Auth{ID: id, Provider: "gemini-cli", FileName: "shared.json", Metadata: map[string]any{"project_id": id}, Attributes: map[string]string{"path": path}}
		coreauth.MarkPluginVirtualAuth(a, path, i)
		var err error
		trigger, err = manager.Register(context.Background(), a)
		if err != nil {
			t.Fatal(err)
		}
	}
	h := &Handler{authManager: manager}
	if err := h.updateProAuth(context.Background(), trigger.Index, func(a *coreauth.Auth) { setProAuthDisabledState(a, true) }); err != nil {
		t.Fatal(err)
	}
	for _, a := range manager.List() {
		if !a.Disabled {
			t.Fatalf("shared identity %s was not disabled", a.ID)
		}
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var metadata map[string]any
	if err = json.Unmarshal(saved, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata["disabled"] != true || metadata["access_token"] != "source-token" || metadata["project_id"] != "a" || metadata["custom"] != "retained" || len(metadata["project_ids"].([]any)) != 2 {
		t.Fatalf("shared source corrupted: %s", saved)
	}
}

// Done is read by the native mutation gate after the Management snapshot has
// been obtained. This makes release ordering independent of scheduling sleeps.
type proMutationWaitContext struct {
	context.Context
	reached chan struct{}
	once    sync.Once
}

func (c *proMutationWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.reached) })
	return c.Context.Done()
}
func proMutationGateContext(ctx context.Context) *proMutationWaitContext {
	return &proMutationWaitContext{Context: ctx, reached: make(chan struct{})}
}
func proMutationAwaitGate(t *testing.T, ctx *proMutationWaitContext) {
	t.Helper()
	select {
	case <-ctx.reached:
	case <-time.After(5 * time.Second):
		t.Fatal("native mutation gate not reached")
	}
}

func TestProNativeMutationCanceledGateWait(t *testing.T) {
	h, base, store := proMutationFixture(t)
	entered, release := store.gate()
	nativeDone := make(chan error, 1)
	go func() {
		_, _, err := h.authManager.Modify(context.Background(), base.ID, base.RegistrationEpoch, func(a *coreauth.Auth) error { a.Prefix = "native"; return nil })
		nativeDone <- err
	}()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	waitCtx := proMutationGateContext(ctx)
	done := make(chan error, 1)
	called := make(chan struct{}, 1)
	go func() {
		done <- h.updateProAuth(waitCtx, base.Index, func(a *coreauth.Auth) { called <- struct{}{}; setProAuthDisabledState(a, true) })
	}()
	proMutationAwaitGate(t, waitCtx)
	cancel()
	if err := proMutationWait(t, done); !errors.Is(err, context.Canceled) {
		t.Errorf("gate cancellation=%v", err)
	}
	close(release)
	if err := proMutationWait(t, nativeDone); err != nil {
		t.Fatal(err)
	}
	select {
	case <-called:
		t.Error("canceled waiter invoked edit before acquiring native gate")
	default:
	}
	runtime, _ := h.authManager.GetByID(base.ID)
	if runtime.Disabled || store.saved(t, base.ID).Disabled {
		t.Fatal("canceled waiter changed auth")
	}
}

func TestProNativeMutationRecoveryRejectsRefreshedCredentials(t *testing.T) {
	h, base, store := proMutationFixture(t)
	_, _, err := h.authManager.Modify(context.Background(), base.ID, base.RegistrationEpoch, func(a *coreauth.Auth) error {
		syncAuthInspectionLastError(a, &coreauth.Error{Code: "inspection_probe_error", Message: "old probe"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	observed, _ := h.authManager.GetByID(base.ID)
	account := accountFromAuth(observed)
	entered, release := store.gate()
	nativeDone := make(chan error, 1)
	go func() {
		a := observed.Clone()
		a.Metadata["access_token"] = "refreshed"
		_, err := h.authManager.UpdateRefreshedAuth(context.Background(), observed, a)
		nativeDone <- err
	}()
	<-entered
	waitCtx := proMutationGateContext(context.Background())
	done := make(chan error, 1)
	go func() { (&accountInspectionScheduler{h: h}).clearInspectionAuthError(waitCtx, account); done <- nil }()
	proMutationAwaitGate(t, waitCtx)
	close(release)
	if err := proMutationWait(t, nativeDone); err != nil {
		t.Fatal(err)
	}
	proMutationWait(t, done)
	runtime, _ := h.authManager.GetByID(base.ID)
	if runtime.Metadata["access_token"] != "refreshed" {
		t.Fatal("old recovery reverted refreshed token")
	}
	if a := store.saved(t, base.ID); a.Metadata["access_token"] != "refreshed" {
		t.Fatal("old recovery reverted refreshed token on disk")
	}
}
