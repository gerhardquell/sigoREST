# sigoREST — Retrospektiven

Dieses Dokument enthält detaillierte Historie vergangener Entwicklungssessions.

---

## Session 2026-09-27: TODO-Nacharbeiten (.env, CSV-Export, Kommunikationsprotokoll) + zwei Altbugs

**Zielsetzung:**
Drei kleinere Punkte aus `TODO.md` (20260927) umsetzen: die Env-Datei von `env` auf `.env` umstellen, die Modellliste als CSV ausgeben und ein per CLI-Flag einschaltbares Protokoll der externen Kommunikation einführen.

**Was erreicht wurde:**

### 1. `.env` statt `env` (`01ef941`)
`ResolveEnvFile` wählt die Datei als reine Funktion (nur `os.Stat`, ohne globalen Zustand), `LoadDefaultEnvFile` lädt sie. Eine veraltete `env` wird noch geladen, aber mit Warnung. Liegen beide vor, gewinnt `.env` und es wird ebenfalls gewarnt. Die Warnung wird als String zurückgegeben und erst nach `SetLogLevel`/`SetQuietMode` geloggt, weil beide `main()` die Env-Datei vor der Log-Konfiguration laden. Sie nennt den absoluten Pfad, weil unter systemd sonst unklar bleibt, welches Arbeitsverzeichnis gemeint ist.

**Live-Zwischenfall:** Gerhard hatte `/usr/local/slib/sigoREST/env` während der Session in `.env` umbenannt. Die Unit zeigte aber noch mit `EnvironmentFile=` auf `env`. systemd liest diese Datei selbst, bevor der Prozess startet, und bricht bei fehlender Datei ohne `-`-Präfix ab (`Failed with result 'resources'`). Der Dienst lief in eine Neustart-Schleife (54 Restarts). Das war vorher angekündigt; der Fix bestand darin, `EnvironmentFile` in der Unit umzustellen und `daemon-reload` auszuführen.

### 2. CSV-Export `GET /api/models?format=csv` (`298f9f5`)
Entscheidung: Server-Endpoint statt CLI-Flag, weil `sigoE -l` nur die lokale Registry kennt und nicht die Live-Modelle mit den Registry-Shortcodes. Das Format ist das bestehende Registry-CSV (Semikolon), damit die Ausgabe als `models.csv` für die CLI wiederverwendbar ist. Die Kopfzeile beginnt mit `#`, weil der Parser `Comment = '#'` setzt; ohne `#` würde sie als Modell `id` eingelesen. Zusatzspalten stören nicht (`FieldsPerRecord = -1`). Der Round-Trip-Test durch den echten Parser zeigte, dass `upstream_id` beim Einlesen verloren ging. Ohne diese Spalte gingen `ci-*`-Modelle mit falschem `model`-Feld an die API, daher liest `parseCSVRecord` sie jetzt als Spalte 14 mit ein.

### 3. Kommunikationsprotokoll `-comm-log` (`c0b9892`)
Entscheidungen von Gerhard: nur Chat- und Embedding-Calls, vollständige Request- und Response-Bodies, JSONL. Die Umsetzung ist ein `http.RoundTripper`. Chat-Calls bekamen einen eigenen `chatHTTPClient`, weil der Provider-Ping denselben `defaultHTTPClient` nutzte und sonst mitprotokolliert worden wäre. Der Response-Body wird beim Lesen mitgeschnitten und der Eintrag einmalig beim `Close()` geschrieben, damit auch SSE-Streams vollständig drinstehen. Gültiges JSON kommt als `json.RawMessage` in den Eintrag; `encoding/json` kompaktiert das beim Marshal, sodass eine Zeile eine Zeile bleibt. Secret-Header werden maskiert, Bodies über 10 MiB gekürzt, die Datei hat `0600` und wird im Append-Modus beschrieben (logrotate mit `copytruncate`). Schlägt das Öffnen fehl, bricht der Start ab. Das ist bewusst anders als bei `costDB`: Ein angefordertes Debug-Protokoll, das unbemerkt fehlt, hilft niemandem.

### 4. Altbug: Ollama-Chat ging nie (`f75956c`)
Beim Testen von Punkt 3 gefunden. Chat läuft über `ChannelManager.Resolve`, aber `DiscoverFromEnv` legt Kanäle nur für Provider mit API-Key an. Ollama hatte deshalb keinen Kanal, und jeder Ollama-Chat (auch `/v1/messages`) endete mit `404 CONFIG_NOT_FOUND`, ohne Ollama je zu erreichen. Live ebenfalls betroffen: `channels.json` enthielt keinen Ollama-Eintrag. Embeddings gingen, weil `/v1/embeddings` keinen Kanal nutzt; das hat den Fehler verdeckt. Gerhards Vorgabe war „keinen Sonderweg“. Der Fix: `AddKeylessChannel`, die Ollama-Discovery läuft jetzt vor der Registry, und `newChannelRegistry` wird von `main()` und vom Test gemeinsam genutzt. Die Reihenfolge ist erst registrieren, dann `LoadState`, damit ein deaktivierter Kanal deaktiviert bleibt. Der Regressionstest schlägt ohne den Fix mit exakt dem Produktionsfehler fehl.

### 5. Altbug: doppeltes `data: [DONE]` (`5500406`)
`streamProviderResponse` reichte das `[DONE]` des Upstreams durch und hängte immer ein eigenes an. Jetzt merkt sich ein `sawDone`-Flag das empfangene `[DONE]`, und der eigene Terminator kommt nur noch, wenn der Upstream keinen geschickt hat. Der Test prüft beide Fälle, damit die Absicherung für Upstreams ohne `[DONE]` nicht verloren geht.

### 6. Reality-Check mit echtem Ollama (gemma4:12b)
Chat, Stream und `/v1/messages` liefern HTTP 200 mit korrekter Antwort. Die Kosten-DB bucht die Calls unter `ollama#ollama-default`, im Protokoll steht eine Zeile pro Call. Der erste Versuch mit `max_tokens` 40–60 brachte leere Antworten (`finish_reason: length`). Das Kommunikationsprotokoll zeigte sofort den Grund: Das Thinking-Modell hatte alle Tokens im `reasoning`-Feld verbraucht. Das Werkzeug aus Punkt 3 hat sich also schon im ersten Einsatz bezahlt gemacht.

