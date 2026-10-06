# aufgabe 20261006 - openrouter eintragen [ERLEDIGT 2026-10-06]

Wir können jetzt openrouter mit einbinden. Die API_KEYs + Channels dazu sind schon 
in .env eingetragen
- OPENROUTER_API_KEY  --> default
- OPENROUTER_API_KEY_0 .. OPENROUTER_API_KEY_4 --> Channels

OpenRouter als sechster Provider ausgerollt (Fetcher, Channel-Discovery,
Provider-Erkennung, Health-Endpoint, Shortcode-Vendor-Präfix-Fix, CLI-Bug
nebenbei gefixt — Details in CLAUDE.md "Dynamisches Modell-Laden" und
"Provider-Kennzeichnung"). Echter End-to-End-Test gegen Testserver
(Port 19080) erfolgreich: Chat-Call per ID und Shortcode, Kosten-Tracking
korrekt.

Nachgezogen, noch am selben Tag: `:`-Suffixe (`:batch`/`:free`/...) wurden
von `GenerateShortcode` nicht erkannt (Split nur auf `-`) — gefixt. Beim
echten Chat-Call aufgefallen, dass `:batch`-Modelle über
`/chat/completions` generell mit HTTP 404 scheitern (eigene Async-Batch-
API bei OpenRouter, anderer Adapter) — werden seither beim Fetch
komplett ausgefiltert (73 von ursprünglich 449). Live auf dem
Produktions-Dienst verifiziert nach manuellem `id_registry.db`-Reset
(OpenRouter hatte noch keine genutzten Shortcodes, Reset unkritisch):
376 Modelle, keine `:batch`-Reste, `:free` normal nutzbar.


## Überlegung: Anzahl gleicher Systeme
Wir verfügen jetzt über eine sehr große Anzahl von KIs. Davon sind viele rendundant
vorhanden. Hier wäre z.B. ein Preisvergleich sinnvoll. Oder aber auch eine Fallback-
Kaskade.

## Nutzung des jev-Modells
Im Verzeichnis ./docs findest du die Datei jev-modell.md als Info. Was meinst Du, 
könnten wir diese Methodik für unser sigoREST nutzen? Z.B. das wir einen Pool von 
bevorzugten KIs aufbauen, deren Nutzung mit jev optimieren? Oder fallen dir noch
anderes Anwendungen ein?
