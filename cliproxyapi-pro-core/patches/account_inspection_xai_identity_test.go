package management

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/embeddedusage"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

// Written before implementation: a real same-name auth-file upload must bind
// SQLite reads and merged writes to its subject, never its filename/index.
// Token refresh and cosmetic labels must retain that subject's observations;
// legacy/unbound data must be replaced by fresh evidence without promotion.
func TestXAIUploadQuotaIdentityHTTPAndSQLite(t *testing.T) {
	for _, tc := range []struct {
		name, oldSubject, newSubject, oldEmail, newEmail string
		reuse, legacy, tokenSubject                      bool
	}{
		{name: "different subject and email", oldSubject: "old", newSubject: "new", oldEmail: "old@example.test", newEmail: "new@example.test"},
		{name: "different subject same email", oldSubject: "old", newSubject: "new", oldEmail: "same@example.test", newEmail: "same@example.test"},
		{name: "same subject changed email and name", oldSubject: "same", newSubject: "same", oldEmail: "old@example.test", newEmail: "new@example.test", reuse: true},
		{name: "jwt subject token refresh", oldSubject: "same", newSubject: "same", reuse: true, tokenSubject: true},
		{name: "email fallback", oldEmail: "SAME@example.test", newEmail: "same@example.test", reuse: true},
		{name: "missing identity"},
		{name: "legacy fingerprint", oldSubject: "same", newSubject: "same", legacy: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := startProQuotaTestService(t)
			manager := coreauth.NewManager(nil, nil, nil)
			h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)
			upload := func(subject, email, name, tokenSuffix string) accountInspectionAccount {
				t.Helper()
				metadata := map[string]any{"type": "xai", "using_api": false, "access_token": "token-" + tokenSuffix, "name": name}
				if email != "" {
					metadata["email"] = email
				}
				if subject != "" {
					if tc.tokenSubject {
						claims, _ := json.Marshal(map[string]any{"sub": subject, "tier": 0, "jti": tokenSuffix})
						metadata["access_token"] = "header." + base64.RawURLEncoding.EncodeToString(claims) + ".signature"
					} else {
						metadata["sub"] = subject
					}
				}
				body, _ := json.Marshal(metadata)
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v0/management/auth-files?name=same.json", strings.NewReader(string(body))).WithContext(ctx)
				c.Request.Header.Set("Content-Type", "application/json")
				h.UploadAuthFile(c)
				if rec.Code != http.StatusOK {
					t.Fatalf("upload status=%d body=%s", rec.Code, rec.Body.String())
				}
				return accountFromAuth(manager.List()[0])
			}
			old := upload(tc.oldSubject, tc.oldEmail, "Old display", "old")
			if err := persistQuotaState(ctx, old, quotaSuccessState(map[string]any{"billing": map[string]any{
				"planType": "free", "planLabel": "Old Plan", "freeQuota": map[string]any{"exhausted": true, "observedAt": 100},
			}})); err != nil {
				t.Fatal(err)
			}
			oldEntries, err := embeddedusage.GetQuotaCache(ctx, "xai", "same.json")
			if err != nil || len(oldEntries) != 1 {
				t.Fatalf("old entries=%v err=%v", oldEntries, err)
			}
			if tc.legacy {
				entry := oldEntries[0]
				entry.IdentityFingerprint = "legacy-filename-email-label"
				if err := embeddedusage.SetQuotaCache(ctx, entry); err != nil {
					t.Fatal(err)
				}
			}
			account := upload(tc.newSubject, tc.newEmail, "New display", "new")
			billing := mergeCachedXAIFreeQuota(ctx, account, map[string]any{"planType": "free", "usagePercent": 0})
			if (billing["freeQuota"] != nil) != tc.reuse {
				t.Fatalf("upload inherited wrong quota: billing=%v reuse=%v", billing, tc.reuse)
			}
			if err := persistQuotaState(ctx, account, quotaSuccessState(map[string]any{"billing": billing})); err != nil {
				t.Fatal(err)
			}
			entries, err := embeddedusage.GetQuotaCache(ctx, "xai", "same.json")
			if err != nil || len(entries) != 1 {
				t.Fatalf("entries=%v err=%v", entries, err)
			}
			var state map[string]any
			if err := json.Unmarshal(entries[0].Data, &state); err != nil {
				t.Fatal(err)
			}
			persisted := firstMap(state, "billing")
			if (persisted["freeQuota"] != nil) != tc.reuse || (persisted["planLabel"] != nil) != tc.reuse {
				t.Fatalf("wrong persisted identity data: %s", entries[0].Data)
			}
			if tc.reuse && oldEntries[0].IdentityFingerprint != entries[0].IdentityFingerprint {
				t.Fatal("stable identity changed during token/label refresh")
			}
			beforeObservation := entries[0].IdentityFingerprint
			_ = observeAccountXAIQuota(ctx, account, "grok-free", accountInspectionHTTPResult{
				StatusCode: http.StatusOK, Header: http.Header{"X-Ratelimit-Limit-Tokens": {"100"}, "X-Ratelimit-Remaining-Tokens": {"40"}},
			})
			observedEntries, err := embeddedusage.GetQuotaCache(ctx, "xai", "same.json")
			if err != nil || len(observedEntries) != 1 {
				t.Fatalf("observed entries=%v err=%v", observedEntries, err)
			}
			if observedEntries[0].IdentityFingerprint != beforeObservation {
				t.Fatal("observation and inspection used different identities")
			}
			t.Logf("uploaded=%s identity=%s persisted=%s observation=%s at=%s", account.FileName, beforeObservation, entries[0].Data, observedEntries[0].Data, time.Now().UTC().Format(time.RFC3339))
		})
	}
}

