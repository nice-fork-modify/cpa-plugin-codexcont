package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestDecodeConfigNormalizesDefaults(t *testing.T) {
	cfg, err := decodeConfig([]byte(`
enabled: true
priority: 10
source_formats:
  - responses
  - openai-response
exit_protocol: codex
model_patterns:
  - gpt-5.5
rechunk_size: 0
`))
	if err != nil {
		t.Fatalf("decodeConfig() error = %v", err)
	}
	if len(cfg.SourceFormats) != 1 || cfg.SourceFormats[0] != "responses" {
		t.Fatalf("source_formats = %#v", cfg.SourceFormats)
	}
	if cfg.ExitProtocol != "responses" {
		t.Fatalf("exit_protocol = %q, want responses", cfg.ExitProtocol)
	}
	if cfg.RechunkSize != 8 {
		t.Fatalf("rechunk_size = %d, want 8", cfg.RechunkSize)
	}
}

func TestPluginRegistrationExposesCommonFieldsOnly(t *testing.T) {
	reg := pluginRegistration()
	if got := reg.Capabilities.ExecutorInputFormats; len(got) != 1 || got[0] != "responses" {
		t.Fatalf("executor_input_formats = %#v, want [responses]", got)
	}
	if got := reg.Capabilities.ExecutorOutputFormats; len(got) != 1 || got[0] != "responses" {
		t.Fatalf("executor_output_formats = %#v, want [responses]", got)
	}
	fields := reg.Metadata.ConfigFields
	if len(fields) != 2 {
		t.Fatalf("config_fields = %#v, want model_patterns and max_continue", fields)
	}
	modelField := fields[0]
	if modelField.Name != "model_patterns" || modelField.Type != pluginapi.ConfigFieldTypeString {
		t.Fatalf("model config field = %#v", modelField)
	}
	if !strings.Contains(modelField.Description, "/v0/resource/plugins/codexcont/status") {
		t.Fatalf("description missing status path: %q", modelField.Description)
	}
	continueField := fields[1]
	if continueField.Name != "max_continue" || continueField.Type != pluginapi.ConfigFieldTypeInteger {
		t.Fatalf("continuation config field = %#v", continueField)
	}
}

func TestHostProtocolsStayOnResponses(t *testing.T) {
	entryProtocol, exitProtocol := hostProtocols(pluginapi.ExecutorRequest{SourceFormat: "responses"})
	if entryProtocol != "openai-response" {
		t.Fatalf("entry_protocol = %q, want openai-response", entryProtocol)
	}
	if exitProtocol != "openai-response" {
		t.Fatalf("exit_protocol = %q, want openai-response", exitProtocol)
	}
}

func TestHostProtocolsPreferExecutorFormatForPayload(t *testing.T) {
	entryProtocol, _ := hostProtocols(pluginapi.ExecutorRequest{
		Format:       "responses",
		SourceFormat: "codex",
	})
	if entryProtocol != "openai-response" {
		t.Fatalf("entry_protocol = %q, want openai-response", entryProtocol)
	}
}

func TestBuildRoundPayloadForcesEncryptedInclude(t *testing.T) {
	body := map[string]any{
		"model":  "gpt-5",
		"stream": false,
		"input":  []any{},
	}
	cfg := defaultPluginConfig()
	payload := buildRoundPayload(body, []any{}, []any{map[string]any{"type": "message"}}, cfg, true)
	include, _ := payload["include"].([]any)
	if len(include) != 1 || include[0] != encryptedInclude {
		t.Fatalf("include = %#v", include)
	}
}

func TestBuildRoundPayloadPreservesStringInputOnContinuation(t *testing.T) {
	body := map[string]any{
		"model": "gpt-5",
		"input": "Solve this carefully.",
	}
	cfg := defaultPluginConfig()
	payload := buildRoundPayload(body, body["input"], []any{map[string]any{"type": "reasoning", "id": "rs_1"}}, cfg, true)
	input, ok := payload["input"].([]any)
	if !ok || len(input) != 2 {
		t.Fatalf("input = %#v", payload["input"])
	}
	first, _ := input[0].(map[string]any)
	if toString(first["role"]) != "user" {
		t.Fatalf("first input role = %q, want user", toString(first["role"]))
	}
}

func TestBuildRoundPayloadPreservesObjectInputOnContinuation(t *testing.T) {
	body := map[string]any{
		"model": "gpt-5",
		"input": map[string]any{
			"role": "user",
			"content": []any{
				map[string]any{"type": "input_text", "text": "Solve this carefully."},
			},
		},
	}
	cfg := defaultPluginConfig()
	payload := buildRoundPayload(body, body["input"], []any{map[string]any{"type": "reasoning", "id": "rs_1"}}, cfg, true)
	input, ok := payload["input"].([]any)
	if !ok || len(input) != 2 {
		t.Fatalf("input = %#v", payload["input"])
	}
	first, _ := input[0].(map[string]any)
	if toString(first["role"]) != "user" {
		t.Fatalf("first input role = %q, want user", toString(first["role"]))
	}
}

