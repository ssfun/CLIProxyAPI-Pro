package management

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/embeddedusage"
	prorouting "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/routing"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	coreusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
)

func TestUpdateProAuthSerializesInspectionPriority(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	registered, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "serialized-auth",
		Provider: "xai",
	})
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{authManager: manager}
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- h.updateProAuth(context.Background(), registered.Index, func(auth *coreauth.Auth) {
			close(firstEntered)
			<-releaseFirst
			setProAuthDisabledState(auth, true)
			auth.Metadata[routingProtectionMetadataKey] = map[string]any{"owner": routingProtectionOwner}
		})
	}()
	<-firstEntered
	inspectionDone := make(chan error, 1)
	go func() {
		inspectionDone <- h.updateProAuth(context.Background(), registered.Index, func(auth *coreauth.Auth) {
			setProAuthDisabledState(auth, false)
		})
	}()
	select {
	case err = <-inspectionDone:
		t.Fatalf("inspection mutation bypassed serialization: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(releaseFirst)
	if err = <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err = <-inspectionDone; err != nil {
		t.Fatal(err)
	}
	updated, ok := manager.GetByID(registered.ID)
	if !ok || updated == nil {
		t.Fatal("updated auth missing")
	}
	if updated.Disabled || routingProtectionOwned(updated) {
		t.Fatalf("inspection must win: disabled=%v metadata=%#v", updated.Disabled, updated.Metadata)
	}
}

func TestRoutingProtectionNonMatchingFailureBreaksConfirmationSequence(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	auth, err := manager.Register(context.Background(), &coreauth.Auth{ID: "confirmation-auth", Provider: "xai"})
	if err != nil {
		t.Fatal(err)
	}
	controller := &routingPolicyController{
		h:             &Handler{authManager: manager},
		confirmations: prorouting.NewConfirmationTracker(),
		requestProtection: routingRequestProtectionConfig{
			Enabled: true,
			Mode:    routingProtectionModeObserve,
			Providers: map[string]routingProtectionProviderPolicy{
				"xai": {Enabled: true, StatusCodes: []int{429}, Confirmations: 2, ConfirmationWindowSeconds: 600},
			},
		},
	}
	for _, status := range []int{429, 500, 429} {
		controller.HandleUsage(context.Background(), coreusage.Record{
			Provider: "xai",
			AuthID:   auth.ID,
			Failed:   true,
			Fail:     coreusage.Failure{StatusCode: status},
		})
	}
	events := controller.recentEvents()
	if len(events) != 2 || events[0].Count != 1 || events[0].Action != "pending" {
		t.Fatalf("events = %#v, want restarted 1/2 confirmation", events)
	}
}

func TestRoutingProtectionConfigChangeResetsConfirmationSequence(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	auth, err := manager.Register(context.Background(), &coreauth.Auth{ID: "config-reset-auth", Provider: "xai"})
	if err != nil {
		t.Fatal(err)
	}
	policy := routingProtectionProviderPolicy{
		Enabled: true, StatusCodes: []int{429}, Confirmations: 2, ConfirmationWindowSeconds: 600,
	}
	controller := &routingPolicyController{
		h:             &Handler{authManager: manager},
		confirmations: prorouting.NewConfirmationTracker(),
		requestProtection: routingRequestProtectionConfig{
			Enabled: true,
			Mode:    routingProtectionModeObserve,
			Providers: map[string]routingProtectionProviderPolicy{
				"xai": policy,
			},
		},
	}
	failure := coreusage.Record{
		Provider: "xai", AuthID: auth.ID, Failed: true,
		Fail: coreusage.Failure{StatusCode: http.StatusTooManyRequests},
	}
	controller.HandleUsage(context.Background(), failure)
	controller.setRequestProtectionConfig(routingRequestProtectionConfig{Enabled: false})
	controller.setRequestProtectionConfig(routingRequestProtectionConfig{
		Enabled: true,
		Mode:    routingProtectionModeObserve,
		Providers: map[string]routingProtectionProviderPolicy{
			"xai": policy,
		},
	})
	controller.HandleUsage(context.Background(), failure)
	events := controller.recentEvents()
	if len(events) != 2 || events[0].Action != "pending" || events[0].Count != 1 {
		t.Fatalf("events = %#v, want a new 1/2 confirmation after the config change", events)
	}
}

func TestRoutingProtectionRequiresAllAvailableAuthIdentityFields(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	auth, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "identity-auth",
		Provider: "xai",
		FileName: "identity.json",
		Metadata: map[string]any{"email": "user@example.com", "access_token": "current-token"},
	})
	if err != nil {
		t.Fatal(err)
	}
	controller := &routingPolicyController{h: &Handler{authManager: manager}}
	matching := coreusage.Record{
		Provider:          auth.Provider,
		AuthID:            auth.ID,
		AuthIndex:         auth.Index,
		AccessTokenSHA256: coreauth.AccessTokenSHA256(auth),
	}
	if got := controller.authForRecord(matching); got == nil || got.ID != auth.ID {
		t.Fatalf("matching record resolved auth = %#v", got)
	}

	tests := []struct {
		name   string
		mutate func(*coreusage.Record)
	}{
		{name: "auth id", mutate: func(record *coreusage.Record) { record.AuthID = "other" }},
		{name: "auth index", mutate: func(record *coreusage.Record) { record.AuthIndex = "other" }},
		{name: "provider", mutate: func(record *coreusage.Record) { record.Provider = "codex" }},
		{name: "access token", mutate: func(record *coreusage.Record) { record.AccessTokenSHA256 = strings.Repeat("0", 64) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := matching
			tt.mutate(&record)
			if got := controller.authForRecord(record); got != nil {
				t.Fatalf("mismatched record resolved auth = %#v", got)
			}
		})
	}
}

