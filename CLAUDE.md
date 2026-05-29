# CLAUDE.md

Anleitung für Claude Code (claude.ai/code) bei Arbeit mit diesem Repo.

## Developer

- <IMPORTANCE>Deutsch mit User, "Du" und "Gerhard"</IMPORTANCE>

## Project Overview

sigoREST = drei-schichtiges Go-Projekt, zwei User-Interfaces:

- **sigoE CLI**: Command-line Engine für direkte AI-Abfragen
- **sigoREST Server**: OpenAI-kompatibler REST-Server für parallele Verbindungen (~100)

Beide nutzen **Shared Package** `sigoengine` für:
- Model-Registry (60+ Modelle von Mammoth.ai, Moonshot.ai, Z.ai)
- API-Abstraktion (OpenAI + Anthropic Formate)
- Circuit Breaker + Retry Logic
- Session-Management (JSON-basiert)
- Thread-safes Logging

## Development Commands

### Alle Pakete bauen
```bash
go build ./...
```

### REST-Server bauen & starten
```bash
# Server bauen
go build -o sigoREST/sigoREST ./sigoREST/

# Server starten (HTTP localhost:9080, HTTPS privates Netz:9443)
./sigoREST/sigoREST -v debug
```

### CLI bauen & nutzen
```bash
# CLI bauen
go build -o sigoE ./cmd/sigoE/

# Alle verfügbaren Modelle auflisten
./sigoE -l

# Prompt senden
echo "Hallo" | ./sigoE -m claude-h

# Mit Session
./sigoE -m claude-h -s projekt-x "Erste Nachricht"
./sigoE -m claude-h -s projekt-x "Zweite Nachricht"
```

### Server testen
```bash
# Health check
curl -s http://localhost:9080/api/health

# Chat completion
curl -s http://localhost:9080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"claude-h","messages":[{"role":"user","content":"Hallo"}]}'

# Modell-Liste
curl -s http://localhost:9080/v1/models | jq '.data[].id'
```

### Running Tests
Go-Tests liegen in `sigoengine/` (engine.go ist getrennt von `*_test.go`):
```bash
go test ./...                              # alle Tests
go test ./sigoengine/ -v                   # mit Details
go test ./sigoengine/ -run TestFetchWithRetry   # einzelner Test (Regex)
```
Abgedeckt u.a.: `retry`, `shortcode`, `usage`, `finish_reason`. Server +
CLI selbst haben keine Go-Tests → manuell via CLI/REST-API testen.

## Architecture

### Drei-Schichten-Design

```
sigorest/
├── sigoengine/             # Shared Package (thread-safe), mehrere Dateien:
│   ├── engine.go           #   CallAPI, CircuitBreaker, Session, Logging
│   ├── models.go           #   Model-Typ, CoreModels (Fallback-Liste)
│   ├── models_registry.go  #   Lookup-Maps, Laden JSON→CSV→CoreModels
│   ├── provider_fetchers.go#   Dynamischer Abruf Mammoth/Moonshot/ZAI
│   ├── retry.go            #   FetchWithRetry (Backoff gegen Boot-DNS-Race)
│   └── shortcode.go        #   Shortcode-Generierung (Familie+Version+Variante)
├── cmd/sigoE/main.go       # CLI-Wrapper
├── sigoREST/main.go        # REST-Server
└── sigoREST/memory.json    # Globaler Memory-Block (embedded + Disk)
```

### sigoengine — Shared Package

Thread-safe Package für CLI und REST (mehrere Dateien, siehe Baum oben). Exportiert u.a.:

| Export | Zweck |
|--------|-------|
| `MammothModels` | Model-Registry (Map: name → config) |
| `LoadConfig(model)` | Lädt ProviderConfig aus Registry + ENV |
| `CallAPI(ctx, cfg, req, timeout)` | HTTP-Call mit Retry → `(string, *UsageData, error)` |
| `UsageData` | Token-Verbrauch (InputTokens, OutputTokens, TotalTokens) |
| `CircuitBreaker` | Pro-Modell Fehlerisolierung |
| `Session{History []Message}` | Gesprächsverlauf (max 20) |
| `Log*()` | Thread-safes Logging (DEBUG/INFO/WARN/ERROR/FATAL) |
| `DiscoverOllamaModels(endpoint)` | Auto-Discovery lokaler LLMs |
| `ResolveModelName(shortcode)` | Shortcode → vollständiger Name |
| `Fetch{Mammouth,Moonshot,ZAI}Models()` | Dynamischer Modell-Abruf pro Provider |
| `FetchWithRetry(name, attempts, backoff, fn)` | Retry-Wrapper mit Backoff um einen Fetcher |
| `GenerateShortcode(id, used)` | Sprechender Shortcode aus Modellname |

