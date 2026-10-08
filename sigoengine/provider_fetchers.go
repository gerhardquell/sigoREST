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
	"strconv"
	"strings"
	"time"
)

const (
	mammouthChatEndpoint = "https://api.mammouth.ai/v1/chat/completions"
	moonshotChatEndpoint = "https://api.moonshot.ai/v1/chat/completions"
	zaiChatEndpoint      = "https://api.z.ai/api/paas/v4/chat/completions"
	// ACHTUNG: offizielle Domain ist api.longcat.chat, nicht api.longcat.ai
	// (TODO 20260830 nannte fälschlich .ai — per Doku-Recherche korrigiert).
	longcatChatEndpoint          = "https://api.longcat.chat/openai/v1/chat/completions"
	cheaperinferenceChatEndpoint = "https://api.cheaperinference.com/v1/chat/completions"
	openrouterChatEndpoint       = "https://openrouter.ai/api/v1/chat/completions"
)

// Provider-Model-Listen-Endpoints (GET, kostenlos — keine Token-Billing).
// Genutzt von ProbeProviderModelList für Health-Checks, statt eines
// Chat-Completion-"ping"-Requests der Input-Token kosten verursacht.
const (
	mammouthModelsEndpoint = "https://api.mammouth.ai/public/models"     // key-less
	moonshotModelsEndpoint = "https://api.moonshot.ai/v1/models"         // Bearer
	zaiModelsEndpoint      = "https://api.z.ai/api/paas/v4/models"       // Bearer
	longcatModelsEndpoint  = "https://api.longcat.chat/openai/v1/models" // Bearer

	cheaperinferenceModelsEndpoint = "https://api.cheaperinference.com/v1/models" // Bearer
	openrouterModelsEndpoint       = "https://openrouter.ai/api/v1/models"        // öffentlich, Bearer optional
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
		// Thinking-Modell: Moonshot akzeptiert nur temperature=1.
		// Min==Max signalisiert "fixed temperature" → main.go erzwingt den Wert.
		MinTemperature: 1.0, MaxTemperature: 1.0,
	},
}

// **********************************************************************
// ZAI — statische Fallback-Liste (13 Modelle, Quelle: Mastra, Stand 2026-04)
// Wird verwendet wenn GET https://api.z.ai/api/paas/v4/models keinen
// verwertbaren Response liefert.
var zaiStaticModels = []Model{
	{ID: "glm-4.5", Shortcode: "glm45", Endpoint: zaiChatEndpoint, APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 131072, MaxOutputTokens: 4096, InputCost: 0.60, OutputCost: 2.00, MinTemperature: 0.0, MaxTemperature: 2.0},
	{ID: "glm-4.5-air", Shortcode: "glm45a", Endpoint: zaiChatEndpoint, APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 131072, MaxOutputTokens: 4096, InputCost: 0.20, OutputCost: 1.00, MinTemperature: 0.0, MaxTemperature: 2.0},
	{ID: "glm-4.5-flash", Shortcode: "glm45f", Endpoint: zaiChatEndpoint, APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 131072, MaxOutputTokens: 4096, InputCost: 0.00, OutputCost: 0.00, MinTemperature: 0.0, MaxTemperature: 2.0},
	{ID: "glm-4.5v", Shortcode: "glm45v", Endpoint: zaiChatEndpoint, APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 65536, MaxOutputTokens: 4096, InputCost: 0.60, OutputCost: 2.00, MinTemperature: 0.0, MaxTemperature: 2.0},
	{ID: "glm-4.6", Shortcode: "glm46", Endpoint: zaiChatEndpoint, APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 204800, MaxOutputTokens: 4096, InputCost: 0.60, OutputCost: 2.00, MinTemperature: 0.0, MaxTemperature: 2.0},
	{ID: "glm-4.6v", Shortcode: "glm46v", Endpoint: zaiChatEndpoint, APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 131072, MaxOutputTokens: 4096, InputCost: 0.30, OutputCost: 0.90, MinTemperature: 0.0, MaxTemperature: 2.0},
	{ID: "glm-4.7", Shortcode: "glm47", Endpoint: zaiChatEndpoint, APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 204800, MaxOutputTokens: 4096, InputCost: 0.60, OutputCost: 2.00, MinTemperature: 0.0, MaxTemperature: 2.0},
	{ID: "glm-4.7-flash", Shortcode: "glm47f", Endpoint: zaiChatEndpoint, APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 204800, MaxOutputTokens: 4096, InputCost: 0.00, OutputCost: 0.00, MinTemperature: 0.0, MaxTemperature: 2.0},
	{ID: "glm-4.7-flashx", Shortcode: "glm47fx", Endpoint: zaiChatEndpoint, APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 204800, MaxOutputTokens: 4096, InputCost: 0.07, OutputCost: 0.40, MinTemperature: 0.0, MaxTemperature: 2.0},
	{ID: "glm-5", Shortcode: "glm5", Endpoint: zaiChatEndpoint, APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 204800, MaxOutputTokens: 4096, InputCost: 1.00, OutputCost: 3.00, MinTemperature: 0.0, MaxTemperature: 2.0},
	{ID: "glm-5-turbo", Shortcode: "glm5t", Endpoint: zaiChatEndpoint, APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 204800, MaxOutputTokens: 4096, InputCost: 1.00, OutputCost: 4.00, MinTemperature: 0.0, MaxTemperature: 2.0},
	{ID: "glm-5.1", Shortcode: "glm51", Endpoint: zaiChatEndpoint, APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 204800, MaxOutputTokens: 4096, InputCost: 1.00, OutputCost: 4.00, MinTemperature: 0.0, MaxTemperature: 2.0},
	{ID: "glm-5v-turbo", Shortcode: "glm5vt", Endpoint: zaiChatEndpoint, APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 204800, MaxOutputTokens: 4096, InputCost: 1.00, OutputCost: 4.00, MinTemperature: 0.0, MaxTemperature: 2.0},
	{ID: "glm-5.3", Shortcode: "glm53", Endpoint: zaiChatEndpoint, APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 204800, MaxOutputTokens: 4096, InputCost: 1.00, OutputCost: 4.00, MinTemperature: 0.0, MaxTemperature: 2.0},
	{ID: "glm-5.3-flash", Shortcode: "glm53-f", Endpoint: zaiChatEndpoint, APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 204800, MaxOutputTokens: 4096, InputCost: 0.07, OutputCost: 0.40, MinTemperature: 0.0, MaxTemperature: 2.0},
}