// Failure contract before the HTTP binding adapter: manual quota writes must
// carry their original observation identity, reject missing/stale identities,
// and merge with request observations only for the currently registered user.
func TestXAIQuotaCacheBindingHTTPAndSQLite(t *testing.T) {
	for _, tc := range []struct {
		name                                string
		missing, replaced, unknown, hydrate bool
	}{
		{name: "manual observer inspection same identity"},
		{name: "missing binding", missing: true},
		{name: "stale upload completion", replaced: true},
		{name: "unknown auth", unknown: true},
		{name: "hydrate after replacement", replaced: true, hydrate: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := startProQuotaTestService(t)
			manager := coreauth.NewManager(nil, nil, nil)
			h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)
			upload := func(subject string) accountInspectionAccount {
				t.Helper()
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v0/management/auth-files?name=same.json", strings.NewReader(`{"type":"xai","sub":"`+subject+`","email":"same@example.test","using_api":false,"access_token":"opaque"}`)).WithContext(ctx)
				c.Request.Header.Set("Content-Type", "application/json")
				h.UploadAuthFile(c)
				if rec.Code != http.StatusOK {
					t.Fatalf("upload=%d %s", rec.Code, rec.Body.String())
				}
				return accountFromAuth(manager.List()[0])
			}
			old := upload("old-subject")
			if err := persistQuotaState(ctx, old, quotaSuccessState(map[string]any{"billing": map[string]any{"planLabel": "Old Plan", "freeQuota": map[string]any{"exhausted": true, "observedAt": 100}}})); err != nil {
				t.Fatal(err)
			}
			oldEntries, _ := embeddedusage.GetQuotaCache(ctx, "xai", "same.json")
			account := old
			if tc.replaced {
				account = upload("new-subject")
			}
			router := gin.New()
			embeddedusage.RegisterGinRoutes(router.Group("/v0/management/usage", h.BindQuotaCacheIdentity))
			if tc.hydrate {
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v0/management/usage/quota-cache?provider=xai", nil).WithContext(ctx))
				var response struct {
					Items []embeddedusage.QuotaCacheEntry `json:"items"`
				}
				_ = json.Unmarshal(rec.Body.Bytes(), &response)
				if rec.Code != http.StatusOK || len(response.Items) != 0 {
					t.Fatalf("stale identity hydrated: status=%d body=%s", rec.Code, rec.Body.String())
				}
				return
			}
			fingerprint, fileName := oldEntries[0].IdentityFingerprint, "same.json"
			if tc.missing {
				fingerprint = ""
			}
			if tc.unknown {
				fileName = "unknown.json"
			}
			body, _ := json.Marshal(map[string]any{"provider": "xai", "fileName": fileName, "identityFingerprint": fingerprint, "data": quotaSuccessState(map[string]any{"billing": map[string]any{"planType": "free", "usagePercent": 0}})})
			rec := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPut, "/v0/management/usage/quota-cache", strings.NewReader(string(body))).WithContext(ctx)
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(rec, request)
			if tc.missing || tc.replaced || tc.unknown {
				if rec.Code != http.StatusConflict {
					t.Fatalf("unbound/stale write accepted: status=%d body=%s", rec.Code, rec.Body.String())
				}
				entries, _ := embeddedusage.GetQuotaCache(ctx, "xai", "same.json")
				if len(entries) != 1 || string(entries[0].Data) != string(oldEntries[0].Data) {
					t.Fatalf("rejected write modified cache: %v", entries)
				}
				return
			}
			if rec.Code != http.StatusOK {
				t.Fatalf("manual write=%d %s", rec.Code, rec.Body.String())
			}
			_ = observeAccountXAIQuota(ctx, account, "grok-free", accountInspectionHTTPResult{StatusCode: http.StatusOK, Header: http.Header{"X-Ratelimit-Limit-Tokens": {"100"}, "X-Ratelimit-Remaining-Tokens": {"40"}}})
			billing := mergeCachedXAIFreeQuota(ctx, account, map[string]any{"planType": "free"})
			if err := persistQuotaState(ctx, account, quotaSuccessState(map[string]any{"billing": billing})); err != nil {
				t.Fatal(err)
			}
			entries, _ := embeddedusage.GetQuotaCache(ctx, "xai", "same.json")
			var state map[string]any
			_ = json.Unmarshal(entries[0].Data, &state)
			persisted := firstMap(state, "billing")
			free := firstMap(persisted, "freeQuota")
			if persisted["planLabel"] != "Old Plan" || free["remainingTokens"] != float64(40) || entries[0].IdentityFingerprint != fingerprint {
				t.Fatalf("same identity lost data: %s", entries[0].Data)
			}
			t.Logf("manual→observer→inspection fingerprint=%s state=%s", fingerprint, entries[0].Data)
		})
	}
}

