package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type usageTotals struct {
	InputTokens     int
	OutputTokens    int
	TotalTokens     int
	CachedTokens    *int
	ReasoningTokens int
}

type roundUsage struct {
	InputTokens     int
	OutputTokens    int
	TotalTokens     int
	CachedTokens    *int
	ReasoningTokens int
}

type bufferedEntry struct {
	UpstreamIndex any
	ItemType      string
	Events        []map[string]any
	Item          map[string]any
}

type roundState struct {
	index        int
	responseID   string
	baseResponse map[string]any
	terminal     map[string]any
	usage        roundUsage
	hasUsage     bool
	reasoning    []map[string]any
	buffered     []*bufferedEntry
	itemKind     map[string]string
	itemIndexMap map[string]int
	sawDone      bool
	sawTerminal  bool
}

func runFoldedExecution(ctx context.Context, req pluginapi.ExecutorRequest, hostCallbackID, pluginStreamID string) (runErr error) {
	body, err := executorPayloadBody(req)
	if err != nil {
		return err
	}
	baseBody, ok := parseJSONObject(body)
	if !ok {
		return fmt.Errorf("request body is not a JSON object")
	}
	if !shouldRoute(pluginapi.ModelRouteRequest{
		SourceFormat:   req.SourceFormat,
		RequestedModel: req.Model,
		Stream:         true,
		Body:           body,
	}) {
		return forwardHostStream(ctx, req, hostCallbackID, pluginStreamID)
	}

	tracker := processStats.begin(req.Model)
	totalUsage := usageTotals{}
	completed := false
	outcomeReason := ""
	defer func() {
		if recovered := recover(); recovered != nil {
			tracker.finish(totalUsage, false, "panic", fmt.Errorf("folded execution panic"))
			panic(recovered)
		}
		tracker.finish(totalUsage, completed, outcomeReason, runErr)
	}()

	cfg := loadedConfig()
	origInput := cloneValue(baseBody["input"])
	replayTail := make([]any, 0, 8)
	finalOutput := make([]any, 0, 8)
	roundSummaries := make([]map[string]any, 0, cfg.MaxContinue+1)
	var firstUsage *roundUsage
	seq := 0
	dsOI := 0
	var baseResponse map[string]any
	var billedUsage map[string]any

	currentBody := body
	for roundNo := 1; ; roundNo++ {
		resp, err := openHostModelStream(req, hostCallbackID, currentBody, true)
		if err != nil {
			if roundNo > 1 {
				outcomeReason = "upstream_error"
				return emitUpstreamIncomplete(pluginStreamID, baseResponse, finalOutput, firstUsage, totalUsage, seq, roundSummaries)
			}
			return err
		}
		if resp.StatusCode >= 400 {
			_ = closeHostModelStream(resp.StreamID)
			if roundNo > 1 {
				outcomeReason = "upstream_error"
				return emitUpstreamIncomplete(pluginStreamID, baseResponse, finalOutput, firstUsage, totalUsage, seq, roundSummaries)
			}
			return fmt.Errorf("host model status %d", resp.StatusCode)
		}
		if strings.TrimSpace(resp.StreamID) == "" {
			if roundNo > 1 {
				outcomeReason = "upstream_error"
				return emitUpstreamIncomplete(pluginStreamID, baseResponse, finalOutput, firstUsage, totalUsage, seq, roundSummaries)
			}
			return fmt.Errorf("host model stream: empty stream_id")
		}

		state, emitErr := consumeRound(resp.StreamID, roundNo, hostCallbackID, pluginStreamID, &seq, &dsOI, &baseResponse)
		_ = closeHostModelStream(resp.StreamID)
		if emitErr != nil {
			return emitErr
		}

		if state.hasUsage {
			accumulateUsage(&totalUsage, state.usage)
			u := state.usage
			if firstUsage == nil {
				firstUsage = &u
			}
		}
		for _, item := range state.reasoning {
			finalOutput = append(finalOutput, cloneMap(item))
		}
		roundSummaries = append(roundSummaries, map[string]any{
			"round":            roundNo,
			"reasoning_tokens": state.usage.ReasoningTokens,
			"n":                tierValue(state.usage.ReasoningTokens, cfg.TruncationStep),
		})
		hasEncrypted := roundHasEncryptedReasoning(state.reasoning)
		withinCap := cfg.MaxTotalOutputTokens == 0 || totalUsage.OutputTokens < cfg.MaxTotalOutputTokens
		canContinue := state.sawTerminal &&
			shouldContinue(state.usage.ReasoningTokens, cfg) &&
			hasEncrypted &&
			roundNo <= cfg.MaxContinue &&
			withinCap
		logDegradedReasoningDecision(hostCallbackID, req.Model, roundNo, state, cfg, totalUsage, hasEncrypted, withinCap, canContinue)

		if canContinue {
			tracker.recordContinuation()
			for _, item := range state.reasoning {
				replayTail = append(replayTail, cloneMap(item))
			}
			marker := commentaryMessage(cfg.MarkerText)
			replayTail = append(replayTail, marker)
			if cfg.ForwardMarker {
				commentary := forwardCommentaryItem(roundNo, cfg.MarkerText)
				if err := emitSyntheticCommentary(pluginStreamID, commentary, &seq, dsOI); err != nil {
					return err
				}
				dsOI++
				finalOutput = append(finalOutput, commentary)
			}
			nextPayload := buildRoundPayload(baseBody, origInput, replayTail, cfg, true)
			rawNext, err := json.Marshal(nextPayload)
			if err != nil {
				return err
			}
			currentBody = rawNext
			continue
		}

		billedUsage = usageMapFromTotals(totalUsage)
		agentUsageView := agentUsage(firstUsage, totalUsage, state.usage, false)
		if !state.sawTerminal {
			outcomeReason = "upstream_eof"
			event := syntheticIncomplete(
				baseResponse,
				finalOutput,
				agentUsageView,
				seq,
				"upstream_eof",
				roundSummaries,
				billedUsage,
				agentUsageView,
			)
			payload, err := serializeEvent(event)
			if err != nil {
				return err
			}
			return emitPluginStreamChunk(pluginStreamID, payload)
		}

		for _, entry := range state.buffered {
			chunks, err := flushBufferedEntry(entry, dsOI, &seq, cfg)
			if err != nil {
				return err
			}
			for _, chunk := range chunks {
				if err := emitPluginStreamChunk(pluginStreamID, chunk); err != nil {
					return err
				}
			}
			dsOI++
			finalOutput = append(finalOutput, cloneMap(entry.Item))
		}

		finalAgentUsageView := agentUsage(firstUsage, totalUsage, state.usage, true)
		finalStopReason := stopReason(state, cfg, hasEncrypted, withinCap, roundNo)
		terminal := reconstructTerminal(
			state.terminal,
			baseResponse,
			finalOutput,
			finalAgentUsageView,
			seq,
			roundSummaries,
			finalStopReason,
			billedUsage,
			finalAgentUsageView,
		)
		payload, err := serializeEvent(terminal)
		if err != nil {
			return err
		}
		if err := emitPluginStreamChunk(pluginStreamID, payload); err != nil {
			return err
		}
		if state.sawDone {
			if err := emitPluginStreamChunk(pluginStreamID, serializeDone()); err != nil {
				return err
			}
		}
		completed, outcomeReason = terminalStatsOutcome(state.terminal, finalStopReason)
		return nil
	}
}

