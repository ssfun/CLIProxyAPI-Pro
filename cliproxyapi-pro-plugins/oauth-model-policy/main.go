package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct { void* ptr; size_t len; } cliproxy_buffer;
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

static const cliproxy_host_api* stored_host;
static void store_host_api(const cliproxy_host_api* host) { stored_host = host; }
static int call_host_api(const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
	if (stored_host == NULL || stored_host->call == NULL) return 1;
	return stored_host->call(stored_host->host_ctx, method, request, request_len, response);
}
static void free_host_buffer(void* ptr, size_t len) {
	if (stored_host != NULL && stored_host->free_buffer != NULL && ptr != NULL) stored_host->free_buffer(ptr, len);
}
*/
import "C"

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"unsafe"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	pluginconfig "github.com/ssfun/CLIProxyAPI-Pro/cliproxyapi-pro-plugins/oauth-model-policy/internal/config"
	"github.com/ssfun/CLIProxyAPI-Pro/cliproxyapi-pro-plugins/oauth-model-policy/internal/policy"
)

const (
	pluginVersion         = "0.2.0"
	methodAuthModelFilter = "model.filter_for_auth"
)

var pluginState = struct {
	sync.RWMutex
	engine *policy.Engine
}{engine: policy.New()}

type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *envelopeError  `json:"error,omitempty"`
}

type envelopeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type lifecycleRequest struct {
	ConfigYAML []byte `json:"config_yaml"`
}

type registration struct {
	SchemaVersion uint32                   `json:"schema_version"`
	Metadata      pluginapi.Metadata       `json:"metadata"`
	Capabilities  registrationCapabilities `json:"capabilities"`
}

type registrationCapabilities struct {
	AuthModelFilter bool `json:"auth_model_filter"`
}

type authModelFilterRequest struct {
	AuthID         string                `json:"auth_id"`
	AuthProvider   string                `json:"auth_provider"`
	AuthKind       string                `json:"auth_kind"`
	StorageJSON    []byte                `json:"storage_json"`
	Metadata       map[string]any        `json:"metadata"`
	Attributes     map[string]string     `json:"attributes"`
	Models         []pluginapi.ModelInfo `json:"models"`
	HostCallbackID string                `json:"host_callback_id"`
}

type authModelFilterResponse struct {
	Handled          bool              `json:"handled"`
	ExcludedModelIDs []string          `json:"excluded_model_ids,omitempty"`
	Annotations      map[string]string `json:"annotations,omitempty"`
}

type hostHTTPRequest struct {
	HostCallbackID string      `json:"host_callback_id,omitempty"`
	Request        httpRequest `json:"request"`
}

type httpRequest struct {
	Method  string              `json:"method,omitempty"`
	URL     string              `json:"url,omitempty"`
	Headers map[string][]string `json:"headers,omitempty"`
	Body    []byte              `json:"body,omitempty"`
}

func main() {}

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if plugin == nil {
		return 1
	}
	C.store_host_api(host)
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
		writeResponse(response, errorEnvelope("invalid_method", "method is required"))
		return 1
	}
	var requestBytes []byte
	if request != nil && requestLen > 0 {
		requestBytes = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}
	raw, errHandle := handleMethod(C.GoString(method), requestBytes)
	if errHandle != nil {
		writeResponse(response, errorEnvelope("plugin_error", errHandle.Error()))
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
func cliproxyPluginShutdown() {}

func handleMethod(method string, request []byte) ([]byte, error) {
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		return configurePlugin(request)
	case methodAuthModelFilter:
		return filterAuthModels(request)
	default:
		return errorEnvelope("unknown_method", "unknown method: "+method), nil
	}
}

func configurePlugin(raw []byte) ([]byte, error) {
	request := lifecycleRequest{}
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
	pluginState.engine.ApplyConfig(cfg)
	pluginState.Unlock()
	return okEnvelope(pluginRegistration())
}