func TestBuildRoundPayloadDropsPreviousResponseIDWhenRequested(t *testing.T) {
	body := map[string]any{
		"model":                "gpt-5",
		"input":                []any{},
		"previous_response_id": "resp_prev",
	}
	cfg := defaultPluginConfig()
	payload := buildRoundPayload(body, body["input"], []any{map[string]any{"type": "reasoning", "id": "rs_1"}}, cfg, true)
	if _, ok := payload["previous_response_id"]; ok {
		t.Fatalf("payload previous_response_id = %#v, want dropped", payload["previous_response_id"])
	}
}

func TestRoundHasEncryptedReasoningMatchesAnyItem(t *testing.T) {
	items := []map[string]any{
		{"id": "rs_1", "encrypted_content": ""},
		{"id": "rs_2", "encrypted_content": "ciphertext"},
	}
	if !roundHasEncryptedReasoning(items) {
		t.Fatal("roundHasEncryptedReasoning() = false, want true")
	}
}

func TestDegradedReasoningLogFieldsForContinuation(t *testing.T) {
	cfg := defaultPluginConfig()
	total := usageTotals{OutputTokens: 518}
	state := roundState{
		sawTerminal: true,
		usage: roundUsage{
			ReasoningTokens: 516,
		},
	}

	fields := degradedReasoningLogFields("gpt-5.5", 1, state, cfg, total, true, true, true)
	if fields == nil {
		t.Fatal("fields = nil, want structured debug fields")
	}
	if got := fields["degraded_reasoning_detected"]; got != true {
		t.Fatalf("degraded_reasoning_detected = %#v, want true", got)
	}
	if got := fields["can_continue"]; got != true {
		t.Fatalf("can_continue = %#v, want true", got)
	}
	if got := fields["truncation_tier"]; got != 1 {
		t.Fatalf("truncation_tier = %#v, want 1", got)
	}
	if _, exists := fields["stop_reason"]; exists {
		t.Fatalf("stop_reason present in continuation fields: %#v", fields["stop_reason"])
	}
}

func TestDegradedReasoningLogFieldsForStop(t *testing.T) {
	cfg := defaultPluginConfig()
	total := usageTotals{OutputTokens: 1036}
	state := roundState{
		sawTerminal: true,
		usage: roundUsage{
			ReasoningTokens: 1034,
		},
	}

	fields := degradedReasoningLogFields("gpt-5.5", 2, state, cfg, total, false, true, false)
	if fields == nil {
		t.Fatal("fields = nil, want structured debug fields")
	}
	if got := fields["can_continue"]; got != false {
		t.Fatalf("can_continue = %#v, want false", got)
	}
	if got := fields["stop_reason"]; got != "no_encrypted_content" {
		t.Fatalf("stop_reason = %#v, want no_encrypted_content", got)
	}
}

func TestDegradedReasoningLogMessageForContinuation(t *testing.T) {
	message := degradedReasoningLogMessage(map[string]any{
		"round":                       1,
		"reasoning_tokens":            516,
		"truncation_step":             518,
		"truncation_tier":             1,
		"degraded_reasoning_detected": true,
		"saw_terminal":                true,
		"has_encrypted_content":       true,
		"within_output_cap":           true,
		"total_output_tokens":         518,
		"max_total_output_tokens":     0,
		"max_continue":                3,
		"can_continue":                true,
	}, true)
	if !strings.Contains(message, "codexcont detected degraded reasoning pattern; continuing") {
		t.Fatalf("message = %q, want continuing prefix", message)
	}
	if !strings.Contains(message, "round=1") || !strings.Contains(message, "truncation_tier=1") {
		t.Fatalf("message = %q, want round and truncation tier details", message)
	}
	if strings.Contains(message, "stop_reason=") {
		t.Fatalf("message = %q, should not include stop_reason", message)
	}
}

