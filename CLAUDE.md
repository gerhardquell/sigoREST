# CLAUDE.md

Anleitung für Claude Code bei Arbeit mit diesem Repo.

- Sprache: 
    - Deutsch 
    - Gerhard duzen und 
    - mit Namen ansprechen.
- Inhalt:
  - Projekt
  - Befehle
  - Regeln für Änderunge
    - Modelle & Provider
    - Server-Code
  - Betrieb

--- 

## Projekt

sigoREST: Go-Projekt (Modul `sigorest`, Go 1.26) mit drei Schichten — Shared
Package `sigoengine/`, CLI `cmd/sigoE/`, OpenAI-kompatibler REST-Server
`sigoREST/` (~100 parallele Verbindungen, Anthropic-Bridge unter
`/v1/messages`). Provider: Mammouth, Moonshot, Z.ai, Longcat,
cheaperinference, OpenRouter, lokales Ollama.

Ausführliche Doku in `README.md.d/` — bei Arbeit am jeweiligen Bereich dort
nachlesen:

| Bereich | Datei |
|---------|-------|
| Aufbau, `sigoengine`-Exporte, Circuit Breaker, Sessions, Logging | `README.md.d/README_architektur.md` |
| Flags, Zugriffskontrolle, API-Keys, Datenverzeichnis | `README.md.d/README_konfiguration.md` |
| Endpoints, Request-/Response-Erweiterungen, Bridge | `README.md.d/README_api.md` |
| Fetcher, Provider-Eigenheiten, Shortcodes, Provider-Erkennung, CSV, Ollama | `README.md.d/README_provider-modelle.md` |
| Persistente Shortcode-Registry (`id_registry.db`) | `README.md.d/README_shortcode-registry.md` |
| Kanäle, Failover, Rate-Limiter, Health-Monitor | `README.md.d/README_kanaele.md` |
| Usage, Kosten, Budget (`costs.db`) | `README.md.d/README_kosten-budget.md` |
| Installation, systemd, Kommunikationsprotokoll | `README.md.d/README_betrieb.md` |

Session-Historie und Lektionen: `RETROSPECTIVE.md`. Offene Punkte: `TODO.md`.
Die Doku in `README.md` + `README.md.d/` wird mitgepflegt;
`README_EN.md`/`README_CN.md` sind eingefroren und werden nicht angefasst.

## Befehle

```bash
make build          # sigoREST, sigoE, mockprovider → ./build/
make test           # alle Tests (= go test ./...)
go test ./sigoengine/ -run TestFetchWithRetry   # einzelne Tests (Regex)
```

- Server-Handler-Tests: `sigoREST/main_test.go` (Fake-Ollama `startFakeOllama`).
  Die CLI hat keine Go-Tests — manuell testen.
- Nach Server-Änderungen zusätzlich ein echter Call gegen einen Test-Server
  auf freiem Port: `./build/sigoREST -http-port 19080 -https-port 19443
  -data-dir <tmp>`. Der Live-Dienst belegt 9080/9443; auf Gerhards System
  sind außerdem 8080/8443 belegt (lokaler Webserver).
- Nach einem neuen Provider einen echten End-to-End-Chat-Call testen, nicht
  nur den Models-Fetch beim Boot.

## Regeln für Änderungen

**Scope:** sigoREST bleibt schlanker Proxy, kein Agent-Harness — kein
Tool-Call-Repair o.ä.

**Formatierung:** Go-Code ist gofmt-konform; vor jedem Commit muss
`gofmt -l .` leer sein.

**Modelle & Provider**
- Der Server lädt Modelle beim Start dynamisch (`loadModelsFromProviders()`),
  nicht aus einer CSV. Die CSV/Registry (`models_registry.go`) ist nur für die
  CLI. Nur `sigoREST/memory.json` ist eingebettet, die Disk-Version hat Vorrang.
