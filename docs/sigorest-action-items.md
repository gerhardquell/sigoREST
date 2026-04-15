# sigoREST: Action Items für Verbindungsstabilität

**Priorität:** Hoch
**Erstellt:** 2025-02-26
**Referenz:** GoLisp Demo-Entwicklung - HTTP 502/Circuit Breaker Probleme

---

## 🎯 Ziel

Robuste KI-API-Integration mit graceful degradation, transparenten Fehlern und automatischer Wiederherstellung.

---

## 📋 Action Items

### 1. Circuit Breaker Konfiguration anpassen

**Status:** Offen
**Priorität:** Hoch
**Geschätzter Aufwand:** 2-4 Stunden

**Aktuelles Problem:**
- Circuit Breaker öffnet sich zu schnell (nach ~3 Fehlern)
- Lange Cooldown-Phase (vermutet: 60s+)
- Keine Unterscheidung zwischen Fehlertypen

**Erforderliche Änderung:**

```go
// internal/circuitbreaker/config.go
package circuitbreaker

type Config struct {
    // Anzahl Fehler vor Öffnen (erhöhen von 3 auf 5)
    FailureThreshold int `default:"5"`

    // Zeitfenster für Fehlerzählung
    FailureWindow time.Duration `default:"60s"`

    // Cooldown vor Wiederversuch (reduzieren auf 10s)
    CooldownDuration time.Duration `default:"10s"`

    // Testanfragen im halb-offenen Zustand
    HalfOpenMaxCalls int `default:"3"`
}

// Fehlertypen unterscheiden
func shouldCountForCircuitBreaker(err error) bool {
    // Nicht zählen: Client-Fehler (400, 401)
    if isClientError(err) {
        return false
    }

    // Zählen: Server-Fehler (500, 502, 503), Timeouts, Rate Limits (429)
    return isServerError(err) || isTimeout(err) || isRateLimit(err)
}
```

**Akzeptanzkriterien:**
- [ ] Circuit Breaker öffnet sich erst nach 5 Fehlern in 60s
- [ ] Cooldown-Phase auf 10s reduziert
- [ ] 401/400 Fehler lösen keinen Circuit Breaker aus
- [ ] Tests für alle Fehlerszenarien vorhanden

---

### 2. Retry-Mechanismus mit Exponential Backoff

**Status:** Offen
**Priorität:** Hoch
**Geschätzter Aufwand:** 4-6 Stunden

**Aktuelles Problem:**
- Kein automatisches Retry bei transienten Fehlern
- Sofortiger Failover auf Circuit Breaker

**Erforderliche Änderung:**

```go
// internal/client/retry.go
package client

import (
    "context"
    "time"
)

type RetryConfig struct {
    MaxRetries  int           `default:"3"`
    BaseDelay   time.Duration `default:"500ms"`
    MaxDelay    time.Duration `default:"5s"`
    Multiplier  float64       `default:"2.0"`
}

func CallWithRetry(ctx context.Context, call func() error, config RetryConfig) error {
    var lastErr error
    delay := config.BaseDelay

    for attempt := 0; attempt <= config.MaxRetries; attempt++ {
        err := call()
        if err == nil {
            return nil
        }

        lastErr = err

        // Bei permanenten Fehlern sofort abbrechen
        if !isRetryable(err) {
            return err
        }

        // Letzter Versuch failed
        if attempt == config.MaxRetries {
            break
        }

        // Exponential Backoff mit Jitter
        select {
        case <-ctx.Done():
            return ctx.Err()
        case <-time.After(delay):
            delay = time.Duration(float64(delay) * config.Multiplier)
            if delay > config.MaxDelay {
                delay = config.MaxDelay
            }
        }
    }

    return fmt.Errorf("max retries exceeded: %w", lastErr)
}

func isRetryable(err error) bool {
    // Retryable: Timeout, 429 Rate Limit, 5xx Server Errors
    if errors.Is(err, context.DeadlineExceeded) {
        return true
    }

    if apiErr, ok := err.(*APIError); ok {
        switch apiErr.StatusCode {
        case 429, 500, 502, 503, 504:
            return true
        case 400, 401, 403, 404:
            return false
        }
    }

    return false
}
```

**Akzeptanzkriterien:**
- [ ] 3 Retries mit Exponential Backoff (500ms → 1s → 2s)
- [ ] Kein Retry bei 4xx Client-Fehlern
- [ ] Max. 5s Delay
- [ ] Context-Cancellation wird respektiert
- [ ] Metriken: Retry-Count pro Request

