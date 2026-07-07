package main

import (
	"crypto/sha1"
	"encoding/hex"
	"strings"
)

const encryptedInclude = "reasoning.encrypted_content"

func isTruncationPattern(tokens, step int) bool {
	return tokens >= step-2 && (tokens+2)%step == 0
}

func tierN(tokens, step int) int {
	if !isTruncationPattern(tokens, step) {
		return 0
	}
	return (tokens + 2) / step
}

func shouldContinue(tokens int, cfg pluginConfig) bool {
	n := tierN(tokens, cfg.TruncationStep)
	if n == 0 {
		return false
	}
	if n < cfg.MinN {
		return false
	}
	if cfg.MaxN > 0 && n > cfg.MaxN {
		return false
	}
	return true
}

func commentaryMessage(text string) map[string]any {
	return map[string]any{
		"type": "message",
		"role": "assistant",
		"content": []any{
			map[string]any{
				"type": "output_text",
				"text": text,
			},
		},
		"phase": "commentary",
	}
}

func continueCallID(reasoningID string) string {
	sum := sha1.Sum([]byte(reasoningID))
	return "call_" + hex.EncodeToString(sum[:])[:24]
}

func mergeInclude(include any, forceEncrypted bool) []any {
	var out []any
	seen := map[string]struct{}{}
	if list, ok := include.([]any); ok {
		for _, item := range list {
			text := strings.TrimSpace(toString(item))
			if text == "" {
				continue
			}
			if _, exists := seen[text]; exists {
				continue
			}
			seen[text] = struct{}{}
			out = append(out, text)
		}
	}
	if forceEncrypted {
		if _, exists := seen[encryptedInclude]; !exists {
			out = append(out, encryptedInclude)
		}
	}
	return out
}

func buildRoundPayload(baseBody map[string]any, originalInput any, replayTail []any, cfg pluginConfig, dropPreviousResponseID bool) map[string]any {
	body := cloneMap(baseBody)
	body["stream"] = true
	body["input"] = continuedInputValue(originalInput, replayTail)
	include := mergeInclude(baseBody["include"], cfg.ForceIncludeEncrypted)
	if len(include) > 0 {
		body["include"] = include
	}
	if dropPreviousResponseID {
		delete(body, "previous_response_id")
	}
	return body
}

func continuedInputValue(originalInput any, replayTail []any) any {
	tail := cloneSlice(replayTail)
	switch input := cloneValue(originalInput).(type) {
	case []any:
		return append(input, tail...)
	case map[string]any:
		return append([]any{input}, tail...)
	case string:
		text := strings.TrimSpace(input)
		if text == "" {
			return tail
		}
		return append([]any{userInputTextMessage(text)}, tail...)
	case nil:
		return tail
	default:
		return append([]any{input}, tail...)
	}
}

func userInputTextMessage(text string) map[string]any {
	return map[string]any{
		"role": "user",
		"content": []any{
			map[string]any{
				"type": "input_text",
				"text": text,
			},
		},
	}
}

func reasoningEnabled(body map[string]any) bool {
	value, exists := body["reasoning"]
	return !exists || value != false
}
