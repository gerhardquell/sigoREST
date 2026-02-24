# sigoREST als systemd-Dienst installieren

Konvention:
- Binary/Wrapper: `/usr/local/sbin/sigoREST`
- Konfiguration/Daten: `/usr/local/slib/sigoREST/`

## 1. Binary installieren

```bash
go build -o sigoREST/sigoREST ./sigoREST/
sudo cp sigoREST/sigoREST /usr/local/sbin/sigoREST
```

## 2. Bibliotheksverzeichnis anlegen

```bash
sudo mkdir -p /usr/local/slib/sigoREST/certs
sudo cp sigoREST/models.csv /usr/local/slib/sigoREST/
sudo cp sigoREST/memory.json /usr/local/slib/sigoREST/
```

## 3. Environment-Datei anlegen

```bash
sudo nano /usr/local/slib/sigoREST/env
```

Inhalt:
```
MAMMOUTH_API_KEY=dein-key-hier
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
After=network.target

[Service]
Type=simple
WorkingDirectory=/usr/local/slib/sigoREST
EnvironmentFile=/usr/local/slib/sigoREST/env
ExecStart=/usr/local/sbin/sigoREST \
    -http-port 9080 \
    -https-port 9443 \
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

- Das TLS-Zertifikat wird beim ersten Start automatisch in `/usr/local/slib/sigoREST/certs/` generiert
- `models.csv` und `memory.json` können ohne Neuinstallation angepasst werden
  (Änderungen erst nach `systemctl restart sigoREST` aktiv)
- Die `env`-Datei enthält API-Keys → `chmod 600` ist Pflicht
