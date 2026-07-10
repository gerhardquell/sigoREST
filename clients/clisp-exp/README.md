# sigoclient v2 — Modernisierter Common Lisp Client für sigoREST

**Aktualisiert Juli 2026** — Jetzt mit Unterstützung aktueller Modelle und **echtem Streaming**.

## Installation

```bash
# Benötigte Quicklisp-Pakete (einmalig)
(ql:quickload '(:drakma :yason :babel :flexi-streams))

# Oder via apt (Debian/Ubuntu)
sudo apt-get install cl-drakma cl-yason cl-babel
```

## Schnellstart

```lisp
(load "/u/go-projekte/sigoREST/clients/clisp-exp/sigoclient.lisp")
(use-package :sigoclient)

;; Einfacher Chat (kompatibel zur alten API)
(chat "cl5-s" "Hallo Gerhard! Wie geht's?")

;; Mit Session (Kontext bleibt erhalten)
(chat "cl5-s" "Wie heiße ich?" :session-id "gerhard-test")

;; Streaming (echtes SSE)
(let ((stream (chat-stream "cl5-s" "Schreibe ein Haiku über Common Lisp.")))
  (loop for chunk = (funcall stream)
        while chunk
        when (stringp chunk)
          do (format t "~A" chunk)
        when (eq chunk :done)
          do (terpri) (return)))
```

## Neue Features (v2)

- **Aktuelle Modelle**: `cl5-s`, `cl48-o`, `kimi-k2.7`, `ollama-*` etc.
- **Echtes Streaming**: `chat-stream` gibt einen Iterator zurück, der SSE-Events parst
- Bessere Fehlerbehandlung und Logging
- Saubere Trennung zwischen synchronem `chat` und asynchronem Streaming
- Vollständig kompatibel mit dem neuen sigoREST-Server (inkl. Memory, Channels, Circuit-Breaker-Status)

## API

### Kernfunktionen

- `(ping)` → `T` / `NIL`
- `(health)` → ALIST mit Status und Modellanzahl
- `(list-models)` → Liste aller Modelle
- `(chat model message &key system-prompt session-id temperature max-tokens)` → String-Antwort
- `(chat-stream model message &key ...)` → Closure/Iterator für Streaming
- `(get-memory)` / `(set-memory content &optional cache)`

### Streaming-Beispiel (idiomatisch)

```lisp
(let ((stream (chat-stream "cl5-s" "Erkläre Quantencomputing in einem Haiku.")))
  (loop for token = (funcall stream)
        until (eq token :done)
        when (stringp token)
          do (format t "~A" token)))
```

## Hinweise

- Der Client ist weiterhin **experimentell**, aber deutlich robuster als v1.
- Streaming verwendet echte SSE-Parsing (keine reine Simulation mehr).
- Für Produktion empfehlen wir weiterhin den Python- oder Go-Client.
- `quicklisp` wird empfohlen für einfachere Abhängigkeitsverwaltung.

**Lisp forever!** 🧙‍♂️
```

**Siehe auch:** `RETROSPECTIVE.md` für detaillierte Entwicklungsgeschichte dieser Modernisierung.