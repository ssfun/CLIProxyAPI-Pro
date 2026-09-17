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
	if !ProtectionBlocks(probe, "gpt-test", now) {
		t.Fatal("due probe-request must remain blocked until its single probe completes")
	}
}

func TestScheduleRecheckAtPrefersResetOverBackoff(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	reset := now.Add(2 * time.Hour).UnixMilli()
	got := ScheduleRecheckAt(reset, "auth-a", now)
	if got < reset || got > reset+15000 {
		t.Fatalf("recheck at = %d, reset = %d", got, reset)
	}
	unknown := NextRecheckAt(0, "auth-a", 3, now)
	minDelay := now.Add(8 * time.Minute).UnixMilli()
	maxDelay := now.Add(8*time.Minute + 15*time.Second).UnixMilli()
	if unknown < minDelay || unknown > maxDelay {
		t.Fatalf("unknown reset backoff = %d", unknown)
	}
}

func TestModelMatchesProtectionAcceptsPatterns(t *testing.T) {
	if !ModelMatchesProtection("claude-opus-*,claude-sonnet-*", "claude-opus-4-6") {
		t.Fatal("opus pattern should match")
	}
	if ModelMatchesProtection("claude-opus-*", "claude-sonnet-4-6") {
		t.Fatal("unrelated model matched")
	}
}
