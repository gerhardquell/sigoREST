#include "sigorest/qt/client.hpp"
#include <QCoreApplication>
#include <QTimer>
#include <iostream>

int main(int argc, char* argv[]) {
    QCoreApplication app(argc, argv);

    QString baseUrl = "http://localhost:9080";
    QString model = "glm47";
    if (argc > 1) {
        baseUrl = QString::fromUtf8(argv[1]);
    }
    if (argc > 2) {
        model = QString::fromUtf8(argv[2]);
    }

    sigorest::QtClient client(baseUrl, &app);

    QObject::connect(&client, &sigorest::QtClient::completed, [&](const sigorest::ChatResponse& response) {
        if (!response.choices.empty()) {
            std::cout << "Translation: " << response.choices[0].message.content << std::endl;
            std::cout << "Finish reason: " << response.choices[0].finish_reason << std::endl;
        }
        std::cout << "Tokens used: " << response.usage.total_tokens << std::endl;
        app.quit();
    });

    QObject::connect(&client, &sigorest::QtClient::error, [&](const QString& message) {
        std::cerr << "Error: " << message.toStdString() << std::endl;
        app.exit(1);
    });

    QList<sigorest::ChatMessage> messages = {
        {"user", "Translate 'Hello world' to German"}
    };

    QTimer::singleShot(0, &client, [&]() {
        client.chatCompletion(model, messages, 500);
    });

    return app.exec();
}
