# sigoengine

Shared Engine Package für sigoREST und sigoE CLI.
Thread-safe für parallele REST-Server-Nutzung.

## Architektur

```
sigoengine/
├── engine.go              # Core: API-Call, Session, CircuitBreaker, Vision
├── models.go              # Model-Struct + CoreModels (CLI-Fallback)
├── models_registry.go     # Registry-Logik (Lookup, Shortcode)
├── provider_fetchers.go   # Provider-Fetcher (Mammouth, Moonshot, ZAI, Ollama)
├── finish_reason_test.go  # Tests
└── usage_test.go          # Tests
```

## Kern-Typen

### Message

```go
type Message struct {
    Role    string          `json:"role"`
    Content json.RawMessage `json:"content"`
}
```

`Content` ist `json.RawMessage` — unterstützt String und Vision-Array-Format:

- String: `"Hallo Welt"`
- Vision-Array: `[{"type":"text","text":"..."},{"type":"image_url","image_url":{...}}]`

### Session

Gesprächsverlauf mit max. 20 Nachrichten (älteste werden verworfen).
Gespeichert als `.sessions/<model>-<sessionID>.json`.

```go
session := LoadSession("mein-projekt", "kimi")
session.AddMessage("user", "Hallo!")
messages := session.BuildMessages("Wie geht es dir?")
session.Save("mein-projekt", "kimi")
```

## Wichtige Funktionen

### ExtractTextFromContent

Extrahiert Text aus `Content` (String oder Vision-Array):

```go
text := ExtractTextFromContent(message.Content)
```

- String-Content → direkter Text
- Vision-Array → nur `type:"text"`-Einträge, durch Leerzeichen getrennt
- Fallback → RawMessage als String

### AddMessage

Fügt Nachricht zur Session hinzu. Speichert nur Text in Session-Dateien
(keine Vision-Bilddaten):

```go
session.AddMessage("user", "Hallo!")
```

### BuildMessages

Erstellt Nachricht-Array für API-Call. Liest History und fügt neuen Prompt an:

```go
messages := session.BuildMessages("Neue Frage")
```

Rückgabe-Typ: `[]map[string]interface{}` — unterstützt String und Vision-Array-Content.

## Fehlercodes

| Code | Bedeutung |
|------|-----------|
| `CONFIG_NOT_FOUND` | Konfigurationsdatei nicht gefunden |
| `API_KEY_MISSING` | API-Key fehlt |
| `API_FAILED` | API-Aufruf fehlgeschlagen |
| `INVALID_INPUT` | Ungültige Eingabe |
| `SESSION_ERROR` | Session-Fehler |
| `CIRCUIT_OPEN` | Circuit-Breaker offen |
| `UNEXPECTED_FORMAT` | Unerwartetes Antwortformat |
| `RATE_LIMIT` | Rate-Limit erreicht |
| `AUTH_FAILED` | Authentifizierung fehlgeschlagen |
| `TIMEOUT` | Zeitüberschreitung |
| `SERVER_ERROR` | Server-Fehler (5xx) |
| `CLIENT_ERROR` | Client-Fehler (4xx) |

## Abhängigkeiten

- Go Standard Library (keine externen Packages)
- Thread-safe via `sync.Mutex` für parallele REST-Nutzung
