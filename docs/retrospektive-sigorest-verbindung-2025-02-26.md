# Retrospektive: sigoREST Verbindungsabbrüche & HTTP 502

**Datum:** 2025-02-26
**Autor:** Claude + Gerhard Quell
**Thema:** Analyse der HTTP 502 Fehler und Circuit Breaker Probleme

---

## Problemstellung

Bei der Entwicklung der Parallel Mind Demo mit echten KI-Aufrufen traten wiederholt Verbindungsabbrüche auf:

### Fehlermuster

```
ERR: sigo HTTP 502: {"error":{"message":"CIRCUIT_OPEN: Circuit breaker open",
    "type":"api_error","code":"api_error"}}

ERR: sigo HTTP 502: {"error":{"message":"API_FAILED: HTTP 400",
    "type":"api_error","code":"api_error"}}
```

### Betroffene Modelle

| Modell | Status | Fehlertyp |
|--------|--------|-----------|
| claude-h | ❌ | CIRCUIT_OPEN |
| gpt41 | ❌ | API_FAILED |
| deepseek-v3 | ❌ | CIRCUIT_OPEN |
| gemini-p | ✅ | Funktioniert |
| kimi | ✅ | Funktioniert |
| grok3 | ❌ | API_FAILED |

---

## Wurzelursachen-Analyse

### 1. Circuit Breaker Pattern

**Was ist ein Circuit Breaker?**
- Schutzmechanismus bei verteilten Systemen
- Öffnet sich nach zu vielen aufeinanderfolgenden Fehlern
- Verhindert Kaskadenfehler
- Schließt sich nach "Cooldown-Periode" (typisch: 30-60s)

**Warum ist er bei uns offen?**
```
Zu viele Fehler in kurzer Zeit:
- Rate Limiting bei Providern (Anthropic, OpenAI, etc.)
- Timeouts bei langsamen Antworten
- Authentifizierungsfehler
- Temporäre API-Ausfälle
```

### 2. HTTP 502 Bad Gateway

**Bedeutung:**
- sigoREST als Gateway konnte keine Verbindung zum Upstream (KI-Provider) herstellen
- Oder: Upstream hat mit Fehler geantwortet

**Mögliche Ursachen:**
1. **Rate Limiting** bei KI-Providern
2. **Authentifizierungsprobleme** (API Keys ungültig/abgelaufen)
3. **Timeout** - Antwort dauerte zu lange
4. **Temporäre Ausfälle** bei Providern
5. **Queue-Überlastung** bei sigoREST

---

## Erforderliche Anpassungen in sigoREST

### 1. Circuit Breaker Konfiguration

**Aktuelles Verhalten (vermutet):**
- Schnelles Öffnen nach wenigen Fehlern
- Lange Cooldown-Phase
- Keine Unterscheidung zwischen Fehlertypen

**Empfohlene Änderungen:**

```go
// Vorschlag: Feingranularere Circuit Breaker Konfiguration
type CircuitBreakerConfig struct {
    // Anzahl Fehler vor Öffnen
    FailureThreshold int           // z.B. 5 statt 3

    // Zeitfenster für Fehlerzählung
    FailureWindow    time.Duration // z.B. 60s

    // Cooldown vor Wiederversuch
    CooldownDuration time.Duration // z.B. 10s statt 60s

    // Halb-offen: Wie viele Testanfragen?
    HalfOpenMaxCalls int           // z.B. 3
}
```

**Wichtig:** Unterschiedliche Behandlung von:
- **Retryable Errors** (Timeout, 429 Rate Limit) → Circuit Breaker
- **Permanent Errors** (401 Unauthorized, 400 Bad Request) → Kein Circuit Breaker

### 2. Retry-Mechanismus mit Exponential Backoff

**Aktuell fehlend oder unzureichend:**

```go
// Vorschlag: Intelligentes Retry
func callWithRetry(provider Provider, request Request) (*Response, error) {
    maxRetries := 3
    backoff := time.Second

    for i := 0; i < maxRetries; i++ {
        resp, err := provider.Call(request)

        // Bei Rate Limiting (429) warte länger
        if isRateLimit(err) {
            time.Sleep(backoff * time.Duration(i+1))
            continue
        }

        // Bei Timeout retry
        if isTimeout(err) {
            time.Sleep(backoff)
            continue
        }

        return resp, err
    }

    return nil, fmt.Errorf("max retries exceeded")
}
```

### 3. Health Check Endpoint

**Fehlt aktuell:**

```go
// GET /health oder /ready
func healthCheck() HealthStatus {
    return HealthStatus{
        Status: "healthy",
        Providers: map[string]ProviderStatus{
            "claude-h": checkProvider("claude-h"),
            "gemini-p": checkProvider("gemini-p"),
            // ...
        },
    }
}
```

**Nutzen für GoLisp:**
- Vorab prüfen welche Modelle verfügbar
- Bessere Fehlermeldungen statt generischem 502

### 4. Queue-Management & Load Balancing

