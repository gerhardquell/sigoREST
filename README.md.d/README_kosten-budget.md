# Kosten, Usage & Budget

[← README](../README.md)

Code: `sigoengine/costdb.go`, `sigoREST/costhandlers.go`.

## Zwei Zähler

| | `/api/usage` | `/api/costs` |
|---|---|---|
| Speicher | nur RAM, Reset bei Neustart | persistent, `<data-dir>/costs.db` (SQLite, WAL) |
| Inhalt | Tokens + Requests | Tokens, Requests **und** USD-Kosten |

## Kosten-Tracking (`costs.db`)

Jeder abgeschlossene Chat-Call (`/v1/chat/completions` und `/v1/messages`,
über `recordUsage`/`recordUsageWithSession` in `sigoREST/main.go`) schreibt
ein Event in `usage_events`: Modell, Provider, Kanal, Session-ID (falls
gesetzt), Token-Zahlen und die daraus berechneten USD-Kosten anhand der
Modell-Preise (`ModelInfo.InputCost`/`OutputCost`, $/1M Tokens,
`CalcCostUSD`). Modelle ohne Preisangabe (z.B. `ollama-*`) werden mit $0
geloggt, nicht übersprungen — Tokens und Requests bleiben so auswertbar.

**Best-effort, nil-safe:** `Server.costDB` ist ein `*sigoengine.CostDB`,
dessen Methoden einen `nil`-Empfänger sauber abfangen (No-Op statt Panic).
Schlägt `OpenCostDB` beim Serverstart fehl (z.B. Disk voll,
Berechtigungsproblem), läuft der Server ohne Kosten-Persistenz weiter —
`recordUsage` überspringt den DB-Write, `/api/costs` und `/api/budget`
antworten mit `503 cost_tracking_disabled`. Ein Fehler beim Schreiben wird
nur geloggt (`LogWarn`) und bricht nie den Chat-Response-Pfad ab.

### Cache-Rabatt

`cost_usd` = `CalcCostUSD(input_cost, output_cost, cached_input_cost)`.
Der Cache-Rabatt greift nur, wenn der Provider einen Cache-Read-Preis liefert
(aktuell nur cheaperinference, `ModelInfo.CachedInputCost` aus
`pricing.cache_read_input_per_million`). Sonst (0) zählen Cache-Reads voll
zum `input_cost` — eine obere Schranke, da `prompt_tokens` dort die
gecachten Tokens mit enthält.

### Preis unbekannt ≠ gratis

`cost_usd` in der Response ist `null` statt `0.0`, wenn für das Modell kein
Preis bekannt ist (`InputCost`/`OutputCost` beide 0 UND nicht Ollama) — ein
Fetcher ohne Preisdaten heißt "unbekannt", nicht "gratis" (Anlass: in golisp2
lieferte `(json-parse (sigo …))` sonst `0` statt `:null`). Ollama bleibt
`0.0` (lokale Inferenz, bekannt kostenlos). Gilt für Streaming (eigener
Chunk vor `[DONE]`) und Non-Streaming gleich (`buildChatUsage`).
`/api/costs` ergänzt dieselbe Unterscheidung pro Modell als
`price_known: {"<model-id>": bool}`.

### Stream-Usage ist opt-in

OpenAI-kompatible Provider schicken im SSE-Stream nur dann einen
`usage`-Chunk, wenn `stream_options.include_usage` gesetzt ist. Deshalb setzt
`CallAPIStream` `stream_options:{include_usage:true}` für alle
Nicht-Anthropic-Provider (ein vom Client gesetzter Wert bleibt erhalten), und
`streamAnthropicResponse` wertet Usage über `ExtractUsage` aus (inkl.
`cached_tokens`). Fehlt der Chunk, fallen beide Stream-Pfade still auf
`EstimateUsage` zurück (Runen/3, kein Cache-Rabatt) — die Buchung ist dann
ein Mehrfaches zu hoch und der Hard-Stop greift zu früh. Tests:
`TestCallAPIStream_RequestsUsage`,
`TestStreamAnthropicResponse_UsageIncludesCachedTokens`.

`costs.db`-Werte für cheaperinference bis einschließlich 2026-10-07 sind
überhöht (2026-10-08 korrigiert).

### `until` aufrunden (`Summary()`)

Gespeicherte Timestamps sind ganze Sekunden (`ts.Unix()`), `until` in
`/api/costs` ist `time.Now()` mit Nanosekunden-Anteil. `ceilUnix()` rundet
`until` für die `Summary()`-Query auf die nächste volle Sekunde auf; mit
`until.Unix()` (floor) fiele ein Event aus derselben Sekunde heraus.
Regressionstest: `TestSummary_IncludesEventFromSameInstantAsUntil`.

## GET /api/usage

```bash
curl -s http://localhost:9080/api/usage
```

Kumulierte Token-Statistiken seit Serverstart — pro Modell, pro Kanal und
gesamt. Nur RAM, Reset bei Neustart.

```json
{
  "by_model": {
    "claude-sonnet-4-6": {
      "input_tokens": 1200,
      "output_tokens": 340,
      "total_tokens": 1540,
      "requests": 5
    }
  },
  "by_channel": {
    "claude-sonnet-4-6#mammouth-0": {
      "input_tokens": 600,
      "output_tokens": 170,
      "total_tokens": 770,
      "requests": 2
    }
  },
  "total": {
    "input_tokens": 1200,
    "output_tokens": 340,
    "total_tokens": 1540,
    "requests": 5
  }
}
```

## GET /api/costs

