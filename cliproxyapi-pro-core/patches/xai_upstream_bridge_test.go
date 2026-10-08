package executor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/embeddedusage"
	proquota "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/quota"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/requestmeta"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

// HTTP headers observed through the real request observer must merge with the
// same SQLite identity used by inspection/manual writers, whether metadata,
// attributes, or the access JWT carries the stable subject.
func TestXAIRequestObserverHTTPAndSQLiteIdentity(t *testing.T) {
	for _, source := range []string{"metadata", "attributes", "jwt", "email"} {
		t.Run(source, func(t *testing.T) {
			t.Setenv("USAGE_DB_PATH", filepath.Join(t.TempDir(), "usage.sqlite"))
			t.Setenv("USAGE_SERVICE_ENABLED", "false")
			ctx, cancel := context.WithCancel(context.Background())
			service, err := embeddedusage.Start(ctx)
			if err != nil {
				t.Fatal(err)
			}
			embeddedusage.SetDefaultService(service)
			t.Cleanup(func() { embeddedusage.SetDefaultService(nil); cancel() })
			auth := &cliproxyauth.Auth{ID: "same.json", FileName: "same.json", Provider: "xai", Index: "same-index", Label: "Cosmetic label", Metadata: map[string]any{}, Attributes: map[string]string{"using_api": "false"}}
			subject, email, token := "stable-subject", "", ""
			switch source {
			case "metadata":
				auth.Metadata["sub"] = subject
			case "attributes":
				auth.Attributes["sub"] = subject
			case "jwt":
				token = "header." + base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"stable-subject","tier":0}`)) + ".signature"
				auth.Metadata["access_token"] = token
			case "email":
				subject, email = "", "stable@example.test"
				auth.Metadata["email"] = email
			}
			fingerprint := proquota.XAIQuotaIdentityFingerprint(auth.FileName, subject, email, token)
			now := time.Now().UnixMilli()
			if err := embeddedusage.MergeXAIQuotaCache(ctx, embeddedusage.QuotaCacheEntry{Provider: "xai", FileName: auth.FileName, AuthIndex: auth.Index, IdentityFingerprint: fingerprint, Data: json.RawMessage(`{"billing":{"planLabel":"Same Plan"}}`), CachedAt: now, ObservedAt: now}); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Ratelimit-Limit-Tokens", "100")
				w.Header().Set("X-Ratelimit-Remaining-Tokens", "40")
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()
			observerCtx := withXAIQuotaObserver(ctx, auth, "grok-free")
			request, _ := http.NewRequestWithContext(observerCtx, http.MethodGet, server.URL, nil)
			response, err := server.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			requestmeta.ObserveUpstreamResponse(observerCtx, response.StatusCode, response.Header, nil)
			entries, err := embeddedusage.GetQuotaCache(ctx, "xai", auth.FileName)
			if err != nil || len(entries) != 1 {
				t.Fatalf("entries=%v err=%v", entries, err)
			}
			var state map[string]any
			_ = json.Unmarshal(entries[0].Data, &state)
			billing := state["billing"].(map[string]any)
			if entries[0].IdentityFingerprint != fingerprint || billing["planLabel"] != "Same Plan" || billing["freeQuota"] == nil {
				t.Fatalf("observer identity mismatch: source=%s entry=%+v", source, entries[0])
			}
			t.Logf("source=%s fingerprint=%s state=%s", source, fingerprint, entries[0].Data)
		})
	}
}

func TestProXAIChatRequestHeadersReuseUpstreamIdentity(t *testing.T) {
	auth := &cliproxyauth.Auth{
		Provider:   "xai",
		Attributes: map[string]string{"using_api": "false"},
	}
	headers := XAIChatRequestHeaders(auth, "token", false)
	if got := headers.Get(xaiClientVersionHeader); got != xaiClientVersionValue {
		t.Fatalf("%s = %q, want %q", xaiClientVersionHeader, got, xaiClientVersionValue)
	}
	if got := headers.Get("User-Agent"); got != "xai-grok-workspace/"+xaiClientVersionValue {
		t.Fatalf("User-Agent = %q", got)
	}
}

func TestProXAIHTTPRequestIdentityOverridesStaleManagementHeaders(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://cli-chat-proxy.grok.com/v1/billing", nil)
	req.Header.Set(xaiClientVersionHeader, "0.2.91")
	req.Header.Set("User-Agent", "grok-pager/0.2.91")
	auth := &cliproxyauth.Auth{Provider: "xai", Attributes: map[string]string{"using_api": "false"}}
	applyProXAIHTTPRequestIdentity(req, auth)
	if got := req.Header.Get(xaiClientVersionHeader); got != xaiClientVersionValue {
		t.Fatalf("%s = %q, want %q", xaiClientVersionHeader, got, xaiClientVersionValue)
	}
	if got := req.Header.Get("User-Agent"); got != "xai-grok-workspace/"+xaiClientVersionValue {
		t.Fatalf("User-Agent = %q", got)
	}
}

func TestShouldObserveXAIQuotaOnlyForCLIChatProxy(t *testing.T) {
	tests := []struct {
		name string
		auth *cliproxyauth.Auth
		want bool
	}{
		{
			name: "explicit cli mode",
			auth: &cliproxyauth.Auth{Provider: "xai", Attributes: map[string]string{"using_api": "false"}},
			want: true,
		},
		{
			name: "oauth default",
			auth: &cliproxyauth.Auth{Provider: "xai", Attributes: map[string]string{"auth_kind": "oauth"}},
			want: true,
		},
		{
			name: "official api",
			auth: &cliproxyauth.Auth{Provider: "xai", Attributes: map[string]string{"using_api": "true"}},
		},
		{
			name: "custom gateway",
			auth: &cliproxyauth.Auth{Provider: "xai", Attributes: map[string]string{"using_api": "false", "base_url": "https://gateway.example/v1"}},
		},
		{name: "other provider", auth: &cliproxyauth.Auth{Provider: "codex"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldObserveXAIQuota(tt.auth); got != tt.want {
				t.Fatalf("shouldObserveXAIQuota() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWithXAIQuotaObserverLeavesNonXAIContextUnchanged(t *testing.T) {
	ctx := context.Background()
	got := withXAIQuotaObserver(ctx, &cliproxyauth.Auth{Provider: "codex"}, "gpt-5")
	if got != ctx {
		t.Fatal("non-xAI request received an upstream response observer")
	}
}
