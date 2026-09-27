# TODO 20260927 - Kleinere Nacharbeiten

## 1. Enviroment — ✅ erledigt (`01ef941`)
Bisher wird die env - Datei für alle sichtbar angezeigt. Dieses soll geändert werden,
so daß in Zukunft nur noch .env - Dateien akzeptiert werden.

> Umgesetzt: `./.env` wird geladen. Eine veraltete `./env` wird noch geladen,
> aber mit Warnung (absoluter Pfad, Hinweis zum Umbenennen). Liegen beide vor,
> gewinnt `.env` und `env` wird mit Warnung ignoriert. Die Warnung kommt erst
> nach der Log-Konfiguration, `-q` unterdrückt sie. systemd-Doku auf
> `EnvironmentFile=.../.env` umgestellt, `.env` und `/env` in `.gitignore`.

## 2. Model-Liste — ✅ erledigt (`298f9f5`)
Die Modelliste soll auch als csv-Datei ausgeben werden können.

> Umgesetzt: `curl -s 'http://localhost:9080/api/models?format=csv' > models.csv`.
> Das Format entspricht der CLI-Registry (Semikolon, 11 Felder) plus
> `provider;provider_code;upstream_id`. Die Kopfzeile beginnt mit `#`. Damit
> ist die Datei direkt als `models.csv` für `sigoE` nutzbar und bringt die
> Live-Shortcodes des Servers mit.

## 3. Kommunikationsprotokoll — ✅ erledigt (`c0b9892`)
Ich möchte die Möglichkeit haben, mit einem cli-Parameter die Protokollierung der
gesamten externen Kommunikation von sigoREST einschalten zu können. Die Daten sollen
in eine Datei gespeichert werden. Meine Idee wäre /var/log/sigoREST/communication.json

> Umgesetzt: `-comm-log /var/log/sigoREST/communication.jsonl`. Protokolliert
> werden alle Chat- und Embedding-Calls zu den Providern (inkl. Streaming und
> `/v1/messages`) mit vollständigem Request und Response als JSONL.
> API-Key-Header werden maskiert, die Datei hat `0600`. Pings,
> Health-Checks und der Modellabruf beim Boot werden nicht protokolliert.
> systemd (`LogsDirectory=`) und logrotate (`copytruncate`) stehen in
> `docs/systemd-install.md`.

---

## Nebenbei gefunden und behoben

- **Ollama-Chat ging nie** (`f75956c`): Ollama hat keinen API-Key und bekam
  deshalb keinen Kanal. Jeder Chat-Call endete mit
  `404 CONFIG_NOT_FOUND: no active channel for provider`, betroffen war auch
  der Live-Dienst. Jetzt gibt es den keylosen Kanal `ollama-default`.
- **Doppeltes `data: [DONE]`** in allen OpenAI-kompatiblen Streams (`5500406`).
- **Health-Probe für Longcat/cheaperinference** fehlte (`b5a9acb`).
- **Modellabrufe ignorierten `.env`**: Die Fetcher für Moonshot, Z.ai, Longcat
  und cheaperinference lasen `os.Getenv` statt `GetEnvWithFile`. Das betraf
  nur manuelle Starts mit Keys ausschließlich in `./.env` (unter systemd setzt
  `EnvironmentFile=` echte Umgebungsvariablen). `TestNoDirectOsGetenv`
  verhindert per AST-Prüfung, dass das wiederkommt.
- **Manuelles `disable` wurde vom Health-Monitor zurückgenommen**: Jetzt hat
  manuelles `disable` Vorrang (Gerhards Entscheidung). Das Flag
  `manually_disabled` wird in `channels.json` gespeichert und ist in
  `/api/channels` sichtbar. Nur `/enable` hebt es auf.

## Offen

- [x] **Deployment** am 27.09. um 13:27 (`sigoREST` und `sigoE`), live geprüft.
- [ ] **`-comm-log` live einschalten** (optional, nur zur Fehlersuche):
  `LogsDirectory=sigoREST` in die Unit, dazu das Flag an `ExecStart` anhängen.
- [ ] **Shortcode-Kuriosum** `che-cl-f025` für `claude-fable-5`:
  `GenerateShortcode` kennt „fable“ nicht als Unterfamilie. Wegen
  Assign-Once ist das Kürzel eingefroren und betrifft nur künftige Modelle.
- [ ] **Fallback-Provider** (aus TODO 20260920, noch nicht umgesetzt): Für
  jeden Zugang soll es einen Fallback-Provider geben können.
