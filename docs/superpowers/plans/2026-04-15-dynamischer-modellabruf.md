# Dynamischer Modellabruf & Erweiterungen — Implementierungsplan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Statische models.csv durch dynamischen API-Abruf von Mammouth, Moonshot, ZAI und Ollama ersetzen; Server-Ping vor jedem API-Call; Custom System-Prompt global und per Request.

**Architecture:** Neue Fetcher-Funktionen in `sigoengine/provider_fetchers.go` (neues File); `PingProvider()` in `sigoengine/engine.go`; `sigoREST/main.go` verliert `loadModels()` + CSV-Embed und gewinnt `loadModelsFromProviders()` + System-Prompt-State.

**Tech Stack:** Go 1.26, `net/http`, `encoding/json`, keine neuen Abhängigkeiten.

---

### Task 1: PingProvider() in sigoengine/engine.go

**Files:**
- Modify: `sigoengine/engine.go` — nach `GetOllamaModels()` (ca. Zeile 479) einfügen

- [ ] **Schritt 1: PingProvider-Funktion einfügen**

Direkt nach der `GetOllamaModels()`-Funktion (Ende ca. Zeile 479) einfügen:

```go
// **********************************************************************
// PingProvider prüft ob ein Provider-Endpoint erreichbar ist.
// Sendet HEAD-Request; jeder HTTP-Response gilt als "erreichbar".
// Timeout: 5 Sekunden.
func PingProvider(endpoint string) error {
	client := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequest(http.MethodHead, endpoint, nil)
	if err != nil {
		return fmt.Errorf("ping: ungültiger Endpoint %q: %w", endpoint, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("ping: %s nicht erreichbar: %w", endpoint, err)
	}
	resp.Body.Close()
	// Jeder HTTP-Response (auch 4xx/5xx) = Server läuft
	return nil
}
```

- [ ] **Schritt 2: Build prüfen**

```bash
go build ./sigoengine/
```
Erwartetes Ergebnis: kein Fehler.

- [ ] **Schritt 3: Commit**

```bash
git add sigoengine/engine.go
git commit -m "feat(sigoengine): add PingProvider() for endpoint health check"
```

---

### Task 2: provider_fetchers.go — Grundgerüst, Hilfsfunktionen, ZAI-Fallback-Daten

**Files:**
- Create: `sigoengine/provider_fetchers.go`

- [ ] **Schritt 1: Datei anlegen**

