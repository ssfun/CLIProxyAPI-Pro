package management

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	coreusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
)

type authFileConnectionExecutor struct {
	lastAuthID  string
	lastModel   string
	response    []byte
	err         error
	skipMonitor bool
}

func (e *authFileConnectionExecutor) Identifier() string { return "codex" }

func (e *authFileConnectionExecutor) Execute(ctx context.Context, auth *coreauth.Auth, req coreexecutor.Request, _ coreexecutor.Options) (coreexecutor.Response, error) {
	if auth != nil {
		e.lastAuthID = auth.ID
	}
	e.lastModel = req.Model
	e.skipMonitor = coreusage.SkipMonitoringFromContext(ctx)
	return coreexecutor.Response{Payload: e.response}, e.err
}

func (e *authFileConnectionExecutor) ExecuteStream(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (*coreexecutor.StreamResult, error) {
	return nil, nil
}

func (e *authFileConnectionExecutor) Refresh(_ context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	return auth, nil
}

func (e *authFileConnectionExecutor) CountTokens(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (coreexecutor.Response, error) {
	return coreexecutor.Response{}, nil
}

func (e *authFileConnectionExecutor) HttpRequest(context.Context, *coreauth.Auth, *http.Request) (*http.Response, error) {
	return nil, nil
}

func TestAuthFileConnectionUsesExactAuthAndReturnsOutput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := coreauth.NewManager(nil, nil, nil)
	executor := &authFileConnectionExecutor{response: []byte(`{"choices":[{"message":{"content":"OK"}}]}`)}
	manager.RegisterExecutor(executor)
	auth, errRegister := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "auth-1",
		Provider: "codex",
		FileName: "account.json",
		Status:   coreauth.StatusDisabled,
		Disabled: true,
	})
	if errRegister != nil {
		t.Fatalf("Register() error = %v", errRegister)
	}
	if registered := registry.GetGlobalRegistry().GetModelsForClient(auth.ID); len(registered) != 0 {
		t.Fatalf("disabled auth unexpectedly has registered models: %#v", registered)
	}
	testModels := authFileConnectionTestModels(auth)
	if len(testModels) == 0 {
		t.Fatal("disabled auth did not receive fallback test models")
	}
	testModel := testModels[0].ID

	handler := &Handler{authManager: manager}
	ordinaryRecorder := httptest.NewRecorder()
	ordinaryContext, _ := gin.CreateTestContext(ordinaryRecorder)
	ordinaryContext.Request = httptest.NewRequest(http.MethodGet, "/v0/management/auth-files/models?name=account.json", nil)
	handler.GetAuthFileModels(ordinaryContext)
	if ordinaryRecorder.Code != http.StatusOK || ordinaryRecorder.Body.String() != "{\"models\":[]}" {
		t.Fatalf("disabled auth ordinary models = %s", ordinaryRecorder.Body.String())
	}
	modelsRecorder := httptest.NewRecorder()
	modelsContext, _ := gin.CreateTestContext(modelsRecorder)
	modelsContext.Request = httptest.NewRequest(
		http.MethodGet,
		"/v0/management/auth-files/models?name=account.json&purpose=connection-test",
		nil,
	)
	handler.GetAuthFileModels(modelsContext)
	if modelsRecorder.Code != http.StatusOK || !strings.Contains(modelsRecorder.Body.String(), `"id":"`+testModel+`"`) {
		t.Fatalf("test models status = %d, body = %s", modelsRecorder.Code, modelsRecorder.Body.String())
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/v0/management/auth-files/test",
		strings.NewReader(fmt.Sprintf(`{"name":"account.json","auth_index":"%s","model":"%s"}`, auth.EnsureIndex(), testModel)),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.TestAuthFileConnection(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if executor.lastAuthID != auth.ID || executor.lastModel != testModel {
		t.Fatalf("executor received auth=%q model=%q", executor.lastAuthID, executor.lastModel)
	}
	if !executor.skipMonitor {
		t.Fatal("connection test execution did not carry skip-monitoring context")
	}
	if updated, ok := manager.GetByID(auth.ID); !ok || updated == nil || !updated.Disabled {
		t.Fatalf("connection test changed the operator-disabled state: %#v", updated)
	}
	if body := recorder.Body.String(); !strings.Contains(body, `"success":true`) || !strings.Contains(body, `"output":"OK"`) {
		t.Fatalf("response body = %s", body)
	}
}

func TestGetAuthFileModelsUsesAuthIndexForDuplicateFileNames(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := coreauth.NewManager(nil, nil, nil)
	first, errFirst := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "auth-models-first",
		Index:    "auth-index-first",
		Provider: "codex",
		FileName: "duplicate.json",
		Status:   coreauth.StatusActive,
	})
	if errFirst != nil {
		t.Fatalf("register first auth: %v", errFirst)
	}
	second, errSecond := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "auth-models-second",
		Index:    "auth-index-second",
		Provider: "codex",
		FileName: "duplicate.json",
		Status:   coreauth.StatusActive,
	})
	if errSecond != nil {
		t.Fatalf("register second auth: %v", errSecond)
	}
	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(first.ID, "codex", []*registry.ModelInfo{{ID: "model-first", Type: "openai"}})
	reg.RegisterClient(second.ID, "codex", []*registry.ModelInfo{{ID: "model-second", Type: "openai"}})
	t.Cleanup(func() {
		reg.UnregisterClient(first.ID)
		reg.UnregisterClient(second.ID)
	})

	handler := &Handler{authManager: manager}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(
		http.MethodGet,
		"/v0/management/auth-files/models?name=duplicate.json&auth_index="+second.EnsureIndex(),
		nil,
	)
	handler.GetAuthFileModels(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `"id":"model-second"`) || strings.Contains(body, `"id":"model-first"`) {
		t.Fatalf("models response did not select the indexed auth: %s", body)
	}
}

func TestAuthFileConnectionRejectsUnsupportedModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(&authFileConnectionExecutor{})
	auth, errRegister := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "auth-unsupported-model",
		Provider: "codex",
		FileName: "account.json",
		Status:   coreauth.StatusActive,
	})
	if errRegister != nil {
		t.Fatalf("Register() error = %v", errRegister)
	}
	registry.GetGlobalRegistry().RegisterClient(auth.ID, "codex", []*registry.ModelInfo{{ID: "gpt-test", Type: "openai"}})
	t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(auth.ID) })

	handler := &Handler{authManager: manager}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v0/management/auth-files/test", strings.NewReader(`{"name":"account.json","model":"image-model"}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	handler.TestAuthFileConnection(ctx)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestExtractAuthFileConnectionOutput(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{name: "chat completions", payload: `{"choices":[{"message":{"content":"OK"}}]}`, want: "OK"},
		{name: "responses", payload: `{"output":[{"content":[{"type":"output_text","text":"ready"}]}]}`, want: "ready"},
		{name: "gemini", payload: `{"candidates":[{"content":{"parts":[{"text":"connected"}]}}]}`, want: "connected"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := extractAuthFileConnectionOutput([]byte(test.payload)); got != test.want {
				t.Fatalf("extractAuthFileConnectionOutput() = %q, want %q", got, test.want)
			}
		})
	}
}
