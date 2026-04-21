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
