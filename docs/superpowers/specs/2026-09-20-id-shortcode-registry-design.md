# Design: Langzeitstabiles ID-/Shortcode-Schema mit persistenter Registry

**Datum:** 2026-09-20
**Thema:** Provider+Modell+Kanal-Kennzeichnung, die Jahrzehnte stabil bleibt
**Status:** Vom Benutzer validiert, bereit für Implementierung
**Bezug:** Ersetzt Abschnitt "Shortcode" in `TODO.md` (20260920); Abschnitt "ID Vereinfachung" bleibt inhaltlich bestehen (siehe Korrekturen unten), Abschnitt "Fallback" ist separates Thema.

## Zusammenfassung

sigoREST lädt seine Modell-Liste bei jedem Boot dynamisch von fünf Live-Provider-APIs (`loadModelsFromProviders`). Das ursprünglich in `TODO.md` vorgeschlagene Shortcode-Schema (`p1m1c0` — Position in einer bei jedem Boot neu sortierten Liste) wäre bei jeder Änderung dieser Liste instabil: ein Provider-Ausfall, ein neues Modell oder die bereits dokumentierte Fallback-Asymmetrie (Mammoth/Moonshot liefern bei Fehler 0 Modelle, ZAI/Longcat eine statische Liste) verschiebt alle nachfolgenden Positionsindizes. Ein Shortcode, der heute Modell A meint, könnte nach einem Neustart Modell B meinen — ohne Fehler, einfach falsch.

Dieses Design ersetzt Positions-Encoding durch eine **persistente SQLite-Registry** (`id_registry.db`, gleiches Muster wie `costs.db`): Provider- und Modell-Kürzel werden beim ersten Auftreten einmalig vergeben (assign-once) und nie wiederverwendet. Der Shortcode bleibt lesbar (zeigt Provider + Modell), garantiert kurz (3-stelliges Provider-Kürzel + bestehender semantischer Modell-Code) und überlebt Boot-zu-Boot-Schwankungen der Live-Modell-Listen. Modelle, die dauerhaft aus dem Angebot eines Providers verschwinden, werden als `retired` markiert statt gelöscht oder wiederverwendet — ein Aufruf mit einem retired Shortcode liefert einen klaren Fehler, niemals ein stillschweigend anderes Modell.

## Motivation

- **Positions-Encoding ist an dynamisches Laden gekoppelt.** Jede Boot-zu-Boot-Änderung der Live-Modell-Liste verschiebt Indizes. Das betrifft nicht nur Provider-Ausfälle, sondern jedes neue Modell, das ein Provider live hinzufügt.
- **Das Problem existiert bereits heute, kleiner.** `GenerateShortcodesBatch` sortiert Modell-IDs alphabetisch und vergibt Kollisions-Suffixe (`-2`, `-3`) in dieser Reihenfolge, bei jedem Boot neu berechnet. Erscheint ein neues Modell, das alphabetisch vor einem bestehenden mit gleichem Basis-Code einsortiert, bekommt das **etablierte** Modell den Suffix — nicht das neue.
- **Langzeit-Anforderung:** Software soll auch 2030 mit heutigen Parametern funktionieren. Das bedeutet nicht nur "der Aufruf funktioniert noch", sondern "der Aufruf ruft garantiert dasselbe Modell wie heute" — ein still umgebogener Shortcode wäre schlimmer als ein Fehler.
- **Aktueller Shortcode zeigt keinen Provider.** `cl5-s` sagt "Claude Sonnet 5", aber nicht ob über Mammouth, Moonshot, ZAI oder cheaperinference bezogen — obwohl dieselbe Modellfamilie über mehrere Provider laufen kann (siehe `ci-`-Präfix-Problem bei cheaperinference).
- **Exponentiell wachsende Modellzahl** (Annahme des Nutzers) braucht ein Schema, das nicht auf eine feste, kleine sortierte Liste angewiesen ist.

## Anforderungen

