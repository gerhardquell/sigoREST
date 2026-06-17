# Design: Multi-Channel Support für sigoREST

**Datum:** 2026-06-17  
**Thema:** Parallele KI-Kanäle mit eigenen API-Keys, Memorys und Sessions  
**Status:** Vom Benutzer validiert, bereit für Implementierung  
**Version:** 1.1 (sigoengine/sigoREST/sigoE)

## Zusammenfassung

sigoREST erhält die Möglichkeit, pro Provider mehrere API-Key-Kanäle zu verwalten. Jeder Kanal hat seinen eigenen API-Key, optional eigenen Memory, eigene Sessions und einen eigenen Circuit Breaker. Kanäle sind manuell aktivierbar, werden standardmäßig aber nur bei Bedarf (Failover) oder durch einen Health-Monitor zugeschaltet.

Die OpenAI-kompatible API (`/v1/chat/completions`) bleibt unverändert erreichbar; Kanalwahl erfolgt optional über ein neues Feld im Request-Body. Der Versions-String wird zentral in `sigoengine` verwaltet und ist über CLI (`-version`) und REST (`GET /api/version`) abrufbar.

## Motivation

- Mehrere API-Keys pro Provider ermöglichen höhere Durchsatzgrenzen und Redundanz.
- Parallele Aufgaben sollen auf eigene Kanäle verteilt werden können.
- Trennung von Kontexten (Memory + Sessions) pro Kanal verhindert Quersprecher.
- Automatisches Failover schützt vor Rate-Limits und temporären Provider-Ausfällen.

## Anforderungen

1. **Mehrere API-Keys pro Provider** aus Env-Vars (`MAMMOUTH_API_KEY`, `MAMMOUTH_API_KEY_0`, `MAMMOUTH_API_KEY_1`, ...).
2. **Standardverhalten:** Nur der unindizierte Key (`MAMMOUTH_API_KEY`) ist als `default`-Kanal aktiv.
3. **Manuelle Aktivierung:** Kanäle können zur Laufzeit und/oder per Env-Var aktiviert werden.
4. **Automatisches Failover:** Wenn ein Kanal fehlschlägt, wird der nächste aktive Kanal probiert.
5. **Auto-Aktivierung:** Health-Monitor schaltet inaktive Reservekanäle zu, wenn alle aktiven Kanäle unhealthy sind.
6. **Eigener Memory und eigene Sessions pro Kanal**, persistiert unter `/var/sigoREST`.
7. **OpenAI-Kompatibilität** bleibt erhalten (`/v1/models` zeigt keine Kanäle).
8. **CLI-Unterstützung** mit `-c <channel>` und konfigurierbarem Session-Verzeichnis.
9. **Versions-String** zentral verfügbar über CLI und REST.

## Architektur

```text
┌─────────────────────────────────────────────────────────────┐
│                         Client                              │
└──────────────────────┬──────────────────────────────────────┘
                       │ POST /v1/chat/completions
                       │ { "model": "claude-h", "channel": "mammouth-0" }
                       ▼
┌─────────────────────────────────────────────────────────────┐
│                      sigoREST Server                        │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────────┐  │
│  │ /api/version │  │ /api/channels│  │ /v1/chat/...     │  │
│  └──────────────┘  └──────────────┘  └──────────────────┘  │
│                         │                                    │
│                         ▼                                    │
│              ┌─────────────────────┐                        │
│              │   ChannelManager    │                        │
│              │  - Resolve channel  │                        │
│              │  - Failover logic   │                        │
│              │  - Health monitor   │                        │
│              └─────────────────────┘                        │
│                         │                                    │
│              ┌──────────┴──────────┐                        │
│              ▼                     ▼                        │
│      ChannelRegistry           ProviderConfig               │
│      (Env-Scan + State)        (API-Key + Model)            │
└─────────────────────────────────────────────────────────────┘
```

### Neue Dateien

- `sigoengine/channel.go` — `Channel`, `ChannelRegistry`, Env-Discovery
- `sigoengine/channel_manager.go` — Auflösung, Failover, Health-Monitor
- `sigoengine/version.go` — zentrale Versions-Konstante
- Erweiterungen in `sigoREST/main.go` und `cmd/sigoE/main.go`

## Datenmodell

### `Channel`

```go
type Channel struct {
    Provider          string        // z.B. "mammouth"
    Name              string        // "default", "0", "1", ...
    APIKey            string
    Active            bool          // manuell oder auto-aktiviert
    Order             int           // default=0, 0=1, 1=2, ...
    Healthy           bool
    LastHealthCheck   time.Time
    LastError         error
    ConsecutiveErrors int
}
```

