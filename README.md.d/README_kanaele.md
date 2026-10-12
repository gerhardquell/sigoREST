# Kanäle: Multi-Channel, Failover, Rate-Limiting, Health-Monitor

[← README](../README.md)

Pro Provider können mehrere API-Key-**Kanäle** verwaltet werden
(`ChannelRegistry` in `sigoengine/channel.go`, Zustand in `channels.json`).
Jeder Kanal hat eigenen API-Key, eigenen Memory, eigenen System-Prompt,
eigene Sessions und eigenen Circuit Breaker.

## Standardverhalten

- Nur der unindizierte Key (z.B. `MAMMOUTH_API_KEY`) wird als
  `default`-Kanal aktiv geschaltet.
- Reservekanäle (`_0`, `_1`, ...) sind inaktiv, können aber manuell oder
  automatisch zugeschaltet werden.
- Ollama hat keinen Key: Werden beim Start Ollama-Modelle gefunden, legt
  sigoREST den keylosen Kanal `ollama-default` an (siehe
  [Provider & Modelle → Ollama](README_provider-modelle.md#ollama-lokale-llms)).

## Kanäle verwalten

```bash
# Alle Kanäle anzeigen
curl -s http://localhost:9080/api/channels

# Einzelkanal anzeigen
curl -s http://localhost:9080/api/channels/mammouth/0

# Kanal aktivieren / deaktivieren
curl -s -X POST http://localhost:9080/api/channels/mammouth/0/enable
curl -s -X POST http://localhost:9080/api/channels/mammouth/0/disable

# Kanal-Memory setzen
curl -s -X PUT http://localhost:9080/api/channels/mammouth/0/memory \
  -H "Content-Type: application/json" \
  -d '{"content":"Kanal-spezifischer Kontext","cache":false}'

# Kanal-System-Prompt setzen
curl -s -X PUT http://localhost:9080/api/channels/mammouth/0/system-prompt \
  -H "Content-Type: application/json" \
  -d '{"system_prompt":"Antworte wie ein Pirat."}'
```

Ein Request kann den Kanal wählen: Feld `channel` (z.B. `mammouth-0`) oder
Kanal-Suffix am Shortcode (`zai-glm45-2`), siehe [API](README_api.md).

## Auto-Failover

Bei einem Request probiert der `ChannelManager`
(`sigoengine/channel_manager.go`) die aktiven Kanäle eines Providers der
Reihe nach durch — Failover ist transparent für den Client. Schlägt ein Kanal
fehl (Rate-Limit, Timeout, Server-Fehler), kommt der nächste dran.
Auth-Fehler deaktivieren den betroffenen Kanal sofort persistent.

Ausnahme: Ein vom Provider gemeldetes erschöpftes Budget ist **kein**
Rate-Limit und löst keinen Failover aus (alle Keys gehören demselben User),
siehe [Kosten & Budget](README_kosten-budget.md#provider-budget-erschöpft--rate-limit).

## Rate-Limiter (pro Kanal, hybrid)

Jeder Kanal hat einen eigenen Rate-Limiter, der zu schnelle
aufeinanderfolgende Calls an denselben API-Key drosselt — Provider-Rate-Limits
werden so vermieden statt im Fehlerfall repariert.

- **`min_interval`** (Default `-rate-min-interval 500ms`): Mindest-Abstand
  zwischen zwei Calls pro Kanal.
- **`max_wait`** (Default `-rate-max-wait 1000ms`): Wie lange ein Request
  maximal auf den freien Kanal wartet.

Verhalten: `RateLimiter.Acquire` lässt einen Request, der innerhalb von
`min_interval` nach dem letzten Call ankommt, warten, bis das Intervall
verstrichen ist. Würde die Wartezeit `max_wait` überschreiten, schlägt er mit
`ErrRateLimited` fehl → der Failover probiert den **nächsten Kanal**. Erst
wenn alle Kanäle eines Providers erschöpft sind, erhält der Client HTTP 429 +
`Retry-After`. So verteilt sich ein Burst automatisch auf freie API-Keys.

Die Granularität ist bewusst pro Kanal, nicht pro Provider: Ein
Provider-weiter Limiter würde alle Failover-Keys gleichzeitig blockieren.

Pro-Kanal-Override in `channels.json` (Struct-Felder `MinInterval`/`MaxWait`, beide optional, `0`/fehlend →
Server-Default; sichtbar in `/api/channels`):

```json
{
  "provider": "mammouth",
  "name": "0",
  "active": true,
  "min_interval_ms": 800,
  "max_wait_ms": 2000
}
```

Serverweit deaktivieren: `-rate-min-interval 0`.

## Health-Monitor

`StartHealthMonitor` (`sigoengine/channel_health.go`) prüft im Hintergrund
alle `-channel-health-interval` die aktiven Kanäle. Sind alle aktiven Kanäle
eines Providers unhealthy, wird der nächste inaktive Reservekanal automatisch
aktiviert.

Die Probe läuft lazy über GET `/models` (oder das Äquivalent des Providers),
**nicht** über einen echten Chat-Call — im Leerlauf entstehen keine
API-Kosten. Jeder Provider braucht dafür einen Eintrag in
`modelsEndpointForProvider()` (`sigoengine/engine.go`); fehlt er, meldet die
Probe immer "unavailable" (abgesichert durch `TestModelsEndpointForProvider`).

## Manuelles Deaktivieren hat Vorrang

`POST /api/channels/:provider/:name/disable` → `SetActiveManual` setzt
`Channel.ManuallyDisabled` (`manually_disabled: true`, persistiert in
`channels.json`, sichtbar in `/api/channels`). Der Health-Monitor überspringt
solche Kanäle bei der Reserve-Suche und aktiviert sie **nie** wieder — auch
nicht nach einem Neustart und auch nicht, wenn es der einzige Kanal des
Providers ist. Eine andere Reserve desselben Providers darf einspringen. Nur
ein manuelles `/enable` (`SetActiveManual(true)`) hebt das auf.

Automatische Übergänge (Health-Monitor, Auth-Fehler im Chat-Handler) nutzen
`SetActive` und lassen das Flag unangetastet.

Historie: `RETROSPECTIVE.md`, Sessions 2026-08-18 und 2026-09-27.
