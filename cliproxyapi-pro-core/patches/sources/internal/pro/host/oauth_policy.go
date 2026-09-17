package host

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	oauthpolicy "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/oauthpolicy/policy"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

// ModelFilter is the business capability consumed by the upstream adapter.
type ModelFilter interface {
	Filter(context.Context, oauthpolicy.Input) oauthpolicy.Result
	EffectivePolicy(string) (oauthpolicy.Result, bool)
}

type AuthHTTPRequester interface {
	HttpRequest(context.Context, *coreauth.Auth, *http.Request) (*http.Response, error)
}

// FilterModels converts volatile upstream auth/model types at the boundary,
// leaving the OAuth account-policy module independent from host internals.
func FilterModels(ctx context.Context, hostCfg *internalconfig.Config, auth *coreauth.Auth, models []*registry.ModelInfo, filter ModelFilter, requester AuthHTTPRequester) []*registry.ModelInfo {
	if auth == nil || filter == nil {
		return models
	}
	inputModels := make([]oauthpolicy.ModelInfo, 0, len(models))
	for _, model := range models {
		if model != nil {
			inputModels = append(inputModels, oauthpolicy.ModelInfo{ID: model.ID})
		}
	}
	storageJSON := storageJSONFromAuth(auth)
	credentialFingerprint := strings.TrimSpace(coreauth.AccessTokenSHA256(auth))
	if credentialFingerprint == "" && len(storageJSON) > 0 {
		credentialFingerprint = fmt.Sprintf("%x", sha256.Sum256(storageJSON))
	}
	result := filter.Filter(ctx, oauthpolicy.Input{
		AuthID: auth.ID, AuthGeneration: auth.Generation, AuthRegistrationEpoch: auth.RegistrationEpoch,
		CredentialFingerprint: credentialFingerprint, AuthProvider: auth.Provider, AuthKind: auth.AuthKind(),
		AuthIndex: auth.Index, FileName: auth.FileName,
		StorageJSON: storageJSON, Metadata: auth.Metadata, Attributes: auth.Attributes, AuthPrefix: auth.Prefix, Models: inputModels,
		HTTPDo: func(callCtx context.Context, req oauthpolicy.HTTPRequest) (oauthpolicy.HTTPResponse, error) {
			return doPolicyHTTP(callCtx, hostCfg, auth, req, requester)
		},
	})
	applyAccountPolicy(auth, result)
	if len(result.ExcludedModelIDs) == 0 {
		return models
	}
	blocked := make(map[string]struct{}, len(result.ExcludedModelIDs))
	for _, id := range result.ExcludedModelIDs {
		blocked[strings.ToLower(strings.TrimSpace(id))] = struct{}{}
	}
	filtered := make([]*registry.ModelInfo, 0, len(models))
	for _, model := range models {
		if model == nil {
			continue
		}
		if _, found := blocked[strings.ToLower(strings.TrimSpace(model.ID))]; !found {
			filtered = append(filtered, model)
		}
	}
	return filtered
}

func ApplyCachedAccountPolicy(auth *coreauth.Auth, filter ModelFilter) *coreauth.Auth {
	if auth == nil {
		return nil
	}
	clone := auth.Clone()
	coreauth.RestoreAccountPolicyBase(clone)
	if filter == nil {
		return clone
	}
	policy, found := filter.EffectivePolicy(auth.ID)
	if !found {
		return clone
	}
	applyAccountPolicy(clone, policy)
	return clone
}

func applyAccountPolicy(auth *coreauth.Auth, result oauthpolicy.Result) {
	if auth == nil || !result.Handled {
		return
	}
	coreauth.RememberAccountPolicyBase(auth)
	if result.Prefix != nil {
		auth.Prefix = *result.Prefix
	}
	if result.Priority != nil || result.Weight != nil {
		if auth.Attributes == nil {
			auth.Attributes = make(map[string]string)
		}
		if result.Priority != nil {
			auth.Attributes["priority"] = strconv.Itoa(*result.Priority)
		}
		if result.Weight != nil {
			auth.Attributes[coreauth.AttributeWeight] = strconv.FormatInt(*result.Weight, 10)
		}
	}
}

func storageJSONFromAuth(auth *coreauth.Auth) []byte {
	if rawProvider, ok := auth.Storage.(interface{ RawJSON() []byte }); ok {
		return bytes.Clone(rawProvider.RawJSON())
	}
	raw, _ := json.Marshal(auth.Metadata)
	return raw
}

func doPolicyHTTP(ctx context.Context, cfg *internalconfig.Config, auth *coreauth.Auth, req oauthpolicy.HTTPRequest, requester AuthHTTPRequester) (oauthpolicy.HTTPResponse, error) {
	method := strings.TrimSpace(req.Method)
	if method == "" {
		method = http.MethodGet
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, req.URL, bytes.NewReader(req.Body))
	if err != nil {
		return oauthpolicy.HTTPResponse{}, err
	}
	for key, values := range req.Headers {
		for _, value := range values {
			httpReq.Header.Add(key, value)
		}
	}
	var resp *http.Response
	if requester != nil {
		resp, err = requester.HttpRequest(ctx, auth, httpReq)
	} else {
		client := helps.NewProxyAwareHTTPClient(ctx, cfg, auth, 0)
		resp, err = client.Do(httpReq)
	}
	if err != nil {
		helps.RecordAPIResponseError(ctx, cfg, err)
		return oauthpolicy.HTTPResponse{}, err
	}
	if resp == nil {
		return oauthpolicy.HTTPResponse{}, fmt.Errorf("auth HTTP executor returned no response")
	}
	defer resp.Body.Close()
	helps.RecordAPIResponseMetadata(ctx, cfg, resp.StatusCode, resp.Header.Clone())
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		helps.RecordAPIResponseError(ctx, cfg, err)
		return oauthpolicy.HTTPResponse{}, err
	}
	if len(body) > 0 {
		helps.AppendAPIResponseChunk(ctx, cfg, body)
	}
	headers := make(map[string][]string, len(resp.Header))
	for key, values := range resp.Header {
		headers[key] = append([]string(nil), values...)
	}
	return oauthpolicy.HTTPResponse{StatusCode: resp.StatusCode, Headers: headers, Body: body}, nil
}
