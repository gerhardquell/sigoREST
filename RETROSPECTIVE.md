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