func TestDegradedReasoningLogMessageForStop(t *testing.T) {
	message := degradedReasoningLogMessage(map[string]any{
		"round":                       2,
		"reasoning_tokens":            1034,
		"truncation_step":             518,
		"truncation_tier":             2,
		"degraded_reasoning_detected": true,
		"saw_terminal":                true,
		"has_encrypted_content":       false,
		"within_output_cap":           true,
		"total_output_tokens":         1036,
		"max_total_output_tokens":     0,
		"max_continue":                3,
		"can_continue":                false,
		"stop_reason":                 "no_encrypted_content",
	}, false)
	if !strings.Contains(message, "codexcont detected degraded reasoning pattern; stopping") {
		t.Fatalf("message = %q, want stopping prefix", message)
	}
	if !strings.Contains(message, "stop_reason=no_encrypted_content") {
		t.Fatalf("message = %q, want stop_reason", message)
	}
}

func TestTerminalStatsOutcomeClassifiesUpstreamTerminal(t *testing.T) {
	tests := []struct {
		eventType string
		stop      string
		completed bool
		reason    string
	}{
		{eventType: "response.completed", stop: "max_continue", completed: true, reason: "max_continue"},
		{eventType: "response.failed", completed: false, reason: "upstream_failed"},
		{eventType: "response.incomplete", completed: false, reason: "upstream_incomplete"},
	}
	for _, tc := range tests {
		completed, reason := terminalStatsOutcome(map[string]any{"type": tc.eventType}, tc.stop)
		if completed != tc.completed || reason != tc.reason {
			t.Fatalf("terminalStatsOutcome(%q) = (%v, %q), want (%v, %q)", tc.eventType, completed, reason, tc.completed, tc.reason)
		}
	}
}

func TestShouldContinueMatches518Fingerprint(t *testing.T) {
	cfg := defaultPluginConfig()
	if !shouldContinue(516, cfg) {
		t.Fatal("shouldContinue(516) = false, want true")
	}
	if shouldContinue(517, cfg) {
		t.Fatal("shouldContinue(517) = true, want false")
	}
}

func TestSSEDecoderParsesDoneAndJSON(t *testing.T) {
	decoder := &sseDecoder{}
	msgs, err := decoder.Feed([]byte("data: {\"type\":\"response.created\"}\n\ndata: [DONE]\n\n"))
	if err != nil {
		t.Fatalf("Feed() error = %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("len(msgs) = %d, want 2", len(msgs))
	}
	if msgs[0].done {
		t.Fatal("first message should not be done")
	}
	if !msgs[1].done {
		t.Fatal("second message should be done")
	}
}

func TestSSEDecoderParsesFrameWithoutDelimiter(t *testing.T) {
	decoder := &sseDecoder{}
	msgs, err := decoder.Feed([]byte("event: response.created\ndata: {\"type\":\"response.created\"}\n"))
	if err != nil {
		t.Fatalf("Feed() error = %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("len(msgs) = %d, want 1", len(msgs))
	}
	if msgs[0].done {
		t.Fatal("message should not be done")
	}
}

func TestSSEDecoderParsesLineChunkedFramesWithoutNewlines(t *testing.T) {
	decoder := &sseDecoder{}
	chunks := [][]byte{
		[]byte("event: response.created"),
		[]byte("data: {\"type\":\"response.created\"}"),
		[]byte("event: response.completed"),
		[]byte("data: {\"type\":\"response.completed\"}"),
	}

	var msgs []sseMessage
	for _, chunk := range chunks {
		next, err := decoder.Feed(chunk)
		if err != nil {
			t.Fatalf("Feed(%q) error = %v", chunk, err)
		}
		msgs = append(msgs, next...)
	}
	flushed, err := decoder.Flush()
	if err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	msgs = append(msgs, flushed...)

	if len(msgs) != 2 {
		t.Fatalf("len(msgs) = %d, want 2", len(msgs))
	}
	if string(msgs[0].data) != "{\"type\":\"response.created\"}" {
		t.Fatalf("first message = %s", string(msgs[0].data))
	}
	if string(msgs[1].data) != "{\"type\":\"response.completed\"}" {
		t.Fatalf("second message = %s", string(msgs[1].data))
	}
}

func TestSSEDecoderDoesNotSplitPartialJSONThatStartsWithDataPrefix(t *testing.T) {
	decoder := &sseDecoder{}
	chunks := [][]byte{
		[]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"prefix "),
		[]byte("data: suffix\"}"),
	}

	var msgs []sseMessage
	for _, chunk := range chunks {
		next, err := decoder.Feed(chunk)
		if err != nil {
			t.Fatalf("Feed(%q) error = %v", chunk, err)
		}
		msgs = append(msgs, next...)
	}
	flushed, err := decoder.Flush()
	if err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	msgs = append(msgs, flushed...)

	if len(msgs) != 1 {
		t.Fatalf("len(msgs) = %d, want 1", len(msgs))
	}
	if !strings.Contains(string(msgs[0].data), "\"delta\":\"prefix data: suffix\"") {
		t.Fatalf("message = %s", string(msgs[0].data))
	}
}

func TestReconstructTerminalAddsProxyMetadata(t *testing.T) {
	base := map[string]any{"id": "resp_1"}
	terminal := map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"status": "completed",
		},
	}
	usage := map[string]any{
		"input_tokens":  10,
		"output_tokens": 20,
		"total_tokens":  30,
	}
	event := reconstructTerminal(terminal, base, []any{}, usage, 7, []map[string]any{{"round": 1}}, "", map[string]any{"output_tokens": 40}, map[string]any{"output_tokens": 20})
	resp := event["response"].(map[string]any)
	metadata := resp["metadata"].(map[string]any)
	if _, ok := metadata["proxy_rounds"]; !ok {
		t.Fatal("proxy_rounds metadata missing")
	}
	if _, ok := metadata["proxy_billed_usage"]; !ok {
		t.Fatal("proxy_billed_usage metadata missing")
	}
	if _, ok := metadata["proxy_agent_usage"]; !ok {
		t.Fatal("proxy_agent_usage metadata missing")
	}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("serialized event is empty")
	}
}

