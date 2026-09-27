# CLAUDE.md

claude --worktree anthropic-messages-bridge --resume cd4cd259-7826-41a6-b845-5383fc49acf2


Anleitung für Claude Code (claude.ai/code) bei Arbeit mit diesem Repo.

## Developer

- <IMPORTANCE>Deutsch mit User, "Du" und "Gerhard"</IMPORTANCE>

## Project Overview

sigoREST = drei-schichtiges Go-Projekt, zwei User-Interfaces:

- **sigoE CLI**: Command-line Engine für direkte AI-Abfragen
- **sigoREST Server**: OpenAI-kompatibler REST-Server für parallele Verbindungen (~100)

Beide nutzen **Shared Package** `sigoengine` für:
- Model-Registry (Modelle von Mammouth.ai, Moonshot.ai, Z.ai, Longcat, cheaperinference)
- API-Abstraktion (OpenAI + Anthropic Formate)
- Multi-Channel-Support mit Failover + pro-Kanal Rate-Limiting + Health-Monitor
- Circuit Breaker + Retry Logic
- Session-Management (JSON-basiert)
- Thread-safes Logging

## Development Commands

### Alle Programme bauen (Makefile → `./build/`)
Alle kompilierten Binaries landen im `./build/`-Verzeichnis (in `.gitignore`).
```bash
make build          # alle: sigoREST, sigoE, mockprovider
make sigorest       # nur REST-Server
make sigoe          # nur CLI
make mockprovider   # nur Mock-Provider (für Rate-Limit-Tests)
make test           # alle Tests
make clean          # ./build/ entfernen
```

### REST-Server starten
```bash
# HTTP localhost:9080, HTTPS privates Netz:9443
./build/sigoREST -v debug
```

### CLI nutzen
```bash
# Alle verfügbaren Modelle auflisten
./build/sigoE -l

# Prompt senden
echo "Hallo" | ./build/sigoE -m claude-h

# Mit Session
./build/sigoE -m claude-h -s projekt-x "Erste Nachricht"
./build/sigoE -m claude-h -s projekt-x "Zweite Nachricht"
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
Abgedeckt u.a.: `retry`, `shortcode`, `usage`, `finish_reason`, `env`,
`commlog`, `models_csv`. Der Server hat Handler-Tests in
`sigoREST/main_test.go` (u.a. mit Fake-Ollama `startFakeOllama`); die CLI hat
keine Go-Tests → manuell testen. Nach Server-Änderungen zusätzlich einen
echten Call gegen einen Test-Server auf freiem Port (`-http-port 19080
-https-port 19443 -data-dir <tmp>`) — der Live-Dienst belegt 9080/9443.

## Architecture

### Drei-Schichten-Design

```
sigorest/
├── sigoengine/                  # Shared Package (thread-safe), mehrere Dateien:
│   ├── engine.go                #   CallAPI, CircuitBreaker, Session, Logging
│   ├── models.go                #   Model-Typ, CoreModels (Fallback-Liste)
│   ├── models_registry.go       #   Lookup-Maps, Laden JSON→CSV→CoreModels
│   ├── provider_fetchers.go     #   Dynamischer Abruf Mammoth/Moonshot/ZAI/Longcat/cheaperinference
│   ├── retry.go                 #   FetchWithRetry (Backoff gegen Boot-DNS-Race)
│   ├── shortcode.go             #   Shortcode-Generierung (Familie+Version+Variante)
│   ├── channel.go               #   Channel-Datenmodell + Registry (Multi-Channel)
│   ├── channel_manager.go       #   Kanal-Auflösung + Failover-Logik
│   ├── channel_health.go        #   Hintergrund-Health-Monitor (lazy, kein Chat-Ping)
│   ├── loadconfig_channel.go    #   LoadConfig-Erweiterung für Kanal-Auswahl
│   ├── rate_limiter.go          #   Pro-Kanal Rate-Limiter (hybrid, siehe unten)
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
└── sigoREST/memory.json         # Globaler Memory-Block (embedded + Disk)
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
| `Fetch{Mammouth,Moonshot,ZAI,Longcat,Cheaperinference}Models()` | Dynamischer Modell-Abruf pro Provider |
| `FetchWithRetry(name, attempts, backoff, fn)` | Retry-Wrapper mit Backoff um einen Fetcher |
| `GenerateShortcode(id, used)` | Sprechender Shortcode aus Modellname |
| `ChannelRegistry` / `ChannelManager` | Multi-Channel-Verwaltung + Failover-Auflösung |
| `LoadConfigWithChannel(model, ch)` | Wie `LoadConfig`, aber für einen bestimmten Kanal |
| `RateLimiter.Acquire/Release` | Pro-Kanal hybrides Rate-Limiting (→ `ErrRateLimited`) |
| `StartHealthMonitor(...)` | Lazy Hintergrund-Health-Check (GET `/models`, kein Chat-Ping) |
| `OpenCostDB(dataDir)` | Öffnet/erstellt `costs.db` (SQLite, WAL), Schema-Migration |
| `CostDB.RecordUsage/Summary/CheckBudget` | Kosten-Event schreiben, Zeitraum aggregieren, Budget prüfen |
| `CalcCostUSD(inTok, outTok, inPrice, outPrice)` | Token→USD anhand Modell-Preisen ($/1M Tokens) |
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
**dynamisch** über `loadModelsFromProviders()` (Mammoth/Moonshot/ZAI/Longcat/cheaperinference
per HTTP) plus Ollama-Discovery — **nicht** aus einer models.csv. Nur
`memory.json` ist embedded (`//go:embed memory.json`), Disk hat Vorrang. Die
CSV/Registry (`models_registry.go`) ist primär für die CLI; der Server nutzt
sie nicht.