---

### 3. Health Check Endpoints implementieren

**Status:** Offen
**Priorität:** Mittel
**Geschätzter Aufwand:** 3-4 Stunden

**Aktuelles Problem:**
- Keine Möglichkeit zu prüfen welche Modelle verfügbar sind
- GoLisp muss "blind" API-Aufrufe machen

**Erforderliche Änderung:**

```go
// api/handlers/health.go
package handlers

import (
    "net/http"
    "time"
)

type HealthResponse struct {
    Status    string                       `json:"status"` // "healthy", "degraded", "unhealthy"
    Timestamp time.Time                    `json:"timestamp"`
    Providers map[string]ProviderStatus    `json:"providers"`
}

type ProviderStatus struct {
    Status      string        `json:"status"` // "available", "unavailable", "circuit_open"
    LastChecked time.Time     `json:"last_checked"`
    Latency     time.Duration `json:"latency_ms"`
    Error       string        `json:"error,omitempty"`
}

func (h *HealthHandler) Check(c *gin.Context) {
    ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
    defer cancel()

    response := HealthResponse{
        Status:    "healthy",
        Timestamp: time.Now(),
        Providers: make(map[string]ProviderStatus),
    }

    // Parallel alle Provider prüfen
    var wg sync.WaitGroup
    providerChan := make(chan struct {
        name   string
        status ProviderStatus
    }, len(h.providers))

    for name, provider := range h.providers {
        wg.Add(1)
        go func(n string, p Provider) {
            defer wg.Done()

            start := time.Now()
            status := ProviderStatus{
                LastChecked: time.Now(),
            }

            // Lightweight health check (z.B. models endpoint)
            _, err := p.HealthCheck(ctx)
            status.Latency = time.Since(start)

            if err != nil {
                status.Status = "unavailable"
                status.Error = err.Error()

                // Prüfen ob Circuit Breaker offen
                if isCircuitBreakerOpen(err) {
                    status.Status = "circuit_open"
                }

                response.Status = "degraded"
            } else {
                status.Status = "available"
            }

            providerChan <- struct {
                name   string
                status ProviderStatus
            }{n, status}
        }(name, provider)
    }

    go func() {
        wg.Wait()
        close(providerChan)
    }()

    for ps := range providerChan {
        response.Providers[ps.name] = ps.status
    }

    c.JSON(http.StatusOK, response)
}
```

**Neue Endpoints:**

```
GET /health          # Gesamt-Status aller Provider
GET /ready           # Kubernetes-style readiness check
GET /providers       # Liste aller Provider mit Status
```

**Response-Beispiel:**

```json
{
  "status": "degraded",
  "timestamp": "2025-02-26T18:30:00Z",
  "providers": {
    "claude-h": {
      "status": "circuit_open",
      "last_checked": "2025-02-26T18:29:55Z",
      "latency_ms": 0,
      "error": "circuit breaker is open"
    },
    "gemini-p": {
      "status": "available",
      "last_checked": "2025-02-26T18:29:58Z",
      "latency_ms": 234
    }
  }
}
```

**Akzeptanzkriterien:**
- [ ] Endpoint `/health` liefert Status aller Provider
- [ ] Check dauert max. 5s (parallel)
- [ ] Status: "available", "unavailable", "circuit_open"
- [ ] Latenz-Messung pro Provider
- [ ] GoLisp kann vorab prüfen welche Modelle funktionieren

---

### 4. Bessere Fehlermeldungen

**Status:** Offen
**Priorität:** Mittel
**Geschätzter Aufwand:** 2-3 Stunden

**Aktuelles Problem:**
- Generische "API_FAILED" Meldungen
- Keine Information über Retry-Möglichkeit
- Keine Empfehlung für Alternativen

**Erforderliche Änderung:**

