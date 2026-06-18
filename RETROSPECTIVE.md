# sigoREST — Retrospektiven

Dieses Dokument enthält detaillierte Historie vergangener Entwicklungssessions.

---

## Session 2026-03-08: Konfigurierbarer models.csv Pfad

**Zielsetzung:**
sigoREST benötigt ein Flag um den Pfad zur `models.csv` explizit anzugeben (z.B. für systemd-Installationen unter `/usr/local/slib/sigoREST/models.csv`). Bei systemd-Installationen ist `~/.config/sigorest/` nicht verfügbar (kein User-Home).

**Was erreicht wurde:**

1. **Neues Flag `-models` in `sigoREST/main.go`**
   - `modelsPath := flag.String("models", "", "Pfad zur models.csv (optional)")`
   - Optionale Angabe, bei Nicht-Verwendung bleibt bestehende Ladereihenfolge erhalten

2. **Neue Funktionen in `sigoengine/models_registry.go`**
   - `SetModelsCSVPath(path string)` — Setzt den Custom-Pfad vor Registry-Initialisierung
   - `GetModelsCSVPath() string` — Getter für den gesetzten Pfad
   - `overrideModelsPath` Variable (package-level)

3. **Angepasste Ladereihenfolge in `loadModelsWithOverride()`**
   - Priorität 1: Custom Path (aus `-models` Flag)
   - Priorität 2: `~/.config/sigorest/models.json`
   - Priorität 3: `~/.config/sigorest/models.csv`
   - Priorität 4: System-weite Pfade (Projekt-Disk, etc.)
   - Priorität 5: `CoreModels` (embedded Fallback)

4. **Angepasste `loadModels()` in `sigoREST/main.go`**
   - Prüft zuerst `sigoengine.GetModelsCSVPath()`
   - Dann lokale `./models.csv`
   - Zuletzt embedded default
   - Logging zeigt Quelle des geladenen files

**Architektur-Entscheidungen:**

| Entscheidung | Begründung |
|--------------|------------|
| **sigoE bleibt unverändert** | CLI-Tool nutzt weiterhin automatische Suche. Die Änderung betrifft nur den Server, der unter systemd läuft. |
| **Getter/Setter Pattern** | `GetModelsCSVPath()` ermöglicht der lokalen `loadModels()` den Zugriff ohne direkte Variable-Export. |
| **Pfad vor Registry-Init setzen** | `SetModelsCSVPath()` muss vor dem ersten Registry-Zugriff aufgerufen werden, da `sync.Once` die Initialisierung nur einmal erlaubt. |
| **Optionales Flag** | Wenn `-models` nicht gesetzt, verhält sich der Server exakt wie vorher (Rückwärtskompatibilität). |

**Code-Änderungen:**

| Datei | Änderung |
|-------|----------|
| `sigoengine/models_registry.go` | `overrideModelsPath` Variable, `SetModelsCSVPath()`, `GetModelsCSVPath()`, Integration in `loadModelsWithOverride()` |
| `sigoREST/main.go` | `-models` Flag, Aufruf von `SetModelsCSVPath()`, angepasste `loadModels()` mit Custom-Pfad-Prüfung |

**Testing & Verifikation:**

```bash
# Build erfolgreich
go build ./...

# Ohne Flag (bestehendes Verhalten)
./sigoREST/sigoREST -v debug
# → "models.csv (embedded default) verwendet" oder "von Disk geladen"

# Mit custom Pfad
./sigoREST/sigoREST -models /usr/local/slib/sigoREST/models.csv -v debug
# → "Custom models.csv Pfad gesetzt"
# → "models.csv von custom Pfad geladen"

# Health check zeigt Modelle
curl -s http://localhost:9080/api/health | jq '.available_models'
```

**systemd-Service Beispiel:**

```ini
[Unit]
Description=sigoREST Server
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/sbin/sigoREST -models /usr/local/slib/sigoREST/models.csv
Restart=on-failure
User=sigorest
Group=sigorest

[Install]
WantedBy=multi-user.target
```

**Erkenntnisse & Learnings:**

1. **Timing ist kritisch bei sync.Once**: Die Registry-Initialisierung mit `sync.Once` passiert beim ersten Zugriff. Der Custom-Pfad muss davor gesetzt werden, sonst hat er keine Wirkung.

2. **Separation CLI vs Server**: sigoE läuft immer im User-Kontext und hat Zugriff auf `~/.config/`. sigoREST läuft oft als Service-User ohne Home-Verzeichnis — daher ist der explizite Pfad notwendig.

3. **Zweistufiges Loading**: Die `sigoengine` Registry und die lokale `loadModels()` im Server haben jetzt beide Custom-Pfad-Support. Das ist redundant, aber notwendig da sie unabhängige Loading-Strategien haben.

**Status:** ✅ Erfolgreich abgeschlossen

---

## Session 2026-03-08: Model Registry Refactoring

**Zielsetzung:**
Die Modell-Definitionen waren redundant verteilt zwischen `sigoengine/engine.go` (hardcodierte Map) und `sigoREST/models.csv` (CSV-Datei). Das Ziel war eine typisierte, zentrale Registry mit Override-Möglichkeit für User.

**Was erreicht wurde:**

1. **Neue typisierte Registry (`sigoengine/models.go`)**
   - `Model` struct mit 11 Feldern (ID, Shortcode, Endpoint, APIKeyEnv, etc.)
   - `CoreModels` Slice mit 5 Fallback-Modellen (3 Mammouth, 1 Moonshot, 1 ZAI)
   - Typsicherheit statt `map[string]interface{}` mit Type Assertions

