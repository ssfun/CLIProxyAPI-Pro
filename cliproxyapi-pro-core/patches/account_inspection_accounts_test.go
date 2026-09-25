package management

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	proinspection "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/inspection"
	prorouting "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/routing"
)

func TestAccountFromAuthUsesFileNameWhenEmailUnavailable(t *testing.T) {
	auth := &coreauth.Auth{
		ID:       "codex-account",
		Provider: "codex",
		FileName: "codex-account.json",
		Metadata: map[string]any{"name": "Codex Account"},
	}

	account := accountFromAuth(auth)
	if account.DisplayName != auth.FileName {
		t.Fatalf("display name = %q, want file name %q", account.DisplayName, auth.FileName)
	}
	if account.Name != "Codex Account" {
		t.Fatalf("name = %q, want metadata name preserved", account.Name)
	}
}

func TestAccountFromAuthPrefersEmailOverFileName(t *testing.T) {
	auth := &coreauth.Auth{
		ID:       "codex-account",
		Provider: "codex",
		FileName: "codex-account.json",
		Metadata: map[string]any{"email": "owner@example.com"},
	}

	account := accountFromAuth(auth)
	if account.DisplayName != "owner@example.com" {
		t.Fatalf("display name = %q, want email", account.DisplayName)
	}
}

func TestAccountInspectionHealthClassificationUsesSemanticEvidenceAcrossProviders(t *testing.T) {
	providers := []string{"antigravity", "claude", "codex", "gemini-cli", "kimi", "xai"}
	for _, provider := range providers {
		provider := provider
		t.Run(provider+"/auth-invalid", func(t *testing.T) {
			result := testInspectionAuthInvalidResult(provider+"-auth", provider, accountInspectionActionDisable)
			if provider == "codex" {
				result.Action = accountInspectionActionDelete
			}
			if got := proinspection.HealthBucketOf(result); got != accountInspectionHealthAuthInvalid {
				t.Fatalf("health bucket = %q, want %q", got, accountInspectionHealthAuthInvalid)
			}
		})

		t.Run(provider+"/quota", func(t *testing.T) {
			result := testInspectionQuotaResult(provider+"-quota", provider, accountInspectionActionDisable)
			if provider == "codex" || provider == "xai" {
				result.StatusCode = testStatusCode(http.StatusPaymentRequired)
			}
			if got := proinspection.HealthBucketOf(result); got != accountInspectionHealthQuotaExhausted {
				t.Fatalf("health bucket = %q, want %q", got, accountInspectionHealthQuotaExhausted)
			}
		})
	}

	requestErrors := []struct {
		name            string
		provider        string
		errorCode       string
		status          *int
		deepProbeStatus string
	}{
		{name: "antigravity-deep-probe", provider: "antigravity", errorCode: "antigravity_deep_probe_error", status: testStatusCode(http.StatusBadRequest), deepProbeStatus: string(accountInspectionDeepProbeTransientError)},
		{name: "claude-probe", provider: "claude", errorCode: "inspection_probe_error", status: testStatusCode(http.StatusBadGateway)},
		{name: "codex-missing-auth-index", provider: "codex", errorCode: "missing_auth_index"},
		{name: "gemini-cli-probe", provider: "gemini-cli", errorCode: "inspection_probe_error", status: testStatusCode(http.StatusServiceUnavailable)},
		{name: "kimi-token-refresh", provider: "kimi", errorCode: "token_refresh_error"},
		{name: "xai-deep-probe", provider: "xai", errorCode: "xai_deep_probe_error", status: testStatusCode(http.StatusBadRequest), deepProbeStatus: string(accountInspectionDeepProbeTransientError)},
	}
	for _, tt := range requestErrors {
		t.Run(tt.name, func(t *testing.T) {
			result := testInspectionProviderResult(tt.name, tt.provider, accountInspectionActionDelete, false, tt.status, false, "probe failed")
			result.ErrorCode = tt.errorCode
			result.DeepProbeStatus = tt.deepProbeStatus
			if got := proinspection.HealthBucketOf(result); got != accountInspectionHealthInspectionError {
				t.Fatalf("health bucket = %q, want %q", got, accountInspectionHealthInspectionError)
			}
			if proinspection.IsAccountInvalidResult(result) {
				t.Fatal("request error was classified as account invalid")
			}
		})
	}
}

