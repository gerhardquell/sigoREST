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