2. **Registry-Logik (`sigoengine/models_registry.go`)**
   - Thread-safe mit `sync.RWMutex` und `sync.Once`
   - Ladereihenfolge: `~/.config/sigorest/models.json` → `~/.config/sigorest/models.csv` → `sigoREST/models.csv` → `CoreModels`
   - Lookup-Funktionen: `GetModelByID()`, `GetModelByShortcode()`, `GetAllModels()`
   - Ollama-Integration: `AddOllamaModel()` für Runtime-Discovery

3. **Anpassung `sigoengine/engine.go`**
   - `LoadConfig()` nutzt nun die neue Registry
   - `MammothModels` Map wird zur Laufzeit aus Registry befüllt (Abwärtskompatibilität)
   - Alte `ResolveModelName()`, `GetModelDefaultTokens()`, `GetModelTemperatureRange()` entfernt (Duplikate)

4. **Anpassung `cmd/sigoE/main.go`**
   - `listAllModels()` iteriert über `GetAllModels()` statt `MammothModels` Map
   - `showModelInfo()` nutzt `GetModelByID()` und `GetModelByShortcode()`

**Architektur-Entscheidungen:**

| Entscheidung | Begründung |
|--------------|------------|
 | **Nur 5 Core-Modelle embedded** | Binary-Größe minimieren, trotzdem funktionsfähig ohne externe Dateien. Vollständige Liste in `models.csv`. |
| **JSON vor CSV im User-Config** | JSON ist typisierter und moderner, aber CSV bleibt primär für einfache manuelle Pflege. |
| **Semikolon als CSV-Trennzeichen** | Komma ist in JSON-Arrays zu verbreitet. Semikolon erlaubt kommagetrennte Listen ohne Escaping. |
| **Thread-Safety mit RWMutex** | Registry wird von mehreren goroutines (REST-Server) gleichzeitig gelesen, Ollama-Discovery schreibt. |
| **Legacy `MammothModels` Map beibehalten** | Bestehender Code in `cmd/sigoE/main.go` nutzt die Map noch. Migration in kleinen Schritten. |

**Code-Änderungen:**

| Datei | Änderung |
|-------|----------|
| `sigoengine/models.go` | **NEU** - `Model` struct + `CoreModels` Slice |
| `sigoengine/models_registry.go` | **NEU** - Registry-Logik mit init(), Loading, Lookup |
| `sigoengine/engine.go` | `LoadConfig()` angepasst, Legacy-Map-Initialisierung, Duplikate entfernt |
| `cmd/sigoE/main.go` | `listAllModels()` und `showModelInfo()` auf neue Registry umgestellt |

**Testing & Verifikation:**

```bash
# Build erfolgreich
go build ./...

# 38 Modelle aus models.csv geladen
./sigoE -l | wc -l  # ~40 Zeilen

# Shortcode-Auflösung funktioniert
./sigoE -m cl-o -i  # Zeigt claude-opus-4-6 Info

# API-Requests funktionieren
echo "Hallo" | ./sigoE -m gpt41  # Antwort vom Modell
```

**Erkenntnisse & Learnings:**

1. **Single Source of Truth**: Die `models.csv` ist nun die primäre Definition. CoreModels sind nur noch Fallback.

2. **Typisierung zahlt sich aus**: `model.MaxOutputTokens` statt `info["max_output"].(int)` ist lesbarer und sicherer.

3. **CSV-Parsing Robustheit**: Semikolon als Trennzeichen vermeidet Escaping-Probleme bei JSON-Arrays in Feldern.

4. **Migration in kleinen Schritten**: Die `MammothModels` Map bleibt für Abwärtskompatibilität erhalten, wird aber aus der neuen Registry befüllt.

**Status:** ✅ Erfolgreich abgeschlossen

---

## Session 2026-03-07: Versions-Management und Health-Checks

**Zielsetzung:**
Versions-Informationen über die Kommandozeile verfügbar machen und einen einfachen Health-Check Endpoint für Load Balancer hinzufügen.

**Was erreicht wurde:**

1. **CLI Version Flag (`sigoE`)**
   - Neues Flag: `-V` (großes V, da `-v` bereits für Log-Level genutzt wird)
   - Ausgabe: `sigoE Version 1.0`
   - Konstante `const version = "1.0"` zentral definiert

2. **REST-Server Version Flag (`sigoREST`)**
   - Neues Flag: `-version`
   - Ausgabe: `sigoREST Version 1.0`
   - Gleiche Konstante für konsistente Versionsverwaltung

3. **HTTP Server-Header**
   - Neue Middleware: `serverHeaderMiddleware()`
   - Fügt `Server: sigoREST/1.0` zu jeder HTTP-Antwort hinzu
   - Wird in der Handler-Chain nach `ipMiddleware` eingebunden
   - Gilt für beide Listener (HTTP :9080 und HTTPS :9443)

4. **Ping Endpoint**
   - Neuer Endpoint: `GET /ping`
   - Antwort: Plain-Text `pong` (4 Bytes)
   - Ideal für Load Balancer Health Checks (schnell, kein JSON-Parsing)
   - Status: 200 OK

**Architektur-Entscheidungen:**

| Entscheidung | Begründung |
|-------------|--------------|
| **`-V` statt `-v` für CLI** | `-v` war bereits für Log-Level (`debug|info|warn|error`) belegt. Großes `-V` ist Unix-Konvention für Version. |
| **`-version` für REST-Server** | Server hat weniger Flags, daher ist ausgeschriebene `-version` lesbarer. |
| **Middleware-Chain-Reihenfolge** | `serverHeaderMiddleware` außen (zuerst aufgerufen, zuletzt verarbeitet) → Header wird auch für Fehlerantworten gesetzt. |
| **Plain-Text für `/ping`** | Load Balancer prüfen oft nur den Status-Code. JSON wäre Overhead für diesen Zweck. |
| **Separation von `/ping` und `/api/health`** | `/ping` = schneller Liveness-Check, `/api/health` = detaillierter Readiness-Check mit Circuit Breaker Status. |

