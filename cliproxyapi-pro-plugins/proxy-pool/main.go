package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct {
	void* ptr;
	size_t len;
} cliproxy_buffer;

typedef int (*cliproxy_host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_host_free_fn)(void*, size_t);

typedef struct {
	uint32_t abi_version;
	void* host_ctx;
	cliproxy_host_call_fn call;
	cliproxy_host_free_fn free_buffer;
} cliproxy_host_api;

typedef int (*cliproxy_plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);

typedef struct {
	uint32_t abi_version;
	cliproxy_plugin_call_fn call;
	cliproxy_plugin_free_fn free_buffer;
	cliproxy_plugin_shutdown_fn shutdown;
} cliproxy_plugin_api;

extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);
*/
import "C"

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	pluginconfig "github.com/ssfun/CLIProxyAPI-Pro/cliproxyapi-pro-plugins/proxy-pool/internal/config"
	"github.com/ssfun/CLIProxyAPI-Pro/cliproxyapi-pro-plugins/proxy-pool/internal/engine"
)

const pluginVersion = "0.2.0"

var pluginState = struct {
	sync.Mutex
	engine *engine.Engine
}{engine: engine.New()}

type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *envelopeError  `json:"error,omitempty"`
}

type envelopeError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	HTTPStatus int    `json:"http_status,omitempty"`
}

type lifecycleRequest struct {
	ConfigYAML    []byte `json:"config_yaml"`
	SchemaVersion uint32 `json:"schema_version"`
}

type registration struct {
	SchemaVersion uint32                   `json:"schema_version"`
	Metadata      pluginapi.Metadata       `json:"metadata"`
	Capabilities  registrationCapabilities `json:"capabilities"`
}

type registrationCapabilities struct {
	ManagementAPI bool `json:"management_api"`
}

type managementRegistration struct {
	Routes []managementRoute `json:"routes"`
}

type managementRoute struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	Description string `json:"description"`
}

type managementRequest struct {
	Method string      `json:"Method"`
	Path   string      `json:"Path"`
	Body   []byte      `json:"Body"`
	Query  interface{} `json:"Query"`
}

func main() {}

//export cliproxy_plugin_init
func cliproxy_plugin_init(_ *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if plugin == nil {
		return 1
	}
	plugin.abi_version = C.uint32_t(pluginabi.ABIVersion)
	plugin.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
	plugin.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
	plugin.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)
	return 0
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) C.int {
	if response != nil {
		response.ptr = nil
		response.len = 0
	}
	if method == nil {
		writeResponse(response, errorEnvelope("invalid_method", "method is required", http.StatusBadRequest))
		return 1
	}
	var requestBytes []byte
	if request != nil && requestLen > 0 {
		requestBytes = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}
	raw, errHandle := handleMethod(C.GoString(method), requestBytes)
	if errHandle != nil {
		writeResponse(response, errorEnvelope("plugin_error", errHandle.Error(), http.StatusInternalServerError))
		return 1
	}
	writeResponse(response, raw)
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, _ C.size_t) {
	if ptr != nil {
		C.free(ptr)
	}
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {
	pluginState.Lock()
	current := pluginState.engine
	pluginState.engine = engine.New()
	pluginState.Unlock()
	if current != nil {
		current.Close()
	}
}

func handleMethod(method string, request []byte) ([]byte, error) {
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		return configurePlugin(request)
	case pluginabi.MethodManagementRegister:
		return okEnvelope(managementRegistration{Routes: []managementRoute{
			{Method: http.MethodGet, Path: "/pro/proxy-pool/status", Description: "Return the Pro proxy pool runtime status."},
			{Method: http.MethodPost, Path: "/pro/proxy-pool/test", Description: "Test one configured proxy node."},
			{Method: http.MethodPost, Path: "/pro/proxy-pool/test-all", Description: "Test all configured proxy nodes."},
			{Method: http.MethodPost, Path: "/pro/proxy-pool/reset-stats", Description: "Reset proxy pool runtime statistics."},
			{Method: http.MethodPost, Path: "/pro/proxy-pool/recover", Description: "Clear transient isolation for one proxy node."},
		}})
	case pluginabi.MethodManagementHandle:
		return handleManagement(request)
	default:
		return errorEnvelope("unknown_method", "unknown method: "+method, http.StatusNotFound), nil
	}
}

func configurePlugin(raw []byte) ([]byte, error) {
	var request lifecycleRequest
	if len(raw) > 0 {
		if errUnmarshal := json.Unmarshal(raw, &request); errUnmarshal != nil {
			return nil, fmt.Errorf("decode plugin lifecycle request: %w", errUnmarshal)
		}
	}
	cfg, errConfig := pluginconfig.Parse(request.ConfigYAML)
	if errConfig != nil {
		return nil, errConfig
	}
	pluginState.Lock()
	current := pluginState.engine
	if current == nil {
		current = engine.New()
		pluginState.engine = current
	}
	pluginState.Unlock()
	if errApply := current.ApplyConfig(cfg); errApply != nil {
		return nil, errApply
	}
	return okEnvelope(pluginRegistration())
}

