package management

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	prorouting "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/routing"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestGetRoutingPolicyReturnsReadOnlyBoard(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := startProQuotaTestService(t)
	now := time.Now()
	manager := coreauth.NewManager(nil, nil, nil)
	quotaAuth, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:             "quota-auth",
		Provider:       "codex",
		FileName:       "quota.json",
		Unavailable:    true,
		NextRetryAfter: now.Add(2 * time.Minute),
		Quota: coreauth.QuotaState{
			Exceeded:      true,
			Reason:        "credential_quota",
			NextRecoverAt: now.Add(2 * time.Minute),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	inspectionAuth, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "inspection-auth",
		Provider: "claude",
		FileName: "inspection.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	excluded, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "disabled-auth",
		Provider: "xai",
		FileName: "disabled.json",
		Disabled: true,
		Status:   coreauth.StatusDisabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = quotaAuth
	_ = excluded
	hold := prorouting.QuotaProtection{Recheck: true, RetryAt: now.Add(5 * time.Minute).UnixMilli(), Reason: "inspection quota threshold"}
	if err = manager.ChangeQuotaProtection(ctx, inspectionAuth, inspectionQuotaSource, 0, &hold); err != nil {
		t.Fatal(err)
	}

	h := &Handler{authManager: manager}
	router := gin.New()
	h.RegisterRoutingPolicyRoutes(router.Group("/"))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/routing-policy", nil)
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var response routingPolicyResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Summary.Blocked != 2 || response.Summary.Quota != 1 || response.Summary.Recheck != 1 || response.Summary.Excluded != 1 {
		t.Fatalf("summary = %+v", response.Summary)
	}
	if len(response.Accounts) != 2 {
		t.Fatalf("accounts = %#v", response.Accounts)
	}
}

func TestRetiredRoutingPolicyWritesAreGone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{}
	router := gin.New()
	h.RegisterRoutingPolicyRoutes(router.Group("/"))
	for _, target := range []struct {
		method string
		path   string
	}{
		{http.MethodPut, "/routing-policy"},
		{http.MethodPatch, "/routing-policy"},
		{http.MethodPut, "/routing-policy/request-protection"},
		{http.MethodPost, "/routing-policy/release"},
	} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(target.method, target.path, nil))
		if recorder.Code != http.StatusGone {
			t.Fatalf("%s %s status = %d", target.method, target.path, recorder.Code)
		}
	}
}

func TestSchedulingBoardPrefersInspectionRecheckOverUpstreamExpiry(t *testing.T) {
	now := time.Now()
	auth := &coreauth.Auth{
		ID:             "overlap-auth",
		Provider:       "claude",
		FileName:       "overlap.json",
		Unavailable:    true,
		NextRetryAfter: now.Add(time.Minute),
		Quota: coreauth.QuotaState{
			Exceeded:      true,
			Reason:        "credential_quota",
			NextRecoverAt: now.Add(time.Minute),
		},
		Metadata: map[string]any{},
	}
	setQuotaProtectionsForTest(auth, map[string]prorouting.QuotaProtection{
		inspectionQuotaSource: {Recheck: true, RetryAt: now.Add(10 * time.Minute).UnixMilli(), Reason: "inspection quota threshold"},
	})
	account := schedulingBoardAccount(auth, now)
	if account.Resume != "recheck-quota" || account.Bucket != "overlap" || !account.Overlap {
		t.Fatalf("account = %+v", account)
	}
	if account.RetryAt != now.Add(10*time.Minute).UnixMilli() {
		t.Fatalf("retry at = %d", account.RetryAt)
	}
}

func TestSchedulingBoardKeepsManualInspectionHold(t *testing.T) {
	now := time.Now()
	auth := &coreauth.Auth{ID: "manual-auth", Provider: "codex", FileName: "manual.json", Metadata: map[string]any{}}
	setQuotaProtectionsForTest(auth, map[string]prorouting.QuotaProtection{
		inspectionQuotaSource: {Reason: "inspection quota threshold"},
	})
	account := schedulingBoardAccount(auth, now)
	if account.Resume != "manual" || account.RetryAt != 0 || account.Bucket != "quota" {
		t.Fatalf("account = %+v", account)
	}
}

func TestSchedulingBoardKeepsDueInspectionRecheckTime(t *testing.T) {
	now := time.Now()
	due := now.Add(-time.Minute).UnixMilli()
	auth := &coreauth.Auth{ID: "due-auth", Provider: "claude", FileName: "due.json", Metadata: map[string]any{}}
	setQuotaProtectionsForTest(auth, map[string]prorouting.QuotaProtection{
		inspectionQuotaSource: {Recheck: true, RetryAt: due, Reason: "inspection quota threshold"},
	})
	account := schedulingBoardAccount(auth, now)
	if account.Resume != "recheck-quota" || account.RetryAt != due || account.Bucket != "recheck" {
		t.Fatalf("account = %+v", account)
	}
}

