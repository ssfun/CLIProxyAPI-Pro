package responses

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestConvertOpenAIResponsesRequestToGeminiCompactionTriggerEndsWithUser(t *testing.T) {
	input := []byte(`{
		"model":"gemini-3.7-flash-high",
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"start"}]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]},
			{"type":"reasoning","id":"rs_resp_test_detached_after_1","summary":[],"content":null,"encrypted_content":"cpa-gemini-responses-carrier-v1:previous:text:opaque"},
			{"type":"compaction_trigger"}
		]
	}`)

	output := ConvertOpenAIResponsesRequestToGemini("gemini-3.7-flash-high", input, false)
	contents := gjson.GetBytes(output, "contents").Array()
	if len(contents) != 3 {
		t.Fatalf("contents length = %d, want 3; output=%s", len(contents), output)
	}
	if got := contents[len(contents)-1].Get("role").String(); got != "user" {
		t.Fatalf("final content role = %q, want user; output=%s", got, output)
	}
	if got := contents[len(contents)-1].Get("parts.0.text").String(); got != "" {
		t.Fatalf("synthetic user text = %q, want empty; output=%s", got, output)
	}
}

func TestConvertOpenAIResponsesRequestToGeminiDoesNotAppendUserWithoutCompaction(t *testing.T) {
	input := []byte(`{"input":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]}]}`)
	output := ConvertOpenAIResponsesRequestToGemini("gemini-3.7-flash-high", input, false)
	contents := gjson.GetBytes(output, "contents").Array()
	if len(contents) != 1 || contents[0].Get("role").String() != "model" {
		t.Fatalf("non-compaction contents changed unexpectedly: %s", output)
	}
}
