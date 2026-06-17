# sigoREST als systemd-Dienst installieren

Konvention:
- Binary/Wrapper: `/usr/local/sbin/sigoREST`
- Konfiguration/Daten: `/usr/local/slib/sigoREST/`

## 1. Binary installieren

```bash
go build -o sigoREST/sigoREST ./sigoREST/
sudo cp sigoREST/sigoREST /usr/local/sbin/sigoREST
```

## 2. Bibliotheks-/Datenverzeichnis anlegen

```bash
sudo mkdir -p /usr/local/slib/sigoREST/certs
sudo mkdir -p /var/sigoREST
sudo cp sigoREST/memory.json /var/sigoREST/
sudo chown -R sigorest:sigorest /var/sigoREST
sudo chmod 0755 /var/sigoREST
```

Falls der `sigorest`-Benutzer noch nicht existiert:

```bash
sudo useradd -r -s /usr/sbin/nologin sigorest
```

`sigoREST` lädt `memory.json` und `system-prompt.txt` aus dem via `-data-dir` konfigurierten Verzeichnis (default `/var/sigoREST`). Kanal-Sessions, Kanal-Memory und `channels.json` landen ebenfalls dort.

## 3. Environment-Datei anlegen

```bash
sudo nano /usr/local/slib/sigoREST/env
```

Inhalt:
```
MAMMOUTH_API_KEY=dein-key-hier
MAMMOUTH_API_KEY_0=zusätzlicher-key-0
MAMMOUTH_API_KEY_1=zusätzlicher-key-1
MOONSHOT_API_KEY=dein-key-hier
ZAI_API_KEY=dein-key-hier
```

Berechtigungen einschränken:
```bash
sudo chmod 600 /usr/local/slib/sigoREST/env
```

## 4. systemd Unit-Datei anlegen

```bash
sudo nano /etc/systemd/system/sigoREST.service
```

Inhalt:
```ini
[Unit]
Description=sigoREST - AI REST Gateway
# network-online.target (nicht network.target!): wartet bis Netz + DNS
# wirklich oben sind. Sonst scheitert beim Boot der Provider-Fetch mit
# "no such host" und nur statische Fallback-Modelle laden.
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=sigorest
Group=sigorest
WorkingDirectory=/usr/local/slib/sigoREST
EnvironmentFile=/usr/local/slib/sigoREST/env
ExecStart=/usr/local/sbin/sigoREST \
    -http-port 9080 \
    -https-port 9443 \
    -data-dir /var/sigoREST \
    -channel-health-interval 30s \
    -cert /usr/local/slib/sigoREST/certs/server.crt \
    -key  /usr/local/slib/sigoREST/certs/server.key \
    -v info
Restart=on-failure
RestartSec=5s

# Logging nach journald
StandardOutput=journal
StandardError=journal
SyslogIdentifier=sigoREST

[Install]
WantedBy=multi-user.target
```

## 5. Dienst aktivieren und starten

```bash
sudo systemctl daemon-reload
sudo systemctl enable sigoREST
sudo systemctl start sigoREST
```

## 6. Status prüfen

```bash
sudo systemctl status sigoREST
journalctl -u sigoREST -f
```

## Nützliche Befehle

```bash
# Neu starten (z.B. nach Binary-Update)
sudo systemctl restart sigoREST

# Stoppen
sudo systemctl stop sigoREST

# Logs der letzten 100 Zeilen
journalctl -u sigoREST -n 100

# Memory-Block aktualisieren (kein Restart nötig)
curl -X PUT http://localhost:9080/api/memory \
  -H "Content-Type: application/json" \
  -d '{"content":"Neuer System-Kontext hier","cache":true}'
```

## Hinweise

- `memory.json` liegt in `/var/sigoREST/` und kann dort angepasst werden
  (Änderungen erst nach `systemctl restart sigoREST` aktiv)
- Zusätzliche Kanäle (z.B. `MAMMOUTH_API_KEY_0`) sind standardmäßig inaktiv.
  Aktivierung via REST: `curl -X POST http://localhost:9080/api/channels/mammouth/0/enable`
- Der Health-Monitor prüft alle aktiven Kanäle im `-channel-health-interval` und schaltet bei Bedarf Reservekanäle zu
