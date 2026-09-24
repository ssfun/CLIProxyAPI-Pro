package management

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	coreusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
)

const authFileConnectionTestTimeout = 45 * time.Second

type authFileConnectionTestRequest struct {
	Name      string `json:"name"`
	AuthIndex string `json:"auth_index"`
	Model     string `json:"model"`
}

type authFileConnectionTestResponse struct {
	Success    bool   `json:"success"`
	Model      string `json:"model,omitempty"`
	LatencyMS  int64  `json:"latency_ms"`
	Output     string `json:"output,omitempty"`
	Error      string `json:"error,omitempty"`
	ErrorCode  string `json:"error_code,omitempty"`
	HTTPStatus int    `json:"http_status,omitempty"`
}

// TestAuthFileConnection sends a minimal real model request through one exact
// auth file. The pinned auth ID prevents a healthy credential from hiding a
// failure on the card the operator selected.
func (h *Handler) TestAuthFileConnection(c *gin.Context) {
	var body authFileConnectionTestRequest
	if errBind := c.ShouldBindJSON(&body); errBind != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	body.AuthIndex = strings.TrimSpace(body.AuthIndex)
	body.Model = strings.TrimSpace(body.Model)
	if body.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}
	if h == nil || h.authManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "auth manager is unavailable"})
		return
	}

	auth, ok := h.lookupAuthFile(body.Name, body.AuthIndex)
	if !ok || auth == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "auth file not found"})
		return
	}
	model, errModel := resolveAuthFileConnectionTestModel(auth, body.Model)
	if errModel != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": errModel.Error()})
		return
	}

	c.JSON(http.StatusOK, h.testAuthConnection(c.Request.Context(), auth, model))
}

// testAuthConnection is shared by the auth-file diagnostic and scheduling
// recovery. Its real result continues through the upstream result accounting.
func (h *Handler) testAuthConnection(ctx context.Context, auth *coreauth.Auth, model string) authFileConnectionTestResponse {
	payload, errPayload := json.Marshal(map[string]any{
		"model": model,
		"messages": []map[string]any{{
			"role":    "user",
			"content": "Reply with OK only.",
		}},
		"stream": false,
	})
	if errPayload != nil {
		return authFileConnectionTestResponse{Error: "failed to build test request", ErrorCode: "request_build_failed"}
	}

	ctx, cancel := context.WithTimeout(ctx, authFileConnectionTestTimeout)
	defer cancel()
	ctx = coreusage.WithSkipMonitoring(ctx)
	startedAt := time.Now()
	response, errExecute := h.authManager.ExecutePinnedAuth(
		ctx,
		auth.ID,
		coreexecutor.Request{
			Model:   model,
			Payload: payload,
			Format:  sdktranslator.FormatOpenAI,
		},
		coreexecutor.Options{
			OriginalRequest: payload,
			SourceFormat:    sdktranslator.FormatOpenAI,
			ResponseFormat:  sdktranslator.FormatOpenAI,
			Metadata: map[string]any{
				coreexecutor.PinnedAuthMetadataKey:     auth.ID,
				coreexecutor.RequestedModelMetadataKey: model,
				coreexecutor.GenerateMetadataKey:       true,
			},
		},
		auth,
	)
	latencyMS := time.Since(startedAt).Milliseconds()
	if errExecute != nil {
		message, code, status := authFileConnectionTestError(errExecute)
		return authFileConnectionTestResponse{
			Success:    false,
			Model:      model,
			LatencyMS:  latencyMS,
			Error:      message,
			ErrorCode:  code,
			HTTPStatus: status,
		}
	}

	output := extractAuthFileConnectionOutput(response.Payload)
	if output == "" {
		return authFileConnectionTestResponse{
			Success:   false,
			Model:     model,
			LatencyMS: latencyMS,
			Error:     "upstream completed without text output",
			ErrorCode: "empty_output",
		}
	}

	return authFileConnectionTestResponse{
		Success:   true,
		Model:     model,
		LatencyMS: latencyMS,
		Output:    output,
	}
}

func resolveAuthFileConnectionTestModel(auth *coreauth.Auth, requested string) (string, error) {
	models := authFileConnectionTestModels(auth)
	textModels := make([]string, 0, len(models))
	for _, model := range models {
		textModels = append(textModels, strings.TrimSpace(model.ID))
	}
	if len(textModels) == 0 {
		return "", fmt.Errorf("auth file has no text model available for testing")
	}
	if requested == "" {
		return textModels[0], nil
	}
	for _, model := range textModels {
		if strings.EqualFold(model, requested) {
			return model, nil
		}
	}
	return "", fmt.Errorf("auth file does not support test model %q", requested)
}

func authFileConnectionTestModels(auth *coreauth.Auth) []*registry.ModelInfo {
	if auth == nil {
		return nil
	}
	models := registry.GetGlobalRegistry().GetModelsForClient(strings.TrimSpace(auth.ID))
	if len(models) == 0 {
		models = authFileManagementFallbackModels(auth)
	}

	result := make([]*registry.ModelInfo, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		if model == nil || !isAuthFileConnectionTextModel(model) {
			continue
		}
		id := strings.TrimSpace(model.ID)
		key := strings.ToLower(id)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, model)
	}
	return result
}