1. Shortcode kurz und einprägsam, zeigt erkennbar Provider **und** Modell.
2. Einmal vergebener Shortcode ändert sich nie — stabil über Boots, Provider-Ausfälle, neue Modelle hinweg.
3. Ein Modell, das ein Provider dauerhaft nicht mehr anbietet, wird `retired`: Aufruf liefert einen klaren Fehler, **kein** automatischer Fallback auf ein anderes Modell.
4. Kein Namensraum-Engpass bei wachsender Modellzahl über Jahre/Jahrzehnte.
5. Provider-Kürzel 3-stellig (a–z, ca. 17.576 Kombinationen), Modell-Kürzel semantisch (bestehendes `GenerateShortcode`), Kanal-Suffix nur bei Nicht-Default-Kanal.
6. Persistenz via SQLite, gleicher Treiber wie `costs.db` (`modernc.org/sqlite`, pure Go, kein CGO), im selben `-data-dir`.
7. Full-ID-Format (`provider:model:channel`, ursprünglich Abschnitt "ID Vereinfachung") bleibt erhalten, zwei Spezifikationsfehler werden korrigiert (siehe unten) — dieser Teil war bereits namensbasiert und nicht vom Dynamisierungs-Problem betroffen.

## Architektur

```text
Boot: loadModelsFromProviders()
        │
        ▼
  für jeden Provider/jedes Modell:
        │
        ▼
┌───────────────────────────────┐
│   IDRegistry (id_registry.db) │  ← neu, sigoengine/id_registry.go
│  - Provider-Kürzel zuweisen   │
│  - Modell-Kürzel zuweisen     │
│  - Miss-Streak / Retire-Logik │
└───────────────────────────────┘
        │
        ▼
  Shortcode verfügbar (z.B. "zai-glm45")
        │
        ▼
Request: Client ruft Shortcode
        │
        ▼
┌───────────────────────────────┐
│  Resolver prüft Registry       │
│  - aktiv  → normale Weiterleitung
│  - retired → HTTP 410, klare Meldung, kein Fallback
│  - unbekannt → bestehendes Verhalten (404)
└───────────────────────────────┘
```

### Neue Datei

- `sigoengine/id_registry.go` — `IDRegistry`-Typ, Öffnen/Migrieren der DB, Vergabe- und Retire-Logik. Analog zu `costdb.go` aufgebaut (nil-safe Empfänger, `OpenIDRegistry(dataDir)`).

### Berührte Stellen

- `sigoREST/main.go`: `loadModelsFromProviders()` ruft nach jedem Fetch die Registry-Synchronisation auf; Shortcode-Resolution (aktuell: erst ID, dann Shortcode-Scan) prüft zusätzlich `retired_at`.
- `sigoengine/shortcode.go`: `GenerateShortcode()` bleibt unverändert als Baustein für den semantischen Modell-Code-Teil; die Kollisions-Suffix-Vergabe (`used map[string]bool`) wird durch die Registry ersetzt (persistent statt pro Boot neu).
- `sigoengine/provider_id.go`: liefert weiterhin den kanonischen Provider-Namen (`mammouth`, `zai`, ...) als Schlüssel für die Registry; das bestehende 5-Zeichen-`ProviderCode` (für Tabellen/CLI) bleibt unverändert und unabhängig vom neuen 3-Zeichen-Shortcode-Präfix.

## Datenmodell (SQLite-Schema)

```sql
CREATE TABLE providers (
  name        TEXT PRIMARY KEY,     -- kanonisch, z.B. "zai"
  code        TEXT UNIQUE NOT NULL, -- 3-Zeichen-Kürzel, z.B. "zai"
  assigned_at INTEGER NOT NULL      -- Unix-Timestamp
);

CREATE TABLE models (
  provider     TEXT NOT NULL,       -- FK auf providers.name
  upstream_id  TEXT NOT NULL,       -- Original-Modellname vom Provider
  shortcode    TEXT UNIQUE NOT NULL, -- z.B. "zai-glm45" oder "zai-glm45-2"
  assigned_at  INTEGER NOT NULL,
  retired_at   INTEGER,             -- NULL = aktiv
  miss_streak  INTEGER NOT NULL DEFAULT 0, -- aufeinanderfolgende erfolgreiche Fetches ohne dieses Modell
  PRIMARY KEY (provider, upstream_id)
);
```