func TestAccountInspectionHealthClassificationDoesNotInferFactsFromActions(t *testing.T) {
	deleteOnly := testInspectionResult("delete-only", accountInspectionActionDelete, false, nil, false, "")
	if got := proinspection.HealthBucketOf(deleteOnly); got != accountInspectionHealthHealthy {
		t.Fatalf("delete-only health bucket = %q, want %q", got, accountInspectionHealthHealthy)
	}

	disableOnly := testInspectionResult("disable-only", accountInspectionActionDisable, false, nil, false, "")
	if got := proinspection.HealthBucketOf(disableOnly); got != accountInspectionHealthHealthy {
		t.Fatalf("disable-only health bucket = %q, want %q", got, accountInspectionHealthHealthy)
	}
}

func TestAutoErrorActionsUseSemanticErrorCategory(t *testing.T) {
	settings := proinspection.DefaultSettings()
	settings.AutoExecuteAccountInvalidAction = accountInspectionActionDelete
	settings.AutoExecuteRequestErrorAction = accountInspectionActionDisable

	authInvalid := testInspectionAuthInvalidResult("auth-invalid", "claude", accountInspectionActionKeep)
	if got := proinspection.AutoActionForResult(authInvalid, settings); got != accountInspectionActionDelete {
		t.Fatalf("401 auto action = %q, want %q", got, accountInspectionActionDelete)
	}

	requestError := testInspectionProviderResult("request-error", "xai", accountInspectionActionKeep, false, testStatusCode(http.StatusBadRequest), false, "temporary deep-probe failure")
	requestError.ErrorCode = "xai_deep_probe_error"
	requestError.DeepProbeStatus = string(accountInspectionDeepProbeTransientError)
	if got := proinspection.AutoActionForResult(requestError, settings); got != accountInspectionActionNone {
		t.Fatalf("400 auto action = %q, want none", got)
	}
}

func TestApplyAutomaticActionsSkipsNone(t *testing.T) {
	scheduler := &accountInspectionScheduler{}
	requestError := testInspectionProviderResult("codex-request-error", "codex", accountInspectionActionKeep, false, testStatusCode(http.StatusBadGateway), false, "temporary probe failure")
	requestError.ErrorCode = "inspection_probe_error"
	results := []accountInspectionResult{
		testInspectionResult("healthy", accountInspectionActionKeep, false, nil, false, ""),
		requestError,
	}

	scheduler.applyAutomaticActions(context.Background(), results, proinspection.DefaultSettings())

	for _, result := range results {
		if result.ExecuteError != "" || result.Executed {
			t.Fatalf("automatic none action mutated result = %#v", result)
		}
	}
	if scheduler.autoActionConfirmations != nil {
		t.Fatal("automatic none action created a confirmation counter")
	}
}

func TestSyncAuthInspectionLastErrorClearsMetadata(t *testing.T) {
	auth := &coreauth.Auth{
		LastError: &coreauth.Error{Code: "token_refresh_error", Message: "old refresh failed"},
		Metadata: map[string]any{
			"last_error": map[string]any{"code": "token_refresh_error", "message": "old refresh failed"},
			"email":      "user@example.com",
		},
	}

	syncAuthInspectionLastError(auth, nil)

	if auth.LastError != nil {
		t.Fatalf("LastError = %#v, want nil", auth.LastError)
	}
	if _, ok := auth.Metadata["last_error"]; ok {
		t.Fatalf("metadata last_error = %#v, want removed", auth.Metadata["last_error"])
	}
	if auth.Metadata["email"] != "user@example.com" {
		t.Fatalf("metadata email = %#v, want preserved", auth.Metadata["email"])
	}
}

