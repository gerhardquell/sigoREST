package sigoengine

import "testing"

func TestMoonshotTempRange(t *testing.T) {
	cases := []struct {
		id       string
		min, max float64
	}{
		{"kimi-k3", 1, 1},
		{"kimi-k2.7-code-highspeed", 1, 1},
		{"moonshot-v1-8k", 0, 2},
	}
	for _, c := range cases {
		min, max := moonshotTempRange(c.id)
		if min != c.min || max != c.max {
			t.Errorf("%s: got %v–%v, want %v–%v", c.id, min, max, c.min, c.max)
		}
	}
}

func TestPriceUnknown(t *testing.T) {
	ms := []Model{
		{ID: "known-a", InputCost: 1, OutputCost: 4},
		{ID: "new"},
		{ID: "known-b", InputCost: 3, OutputCost: 2},
		{ID: "free", InputCost: 0, OutputCost: 0}, // bekannt kostenlos: bleibt 0
	}
	priceUnknown("test", ms, []int{1}, nil)
	if ms[1].InputCost != 3 || ms[1].OutputCost != 4 {
		t.Errorf("new: got %v/%v, want 3/4", ms[1].InputCost, ms[1].OutputCost)
	}
	if ms[3].InputCost != 0 || ms[3].OutputCost != 0 {
		t.Errorf("free wurde verändert: %v/%v", ms[3].InputCost, ms[3].OutputCost)
	}

	// Nur unbekannte Modelle im Fetch → Höchstpreis der Tabelle.
	only := []Model{{ID: "new"}}
	priceUnknown("test", only, []int{0}, []Model{{InputCost: 0.75, OutputCost: 2.95}})
	if only[0].InputCost != 0.75 || only[0].OutputCost != 2.95 {
		t.Errorf("only: got %v/%v, want 0.75/2.95", only[0].InputCost, only[0].OutputCost)
	}
}
