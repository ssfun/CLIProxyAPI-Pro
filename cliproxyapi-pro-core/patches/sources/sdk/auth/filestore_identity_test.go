package auth

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestFileTokenStoreSaveReusesExistingCodexStrongIdentity(t *testing.T) {
	baseDir := t.TempDir()
	oldName := "codex-abc12345-user@example.com-free.json"
	oldPath := filepath.Join(baseDir, oldName)
	if err := os.WriteFile(oldPath, []byte(`{"type":"codex","account_id":"acct-1","email":"user@example.com","access_token":"old"}`), 0o600); err != nil {
		t.Fatalf("write old auth: %v", err)
	}

	store := NewFileTokenStore()
	store.SetBaseDir(baseDir)
	auth := &cliproxyauth.Auth{
		ID:       "codex-abc12345-user@example.com-plus.json",
		FileName: "codex-abc12345-user@example.com-plus.json",
		Provider: "codex",
		Metadata: map[string]any{
			"type":         "codex",
			"account_id":   "acct-1",
			"email":        "user@example.com",
			"access_token": "new",
		},
	}

	savedPath, err := store.Save(context.Background(), auth)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if savedPath != oldPath || auth.ID != oldName || auth.FileName != oldName {
		t.Fatalf("reused identity = path:%q id:%q file:%q, want %q", savedPath, auth.ID, auth.FileName, oldName)
	}
	if !TakeReusedExistingAuthIdentity(auth) || TakeReusedExistingAuthIdentity(auth) {
		t.Fatal("reused identity marker was not consumed exactly once")
	}
	if _, err := os.Stat(filepath.Join(baseDir, "codex-abc12345-user@example.com-plus.json")); !os.IsNotExist(err) {
		t.Fatalf("Save() created a duplicate credential: %v", err)
	}
	persisted, err := os.ReadFile(oldPath)
	if err != nil {
		t.Fatalf("read reused auth: %v", err)
	}
	if !jsonEqual(persisted, []byte(`{"type":"codex","account_id":"acct-1","email":"user@example.com","access_token":"new","disabled":false}`)) {
		t.Fatalf("reused auth content = %s", persisted)
	}
}

func TestFileTokenStoreSaveDoesNotMergeCodexIdentityWithoutGuardedEmail(t *testing.T) {
	baseDir := t.TempDir()
	oldPath := filepath.Join(baseDir, "codex-old.json")
	if err := os.WriteFile(oldPath, []byte(`{"type":"codex","account_id":"acct-1","email":"first@example.com"}`), 0o600); err != nil {
		t.Fatalf("write old auth: %v", err)
	}

	store := NewFileTokenStore()
	store.SetBaseDir(baseDir)
	auth := &cliproxyauth.Auth{
		ID: "codex-new.json", FileName: "codex-new.json", Provider: "codex",
		Metadata: map[string]any{"type": "codex", "account_id": "acct-1", "email": "second@example.com"},
	}
	savedPath, err := store.Save(context.Background(), auth)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if savedPath != filepath.Join(baseDir, "codex-new.json") || TakeReusedExistingAuthIdentity(auth) {
		t.Fatalf("conflicting email reused old identity: path=%q", savedPath)
	}
}

