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

static const cliproxy_host_api* stored_host;

static void store_host_api(const cliproxy_host_api* host) {
	stored_host = host;
}

static int call_host_api(const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
	if (stored_host == NULL || stored_host->call == NULL) {
		return 1;
	}
	return stored_host->call(stored_host->host_ctx, method, request, request_len, response);
}

static void free_host_buffer(void* ptr, size_t len) {
	if (stored_host != NULL && stored_host->free_buffer != NULL && ptr != NULL) {
		stored_host->free_buffer(ptr, len);
	}
}
*/
import "C"

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"unsafe"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

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
	SchemaVersion uint32                 `json:"schema_version"`
	Metadata      pluginapi.Metadata     `json:"metadata"`
	Capabilities  registrationCapability `json:"capabilities"`
}

type registrationCapability struct {
	ModelRouter           bool                         `json:"model_router"`
	Executor              bool                         `json:"executor"`
	ExecutorModelScope    pluginapi.ExecutorModelScope `json:"executor_model_scope"`
	ExecutorInputFormats  []string                     `json:"executor_input_formats,omitempty"`
	ExecutorOutputFormats []string                     `json:"executor_output_formats,omitempty"`
}

type identifierResponse struct {
	Identifier string `json:"identifier"`
}

type rpcExecutorRequest struct {
	pluginapi.ExecutorRequest
	StreamID       string `json:"stream_id,omitempty"`
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type rpcModelRouteRequest struct {
	pluginapi.ModelRouteRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
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
		if err := configurePlugin(request); err != nil {
			return nil, err
		}
		return okEnvelope(pluginRegistration())
	case pluginabi.MethodModelRoute:
		return routeModel(request)
	case pluginabi.MethodExecutorIdentifier:
		return okEnvelope(identifierResponse{Identifier: pluginIdentifier})
	case pluginabi.MethodExecutorExecute:
		return execute(request)
	case pluginabi.MethodExecutorExecuteStream:
		return executeStream(request)
	case pluginabi.MethodExecutorCountTokens:
		return okEnvelope(pluginapi.ExecutorResponse{Payload: []byte(`{"input_tokens":0}`)})
	case pluginabi.MethodExecutorHTTPRequest:
		return okEnvelope(pluginapi.ExecutorHTTPResponse{
			StatusCode: http.StatusNotImplemented,
			Body:       []byte(`{"error":"http bridge is not implemented by codexcont"}`),
		})
	default:
		return errorEnvelope("unknown_method", "unknown method: "+method), nil
	}
}

func pluginRegistration() registration {
	return registration{
		SchemaVersion: pluginabi.SchemaVersion,
		Metadata: pluginapi.Metadata{
			Name:             pluginIdentifier,
			Version:          pluginVersion,
			Author:           pluginAuthor,
			GitHubRepository: pluginRepo,
			ConfigFields: []pluginapi.ConfigField{
				{Name: "source_formats", Type: pluginapi.ConfigFieldTypeArray, Description: "Accepted client protocols, for example responses or codex."},
				{Name: "exit_protocol", Type: pluginapi.ConfigFieldTypeString, Description: "Target response protocol passed to host.model callbacks. Only responses is supported; invalid values fall back to responses."},
				{Name: "model_patterns", Type: pluginapi.ConfigFieldTypeArray, Description: "Glob patterns that opt models into folding."},
				{Name: "truncation_step", Type: pluginapi.ConfigFieldTypeInteger, Description: "Reasoning truncation step used by the 518*n-2 detector."},
				{Name: "max_continue", Type: pluginapi.ConfigFieldTypeInteger, Description: "Maximum number of continuation rounds."},
				{Name: "min_n", Type: pluginapi.ConfigFieldTypeInteger, Description: "Minimum truncation tier n that may continue."},
				{Name: "max_n", Type: pluginapi.ConfigFieldTypeInteger, Description: "Maximum truncation tier n that may continue. Zero disables the cap."},
				{Name: "marker_text", Type: pluginapi.ConfigFieldTypeString, Description: "Hidden commentary message appended before continuation rounds."},
				{Name: "forward_marker", Type: pluginapi.ConfigFieldTypeBoolean, Description: "Emit the commentary marker downstream so the client records it."},
				{Name: "force_include_encrypted", Type: pluginapi.ConfigFieldTypeBoolean, Description: "Ensure reasoning.encrypted_content is requested upstream."},
				{Name: "rechunk_final_answer", Type: pluginapi.ConfigFieldTypeBoolean, Description: "Rechunk the final answer text into fixed-size deltas."},
				{Name: "rechunk_size", Type: pluginapi.ConfigFieldTypeInteger, Description: "Chunk size used when rechunk_final_answer is enabled."},
				{Name: "max_total_output_tokens", Type: pluginapi.ConfigFieldTypeInteger, Description: "Stop continuing once summed billed output tokens reach this cap. Zero disables it."},
			},
		},
		Capabilities: registrationCapability{
			ModelRouter:           true,
			Executor:              true,
			ExecutorModelScope:    pluginapi.ExecutorModelScopeStatic,
			ExecutorInputFormats:  []string{"responses"},
			ExecutorOutputFormats: []string{"responses"},
		},
	}
}