```bash
# Heute (lokale Zeitzone), Default ohne Parameter
curl -s http://localhost:9080/api/costs

# Bequemlichkeits-Shortcuts
curl -s "http://localhost:9080/api/costs?period=today"
curl -s "http://localhost:9080/api/costs?period=month"

# Frei wählbarer Zeitraum (RFC3339 oder YYYY-MM-DD)
curl -s "http://localhost:9080/api/costs?since=2026-09-01&until=2026-09-19"
```

Antwort — Gesamtsumme plus Aufschlüsselung nach Modell und Kanal:

```json
{
  "since": "2026-09-19T00:00:00+02:00",
  "until": "2026-09-19T18:05:15.869063475+02:00",
  "requests": 12,
  "input_tokens": 4400,
  "output_tokens": 28100,
  "total_tokens": 32500,
  "total_cost_usd": 0.842,
  "by_model": {
    "claude-sonnet-4-6": {"requests": 5, "input_tokens": 2000, "output_tokens": 12000, "total_tokens": 14000, "total_cost_usd": 0.6}
  },
  "by_channel": {
    "mammouth#mammouth-default": {"requests": 5, "input_tokens": 2000, "output_tokens": 12000, "total_tokens": 14000, "total_cost_usd": 0.6}
  }
}
```

## Budget (GET/PUT /api/budget)

Tages- und/oder Monats-Limit in USD, optional mit Hard-Stop (`0` = kein
Limit für dieses Feld). Ohne Hard-Stop ist ein überschrittenes Limit reine
Information (`daily_exceeded`/`monthly_exceeded`) — Default ist reines
Tracking, kein Eingriff. Mit `hard_stop_enabled: true` lehnt der Server jeden
weiteren Chat-Call mit `HTTP 402 budget_exceeded` ab, sobald ein gesetztes
Limit erreicht ist.

```bash
# Aktuelle Config + Verbrauchsstatus (Tag/Monat)
curl -s http://localhost:9080/api/budget

# Hartes Limit setzen (Server lehnt Calls mit HTTP 402 ab, sobald erreicht)
curl -s -X PUT http://localhost:9080/api/budget \
  -H "Content-Type: application/json" \
  -d '{"daily_limit_usd":5,"monthly_limit_usd":100,"hard_stop_enabled":true}'

# Nur beobachten, nie blockieren
curl -s -X PUT http://localhost:9080/api/budget \
  -H "Content-Type: application/json" \
  -d '{"daily_limit_usd":5,"monthly_limit_usd":100,"hard_stop_enabled":false}'
```

`GET`-Antwort:

```json
{
  "config": {"daily_limit_usd": 5, "monthly_limit_usd": 100, "hard_stop_enabled": true},
  "status": {
    "daily_spend_usd": 2.3, "monthly_spend_usd": 41.7,
    "daily_limit_usd": 5, "monthly_limit_usd": 100,
    "daily_exceeded": false, "monthly_exceeded": false,
    "hard_stop_enabled": true, "blocked": false
  }
}
```

Limit erreicht + Hard-Stop aktiv → jeder weitere Chat-Call:

```json
{"error": {"message": "Budget überschritten (Tag: $5.10/$5.00, Monat: $41.70/$100.00) — Hard-Stop aktiv", "type": "budget_exceeded", "code": "budget_exceeded"}}
```

In der Anthropic-Bridge (`/v1/messages`) kommt stattdessen
`402 billing_error` im Anthropic-Fehlerformat.

### Budget-Check vor jedem Call

`Server.budgetBlocked()` (`sigoREST/main.go`) läuft in `handleChatCompletions`
(nach dem Provider-Ping) **und** in `handleMessages`. Jeder neue Chat-Pfad
braucht diesen Check — ohne ihn läuft z.B. Claude Code über
`ANTHROPIC_BASE_URL` ungebremst am Hard-Stop vorbei. `CheckBudget(now)` prüft
die Tages-/Monats-Ausgaben (`spendSince`, lokale Zeitzone über
`StartOfDay`/`StartOfMonth`) gegen die konfigurierten Limits
(`budget_config`-Tabelle, Singleton-Zeile).

Modelle ohne Preis buchen $0 und zählen nicht gegen das Limit — ein Fetcher,
der Preise nicht findet, hebelt das Budget also still aus (siehe
[Provider & Modelle → Mammouth](README_provider-modelle.md#mammouth)).
Ausnahme: Moonshot, Z.ai und Longcat liefern keine Preise per API. Ein dort
neues Modell ohne Tabelleneintrag bekommt beim Boot den Höchstpreis der
bekannten Modelle desselben Providers (`priceUnknown`), mit Log-Warnung
„Modell ohne Preis … Preis in Tabelle nachtragen“. Zu viel gebucht ist die
sichere Seite.

Gerhards Tageslimit: $55.93 (= 50 €) mit Hard-Stop, als Sicherheitsnetz gegen
KI-Schwärme.

## Provider-Budget erschöpft ≠ Rate-Limit

Mammouth (LiteLLM-Gateway) meldet ein erschöpftes User-Budget als HTTP 429
(`"type":"budget_exceeded"`, `ExceededBudget: User=… over budget`),
unabhängig vom Guthaben. `classifyHTTPError` erkennt 429/402 mit
`budget_exceeded`/`ExceededBudget`/`insufficient_quota` im Body als
`ErrQuotaExceeded` — nicht retrybar, kein Failover (alle Keys = derselbe
User), der Client bekommt 402 (`budget_exceeded` bzw. `billing_error` in der
Bridge). Als `ErrRateLimit` eingestuft, liefe das in 4 Retries pro Kanal ×
Failover über alle Kanäle.