**Thread-Safety:**
- `sync.RWMutex` für Logging-Konfiguration
- `sync.Once` für Shortcode-Lookup-Map
- `sync.RWMutex` für Ollama-Registry

### cmd/sigoE/main.go — CLI-Wrapper

Schlanker Wrapper nutzt sigoengine. Rückwärtskompatibel zu ursprünglichem sigoEngine Binary.

**Flags:**
- `-m` Modell (default: `claude-h`, Shortcode oder vollständiger Name)
- `-s` Session-ID (für Gesprächsverlauf)
- `-n` Max Tokens (0 = Modell-Default)
- `-T` Temperatur (-1 = Modell-Default)
- `-t` Timeout Sekunden (default: 180)
- `-r` Retries (default: 3)
- `-l` Alle Modelle auflisten
- `-i` Modell-Info anzeigen
- `-v` Log-Level: `debug|info|warn|error`
- `-j` JSON-Ausgabe
- `-q` Quiet Mode (nur Fehler)

### sigoREST/main.go — REST-Server

OpenAI-kompatibler Server mit IP-basierter Zugriffskontrolle.

**Zwei Listener teilen einen `http.ServeMux`:**
- HTTP `:9080` — Nur localhost (127.0.0.0/8)
- HTTPS `:9443` — Privates Netz (192.168.0.0/16, 10.0.0.0/8)

**Modell-Quelle (wichtig):** Der Server lädt seine Modelle beim Start
**dynamisch** über `loadModelsFromProviders()` (Mammoth/Moonshot/ZAI per HTTP)
plus Ollama-Discovery — **nicht** aus einer models.csv. Nur `memory.json` ist
embedded (`//go:embed memory.json`), Disk hat Vorrang. Die CSV/Registry
(`models_registry.go`) ist primär für die CLI; der Server nutzt sie nicht.

**Endpoints:**
| Pfad | Methode | Zweck |
|-------|---------|-------|
| `/v1/chat/completions` | POST | OpenAI-kompatible Chat API |
| `/v1/models` | GET | Modell-Liste (ID + Shortcode) |
| `/api/models` | GET | Volle Modell-Infos (Preise, Limits) |
| `/api/shortcodes` | GET | Kompaktes Mapping `{id: shortcode}` (nach ID sortiert) |
| `/api/health` | GET | Server-Status + Circuit-Breaker |
| `/api/memory` | GET/PUT | Globaler Memory-Block |
| `/api/usage`  | GET | Token-Statistiken (RAM, Reset bei Neustart) |
| `/api/system-prompt` | GET/PUT | Globaler System-Prompt |
| `/api/help`   | GET | Endpoint-Dokumentation |
| `/ping`       | GET | Load-Balancer Health-Check |

**sigoREST-Erweiterungen im Request:**
```json
{
  "model": "claude-h",
  "messages": [...],
  "session_id": "mein-projekt",   // Optional
  "timeout": 120,                  // Optional (default 180)
  "retries": 3                     // Optional (default 3)
}
```

### Dynamisches Modell-Laden (Server)

`loadModelsFromProviders()` ruft beim Start sequenziell drei Provider-APIs ab.
Jeder Fetcher ist in `FetchWithRetry` gewickelt (4 Versuche, 2s/4s/8s Backoff).
Einzelne Fehlschläge werden geloggt; der Server startet mit dem Rest weiter.

**Wichtige Fallback-Asymmetrie** (relevant bei Netz-/DNS-Problemen):
| Provider | bei Fehler | Folge |
|----------|-----------|-------|
| Mammoth (`/public/models`, kein Key) | `return nil, err` | 0 Modelle |
| Moonshot (`MOONSHOT_API_KEY`) | `return nil, err` | 0 Modelle |
| ZAI (`ZAI_API_KEY`) | `return zaiStaticModels, nil` | 13 statische Modelle |

→ Wenn beim Boot nur ~13 Modelle erscheinen ("no such host" im Log): DNS war
beim Start noch nicht oben. Schutz: systemd-Unit mit `Wants/After=network-online.target`
(nicht `network.target`!) **plus** der Retry. Siehe `docs/systemd-install.md`.
Workaround zur Laufzeit: `systemctl restart sigoREST`.

**Shortcode-Generierung:** `GenerateShortcode` (in `shortcode.go`) baut sprechende
Kürzel: Familie (longest-prefix, z.B. `gpt`/`claude→cl`/`gemini→gem`) + Subfamily
(`sonnet→s`) + Version (`5.1→51`) + Variante (`mini→m`/`flash→f`), Kollision via
numerischem Suffix, Cutter-Sanborn-Fallback für Unbekanntes.

**Shortcode-Resolution:** Server prüft zuerst nach ID, dann scannt alle Shortcodes.
Bei Treffer wird ID für den API-Call verwendet.

### CSV/Registry-Format (CLI, `sigoengine/models_registry.go`)

