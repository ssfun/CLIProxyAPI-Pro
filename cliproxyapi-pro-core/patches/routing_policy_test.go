package management

import (
	"context"
	"net/http"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	coreusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
)

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
	got := normalizeRoutingRequestProtectionConfig(config.RequestProtectionConfig{
		Mode: "ENFORCE",
		Providers: map[string]config.RequestProtectionProviderPolicy{
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
	got := routingProtectionReleaseAt(record, config.RequestProtectionProviderPolicy{AutoEnable: true}, now)
	want := now.Add(5 * time.Minute)
	if !got.Equal(want) {
		t.Fatalf("release at = %v want %v", got, want)
	}
}

func TestRoutingProtectionReleaseAtFallback(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	got := routingProtectionReleaseAt(coreusage.Record{}, config.RequestProtectionProviderPolicy{
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
			setAuthInspectionDisabledState(auth, disabled)
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
	err = controller.disableAuth(context.Background(), registered, routingProtectionEvent{
		Provider:    "xai",
		StatusCode:  http.StatusTooManyRequests,
		Reason:      "quota exhausted",
		TriggeredAt: time.Now().UnixMilli(),
	})
	if err != nil {
		t.Fatalf("disable auth: %v", err)
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
