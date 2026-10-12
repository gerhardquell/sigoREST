# Architektur

[← README](../README.md)

sigoREST ist ein dreischichtiges Go-Projekt mit zwei Oberflächen:

- **sigoE CLI** (`cmd/sigoE/main.go`): Kommandozeile für direkte KI-Abfragen.
  Schlanker Wrapper um `sigoengine`, rückwärtskompatibel zum ursprünglichen
  sigoEngine-Binary.
- **sigoREST Server** (`sigoREST/main.go`): OpenAI-kompatibler REST-Server für
  ~100 parallele Verbindungen, mit IP-basierter Zugriffskontrolle.

Beide nutzen das Shared Package **`sigoengine`** für Model-Registry,
API-Abstraktion (OpenAI- und Anthropic-Format), Multi-Channel-Support mit
Failover, Rate-Limiting pro Kanal, Health-Monitor, Circuit Breaker,
Retry-Logik, Session-Management und thread-sicheres Logging.

## Verzeichnisstruktur

```
sigorest/
├── sigoengine/                  # Shared Package (thread-safe), mehrere Dateien:
│   ├── engine.go                #   CallAPI, CircuitBreaker, Session, Logging
│   ├── models.go                #   Model-Typ, CoreModels (Fallback-Liste)
│   ├── models_registry.go       #   Lookup-Maps, Laden JSON→CSV→CoreModels
│   ├── provider_fetchers.go     #   Dynamischer Abruf Mammouth/Moonshot/ZAI/Longcat/cheaperinference/OpenRouter
│   ├── retry.go                 #   FetchWithRetry (Backoff gegen Boot-DNS-Race)
│   ├── shortcode.go             #   Shortcode-Generierung (Familie+Version+Variante)
│   ├── channel.go               #   Channel-Datenmodell + Registry (Multi-Channel), Env-Discovery
│   ├── channel_manager.go       #   Kanal-Auflösung + Failover-Logik
│   ├── channel_health.go        #   Hintergrund-Health-Monitor (lazy, kein Chat-Ping)
│   ├── loadconfig_channel.go    #   LoadConfig-Erweiterung für Kanal-Auswahl
│   ├── rate_limiter.go          #   Pro-Kanal Rate-Limiter (hybrid)
│   ├── session_memory.go        #   Session-/Memory-Pfade pro Kanal
│   ├── models_csv.go            #   WriteModelsCSV (Export im Registry-CSV-Format)
│   ├── commlog.go               #   Kommunikationsprotokoll (-comm-log, JSONL, RoundTripper)
│   ├── env.go                   #   Optionale ./.env Datei (veraltete ./env mit Warnung)
│   ├── costdb.go                #   Kosten-Tracking (SQLite, WAL) + Budget-Check
│   ├── id_registry.go           #   Persistente Shortcode-Registry (SQLite, assign-once)
│   ├── provider_id.go           #   Kanonische Provider-Erkennung + 5-Zeichen-Code
│   └── version.go               #   Zentrale Versions-Konstante
├── cmd/sigoE/main.go            # CLI-Wrapper
├── sigoREST/main.go             # REST-Server
├── sigoREST/costhandlers.go     # /api/costs, /api/budget Handler
└── sigoREST/memory.json         # Globaler Memory-Block (embedded, Disk hat Vorrang)
```

## sigoengine – wichtige Exporte

| Export | Zweck |
|--------|-------|
| `MammothModels` | Model-Registry (Map: name → config) |
| `LoadConfig(model)` | Lädt ProviderConfig aus Registry + ENV |
| `LoadConfigWithChannel(model, ch)` | Wie `LoadConfig`, aber für einen bestimmten Kanal |
| `CallAPI(ctx, cfg, req, timeout)` | HTTP-Call mit Retry → `(string, *UsageData, error)` |
| `UsageData` | Token-Verbrauch (InputTokens, OutputTokens, TotalTokens) |
| `CircuitBreaker` | Pro-Modell Fehlerisolierung |
| `Session{History []Message}` | Gesprächsverlauf (max 20) |
| `Log*()` | Thread-safes Logging (DEBUG/INFO/WARN/ERROR/FATAL) |
| `DiscoverOllamaModels(endpoint)` | Auto-Discovery lokaler LLMs |
| `ResolveModelName(shortcode)` | Shortcode → vollständiger Name |
| `Fetch{Mammouth,Moonshot,ZAI,Longcat,Cheaperinference,OpenRouter}Models()` | Dynamischer Modell-Abruf pro Provider |
| `FetchWithRetry(name, attempts, backoff, fn)` | Retry-Wrapper mit Backoff um einen Fetcher |
| `GenerateShortcode(id, used)` | Sprechender Shortcode aus Modellname |
| `ChannelRegistry` / `ChannelManager` | Multi-Channel-Verwaltung + Failover-Auflösung |
| `RateLimiter.Acquire/Release` | Pro-Kanal hybrides Rate-Limiting (→ `ErrRateLimited`) |
| `StartHealthMonitor(...)` | Lazy Hintergrund-Health-Check (GET `/models`, kein Chat-Ping) |
| `OpenCostDB(dataDir)` | Öffnet/erstellt `costs.db` (SQLite, WAL), Schema-Migration |
| `CostDB.RecordUsage/Summary/CheckBudget` | Kosten-Event schreiben, Zeitraum aggregieren, Budget prüfen |
| `CalcCostUSD(inTok, outTok, cachedTok, inPrice, outPrice, cachedPrice)` | Token→USD anhand Modell-Preisen ($/1M Tokens); `cachedTok` zum günstigeren `cachedPrice`, wenn >0 bekannt, sonst voll zu `inPrice` |
| `OpenIDRegistry(dataDir)` | Öffnet/erstellt `id_registry.db` (SQLite, WAL), Schema-Migration |
| `IDRegistry.AssignModel(provider, upstreamID, hint)` | Einmalig vergebener Shortcode für ein Modell (assign-once, Hint = semantischer Code des Fetchers) |
| `IDRegistry.SyncProvider(provider, seeds)` | Live-Liste abgleichen: Shortcodes vergeben, fehlende Modelle zählen/retiren |
| `IDRegistry.ResolveShortcode(input)` | Shortcode → Eintrag + Kanal-Suffix (auch retired Modelle) |
| `ResolveProvider(endpoint, modelID)` | Kanonische Provider-Erkennung (Endpoint zuerst, Namens-Heuristik als Fallback) |
| `ProviderCode(provider)` | Normiert Provider-Namen auf festen 5-Zeichen-Code (`mammo`, `zai__`, ...) |