// **********************************************************************
// Mammouth — statische Preis-Fallback-Tabelle
// Die Mammouth /public/models API liefert keine Preise. Bekannte Modelle
// werden angereichert; unbekannte bleiben bei 0 (→ $0 in der Kostenrechnung).
// Preise in USD/1M tokens.
var mammouthKnownModels = map[string]Model{
	"gpt-4.1": {
		ID: "gpt-4.1", Shortcode: "gpt41",
		Endpoint: mammouthChatEndpoint, APIKeyEnv: "MAMMOUTH_API_KEY",
		MaxInputTokens: 128000, MaxOutputTokens: 8192,
		InputCost: 2.0, OutputCost: 8.0,
	},
	"gpt-4o": {
		ID: "gpt-4o", Shortcode: "gpt4o",
		Endpoint: mammouthChatEndpoint, APIKeyEnv: "MAMMOUTH_API_KEY",
		MaxInputTokens: 128000, MaxOutputTokens: 16384,
		InputCost: 2.5, OutputCost: 10.0,
	},
	"claude-haiku-4-5": {
		ID: "claude-haiku-4-5", Shortcode: "cl45-h",
		Endpoint: mammouthChatEndpoint, APIKeyEnv: "MAMMOUTH_API_KEY",
		MaxInputTokens: 200000, MaxOutputTokens: 8192,
		InputCost: 0.8, OutputCost: 4.0,
	},
	"claude-sonnet-4-6": {
		ID: "claude-sonnet-4-6", Shortcode: "cl46-s",
		Endpoint: mammouthChatEndpoint, APIKeyEnv: "MAMMOUTH_API_KEY",
		MaxInputTokens: 200000, MaxOutputTokens: 8192,
		InputCost: 3.0, OutputCost: 15.0,
	},
	"claude-opus-4-6": {
		ID: "claude-opus-4-6", Shortcode: "cl46-o",
		Endpoint: mammouthChatEndpoint, APIKeyEnv: "MAMMOUTH_API_KEY",
		MaxInputTokens: 200000, MaxOutputTokens: 8192,
		InputCost: 15.0, OutputCost: 75.0,
	},
	"claude-sonnet-5": {
		ID: "claude-sonnet-5", Shortcode: "cl5-s",
		Endpoint: mammouthChatEndpoint, APIKeyEnv: "MAMMOUTH_API_KEY",
		MaxInputTokens: 200000, MaxOutputTokens: 8192,
		InputCost: 3.0, OutputCost: 15.0,
	},
	"claude-opus-5": {
		ID: "claude-opus-5", Shortcode: "cl5-o",
		Endpoint: mammouthChatEndpoint, APIKeyEnv: "MAMMOUTH_API_KEY",
		MaxInputTokens: 200000, MaxOutputTokens: 8192,
		InputCost: 15.0, OutputCost: 75.0,
	},
	"grok-4.5": {
		ID: "grok-4.5", Shortcode: "grok45",
		Endpoint: mammouthChatEndpoint, APIKeyEnv: "MAMMOUTH_API_KEY",
		MaxInputTokens: 131072, MaxOutputTokens: 32768,
		InputCost: 3.0, OutputCost: 15.0,
	},
	"grok-4.6": {
		ID: "grok-4.6", Shortcode: "grok46",
		Endpoint: mammouthChatEndpoint, APIKeyEnv: "MAMMOUTH_API_KEY",
		MaxInputTokens: 131072, MaxOutputTokens: 32768,
		InputCost: 3.0, OutputCost: 15.0,
	},
	"grok-4.7": {
		ID: "grok-4.7", Shortcode: "grok47",
		Endpoint: mammouthChatEndpoint, APIKeyEnv: "MAMMOUTH_API_KEY",
		MaxInputTokens: 131072, MaxOutputTokens: 32768,
		InputCost: 3.0, OutputCost: 15.0,
	},
	"kimi-k2.5": {
		ID: "kimi-k2.5", Shortcode: "kimi",
		Endpoint: mammouthChatEndpoint, APIKeyEnv: "MAMMOUTH_API_KEY",
		MaxInputTokens: 256000, MaxOutputTokens: 4096,
		InputCost: 0.6, OutputCost: 3.0,
	},
}