func pluginRegistration() registration {
	return registration{
		SchemaVersion: pluginabi.SchemaVersion,
		Metadata: pluginapi.Metadata{
			Name:             "OAuth Model Policy",
			Version:          pluginVersion,
			Author:           "ssfun",
			GitHubRepository: "https://github.com/ssfun/CLIProxyAPI-Pro",
			ConfigFields: []pluginapi.ConfigField{
				{Name: "cache-ttl", Type: pluginapi.ConfigFieldTypeString, Description: "How long a resolved account plan remains fresh."},
				{Name: "resolve-timeout", Type: pluginapi.ConfigFieldTypeString, Description: "Timeout for provider plan discovery."},
				{Name: "providers", Type: pluginapi.ConfigFieldTypeObject, Description: "Provider plan to excluded-model policy map."},
			},
		},
		Capabilities: registrationCapabilities{AuthModelFilter: true},
	}
}

func filterAuthModels(raw []byte) ([]byte, error) {
	request := authModelFilterRequest{}
	if errUnmarshal := json.Unmarshal(raw, &request); errUnmarshal != nil {
		return nil, fmt.Errorf("decode auth model filter request: %w", errUnmarshal)
	}
	pluginState.RLock()
	engine := pluginState.engine
	pluginState.RUnlock()
	result := engine.Filter(context.Background(), policy.Input{
		AuthID:       request.AuthID,
		AuthProvider: request.AuthProvider,
		AuthKind:     request.AuthKind,
		StorageJSON:  request.StorageJSON,
		Metadata:     request.Metadata,
		Attributes:   request.Attributes,
		Models:       request.Models,
		HTTPDo: func(_ context.Context, req pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
			return callHostHTTP(request.HostCallbackID, req)
		},
	})
	return okEnvelope(authModelFilterResponse{
		Handled:          result.Handled,
		ExcludedModelIDs: result.ExcludedModelIDs,
		Annotations:      result.Annotations,
	})
}

func callHostHTTP(callbackID string, request pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
	result, errCall := callHost(pluginabi.MethodHostHTTPDo, hostHTTPRequest{
		HostCallbackID: callbackID,
		Request:        httpRequest{Method: request.Method, URL: request.URL, Headers: request.Headers, Body: request.Body},
	})
	if errCall != nil {
		return pluginapi.HTTPResponse{}, errCall
	}
	response := pluginapi.HTTPResponse{}
	if errUnmarshal := json.Unmarshal(result, &response); errUnmarshal != nil {
		return pluginapi.HTTPResponse{}, fmt.Errorf("decode host http response: %w", errUnmarshal)
	}
	return response, nil
}

func callHost(method string, payload any) (json.RawMessage, error) {
	rawPayload, errMarshal := json.Marshal(payload)
	if errMarshal != nil {
		return nil, fmt.Errorf("marshal host callback: %w", errMarshal)
	}
	cMethod := C.CString(method)
	defer C.free(unsafe.Pointer(cMethod))
	var response C.cliproxy_buffer
	var requestPtr *C.uint8_t
	if len(rawPayload) > 0 {
		cPayload := C.CBytes(rawPayload)
		if cPayload == nil {
			return nil, fmt.Errorf("allocate host callback payload")
		}
		defer C.free(cPayload)
		requestPtr = (*C.uint8_t)(cPayload)
	}
	callCode := C.call_host_api(cMethod, requestPtr, C.size_t(len(rawPayload)), &response)
	var rawResponse []byte
	if response.ptr != nil && response.len > 0 {
		rawResponse = C.GoBytes(response.ptr, C.int(response.len))
	}
	if response.ptr != nil {
		C.free_host_buffer(response.ptr, response.len)
	}
	if len(rawResponse) == 0 {
		return nil, fmt.Errorf("host callback returned no response, code=%d", int(callCode))
	}
	env := envelope{}
	if errUnmarshal := json.Unmarshal(rawResponse, &env); errUnmarshal != nil {
		return nil, fmt.Errorf("decode host callback envelope: %w", errUnmarshal)
	}
	if !env.OK || callCode != 0 {
		if env.Error != nil {
			return nil, fmt.Errorf("%s: %s", env.Error.Code, env.Error.Message)
		}
		return nil, fmt.Errorf("host callback failed, code=%d", int(callCode))
	}
	return append(json.RawMessage(nil), env.Result...), nil
}

func okEnvelope(value any) ([]byte, error) {
	raw, errMarshal := json.Marshal(value)
	if errMarshal != nil {
		return nil, errMarshal
	}
	return json.Marshal(envelope{OK: true, Result: raw})
}

func errorEnvelope(code, message string) []byte {
	raw, _ := json.Marshal(envelope{OK: false, Error: &envelopeError{Code: code, Message: message}})
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
