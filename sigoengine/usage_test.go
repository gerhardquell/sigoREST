package sigoengine

import "testing"

func TestExtractUsageOpenAI(t *testing.T) {
	result := map[string]interface{}{
		"usage": map[string]interface{}{
			"prompt_tokens":     float64(10),
			"completion_tokens": float64(5),
			"total_tokens":      float64(15),
		},
	}
	u := extractUsage(result, "openai")
	if u == nil {
		t.Fatal("expected usage, got nil")
	}
	if u.InputTokens != 10 || u.OutputTokens != 5 || u.TotalTokens != 15 {
		t.Fatalf("unexpected tokens: %+v", u)
	}
}

func TestExtractUsageMissing(t *testing.T) {
	result := map[string]interface{}{}
	u := extractUsage(result, "openai")
	if u != nil {
		t.Fatal("expected nil, got usage")
	}
}

func TestEstimateUsage(t *testing.T) {
	u := EstimateUsage("Hallo Welt.", "Antwort.")
	if u == nil {
		t.Fatal("expected usage, got nil")
	}
	if u.InputTokens < 1 || u.OutputTokens < 1 || u.TotalTokens < 2 {
		t.Fatalf("unexpected tokens: %+v", u)
	}
}

func TestExtractUsageOpenAIDetails(t *testing.T) {
	result := map[string]interface{}{
		"usage": map[string]interface{}{
			"prompt_tokens":     float64(5210),
			"completion_tokens": float64(115),
			"total_tokens":      float64(5325),
			"prompt_tokens_details": map[string]interface{}{
				"cached_tokens": float64(5120),
			},
			"completion_tokens_details": map[string]interface{}{
				"reasoning_tokens": float64(98),
			},
		},
	}
	u := extractUsage(result, "openai")
	if u == nil {
		t.Fatal("expected usage, got nil")
	}
	if u.CachedTokens != 5120 || u.ReasoningTokens != 98 {
		t.Fatalf("unexpected details: %+v", u)
	}
	if u.InputTokens != 5210 || u.OutputTokens != 115 {
		t.Fatalf("unexpected base tokens: %+v", u)
	}
}

func TestExtractUsageAnthropicCacheRead(t *testing.T) {
	result := map[string]interface{}{
		"usage": map[string]interface{}{
			"input_tokens":            float64(10),
			"output_tokens":           float64(5),
			"cache_read_input_tokens": float64(4000),
		},
	}
	u := extractUsage(result, "anthropic")
	if u == nil {
		t.Fatal("expected usage, got nil")
	}
	if u.CachedTokens != 4000 || u.ReasoningTokens != 0 {
		t.Fatalf("unexpected details: %+v", u)
	}
}

func TestExtractUsageNoDetails(t *testing.T) {
	result := map[string]interface{}{
		"usage": map[string]interface{}{
			"prompt_tokens": float64(10), "completion_tokens": float64(5),
		},
	}
	u := extractUsage(result, "openai")
	if u.CachedTokens != 0 || u.ReasoningTokens != 0 {
		t.Fatalf("expected zero details, got %+v", u)
	}
}