func consumeRound(streamID string, roundNo int, hostCallbackID string, pluginStreamID string, seq *int, dsOI *int, baseResponse *map[string]any) (roundState, error) {
	state := roundState{
		index:        roundNo,
		itemKind:     map[string]string{},
		itemIndexMap: map[string]int{},
	}
	decoder := &sseDecoder{}
	reads := 0
	payloadBytes := 0
	nonEmptyChunks := 0
	decodedMessages := 0
	eventTypes := make([]string, 0, 8)
	firstChunkKind := ""
	firstChunkLen := 0
	lastChunkKind := ""
	lastChunkLen := 0
	for {
		chunk, err := readHostModelStream(streamID)
		if err != nil {
			return state, err
		}
		reads++
		if len(chunk.Payload) > 0 {
			payloadBytes += len(chunk.Payload)
			nonEmptyChunks++
			kind := classifyStreamChunk(chunk.Payload)
			if firstChunkKind == "" {
				firstChunkKind = kind
				firstChunkLen = len(chunk.Payload)
			}
			lastChunkKind = kind
			lastChunkLen = len(chunk.Payload)
		}
		messages, err := decoder.Feed(chunk.Payload)
		if err != nil {
			return state, err
		}
		decodedMessages += len(messages)
		eventTypes = appendUniqueEventTypes(eventTypes, messages)
		if err := consumeDecodedMessages(&state, messages, roundNo, pluginStreamID, seq, dsOI, baseResponse); err != nil {
			return state, err
		}
		if chunk.Done {
			flushed, err := decoder.Flush()
			if err != nil {
				return state, err
			}
			decodedMessages += len(flushed)
			eventTypes = appendUniqueEventTypes(eventTypes, flushed)
			if err := consumeDecodedMessages(&state, flushed, roundNo, pluginStreamID, seq, dsOI, baseResponse); err != nil {
				return state, err
			}
		}
		if state.sawTerminal || chunk.Done {
			if !state.sawTerminal {
				hostDebugLog(
					hostCallbackID,
					fmt.Sprintf(
						"codexcont stream ended before terminal event round=%d stream_id_present=%t reads=%d payload_bytes=%d non_empty_chunks=%d decoded_messages=%d event_types=%s first_chunk_kind=%s first_chunk_len=%d last_chunk_kind=%s last_chunk_len=%d saw_done=%t chunk_done=%t",
						roundNo,
						strings.TrimSpace(streamID) != "",
						reads,
						payloadBytes,
						nonEmptyChunks,
						decodedMessages,
						strings.Join(eventTypes, ","),
						firstChunkKind,
						firstChunkLen,
						lastChunkKind,
						lastChunkLen,
						state.sawDone,
						chunk.Done,
					),
					nil,
				)
			}
			return state, nil
		}
	}
}