**Code-Änderungen:**

| Datei | Änderung |
|-------|----------|
| `cmd/sigoE/main.go` | `const version`, `-V` Flag, `showVersion` Handler |
| `sigoREST/main.go` | `const version`, `-version` Flag, `serverHeaderMiddleware()`, `handlePing()` |

**Beispiele:**

```bash
# CLI Version
$ ./sigoE/sigoE -V
sigoE Version 1.0

# Server Version
$ ./sigoREST/sigoREST -version
sigoREST Version 1.0

# Server-Header in Antworten
$ curl -si http://localhost:9080/api/health | grep Server
Server: sigoREST/1.0

# Ping Endpoint
$ curl -i http://localhost:9080/ping
HTTP/1.1 200 OK
Server: sigoREST/1.0
Content-Type: text/plain

pong
```

**Lessons Learned:**

1. **Middleware-Komposition** — Durch das Wrappen von Handlern (`serverHeaderMiddleware(ipMiddleware(...))`) bleibt der Code modular und wiederverwendbar.

2. **Versions-Konstanten** — Zentrale Definition als `const` erleichtert zukünftige Releases (nur eine Stelle ändern).

3. **Header-Setzung** — Der `Server`-Header ist Teil der HTTP-Spezifikation und hilft Clients bei der API-Erkennung ohne zusätzliche Calls.

4. **Load Balancer Patterns** — Ein dedizierter `/ping` Endpoint ist schneller als `/api/health` (keine Locks, keine JSON-Serialisierung) und ideal für häufige Health-Checks.

**Status:** ✅ Erfolgreich abgeschlossen

---

## Session 2026-02-19

**Was gebaut wurde:**
sigoEngine wurde zu einem drei-schichtigen Projekt erweitert:
- `sigoengine/engine.go` — thread-sicheres Shared Package (sync.RWMutex für Logging, sync.Once für Shortcode-Map)
- `cmd/sigoE/main.go` — schlanker CLI-Wrapper (~170 Zeilen), rückwärtskompatibel
- `sigoREST/main.go` — REST-Server, OpenAI-kompatibel, IP-Zugriffskontrolle, TLS Auto-Cert

**Architektur-Entscheidungen:**
- **Shared Module** statt Code-Duplikat: engine.go einmal, CLI und Server nutzen dasselbe Package
- **Zwei Listener** (HTTP :9080 localhost, HTTPS :9443 privates Netz) teilen einen `http.ServeMux` — kein Code-Duplikat
- **Circuit Breaker pro Modell** (nicht global) — Fehler bei einem Modell blockieren andere nicht
- **Embedded Files** (models.csv, memory.json) mit Disk-Vorrang — Server läuft ohne externe Dateien

**Ollama Integration:**
- Auto-Discovery via `GET /api/tags` beim Serverstart — kein API-Key, keine Konfiguration
- Shortcode-Schema: `ollama-<name>` (`:latest` weggeschnitten, andere Tags als Suffix)
- `CallAPI` setzt Authorization-Header nur wenn APIKey nicht leer → funktioniert für Ollama ohne Key

**Bekannte Limitierungen / nächste Schritte:**
- Ollama-Discovery nur beim Start → Neustart nötig nach `ollama pull`
- models.csv und memory.json werden nur beim Start geladen (kein Hot-Reload)
- Mögliche Erweiterung: `POST /api/reload` für Ollama-Discovery zur Laufzeit

**Ports:**
- HTTP: **9080** (localhost), HTTPS: **9443** (privates Netz)
- 8080/8443 sind auf Gerhards System belegt (lokaler Webserver)

**systemd Installation:**
- Binary: `/usr/local/sbin/sigoREST`
- Konfiguration/Daten: `/usr/local/slib/sigoREST/`
- Siehe `systemd-install.md`

---

## Session 2026-02-23: Kompakte CSV als Modell-Quelle

**Zielsetzung:**
Umstellung von der redundanten `models.json` (~720 Zeilen, viele Wiederholungen) auf eine kompakte `models.csv` als primäre Modell-Quelle für sigoREST.

**Was erreicht wurde:**

1. **Neue CSV-Datei erstellt** (`sigoREST/models.csv`)
   - Format: Semikolon-getrennt mit 11 Feldern
   - `id;shortcode;endpoint;apikey;max_input;max_output;input_cost;output_cost;min_temp;max_temp;requires_completion_tokens`
   - Leere Felder erlaubt (z.B. Ollama ohne apikey)
   - 61 Modelle von verschiedenen Anbietern (GPT, Claude, Gemini, DeepSeek, etc.)

2. **Code-Umbau in `main.go`**
   - Neue `ModelInfo` struct mit allen Modell-Feldern
   - `loadModels()` ersetzt `loadAllowedModels()` (läd vollständige CSV statt nur Whitelist)
   - `Server` struct nutzt jetzt `models map[string]ModelInfo` statt `allowedModels map[string]bool`
   - `handleChatCompletions` liest Modell-Infos direkt aus `models` (ohne sigoengine.Abhängigkeit)
   - `handleModels` und `handleAPIModels` nutzen die neuen Daten
   - GPT-5 Unterstützung mit `max_completion_tokens` anstatt `max_tokens`

3. **Entfernte Datei:**
   - `models.json` gelöscht (nicht mehr benötigt)

**Architektur-Entscheidungen:**