func TestInspectionHTTPErrorDoesNotChangeNativeSchedulingState(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	registered, err := manager.Register(context.Background(), &coreauth.Auth{
		Provider: "codex",
		ID:       "codex-user",
		FileName: "codex-user.json",
		Metadata: map[string]any{"email": "user@example.com"},
	})
	if err != nil {
		t.Fatalf("Register auth error = %v", err)
	}

	scheduler := &accountInspectionScheduler{h: &Handler{authManager: manager}}
	scheduler.syncInspectionAuthStatus(context.Background(), accountFromAuth(registered), http.StatusUnauthorized)

	var got *coreauth.Auth
	for _, auth := range manager.List() {
		if auth.ID == registered.ID {
			got = auth
			break
		}
	}
	if got == nil {
		t.Fatal("updated auth not found")
	}
	if got.Status == coreauth.StatusError || got.Unavailable || got.StatusMessage != "" || got.LastError != nil || got.Metadata["last_error"] != nil {
		t.Fatalf("inspection HTTP error changed native auth state: %#v", got)
	}
}

func TestInspectionRecoveryDoesNotClearReauthenticatedAccount(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	registered, err := manager.Register(context.Background(), &coreauth.Auth{
		Provider: "codex",
		ID:       "reauthenticated-inspection-error",
		FileName: "reauthenticated-inspection-error.json",
		Metadata: map[string]any{"access_token": "old-token"},
	})
	if err != nil {
		t.Fatalf("Register auth error = %v", err)
	}
	observed := accountFromAuth(registered)
	current := registered.Clone()
	current.Metadata["access_token"] = "new-token"
	if _, err = manager.Update(context.Background(), current); err != nil {
		t.Fatalf("Update auth error = %v", err)
	}

	scheduler := &accountInspectionScheduler{h: &Handler{authManager: manager}}
	scheduler.syncInspectionAuthStatus(context.Background(), observed, http.StatusOK)

	got, _ := manager.GetByID(registered.ID)
	if got == nil {
		t.Fatal("reauthenticated auth not found")
	}
	if got.Status == coreauth.StatusError || got.Unavailable || got.StatusMessage != "" || got.LastError != nil {
		t.Fatalf("reauthenticated auth mutated by stale observation = %#v", got)
	}
}

func TestClearInspectionAuthErrorClearsMetadataOnlyError(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	registered, err := manager.Register(context.Background(), &coreauth.Auth{
		Provider:      "antigravity",
		ID:            "antigravity-user",
		FileName:      "antigravity-user.json",
		Status:        coreauth.StatusActive,
		StatusMessage: "",
		Unavailable:   false,
		Metadata: map[string]any{
			"email": "user@example.com",
			"last_error": map[string]any{
				"code":        "inspection_probe_error",
				"http_status": 0,
				"message":     "antigravity quota unavailable",
				"retryable":   false,
			},
		},
	})
	if err != nil {
		t.Fatalf("Register auth error = %v", err)
	}

	scheduler := &accountInspectionScheduler{h: &Handler{authManager: manager}}
	scheduler.clearInspectionAuthError(context.Background(), accountFromAuth(registered))

	var got *coreauth.Auth
	for _, auth := range manager.List() {
		if auth.ID == registered.ID {
			got = auth
			break
		}
	}
	if got == nil {
		t.Fatal("updated auth not found")
	}
	if got.LastError != nil {
		t.Fatalf("LastError = %#v, want nil", got.LastError)
	}
	if _, ok := got.Metadata["last_error"]; ok {
		t.Fatalf("metadata last_error = %#v, want removed", got.Metadata["last_error"])
	}
	if got.Status != coreauth.StatusActive || got.StatusMessage != "" || got.Unavailable {
		t.Fatalf("status = %q message=%q unavailable=%v, want active/empty/false", got.Status, got.StatusMessage, got.Unavailable)
	}
	if got.Metadata["email"] != "user@example.com" {
		t.Fatalf("metadata email = %#v, want preserved", got.Metadata["email"])
	}
}

