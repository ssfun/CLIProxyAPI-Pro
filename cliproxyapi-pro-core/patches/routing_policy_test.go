package management

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	proinspection "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/inspection"
	prorouting "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/routing"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

func TestRoutingRecoveryChecksPinnedModelAndClearsNativeCooldown(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := coreauth.NewManager(nil, nil, nil)
	executor := &authFileConnectionExecutor{response: []byte(`{"choices":[{"message":{"content":"OK"}}]}`)}
	manager.RegisterExecutor(executor)
	model := authFileConnectionTestModels(&coreauth.Auth{Provider: "codex"})[0].ID
	now := time.Now()
	auth, err := manager.Register(context.Background(), &coreauth.Auth{
		ID: "recover-model", Provider: "codex", FileName: "recover.json",
		ModelStates: map[string]*coreauth.ModelState{
			model: {Unavailable: true, Status: coreauth.StatusError, NextRetryAfter: now.Add(time.Hour), UpdatedAt: now},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{authManager: manager}
	router := gin.New()
	h.RegisterRoutingPolicyRoutes(router.Group("/"))
	body, _ := json.Marshal(routingRecoveryRequest{AuthID: auth.ID, AuthIndex: auth.EnsureIndex(), RegistrationEpoch: strconv.FormatUint(auth.RegistrationEpoch, 10)})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/routing-policy/check", strings.NewReader(string(body))))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if executor.lastAuthID != auth.ID || executor.lastModel != model {
		t.Fatalf("pinned request = %s/%s", executor.lastAuthID, executor.lastModel)
	}
	var response struct {
		After routingBoardAccount            `json:"after"`
		Test  authFileConnectionTestResponse `json:"test"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Test.Success || response.After.AuthID != "" {
		t.Fatalf("recovery = %+v body = %s", response, recorder.Body.String())
	}
}

func TestRoutingRecoveryReleaseOnlySelectedSourceAndRejectsStaleRevision(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := startProQuotaTestService(t)
	manager := coreauth.NewManager(nil, nil, nil)
	now := time.Now()
	auth, err := manager.Register(context.Background(), &coreauth.Auth{
		ID: "overlap-release", Provider: "codex", FileName: "overlap.json",
		Unavailable: true, NextRetryAfter: now.Add(time.Hour), UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ChangeQuotaProtection(ctx, auth, inspectionQuotaSource, 0,
		&prorouting.QuotaProtection{RetryAt: now.Add(2 * time.Hour).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	current, _ := manager.GetByID(auth.ID)
	before := schedulingBoardAccount(current, time.Now())
	var inspectionRevision, upstreamRevision string
	for _, detail := range before.Details {
		if detail.Source == "inspection" {
			inspectionRevision = detail.Revision
		}
		if detail.Source == "upstream" {
			upstreamRevision = detail.Revision
		}
	}
	if inspectionRevision == "" || upstreamRevision == "" {
		t.Fatalf("missing revisions: %+v", before.Details)
	}
	h := &Handler{authManager: manager}
	router := gin.New()
	h.RegisterRoutingPolicyRoutes(router.Group("/"))
	send := func(request routingRecoveryRequest) *httptest.ResponseRecorder {
		body, _ := json.Marshal(request)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/routing-policy/restrictions/release", strings.NewReader(string(body))))
		return recorder
	}
	base := routingRecoveryRequest{AuthID: auth.ID, AuthIndex: auth.EnsureIndex(), RegistrationEpoch: strconv.FormatUint(auth.RegistrationEpoch, 10), Source: "inspection", Revision: inspectionRevision}
	if recorder := send(base); recorder.Code != http.StatusOK {
		t.Fatalf("inspection release status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if recorder := send(base); recorder.Code != http.StatusConflict {
		t.Fatalf("stale release status = %d", recorder.Code)
	}
	remaining, _ := manager.GetByID(auth.ID)
	if board := schedulingBoardAccount(remaining, time.Now()); board.AuthID == "" || board.Inspection || !containsString(board.Sources, "upstream") {
		t.Fatalf("release changed native cooldown: %+v", board)
	}
	if recorder := send(routingRecoveryRequest{AuthID: auth.ID, AuthIndex: auth.EnsureIndex(), RegistrationEpoch: strconv.FormatUint(auth.RegistrationEpoch, 10), Source: "upstream", Revision: upstreamRevision}); recorder.Code != http.StatusConflict {
		t.Fatalf("stale native release status = %d", recorder.Code)
	}
	for _, detail := range schedulingBoardAccount(remaining, time.Now()).Details {
		if detail.Source == "upstream" {
			upstreamRevision = detail.Revision
		}
	}
	if recorder := send(routingRecoveryRequest{AuthID: auth.ID, AuthIndex: auth.EnsureIndex(), RegistrationEpoch: strconv.FormatUint(auth.RegistrationEpoch, 10), Source: "upstream", Revision: upstreamRevision}); recorder.Code != http.StatusOK {
		t.Fatalf("native release status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	remaining, _ = manager.GetByID(auth.ID)
	if board := schedulingBoardAccount(remaining, time.Now()); board.AuthID != "" {
		t.Fatalf("cooldown not cleared: %+v", board)
	}
}

func TestRoutingRecoveryRejectsReplacedAccountWithSameIndex(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := coreauth.NewManager(nil, nil, nil)
	old, err := manager.Register(context.Background(), &coreauth.Auth{
		ID: "replaced-recovery", Provider: "codex", FileName: "same.json",
		Unavailable: true, NextRetryAfter: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	oldEpoch := strconv.FormatUint(old.RegistrationEpoch, 10)
	newAuth, err := manager.Register(context.Background(), &coreauth.Auth{
		ID: old.ID, Provider: old.Provider, FileName: old.FileName,
		Unavailable: true, NextRetryAfter: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if old.EnsureIndex() != newAuth.EnsureIndex() {
		t.Fatal("test account index changed")
	}
	h := &Handler{authManager: manager}
	router := gin.New()
	h.RegisterRoutingPolicyRoutes(router.Group("/"))
	body, _ := json.Marshal(routingRecoveryRequest{AuthID: old.ID, AuthIndex: old.EnsureIndex(), RegistrationEpoch: oldEpoch})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/routing-policy/check", strings.NewReader(string(body))))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("old account check status = %d body = %s", recorder.Code, recorder.Body.String())
	}
}

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

func TestSchedulingBoardSeparatesInspectionActionFromUpstreamTransition(t *testing.T) {
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
	if account.Resume != "multiple" || account.Bucket != "overlap" || !account.Overlap {
		t.Fatalf("account = %+v", account)
	}
	if account.NextActionAt != now.Add(10*time.Minute).UnixMilli() {
		t.Fatalf("next action at = %d", account.NextActionAt)
	}
	if account.NextTransitionAt != now.Add(time.Minute).UnixMilli() || account.RetryAt != account.NextTransitionAt {
		t.Fatalf("transition/retry = %d/%d", account.NextTransitionAt, account.RetryAt)
	}
}

func TestSchedulingBoardKeepsDueInspectionActionSeparateFromLaterCooldown(t *testing.T) {
	now := time.Now()
	due := now.Add(-time.Minute).UnixMilli()
	auth := &coreauth.Auth{
		ID: "due-overlap", Provider: "claude", FileName: "due-overlap.json",
		Unavailable: true, NextRetryAfter: now.Add(10 * time.Minute),
		Metadata: map[string]any{},
	}
	setQuotaProtectionsForTest(auth, map[string]prorouting.QuotaProtection{
		inspectionQuotaSource: {Recheck: true, RetryAt: due, Reason: "inspection quota threshold"},
	})
	account := schedulingBoardAccount(auth, now)
	if account.NextActionAt != due || account.NextTransitionAt != now.Add(10*time.Minute).UnixMilli() {
		t.Fatalf("account = %+v", account)
	}
}

func TestSchedulingBoardUsesEarliestKnownModelTransition(t *testing.T) {
	now := time.Now()
	auth := &coreauth.Auth{ID: "multi-model", Provider: "codex", ModelStates: map[string]*coreauth.ModelState{
		"model-a": {Unavailable: true, NextRetryAfter: now.Add(time.Minute)},
		"model-b": {Unavailable: true, NextRetryAfter: now.Add(5 * time.Minute)},
	}}
	account := schedulingBoardAccount(auth, now)
	if account.NextTransitionAt != now.Add(time.Minute).UnixMilli() {
		t.Fatalf("account = %+v", account)
	}
	if len(account.Details) != 2 || account.Details[0].RetryAt == account.Details[1].RetryAt {
		t.Fatalf("details = %+v", account.Details)
	}
}

func TestSchedulingBoardReportsUntimedUnavailableAccount(t *testing.T) {
	now := time.Now()
	auth := &coreauth.Auth{ID: "untimed", Provider: "codex", Unavailable: true}
	account := schedulingBoardAccount(auth, now)
	if account.AuthID != auth.ID || account.Resume != "await-state-change" || account.RetryAt != 0 || account.Bucket != "authTransient" {
		t.Fatalf("account = %+v", account)
	}
	if len(account.Details) != 1 || account.Details[0].Reason != "unavailable" {
		t.Fatalf("details = %+v", account.Details)
	}
}

func TestSchedulingBoardCredentialScopeOverridesModelList(t *testing.T) {
	now := time.Now()
	auth := &coreauth.Auth{
		ID: "scope-overlap", Provider: "claude", Metadata: map[string]any{},
		ModelStates: map[string]*coreauth.ModelState{
			"model-a": {Unavailable: true, NextRetryAfter: now.Add(time.Minute)},
		},
	}
	setQuotaProtectionsForTest(auth, map[string]prorouting.QuotaProtection{
		inspectionQuotaSource: {Recheck: true, RetryAt: now.Add(2 * time.Minute).UnixMilli(), Reason: "inspection quota threshold"},
	})
	account := schedulingBoardAccount(auth, now)
	if account.Scope != "credential" || !reflect.DeepEqual(account.Models, []string{"model-a"}) {
		t.Fatalf("account = %+v", account)
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
	if account.Resume != "recheck-quota" || account.RetryAt != due || account.NextActionAt != due || account.NextTransitionAt != 0 || account.Bucket != "recheck" {
		t.Fatalf("account = %+v", account)
	}
}

func TestSchedulingBoardKeepsDueProbeBlockedUntilRecoveryRuns(t *testing.T) {
	now := time.Now()
	due := now.Add(-time.Minute).UnixMilli()
	auth := &coreauth.Auth{ID: "probe-auth", Provider: "xai", FileName: "probe.json", Metadata: map[string]any{}}
	setQuotaProtectionsForTest(auth, map[string]prorouting.QuotaProtection{
		inspectionQuotaSource: {RetryAt: due, Reason: "inspection quota threshold"},
	})
	account := schedulingBoardAccount(auth, now)
	if account.Resume != "probe-request" || account.RetryAt != due || account.NextActionAt != due || account.NextTransitionAt != 0 || account.Bucket != "quota" {
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

func TestUpdateProAuthSerializesAdministrativeEnable(t *testing.T) {
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
	if updated.Disabled || !prorouting.ProtectionOwned(updated.Metadata) {
		t.Fatalf("enable must preserve independent protection: disabled=%v metadata=%#v", updated.Disabled, updated.Metadata)
	}
}

func TestRoutingRecoveryRejectsDisappearedSource(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := coreauth.NewManager(nil, nil, nil)
	auth, err := manager.Register(context.Background(), &coreauth.Auth{ID: "source-disappeared", Provider: "codex", Unavailable: true, NextRetryAfter: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{authManager: manager}
	router := gin.New()
	h.RegisterRoutingPolicyRoutes(router.Group("/"))
	body, _ := json.Marshal(routingRecoveryRequest{AuthID: auth.ID, AuthIndex: auth.EnsureIndex(), RegistrationEpoch: strconv.FormatUint(auth.RegistrationEpoch, 10), Source: "inspection"})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/routing-policy/check", strings.NewReader(string(body))))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestRoutingInspectionReleaseImmediatelyUpdatesStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := startProQuotaTestService(t)
	t.Setenv("ACCOUNT_INSPECTION_SCHEDULE_PATH", t.TempDir()+"/schedule.json")
	t.Setenv("ACCOUNT_INSPECTION_SNAPSHOT_PATH", t.TempDir()+"/snapshot.json")
	manager := coreauth.NewManager(nil, nil, nil)
	auth, err := manager.Register(ctx, &coreauth.Auth{ID: "release-inspection-status", Provider: "codex", FileName: "release.json"})
	if err != nil {
		t.Fatal(err)
	}
	if err = manager.ChangeQuotaProtection(ctx, auth, inspectionQuotaSource, 0, &prorouting.QuotaProtection{RetryAt: time.Now().Add(time.Hour).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	auth, _ = manager.GetByID(auth.ID)
	hold := prorouting.QuotaProtections(auth.Metadata)[inspectionQuotaSource]
	h := &Handler{authManager: manager}
	scheduler := newAccountInspectionScheduler(h, nil)
	accountInspectionSchedulers.Store(h, scheduler)
	t.Cleanup(func() { accountInspectionSchedulers.Delete(h) })
	result := accountFromAuth(auth).baseResult()
	scheduler.fillQuotaProtectionResult(auth, &result)
	result.IsQuota = true
	result.Action = accountInspectionActionDisable
	result.ActionReason = "最近探测额度不足"
	used := 99.0
	result.UsedPercent = &used
	scheduler.status.Results = []accountInspectionResult{result}
	updates := make(chan accountInspectionLogStreamMessage, 4)
	scheduler.subscribers[updates] = struct{}{}
	router := gin.New()
	h.RegisterRoutingPolicyRoutes(router.Group("/"))
	router.GET("/account-inspection/status", h.GetAccountInspectionStatus)
	statusResult := func() accountInspectionResult {
		t.Helper()
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/account-inspection/status", nil))
		var response struct {
			Status accountInspectionStatus `json:"status"`
		}
		if recorder.Code != http.StatusOK {
			t.Fatalf("status GET=%d", recorder.Code)
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if len(response.Status.Results) != 1 {
			t.Fatalf("results=%+v", response.Status.Results)
		}
		return response.Status.Results[0]
	}
	if !statusResult().QuotaCooling {
		t.Fatal("fixture has no inspection hold")
	}
	body, _ := json.Marshal(routingRecoveryRequest{AuthID: auth.ID, AuthIndex: auth.EnsureIndex(), RegistrationEpoch: strconv.FormatUint(auth.RegistrationEpoch, 10), Source: "inspection", Revision: strconv.FormatInt(hold.Revision, 10)})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/routing-policy/restrictions/release", strings.NewReader(string(body))))
	if recorder.Code != http.StatusOK {
		t.Fatalf("release=%d %s", recorder.Code, recorder.Body.String())
	}
	updated := statusResult()
	if updated.QuotaCooling || updated.QuotaRetryAt != 0 || updated.QuotaRevision != 0 || !updated.IsQuota || updated.Action != result.Action || updated.ActionReason != result.ActionReason || updated.UsedPercent == nil || *updated.UsedPercent != used {
		t.Fatalf("stale status after release: %+v", updated)
	}
	select {
	case <-updates:
	default:
		t.Fatal("inspection status update was not broadcast")
	}
}

func TestRoutingReleaseUsesNormalizedHistoricalModelStateKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := coreauth.NewManager(nil, nil, nil)
	now := time.Now()
	const key = " gpt-5(high) "
	auth, err := manager.Register(context.Background(), &coreauth.Auth{ID: "historical-model", Provider: "codex", ModelStates: map[string]*coreauth.ModelState{
		key:     {Status: coreauth.StatusError, Unavailable: true, NextRetryAfter: now.Add(time.Hour), UpdatedAt: now},
		"gpt-5": {Status: coreauth.StatusError, Unavailable: true, NextRetryAfter: now.Add(time.Minute), UpdatedAt: now.Add(-time.Second)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	board := schedulingBoardAccount(auth, now)
	if len(board.Details) != 1 || board.Details[0].Model != "gpt-5" || board.Details[0].Revision != strconv.FormatInt(now.UnixNano(), 10) {
		t.Fatalf("details=%+v", board.Details)
	}
	h := &Handler{authManager: manager}
	router := gin.New()
	h.RegisterRoutingPolicyRoutes(router.Group("/"))
	body, _ := json.Marshal(routingRecoveryRequest{AuthID: auth.ID, AuthIndex: auth.EnsureIndex(), RegistrationEpoch: strconv.FormatUint(auth.RegistrationEpoch, 10), Source: "upstream", Model: board.Details[0].Model, Revision: board.Details[0].Revision})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/routing-policy/restrictions/release", strings.NewReader(string(body))))
	if recorder.Code != http.StatusOK {
		t.Fatalf("release=%d %s", recorder.Code, recorder.Body.String())
	}
	current, _ := manager.GetByID(auth.ID)
	if len(current.ModelStates) != 1 || current.ModelStates["gpt-5"].Unavailable {
		t.Fatal("release changed wrong model state")
	}
}

type deadlineRoutingExecutor struct {
	authFileConnectionExecutor
	deadline time.Time
}

func (e *deadlineRoutingExecutor) Execute(ctx context.Context, auth *coreauth.Auth, req coreexecutor.Request, opts coreexecutor.Options) (coreexecutor.Response, error) {
	e.deadline, _ = ctx.Deadline()
	<-ctx.Done()
	return coreexecutor.Response{}, ctx.Err()
}
func TestRoutingRecoveryReturnsTimeoutPhaseAndLiveRestriction(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := coreauth.NewManager(nil, nil, nil)
	executor := &deadlineRoutingExecutor{}
	manager.RegisterExecutor(executor)
	now := time.Now()
	auth, err := manager.Register(context.Background(), &coreauth.Auth{ID: "deadline", Provider: "codex", Unavailable: true, NextRetryAfter: now.Add(time.Hour), UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{authManager: manager}
	router := gin.New()
	h.RegisterRoutingPolicyRoutes(router.Group("/"))
	body, _ := json.Marshal(routingRecoveryRequest{AuthID: auth.ID, AuthIndex: auth.EnsureIndex(), RegistrationEpoch: strconv.FormatUint(auth.RegistrationEpoch, 10)})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	deadline, _ := ctx.Deadline()
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/routing-policy/check", strings.NewReader(string(body))).WithContext(ctx))
	var response struct {
		After  routingBoardAccount
		Phases []routingRecoveryPhase
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != 200 || len(response.Phases) != 1 || response.Phases[0].Status != "timeout" || response.After.AuthID != auth.ID || executor.deadline != deadline {
		t.Fatalf("response=%s deadline=%v want=%v", recorder.Body.String(), executor.deadline, deadline)
	}
}

type twoStageClaudeExecutor struct{ deadlineRoutingExecutor }

func (*twoStageClaudeExecutor) Identifier() string { return "claude" }

func TestRoutingRecoveryPreservesQuotaSuccessWhenUpstreamTimesOut(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := startProQuotaTestService(t)
	manager := coreauth.NewManager(nil, nil, nil)
	executor := &twoStageClaudeExecutor{}
	manager.RegisterExecutor(executor)
	now := time.Now()
	auth, err := manager.Register(ctx, &coreauth.Auth{
		ID: "two-stage-recovery", FileName: "two-stage.json", Provider: "claude",
		Metadata: map[string]any{"access_token": "test-token"}, Runtime: inspectionProbeRefreshDue(false),
		Unavailable: true, NextRetryAfter: now.Add(time.Hour), UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	settings := proinspection.DefaultSettings()
	settings.AutoExecuteQuotaRecoveryEnable = true
	settings.UsedPercentThreshold = 95
	raw, _ := json.Marshal(settings)
	if err := manager.ChangeQuotaProtection(ctx, auth, inspectionQuotaSource, 0, &prorouting.QuotaProtection{
		Recheck: true, RetryAt: now.Add(-time.Minute).UnixMilli(), Settings: raw,
	}); err != nil {
		t.Fatal(err)
	}
	auth, _ = manager.GetByID(auth.ID)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/profile") {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		_, _ = w.Write([]byte(`{"five_hour":{"utilization":10}}`))
	}))
	defer server.Close()
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.TLSClientConfig.ServerName = "example.com"
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	previousTransport := http.DefaultTransport
	http.DefaultTransport = transport
	defer func() { http.DefaultTransport = previousTransport; transport.CloseIdleConnections() }()
	h := &Handler{authManager: manager}
	scheduler := newAccountInspectionScheduler(h, nil)
	accountInspectionSchedulers.Store(h, scheduler)
	t.Cleanup(func() { accountInspectionSchedulers.Delete(h) })
	router := gin.New()
	h.RegisterRoutingPolicyRoutes(router.Group("/"))
	body, _ := json.Marshal(routingRecoveryRequest{AuthID: auth.ID, AuthIndex: auth.EnsureIndex(), RegistrationEpoch: strconv.FormatUint(auth.RegistrationEpoch, 10)})
	requestCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	deadline, _ := requestCtx.Deadline()
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/routing-policy/check", strings.NewReader(string(body))).WithContext(requestCtx))
	var response struct {
		After  routingBoardAccount
		Phases []routingRecoveryPhase
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusOK || len(response.Phases) != 2 || response.Phases[0].Source != "inspection" || response.Phases[0].Status != "completed" || response.Phases[1].Source != "upstream" || response.Phases[1].Status != "timeout" {
		t.Fatalf("wrong recovery phases: %s", recorder.Body.String())
	}
	if response.After.AuthID != auth.ID || response.After.Inspection || !containsString(response.After.Sources, "upstream") {
		t.Fatalf("response lost remaining native restriction: %s", recorder.Body.String())
	}
	if executor.deadline != deadline {
		t.Fatalf("upstream deadline = %v, want shared deadline %v", executor.deadline, deadline)
	}
	current, _ := manager.GetByID(auth.ID)
	if _, held := prorouting.QuotaProtections(current.Metadata)[inspectionQuotaSource]; held {
		t.Fatal("successful quota stage did not release its protection")
	}
}

type xaiRecoveryResultHook struct {
	coreauth.NoopHook
	results atomic.Int32
}

func (h *xaiRecoveryResultHook) OnResult(context.Context, coreauth.Result) { h.results.Add(1) }

type controlledXAIRecoveryExecutor struct {
	xaiInspectionRoutingExecutor
	started chan struct{}
	release chan struct{}
}

func (e *controlledXAIRecoveryExecutor) HttpRequest(ctx context.Context, auth *coreauth.Auth, req *http.Request) (*http.Response, error) {
	if strings.HasSuffix(req.URL.Path, "/chat/completions") && e.started != nil {
		e.started <- struct{}{}
		select {
		case <-e.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return e.xaiInspectionRoutingExecutor.HttpRequest(ctx, auth, req)
}

type xaiRoutingRecoveryFixture struct {
	ctx      context.Context
	manager  *coreauth.Manager
	auth     *coreauth.Auth
	executor *controlledXAIRecoveryExecutor
	hook     *xaiRecoveryResultHook
	router   *gin.Engine
}

func newXAIRoutingRecoveryFixture(t *testing.T, status int, body string, blocked bool) xaiRoutingRecoveryFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ctx := startProQuotaTestService(t)
	hook := &xaiRecoveryResultHook{}
	manager := coreauth.NewManager(nil, nil, hook)
	executor := &controlledXAIRecoveryExecutor{}
	executor.officialStatus = status
	executor.officialBody = body
	if blocked {
		executor.started = make(chan struct{}, 1)
		executor.release = make(chan struct{})
	}
	manager.RegisterExecutor(executor)
	auth, err := manager.Register(ctx, &coreauth.Auth{
		ID: "xai-recovery-" + t.Name(), FileName: "xai-recovery.json", Provider: "xai",
		Attributes: map[string]string{"api_key": "test-token", "base_url": "https://api.x.ai/v1", "using_api": "true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	settings := proinspection.DefaultSettings()
	settings.AutoExecuteQuotaRecoveryEnable = false
	settings.XAIDeepProbeModel = "grok-4.5"
	raw, _ := json.Marshal(settings)
	if err := manager.ChangeQuotaProtection(ctx, auth, inspectionQuotaSource, 0, &prorouting.QuotaProtection{
		Recheck: false, RetryAt: time.Now().Add(-time.Minute).UnixMilli(), Settings: raw,
	}); err != nil {
		t.Fatal(err)
	}
	auth, _ = manager.GetByID(auth.ID)
	h := &Handler{authManager: manager}
	scheduler := newAccountInspectionScheduler(h, nil)
	scheduler.schedule.Settings = settings
	accountInspectionSchedulers.Store(h, scheduler)
	t.Cleanup(func() { accountInspectionSchedulers.Delete(h) })
	router := gin.New()
	h.RegisterRoutingPolicyRoutes(router.Group("/"))
	return xaiRoutingRecoveryFixture{ctx: ctx, manager: manager, auth: auth, executor: executor, hook: hook, router: router}
}

func (f xaiRoutingRecoveryFixture) check() *httptest.ResponseRecorder {
	request := routingRecoveryRequest{AuthID: f.auth.ID, AuthIndex: f.auth.EnsureIndex(), RegistrationEpoch: strconv.FormatUint(f.auth.RegistrationEpoch, 10), Source: "inspection"}
	body, _ := json.Marshal(request)
	recorder := httptest.NewRecorder()
	f.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/routing-policy/check", strings.NewReader(string(body))))
	return recorder
}

func (f xaiRoutingRecoveryFixture) startCheck(t *testing.T) <-chan *httptest.ResponseRecorder {
	t.Helper()
	finished := make(chan *httptest.ResponseRecorder, 1)
	go func() { finished <- f.check() }()
	select {
	case <-f.executor.started:
	case <-time.After(2 * time.Second):
		t.Fatal("official xAI recovery probe did not start")
	}
	return finished
}

func finishXAIRoutingCheck(t *testing.T, f xaiRoutingRecoveryFixture, finished <-chan *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	t.Helper()
	close(f.executor.release)
	select {
	case response := <-finished:
		return response
	case <-time.After(2 * time.Second):
		t.Fatal("official xAI recovery probe did not finish")
		return nil
	}
}

func TestXAIRoutingRecoveryKeepsNewerModelCooldown(t *testing.T) {
	for _, stripMetadata := range []bool{false, true} {
		name := "default policy"
		if stripMetadata {
			name = "result policy strips metadata"
		}
		t.Run(name, func(t *testing.T) {
			f := newXAIRoutingRecoveryFixture(t, http.StatusOK, `{"id":"chatcmpl-test","choices":[]}`, true)
			finished := f.startCheck(t)
			f.manager.MarkResult(f.ctx, coreauth.Result{AuthID: f.auth.ID, Provider: "xai", Model: "grok-4.5", Success: false, Error: &coreauth.Error{HTTPStatus: http.StatusTooManyRequests, Message: "newer quota failure"}})
			before, _ := f.manager.GetByID(f.auth.ID)
			state := before.ModelStates["grok-4.5"]
			if state == nil || !state.NextRetryAfter.After(time.Now()) {
				t.Fatalf("newer native cooldown was not established: %+v", state)
			}
			resultCount := f.hook.results.Load()
			if stripMetadata {
				f.manager.SetResultPolicy(coreauth.ResultPolicyFunc(func(_ context.Context, result coreauth.Result) coreauth.Result {
					result.Options.Metadata = nil
					return result
				}))
			}
			response := finishXAIRoutingCheck(t, f, finished)
			after, _ := f.manager.GetByID(f.auth.ID)
			hold, held := prorouting.QuotaProtections(after.Metadata)[inspectionQuotaSource]
			if response.Code != http.StatusOK || after.ModelStates["grok-4.5"] == nil || !after.ModelStates["grok-4.5"].NextRetryAfter.Equal(state.NextRetryAfter) || f.hook.results.Load() != resultCount || !held || hold.Revision == 0 {
				t.Fatalf("old success changed newer failure, emitted result or released hold: response=%s state=%+v hold=%+v hooks=%d/%d", response.Body.String(), after.ModelStates["grok-4.5"], hold, f.hook.results.Load(), resultCount)
			}
		})
	}
}

func TestXAIRoutingRecoveryRejectsReplacementIdentity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		apiKey string
	}{
		{name: "new credential", apiKey: "replacement-token"},
		{name: "same credential new registration", apiKey: "test-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newXAIRoutingRecoveryFixture(t, http.StatusOK, `{"id":"chatcmpl-test","choices":[]}`, true)
			finished := f.startCheck(t)
			replacement, err := f.manager.Register(f.ctx, &coreauth.Auth{
				ID: f.auth.ID, FileName: f.auth.FileName, Provider: "xai",
				Attributes:  map[string]string{"api_key": tc.apiKey, "base_url": "https://api.x.ai/v1", "using_api": "true"},
				ModelStates: map[string]*coreauth.ModelState{"grok-4.5": {Unavailable: true, Status: coreauth.StatusError, NextRetryAfter: time.Now().Add(time.Hour)}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if replacement.RegistrationEpoch == f.auth.RegistrationEpoch {
				t.Fatal("fixture did not replace registration identity")
			}
			replacement, _ = f.manager.GetByID(f.auth.ID)
			existing := prorouting.QuotaProtections(replacement.Metadata)[inspectionQuotaSource]
			if existing.Revision == 0 {
				t.Fatal("replacement lost the test protection before the old probe returned")
			}
			newHold := prorouting.QuotaProtection{RetryAt: time.Now().Add(time.Hour).UnixMilli(), Reason: "replacement hold"}
			if err := f.manager.ChangeQuotaProtection(f.ctx, replacement, inspectionQuotaSource, existing.Revision, &newHold); err != nil {
				t.Fatal(err)
			}
			before, _ := f.manager.GetByID(f.auth.ID)
			count := f.hook.results.Load()
			response := finishXAIRoutingCheck(t, f, finished)
			after, _ := f.manager.GetByID(f.auth.ID)
			oldHold := prorouting.QuotaProtections(before.Metadata)[inspectionQuotaSource]
			remaining := prorouting.QuotaProtections(after.Metadata)[inspectionQuotaSource]
			if response.Code != http.StatusConflict || remaining.Revision != oldHold.Revision || !reflect.DeepEqual(after.ModelStates, before.ModelStates) || after.Generation != before.Generation || f.hook.results.Load() != count {
				t.Fatalf("old probe changed replacement: response=%s before=%+v after=%+v hooks=%d/%d", response.Body.String(), before, after, f.hook.results.Load(), count)
			}
		})
	}
}

func TestXAIRoutingRecoveryRetainsChangedHoldRevision(t *testing.T) {
	f := newXAIRoutingRecoveryFixture(t, http.StatusOK, `{"id":"chatcmpl-test","choices":[]}`, true)
	finished := f.startCheck(t)
	current, _ := f.manager.GetByID(f.auth.ID)
	previous := prorouting.QuotaProtections(current.Metadata)[inspectionQuotaSource]
	newHold := previous
	newHold.RetryAt = time.Now().Add(time.Hour).UnixMilli()
	if err := f.manager.ChangeQuotaProtection(f.ctx, current, inspectionQuotaSource, previous.Revision, &newHold); err != nil {
		t.Fatal(err)
	}
	current, _ = f.manager.GetByID(f.auth.ID)
	newHold = prorouting.QuotaProtections(current.Metadata)[inspectionQuotaSource]
	response := finishXAIRoutingCheck(t, f, finished)
	current, _ = f.manager.GetByID(f.auth.ID)
	remaining := prorouting.QuotaProtections(current.Metadata)[inspectionQuotaSource]
	if response.Code != http.StatusOK || remaining.Revision != newHold.Revision || remaining.RetryAt != newHold.RetryAt {
		t.Fatalf("old probe released newer hold: response=%s hold=%+v", response.Body.String(), remaining)
	}
}

func TestXAIRoutingRecoveryRetainsAccountHoldOnFailedProbe(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
	}{
		{name: "quota 429", status: http.StatusTooManyRequests},
		{name: "unsupported model 400", status: http.StatusBadRequest},
		{name: "unsupported model 404", status: http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newXAIRoutingRecoveryFixture(t, tc.status, `{"error":{"message":"model unavailable"}}`, false)
			response := f.check()
			current, _ := f.manager.GetByID(f.auth.ID)
			hold, exists := prorouting.QuotaProtections(current.Metadata)[inspectionQuotaSource]
			if response.Code != http.StatusOK || !exists || hold.RetryAt <= time.Now().UnixMilli() || hold.Failures == 0 || hold.Model != "" || !prorouting.ProtectionBlocks(hold, "grok-4.5", time.Now()) || !prorouting.ProtectionBlocks(hold, "grok-3", time.Now()) {
				t.Fatalf("failed probe narrowed account hold: response=%s hold=%+v", response.Body.String(), hold)
			}
			if current.Quota.Exceeded && current.Quota.Reason == "credential_quota" {
				t.Fatalf("model/request error escalated credential quota: %+v", current.Quota)
			}
		})
	}
}

func TestXAIRoutingRecoveryReleasesObservedHoldOnSuccess(t *testing.T) {
	f := newXAIRoutingRecoveryFixture(t, http.StatusOK, `{"id":"chatcmpl-test","choices":[]}`, false)
	response := f.check()
	current, _ := f.manager.GetByID(f.auth.ID)
	if response.Code != http.StatusOK || len(f.executor.requests) != 1 {
		t.Fatalf("official success did not complete: %s requests=%d", response.Body.String(), len(f.executor.requests))
	}
	if _, exists := prorouting.QuotaProtections(current.Metadata)[inspectionQuotaSource]; exists {
		t.Fatalf("successful probe did not release observed hold: %s", response.Body.String())
	}
	if current.Quota.Exceeded {
		t.Fatalf("successful probe changed native credential quota: %+v", current.Quota)
	}
}

func TestRoutingTargetedModelCheckPreservesOtherRestrictions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := startProQuotaTestService(t)
	manager := coreauth.NewManager(nil, nil, nil)
	executor := &authFileConnectionExecutor{response: []byte(`{"choices":[{"message":{"content":"OK"}}]}`)}
	manager.RegisterExecutor(executor)
	models := authFileConnectionTestModels(&coreauth.Auth{Provider: "codex"})
	if len(models) < 2 {
		t.Fatal("need two supported diagnostic models")
	}
	modelA, modelB := models[0].ID, models[1].ID
	now := time.Now()
	auth, err := manager.Register(ctx, &coreauth.Auth{ID: "targeted-model", Provider: "codex", ModelStates: map[string]*coreauth.ModelState{
		modelA: {Status: coreauth.StatusError, Unavailable: true, NextRetryAfter: now.Add(time.Hour), UpdatedAt: now},
		modelB: {Status: coreauth.StatusError, Unavailable: true, NextRetryAfter: now.Add(time.Hour), UpdatedAt: now},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ChangeQuotaProtection(ctx, auth, inspectionQuotaSource, 0, &prorouting.QuotaProtection{RetryAt: now.Add(time.Hour).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	auth, _ = manager.GetByID(auth.ID)
	hold := prorouting.QuotaProtections(auth.Metadata)[inspectionQuotaSource]
	h := &Handler{authManager: manager}
	router := gin.New()
	h.RegisterRoutingPolicyRoutes(router.Group("/"))
	body, _ := json.Marshal(routingRecoveryRequest{AuthID: auth.ID, AuthIndex: auth.EnsureIndex(), RegistrationEpoch: strconv.FormatUint(auth.RegistrationEpoch, 10), Source: "upstream", Model: modelB, Revision: strconv.FormatInt(now.UnixNano(), 10)})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/routing-policy/check", strings.NewReader(string(body))))
	current, _ := manager.GetByID(auth.ID)
	if recorder.Code != 200 || executor.lastModel != modelB || current.ModelStates[modelB].Unavailable || !current.ModelStates[modelA].Unavailable || prorouting.QuotaProtections(current.Metadata)[inspectionQuotaSource].Revision != hold.Revision {
		t.Fatalf("targeted result=%s model=%s", recorder.Body.String(), executor.lastModel)
	}
}
