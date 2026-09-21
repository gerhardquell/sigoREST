# TODO 20260920 - Vereinfachter API-Zugang

\plan
\brainstorming

## ID Vereinfachung

lass uns die ID komplett ändern. In zukunft soll das Format der api-id sein:
 "provider:model:channe" ==>  "omnir:cl-haiku45:0:"
- provider => 5stelliger Providercode,
  wichtig: zai wird zu zai00 oder zai__  beides erlaubt (5stellig)
- model => 10stelliger Modellcode, wie sonnet5 -> sonnet5___
- channel => 0==default und >0 die Nummer des channels

Alle ID-Codes bestehen aus Großbuchstaben und Zahlen und : und _ ;
im Modell-Feld zusätzlich `-` erlaubt (Upstream-Modellnamen wie
"gpt-4o"/"glm-4.5" enthalten routinemäßig Bindestriche).

Beispiel
```python
st="omnir:cl-haiku45:0:"
st=st.upper()
prv = st[:5]    # omnir
mod = st[6:16]  # cl-haiku45
chn = st[17:18] # 0
```

---

## Shortcode

**Erledigt, siehe `docs/superpowers/specs/2026-09-20-id-shortcode-registry-design.md`
und `docs/superpowers/plans/2026-09-20-id-shortcode-registry.md`.**

Ursprünglicher Positions-Vorschlag (`p1m1c0`, Position in sortierter
Provider-/Modell-Liste) verworfen: sigoREST lädt Modelle dynamisch bei
jedem Boot, eine Positions-Nummer verschiebt sich bei jeder Änderung der
Live-Liste. Ersetzt durch eine persistente SQLite-Registry mit
assign-once Provider-/Modell-Kürzeln (Format `{provider3}-{semanticCode}
[-{channel}]`, z.B. `zai-glm45-2`) — siehe Spec für Details.

**Achtung beim Upgrade (Breaking Change für Clients):** Beim ersten Boot
mit dieser Version vergibt die Registry alle Shortcodes einmalig neu. Sie
unterscheiden sich praktisch immer von den bisherigen, pro Boot
berechneten: aus `glm46` wird `zai-glm46`, aus `cl45-s` wird `mam-cl45-s`.
Es gibt bewusst keine Kompatibilitätsschicht und keine Alias-Tabelle für
die alten Kürzel. Jeder Client, der Shortcodes fest verdrahtet hat
(Skripte, `ANTHROPIC_BASE_URL`-Konfigurationen, der C++-Client), muss nach
dem Upgrade einmal `/api/shortcodes` neu abrufen. Ab dann sind die Kürzel
über Boots und Provider-Listen-Änderungen hinweg stabil — genau das ist
der Zweck der Registry.

---

## Fallback

Für jeden Zugang soll es einen Fallback-Provider geben können. 




