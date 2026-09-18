package proxyutil

import (
	"strings"
	"sync"
)

var runtimeProxyOverride struct {
	sync.RWMutex
	enabled    bool
	base       string
	effective  string
	generation uint64
}

// RuntimeProxyResolution is an atomic snapshot of the process-wide proxy
// takeover decision. Cache users must key and build transports from the same
// snapshot so an override change cannot pair a new key with an old route (or
// vice versa).
type RuntimeProxyResolution struct {
	Raw        string
	Effective  string
	Generation uint64
	Overridden bool
}

// SetRuntimeProxyOverride replaces only the configured global proxy value at
// transport construction time. Credential-level proxy URLs and explicit
// "direct" settings remain untouched.
func SetRuntimeProxyOverride(base, effective string) {
	base = strings.TrimSpace(base)
	effective = strings.TrimSpace(effective)
	enabled := effective != ""
	runtimeProxyOverride.Lock()
	if runtimeProxyOverride.enabled != enabled || runtimeProxyOverride.base != base || runtimeProxyOverride.effective != effective {
		runtimeProxyOverride.enabled = enabled
		runtimeProxyOverride.base = base
		runtimeProxyOverride.effective = effective
		runtimeProxyOverride.generation++
	}
	runtimeProxyOverride.Unlock()
}

func ClearRuntimeProxyOverride() {
	runtimeProxyOverride.Lock()
	if runtimeProxyOverride.enabled || runtimeProxyOverride.base != "" || runtimeProxyOverride.effective != "" {
		runtimeProxyOverride.enabled = false
		runtimeProxyOverride.base = ""
		runtimeProxyOverride.effective = ""
		runtimeProxyOverride.generation++
	}
	runtimeProxyOverride.Unlock()
}

// ResolveEffectiveProxy returns the effective route and the generation that
// produced it. Resolution intentionally remains separate from Parse: internal
// proxy-pool hops must be able to parse and dial their configured node without
// re-entering the process-wide takeover.
func ResolveEffectiveProxy(raw string) RuntimeProxyResolution {
	trimmed := strings.TrimSpace(raw)
	runtimeProxyOverride.RLock()
	enabled := runtimeProxyOverride.enabled
	base := runtimeProxyOverride.base
	effective := runtimeProxyOverride.effective
	generation := runtimeProxyOverride.generation
	runtimeProxyOverride.RUnlock()
	if enabled && trimmed == base {
		return RuntimeProxyResolution{
			Raw:        trimmed,
			Effective:  effective,
			Generation: generation,
			Overridden: true,
		}
	}
	return RuntimeProxyResolution{Raw: trimmed, Effective: trimmed, Generation: generation}
}

func resolveRuntimeProxyOverride(raw string) string {
	return ResolveEffectiveProxy(raw).Effective
}
