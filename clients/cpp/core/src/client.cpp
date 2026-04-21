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

    std::string reqBody = reqJson.dump();
    std::string url = baseUrl_ + "/v1/chat/completions";
    curl_easy_setopt(curl, CURLOPT_URL, url.c_str());
    curl_easy_setopt(curl, CURLOPT_POSTFIELDS, reqBody.c_str());
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