```go
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
	{ID: "glm-4.5",       Shortcode: "glm45",   Endpoint: zaiChatEndpoint, APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 131072, MaxOutputTokens: 4096, InputCost: 0.60, OutputCost: 2.00, MinTemperature: 0.0, MaxTemperature: 2.0},
	{ID: "glm-4.5-air",   Shortcode: "glm45a",  Endpoint: zaiChatEndpoint, APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 131072, MaxOutputTokens: 4096, InputCost: 0.20, OutputCost: 1.00, MinTemperature: 0.0, MaxTemperature: 2.0},
	{ID: "glm-4.5-flash", Shortcode: "glm45f",  Endpoint: zaiChatEndpoint, APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 131072, MaxOutputTokens: 4096, InputCost: 0.00, OutputCost: 0.00, MinTemperature: 0.0, MaxTemperature: 2.0},
	{ID: "glm-4.5v",      Shortcode: "glm45v",  Endpoint: zaiChatEndpoint, APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 65536,  MaxOutputTokens: 4096, InputCost: 0.60, OutputCost: 2.00, MinTemperature: 0.0, MaxTemperature: 2.0},
	{ID: "glm-4.6",       Shortcode: "glm46",   Endpoint: zaiChatEndpoint, APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 204800, MaxOutputTokens: 4096, InputCost: 0.60, OutputCost: 2.00, MinTemperature: 0.0, MaxTemperature: 2.0},
	{ID: "glm-4.6v",      Shortcode: "glm46v",  Endpoint: zaiChatEndpoint, APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 131072, MaxOutputTokens: 4096, InputCost: 0.30, OutputCost: 0.90, MinTemperature: 0.0, MaxTemperature: 2.0},
	{ID: "glm-4.7",       Shortcode: "glm47",   Endpoint: zaiChatEndpoint, APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 204800, MaxOutputTokens: 4096, InputCost: 0.60, OutputCost: 2.00, MinTemperature: 0.0, MaxTemperature: 2.0},
	{ID: "glm-4.7-flash", Shortcode: "glm47f",  Endpoint: zaiChatEndpoint, APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 204800, MaxOutputTokens: 4096, InputCost: 0.00, OutputCost: 0.00, MinTemperature: 0.0, MaxTemperature: 2.0},
	{ID: "glm-4.7-flashx",Shortcode: "glm47fx", Endpoint: zaiChatEndpoint, APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 204800, MaxOutputTokens: 4096, InputCost: 0.07, OutputCost: 0.40, MinTemperature: 0.0, MaxTemperature: 2.0},
	{ID: "glm-5",         Shortcode: "glm5",    Endpoint: zaiChatEndpoint, APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 204800, MaxOutputTokens: 4096, InputCost: 1.00, OutputCost: 3.00, MinTemperature: 0.0, MaxTemperature: 2.0},
	{ID: "glm-5-turbo",   Shortcode: "glm5t",   Endpoint: zaiChatEndpoint, APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 204800, MaxOutputTokens: 4096, InputCost: 1.00, OutputCost: 4.00, MinTemperature: 0.0, MaxTemperature: 2.0},
	{ID: "glm-5.1",       Shortcode: "glm51",   Endpoint: zaiChatEndpoint, APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 204800, MaxOutputTokens: 4096, InputCost: 1.00, OutputCost: 4.00, MinTemperature: 0.0, MaxTemperature: 2.0},
	{ID: "glm-5v-turbo",  Shortcode: "glm5vt",  Endpoint: zaiChatEndpoint, APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 204800, MaxOutputTokens: 4096, InputCost: 1.00, OutputCost: 4.00, MinTemperature: 0.0, MaxTemperature: 2.0},
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
```

- [ ] **Schritt 2: Build prüfen**

```bash
go build ./sigoengine/
```
Erwartetes Ergebnis: kein Fehler.

- [ ] **Schritt 3: Commit**

```bash
git add sigoengine/provider_fetchers.go
git commit -m "feat(sigoengine): add provider_fetchers scaffold with ZAI/Moonshot static data"
```

---

### Task 3: FetchMammouthModels() implementieren

**Files:**
- Modify: `sigoengine/provider_fetchers.go` — Funktionen anhängen

- [ ] **Schritt 1: Mammouth-Typen und Fetcher anhängen**

Am Ende von `provider_fetchers.go` einfügen:

```go
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
	ID   string `json:"id"`
	// Kontextfenster (mögliche Feldnamen)
	ContextWindow   int `json:"context_window"`
	MaxContext      int `json:"max_context"`
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
```

- [ ] **Schritt 2: Build prüfen**

```bash
go build ./sigoengine/
```
Erwartetes Ergebnis: kein Fehler.

- [ ] **Schritt 3: Commit**

```bash
git add sigoengine/provider_fetchers.go
git commit -m "feat(sigoengine): implement FetchMammouthModels() with flexible JSON parser"
```

---

### Task 4: FetchMoonshotModels() implementieren

**Files:**
- Modify: `sigoengine/provider_fetchers.go` — Funktion anhängen

- [ ] **Schritt 1: FetchMoonshotModels am Ende der Datei anhängen**

```go
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
```

- [ ] **Schritt 2: Build prüfen**

```bash
go build ./sigoengine/
```
Erwartetes Ergebnis: kein Fehler.

- [ ] **Schritt 3: Commit**

```bash
git add sigoengine/provider_fetchers.go
git commit -m "feat(sigoengine): implement FetchMoonshotModels() with static param table"
```

---

### Task 5: FetchZAIModels() implementieren

**Files:**
- Modify: `sigoengine/provider_fetchers.go` — Funktion anhängen

- [ ] **Schritt 1: FetchZAIModels am Ende der Datei anhängen**

```go
// **********************************************************************
// FetchZAIModels versucht https://api.z.ai/api/paas/v4/models abzurufen.
// API-Key aus ENV: ZAI_API_KEY (Bearer Token).
// Fallback: zaiStaticModels (13 Modelle) wenn API nicht antwortet oder
// keinen /models-Endpoint hat (nicht dokumentiert).
func FetchZAIModels() ([]Model, error) {
	apiKey := os.Getenv("ZAI_API_KEY")
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
```