**Endpoints:**
| Pfad | Methode | Zweck |
|-------|---------|-------|
| `/v1/chat/completions` | POST | OpenAI-kompatible Chat API |
| `/v1/models` | GET | Modell-Liste (ID + Shortcode), `owned_by` = kanonischer Provider |
| `/v1/messages` | POST | Anthropic-Messages-API-Bridge (Claude Code o.ä.), Tool-Calling, alle Provider |
| `/api/models` | GET | Volle Modell-Infos (Preise, Limits, `provider`/`provider_code`); `?format=csv` → Registry-CSV (als CLI-`models.csv` nutzbar) |
| `/api/shortcodes` | GET | Kompaktes Mapping `{id: shortcode}` (nach ID sortiert) |
| `/api/shortlist` | GET | Kompakt: Shortcode + Provider + 5-Zeichen-Code, sortiert |
| `/api/channels` | GET | Status aller Kanäle (inkl. `min_interval_ms`/`max_wait_ms`) |
| `/api/channels/:provider/:name` | GET | Einzelkanal-Detail |
| `/api/channels/:provider/:name/enable` | POST | Kanal aktivieren |
| `/api/channels/:provider/:name/disable` | POST | Kanal deaktivieren |
| `/api/channels/:provider/:name/memory` | GET/PUT | Kanal-spezifischer Memory-Block |
| `/api/channels/:provider/:name/system-prompt` | GET/PUT | Kanal-spezifischer System-Prompt |
| `/api/health` | GET | Server-Status + Circuit-Breaker |
| `/api/memory` | GET/PUT | Globaler Memory-Block |
| `/api/usage`  | GET | Token-Statistiken (RAM, Reset bei Neustart) |
| `/api/costs`  | GET | Token-Statistiken + USD-Kosten, persistent (SQLite, `costs.db`) |
| `/api/budget` | GET/PUT | Tages-/Monats-Budget-Limits + Verbrauchsstatus, optional Hard-Stop |
| `/api/system-prompt` | GET/PUT | Globaler System-Prompt |
| `/api/version` | GET | Version + Component-Name |
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

