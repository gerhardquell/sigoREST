# sigoREST — Retrospektiven

Dieses Dokument enthält detaillierte Historie vergangener Entwicklungssessions.

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