func appendUniqueEventTypes(dst []string, messages []sseMessage) []string {
	for _, msg := range messages {
		if msg.done {
			dst = appendUniqueString(dst, "[DONE]")
			continue
		}
		event, ok := parseJSONObject(msg.data)
		if !ok {
			dst = appendUniqueString(dst, "[invalid-json]")
			continue
		}
		dst = appendUniqueString(dst, toString(event["type"]))
	}
	return dst
}

func appendUniqueString(dst []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return dst
	}
	for _, item := range dst {
		if item == value {
			return dst
		}
	}
	return append(dst, value)
}

func classifyStreamChunk(payload []byte) string {
	trimmed := strings.TrimSpace(string(payload))
	switch {
	case trimmed == "":
		return "empty"
	case strings.HasPrefix(trimmed, "event:"):
		return "event"
	case strings.HasPrefix(trimmed, "data: [DONE]"):
		return "data-done"
	case strings.HasPrefix(trimmed, "data: {"):
		return "data-json"
	case strings.HasPrefix(trimmed, "data:"):
		return "data-other"
	case strings.HasPrefix(trimmed, "{"):
		return "json"
	default:
		return "other"
	}
}

func consumeDecodedMessages(state *roundState, messages []sseMessage, roundNo int, pluginStreamID string, seq *int, dsOI *int, baseResponse *map[string]any) error {
	if state == nil {
		return nil
	}
	for _, msg := range messages {
		if msg.done {
			state.sawDone = true
			continue
		}
		event, ok := parseJSONObject(msg.data)
		if !ok {
			continue
		}
		etype := toString(event["type"])
		switch etype {
		case "response.created", "response.in_progress":
			if roundNo == 1 {
				if etype == "response.created" {
					if resp, ok := event["response"].(map[string]any); ok {
						cloned := cloneMap(resp)
						state.baseResponse = cloned
						*baseResponse = cloned
					}
				}
				event["sequence_number"] = nextSeq(seq)
				payload, err := serializeEvent(event)
				if err != nil {
					return err
				}
				if err := emitPluginStreamChunk(pluginStreamID, payload); err != nil {
					return err
				}
			}
		case "response.completed", "response.failed", "response.incomplete":
			state.terminal = event
			state.sawTerminal = true
			state.usage, state.hasUsage = parseUsage(event)
		default:
			if err := consumeOutputEvent(state, event, pluginStreamID, seq, dsOI); err != nil {
				return err
			}
		}
		if state.sawTerminal {
			return nil
		}
	}
	return nil
}

