# Design: Dynamischer Modellabruf & Erweiterungen (PROJECT-2)

**Datum:** 2026-04-15  
**Autor:** Gerhard Quell  
**Status:** Genehmigt

---

## Überblick

Dieses Dokument beschreibt die Erweiterungen für sigoREST gemäß PROJECT-2.md:

1. Dynamischer Modellabruf von Mammouth, Moonshot, ZAI und Ollama beim Serverstart
2. Modell-Parameter pro Modell über die REST-API abrufbar
3. Server-Ping vor jedem API-Call
4. Custom System-Prompt (global + per Request)

---

## 1. Dynamischer Modellabruf (Aufgabe 1)

### Entscheidungen

- **Zeitpunkt:** Nur beim Serverstart (Server läuft täglich einmal)
- **Fehlerverhalten:** Ist ein Provider nicht erreichbar → Warnung loggen, Server startet trotzdem mit den übrigen Modellen
- **CSV entfällt vollständig:** `models.csv`, `//go:embed models.csv`, `loadModels()` und das `-models` Flag werden ersatzlos entfernt

### Provider-Quellen

| Provider | Endpoint | Auth | Fallback |
|----------|----------|------|----------|
| Mammouth | `GET https://api.mammouth.ai/public/models` | keine (public) | — |
| Moonshot | `GET https://api.moonshot.ai/v1/models` | `MOONSHOT_API_KEY` (Bearer) | — |
| ZAI | `GET https://api.z.ai/api/paas/v4/models` | `ZAI_API_KEY` (Bearer) | 13 statische Modelle |
| Ollama | `GET http://localhost:11434/api/tags` | keine | — (wie bisher) |

### ZAI — Statische Fallback-Liste (13 Modelle)

Falls `GET /models` bei Z.ai keinen verwertbaren Response liefert:

| Modell-ID | Kontext | Input $/1M | Output $/1M | Vision |
|-----------|---------|-----------|-------------|--------|
| `glm-4.5` | 131K | 0.60 | 2.00 | nein |
| `glm-4.5-air` | 131K | 0.20 | 1.00 | nein |
| `glm-4.5-flash` | 131K | 0.00 | 0.00 | nein |
| `glm-4.5v` | 64K | 0.60 | 2.00 | ja |
| `glm-4.6` | 205K | 0.60 | 2.00 | nein |
| `glm-4.6v` | 128K | 0.30 | 0.90 | ja |
| `glm-4.7` | 205K | 0.60 | 2.00 | nein |
| `glm-4.7-flash` | 200K | 0.00 | 0.00 | nein |
| `glm-4.7-flashx` | 200K | 0.07 | 0.40 | nein |
| `glm-5` | 205K | 1.00 | 3.00 | nein |
| `glm-5-turbo` | 200K | 1.00 | 4.00 | nein |
| `glm-5.1` | 200K | 1.00 | 4.00 | nein |
| `glm-5v-turbo` | 200K | 1.00 | 4.00 | ja |

ZAI-Endpoint für Chat: `https://api.z.ai/api/paas/v4/chat/completions`

### Startup-Sequenz

```
main() startet
  ├── FetchMammouthModels()   → Fehler: LogWarn + weiter
  ├── FetchMoonshotModels()   → Fehler: LogWarn + weiter
  ├── FetchZAIModels()        → Fehler: statische Liste verwenden
  └── DiscoverOllamaModels()  → Fehler: LogWarn + weiter (unverändert)
Ergebnis: srv.models befüllt ausschließlich aus diesen vier Quellen
```

### Neue Funktionen in `sigoengine/engine.go`

```go
// Gibt DiscoveredModel-Slice zurück; bei Fehler leerer Slice + Fehler
// API-Keys werden intern aus ENV gelesen: MOONSHOT_API_KEY, ZAI_API_KEY
func FetchMammouthModels() ([]DiscoveredModel, error)   // kein API-Key nötig (public)
func FetchMoonshotModels() ([]DiscoveredModel, error)   // liest MOONSHOT_API_KEY aus ENV
func FetchZAIModels() ([]DiscoveredModel, error)        // liest ZAI_API_KEY aus ENV

// Gemeinsamer Typ für alle Provider-Ergebnisse
type DiscoveredModel struct {
    ID                       string
    Shortcode                string
    Endpoint                 string
    APIKeyEnv                string
    MaxInputTokens           int
    MaxOutputTokens          int
    InputCost                float64
    OutputCost               float64
    MinTemperature           float64
    MaxTemperature           float64
    RequiresCompletionTokens bool
}
```

