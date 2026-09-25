package inspection

import "testing"

func intPointer(value int) *int { return &value }

func TestResultSemanticsClassifiesQuotaAndErrors(t *testing.T) {
	quota := NormalizeResultSemantics(Result{
		Provider:  "antigravity",
		Error:     `{"error":{"status":"RESOURCE_EXHAUSTED","details":[{"reason":"QUOTA_EXHAUSTED"}]}}`,
		ErrorCode: "inspection_http_error",
	})
	if !quota.IsQuota || quota.ErrorCode != "" || HealthBucketOf(quota) != HealthQuotaExhausted {
		t.Fatalf("quota result = %+v", quota)
	}

	authInvalid := Result{Provider: "codex", StatusCode: intPointer(401), ErrorCode: "inspection_http_error"}
	if !IsAccountInvalidResult(authInvalid) || HealthBucketOf(authInvalid) != HealthAuthInvalid {
		t.Fatalf("auth-invalid result = %+v", authInvalid)
	}

	requestError := Result{Provider: "codex", ErrorCode: "inspection_probe_error", Error: "network error"}
	if !IsRequestErrorResult(requestError) || HealthBucketOf(requestError) != HealthInspectionError {
		t.Fatalf("request-error result = %+v", requestError)
	}

	incomplete := Result{Provider: "codex", ErrorCode: "inspection_incomplete"}
	if HealthBucketOf(incomplete) != HealthInspectionError {
		t.Fatalf("incomplete result bucket = %q, want %q", HealthBucketOf(incomplete), HealthInspectionError)
	}
	if !ResultMatchesFilter(incomplete, "requestError") || !ResultMatchesFilter(incomplete, "accountIssues") {
		t.Fatal("incomplete result should be included in inspection-error filters")
	}
}

func TestResultPaginationFiltersAndCopies(t *testing.T) {
	results := []Result{
		{Key: "healthy", Provider: "codex", Action: ActionKeep, Email: "healthy@example.com"},
		{Key: "quota", Provider: "xai", Action: ActionDisable, IsQuota: true, Email: "quota@example.com"},
		{Key: "pending", Provider: "codex", Action: ActionDelete, Email: "pending@example.com"},
	}
	page, info := PaginateResults(results, 1, 20, 500, "pending", true, "codex", "pending")
	if len(page) != 1 || page[0].Key != "pending" || info.Total != 1 || info.PageSize != 20 {
		t.Fatalf("page/info = %+v / %+v", page, info)
	}
	page[0].Key = "changed"
	if results[2].Key != "pending" {
		t.Fatal("pagination returned a slice sharing result storage")
	}
}

func TestHealthSummaryAndAutomaticActions(t *testing.T) {
	results := []Result{
		{Key: "delete", Action: ActionDelete, StatusCode: intPointer(401), ErrorCode: "inspection_http_error", Executed: true},
		{Key: "quota", Action: ActionDisable, IsQuota: true},
		{Key: "recover", Action: ActionEnable, Disabled: true},
		{Key: "healthy", Action: ActionKeep},
	}
	counts := ResultHealthCounts(results)
	if counts.Total != 4 || counts.AuthInvalid != 1 || counts.QuotaExhausted != 1 || counts.Recoverable != 1 || counts.Healthy != 1 {
		t.Fatalf("health counts = %+v", counts)
	}
	summary := SummarizeResults(6, 5, 2, 3, results)
	if summary.SampledCount != 4 || summary.DeleteCount != 1 || summary.DisableCount != 1 || summary.EnableCount != 1 || summary.KeepCount != 1 || summary.ExecutedDeleteCount != 1 {
		t.Fatalf("summary = %+v", summary)
	}
	settings := DefaultSettings()
	settings.AutoExecuteAccountInvalidAction = ActionDelete
	settings.AutoExecuteQuotaLimitDisable = true
	if got := AutoActionForResult(results[0], settings); got != ActionDelete {
		t.Fatalf("401 auto action = %q, want %q", got, ActionDelete)
	}
	if got := AutoActionForResult(results[1], settings); got != ActionDisable {
		t.Fatalf("quota action = %q", got)
	}
}

func TestResultMatchesSearchIncludesAuthID(t *testing.T) {
	result := Result{Key: "file.json::-", FileName: "", AuthIndex: "-", AuthID: "plugin-auth-id"}
	if !ResultMatchesSearch(result, "plugin-auth-id") {
		t.Fatal("auth id should match inspection search")
	}
	if ResultMatchesSearch(result, "missing") {
		t.Fatal("unrelated query matched")
	}
}

// Failure matrix: only inspection-confirmed HTTP 401/403 may inherit the
// account-invalid policy. HTTP 400/404 and every other transient/provider/
// parser failure must remain non-destructive; stale suggestions must not bypass
// this boundary, while confirmed quota retains its independent policy.
func TestAutomaticMutationRequiresAccountEvidence(t *testing.T) {
	cases := []struct {
		name   string
		result Result
	}{
		{"rate limit", Result{ErrorCode: "inspection_rate_limited", StatusCode: intPointer(429)}},
		{"rate limit stale action", Result{ErrorCode: "inspection_rate_limited", Action: ActionDelete}},
		{"rate limit stale quota", Result{ErrorCode: "inspection_rate_limited", Action: ActionDisable, IsQuota: true}},
		{"incomplete", Result{ErrorCode: "inspection_incomplete"}},
		{"parser", Result{ErrorCode: "inspection_parser_error"}},
		{"network", Result{ErrorCode: "inspection_probe_error", Error: "network timeout"}},
		{"transport", Result{ErrorCode: "inspection_transport_error"}},
		{"provider", Result{ErrorCode: "inspection_provider_error", StatusCode: intPointer(503)}},
		{"legacy provider", Result{ErrorCode: "inspection_probe_error", StatusCode: intPointer(502)}},
		{"unauthorized without confirmed HTTP evidence", Result{ErrorCode: "inspection_probe_error", StatusCode: intPointer(401)}},
		{"forbidden transient deep probe", Result{StatusCode: intPointer(403), DeepProbeStatus: string(DeepProbeTransientError)}},
		{"bad request", Result{ErrorCode: "inspection_http_error", StatusCode: intPointer(400)}},
		{"not found", Result{ErrorCode: "inspection_http_error", StatusCode: intPointer(404)}},
		{"server error", Result{ErrorCode: "inspection_http_error", StatusCode: intPointer(500)}},
		{"deep transient", Result{DeepProbeStatus: string(DeepProbeTransientError)}},
		{"refresh", Result{ErrorCode: "token_refresh_error", TokenRefreshStatus: "failed"}},
	}
	for _, action := range []Action{ActionDisable, ActionDelete} {
		settings := DefaultSettings()
		settings.AutoExecuteRequestErrorAction = action
		settings.AutoExecuteAccountInvalidAction = action
		settings.AutoExecuteQuotaLimitDisable = true
		for _, tc := range cases {
			t.Run(string(action)+"/"+tc.name, func(t *testing.T) {
				if got := AutoActionForResult(tc.result, settings); got != ActionNone {
					t.Fatalf("auto action = %q, want none", got)
				}
			})
		}
		for _, status := range []int{401, 403} {
			if got := AutoActionForResult(Result{ErrorCode: "inspection_http_error", StatusCode: intPointer(status)}, settings); got != action {
				t.Fatalf("%d auto action = %q, want %q", status, got, action)
			}
		}
	}
}
