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
