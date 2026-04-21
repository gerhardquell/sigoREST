# C++ Client Library mit Qt-Wrapper Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** C++17 Client fuer sigoREST mit optionalem Qt 6.9 Wrapper.

**Architecture:** Core (synchron, libcurl + nlohmann/json) + Qt-Wrapper (asynchron, QNetworkAccessManager).

**Tech Stack:** C++17, CMake 3.16+, libcurl, nlohmann/json, Qt6 (optional)

---

### Task 1: Verzeichnisstruktur und CMake-Setup

**Files:**
- Create: `clients/cpp/CMakeLists.txt`
- Create: `clients/cpp/core/CMakeLists.txt`
- Create: `clients/cpp/qt/CMakeLists.txt`
- Create: `clients/cpp/examples/basic/CMakeLists.txt`
- Create: `clients/cpp/examples/qt-translator/CMakeLists.txt`

- [ ] **Step 1: Haupt-CMakeLists.txt erstellen**

Erstelle `clients/cpp/CMakeLists.txt`:

```cmake
cmake_minimum_required(VERSION 3.16)
project(sigorest-cpp VERSION 1.0.0 LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

# Options
option(SIGOREST_BUILD_QT "Build Qt wrapper" ON)
option(SIGOREST_BUILD_EXAMPLES "Build examples" ON)

# Dependencies
find_package(CURL REQUIRED)
include(FetchContent)
FetchContent_Declare(
    json
    URL https://github.com/nlohmann/json/releases/download/v3.11.3/json.hpp
    DOWNLOAD_NO_EXTRACT TRUE
)
FetchContent_MakeAvailable(json)

# Core library
add_subdirectory(core)

# Qt wrapper
if(SIGOREST_BUILD_QT)
    find_package(Qt6 COMPONENTS Core Network)
    if(Qt6_FOUND)
        add_subdirectory(qt)
    else()
        message(WARNING "Qt6 not found, skipping Qt wrapper")
    endif()
endif()

# Examples
if(SIGOREST_BUILD_EXAMPLES)
    add_subdirectory(examples/basic)
    if(SIGOREST_BUILD_QT AND Qt6_FOUND)
        add_subdirectory(examples/qt-translator)
    endif()
endif()
```

- [ ] **Step 2: Core CMakeLists.txt erstellen**

Erstelle `clients/cpp/core/CMakeLists.txt`:

```cmake
add_library(sigorest-core STATIC)

target_sources(sigorest-core
    PUBLIC
        include/sigorest/models.hpp
        include/sigorest/client.hpp
    PRIVATE
        src/client.cpp
)

target_include_directories(sigorest-core
    PUBLIC
        ${CMAKE_CURRENT_SOURCE_DIR}/include
        ${json_SOURCE_DIR}
)

target_link_libraries(sigorest-core
    PUBLIC
        CURL::libcurl
)
```

- [ ] **Step 3: Qt CMakeLists.txt erstellen**

Erstelle `clients/cpp/qt/CMakeLists.txt`:

```cmake
add_library(sigorest-qt STATIC)

target_sources(sigorest-qt
    PUBLIC
        include/sigorest/qt/client.hpp
    PRIVATE
        src/client.cpp
)

target_include_directories(sigorest-qt
    PUBLIC
        ${CMAKE_CURRENT_SOURCE_DIR}/include
)

target_link_libraries(sigorest-qt
    PUBLIC
        sigorest-core
        Qt6::Core
        Qt6::Network
)
```

- [ ] **Step 4: Examples CMakeLists.txt erstellen**

Erstelle `clients/cpp/examples/basic/CMakeLists.txt`:

```cmake
add_executable(sigorest-basic main.cpp)
target_link_libraries(sigorest-basic PRIVATE sigorest-core)
```

Erstelle `clients/cpp/examples/qt-translator/CMakeLists.txt`:

```cmake
add_executable(sigorest-qt-translator main.cpp)
target_link_libraries(sigorest-qt-translator PRIVATE sigorest-qt)
set_target_properties(sigorest-qt-translator PROPERTIES AUTOMOC ON)
```

- [ ] **Step 5: Verzeichnisstruktur anlegen**

Run:
```bash
mkdir -p clients/cpp/core/include/sigorest
mkdir -p clients/cpp/core/src
mkdir -p clients/cpp/qt/include/sigorest/qt
mkdir -p clients/cpp/qt/src
mkdir -p clients/cpp/examples/basic
mkdir -p clients/cpp/examples/qt-translator
```

- [ ] **Step 6: Commit**

```bash
git add clients/cpp/
git commit -m "$(cat <<'EOF'
build: CMake-Setup fuer C++ Client Library

Verzeichnisstruktur, CMakeLists.txt fuer Core, Qt und Examples.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

### Task 2: Core Models (Header)

**Files:**
- Create: `clients/cpp/core/include/sigorest/models.hpp`

- [ ] **Step 1: models.hpp schreiben**

Erstelle `clients/cpp/core/include/sigorest/models.hpp`:

```cpp
#pragma once