func TestAutoActionConfirmationDelaysExecution(t *testing.T) {
	scheduler := &accountInspectionScheduler{}
	result := testInspectionResult("quota", accountInspectionActionDisable, false, nil, true, "")
	settings := proinspection.DefaultSettings()
	settings.AutoExecuteConfirmations = 2
	settings.AutoExecuteQuotaLimitDisable = true

	action := proinspection.AutoActionForResult(result, settings)
	if action != accountInspectionActionDisable {
		t.Fatalf("proinspection.AutoActionForResult() = %q, want disable", action)
	}
	confirmed, count, required := scheduler.confirmAutoAction(result, action, settings.AutoExecuteConfirmations)
	if confirmed || count != 1 || required != 2 {
		t.Fatalf("first confirmation = confirmed:%v count:%d required:%d, want false/1/2", confirmed, count, required)
	}
	scheduler.autoActionConfirmations.BeginRun()
	confirmed, count, required = scheduler.confirmAutoAction(result, action, settings.AutoExecuteConfirmations)
	if !confirmed || count != 2 || required != 2 {
		t.Fatalf("second confirmation = confirmed:%v count:%d required:%d, want true/2/2", confirmed, count, required)
	}
	scheduler.clearAutoActionConfirmation(result)
	scheduler.autoActionConfirmations.BeginRun()
	confirmed, count, required = scheduler.confirmAutoAction(result, action, settings.AutoExecuteConfirmations)
	if confirmed || count != 1 || required != 2 {
		t.Fatalf("confirmation after clear = confirmed:%v count:%d required:%d, want false/1/2", confirmed, count, required)
	}
}

func TestAutoActionConfirmationDoesNotCrossCredentialReplacement(t *testing.T) {
	scheduler := &accountInspectionScheduler{}
	action := accountInspectionActionDelete
	oldCredential := testInspectionAuthInvalidResult("account", "codex", action)
	oldCredential.CredentialObserved = "epoch-1:gen-1:token-a"
	oldCredential.CredentialFinal = "epoch-1:gen-1:token-a"

	confirmed, count, required := scheduler.confirmAutoAction(oldCredential, action, 2)
	if confirmed || count != 1 || required != 2 {
		t.Fatalf("old credential confirmation = confirmed:%v count:%d required:%d", confirmed, count, required)
	}
	scheduler.autoActionConfirmations.BeginRun()

	replacement := oldCredential
	replacement.CredentialObserved = "epoch-2:gen-1:token-b"
	replacement.CredentialFinal = "epoch-2:gen-1:token-b"
	confirmed, count, required = scheduler.confirmAutoAction(replacement, action, 2)
	if confirmed || count != 1 || required != 2 {
		t.Fatalf("replacement confirmation = confirmed:%v count:%d required:%d, want reset", confirmed, count, required)
	}
}

func TestAccountInspectionCredentialFingerprintTracksCredentialIdentityOnly(t *testing.T) {
	base := &coreauth.Auth{
		RegistrationEpoch: 4,
		Generation:        1,
		Status:            coreauth.StatusActive,
		Metadata: map[string]any{
			"access_token":  "access-a",
			"refresh_token": "refresh-a",
		},
	}
	baseFingerprint := accountInspectionCredentialFingerprint(base)
	if baseFingerprint == "" {
		t.Fatal("base credential fingerprint is empty")
	}

	statusOnly := base.Clone()
	statusOnly.Generation = 2
	statusOnly.Status = coreauth.StatusError
	statusOnly.LastError = &coreauth.Error{Code: "inspection_probe_error", Message: "temporary"}
	if got := accountInspectionCredentialFingerprint(statusOnly); got != baseFingerprint {
		t.Fatalf("status-only fingerprint = %q, want %q", got, baseFingerprint)
	}

	refreshed := base.Clone()
	refreshed.Generation = 2
	refreshed.Metadata["access_token"] = "access-b"
	refreshed.Metadata["refresh_token"] = "refresh-b"
	if got := accountInspectionCredentialFingerprint(refreshed); got != baseFingerprint {
		t.Fatalf("normal refresh fingerprint = %q, want %q", got, baseFingerprint)
	}

	replacement := base.Clone()
	replacement.RegistrationEpoch = 5
	if got := accountInspectionCredentialFingerprint(replacement); got == baseFingerprint {
		t.Fatal("replacement credential fingerprint did not change")
	}

	legacy := base.Clone()
	legacy.RegistrationEpoch = 0
	legacyFingerprint := accountInspectionCredentialFingerprint(legacy)
	legacy.Metadata["refresh_token"] = "refresh-c"
	if got := accountInspectionCredentialFingerprint(legacy); got == legacyFingerprint {
		t.Fatal("legacy credential-material fingerprint did not change")
	}
}