func roundHasEncryptedReasoning(items []map[string]any) bool {
	for _, item := range items {
		if strings.TrimSpace(toString(item["encrypted_content"])) != "" {
			return true
		}
	}
	return false
}

func consumeOutputEvent(state *roundState, event map[string]any, pluginStreamID string, seq *int, dsOI *int) error {
	eventType := toString(event["type"])
	upKey := oiKey(event["output_index"])
	switch eventType {
	case "response.output_item.added":
		item, _ := event["item"].(map[string]any)
		if toString(item["type"]) == "reasoning" {
			state.itemKind[upKey] = "reasoning"
			state.itemIndexMap[upKey] = *dsOI
			event["output_index"] = *dsOI
			event["sequence_number"] = nextSeq(seq)
			increment(dsOI)
			payload, err := serializeEvent(event)
			if err != nil {
				return err
			}
			return emitPluginStreamChunk(pluginStreamID, payload)
		}
		state.itemKind[upKey] = "buffered"
		state.buffered = append(state.buffered, &bufferedEntry{
			UpstreamIndex: event["output_index"],
			ItemType:      toString(item["type"]),
			Events:        []map[string]any{cloneMap(event)},
			Item:          cloneMap(item),
		})
		return nil
	}

	switch state.itemKind[upKey] {
	case "reasoning":
		if mapped, ok := state.itemIndexMap[upKey]; ok {
			event["output_index"] = mapped
		}
		event["sequence_number"] = nextSeq(seq)
		if eventType == "response.output_item.done" {
			item, _ := event["item"].(map[string]any)
			state.reasoning = append(state.reasoning, cloneMap(item))
		}
		payload, err := serializeEvent(event)
		if err != nil {
			return err
		}
		return emitPluginStreamChunk(pluginStreamID, payload)
	case "buffered":
		entry := findBuffered(state.buffered, upKey)
		if entry != nil {
			entry.Events = append(entry.Events, cloneMap(event))
			if eventType == "response.output_item.done" {
				if item, ok := event["item"].(map[string]any); ok {
					entry.Item = cloneMap(item)
				}
			}
		}
		return nil
	default:
		event["sequence_number"] = nextSeq(seq)
		payload, err := serializeEvent(event)
		if err != nil {
			return err
		}
		return emitPluginStreamChunk(pluginStreamID, payload)
	}
}

func flushBufferedEntry(entry *bufferedEntry, dsOI int, seq *int, cfg pluginConfig) ([][]byte, error) {
	if entry == nil {
		return nil, nil
	}
	if !cfg.RechunkFinalAnswer || entry.ItemType != "message" {
		return rewriteBufferedEvents(entry.Events, dsOI, seq)
	}
	contentIndexes := map[int]struct{}{}
	var text strings.Builder
	for _, event := range entry.Events {
		if toString(event["type"]) == "response.output_text.delta" {
			contentIndexes[toInt(event["content_index"])] = struct{}{}
			text.WriteString(toString(event["delta"]))
		}
	}
	if len(contentIndexes) > 1 {
		return rewriteBufferedEvents(entry.Events, dsOI, seq)
	}
	chunks := make([][]byte, 0, len(entry.Events))
	emitted := false
	for _, original := range entry.Events {
		event := cloneMap(original)
		if toString(event["type"]) == "response.output_text.delta" {
			if !emitted {
				itemID := toString(event["item_id"])
				contentIndex := toInt(event["content_index"])
				fullText := []rune(text.String())
				size := cfg.RechunkSize
				if size <= 0 {
					size = 8
				}
				for offset := 0; offset < len(fullText); offset += size {
					end := offset + size
					if end > len(fullText) {
						end = len(fullText)
					}
					delta := map[string]any{
						"type":            "response.output_text.delta",
						"item_id":         itemID,
						"output_index":    dsOI,
						"content_index":   contentIndex,
						"delta":           string(fullText[offset:end]),
						"sequence_number": nextSeq(seq),
					}
					payload, err := serializeEvent(delta)
					if err != nil {
						return nil, err
					}
					chunks = append(chunks, payload)
				}
				emitted = true
			}
			continue
		}
		if _, ok := event["output_index"]; ok {
			event["output_index"] = dsOI
		}
		event["sequence_number"] = nextSeq(seq)
		payload, err := serializeEvent(event)
		if err != nil {
			return nil, err
		}
		chunks = append(chunks, payload)
	}
	return chunks, nil
}