#include <string>
#include <vector>

namespace sigorest {

struct ChatMessage {
    std::string role;
    std::string content;
};

struct ChatChoice {
    int index = 0;
    ChatMessage message;
    std::string finish_reason;
};

struct ChatUsage {
    int prompt_tokens = 0;
    int completion_tokens = 0;
    int total_tokens = 0;
};

struct ChatResponse {
    std::string id;
    std::vector<ChatChoice> choices;
    ChatUsage usage;
};

} // namespace sigorest
```

- [ ] **Step 2: Commit**

```bash
git add clients/cpp/core/include/sigorest/models.hpp
git commit -m "$(cat <<'EOF'
feat: Core Models fuer C++ Client

ChatMessage, ChatChoice, ChatUsage, ChatResponse.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

### Task 3: Core Client Implementierung

**Files:**
- Create: `clients/cpp/core/include/sigorest/client.hpp`
- Create: `clients/cpp/core/src/client.cpp`

- [ ] **Step 1: client.hpp schreiben**

Erstelle `clients/cpp/core/include/sigorest/client.hpp`:

```cpp
#pragma once

#include "sigorest/models.hpp"
#include <string>
#include <vector>

namespace sigorest {

class Client {
public:
    explicit Client(const std::string& baseUrl);

    ChatResponse chatCompletion(const std::string& model,
                                const std::vector<ChatMessage>& messages,
                                int maxTokens = 0,
                                const std::string& systemPrompt = "");

private:
    std::string baseUrl_;
};

} // namespace sigorest
```

- [ ] **Step 2: client.cpp schreiben**

Erstelle `clients/cpp/core/src/client.cpp`:

```cpp
#include "sigorest/client.hpp"
#include <nlohmann/json.hpp>
#include <curl/curl.h>
#include <sstream>
#include <stdexcept>

namespace sigorest {

using json = nlohmann::json;

static size_t WriteCallback(void* contents, size_t size, size_t nmemb, void* userp) {
    ((std::string*)userp)->append((char*)contents, size * nmemb);
    return size * nmemb;
}

Client::Client(const std::string& baseUrl) : baseUrl_(baseUrl) {
    curl_global_init(CURL_GLOBAL_DEFAULT);
}

ChatResponse Client::chatCompletion(const std::string& model,
                                    const std::vector<ChatMessage>& messages,
                                    int maxTokens,
                                    const std::string& systemPrompt) {
    json reqJson;
    reqJson["model"] = model;
    reqJson["messages"] = json::array();
    for (const auto& msg : messages) {
        reqJson["messages"].push_back({{"role", msg.role}, {"content", msg.content}});
    }
    if (maxTokens > 0) {
        reqJson["max_tokens"] = maxTokens;
    }
    if (!systemPrompt.empty()) {
        reqJson["system_prompt"] = systemPrompt;
    }

    std::string responseStr;
    CURL* curl = curl_easy_init();
    if (!curl) {
        throw std::runtime_error("Failed to initialize CURL");
    }

    struct curl_slist* headers = nullptr;
    headers = curl_slist_append(headers, "Content-Type: application/json");

    std::string url = baseUrl_ + "/v1/chat/completions";
    curl_easy_setopt(curl, CURLOPT_URL, url.c_str());
    curl_easy_setopt(curl, CURLOPT_POSTFIELDS, reqJson.dump().c_str());
    curl_easy_setopt(curl, CURLOPT_HTTPHEADER, headers);
    curl_easy_setopt(curl, CURLOPT_WRITEFUNCTION, WriteCallback);
    curl_easy_setopt(curl, CURLOPT_WRITEDATA, &responseStr);
    curl_easy_setopt(curl, CURLOPT_TIMEOUT, 180L);

    CURLcode res = curl_easy_perform(curl);
    curl_slist_free_all(headers);
    curl_easy_cleanup(curl);

    if (res != CURLE_OK) {
        throw std::runtime_error(std::string("CURL error: ") + curl_easy_strerror(res));
    }

    json respJson = json::parse(responseStr);
    if (respJson.contains("error")) {
        throw std::runtime_error(respJson["error"]["message"].get<std::string>());
    }

    ChatResponse response;
    response.id = respJson.value("id", "");

    if (respJson.contains("choices") && respJson["choices"].is_array()) {
        for (const auto& choice : respJson["choices"]) {
            ChatChoice c;
            c.index = choice.value("index", 0);
            if (choice.contains("message")) {
                c.message.role = choice["message"].value("role", "");
                c.message.content = choice["message"].value("content", "");
            }
            c.finish_reason = choice.value("finish_reason", "");
            response.choices.push_back(c);
        }
    }

    if (respJson.contains("usage")) {
        response.usage.prompt_tokens = respJson["usage"].value("prompt_tokens", 0);
        response.usage.completion_tokens = respJson["usage"].value("completion_tokens", 0);
        response.usage.total_tokens = respJson["usage"].value("total_tokens", 0);
    }

    return response;
}

} // namespace sigorest
```

