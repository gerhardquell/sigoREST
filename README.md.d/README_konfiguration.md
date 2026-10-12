# Konfiguration

[← README](../README.md)

## Server-Flags (`sigoREST`)

| Flag | Default | Beschreibung |
|------|---------|--------------|
| `-http-port` | `9080` | HTTP (nur localhost 127.0.0.0/8) |
| `-https-port` | `9443` | HTTPS (privates Netz 192.168.0.0/16, 10.0.0.0/8) |
| `-cert` | `./certs/server.crt` | TLS-Zertifikat (self-signed, wird beim ersten Start auto-generiert) |
| `-key` | `./certs/server.key` | TLS-Schlüssel |
| `-data-dir` | `/var/sigoREST` | Basisverzeichnis für Memory, System-Prompt, `channels.json`, Sessions, `costs.db`, `id_registry.db` |
| `-channel-health-interval` | `30s` | Intervall für Kanal-Health-Checks |
| `-rate-min-interval` | `500ms` | Default Mindest-Abstand zwischen Calls pro Kanal (`0` = deaktiviert) |
| `-rate-max-wait` | `1000ms` | Default max. Queue-Wartezeit bis HTTP 429 pro Kanal |
| `-comm-log` | — (aus) | Kommunikationsprotokoll als JSONL, z.B. `/var/log/sigoREST/communication.jsonl`, siehe [Betrieb](README_betrieb.md#kommunikationsprotokoll--comm-log) |
| `-v` | `info` | Log-Level: `debug\|info\|warn\|error` |
| `-q` | — | Quiet Mode (nur Fehler) |
| `-j` | — | JSON-Logs |
| `-version` | — | Version anzeigen und beenden |

Für Tests neben dem Live-Dienst einen freien Port und ein eigenes
Datenverzeichnis nehmen, z.B. `-http-port 19080 -https-port 19443 -data-dir <tmp>`.
Auf Gerhards System sind 8080/8443 durch einen lokalen Webserver belegt.

## CLI-Flags (`sigoE`)

| Flag | Default | Beschreibung |
|------|---------|--------------|
| `-m` | `gpt41` | Modell (Shortcode oder vollständiger Name) |
| `-s` | — | Session-ID für Gesprächsverlauf |
| `-session-dir` | `.sessions/` | Verzeichnis für Session-Dateien |
| `-c` | — | Kanal wählen, z.B. `mammouth-0` |
| `-n` | `0` | Max. Tokens (0 = Modell-Default) |
| `-T` | `-1` | Temperatur (-1 = Modell-Default) |
| `-t` | `180` | Timeout in Sekunden |
| `-r` | `3` | Anzahl Wiederholungsversuche |
| `-v` | `info` | Log-Level: `debug\|info\|warn\|error` |
| `-V` / `-version` | — | Version anzeigen |
| `-j` | — | JSON-Ausgabe |
| `-q` | — | Quiet Mode (nur Fehler) |
| `-l` | — | Alle verfügbaren Modelle anzeigen (nach Provider gruppiert) |
| `-i` | — | Modell-Info anzeigen (inkl. Provider) |
| `-h` | — | Hilfe anzeigen |
| `-sp` | — | System-Prompt |

```bash
./build/sigoE -l
echo "Hallo" | ./build/sigoE -m gpt41
./build/sigoE -m gpt41 -s projekt-x "Erste Nachricht"
./build/sigoE -m gpt41 -s projekt-x "Zweite Nachricht"
```

Die CLI nutzt die CSV/Registry (`models_registry.go`), nicht die
Live-Shortcodes des Servers — siehe
[Provider & Modelle → CSV/Registry-Format](README_provider-modelle.md#csvregistry-format-cli).

## Zugriffskontrolle

| Port | Protokoll | Erlaubte IPs |
|------|-----------|--------------|
| 9080 | HTTP | 127.0.0.0/8 (localhost) |
| 9443 | HTTPS | 192.168.0.0/16, 10.0.0.0/8 |
| beide | — | IPv6 geblockt (außer `::1`) |

TLS: self-signed Zertifikat, beim ersten Start automatisch generiert.

## Environment / API-Keys

sigoREST liest API-Keys in dieser Reihenfolge:

1. Optionale `.env`-Datei im Startverzeichnis (`./.env`). Eine veraltete
   `./env` wird noch geladen, erzeugt aber eine Warnung (liegen beide vor,
   gewinnt `.env` und `env` wird ignoriert).
2. Echte Environment-Variablen

```bash
MAMMOUTH_API_KEY=sk-...          # Mammouth.ai (GPT, Claude, Gemini, Grok, DeepSeek, ...) — für den Modellabruf optional
MAMMOUTH_API_KEY_0=sk-...        # Zusätzlicher Kanal 0
MAMMOUTH_API_KEY_1=sk-...        # Zusätzlicher Kanal 1
MOONSHOT_API_KEY=sk-...          # Moonshot.ai (Kimi)
ZAI_API_KEY=sk-...               # Z.ai (GLM)
LONGCAT_API_KEY=sk-...           # Longcat
OMNIROUTE_API_KEY=sk-...         # cheaperinference
OPENROUTER_API_KEY=sk-...        # OpenRouter
```

Indizierte Keys (`_0`, `_1`, ...) erzeugen zusätzliche Kanäle, der
unindizierte Key wird zum `default`-Kanal. Ollama braucht keinen Key. Die
Zuordnung ENV-Variable → Provider steht in `knownProviders`
(`sigoengine/channel.go`). Details: [Kanäle](README_kanaele.md).

## Datenverzeichnis (`-data-dir`)

Standard: `/var/sigoREST`

```text
/var/sigoREST/
├── channels.json                     # Persistenter Aktivierungs-Status der Kanäle (+ manually_disabled, Rate-Overrides)
├── memory.json                       # Globaler Memory-Block
├── system-prompt.txt                 # Globaler System-Prompt
├── costs.db                          # Kosten-Tracking (SQLite, WAL)
├── id_registry.db                    # Persistente Shortcode-Registry (SQLite, WAL)
├── channels/
│   └── <provider>/
│       └── <channel>/
│           ├── memory.json           # Kanal-spezifischer Memory
│           └── system-prompt.txt     # Kanal-spezifischer System-Prompt
└── sessions/
    └── <provider>/
        └── <channel>/
            └── <model>-<session>.json
```

## memory.json (global)

Globaler System-Kontext für alle Anfragen über `/v1/chat/completions`
(wird immer zuerst eingefügt, außer bei `bare:true`):

```json
{
  "content": "Antworte immer auf Deutsch. Du sprichst mit Gerhard, einem erfahrenen Software-Entwickler.",
  "cache": true
}
```

`cache: true` → Anthropic ephemeral caching. OpenAI cached automatisch ab
1024 Tokens. Eine Default-Version ist ins Binary eingebettet
(`sigoREST/memory.json`), die Datei im `-data-dir` hat Vorrang. Ändern zur
Laufzeit: `PUT /api/memory`, siehe [API](README_api.md).

Neben dem globalen Memory gibt es einen Memory-Block und einen System-Prompt
pro Kanal. Ein `system_prompt` im Request hat die höchste Priorität und
überschreibt globalen und kanal-spezifischen System-Prompt; `bare:true` lässt
Memory und Server-Prompt ganz weg (siehe [API](README_api.md)).