### Shortcode-Vergabe

- Mammouth/Moonshot: Shortcode aus der API-Antwort (falls vorhanden); sonst erste 6 alphanumerische Zeichen des Modellnamens (Bindestriche/Punkte entfernt), z.B. `gpt-4.1-mini` → `gpt41m`. Bei Kollision: Suffix `-2`, `-3` usw.
- ZAI: Kurzform aus Modell-ID, z.B. `glm-4.7-flash` → `glm47f`, `glm-5.1` → `glm51`
- Ollama: unverändert (wie bisher: `ollama-<name>`)

---

## 2. Modell-Parameter (Aufgabe 2)

### Strategie: Hybrid

| Quelle | Parameterbeschaffung |
|--------|----------------------|
| Mammouth | Aus API-Response extrahieren (was auch immer geliefert wird) |
| Moonshot | Statische Tabelle im Code (stabile Werte, selten geändert) |
| ZAI | Statische Tabelle (Mastra-Daten, s.o.) |
| Ollama | Zusätzlich `GET /api/show?name=<model>` für context_length |

### REST-Zugriff

Kein neuer Endpoint nötig. Der bestehende `GET /api/models` gibt bereits volle `ModelInfo` zurück und wird damit automatisch vollständig.

---

## 3. Server-Ping vor API-Call (Aufgabe 3)

### Verhalten

- Vor jedem `handleChatCompletions`-Call wird der Provider-Endpoint gepingt
- Timeout: 5 Sekunden
- Bei Fehler: sofortiger HTTP 503 an den Client, kein API-Call

### Neue Funktion in `sigoengine/engine.go`

```go
// Sendet HEAD-Request an endpoint, Timeout 5s
// Gibt nil zurück wenn erreichbar, sonst Fehler
func PingProvider(endpoint string) error
```

### Integration in `sigoREST/main.go`

```go
func (s *Server) handleChatCompletions(...) {
    // ... Request parsen, Modell auflösen ...

    if err := sigoengine.PingProvider(modelInfo.Endpoint); err != nil {
        writeError(w, "Provider nicht erreichbar", "provider_unavailable", 503)
        return
    }

    // ... CallAPI wie bisher ...
}
```

---

## 4. Custom System-Prompt (Aufgabe 4)

### Priorität

```
Request "system_prompt" > Globaler System-Prompt > kein System-Prompt
```

### Server-State

```go
type Server struct {
    // ... bestehende Felder ...
    systemPrompt string  // globaler Default, leer = kein System-Prompt
}
```

### Persistenz

Globaler System-Prompt wird in `system-prompt.txt` gespeichert (analog memory.json). Beim Start geladen, falls vorhanden.

### Neue Endpoints

| Endpoint | Methode | Beschreibung |
|----------|---------|--------------|
| `/api/system-prompt` | GET | Aktuellen globalen System-Prompt zurückgeben |
| `/api/system-prompt` | PUT | Globalen System-Prompt setzen und speichern |

### Request-Erweiterung

```json
{
  "model": "claude-h",
  "messages": [...],
  "system_prompt": "Du bist ein hilfreicher Assistent."
}
```

### Integration in `handleChatCompletions`

Der System-Prompt wird als erste Nachricht mit Role `system` in den Messages-Array eingefügt, bevor der API-Call erfolgt.

---

## Entfernte Komponenten

| Komponente | Grund |
|------------|-------|
| `sigoREST/models.csv` | Durch dynamischen Abruf ersetzt |
| `//go:embed models.csv` | Entfällt mit CSV |
| `var defaultModelsCSV string` | Entfällt mit CSV |
| `loadModels()` | Ersetzt durch Fetcher-Funktionen |
| `-models` Flag | Kein custom CSV-Pfad mehr nötig |

---

## Nicht geändert

- `memory.json` + `/api/memory` Endpoint — bleibt unverändert
- `DiscoverOllamaModels()` — Logik unverändert, nur in Startup-Sequenz integriert
- Circuit Breaker, Retry, Session-Management — unverändert
- HTTP/HTTPS Listener, IP-Zugangskontrolle — unverändert
- `cmd/sigoE/main.go` CLI — unverändert
