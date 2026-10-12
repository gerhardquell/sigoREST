# Provider & Modelle

[← README](../README.md)

## Dynamisches Modell-Laden (Server)

Beim Serverstart ruft `loadModelsFromProviders()` sequenziell sechs
Provider-APIs ab, dazu kommt die Ollama-Discovery. Jeder Fetcher ist in
`FetchWithRetry` gewickelt (4 Versuche, 2s/4s/8s Backoff). Einzelne
Fehlschläge werden geloggt; der Server startet mit dem Rest weiter.

| Provider | Auth | Modelle |
|----------|------|---------|
| Mammouth | `/public/models` ohne Key; Chat mit `MAMMOUTH_API_KEY` | GPT, Claude, Gemini, Grok, DeepSeek, ... |
| Moonshot | `MOONSHOT_API_KEY` | Kimi, moonshot-v1-* |
| ZAI | `ZAI_API_KEY` | GLM-Serie |
| Longcat | `LONGCAT_API_KEY` | LongCat |
| cheaperinference | `OMNIROUTE_API_KEY` | Aggregator, IDs `ci-…` |
| OpenRouter | `OPENROUTER_API_KEY` | Aggregator, IDs `<vendor>/<modell>` |
| Ollama | — | lokal installierte Modelle |

Die aktuelle Liste (am 2026-10-12 rund 570 Modelle) liefert der Server selbst:

```bash
curl -s http://localhost:9080/v1/models | jq '.data[].id'
```

### Fallback-Asymmetrie bei Fehlern

Relevant bei Netz-/DNS-Problemen:

| Provider | bei Fehler | Folge |
|----------|-----------|-------|
| Mammouth | `return nil, err` | 0 Modelle |
| Moonshot | `return nil, err` | 0 Modelle |
| ZAI | `return zaiStaticModels, nil` | statische Modelle |
| Longcat | `return longcatKnownModels, nil` | statische Modelle |
| cheaperinference | `return nil, err` | 0 Modelle (kein Static-Fallback — Preise sind der Zweck) |
| OpenRouter | `return nil, err` | 0 Modelle (kein Static-Fallback — Preise sind der Zweck) |