func TestRoutingProtectionDisableRechecksObservedToken(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	auth, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "reauthenticated-routing-auth",
		Provider: "xai",
		FileName: "reauthenticated.json",
		Metadata: map[string]any{"email": "user@example.com", "access_token": "old-token"},
	})
	if err != nil {
		t.Fatal(err)
	}
	event := routingProtectionEvent{
		Provider:          auth.Provider,
		AuthID:            auth.ID,
		AuthIndex:         auth.Index,
		StatusCode:        http.StatusTooManyRequests,
		accessTokenSHA256: coreauth.AccessTokenSHA256(auth),
	}
	reauthenticated := auth.Clone()
	reauthenticated.Metadata["access_token"] = "new-token"
	if _, err = manager.Update(context.Background(), reauthenticated); err != nil {
		t.Fatal(err)
	}
	controller := &routingPolicyController{h: &Handler{authManager: manager}}
	disabled, err := controller.disableAuth(context.Background(), auth, event)
	if err != nil {
		t.Fatal(err)
	}
	if disabled {
		t.Fatal("routing protection disabled an auth after its observed token changed")
	}
	current, _ := manager.GetByID(auth.ID)
	if current == nil || current.Disabled {
		t.Fatalf("reauthenticated auth mutated = %#v", current)
	}
}

func TestRoutingProtectionSkippedDisableReportsActualOutcome(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	auth, err := manager.Register(context.Background(), &coreauth.Auth{ID: "skipped-disable-auth", Provider: "xai"})
	if err != nil {
		t.Fatal(err)
	}
	applyAuthDisabledState(auth, true)
	if _, err = manager.Update(context.Background(), auth); err != nil {
		t.Fatal(err)
	}
	controller := &routingPolicyController{h: &Handler{authManager: manager}}
	disabled, err := controller.disableAuth(context.Background(), auth, routingProtectionEvent{
		Provider: "xai", StatusCode: http.StatusTooManyRequests,
	})
	if err != nil {
		t.Fatalf("disable auth: %v", err)
	}
	if disabled {
		t.Fatal("routing protection reported a mutation after inspection/manual ownership took priority")
	}
	updated, ok := manager.GetByID(auth.ID)
	if !ok || updated == nil || !updated.Disabled || routingProtectionOwned(updated) {
		t.Fatalf("unexpected auth state after skipped disable: %#v", updated)
	}
}

