# sigoREST — Retrospektiven

Dieses Dokument enthält detaillierte Historie vergangener Entwicklungssessions.

---

## Session 2026-07-11: Doku-Sync — CN/EN-READMEs + chinese/ auf Multi-Channel-Master-Stand

**Zielsetzung:**
`README_CN.md` und `README_EN.md` hingen eine ganze Generation hinter der deutschen Master-`README.md` nach (pre-Multi-Channel-Ära). Sie fehlten sämtliche Channel-, Failover-, data-dir- und Vision-Funktionalität sowie die neuen Endpoints und Client-Libraries. Ziel war ein treuer 1:1-Spiegel des deutschen Masters in Chinesisch und Englisch plus Aktualisierung des `chinese/`-Subdirs.

**Was erreicht wurde:**

### 1. CN/EN komplett neu als treuer Spiegel

Beide READMEs wurden von Grund auf neu übersetzt (CN 618 Zeilen, EN 616 Zeilen) und enthalten jetzt 1:1 den Master-Stand:

- Architektur-Block mit allen 6 neuen `sigoengine`-Files (`channel.go`, `channel_manager.go`, `channel_health.go`, `session_memory.go`, `env.go`, `version.go`)
- Server-Flags `-data-dir`, `-channel-health-interval`; CLI-Flags `-session-dir`, `-c`
- env-Datei (`./env`) + indizierte API-Keys (`_0`, `_1`, …) für zusätzliche Kanäle
- Datenverzeichnis-Layout unter `/var/sigoREST` mit `channels/`- und `sessions/`-Subdirs
- Multi-Channel-Support, Auto-Failover, Health-Monitor
- Alle `/api/channels/*`-Endpoints, `/api/version`, `/api/usage`, `/api/help`
- Vision-Support, 4 Client-Libraries (Python v2 / Go / JavaScript / Common-Lisp)
- systemd mit `network-online.target` + `EnvironmentFile` (DNS-Race-Fix)
- ~89 Modelle, Shortcodes `cl46-s`/`gpt4o`, Version 1.1

### 2. memory.json-Beispiel korrigiert

Der deutsche Master zeigte das memory.json-Beispiel gekürzt (`"…mit Gerhard."`), die echte eingebettete `sigoREST/memory.json` enthält aber `"…mit Gerhard, einem erfahrenen Software-Entwickler."`. CN/EN zeigten bisher eine falsche englische Übersetzung. Korrektur: alle drei READMEs zeigen jetzt den realen deutschen Default — dokumentiert das tatsächliche Verhalten, keine erfundene Übersetzung.

### 3. chinese/ Subdir aktualisiert

| Datei | Änderung |
|-------|----------|
| `chinese/README.md` | Build-Pfad fix (`./sigoREST/sigoREST` statt `sigoREST`), Multi-Channel-Feature-Block |
| `chinese/KEYWORDS.md` | Keywords für Multi-Channel/Failover/Health-Monitor ergänzt |
| `chinese/PITCH.txt` | Pitch um Multi-Channel + Auto-Failover erweitert |

**Architektur-Entscheidungen:**

| Entscheidung | Begründung |
|--------------|------------|
| **Treuer Spiegel statt zielgruppen-angepasst** | Konsistenz mit Master, wartbar. Nur CN behält das China-Fokus-Intro (Zielgruppe), EN folgt dem Master ohne Intro. |
| **memory.json als realer deutscher Default** | Dokumentation soll echtes Verhalten zeigen, keine erfundene Lokalisierung. |
| **Subagent-Driven Development** | Zwei unabhängige README-Übersetzungen parallel via Fresh-Subagent-pro-Datei, Controller macht Spec-Review gegen Master. Effizient, kein Context-Pollution. |
| **TODO-Archive local-only** | WIP-`.gitignore` ignoriert `TODO-*.md` bewusst → done-Archive bleiben lokale Scratch-Dateien, nicht im Repo. Bestehende tracked done-Files bleiben unangetastet. |

**Testing & Verifikation:**

```bash
# Build unverändert (nur Doku)
go build ./...   # BUILD OK

# Spec-Review gegen Master: Struktur-Marker geprüft
grep -c 'channel.go|channel_manager.go|...' README_CN.md   # 6 ✓
grep -c 'network-online.target' README_EN.md                # 3 ✓
grep -c 'cl46-s' README_CN.md README_EN.md                  # 6/6 ✓

# GitHub-Render-Check (Playwright)
# README.md    → memory.json-Fix live ✓
# README_CN.md → 多渠道支持-Section rendert ✓
# README_EN.md → Multi-Channel Support, kein About-Intro ✓
```