Lade-Reihenfolge der Registry: JSON → CSV → `CoreModels`. Semikolon-getrennte CSV,
11 Felder (Semikolon weil Komma in JSON-Arrays/Listen kollidiert):
```
id;shortcode;endpoint;apikey;max_input;max_output;input_cost;output_cost;min_temp;max_temp;requires_completion_tokens
```
`requires_completion_tokens=true` → Modell nutzt `max_completion_tokens` statt
`max_tokens` (z.B. GPT-5). `apikey` ist der ENV-Var-Name (leer bei Ollama).

### Ollama Auto-Discovery

Ollama-Modelle beim Serverstart via `GET /api/tags` entdeckt:

```go
ollamaEndpoint := "http://localhost:11434"
sigoengine.DiscoverOllamaModels(ollamaEndpoint)
```

**Shortcode-Schema:**
- `llama3:latest` → `ollama-llama3` (`:latest` weggeschnitten)
- `gemma3:12b` → `ollama-gemma3-12b` (andere Tags als Suffix)

Ollama-Modelle: kein API-Key (`APIKey: ""`), nutzen `http://localhost:11434/v1/chat/completions`.

**Limitation:** Nur Startzeit-Discovery → Neustart nötig nach `ollama pull`.

### Circuit Breaker

Pro Modell ein Circuit Breaker (nicht global):

Server nutzt `NewEnhancedCircuitBreaker` pro Modell (`handleChatCompletions`):
| Parameter | Wert |
|-----------|-------|
| Threshold | 5 Fehler |
| Window | 60s Zeitfenster |
| Cooldown | 10s vor Half-Open |
| HalfOpenMax | 3 Requests in Half-Open |
| Scope | Pro Modell (`map[string]*...`) |

Fehler bei einem Modell blockieren andere nicht.

### Session-Management

Sessions als JSON-Dateien:
- Pfad: `.sessions/<model>-<sessionID>.json`
- Max 20 Messages pro Session (älteste verworfen)
- Modell-spezifisch (`claude-h-projekt-a` vs `gpt41-projekt-a`)

### Logging

Thread-safes, strukturiertes Logging:

```go
sigoengine.SetLogLevel(sigoengine.ParseLogLevel("debug"))
sigoengine.SetJSONMode(true)   // Machine-parsable
sigoengine.SetQuietMode(true)  // Nur ERROR und FATAL
```

**Ausgabe:** stderr (stdout sauber für UNIX piping)

## Important Notes

- **Go-Modul**: `sigorest` mit Go 1.26
- **Embedded Files**: nur `memory.json` eingebettet (Disk hat Vorrang); Server-Modelle kommen dynamisch von den Providern, nicht aus einer embedded CSV
- **systemd**: Unit muss `Wants/After=network-online.target` setzen, sonst lädt beim Boot nur die ZAI-Fallback-Liste (DNS-Race)
- **IPv6**: Geblockt (außer `::1` loopback)
- **TLS**: Self-signed Zertifikat automatisch generiert beim ersten Start
- **Ports**: 8080/8443 belegt auf Gerhards System (lokaler Webserver)

## Retrospektiven

Detaillierte Session-Historie: Siehe `RETROSPECTIVE.md`

## Common Tasks

### Neues Modell hinzufügen
Server: Modelle kommen dynamisch vom Provider — bekannte Modelle werden in
`provider_fetchers.go` angereichert (`moonshotKnownModels`, `zaiStaticModels`,
Mammoth via API). Neuen Provider → neue `Fetch*`-Funktion + Aufruf in
`loadModelsFromProviders()`. CLI: Eintrag in CSV/Registry.

### REST-Server als systemd-Service installieren
```bash
sudo cp sigoREST/sigoREST /usr/local/sbin/sigoREST
sudo mkdir -p /usr/local/slib/sigoREST
sudo cp sigoREST/memory.json /usr/local/slib/sigoREST/
# API-Keys in EnvironmentFile + Wants/After=network-online.target
# → vollständige Service-Datei in docs/systemd-install.md
```

### Ollama-Modell nutzen
```bash
# Modell installieren
ollama pull llama3.3

# Server neu starten
systemctl restart sigorest  # oder: ./sigoREST/sigoREST

# Nutzen (auto-discovered)
curl -s http://localhost:9080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"ollama-llama3.3","messages":[{"role":"user","content":"Hallo"}]}'
```

### Debugging REST-Server
```bash
# Debug-Logs
./sigoREST/sigoREST -v debug

# Health check (Circuit Breaker Status)
curl -s http://localhost:9080/api/health | jq

# Memory-Block prüfen
curl -s http://localhost:9080/api/memory

# Session-Datei prüfen
cat .sessions/claude-h-mein-projekt.json
```

### Session löschen
```bash
rm .sessions/claude-h-mein-projekt.json
```