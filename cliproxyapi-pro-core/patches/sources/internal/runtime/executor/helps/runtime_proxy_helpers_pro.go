package helps

import (
	"net/http"
	"sync"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"
)

type runtimeProxyTransportKey struct {
	scope      string
	generation uint64
}

func buildProxyTransport(proxyURL string) *http.Transport {
	return buildResolvedProxyTransport(proxyutil.ResolveEffectiveProxy(proxyURL))
}

// Keep raw-setting helpers for upstream callers; production cache paths pass the
// captured resolution all the way through the key and builder.
func pooledProxyTransport(proxyURL string, cfg *config.Config, auth *cliproxyauth.Auth) *http.Transport {
	return pooledProxyTransportResolved(proxyutil.ResolveEffectiveProxy(proxyURL), cfg, auth)
}

func buildPooledBaseTransport(proxyURL string) (*http.Transport, error) {
	return buildPooledBaseTransportResolved(proxyutil.ResolveEffectiveProxy(proxyURL).Effective)
}

var runtimeProxyPoolFence struct {
	sync.RWMutex
	generation uint64
}

// Hold the read fence throughout lookup/build, so an old captured resolution
// cannot refill a cache after a newer generation has retired its idle pools.
func lockRuntimeProxyPoolGeneration(generation uint64) (func(), bool) {
	runtimeProxyPoolFence.Lock()
	var retired []*http.Transport
	if generation > runtimeProxyPoolFence.generation {
		runtimeProxyPoolFence.generation = generation
		retired = proxyTransportCache.detachMatching(func(key proxyPoolKey) bool { return key.runtimeGeneration != generation })
	}
	runtimeProxyPoolFence.Unlock()
	for _, transport := range retired {
		transport.CloseIdleConnections()
	}
	runtimeProxyPoolFence.RLock()
	return runtimeProxyPoolFence.RUnlock, generation == runtimeProxyPoolFence.generation
}