**Problem:** Parallele Anfragen überlasten Provider

**Lösungsansätze:**

```go
// 1. Per-Provider Rate Limiting
type ProviderQueue struct {
    Name          string
    MaxConcurrent int           // z.B. 3 parallel für Claude
    RequestQueue  chan Request  // Gepufferte Queue
    RateLimit     rate.Limiter  // Token Bucket
}

// 2. Globale Fairness-Queue
// - Anfragen werden nicht sofort abgelehnt
// - Sondern in Queue mit Timeout geparkt
```

### 5. Bessere Fehlermeldungen

**Aktuell:** Generisches "API_FAILED"

**Besser:**

```json
{
  "error": {
    "type": "rate_limit",
    "message": "Rate limit exceeded for provider 'anthropic'",
    "provider": "claude-h",
    "retry_after": 30,
    "suggestion": "Try model 'gemini-p' instead"
  }
}
```

### 6. Fallback-Mechanismus

**Neue Funktion:** Automatischer Modell-Wechsel

```go
// Config: Fallback-Kette
type FallbackChain struct {
    Primary   string   // "claude-h"
    Fallbacks []string // ["gemini-p", "kimi", "mistral-l3"]
}

// Bei Fehler automatisch nächstes Modell versuchen
func callWithFallback(chain FallbackChain, prompt string) (*Response, error)
```

---

## Empfohlene Architektur-Änderungen

### Request Flow (aktuell vs. verbessert)

```
AKTUELL:
GoLisp -> sigoREST -> [Circuit Breaker] -> Provider
                ↓
         Bei Fehler: 502

VERBESSERT:
GoLisp -> sigoREST -> [Queue] -> [Rate Limiter] -> [Retry] -> Provider
                ↓
         [Health Check] für Modell-Auswahl
         [Fallback Chain] bei Fehler
```

### Neue Endpoints für GoLisp

```
GET  /v1/health              # Gesamt-Status
GET  /v1/providers          # Liste mit Verfügbarkeit
GET  /v1/providers/{id}/health  # Einzel-Status

POST /v1/chat/completions   # Mit Retry-Config-Header
  X-Sigo-Retry-Count: 3
  X-Sigo-Fallback-Models: gemini-p,kimi
```

---

## Workarounds für GoLisp (bis sigoREST verbessert)

### 1. Modell-Validierung vor parfunc

```lisp
;; Vor parfunc prüfen welche Modelle funktionieren
(defun check-model (model)
  (catch
    (begin (sigo "test" model) t)
    (lambda (e) nil)))

;; Nur funktionierende Modelle verwenden
(define available-models
  (filter check-model '("gemini-p" "kimi" "mistral-l3")))
```

### 2. Sequentielle Ausführung mit Delay

```lisp
;; Wenn parfunc zu viele parallele Anfragen erzeugt:
(define result1 (sigo prompt "gemini-p"))
(sleep 1000)  ; 1 Sekunde Pause
(define result2 (sigo prompt "kimi"))
```

### 3. Error Handling

```lisp
(defun safe-sigo (prompt model)
  (catch
    (sigo prompt model)
    (lambda (e)
      (string-append "[Fehler: " model " nicht verfügbar]"))))
```

---

## Monitoring-Vorschläge

### Was sigoREST loggen sollte:

```
[2025-02-26T18:30:00Z] INFO  Request: model=claude-h duration=2.3s status=success
[2025-02-26T18:30:05Z] WARN  Circuit Breaker opened: model=claude-h
[2025-02-26T18:30:05Z] ERROR Provider error: model=gpt41 error="rate limit"
[2025-02-26T18:30:10Z] INFO  Circuit Breaker closed: model=claude-h
```

### Metrics für Dashboard:

- Request Rate per Modell
- Error Rate (4xx, 5xx, Timeout)
- Circuit Breaker State Changes
- Durchschnittliche Latenz
- Queue Depth

---

## Zusammenfassung

### Kurzfristig (Workarounds):
- ✅ Verwendung funktionierender Modelle (gemini-p, kimi)
- ✅ Error Handling in GoLisp
- ✅ Sequentielle Ausführung bei Bedarf

### Mittelfristig (sigoREST Änderungen):
- 🔧 Granularere Circuit Breaker Config
- 🔧 Retry-Mechanismus mit Exponential Backoff
- 🔧 Health Check Endpoints
- 🔧 Bessere Fehlermeldungen

### Langfristig (Architektur):
- 🔨 Queue-Management pro Provider
- 🔨 Automatische Fallback-Ketten
- 🔨 Load Balancing über mehrere Provider-Keys

---

## Fazit

Die Verbindungsabbrüche sind ein typisches Problem bei der Integration von KI-APIs. sigoREST benötigt robusteres Error Handling, intelligenteres Retry-Management und bessere Observability. Bis diese Implementiert sind, erfordert GoLisp defensive Programmierung mit Fallbacks und Error Handling.

**Priorität:** Hoch - Ohne zuverlässige KI-Anbindung ist GoLisp's USP gefährdet.
