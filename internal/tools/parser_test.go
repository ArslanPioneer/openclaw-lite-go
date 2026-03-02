package tools

import "testing"

func TestParseToolCallAcceptsMarkdownFence(t *testing.T) {
	raw := "```text\nTOOL_CALL {\"name\":\"web_search\",\"query\":\"NVDA price\"}\n```"

	call, requested, err := ParseToolCall(raw)
	if err != nil {
		t.Fatalf("ParseToolCall() error = %v", err)
	}
	if !requested {
		t.Fatal("expected tool call to be requested")
	}
	if call.Name != "web_search" || call.Query != "NVDA price" {
		t.Fatalf("unexpected call: %+v", call)
	}
}

func TestParseToolCallFindsLineAfterAssistantText(t *testing.T) {
	raw := "I will use a tool now.\nTOOL_CALL {\"name\":\"stock_price\",\"query\":\"NVDA\"}"

	call, requested, err := ParseToolCall(raw)
	if err != nil {
		t.Fatalf("ParseToolCall() error = %v", err)
	}
	if !requested {
		t.Fatal("expected tool call to be requested")
	}
	if call.Name != "stock_price" || call.Query != "NVDA" {
		t.Fatalf("unexpected call: %+v", call)
	}
}

func TestParseToolCallBrokenJSONIsRecoverable(t *testing.T) {
	raw := "TOOL_CALL {\"name\":\"web_search\",\"query\":\"NVDA\""

	_, requested, err := ParseToolCall(raw)
	if !requested {
		t.Fatal("expected requested=true for malformed tool call payload")
	}
	if err == nil {
		t.Fatal("expected parse error for malformed JSON")
	}
}
