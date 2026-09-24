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
	if got := AutoActionForResult(results[0], settings); got != ActionNone {
		t.Fatalf("401 auto action = %q, want none", got)
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
