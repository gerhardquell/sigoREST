package sigoengine

import (
	"encoding/json"
	"testing"
)

func TestExtractFinishReason(t *testing.T) {
	body := `{
		"choices": [{
			"finish_reason": "stop",
			"index": 0,
			"message": {"role": "assistant", "content": "Hallo"}
		}],
		"usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
	}`
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatal(err)
	}

	finishReason := ""
	if choices, ok := result["choices"].([]interface{}); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]interface{}); ok {
			if fr, ok := choice["finish_reason"].(string); ok {
				finishReason = fr
			}
		}
	}

	if finishReason != "stop" {
		t.Fatalf("expected finish_reason=stop, got=%q", finishReason)
	}
}
