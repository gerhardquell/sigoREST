#include "sigorest/client.hpp"
#include <nlohmann/json.hpp>
#include <curl/curl.h>
#include <functional>
#include <mutex>
#include <stdexcept>
#include <sstream>

namespace sigorest {

using json = nlohmann::json;

constexpr long DEFAULT_TIMEOUT_SECONDS = 180L;

static size_t WriteCallback(void* contents, size_t size, size_t nmemb, void* userp) {
    ((std::string*)userp)->append((char*)contents, size * nmemb);
    return size * nmemb;
}

struct StreamContext {
    std::string buffer;
    std::function<void(const ChatCompletionChunk&)> onChunk;
};

static void parseAndEmitSSELine(const std::string& line,
                                const std::function<void(const ChatCompletionChunk&)>& onChunk) {
    if (line.rfind("data: ", 0) != 0) {
        return;
    }
    std::string data = line.substr(6);
    while (!data.empty() && (data.front() == ' ' || data.front() == '\t')) {
        data.erase(data.begin());
    }
    if (data == "[DONE]") {
        return;
    }
    try {
        json chunkJson = json::parse(data);
        ChatCompletionChunk chunk;
        chunk.id = chunkJson.value("id", "");
        chunk.object = chunkJson.value("object", "");
        chunk.created = chunkJson.value("created", 0LL);
        chunk.model = chunkJson.value("model", "");
        if (chunkJson.contains("choices") && chunkJson["choices"].is_array()) {
            for (const auto& choice : chunkJson["choices"]) {
                ChatChunkChoice c;
                c.index = choice.value("index", 0);
                if (choice.contains("delta")) {
                    c.delta.role = choice["delta"].value("role", "");
                    c.delta.content = choice["delta"].value("content", "");
                }
                c.finish_reason = choice.value("finish_reason", "");
                chunk.choices.push_back(c);
            }
        }
        onChunk(chunk);
    } catch (...) {
        // Malformed chunk; skip.
    }
}

static size_t StreamWriteCallback(void* contents, size_t size, size_t nmemb, void* userp) {
    auto* ctx = static_cast<StreamContext*>(userp);
    ctx->buffer.append(static_cast<char*>(contents), size * nmemb);

    std::size_t pos;
    while ((pos = ctx->buffer.find('\n')) != std::string::npos) {
        std::string line = ctx->buffer.substr(0, pos);
        ctx->buffer.erase(0, pos + 1);

        while (!line.empty() && (line.back() == '\r' || line.back() == ' ' || line.back() == '\t')) {
            line.pop_back();
        }
        parseAndEmitSSELine(line, ctx->onChunk);
    }
    return size * nmemb;
}

Client::Client(const std::string& baseUrl) : baseUrl_(baseUrl) {
    static std::once_flag curlInitFlag;
    std::call_once(curlInitFlag, []() {
        curl_global_init(CURL_GLOBAL_DEFAULT);
    });
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

    std::string reqBody = reqJson.dump();
    std::string url = baseUrl_ + "/v1/chat/completions";
    curl_easy_setopt(curl, CURLOPT_URL, url.c_str());
    curl_easy_setopt(curl, CURLOPT_POSTFIELDS, reqBody.c_str());
    curl_easy_setopt(curl, CURLOPT_HTTPHEADER, headers);
    curl_easy_setopt(curl, CURLOPT_WRITEFUNCTION, WriteCallback);
    curl_easy_setopt(curl, CURLOPT_WRITEDATA, &responseStr);
    curl_easy_setopt(curl, CURLOPT_TIMEOUT, DEFAULT_TIMEOUT_SECONDS);

    CURLcode res = curl_easy_perform(curl);

    long httpCode = 0;
    if (res == CURLE_OK) {
        curl_easy_getinfo(curl, CURLINFO_RESPONSE_CODE, &httpCode);
    }

    curl_slist_free_all(headers);
    curl_easy_cleanup(curl);

    if (res != CURLE_OK) {
        throw std::runtime_error(std::string("CURL error: ") + curl_easy_strerror(res));
    }

    if (httpCode >= 400) {
        throw std::runtime_error("HTTP error: " + std::to_string(httpCode));
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

void Client::chatCompletionStream(const std::string& model,
                                  const std::vector<ChatMessage>& messages,
                                  std::function<void(const ChatCompletionChunk&)> onChunk,
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
    reqJson["stream"] = true;

    CURL* curl = curl_easy_init();
    if (!curl) {
        throw std::runtime_error("Failed to initialize CURL");
    }

    struct curl_slist* headers = nullptr;
    headers = curl_slist_append(headers, "Content-Type: application/json");
    headers = curl_slist_append(headers, "Accept: text/event-stream");

    std::string reqBody = reqJson.dump();
    std::string url = baseUrl_ + "/v1/chat/completions";

    StreamContext ctx;
    ctx.onChunk = std::move(onChunk);

    curl_easy_setopt(curl, CURLOPT_URL, url.c_str());
    curl_easy_setopt(curl, CURLOPT_POSTFIELDS, reqBody.c_str());
    curl_easy_setopt(curl, CURLOPT_HTTPHEADER, headers);
    curl_easy_setopt(curl, CURLOPT_WRITEFUNCTION, StreamWriteCallback);
    curl_easy_setopt(curl, CURLOPT_WRITEDATA, &ctx);
    curl_easy_setopt(curl, CURLOPT_TIMEOUT, DEFAULT_TIMEOUT_SECONDS);

    CURLcode res = curl_easy_perform(curl);

    long httpCode = 0;
    if (res == CURLE_OK) {
        curl_easy_getinfo(curl, CURLINFO_RESPONSE_CODE, &httpCode);
    }

    curl_slist_free_all(headers);
    curl_easy_cleanup(curl);

    if (res != CURLE_OK) {
        throw std::runtime_error(std::string("CURL error: ") + curl_easy_strerror(res));
    }

    if (httpCode >= 400) {
        throw std::runtime_error("HTTP error: " + std::to_string(httpCode));
    }

    // Flush any trailing partial line.
    if (!ctx.buffer.empty()) {
        std::string line = ctx.buffer;
        while (!line.empty() && (line.back() == '\r' || line.back() == ' ' || line.back() == '\t')) {
            line.pop_back();
        }
        parseAndEmitSSELine(line, ctx.onChunk);
    }
}

} // namespace sigorest