func TestFileTokenStoreSaveReusesProviderScopedStrongIdentities(t *testing.T) {
	tests := []struct {
		provider string
		oldJSON  string
		metadata map[string]any
	}{
		{provider: "claude", oldJSON: `{"type":"claude","account_uuid":"account-1","email":"user@example.com"}`, metadata: map[string]any{"type": "claude", "account_uuid": "account-1", "email": "user@example.com"}},
		{provider: "xai", oldJSON: `{"type":"xai","sub":"subject-1","email":"user@example.com"}`, metadata: map[string]any{"type": "xai", "sub": "subject-1", "email": "user@example.com"}},
		{provider: "antigravity", oldJSON: `{"type":"antigravity","email":"user@example.com","project_id":"old-project"}`, metadata: map[string]any{"type": "antigravity", "email": "user@example.com", "project_id": "new-project"}},
		{provider: "vertex", oldJSON: `{"type":"vertex","project_id":"project-1","email":"service@example.com"}`, metadata: map[string]any{"type": "vertex", "project_id": "project-1", "email": "service@example.com"}},
		{provider: "gemini-cli", oldJSON: `{"type":"gemini-cli","project_id":"project-1","email":"user@example.com"}`, metadata: map[string]any{"type": "gemini-cli", "project_id": "project-1", "email": "user@example.com"}},
	}

	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			baseDir := t.TempDir()
			oldPath := filepath.Join(baseDir, tt.provider+"-old.json")
			if err := os.WriteFile(oldPath, []byte(tt.oldJSON), 0o600); err != nil {
				t.Fatalf("write old auth: %v", err)
			}
			store := NewFileTokenStore()
			store.SetBaseDir(baseDir)
			auth := &cliproxyauth.Auth{
				ID: tt.provider + "-new.json", FileName: tt.provider + "-new.json",
				Provider: tt.provider, Metadata: tt.metadata,
			}
			savedPath, err := store.Save(context.Background(), auth)
			if err != nil {
				t.Fatalf("Save() error = %v", err)
			}
			if savedPath != oldPath || !TakeReusedExistingAuthIdentity(auth) {
				t.Fatalf("provider identity was not reused: path=%q", savedPath)
			}
		})
	}
}

func TestFileTokenStoreSaveDoesNotGuessUnsupportedProviderIdentity(t *testing.T) {
	baseDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(baseDir, "kimi-old.json"), []byte(`{"type":"kimi","device_id":"device-1"}`), 0o600); err != nil {
		t.Fatalf("write old auth: %v", err)
	}
	store := NewFileTokenStore()
	store.SetBaseDir(baseDir)
	auth := &cliproxyauth.Auth{
		ID: "kimi-new.json", FileName: "kimi-new.json", Provider: "kimi",
		Metadata: map[string]any{"type": "kimi", "device_id": "device-1"},
	}
	savedPath, err := store.Save(context.Background(), auth)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if savedPath != filepath.Join(baseDir, "kimi-new.json") || TakeReusedExistingAuthIdentity(auth) {
		t.Fatalf("unsupported provider identity was guessed: path=%q", savedPath)
	}
}

func TestFileTokenStoreSaveDoesNotReuseClaudeAcrossOrganizations(t *testing.T) {
	for _, organization := range []string{"organization-b", ""} {
		t.Run(organization, func(t *testing.T) {
			baseDir := t.TempDir()
			oldPath := filepath.Join(baseDir, "claude-old.json")
			oldJSON := []byte(`{"type":"claude","account_uuid":"account-1","email":"user@example.com","organization_uuid":"organization-a","access_token":"old"}`)
			if err := os.WriteFile(oldPath, oldJSON, 0o600); err != nil {
				t.Fatal(err)
			}
			store := NewFileTokenStore()
			store.SetBaseDir(baseDir)
			auth := &cliproxyauth.Auth{ID: "claude-custom.json", FileName: "claude-custom.json", Provider: "claude",
				Metadata: map[string]any{"type": "claude", "account_uuid": "account-1", "email": "user@example.com", "organization_uuid": organization}}
			path, err := store.Save(context.Background(), auth)
			if err != nil || path != filepath.Join(baseDir, auth.FileName) || path == oldPath || TakeReusedExistingAuthIdentity(auth) {
				t.Fatalf("Save() = %q, %v; crossed organization boundary", path, err)
			}
			got, err := os.ReadFile(oldPath)
			if err != nil || string(got) != string(oldJSON) {
				t.Fatalf("old credential changed: %s, %v", got, err)
			}
		})
	}
}

func TestFileTokenStoreSaveRejectsAmbiguousCodexStrongIdentity(t *testing.T) {
	baseDir := t.TempDir()
	for _, name := range []string{"codex-old-free.json", "codex-old-plus.json"} {
		if err := os.WriteFile(filepath.Join(baseDir, name), []byte(`{"type":"codex","account_id":"acct-1","email":"user@example.com"}`), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	store := NewFileTokenStore()
	store.SetBaseDir(baseDir)
	auth := &cliproxyauth.Auth{
		ID: "codex-new.json", FileName: "codex-new.json", Provider: "codex",
		Metadata: map[string]any{"type": "codex", "account_id": "acct-1", "email": "user@example.com"},
	}
	if _, err := store.Save(context.Background(), auth); err == nil {
		t.Fatal("Save() accepted an ambiguous strong identity")
	}
}