- [ ] **Step 3: Build pruefen**

Run:
```bash
cd clients/cpp && mkdir -p build && cd build && cmake .. && make sigorest-core -j$(nproc)
```

Expected: `sigorest-core` compiles without errors.

- [ ] **Step 4: Commit**

```bash
git add clients/cpp/core/
git commit -m "$(cat <<'EOF'
feat: Core Client Implementierung (synchron, libcurl)

chatCompletion mit JSON-Serialisierung und Response-Parsing.
Unterstuetzt model, messages, max_tokens, system_prompt.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

### Task 4: Qt-Wrapper Implementierung

**Files:**
- Create: `clients/cpp/qt/include/sigorest/qt/client.hpp`
- Create: `clients/cpp/qt/src/client.cpp`

- [ ] **Step 1: Qt client.hpp schreiben**

Erstelle `clients/cpp/qt/include/sigorest/qt/client.hpp`:

```cpp
#pragma once

#include <QObject>
#include <QString>
#include <QList>
#include "sigorest/models.hpp"

namespace sigorest::qt {

struct ChatMessage {
    QString role;
    QString content;
};

struct ChatResponse {
    QString id;
    QList<ChatChoice> choices;
    ChatUsage usage;
};

struct ChatChoice {
    int index = 0;
    ChatMessage message;
    QString finish_reason;
};

struct ChatUsage {
    int prompt_tokens = 0;
    int completion_tokens = 0;
    int total_tokens = 0;
};

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

private:
    QString baseUrl_;
};

} // namespace sigorest::qt
```

- [ ] **Step 2: Qt client.cpp schreiben**

Erstelle `clients/cpp/qt/src/client.cpp`:

```cpp
#include "sigorest/qt/client.hpp"
#include <QNetworkAccessManager>
#include <QNetworkRequest>
#include <QNetworkReply>
#include <QJsonDocument>
#include <QJsonObject>
#include <QJsonArray>
#include <QUrl>

namespace sigorest::qt {

Client::Client(const QString& baseUrl, QObject* parent)
    : QObject(parent), baseUrl_(baseUrl) {}

void Client::chatCompletion(const QString& model,
                            const QList<ChatMessage>& messages,
                            int maxTokens,
                            const QString& systemPrompt) {
    QJsonObject reqJson;
    reqJson["model"] = model;
    QJsonArray msgs;
    for (const auto& msg : messages) {
        QJsonObject m;
        m["role"] = msg.role;
        m["content"] = msg.content;
        msgs.append(m);
    }
    reqJson["messages"] = msgs;
    if (maxTokens > 0) {
        reqJson["max_tokens"] = maxTokens;
    }
    if (!systemPrompt.isEmpty()) {
        reqJson["system_prompt"] = systemPrompt;
    }

    QNetworkAccessManager* mgr = new QNetworkAccessManager(this);
    QNetworkRequest req(QUrl(baseUrl_ + "/v1/chat/completions"));
    req.setHeader(QNetworkRequest::ContentTypeHeader, "application/json");

    QJsonDocument doc(reqJson);
    QNetworkReply* reply = mgr->post(req, doc.toJson());

    connect(reply, &QNetworkReply::finished, this, [this, reply]() {
        reply->deleteLater();
        reply->.manager()->deleteLater();

        if (reply->error() != QNetworkReply::NoError) {
            emit error(reply->errorString());
            return;
        }

        QJsonDocument respDoc = QJsonDocument::fromJson(reply->readAll());
        QJsonObject respJson = respDoc.object();

        if (respJson.contains("error")) {
            emit error(respJson["error"].toObject()["message"].toString());
            return;
        }

        ChatResponse response;
        response.id = respJson["id"].toString();

        QJsonArray choices = respJson["choices"].toArray();
        for (const auto& choiceVal : choices) {
            QJsonObject choice = choiceVal.toObject();
            ChatChoice c;
            c.index = choice["index"].toInt();
            QJsonObject msg = choice["message"].toObject();
            c.message.role = msg["role"].toString();
            c.message.content = msg["content"].toString();
            c.finish_reason = choice["finish_reason"].toString();
            response.choices.append(c);
        }

        QJsonObject usage = respJson["usage"].toObject();
        response.usage.prompt_tokens = usage["prompt_tokens"].toInt();
        response.usage.completion_tokens = usage["completion_tokens"].toInt();
        response.usage.total_tokens = usage["total_tokens"].toInt();

        emit finished(response);
    });
}

} // namespace sigorest::qt
```

- [ ] **Step 3: Build pruefen**

Run:
```bash
cd clients/cpp/build && cmake .. && make sigorest-qt -j$(nproc)
```

Expected: `sigorest-qt` compiles without errors.

- [ ] **Step 4: Commit**

```bash
git add clients/cpp/qt/
git commit -m "$(cat <<'EOF'
feat: Qt-Wrapper Implementierung (asynchron)