Erscheinen beim Boot nur die statischen ZAI-/Longcat-Modelle ("no such host"
im Log), war DNS beim Start noch nicht oben. Schutz und Workaround:
[Betrieb → DNS-Race beim Boot](README_betrieb.md#dns-race-beim-boot).

### Mammouth

`/public/models` liefert Preise und Limits im LiteLLM-Format verschachtelt
unter `model_info` (`input_cost_per_token`/`output_cost_per_token`,
**USD pro Token** → ×1e6; `max_input_tokens`/`max_output_tokens`). Diese
Werte haben Vorrang; die statische `mammouthKnownModels`-Tabelle ist nur
Fallback und teils veraltet (Test: `sigoengine/mammouth_fetcher_test.go`).
Fehlende Preise buchen $0 und hebeln das Budget aus, siehe
[Kosten & Budget](README_kosten-budget.md#budget-check-vor-jedem-call).

### cheaperinference — Aggregator statt Einzel-Provider

`GET /v1/models` liefert live Preise (`pricing.input_per_million`/
`output_per_million`, `cache_read_input_per_million`) und Kontextfenster
(`context_length`/`max_output_tokens`) für alle Modelle mit, gefiltert auf
`type=="text" && endpoint=="/v1/chat/completions"` (Rest sind
Bild-/Video-Modelle). Kein statisches Known-Model-Mapping nötig wie bei
ZAI/Longcat/Moonshot.

IDs sind `ci-`-präfixt (z.B. `ci-claude-opus-5`), weil dieselben Modelle oft
auch direkt über Mammouth/Moonshot/ZAI laufen und sonst die ID-Map
kollidieren würde. `sigoengine.Model.UpstreamID` trägt den unpräfixten
Original-Namen; `handleChatCompletions` überschreibt damit `cfg.Model` (nicht
nur `cfg.Endpoint`) — sonst ginge `"ci-claude-opus-5"` als `model`-Feld raus,
das die API nicht kennt.

### OpenRouter — Aggregator mit Vendor-Präfix in der ID

`GET /v1/models` ist öffentlich (laut OpenRouter-Doku kein Key nötig; der
Fetcher schickt ihn trotzdem mit, konsistent zu den anderen Bearer-Fetchern)
und liefert live Preise (`pricing.prompt`/`completion`/`input_cache_read`,
**USD pro Token**, nicht USD/1M wie der Rest der Registry — der Fetcher
multipliziert ×1e6), Kontextfenster (`context_length`) und
`top_provider.max_completion_tokens`. Gefiltert auf `architecture.modality`
mit Text-Output (Suffix `->text`), sonst landen reine Bild-/Audio-Modelle in
der Chat-Liste.

IDs sind bereits `<vendor>/<modell>` (z.B. `anthropic/claude-opus-5`) und
gehen unverändert als `model`-Feld an die API — kein `UpstreamID`-Mapping
nötig, keine ID-Kollision mit anderen Fetchern (deren IDs kein `/` enthalten).

- **Shortcode:** `GenerateShortcode` erkennt Familien nur am Anfang des
  Strings (`"claude-..."`, `"gpt-..."`). `FetchOpenRouterModels` schneidet
  deshalb den Vendor-Teil vor dem `/` ab, erst der Rest geht in
  `generateProviderShortcode` — `anthropic/claude-opus-5` erkennt so die
  Familie `claude`. Auf die volle ID angewandt, landeten alle Modelle eines
  Vendors über den Cutter-Fallback auf praktisch demselben Code.
- **`:batch`-Varianten werden beim Fetch verworfen**
  (`strings.HasSuffix(item.ID, ":batch")`, Stand 2026-10-06: 73 von 449).
  OpenRouter lehnt sie über `/chat/completions` mit HTTP 404 ab ("cannot be
  used with the chat/completions endpoint") — sie laufen über eine separate
  Async-Batch-API, die sigoREST nicht anbietet. Andere `:`-Suffixe (`:free`,
  `:beta`, `:nitro`, `:floor`, `:extended`) sind normale
  Chat-Completion-Routing-Modifier und bleiben drin.

## Shortcodes

### Generierung

`GenerateShortcode` (`sigoengine/shortcode.go`) baut sprechende Kürzel:
Familie (longest-prefix, z.B. `gpt`, `claude→cl`, `gemini→gem`) + Subfamily
(`sonnet→s`) + Version (`5.1→51`) + Variante (`mini→m`, `flash→f`).
Kollisionen bekommen einen numerischen Suffix, Unbekanntes einen
Cutter-Sanborn-Code.

- **Trennzeichen:** Teile werden über `strings.FieldsFunc` an `-` **und** `:`
  gesplittet; `variantMap` kennt auch `batch`/`free`/`nitro`/`floor`/`extended`
  (dieselbe Kategorie wie `beta`/`thinking`) → `claude-opus-4.5:free` →
  `cl45-ofree`. Der `batch`-Eintrag greift trotz des OpenRouter-Filters nur,
  falls ein Modellname "batch" bindestrich-getrennt enthält.
- **Bekannte Marotte:** Modelle ohne Ziffern-Version vor dem ersten erkannten
  Varianten-Wort (z.B. `claude-fable-5.1:free`, `gpt-oss-120b:free`) lassen
  das erste Treffer-Wort als "Subfamily" vorn landen (`cl-freef025.1` statt
  `cl-f025.1free`) — dieselbe Ursache wie das `fable`-Kuriosum, kein Bug.
- **Kollisionen in beiden Zweigen:** Der No-Family-Zweig (IDs ohne
  Präfix-Treffer wie `ci-...`, `LongCat-...`) nutzt `cutterCode(modelID)`,
  das nur den längsten Tabellen-Präfix liefert — viele IDs ergeben denselben
  Code. Familien- und No-Family-Pfad laufen deshalb durch denselben
  `resolveCollision(sc, used)`-Helper (`l62`, `l62-2`, `l62-3`). Ein neuer
  Zweig muss das auch tun. Regressionstest:
  `TestGenerateShortcode_NoFamilyMatchStillDeduplicates`.

Auf dem Server wird dieser Code nur als Vorschlag (`SemanticHint`) an die
persistente Registry übergeben, die das endgültige Kürzel
`{provider3}-{code}` einmalig vergibt — siehe
[Shortcode-Registry](README_shortcode-registry.md).

### Auflösung

Der Server prüft zuerst nach ID, dann alle Shortcodes. Bei Treffer geht die
ID in den API-Call. `model` akzeptiert ID, Shortcode oder Shortcode mit
Kanal-Suffix (`zai-glm45-2`), siehe [API](README_api.md).

### Beispiele

Shortcodes ändern sich je nach Provider-Angebot — aktuell gültige liefert
`GET /api/shortcodes` bzw. `GET /api/shortlist`. Stand 2026-10-12:

| Shortcode (Server) | Provider | Code |
|-----------|----------|------|
| `mam-gpt41` | mammouth | `mammo` |
| `mam-gpt4o` | mammouth | `mammo` |
| `mam-cl46-s` | mammouth | `mammo` |
| `moo-kimik3` | moonshot | `moons` |
| `zai-glm51` | zai | `zai__` |
| `lon-l62` | longcat | `longc` |
| `che-cl46-s` | cheaperinference | `cheap` |
| `ope-cl46-s` | openrouter | `openr` |

Die CLI nutzt eigene Kürzel aus ihrer Registry bzw. `CoreModels`
(z.B. `gpt41`, `cl-s`, `kimi`, `zai-glm45`).

## Provider-Kennzeichnung (`sigoengine/provider_id.go`)

Welcher Provider hinter einem Shortcode steckt, ist nicht immer am Namen
ablesbar (`ci-…`-IDs liefern z.B. eigentlich Claude). Es gibt genau eine
kanonische Erkennung:

- `ProviderFromEndpoint(endpoint)` — erkennt anhand der Endpoint-URL
  (`mammouth`, `moonshot`, `z.ai`, `longcat`, `cheaperinference`,
  `localhost:11434`/`127.0.0.1:11434` → `ollama`); `""` bei keinem Match
- `ProviderFromModelID(modelID)` — Namens-Heuristik als Fallback
  (`ollama-`-Präfix, `kimi`, `glm`, `longcat`, `ci-`-Präfix, `/` →
  `openrouter` — einzige IDs mit Slash), Default `"mammouth"`
- `ResolveProvider(endpoint, modelID)` — kombiniert beide: Endpoint zuerst
  (zuverlässiger, vom Server selbst gesetzt), Namens-Heuristik nur als Fallback
- `ProviderCode(provider)` — normiert auf einen festen 5-Zeichen-Code
  (`providerCodes`-Map; unbekannte Provider werden mit `_` aufgefüllt bzw.
  auf 5 Zeichen gekappt — Tabellen bleiben so ausgerichtet)

| Provider | Code |
|----------|------|
| `mammouth` | `mammo` |
| `moonshot` | `moons` |
| `zai` | `zai__` |
| `longcat` | `longc` |
| `cheaperinference` | `cheap` |
| `ollama` | `ollam` |
| `openrouter` | `openr` |

Genutzt von `sigoREST/main.go` (`providerForModel`/`providerForModelLocked`
sind dünne Wrapper), `/api/shortlist`, `/api/models` (`provider`/
`provider_code`), `/v1/models` (`owned_by`) und `cmd/sigoE` (`-l`, `-i`,
`providerForModelCLI`). Eine eigene Heuristik daneben zu pflegen führt dazu,
dass Calls mit dem falschen API-Key rausgehen. Früher gab es drei
unabhängige, unterschiedlich vollständige Kopien dieser Logik (Server, CLI,
`knownProviders`); Historie in `RETROSPECTIVE.md`.

Wege, den Provider ohne Rätselraten herauszufinden:

1. **`sigoE -l`** — CLI-Übersicht, nach Provider gruppiert, jede Gruppe mit
   ihrem Code im Header:
   ```
   --- mammouth [mammo] ---
   Modell               Shortcode      Input$/M  Output$/M
   ...
   --- zai [zai__] ---
   ...
   ```
2. **`sigoE -i <shortcode>`** — Einzel-Lookup, zeigt `Provider: zai [zai__]`.
3. **`GET /api/shortlist`** — dieselbe Info für Skripte; `GET /api/models`
   und `GET /v1/models` führen den Provider ebenfalls (`provider`/
   `provider_code` bzw. `owned_by`).

## CSV/Registry-Format (CLI)

`sigoengine/models_registry.go`. Lade-Reihenfolge: JSON → CSV → `CoreModels`.
Semikolon-getrennt (Komma kollidiert mit JSON-Arrays/Listen), 11 Felder:

```
id;shortcode;endpoint;apikey;max_input;max_output;input_cost;output_cost;min_temp;max_temp;requires_completion_tokens
```

Optional danach `;provider;provider_code;upstream_id;cached_input_cost` — so
exportiert `GET /api/models?format=csv` (`sigoengine.WriteModelsCSV`,
Kopfzeile mit `#` = Parser-Kommentar). Beim Laden werden `upstream_id` (nötig
für `ci-*`) und `cached_input_cost` übernommen, der Provider wird neu
berechnet.

- `requires_completion_tokens=true` → Modell nutzt `max_completion_tokens`
  statt `max_tokens` (z.B. GPT-5).
- `apikey` ist der Name der ENV-Variable, nie der Key (leer bei Ollama).
- `cached_input_cost` ($/1M Cache-Read-Tokens, 0 = kein bekannter Rabatt) —
  aktuell nur von cheaperinference befüllt.

```bash
# Live-Shortcodes des Servers als CLI-Registry übernehmen
curl -s 'http://localhost:9080/api/models?format=csv' > models.csv
```

## Ollama (lokale LLMs)

Ollama-Modelle werden beim Serverstart via `GET /api/tags` automatisch
entdeckt — kein API-Key, keine Konfiguration. Voraussetzung: Ollama läuft auf
`http://localhost:11434` (`ollama serve`, falls nicht als Dienst aktiv).

```go
ollamaEndpoint := "http://localhost:11434"
sigoengine.DiscoverOllamaModels(ollamaEndpoint)
```

Ollama-Modelle haben `APIKey: ""` und nutzen
`http://localhost:11434/v1/chat/completions`.

**Shortcode-Schema:** `ollama-<modellname>` — `:latest` wird weggeschnitten,
andere Tags werden Suffix.

| Ollama-Modell | Shortcode |
|---------------|-----------|
| `llama3:latest` | `ollama-llama3` |
| `gemma3:4b` | `ollama-gemma3-4b` |
| `gemma3:12b` | `ollama-gemma3-12b` |
| `qwen3:latest` | `ollama-qwen3` |
| `qwen3:32b` | `ollama-qwen3-32b` |
| `devstral:latest` | `ollama-devstral` |
| `llama3.2-vision:latest` | `ollama-llama3.2-vision` |

**Kanal ohne Key:** Chat läuft auch für Ollama über `ChannelManager.Resolve`,
aber `DiscoverFromEnv` legt Kanäle nur für Provider mit API-Key an. Deshalb
läuft die Ollama-Discovery VOR der Registry, und `newChannelRegistry`
(`sigoREST/main.go`) registriert bei gefundenen Modellen
`AddKeylessChannel("ollama")` → Kanal `ollama-default` (sichtbar unter
`/api/channels`, per `.../ollama/default/disable` abschaltbar), danach
`LoadState` (per API deaktiviert bleibt deaktiviert). Fehlt der Kanal, endet
jeder Ollama-Chat (auch `/v1/messages`) mit `404 CONFIG_NOT_FOUND: no active
channel for provider`, während `/v1/embeddings` (ohne Kanal) weiter
funktioniert. Regressionstest: `TestHandleChatCompletions_OllamaWithoutAPIKey`.

**Limitation:** Nur Startzeit-Discovery — nach `ollama pull` ist ein
Neustart nötig.

```bash
ollama pull llama3.3
systemctl restart sigoREST   # oder ./build/sigoREST neu starten
# llama3.3 erscheint danach als "ollama-llama3.3"

# Erkannte Ollama-Modelle
curl -s http://localhost:9080/v1/models | jq -r '.data[].id | select(startswith("ollama-"))'

# Anfrage an lokales Modell
curl -s http://localhost:9080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"ollama-gemma3-4b","messages":[{"role":"user","content":"Hallo"}]}'
```

## Neues Modell hinzufügen

- **Server:** Modelle kommen dynamisch vom Provider. Bekannte Modelle werden
  in `provider_fetchers.go` angereichert (`moonshotKnownModels`,
  `zaiStaticModels`, `longcatKnownModels`; Mammouth über die API).
- **CLI:** Eintrag in CSV/Registry.

## Neuen Provider hinzufügen

Eine `Fetch*`-Funktion in `provider_fetchers.go`, ein Eintrag in
`knownProviders` (`channel.go`) und der Aufruf in
`loadModelsFromProviders()` reichen **nicht**. Zusätzlich nötig:

- **`modelsEndpointForProvider()`** in `sigoengine/engine.go` braucht einen
  `case` mit dem `/models`-Endpoint — sonst meldet die Health-Probe immer
  "unavailable" und der Health-Monitor aktiviert nie einen Kanal des
  Providers (abgesichert durch `TestModelsEndpointForProvider`, der
  `knownProviders` gegenprüft).
- **Provider-Erkennung** in `sigoengine/provider_id.go`: den neuen Provider in
  `ProviderFromEndpoint` **und** `ProviderFromModelID` sowie in
  `providerCodes` eintragen. Sonst fällt jedes Modell auf den Default-Zweig
  (`mammouth`) zurück, und Chat-Calls gehen mit falschem API-Key raus
  (Symptom: HTTP 401 "incorrect api key", obwohl der Key gültig ist — beim
  Longcat-Rollout genau so aufgetreten).

Danach einen echten End-to-End-Chat-Call testen, nicht nur den Models-Fetch
beim Boot.
