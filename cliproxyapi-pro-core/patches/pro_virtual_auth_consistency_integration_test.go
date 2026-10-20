package management

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	proinspection "github.com/router-for-me/CLIProxyAPI/v8/internal/pro/inspection"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

type virtualAcceptanceHook struct {
	coreauth.NoopHook
	update func(*coreauth.Auth)
}

func (h *virtualAcceptanceHook) OnAuthUpdated(_ context.Context, a *coreauth.Auth) {
	if h.update != nil {
		h.update(a)
	}
}
func virtualAcceptanceFixture(t *testing.T, mixed bool) (*Handler, *coreauth.Auth, string, *virtualAcceptanceHook) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "shared.json")
	raw := []byte(`{"type":"gemini-cli","access_token":"old-token","custom":"keep","disabled":false,"project_ids":["a","b","c"]}`)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	hook := &virtualAcceptanceHook{}
	m := coreauth.NewManager(nil, nil, hook)
	var trigger *coreauth.Auth
	for i, id := range []string{"a", "b", "c"} {
		a := &coreauth.Auth{ID: id, Provider: "gemini-cli", FileName: "shared.json", Attributes: map[string]string{"path": path}, Metadata: map[string]any{"access_token": "old-token", "custom": "keep", "project_id": id}}
		if !mixed || i != 0 {
			coreauth.MarkPluginVirtualAuth(a, path, i)
		}
		a.BindFileSnapshot(path, raw)
		var err error
		trigger, err = m.Register(context.Background(), a)
		if err != nil {
			t.Fatal(err)
		}
	}
	return &Handler{authManager: m}, trigger, path, hook
}
func virtualAcceptanceSource(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err = json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func virtualAcceptanceRecord(t *testing.T, h *Handler, path string, err error) {
	t.Helper()
	raw, _ := os.ReadFile(path)
	record := map[string]any{"error": fmt.Sprint(err), "source": string(raw), "runtime": h.authManager.List()}
	data, e := json.MarshalIndent(record, "", "  ")
	if e != nil {
		t.Fatal(e)
	}
	t.Log(string(data))
	if root := os.Getenv("VIRTUAL_ACCEPTANCE_EVIDENCE"); root != "" {
		name := strings.ReplaceAll(t.Name(), "/", "-") + ".json"
		if e = os.MkdirAll(root, 0700); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(root, name), data, 0600); e != nil {
			t.Fatal(e)
		}
	}
}
func TestProVirtualConsistencySourceFirst(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		t.Run(fmt.Sprint(mixed), func(t *testing.T) {
			h, trigger, path, hook := virtualAcceptanceFixture(t, mixed)
			calls := 0
			hook.update = func(a *coreauth.Auth) {
				calls++
				if virtualAcceptanceSource(t, path)["disabled"] != true {
					t.Error("runtime published before source commit")
				}
			}
			err := h.updateProAuth(context.Background(), trigger.Index, func(a *coreauth.Auth) { setProAuthDisabledState(a, true) })
			virtualAcceptanceRecord(t, h, path, err)
			if err != nil {
				t.Fatal(err)
			}
			if calls != 3 {
				t.Fatalf("updated %d siblings", calls)
			}
			for _, a := range h.authManager.List() {
				if !a.Disabled || a.Metadata["project_id"] != a.ID {
					t.Fatalf("identity corrupted: %#v", a)
				}
			}
			source := virtualAcceptanceSource(t, path)
			projectIDs, ok := source["project_ids"].([]any)
			if source["custom"] != "keep" || source["access_token"] != "old-token" || !ok || len(projectIDs) != 3 {
				t.Fatal(source)
			}
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatalf("source permissions: %v %v", info, err)
			}
		})
	}
}
func TestProVirtualConsistencySourceFailure(t *testing.T) {
	for _, failure := range []string{"missing", "directory", "malformed", "cancel", "removed-during-prepare", "replaced-during-prepare", "trigger-replaced"} {
		t.Run(failure, func(t *testing.T) {
			h, trigger, path, _ := virtualAcceptanceFixture(t, false)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch failure {
			case "missing":
				os.Remove(path)
			case "directory":
				os.Remove(path)
				os.Mkdir(path, 0700)
			case "malformed":
				os.WriteFile(path, []byte("{"), 0600)
			case "cancel":
				cancel()
			}
			calls := 0
			err := h.updateProAuth(ctx, trigger.Index, func(a *coreauth.Auth) {
				calls++
				if calls == 1 {
					switch failure {
					case "removed-during-prepare":
						if e := os.Remove(path); e != nil {
							t.Fatal(e)
						}
					case "replaced-during-prepare":
						replacement := path + ".new"
						if e := os.WriteFile(replacement, []byte(`{"access_token":"replacement"}`), 0600); e != nil {
							t.Fatal(e)
						}
						if e := os.Rename(replacement, path); e != nil {
							t.Fatal(e)
						}
					case "trigger-replaced":
						fresh := trigger.Clone()
						fresh.Metadata["access_token"] = "replacement"
						if _, e := h.authManager.Register(context.Background(), fresh); e != nil {
							t.Fatal(e)
						}
					}
				}
				setProAuthDisabledState(a, true)
			})
			virtualAcceptanceRecord(t, h, path, err)
			if err == nil {
				t.Fatal("source failure reported success")
			}
			for _, a := range h.authManager.List() {
				if a.Disabled {
					t.Error("runtime changed despite source failure")
				}
			}
			if failure == "missing" || failure == "removed-during-prepare" {
				if _, e := os.Stat(path); !os.IsNotExist(e) {
					t.Fatal("missing source recreated")
				}
			}
			leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".pro-auth-*"))
			if len(leftovers) > 0 {
				t.Fatal(leftovers)
			}
		})
	}
}
func TestProVirtualConsistencyRefreshAndLatestSibling(t *testing.T) {
	h, trigger, path, hook := virtualAcceptanceFixture(t, false)
	calls := 0
	nativeID := ""
	hook.update = func(a *coreauth.Auth) {
		if nativeID != "" {
			return
		}
		nativeID = a.ID
		for _, sibling := range h.authManager.List() {
			if sibling.ID == a.ID {
				continue
			}
			nativeID = sibling.ID
			_, ok, e := h.authManager.Modify(context.Background(), sibling.ID, sibling.RegistrationEpoch, func(latest *coreauth.Auth) error { latest.Metadata["custom"] = "native-commit"; return nil })
			if e != nil || !ok {
				t.Fatalf("native: %v %v", ok, e)
			}
			break
		}
	}
	err := h.updateProAuth(context.Background(), trigger.Index, func(a *coreauth.Auth) {
		calls++
		if calls == 1 {
			if e := os.WriteFile(path, []byte(`{"type":"gemini-cli","access_token":"new-token","custom":"fresh-source","disabled":false}`), 0600); e != nil {
				t.Fatal(e)
			}
			// Native refresh occurs before runtime Modify, not reentrantly within its gate.
			savedHook := hook.update
			hook.update = nil
			for _, base := range h.authManager.List() {
				fresh := base.Clone()
				fresh.Metadata["access_token"] = "new-token"
				if _, e := h.authManager.UpdateRefreshedAuth(context.Background(), base, fresh); e != nil {
					t.Fatal(e)
				}
			}
			hook.update = savedHook
		}
		setProAuthDisabledState(a, true)
	})
	virtualAcceptanceRecord(t, h, path, err)
	if err != nil {
		t.Fatal(err)
	}
	source := virtualAcceptanceSource(t, path)
	if source["access_token"] != "new-token" || source["custom"] != "fresh-source" {
		t.Fatal(source)
	}
	for _, a := range h.authManager.List() {
		if a.Metadata["access_token"] != "new-token" {
			t.Fatal("native token lost")
		}
	}
	a, _ := h.authManager.GetByID(nativeID)
	if a.Metadata["custom"] != "native-commit" {
		t.Fatal("native sibling commit lost")
	}
}
func TestProVirtualConsistencyPartialAndRetry(t *testing.T) {
	for _, failure := range []string{"cancel", "remove", "replace", "move-source"} {
		t.Run(failure, func(t *testing.T) {
			h, trigger, path, hook := virtualAcceptanceFixture(t, false)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			fired := false
			failedID := ""
			hook.update = func(a *coreauth.Auth) {
				if fired {
					return
				}
				fired = true
				for _, next := range h.authManager.List() {
					if next.ID == a.ID {
						continue
					}
					failedID = next.ID
					switch failure {
					case "cancel":
						cancel()
					case "remove":
						h.authManager.Remove(context.Background(), next.ID)
					case "replace":
						next.Metadata["custom"] = "replacement"
						if _, e := h.authManager.Register(context.Background(), next); e != nil {
							t.Fatal(e)
						}
					case "move-source":
						_, _, e := h.authManager.Modify(context.Background(), next.ID, next.RegistrationEpoch, func(current *coreauth.Auth) error {
							current.Attributes[coreauth.AttributeVirtualSource] = path + ".other"
							return nil
						})
						if e != nil {
							t.Fatal(e)
						}
					}
					break
				}
			}
			err := h.updateProAuth(ctx, trigger.Index, func(a *coreauth.Auth) { setProAuthDisabledState(a, true) })
			virtualAcceptanceRecord(t, h, path, err)
			if err == nil || !strings.Contains(err.Error(), "source saved") {
				t.Fatalf("partial outcome missing: %v", err)
			}
			if virtualAcceptanceSource(t, path)["disabled"] != true {
				t.Fatal("committed source rolled back")
			}
			if failure == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation error lost")
			}
			changed := 0
			for _, a := range h.authManager.List() {
				if a.Disabled {
					changed++
				}
				if a.ID == failedID && a.Disabled {
					t.Fatal("failed/replaced identity mutated")
				}
			}
			if failure != "cancel" && changed != 2 {
				t.Fatalf("eligible sibling skipped: %d", changed)
			}
			hook.update = nil
			var retry *coreauth.Auth
			for _, a := range h.authManager.List() {
				if coreauth.IsPluginVirtualAuth(a) && sameAuthSourcePath(pluginVirtualSourcePath(a), path) {
					retry = a
					break
				}
			}
			if err = h.updateProAuth(context.Background(), retry.Index, func(a *coreauth.Auth) { setProAuthDisabledState(a, true) }); err != nil {
				t.Fatal(err)
			}
			for _, a := range h.authManager.List() {
				if sameAuthSourcePath(pluginVirtualSourcePath(a), path) && !a.Disabled {
					t.Fatal("manual retry did not converge")
				}
			}
		})
	}
}
func TestProVirtualConsistencyIdentityLocal(t *testing.T) {
	h, trigger, path, _ := virtualAcceptanceFixture(t, false)
	before, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	e = h.updateProErrorAuthObserved(context.Background(), trigger, nil, func(a *coreauth.Auth) { syncAuthInspectionLastError(a, &coreauth.Error{Code: "inspection_http_error"}) })
	if e != nil {
		t.Fatal(e)
	}
	after, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if string(after) != string(before) {
		t.Fatal("local error changed source")
	}
	for _, a := range h.authManager.List() {
		if (a.LastError != nil) != (a.ID == trigger.ID) {
			t.Fatal("identity-local error propagated")
		}
	}
}
func TestProVirtualConsistencyHTTPPartial(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, trigger, path, hook := virtualAcceptanceFixture(t, false)
	result := accountFromAuth(trigger).baseResult()
	scheduler := &accountInspectionScheduler{h: h, path: filepath.Join(filepath.Dir(path), "schedule.json"), status: accountInspectionStatus{Results: []accountInspectionResult{result}}, subscribers: make(map[chan accountInspectionLogStreamMessage]struct{})}
	accountInspectionSchedulers.Store(h, scheduler)
	t.Cleanup(func() { accountInspectionSchedulers.Delete(h) })
	fired := false
	hook.update = func(a *coreauth.Auth) {
		if fired {
			return
		}
		fired = true
		for _, b := range h.authManager.List() {
			if b.ID != a.ID {
				h.authManager.Remove(context.Background(), b.ID)
				break
			}
		}
	}
	item := proinspection.ActionItemFromResult(result, accountInspectionActionDisable)
	raw, e := json.Marshal(accountInspectionActionRequest{Items: []accountInspectionActionItem{item}})
	if e != nil {
		t.Fatal(e)
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/account-inspection/actions", strings.NewReader(string(raw)))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), inspectionBatchAuditContextKey{}, true))
	h.ExecuteAccountInspectionActions(c)
	t.Logf("HTTP %d: %s", recorder.Code, recorder.Body.String())
	if !strings.Contains(recorder.Body.String(), "source saved") || !strings.Contains(recorder.Body.String(), `"success":false`) {
		t.Fatal("partial outcome not visible to HTTP client")
	}
	virtualAcceptanceRecord(t, h, path, nil)
}

// Filesystem faults are injected only into the writer's private operation value.
// Every test uses real source/temp files and checks original bytes plus cleanup.
type virtualFaultTemp struct {
	*os.File
	stage string
}

func (f virtualFaultTemp) Chmod(mode os.FileMode) error {
	if f.stage == "chmod" {
		return errors.New("chmod failure")
	}
	return f.File.Chmod(mode)
}
func (f virtualFaultTemp) Write(raw []byte) (int, error) {
	if f.stage == "write" {
		return 0, errors.New("write failure")
	}
	return f.File.Write(raw)
}
func (f virtualFaultTemp) Sync() error {
	if f.stage == "sync" {
		return errors.New("sync failure")
	}
	return f.File.Sync()
}
func (f virtualFaultTemp) Close() error {
	err := f.File.Close()
	if f.stage == "close" {
		return errors.New("close failure")
	}
	return err
}
func TestProVirtualConsistencyAtomicWriteFailures(t *testing.T) {
	for _, stage := range []string{"create", "chmod", "write", "sync", "close", "conflict", "rename", "cancel"} {
		t.Run(stage, func(t *testing.T) {
			_, _, path, _ := virtualAcceptanceFixture(t, false)
			before, e := os.ReadFile(path)
			if e != nil {
				t.Fatal(e)
			}
			snapshot, e := readProAuthSource(path)
			if e != nil {
				t.Fatal(e)
			}
			ops := proAuthSourceFileOps{createTemp: func(dir, pattern string) (proAuthSourceTempFile, error) {
				if stage == "create" {
					return nil, errors.New("create failure")
				}
				f, e := os.CreateTemp(dir, pattern)
				if e != nil {
					return nil, e
				}
				return virtualFaultTemp{f, stage}, nil
			}, rename: func(from, to string) error {
				if stage == "rename" {
					return errors.New("rename failure")
				}
				return os.Rename(from, to)
			}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			guard := func() error {
				if stage == "conflict" {
					return os.WriteFile(path, []byte(`{"external":"new"}`), 0600)
				}
				if stage == "cancel" {
					cancel()
				}
				return nil
			}
			_, e = writeProAuthSource(ctx, snapshot, []byte(`{"disabled":true}`), guard, ops)
			if e == nil {
				t.Fatal("fault did not fail")
			}
			after, e := os.ReadFile(path)
			if e != nil {
				t.Fatal(e)
			}
			if stage != "conflict" && string(after) != string(before) {
				t.Fatal("original truncated")
			}
			if stage == "conflict" && string(after) != `{"external":"new"}` {
				t.Fatal("concurrent write overwritten")
			}
			leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".pro-auth-*"))
			if len(leftovers) > 0 {
				t.Fatal(leftovers)
			}
		})
	}
}