func TestRoutingProtectionSkippedReleaseReportsActualOutcome(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	auth, err := manager.Register(context.Background(), &coreauth.Auth{ID: "skipped-release-auth", Provider: "xai"})
	if err != nil {
		t.Fatal(err)
	}
	applyAuthDisabledState(auth, true)
	if _, err = manager.Update(context.Background(), auth); err != nil {
		t.Fatal(err)
	}
	controller := &routingPolicyController{h: &Handler{authManager: manager}}
	released, err := controller.releaseAuth(context.Background(), auth)
	if err != nil {
		t.Fatalf("release auth: %v", err)
	}
	if released {
		t.Fatal("routing protection reported a release after ownership was lost")
	}
}

func TestConcurrentRoutingPolicySavesApplyLatestStoredValue(t *testing.T) {
	oldSetAndApply := setAndApplyLatestRoutingPolicyProSetting
	defer func() { setAndApplyLatestRoutingPolicyProSetting = oldSetAndApply }()
	var stored embeddedusage.ProSetting
	firstStored := make(chan struct{})
	releaseFirst := make(chan struct{})
	setAndApplyLatestRoutingPolicyProSetting = func(ctx context.Context, item embeddedusage.ProSetting, apply func(context.Context, embeddedusage.ProSetting) error) error {
		var value routingRequestProtectionConfig
		if err := json.Unmarshal(item.Settings, &value); err != nil {
			return err
		}
		stored = item
		if value.Mode == routingProtectionModeObserve {
			close(firstStored)
			<-releaseFirst
		}
		return apply(ctx, stored)
	}
	h := &Handler{}
	controller := &routingPolicyController{requestProtection: defaultRoutingRequestProtectionConfig()}
	routingPolicyControllers.Store(h, controller)
	defer routingPolicyControllers.Delete(h)
	save := func(mode string) bool {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodPut, "/routing-policy", nil)
		return h.persistRoutingRequestProtection(ctx, routingRequestProtectionConfig{Mode: mode})
	}
	firstDone := make(chan bool, 1)
	go func() { firstDone <- save(routingProtectionModeObserve) }()
	<-firstStored
	if !save(routingProtectionModeEnforce) {
		t.Fatal("second save failed")
	}
	close(releaseFirst)
	if !<-firstDone {
		t.Fatal("first save failed")
	}
	if got := controller.requestProtectionConfig().Mode; got != routingProtectionModeEnforce {
		t.Fatalf("runtime mode = %q, want latest stored enforce", got)
	}
}

func TestRoutingProtectionProviders(t *testing.T) {
	want := []string{
		"antigravity",
		"xai",
		"codex",
		"gemini-cli",
		"gemini",
		"gemini-interactions",
		"vertex",
		"aistudio",
		"claude",
		"kimi",
	}
	if !reflect.DeepEqual(routingProtectionProviders, want) {
		t.Fatalf("providers = %#v want %#v", routingProtectionProviders, want)
	}
}

