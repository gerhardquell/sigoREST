package sigoengine

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var csvTestModels = []Model{
	{ID: "zeta-model", Shortcode: "zai-z1", Endpoint: "https://api.z.ai/api/paas/v4/chat/completions",
		APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 128000, MaxOutputTokens: 8192,
		InputCost: 0.6, OutputCost: 2.2, MinTemperature: 0, MaxTemperature: 1},
	{ID: "ci-claude-opus-5", Shortcode: "che-clo5", Endpoint: "https://api.cheaperinference.com/v1/chat/completions",
		APIKeyEnv: "OMNIROUTE_API_KEY", MaxInputTokens: 200000, MaxOutputTokens: 32000,
		InputCost: 5, OutputCost: 25, CachedInputCost: 1.25, MinTemperature: 0, MaxTemperature: 1,
		RequiresCompletionTokens: true, UpstreamID: "claude-opus-5"},
	{ID: "ollama-llama3", Shortcode: "ollama-llama3", Endpoint: "http://localhost:11434/v1/chat/completions"},
}

// TestWriteModelsCSV_HeaderIsComment: Kopfzeile muss mit '#' beginnen,
// sonst liest loadModelsFromCSV sie als Modell "id" ein.
func TestWriteModelsCSV_HeaderIsComment(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteModelsCSV(&buf, csvTestModels); err != nil {
		t.Fatal(err)
	}
	first := strings.SplitN(buf.String(), "\n", 2)[0]
	if !strings.HasPrefix(first, "# id;shortcode;endpoint;apikey;") {
		t.Fatalf("Kopfzeile = %q", first)
	}
}

// TestWriteModelsCSV_SortedAndProvider: sortiert nach Provider, dann Shortcode;
// Provider-Spalten aus ResolveProvider.
func TestWriteModelsCSV_SortedAndProvider(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteModelsCSV(&buf, csvTestModels); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")[1:]
	if len(lines) != 3 {
		t.Fatalf("erwartet 3 Datenzeilen, bekommen %d:\n%s", len(lines), buf.String())
	}
	wantPrefix := []string{"ci-claude-opus-5;", "ollama-llama3;", "zeta-model;"} // cheaperinference < ollama < zai
	for i, p := range wantPrefix {
		if !strings.HasPrefix(lines[i], p) {
			t.Errorf("Zeile %d = %q, erwartet Präfix %q", i, lines[i], p)
		}
	}
	if !strings.HasSuffix(lines[0], ";cheaperinference;cheap;claude-opus-5;1.25") {
		t.Errorf("Provider/UpstreamID/CachedInputCost fehlen: %q", lines[0])
	}
}

// TestWriteModelsCSV_RoundTrip: Ausgabe ist als CLI-models.csv wieder
// einlesbar, inkl. UpstreamID (sonst geht ci-* mit falschem model-Feld raus).
func TestWriteModelsCSV_RoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteModelsCSV(&buf, csvTestModels); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "models.csv")
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := loadModelsFromCSV(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(csvTestModels) {
		t.Fatalf("erwartet %d Modelle, bekommen %d", len(csvTestModels), len(got))
	}
	byID := map[string]Model{}
	for _, m := range got {
		byID[m.ID] = m
	}
	for _, want := range csvTestModels {
		if byID[want.ID] != want {
			t.Errorf("Round-Trip %s:\n got  %+v\n want %+v", want.ID, byID[want.ID], want)
		}
	}
}
