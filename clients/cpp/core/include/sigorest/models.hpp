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

struct ChatChunkDelta {
    std::string role;
    std::string content;
};

struct ChatChunkChoice {
    int index = 0;
    ChatChunkDelta delta;
    std::string finish_reason;
};

struct ChatCompletionChunk {
    std::string id;
    std::string object;
    long long created = 0;
    std::string model;
    std::vector<ChatChunkChoice> choices;
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