func TestRoutingPolicyControllerStopWaitsForInFlightUsage(t *testing.T) {
	controller := &routingPolicyController{}
	if !controller.beginUsage() {
		t.Fatal("fresh controller rejected usage")
	}
	stopDone := make(chan struct{})
	go func() {
		controller.stop()
		close(stopDone)
	}()

	deadline := time.Now().Add(time.Second)
	for {
		controller.lifecycleMu.Lock()
		stopped := controller.stopped
		controller.lifecycleMu.Unlock()
		if stopped {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("controller did not enter stopped state")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-stopDone:
		t.Fatal("controller stopped before in-flight usage completed")
	default:
	}

	controller.usageWG.Done()
	select {
	case <-stopDone:
	case <-time.After(time.Second):
		t.Fatal("controller did not stop after in-flight usage completed")
	}
	if controller.beginUsage() {
		controller.usageWG.Done()
		t.Fatal("stopped controller accepted new usage")
	}
}

func TestNormalizeRoutingRequestProtectionConfig(t *testing.T) {
	got := normalizeRoutingRequestProtectionConfig(routingRequestProtectionConfig{
		Mode: "ENFORCE",
		Providers: map[string]routingProtectionProviderPolicy{
			"codex": {
				StatusCodes:               []int{429, 429, 99, 600},
				Confirmations:             9,
				ConfirmationWindowSeconds: 0,
				FallbackDisableMinutes:    20000,
			},
		},
	})
	if got.Mode != routingProtectionModeEnforce {
		t.Fatalf("mode = %q", got.Mode)
	}
	codex := got.Providers["codex"]
	if len(codex.StatusCodes) != 1 || codex.StatusCodes[0] != 429 {
		t.Fatalf("status codes = %#v", codex.StatusCodes)
	}
	defaults := normalizeRoutingRequestProtectionConfig(routingRequestProtectionConfig{}).Providers["codex"]
	if len(defaults.StatusCodes) != 2 || defaults.StatusCodes[0] != http.StatusPaymentRequired || defaults.StatusCodes[1] != http.StatusTooManyRequests {
		t.Fatalf("default status codes = %#v", defaults.StatusCodes)
	}
	if codex.Confirmations != 5 {
		t.Fatalf("confirmations = %d", codex.Confirmations)
	}
	if codex.ConfirmationWindowSeconds != 600 {
		t.Fatalf("confirmation window = %d", codex.ConfirmationWindowSeconds)
	}
	if codex.FallbackDisableMinutes != 10080 {
		t.Fatalf("fallback minutes = %d", codex.FallbackDisableMinutes)
	}
	for _, provider := range routingProtectionProviders {
		if _, ok := got.Providers[provider]; !ok {
			t.Fatalf("provider %s missing", provider)
		}
	}
}

func TestLegacyRoutingRequestProtectionReadLeavesConfigUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := "# keep\nrouting:\n  strategy: fill-first\n  request-protection:\n    enabled: true\n    mode: enforce\n    providers:\n      codex:\n        enabled: true\n        status-codes: [429]\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	value, found, err := readLegacyRoutingRequestProtectionConfig(path)
	if err != nil || !found || !value.Enabled || value.Mode != "enforce" || !value.Providers["codex"].Enabled {
		t.Fatalf("legacy config = %+v, %v, %v", value, found, err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Fatalf("legacy config was modified:\n%s", got)
	}
}

func TestRoutingProtectionAvailableProviders(t *testing.T) {
	available := routingProtectionConfiguredProviderSet(&config.Config{
		GeminiKey:          []config.GeminiKey{{APIKey: "gemini-key"}},
		InteractionsKey:    []config.GeminiKey{{APIKey: "interactions-key"}},
		CodexKey:           []config.CodexKey{{APIKey: "codex-key"}},
		XAIKey:             []config.XAIKey{{APIKey: "xai-key"}},
		VertexCompatAPIKey: []config.VertexCompatKey{{APIKey: "vertex-key"}},
	})
	auths := []*coreauth.Auth{
		{Provider: "antigravity"},
		{Provider: "gemini-cli"},
		{Provider: "aistudio"},
		{Provider: "anthropic"},
		{Provider: "kimi"},
		{Provider: "custom-provider"},
		nil,
	}
	want := []string{
		"antigravity",
		"xai",
		"codex",
		"gemini-cli",
		"gemini",
		"gemini-interactions",
		"vertex",
		"aistudio",
		"claude",
		"kimi",
	}
	if got := orderedRoutingProtectionAvailableProviders(available, auths); !reflect.DeepEqual(got, want) {
		t.Fatalf("providers = %#v want %#v", got, want)
	}
}

func TestRoutingProtectionAuthFileName(t *testing.T) {
	tests := []struct {
		name string
		auth *coreauth.Auth
		want string
	}{
		{
			name: "direct file name",
			auth: &coreauth.Auth{FileName: "antigravity-user@example.com.json"},
			want: "antigravity-user@example.com.json",
		},
		{
			name: "absolute file path",
			auth: &coreauth.Auth{FileName: filepath.Join("tmp", "auth", "antigravity-user.json")},
			want: "antigravity-user.json",
		},
		{
			name: "plugin virtual source",
			auth: &coreauth.Auth{Attributes: map[string]string{
				coreauth.AttributeVirtualSource: filepath.Join("tmp", "auth", "antigravity-plugin.json"),
			}},
			want: "antigravity-plugin.json",
		},
		{
			name: "path attribute fallback",
			auth: &coreauth.Auth{Attributes: map[string]string{
				"path": filepath.Join("tmp", "auth", "antigravity-path.json"),
			}},
			want: "antigravity-path.json",
		},
		{name: "missing file", auth: &coreauth.Auth{}, want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := routingProtectionAuthFileName(test.auth); got != test.want {
				t.Fatalf("file name = %q want %q", got, test.want)
			}
		})
	}
}