**Learnings:**

1. **Zwei Wege zur Datei müssen zusammen umgestellt werden.** In der systemd-Installation wird die Env-Datei doppelt gelesen: von systemd (`EnvironmentFile=`) und von sigoREST selbst (`WorkingDirectory`). Wer nur die Datei umbenennt, legt den Dienst lahm. Das sollte bei jeder Pfadänderung ein Prüfpunkt sein.
2. **Round-Trip-Tests finden, was Formattests übersehen.** Der CSV-Test „Ausgabe durch den echten Parser zurücklesen“ hat den `upstream_id`-Verlust aufgedeckt. Ein Test, der nur Spalten zählt, hätte ihn nicht gefunden.
3. **Ein Pfad, der funktioniert, kann den kaputten verdecken.** Embeddings liefen, also galt „Ollama geht“. Chat nimmt aber einen anderen Weg (Kanal-Manager). Sobald es zwei Wege zum selben Provider gibt, braucht jeder seinen eigenen echten Test.
4. **Regressionstests über den echten Startpfad führen.** Der Ollama-Fehler lag in der Verdrahtung in `main()`. Ein Test, der den Kanal von Hand anlegt, wäre auch vor dem Fix grün gewesen. Deshalb gibt es `newChannelRegistry` als gemeinsame Funktion für `main()` und Test.
5. **Den Fix gegenprüfen:** Den Fix testweise entfernen und zusehen, wie der Test mit dem echten Fehlertext rot wird. Das ist der billigste Nachweis, dass der Test das Richtige prüft.
6. **Beim Ausprobieren gefundene Altlasten nicht nebenbei mitreparieren.** Der Ollama-Kanal und das doppelte `[DONE]` sind erst als Fund gemeldet und dann als eigene, getrennt getestete Commits behoben worden, nicht im Commit von Punkt 3 versteckt.

**Nachtrag (noch am 27. September 2026):** Zwei weitere Funde behoben.
- **Health-Probe für Longcat und cheaperinference** (`b5a9acb`): `modelsEndpointForProvider` kannte nur mammouth, moonshot und zai. Ein Kanal dieser beiden Provider wurde deshalb nie aktiviert, weder eine Reserve noch ein ausgefallener Default. Im Vergleich auf zwei Test-Servern blieben die Kanäle mit dem Binary vor dem Fix tot und erholten sich mit dem neuen. Der Test prüft gegen `knownProviders` (ein Key-Provider ohne Probe-Endpoint wird rot), und die `CLAUDE.md`-Checkliste „Neuen Provider hinzufügen“ ist ergänzt.
- **Fetcher lasen `.env` nicht** (`ce4244a`): Die Modellabrufe nutzten `os.Getenv`, die Kanäle `GetEnvWithFile`. `TestNoDirectOsGetenv` prüft per `go/ast`, dass in `sigoengine` außer `env.go` niemand `os.Getenv` aufruft. Beim E2E-Test zunächst gegen das alte Binary getestet, weil `go build ./...` `build/sigoREST` nicht neu schreibt. Das fiel nur auf, weil der Test das erwartete Ergebnis explizit geprüft hat. **Learning:** Vor jedem E2E-Test gegen `./build/` erst `make sigorest` ausführen.

- **Manuelles `disable` hat Vorrang** (Gerhards Entscheidung): Beim E2E-Test der Probe fiel auf, dass der Health-Monitor einen per API abgeschalteten einzigen Kanal nach einem Intervall zurückholte. Er suchte „irgendeinen inaktiven Kanal“ und fand nach `Order` wieder den gerade abgeschalteten `default`. Das Fehlen einer Probe hatte das bisher für Longcat und cheaperinference verdeckt. Der Fix: `Channel.ManuallyDisabled` (persistiert), `SetActiveManual` für die API, `SetActive` für die Automatik; `runHealthChecks` überspringt manuell deaktivierte Kanäle. Vorgehen in zwei Stufen: erst Datenmodell und Persistenz, sodass die Monitor-Tests fachlich rot wurden (genau das beobachtete Verhalten), dann der Monitor-Fix. Der E2E-Test deckte zusätzlich auf, dass `/api/channels` das Flag nicht zeigte, weil die Ausgabe an zwei Stellen von Hand als Map gebaut wird. Der Test prüft jetzt auch die API-Ausgabe. Vorher hatten schon die Unit-Tests das Verhalten abgedeckt, nicht aber die Sichtbarkeit.
- **Bewusste Grenze:** Bestehende `channels.json`-Einträge haben kein Flag und gelten als nicht manuell deaktiviert. Ein vor dem Upgrade per API abgeschalteter Kanal muss einmal erneut per `/disable` abgeschaltet werden, sonst kann ihn der Monitor weiterhin zurückholen (wie bisher).

- **Aufräumen:** Veraltete, nicht getrackte Binaries außerhalb von `./build/` gelöscht: `sigoREST/sigoREST` (25.09.) sowie `olds/sigoE-binary` und `olds/sigoE/sigoE` (Juni). Auf dem System lag unter `/usr/local/slib/sigoREST/sigoREST` eine identische Kopie des Dienst-Binaries, die keine Unit nutzte (`ExecStart` zeigt auf `/usr/local/sbin/sigoREST`). Gerhard hat sie entfernt. Solche Kopien veralten unbemerkt beim nächsten Update und führen dann in die Irre („welches Binary läuft eigentlich?“).
- **Deployment (13:27):** `sigoREST` nach `/usr/local/sbin`, `sigoE` nach `/usr/local/bin` (MD5 identisch mit `./build/`). Live geprüft: `/api/health` ok mit 188 Modellen, Kanal `ollama-default` vorhanden, `/api/models?format=csv` liefert 200. `longcat-1` bis `longcat-4` sind nicht als manuell deaktiviert markiert. Mit der neuen Probe kann der Health-Monitor sie jetzt als Reserve zuschalten, wenn `longcat-default` ausfällt; vorher war das bei Longcat nicht möglich.

**Ergebnis des Tages:** 3 TODO-Punkte umgesetzt und 6 Altbugs behoben (Ollama-Kanal, doppeltes `[DONE]`, fehlende Probe für Longcat/cheaperinference, Fetcher ohne `.env`, Rückholen eines manuell deaktivierten Kanals, fehlendes Flag in `/api/channels`). Alles mit Regressionstests (136 Tests grün), E2E auf Test-Servern geprüft und live deployt.