func TestExecuteActionDisablesGeminiCLIPluginVirtualSourceFile(t *testing.T) {
	authDir := t.TempDir()
	authPath := filepath.Join(authDir, "gemini-cli.json")
	if err := os.WriteFile(authPath, []byte(`{"type":"gemini-cli","email":"user@example.com","project_id":"project-a","project_ids":["project-a","project-b"]}`), 0o600); err != nil {
		t.Fatalf("WriteFile auth error = %v", err)
	}

	manager := coreauth.NewManager(nil, nil, nil)
	primary := &coreauth.Auth{
		Provider:   "gemini-cli",
		ID:         "gemini-cli-primary",
		FileName:   "gemini-cli.json",
		Metadata:   map[string]any{"type": "gemini-cli", "email": "user@example.com", "project_id": "project-a"},
		Attributes: map[string]string{"path": authPath, "project_id": "project-a"},
		Storage:    &accountInspectionTestStorage{},
	}
	coreauth.MarkPluginVirtualAuth(primary, authPath, 0)
	secondary := &coreauth.Auth{
		Provider:   "gemini-cli",
		ID:         "gemini-cli-project-b",
		FileName:   "user-project-b.json",
		Metadata:   map[string]any{"type": "gemini-cli", "email": "user@example.com", "project_id": "project-b", "virtual": true},
		Attributes: map[string]string{"path": authPath, "project_id": "project-b", "runtime_only": "true"},
		Storage:    &accountInspectionTestStorage{},
	}
	coreauth.MarkPluginVirtualAuth(secondary, authPath, 1)
	registeredSecondary, err := manager.Register(context.Background(), secondary)
	if err != nil {
		t.Fatalf("Register secondary error = %v", err)
	}
	if _, err = manager.Register(context.Background(), primary); err != nil {
		t.Fatalf("Register primary error = %v", err)
	}

	scheduler := &accountInspectionScheduler{h: &Handler{authManager: manager}}
	err = scheduler.executeAction(context.Background(), accountInspectionResult{AuthIndex: registeredSecondary.Index}, accountInspectionActionDisable)
	if err != nil {
		t.Fatalf("executeAction(disable) error = %v", err)
	}

	raw, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatalf("ReadFile auth error = %v", err)
	}
	var saved map[string]any
	if err = json.Unmarshal(raw, &saved); err != nil {
		t.Fatalf("saved auth invalid JSON: %v", err)
	}
	if saved["disabled"] != true {
		t.Fatalf("saved disabled = %#v, want true in source auth file", saved["disabled"])
	}
	if saved["project_id"] != "project-a" {
		t.Fatalf("saved project_id = %#v, want primary project preserved", saved["project_id"])
	}
	projectIDs, ok := saved["project_ids"].([]any)
	if !ok || len(projectIDs) != 2 || projectIDs[0] != "project-a" || projectIDs[1] != "project-b" {
		t.Fatalf("saved project_ids = %#v, want original source project_ids preserved", saved["project_ids"])
	}
}

func TestWritePluginVirtualManagedMetadataDoesNotRecreateMissingSource(t *testing.T) {
	authPath := filepath.Join(t.TempDir(), "removed.json")
	auth := &coreauth.Auth{Disabled: true, Metadata: map[string]any{}}
	err := writePluginVirtualManagedMetadataToSourceFile(authPath, auth, map[string]any{"type": "gemini-cli"})
	if !os.IsNotExist(err) {
		t.Fatalf("writePluginVirtualManagedMetadataToSourceFile() error = %v, want not-exist", err)
	}
	if _, statErr := os.Stat(authPath); !os.IsNotExist(statErr) {
		t.Fatalf("removed source file was recreated: %v", statErr)
	}
}

func TestUpdatePluginVirtualRuntimeAuthsRejectsMissingIdentity(t *testing.T) {
	handler := &Handler{authManager: coreauth.NewManager(nil, nil, nil)}
	auth := &coreauth.Auth{ID: "removed", Provider: "gemini-cli", Metadata: map[string]any{}}
	err := handler.updatePluginVirtualRuntimeAuths(context.Background(), auth, func(updated *coreauth.Auth) {
		updated.Disabled = true
	})
	if err == nil || !strings.Contains(err.Error(), "no longer exists") {
		t.Fatalf("updatePluginVirtualRuntimeAuths() error = %v, want missing-identity error", err)
	}
}