| Entscheidung | Begründung |
|-------------|--------------|
| **Semikolon als Trennzeichen** | Komma ist zu verbreitet (CSV-Standard, JSON-Arrays). Semikolon erlaubt kommagetrennte Listen in Feldern ohne Escaping. |
| **Keine sigoengine.LoadConfig()** | REST-Server soll von `MammothModels` Registry unabhängig sein. CLI und REST können getrennt konfiguriert werden. |
| **ID + Shortcode in CSV** | OpenAI-kompatibilität (API nutzt vollständige IDs) + UX (Shortcodes für Menschen). `/v1/models` zeigt beide Formen. |

**Challenges und Lösungen:**

1. **Shortcode-Resolution:**
   - Problem: API-Requests nutzen sowohl vollständige IDs als auch Shortcodes.
   - Lösung: Zwei-Phasen-Validierung (zuerst nach ID suchen, dann nach Shortcode scannen)

2. **Ollama Discovery Integration:**
   - Problem: Ollama-Modelle werden zur Laufzeit entdeckt, nicht aus CSV.
   - Lösung: Ollama-Discovery fügt Modelle zur `models` Map hinzu statt zu `allowedModels`.

3. **Pointer vs Value:**
   - Problem: `sigoengine.CallAPI` erwartet `*ProviderConfig`, struct-Initialisierung erstellt Wert.
   - Lösung: Explizit `&sigoengine.ProviderConfig{...}` für Pointer-Initialisierung.

**Metriken:**

| Metrik | Vorher | Nachher |
|--------|---------|----------|
| Dateigröße (models) | ~24 KB (JSON) | ~8 KB (CSV) |
| Felder pro Modell | 8 (via sigoengine) | 11 (direkt) |
| Code-Zeilen (main.go) | ~770 | ~820 (+50 für neue Logik) |
| API-Response (/api/models) | 44 JSON-Einträge | 61 JSON-Einträge (vollständig) |

**Nächste Schritte (Optional):**
- [ ] Hot-Reload Endpunkt `POST /api/reload` für Laufzeit-Updates
- [ ] Model-Management API (CRUD-Endpunkte für Modelle)
- [ ] Optimierung: Index-Map für Shortcodes (O(1) Lookup)
- [ ] Dokumentation: CSV-Format in README.md dokumentieren

**Lessons Learned:**
1. Semikolon als Trennzeichen war eine gute Entscheidung — Einfache CSV-Parsing-Logik ohne komplexe Escaping-Regeln.
2. Embedded + Disk ist ein mächtiges Pattern — Ermöglicht Distribution eines fertigen Binaries mit konfigurierbaren Defaults.
3. GPT-5 `max_completion_tokens` — Spezielle Behandlung für einzelne Modellfamilien ist in der CSV einfach via boolean-Flag realisierbar.
4. Independence von sigoengine.MammothModels — REST-Server kann Modelle konfigurieren, ohne die CLI-Registry zu beeinflussen.

**Status:** ✅ Erfolgreich abgeschlossen

---

## Session 2026-04-20: Usage-Daten (Token-Tracking)

**Zielsetzung:**
sigoREST lieferte bisher keine Token-Verbrauchsdaten — Provider-Responses wurden geparst, aber `usage`-Felder verworfen. Ziel: (A) Usage im `/v1/chat/completions` Response (OpenAI-kompatibel), (B) kumulierte Statistiken via `/api/usage`.

**Was erreicht wurde:**

1. **`UsageData` Struct in `sigoengine/engine.go`**
   - Neuer Export: `UsageData{InputTokens, OutputTokens, TotalTokens}`
   - `CallAPI()` Signatur erweitert: `(string, error)` → `(string, *UsageData, error)`
   - Neue Hilfsfunktion `extractUsage()` — liest beide Provider-Formate:
     - Anthropic: `usage.input_tokens` / `usage.output_tokens`
     - OpenAI: `usage.prompt_tokens` / `usage.completion_tokens`
   - Bei Fehler oder fehlendem Usage-Block: `nil` zurückgegeben (kein Hard Fail)

2. **`ChatUsage` + erweiterter `ChatResponse` in `sigoREST/main.go`**
   - Neuer Struct `ChatUsage{PromptTokens, CompletionTokens, TotalTokens}` (OpenAI-Feldnamen)
   - `ChatResponse.Usage *ChatUsage` — `omitempty`, erscheint nur wenn Provider Daten liefert

3. **In-Memory Usage-Tracking im `Server`**
   - `ModelUsageStats{InputTokens, OutputTokens, TotalTokens, Requests int64}`
   - `Server.usage map[string]*ModelUsageStats` + `Server.usageMu sync.RWMutex`
   - Akkumulation nach jedem erfolgreichen API-Call — thread-safe

4. **Neuer Endpoint `GET /api/usage`**
   - Antwortet mit `by_model` (pro Modell) + `total` (Summe aller Modelle)
   - Nur RAM — kein Disk-Persist, Reset bei Serverstart

**Architektur-Entscheidungen:**

| Entscheidung | Begründung |
|--------------|------------|
| **`nil` bei fehlendem Usage** | Nicht alle Provider (Ollama) liefern Usage-Daten. `omitempty` vermeidet leeres `"usage": null` im Response. |
| **Getrennte `usageMu`** | Eigener Mutex statt `s.mu` — vermeidet Lock-Contention zwischen Modell-Registry und Usage-Updates. |
| **OpenAI-Feldnamen in `ChatUsage`** | `prompt_tokens`/`completion_tokens` statt `input_tokens`/`output_tokens` — OpenAI-Clients erwarten diese Namen. |
| **Kein Disk-Persist** | Einfachheit. Für persistente Statistiken wäre SQLite oder JSON-Append nötig — noch kein Bedarf. |

**Betroffene Call-Sites von `CallAPI`:**

