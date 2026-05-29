//**********************************************************************
//      sigoengine/shortcode_test.go
//**********************************************************************
//  Autor    : Gerhard Quell - gquell@skequell.de
//  CoAutor  : claude sonnet 4.6
//  Copyright: 2026 Gerhard Quell - SKEQuell
//  Erstellt : 20260513
//**********************************************************************

package sigoengine

import (
	"fmt"
	"testing"
)

func TestGenerateShortcode(t *testing.T) {
	tests := []struct {
		model string
		want  string
	}{
		// GPT
		{"gpt-5.1", "gpt51"},
		{"gpt-5.1-codex", "gpt51-cx"},
		{"gpt-5.1-codex-mini", "gpt51-cxm"},
		{"gpt-5.1-codex-max", "gpt51-cxm01"},
		{"gpt-4.1", "gpt41"},
		{"gpt-4.1-mini", "gpt41-m"},
		{"gpt-4.1-nano", "gpt41-n"},
		{"gpt-4o", "gpt4o"},
		{"gpt-5-mini", "gpt5-m"},
		{"gpt-5-nano", "gpt5-n"},
		{"gpt-5.5", "gpt55"},
		// Claude
		{"claude-sonnet-4-5", "cl45-s"},
		{"claude-sonnet-4-6", "cl46-s"},
		{"claude-opus-4-5", "cl45-o"},
		{"claude-opus-4-6", "cl46-o"},
		{"claude-opus-4.7", "cl47-o"},
		{"claude-haiku-4-5", "cl45-h"},
		// Gemini
		{"gemini-2.5-flash", "gem25-f"},
		{"gemini-2.5-pro", "gem25-p"},
		{"gemini-2.5-flash-lite", "gem25-flt"},
		{"gemini-2.5-flash-image", "gem25-fimg"},
		{"gemini-3-flash-preview", "gem3-fpv"},
		// DeepSeek
		{"deepseek-v4-flash", "ds4-f"},
		{"deepseek-v4-pro", "ds4-p"},
		{"deepseek-v3.2", "ds32"},
		{"deepseek-r1-0528", "dsr10528"},
		// Grok
		{"grok-4-0709", "grok40709"},
		{"grok-4-1-fast", "grok41-fst"},
		{"grok-4.20-beta", "grok420-b"},
		// Kimi/Moonshot
		{"kimi-k2.5", "kimik25"},
		{"moonshot-v1-8k", "moon18"},
		{"moonshot-v1-128k", "moon1128"},
		{"moonshot-v1-8k-vision-preview", "moon18-vispv"},
		// GLM
		{"glm-4.5", "glm45"},
		{"glm-4.5-air", "glm45-a"},
		{"glm-5-turbo", "glm5-t"},
		// Embeddings
		{"text-embedding-3-small", "emb3-s"},
		{"text-embedding-3-large", "emb3-l"},
		// Sonstige
		{"llama-4-maverick", "llama4-mv"},
		{"llama-4-scout", "llama4-sc"},
		{"qwen3-coder", "qwen3-c52"},
		{"mistral-large-3", "mist3-l"},
		{"minimax-m2.7", "mmxm27"},
		{"sonar-pro", "son-p"},
		{"codestral-2508", "cstr2508"},
		{"devstral-2512", "dvstr2512"},
	}

	used := make(map[string]bool)
	for _, tt := range tests {
		got := GenerateShortcode(tt.model, used)
		if got != tt.want {
			t.Errorf("GenerateShortcode(%q) = %q, want %q", tt.model, got, tt.want)
		}
	}
}

func TestGenerateShortcodeNoCollisions(t *testing.T) {
	models := []string{
		"gpt-5.1", "gpt-5.1-codex", "gpt-5.1-codex-max", "gpt-5.1-codex-mini",
		"gpt-5.1-chat", "gpt-5.2", "gpt-5.2-chat", "gpt-5.2-codex",
		"gpt-5.3-chat", "gpt-5.3-codex", "gpt-5.4", "gpt-5.4-image-2",
		"gpt-5.4-mini", "gpt-5.4-nano", "gpt-5.5",
		"gpt-5", "gpt-5-mini", "gpt-5-nano",
		"gpt-4.1", "gpt-4.1-mini", "gpt-4.1-nano", "gpt-4o",
		"claude-sonnet-4-5", "claude-sonnet-4-6", "claude-opus-4-5",
		"claude-opus-4-6", "claude-opus-4.7", "claude-haiku-4-5",
		"gemini-2.5-flash", "gemini-2.5-pro", "gemini-2.5-flash-lite",
		"gemini-3-flash-preview", "gemini-3-pro-image-preview",
		"deepseek-v4-flash", "deepseek-v4-pro", "deepseek-v3.2",
		"grok-4-0709", "grok-4-1-fast", "grok-4-fast-non-reasoning",
		"kimi-k2.5", "glm-4.5", "glm-4.5-air", "glm-5-turbo",
		"qwen3-coder", "qwen3.5-9b",
		"text-embedding-3-small", "text-embedding-3-large",
		"llama-4-maverick", "llama-4-scout",
		"mistral-large-3", "minimax-m2.7",
		"sonar-pro", "codestral-2508",
	}

	used := make(map[string]bool)
	shortcodes := make(map[string]string) // shortcode → modelID

	for _, m := range models {
		sc := GenerateShortcode(m, used)
		if existing, ok := shortcodes[sc]; ok {
			t.Errorf("Kollision! %q und %q → beide %q", existing, m, sc)
		}
		shortcodes[sc] = m
	}

	fmt.Printf("  %d Modelle, %d eindeutige Shortcodes – keine Kollisionen\n", len(models), len(shortcodes))
}

func TestCutterCode(t *testing.T) {
	tests := []struct {
		word string
		want string
	}{
		{"schmidt", "S13"},
		{"muller", "M84"},
		{"computer", "C68"},
		{"programmierung", "P85"},
		{"python", "P99"},
		{"data", "D12"},
		{"max", "M01"},
		{"mini", "M47"},
		{"flash", "F32"},
		{"pro", "P78"},
		{"codex", "C52"},
	}

	for _, tt := range tests {
		got := cutterCode(tt.word)
		if got != tt.want {
			t.Errorf("cutterCode(%q) = %q, want %q", tt.word, got, tt.want)
		}
	}
}