func rewriteBufferedEvents(events []map[string]any, dsOI int, seq *int) ([][]byte, error) {
	chunks := make([][]byte, 0, len(events))
	for _, original := range events {
		event := cloneMap(original)
		if _, ok := event["output_index"]; ok {
			event["output_index"] = dsOI
		}
		event["sequence_number"] = nextSeq(seq)
		payload, err := serializeEvent(event)
		if err != nil {
			return nil, err
		}
		chunks = append(chunks, payload)
	}
	return chunks, nil
}

func emitSyntheticCommentary(pluginStreamID string, item map[string]any, seq *int, dsOI int) error {
	itemID := toString(item["id"])
	text := ""
	if content, ok := item["content"].([]any); ok && len(content) > 0 {
		if first, ok := content[0].(map[string]any); ok {
			text = toString(first["text"])
		}
	}
	events := []map[string]any{
		{
			"type":         "response.output_item.added",
			"output_index": dsOI,
			"item": map[string]any{
				"id":    itemID,
				"type":  "message",
				"role":  "assistant",
				"phase": "commentary",
			},
		},
		{
			"type":          "response.content_part.added",
			"output_index":  dsOI,
			"item_id":       itemID,
			"content_index": 0,
			"part":          map[string]any{"type": "output_text", "text": ""},
		},
		{
			"type":          "response.output_text.delta",
			"output_index":  dsOI,
			"item_id":       itemID,
			"content_index": 0,
			"delta":         text,
		},
		{
			"type":          "response.output_text.done",
			"output_index":  dsOI,
			"item_id":       itemID,
			"content_index": 0,
			"text":          text,
		},
		{
			"type":          "response.content_part.done",
			"output_index":  dsOI,
			"item_id":       itemID,
			"content_index": 0,
			"part":          map[string]any{"type": "output_text", "text": text},
		},
		{
			"type":         "response.output_item.done",
			"output_index": dsOI,
			"item":         cloneMap(item),
		},
	}
	for _, event := range events {
		event["sequence_number"] = nextSeq(seq)
		payload, err := serializeEvent(event)
		if err != nil {
			return err
		}
		if err := emitPluginStreamChunk(pluginStreamID, payload); err != nil {
			return err
		}
	}
	return nil
}

func forwardCommentaryItem(roundNo int, text string) map[string]any {
	return map[string]any{
		"id":     fmt.Sprintf("msg_continue_%d", roundNo),
		"type":   "message",
		"status": "completed",
		"role":   "assistant",
		"phase":  "commentary",
		"content": []any{
			map[string]any{
				"type": "output_text",
				"text": text,
			},
		},
	}
}

func reconstructTerminal(terminal, baseResponse map[string]any, outputItems []any, usage map[string]any, seq int, rounds []map[string]any, stoppedReason string, billedUsage map[string]any, agentUsageView map[string]any) map[string]any {
	tResp, _ := terminal["response"].(map[string]any)
	resp := cloneMap(baseResponse)
	if len(resp) == 0 {
		resp = cloneMap(tResp)
	}
	resp["output"] = cloneSlice(outputItems)
	if usage != nil {
		resp["usage"] = usage
	}
	resp["status"] = toString(tResp["status"])
	if resp["status"] == "" {
		resp["status"] = "completed"
	}
	if details, ok := tResp["incomplete_details"]; ok {
		resp["incomplete_details"] = details
	}
	withProxyMetadata(resp, rounds, stoppedReason, billedUsage, agentUsageView)
	return map[string]any{
		"type":            toString(terminal["type"]),
		"response":        resp,
		"sequence_number": seq,
	}
}