**Code-Änderungen (Zusammenfassung):**

- `README.md`: memory.json-Beispiel auf volle Version korrigiert
- `README_CN.md`: komplette Neuübersetzung (338 → 618 Zeilen)
- `README_EN.md`: komplette Neuübersetzung (338 → 616 Zeilen)
- `chinese/README.md`: Build-Pfad + Multi-Channel-Feature
- `chinese/KEYWORDS.md`: Kanal/Failover/Health-Keywords
- `chinese/PITCH.txt`: Pitch erweitert

**Git:**

Commit `43d93a7` (Doku-Sync) + `6579e50` (TODO-Archiv) direkt auf `main` gepusht. Archiv-File `TODO-20260711-docs-done.md` bleibt local (gitignored). GitHub-Render per Playwright verifiziert.

---

## Session 2026-07-11: Entfernen des Fake-Streamings + Echtes Provider-SSE für alle OpenAI-kompatiblen Clients

**Zielsetzung:**
Der Server und die Clients hatten zwar bereits SSE-Endpunkte, aber der Server sammelte die vollständige Antwort ein und splittete sie wortweise mit künstlichen 8-ms-Verzögerungen („Fake-Streaming“). Ziel war die Umstellung auf echtes, provider-durchgereichtes SSE-Streaming für alle OpenAI-kompatiblen Provider. Anthropic wurde bewusst ausgeschlossen (Kostengründe).

**Was erreicht wurde:**

### 1. Server-seitiges echtes Streaming

- `sigoengine/engine.go`:
  - Neuer gemeinsamer `defaultHTTPClient` (`http.Client{}`) für Connection Reuse.
  - `CallAPI` berücksichtigt bereits gesetzte Deadlines und wendet `timeoutSec` nur an, wenn der Kontext noch keine Deadline hat.
  - Neue `CallAPIStream()`-Funktion: setzt `stream=true`, `Accept: text/event-stream` und liefert `io.ReadCloser` mit dem Provider-Response-Body.

- `sigoREST/main.go`:
  - Entfernung der Fake-Streaming-Hilfsfunktionen (`writeSSEEvent`, `writeStreamingResponse`).
  - Neue `streamProviderResponse()` leitet den Provider-Stream 1:1 an den Client weiter, puffert Zeilen, extrahiert parallel den Text für Session/Memory und sendet abschließend `data: [DONE]`.
  - Handler wählt bei `stream=true` und OpenAI-kompatiblen Providern den `CallAPIStream`-Pfad; Anthropic und Fehlerfälle laufen weiterhin über `CallAPI` mit Retry.

### 2. Clients auf echtes SSE umgestellt

| Client | Änderung |
|--------|----------|
| **Python** | Fake-Streaming-Fallback komplett entfernt; sync + async nutzen `httpx.stream()` / `aiter_lines()` gegen `text/event-stream`. |
| **Go** | Neue Typen `ChatCompletionChunk`, `ChatCompletionChunkChoice`, `ChatCompletionChunkDelta`; neue `ChatStream()`-Methode mit SSE-Zeilenparser; Leerzeilen werden korrekt als Event-Trenner behandelt. |
| **JavaScript** | Neue `chatStream()`-Methode als AsyncGenerator; liest `response.body.getReader()` und parst SSE-Zeilen. |
| **C++** | Neue Chunk-Modelle in `models.hpp`; `chatCompletionStream()` mit Callback und CURL-Write-Callback-Parsing. |
| **Common Lisp** | `chat-stream()` fordert jetzt explizit `Accept: text/event-stream` an. |

### 3. Weitere Verbesserungen

- `sigoengine/channel_health.go`: dynamische Provider-Typ-Erkennung für Health-Checks (`anthropic`, `ollama`, `mammoth`) statt hartkodiertem `"mammoth"`.
- C++ Client: `curl_easy_getinfo` vor `curl_easy_cleanup` ausgeführt (Use-after-free vermieden).

**Architektur-Entscheidungen:**