QNetworkAccessManager, Signals/Slots fuer async Completion.
ChatResponse/ChatChoice als Qt-Varianten mit QString/QList.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

### Task 5: Beispiele

**Files:**
- Create: `clients/cpp/examples/basic/main.cpp`
- Create: `clients/cpp/examples/qt-translator/main.cpp`

- [ ] **Step 1: Basic Example schreiben**

Erstelle `clients/cpp/examples/basic/main.cpp`:

```cpp
#include <iostream>
#include "sigorest/client.hpp"

int main() {
    try {
        sigorest::Client client("http://127.0.0.1:9080");
        auto response = client.chatCompletion("gpt41", {
            {"user", "Hallo Welt"}
        }, 50);

        if (!response.choices.empty()) {
            std::cout << "Antwort: " << response.choices[0].message.content << std::endl;
            std::cout << "Finish: " << response.choices[0].finish_reason << std::endl;
            std::cout << "Tokens: " << response.usage.total_tokens << std::endl;
        }
    } catch (const std::exception& e) {
        std::cerr << "Fehler: " << e.what() << std::endl;
        return 1;
    }
    return 0;
}
```

- [ ] **Step 2: Qt-Translator Example schreiben**

Erstelle `clients/cpp/examples/qt-translator/main.cpp`:

```cpp
#include <QCoreApplication>
#include <QTimer>
#include <QDebug>
#include "sigorest/qt/client.hpp"

int main(int argc, char* argv[]) {
    QCoreApplication app(argc, argv);

    sigorest::qt::Client client("http://127.0.0.1:9080", &app);

    QObject::connect(&client, &sigorest::qt::Client::finished, [](const sigorest::qt::ChatResponse& resp) {
        if (!resp.choices.isEmpty()) {
            qDebug() << "Antwort:" << resp.choices[0].message.content;
            qDebug() << "Finish:" << resp.choices[0].finish_reason;
            qDebug() << "Tokens:" << resp.usage.total_tokens;
        }
        QCoreApplication::quit();
    });

    QObject::connect(&client, &sigorest::qt::Client::error, [](const QString& msg) {
        qWarning() << "Fehler:" << msg;
        QCoreApplication::quit();
    });

    client.chatCompletion("gpt41", {{"user", "Hallo Welt"}}, 50);

    QTimer::singleShot(60000, &app, &QCoreApplication::quit);
    return app.exec();
}
```

- [ ] **Step 3: Build pruefen**

Run:
```bash
cd clients/cpp/build && cmake .. && make -j$(nproc)
```

Expected: Both examples compile.

- [ ] **Step 4: Commit**

```bash
git add clients/cpp/examples/
git commit -m "$(cat <<'EOF'
feat: Beispiele fuer C++ Client (basic + qt-translator)

Basic: synchroner Core-Client.
Qt-Translator: asynchroner Qt-Wrapper mit Event-Loop.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

### Task 6: Manueller Test

**Files:**
- Keine Code-Aenderungen

- [ ] **Step 1: sigoREST Server starten**

Run: `./sigoREST/sigoREST -v debug`

- [ ] **Step 2: Basic Example testen**

Run:
```bash
cd clients/cpp/build && ./examples/basic/sigorest-basic
```

Expected: Ausgabe mit Antwort, finish_reason="stop", Token-Anzahl.

- [ ] **Step 3: Qt-Translator Example testen**

Run:
```bash
cd clients/cpp/build && ./examples/qt-translator/sigorest-qt-translator
```

Expected: Gleiche Ausgabe, aber asynchron ueber Qt Event-Loop.

---

## Self-Review

**Spec coverage:**
- [x] Verzeichnisstruktur + CMake → Task 1
- [x] Core Models → Task 2
- [x] Core Client (synchron) → Task 3
- [x] Qt-Wrapper (asynchron) → Task 4
- [x] Beispiele → Task 5
- [x] Manuelle Verifikation → Task 6

**Placeholder scan:** Keine TBD/TODO. Alle Code-Bloecke vollstaendig.

**Type consistency:**
- Core: `std::string`, `std::vector` → konsistent
- Qt: `QString`, `QList` → konsistent
- Build: `SIGOREST_BUILD_QT`, `SIGOREST_BUILD_EXAMPLES` → CMake-Optionen konsistent
