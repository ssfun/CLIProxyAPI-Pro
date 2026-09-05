package settings

import (
	"context"
	"encoding/json"
)

const (
	SchemaVersionOne = 1

	NamespaceRoutingRequestProtection = "routing.request-protection"
	NamespaceProxyPool                = "proxy.pool"
	NamespaceOAuthPolicy              = "oauth-policy"
	LegacyNamespaceOAuthModelPolicy   = "model.oauth-policy"
)

// Item is the module-facing representation of one versioned Pro setting.
// Keeping it outside embeddedusage prevents business modules from depending
// on the current SQLite implementation.
type Item struct {
	Namespace     string
	SchemaVersion int
	Settings      json.RawMessage
	UpdatedAtMS   int64
}

// PlanSnapshot carries quota.PlanEvidence from persistence to account policy.
// Data contains business evidence, never card rendering state.
type PlanSnapshot struct {
	Data         []byte
	ObservedAtMS int64
}

// Store is the persistence port consumed by static Pro business modules.
type Store interface {
	Get(context.Context, string) (Item, bool, error)
	Put(context.Context, Item) error
	Delete(context.Context, string) error
	Subscribe(string, func(context.Context, Item) error) func()
}

// WriteCoordinator keeps a persisted setting and its in-memory application
// inside the same backup write barrier. The Store passed to operation performs
// uncoordinated writes because the outer barrier is already held.
type WriteCoordinator interface {
	ExecuteWrite(context.Context, func(context.Context, Store) error) error
}
