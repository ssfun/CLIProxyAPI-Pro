package pool

import (
	"errors"
	"testing"
	"time"

	proxyconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/proxypool/config"
)

func testConfig(strategy string) proxyconfig.Config {
	return proxyconfig.Config{
		Strategy: strategy,
		Nodes: []proxyconfig.NodeConfig{
			{ID: "a", URL: "http://user:secret@127.0.0.1:19001", Enabled: true, Weight: 2, Order: 10},
			{ID: "b", URL: "http://127.0.0.1:19002", Enabled: true, Weight: 1, Order: 20},
		},
	}
}

func TestRoundRobinStartsAtFirstNode(t *testing.T) {
	p := New(testConfig("round-robin"))
	want := []string{"a", "b", "a", "b"}
	for index, expected := range want {
		selected := p.Select(nil)
		if selected == nil || selected.ID() != expected {
			t.Fatalf("Select() #%d = %#v, want %s", index, selected, expected)
		}
	}
}

func TestWeightedUsesSmoothDistribution(t *testing.T) {
	p := New(testConfig("weighted"))
	counts := map[string]int{}
	for range 6 {
		counts[p.Select(nil).ID()]++
	}
	if counts["a"] != 4 || counts["b"] != 2 {
		t.Fatalf("weighted counts = %+v, want a:4 b:2", counts)
	}
}

func TestIsolationRemovesNodeUntilExpiry(t *testing.T) {
	p := New(testConfig("round-robin"))
	node := p.Node("a")
	node.MarkAttempt()
	node.MarkFailure(errors.New("dial failed"), 1, 50*time.Millisecond)
	if selected := p.Select(nil); selected == nil || selected.ID() != "b" {
		t.Fatalf("Select() while a isolated = %#v, want b", selected)
	}
	time.Sleep(60 * time.Millisecond)
	foundA := false
	for range 2 {
		if selected := p.Select(nil); selected != nil && selected.ID() == "a" {
			foundA = true
		}
	}
	if !foundA {
		t.Fatal("expired isolated node did not become eligible")
	}
	snapshot := node.Snapshot()
	if snapshot.State != HealthUnknown || !snapshot.IsolationUntil.IsZero() {
		t.Fatalf("expired isolation remained visible in snapshot: %+v", snapshot)
	}
}

func TestEligibleCountMatchesSelectionRules(t *testing.T) {
	cfg := testConfig("round-robin")
	cfg.Nodes = append(cfg.Nodes,
		proxyconfig.NodeConfig{ID: "disabled", URL: "http://127.0.0.1:19003", Enabled: false, Weight: 1, Order: 30},
	)
	p := New(cfg)
	now := time.Now()

	// Unknown and degraded nodes are both selectable until actively isolated.
	p.Node("b").mu.Lock()
	p.Node("b").state = HealthDegraded
	p.Node("b").mu.Unlock()
	if got := p.EligibleCount(now); got != 2 {
		t.Fatalf("EligibleCount() = %d, want 2", got)
	}

	p.Node("a").MarkAttempt()
	p.Node("a").MarkFailure(errors.New("dial failed"), 1, time.Hour)
	if got := p.EligibleCount(now); got != 1 {
		t.Fatalf("EligibleCount() with active isolation = %d, want 1", got)
	}
	if selected := p.Select(nil); selected == nil || selected.ID() != "b" {
		t.Fatalf("Select() with active isolation = %#v, want b", selected)
	}

	if got := p.EligibleCount(now.Add(2 * time.Hour)); got != 2 {
		t.Fatalf("EligibleCount() after isolation expiry = %d, want 2", got)
	}
}

func TestRecoverClearsIsolationAndPreservesStats(t *testing.T) {
	p := New(testConfig("round-robin"))
	node := p.Node("a")
	node.MarkAttempt()
	node.MarkFailure(errors.New("dial failed"), 1, time.Hour)
	if !p.Recover("a") {
		t.Fatal("Recover() = false, want true")
	}
	snapshot := node.Snapshot()
	if snapshot.State != HealthUnknown || snapshot.ConsecutiveFailures != 0 || !snapshot.IsolationUntil.IsZero() {
		t.Fatalf("snapshot after recovery = %+v", snapshot)
	}
	if snapshot.TotalConnects != 1 || snapshot.FailedConnects != 1 {
		t.Fatalf("recovery discarded counters: %+v", snapshot)
	}
	if p.Recover("missing") {
		t.Fatal("Recover(missing) = true, want false")
	}
}

func TestSnapshotKeepsConnectionCountersConsistentAfterResetRace(t *testing.T) {
	node := New(testConfig("round-robin")).Node("a")
	node.totalConnects.Store(2)
	node.successConnects.Store(3)
	node.failedConnects.Store(1)

	snapshot := node.Snapshot()
	if snapshot.TotalConnects != 4 {
		t.Fatalf("TotalConnects = %d, want completed connection floor 4", snapshot.TotalConnects)
	}
	if snapshot.SuccessConnects > snapshot.TotalConnects {
		t.Fatalf("inconsistent snapshot: %+v", snapshot)
	}
}

func TestMarkProbeDoesNotChangeTunnelCounters(t *testing.T) {
	node := New(testConfig("round-robin")).Node("a")
	node.MarkProbe(25*time.Millisecond, nil, 3, time.Minute)
	snapshot := node.Snapshot()
	if snapshot.State != HealthHealthy || snapshot.LastCheck.IsZero() || snapshot.LastSuccess.IsZero() {
		t.Fatalf("probe did not update health: %+v", snapshot)
	}
	if snapshot.TotalConnects != 0 || snapshot.SuccessConnects != 0 || snapshot.FailedConnects != 0 {
		t.Fatalf("probe changed tunnel counters: %+v", snapshot)
	}
}

func TestRedactURL(t *testing.T) {
	got := RedactURL("socks5://alice:secret@proxy.example:1080")
	if got != "socks5://alice:***@proxy.example:1080" {
		t.Fatalf("RedactURL() = %q", got)
	}
}
