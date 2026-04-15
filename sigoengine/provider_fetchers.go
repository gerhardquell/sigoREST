//**********************************************************************
//      sigoengine/provider_fetchers.go
//**********************************************************************
// Beschreibung: Dynamischer Modellabruf von Mammouth, Moonshot und ZAI.
//               Fetcher lesen API-Keys direkt aus ENV.
//               Gibt []Model zurück; bei Fehler leerer Slice + Fehler.
//**********************************************************************

package sigoengine

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	mammouthChatEndpoint = "https://api.mammouth.ai/v1/chat/completions"
	moonshotChatEndpoint = "https://api.moonshot.ai/v1/chat/completions"
	zaiChatEndpoint      = "https://api.z.ai/api/paas/v4/chat/completions"
)

// **********************************************************************
// Moonshot — statische Parameter-Tabelle
// Die Moonshot /v1/models API liefert nur Model-IDs, keine Preise/Limits.
// Bekannte Modelle werden angereichert; unbekannte erhalten sichere Defaults.
// ACHTUNG: Preise in USD/1M tokens, Moonshot rechnet in CNY — bitte verifizieren.
var moonshotKnownModels = map[string]Model{
	"moonshot-v1-8k": {
		ID: "moonshot-v1-8k", Shortcode: "moon8k",
		Endpoint: moonshotChatEndpoint, APIKeyEnv: "MOONSHOT_API_KEY",
		MaxInputTokens: 8000, MaxOutputTokens: 4096,
		InputCost: 12.0, OutputCost: 12.0,
		MinTemperature: 0.0, MaxTemperature: 2.0,
	},
	"moonshot-v1-32k": {
		ID: "moonshot-v1-32k", Shortcode: "moon32k",
		Endpoint: moonshotChatEndpoint, APIKeyEnv: "MOONSHOT_API_KEY",
		MaxInputTokens: 32000, MaxOutputTokens: 4096,
		InputCost: 24.0, OutputCost: 24.0,
		MinTemperature: 0.0, MaxTemperature: 2.0,
	},
	"moonshot-v1-128k": {
		ID: "moonshot-v1-128k", Shortcode: "moon128k",
		Endpoint: moonshotChatEndpoint, APIKeyEnv: "MOONSHOT_API_KEY",
		MaxInputTokens: 128000, MaxOutputTokens: 4096,
		InputCost: 60.0, OutputCost: 60.0,
		MinTemperature: 0.0, MaxTemperature: 2.0,
	},
	"kimi-k2.5": {
		ID: "kimi-k2.5", Shortcode: "kimi",
		Endpoint: moonshotChatEndpoint, APIKeyEnv: "MOONSHOT_API_KEY",
		MaxInputTokens: 256000, MaxOutputTokens: 4096,
		InputCost: 0.6, OutputCost: 3.0,
		MinTemperature: 0.0, MaxTemperature: 2.0,
	},
}