| Entscheidung | Begründung |
|--------------|------------|
| **Provider-Stream direkt durchreichen** | Keine künstlichen Verzögerungen mehr; echte First-Token-Latenz. |
| **Anthropic ausgeschlossen** | Anwender hat explizit festgelegt, dass Anthropic aufgrund der Kosten nicht für Streaming genutzt wird. |
| **Shared `http.Client`** | Verhindert Connection-Pool-Überlastung bei ~100 parallelen Verbindungen. |
| **Keine externen SSE-Bibliotheken** | Clients parsen SSE selbst, um Abhängigkeiten minimal zu halten. |

**Testing & Verifikation:**

```bash
# Server bauen & starten
go build -o sigoREST/sigoREST ./sigoREST/
./sigoREST/sigoREST -v debug

# curl
curl -s -N -X POST http://127.0.0.1:9080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Accept: text/event-stream" \
  -d '{"model":"cl5-s","messages":[{"role":"user","content":"zähle von 1 bis 3"}],"stream":true}'

# Clients (jeweils getestet)
# Python: SigoClient().chat.completions.create(stream=True)
# Go:     client.ChatStream(ctx, "cl5-s", "...")
# JS:     for await (const ch of client.chatStream("cl5-s", "..."))
# C++:    client.chatCompletionStream("cl5-s", msgs, callback)
```

Alle getesteten Clients lieferten für das Prompt "zähle von 1 bis 3" das erwartete Ergebnis `1, 2, 3`.

**Code-Änderungen (Zusammenfassung):**

- `sigoengine/engine.go`: Shared HTTP-Client, Deadline-Handling, `CallAPIStream()`
- `sigoengine/channel_health.go`: Dynamische Provider-Typ-Erkennung
- `sigoREST/main.go`: Echtes Provider-SSE-Streaming
- `clients/python/src/sigo_client/client.py`: Entfernung Fake-Streaming, echtes SSE
- `clients/go/client.go`: `ChatStream()` + Chunk-Typen + SSE-Parser
- `clients/javascript/client.js`: `chatStream()` AsyncGenerator
- `clients/cpp/core/include/sigorest/{client.hpp,models.hpp}`: Streaming-API + Chunk-Modelle
- `clients/cpp/core/src/client.cpp`: `chatCompletionStream()` Implementierung
- `clients/clisp-exp/sigoclient.lisp`: `Accept: text/event-stream` in `chat-stream`

**Git:**

Branch `feat/real-sse-streaming` erstellt, gepusht, PR #1 eröffnet, gemergt (Squash) und gelöscht. Server nach dem Merge auf `main` neu gebaut und gestartet.

---

## Session 2026-07-10: Modernisierung des Python-Clients + Echter SSE-Streaming-Support

**Zielsetzung:**
Der bestehende `clients/python/` Client (v1.0, basierend auf `requests` + Dataclasses) war funktional, aber veraltet. Ziel war ein kompletter Neubau als moderner, produktionsreifer Client mit:

- Vollständiger OpenAI-SDK-Kompatibilität (`chat.completions.create()`)
- Sync + Async Support (`httpx` + `AsyncClient`)
- Pydantic v2 für starke Typisierung und Validierung
- Echtes Server-Sent Events (SSE) Streaming
- Moderne Projektstruktur (`pyproject.toml`, `src/`-Layout)
- Vollständige Abwärtskompatibilität zum bestehenden sigoREST-Server

**Was erreicht wurde:**

### 1. Komplette Neuentwicklung des Python-Clients (`sigo-client` v2.0)

- **Neue Struktur**: `src/sigo_client/` mit `client.py`, `models.py`, `__init__.py`
- **Kern-Features**:
  - `SigoClient` und `AsyncSigoClient`
  - Vollständige OpenAI-kompatible Schnittstelle
  - Pydantic-Modelle (`ChatCompletion`, `ChatCompletionChunk`, `Model`, `HealthResponse`, `MemoryBlock`)
  - Robuste Fehlerbehandlung (`SigoError`, `SigoAPIError`, `SigoConnectionError`, `SigoTimeoutError`)
  - Context-Manager Unterstützung
- **Modernes Packaging**: `pyproject.toml` mit `hatchling`, `ruff`, `pytest`, `dev` + `test` Extras

### 2. Echter SSE-Streaming-Support (Server + Client)

