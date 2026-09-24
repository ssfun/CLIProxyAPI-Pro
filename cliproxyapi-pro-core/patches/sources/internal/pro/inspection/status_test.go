package inspection

import "testing"

func TestProjectStatusOmitsOrPagesDetails(t *testing.T) {
	status := Status{
		Results: []Result{{Key: "healthy", Provider: "codex"}, {Key: "quota", Provider: "xai", IsQuota: true}},
		Logs:    []LogEntry{{Time: 1, Level: "info"}, {Time: 2, Level: "warning"}, {Time: 3, Level: "info"}},
	}
	light := ProjectStatus(status, ResultHealthCounts(status.Results), SnapshotOptions{}, 500, 500)
	if light.Results != nil || light.Logs != nil || light.HealthCounts != nil || light.ProviderHealthCounts != nil {
		t.Fatalf("light status leaked details: %#v", light)
	}

	detailed := ProjectStatus(status, ResultHealthCounts(status.Results), SnapshotOptions{
		IncludeDetails: true,
		ResultPage:     1,
		ResultPageSize: 1,
		LogPage:        1,
		LogPageSize:    2,
		LogLevel:       "info",
	}, 500, 500)
	if detailed.HealthCounts == nil || detailed.HealthCounts.Total != 2 || detailed.ProviderHealthCounts["xai"].QuotaExhausted != 1 {
		t.Fatalf("detailed health = %#v / %#v", detailed.HealthCounts, detailed.ProviderHealthCounts)
	}
	if len(detailed.Results) != 1 || detailed.ResultsPage == nil || detailed.ResultsPage.Total != 2 || !detailed.ResultsLimited {
		t.Fatalf("result page = %#v / %#v", detailed.Results, detailed.ResultsPage)
	}
	if len(detailed.Logs) != 2 || detailed.Logs[0].Time != 1 || detailed.Logs[1].Time != 3 {
		t.Fatalf("log page = %#v", detailed.Logs)
	}
}

func TestMergeTokenRefreshResultTransitionsErrors(t *testing.T) {
	current := Result{Key: "account", Provider: "codex"}
	failed := current
	failed.TokenRefreshTriggered = true
	failed.TokenRefreshStatus = "failed"
	failed.TokenRefreshError = "refresh failed"
	failed.Error = "refresh failed"
	failed.ErrorDetail = "raw failure"
	failed.ErrorCode = "token_refresh_error"
	failed.ActionReason = "keep"
	merged, updateSummary := MergeTokenRefreshResult(current, failed)
	if !updateSummary || merged.ErrorCode != "token_refresh_error" || merged.TokenRefreshStatus != "failed" {
		t.Fatalf("failed merge = %#v update=%v", merged, updateSummary)
	}

	success := merged
	success.TokenRefreshStatus = "success"
	success.TokenRefreshError = ""
	merged, updateSummary = MergeTokenRefreshResult(merged, success)
	if !updateSummary || merged.Error != "" || merged.ErrorDetail != "" || merged.ErrorCode != "" {
		t.Fatalf("success merge = %#v update=%v", merged, updateSummary)
	}
}

func TestMergeReinspectionResultStartsNewObservation(t *testing.T) {
	current := Result{Key: "account", Executed: true, ExecuteError: "previous"}
	incoming := Result{Key: "account", Provider: "xai"}
	merged, updateSummary := MergeReinspectionResult(current, incoming)
	if !updateSummary || merged.Executed || merged.ExecuteError != "" || merged.Provider != "xai" {
		t.Fatalf("merged = %#v update=%v", merged, updateSummary)
	}
}
