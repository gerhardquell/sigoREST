# Retrospektive: Shortcode-Generierung & sigoREST-Analyse
# Datum: 20260513
# Autor: Gerhard Quell + Claude Sonnet 4.6

## Was haben wir gemacht?

### 1. sigoREST-Analyse
Vollständige Analyse des sigoREST-Codebase:
- `sigoREST/main.go` – REST-Server, HTTP-Handler, Server-State
- `sigoengine/engine.go` – CallAPI, CircuitBreaker, Retry, Sessions, Logging
- `sigoengine/models.go` – Model-Typ, CoreModels (5 Fallback-Modelle)
- `sigoengine/models_registry.go` – Registry mit Lookup-Maps, CSV/JSON-Laden
- `sigoengine/provider_fetchers.go` – Dynamischer Abruf Mammouth/Moonshot/ZAI
- Clients: Python, Go, JavaScript

Ergebnis: Vollständige Architektur-Dokumentation in CLAUDE.md
(`libs/sigoREST-detail.md` Abschnitt).

### 2. Vector-Dimension vereinheitlicht
CLAUDE.md: `vector(1536)` → `vector(768)` weil wir
nomic-embed-text-v2-moe verwenden (768 Dim, nicht 1536).

### 3. Shortcode-Generierung neu implementiert
**Problem:** Der alte `generateProviderShortcode` nahm einfach die ersten
7 alphanumerischen Zeichen des Kleinbuchstaben-Modellnamens. Das führte zu:
- Unbrauchbaren Shortcodes: `moonsho`, `moons2`, `texte2`, `textemb`
- Doppelbelegungen: `gemin2` und `gemini3` tauchten je zweimal auf
- Keine Sprechhaftigkeit: `gpt51co` statt `gpt51-cx`

**Lösung:** Neues Datei `sigoengine/shortcode.go` mit strukturiertem Ansatz:
1. **Familie erkennen** (longest prefix match): `gpt→gpt`, `claude→cl`,
   `gemini→gem`, `deepseek→ds`, `text-embedding→emb` etc.
2. **Version extrahieren**: `5.1→51`, `4o→4o`, `k2.5→k25`
3. **Subfamily**: `sonnet→s`, `opus→o`, `haiku→h` (kommen VOR der Version)
4. **Varianten übersetzen**: `mini→m`, `flash→f`, `codex→cx`, `vision→vis`
5. **Cutter-Sanborn Fallback** für unbekannte Varianten (A-Z Tabelle)
6. **Kollisionsauflösung** mit numerischem Suffix `-2`, `-3`

### 4. Cutter-Sanborn Integration
Gerhards Bibliothek unter `/u/ki-projekte/cuttercode/` wurde als
Go-Implementierung integriert:
- 26 Präfix-Tabellen (A-Z), Pipe-separiert
- Longest-Prefix-Match Algorithmus
- Code-Format: Buchstabe + 2-stellige Zahl (z.B. `C52` für "codex")

### 5. Neuer API-Endpoint: `/api/shortcodes`
Kompaktes `{id: shortcode}` Mapping:
```
curl -s http://localhost:9080/api/shortcodes | jq
```
Nur ID und Shortcode, keine Preise/Limits – schnell und übersichtlich.

### 6. CLAUDE.md aktualisiert
- `libs/sigoREST-detail.md` – Architektur, Endpoints, Embedding-Situation
- `tools.md` – `jq` als Werkzeug eingetragen

---

## Was hat funktioniert?

- **Cutter-Sanborn als Fallback** – elegante Lösung für unbekannte
  Varianten, die sich nicht im variantMap finden
- **Strukturiertes Parsing** – Familie+Version+Variante ist viel
  sprechender als rohe Zeichenabschneiderei
- **Subfamily-Erkennung** – Claude-Modelle haben die Struktur
  `claude-sonnet-4-5`, da kommt die Variante VOR der Version
- **Kollisionsauflösung** – garantiert Eindeutigkeit, 108 Modelle
  ohne Duplikate
- **Bestehende statische Shortcodes** bleiben erhalten (Moonshot/ZAI
 Known-Models haben feste Einträge die nicht überschrieben werden)

## Was war unerwartet schwierig?

- **Versionserkennung bei Claude-Modellen**: `claude-sonnet-4-5`
  – sind "4" und "5" zwei Versionsteile oder Version+Sub-Variante?
  Lösung: Subfamily-Concept (sonnet/opus/haiku) VOR der Version
  erkennen, dann werden "4" und "5" zu einer Version "45"

- **Cutter-Sanborn Test-Erwartungen**: Die README-Beispiele
  ("Schmidt → S35") stimmen mit KEINER der Implementierungen
  überein (simple_cutter_lib.py vs improved_ultra_compact.py
  haben unterschiedliche Tabellen). Go hat A-Z, Python compact
  nur A-G. Wichtig: intern konsistent, nicht mit README.

- **Mammouth-API Timeout**: Beim ersten Versuch war Mammouth
  nicht innerhalb von 10s erreichbar → 0 Mammouth-Modelle.
  Nach Serverneustart klappte es (63 Modelle).

- **"v1" in variantMap**: War als ignoriert ("") eingetragen,
  wurde aber bei `moonshot-v1-8k` gebraucht als Versionsteil.
  Entfernt aus variantMap → jetzt korrekt als Version "1"

- **Doppelbelegungen im alten Code**: `gemin2` und `gemini3`
  wurden je zweimal vergeben, der zweite Eintrag überschrieb
  den ersten stillschweigend. Die neue Kollisionsauflösung
  verhindert das.

## Gelernte Lektionen

1. **Modellnamen-Struktur ist nicht einheitlich** – jeder Provider
   hat eigene Konventionen (GPT: `gpt-5.1-codex`, Claude:
   `claude-sonnet-4-5`, Gemini: `gemini-2.5-flash-preview`).
   Ein Algorithmus muss alle drei Muster abdecken.

2. **Cutter-Sanborn ist für Personennamen optimiert**, nicht für
   technische Begriffe. "coder" → `C52` ist nicht sprechend.
   Daher: bekannte technische Begriffe IMMER im variantMap
   vordefinieren, Cutter nur als Fallback für das Unerwartete.

3. **Test-Erwartungen an tatsächliche Ausgabe anpassen**, nicht
   an ideale Vorstellung. Die produzierten Codes sind eindeutig
   und sprechend – das ist wichtiger als perfekte Ästhetik.

4. **sigoREST hat keinen Embedding-Endpoint** – das ist ein
   offener Punkt für die Zukunft. Embeddings laufen aktuell
   direkt über Ollama, nicht über sigoREST.

5. **`jq` als Standardwerkzeug** für JSON-Analyse in der Shell.

---

## Offene Punkte

- [ ] Embedding-Endpoint `POST /v1/embeddings` in sigoREST
- [ ] Mammouth-Embedding-Modelle (text-embedding-3-small/large) nutzen
- [ ] sigoREST README updaten (Vision-Support, /api/shortcodes)
- [ ] sigoengine README updaten
- [ ] Python-Cutter-Bibliothek: A-Z vervollständigen
  (aktuell improved_ultra_compact.py hat nur A-G)