| Datei | Änderung |
|-------|----------|
| `sigoengine/engine.go` (PingProvider-Probe) | `_, _, err :=` |
| `sigoREST/main.go` (Handler) | `text, u, e :=` → Usage akkumulieren |
| `cmd/sigoE/main.go` (CLI) | `text, _, e :=` (Usage ignoriert) |

**Testing & Verifikation:**

```bash
# Build
go build ./...

# Usage im Chat-Response
curl -s http://localhost:9080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"claude-h","messages":[{"role":"user","content":"Hallo"}]}' \
  | jq '.usage'
# → {"prompt_tokens": 42, "completion_tokens": 18, "total_tokens": 60}

# Kumulierte Statistiken
curl -s http://localhost:9080/api/usage | jq
```

**Erkenntnisse & Learnings:**

1. **3 Call-Sites bei `CallAPI`-Signaturänderung**: engine.go (Probe), sigoREST/main.go (Handler), cmd/sigoE/main.go (CLI). Bei zukünftigen Signaturänderungen alle drei prüfen.

2. **Provider-Format-Unterschied**: Anthropic nutzt `input_tokens`/`output_tokens`, OpenAI `prompt_tokens`/`completion_tokens`. `extractUsage()` normalisiert beide auf `UsageData`. Ollama liefert kein `usage`-Feld.

3. **`omitempty` für optionale Felder**: Wenn nicht alle Provider Usage liefern, ist `*ChatUsage` mit `omitempty` sauberer als leeres Struct — Clients sehen kein `"usage": null`.

**Status:** ✅ Erfolgreich abgeschlossen

---

## Session 2026-06-17: Multi-Channel Support abschließen

**Zielsetzung:**
Offene Punkte aus der Multi-Channel-Implementierung schließen: konfigurierbares Datenverzeichnis, Health-Monitor-Intervall, Kanal-spezifisches Memory/System-Prompt, Usage-Tracking pro Kanal, Provider-Mismatch-Schutz, persistierende Auth-Fehler-Deaktivierung und vollständige API-Doku.

**Was erreicht wurde:**

1. **Server-Konfiguration**
   - Neue Flags `-data-dir` (default `/var/sigoREST`) und `-channel-health-interval` (default `30s`)
   - Globales Memory und System-Prompt werden aus `-data-dir` geladen/gespeichert
   - `channels.json`, Kanal-Sessions und Kanal-Memory liegen ebenfalls unter `-data-dir`

2. **Kanal-spezifisches Memory und System-Prompt**
   - `PUT/GET /api/channels/:provider/:name/memory`
   - `PUT/GET /api/channels/:provider/:name/system-prompt`
   - In `handleChatCompletions` injiziert: Reihenfolge Global-Memory → Kanal-Memory → Global-System-Prompt → Kanal-System-Prompt → Request-System-Prompt

3. **Usage-Tracking pro Kanal**
   - Zusätzliche Map `usageByChannel` mit Key `model-id#provider-channel`
   - `/api/usage` liefert `by_model`, `by_channel` und `total`
   - `by_model` bleibt rückwärtskompatibel

4. **Kanal-Auflösung robuster gemacht**
   - `ChannelManager.Resolve` lehnt FullNames ab, deren Provider nicht zum Modell passt
   - Verhindert z.B. `moonshot-0` bei einem Mammoth-Modell

5. **Auth-Fehler führt zu persistierter Deaktivierung**
   - Im Chat-Handler und im Health-Monitor wird bei `AUTH_FAILED` `registry.SetActive(..., false)` aufgerufen
   - `channels.json` wird sofort aktualisiert
   - `ProbeProvider` meldet Auth-Fehler jetzt als `auth_failed` statt `available`

6. **Dokumentation**
   - `/api/help` um `/api/version`, `/api/channels/*`, `channel` Request-Parameter und Multi-Channel-Features erweitert
   - `docs/systemd-install.md` an `-data-dir` und `-channel-health-interval` angepasst

7. **Tests**
   - `sigoREST/main_test.go` mit Handler-Tests für Channels, Memory, System-Prompt, Version, Usage
   - `sigoengine/channel_health_test.go` mit Tests für Auto-Aktivierung und Auth-Deaktivierung

**Architektur-Entscheidungen:**

| Entscheidung | Begründung |
|--------------|------------|
| **Ein `-data-dir` für alles** | Zentrale Pfad-Quelle für State, Memory, Prompts, Sessions. Einfacher Backup und systemd-Betrieb. |
| **Request > Kanal > Global** für System-Prompts | Klare Hierarchie: spezifischste Vorgabe gewinnt. |
| **`usageByChannel` zusätzlich zu `usage`** | Rückwärtskompatibilität für `/api/usage` erhalten, Kanal-Details zusätzlich sichtbar. |
| **Registry erhält Registry-Pointer in `checkChannel`** | Seiteneffekt „Deaktivieren“ wird explizit und testbar, keine versteckte globale Variable. |
| **Nur pro Kanal, nicht pro Session Memory** | Entscheidung des Users: Kanal-Memory reicht, pro Session wäre übermäßig granular. |

**Code-Änderungen:**

| Datei | Änderung |
|-------|----------|
| `sigoREST/main.go` | Flags, Datenverzeichnis, Kanal-System-Prompt, Usage pro Kanal, Auth-Deaktivierung, `/api/help` |
| `sigoengine/channel_manager.go` | Provider-Mismatch-Schutz in `Resolve` |
| `sigoengine/channel_health.go` | Statische Endpoint-Fallbacks, Auth-Deaktivierung via `SetActive` |
| `sigoengine/engine.go` | `ProbeProvider` meldet `auth_failed` und `empty_probe_response` |
| `sigoengine/env.go` | **NEU** — `LoadEnvFile` / `GetEnvWithFile` für optionale `./env` Datei |
| `sigoREST/main_test.go` | **NEU** — Handler-Tests für Channels/Memory/System-Prompt/Usage |
| `sigoengine/channel_health_test.go` | **NEU** — Tests für Auto-Aktivierung und Auth-Deaktivierung |
| `docs/systemd-install.md` | `-data-dir`, `-channel-health-interval`, Memory-Pfad aktualisiert |