```go
// internal/errors/types.go
package errors

type APIError struct {
    Type       string            `json:"type"`        // "rate_limit", "auth_failed", "timeout", etc.
    Message    string            `json:"message"`
    Provider   string            `json:"provider"`
    StatusCode int               `json:"status_code,omitempty"`
    RetryAfter int               `json:"retry_after,omitempty"` // Sekunden
    Suggestion string            `json:"suggestion,omitempty"`
    Fallbacks  []string          `json:"fallback_models,omitempty"`
    Details    map[string]interface{} `json:"details,omitempty"`
}

func (e *APIError) Error() string {
    return fmt.Sprintf("[%s] %s", e.Type, e.Message)
}

// Konkrete Fehlertypen
func NewRateLimitError(provider string, retryAfter int) *APIError {
    return &APIError{
        Type:       "rate_limit",
        Message:    fmt.Sprintf("Rate limit exceeded for provider '%s'", provider),
        Provider:   provider,
        StatusCode: 429,
        RetryAfter: retryAfter,
        Suggestion: "Wait before retrying or use alternative model",
        Fallbacks:  getAlternativeModels(provider),
    }
}

func NewCircuitBreakerError(provider string) *APIError {
    return &APIError{
        Type:       "circuit_open",
        Message:    fmt.Sprintf("Circuit breaker open for provider '%s'", provider),
        Provider:   provider,
        StatusCode: 503,
        Suggestion: "Provider temporarily unavailable - try alternative models",
        Fallbacks:  getAlternativeModels(provider),
    }
}

func getAlternativeModels(provider string) []string {
    // Mapping von failed provider zu Alternativen
    alternatives := map[string][]string{
        "claude-h":   {"gemini-p", "kimi", "mistral-l3"},
        "gpt41":      {"gemini-p", "deepseek-v3", "kimi"},
        "gemini-p":   {"claude-h", "kimi"},
    }
    return alternatives[provider]
}
```

**HTTP Response:**

```json
{
  "error": {
    "type": "rate_limit",
    "message": "Rate limit exceeded for provider 'anthropic'",
    "provider": "claude-h",
    "status_code": 429,
    "retry_after": 30,
    "suggestion": "Wait before retrying or use alternative model",
    "fallback_models": ["gemini-p", "kimi", "mistral-l3"]
  }
}
```

**Akzeptanzkriterien:**
- [ ] Alle Fehler haben `type` Feld
- [ ] Rate Limit liefert `retry_after`
- [ ] Circuit Open liefert `fallback_models`
- [ ] GoLisp kann Fehlertyp auswerten und reagieren

---

### 5. Per-Provider Rate Limiting & Queue

**Status:** Offen
**Priorität:** Mittel
**Geschätzter Aufwand:** 6-8 Stunden

**Aktuelles Problem:**
- Parallele Anfragen überlasten Provider
- Keine Fairness zwischen Nutzern

**Erforderliche Änderung:**

```go
// internal/ratelimit/provider.go
package ratelimit

import (
    "context"
    "golang.org/x/time/rate"
)

type ProviderLimiter struct {
    Name          string
    Limiter       *rate.Limiter    // Token Bucket
    Semaphore     chan struct{}    // Max Concurrent
    Queue         chan QueuedRequest
    MaxQueueSize  int
}

type QueuedRequest struct {
    Request   interface{}
    Response  chan<- Response
    Deadline  time.Time
}

func NewProviderLimiter(name string, rps int, burst int, maxConcurrent int) *ProviderLimiter {
    p := &ProviderLimiter{
        Name:         name,
        Limiter:      rate.NewLimiter(rate.Limit(rps), burst),
        Semaphore:    make(chan struct{}, maxConcurrent),
        Queue:        make(chan QueuedRequest, 100),
        MaxQueueSize: 100,
    }

    go p.processQueue()

    return p
}

func (p *ProviderLimiter) processQueue() {
    for req := range p.Queue {
        // Rate Limiting
        p.Limiter.Wait(context.Background())

        // Concurrency Limiting
        p.Semaphore <- struct{}{}

        go func(r QueuedRequest) {
            defer func() { <-p.Semaphore }()

            // Timeout prüfen
            if time.Now().After(r.Deadline) {
                r.Response <- Response{Error: context.DeadlineExceeded}
                return
            }

            // Request ausführen
            resp := executeRequest(r.Request)
            r.Response <- resp
        }(req)
    }
}

func (p *ProviderLimiter) Submit(ctx context.Context, req interface{}) (Response, error) {
    responseChan := make(chan Response, 1)

    queuedReq := QueuedRequest{
        Request:  req,
        Response: responseChan,
        Deadline: time.Now().Add(30 * time.Second),
    }

    // Queue-Größe prüfen
    if len(p.Queue) >= p.MaxQueueSize {
        return Response{}, fmt.Errorf("queue full for provider %s", p.Name)
    }

    select {
    case p.Queue <- queuedReq:
        select {
        case resp := <-responseChan:
            return resp, resp.Error
        case <-ctx.Done():
            return Response{}, ctx.Err()
        }
    case <-ctx.Done():
        return Response{}, ctx.Err()
    }
}
```