// **********************************************************************
// ZAI — statische Fallback-Liste (13 Modelle, Quelle: Mastra, Stand 2026-04)
// Wird verwendet wenn GET https://api.z.ai/api/paas/v4/models keinen
// verwertbaren Response liefert.
var zaiStaticModels = []Model{
	{ID: "glm-4.5",        Shortcode: "glm45",   Endpoint: zaiChatEndpoint, APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 131072, MaxOutputTokens: 4096, InputCost: 0.60, OutputCost: 2.00, MinTemperature: 0.0, MaxTemperature: 2.0},
	{ID: "glm-4.5-air",    Shortcode: "glm45a",  Endpoint: zaiChatEndpoint, APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 131072, MaxOutputTokens: 4096, InputCost: 0.20, OutputCost: 1.00, MinTemperature: 0.0, MaxTemperature: 2.0},
	{ID: "glm-4.5-flash",  Shortcode: "glm45f",  Endpoint: zaiChatEndpoint, APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 131072, MaxOutputTokens: 4096, InputCost: 0.00, OutputCost: 0.00, MinTemperature: 0.0, MaxTemperature: 2.0},
	{ID: "glm-4.5v",       Shortcode: "glm45v",  Endpoint: zaiChatEndpoint, APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 65536,  MaxOutputTokens: 4096, InputCost: 0.60, OutputCost: 2.00, MinTemperature: 0.0, MaxTemperature: 2.0},
	{ID: "glm-4.6",        Shortcode: "glm46",   Endpoint: zaiChatEndpoint, APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 204800, MaxOutputTokens: 4096, InputCost: 0.60, OutputCost: 2.00, MinTemperature: 0.0, MaxTemperature: 2.0},
	{ID: "glm-4.6v",       Shortcode: "glm46v",  Endpoint: zaiChatEndpoint, APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 131072, MaxOutputTokens: 4096, InputCost: 0.30, OutputCost: 0.90, MinTemperature: 0.0, MaxTemperature: 2.0},
	{ID: "glm-4.7",        Shortcode: "glm47",   Endpoint: zaiChatEndpoint, APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 204800, MaxOutputTokens: 4096, InputCost: 0.60, OutputCost: 2.00, MinTemperature: 0.0, MaxTemperature: 2.0},
	{ID: "glm-4.7-flash",  Shortcode: "glm47f",  Endpoint: zaiChatEndpoint, APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 204800, MaxOutputTokens: 4096, InputCost: 0.00, OutputCost: 0.00, MinTemperature: 0.0, MaxTemperature: 2.0},
	{ID: "glm-4.7-flashx", Shortcode: "glm47fx", Endpoint: zaiChatEndpoint, APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 204800, MaxOutputTokens: 4096, InputCost: 0.07, OutputCost: 0.40, MinTemperature: 0.0, MaxTemperature: 2.0},
	{ID: "glm-5",          Shortcode: "glm5",    Endpoint: zaiChatEndpoint, APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 204800, MaxOutputTokens: 4096, InputCost: 1.00, OutputCost: 3.00, MinTemperature: 0.0, MaxTemperature: 2.0},
	{ID: "glm-5-turbo",    Shortcode: "glm5t",   Endpoint: zaiChatEndpoint, APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 204800, MaxOutputTokens: 4096, InputCost: 1.00, OutputCost: 4.00, MinTemperature: 0.0, MaxTemperature: 2.0},
	{ID: "glm-5.1",        Shortcode: "glm51",   Endpoint: zaiChatEndpoint, APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 204800, MaxOutputTokens: 4096, InputCost: 1.00, OutputCost: 4.00, MinTemperature: 0.0, MaxTemperature: 2.0},
	{ID: "glm-5v-turbo",   Shortcode: "glm5vt",  Endpoint: zaiChatEndpoint, APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 204800, MaxOutputTokens: 4096, InputCost: 1.00, OutputCost: 4.00, MinTemperature: 0.0, MaxTemperature: 2.0},
}

// **********************************************************************
// generateProviderShortcode erzeugt einen kurzen eindeutigen Shortcode.
// Beispiel: "gpt-4.1-mini" → "gpt41mi"; bei Kollision → "gpt415"
func generateProviderShortcode(id string, used map[string]bool) string {
	clean := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return -1
	}, strings.ToLower(id))

	candidate := clean
	if len(candidate) > 7 {
		candidate = candidate[:7]
	}
	if !used[candidate] {
		return candidate
	}
	base := candidate
	if len(base) > 5 {
		base = base[:5]
	}
	for i := 2; i < 100; i++ {
		c := fmt.Sprintf("%s%d", base, i)
		if !used[c] {
			return c
		}
	}
	return id
}

// **********************************************************************
// FetchMammouthModels ruft https://api.mammouth.ai/public/models ab.
// Kein API-Key nötig (öffentlicher Endpoint).
// Unterstützt zwei Response-Formate: Array oder {"data": [...]}
func FetchMammouthModels() ([]Model, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get("https://api.mammouth.ai/public/models")
	if err != nil {
		return nil, fmt.Errorf("mammouth: GET /public/models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mammouth: /public/models returned HTTP %d", resp.StatusCode)
	}

	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("mammouth: invalid JSON: %w", err)
	}

	models, err := parseMammouthResponse(raw)
	if err != nil {
		return nil, err
	}
	LogInfo("Mammouth-Modelle geladen", map[string]interface{}{"count": len(models)})
	return models, nil
}

// mammouthModel deckt die bekannten Feldnamen beider API-Formate ab.
type mammouthModel struct {
	ID string `json:"id"`
	// Kontextfenster (mögliche Feldnamen)
	ContextWindow int `json:"context_window"`
	MaxContext    int `json:"max_context"`
	// Max Output (mögliche Feldnamen)
	MaxOutputTokens int `json:"max_output_tokens"`
	MaxOutput       int `json:"max_output"`
	// Preise (mögliche Feldnamen, $/1M tokens)
	InputPricePerMillion  float64 `json:"input_price_per_million"`
	OutputPricePerMillion float64 `json:"output_price_per_million"`
	InputCost             float64 `json:"input_cost"`
	OutputCost            float64 `json:"output_cost"`
}