**Testing & Verifikation:**

```bash
# Build und Tests

go build ./...
go test ./sigoengine/ ./sigoREST/ -v

# Live am systemd-Daemon

curl -s http://127.0.0.1:9080/api/version
curl -s http://127.0.0.1:9080/api/channels
curl -s http://127.0.0.1:9080/api/channels/mammouth/0
curl -s -X POST http://127.0.0.1:9080/api/channels/mammouth/0/enable
curl -s http://127.0.0.1:9080/api/usage

# Chat mit und ohne Kanal
curl -s http://127.0.0.1:9080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"cl46-s","channel":"mammouth-0","messages":[{"role":"user","content":"Sag Hallo"}]}'

curl -s http://127.0.0.1:9080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"cl46-s","messages":[{"role":"user","content":"Sag Hallo"}]}'
```

**Erkenntnisse & Learnings:**

1. **Health-Probe mit `max_tokens:1` liefert leere Antworten.** Einige Modelle antworten mit `content:null`, was unser Code als `CLIENT_ERROR` klassifizierte. `ProbeProvider` behandelt diesen Fall jetzt als erreichbar.

2. **Auth-Fehler wurde von `ProbeProvider` verschluckt.** Weil Auth als `available` galt, deaktivierte der Health-Monitor Kanäle mit ungültigem Key nie. Trennung in `auth_failed` macht den Fehler sichtbar und handhabbar.

3. **Unit-Tests für HTTP-Handler beschleunigen Iteration.** `httptest` erlaubt schnelle Channel-Endpoint-Tests ohne laufenden Server.

4. **Persistierter Kanal-Status kann überraschen.** Wenn Kanäle einmal aktiviert wurden, bleiben sie über `channels.json` aktiv. Das ist gewollt, aber bei Tests leicht übersehen.

**Status:** ✅ Erfolgreich abgeschlossen und gepusht


**Kontext:** Der DNS-Race-Fix (Commit `8d22b4a` `fix: Provider-Fetch Retry
gegen Boot-DNS-Race` + systemd `Wants/After=network-online.target`) lag bisher
nur als Code vor. Die Wirksamkeit beim echten Systemstart war unverifiziert.

**Verifikation:** Boot-Test erfolgreich absolviert. Server lädt beim Start
alle Provider (Mammoth/Moonshot/ZAI), **nicht** nur die 13er-ZAI-Fallback-Liste.

**Problem-Hintergrund (Asymmetrie):** Bei DNS-Ausfall liefern Mammoth und
Moonshot `nil, err` (0 Modelle), ZAI dagegen `zaiStaticModels, nil` (13
statische Modelle). Beim Boot-DNS-Race erschienen daher nur ~13 Modelle.
Schutz: systemd `network-online.target` (nicht `network.target`!) **plus**
`FetchWithRetry` (4 Versuche, 2s/4s/8s Backoff).

**Erkenntnisse & Learnings:**

1. **Code ≠ verifiziert**: Race-Conditions beim Boot lassen sich nur im echten
   Systemstart prüfen, nicht im Build/Unit-Test. Jetzt in der Praxis grün.