**Server-seitig (`sigoREST/main.go`)**:
- Erweiterung von `ChatRequest` um `Stream bool`
- Neue Hilfsfunktionen `writeSSEEvent()` und `writeStreamingResponse()`
- Korrekte OpenAI-kompatible SSE-Formatierung (`event: message_start`, `message_delta`, `message_stop`, `usage`, `[DONE]`)
- Vollständige Abwärtskompatibilität: `stream=false` oder fehlender Parameter → unveränderte JSON-Antwort
- Verbessertes Chunk-Format mit `id`, `object`, `choices[].delta` und `finish_reason`

**Client-seitig**:
- Sync- und Async-Implementierung von `_create_stream()`
- Verwendung von `httpx.stream()` / `aiter_lines()` zum Parsen von `text/event-stream`
- Intelligenter Fallback auf simulierte Wort-für-Wort-Ausgabe bei Fehlern
- Aktualisierte Beispiele (`basic_chat.py`, `streaming_chat.py`, `async_chat.py`, `list_models.py`)

### 3. Dokumentation & Testing

- Umfassendes neues `clients/python/README.md`
- Mehrere getestete Beispiele mit realem Streaming
- Aktualisierte Hilfe-Texte im Server (`/api/help`)
- Diese Retrospektive

**Architektur-Entscheidungen:**

| Entscheidung | Begründung |
|--------------|------------|
| **Simuliertes Fallback** | Der Server unterstützt SSE nur bei `stream=true`. Fallback gewährleistet Robustheit. |
| **OpenAI-kompatibles Chunk-Format** | Ermöglicht zukünftige Nutzung mit dem offiziellen `openai` Python-Paket. |
| **Wort-für-Wort in SSE** | Bietet angenehmes „Typewriter“-Gefühl ohne zu viele Events. |
| **src/-Layout + pyproject.toml** | Folgt aktuellen Python-Best-Practices (PEP 621, editable installs). |

**Code-Änderungen (Zusammenfassung):**

**Server:**
- `sigoREST/main.go`: `ChatRequest.Stream`, SSE-Helper, `writeStreamingResponse()`, angepasster Handler, erweiterte Hilfe

**Client:**
- `clients/python/pyproject.toml` (neu)
- `clients/python/src/sigo_client/__init__.py`, `models.py`, `client.py` (komplett neu/überarbeitet)
- `clients/python/examples/*.py` (modernisiert + neues `streaming_chat.py`)

**Testing & Verifikation:**

```bash
# Server mit SSE bauen & starten
go build -o sigoREST/sigoREST ./sigoREST/
./sigoREST/sigoREST -q

# Client installieren
cd clients/python
pip install -e .

# Tests
python examples/list_models.py
python examples/basic_chat.py
python examples/streaming_chat.py        # echtes SSE
python examples/async_chat.py            # Async SSE
```

Alle Beispiele funktionieren. Streaming zeigt jetzt echte `event: message_delta` Zeilen.

**Erkenntnisse & Learnings:**

1. **Abwärtskompatibilität zuerst**: Durch das klare `if isStreaming` im Handler konnten wir Streaming hinzufügen, ohne bestehende Clients zu brechen.

2. **SSE ist trickreich**: Korrekte Header (`X-Accel-Buffering: no`), Flushing, Event-Format und `[DONE]` sind entscheidend. Das `event:` Feld ist optional, aber hilfreich.

3. **Python Streaming-Parsing**: `httpx.stream()` + `iter_lines()` / `aiter_lines()` ist sehr elegant. Der Fallback-Mechanismus hat sich als extrem nützlich erwiesen.

4. **Modernisierung lohnt sich**: Der Sprung von `requests` + Dataclasses zu `httpx` + Pydantic v2 + vollem OpenAI-Interface hat den Client von „brauchbar“ zu „State of the Art“ gemacht.

5. **Zusammenarbeit von Go und Python**: Die enge Abstimmung des Chunk-Formats zwischen Server und Client war der Schlüssel zum Erfolg.

**Nächste mögliche Schritte:**
- Offiziellen `openai` Python-Paket-Adapter (`SigoOpenAIClient`)
- Echte Unit-Tests mit `pytest` + `respx` (Mock SSE)
- Performance-Optimierungen bei sehr langen Streams
- Unterstützung für `tools` / Function Calling in den Streaming-Chunks

**Co-Autor**: Grok (xAI) — Session vom 10. Juli 2026.

---

*(Vorherige Retrospektiven siehe weiter unten im Dokument)*