// Official API config auths legitimately have only Attributes.api_key. Their
// exact credential must support a bound manual cache, while rotation creates a
// new identity and wholly missing credentials remain unbound.
func TestXAIAPIKeyQuotaCacheIdentityHTTPAndSQLite(t *testing.T) {
	for _, tc := range []struct {
		name                                        string
		attributeKey, opaqueToken, rotated, missing bool
	}{
		{name: "official API key", attributeKey: true},
		{name: "official API key rotated", attributeKey: true, rotated: true},
		{name: "opaque OAuth token", opaqueToken: true},
		{name: "opaque OAuth token rotated", opaqueToken: true, rotated: true},
		{name: "no credential", missing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := startProQuotaTestService(t)
			manager := coreauth.NewManager(nil, nil, nil)
			auth := &coreauth.Auth{ID: "key-auth", Provider: "xai", Label: "xai-apikey", Attributes: map[string]string{"using_api": "true"}}
			if tc.attributeKey {
				auth.Attributes["api_key"] = "exact-key"
			}
			if tc.opaqueToken {
				auth.Metadata = map[string]any{"access_token": "exact-token"}
				auth.Attributes["using_api"] = "false"
			}
			registered, err := manager.Register(ctx, auth)
			if err != nil {
				t.Fatal(err)
			}
			account := accountFromAuth(registered)
			fingerprint := xaiAccountQuotaIdentityFingerprint(account)
			if (fingerprint == "") != tc.missing {
				t.Fatalf("credential identity missing=%v fingerprint=%q", tc.missing, fingerprint)
			}
			if !tc.missing {
				if err := persistQuotaState(ctx, account, quotaSuccessState(map[string]any{"billing": map[string]any{"planLabel": "API Health"}})); err != nil {
					t.Fatal(err)
				}
			}
			if tc.rotated {
				next := registered.Clone()
				if tc.attributeKey {
					next.Attributes["api_key"] = "different-key"
				} else {
					next.Metadata["access_token"] = "different-token"
				}
				if _, err := manager.Register(ctx, next); err != nil {
					t.Fatal(err)
				}
			}
			h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)
			router := gin.New()
			embeddedusage.RegisterGinRoutes(router.Group("/v0/management/usage", h.BindQuotaCacheIdentity))
			body, _ := json.Marshal(map[string]any{"provider": "xai", "fileName": account.FileName, "identityFingerprint": fingerprint, "data": quotaSuccessState(map[string]any{"billing": map[string]any{"planType": "paid"}})})
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/v0/management/usage/quota-cache", strings.NewReader(string(body))).WithContext(ctx))
			want := http.StatusOK
			if tc.rotated || tc.missing {
				want = http.StatusConflict
			}
			if rec.Code != want {
				t.Fatalf("PUT status=%d want=%d body=%s", rec.Code, want, rec.Body.String())
			}
			entries, err := embeddedusage.GetQuotaCache(ctx, "xai", account.FileName)
			if err != nil {
				t.Fatal(err)
			}
			if tc.missing {
				if len(entries) != 0 {
					t.Fatalf("unbound credential wrote cache: %+v", entries)
				}
				return
			}
			if len(entries) != 1 || entries[0].IdentityFingerprint != fingerprint {
				t.Fatalf("credential binding lost: %+v", entries)
			}
			t.Logf("shape=%s fingerprint=%s PUT=%d data=%s", tc.name, fingerprint, rec.Code, entries[0].Data)
		})
	}
}