**Nächste mögliche Schritte:** siehe `TODO.md`, Abschnitt „Offen“ (`-comm-log` live einschalten, Shortcode-Kuriosum `che-cl-f025`, Fallback-Provider).

**Co-Autor**: Claude Opus 5.5 (Anthropic) — Session vom 27. September 2026.

---

## Session 2026-09-21: Persistente ID-/Shortcode-Registry — Subagent-Driven Development bis zum finalen Review

**Zielsetzung:**
Eine vorherige Session hatte Tasks 1-6 eines 8-Task-Plans (SQLite-Registry für stabile, assign-once Shortcodes) implementiert und mitten im Plan pausiert (Budget-Stop). Diese Session: Tasks 7-8 fertigstellen, finalen Whole-Branch-Review durchführen, gefundene Bugs fixen, nach `main` mergen, pushen.

**Was erreicht wurde:**

### 1. Resume aus Ledger
Das SDD-Ledger (`.superpowers/sdd/2026-09-20-id-shortcode-registry/progress.md`) zeigte exakten Stand: Tasks 1-6 committed und reviewed, Task 7 (Brief bereits vorbereitet) und Task 8 offen. `HEAD` gegen die Ledger-Notiz (`9feabee`) verifiziert — sauberer Resume ohne Re-Arbeit oder verlorenen Kontext.

### 2. Task 7 — Retired-Fehlerpfad + Kanal-Suffix in `lookupModel`
`lookupModel` von `(ModelInfo, string, bool)` auf einen `lookupResult`-Struct umgebaut: Retired Modelle liefern `HTTP 410` (`model_retired` bzw. Anthropic `not_found_error`) statt stillem Fallback auf ein anderes Modell; ein Kanal-Suffix im Shortcode (`zai-glm45-2`) wird aufgelöst und an `channelManager.Resolve` durchgereicht. Der Implementer fand einen Selbstwiderspruch im vorbereiteten Plan-Brief (der Anthropic-Request-Typ hat kein `Channel`-Feld, ein Codeblock im Brief widersprach der eigenen Prosa direkt daneben) und folgte der Prosa — vom Task-Reviewer als einzig kompilierbare Interpretation bestätigt.

### 3. Task 8 — TODO.md-Dokufehler
Reiner Doku-Task: Zeichensatz-/Padding-Fehler in der alten Format-Spezifikation korrigiert, der verworfene Positions-Shortcode-Vorschlag (`p1m1c0`) durch einen Verweis auf die neue Registry-Spec ersetzt.

### 4. Finaler Whole-Branch-Review — zwei echte Bugs, erst hier sichtbar
Alle 8 Einzel-Tasks waren je für sich review-approved. Der finale Review über den gesamten Branch (Opus) fand trotzdem zwei Defekte, die keiner der Einzel-Reviews sehen konnte, weil sie erst im Zusammenspiel mehrerer Tasks entstehen:

- **Critical — Shortcode-Kollisionen bei cheaperinference/longcat:** `AssignModel` berechnete den semantischen Code-Teil selbst neu (`GenerateShortcode(upstreamID, nil)`), ohne die Dedup-Map, die die echten Provider-Fetcher pro Batch mitführen. Für ~60 cheaperinference-Modelle und unbekannte Longcat-Modelle hat `GenerateShortcode` keinen Familien-Präfix-Treffer und fällt auf `cutterCode()` über die *ganze* ID zurück — alle Modelle eines Providers bekamen denselben Code, nur durch einen numerischen Suffix unterscheidbar (`che-c01`, `che-c01.2`, … `.60`). Durch Assign-Once für immer eingefroren — genau das Problem, das die Registry eigentlich lösen sollte.
- **Important — Check-then-INSERT nicht atomar über Prozessgrenzen:** ein UNIQUE-Konflikt riss bislang den kompletten Provider-Sync für diesen Boot ab, statt nur das eine Modell zu überspringen.
- **Important — `lookupModel` hielt `s.mu.RLock()` während einer SQLite-Query:** dieselbe Gefahrenklasse wie ein bereits dokumentierter, bereits gefixter rekursiver-RLock-Bug (siehe unten, "Provider-Kennzeichnung"-Session) — unter Last (~100 parallele Verbindungen, das dokumentierte Server-Ziel) hätte eine langsame Registry-Query den kompletten Server für Leser blockieren können.
- **Important — kein Hinweis auf Breaking Change:** Shortcodes ändern sich beim Upgrade einmalig, `CLAUDE.md` war nicht aktualisiert.

### 5. Eine Fix-Welle, ein skalierter Re-Review
Alle Findings in *einem* Fix-Dispatch (Opus) gebündelt behoben, statt pro Finding einen eigenen Durchlauf zu fahren:
- Semantischer Code kommt jetzt vom Fetcher (`Model.Shortcode` als `SemanticHint` statt Neuberechnung durch die Registry).
- Retry-on-Conflict statt Hard-Fail bei INSERT-Kollision; `SyncProvider` überspringt nur das eine gescheiterte Modell statt den ganzen Provider abzubrechen.
- `lookupModel` in ein selbstsperrendes `lookupModel` (macht die Registry-Query *ohne* gehaltenen Lock) und ein lockfreies `lookupModelMemory` (für Aufrufer, die `s.mu` bereits halten) aufgeteilt — das exakte Design wurde dem Implementer vom Controller vorgegeben (inkl. Beispielcode), um den dokumentierten rekursiven-RLock-Fehler nicht ein zweites Mal einzubauen.
- `CLAUDE.md` + `TODO.md` um die neue Registry, ihre Semantik und die zwei operativen Eigenheiten (Breaking Change beim Upgrade, Retirement zählt Boots statt Fetch-Zyklen) ergänzt.

Der anschließende skalierte Re-Review (Sonnet) verifizierte unabhängig — nicht nur den Implementer-Report gegenlesend, sondern von Hand nachverfolgt —, dass an keiner der elf `lookupModel`-Aufrufstellen ein rekursiver Lock entstehen kann. Ein Punkt blieb bewusst offen: das Longcat-Kollisionsproblem greift auch nach dem Fix noch für *künftige, noch nicht handkuratierte* Longcat-Modelle (der No-Family-Zweig von `GenerateShortcode` liest die Dedup-Map nie) — als dokumentierte, aktuell folgenlose Einschränkung geparkt statt in einer zweiten Fix-Runde nachgejagt.

