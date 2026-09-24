package inspection

import "testing"

func TestDedupeActionItems(t *testing.T) {
	items := []ActionItem{
		{FileName: "a.json", AuthIndex: "one", Action: ActionDisable},
		{FileName: "a.json", AuthIndex: "one", Action: ActionDelete},
		{FileName: "b.json", Action: ActionKeep},
		{FileName: "none.json", Action: ActionNone},
		{FileName: "c.json", Action: ActionDelete},
	}
	got := DedupeActionItems(items)
	if len(got) != 2 || got[0].Key != "a.json::one" || got[1].Key != "c.json::-" {
		t.Fatalf("items = %#v", got)
	}
}

func TestActionItemResultRoundTrip(t *testing.T) {
	result := Result{AuthID: "auth-id", AccessTokenSHA256: "token-sha256", Key: "key", Provider: "xai", FileName: "xai.json", AuthIndex: "auth", Disabled: true}
	item := ActionItemFromResult(result, ActionEnable)
	roundTrip := item.ToResult()
	if roundTrip.AuthID != result.AuthID || roundTrip.AccessTokenSHA256 != result.AccessTokenSHA256 || roundTrip.Key != result.Key || roundTrip.Provider != result.Provider || roundTrip.Action != ActionEnable || !roundTrip.Disabled {
		t.Fatalf("round trip = %#v", roundTrip)
	}
}

func TestSummarizeActionOutcomes(t *testing.T) {
	summary := SummarizeActionOutcomes([]ActionOutcome{{Success: true}, {Success: false}, {Success: true}})
	if summary.Total != 3 || summary.Success != 2 || summary.Failed != 1 {
		t.Fatalf("summary = %#v", summary)
	}
}

func TestMergeManualActionResult(t *testing.T) {
	current := Result{Key: "key", Action: ActionDisable, ActionReason: "disable", Error: "old"}
	executed := Result{Key: "key", Provider: "xai", Disabled: true, Executed: true, Action: ActionDisable}
	merged, updateSummary := MergeManualActionResult(current, executed)
	if !updateSummary || merged.Action != ActionDisable || merged.ActionReason != "disable" || merged.Error != "old" || !merged.Disabled || !merged.Executed || merged.OperationAction != ActionDisable {
		t.Fatalf("merged = %#v update=%v", merged, updateSummary)
	}
}