func parseMammouthResponse(raw json.RawMessage) ([]Model, error) {
	// Versuche Array-Format: [{"id": "..."}, ...]
	var arr []mammouthModel
	if err := json.Unmarshal(raw, &arr); err == nil && len(arr) > 0 {
		return convertMammouthModels(arr), nil
	}

	// Versuche OpenAI-Format: {"data": [...], "object": "list"}
	var wrapper struct {
		Data []mammouthModel `json:"data"`
	}
	if err := json.Unmarshal(raw, &wrapper); err == nil && len(wrapper.Data) > 0 {
		return convertMammouthModels(wrapper.Data), nil
	}

	return nil, fmt.Errorf("mammouth: unbekanntes Response-Format (weder Array noch {data:[]})")
}

func convertMammouthModels(items []mammouthModel) []Model {
	used := make(map[string]bool)
	var result []Model
	for _, m := range items {
		if m.ID == "" {
			continue
		}
		maxIn := firstNonZero(m.ContextWindow, m.MaxContext)
		maxOut := firstNonZero(m.MaxOutputTokens, m.MaxOutput)
		inCost := firstNonZeroFloat(m.InputPricePerMillion, m.InputCost)
		outCost := firstNonZeroFloat(m.OutputPricePerMillion, m.OutputCost)

		sc := generateProviderShortcode(m.ID, used)
		used[sc] = true

		result = append(result, Model{
			ID:              m.ID,
			Shortcode:       sc,
			Endpoint:        mammouthChatEndpoint,
			APIKeyEnv:       "MAMMOUTH_API_KEY",
			MaxInputTokens:  maxIn,
			MaxOutputTokens: maxOut,
			InputCost:       inCost,
			OutputCost:      outCost,
			MinTemperature:  0.0,
			MaxTemperature:  2.0,
		})
	}
	return result
}

func firstNonZero(vals ...int) int {
	for _, v := range vals {
		if v != 0 {
			return v
		}
	}
	return 0
}

func firstNonZeroFloat(vals ...float64) float64 {
	for _, v := range vals {
		if v != 0 {
			return v
		}
	}
	return 0
}

// **********************************************************************
// FetchMoonshotModels ruft https://api.moonshot.ai/v1/models ab.
// API-Key aus ENV: MOONSHOT_API_KEY (Bearer Token).
// OpenAI-Format: Response enthält nur Model-IDs, keine Preise.
// Bekannte Modelle werden aus moonshotKnownModels angereichert.
func FetchMoonshotModels() ([]Model, error) {
	apiKey := os.Getenv("MOONSHOT_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("moonshot: MOONSHOT_API_KEY nicht gesetzt")
	}

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodGet, "https://api.moonshot.ai/v1/models", nil)
	if err != nil {
		return nil, fmt.Errorf("moonshot: Request-Erstellung fehlgeschlagen: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("moonshot: GET /v1/models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("moonshot: /v1/models returned HTTP %d", resp.StatusCode)
	}

	var listResp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		return nil, fmt.Errorf("moonshot: invalid JSON: %w", err)
	}

	used := make(map[string]bool)
	var result []Model

	for _, item := range listResp.Data {
		if item.ID == "" {
			continue
		}
		if known, ok := moonshotKnownModels[item.ID]; ok {
			result = append(result, known)
			used[known.Shortcode] = true
		} else {
			// Unbekanntes Moonshot-Modell: generiere Shortcode, verwende sichere Defaults
			sc := generateProviderShortcode(item.ID, used)
			used[sc] = true
			result = append(result, Model{
				ID:              item.ID,
				Shortcode:       sc,
				Endpoint:        moonshotChatEndpoint,
				APIKeyEnv:       "MOONSHOT_API_KEY",
				MaxInputTokens:  128000,
				MaxOutputTokens: 4096,
				MinTemperature:  0.0,
				MaxTemperature:  2.0,
			})
		}
	}

	// Fallback: API liefert keine Modelle → statische bekannte Liste
	if len(result) == 0 {
		LogWarn("Moonshot /v1/models leer, verwende statische Liste")
		for _, m := range moonshotKnownModels {
			result = append(result, m)
		}
	}

	LogInfo("Moonshot-Modelle geladen", map[string]interface{}{"count": len(result)})
	return result, nil
}
