package sigoengine

import (
	"encoding/json"
	"math"
	"testing"
)

// mammouthPublicModelsSample ist ein echter Ausschnitt von
// https://api.mammouth.ai/public/models (Stand 2026-10-08): Preise und
// Limits stecken im LiteLLM-Format unter "model_info", Preise in USD pro
// Token. Bis dahin las der Fetcher nur nicht existierende Top-Level-Felder
// — claude-opus-4-7 lief mit $0, claude-sonnet-5 mit veralteten statischen
// Preisen (3/15 statt 2/10 $/1M).
const mammouthPublicModelsSample = `{"data":[
{"id":"claude-opus-4-7","object":"model","created":1791462305,"owned_by":"openai","model_info":{"id":"6d4f","max_input_tokens":1000000,"max_output_tokens":128000,"input_cost_per_token":5e-06,"output_cost_per_token":2.5e-05}},
{"id":"claude-sonnet-5","object":"model","created":1791462305,"owned_by":"openai","model_info":{"id":"3984","max_input_tokens":1000000,"max_output_tokens":128000,"input_cost_per_token":2e-06,"output_cost_per_token":1e-05}}
],"object":"list"}`

func TestParseMammouthResponse_UsesModelInfoPricesAndLimits(t *testing.T) {
	models, err := parseMammouthResponse(json.RawMessage(mammouthPublicModelsSample))
	if err != nil {
		t.Fatalf("parseMammouthResponse: %v", err)
	}
	byID := map[string]Model{}
	for _, m := range models {
		byID[m.ID] = m
	}

	cases := []struct {
		id            string
		in, out       float64
		maxIn, maxOut int
	}{
		{"claude-opus-4-7", 5, 25, 1000000, 128000},
		// Live-Preis schlägt die statische mammouthKnownModels-Tabelle.
		{"claude-sonnet-5", 2, 10, 1000000, 128000},
	}
	for _, c := range cases {
		m, ok := byID[c.id]
		if !ok {
			t.Fatalf("%s fehlt im Ergebnis", c.id)
		}
		if math.Abs(m.InputCost-c.in) > 1e-9 || math.Abs(m.OutputCost-c.out) > 1e-9 {
			t.Errorf("%s: Preise %v/%v, want %v/%v $/1M", c.id, m.InputCost, m.OutputCost, c.in, c.out)
		}
		if m.MaxInputTokens != c.maxIn || m.MaxOutputTokens != c.maxOut {
			t.Errorf("%s: Limits %d/%d, want %d/%d", c.id, m.MaxInputTokens, m.MaxOutputTokens, c.maxIn, c.maxOut)
		}
	}
}

// Ohne model_info bleibt die statische Tabelle der Fallback.
func TestParseMammouthResponse_FallsBackToKnownModelsWithoutModelInfo(t *testing.T) {
	models, err := parseMammouthResponse(json.RawMessage(`{"data":[{"id":"gpt-4o"}]}`))
	if err != nil {
		t.Fatalf("parseMammouthResponse: %v", err)
	}
	known := mammouthKnownModels["gpt-4o"]
	if len(models) != 1 || models[0].InputCost != known.InputCost || models[0].OutputCost != known.OutputCost {
		t.Fatalf("erwartet statische gpt-4o-Preise %v/%v, bekommen %+v", known.InputCost, known.OutputCost, models)
	}
}
