package responses

import (
	"testing"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func TestConvertOpenAIResponsesRequestToCodexNormalizesServiceTier(t *testing.T) {
	tests := []struct {
		name        string
		serviceTier any
		want        string
	}{
		{name: "fast alias", serviceTier: "fast", want: "priority"},
		{name: "priority", serviceTier: "priority", want: "priority"},
		{name: "normalized priority", serviceTier: " PRIORITY ", want: "priority"},
		{name: "ultrafast", serviceTier: "ultrafast", want: "ultrafast"},
		{name: "normalized ultrafast", serviceTier: " ULTRAFAST ", want: "ultrafast"},
		{name: "unsupported tier", serviceTier: "default"},
		{name: "non-string tier", serviceTier: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := []byte(`{"model":"gpt-5.6","input":"hello"}`)
			var err error
			input, err = sjson.SetBytes(input, "service_tier", tt.serviceTier)
			if err != nil {
				t.Fatal(err)
			}
			output := ConvertOpenAIResponsesRequestToCodex("gpt-5.6", input, true)
			serviceTier := gjson.GetBytes(output, "service_tier")
			if got := serviceTier.String(); got != tt.want {
				t.Fatalf("service_tier = %q, want %q: %s", got, tt.want, output)
			}
			if tt.want == "" && serviceTier.Exists() {
				t.Fatalf("unsupported service_tier was forwarded: %s", output)
			}
		})
	}
}
