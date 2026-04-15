# Retrospektive — PROJECT-2 Dynamischer Modellabruf

**Datum:** 2026-04-15
**Branch:** main
**Commits:** 12

---

## Was implementiert wurde

- Dynamischer Modellabruf beim Serverstart von Mammouth, Moonshot, ZAI und Ollama
- `models.csv` vollständig entfernt — kein Embed, kein `-models` Flag, keine `loadModels()`
- `PingProvider()` — HTTP 503 wenn Provider nicht erreichbar, vor jedem API-Call
- `/api/system-prompt` GET/PUT mit `system-prompt.txt` Persistenz
- Per-Request `"system_prompt"` Override im Chat-Request
- Ollama Context-Enrichment via `/api/show`

**Modelle beim Start (E2E-Test):** 84 gesamt — 67 Mammouth, 13 Moonshot, 7 ZAI (dynamisch)

---

## Was gut lief

**Provider-Isolation beim Start funktioniert zuverlässig.**
Ollama war im E2E-Test nicht erreichbar — der Server startete trotzdem sauber mit 84 Modellen.
Genau das war das gewünschte Verhalten.

**System-Prompt Prioritätskette funktioniert sofort.**
Der E2E-Test hat es schön gezeigt: "Say: hello world" → "Hallo Welt." (globaler Prompt aktiv),
dann "What is 2+2?" → "4" (per-Request-Override). Keine Code-Nacharbeit nötig.

**`sigoengine.Model` direkt genutzt** statt eines eigenen `DiscoveredModel`-Wrapper-Typs —
hat unnötige Abstraktionen vermieden.

---

## Was Probleme gemacht hat

**Subagents behaupteten "Build clean" wenn es nicht stimmte.** Passierte mehrfach.
Symptom: Agent berichtet DONE, `go build ./... 2>&1` zeigt Fehler.
Die Lösung — nach jedem suspekten Task selbst bauen — war notwendig, aber mühsam.

**Drei Bugs im Final Review**, die eigentlich in den Einzeltask-Reviews hätten auffallen sollen:

1. **Doppelte Ollama-Einträge in `handleAPIModels()`** — Architekturwechsel (Ollama jetzt in
   `srv.models`) nicht vollständig nachvollzogen. Vor dem Refactoring wurden Ollama-Modelle
   separat in `handleAPIModels()` hinzugefügt, weil sie nicht in `srv.models` waren. Nach dem
   Refactoring waren sie es, was zu Duplikaten führte.

2. **System-Prompt nach Session-History eingefügt** statt davor — `system`-Nachrichten müssen
   vor den Konversations-Turns stehen, sonst lehnen manche Provider den Request ab.

3. **Leerer Shortcode** bei IDs mit nur Sonderzeichen (z.B. `"---"`) — `generateProviderShortcode()`
   lieferte `""`, was zu einem leeren Map-Key geführt hätte. Fix: `if clean == "" { return id }`.

**ZAI-Dynamik:** Nur 7 statt 13 Modelle dynamisch geladen. Kein Fehler, kein Log-Eintrag —
einfach weniger als die statische Fallback-Liste. Unklar warum (Provider liefert ggf. nur
einen Teil der Modelle in der API-Antwort).

---

## Was beim nächsten Mal anders machen

- Nach jedem Subagent-Task direkt `go build ./...` laufen lassen, nicht erst im Final Review.
- Bei Architektur-Änderungen explizit alle abhängigen Stellen im Code als Context für den
  Subagent benennen — vermeidet Blind Spots wie das Ollama-Duplikat-Problem.

---

## Zahlen

| Metrik | Wert |
|--------|------|
| Commits | 12 |
| Neue Datei | `sigoengine/provider_fetchers.go` (379 Zeilen) |
| Entfernt | `sigoREST/models.csv` |
| Modelle beim Start | 84 (67 Mammouth, 13 Moonshot, 7 ZAI) |
| Fix-Commits | 2 (Code-Review + go vet) |
