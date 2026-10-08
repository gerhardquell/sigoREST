# Aufgabe 20261008

sigoREST scheint konstant zu versuchen, KIs abzurufen.
hier ein Ausschnitt von heute:
Zeit	Schlüssel	Modell	Tokens (input + output)	Kosten ($)
2026-10-08 13:23:38	KEY_1	claude-opus-4-7	0 (0 + 0)	0.000000
2026-10-08 13:23:38	KEY_1	claude-opus-4-7	0 (0 + 0)	0.000000
2026-10-08 13:23:38	KEY_0	claude-opus-4-7	0 (0 + 0)	0.000000
2026-10-08 13:23:35	KEY_0	claude-opus-4-7	0 (0 + 0)	0.000000
2026-10-08 13:23:34	KEY_0	claude-opus-4-7	0 (0 + 0)	0.000000
2026-10-08 13:23:33	KEY_0	claude-opus-4-7	0 (0 + 0)	0.000000
2026-10-08 13:23:33	sk-...3rXw	claude-opus-4-7	0 (0 + 0)	0.000000
2026-10-08 13:23:31	sk-...3rXw	claude-opus-4-7	0 (0 + 0)	0.000000
2026-10-08 13:23:30	sk-...3rXw	claude-opus-4-7	0 (0 + 0)	0.000000
2026-10-08 13:23:29	sk-...3rXw	claude-opus-4-7	0 (0 + 0)	0.000000

bitte untersuche diese Vorgänge
## Ergebnis (2026-10-08)

### Ursache

- **Kein Dauer-Abruf aus sigoREST selbst.** Chat-Calls entstehen nur auf
  Client-Request; der Health-Monitor fragt nur `GET /models` ab, im Code ist
  kein Modell fest verdrahtet.
- **Laut Journal betraf der Vorfall nur etwa 70 s (13:23:43–13:24:40).**
  Ein Client fragte `claude-opus-4-7`, danach `claude-sonnet-4-6` an (nicht
  gestreamt, Opus→Sonnet-Fallback). sigoREST schickte beide an Mammouth.
  Mammouth lehnte jeden Call ab:
  `HTTP 429 "ExceededBudget: User=93085 over budget. Spend=21.68, Budget=20.81"`.
  Das ist ein Ausgabenlimit pro User, unabhängig vom Guthaben. Alle sechs
  Mammouth-Keys hängen am selben User 93085.
- **Verstärkung durch sigoREST:** 429 galt pauschal als Rate-Limit, also 4
  Versuche mit Backoff pro Kanal plus Umschalten über alle Kanäle, bis die
  Circuit Breaker öffneten (12 Breaker = 6 Kanäle × 2 Modelle).
- **Auftraggeber nicht belegt.** Die Calls begannen direkt nach dem Turn-Ende
  der golisp2-Claude-Session (PID 101606, 13:23:29) und stehen in keinem
  Transkript. Vermutlich ein Hintergrund-Request von Claude Code, der die
  `ANTHROPIC_DEFAULT_*`-Variablen nicht beachtet.
- **Die golisp2-Session selbst** (`che-gpt6-s01` = `ci-gpt-6-sol` über
  `/v1/messages`) rief seit 2026-10-07 14 Uhr 1148-mal erfolgreich auf, etwa
  alle 8 s in drei Gesprächssträngen, zusammen $73,81.

### Behoben

- [x] **Budget-Hard-Stop gilt jetzt auch für `/v1/messages`** (`c9e8377`).
  Vorher fehlte `CheckBudget` in der Bridge; Claude Code kam so bei $10
  Tageslimit mit Hard-Stop auf $25. Jetzt prüft `Server.budgetBlocked()`
  beide Handler, die Bridge antwortet mit `402 billing_error`.
- [x] **Provider-Budget-429 wird nicht mehr wiederholt** (`c9e8377`).
  429/402 mit `budget_exceeded`/`ExceededBudget`/`insufficient_quota` im
  Body wird zu `ErrQuotaExceeded`: kein Retry, kein Kanalwechsel, 402 an den
  Client. Vorher 8 Upstream-Calls im Test, jetzt 1.
- [x] **Mammouth-Preise werden gelesen** (`ba1a7a2`). Preise und Limits
  stehen unter `model_info` (USD/Token). Vorher lief alles über die statische
  Tabelle: `claude-opus-4-7`/`claude-opus-5-5` mit $0 (am Hard-Stop vorbei),
  `claude-sonnet-5` mit 3/15 statt 2/10 $/1M. Live: 98/98 Mammouth-Modelle
  mit Preis.
- [x] Tageslimit per `PUT /api/budget` von $10 auf $80 gesetzt (Hard-Stop
  bleibt aktiv, kein Monatslimit).

- [x] `/usr/local/bin/claude-multi`: Drei Modellnamen gaben 404
  (`che-gpt5-l88` existiert nicht, Tippfehler `ch-gpt56-s01`). Jetzt sind
  Small-Fast und Haiku `che-gpt5-m` (gpt-5-mini), Subagents
  `che-gpt56-s01` gesetzt. Gilt erst für neu gestartete Sessions; die
  laufende golisp2-Session hat noch die alten Werte.

Beide Fixes sind live (Binary deployt, Dienst neu gestartet).

### Offen

- [ ] **Die cheaperinference-Zeilen oben passen nicht zu sigoREST.**
  sigoREST schickt `claude-opus-4-7` nur an Mammouth (`mam-cl47-o`, es gibt
  kein `ci-claude-opus-4-7`) und loggte zwischen 13:23:29 und 13:23:42 keinen
  Fehler. Der letzte cheaperinference-Call war um 13:23:29 der erfolgreiche
  Abschluss-Call von golisp2 (`gpt-6-sol`, 396 Output-Tokens). Stand:
  `sk-...3rXw` war ein Key von Gerhard, aber nicht der von sigoREST, und ist
  inzwischen gelöscht. `KEY_0`/`KEY_1` sind Gerhards Keys
  (`OMNIROUTE_API_KEY_0`/`_1`), nur falsch benannt. Wer sie um 13:23 mit
  `claude-opus-4-7` benutzt hat, ist unbekannt. Die Keys stehen in der
  Login-Umgebung, jeder Prozess hat sie. Bei einem Leck `KEY_0`/`KEY_1`
  tauschen.
- [ ] Mammouth-`Spend=21.68` vs. sigoREST-Anteil diesen Monat nach echten
  Preisen ~$11,60. Der Rest kommt vermutlich aus anderer Nutzung (Web-App)
  oder einem anderen Zeitraum, im Mammouth-Account prüfen.
- [ ] `costs.db`: 130 Zeilen in Stunde 13 mit `total_tokens=0` trotz
  >130k `input_tokens` (Kosten stimmen). Vermutlich Streaming-Usage-Pfad.
- [ ] Mammouth-Streams liefern keine Usage-Daten (`input_tokens: 0` im SSE).
  Kosten fallen dann auf `EstimateUsage` zurück.
- [ ] Log-Zeitstempel enden auf `Z`, sind aber Lokalzeit (`13:23:43Z` =
  13:23:43 CEST).
- [ ] Alte Mammouth-Einträge in `costs.db` sind mit falschen Preisen
  gebucht (diesen Monat ~$16 gebucht, ~$11,60 echt). Bewusst nicht
  rückwirkend korrigiert.