2. **Diagnose-Heuristik**: Tauchen beim Boot nur ~13 Modelle auf ("no such
   host" im Log), ist DNS noch nicht oben → ab jetzt Regression, nicht
   erwartetes Verhalten.

3. **Begleit-Commit**: `docs/deepseek_beats_opus.txt` als Referenz zur
   Harness-These festgehalten (Gegenpol zur Scope-Entscheidung: sigoREST
   bleibt schlanker Proxy ohne Tool-Call-Repair).

**Status:** ✅ Erfolgreich abgeschlossen

---

## Session 2026-06-18: glm-500 — Registry-Gap nach Multi-Channel-Migration

**Zielsetzung:**
Sigils Übersetzer (Block Translator, s. [[project_sigil_block_translator]]) warf
beim Zugriff auf sigoREST `HTTP 500 Internal Server Error`. Konkret: Aufruf des
Modells `glm-4.5` schlug fehl. Ziel: Root cause finden, Fix implementieren,
Daemon neustarten, verifizieren.

**Diagnose-Verlauf:**

1. **Symptom eingegrenzt.** Server lief (PID 1737, systemd `sigorest`),
   `/api/health` OK, 89 Modelle. Also kein Server-Ausfall, sondern client- oder
   routing-seitig. Gerhard klärte: Fehler liegt **server-seitig** am
   Provider-Call, nicht am Sigil-Client.

2. **Syslog gelesen.** Server loggte wiederholt:
   `HTTP request failed endpoint=https://api.z.ai/api/paas/v4/chat/completions
   model=GLM-4.5 error=Post "...": context deadline exceeded`. Also: Server
   empfing Request, resolved zu Z.ai, aber Upstream-Call timeoutte → 500.

3. **Z.ai erreichbar.** Direkter `curl` auf den Z.ai-Endpoint: HTTP 401 in
   0,87 s (Auth nötig, aber Endpoint da), DNS + Ping (267 ms IPv6) ok. Damit
   kein DNS-/Netz-Ausfall — der echte Chat-Call scheitert.

4. **Reproduziert.** `curl` auf `localhost:9080/v1/chat/completions` mit
   `model: "glm-4.5"` (lowercase, wie `/v1/models` es liefert):
   `CONFIG_NOT_FOUND: Model not found in registry`, `config_error`, HTTP 500.
   Damit war der 500 reproduzierbar **ohne** Upstream-Beteiligung.

5. **Zwei-Map-Disconnect gefunden.** sigoREST hält **zwei getrennte**
   Modell-Maps vor:
   - `s.models` (Server, dynamisch via `loadModelsFromProviders()` von
     Mammoth/Moonshot/Z.ai) → speist `/v1/models`, liefert lowercase `glm-4.5`.
   - `modelsByID` (typisierte Registry in `models_registry.go`, 1× via
     `registryOnce` aus CSV → `CoreModels`) → `LoadConfigWithChannel` nutzt
     `GetModelByID`.
   `/v1/models` listet also glm, aber `LoadConfigWithChannel` findet es nicht.

6. **CSV verschwunden.** Bei der Multi-Channel-Migration (17. Jun) wurde
   `/usr/local/slib/sigoREST/models.csv` nach `olds/` verschoben
   (`channels.json` übernahm die Key-Verwaltung). Seitdem fällt die Registry auf
   `CoreModels` zurück — die keine glm-Einträge enthält → `GetModelByID` fail.

7. **Case-Mismatch als zweiter Teil.** Die alte CSV hatte `GLM-4.5` (groß),
   `/v1/models` liefert `glm-4.5` (klein). `GetModelByID` ist case-sensitiv.
   Selbst mit restoreter CSV hätte Sigils kleingeschriebener Modellname nicht
   gematcht. Zudem erwartet Z.ai lowercase — uppercase `cfg.Model="GLM-4.5"`
   war vermutlich Mit-Ursache des früheren `context deadline exceeded`.

**Architektur-Entscheidungen:**

| Entscheidung | Begründung |
|--------------|------------|
| **Channel-Only Fallback in `LoadConfigWithChannel`** | Bei Registry-Miss + vorhandenem Channel-Key Config aus Channel (APIKey) + Modellname (1:1-Passthrough) + Type `mammoth` bauen. Endpoint setzt main.go via `modelInfo.Endpoint`. Macht Server wirklich CSV-frei — wie CLAUDE.md verspricht. |
| **Modellname 1:1 durchreichen** | Casing/Form stammt vom dynamischen Fetch und passt zum Provider (Z.ai will lowercase). Kein Registry-Lookup, der case-sensitiv fehlschlagen kann. |
| **Type `"mammoth"` hartcodiert** | Entspricht bestehendem Verhalten (Original L49 setzte für alle Registry-Modelle `Type:"mammoth"`). Alle aktuellen Provider (mammouth/moonshot/zai) proxen OpenAI-Style (Bearer), siehe `CallAPI`. |
| **Nicht gewählt: CSV restore + case-insensitiv** | Schneller Fix, aber CSV bliebe Wartungslast und widerspräche der Multi-Channel-Migration. |
| **Nicht gewählt: Registry aus dynamischem Fetch füllen** | Sauberer, aber größere Änderung (neue Export-Funktion, Thread-Safety, Casing-Konsistenz). Für später offen. |

**Code-Änderungen:**

| Datei | Änderung |
|-------|----------|
| `sigoengine/loadconfig_channel.go` | Fallback-Zweig in `LoadConfigWithChannel`: wenn `GetModelByID` miss + `ch != nil && ch.APIKey != ""` → `ProviderConfig{Endpoint:"", Model:model, APIKey:ch.APIKey, Type:"mammoth"}`. Debug-Log `Registry-Miss, nutze Channel-Only Config`. Sonst unverändert (bestehende Registry-Modelle wie claude/gpt via CSV unberührt). |

**Testing & Verifikation:**

```bash
# Build + Vet
go vet ./sigoengine/     # clean
go build -o sigoREST/sigoREST ./sigoREST/   # BUILD OK

# Unit-Tests (Regression)
go test ./sigoengine/    # ok sigorest/sigoengine 0.381s

# Binary installieren + Daemon neustarten
cp sigoREST/sigoREST /usr/local/sbin/sigoREST
sudo systemctl restart sigorest     # neue PID 318828

# Live-Test: glm-4.5 (lowercase, wie /v1/models)
curl -s http://localhost:9080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"glm-4.5","messages":[{"role":"user","content":"sag nur OK"}],"timeout":60}'
# → {"id":"chatcmpl-...","model":"glm-4.5","choices":[{"message":{"content":"OK."}}],"usage":{"total_tokens":38}}
# real 0m1,222s  — kein 500 mehr, Z.ai antwortet korrekt.
```

**Bekannter Folgewert (nicht behoben):** Uppercase `GLM-4.5` liefert weiter
HTTP 400 `model_not_found`, weil der `s.models`-Lookup in `main.go` (L442)
case-sensitiv ist. Sigil zieht die Modellliste frisch aus `/v1/models`
(lowercase), nach einem Modell-Refresh in den Übersetzer-Einstellungen also
kein Problem. Optional nachträglich: `s.models`-Lookup case-insensitiv machen.

**Erkenntnisse & Learnings:**

1. **CLAUDE.md-Versprechen vs. Code-Realität.** "Server nutzt
   [CSV/Registry] nicht" (CLAUDE.md L150) stimmte seit der Multi-Channel-
   Migration nicht mehr — `LoadConfigWithChannel` fragt sehr wohl die
   typisierte Registry. Doku hinkte Code hinterher. Fix erfüllt das Versprechen
   erst wirklich.

2. **Zwei Maps, eine Wahrheit.** `s.models` (dynamisch, für `/v1/models`) und
   `modelsByID` (statisch, für Config) können divergieren. Ein Modell, das
   gelistet aber nicht konfigurierbar ist, ist ein **Rezept für 500er**.
   Künftige Modell-Quellen müssen beide Maps speisen — oder Config darf nicht
   von der statischen Map abhängen (dieser Fix geht den zweiten Weg).

3. **Casing ist Vertrag.** Provider-Modell-IDs sind lowercase (`glm-4.5`).
   Modellnamen dürfen nicht beim Durchreichen "aufgehübscht" werden. Case-
   sensitive Lookups an Provider-Grenzen sind Fail-Fallen.

4. **Diagnose-Pfad zählt.** Syslog → Upstream erreichbar? → reproduzierbarer
   Minimal-Call → Code-Path zurückverfolgt. Fünf Schritte, kein Raten.

**Status:** ✅ Erfolgreich abgeschlossen (Build grün, Tests grün, Live-Call
`glm-4.5` → `OK.` in 1,2 s). Commit ausstehend.

---

## Session 2026-06-18 (Fortsetzung): Case-insensitiver Modell-Lookup

**Zielsetzung:**
Der vorherige Fix (Channel-Only Fallback) hatte den `config_error`-500 behoben,
aber der `s.models`-Lookup in `main.go` blieb case-sensitiv. Uppercase
`GLM-4.5` lieferte weiter HTTP 400 `model_not_found`, weil der Map-Key-Lookup
streng auf lowercase `glm-4.5` matcht. Ziel: Lookup case-insensitiv machen,
damit beliebige Casing aus Sigil/CLI nicht mehr scheitert.

**Was erreicht wurde:**

1. **Neue Helper `lookupModel` in `sigoREST/main.go`**
   - Sucht case-insensitiv nach ID (Map-Key) **und** Shortcode
   - Liefert `ModelInfo` + kanonische ID zurück (Aufrufer hält `s.mu` RLock)
   - Zwei Phasen: erst ID-Match über alle Keys, dann Shortcode-Match

2. **Beide Lookup-Sites umgestellt**
   - `handleChatCompletions`: ersetzt direkten `s.models[modelID]`-Zugriff +
     manuelle Shortcode-Schleife durch `lookupModel`
   - `providerForModel`: nutzt ebenfalls `lookupModel`

3. **Heuristic-Fallback case-insensitiv**
   - `providerForModel`-Fallback (`kimi`→moonshot, `glm`→zai) auf
     `strings.ToLower(modelID)` umgestellt — war vorher `Contains(modelID,"GLM")`
     (case-sensitiv, hätte `glm` verfehlt)

**Architektur-Entscheidungen:**

| Entscheidung | Begründung |
|--------------|------------|
| **Helper statt Inline-Normalisierung** | Eine Stelle für Lookup-Logik, beide Sites profitieren. Vermeidet driften zwischen Chat-Handler und `providerForModel`. |
| **Kanonische ID zurückgeben** | Downstream-Code (Circuit-Breaker-Key, Logging, API-Request) bekommt konsistente Form, unabhängig vom Input-Casing. |
| **Aufrufer hält Lock, nicht Helper** | `handleChatCompletions` hält RLock ohnehin über mehrere Reads (memory, systemPrompt). Helper ohne eigene Lock-Logik = kein Deadlock-Risiko, klare Kontrakt. |
| **Shortcode case-insensitiv mit** | Shortcodes wie `glm45` könnten ebenfalls variiert werden; gleiche Robustheit. |

**Code-Änderungen:**

| Datei | Änderung |
|-------|----------|
| `sigoREST/main.go` | `lookupModel` Helper (case-insensitiv ID+Shortcode), `providerForModel` + `handleChatCompletions` darauf umgestellt, Heuristic-Fallback `strings.ToLower`. +30/-17. |

**Testing & Verifikation:**

```bash
go vet ./sigoREST/       # clean
go build ./sigoREST/     # BUILD OK
go test ./sigoengine/    # ok (cached)

# Binary tauschen (Daemon läuft → unlink + cp)
rm -f /usr/local/sbin/sigoREST
cp sigoREST/sigoREST /usr/local/sbin/sigoREST
sudo systemctl restart sigorest     # PID 385201

# Drei Casing-Varianten, alle → "OK"
curl ... -d '{"model":"glm-4.5",...}'   # lowercase → OK
curl ... -d '{"model":"GLM-4.5",...}'   # UPPERCASE → OK (vorher 400)
curl ... -d '{"model":"glm45",...}'     # shortcode → OK
```

**Erkenntnisse & Learnings:**

1. **Lookup-Grenzen sind Vertrag-Grenzen.** Jede Map, die Eingaben von außen
   (Client, CLI) entgegennimmt, sollte case-insensitiv matchen — Provider-
   Modell-IDs sind lowercase, Clients senden aber oft anders. Case-sensitive
   Lookups an Systemgrenzen sind Fail-Fallen (gleiche Lektion wie im
   vorherigen Fix, diesmal an der `s.models`-Schicht).

2. **`cp` über laufendes Binary scheitert (ETXTBSY).** Daemon hält Inode offen.
   Workaround: `rm -f` (unlink) + `cp` — der laufende Prozess behält seinen
   Inode, der neue Start holt sich die frische Datei. Sauberer als Service
   stoppen vor dem Kopieren.

3. **Zwei Fixes, eine Wurzel.** Beide 2026-06-18er Fixes drehen sich um
   Casing/Quelle-Mismatch an Provider-Grenzen: Config-Lookup (Registry vs.
   Channel) und Modell-Lookup (Map-Key vs. Input). Symptom war derselbe 500er,
   Ursache lag in zwei verschiedenen Schichten.

**Status:** ✅ Erfolgreich abgeschlossen und gepusht (Commit `7704998`).
