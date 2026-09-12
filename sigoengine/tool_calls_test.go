package sigoengine

import "testing"

func TestExtractToolCallsOpenAI(t *testing.T) {
	result := map[string]interface{}{
		"choices": []interface{}{
			map[string]interface{}{
				"message": map[string]interface{}{
					"content": nil,
					"tool_calls": []interface{}{
						map[string]interface{}{
							"id":   "call_abc123",
							"type": "function",
							"function": map[string]interface{}{
								"name":      "read_file",
								"arguments": `{"path":"/tmp/x.txt"}`,
							},
						},
					},
				},
			},
		},
	}
	calls := extractToolCalls(result, "openai")
	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(calls))
	}
	if calls[0].ID != "call_abc123" || calls[0].Function.Name != "read_file" {
		t.Fatalf("unexpected tool call: %+v", calls[0])
	}
	if calls[0].Function.Arguments != `{"path":"/tmp/x.txt"}` {
		t.Fatalf("unexpected arguments: %q", calls[0].Function.Arguments)
	}
}

func TestExtractToolCallsOpenAINone(t *testing.T) {
	result := map[string]interface{}{
		"choices": []interface{}{
			map[string]interface{}{
				"message": map[string]interface{}{"content": "Hallo"},
			},
		},
	}
	if calls := extractToolCalls(result, "openai"); calls != nil {
		t.Fatalf("expected nil, got %+v", calls)
	}
}

func TestExtractToolCallsAnthropic(t *testing.T) {
	result := map[string]interface{}{
		"content": []interface{}{
			map[string]interface{}{"type": "text", "text": "Ich schaue nach."},
			map[string]interface{}{
				"type":  "tool_use",
				"id":    "toolu_01",
				"name":  "read_file",
				"input": map[string]interface{}{"path": "/tmp/x.txt"},
			},
		},
	}
	calls := extractToolCalls(result, "anthropic")
	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(calls))
	}
	if calls[0].ID != "toolu_01" || calls[0].Function.Name != "read_file" {
		t.Fatalf("unexpected tool call: %+v", calls[0])
	}
	if calls[0].Function.Arguments != `{"path":"/tmp/x.txt"}` {
		t.Fatalf("unexpected arguments: %q", calls[0].Function.Arguments)
	}
}
