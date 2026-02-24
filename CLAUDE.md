# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Developer

- <IMPORTANCE>Speak german with the User, say "Du" and "Gerhard"</IMPORTANCE>

## Project Overview

sigoREST ist ein drei-schichtiges Go-Projekt mit zwei User-Interfaces:

- **sigoE CLI**: Command-line Engine für direkte AI-Abfragen
- **sigoREST Server**: OpenAI-kompatibler REST-Server für parallele Verbindungen (~100)

Beide nutzen das **Shared Package** `sigoengine` für:
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
Kein formales Test-Suite. Manual testing via CLI und REST-API wie oben gezeigt.

## Architecture

### Drei-Schichten-Design

```
sigorest/
├── sigoengine/engine.go    # Shared Package (thread-safe)
├── cmd/sigoE/main.go       # CLI-Wrapper (~170 Zeilen)
├── sigoREST/main.go        # REST-Server (~840 Zeilen)
├── sigoREST/models.csv     # Modell-Quelle (embedded + Disk)
└── sigoREST/memory.json    # Globaler Memory-Block (embedded + Disk)
```

### sigoengine/engine.go — Shared Package

Thread-safe Package für CLI und REST. Exportiert:

| Export | Zweck |
|--------|-------|
| `MammothModels` | Model-Registry (Map: name → config) |
| `LoadConfig(model)` | Lädt ProviderConfig aus Registry + ENV |
| `CallAPI(ctx, cfg, req, timeout)` | HTTP-Call mit Retry |
| `CircuitBreaker` | Pro-Modell Fehlerisolierung |
| `Session{History []Message}` | Gesprächsverlauf (max 20) |
| `Log*()` | Thread-safes Logging (DEBUG/INFO/WARN/ERROR/FATAL) |
| `DiscoverOllamaModels(endpoint)` | Auto-Discovery lokaler LLMs |
| `ResolveModelName(shortcode)` | Shortcode → vollständiger Name |

**Thread-Safety:**
- `sync.RWMutex` für Logging-Konfiguration
- `sync.Once` für Shortcode-Lookup-Map
- `sync.RWMutex` für Ollama-Registry

### cmd/sigoE/main.go — CLI-Wrapper

Schlanker Wrapper der sigoengine nutzt. Rückwärtskompatibel zum ursprünglichen sigoEngine Binary.

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

**Embedded + Disk Pattern:**
- `//go:embed models.csv` und `//go:embed memory.json` als Defaults
- Disk-Dateien haben Vorrang wenn vorhanden → Server läuft ohne externe Files

**Endpoints:**
| Pfad | Methode | Zweck |
|-------|---------|-------|
| `/v1/chat/completions` | POST | OpenAI-kompatible Chat API |
| `/v1/models` | GET | Modell-Liste (ID + Shortcode) |
| `/api/models` | GET | Volle Modell-Infos (Preise, Limits) |
| `/api/health` | GET | Server-Status + Circuit-Breaker |
| `/api/memory` | GET/PUT | Globaler Memory-Block |

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

### Modell-Konfiguration (models.csv)

Semikolon-getrennte CSV mit 11 Feldern:
```
id;shortcode;endpoint;apikey;max_input;max_output;input_cost;output_cost;min_temp;max_temp;requires_completion_tokens
```

**Felder:**
- `id`: Vollständiger Modellname (z.B. `claude-3-5-haiku-20241022`)
- `shortcode`: Kurzbezeichnung (z.B. `claude-h`)
- `endpoint`: API URL
- `apikey`: Environment-Variable (leer bei Ollama)
- `max_input`: Kontext-Fenster
- `max_output`: Max Ausgabe-Tokens
- `input_cost/output_cost`: $/1M Tokens
- `min_temp/max_temp`: Gültiger Temperatur-Bereich
- `requires_completion_tokens`: `true` für GPT-5 (nutzt `max_completion_tokens` statt `max_tokens`)

**Warum Semikolon?** Komma ist zu verbreitet (CSV-Standard, JSON-Arrays). Semikolon erlaubt kommagetrennte Listen ohne Escaping.

**Shortcode-Resolution:** Der Server prüft zuerst nach ID, dann scannt er alle Shortcodes. Bei Treffer wird die ID für den API-Call verwendet.

### Ollama Auto-Discovery

Ollama-Modelle werden beim Serverstart via `GET /api/tags` entdeckt:

```go
ollamaEndpoint := "http://localhost:11434"
sigoengine.DiscoverOllamaModels(ollamaEndpoint)
```

**Shortcode-Schema:**
- `llama3:latest` → `ollama-llama3` (`:latest` weggeschnitten)
- `gemma3:12b` → `ollama-gemma3-12b` (andere Tags als Suffix)

Ollama-Modelle haben keine API-Key (`APIKey: ""`) und nutzen `http://localhost:11434/v1/chat/completions`.

**Limitation:** Nur Startzeit-Discovery → Neustart nötig nach `ollama pull`.

### Circuit Breaker

Pro Modell ein Circuit Breaker (nicht global):

| Parameter | Wert |
|-----------|-------|
| Threshold | 3 aufeinanderfolgende Fehler |
| Timeout | 5 Minuten vor Reset-Attempt |
| Scope | Pro Modell (`map[string]*CircuitBreaker`) |

Fehler bei einem Modell blockieren andere nicht.

### Session-Management

Sessions als JSON-Dateien:
- Pfad: `.sessions/<model>-<sessionID>.json`
- Max 20 Messages pro Session (älteste werden verworfen)
- Modell-spezifisch (`claude-h-projekt-a` vs `gpt41-projekt-a`)

### Logging

Thread-safes, strukturiertes Logging:

```go
sigoengine.SetLogLevel(sigoengine.ParseLogLevel("debug"))
sigoengine.SetJSONMode(true)   // Machine-parsable
sigoengine.SetQuietMode(true)  // Nur ERROR und FATAL
```

**Ausgabe:** stderr (stdout bleibt sauber für UNIX piping)

## Important Notes

- **Go-Modul**: `sigorest` mit Go 1.26
- **Embedded Files**: models.csv und memory.json sind eingebettet, aber Disk hat Vorrang
- **IPv6**: Geblockt (außer `::1` loopback)
- **TLS**: Self-signed Zertifikat wird beim ersten Start automatisch generiert
- **Ports**: 8080/8443 sind auf Gerhards System belegt (lokaler Webserver)

## Retrospektiven

Detaillierte Session-Historie: Siehe `RETROSPECTIVE.md`

## Common Tasks

### Neues Modell hinzufügen
Eintrag in `sigoREST/models.csv` hinzufügen. Format siehe oben.

### REST-Server als systemd-Service installieren
```bash
sudo cp sigoREST/sigoREST /usr/local/sbin/sigoREST
sudo mkdir -p /usr/local/slib/sigoREST
sudo cp sigoREST/models.csv /usr/local/slib/sigoREST/
sudo cp sigoREST/memory.json /usr/local/slib/sigoREST/
# Siehe systemd-install.md für Service-Datei
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