func TestRoutingProtectionHasQuotaEvidence(t *testing.T) {
	tests := []struct {
		name   string
		record coreusage.Record
		want   bool
	}{
		{
			name:   "retry after",
			record: coreusage.Record{ResponseHeaders: http.Header{"Retry-After": []string{"30"}}},
			want:   true,
		},
		{
			name:   "codex usage percent",
			record: coreusage.Record{ResponseHeaders: http.Header{"X-Codex-Primary-Used-Percent": []string{"100"}}},
			want:   true,
		},
		{
			name:   "body marker",
			record: coreusage.Record{Fail: coreusage.Failure{Body: `{"error":{"type":"usage_limit_reached"}}`}},
			want:   true,
		},
		{name: "generic 429", record: coreusage.Record{}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := routingProtectionHasQuotaEvidence(test.record); got != test.want {
				t.Fatalf("got %v want %v", got, test.want)
			}
		})
	}
}

func TestRoutingProtectionReasonPreservesCompleteBody(t *testing.T) {
	body := `{"error":{"message":"` + strings.Repeat("detailed upstream response ", 20) + `"}}`
	if len(body) <= 240 {
		t.Fatalf("test body length = %d, want more than 240", len(body))
	}
	got := routingProtectionReason(coreusage.Record{
		Fail: coreusage.Failure{StatusCode: http.StatusTooManyRequests, Body: body},
	})
	if got != body {
		t.Fatalf("reason was truncated: got %d bytes want %d", len(got), len(body))
	}
}

func TestRoutingProtectionRedactsReasonBeforeEventAndAuthPersistence(t *testing.T) {
	startProQuotaTestService(t)
	manager := coreauth.NewManager(nil, nil, nil)
	auth, err := manager.Register(context.Background(), &coreauth.Auth{ID: "redacted-reason-auth", Provider: "xai"})
	if err != nil {
		t.Fatal(err)
	}
	controller := &routingPolicyController{
		h:             &Handler{authManager: manager},
		confirmations: prorouting.NewConfirmationTracker(),
		requestProtection: routingRequestProtectionConfig{
			Enabled: true,
			Mode:    routingProtectionModeEnforce,
			Providers: map[string]routingProtectionProviderPolicy{
				"xai": {Enabled: true, StatusCodes: []int{429}, Confirmations: 1},
			},
		},
	}
	body := `{"error":"quota exceeded","access_token":"sk-secret","authorization":"Bearer private"}`
	controller.HandleUsage(context.Background(), coreusage.Record{
		Provider: "xai", AuthID: auth.ID, Failed: true,
		Fail: coreusage.Failure{StatusCode: http.StatusTooManyRequests, Body: body},
	})
	events := controller.recentEvents()
	if len(events) != 1 || events[0].Action != "cooldown" {
		t.Fatalf("events = %#v", events)
	}
	if strings.Contains(events[0].Reason, "sk-secret") || strings.Contains(events[0].Reason, "Bearer private") {
		t.Fatalf("event exposed a secret: %q", events[0].Reason)
	}
	updated, ok := manager.GetByID(auth.ID)
	if !ok || updated == nil {
		t.Fatal("updated auth missing")
	}
	reason := prorouting.QuotaProtections(updated.Metadata)["routing:"].Reason
	if reason == "" || strings.Contains(reason, "sk-secret") || strings.Contains(reason, "Bearer private") {
		t.Fatalf("persisted reason exposed a secret or was lost: %q", reason)
	}
}

