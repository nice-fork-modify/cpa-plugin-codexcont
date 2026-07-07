package main

import "encoding/json"

func jsonUnmarshal[T any](raw []byte, out *T) error {
	return json.Unmarshal(raw, out)
}

func jsonMarshal(v any) ([]byte, error) {
	return json.Marshal(v)
}
