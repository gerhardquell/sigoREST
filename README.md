# sigoREST

OpenAI-kompatibler REST-Server und CLI für viele KI-Provider (Mammouth,
Moonshot, Z.ai, Longcat, cheaperinference, OpenRouter, lokales Ollama) hinter
einer einheitlichen API — ausgelegt für ~100 parallele Verbindungen.

- IP-basierte Zugriffskontrolle (HTTP localhost, HTTPS privates Netz)
- Modelle werden beim Start live von den Providern geladen, mit stabilen,
  einmal vergebenen Shortcodes
- Mehrere API-Keys pro Provider als Kanäle, mit Failover, Rate-Limiting und
  Health-Monitor
- Globaler und kanal-spezifischer Memory/System-Prompt, Sessions
- Persistentes Kosten-Tracking mit Tages-/Monats-Budget und Hard-Stop
- Anthropic-Messages-Bridge: jedes Modell auch für Claude Code nutzbar

> README_EN.md und README_CN.md sind seit 2026-10-12 eingefroren und werden
> nicht mehr gepflegt. Maßgeblich ist diese README mit `README.md.d/`.

## Schnellstart

```bash
make build                     # → ./build/sigoREST, ./build/sigoE, ./build/mockprovider
./build/sigoREST -v debug      # HTTP localhost:9080, HTTPS privates Netz :9443

curl -s http://localhost:9080/api/health
curl -s http://localhost:9080/api/shortcodes          # gültige Modell-Kürzel
curl -s http://localhost:9080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"mam-cl46-s","messages":[{"role":"user","content":"Hallo"}]}'

./build/sigoE -l                                      # CLI: Modelle auflisten
echo "Hallo" | ./build/sigoE -m gpt41
```

API-Keys kommen aus `./.env` oder der Umgebung, siehe
[Konfiguration](README.md.d/README_konfiguration.md#environment--api-keys).

## Dokumentation

| Thema | Inhalt |
|-------|--------|
| [Architektur](README.md.d/README_architektur.md) | Schichten, Verzeichnisstruktur, `sigoengine`-Exporte, Circuit Breaker, Sessions, Logging |
| [Konfiguration](README.md.d/README_konfiguration.md) | Server- und CLI-Flags, Zugriffskontrolle, API-Keys, Datenverzeichnis, `memory.json` |
| [API-Referenz](README.md.d/README_api.md) | Alle Endpoints, Request-Erweiterungen, Vision, Anthropic-Bridge |
| [Provider & Modelle](README.md.d/README_provider-modelle.md) | Dynamisches Laden, Provider-Eigenheiten, Shortcodes, Provider-Erkennung, CSV-Registry, Ollama, neue Provider |
| [Shortcode-Registry](README.md.d/README_shortcode-registry.md) | Persistente, einmal vergebene Shortcodes, Retirement |
| [Kanäle](README.md.d/README_kanaele.md) | Multi-Channel, Failover, Rate-Limiter, Health-Monitor |
| [Kosten & Budget](README.md.d/README_kosten-budget.md) | `/api/usage`, `/api/costs`, Budget mit Hard-Stop, Cache-Rabatt |
| [Clients](README.md.d/README_clients.md) | Eigene Client-Bibliotheken, OpenAI-SDKs, Claude Code |
| [Betrieb](README.md.d/README_betrieb.md) | Installation, systemd, DNS-Race, Kommunikationsprotokoll, Debugging |

Entwicklungs-Historie und Lektionen: [`RETROSPECTIVE.md`](RETROSPECTIVE.md).

## Entwicklung

```bash
make build          # alle: sigoREST, sigoE, mockprovider → ./build/
make sigorest       # nur REST-Server
make sigoe          # nur CLI
make mockprovider   # nur Mock-Provider (für Rate-Limit-Tests)
make test           # alle Tests
make clean          # ./build/ entfernen

go test ./sigoengine/ -v                       # mit Details
go test ./sigoengine/ -run TestFetchWithRetry  # einzelne Tests (Regex)
```

Go-Tests liegen in `sigoengine/` (u.a. `retry`, `shortcode`, `usage`,
`finish_reason`, `env`, `commlog`, `models_csv`). Der Server hat
Handler-Tests in `sigoREST/main_test.go` (u.a. mit Fake-Ollama
`startFakeOllama`); die CLI hat keine Go-Tests und wird manuell getestet.

Nach Server-Änderungen zusätzlich einen echten Call gegen einen Test-Server
auf freiem Port machen (`-http-port 19080 -https-port 19443 -data-dir <tmp>`),
weil der Live-Dienst 9080/9443 belegt.