### 6. Merge + Push
Fast-Forward-Merge nach `main` (`f994343`), volle Test-Suite grün (100 Tests, 5 Pakete), SDD-Workspace + Worktree + Feature-Branch aufgeräumt, nach `origin/main` gepusht.

**Learnings:**

1. **Task-Reviews fangen nicht alles ab — der Whole-Branch-Review ist kein Ritual, er findet echte Bugs.** Beide Critical/Important-Findings entstanden aus dem Zusammenspiel mehrerer, je für sich korrekt reviewter Tasks (Kollisionsauflösung aus Task 2/3 + Boot-Verdrahtung aus Task 6 + reale Provider-ID-Muster, die kein Einzel-Task-Diff je gleichzeitig zeigte).
2. **"Alle Shortcodes eindeutig" ist der falsche Test.** `TestAssignModel_CollisionAppendsNumericSuffix` bestand acht Reviews lang, weil er nur Eindeutigkeit prüfte, nicht Aussagekraft — `che-c01.2` ist eindeutig *und* bedeutungslos. Ein Regressionstest gegen einen realistischen Modell-Batch (mehrere echte cheaperinference-IDs) hätte den Bug sofort gezeigt; genau ein solcher Test wurde Teil der Fix-Welle.
3. **Bekannte Bug-Klassen wiederholen sich, wenn der Fix nicht explizit mitgegeben wird.** Der rekursive-RLock-Bug war in `CLAUDE.md` bereits dokumentiert. Der naheliegendste Fix für Task 7s neuen Lock-Hazard (`lookupModel` selbst sperren lassen) hätte ihn exakt reproduziert, weil `providerForModelLocked` von Aufrufern kommt, die `s.mu` schon halten. Nur weil der Fix-Dispatch das Design vorab im Detail (mit Beispielcode) vorgab statt dem Implementer freie Hand zu lassen, blieb die Falle aus.
4. **Ledger-basiertes Resume funktioniert zuverlässig über Session-Grenzen hinweg.** Budget-Stop mitten im Plan, neue Session, Ledger gelesen, `HEAD` gegen die Ledger-Notiz verifiziert, exakt bei Task 7 weitergemacht — kein Re-Dispatch bereits fertiger Tasks, kein verlorener Kontext.
5. **"Ready to merge? No" ist beim finalen Review ein gutes Zeichen, kein Fehlschlag.** Der Reviewer fand echte, vorher unsichtbare Bugs in Code, der sonst mit stillen Shortcode-Kollisionen und einem Lock-Hazard produktiv gegangen wäre — genau der Zweck des zusätzlichen Reviewschritts nach acht bereits grünen Einzel-Reviews.

**Nächste mögliche Schritte:**
- ~~Longcat-Kollisionsproblem für unbekannte/künftige Modelle richtig lösen~~ — noch am selben Tag nachgezogen, siehe Nachtrag unten.
- Retirement-Kadenz überdenken: aktuell zählt `SyncProvider` nur Boots, auf einem langlaufenden Deployment praktisch träge (3 Neustarts statt 3 Fetch-Zyklen bis zum Retire) — bewusste Scope-Entscheidung, aber wert, im Auge zu behalten.
- `chinese/README.md` (Marketing-Landingpage) ist von dieser Doku-Ergänzung unberührt geblieben — bei Bedarf separat nachziehen.

**Nachtrag (noch am 21. September 2026):** Das oben geparkte Longcat-Kollisionsproblem war ein Bug tiefer im gemeinsamen `GenerateShortcode` selbst, nicht nur in der Registry: der No-Family-Zweig (kein Treffer in `familyPrefixes`, z.B. bei `LongCat-...`-IDs) rief `cutterCode(modelID)` auf und gab sofort zurück — ohne, anders als der Familien-Pfad, die `used`-Dedup-Map zu konsultieren. Da `cutterCode` nur den längsten Tabellen-Präfix zurückgibt statt der ganzen ID, bekamen alle unbekannten Longcat-Modelle denselben Code (`l62`). Fix: Kollisionsauflösung (Schritt 6, das bestehende `-2`/`-3`-Suffix-Schema) in einen `resolveCollision`-Helper ausgelagert und für beide Zweige nutzbar gemacht — dasselbe, bereits akzeptierte Schema, keine neue Ambiguitätsklasse gegenüber dem, was der finale Review schon absegnete. Verifiziert: `LongCat-Flash-Chat/-Thinking/-Video` → `l62`, `l62-2`, `l62-3` statt dreimal `l62`. Neuer Regressionstest `TestGenerateShortcode_NoFamilyMatchStillDeduplicates` in `sigoengine/shortcode_test.go`. Die ursprüngliche Sorge (naheliegender Fix bringt die Kanal-Suffix-Zweideutigkeit zurück) griff hier nicht — Schritt 6 nutzt seit jeher den `-`-Suffix produktiv (jeder familien-basierte Fetcher-Aufruf mit echter `used`-Map), das war schon vor diesem Fix akzeptierter Bestandteil der Fetcher-Shortcode-Erzeugung, nur eben nicht im No-Family-Zweig aktiv.

**Co-Autor**: Claude Sonnet 5 (Anthropic) — Session vom 21. September 2026.

---

## Session 2026-08-18: Pro-Kanal Rate-Limiter (hybrid) + zentralisiertes ./build/

**Zielsetzung:**
sigoREST stoppte Provider-APIs bei zu schnellem Request-Feuer. Gerhards These: gezieltes Bremsen (0,5–1 s zwischen Calls) macht die Gesamtperformance schneller, weil 429-Retrys entfallen. Gesucht war eine pro-Kanal-Lösung, die das bestehende Multi-Channel-Failover nicht ausbremst.

**Was erreicht wurde:**

### 1. Brainstorming — drei Designentscheidungen vorab