- [ ] **Schritt 2: Build prüfen**

```bash
go build ./sigoengine/
```
Erwartetes Ergebnis: kein Fehler.

- [ ] **Schritt 3: Commit**

```bash
git add sigoengine/provider_fetchers.go
git commit -m "feat(sigoengine): implement FetchZAIModels() with static fallback"
```

---

### Task 6: Ollama Context-Enrichment via /api/show

**Files:**
- Modify: `sigoengine/engine.go` — `OllamaModelInfo` um `ContextLength` erweitern, `DiscoverOllamaModels()` um Show-Call ergänzen

- [ ] **Schritt 1: OllamaModelInfo-Struct erweitern**

In `engine.go` die `OllamaModelInfo`-Struct (ca. Zeile 401) anpassen:

```go
type OllamaModelInfo struct {
	Shortcode     string // z.B. "ollama-llama3"
	OllamaName    string // z.B. "llama3:latest" (echter Ollama-Name)
	Size          int64  `json:"size"`
	ContextLength int    // aus /api/show, 0 wenn unbekannt
}
```

- [ ] **Schritt 2: fetchOllamaContextLength-Hilfsfunktion einfügen**

Direkt nach der `OllamaModelInfo`-Struct-Definition einfügen:

```go
// fetchOllamaContextLength fragt /api/show für ein Modell ab und
// gibt die Context-Length zurück (0 wenn nicht verfügbar).
func fetchOllamaContextLength(endpoint, modelName string) int {
	client := &http.Client{Timeout: 5 * time.Second}

	type showReq struct {
		Name string `json:"name"`
	}
	body, _ := json.Marshal(showReq{Name: modelName})

	resp, err := client.Post(
		endpoint+"/api/show",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()

	var result struct {
		ModelInfo map[string]interface{} `json:"modelinfo"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0
	}

	// Suche nach context_length in modelinfo (Feldname variiert je nach Modell-Typ)
	for k, v := range result.ModelInfo {
		if strings.HasSuffix(k, ".context_length") || k == "context_length" {
			switch val := v.(type) {
			case float64:
				return int(val)
			}
		}
	}
	return 0
}
```

**Hinweis:** `bytes` ist bereits in den engine.go-Imports vorhanden. Falls nicht: `"bytes"` zu den Imports hinzufügen.

- [ ] **Schritt 3: DiscoverOllamaModels() um Context-Abruf erweitern**

Im Registrierungsblock in `DiscoverOllamaModels()` (nach `shortcode`-Berechnung) den `fetchOllamaContextLength`-Aufruf ergänzen:

```go
		ctxLen := fetchOllamaContextLength(endpoint, m.Name)
		ollamaRegistry[shortcode] = OllamaModelInfo{
			Shortcode:     shortcode,
			OllamaName:    name,
			Size:          m.Size,
			ContextLength: ctxLen,
		}
```

- [ ] **Schritt 4: Build prüfen**

```bash
go build ./sigoengine/
```
Erwartetes Ergebnis: kein Fehler.

- [ ] **Schritt 5: Commit**

```bash
git add sigoengine/engine.go
git commit -m "feat(sigoengine): enrich Ollama models with context_length from /api/show"
```

---

### Task 7: sigoREST/main.go — CSV entfernen, Fetcher integrieren

**Files:**
- Modify: `sigoREST/main.go` — loadModels() ersetzen, embed entfernen, main() aktualisieren

- [ ] **Schritt 1: Imports bereinigen**

In `sigoREST/main.go` die Import-Sektion aktualisieren. Folgendes entfernen:

```go
_ "embed"   // entfernen
```

Folgende Zeilen auch entfernen, wenn nach den anderen Änderungen nirgends mehr genutzt:
- `"strconv"` (war nur in loadModels() für Atoi/ParseFloat)
- `"strings"` bleibt (wird noch an anderen Stellen genutzt)

- [ ] **Schritt 2: Embed-Direktive und Variable entfernen**

Diese drei Zeilen in `main.go` löschen:

```go
//go:embed models.csv
var defaultModelsCSV string
```

- [ ] **Schritt 3: models-Flag entfernen**

Im `var`-Block der Flags diese Zeile löschen:

```go
modelsPath  = flag.String("models", "", "Pfad zur models.csv (optional)")
```

- [ ] **Schritt 4: loadModels() durch loadModelsFromProviders() ersetzen**

Die gesamte `loadModels()`-Funktion (ca. Zeilen 250–318) löschen und durch diese ersetzen:

```go
// modelInfoFromEngine konvertiert sigoengine.Model → ModelInfo
func modelInfoFromEngine(m sigoengine.Model) ModelInfo {
	return ModelInfo{
		ID:                       m.ID,
		Shortcode:                m.Shortcode,
		Endpoint:                 m.Endpoint,
		APIKey:                   m.APIKeyEnv,
		MaxInputTokens:           m.MaxInputTokens,
		MaxOutputTokens:          m.MaxOutputTokens,
		InputCost:                m.InputCost,
		OutputCost:               m.OutputCost,
		MinTemperature:           m.MinTemperature,
		MaxTemperature:           m.MaxTemperature,
		RequiresCompletionTokens: m.RequiresCompletionTokens,
	}
}

