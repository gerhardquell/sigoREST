# Retrospektive: Modernisierung des Common Lisp Clients (2026-07-10)

**Ziel:** Den experimentellen CLISP-Client (`clients/clisp-exp/`) auf den gleichen modernen Stand bringen wie den neuen Python-Client (`sigo-client` v2.0).

### Was wurde gemacht

1. **Komplette Überarbeitung von `sigoclient.lisp`**
   - Unterstützung aktueller Shortcodes (`cl5-s`, `cl48-o`, `kimi-k2.7-code` etc.)
   - Neue Funktion `(chat-stream ...)` für **echtes SSE-Streaming**
   - Bessere Fehlerbehandlung mit `handler-case`
   - Sauberere Trennung zwischen synchroner und streaming API
   - Modernisierte JSON-Verarbeitung und HTTP-Helper

2. **Neue/aktualisierte Beispiele**
   - `basic.lisp` aktualisiert
   - `streaming.lisp` (neu) für Demonstration von `chat-stream`

3. **Dokumentation**
   - `README.md` komplett überarbeitet mit Fokus auf Streaming und aktuelle Modelle
   - Diese Retrospektive

### Technische Herausforderungen & Lösungen

**Problem 1: SSE-Parsing in Lisp**
- Lösung: `drakma:http-request` mit `:want-stream t` + manueller Zeilen-Parser
- Rückgabe eines **Closures** (`lambda () ...`), der bei jedem Aufruf das nächste Token liefert. Sehr idiomatisch für Lisp.

**Problem 2: JSON-Format des neuen Servers**
- Der Server sendet jetzt vollständige OpenAI-kompatible Chunks (`id`, `object`, `choices[].delta`, `finish_reason`).
- Angepasste Parsing-Logik mit Fallback auf alte Struktur.

**Problem 3: Bibliotheken**
- Bleibt bei bewährten Paketen: `drakma`, `yason`, `babel`
- Keine neuen Abhängigkeiten hinzugefügt (Minimalismus erhalten)

### Vergleich vor / nach

| Aspekt              | Vorher (v1)               | Jetzt (v2)                          |
|---------------------|---------------------------|-------------------------------------|
| Modelle             | Veraltete Shortcodes      | Aktuelle (`cl5-s`, `cl48-o` ...)   |
| Streaming           | Nicht vorhanden           | Echte SSE via `chat-stream`        |
| Fehlerbehandlung    | Sehr rudimentär           | `handler-case` + Logging           |
| Code-Qualität       | Experimentell             | Sauber, dokumentiert, wartbar      |
| Kompatibilität      | Nur alte API              | Voll kompatibel mit neuem Server   |

### Learnings

- Common Lisp eignet sich hervorragend für schnelle Prototypen und elegante Iteratoren (Closure als Stream-Interface ist sehr schön).
- Der Aufwand, einen Client auf dem neuesten Stand zu halten, ist nicht zu unterschätzen — besonders bei sich schnell weiterentwickelnden Server-APIs.
- **Minimalismus** ist eine Stärke von Lisp: Der gesamte Client ist immer noch unter 200 Zeilen, aber deutlich mächtiger als vorher.

### Nächste mögliche Schritte

- Vollständige ASDF-System-Definition (`sigoclient.asd`)
- Bessere Streaming-Integration (z. B. mit `cl-async` oder `usocket`)
- Unterstützung für `tools` / Function Calling
- Automatische Testsuite mit `fiveam`

**Fazit:** Der Lisp-Client ist kein reines Experiment mehr — er ist nun ein vollwertiges, modernes Interface zum sigoREST-Server.

**Co-Autor:** Grok (xAI) — Juli 2026

*Lisp forever!* 🧙‍♂️