func TestAgentUsageMatchesSingleResponseView(t *testing.T) {
	first := &roundUsage{
		InputTokens:  100,
		CachedTokens: func() *int { v := 7; return &v }(),
	}
	total := usageTotals{
		ReasoningTokens: 300,
	}
	final := roundUsage{
		OutputTokens:    380,
		ReasoningTokens: 300,
	}
	usage := agentUsage(first, total, final, true)
	if got := toInt(usage["input_tokens"]); got != 100 {
		t.Fatalf("input_tokens = %d, want 100", got)
	}
	if got := toInt(usage["output_tokens"]); got != 380 {
		t.Fatalf("output_tokens = %d, want 380", got)
	}
	if details, _ := usage["input_tokens_details"].(map[string]any); toInt(details["cached_tokens"]) != 7 {
		t.Fatalf("cached_tokens = %#v, want 7", details["cached_tokens"])
	}
}

func TestShouldRouteDefaultsToGPT55Prefix(t *testing.T) {
	currentConfig.Store(defaultPluginConfig())

	body := []byte(`{"input":[],"stream":true}`)
	tests := []struct {
		model string
		want  bool
	}{
		{model: "gpt-5.5", want: true},
		{model: "gpt-5", want: false},
		{model: "gpt-5.5-mini", want: true},
		{model: "gpt-5.4", want: false},
		{model: "codex-mini-latest", want: false},
	}

	for _, tc := range tests {
		got := shouldRoute(pluginapi.ModelRouteRequest{
			SourceFormat:   "responses",
			RequestedModel: tc.model,
			Stream:         true,
			Body:           body,
		})
		if got != tc.want {
			t.Fatalf("shouldRoute(%q) = %v, want %v", tc.model, got, tc.want)
		}
	}
}

func TestExecutorMethodsValidateStreamMode(t *testing.T) {
	rawExecute, err := json.Marshal(rpcExecutorRequest{
		ExecutorRequest: pluginapi.ExecutorRequest{Stream: true},
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	resp, err := execute(rawExecute)
	if err != nil {
		t.Fatalf("execute() error = %v", err)
	}
	if !strings.Contains(string(resp), "executor.execute requires stream=false") {
		t.Fatalf("execute() response = %s", string(resp))
	}

	rawStream, err := json.Marshal(rpcExecutorRequest{
		ExecutorRequest: pluginapi.ExecutorRequest{Stream: false},
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	resp, err = executeStream(rawStream)
	if err != nil {
		t.Fatalf("executeStream() error = %v", err)
	}
	if !strings.Contains(string(resp), "executor.execute_stream requires stream=true") {
		t.Fatalf("executeStream() response = %s", string(resp))
	}
}

func TestDecodeRPCResultUnwrapsEnvelope(t *testing.T) {
	raw, err := okEnvelope(map[string]any{
		"stream_id":   "stream-1",
		"status_code": 200,
	})
	if err != nil {
		t.Fatalf("okEnvelope() error = %v", err)
	}
	out, err := decodeRPCResult(raw)
	if err != nil {
		t.Fatalf("decodeRPCResult() error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if payload["stream_id"] != "stream-1" {
		t.Fatalf("stream_id = %#v, want stream-1", payload["stream_id"])
	}
}

func TestDecodeRPCResultReturnsEnvelopeError(t *testing.T) {
	_, err := decodeRPCResult(errorEnvelope("rpc_failed", "host model stream bridge is unavailable"))
	if err == nil {
		t.Fatal("decodeRPCResult() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "host model stream bridge is unavailable") {
		t.Fatalf("decodeRPCResult() error = %v", err)
	}
}
