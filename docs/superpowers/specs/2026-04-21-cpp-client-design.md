# Design: C++ Client Library mit Qt-Wrapper

## Status

Aktives Design-Dokument. Feature noch nicht implementiert.

## Ziel

C++17 Client fuer sigoREST mit optionalem Qt 6.9 Wrapper. Zwei Targets: Core (synchron) und Qt-Wrapper (asynchron).

## Motivation

Bestehende Clients (Python, Go, JS, CLisp) decken C++ nicht ab. Qt 6.9 Anwendung benoetigt asynchronen Zugriff auf sigoREST fuer Uebersetzungsmodul.

## Architektur

```
┌─────────────────┐     ┌─────────────────┐
│  sigorest-qt    │────▶│  sigorest-core  │
│  (QObject)      │     │  (HTTP + JSON)  │
└─────────────────┘     └─────────────────┘
        │                        │
        ▼                        ▼
   QNetworkAccessManager      libcurl
   QJsonObject                nlohmann/json
```

- **Core** ist eigenstaendig nutzbar (synchron, libcurl)
- **Qt-Wrapper** baut auf Core auf (asynchron, QNetworkAccessManager)

## API-Spezifikation

### Core API (synchron)

```cpp
namespace sigorest {
    struct ChatMessage {
        std::string role;
        std::string content;
    };

    struct ChatChoice {
        int index;
        ChatMessage message;
        std::string finish_reason;
    };

    struct ChatUsage {
        int prompt_tokens;
        int completion_tokens;
        int total_tokens;
    };

    struct ChatResponse {
        std::string id;
        std::vector<ChatChoice> choices;
        ChatUsage usage;
    };

    class Client {
    public:
        explicit Client(const std::string& baseUrl);
        ChatResponse chatCompletion(const std::string& model,
                                    const std::vector<ChatMessage>& messages,
                                    int maxTokens = 0,
                                    const std::string& systemPrompt = "");
    };
}
```

### Qt-Wrapper API (asynchron)

```cpp
namespace sigorest::qt {
    class Client : public QObject {
        Q_OBJECT
    public:
        explicit Client(const QString& baseUrl, QObject* parent = nullptr);
        void chatCompletion(const QString& model,
                            const QList<ChatMessage>& messages,
                            int maxTokens = 0,
                            const QString& systemPrompt = "");
    signals:
        void finished(const ChatResponse& response);
        void error(const QString& message);
    };
}
```

## Verzeichnisstruktur

```
clients/cpp/
├── CMakeLists.txt
├── core/
│   ├── include/sigorest/
│   │   ├── client.hpp
│   │   └── models.hpp
│   └── src/client.cpp
├── qt/
│   ├── include/sigorest/qt/
│   │   └── client.hpp
│   └── src/client.cpp
└── examples/
    ├── basic/         # Core, synchron
    └── qt-translator/ # Qt, asynchron
```

## Build-System

CMake 3.16+. Options:
- `SIGOREST_BUILD_QT=ON` (default ON, wenn Qt6 gefunden)
- `SIGOREST_BUILD_EXAMPLES=ON`

**Dependencies:**
- Core: libcurl, nlohmann/json (header-only)
- Qt: Qt6::Network, Qt6::Core

## Thread-Safety

- Core: **Nicht thread-safe**. Ein Client-Objekt pro Thread oder externe Synchronisation.
- Qt-Wrapper: Thread-affine (QObject). Requests laufen im Event-Loop-Thread.

## Test-Strategie

- Core: JSON-Serialisierung mit Catch2 testen
- Qt: Async-Completion mit QSignalSpy testen
- Manuell: Gegen laufenden sigoREST Server (localhost:9080)

## Aenderungshistorie

| Datum | Autor | Aenderung |
|-------|-------|-----------|
| 2026-04-21 | kimi | Initiales Design-Dokument |
