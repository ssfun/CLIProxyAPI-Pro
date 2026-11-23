package auth

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	homekv "github.com/router-for-me/CLIProxyAPI/v8/internal/home"
	log "github.com/sirupsen/logrus"
)

// CompareAndSwapAntigravityCreditsHintForAuth invalidates an observed failure without
// replacing a balance published by a concurrent refresh.
func CompareAndSwapAntigravityCreditsHintForAuth(auth *Auth, old, next AntigravityCreditsHint) bool {
	unlock, current := lockAntigravityCreditsHintScope(auth)
	if !current {
		return false
	}
	defer unlock()
	authID := strings.TrimSpace(auth.ID)
	scope := AntigravityCreditsScope(auth)
	if next.UpdatedAt.IsZero() {
		next.UpdatedAt = time.Now()
	}
	if client, homeMode, err := homekv.CurrentKVClient(); homeMode {
		if err != nil {
			log.Errorf("antigravity credits: conditional hint update: %v", err)
			return false
		}
		swapped, err := compareAndSwapHomeAntigravityCreditsHint(context.Background(), client, antigravityCreditsHintKey(authID, scope), old, next)
		if err != nil {
			log.Errorf("antigravity credits: conditional hint update: %v", err)
		}
		return swapped
	}
	return antigravityCreditsHintByAuth.CompareAndSwap(newAntigravityCreditsHintCacheScope(authID, scope), old, next)
}

type antigravityCreditsHintCASClient interface {
	KVGet(context.Context, string) ([]byte, bool, error)
	KVCompareAndSwap(context.Context, string, []byte, bool, []byte, time.Duration) (bool, error)
}

func compareAndSwapHomeAntigravityCreditsHint(ctx context.Context, client antigravityCreditsHintCASClient, key string, old, next AntigravityCreditsHint) (bool, error) {
	raw, found, err := client.KVGet(ctx, key)
	if err != nil || !found {
		return false, err
	}
	var current AntigravityCreditsHint
	if err = json.Unmarshal(raw, &current); err != nil {
		return false, err
	}
	if current != old {
		return false, nil
	}
	encoded, err := json.Marshal(next)
	if err != nil {
		return false, err
	}
	// Compare the original bytes, so a publication between GET and CAS is safe.
	// Older Home servers may reject CAS; never fall back to an unconditional SET.
	return client.KVCompareAndSwap(ctx, key, raw, true, encoded, 30*time.Minute)
}
