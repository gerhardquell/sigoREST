# Persistente Shortcode-Registry

[← README](../README.md)

Code: `sigoengine/id_registry.go`. Datenbank: `<data-dir>/id_registry.db`
(SQLite über `modernc.org/sqlite`, WAL — gleicher Treiber wie `costs.db`).

## Wozu

sigoREST lädt seine Modelle bei jedem Boot dynamisch von den Provider-APIs.
Pro Boot neu berechnete Shortcodes würden sich bei jeder Änderung der
Live-Liste verschieben. Die Registry vergibt deshalb jedem Provider ein
3-Zeichen-Kürzel und jedem `(provider, upstream_id)`-Paar **einmalig** einen
Shortcode im Format `{provider3}-{semanticCode}` (z.B. `zai-glm45`,
`mam-cl45-s`). Einmal vergeben, bleibt er für immer bei seinem Modell — auch
über Neustarts und Provider-Ausfälle hinweg — und wird nie wiederverwendet.

## Regeln

- **Semantischer Code kommt vom Fetcher, nicht aus einer Neuberechnung:**
  `SyncProvider` bekommt pro Modell einen `ProviderModelSeed` mit
  `SemanticHint` (= `Model.Shortcode` aus `provider_fetchers.go`, bei
  cheaperinference ohne das `ci-`-Präfix). Die Fetcher berechnen ihre Codes
  mit einer fetch-weiten `used`-Map und liefern deshalb innerhalb eines
  Providers unterscheidbare Kürzel (siehe
  [Provider & Modelle → Shortcodes](README_provider-modelle.md#shortcodes)).
- **Kollisions-Suffix in der Registry ist `.2`/`.3`**, nicht `-2` — `-`
  trennt den Kanal ab, `zai-glm45-2` wäre sonst zweideutig.
- **Kanal-Suffix:** `zai-glm45-2` → Modell `zai-glm45`, Kanal `2`
  (`IDRegistry.ResolveShortcode`).
- **Retire nach 3 aufeinanderfolgenden ERFOLGREICHEN Fetches ohne das
  Modell** (`miss_streak`). Ein fehlgeschlagener Fetch lässt den Zähler
  unangetastet — das schützt vor der
  [Fallback-Asymmetrie](README_provider-modelle.md#fallback-asymmetrie-bei-fehlern).
  Retired Modelle bleiben auflösbar und werden mit **HTTP 410**
  (`"model_retired"`) abgelehnt, kein stiller Fallback auf ein anderes
  Modell. Taucht ein retired Modell wieder auf, bekommt es seinen alten
  Shortcode zurück.
- **Nil-safe wie `costDB`:** Schlägt `OpenIDRegistry` beim Start fehl (z.B.
  Disk voll), läuft der Server mit pro Boot berechneten Shortcodes weiter.
  Scheitert die Zuweisung für ein einzelnes Modell, wird nur dieses übersprungen
  (`LogWarn`), nicht der ganze Provider-Sync.

## Operative Eigenheiten

1. **Retirement zählt Boots, nicht Fetch-Zyklen.** `SyncProvider` wird nur
   aus `loadModelsFromProviders()` aufgerufen, also ausschließlich beim
   Serverstart. Ein verschwundenes Modell braucht auf einem langlaufenden
   Deployment 3 **Neustarts** bis zum Retire. Der HTTP-410-Pfad ist
   erreichbar, aber träge — bewusste Scope-Entscheidung (Boot-Sync statt
   Hintergrund-Poller).
2. **Einmaliger Wechsel beim Upgrade auf die Registry-Version:** Beim ersten
   Boot mit Registry wurden alle Kürzel neu vergeben (`glm46` → `zai-glm46`,
   `cl45-s` → `mam-cl45-s`). Es gibt bewusst keine Kompatibilitätsschicht —
   Clients (Skripte, `ANTHROPIC_BASE_URL`-Configs, C++-Client, eigene Tools)
   müssen `/api/shortcodes` einmal neu abrufen. Siehe `TODO.md`, Abschnitt
   "Shortcode".
