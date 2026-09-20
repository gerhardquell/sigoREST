//**********************************************************************
//      sigoengine/provider_id_test.go
//**********************************************************************

package sigoengine

import "testing"

func TestProviderCode_KnownProviders(t *testing.T) {
	cases := map[string]string{
		"mammouth":         "mammo",
		"moonshot":         "moons",
		"zai":              "zai__",
		"longcat":          "longc",
		"cheaperinference": "cheap",
		"ollama":           "ollam",
	}
	for provider, want := range cases {
		got := ProviderCode(provider)
		if got != want {
			t.Errorf("ProviderCode(%q) = %q, erwartet %q", provider, got, want)
		}
		if len(got) != 5 {
			t.Errorf("ProviderCode(%q) hat Länge %d, erwartet 5", provider, len(got))
		}
	}
}

func TestProviderCode_UnknownProviderPadsOrTruncatesToFive(t *testing.T) {
	if got := ProviderCode("abc"); got != "abc__" {
		t.Errorf("ProviderCode(abc) = %q, erwartet %q", got, "abc__")
	}
	if got := ProviderCode("abcdefgh"); got != "abcde" {
		t.Errorf("ProviderCode(abcdefgh) = %q, erwartet %q", got, "abcde")
	}
	if got := ProviderCode("abcde"); got != "abcde" {
		t.Errorf("ProviderCode(abcde) = %q, erwartet %q (bereits exakt 5)", got, "abcde")
	}
	if len(ProviderCode("x")) != 5 {
		t.Errorf("ProviderCode(x) hat nicht Länge 5")
	}
}

func TestProviderFromEndpoint(t *testing.T) {
	cases := map[string]string{
		"https://api.mammouth.ai/v1/chat/completions":          "mammouth",
		"https://api.moonshot.ai/v1/chat/completions":          "moonshot",
		"https://api.z.ai/api/paas/v4/chat/completions":        "zai",
		"https://api.longcat.chat/openai/v1/chat/completions":  "longcat",
		"https://api.cheaperinference.com/v1/chat/completions": "cheaperinference",
		"http://localhost:11434/v1/chat/completions":           "ollama",
		"http://127.0.0.1:11434/v1/chat/completions":           "ollama",
		"https://unknown-provider.example.com/v1/chat":         "",
	}
	for endpoint, want := range cases {
		if got := ProviderFromEndpoint(endpoint); got != want {
			t.Errorf("ProviderFromEndpoint(%q) = %q, erwartet %q", endpoint, got, want)
		}
	}
}

func TestProviderFromModelID(t *testing.T) {
	cases := map[string]string{
		"ollama-llama3.3":   "ollama",
		"kimi-k2.5":         "moonshot",
		"glm-5.1":           "zai",
		"longcat-flash":     "longcat",
		"ci-claude-opus-5":  "cheaperinference",
		"gpt-4.1":           "mammouth", // Default-Fallback
		"claude-sonnet-4-6": "mammouth", // Default-Fallback
	}
	for modelID, want := range cases {
		if got := ProviderFromModelID(modelID); got != want {
			t.Errorf("ProviderFromModelID(%q) = %q, erwartet %q", modelID, got, want)
		}
	}
}

func TestResolveProvider_EndpointWinsOverNameHeuristic(t *testing.T) {
	// Endpoint sagt moonshot, Name-Heuristik würde (ohne "kimi"/"glm"/...)
	// auf den mammouth-Default fallen — Endpoint muss gewinnen.
	got := ResolveProvider("https://api.moonshot.ai/v1/chat/completions", "some-custom-model-name")
	if got != "moonshot" {
		t.Errorf("ResolveProvider = %q, erwartet moonshot (Endpoint muss Vorrang vor Namens-Fallback haben)", got)
	}
}

func TestResolveProvider_FallsBackToNameHeuristicWithoutEndpointMatch(t *testing.T) {
	got := ResolveProvider("", "ci-claude-opus-5")
	if got != "cheaperinference" {
		t.Errorf("ResolveProvider = %q, erwartet cheaperinference (Namens-Fallback bei leerem/unbekanntem Endpoint)", got)
	}
}

func TestResolveProvider_ThenProviderCode_RoundTrip(t *testing.T) {
	// End-to-End: genau der Pfad, den main.go/costdb/CLI künftig nutzen.
	provider := ResolveProvider("https://api.z.ai/api/paas/v4/chat/completions", "glm-5.1")
	code := ProviderCode(provider)
	if provider != "zai" || code != "zai__" {
		t.Errorf("ResolveProvider+ProviderCode = (%q, %q), erwartet (zai, zai__)", provider, code)
	}
}