func TestManualActionsBindIdentityToCurrentSnapshot(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	registeredA, err := manager.Register(context.Background(), &coreauth.Auth{Provider: "codex", ID: "auth-a", FileName: "a.json", Metadata: map[string]any{"email": "a@example.com"}})
	if err != nil {
		t.Fatalf("Register(auth-a) error = %v", err)
	}
	registeredB, err := manager.Register(context.Background(), &coreauth.Auth{Provider: "codex", ID: "auth-b", FileName: "b.json", Metadata: map[string]any{"email": "b@example.com"}})
	if err != nil {
		t.Fatalf("Register(auth-b) error = %v", err)
	}
	resultA := accountFromAuth(registeredA).baseResult()
	scheduler := &accountInspectionScheduler{
		h:      &Handler{authManager: manager},
		status: accountInspectionStatus{Results: []accountInspectionResult{resultA}},
	}
	outcomes, err := scheduler.executeManualActions(context.Background(), []accountInspectionActionItem{{
		Key:       resultA.Key,
		FileName:  registeredB.FileName,
		AuthIndex: registeredB.Index,
		Provider:  registeredB.Provider,
		Action:    accountInspectionActionDisable,
	}})
	if err != nil {
		t.Fatalf("executeManualActions() error = %v", err)
	}
	if len(outcomes) != 1 || !outcomes[0].Success || outcomes[0].AuthIndex != registeredA.Index || outcomes[0].FileName != registeredA.FileName {
		t.Fatalf("outcomes = %+v, want canonical auth-a identity", outcomes)
	}
	gotA, _ := manager.GetByID(registeredA.ID)
	gotB, _ := manager.GetByID(registeredB.ID)
	if gotA == nil || !gotA.Disabled {
		t.Fatalf("auth-a = %#v, want disabled", gotA)
	}
	if gotB == nil || gotB.Disabled {
		t.Fatalf("auth-b = %#v, want enabled", gotB)
	}
}

func TestExecuteActionRejectsStaleRuntimeIdentity(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	registeredA, _ := manager.Register(context.Background(), &coreauth.Auth{Provider: "codex", ID: "auth-a", FileName: "a.json", Metadata: map[string]any{}})
	registeredB, _ := manager.Register(context.Background(), &coreauth.Auth{Provider: "codex", ID: "auth-b", FileName: "b.json", Metadata: map[string]any{}})
	result := accountFromAuth(registeredA).baseResult()
	result.AuthIndex = registeredB.Index
	scheduler := &accountInspectionScheduler{h: &Handler{authManager: manager}}
	if err := scheduler.executeAction(context.Background(), result, accountInspectionActionDisable); !errors.Is(err, errAccountInspectionResultStale) {
		t.Fatalf("executeAction() error = %v, want stale result", err)
	}
	gotA, _ := manager.GetByID(registeredA.ID)
	gotB, _ := manager.GetByID(registeredB.ID)
	if gotA.Disabled || gotB.Disabled {
		t.Fatalf("auths mutated after stale action: a=%v b=%v", gotA.Disabled, gotB.Disabled)
	}
}

func TestExecuteActionRejectsReauthenticatedAccount(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	registered, err := manager.Register(context.Background(), &coreauth.Auth{
		Provider: "codex",
		ID:       "reauthenticated-inspection-action",
		FileName: "reauthenticated-inspection-action.json",
		Metadata: map[string]any{"access_token": "old-token"},
	})
	if err != nil {
		t.Fatalf("Register auth error = %v", err)
	}
	result := accountFromAuth(registered).baseResult()
	current := registered.Clone()
	current.Metadata["access_token"] = "new-token"
	if _, err = manager.Update(context.Background(), current); err != nil {
		t.Fatalf("Update auth error = %v", err)
	}

	scheduler := &accountInspectionScheduler{h: &Handler{authManager: manager}}
	if err = scheduler.executeAction(context.Background(), result, accountInspectionActionDisable); !errors.Is(err, errAccountInspectionResultStale) {
		t.Fatalf("executeAction() error = %v, want stale result", err)
	}
	got, _ := manager.GetByID(registered.ID)
	if got == nil || got.Disabled {
		t.Fatalf("reauthenticated auth mutated by stale action = %#v", got)
	}
}