// loadModelsFromProviders ruft alle Provider-APIs beim Start ab.
// Fehler bei einzelnen Providern werden geloggt; der Server startet
// trotzdem mit den verfügbaren Modellen.
func loadModelsFromProviders() map[string]ModelInfo {
	models := make(map[string]ModelInfo)

	// 1. Mammouth (kein API-Key nötig)
	if ms, err := sigoengine.FetchMammouthModels(); err != nil {
		sigoengine.LogWarn("Mammouth-Modelle nicht geladen", map[string]interface{}{"error": err.Error()})
	} else {
		for _, m := range ms {
			models[m.ID] = modelInfoFromEngine(m)
		}
	}

	// 2. Moonshot
	if ms, err := sigoengine.FetchMoonshotModels(); err != nil {
		sigoengine.LogWarn("Moonshot-Modelle nicht geladen", map[string]interface{}{"error": err.Error()})
	} else {
		for _, m := range ms {
			models[m.ID] = modelInfoFromEngine(m)
		}
	}

	// 3. ZAI (fällt intern auf statische Liste zurück)
	if ms, err := sigoengine.FetchZAIModels(); err != nil {
		sigoengine.LogWarn("ZAI-Modelle nicht geladen", map[string]interface{}{"error": err.Error()})
	} else {
		for _, m := range ms {
			models[m.ID] = modelInfoFromEngine(m)
		}
	}

	sigoengine.LogInfo("Provider-Modelle geladen", map[string]interface{}{"count": len(models)})
	return models
}
```

- [ ] **Schritt 5: main() aktualisieren**

In `main()` den Abschnitt:

```go
// Custom models.csv Pfad setzen (muss vor Registry-Zugriff passieren)
if *modelsPath != "" {
    sigoengine.SetModelsCSVPath(*modelsPath)
    sigoengine.LogInfo("Custom models.csv Pfad gesetzt", map[string]interface{}{"path": *modelsPath})
}
```

und im `srv := &Server{...}`-Block:

```go
srv := &Server{
    models:   loadModels(),    // ← ersetzen durch:
```

ändern zu:

```go
srv := &Server{
    models:   loadModelsFromProviders(),
```

- [ ] **Schritt 6: Ollama-Block in main() anpassen**

Den bestehenden Ollama-Block (`if n := sigoengine.DiscoverOllamaModels(...)`) so erweitern, dass `ContextLength` genutzt wird:

```go
ollamaEndpoint := "http://localhost:11434"
if n := sigoengine.DiscoverOllamaModels(ollamaEndpoint); n > 0 {
    srv.mu.Lock()
    ollamaModels := sigoengine.GetOllamaModels()
    for sc, info := range ollamaModels {
        srv.models[sc] = ModelInfo{
            ID:              sc,
            Shortcode:       sc,
            Endpoint:        "http://localhost:11434/v1/chat/completions",
            APIKey:          "",
            MaxInputTokens:  info.ContextLength, // aus /api/show
            MaxOutputTokens: 0,
            MinTemperature:  0.0,
            MaxTemperature:  2.0,
        }
    }
    srv.mu.Unlock()
}
```

- [ ] **Schritt 7: Build prüfen**

```bash
go build ./sigoREST/
```
Erwartetes Ergebnis: kein Fehler. Sollte `strconv` fehlen — Import entfernen.

- [ ] **Schritt 8: Commit**

```bash
git add sigoREST/main.go
git commit -m "feat(sigoREST): replace loadModels()/CSV with loadModelsFromProviders()"
```

---

### Task 8: PingProvider in handleChatCompletions integrieren

**Files:**
- Modify: `sigoREST/main.go` — in handleChatCompletions() einfügen

- [ ] **Schritt 1: Ping-Aufruf nach Model-Lookup einfügen**

In `handleChatCompletions()`, direkt nach dem Block wo `cfg` aufgebaut wird (ca. nach Zeile 427 `cfg := &sigoengine.ProviderConfig{...}`) einfügen:

```go
	// Provider-Ping: scheitert → sofortiger Fehler, kein API-Call
	if err := sigoengine.PingProvider(modelInfo.Endpoint); err != nil {
		sigoengine.LogWarn("Provider nicht erreichbar", map[string]interface{}{
			"model":    modelID,
			"endpoint": modelInfo.Endpoint,
			"error":    err.Error(),
		})
		writeError(w, "Provider nicht erreichbar: "+err.Error(), "provider_unavailable", http.StatusServiceUnavailable)
		return
	}
```

- [ ] **Schritt 2: Build prüfen**

```bash
go build ./sigoREST/
```

- [ ] **Schritt 3: Ping manuell testen**

Server starten und Health-Check senden (Provider-Ping passiert nur bei Chat-Requests, hier indirekt getestet):

```bash
./sigoREST/sigoREST -v debug &
curl -s http://localhost:9080/api/health | jq '.status'
```
Erwartetes Ergebnis: `"ok"`

- [ ] **Schritt 4: Commit**

```bash
git add sigoREST/main.go
git commit -m "feat(sigoREST): ping provider endpoint before each API call"
```

---

### Task 9: System-Prompt — Server-State, Persistenz, Laden

**Files:**
- Modify: `sigoREST/main.go` — Server-Struct, loadSystemPrompt(), main()

- [ ] **Schritt 1: systemPrompt-Feld zu Server-Struct hinzufügen**

`Server`-Struct (ca. Zeile 78) erweitern:

```go
type Server struct {
	mu           sync.RWMutex
	memory       MemoryBlock
	models       map[string]ModelInfo
	breakers     map[string]*sigoengine.EnhancedCircuitBreaker
	systemPrompt string // globaler Default-Prompt (leer = kein Prompt)
}
```

- [ ] **Schritt 2: loadSystemPrompt()-Funktion hinzufügen**

Nach `loadMemory()` einfügen:

```go
// loadSystemPrompt liest system-prompt.txt von Disk (optional).
// Gibt leeren String zurück wenn Datei nicht existiert.
func loadSystemPrompt() string {
	data, err := os.ReadFile("./system-prompt.txt")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
```

- [ ] **Schritt 3: loadSystemPrompt() im main() aufrufen**

`srv := &Server{...}` Block erweitern:

```go
srv := &Server{
	models:       loadModelsFromProviders(),
	memory:       loadMemory(),
	breakers:     make(map[string]*sigoengine.EnhancedCircuitBreaker),
	systemPrompt: loadSystemPrompt(),
}
```

- [ ] **Schritt 4: Build prüfen**

```bash
go build ./sigoREST/
```

- [ ] **Schritt 5: Commit**

```bash
git add sigoREST/main.go
git commit -m "feat(sigoREST): add system prompt server state and file persistence"
```

---

### Task 10: /api/system-prompt GET/PUT Handler

**Files:**
- Modify: `sigoREST/main.go` — Handler-Funktion + Route registrieren

- [ ] **Schritt 1: handleSystemPrompt() hinzufügen**

Nach `handleMemory()` (ca. Zeile 789) einfügen:

```go
// **********************************************************************
// GET/PUT /api/system-prompt — Globalen System-Prompt lesen/setzen
func (s *Server) handleSystemPrompt(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.mu.RLock()
		prompt := s.systemPrompt
		s.mu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"system_prompt": prompt})

	case http.MethodPut:
		var body struct {
			SystemPrompt string `json:"system_prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, "Invalid JSON: "+err.Error(), "invalid_request", http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.systemPrompt = body.SystemPrompt
		s.mu.Unlock()

		if err := os.WriteFile("./system-prompt.txt", []byte(body.SystemPrompt), 0644); err != nil {
			sigoengine.LogWarn("system-prompt.txt speichern fehlgeschlagen", map[string]interface{}{"error": err.Error()})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status":        "ok",
			"system_prompt": body.SystemPrompt,
		})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}
```

- [ ] **Schritt 2: Route in main() registrieren**

Im Mux-Block in `main()` (nach den anderen HandleFunc-Zeilen) hinzufügen:

```go
mux.HandleFunc("/api/system-prompt", srv.handleSystemPrompt)
```

- [ ] **Schritt 3: Build prüfen**

```bash
go build ./sigoREST/
```

- [ ] **Schritt 4: Handler manuell testen**

```bash
./sigoREST/sigoREST -v debug &

# GET (leer)
curl -s http://localhost:9080/api/system-prompt | jq
# Erwartetes Ergebnis: {"system_prompt": ""}

# PUT setzen
curl -s -X PUT http://localhost:9080/api/system-prompt \
  -H "Content-Type: application/json" \
  -d '{"system_prompt": "Du bist ein hilfreicher Assistent."}'
# Erwartetes Ergebnis: {"status":"ok","system_prompt":"Du bist ein hilfreicher Assistent."}

# GET nach PUT
curl -s http://localhost:9080/api/system-prompt | jq
# Erwartetes Ergebnis: {"system_prompt": "Du bist ein hilfreicher Assistent."}

# Datei prüfen
cat system-prompt.txt
# Erwartetes Ergebnis: Du bist ein hilfreicher Assistent.
```

Server stoppen: `pkill sigoREST`

- [ ] **Schritt 5: Commit**

```bash
git add sigoREST/main.go
git commit -m "feat(sigoREST): add GET/PUT /api/system-prompt endpoint"
```

---

### Task 11: System-Prompt in handleChatCompletions integrieren

**Files:**
- Modify: `sigoREST/main.go` — ChatRequest-Struct erweitern, handleChatCompletions() anpassen

- [ ] **Schritt 1: ChatRequest-Struct um system_prompt erweitern**

Die `ChatRequest`-Struct (ca. Zeile 351) erweitern:

```go
type ChatRequest struct {
	Model        string        `json:"model"`
	Messages     []ChatMessage `json:"messages"`
	MaxTokens    int           `json:"max_tokens"`
	Temp         float64       `json:"temperature"`
	SessionID    string        `json:"session_id"`
	Timeout      int           `json:"timeout"`
	Retries      int           `json:"retries"`
	SystemPrompt string        `json:"system_prompt"` // per-Request Override
}
```

- [ ] **Schritt 2: globalSystemPrompt aus Server-State lesen**

Im `handleChatCompletions()`-RLock-Block (ca. Zeile 419 wo `mem := s.memory` steht) ergänzen:

```go
	mem := s.memory
	globalSystemPrompt := s.systemPrompt
	s.mu.RUnlock()
```

- [ ] **Schritt 3: Effektiven System-Prompt bestimmen und einfügen**

Im Message-Building-Abschnitt nach dem Memory-Block (ca. nach Zeile 459) einfügen:

```go
	// System-Prompt: Request-Wert hat Vorrang vor globalem Default
	effectiveSystemPrompt := globalSystemPrompt
	if req.SystemPrompt != "" {
		effectiveSystemPrompt = req.SystemPrompt
	}
	if effectiveSystemPrompt != "" {
		messages = append(messages, map[string]interface{}{
			"role":    "system",
			"content": effectiveSystemPrompt,
		})
	}
```

- [ ] **Schritt 4: Build prüfen**

```bash
go build ./sigoREST/
```

- [ ] **Schritt 5: End-to-End-Test System-Prompt**

```bash
./sigoREST/sigoREST -v debug &

# Test mit per-Request System-Prompt
curl -s http://localhost:9080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt41",
    "messages": [{"role": "user", "content": "Wie heißt du?"}],
    "system_prompt": "Antworte immer auf Englisch."
  }' | jq '.choices[0].message.content'
```

Server stoppen: `pkill sigoREST`

- [ ] **Schritt 6: Commit**

```bash
git add sigoREST/main.go
git commit -m "feat(sigoREST): integrate system prompt into chat completions handler"
```

---

### Task 12: models.csv löschen und verbleibende Aufräumarbeiten

**Files:**
- Delete: `sigoREST/models.csv`
- Modify: `sigoREST/main.go` — ggf. verbleibende Referenzen entfernen

- [ ] **Schritt 1: models.csv löschen**

```bash
git rm sigoREST/models.csv
```

- [ ] **Schritt 2: Auf verbleibende Referenzen prüfen**

```bash
grep -rn "models\.csv\|defaultModelsCSV\|modelsPath\|SetModelsCSVPath\|GetModelsCSVPath" sigoREST/main.go
```

Erwartetes Ergebnis: keine Treffer. Sollte noch etwas übrig sein → löschen.

- [ ] **Schritt 3: Gesamtbuild prüfen**

```bash
go build ./...
```
Erwartetes Ergebnis: alle Pakete bauen ohne Fehler.

- [ ] **Schritt 4: Commit**

```bash
git add -A
git commit -m "chore: remove models.csv and remaining CSV loading references"
```

---

### Task 13: Vollständiger End-to-End-Test

**Files:**
- Keine Änderungen — nur Verifikation

- [ ] **Schritt 1: Server starten und Modell-Liste prüfen**

```bash
go build -o sigoREST/sigoREST ./sigoREST/
./sigoREST/sigoREST -v debug 2>&1 &

# Warten bis Server bereit
sleep 2

# Modell-Anzahl prüfen (sollte deutlich mehr als die alten ~50 sein)
curl -s http://localhost:9080/v1/models | jq '.data | length'

# Erste 10 Modelle anzeigen
curl -s http://localhost:9080/v1/models | jq '[.data[].id] | sort | .[:10]'
```

- [ ] **Schritt 2: Mammouth-Modell aufrufen**

```bash
curl -s http://localhost:9080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt41","messages":[{"role":"user","content":"Antworte mit einem Wort: Hauptstadt von Frankreich?"}]}' \
  | jq '.choices[0].message.content'
```
Erwartetes Ergebnis: `"Paris"` (oder Ähnliches)

- [ ] **Schritt 3: ZAI-Modell prüfen**

```bash
curl -s http://localhost:9080/v1/models | jq '[.data[].id | select(startswith("glm"))] | sort'
```
Erwartetes Ergebnis: Array mit glm-4.5, glm-4.5-air, glm-4.7 usw.

- [ ] **Schritt 4: Ping-Fehler simulieren**

```bash
# Modell mit ungültigem Endpoint direkt aufrufen (nur wenn ein Test-Modell existiert)
# Stattdessen: Serverlog auf Ping-Meldungen prüfen
# In den Server-Logs bei normalem Request sollte kein Ping-Fehler auftauchen
```

- [ ] **Schritt 5: System-Prompt globaler Test**

```bash
# Globalen Prompt setzen
curl -s -X PUT http://localhost:9080/api/system-prompt \
  -H "Content-Type: application/json" \
  -d '{"system_prompt": "Antworte immer auf Englisch, egal in welcher Sprache gefragt wird."}' | jq

# Chat-Request ohne system_prompt im Body (globaler greift)
curl -s http://localhost:9080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt41","messages":[{"role":"user","content":"Wie heißt die Hauptstadt von Frankreich?"}]}' \
  | jq '.choices[0].message.content'
# Erwartetes Ergebnis: Antwort auf Englisch ("Paris" or "The capital of France is Paris.")
```

- [ ] **Schritt 6: Server stoppen und Abschluss-Commit**

```bash
pkill sigoREST

git log --oneline -10
```

---

## Übersicht der Dateiänderungen

| Datei | Aktion |
|-------|--------|
| `sigoengine/engine.go` | `PingProvider()`, `OllamaModelInfo.ContextLength`, `fetchOllamaContextLength()` |
| `sigoengine/provider_fetchers.go` | NEU: alle Fetcher + statische Daten |
| `sigoREST/main.go` | `loadModels()` → `loadModelsFromProviders()`, System-Prompt, Ping-Integration |
| `sigoREST/models.csv` | GELÖSCHT |