func TestRoutingProtectionReleaseAtPrefersLatestSignal(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	record := coreusage.Record{
		ResponseHeaders: http.Header{
			"Retry-After":                []string{"60"},
			"X-Codex-Primary-Reset-At":   []string{"1700000120"},
			"X-Codex-Secondary-Reset-At": []string{"1700000300"},
		},
		Fail: coreusage.Failure{Body: `{"error":{"resets_in_seconds":180}}`},
	}
	got := routingProtectionReleaseAt(record, routingProtectionProviderPolicy{AutoEnable: true}, now)
	want := now.Add(5 * time.Minute)
	if !got.Equal(want) {
		t.Fatalf("release at = %v want %v", got, want)
	}
}

func TestRoutingProtectionReleaseAtFallback(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	got := routingProtectionReleaseAt(coreusage.Record{}, routingProtectionProviderPolicy{
		AutoEnable:             true,
		FallbackDisableMinutes: 45,
	}, now)
	if want := now.Add(45 * time.Minute); !got.Equal(want) {
		t.Fatalf("release at = %v want %v", got, want)
	}
}

func TestManualDisabledStateClearsRoutingProtectionOwnership(t *testing.T) {
	auth := &coreauth.Auth{
		Disabled: true,
		Metadata: map[string]any{
			"disabled": true,
			routingProtectionMetadataKey: map[string]any{
				"owner": routingProtectionOwner,
			},
		},
	}
	if !routingProtectionOwned(auth) {
		t.Fatal("auth should initially be owned by request protection")
	}

	applyAuthDisabledState(auth, true)

	if !auth.Disabled {
		t.Fatal("manual disabled state should be preserved")
	}
	if routingProtectionOwned(auth) {
		t.Fatal("manual status change must clear request protection ownership")
	}
}

func TestInspectionStateChangeClearsRoutingProtectionOwnership(t *testing.T) {
	for _, disabled := range []bool{true, false} {
		t.Run(strconv.FormatBool(disabled), func(t *testing.T) {
			auth := &coreauth.Auth{
				Disabled: !disabled,
				Metadata: map[string]any{
					routingProtectionMetadataKey: map[string]any{
						"owner": routingProtectionOwner,
					},
				},
			}
			setProAuthDisabledState(auth, disabled)
			if auth.Disabled != disabled {
				t.Fatalf("disabled = %v want %v", auth.Disabled, disabled)
			}
			if routingProtectionOwned(auth) {
				t.Fatal("account inspection must take ownership from request protection")
			}
		})
	}
}

func TestRoutingProtectionDisableRestoresOwnership(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	registered, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "routing-owned-auth",
		Provider: "xai",
	})
	if err != nil {
		t.Fatalf("register auth: %v", err)
	}
	controller := &routingPolicyController{h: &Handler{authManager: manager}}
	disabled, err := controller.disableAuth(context.Background(), registered, routingProtectionEvent{
		Provider:    "xai",
		StatusCode:  http.StatusTooManyRequests,
		Reason:      "quota exhausted",
		TriggeredAt: time.Now().UnixMilli(),
	})
	if err != nil {
		t.Fatalf("disable auth: %v", err)
	}
	if !disabled {
		t.Fatal("routing protection did not report the auth mutation")
	}
	updated, ok := manager.GetByID(registered.ID)
	if !ok || updated == nil {
		t.Fatal("updated auth missing")
	}
	if !updated.Disabled {
		t.Fatal("routing protection should disable auth")
	}
	if !routingProtectionOwned(updated) {
		t.Fatal("routing protection should restore its ownership metadata")
	}
}

func TestRoutingPolicyResponseUsesEmptyCollections(t *testing.T) {
	h := &Handler{}
	response := h.routingPolicyResponse()
	if response.Active == nil {
		t.Fatal("active must serialize as an empty array instead of null")
	}
	if response.RecentEvents == nil {
		t.Fatal("recentEvents must serialize as an empty array instead of null")
	}
}
