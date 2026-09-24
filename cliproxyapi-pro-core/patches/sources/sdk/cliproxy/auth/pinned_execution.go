package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	cliproxysession "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/session"
)

var ErrSchedulingBlockChanged = errors.New("scheduling block or account changed")

type pinnedExpectedIdentityKey struct{}

func pinnedExpectedIdentityMatches(ctx context.Context, auth *Auth) bool {
	expected, _ := ctx.Value(pinnedExpectedIdentityKey{}).(string)
	return expected == "" || expected == pinnedResultIdentity(auth)
}

func (m *Manager) pinnedCurrentIdentityMatches(auth *Auth) bool {
	current, ok := m.GetByID(auth.ID)
	return ok && pinnedCredentialMatches(auth, current)
}

const pinnedResultIdentityKey = "pro.pinned_auth_identity"

func pinnedResultOptions(opts cliproxyexecutor.Options, auth *Auth) cliproxyexecutor.Options {
	result := opts
	result.Metadata = make(map[string]any, len(opts.Metadata)+1)
	for key, value := range opts.Metadata {
		result.Metadata[key] = value
	}
	result.Metadata[pinnedResultIdentityKey] = pinnedResultIdentity(auth)
	result.Metadata[pinnedResultIdentityKey+".snapshot"] = pinnedRestrictionFingerprint(auth)
	return result
}

