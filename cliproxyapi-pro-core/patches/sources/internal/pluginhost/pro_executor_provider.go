package pluginhost

import "strings"

// PluginExecutorProvider resolves the normalized execution provider declared by a plugin executor.
func (h *Host) PluginExecutorProvider(pluginID string) (string, bool) {
	adapter, errAdapter := h.executorAdapterForPlugin(pluginID, "")
	if errAdapter != nil || adapter == nil {
		return "", false
	}
	provider := strings.ToLower(strings.TrimSpace(adapter.Identifier()))
	return provider, provider != ""
}