**Konfiguration pro Provider:**

```yaml
providers:
  anthropic:
    rate_limit:
      rps: 10          # Requests per second
      burst: 20        # Burst capacity
    concurrency:
      max_parallel: 5  # Max parallel requests
      queue_size: 100  # Max queue depth
      timeout: 30s     # Max wait time in queue
```

**Akzeptanzkriterien:**
- [ ] Token Bucket Rate Limiting pro Provider
- [ ] Max Concurrent Requests begrenzt
- [ ] Gepufferte Queue mit Timeout
- [ ] Fairness: FIFO Verarbeitung
- [ ] Metriken: Queue Depth, Wait Time

---

## 🧪 Testplan

### Unit Tests

```go
func TestCircuitBreakerConfig(t *testing.T) {
    cb := NewCircuitBreaker(Config{
        FailureThreshold: 5,
        CooldownDuration: 10 * time.Second,
    })

    // 4 Fehler sollten nicht öffnen
    for i := 0; i < 4; i++ {
        cb.RecordFailure()
    }
    assert.False(t, cb.IsOpen())

    // 5. Fehler sollte öffnen
    cb.RecordFailure()
    assert.True(t, cb.IsOpen())
}

func TestRetryWithExponentialBackoff(t *testing.T) {
    config := RetryConfig{
        MaxRetries: 3,
        BaseDelay:  100 * time.Millisecond,
        Multiplier: 2.0,
    }

    callCount := 0
    err := CallWithRetry(context.Background(), func() error {
        callCount++
        if callCount < 3 {
            return fmt.Errorf("transient error")
        }
        return nil
    }, config)

    assert.NoError(t, err)
    assert.Equal(t, 3, callCount)
}
```

### Integration Tests

```bash
# Health Check
 curl http://localhost:9080/health | jq

# Rate Limiting
 for i in {1..10}; do
   curl -X POST http://localhost:9080/v1/chat/completions \
     -d '{"model":"claude-h","messages":[{"role":"user","content":"test"}]}' &
 done
 wait

# Circuit Breaker Verhalten
 # 5 Fehler provozieren, dann prüfen ob CB offen
```

---

## 📊 Metriken & Monitoring

### Prometheus Metriken

```go
// Zu implementierende Metriken
var (
    requestDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name: "sigorest_request_duration_seconds",
            Help: "Request duration by provider",
        },
        []string{"provider"},
    )

    requestTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "sigorest_requests_total",
            Help: "Total requests by provider and status",
        },
        []string{"provider", "status"},
    )

    circuitBreakerState = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "sigorest_circuit_breaker_state",
            Help: "Circuit breaker state (0=closed, 1=open, 2=half-open)",
        },
        []string{"provider"},
    )

    queueDepth = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "sigorest_queue_depth",
            Help: "Current queue depth by provider",
        },
        []string{"provider"},
    )
)
```

---

## 🚀 Rollout Plan

### Phase 1: Sofort (1-2 Tage)
- [ ] Circuit Breaker Config anpassen
- [ ] Retry-Mechanismus implementieren
- [ ] Bessere Fehlermeldungen

### Phase 2: Kurzfristig (1 Woche)
- [ ] Health Check Endpoints
- [ ] Metriken & Monitoring
- [ ] Integration Tests

### Phase 3: Mittelfristig (2-4 Wochen)
- [ ] Per-Provider Rate Limiting
- [ ] Queue-Management
- [ ] Automatische Fallbacks

---

## 📝 Notizen

### GoLisp Workaround (bis Implementierung)

```lisp
;; Defensive Programmierung in GoLisp
(defun safe-sigo (prompt model)
  (catch
    (sigo prompt model)
    (lambda (e)
      (println (string-append "[Warnung] " model " fehlgeschlagen, versuche Fallback..."))
      nil)))

;; Verfügbare Modelle prüfen
(defun check-model (model)
  (not (null? (safe-sigo "test" model))))

;; Parallele Anfragen mit Error-Handling
(parfunc results
  (safe-sigo prompt "gemini-p")
  (safe-sigo prompt "kimi")
  (safe-sigo prompt "mistral-l3"))

;; Nur erfolgreiche Ergebnisse verwenden
(define valid-results (filter (lambda (x) (not (null? x))) results))
```

---

**Autor:** Claude + Gerhard Quell
**Letzte Aktualisierung:** 2025-02-26
