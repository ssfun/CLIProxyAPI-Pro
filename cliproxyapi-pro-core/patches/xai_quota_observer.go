package executor

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/embeddedusage"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/requestmeta"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	proquota "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/quota"
)

func withXAIQuotaObserver(ctx context.Context, auth *cliproxyauth.Auth, model string) context.Context {
	if !shouldObserveXAIQuota(auth) {
		return ctx
	}
	authSnapshot := auth.Clone()
	return requestmeta.WithUpstreamResponseObserver(ctx, func(
		callCtx context.Context,
		status int,
		header http.Header,
		body []byte,
	) {
		observeXAIQuotaResponse(callCtx, authSnapshot, model, status, header, body)
	})
}

func observeXAIQuotaResponse(ctx context.Context, auth *cliproxyauth.Auth, model string, status int, header http.Header, body []byte) {
	if !shouldObserveXAIQuota(auth) {
		return
	}
	fileName := filepath.Base(strings.TrimSpace(auth.FileName))
	if fileName == "." || fileName == "" {
		fileName = filepath.Base(strings.TrimSpace(auth.ID))
	}
	subject, email, credential := proquota.XAIQuotaAuthIdentity(auth.Metadata, auth.Attributes)
	_ = embeddedusage.ObserveXAIQuotaResponse(ctx, embeddedusage.XAIQuotaObservation{
		RegistrationEpoch: auth.RegistrationEpoch,
		FileName:          fileName,
		AuthIndex:         auth.Index,
		Subject:           subject,
		Email:             email,
		AccessToken:       credential,
		Label:             auth.Label,
		Model:             model,
		Status:            status,
		Header:            header,
		Body:              body,
		ObservedAt:        time.Now(),
	})
}

func shouldObserveXAIQuota(auth *cliproxyauth.Auth) bool {
	if auth == nil || !strings.EqualFold(strings.TrimSpace(auth.Provider), "xai") {
		return false
	}
	// Free-usage quota is a Grok CLI chat-proxy concept. Official API and
	// custom-gateway rate-limit headers must not contaminate this cache.
	return !xaiUsingAPI(auth) && xaiIsCLIChatProxyBaseURL(xaiChatBaseURL(auth))
}
