package inspection

import (
	"strings"
	"sync"
)

// ConfirmationCounter owns consecutive auto-action confirmation state.
type ConfirmationCounter struct {
	mu       sync.Mutex
	sequence int64
	entries  map[string]ConfirmationEntry
}

type ConfirmationEntry struct {
	Count           int    `json:"count"`
	LastSequence    int64  `json:"lastSequence"`
	LastFingerprint string `json:"lastFingerprint,omitempty"`
}

type ConfirmationState struct {
	Sequence int64                        `json:"sequence"`
	Entries  map[string]ConfirmationEntry `json:"entries"`
}

func NewConfirmationCounter() *ConfirmationCounter {
	return &ConfirmationCounter{sequence: 1, entries: make(map[string]ConfirmationEntry)}
}

func (c *ConfirmationCounter) BeginRun() int64 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	c.sequence++
	sequence := c.sequence
	c.mu.Unlock()
	return sequence
}

func (c *ConfirmationCounter) Confirm(key string, required int) (bool, int, int) {
	return c.ConfirmWithFingerprint(key, "", "", required)
}

func (c *ConfirmationCounter) ConfirmWithFingerprint(key, observedFingerprint, finalFingerprint string, required int) (bool, int, int) {
	if required <= 1 {
		return true, 1, 1
	}
	key = strings.TrimSpace(key)
	if key == "" || c == nil {
		return true, 1, required
	}
	c.mu.Lock()
	if c.entries == nil {
		c.entries = make(map[string]ConfirmationEntry)
	}
	observedFingerprint = strings.TrimSpace(observedFingerprint)
	finalFingerprint = strings.TrimSpace(finalFingerprint)
	if finalFingerprint == "" {
		finalFingerprint = observedFingerprint
	}
	entry := c.entries[key]
	if entry.LastSequence == c.sequence {
		if observedFingerprint != "" && entry.LastFingerprint != observedFingerprint {
			c.entries[key] = ConfirmationEntry{Count: 1, LastSequence: c.sequence, LastFingerprint: finalFingerprint}
			c.mu.Unlock()
			return false, 1, required
		}
		count := entry.Count
		c.mu.Unlock()
		return count >= required, count, required
	}

	consecutive := entry.LastSequence == c.sequence-1
	// A normal inspection refresh forms a chain where the previous run's final
	// credential fingerprint is the next run's observed fingerprint. A credential
	// replacement between runs breaks that chain and must restart destructive-action
	// confirmation from 1. Legacy persisted entries without a fingerprint are also
	// reset once when fingerprint-aware confirmation is first used.
	if consecutive && observedFingerprint != "" && entry.LastFingerprint != observedFingerprint {
		consecutive = false
	}
	count := 1
	if consecutive {
		count = entry.Count + 1
	}
	c.entries[key] = ConfirmationEntry{Count: count, LastSequence: c.sequence, LastFingerprint: finalFingerprint}
	c.mu.Unlock()
	return count >= required, count, required
}

func (c *ConfirmationCounter) ClearPrefix(prefix string) {
	if c == nil || strings.TrimSpace(prefix) == "" {
		return
	}
	c.mu.Lock()
	for key := range c.entries {
		if strings.HasPrefix(key, prefix) {
			delete(c.entries, key)
		}
	}
	c.mu.Unlock()
}

func (c *ConfirmationCounter) Reset() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.sequence = 1
	c.entries = make(map[string]ConfirmationEntry)
	c.mu.Unlock()
}

func (c *ConfirmationCounter) State() ConfirmationState {
	if c == nil {
		return ConfirmationState{Entries: map[string]ConfirmationEntry{}}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entries := make(map[string]ConfirmationEntry, len(c.entries))
	for key, entry := range c.entries {
		entries[key] = entry
	}
	return ConfirmationState{Sequence: c.sequence, Entries: entries}
}

func (c *ConfirmationCounter) Restore(state ConfirmationState) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.sequence = state.Sequence
	c.entries = make(map[string]ConfirmationEntry, len(state.Entries))
	for key, entry := range state.Entries {
		if strings.TrimSpace(key) != "" && entry.Count > 0 && entry.LastSequence > 0 {
			c.entries[key] = entry
		}
	}
	c.mu.Unlock()
}