func syntheticIncomplete(baseResponse map[string]any, outputItems []any, usage map[string]any, seq int, reason string, rounds []map[string]any, billedUsage map[string]any, agentUsageView map[string]any) map[string]any {
	resp := cloneMap(baseResponse)
	resp["output"] = cloneSlice(outputItems)
	if usage != nil {
		resp["usage"] = usage
	}
	resp["status"] = "incomplete"
	resp["incomplete_details"] = map[string]any{"reason": reason}
	withProxyMetadata(resp, rounds, reason, billedUsage, agentUsageView)
	return map[string]any{
		"type":            "response.incomplete",
		"response":        resp,
		"sequence_number": seq,
	}
}

func withProxyMetadata(resp map[string]any, rounds []map[string]any, stoppedReason string, billedUsage map[string]any, agentUsageView map[string]any) {
	metadata, _ := resp["metadata"].(map[string]any)
	metadata = cloneMap(metadata)
	metadata["proxy_rounds"] = cloneSlice(rounds)
	if billedUsage != nil {
		metadata["proxy_billed_usage"] = cloneMap(billedUsage)
	}
	if agentUsageView != nil {
		metadata["proxy_agent_usage"] = cloneMap(agentUsageView)
	}
	if stoppedReason != "" {
		metadata["proxy_stopped_reason"] = stoppedReason
	}
	resp["metadata"] = metadata
}

func parseUsage(terminal map[string]any) (roundUsage, bool) {
	response, _ := terminal["response"].(map[string]any)
	usageMap, _ := response["usage"].(map[string]any)
	if len(usageMap) == 0 {
		return roundUsage{}, false
	}
	u := roundUsage{
		InputTokens:     toInt(usageMap["input_tokens"]),
		OutputTokens:    toInt(usageMap["output_tokens"]),
		TotalTokens:     toInt(usageMap["total_tokens"]),
		ReasoningTokens: extractReasoningTokens(usageMap),
	}
	if inputDetails, ok := usageMap["input_tokens_details"].(map[string]any); ok {
		cached := toInt(inputDetails["cached_tokens"])
		u.CachedTokens = &cached
	}
	return u, true
}

func extractReasoningTokens(usageMap map[string]any) int {
	if outputDetails, ok := usageMap["output_tokens_details"].(map[string]any); ok {
		return toInt(outputDetails["reasoning_tokens"])
	}
	return 0
}

func accumulateUsage(total *usageTotals, round roundUsage) {
	total.InputTokens += round.InputTokens
	total.OutputTokens += round.OutputTokens
	total.TotalTokens += round.TotalTokens
	total.ReasoningTokens += round.ReasoningTokens
	if round.CachedTokens != nil {
		if total.CachedTokens == nil {
			total.CachedTokens = new(int)
		}
		*total.CachedTokens += *round.CachedTokens
	}
}

func usageMapFromTotals(total usageTotals) map[string]any {
	out := map[string]any{
		"input_tokens":  total.InputTokens,
		"output_tokens": total.OutputTokens,
		"total_tokens":  total.TotalTokens,
		"output_tokens_details": map[string]any{
			"reasoning_tokens": total.ReasoningTokens,
		},
	}
	if total.CachedTokens != nil {
		out["input_tokens_details"] = map[string]any{"cached_tokens": *total.CachedTokens}
	}
	return out
}

func agentUsage(first *roundUsage, total usageTotals, final roundUsage, flushedFinal bool) map[string]any {
	if first == nil {
		return nil
	}
	outTokens := total.ReasoningTokens
	if flushedFinal {
		nonReason := final.OutputTokens - final.ReasoningTokens
		if nonReason > 0 {
			outTokens += nonReason
		}
	}
	out := map[string]any{
		"input_tokens":  first.InputTokens,
		"output_tokens": outTokens,
		"total_tokens":  first.InputTokens + outTokens,
		"output_tokens_details": map[string]any{
			"reasoning_tokens": total.ReasoningTokens,
		},
	}
	if first.CachedTokens != nil {
		out["input_tokens_details"] = map[string]any{"cached_tokens": *first.CachedTokens}
	}
	return out
}

func terminalStatsOutcome(terminal map[string]any, stopReason string) (bool, string) {
	switch toString(terminal["type"]) {
	case "response.failed":
		return false, "upstream_failed"
	case "response.incomplete":
		return false, "upstream_incomplete"
	default:
		return true, stopReason
	}
}

