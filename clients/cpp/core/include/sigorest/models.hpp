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
