# sigoclient - Experimenteller Common Lisp Client

⚠️ **EXPERIMENTELL** - Dieser Client ist minimalistisch und für Tests gedacht.

## Überblick

Ein einfacher Common Lisp Client für sigoREST. Benutzt:
- `drakma` für HTTP Requests
- `yason` für JSON Parsing
- `babel` für Zeichenkodierung

## Installation

```bash
# Benötigte Pakete installieren (Debian/Ubuntu)
sudo apt-get install cl-drakma cl-yason cl-babel
```

## Verwendung

```lisp
;; Lade den Client
(load "sigoclient.lisp")
(use-package :sigoclient)

;; Optional: URL anpassen
(setf *base-url* "http://127.0.0.1:9080")

;; Ping Test
(ping)  ; => T oder NIL

;; Health Check
(health)  ; => ((:STATUS . "ok") (:AVAILABLE--MODELS . 39) ...)

;; Einfacher Chat
(chat "kimi" "Hallo!")
; => "Hallo! Wie kann ich dir helfen?"

;; Mit System-Prompt
(chat "kimi" "Erkläre Go"
      :system-prompt "Du bist ein Go-Experte.")

;; Mit Session
(chat "kimi" "Mein Name ist Gerhard"
      :session-id "test-session")

;; Nächste Nachricht mit gleicher Session
(chat "kimi" "Wie heiße ich?"
      :session-id "test-session")
; => "Du hast gesagt, dein Name ist Gerhard."

;; Modelle auflisten
(list-models)  ; => Array von Modell-Informationen

;; Memory verwalten
(set-memory "Antworte immer auf Deutsch.")
(get-memory)  ; => ((:CONTENT . "Antworte...") (:CACHE . T))
```

## Beispiel ausführen

```bash
cd /u/go-projekte/sigoREST/clients/clisp-exp/examples
sbcl --load basic.lisp
```

## API

### Globale Variable

- `*base-url*` - Basis-URL des Servers (default: "http://127.0.0.1:9080")

### Funktionen

| Funktion | Parameter | Beschreibung |
|----------|-----------|--------------|
| `ping` | - | Prüft Server-Verfügbarkeit |
| `health` | - | Gibt Health-Status zurück |
| `list-models` | - | Listet alle Modelle auf |
| `chat` | model message &key system-prompt session-id temperature max-tokens | Sendet Chat-Anfrage |
| `get-memory` | - | Liest globalen Memory-Block |
| `set-memory` | content &optional cache | Setzt globalen Memory-Block |

### Chat Keywords

- `:system-prompt` - System-Kontext
- `:session-id` - Session für Konversationskontinuität
- `:temperature` - Temperatur (0.0-2.0)
- `:max-tokens` - Maximale Tokens

## Hinweise

⚠️ Dieser Client ist **experimentell**:
- Keine komplexe Fehlerbehandlung
- Keine Timeouts pro Request
- Einfache API ohne Strukturen
- Gut für Prototyping und Tests

Für Produktiv-Einsatz empfehlen wir den **Python**, **Go** oder **JavaScript** Client.

## Unterschied zum vollständigen Client

| Feature | exp. CLISP | Andere Clients |
|---------|-----------|----------------|
| Strukturen | ❌ Keine | ✅ Ja |
| Error Handling | Minimal | Vollständig |
| Timeout pro Request | ❌ Nein | ✅ Ja |
| Methoden-Chaining | ❌ Nein | ✅ Ja |
| Production-Ready | ⚠️ Experimentell | ✅ Ja |

---

**Lisp forever!** 🧙‍♂️
