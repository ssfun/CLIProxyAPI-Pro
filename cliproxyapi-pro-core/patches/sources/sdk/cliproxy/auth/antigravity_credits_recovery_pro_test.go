package auth

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

type creditsRecoveryKV struct {
	raw         []byte
	replacement []byte
	found       bool
	err         error
	casErr      error
	casCalls    int
}

func (c *creditsRecoveryKV) KVGet(context.Context, string) ([]byte, bool, error) {
	return c.raw, c.found, c.err
}
func (c *creditsRecoveryKV) KVCompareAndSwap(_ context.Context, _ string, old []byte, exists bool, next []byte, ttl time.Duration) (bool, error) {
	c.casCalls++
	if c.casErr != nil {
		return false, c.casErr
	}
	if c.err != nil {
		return false, c.err
	}
	if c.replacement != nil {
		c.raw = c.replacement
	}
	if !exists || !c.found || string(c.raw) != string(old) {
		return false, nil
	}
	if ttl != 30*time.Minute {
		return false, errors.New("wrong TTL")
	}
	c.raw = next
	return true, nil
}
func TestProCreditsRecoveryHomeCompareAndSwap(t *testing.T) {
	old := AntigravityCreditsHint{Known: true, MinCreditAmount: 50, UpdatedAt: time.Now().UTC()}
	newer := old
	newer.Available = true
	newer.CreditAmount = 2516
	newer.UpdatedAt = newer.UpdatedAt.Add(time.Second)
	oldRaw, _ := json.Marshal(old)
	newerRaw, _ := json.Marshal(newer)
	for _, tc := range []struct {
		name                 string
		initial, replacement []byte
		found                bool
		err, casErr          error
		want                 bool
		calls                int
	}{
		{name: "unchanged", initial: oldRaw, found: true, want: true, calls: 1},
		{name: "updated_before_read", initial: newerRaw, found: true},
		{name: "updated_before_cas", initial: oldRaw, replacement: newerRaw, found: true, calls: 1},
		{name: "cas_unsupported", initial: oldRaw, found: true, casErr: errors.New("CAS unsupported"), calls: 1},
		{name: "missing"},
		{name: "malformed", initial: []byte("{"), found: true},
		{name: "unavailable", initial: oldRaw, found: true, err: errors.New("unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &creditsRecoveryKV{raw: tc.initial, replacement: tc.replacement, found: tc.found, err: tc.err, casErr: tc.casErr}
			got, _ := compareAndSwapHomeAntigravityCreditsHint(context.Background(), client, "auth", old, AntigravityCreditsHint{UpdatedAt: time.Now()})
			if got != tc.want || client.casCalls != tc.calls {
				t.Fatalf("swapped=%v calls=%d; want %v %d", got, client.casCalls, tc.want, tc.calls)
			}
			if !got {
				want := tc.initial
				if tc.replacement != nil {
					want = tc.replacement
				}
				if string(client.raw) != string(want) {
					t.Fatalf("failed swap mutated stored value: %s", client.raw)
				}
			}
		})
	}
}
func TestProCreditsRecoveryLocalCompareAndSwap(t *testing.T) {
	const id = "pro-credits-cas"
	t.Cleanup(func() { antigravityCreditsHintByAuth.Delete(id) })
	old := AntigravityCreditsHint{Known: true, UpdatedAt: time.Now()}
	SetAntigravityCreditsHint(id, old)
	newer := old
	newer.Available = true
	newer.CreditAmount = 2516
	SetAntigravityCreditsHint(id, newer)
	if CompareAndSwapAntigravityCreditsHint(id, old, AntigravityCreditsHint{}) {
		t.Fatal("stale observation replaced new balance")
	}
	if got, _ := GetAntigravityCreditsHint(id); got != newer {
		t.Fatalf("new balance changed: %+v", got)
	}
	if !CompareAndSwapAntigravityCreditsHint(id, newer, AntigravityCreditsHint{}) {
		t.Fatal("unchanged observation not cleared")
	}
	if got, _ := GetAntigravityCreditsHint(id); got.Known || got.UpdatedAt.IsZero() {
		t.Fatalf("invalid cleared hint: %+v", got)
	}
	if CompareAndSwapAntigravityCreditsHint(" ", old, AntigravityCreditsHint{}) {
		t.Fatal("blank id accepted")
	}
}