### `ChannelRegistry`

```go
type ChannelRegistry struct {
    mu        sync.RWMutex
    channels  map[string][]*Channel // provider → sortierte Kanäle
    statePath string                // /var/sigoREST/channels.json
}
```

### Methoden (geplant)

- `NewChannelRegistry(statePath string) *ChannelRegistry`
- `DiscoverFromEnv()` — scannt alle `PROVIDER_API_KEY*` Env-Vars
- `LoadState() / SaveState()` — liest/schreibt `channels.json`
- `SetActive(provider, name string, active bool)`
- `GetChannel(provider, name string) (*Channel, bool)`
- `ActiveChannels(provider string) []*Channel` — nach `Order` sortiert
- `ResolveChannel(provider, requested string) (*Channel, error)`

## API-Erweiterungen

### Neue Endpunkte

| Methode | Pfad | Zweck |
|---|---|---|
| `GET` | `/api/version` | Versions-String |
| `GET` | `/api/channels` | Alle Kanäle mit Status |
| `GET` | `/api/channels/:provider/:name` | Einzelkanal-Status |
| `POST` | `/api/channels/:provider/:name/enable` | Kanal aktivieren |
| `POST` | `/api/channels/:provider/:name/disable` | Kanal deaktivieren |
| `GET/PUT` | `/api/channels/:provider/:name/memory` | Kanal-Memory |
| `GET/PUT` | `/api/channels/:provider/:name/system-prompt` | Kanal-System-Prompt |

### Request-Body-Erweiterung

```json
{
  "model": "claude-h",
  "messages": [...],
  "channel": "mammouth-0"
}
```

`channel` ist optional. Fehlt es, wird der erste aktive Kanal verwendet.

### Beispiel-Response `/api/channels`

```json
[
  {
    "provider": "mammouth",
    "name": "default",
    "full_name": "mammouth-default",
    "active": true,
    "healthy": true,
    "last_health_check": "2026-06-17T12:37:29Z"
  },
  {
    "provider": "mammouth",
    "name": "0",
    "full_name": "mammouth-0",
    "active": false,
    "healthy": false,
    "last_health_check": null
  }
]
```

## Request-Flow

1. Modell validieren und auflösen (wie bisher).
2. Provider bestimmen (`mammouth`, `moonshot`, `zai`, `ollama`).
3. Gewünschten Kanal aus `req.Channel` lesen.
4. `channelManager.Resolve(provider, req.Channel)`:
   - Wenn `req.Channel` gesetzt und aktiv → verwenden.
   - Wenn gesetzt aber inaktiv → `400 channel_inactive`.
   - Wenn leer → erster aktiver Kanal.
5. `ProviderConfig` mit Kanal-API-Key bauen.
6. Circuit Breaker pro Kanal (Key: `model#channel`).
7. `RetryWithBackoff` auf aktuellem Kanal ausführen.
8. Bei retryablem Fehler: nächster aktiver Kanal, Retry zurücksetzen.
9. Session speichern unter `/var/sigoREST/sessions/<provider>/<channel>/<model>-<session>.json`.
10. Memory pro Kanal laden aus `/var/sigoREST/channels/<provider>/<channel>/memory.json`.

### Failover-Regeln

- **Retryable Fehler** (`rate_limit`, `timeout`, `server_error`) → nächster aktiver Kanal.
- **Auth-Fehler** → Kanal als unhealthy markieren, Health-Monitor deaktiviert ihn.
- **Client-Fehler** → kein Failover, direkt `400`.

## Health-Monitor

- Läuft als Goroutine im Server.
- Intervall konfigurierbar via `-channel-health-interval` (Default 30s).
- Prüft alle Kanäle mit `Active=true` mittels `ProbeProvider` (echter Mini-API-Call).
- Ergebnisse:
  - Erfolg → `Healthy=true`, `ConsecutiveErrors=0`.
  - Auth-Fehler → `Healthy=false`, Kanal wird deaktiviert.
  - Temporärer Fehler → `Healthy=false`, `ConsecutiveErrors++`, Kanal bleibt aktiv.
- **Auto-Aktivierung:** Wenn alle aktiven Kanäle eines Providers unhealthy sind, wird der nächste inaktive Kanal in Reihenfolge aktiviert.

## Persistenz

### Verzeichnisstruktur

```text
/var/sigoREST/
├── channels.json
├── memory.json
├── channels/
│   └── <provider>/
│       └── <channel>/
│           ├── memory.json
│           └── system-prompt.txt
└── sessions/
    └── <provider>/
        └── <channel>/
            └── <model>-<session>.json
```

### `channels.json`