// **********************************************************************
// generateProviderShortcode erzeugt einen sprechenden Shortcode.
// Verwendet GenerateShortcode mit strukturiertem Parsing + Cutter-Sanborn.
func generateProviderShortcode(id string, used map[string]bool) string {
	return GenerateShortcode(id, used)
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
	// Tatsächliches Format von /public/models (LiteLLM-Gateway): Preise und
	// Limits verschachtelt unter model_info, Preise in USD pro TOKEN.
	ModelInfo *struct {
		MaxInputTokens     int     `json:"max_input_tokens"`
		MaxOutputTokens    int     `json:"max_output_tokens"`
		InputCostPerToken  float64 `json:"input_cost_per_token"`
		OutputCostPerToken float64 `json:"output_cost_per_token"`
	} `json:"model_info"`
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
		// Live-Werte aus model_info haben Vorrang (USD/Token → $/1M).
		if mi := m.ModelInfo; mi != nil {
			maxIn = firstNonZero(mi.MaxInputTokens, maxIn)
			maxOut = firstNonZero(mi.MaxOutputTokens, maxOut)
			inCost = firstNonZeroFloat(mi.InputCostPerToken*1_000_000, inCost)
			outCost = firstNonZeroFloat(mi.OutputCostPerToken*1_000_000, outCost)
		}

		// Statische Fallback-Tabelle nur noch für Felder, die die API nicht
		// liefert (bis 2026-10-08 las der Fetcher model_info nicht und lief
		// komplett über diese teils veraltete Tabelle).
		if known, ok := mammouthKnownModels[m.ID]; ok {
			if maxIn == 0 {
				maxIn = known.MaxInputTokens
			}
			if maxOut == 0 {
				maxOut = known.MaxOutputTokens
			}
			if inCost == 0 {
				inCost = known.InputCost
			}
			if outCost == 0 {
				outCost = known.OutputCost
			}
		}

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
	apiKey := GetEnvWithFile("MOONSHOT_API_KEY")
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

// **********************************************************************
// FetchZAIModels versucht https://api.z.ai/api/paas/v4/models abzurufen.
// API-Key aus ENV: ZAI_API_KEY (Bearer Token).
// Fallback: zaiStaticModels (13 Modelle) wenn API nicht antwortet oder
// keinen /models-Endpoint hat (nicht dokumentiert).
func FetchZAIModels() ([]Model, error) {
	apiKey := GetEnvWithFile("ZAI_API_KEY")
	if apiKey == "" {
		LogWarn("ZAI_API_KEY nicht gesetzt, verwende statische ZAI-Modelle")
		return zaiStaticModels, nil
	}

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodGet, "https://api.z.ai/api/paas/v4/models", nil)
	if err != nil {
		return zaiStaticModels, nil
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := client.Do(req)
	if err != nil {
		LogInfo("ZAI /models nicht erreichbar, verwende statische Liste", map[string]interface{}{"error": err.Error()})
		return zaiStaticModels, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		LogInfo("ZAI /models nicht verfügbar, verwende statische Liste", map[string]interface{}{"status": resp.StatusCode})
		return zaiStaticModels, nil
	}

	var listResp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil || len(listResp.Data) == 0 {
		LogInfo("ZAI Antwort leer oder ungültig, verwende statische Liste")
		return zaiStaticModels, nil
	}

	// Statische Map für schnellen Lookup
	staticMap := make(map[string]Model, len(zaiStaticModels))
	for _, m := range zaiStaticModels {
		staticMap[m.ID] = m
	}

	used := make(map[string]bool)
	var result []Model
	for _, item := range listResp.Data {
		if item.ID == "" {
			continue
		}
		if known, ok := staticMap[item.ID]; ok {
			result = append(result, known)
			used[known.Shortcode] = true
		} else {
			sc := generateProviderShortcode(item.ID, used)
			used[sc] = true
			result = append(result, Model{
				ID:              item.ID,
				Shortcode:       sc,
				Endpoint:        zaiChatEndpoint,
				APIKeyEnv:       "ZAI_API_KEY",
				MaxInputTokens:  128000,
				MaxOutputTokens: 4096,
				MinTemperature:  0.0,
				MaxTemperature:  2.0,
			})
		}
	}

	LogInfo("ZAI-Modelle geladen (dynamisch)", map[string]interface{}{"count": len(result)})
	return result, nil
}

// **********************************************************************
// Longcat (Meituan) — statische Parameter-Tabelle
// Die Longcat /v1/models API liefert nur {id, object, owned_by}, keine
// Preise/Limits. Bekannte Modelle werden angereichert; unbekannte erhalten
// sichere Defaults. Preise laut models.dev (Stand 2026-08), USD/1M tokens.
var longcatKnownModels = map[string]Model{
	"LongCat-2.0": {
		ID: "LongCat-2.0", Shortcode: "longcat2",
		Endpoint: longcatChatEndpoint, APIKeyEnv: "LONGCAT_API_KEY",
		MaxInputTokens: 1000000, MaxOutputTokens: 131072,
		InputCost: 0.75, OutputCost: 2.95,
		MinTemperature: 0.0, MaxTemperature: 2.0,
	},
}

// **********************************************************************
// FetchLongcatModels ruft https://api.longcat.chat/openai/v1/models ab.
// API-Key aus ENV: LONGCAT_API_KEY (Bearer Token).
// OpenAI-Format: Response enthält nur Model-IDs, keine Preise.
// Bekannte Modelle werden aus longcatKnownModels angereichert.
func FetchLongcatModels() ([]Model, error) {
	apiKey := GetEnvWithFile("LONGCAT_API_KEY")
	if apiKey == "" {
		LogWarn("LONGCAT_API_KEY nicht gesetzt, verwende statische Longcat-Modelle")
		result := make([]Model, 0, len(longcatKnownModels))
		for _, m := range longcatKnownModels {
			result = append(result, m)
		}
		return result, nil
	}

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodGet, longcatModelsEndpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("longcat: Request-Erstellung fehlgeschlagen: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("longcat: GET /v1/models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("longcat: /v1/models returned HTTP %d", resp.StatusCode)
	}

	var listResp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		return nil, fmt.Errorf("longcat: invalid JSON: %w", err)
	}

	used := make(map[string]bool)
	var result []Model

	for _, item := range listResp.Data {
		if item.ID == "" {
			continue
		}
		if known, ok := longcatKnownModels[item.ID]; ok {
			result = append(result, known)
			used[known.Shortcode] = true
		} else {
			// Unbekanntes Longcat-Modell: generiere Shortcode, verwende sichere Defaults
			sc := generateProviderShortcode(item.ID, used)
			used[sc] = true
			result = append(result, Model{
				ID:              item.ID,
				Shortcode:       sc,
				Endpoint:        longcatChatEndpoint,
				APIKeyEnv:       "LONGCAT_API_KEY",
				MaxInputTokens:  128000,
				MaxOutputTokens: 4096,
				MinTemperature:  0.0,
				MaxTemperature:  2.0,
			})
		}
	}

	// Fallback: API liefert keine Modelle → statische bekannte Liste
	if len(result) == 0 {
		LogWarn("Longcat /v1/models leer, verwende statische Liste")
		for _, m := range longcatKnownModels {
			result = append(result, m)
		}
	}

	LogInfo("Longcat-Modelle geladen", map[string]interface{}{"count": len(result)})
	return result, nil
}

