# Design: Usage-Feld robust extrahieren + Fallback

## Status

Aktives Design-Dokument. Feature noch nicht implementiert.

## Ziel

`usage` Feld in `POST /v1/chat/completions` Response nie mehr `null`. Entweder echt vom Provider extrahiert oder heuristisch geschätzt.

## Motivation

`test-translator` Benchmark zeigte: `usage` Feld war lange `null`, dann teilweise verfügbar — Verhalten instabil je nach Provider und Modell. Clients können sich nicht auf Kosten-Tracking verlassen.

## API-Spezifikation

Response-Schema bleibt unverändert (OpenAI-kompatibel):

```json
{
  "usage": {
    "prompt_tokens": 42,
    "completion_tokens": 17,
    "total_tokens": 59
  }
}
```

Kein sichtbarer Unterschied zwischen echten und geschätzten Werten.

## Architektur & Datenfluss

```
Provider-Response
       |
       v
extractUsage(result, providerType)
       |
       +-- usage gefunden --> UsageData (echt)
       |
       +-- usage == nil --> estimateUsage(inputText, outputText)
                          |
                          v
                     UsageData (geschätzt)
```

## Komponenten

### 1. `extractUsage` (Erweiterung)

Ort: `sigoengine/engine.go`

Zusätzlich zu bestehenden Feldnamen:
- `prompt_tokens` / `completion_tokens` (OpenAI-Standard)
- `input_tokens` / `output_tokens` (Anthropic)
- `total_tokens` (Fallback wenn Input/Output fehlen)
- `promptTokenCount` / `candidatesTokenCount` (Gemini-Format)

### 2. `EstimateUsage` (neu)

Ort: `sigoengine/engine.go`

```go
func EstimateUsage(inputText, outputText string) *UsageData
```

Schätzung basiert auf Runen-Länge / 3 (konservative Approximation für westliche Sprachen). Keine externe Dependency (kein Tiktoken).

### 3. Fallback-Integration

Ort: `sigoREST/main.go`

Nach `CallAPI`: Wenn `responseUsage == nil`, rufe `EstimateUsage(inputText, responseText)` auf.

**Input-Berechnung:** Alle `user` und `system` Messages aus Request zusammengefasst.

## Thread-Safety

`extractUsage` und `EstimateUsage` sind stateless. Keine Locks nötig.

## Aenderungshistorie

| Datum | Autor | Aenderung |
|-------|-------|-----------|
| 2026-04-21 | kimi | Initiales Design-Dokument |