Klassifiziert als *bounded* (bestehender `handleChatCompletions`-Flow wird erweitert). Drei Entscheidungen durch Gerhard:
- **Granularität: pro Kanal (API-Key)** — nicht pro Provider. Begründung: ein Provider-Level-Limiter würde alle Failover-Keys blockieren, obwohl andere frei wären.
- **Verhalten bei Treffer: hybrid** — kurz warten, dann 429, nicht sofort 429 und nicht endlos queue.
- **Konfiguration: pro Kanal in `channels.json`** mit globalem Default — verschiedene Provider haben unterschiedliche Limits.

### 2. RateLimiter-Komponente (TDD)

`sigoengine/rate_limiter.go` neu: `RateLimiter.Acquire(ctx, key, minInterval, maxWait)` / `Release(key)`. Hybrid-Logik: innerhalb `minInterval` wartet der Request bis das Intervall verstrichen ist; würde die Wartezeit `maxWait` überschreiten, schlägt er mit `ErrRateLimited` fehl. ctx-Abbruch bricht die Wartezeit ab. `lastCall` wird unter kurzem Lock nur bei erfolgreicher Reservierung gesetzt — nicht während des Sleeps —, sodass parallele Acquires sich korrekt serialisieren.

Sechs Unit-Tests vorab (TDD): Erstaufruf sofort, Warten auf `minInterval`, 429 bei `maxWait`-Überschreitung, ctx-Cancel, unabhängige Keys, Parallel-Serialisierung. Alle grün vor Einbau.

### 3. Server-Einbau + Failover-Synergie

Zwei Flags (`-rate-min-interval 500ms`, `-rate-max-wait 1000ms`), pro-Kanal-Override via `MinInterval`/`MaxWait` (ms) im `Channel`-Struct. `Acquire` sitzt im `channelsToTry`-Loop pro Kanal; `ErrRateLimited` → `continue` →下一个 Kanal. Erst wenn alle Kanäle eines Providers erschöpft sind, geht HTTP 429 + `Retry-After` an den Client.

### 4. Mock-Provider als Testumgebung

`test/mockprovider/` (neu): OpenAI-kompatibler HTTP-Server mit Fixed-Window-Rate-Limit. `New(rps)` für Tests, `NewStandalone` für Standalone-Binary (`test/cmd/mockprovider/`). Integrationstest beweist: 8 parallele Requests ohne Limiter → ≥1× 429 vom Mock; mit Limiter → 0× 429.

### 5. Live-Beweis gegen echten ZAI-Provider

Drei Szenarien gegen `glm-4.5-air` (billigstes Modell, <1 Cent Quota):
- Default (500/1000 ms), 5 parallel → 5× 200, serialisiert (1–2 s), Failover sichtbar im Log.
- Aggressiv (50 ms `max_wait`), 5 parallel → 5× 200 via Failover auf 6 ZAI-Kanäle.
- Überlast (50 ms), 10 parallel → 6× 200 + 4× 429 — exakt passend zu 6 verfügbaren Kanälen.

### 6. ./build/-Zentralisierung

Nebenbei: `go build ./...` war nur Compile-Check, keine Binaries. Stray-Binary `sigoREST/sigoREST` (10,7 M, Juli) trieb umher. Makefile mit `build/sigorest/sigoe/mockprovider/test/clean`, alle Binaries in `./build/` (in `.gitignore`). `CLAUDE.md` und alle drei READMEs + `chinese/README.md` umgestellt.

### 7. Pro-Kanal-Config deployed + LoadState-Bug

Nach der ersten Retrospektive-Version (Commit `c6b0c49`) folgte der Produktiv-Deploy der provider-spezifischen Werte in `/var/sigoREST/channels.json` (Backup `channels.json.bak-20260818`):

| Provider | min_interval_ms | max_wait_ms |
|----------|-----------------|-------------|
| Mammoth  | 800             | 2000        |
| Moonshot | 500             | 1000        |
| ZAI      | 400             | 1000        |

Mammoth dichter als ZAI, weil Mammoth ohne Key läuft (aggressivere Drosselung nötig). 18 Kanäle insgesamt.

Dabei fiel ein kritischer Bug auf: `persistedState` und `LoadState` speicherten nur das `active`-Flag — manuelle `channels.json`-Werte für `MinInterval`/`MaxWait` würden beim Laden ignoriert und beim nächsten `SetActive` überschrieben. Fix (Commit `d6310e2`): `persistedState` um beide Felder erweitern, `LoadState` kopiert sie in den Channel, `saveStateLocked` schreibt sie mit `omitempty`. Roundtrip wäre durch einen Persistenz-Test abgefangen worden — Lücke im TDD.

Live-Beweis der Wirksamkeit: 2 parallele Requests an den expliziten Kanal `mammouth-0` zeigten 881 ms Differenz zwischen den Antworten ≈ die eingestellten 800 ms `min_interval`. Der Limiter drosselt im echten Produktivbetrieb.

Operability-Erweiterung (Commit `e6a24b9`): `AllChannelStatus` exponiert jetzt `min_interval_ms`/`max_wait_ms` pro Kanal in `/api/channels` — Admin sieht die aktive Config ohne Datei-Inspektion.

**Learnings:**

1. **Pro-Kanal-Granularität ist der entscheidende Hebel** — nicht die Limiter-Logik selbst. Der Live-Test bewies es: ein Provider-Level-Limiter hätte bei 50 ms `max_wait` alle 5 Requests sterben lassen. Pro Kanal + Failover verwandelt den Limiter in einen Lastverteiler: volle Kanäle delegieren automatisch an freie Keys.

2. **Sentinel vs. APIError — Schichtentrennung zahlt sich aus.** `ErrRateLimited` ist bewusst `errors.New`, kein `*SigoError`. Er entsteht lokal (Server-Entscheidung), nicht vom Provider. Hätte ich ihn als APIError gebaut, müsste `ClassifyError` wissen, dass lokale Limits existieren — falsche Schicht. Deshalb eigener Early-Return vor dem `apiErr.Type`-Switch.

3. **TDD bei paralleler Logik ist nicht optional.** Der Parallel-Serialisierungs-Test (`TestRateLimiterConcurrentSerializes`) war der wichtigste — er hätte die naive „sleep dann setze lastCall"-Implementierung entlarvt, bei der alle Goroutines gleichzeitig aufwachen und denselben Zeitstempel setzen.