func TestClearLegacyRoutingQuotaProtectionsRemovesRetiredHolds(t *testing.T) {
	ctx := startProQuotaTestService(t)
	now := time.Now()
	manager := coreauth.NewManager(nil, nil, nil)
	auth, err := manager.Register(context.Background(), &coreauth.Auth{ID: "legacy-routing", Provider: "codex", FileName: "legacy.json"})
	if err != nil {
		t.Fatal(err)
	}
	if err = manager.ChangeQuotaProtection(ctx, auth, inspectionQuotaSource, 0, &prorouting.QuotaProtection{Recheck: true, RetryAt: now.Add(time.Hour).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	current, _ := manager.GetByID(auth.ID)
	setQuotaProtectionsForTest(current, map[string]prorouting.QuotaProtection{
		inspectionQuotaSource: prorouting.QuotaProtections(current.Metadata)[inspectionQuotaSource],
		"routing:gpt-test":    {Source: "routing:gpt-test", Model: "gpt-test", RetryAt: now.Add(time.Hour).UnixMilli(), Reason: "request protection"},
	})
	h := &Handler{authManager: manager}
	clearLegacyRoutingQuotaProtections(h)
	current, _ = manager.GetByID(auth.ID)
	got := prorouting.QuotaProtections(current.Metadata)
	if _, ok := got["routing:gpt-test"]; ok {
		t.Fatalf("legacy routing hold remained: %#v", got)
	}
	if _, ok := got[inspectionQuotaSource]; !ok {
		t.Fatal("inspection hold was cleared")
	}
}

func TestClearLegacyRoutingQuotaProtectionsLeavesUnprotectedAuthsUnchanged(t *testing.T) {
	_ = startProQuotaTestService(t)
	manager := coreauth.NewManager(nil, nil, nil)
	auth, err := manager.Register(context.Background(), &coreauth.Auth{ID: "clean-routing", Provider: "codex", FileName: "clean.json"})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := manager.GetByID(auth.ID)
	clearLegacyRoutingQuotaProtections(&Handler{authManager: manager})
	after, _ := manager.GetByID(auth.ID)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("legacy routing sweep mutated an unprotected auth")
	}
}

func TestSchedulingBoardCountsExcludedAndAuthTransient(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Now()
	manager := coreauth.NewManager(nil, nil, nil)
	if _, err := manager.Register(context.Background(), &coreauth.Auth{
		ID: "disabled-auth", Provider: "xai", FileName: "disabled.json", Disabled: true, Status: coreauth.StatusDisabled,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Register(context.Background(), &coreauth.Auth{
		ID: "transient-auth", Provider: "codex", FileName: "transient.json",
		Unavailable: true, NextRetryAfter: now.Add(2 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	h := &Handler{authManager: manager}
	response := h.routingPolicyResponse()
	if response.Summary.Excluded != 1 || response.Summary.AuthTransient != 1 || response.Summary.Blocked != 1 {
		t.Fatalf("summary = %+v", response.Summary)
	}
	if len(response.Accounts) != 1 || response.Accounts[0].Bucket != "authTransient" {
		t.Fatalf("accounts = %#v", response.Accounts)
	}
}

func setQuotaProtectionsForTest(auth *coreauth.Auth, protections map[string]prorouting.QuotaProtection) {
	raw, _ := json.Marshal(protections)
	if auth.Metadata == nil {
		auth.Metadata = map[string]any{}
	}
	auth.Metadata[prorouting.QuotaProtectionMetadataKey] = string(raw)
}

func TestUpdateProAuthSerializesInspectionPriority(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	registered, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "serialized-auth",
		Provider: "xai",
	})
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{authManager: manager}
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- h.updateProAuth(context.Background(), registered.Index, func(auth *coreauth.Auth) {
			close(firstEntered)
			<-releaseFirst
			setProAuthDisabledState(auth, true)
			auth.Metadata[prorouting.ProtectionMetadataKey] = map[string]any{"owner": prorouting.ProtectionOwner}
		})
	}()
	<-firstEntered
	inspectionDone := make(chan error, 1)
	go func() {
		inspectionDone <- h.updateProAuth(context.Background(), registered.Index, func(auth *coreauth.Auth) {
			setProAuthDisabledState(auth, false)
		})
	}()
	select {
	case err = <-inspectionDone:
		t.Fatalf("inspection mutation bypassed serialization: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(releaseFirst)
	if err = <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err = <-inspectionDone; err != nil {
		t.Fatal(err)
	}
	updated, ok := manager.GetByID(registered.ID)
	if !ok || updated == nil {
		t.Fatal("updated auth missing")
	}
	if updated.Disabled || prorouting.ProtectionOwned(updated.Metadata) {
		t.Fatalf("inspection must win: disabled=%v metadata=%#v", updated.Disabled, updated.Metadata)
	}
}
