#include "sigorest/client.hpp"
#include <iostream>

int main(int argc, char* argv[]) {
    std::string baseUrl = "http://localhost:9080";
    std::string model = "glm47";
    if (argc > 1) {
        baseUrl = argv[1];
    }
    if (argc > 2) {
        model = argv[2];
    }

    sigorest::Client client(baseUrl);

    std::vector<sigorest::ChatMessage> messages = {
        {"user", "Say hello in German"}
    };

    try {
        auto response = client.chatCompletion(model, messages, 500);
        if (!response.choices.empty()) {
            std::cout << "Response: " << response.choices[0].message.content << std::endl;
            std::cout << "Finish reason: " << response.choices[0].finish_reason << std::endl;
        }
        std::cout << "Tokens used: " << response.usage.total_tokens << std::endl;
    } catch (const std::exception& e) {
        std::cerr << "Error: " << e.what() << std::endl;
        return 1;
    }

    return 0;
}