4. **Build-Setup ist Dokumentation.** Die zentralisierte `./build/`-Konvention stand nirgends; `go build ./...` suggerierte fertige Binaries, wo keine waren. Gerhards „Augenblick, wo ist das Programm?" war der Trigger. Makefile + CLAUDE.md machen die Konvention explizit und reproduzierbar.

5. **Caveman-Modus + Learning-Insights vertragen sich.** Die `★ Insight`-Blöcke zwangen zur pünktlichen Architektur-Begründung (Sentinel, Failover-Synergie) — genau dort, wo Verkürzung sonst zu unbegründeten Entscheidungen geführt hätte.

6. **Persistenz-Schicht ist eigener Testpfad.** Der LoadState-Bug war kein Logikfehler, sondern ein vergessener roundtrip: Datenmodell-Felder existierten, aber die Load/Save-Schicht wurde nicht mitgeführt. TDD deckte die Limiter-Logik ab, nicht die Persistenz. Lehre: bei jedem `persistedState`-struct-Change gehört ein Roundtrip-Test dazu (setze Werte → speichere → lade neu → vergleiche). Field-Level-Tests allein reichen nicht, wenn Serialisierung im Weg ist.

**Nächste mögliche Schritte:**
- Mock-Provider zu echtem Lasttest ausbauen (z. B. 100 parallele Clients, Latenz-Histogramm).
- Rate-Limiter-Metriken in `/api/usage` oder `/api/health` exponieren (Throttle-Rate pro Kanal).
- ggf. Token-Bucket statt Fixed-Window, falls Provider-Burst-Toleranz das nötig macht.
- Roundtrip-Test für `channels.json`-Persistenz (setze MinInterval/MaxWait → reload → vergleiche) als Regressionsschutz für künftige `persistedState`-Änderungen.

**Co-Autor**: Claude (Anthropic) — Session vom 18. August 2026.

---

## Session 2026-07-11: Doku-Sync — CN/EN-READMEs + chinese/ auf Multi-Channel-Master-Stand

**Zielsetzung:**
`README_CN.md` und `README_EN.md` hingen eine ganze Generation hinter der deutschen Master-`README.md` nach (pre-Multi-Channel-Ära). Sie fehlten sämtliche Channel-, Failover-, data-dir- und Vision-Funktionalität sowie die neuen Endpoints und Client-Libraries. Ziel war ein treuer 1:1-Spiegel des deutschen Masters in Chinesisch und Englisch plus Aktualisierung des `chinese/`-Subdirs.

**Was erreicht wurde:**

### 1. CN/EN komplett neu als treuer Spiegel

Beide READMEs wurden von Grund auf neu übersetzt (CN 618 Zeilen, EN 616 Zeilen) und enthalten jetzt 1:1 den Master-Stand:

- Architektur-Block mit allen 6 neuen `sigoengine`-Files (`channel.go`, `channel_manager.go`, `channel_health.go`, `session_memory.go`, `env.go`, `version.go`)
- Server-Flags `-data-dir`, `-channel-health-interval`; CLI-Flags `-session-dir`, `-c`
- env-Datei (`./env`) + indizierte API-Keys (`_0`, `_1`, …) für zusätzliche Kanäle
- Datenverzeichnis-Layout unter `/var/sigoREST` mit `channels/`- und `sessions/`-Subdirs
- Multi-Channel-Support, Auto-Failover, Health-Monitor
- Alle `/api/channels/*`-Endpoints, `/api/version`, `/api/usage`, `/api/help`
- Vision-Support, 4 Client-Libraries (Python v2 / Go / JavaScript / Common-Lisp)
- systemd mit `network-online.target` + `EnvironmentFile` (DNS-Race-Fix)
- ~89 Modelle, Shortcodes `cl46-s`/`gpt4o`, Version 1.1

### 2. memory.json-Beispiel korrigiert

Der deutsche Master zeigte das memory.json-Beispiel gekürzt (`"…mit Gerhard."`), die echte eingebettete `sigoREST/memory.json` enthält aber `"…mit Gerhard, einem erfahrenen Software-Entwickler."`. CN/EN zeigten bisher eine falsche englische Übersetzung. Korrektur: alle drei READMEs zeigen jetzt den realen deutschen Default — dokumentiert das tatsächliche Verhalten, keine erfundene Übersetzung.

### 3. chinese/ Subdir aktualisiert

| Datei | Änderung |
|-------|----------|
| `chinese/README.md` | Build-Pfad fix (`./sigoREST/sigoREST` statt `sigoREST`), Multi-Channel-Feature-Block |
| `chinese/KEYWORDS.md` | Keywords für Multi-Channel/Failover/Health-Monitor ergänzt |
| `chinese/PITCH.txt` | Pitch um Multi-Channel + Auto-Failover erweitert |

**Architektur-Entscheidungen:**

| Entscheidung | Begründung |
|--------------|------------|
| **Treuer Spiegel statt zielgruppen-angepasst** | Konsistenz mit Master, wartbar. Nur CN behält das China-Fokus-Intro (Zielgruppe), EN folgt dem Master ohne Intro. |
| **memory.json als realer deutscher Default** | Dokumentation soll echtes Verhalten zeigen, keine erfundene Lokalisierung. |
| **Subagent-Driven Development** | Zwei unabhängige README-Übersetzungen parallel via Fresh-Subagent-pro-Datei, Controller macht Spec-Review gegen Master. Effizient, kein Context-Pollution. |
| **TODO-Archive local-only** | WIP-`.gitignore` ignoriert `TODO-*.md` bewusst → done-Archive bleiben lokale Scratch-Dateien, nicht im Repo. Bestehende tracked done-Files bleiben unangetastet. |

**Testing & Verifikation:**

```bash
# Build unverändert (nur Doku)
go build ./...   # BUILD OK

# Spec-Review gegen Master: Struktur-Marker geprüft
grep -c 'channel.go|channel_manager.go|...' README_CN.md   # 6 ✓
grep -c 'network-online.target' README_EN.md                # 3 ✓
grep -c 'cl46-s' README_CN.md README_EN.md                  # 6/6 ✓

# GitHub-Render-Check (Playwright)
# README.md    → memory.json-Fix live ✓
# README_CN.md → 多渠道支持-Section rendert ✓
# README_EN.md → Multi-Channel Support, kein About-Intro ✓
```

**Code-Änderungen (Zusammenfassung):**

