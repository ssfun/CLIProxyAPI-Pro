package helps

import (
	"net/http"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"
	"golang.org/x/net/proxy"
)

func newUtlsRoundTripper(proxyURL string) *utlsRoundTripper {
	return newUtlsRoundTripperResolved(proxyutil.ResolveEffectiveProxy(proxyURL).Effective)
}

type claudeCodeRoundTripperKey struct {
	effective  string
	generation uint64
}

func cachedClaudeCodeRoundTripper(proxyURL string) http.RoundTripper {
	return cachedClaudeCodeRoundTripperResolved(proxyutil.ResolveEffectiveProxy(proxyURL))
}

func newClaudeCodeRoundTripper(proxyURL string) http.RoundTripper {
	return newClaudeCodeRoundTripperResolved(proxyutil.ResolveEffectiveProxy(proxyURL).Effective)
}

func claudeCodeDialer(proxyURL string) proxy.Dialer {
	return claudeCodeDialerResolved(proxyutil.ResolveEffectiveProxy(proxyURL).Effective)
}

func pooledClaudeCodeTransport(proxyURL string, cfg *config.Config, auth *cliproxyauth.Auth) *http.Transport {
	return pooledClaudeCodeTransportResolved(proxyutil.ResolveEffectiveProxy(proxyURL), cfg, auth)
}
