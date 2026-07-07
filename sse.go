package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

type sseMessage struct {
	done bool
	data []byte
}

type sseDecoder struct {
	buf []byte
}

func (d *sseDecoder) Feed(chunk []byte) ([]sseMessage, error) {
	if len(chunk) == 0 {
		return nil, nil
	}
	d.buf = appendSSEChunk(d.buf, chunk)
	d.buf = bytes.ReplaceAll(d.buf, []byte("\r\n"), []byte("\n"))
	d.buf = bytes.ReplaceAll(d.buf, []byte("\r"), []byte("\n"))

	var out []sseMessage
	for {
		idx := bytes.Index(d.buf, []byte("\n\n"))
		if idx < 0 {
			break
		}
		block := append([]byte(nil), d.buf[:idx]...)
		d.buf = append([]byte(nil), d.buf[idx+2:]...)
		msg, ok, err := parseSSEBlock(block)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, msg)
		}
	}
	if responsesSSECanEmitWithoutDelimiter(d.buf) {
		msg, ok, err := parseSSEBlock(append([]byte(nil), d.buf...))
		if err != nil {
			return nil, err
		}
		d.buf = nil
		if ok {
			out = append(out, msg)
		}
	}
	return out, nil
}

func appendSSEChunk(buf []byte, chunk []byte) []byte {
	trimmedChunk := bytes.TrimSpace(chunk)
	if len(buf) > 0 && len(trimmedChunk) > 0 && sseStandaloneFieldChunk(trimmedChunk) {
		if responsesSSECanEmitWithoutDelimiter(buf) {
			buf = append(buf, '\n', '\n')
		} else if !bytes.HasSuffix(buf, []byte("\n")) {
			buf = append(buf, '\n')
		}
	}
	return append(buf, chunk...)
}

func sseStandaloneFieldChunk(chunk []byte) bool {
	switch {
	case bytes.HasPrefix(chunk, []byte("event:")):
		return !bytes.Contains(chunk, []byte("\n"))
	case bytes.HasPrefix(chunk, []byte("data:")):
		value := bytes.TrimSpace(chunk[len("data:"):])
		return bytes.Equal(value, []byte("[DONE]")) || json.Valid(value)
	case bytes.HasPrefix(chunk, []byte(":")):
		return !bytes.Contains(chunk, []byte("\n"))
	default:
		return false
	}
}

func (d *sseDecoder) Flush() ([]sseMessage, error) {
	if len(d.buf) == 0 {
		return nil, nil
	}
	if !responsesSSECanEmitWithoutDelimiter(d.buf) {
		return nil, nil
	}
	msg, ok, err := parseSSEBlock(append([]byte(nil), d.buf...))
	if err != nil {
		return nil, err
	}
	d.buf = nil
	if !ok {
		return nil, nil
	}
	return []sseMessage{msg}, nil
}

func parseSSEBlock(block []byte) (sseMessage, bool, error) {
	lines := bytes.Split(block, []byte("\n"))
	var dataLines [][]byte
	for _, line := range lines {
		if len(line) == 0 || line[0] == ':' {
			continue
		}
		switch {
		case bytes.HasPrefix(line, []byte("data:")):
			value := bytes.TrimSpace(line[len("data:"):])
			dataLines = append(dataLines, append([]byte(nil), value...))
		case bytes.HasPrefix(line, []byte("event:")):
			continue
		default:
			continue
		}
	}
	if len(dataLines) == 0 {
		return sseMessage{}, false, nil
	}
	data := bytes.Join(dataLines, []byte("\n"))
	if bytes.Equal(data, []byte("[DONE]")) {
		return sseMessage{done: true}, true, nil
	}
	if !json.Valid(data) {
		return sseMessage{}, false, fmt.Errorf("invalid SSE JSON payload: %s", string(data))
	}
	return sseMessage{data: data}, true, nil
}

func serializeEvent(event map[string]any) ([]byte, error) {
	raw, err := json.Marshal(event)
	if err != nil {
		return nil, err
	}
	return []byte("data: " + string(raw) + "\n\n"), nil
}

func serializeDone() []byte {
	return []byte("data: [DONE]\n\n")
}

func looksLikeResponsesSSE(payload []byte) bool {
	text := string(payload)
	return strings.Contains(text, `"type":"response.`) ||
		strings.Contains(text, `"type": "response.`) ||
		strings.Contains(text, "event: response.")
}

func responsesSSECanEmitWithoutDelimiter(chunk []byte) bool {
	trimmed := bytes.TrimSpace(chunk)
	if len(trimmed) == 0 || responsesSSENeedsMoreData(trimmed) || !responsesSSEHasField(trimmed, []byte("data:")) {
		return false
	}
	return responsesSSEDataLinesValid(trimmed)
}

func responsesSSENeedsMoreData(chunk []byte) bool {
	trimmed := bytes.TrimSpace(chunk)
	if len(trimmed) == 0 {
		return false
	}
	return responsesSSEHasField(trimmed, []byte("event:")) && !responsesSSEHasField(trimmed, []byte("data:"))
}

func responsesSSEHasField(chunk []byte, prefix []byte) bool {
	s := chunk
	for len(s) > 0 {
		line := s
		if i := bytes.IndexByte(s, '\n'); i >= 0 {
			line = s[:i]
			s = s[i+1:]
		} else {
			s = nil
		}
		line = bytes.TrimSpace(line)
		if bytes.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

func responsesSSEDataLinesValid(chunk []byte) bool {
	s := chunk
	for len(s) > 0 {
		line := s
		if i := bytes.IndexByte(s, '\n'); i >= 0 {
			line = s[:i]
			s = s[i+1:]
		} else {
			s = nil
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 || !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		data := bytes.TrimSpace(line[len("data:"):])
		if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
			continue
		}
		if !json.Valid(data) {
			return false
		}
	}
	return true
}
