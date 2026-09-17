package routing

import (
	"testing"
	"time"
)

func TestWithoutLegacyRoutingQuotaProtectionsDropsRetiredSources(t *testing.T) {
	protections := map[string]QuotaProtection{
		"inspection":       {Source: "inspection", Recheck: true},
		"routing:gpt-test": {Source: "routing:gpt-test", Model: "gpt-test", RetryAt: time.Now().Add(time.Minute).UnixMilli()},
	}
	got, dropped := WithoutLegacyRoutingQuotaProtections(protections)
	if !dropped || len(got) != 1 || got["inspection"].Source != "inspection" {
		t.Fatalf("got=%#v dropped=%v", got, dropped)
	}
}

func TestProtectionBlocksDueRecheckUntilEvidence(t *testing.T) {
	now := time.Now()
	hold := QuotaProtection{Recheck: true, RetryAt: now.Add(-time.Minute).UnixMilli()}
	if !ProtectionBlocks(hold, "gpt-test", now) {
		t.Fatal("due recheck must keep blocking until quota evidence recovers")
	}
	probe := QuotaProtection{RetryAt: now.Add(-time.Minute).UnixMilli()}
	if ProtectionBlocks(probe, "gpt-test", now) {
		t.Fatal("expired probe-request hold must return to the pool")
	}
}