- Provider-Erkennung gibt es genau einmal: `ResolveProvider`
  (`sigoengine/provider_id.go`). Server (`providerForModel`) und CLI
  (`providerForModelCLI`) delegieren dorthin — keine eigene Heuristik, sonst
  gehen Calls mit dem falschen API-Key raus (Symptom: HTTP 401 "incorrect api
  key" trotz gültigem Key).
- Neuer Provider braucht alles davon: `Fetch*` in `provider_fetchers.go`,
  Eintrag in `knownProviders` (`channel.go`), Aufruf in
  `loadModelsFromProviders()`, `case` in `modelsEndpointForProvider()`
  (`engine.go`, sonst aktiviert der Health-Monitor nie einen Kanal),
  `ProviderFromEndpoint` + `ProviderFromModelID` + `providerCodes`.
- Preise immer in $/1M Tokens. Mammouth (`model_info`) und OpenRouter liefern
  USD pro Token → ×1e6. Ein Modell ohne Preis bucht $0 und hebelt das Budget
  aus; in der Response ist `cost_usd` dann `null` (Ollama: `0.0`).
  Bei Moonshot/Z.ai/Longcat (Preise nur aus Tabelle) setzt `priceUnknown`
  für neue Modelle den Provider-Höchstpreis und warnt im Log — die Warnung
  heißt: Preis in der Tabelle nachtragen.
- cheaperinference-IDs sind `ci-`-präfixt; vor dem Call `cfg.Model` mit
  `Model.UpstreamID` überschreiben, nicht nur den Endpoint.
- Shortcodes: Jeder Zweig von `GenerateShortcode` läuft durch
  `resolveCollision` (Suffix `-2`). In der Registry ist der Kollisions-Suffix
  dagegen `.2`, weil `-` dort den Kanal abtrennt.

**Server-Code**
- Wer `s.mu` hält, ruft nur lock-freie Varianten (`providerForModelLocked()`,
  `lookupModelMemory()`). Go-`RWMutex` ist nicht reentrant — ein zweites
  `RLock()` kann deadlocken, sobald ein Writer wartet.
- Jeder Chat-Pfad ruft `Server.budgetBlocked()` vor dem Provider-Call
  (heute `handleChatCompletions` und `handleMessages`).
- Streaming: `CallAPIStream` setzt `stream_options.include_usage` — ohne
  echten Usage-Chunk greift `EstimateUsage` und bucht ein Vielfaches.
- `costDB` und `idRegistry` sind nil-safe; ein DB-Fehler wird nur geloggt und
  darf nie den Response-Pfad stören.
- Provider-Budget erschöpft (429/402 mit `budget_exceeded`,
  `ExceededBudget`, `insufficient_quota`) ist `ErrQuotaExceeded`: kein Retry,
  kein Failover.
- Ollama hat keinen API-Key und bekommt seinen Kanal über
  `AddKeylessChannel("ollama")`; Discovery läuft vor der Channel-Registry.
- Kanal-Status: Automatik (Health-Monitor, Auth-Fehler) nutzt `SetActive`,
  nur die API nutzt `SetActiveManual`. Manuell deaktivierte Kanäle aktiviert
  die Automatik nie wieder.
- Response-Bodys von Provider-Calls immer schließen — das
  Kommunikationsprotokoll (`-comm-log`) schreibt den Eintrag erst beim
  `Close()`.

## Betrieb

- systemd-Unit braucht `Wants=`/`After=network-online.target`, sonst lädt
  beim Boot nur die statische ZAI-/Longcat-Liste (DNS-Race). Details:
  `README.md.d/README_betrieb.md`, `docs/systemd-install.md`.
- API-Keys: `MAMMOUTH_API_KEY`, `MOONSHOT_API_KEY`, `ZAI_API_KEY`,
  `LONGCAT_API_KEY`, `OMNIROUTE_API_KEY` (cheaperinference),
  `OPENROUTER_API_KEY`; indizierte `_0`, `_1`, … sind Reservekanäle.
- Gerhards Tageslimit: $55.93 (= 50 €) mit Hard-Stop.