// Only digests enter result metadata, which is visible to policies and hooks.
// Never attach Auth or raw credential material to an execution result.
func pinnedFingerprint(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func pinnedResultIdentity(auth *Auth) string {
	if auth == nil {
		return ""
	}
	tokens := make(map[string]any)
	for _, key := range []string{"access_token", "accessToken", "token", "Token", "refresh_token", "refreshToken", "id_token", "idToken", "session_id"} {
		tokens[key] = auth.Metadata[key]
	}
	return pinnedFingerprint([]any{auth.ID, auth.EnsureIndex(), auth.RegistrationEpoch,
		auth.Provider, auth.FileName, authRuntimeIdentityFingerprint(auth), tokens, auth.Attributes, auth.ProxyURL})
}

func pinnedRestrictionFingerprint(auth *Auth) string {
	if auth == nil {
		return ""
	}
	return pinnedFingerprint([]any{auth.Disabled, auth.Status, auth.Unavailable,
		auth.UpdatedAt, auth.NextRetryAfter, auth.Quota, auth.LastError, auth.ModelStates})
}

func pinnedResultIdentityMatches(result Result, auth *Auth) bool {
	if result.Options.Metadata == nil {
		return true
	}
	expected, exists := result.Options.Metadata[pinnedResultIdentityKey]
	if !exists {
		return true
	}
	value, ok := expected.(string)
	if !ok || value == "" || value != pinnedResultIdentity(auth) {
		return false
	}
	// Successful diagnostics may only recover the state observed before I/O.
	// Hashing the original revision and restrictions also detects a concurrent
	// failure with an unchanged retry deadline, without exposing stored state.
	if result.Success {
		snapshot, ok := result.Options.Metadata[pinnedResultIdentityKey+".snapshot"].(string)
		return ok && snapshot != "" && snapshot == pinnedRestrictionFingerprint(auth)
	}

	return true
}

func pinnedCredentialMatches(base, current *Auth) bool {
	return inspectionRefreshIdentityMatches(base, current) &&
		pinnedResultIdentity(base) == pinnedResultIdentity(current) &&
		base.ProxyURL == current.ProxyURL && reflect.DeepEqual(base.Attributes, current.Attributes)
}

// ClearSchedulingBlock withdraws only the selected native cooldown. The
// observed state revision and credential identity prevent an old board action
// from clearing a newer failure or a replacement account.
func (m *Manager) ClearSchedulingBlock(ctx context.Context, base *Auth, model string, revision int64) error {
	if m == nil || base == nil || revision <= 0 {
		return ErrSchedulingBlockChanged
	}
	m.mu.Lock()
	current := m.auths[base.ID]
	if !pinnedCredentialMatches(base, current) || current.Disabled || current.Status == StatusDisabled {
		m.mu.Unlock()
		return ErrSchedulingBlockChanged
	}
	now := time.Now()
	previous := current.Clone()
	if model == "" {
		if current.UpdatedAt.UnixNano() != revision ||
			!current.NextRetryAfter.After(now) && !current.Quota.NextRecoverAt.After(now) {
			m.mu.Unlock()
			return ErrSchedulingBlockChanged
		}
		otherIdentityBlock := hasUnauthorizedAuthFailure(current)
		if expires, ok := current.AccessTokenExpirationTime(); ok && !expires.IsZero() && !expires.After(now) {
			otherIdentityBlock = true
		}
		current.NextRetryAfter = time.Time{}
		applyCooldownFields(&current.Quota, QuotaState{})
		if !otherIdentityBlock {
			clearAuthStateOnSuccess(current, now)
		} else {
			current.UpdatedAt = now
		}
		if len(current.ModelStates) > 0 {
			updateAggregatedAvailability(current, now)
		}
	} else {
		state := current.ModelStates[model]
		if state == nil || state.Status == StatusDisabled || state.UpdatedAt.UnixNano() != revision ||
			!state.NextRetryAfter.After(now) && !state.Quota.NextRecoverAt.After(now) {
			m.mu.Unlock()
			return ErrSchedulingBlockChanged
		}
		otherIdentityBlock := state.LastError != nil && (state.LastError.HTTPStatus == 401 || state.LastError.HTTPStatus == 403)
		state.NextRetryAfter = time.Time{}
		applyCooldownFields(&state.Quota, QuotaState{})
		if !otherIdentityBlock {
			resetModelState(state, now)
		} else {
			state.UpdatedAt = now
		}
		updateAggregatedAvailability(current, now)
	}
	if err := m.persist(ctx, current); err != nil {
		*current = *previous
		m.mu.Unlock()
		return err
	}
	m.mu.Unlock()
	m.RefreshSchedulerEntry(base.ID)
	m.persistCooldownStates(context.Background())
	if model != "" {
		registry.GetGlobalRegistry().ClearModelQuotaExceeded(base.ID, model)
		registry.GetGlobalRegistry().ResumeClientModel(base.ID, model)
	}
	return nil
}

// ExecutePinnedAuth executes one non-streaming request through the exact auth
// record identified by authID. Unlike normal scheduling, this diagnostic path
// deliberately bypasses disabled, cooldown, and unavailable eligibility gates
// so an operator can verify whether a credential has recovered. The normal
// preparation, proxy transport, unauthorized refresh, alias mapping, and result
// accounting paths are still applied.
func (m *Manager) ExecutePinnedAuth(ctx context.Context, authID string, req cliproxyexecutor.Request, opts cliproxyexecutor.Options, expected ...*Auth) (cliproxyexecutor.Response, error) {
	if m == nil {
		return cliproxyexecutor.Response{}, &Error{Code: "auth_not_found", Message: "auth manager is unavailable"}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	req, opts = cliproxysession.Enrich(req, opts)
	auth, okAuth := m.GetByID(authID)
	if !okAuth || auth == nil {
		return cliproxyexecutor.Response{}, &Error{Code: "auth_not_found", Message: "auth not found"}
	}
	if len(expected) > 0 && !pinnedCredentialMatches(expected[0], auth) {
		return cliproxyexecutor.Response{}, ErrSchedulingBlockChanged
	}
	if err := ctx.Err(); err != nil {
		return cliproxyexecutor.Response{}, err
	}
	ctx = context.WithValue(ctx, pinnedExpectedIdentityKey{}, pinnedResultIdentity(auth))
	provider := executorKeyFromAuth(auth)
	executor, okExecutor := m.Executor(provider)
	if !okExecutor || executor == nil {
		return cliproxyexecutor.Response{}, &Error{Code: "executor_not_found", Message: "executor not registered"}
	}

	routeModel := authSelectionModelFromOptions(opts, req.Model)
	executionModel, restoreExecutionModel := executionModelForAuthSelection(opts, req.Model)
	opts = ensureRequestedModelMetadata(opts, routeModel)
	execCtx := ctx
	if rt := m.roundTripperFor(auth); rt != nil {
		execCtx = context.WithValue(execCtx, roundTripperContextKey{}, rt)
		execCtx = context.WithValue(execCtx, "cliproxy.roundtripper", rt)
	}
	execCtx = contextWithRequestedModelAlias(execCtx, opts, routeModel)

	models, pooled, aliasResult, routing := m.executionModelCandidatesWithAlias(auth, routeModel)
	if len(models) == 0 {
		return cliproxyexecutor.Response{}, &Error{Code: "model_not_found", Message: "auth does not provide the requested model"}
	}

	if !m.pinnedCurrentIdentityMatches(auth) {
		return cliproxyexecutor.Response{}, ErrSchedulingBlockChanged
	}
	preparedAuth, errPrepare := m.prepareRequestAuth(execCtx, executor, auth)
	if errPrepare != nil {
		if errors.Is(errPrepare, ErrSchedulingBlockChanged) {
			return cliproxyexecutor.Response{}, errPrepare
		}
		result := Result{AuthID: auth.ID, Provider: provider, Model: routeModel, Success: false, Error: resultErrorFromError(errPrepare), Options: pinnedResultOptions(opts, auth)}
		m.MarkResult(execCtx, result)
		return cliproxyexecutor.Response{}, errPrepare
	}
	if preparedAuth == nil || preparedAuth.RegistrationEpoch != auth.RegistrationEpoch || preparedAuth.EnsureIndex() != auth.EnsureIndex() {
		return cliproxyexecutor.Response{}, ErrSchedulingBlockChanged
	}
	auth = preparedAuth

	var lastErr error
	didRefreshOnUnauthorized := false
	for _, upstreamModel := range models {
		resultModel := m.stateModelForExecution(auth, routeModel, upstreamModel, pooled)
		execReq := req
		execReq.Model = upstreamModel
		if restoreExecutionModel {
			execReq.Model = executionModel
		}
		execOpts := opts
		var errIntercept error
		execReq, execOpts, errIntercept = applyRequestAfterAuthInterceptor(execCtx, executor, provider, execReq, execOpts, requestedModelAliasFromOptions(execOpts, routeModel))
		if errIntercept != nil {
			return cliproxyexecutor.Response{}, errIntercept
		}
		if !restoreExecutionModel {
			execReq = attachResolvedAPIKeyModelInfo(routing, execReq, auth, routeModel, upstreamModel)
		}

		if err := execCtx.Err(); err != nil {
			return cliproxyexecutor.Response{}, err
		}
		if !m.pinnedCurrentIdentityMatches(auth) {
			return cliproxyexecutor.Response{}, ErrSchedulingBlockChanged
		}
		resp, errExecute := executor.Execute(execCtx, auth, execReq, execOpts)
		if errExecute != nil {
			if errContext := execCtx.Err(); errContext != nil {
				return cliproxyexecutor.Response{}, errContext
			}
			if refreshed, okRefresh := m.tryRefreshAfterUnauthorized(execCtx, auth, errExecute, didRefreshOnUnauthorized); okRefresh {
				auth = refreshed
				didRefreshOnUnauthorized = true
				if !m.pinnedCurrentIdentityMatches(auth) {
					return cliproxyexecutor.Response{}, ErrSchedulingBlockChanged
				}
				resp, errExecute = executor.Execute(execCtx, auth, execReq, execOpts)
			}
		}
		if errCancel := claudeOAuthRequestCancellation(execCtx, auth, errExecute); errCancel != nil {
			return cliproxyexecutor.Response{}, errCancel
		}

		result := Result{AuthID: auth.ID, Provider: provider, Model: resultModel, Success: errExecute == nil, Options: pinnedResultOptions(opts, auth)}
		if errExecute != nil {
			result.Error = resultErrorFromError(errExecute)
			if retryAfter := retryAfterFromError(errExecute); retryAfter != nil {
				result.RetryAfter = retryAfter
			}
			m.MarkResult(execCtx, result)
			if isRequestInvalidError(errExecute) {
				return cliproxyexecutor.Response{}, errExecute
			}
			lastErr = errExecute
			continue
		}

		m.MarkResult(execCtx, result)
		attemptAliasResult := resolveAttemptAliasResult(routing, auth, routeModel, upstreamModel, aliasResult)
		rewriteForceMappedResponse(&resp, attemptAliasResult)
		return resp, nil
	}
	if lastErr != nil {
		return cliproxyexecutor.Response{}, lastErr
	}
	return cliproxyexecutor.Response{}, &Error{Code: "model_not_found", Message: "auth does not provide the requested model"}
}