- `README.md`: memory.json-Beispiel auf volle Version korrigiert
- `README_CN.md`: komplette Neuübersetzung (338 → 618 Zeilen)
- `README_EN.md`: komplette Neuübersetzung (338 → 616 Zeilen)
- `chinese/README.md`: Build-Pfad + Multi-Channel-Feature
- `chinese/KEYWORDS.md`: Kanal/Failover/Health-Keywords
- `chinese/PITCH.txt`: Pitch erweitert

**Git:**

Commit `43d93a7` (Doku-Sync) + `6579e50` (TODO-Archiv) direkt auf `main` gepusht. Archiv-File `TODO-20260711-docs-done.md` bleibt local (gitignored). GitHub-Render per Playwright verifiziert.

---

## Session 2026-07-11: Entfernen des Fake-Streamings + Echtes Provider-SSE für alle OpenAI-kompatiblen Clients

**Zielsetzung:**
Der Server und die Clients hatten zwar bereits SSE-Endpunkte, aber der Server sammelte die vollständige Antwort ein und splittete sie wortweise mit künstlichen 8-ms-Verzögerungen („Fake-Streaming“). Ziel war die Umstellung auf echtes, provider-durchgereichtes SSE-Streaming für alle OpenAI-kompatiblen Provider. Anthropic wurde bewusst ausgeschlossen (Kostengründe).

**Was erreicht wurde:**

### 1. Server-seitiges echtes Streaming

- `sigoengine/engine.go`:
  - Neuer gemeinsamer `defaultHTTPClient` (`http.Client{}`) für Connection Reuse.
  - `CallAPI` berücksichtigt bereits gesetzte Deadlines und wendet `timeoutSec` nur an, wenn der Kontext noch keine Deadline hat.
  - Neue `CallAPIStream()`-Funktion: setzt `stream=true`, `Accept: text/event-stream` und liefert `io.ReadCloser` mit dem Provider-Response-Body.

- `sigoREST/main.go`:
  - Entfernung der Fake-Streaming-Hilfsfunktionen (`writeSSEEvent`, `writeStreamingResponse`).
  - Neue `streamProviderResponse()` leitet den Provider-Stream 1:1 an den Client weiter, puffert Zeilen, extrahiert parallel den Text für Session/Memory und sendet abschließend `data: [DONE]`.
  - Handler wählt bei `stream=true` und OpenAI-kompatiblen Providern den `CallAPIStream`-Pfad; Anthropic und Fehlerfälle laufen weiterhin über `CallAPI` mit Retry.

### 2. Clients auf echtes SSE umgestellt

| Client | Änderung |
|--------|----------|
| **Python** | Fake-Streaming-Fallback komplett entfernt; sync + async nutzen `httpx.stream()` / `aiter_lines()` gegen `text/event-stream`. |
| **Go** | Neue Typen `ChatCompletionChunk`, `ChatCompletionChunkChoice`, `ChatCompletionChunkDelta`; neue `ChatStream()`-Methode mit SSE-Zeilenparser; Leerzeilen werden korrekt als Event-Trenner behandelt. |
| **JavaScript** | Neue `chatStream()`-Methode als AsyncGenerator; liest `response.body.getReader()` und parst SSE-Zeilen. |
| **C++** | Neue Chunk-Modelle in `models.hpp`; `chatCompletionStream()` mit Callback und CURL-Write-Callback-Parsing. |
| **Common Lisp** | `chat-stream()` fordert jetzt explizit `Accept: text/event-stream` an. |

### 3. Weitere Verbesserungen

- `sigoengine/channel_health.go`: dynamische Provider-Typ-Erkennung für Health-Checks (`anthropic`, `ollama`, `mammoth`) statt hartkodiertem `"mammoth"`.
- C++ Client: `curl_easy_getinfo` vor `curl_easy_cleanup` ausgeführt (Use-after-free vermieden).

**Architektur-Entscheidungen:**

| Entscheidung | Begründung |
|--------------|------------|
| **Provider-Stream direkt durchreichen** | Keine künstlichen Verzögerungen mehr; echte First-Token-Latenz. |
| **Anthropic ausgeschlossen** | Anwender hat explizit festgelegt, dass Anthropic aufgrund der Kosten nicht für Streaming genutzt wird. |
| **Shared `http.Client`** | Verhindert Connection-Pool-Überlastung bei ~100 parallelen Verbindungen. |
| **Keine externen SSE-Bibliotheken** | Clients parsen SSE selbst, um Abhängigkeiten minimal zu halten. |

**Testing & Verifikation:**

```bash
# Server bauen & starten
go build -o sigoREST/sigoREST ./sigoREST/
./sigoREST/sigoREST -v debug

# curl
curl -s -N -X POST http://127.0.0.1:9080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Accept: text/event-stream" \
  -d '{"model":"cl5-s","messages":[{"role":"user","content":"zähle von 1 bis 3"}],"stream":true}'

# Clients (jeweils getestet)
# Python: SigoClient().chat.completions.create(stream=True)
# Go:     client.ChatStream(ctx, "cl5-s", "...")
# JS:     for await (const ch of client.chatStream("cl5-s", "..."))
# C++:    client.chatCompletionStream("cl5-s", msgs, callback)
```

Alle getesteten Clients lieferten für das Prompt "zähle von 1 bis 3" das erwartete Ergebnis `1, 2, 3`.

**Code-Änderungen (Zusammenfassung):**

- `sigoengine/engine.go`: Shared HTTP-Client, Deadline-Handling, `CallAPIStream()`
- `sigoengine/channel_health.go`: Dynamische Provider-Typ-Erkennung
- `sigoREST/main.go`: Echtes Provider-SSE-Streaming
- `clients/python/src/sigo_client/client.py`: Entfernung Fake-Streaming, echtes SSE
- `clients/go/client.go`: `ChatStream()` + Chunk-Typen + SSE-Parser
- `clients/javascript/client.js`: `chatStream()` AsyncGenerator
- `clients/cpp/core/include/sigorest/{client.hpp,models.hpp}`: Streaming-API + Chunk-Modelle
- `clients/cpp/core/src/client.cpp`: `chatCompletionStream()` Implementierung
- `clients/clisp-exp/sigoclient.lisp`: `Accept: text/event-stream` in `chat-stream`

**Git:**