// authFileManagementFallbackModels supplies model metadata when upstream has
// unregistered a disabled auth record from the per-client model registry.
// It is shared by the ordinary auth-file models endpoint and connection tests.
func authFileManagementFallbackModels(auth *coreauth.Auth) []*registry.ModelInfo {
	provider := strings.ToLower(strings.TrimSpace(auth.Provider))
	switch provider {
	case "codex":
		switch strings.ToLower(authFileConnectionAuthValue(auth, "plan_type", "plan")) {
		case "free":
			return registry.GetCodexFreeModels()
		case "team", "business", "go":
			return registry.GetCodexTeamModels()
		case "plus":
			return registry.GetCodexPlusModels()
		default:
			return registry.GetCodexProModels()
		}
	case "gemini-cli", "gemini_cli":
		provider = "gemini"
	case "claude-code", "claude_code":
		provider = "claude"
	}
	if models := registry.GetStaticModelDefinitionsByChannel(provider); len(models) > 0 {
		return models
	}
	return registry.GetGlobalRegistry().GetAvailableModelsByProvider(provider)
}

func authFileConnectionAuthValue(auth *coreauth.Auth, keys ...string) string {
	if auth == nil {
		return ""
	}
	for _, key := range keys {
		if auth.Attributes != nil {
			if value := strings.TrimSpace(auth.Attributes[key]); value != "" {
				return value
			}
		}
		if auth.Metadata != nil {
			if value, exists := auth.Metadata[key]; exists && value != nil {
				if normalized := strings.TrimSpace(fmt.Sprint(value)); normalized != "" {
					return normalized
				}
			}
		}
	}
	return ""
}

func isAuthFileConnectionTextModel(model *registry.ModelInfo) bool {
	if model == nil {
		return false
	}
	id := strings.ToLower(strings.TrimSpace(model.ID))
	if id == "" || strings.Contains(id, "image") || strings.Contains(id, "video") {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(model.Type), registry.OpenAIImageModelType) {
		return false
	}
	if len(model.SupportedOutputModalities) == 0 {
		return true
	}
	for _, modality := range model.SupportedOutputModalities {
		if strings.EqualFold(strings.TrimSpace(modality), "text") {
			return true
		}
	}
	return false
}

func authFileConnectionTestError(err error) (message string, code string, status int) {
	if err == nil {
		return "connection test failed", "connection_test_failed", 0
	}
	if errors.Is(err, coreauth.ErrSchedulingBlockChanged) {
		return "account changed before execution", "account_changed", http.StatusConflict
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "connection test timed out", "timeout", http.StatusGatewayTimeout
	}
	if errors.Is(err, context.Canceled) {
		return "connection test was canceled", "canceled", 0
	}
	message = strings.TrimSpace(err.Error())
	if message == "" {
		message = "connection test failed"
	}
	var authErr *coreauth.Error
	if errors.As(err, &authErr) && authErr != nil {
		code = strings.TrimSpace(authErr.Code)
		status = authErr.HTTPStatus
	}
	var statusErr coreexecutor.StatusError
	if status == 0 && errors.As(err, &statusErr) && statusErr != nil {
		status = statusErr.StatusCode()
	}
	if code == "" {
		code = "connection_test_failed"
	}
	return message, code, status
}

func extractAuthFileConnectionOutput(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	var decoded map[string]any
	if errUnmarshal := json.Unmarshal(payload, &decoded); errUnmarshal != nil {
		return strings.TrimSpace(string(payload))
	}
	if output := stringField(decoded, "output_text"); output != "" {
		return output
	}
	if choices, ok := decoded["choices"].([]any); ok {
		for _, rawChoice := range choices {
			choice, okChoice := rawChoice.(map[string]any)
			if !okChoice {
				continue
			}
			if message, okMessage := choice["message"].(map[string]any); okMessage {
				if output := connectionContentText(message["content"]); output != "" {
					return output
				}
			}
			if output := stringField(choice, "text"); output != "" {
				return output
			}
		}
	}
	if output := connectionContentText(decoded["output"]); output != "" {
		return output
	}
	if output := connectionContentText(decoded["content"]); output != "" {
		return output
	}
	if candidates, ok := decoded["candidates"].([]any); ok {
		for _, rawCandidate := range candidates {
			candidate, okCandidate := rawCandidate.(map[string]any)
			if !okCandidate {
				continue
			}
			if content, okContent := candidate["content"].(map[string]any); okContent {
				if output := connectionContentText(content["parts"]); output != "" {
					return output
				}
			}
		}
	}
	return ""
}

func connectionContentText(raw any) string {
	switch value := raw.(type) {
	case string:
		return strings.TrimSpace(value)
	case map[string]any:
		if text := stringField(value, "text"); text != "" {
			return text
		}
		if text := stringField(value, "output_text"); text != "" {
			return text
		}
		for _, key := range []string{"content", "parts"} {
			if text := connectionContentText(value[key]); text != "" {
				return text
			}
		}
	case []any:
		parts := make([]string, 0, len(value))
		for _, item := range value {
			if text := connectionContentText(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.TrimSpace(strings.Join(parts, ""))
	}
	return ""
}

func stringField(values map[string]any, key string) string {
	if len(values) == 0 {
		return ""
	}
	value, ok := values[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}