**Thread-Safety:**
- `sync.RWMutex` für Logging-Konfiguration
- `sync.Once` für Shortcode-Lookup-Map
- `sync.RWMutex` für Ollama-Registry
- `sync.RWMutex` je Rate-Limiter-Key, `sync.RWMutex` für Channel-Registry
- Server: Wer `s.mu` schon hält, ruft die lock-freien Varianten
  (`providerForModelLocked()`, `lookupModelMemory()`), nie die
  selbstsperrenden — Go-`RWMutex` ist nicht reentrant, ein zweites
  `RLock()` kann deadlocken, sobald ein Writer wartet.

## REST-Server

**Zwei Listener teilen einen `http.ServeMux`:**
- HTTP `:9080` — nur localhost (127.0.0.0/8)
- HTTPS `:9443` — privates Netz (192.168.0.0/16, 10.0.0.0/8)

Details: [Konfiguration → Zugriffskontrolle](README_konfiguration.md#zugriffskontrolle).

**Modell-Quelle:** Der Server lädt seine Modelle beim Start **dynamisch**
über `loadModelsFromProviders()` per HTTP von den Providern plus
Ollama-Discovery — **nicht** aus einer `models.csv`. Nur `memory.json` ist
embedded (`//go:embed memory.json`), die Disk-Version hat Vorrang. Die
CSV/Registry (`models_registry.go`) ist für die CLI; der Server nutzt sie
nicht. Details: [Provider & Modelle](README_provider-modelle.md).

**Scope-Grenze:** sigoREST bleibt ein schlanker Proxy, kein Agent-Harness —
bewusst kein Tool-Call-Repair o.ä.

## Circuit Breaker

Pro Modell ein Circuit Breaker, nicht global. Der Server nutzt
`NewEnhancedCircuitBreaker` pro Modell (`handleChatCompletions`):

| Parameter | Wert |
|-----------|-------|
| Threshold | 5 Fehler |
| Window | 60s Zeitfenster |
| Cooldown | 10s vor Half-Open |
| HalfOpenMax | 3 Requests in Half-Open |
| Scope | Pro Modell (`map[string]*...`) |

Fehler bei einem Modell blockieren andere nicht. Zustand pro Kanal/Modell:
`GET /api/health`.

## Session-Management

Sessions sind JSON-Dateien, max. 20 Nachrichten pro Session (älteste werden
verworfen), modellspezifisch.

- **Server:** `<data-dir>/sessions/<provider>/<channel>/<model>-<sessionID>.json`
  (`session_memory.go`). Sessions sind pro Kanal isoliert — gleiche
  `session_id` auf verschiedenen Kanälen = verschiedene Dateien.
- **CLI / `sigoengine`-Default:** `.sessions/<model>-<sessionID>.json`
  relativ zum Arbeitsverzeichnis (`-session-dir`, Default `.sessions/`),
  z.B. `gpt41-projekt-a` vs `kimi-projekt-a`.

Bei Vision-Requests speichert die Session nur den Text, keine Bilddaten.

```bash
# Server-Session ansehen / löschen
cat /var/sigoREST/sessions/mammouth/default/mam-cl46-s-mein-projekt.json
rm  /var/sigoREST/sessions/mammouth/default/mam-cl46-s-mein-projekt.json

# CLI-Session ansehen / löschen
cat .sessions/gpt41-mein-projekt.json
rm  .sessions/gpt41-mein-projekt.json
```

## Logging

Thread-safes, strukturiertes Logging:

```go
sigoengine.SetLogLevel(sigoengine.ParseLogLevel("debug"))
sigoengine.SetJSONMode(true)   // Machine-parsable
sigoengine.SetQuietMode(true)  // Nur ERROR und FATAL
```

Ausgabe auf stderr — stdout bleibt sauber für UNIX-Pipes.

## Build-Details

- Go-Modul `sigorest`, Go 1.26
- SQLite über `modernc.org/sqlite` (pure Go, kein CGO-Zwang), WAL-Mode, für
  `costs.db` und `id_registry.db` — bewusst analog zur Hermes-`state.db`-
  Entscheidung (lokaler Single-Process-Store, kein Netzwerk-Overhead)
- Alle Binaries landen in `./build/` (Makefile, in `.gitignore`)
