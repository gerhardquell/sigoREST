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
```

---

## Fallback

Für jeden Zugang soll es einen Fallback-Provider geben können. 