func routeModel(raw []byte) ([]byte, error) {
	var req rpcModelRouteRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	if !shouldRoute(req.ModelRouteRequest) {
		return okEnvelope(pluginapi.ModelRouteResponse{Handled: false})
	}
	return okEnvelope(pluginapi.ModelRouteResponse{
		Handled:    true,
		TargetKind: pluginapi.ModelRouteTargetSelf,
		Reason:     "codexcont_folded_stream",
	})
}

func execute(raw []byte) ([]byte, error) {
	var req rpcExecutorRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	if req.Stream {
		return errorEnvelope("executor_error", "executor.execute requires stream=false"), nil
	}
	body, headers, err := delegateOnce(context.Background(), req.ExecutorRequest, req.HostCallbackID)
	if err != nil {
		return errorEnvelope("executor_error", err.Error()), nil
	}
	return okEnvelope(pluginapi.ExecutorResponse{Payload: body, Headers: headers})
}

func executeStream(raw []byte) ([]byte, error) {
	var req rpcExecutorRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	if !req.Stream {
		return errorEnvelope("executor_error", "executor.execute_stream requires stream=true"), nil
	}
	return startExecutorStream(req, runFoldedExecution, closePluginStream)
}

type streamRunner func(context.Context, pluginapi.ExecutorRequest, string, string) error
type streamCloser func(string, string)

func startExecutorStream(req rpcExecutorRequest, runner streamRunner, closeStream streamCloser) ([]byte, error) {
	streamID := req.StreamID
	if streamID == "" {
		return errorEnvelope("executor_error", "stream_id is required for executor.execute_stream"), nil
	}
	if runner == nil {
		return errorEnvelope("executor_error", "stream runner is unavailable"), nil
	}
	if closeStream == nil {
		closeStream = func(string, string) {}
	}
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				closeStream(streamID, fmt.Sprintf("codexcont panic: %v", recovered))
			}
		}()
		errRun := runner(context.Background(), req.ExecutorRequest, req.HostCallbackID, streamID)
		if errRun != nil {
			closeStream(streamID, errRun.Error())
			return
		}
		closeStream(streamID, "")
	}()
	return okEnvelope(map[string]any{
		"headers": http.Header{"Content-Type": []string{"text/event-stream"}},
	})
}

func writeResponse(response *C.cliproxy_buffer, raw []byte) {
	if response == nil || len(raw) == 0 {
		return
	}
	ptr := C.malloc(C.size_t(len(raw)))
	if ptr == nil {
		return
	}
	buf := unsafe.Slice((*byte)(ptr), len(raw))
	copy(buf, raw)
	response.ptr = ptr
	response.len = C.size_t(len(raw))
}

func callHostRPC(method string, rawReq []byte) ([]byte, error) {
	if method == "" {
		return nil, fmt.Errorf("host method is required")
	}
	cMethod := C.CString(method)
	defer C.free(unsafe.Pointer(cMethod))

	var cResp C.cliproxy_buffer
	var reqPtr *C.uint8_t
	if len(rawReq) > 0 {
		reqPtr = (*C.uint8_t)(unsafe.Pointer(&rawReq[0]))
	}
	rc := C.call_host_api(cMethod, reqPtr, C.size_t(len(rawReq)), &cResp)
	defer func() {
		if cResp.ptr != nil {
			C.free_host_buffer(cResp.ptr, cResp.len)
		}
	}()
	if rc != 0 {
		return nil, fmt.Errorf("host call %s failed", method)
	}
	return decodeRPCResult(C.GoBytes(cResp.ptr, C.int(cResp.len)))
}
