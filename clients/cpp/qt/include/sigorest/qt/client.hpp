#pragma once

#include "sigorest/models.hpp"
#include <QObject>
#include <QNetworkAccessManager>
#include <QNetworkReply>
#include <QJsonObject>
#include <QJsonArray>
#include <memory>

namespace sigorest {

class QtClient : public QObject {
    Q_OBJECT

public:
    explicit QtClient(const QString& baseUrl, QObject* parent = nullptr);

    void chatCompletion(const QString& model,
                        const QList<ChatMessage>& messages,
                        int maxTokens = 0,
                        const QString& systemPrompt = QString());

signals:
    void completed(const ChatResponse& response);
    void error(const QString& message);

private slots:
    void onReplyFinished();

private:
    QString baseUrl_;
    QNetworkAccessManager* networkManager_;
    QNetworkReply* currentReply_ = nullptr;
};

} // namespace sigorest
