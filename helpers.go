package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

func okEnvelope(v any) ([]byte, error) {
	result, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.Marshal(envelope{OK: true, Result: result})
}

func errorEnvelope(code, msg string) []byte {
	raw, _ := json.Marshal(envelope{
		OK: false,
		Error: &envelopeError{
			Code:    code,
			Message: msg,
		},
	})
	return raw
}

func decodeRPCResult(raw []byte) ([]byte, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("rpc envelope is empty")
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	if !env.OK {
		if env.Error != nil {
			message := strings.TrimSpace(env.Error.Message)
			if message == "" {
				message = strings.TrimSpace(env.Error.Code)
			}
			if message != "" {
				return nil, fmt.Errorf("%s", message)
			}
		}
		return nil, fmt.Errorf("rpc call failed")
	}
	if len(env.Result) == 0 {
		return nil, fmt.Errorf("rpc result is empty")
	}
	return append([]byte(nil), env.Result...), nil
}

func parseJSONObject(raw []byte) (map[string]any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, false
	}
	return out, true
}

func cloneMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return map[string]any{}
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = cloneValue(value)
	}
	return dst
}

func cloneSlice[T any](src []T) []T {
	if len(src) == 0 {
		return nil
	}
	dst := make([]T, len(src))
	copy(dst, src)
	return dst
}

func cloneValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return cloneMap(v)
	case []any:
		out := make([]any, len(v))
		for i := range v {
			out[i] = cloneValue(v[i])
		}
		return out
	default:
		return v
	}
}

func toString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		return ""
	}
}

func toInt(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		i, _ := v.Int64()
		return int(i)
	default:
		return 0
	}
}

func oiKey(value any) string {
	return fmt.Sprintf("%v", value)
}