// **********************************************************************
// FetchCheaperinferenceModels ruft https://api.cheaperinference.com/v1/models
// ab (OMNIROUTE_API_KEY, Bearer Token). Anders als Moonshot/ZAI/Longcat
// liefert dieser Aggregator Preise + Kontextfenster direkt mit — kein
// statisches Known-Model-Mapping nötig. Die Liste enthält neben Text- auch
// Bild-/Video-Modelle; wir filtern auf type=="text" mit
// endpoint=="/v1/chat/completions".
//
// ID-Präfix "ci-": cheaperinference aggregiert Modelle, die es teils auch
// direkt über Mammouth/Moonshot/ZAI gibt (gleicher Modellname). Ohne Präfix
// würde die spätere Provider-Ladung in loadModelsFromProviders() den
// früheren Eintrag in der ID-Map überschreiben. UpstreamID trägt den
// unpräfixten Original-Namen, den main.go beim Request-Aufbau als
// tatsächliches "model"-Feld verwendet (die API kennt "ci-..." nicht).
func FetchCheaperinferenceModels() ([]Model, error) {
	apiKey := GetEnvWithFile("OMNIROUTE_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("cheaperinference: OMNIROUTE_API_KEY nicht gesetzt")
	}

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodGet, cheaperinferenceModelsEndpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("cheaperinference: Request-Erstellung fehlgeschlagen: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cheaperinference: GET /v1/models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cheaperinference: /v1/models returned HTTP %d", resp.StatusCode)
	}

	var listResp struct {
		Data []struct {
			ID              string `json:"id"`
			Type            string `json:"type"`
			Endpoint        string `json:"endpoint"`
			ContextLength   int    `json:"context_length"`
			MaxOutputTokens int    `json:"max_output_tokens"`
			Pricing         struct {
				InputPerMillion          string `json:"input_per_million"`
				OutputPerMillion         string `json:"output_per_million"`
				CacheReadInputPerMillion string `json:"cache_read_input_per_million"` // deutlich günstiger als InputPerMillion, siehe TODO-20261003-kosten.md Punkt 6
			} `json:"pricing"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		return nil, fmt.Errorf("cheaperinference: invalid JSON: %w", err)
	}

	used := make(map[string]bool)
	var result []Model

	for _, item := range listResp.Data {
		if item.ID == "" || item.Type != "text" || item.Endpoint != "/v1/chat/completions" {
			continue
		}

		inputCost, _ := strconv.ParseFloat(item.Pricing.InputPerMillion, 64)
		outputCost, _ := strconv.ParseFloat(item.Pricing.OutputPerMillion, 64)
		cachedInputCost, _ := strconv.ParseFloat(item.Pricing.CacheReadInputPerMillion, 64)
		sc := "ci-" + generateProviderShortcode(item.ID, used)
		used[sc] = true

		result = append(result, Model{
			ID:              "ci-" + item.ID,
			Shortcode:       sc,
			Endpoint:        cheaperinferenceChatEndpoint,
			APIKeyEnv:       "OMNIROUTE_API_KEY",
			MaxInputTokens:  item.ContextLength,
			MaxOutputTokens: item.MaxOutputTokens,
			InputCost:       inputCost,
			OutputCost:      outputCost,
			CachedInputCost: cachedInputCost,
			MinTemperature:  0.0,
			MaxTemperature:  2.0,
			UpstreamID:      item.ID,
		})
	}

	LogInfo("Cheaperinference-Modelle geladen", map[string]interface{}{"count": len(result)})
	return result, nil
}

// **********************************************************************
// FetchOpenRouterModels ruft https://openrouter.ai/api/v1/models ab
// (OPENROUTER_API_KEY, Bearer Token). Wie cheaperinference ein Aggregator
// mit Preisen + Kontextfenster direkt in der Liste — kein statisches
// Known-Model-Mapping nötig. Preise kommen als USD/Token (String), nicht
// USD/1M wie der Rest der Registry — *1e6 zur Umrechnung.
//
// Kein ID-Präfix nötig: OpenRouter-IDs sind bereits "<provider>/<modell>"
// (z.B. "anthropic/claude-opus-5"), kollidieren also nicht mit den
// unpräfixten IDs der anderen Fetcher, und die API erwartet exakt diese
// ID im "model"-Feld — kein UpstreamID-Mapping wie bei cheaperinference.
// Filter auf architecture.modality mit Text-Output ("->text"), sonst
// landen auch reine Bild-/Audio-Ausgabe-Modelle in der Chat-Liste.
func FetchOpenRouterModels() ([]Model, error) {
	apiKey := GetEnvWithFile("OPENROUTER_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("openrouter: OPENROUTER_API_KEY nicht gesetzt")
	}

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodGet, openrouterModelsEndpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("openrouter: Request-Erstellung fehlgeschlagen: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openrouter: GET /v1/models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openrouter: /v1/models returned HTTP %d", resp.StatusCode)
	}

	var listResp struct {
		Data []struct {
			ID            string `json:"id"`
			ContextLength int    `json:"context_length"`
			Architecture  struct {
				Modality string `json:"modality"`
			} `json:"architecture"`
			TopProvider struct {
				MaxCompletionTokens int `json:"max_completion_tokens"`
			} `json:"top_provider"`
			Pricing struct {
				Prompt         string `json:"prompt"`
				Completion     string `json:"completion"`
				InputCacheRead string `json:"input_cache_read"`
			} `json:"pricing"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		return nil, fmt.Errorf("openrouter: invalid JSON: %w", err)
	}

	used := make(map[string]bool)
	var result []Model

	for _, item := range listResp.Data {
		if item.ID == "" || !strings.HasSuffix(item.Architecture.Modality, "->text") {
			continue
		}
		// ":batch"-Varianten laufen über OpenRouters separate Async-Batch-API
		// (anderer Adapter), nicht über /chat/completions — jeder Call würde
		// hier mit HTTP 404 scheitern ("cannot be used with the
		// chat/completions endpoint"). Andere ":"-Suffixe (":free", ":beta",
		// ":nitro", ":floor", ":extended") sind normale Chat-Completion-
		// Routing-Modifier und bleiben drin.
		if strings.HasSuffix(item.ID, ":batch") {
			continue
		}

		inputCost, _ := strconv.ParseFloat(item.Pricing.Prompt, 64)
		outputCost, _ := strconv.ParseFloat(item.Pricing.Completion, 64)
		cachedInputCost, _ := strconv.ParseFloat(item.Pricing.InputCacheRead, 64)

		maxOutput := item.TopProvider.MaxCompletionTokens
		if maxOutput <= 0 {
			maxOutput = 4096
		}

		// GenerateShortcode erkennt Familien nur am Anfang der ID
		// ("claude-...", "gpt-..."). OpenRouter-IDs sind "<vendor>/<modell>"
		// (z.B. "anthropic/claude-opus-5") — ohne den Vendor-Teil abzuschneiden,
		// matcht nie eine Familie, und alle Modelle eines Vendors kollabieren
		// auf denselben Cutter-Code des Vendor-Namens statt des Modellnamens.
		semanticName := item.ID
		if idx := strings.LastIndex(semanticName, "/"); idx >= 0 {
			semanticName = semanticName[idx+1:]
		}
		sc := generateProviderShortcode(semanticName, used)
		used[sc] = true

		result = append(result, Model{
			ID:              item.ID,
			Shortcode:       sc,
			Endpoint:        openrouterChatEndpoint,
			APIKeyEnv:       "OPENROUTER_API_KEY",
			MaxInputTokens:  item.ContextLength,
			MaxOutputTokens: maxOutput,
			InputCost:       inputCost * 1_000_000,
			OutputCost:      outputCost * 1_000_000,
			CachedInputCost: cachedInputCost * 1_000_000,
			MinTemperature:  0.0,
			MaxTemperature:  2.0,
		})
	}

	LogInfo("OpenRouter-Modelle geladen", map[string]interface{}{"count": len(result)})
	return result, nil
}