`loadModelsFromProviders()` ruft beim Start sequenziell fünf Provider-APIs ab.
Jeder Fetcher ist in `FetchWithRetry` gewickelt (4 Versuche, 2s/4s/8s Backoff).
Einzelne Fehlschläge werden geloggt; der Server startet mit dem Rest weiter.

**Wichtige Fallback-Asymmetrie** (relevant bei Netz-/DNS-Problemen):
| Provider | bei Fehler | Folge |
|----------|-----------|-------|
| Mammoth (`/public/models`, kein Key) | `return nil, err` | 0 Modelle |
| Moonshot (`MOONSHOT_API_KEY`) | `return nil, err` | 0 Modelle |
| ZAI (`ZAI_API_KEY`) | `return zaiStaticModels, nil` | statische Modelle |
| Longcat (`LONGCAT_API_KEY`) | `return longcatKnownModels, nil` | statische Modelle |
| cheaperinference (`OMNIROUTE_API_KEY`) | `return nil, err` | 0 Modelle (kein Static-Fallback — Preise sind der Zweck) |

→ Wenn beim Boot nur die statischen ZAI/Longcat-Modelle erscheinen ("no such
host" im Log): DNS war beim Start noch nicht oben. Schutz: systemd-Unit mit
`Wants/After=network-online.target` (nicht `network.target`!) **plus** der
Retry. Siehe `docs/systemd-install.md`. Workaround zur Laufzeit:
`systemctl restart sigoREST`.

**cheaperinference — Aggregator statt Einzel-Provider:** `GET /v1/models`
liefert live Preise (`pricing.input_per_million`/`output_per_million`) und
Kontextfenster (`context_length`/`max_output_tokens`) für alle Modelle mit,
gefiltert auf `type=="text" && endpoint=="/v1/chat/completions"` (aktuell 60
von 66, Rest sind Bild-/Video-Modelle). Kein statisches Known-Model-Mapping
nötig wie bei ZAI/Longcat/Moonshot. IDs sind `ci-`-präfixt (z.B.
`ci-claude-opus-5`), weil dieselben Modelle oft auch direkt über
Mammouth/Moonshot/ZAI laufen und sonst die ID-Map kollidieren würde.
`sigoengine.Model.UpstreamID` trägt den unpräfixten Original-Namen; in
`handleChatCompletions` wird `cfg.Model` (nicht nur `cfg.Endpoint`) damit
überschrieben, sonst schickt der Server `"ci-claude-opus-5"` als `model`-Feld
raus, das die API nicht kennt.

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
Optional danach `;provider;provider_code;upstream_id` — so exportiert
`GET /api/models?format=csv` (`sigoengine.WriteModelsCSV`, Kopfzeile mit
`#` = Parser-Kommentar). Beim Laden wird nur `upstream_id` übernommen
(nötig für `ci-*`), Provider wird neu berechnet.
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

**Kanal ohne Key (Bug bis 2026-09-27):** Chat läuft auch für Ollama über
`ChannelManager.Resolve`, aber `DiscoverFromEnv` legt Kanäle nur für
Provider mit API-Key an (`knownProviders`). Ollama hatte deshalb keinen
Kanal — jeder Ollama-Chat (auch `/v1/messages`) endete mit `404
CONFIG_NOT_FOUND: no active channel for provider`, ohne Ollama je zu
erreichen (Embeddings liefen, weil `/v1/embeddings` keinen Kanal nutzt).
Fix ohne Sonderweg im Handler: Discovery läuft jetzt VOR der Registry,
`newChannelRegistry` (`sigoREST/main.go`) registriert bei gefundenen
Modellen `AddKeylessChannel("ollama")` → Kanal `ollama-default`, danach
`LoadState` (per API deaktiviert bleibt deaktiviert). Regressionstest:
`TestHandleChatCompletions_OllamaWithoutAPIKey`.

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

### Multi-Channel, Failover, Rate-Limiting, Health-Monitor

Ein Provider kann mehrere **Kanäle** (API-Keys) haben (`ChannelRegistry` in
`channel.go`, Config `channels.json`). Bei einem Request probiert der
`ChannelManager` (`channel_manager.go`) die Kanäle eines Providers der Reihe
nach durch — Failover ist transparent für den Client.

**Rate-Limiter (hybrid, pro Kanal, nicht pro Provider):** `RateLimiter.Acquire`
wartet bis `minInterval` seit letztem Call vergangen ist; würde die Wartezeit
`maxWait` überschreiten, schlägt der Call mit `ErrRateLimited` fehl → Server
probiert den nächsten Kanal. Erst wenn alle Kanäle eines Providers erschöpft
sind, geht HTTP 429 + `Retry-After` an den Client. Konfigurierbar global
(`-rate-min-interval`, `-rate-max-wait`) und pro Kanal (`MinInterval`/`MaxWait`
in `channels.json`, ms). Granularität ist bewusst pro Kanal: ein
Provider-weiter Limiter würde alle Failover-Keys gleichzeitig blockieren.

**Health-Monitor** (`channel_health.go`, `StartHealthMonitor`): lazy
Hintergrund-Check, reaktiviert Reserve-Kanäle bei Bedarf. Probe läuft über
GET `/models` (oder Äquivalent), **nicht** über einen echten Chat-Call — kein
API-Kosten-Verbrauch im Leerlauf.

**Manuelles disable hat Vorrang (Gerhards Vorgabe, 2026-09-27):**
`/api/channels/.../disable` → `SetActiveManual` → `Channel.ManuallyDisabled`
(persistiert in `channels.json`, sichtbar in `/api/channels`). Der
Health-Monitor überspringt solche Kanäle bei der Reserve-Suche und
aktiviert sie **nie** wieder — auch nicht, wenn es der einzige Kanal des
Providers ist; eine andere Reserve darf einspringen. Nur `/enable`
(`SetActiveManual(true)`) löscht das Flag. Automatische Übergänge
(Health-Monitor, Auth-Fehler im Chat-Handler) nutzen `SetActive` und
lassen das Flag unangetastet. Vorher holte `runHealthChecks` einen
manuell abgeschalteten einzigen Kanal nach einem Intervall zurück.

Details/Historie siehe `RETROSPECTIVE.md`, Session 2026-08-18.

### Kosten-Tracking (`sigoengine/costdb.go`, `sigoREST/costhandlers.go`)

Persistente SQLite-Datenbank (`modernc.org/sqlite`, pure Go, kein CGO,
WAL-Mode) unter `<data-dir>/costs.db`, ergänzend zum RAM-only
`/api/usage`. Jeder abgeschlossene Chat-Call (`/v1/chat/completions`
und `/v1/messages`, über `recordUsage`/`recordUsageWithSession` in
`main.go`) schreibt ein Event in `usage_events`: Modell, Provider, Kanal,
Session-ID (optional), Tokens, sowie USD-Kosten berechnet aus
`ModelInfo.InputCost`/`OutputCost` ($/1M Tokens, `CalcCostUSD`). Modelle
ohne Preisangabe (Ollama) landen mit $0 Kosten im Log, nicht komplett
ohne Eintrag — Token-/Request-Zahlen bleiben so auswertbar.

**Nil-safe by design:** `Server.costDB` ist ein `*sigoengine.CostDB`, den
alle Methoden auf `nil`-Empfänger sauber abfangen (No-Op statt Panic).
Schlägt `OpenCostDB` beim Serverstart fehl (z.B. Disk voll,
Berechtigungsproblem), läuft der Server ohne Kosten-Persistenz weiter —
`recordUsage` überspringt den DB-Write, `/api/costs`/`/api/budget`
antworten mit `503 cost_tracking_disabled`. Ein DB-Fehler beim
Schreiben selbst wird nur geloggt (`LogWarn`), niemals dem
API-Response-Pfad in den Weg gestellt.

**Budget-Check vor jedem Call:** In `handleChatCompletions`, direkt nach
dem Provider-Ping (vor dem eigentlichen API-Call, gleiches Muster wie
"Provider nicht erreichbar → kein API-Call"). `CheckBudget(now)` prüft
Tages-/Monats-Ausgaben (`spendSince`, lokale Zeitzone via
`StartOfDay`/`StartOfMonth`) gegen die konfigurierten Limits
(`budget_config`-Tabelle, Singleton-Zeile). Nur bei `hard_stop_enabled:
true` UND überschrittenem Limit wird der Call mit `HTTP 402
budget_exceeded` abgelehnt — Default ist reines Tracking, kein Eingriff.

**Bekannter Bug + Fix (Off-by-One bei `Summary()`):** gespeicherte
Timestamps sind ganze Sekunden (`ts.Unix()`), aber `until` in
`/api/costs` ist `time.Now()` mit Nanosekunden-Anteil. Ohne Rundung
schneidet `until.Unix()` (floor) exakt die Sekunde ab, in der ein Event
GERADE JETZT geschrieben wurde — beobachtet als "Chat-Call meldet Erfolg,
`/api/costs` zeigt trotzdem 0 Requests", wenn beide im selben
Sekunden-Tick liegen. Fix: `ceilUnix()` rundet `until` für die
`Summary()`-Query auf die nächste volle Sekunde auf (Regressionstest:
`TestSummary_IncludesEventFromSameInstantAsUntil`).

### ID-/Shortcode-Registry (`sigoengine/id_registry.go`)

Persistente SQLite-Datenbank (`modernc.org/sqlite`, WAL) unter
`<data-dir>/id_registry.db`. Sie vergibt jedem Provider ein
3-Zeichen-Kürzel und jedem `(provider, upstream_id)`-Paar **einmalig**
einen Shortcode im Format `{provider3}-{semanticCode}` (z.B.
`zai-glm45`, `mam-cl45-s`, optional Kanal-Suffix `-2`). Einmal vergeben,
bleibt ein Shortcode für immer bei seinem Modell und wird nie
wiederverwendet — das löst das Problem, dass der Server seine Modelle
bei jedem Boot dynamisch von Live-Provider-APIs lädt und pro Boot neu
berechnete Kürzel sich bei jeder Änderung der Live-Liste verschoben.

- **Semantischer Code kommt vom Fetcher, nicht aus einer Neuberechnung:**
  `SyncProvider` bekommt pro Modell einen `ProviderModelSeed` mit
  `SemanticHint` (= `Model.Shortcode` aus `provider_fetchers.go`, bei
  cheaperinference ohne das `ci-`-Präfix). Die Fetcher berechnen ihre
  Codes mit einer fetch-weiten `used`-Map und liefern deshalb innerhalb
  eines Providers unterscheidbare Kürzel.
- **Bug behoben (war bis 2026-09-21 live): IDs ohne Familien-Präfix
  kollabierten auf denselben Code.** `GenerateShortcode`s No-Family-Zweig
  (`shortcode.go`) rief bei fehlendem Präfix-Treffer (`ci-...`,
  `LongCat-...`) `cutterCode(modelID)` auf und gab sofort zurück, OHNE
  die `used`-Map zu konsultieren — Schritt 6 (Kollisionsauflösung) griff
  nur im Familien-Pfad. Da `cutterCode` nur den längsten Tabellen-Präfix
  zurückgibt (nicht den Rest der ID), bekamen z.B. alle unbekannten
  Longcat-Modelle denselben Code (`l62`). Fix: gemeinsamer
  `resolveCollision(sc, used)`-Helper, jetzt von beiden Zweigen genutzt
  — No-Family-IDs bekommen bei Kollision denselben `-2`/`-3`-Suffix wie
  familien-basierte Codes (`l62`, `l62-2`, `l62-3`). Regressionstest:
  `TestGenerateShortcode_NoFamilyMatchStillDeduplicates`.
- **Kollisions-Suffix ist `.2`/`.3`**, nicht `-2` — `-` trennt den Kanal
  ab, `zai-glm45-2` wäre sonst zweideutig.
- **Retire nach 3 aufeinanderfolgenden ERFOLGREICHEN Fetches ohne das
  Modell** (`miss_streak`); ein fehlgeschlagener Fetch lässt den Zähler
  unangetastet. Retired Modelle bleiben auflösbar und werden mit
  **HTTP 410** abgelehnt (kein stiller Fallback auf ein anderes Modell).
  Taucht ein retired Modell wieder auf, bekommt es seinen alten
  Shortcode zurück.
- **Nil-safe wie `costDB`:** schlägt `OpenIDRegistry` beim Start fehl,
  läuft der Server mit pro Boot berechneten Shortcodes weiter. Scheitert
  die Zuweisung für ein einzelnes Modell, wird nur dieses Modell
  übersprungen (`LogWarn`) — nicht der ganze Provider-Sync.

**Zwei operative Eigenheiten, die man kennen sollte:**

1. **Shortcodes ändern sich einmalig beim Upgrade.** Beim ersten Boot mit
   dieser Version werden alle Kürzel neu vergeben: aus `glm46` wird
   `zai-glm46`, aus `cl45-s` wird `mam-cl45-s`. Es gibt bewusst keine
   Kompatibilitätsschicht — Clients (Skripte, `ANTHROPIC_BASE_URL`-Configs,
   C++-Client) müssen `/api/shortcodes` einmal neu abrufen. Siehe
   `TODO.md`, Abschnitt "Shortcode".
2. **Retirement zählt Boots, nicht Fetch-Zyklen.** `SyncProvider` wird nur
   aus `loadModelsFromProviders()` aufgerufen, und das passiert
   ausschließlich beim Serverstart. Auf einem langlaufenden Deployment
   braucht ein verschwundenes Modell also 3 **Neustarts** bis zum
   Retire, nicht 3 Abrufe. Der HTTP-410-Pfad ist damit erreichbar, aber
   in der Praxis träge. Bewusste Scope-Entscheidung (Boot-Sync statt
   Hintergrund-Poller), kein Bug.

### Provider-Kennzeichnung (`sigoengine/provider_id.go`)

Vorher: drei unabhängige, unterschiedlich vollständige
Provider-Erkennungen — `sigoREST/main.go:providerForModel()` (nur
Endpoint-Substring + Namens-Fallback), `cmd/sigoE/main.go:listAllModels()`
(nur Mammoth/Moonshot/Z.ai, alles andere landete in "Other" — Longcat,
cheaperinference, Ollama fehlten komplett) und `sigoengine/channel.go:
knownProviders` (nur für ENV-Var-Discovery gedacht, nie für
Anzeige-Zwecke). Jetzt eine kanonische Quelle:

- `ProviderFromEndpoint(endpoint)` — erkennt anhand der Endpoint-URL
  (`mammouth`, `moonshot`, `z.ai`, `longcat`, `cheaperinference`,
  `localhost:11434`/`127.0.0.1:11434` → `ollama`); `""` bei keinem Match
- `ProviderFromModelID(modelID)` — Namens-Heuristik als Fallback
  (`ollama-`-Präfix, `kimi`, `glm`, `longcat`, `ci-`-Präfix), Default
  `"mammouth"`
- `ResolveProvider(endpoint, modelID)` — kombiniert beide: Endpoint
  zuerst (zuverlässiger, vom Server selbst gesetzt), Namens-Heuristik
  nur als Fallback
- `ProviderCode(provider)` — normiert auf festen 5-Zeichen-Code
  (`providerCodes`-Map: `mammo`, `moons`, `zai__`, `longc`, `cheap`,
  `ollam`; unbekannte Provider werden mit `_` aufgefüllt bzw. hart auf 5
  Zeichen gekappt)

Genutzt von `sigoREST/main.go` (`providerForModel`/
`providerForModelLocked` sind dünne Wrapper, die die alte Logik
ersetzen), `/api/shortlist`, `/api/models` (`provider`/`provider_code`),
`/v1/models` (`owned_by` — vorher hart `"sigorest"`) und `cmd/sigoE`
(`-l`, `-i`).

**Zwei Bugs beim Umbau gefunden:**
1. **Rekursiver RLock**: `handleShortlist`/`handleModels` halten
   `s.mu.RLock()` per `defer` und riefen darin `providerForModel()` auf,
   das selbst nochmal `s.mu.RLock()` nimmt — Go's `RWMutex` garantiert
   das nicht deadlock-frei, wenn ein Writer dazwischen wartet. Fix:
   separate lock-freie `providerForModelLocked()` für Aufrufer, die den
   Lock schon halten.
2. **Ollama fiel auf `mammouth` zurück**: die alte Heuristik kannte
   weder `localhost:11434` noch das `ollama-`-Präfix — jetzt in
   `ProviderFromEndpoint`/`ProviderFromModelID` explizit behandelt.

### Anthropic-Messages-Bridge (`/v1/messages`)

Übersetzt Anthropic-Messages-Wire-Format (Request, Response, SSE-Streaming,
Tool-Calling) auf dieselbe interne Engine wie `/v1/chat/completions` — jedes
sigoREST-Modell ist damit auch über `ANTHROPIC_BASE_URL` (z.B. Claude Code)
erreichbar, unabhängig vom Provider-Wire-Format. Modellwahl ist 1:1 wie bei
`/v1/chat/completions` (ID/Shortcode, kein Alias). Keine Memory-/System-
Prompt-/Session-Injektion in diesem Pfad — der Client verwaltet seinen
eigenen Kontext. Details: `docs/superpowers/specs/2026-09-12-anthropic-messages-bridge-design.md`.

### Kommunikationsprotokoll (`sigoengine/commlog.go`, Flag `-comm-log`)

Optional (`-comm-log <pfad>`, leer = aus): jeder Chat-/Embedding-Call zum
Provider wird mit vollständigem Request/Response als JSONL geschrieben.
Umsetzung als `http.RoundTripper` (`NewCommLogTransport`), der in
`chatHTTPClient` (`CallAPI`/`CallAPIStream`, also auch `/v1/messages`) und
im Embedding-Client von `handleEmbeddings` steckt. Provider-Pings
(`defaultHTTPClient`), Health-Monitor und Boot-Modellabrufe laufen bewusst
**nicht** darüber — deshalb der eigene `chatHTTPClient`.

- Eintrag wird beim `Close()` des Response-Bodys geschrieben (Tee-Reader),
  sonst fehlte bei SSE alles nach den Headern. Aufrufer müssen den Body
  schließen (tun sie: `defer stream.Close()`).
- Gültiges JSON landet als eingebettetes Objekt (`json.RawMessage`, wird
  beim Marshal kompaktiert → bleibt eine Zeile), SSE/Sonstiges als String.
- Secret-Header → `[REDACTED]`; Bodies > 10 MiB abgeschnitten; Datei `0600`,
  Append-Modus (logrotate mit `copytruncate`).
- Öffnen fehlgeschlagen → Server-Start bricht ab (explizit angefordert,
  kein stilles Weiterlaufen ohne Protokoll). Ohne Flag: Transport reicht
  unverändert durch (`activeCommLog` = nil).

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
- **systemd**: Unit muss `Wants/After=network-online.target` setzen, sonst lädt beim Boot nur die ZAI-/Longcat-Fallback-Liste (DNS-Race)
- **API-Keys (ENV)**: `MAMMOUTH_API_KEY` (optional), `MOONSHOT_API_KEY`, `ZAI_API_KEY`, `LONGCAT_API_KEY`, `OMNIROUTE_API_KEY` (cheaperinference)
- **Kosten-DB**: `costs.db` (SQLite/WAL) im `-data-dir`; `modernc.org/sqlite` (pure Go) — kein CGO-Zwang im Build, bewusst analog zur Hermes-`state.db`-Entscheidung (lokaler Single-Process-Store, kein Netzwerk-Overhead)
- **Shortcode-DB**: `id_registry.db` (SQLite/WAL) im `-data-dir`, gleicher Treiber wie `costs.db`; hält Provider-Kürzel und Modell-Shortcodes assign-once fest (nie wiederverwendet) — Details unter "ID-/Shortcode-Registry"
- **Scope-Grenze**: sigoREST bleibt schlanker Proxy, kein Agent-Harness — bewusst kein Tool-Call-Repair o.ä.
- **IPv6**: Geblockt (außer `::1` loopback)
- **TLS**: Self-signed Zertifikat automatisch generiert beim ersten Start
- **Ports**: 8080/8443 belegt auf Gerhards System (lokaler Webserver)

## Retrospektiven

Detaillierte Session-Historie: Siehe `RETROSPECTIVE.md`

## Common Tasks

### Neues Modell hinzufügen
Server: Modelle kommen dynamisch vom Provider — bekannte Modelle werden in
`provider_fetchers.go` angereichert (`moonshotKnownModels`, `zaiStaticModels`,
`longcatKnownModels`, Mammoth via API). CLI: Eintrag in CSV/Registry.

### Neuen Provider hinzufügen
Reicht NICHT nur: `Fetch*`-Funktion in `provider_fetchers.go` + Eintrag in
`channel.go` (`knownProviders`) + Aufruf in `loadModelsFromProviders()`.
Zusätzlich nötig:

- **`modelsEndpointForProvider()` in `sigoengine/engine.go`** braucht einen
  `case` mit dem `/models`-Endpoint — sonst meldet die Health-Probe immer
  "unavailable" und der Health-Monitor aktiviert nie einen Kanal des
  Providers (bis 2026-09-27 bei longcat/cheaperinference so; abgesichert
  durch `TestModelsEndpointForProvider`, der `knownProviders` gegenprüft).
- **Provider-Erkennung** (heute `sigoengine/provider_id.go`:
  `ProviderFromEndpoint` + `ProviderFromModelID` + `providerCodes`; die
  Server-Funktion `providerForModel()` ist nur noch ein Wrapper darum)
  braucht den neuen Provider in beiden Zweigen (Endpoint-Match und
  Namens-Heuristik-Fallback) — sonst fällt jedes Modell des neuen Providers auf
den Default-Zweig (`mammouth`) zurück und Chat-Calls gehen mit falschem
API-Key raus (Symptom: HTTP 401 "incorrect api key", obwohl der Key gültig
ist — beim Longcat-Rollout genau so live aufgetreten). Nach jedem neuen
Provider einen echten End-to-End Chat-Call testen, nicht nur den Models-Fetch
beim Boot.

### REST-Server als systemd-Service installieren
```bash
sudo cp build/sigoREST /usr/local/sbin/sigoREST
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
systemctl restart sigorest  # oder: ./build/sigoREST

# Nutzen (auto-discovered)
curl -s http://localhost:9080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"ollama-llama3.3","messages":[{"role":"user","content":"Hallo"}]}'
```

### Kosten im Blick behalten / Budget setzen
```bash
# Verbrauch heute / diesen Monat
curl -s "http://localhost:9080/api/costs?period=today"
curl -s "http://localhost:9080/api/costs?period=month"

# Hartes Tages-/Monats-Limit setzen (Server lehnt Calls mit HTTP 402 ab,
# sobald das Limit erreicht ist)
curl -s -X PUT http://localhost:9080/api/budget \
  -H "Content-Type: application/json" \
  -d '{"daily_limit_usd":5,"monthly_limit_usd":100,"hard_stop_enabled":true}'

# Nur beobachten, nie blockieren: hard_stop_enabled auf false lassen/setzen
curl -s -X PUT http://localhost:9080/api/budget \
  -H "Content-Type: application/json" \
  -d '{"daily_limit_usd":5,"monthly_limit_usd":100,"hard_stop_enabled":false}'
```

### Debugging REST-Server
```bash
# Debug-Logs
./build/sigoREST -v debug

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
