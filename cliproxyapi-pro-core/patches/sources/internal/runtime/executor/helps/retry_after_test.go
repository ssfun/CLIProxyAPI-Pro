package helps

import (
	"net/http"
	"testing"
	"time"
)

func TestParseRetryAfterHeader(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	futureHTTPDate := now.Add(45 * time.Second).UTC().Format(http.TimeFormat)
	futureRFC3339 := now.Add(90 * time.Second).Format(time.RFC3339)

	tests := []struct {
		name string
		raw  string
		want time.Duration
	}{
		{name: "seconds", raw: "12", want: 12 * time.Second},
		{name: "http date", raw: futureHTTPDate, want: 45 * time.Second},
		{name: "rfc3339", raw: futureRFC3339, want: 90 * time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseRetryAfterHeader(tc.raw, now)
			if got == nil || *got != tc.want {
				t.Fatalf("ParseRetryAfterHeader(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}

	for _, raw := range []string{"", "0", "invalid", now.Add(-time.Second).UTC().Format(http.TimeFormat)} {
		if got := ParseRetryAfterHeader(raw, now); got != nil {
			t.Fatalf("ParseRetryAfterHeader(%q) = %v, want nil", raw, *got)
		}
	}
}
