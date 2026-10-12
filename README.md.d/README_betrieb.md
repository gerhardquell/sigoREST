# Betrieb

[← README](../README.md)

## Installation (systemweit)

**sigoREST Server** als systemd-Service:

```bash
make build                                   # alle Binaries → ./build/
sudo cp build/sigoREST /usr/local/sbin/sigoREST

# Datenverzeichnis anlegen
sudo mkdir -p /var/sigoREST
sudo chown -R sigorest:sigorest /var/sigoREST

# Globaler Memory-Block (optional, sonst greift die eingebettete Version)
sudo mkdir -p /usr/local/slib/sigoREST
sudo cp sigoREST/memory.json /usr/local/slib/sigoREST/
```

**sigoE CLI:**

```bash
make build
sudo cp build/sigoE /usr/local/bin/sigoE
```

Ablage auf Gerhards System:
- Binary: `/usr/local/sbin/sigoREST`
- Daten: `/var/sigoREST/`
- Konfiguration/Env: `/usr/local/slib/sigoREST/.env`
- CLI: `/usr/local/bin/sigoE`

## systemd-Service

Service-File (`/etc/systemd/system/sigorest.service`):

```ini
[Unit]
Description=sigoREST Server
# network-online.target wartet, bis DNS verfügbar ist
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/sbin/sigoREST -data-dir /var/sigoREST -channel-health-interval 30s
Restart=on-failure
User=sigorest
Group=sigorest
EnvironmentFile=/usr/local/slib/sigoREST/.env

[Install]
WantedBy=multi-user.target
```

API-Keys gehören ins `EnvironmentFile`, siehe
[Konfiguration → Environment](README_konfiguration.md#environment--api-keys).
Ausführliche Anleitung: [`docs/systemd-install.md`](../docs/systemd-install.md).

```bash
sudo systemctl start sigoREST
sudo systemctl enable sigoREST
journalctl -u sigoREST -f
```

## DNS-Race beim Boot

Die Unit muss `Wants=`/`After=network-online.target` setzen — **nicht**
`network.target`. Sonst ist DNS beim Start oft noch nicht oben: Mammouth,
Moonshot, cheaperinference und OpenRouter liefern dann 0 Modelle, und es
erscheinen nur die statischen ZAI-/Longcat-Modelle ("no such host" im Log;
siehe [Fallback-Asymmetrie](README_provider-modelle.md#fallback-asymmetrie-bei-fehlern)).
Zweiter Schutz ist `FetchWithRetry` (4 Versuche, 2s/4s/8s Backoff).

Workaround zur Laufzeit: `systemctl restart sigoREST`.

## Kommunikationsprotokoll (`-comm-log`)

Optional (`-comm-log <pfad>`, leer = aus), z.B.
`/var/log/sigoREST/communication.jsonl`: Jeder Chat- und Embedding-Call zum
Provider wird mit vollständigem Request/Response als JSONL geschrieben.

Umsetzung (`sigoengine/commlog.go`) als `http.RoundTripper`
(`NewCommLogTransport`) in `chatHTTPClient` (`CallAPI`/`CallAPIStream`, also
auch `/v1/messages`) und im Embedding-Client von `handleEmbeddings`.
Provider-Pings (`defaultHTTPClient`), Health-Monitor und Boot-Modellabrufe
laufen bewusst **nicht** darüber — deshalb der eigene `chatHTTPClient`.

- Der Eintrag wird beim `Close()` des Response-Bodys geschrieben
  (Tee-Reader), sonst fehlte bei SSE alles nach den Headern. Aufrufer müssen
  den Body schließen (`defer stream.Close()`).
- Gültiges JSON landet als eingebettetes Objekt (`json.RawMessage`, beim
  Marshal kompaktiert → bleibt eine Zeile), SSE/Sonstiges als String.
- Secret-Header → `[REDACTED]`; Bodies > 10 MiB werden abgeschnitten.
- Datei `0600`, Append-Modus (logrotate mit `copytruncate`).
- Lässt sich die Datei nicht öffnen, bricht der Server-Start ab — gewollt,
  kein stilles Weiterlaufen ohne Protokoll. Ohne Flag reicht der Transport
  unverändert durch (`activeCommLog` = nil).

## Debugging

```bash
# Debug-Logs
./build/sigoREST -v debug

# Health / Circuit Breaker
curl -s http://localhost:9080/api/health | jq

# Memory-Block prüfen
curl -s http://localhost:9080/api/memory

# Kanäle
curl -s http://localhost:9080/api/channels | jq

# Verbrauch heute / Monat
curl -s "http://localhost:9080/api/costs?period=today"
curl -s "http://localhost:9080/api/costs?period=month"
```

Session-Dateien: siehe [Architektur → Session-Management](README_architektur.md#session-management).