func pluginRegistration() registration {
	return registration{
		SchemaVersion: pluginabi.SchemaVersion,
		Metadata: pluginapi.Metadata{
			Name:             "Pro Proxy Pool",
			Version:          pluginVersion,
			Author:           "ssfun",
			GitHubRepository: "https://github.com/ssfun/CLIProxyAPI-Pro",
			ConfigFields: []pluginapi.ConfigField{
				{Name: "listen", Type: pluginapi.ConfigFieldTypeString, Description: "Loopback SOCKS5 listen address."},
				{Name: "strategy", Type: pluginapi.ConfigFieldTypeEnum, EnumValues: []string{"round-robin", "weighted", "least-connections"}, Description: "Proxy node selection strategy."},
				{Name: "dial-timeout", Type: pluginapi.ConfigFieldTypeString, Description: "Per-node dial timeout."},
				{Name: "max-failover-attempts", Type: pluginapi.ConfigFieldTypeInteger, Description: "Maximum proxy nodes tried for one CONNECT request."},
				{Name: "fail-open", Type: pluginapi.ConfigFieldTypeBoolean, Description: "Allow direct fallback when every proxy node fails."},
				{Name: "health-check", Type: pluginapi.ConfigFieldTypeObject, Description: "Health check policy."},
				{Name: "nodes", Type: pluginapi.ConfigFieldTypeArray, Description: "Configured upstream proxy nodes."},
			},
		},
		Capabilities: registrationCapabilities{ManagementAPI: true},
	}
}

func handleManagement(raw []byte) ([]byte, error) {
	var request managementRequest
	if errUnmarshal := json.Unmarshal(raw, &request); errUnmarshal != nil {
		return okEnvelope(jsonResponse(http.StatusBadRequest, map[string]any{"error": "invalid_request", "message": errUnmarshal.Error()}))
	}
	pluginState.Lock()
	current := pluginState.engine
	pluginState.Unlock()
	if current == nil {
		return okEnvelope(jsonResponse(http.StatusServiceUnavailable, map[string]any{"error": "proxy_pool_unavailable"}))
	}
	path := strings.TrimSpace(request.Path)
	switch {
	case request.Method == http.MethodGet && strings.HasSuffix(path, "/pro/proxy-pool/status"):
		return okEnvelope(jsonResponse(http.StatusOK, current.Status()))
	case request.Method == http.MethodPost && strings.HasSuffix(path, "/pro/proxy-pool/test"):
		var body struct {
			NodeID   string `json:"node_id"`
			URL      string `json:"url"`
			ProxyURL string `json:"proxy_url"`
		}
		if errBody := json.Unmarshal(request.Body, &body); errBody != nil || strings.TrimSpace(body.NodeID) == "" {
			return okEnvelope(jsonResponse(http.StatusBadRequest, map[string]any{"error": "invalid_request", "message": "node_id is required"}))
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		result := current.Probe(ctx, body.NodeID, body.URL)
		if strings.TrimSpace(body.ProxyURL) != "" {
			result = current.ProbeDraft(ctx, body.NodeID, body.ProxyURL, body.URL)
		}
		status := http.StatusOK
		if !result.Success {
			status = http.StatusBadGateway
		}
		return okEnvelope(jsonResponse(status, result))
	case request.Method == http.MethodPost && strings.HasSuffix(path, "/pro/proxy-pool/test-all"):
		var body struct {
			Concurrency int `json:"concurrency"`
		}
		_ = json.Unmarshal(request.Body, &body)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		return okEnvelope(jsonResponse(http.StatusOK, map[string]any{"results": current.ProbeAll(ctx, body.Concurrency)}))
	case request.Method == http.MethodPost && strings.HasSuffix(path, "/pro/proxy-pool/reset-stats"):
		current.ResetStats()
		return okEnvelope(jsonResponse(http.StatusOK, map[string]any{"status": "ok"}))
	case request.Method == http.MethodPost && strings.HasSuffix(path, "/pro/proxy-pool/recover"):
		var body struct {
			NodeID string `json:"node_id"`
		}
		if errBody := json.Unmarshal(request.Body, &body); errBody != nil || strings.TrimSpace(body.NodeID) == "" {
			return okEnvelope(jsonResponse(http.StatusBadRequest, map[string]any{"error": "invalid_request", "message": "node_id is required"}))
		}
		if errRecover := current.Recover(body.NodeID); errRecover != nil {
			return okEnvelope(jsonResponse(http.StatusNotFound, map[string]any{"error": "not_found", "message": errRecover.Error()}))
		}
		return okEnvelope(jsonResponse(http.StatusOK, map[string]any{"status": "ok", "node_id": strings.TrimSpace(body.NodeID)}))
	default:
		return okEnvelope(jsonResponse(http.StatusNotFound, map[string]any{"error": "not_found"}))
	}
}

func jsonResponse(status int, value any) pluginapi.ManagementResponse {
	body, errMarshal := json.Marshal(value)
	if errMarshal != nil {
		body = []byte(`{"error":"response_encode_failed"}`)
		status = http.StatusInternalServerError
	}
	return pluginapi.ManagementResponse{
		StatusCode: status,
		Headers:    http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
		Body:       body,
	}
}

func okEnvelope(value any) ([]byte, error) {
	result, errMarshal := json.Marshal(value)
	if errMarshal != nil {
		return nil, errMarshal
	}
	return json.Marshal(envelope{OK: true, Result: result})
}

func errorEnvelope(code, message string, status int) []byte {
	raw, _ := json.Marshal(envelope{OK: false, Error: &envelopeError{Code: code, Message: message, HTTPStatus: status}})
	return raw
}

func writeResponse(response *C.cliproxy_buffer, raw []byte) {
	if response == nil || len(raw) == 0 {
		return
	}
	ptr := C.CBytes(raw)
	if ptr == nil {
		return
	}
	response.ptr = ptr
	response.len = C.size_t(len(raw))
}
