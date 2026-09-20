# TODO 20260920 - Vereinfachter API-Zugang

\plan
\brainstorming

## ID Vereinfachung 

lass uns die ID komplett ändern. In zukunft soll das Format der api-id sein:
 "provider:model:channe" ==>  "omnir:cl-haiku45:0:"
- provider => 5stelliger Providercode, 
  wichtig: zai wird zu zai00 oder zai__  beides erlaubt (5stellig)
- model => 10stelliger Modellcode, wie sonnet5 -> sonnet5__
- channel => 0==default und >0 die Nummer des channels

Alle ID-Codes bestehen aus Großbuchstaben und Zahlen und : und _

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

Der Shortcode darf maximal 10 stellig sein! Nur alphanumerische Zeichen aus:
a-z0-9 , A-Z wird zu a-z , also tolower
"omnir:cl-haiku45:0" wird zu "p1m1c0":
p1 = 1.Provider bei sortierter Liste der Provider 
m1 = 1.Modell bei sortierter liste der Modelle des Providers!
c0 = Channel, 0 == default

---

## Fallback

Für jeden Zugang soll es einen Fallback-Provider geben können. 