`UNIQUE` auf `shortcode` verhindert Doppelvergabe atomar (kein Read-Modify-Write-Race zwischen parallelen Boots/Prozessen). Lookup ist indiziert statt bei jedem Boot ein komplettes File zu parsen; kein Rewrite der gesamten Datei bei jedem neuen Modell — skaliert auf zehntausende Einträge über Jahre.

## Vergabe-Algorithmus (assign-once)

**Provider** (bei jedem Boot, pro entdecktem Provider):
1. Existiert `providers.name` bereits → Kürzel unverändert übernehmen.
2. Sonst: Kandidat = erste 3 Buchstaben des kanonischen Namens, lowercase. Bei Kollision mit bestehendem `code` → bestehende Cutter-Sanborn-Fallback-Logik (`cutterCode`, schon für Modellvarianten im Code) statt reinem Abschneiden. `INSERT`.

**Modell** (pro entdecktem `(provider, upstream_id)`):
1. Existiert der Eintrag bereits **und ist aktiv** (`retired_at IS NULL`) → Shortcode unverändert übernehmen, `miss_streak = 0`.
2. Existiert der Eintrag bereits **und ist retired** → Modell ist zurückgekehrt (identischer `upstream_id`, also garantiert dasselbe Modell, kein Fremd-Zugriff auf einen alten Code): `retired_at = NULL`, `miss_streak = 0`, Shortcode bleibt unverändert.
3. Existiert der Eintrag nicht → semantischen Code via `GenerateShortcode(upstream_id)` berechnen, `shortcode = provider.code + "-" + semanticCode`. Bei `UNIQUE`-Kollision numerischen Suffix anhängen (`-2`, `-3`, ...) bis frei. `INSERT` mit `miss_streak = 0`, `retired_at = NULL`.

**Retire-Erkennung** (nach jedem Fetch-Zyklus):
- Provider-Fetch **erfolgreich** (kein Fehler) und ein zuvor bekanntes Modell fehlt in der aktuellen Liste → `miss_streak += 1`. Erreicht `miss_streak` einen Schwellwert (Vorschlag: 3 aufeinanderfolgende erfolgreiche Fetches ohne dieses Modell) → `retired_at = now`.
- Provider-Fetch **fehlgeschlagen** (Netzwerkfehler, 0 Modelle zurück) → `miss_streak` für alle Modelle dieses Providers **unverändert** lassen. Schützt vor Fehlalarm durch die dokumentierte Fallback-Asymmetrie.

**Bekannte Grenze:** `FetchZAIModels`/`FetchLongcatModels` liefern bei Fehler die statische Modell-Liste **ohne** Fehler-Rückgabe (`return zaiStaticModels, nil`). Aus Sicht der Registry ist das nicht von einem echten vollständigen Erfolg unterscheidbar — ein Live-only-Modell (nicht in der statischen Liste), das während eines echten ZAI-Ausfalls fehlt, zählt fälschlich als Miss. Diese Spec löst das nicht; mögliche spätere Erweiterung: Fetcher geben ein explizites `degraded bool` statt nur `error` zurück.

## Shortcode-Format

`{provider3}-{semanticModelCode}[-{channel}]`

- `provider3`: 3-Zeichen-Kürzel aus der Registry, assign-once.
- `semanticModelCode`: bestehender `GenerateShortcode`-Output (Familie+Version+Variante), z.B. `glm45`, `cl5-s`.
- `channel`: weggelassen bei Default-Kanal (0), sonst als Zahl angehängt.