```json
{
  "mammouth": {
    "default": { "active": true },
    "0": { "active": false },
    "1": { "active": false }
  },
  "moonshot": {
    "default": { "active": true }
  }
}
```

### CLI

- Default-Session-Verzeichnis: `.sessions/`
- Neues Flag `-session-dir <path>` erlaubt `/var/sigoREST`

## CLI-Änderungen

### Neue Flags

| Flag | Zweck |
|---|---|
| `-c <channel>` | Kanal wählen, z.B. `-c mammouth-0` |
| `-session-dir <path>` | Speicherort für Sessions |
| `-version` | Versions-String anzeigen |

### Verhalten

- Ohne `-c`: automatische Kanalwahl über aktive Kanäle.
- Mit `-c`: exakter Kanal; Fehler wenn inaktiv.
- Sessions pro Kanal isoliert.
- `sigoE -version` gibt `sigoE <version>` aus.

## Versions-String

- Zentrale Konstante in `sigoengine/version.go`:
  ```go
  const Version = "1.1"
  ```
- `sigoREST` und `sigoE` verwenden beide `sigoengine.Version`.
- REST: `GET /api/version` → `{"version": "1.1", "component": "sigoREST"}`
- CLI: `sigoE -version` → `sigoE 1.1`

## Testing

### Unit-Tests (`sigoengine/`)

- `channel_test.go`
  - Env-Scanning
  - Kanal-Sortierung
  - Aktivierung/Deaktivierung
  - `ResolveChannel` mit und ohne Request
- `channel_manager_test.go`
  - Failover-Logik mit gemockten Kanälen
  - Auto-Aktivierung bei Ausfall aller aktiven Kanäle

### Manuelle Integrationstests

- Server mit mehreren Dummy-Keys starten.
- `/api/channels` prüfen.
- Chat-Request mit `"channel": "mammouth-0"`.
- Failover durch ungültigen Key simulieren.
- `/api/version` und `sigoE -version` prüfen.

### Nicht getestet

- Echte Provider-APIs (keine Keys im Repo).
- TLS-Zertifikat-Generierung.

## Risiken und Offene Punkte

1. **Berechtigungen für `/var/sigoREST`:** Server läuft als Service; Verzeichnis muss angelegt und beschreibbar sein. Dokumentation in `docs/systemd-install.md` erweitern.
2. **Env-Scan-Performance:** Bei sehr vielen Env-Vars könnte der Scan langsam werden. Praktisch aber vernachlässigbar.
3. **Ollama-Kanäle:** Ollama hat keinen API-Key → Kanal-Konzept pro Ollama-Endpoint optional (out of scope für diesen Schritt).
4. **Rückwärtskompatibilität:** Bestehende `.sessions/` und `memory.json` bleiben funktionsfähig; Server migriert nicht automatisch.

## Entscheidungsprotokoll

| Entscheidung | Gewählt | Begründung |
|---|---|---|
| Kanal-Auswahl | Client + Auto-Fallback | Flexibel, rückwärtskompatibel |
| Pro-Kanal-Memory/Sessions | Beides | Maximale Kontext-Trennung |
| Aktivierung | Env + REST API | Boot-Konfig + Operator-Steuerung |
| Auto-Zuschaltung | Health-Monitor + Request-Failover | Robusteste Lösung |
| Modell-Liste | Unverändert, Kanäle über `/api/channels` | OpenAI-Kompatibilität |
| Persistenz | `/var/sigoREST` | Zentral, systemd-tauglich |
| CLI-Kanäle | `-c <channel>` | Konsistent mit Server |
| Kanal-Benennung | `<provider>-<index>` | Einfach, aus Env ableitbar |
| Maximale Kanal-Anzahl | Dynamisch | Keine künstliche Grenze |
| Unindizierter Key | `mammouth-default` | Klar getrennt von indizierten |
| Status-Persistenz | `channels.json` | Zentral, Env hat Vorrang |
| Health-Check | `ProbeProvider` (echter Mini-Call) | Keine false positives |
| Kanal gelten pro | Provider | Ein Key = ein Provider-Account |
| Fallback-Reihenfolge | `default → 0 → 1 → ...` | Deterministisch |
| Circuit Breaker | Pro Kanal (`model#channel`) | Feinste Isolation |
| Usage-Tracking | Pro Kanal + Modell-Aggregation | Transparenz |
| Health-Deaktivierung | Nur bei Auth-Fehlern | Temporäre Fehler = Failover |
| Session-Verzeichnis CLI | Konfigurierbar | Server `/var/sigoREST`, CLI `.sessions/` |
| Versions-String | Zentral in `sigoengine` | Einheitliche Version über CLI + REST |