Branch `feat/real-sse-streaming` erstellt, gepusht, PR #1 eröffnet, gemergt (Squash) und gelöscht. Server nach dem Merge auf `main` neu gebaut und gestartet.

---

## Session 2026-07-10: Modernisierung des Python-Clients + Echter SSE-Streaming-Support

**Zielsetzung:**
Der bestehende `clients/python/` Client (v1.0, basierend auf `requests` + Dataclasses) war funktional, aber veraltet. Ziel war ein kompletter Neubau als moderner, produktionsreifer Client mit:

- Vollständiger OpenAI-SDK-Kompatibilität (`chat.completions.create()`)
- Sync + Async Support (`httpx` + `AsyncClient`)
- Pydantic v2 für starke Typisierung und Validierung
- Echtes Server-Sent Events (SSE) Streaming
- Moderne Projektstruktur (`pyproject.toml`, `src/`-Layout)
- Vollständige Abwärtskompatibilität zum bestehenden sigoREST-Server

**Was erreicht wurde:**

### 1. Komplette Neuentwicklung des Python-Clients (`sigo-client` v2.0)

- **Neue Struktur**: `src/sigo_client/` mit `client.py`, `models.py`, `__init__.py`
- **Kern-Features**:
  - `SigoClient` und `AsyncSigoClient`
  - Vollständige OpenAI-kompatible Schnittstelle
  - Pydantic-Modelle (`ChatCompletion`, `ChatCompletionChunk`, `Model`, `HealthResponse`, `MemoryBlock`)
  - Robuste Fehlerbehandlung (`SigoError`, `SigoAPIError`, `SigoConnectionError`, `SigoTimeoutError`)
  - Context-Manager Unterstützung
- **Modernes Packaging**: `pyproject.toml` mit `hatchling`, `ruff`, `pytest`, `dev` + `test` Extras

### 2. Echter SSE-Streaming-Support (Server + Client)

**Server-seitig (`sigoREST/main.go`)**:
- Erweiterung von `ChatRequest` um `Stream bool`
- Neue Hilfsfunktionen `writeSSEEvent()` und `writeStreamingResponse()`
- Korrekte OpenAI-kompatible SSE-Formatierung (`event: message_start`, `message_delta`, `message_stop`, `usage`, `[DONE]`)
- Vollständige Abwärtskompatibilität: `stream=false` oder fehlender Parameter → unveränderte JSON-Antwort
- Verbessertes Chunk-Format mit `id`, `object`, `choices[].delta` und `finish_reason`

**Client-seitig**:
- Sync- und Async-Implementierung von `_create_stream()`
- Verwendung von `httpx.stream()` / `aiter_lines()` zum Parsen von `text/event-stream`
- Intelligenter Fallback auf simulierte Wort-für-Wort-Ausgabe bei Fehlern
- Aktualisierte Beispiele (`basic_chat.py`, `streaming_chat.py`, `async_chat.py`, `list_models.py`)

### 3. Dokumentation & Testing

- Umfassendes neues `clients/python/README.md`
- Mehrere getestete Beispiele mit realem Streaming
- Aktualisierte Hilfe-Texte im Server (`/api/help`)
- Diese Retrospektive

**Architektur-Entscheidungen:**

| Entscheidung | Begründung |
|--------------|------------|
| **Simuliertes Fallback** | Der Server unterstützt SSE nur bei `stream=true`. Fallback gewährleistet Robustheit. |
| **OpenAI-kompatibles Chunk-Format** | Ermöglicht zukünftige Nutzung mit dem offiziellen `openai` Python-Paket. |
| **Wort-für-Wort in SSE** | Bietet angenehmes „Typewriter“-Gefühl ohne zu viele Events. |
| **src/-Layout + pyproject.toml** | Folgt aktuellen Python-Best-Practices (PEP 621, editable installs). |

**Code-Änderungen (Zusammenfassung):**

**Server:**
- `sigoREST/main.go`: `ChatRequest.Stream`, SSE-Helper, `writeStreamingResponse()`, angepasster Handler, erweiterte Hilfe

**Client:**
- `clients/python/pyproject.toml` (neu)
- `clients/python/src/sigo_client/__init__.py`, `models.py`, `client.py` (komplett neu/überarbeitet)
- `clients/python/examples/*.py` (modernisiert + neues `streaming_chat.py`)

**Testing & Verifikation:**

```bash
# Server mit SSE bauen & starten
go build -o sigoREST/sigoREST ./sigoREST/
./sigoREST/sigoREST -q

# Client installieren
cd clients/python
pip install -e .

# Tests
python examples/list_models.py
python examples/basic_chat.py
python examples/streaming_chat.py        # echtes SSE
python examples/async_chat.py            # Async SSE
```

Alle Beispiele funktionieren. Streaming zeigt jetzt echte `event: message_delta` Zeilen.

**Erkenntnisse & Learnings:**

1. **Abwärtskompatibilität zuerst**: Durch das klare `if isStreaming` im Handler konnten wir Streaming hinzufügen, ohne bestehende Clients zu brechen.

2. **SSE ist trickreich**: Korrekte Header (`X-Accel-Buffering: no`), Flushing, Event-Format und `[DONE]` sind entscheidend. Das `event:` Feld ist optional, aber hilfreich.

3. **Python Streaming-Parsing**: `httpx.stream()` + `iter_lines()` / `aiter_lines()` ist sehr elegant. Der Fallback-Mechanismus hat sich als extrem nützlich erwiesen.

4. **Modernisierung lohnt sich**: Der Sprung von `requests` + Dataclasses zu `httpx` + Pydantic v2 + vollem OpenAI-Interface hat den Client von „brauchbar“ zu „State of the Art“ gemacht.

5. **Zusammenarbeit von Go und Python**: Die enge Abstimmung des Chunk-Formats zwischen Server und Client war der Schlüssel zum Erfolg.

**Nächste mögliche Schritte:**
- Offiziellen `openai` Python-Paket-Adapter (`SigoOpenAIClient`)
- Echte Unit-Tests mit `pytest` + `respx` (Mock SSE)
- Performance-Optimierungen bei sehr langen Streams
- Unterstützung für `tools` / Function Calling in den Streaming-Chunks

**Co-Autor**: Grok (xAI) — Session vom 10. Juli 2026.

---

*(Vorherige Retrospektiven siehe weiter unten im Dokument)*