Beispiele: `zai-glm45` (Default-Kanal), `zai-glm45-2` (Kanal 2), `mam-cl5-s`.

## Full-ID-Format — Korrekturen

Der ursprüngliche Abschnitt "ID Vereinfachung" (`provider:model:channel`, z.B. `omnir:cl-haiku45:0:`) bleibt inhaltlich bestehen — er ist bereits namensbasiert, nicht positionsbasiert, und daher nicht vom Dynamisierungs-Problem betroffen. Zwei Fehler im ursprünglichen Beispiel werden korrigiert:

1. **Zeichensatz-Klarstellung:** Das Beispiel `cl-haiku45` enthält einen Bindestrich, obwohl die Regel nur Großbuchstaben, Zahlen, `:` und `_` erlaubte. Da Modell-Upstream-IDs Bindestriche routinemäßig enthalten (`gpt-4o`, `glm-4.5`) und der bestehende `GenerateShortcode` sie strukturell als Trenner nutzt, wird der Bindestrich explizit als zulässiges Zeichen **im Modell-Feld** ergänzt (Provider- und Kanal-Feld bleiben strikt `A-Z0-9`).
2. **Padding-Beispiel-Fehler:** `sonnet5` (7 Zeichen) auf 10 Zeichen aufgefüllt ergibt `sonnet5___` (3 Unterstriche), nicht `sonnet5__` (2 Unterstriche, nur 9 Zeichen gesamt).

## Fehlerverhalten bei Retired-Modellen

Anfrage mit einem Shortcode/ID, dessen Registry-Eintrag `retired_at IS NOT NULL` ist → **HTTP 410 Gone** (Ressource existierte, ist dauerhaft nicht mehr verfügbar — passender Status als 404, das "nie existiert" bedeutet). Fehlermeldung nennt Modell, Provider und Retire-Zeitpunkt, macht explizit, dass kein automatischer Fallback stattfindet. Genaues Wire-Format (OpenAI-JSON vs. Anthropic-Bridge-Format je nach Endpoint) ist Sache des Implementierungsplans.

## Out of Scope

- **Abschnitt "Fallback"** aus `TODO.md` (Fallback-Provider pro Zugang) — eigenes Thema, separat zu brainstormen.
- **Migration bestehender Shortcodes:** Das neue Format ändert bestehende Shortcodes (Provider-Präfix kommt neu dazu, z.B. `cl46-s` → `mam-cl46-s`). Referenzen in Doku, Clients und ggf. Session-Dateinamen müssen aktualisiert werden. Eine Übergangsfrist/Kompatibilitätsschicht ist nicht Teil dieser Spec.
- **Fetcher-seitige Unterscheidung von "echtem Erfolg" vs. "Fehler-Fallback-Liste"** bei ZAI/Longcat (siehe bekannte Grenze oben).

## Testing & Verifikation (Ausblick für Implementierungsplan)

TDD-Vorgehen, konkrete Tests u.a.:
- Zweimaliges Entdecken desselben `(provider, upstream_id)` → identischer Shortcode.
- Kollidierender semantischer Code → numerischer Suffix, danach stabil.
- Modell fehlt N aufeinanderfolgende erfolgreiche Fetches → `retired_at` gesetzt.
- Modell fehlt bei einem fehlgeschlagenen Fetch → `miss_streak` unverändert.
- Retired Modell taucht wieder auf → `retired_at` zurückgesetzt, Shortcode unverändert.
- Anfrage auf retired Shortcode → HTTP 410, kein Fallback auf anderes Modell.
- Leere DB beim ersten Boot → korrekte Erstbefüllung aus Live-Modell-Liste.

## Nächste Schritte

Nach Freigabe dieser Spec: `writing-plans`-Skill für den Datei-für-Datei-Implementierungsplan (TDD, analog zum bestehenden `rate_limiter.go`/`costdb.go`-Vorgehen).
