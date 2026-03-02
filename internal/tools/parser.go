package tools

import (
	"encoding/json"
	"fmt"
	"strings"
)

const toolCallPrefix = "TOOL_CALL "

func ParseToolCall(raw string) (Call, bool, error) {
	payload, requested, err := extractToolCallPayload(raw)
	if err != nil {
		return Call{}, true, err
	}
	if !requested {
		return Call{}, false, nil
	}

	jsonPayload, err := extractFirstJSONObject(payload)
	if err != nil {
		return Call{}, true, err
	}

	var call Call
	decoder := json.NewDecoder(strings.NewReader(jsonPayload))
	if err := decoder.Decode(&call); err != nil {
		return Call{}, true, fmt.Errorf("parse tool call: %w", err)
	}
	call.Name = strings.TrimSpace(call.Name)
	if call.Name == "" {
		return Call{}, true, fmt.Errorf("tool name is required")
	}
	return call, true, nil
}

func extractToolCallPayload(raw string) (string, bool, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return "", false, nil
	}

	idx := strings.Index(text, toolCallPrefix)
	if idx < 0 {
		return "", false, nil
	}
	payload := strings.TrimSpace(text[idx+len(toolCallPrefix):])
	if payload == "" {
		return "", true, fmt.Errorf("parse tool call: empty payload")
	}
	return payload, true, nil
}

func extractFirstJSONObject(payload string) (string, error) {
	start := strings.Index(payload, "{")
	if start < 0 {
		return "", fmt.Errorf("parse tool call: JSON object not found")
	}

	inString := false
	escaped := false
	depth := 0
	for i := start; i < len(payload); i++ {
		ch := payload[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}

		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return payload[start : i+1], nil
			}
		}
	}

	return "", fmt.Errorf("parse tool call: unexpected end of JSON input")
}