func TestExecuteActionRejectsSharedPluginVirtualSourceDelete(t *testing.T) {
	authPath := filepath.Join(t.TempDir(), "gemini-cli.json")
	if err := os.WriteFile(authPath, []byte(`{"type":"gemini-cli","project_ids":["project-a","project-b"]}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	manager := coreauth.NewManager(nil, nil, nil)
	primary := &coreauth.Auth{Provider: "gemini-cli", ID: "primary", FileName: "gemini-cli.json", Metadata: map[string]any{}, Attributes: map[string]string{"path": authPath}}
	secondary := &coreauth.Auth{Provider: "gemini-cli", ID: "secondary", FileName: "project-b.json", Metadata: map[string]any{}, Attributes: map[string]string{"path": authPath, "runtime_only": "true"}}
	coreauth.MarkPluginVirtualAuth(primary, authPath, 0)
	coreauth.MarkPluginVirtualAuth(secondary, authPath, 1)
	registeredPrimary, _ := manager.Register(context.Background(), primary)
	_, _ = manager.Register(context.Background(), secondary)
	scheduler := &accountInspectionScheduler{h: &Handler{authManager: manager}}
	if err := scheduler.executeAction(context.Background(), accountFromAuth(registeredPrimary).baseResult(), accountInspectionActionDelete); !errors.Is(err, errAccountInspectionSharedSourceDelete) {
		t.Fatalf("executeAction(delete) error = %v, want shared-source rejection", err)
	}
	if _, err := os.Stat(authPath); err != nil {
		t.Fatalf("shared source file was removed: %v", err)
	}
}

func TestPluginVirtualInspectionErrorDoesNotChangeNativeState(t *testing.T) {
	authPath := filepath.Join(t.TempDir(), "gemini-cli.json")
	if err := os.WriteFile(authPath, []byte(`{"type":"gemini-cli","project_ids":["project-a","project-b"]}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	manager := coreauth.NewManager(nil, nil, nil)
	primary := &coreauth.Auth{Provider: "gemini-cli", ID: "primary", FileName: "gemini-cli.json", Metadata: map[string]any{"project_id": "project-a"}, Attributes: map[string]string{"path": authPath}}
	secondary := &coreauth.Auth{Provider: "gemini-cli", ID: "secondary", FileName: "project-b.json", Metadata: map[string]any{"project_id": "project-b"}, Attributes: map[string]string{"path": authPath, "runtime_only": "true"}}
	coreauth.MarkPluginVirtualAuth(primary, authPath, 0)
	coreauth.MarkPluginVirtualAuth(secondary, authPath, 1)
	registeredPrimary, _ := manager.Register(context.Background(), primary)
	registeredSecondary, _ := manager.Register(context.Background(), secondary)
	scheduler := &accountInspectionScheduler{h: &Handler{authManager: manager}}
	scheduler.syncInspectionAuthStatus(context.Background(), accountFromAuth(registeredSecondary), http.StatusUnauthorized)

	gotPrimary, _ := manager.GetByID(registeredPrimary.ID)
	gotSecondary, _ := manager.GetByID(registeredSecondary.ID)
	if gotPrimary.LastError != nil || gotPrimary.Status == coreauth.StatusError {
		t.Fatalf("primary auth was polluted: %#v", gotPrimary)
	}
	if gotSecondary.LastError != nil || gotSecondary.Status == coreauth.StatusError || gotSecondary.Unavailable {
		t.Fatalf("secondary auth changed by inspection error: %#v", gotSecondary)
	}
	raw, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.Contains(string(raw), "last_error") {
		t.Fatalf("shared source contains identity-local error: %s", raw)
	}
}

func TestInspectionEnableCannotReleaseNewQuotaProtection(t *testing.T) {
	ctx := startProQuotaTestService(t)
	manager := coreauth.NewManager(nil, nil, nil)
	registered, err := manager.Register(ctx, &coreauth.Auth{
		Provider: "codex", ID: "enable-hold", FileName: "enable-hold.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	staleResult := accountFromAuth(registered).baseResult()
	hold := prorouting.QuotaProtection{Recheck: true}
	if err := manager.ChangeQuotaProtection(ctx, registered, inspectionQuotaSource, 0, &hold); err != nil {
		t.Fatal(err)
	}
	current, _ := manager.GetByID(registered.ID)
	before := prorouting.QuotaProtections(current.Metadata)[inspectionQuotaSource]
	scheduler := &accountInspectionScheduler{h: &Handler{authManager: manager}}
	if err := scheduler.executeAction(ctx, staleResult, accountInspectionActionEnable); err == nil || !strings.Contains(err.Error(), "versioned routing release") {
		t.Fatalf("enable error = %v, want explicit release instruction", err)
	}
	current, _ = manager.GetByID(registered.ID)
	got, ok := prorouting.QuotaProtections(current.Metadata)[inspectionQuotaSource]
	if !ok || got.Revision != before.Revision || current.Disabled {
		t.Fatalf("new quota protection changed by stale enable: hold=%#v disabled=%v", got, current.Disabled)
	}
}

func TestInspectionEnableDisabledAccountPreservesQuotaProtection(t *testing.T) {
	ctx := startProQuotaTestService(t)
	manager := coreauth.NewManager(nil, nil, nil)
	registered, err := manager.Register(ctx, &coreauth.Auth{
		Provider: "codex", ID: "disabled-with-hold", FileName: "disabled-with-hold.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	hold := prorouting.QuotaProtection{Recheck: true}
	if err := manager.ChangeQuotaProtection(ctx, registered, inspectionQuotaSource, 0, &hold); err != nil {
		t.Fatal(err)
	}
	current, _ := manager.GetByID(registered.ID)
	current.Disabled = true
	current.Status = coreauth.StatusDisabled
	if _, err := manager.Update(ctx, current); err != nil {
		t.Fatal(err)
	}
	current, _ = manager.GetByID(registered.ID)
	before := prorouting.QuotaProtections(current.Metadata)[inspectionQuotaSource]
	scheduler := &accountInspectionScheduler{h: &Handler{authManager: manager}}
	if err := scheduler.executeAction(ctx, accountFromAuth(current).baseResult(), accountInspectionActionEnable); err != nil {
		t.Fatalf("enable disabled account: %v", err)
	}
	current, _ = manager.GetByID(registered.ID)
	got, ok := prorouting.QuotaProtections(current.Metadata)[inspectionQuotaSource]
	if current.Disabled || !ok || got.Revision != before.Revision {
		t.Fatalf("enable changed quota protection: hold=%#v disabled=%v", got, current.Disabled)
	}
}

func TestEnablePreservesNonInspectionLastError(t *testing.T) {
	auth := &coreauth.Auth{
		Disabled:    true,
		Status:      coreauth.StatusDisabled,
		LastError:   &coreauth.Error{Code: "upstream_refresh_error", Message: "refresh failed"},
		Metadata:    map[string]any{"last_error": map[string]any{"code": "upstream_refresh_error", "message": "refresh failed"}},
		Unavailable: true,
	}
	setProAuthDisabledState(auth, false)
	if auth.Disabled || auth.Status != coreauth.StatusError || !auth.Unavailable {
		t.Fatalf("enabled auth state = disabled:%v status:%q unavailable:%v", auth.Disabled, auth.Status, auth.Unavailable)
	}
	if auth.LastError == nil || auth.LastError.Code != "upstream_refresh_error" {
		t.Fatalf("LastError = %#v, want preserved upstream error", auth.LastError)
	}
	if _, ok := auth.Metadata["last_error"]; !ok {
		t.Fatal("metadata last_error was removed")
	}
}

func TestEnablePreservesNativeUnavailableWithoutLastError(t *testing.T) {
	auth := &coreauth.Auth{
		Disabled:      true,
		Status:        coreauth.StatusDisabled,
		StatusMessage: "disabled by scheduled account inspection",
		Unavailable:   true,
		Metadata: map[string]any{
			prorouting.ProtectionMetadataKey: map[string]any{"owner": prorouting.ProtectionOwner},
		},
	}
	setProAuthDisabledState(auth, false)
	if auth.Disabled || auth.Status != coreauth.StatusError || !auth.Unavailable {
		t.Fatalf("enabled auth cleared native restriction: %#v", auth)
	}
	if auth.StatusMessage != "account remains unavailable" {
		t.Fatalf("status message = %q, want unresolved availability", auth.StatusMessage)
	}
	if !prorouting.ProtectionOwned(auth.Metadata) {
		t.Fatal("enabling account removed native restriction ownership")
	}
}