func stopReason(state roundState, cfg pluginConfig, hasEncrypted, withinCap bool, roundNo int) string {
	if !isTruncationPattern(state.usage.ReasoningTokens, cfg.TruncationStep) {
		return ""
	}
	switch {
	case !hasEncrypted:
		return "no_encrypted_content"
	case roundNo > cfg.MaxContinue:
		return "max_continue"
	case !withinCap:
		return "max_total_output_tokens"
	default:
		return "tier_out_of_window"
	}
}

func tierValue(tokens, step int) any {
	if !isTruncationPattern(tokens, step) {
		return nil
	}
	return tierN(tokens, step)
}

func findBuffered(entries []*bufferedEntry, upKey string) *bufferedEntry {
	for _, entry := range entries {
		if oiKey(entry.UpstreamIndex) == upKey {
			return entry
		}
	}
	return nil
}

func emitUpstreamIncomplete(pluginStreamID string, baseResponse map[string]any, finalOutput []any, firstUsage *roundUsage, totalUsage usageTotals, seq int, roundSummaries []map[string]any) error {
	billed := usageMapFromTotals(totalUsage)
	agentUsageView := agentUsage(firstUsage, totalUsage, roundUsage{}, false)
	event := syntheticIncomplete(
		baseResponse,
		finalOutput,
		agentUsageView,
		seq,
		"upstream_error",
		roundSummaries,
		billed,
		agentUsageView,
	)
	payload, err := serializeEvent(event)
	if err != nil {
		return err
	}
	return emitPluginStreamChunk(pluginStreamID, payload)
}

func logDegradedReasoningDecision(hostCallbackID, model string, roundNo int, state roundState, cfg pluginConfig, totalUsage usageTotals, hasEncrypted, withinCap, canContinue bool) {
	fields := degradedReasoningLogFields(model, roundNo, state, cfg, totalUsage, hasEncrypted, withinCap, canContinue)
	if fields == nil {
		return
	}
	message := degradedReasoningLogMessage(fields, canContinue)
	hostDebugLog(hostCallbackID, message, fields)
}

func degradedReasoningLogFields(model string, roundNo int, state roundState, cfg pluginConfig, totalUsage usageTotals, hasEncrypted, withinCap, canContinue bool) map[string]any {
	if !isTruncationPattern(state.usage.ReasoningTokens, cfg.TruncationStep) {
		return nil
	}
	fields := map[string]any{
		"plugin_id":                   pluginIdentifier,
		"model":                       strings.TrimSpace(model),
		"round":                       roundNo,
		"reasoning_tokens":            state.usage.ReasoningTokens,
		"truncation_step":             cfg.TruncationStep,
		"truncation_tier":             tierValue(state.usage.ReasoningTokens, cfg.TruncationStep),
		"degraded_reasoning_detected": true,
		"saw_terminal":                state.sawTerminal,
		"has_encrypted_content":       hasEncrypted,
		"within_output_cap":           withinCap,
		"total_output_tokens":         totalUsage.OutputTokens,
		"max_total_output_tokens":     cfg.MaxTotalOutputTokens,
		"max_continue":                cfg.MaxContinue,
		"can_continue":                canContinue,
	}
	if !canContinue {
		if reason := stopReason(state, cfg, hasEncrypted, withinCap, roundNo); reason != "" {
			fields["stop_reason"] = reason
		}
	}
	return fields
}

func degradedReasoningLogMessage(fields map[string]any, canContinue bool) string {
	message := "codexcont detected degraded reasoning pattern; stopping"
	if canContinue {
		message = "codexcont detected degraded reasoning pattern; continuing"
	}
	for _, key := range []string{
		"round",
		"reasoning_tokens",
		"truncation_step",
		"truncation_tier",
		"degraded_reasoning_detected",
		"saw_terminal",
		"has_encrypted_content",
		"within_output_cap",
		"total_output_tokens",
		"max_total_output_tokens",
		"max_continue",
		"can_continue",
		"stop_reason",
	} {
		value, ok := fields[key]
		if !ok {
			continue
		}
		message += fmt.Sprintf(" %s=%v", key, value)
	}
	return message
}

func nextSeq(seq *int) int {
	value := *seq
	*seq = value + 1
	return value
}

func increment(value *int) {
	*value = *value + 1
}
