#include "sigorest/qt/client.hpp"
#include <QJsonDocument>
#include <QJsonObject>
#include <QJsonArray>
#include <QNetworkRequest>

namespace sigorest {

QtClient::QtClient(const QString& baseUrl, QObject* parent)
    : QObject(parent), baseUrl_(baseUrl), networkManager_(new QNetworkAccessManager(this)) {
}

QtClient::~QtClient() {
    if (currentReply_) {
        currentReply_->abort();
        clearReply();
    }
}

void QtClient::clearReply() {
    if (currentReply_) {
        disconnect(currentReply_, nullptr, this, nullptr);
        currentReply_->deleteLater();
        currentReply_ = nullptr;
    }
}

void QtClient::chatCompletion(const QString& model,
                              const QList<ChatMessage>& messages,
                              int maxTokens,
                              const QString& systemPrompt) {
    if (currentReply_) {
        emit error("Request already in progress");
        return;
    }

    QJsonObject reqJson;
    reqJson["model"] = model;

    QJsonArray msgArray;
    for (const auto& msg : messages) {
        QJsonObject m;
        m["role"] = QString::fromStdString(msg.role);
        m["content"] = QString::fromStdString(msg.content);
        msgArray.append(m);
    }
    reqJson["messages"] = msgArray;

    if (maxTokens > 0) {
        reqJson["max_tokens"] = maxTokens;
    }
    if (!systemPrompt.isEmpty()) {
        reqJson["system_prompt"] = systemPrompt;
    }

    QNetworkRequest request(QUrl(baseUrl_ + "/v1/chat/completions"));
    request.setHeader(QNetworkRequest::ContentTypeHeader, "application/json");

    QJsonDocument doc(reqJson);
    currentReply_ = networkManager_->post(request, doc.toJson());
    connect(currentReply_, &QNetworkReply::finished, this, &QtClient::onReplyFinished);
}

void QtClient::onReplyFinished() {
    if (!currentReply_) return;

    if (currentReply_->error() != QNetworkReply::NoError) {
        emit error(currentReply_->errorString());
        clearReply();
        return;
    }

    QByteArray data = currentReply_->readAll();
    QJsonDocument doc = QJsonDocument::fromJson(data);
    if (doc.isNull()) {
        emit error("Invalid JSON response");
        clearReply();
        return;
    }

    QJsonObject respJson = doc.object();
    if (respJson.contains("error")) {
        QJsonObject errObj = respJson["error"].toObject();
        emit error(errObj["message"].toString());
        clearReply();
        return;
    }

    ChatResponse response;
    response.id = respJson["id"].toString().toStdString();

    QJsonArray choices = respJson["choices"].toArray();
    for (const auto& val : choices) {
        QJsonObject choice = val.toObject();
        ChatChoice c;
        c.index = choice["index"].toInt(0);
        QJsonObject msgObj = choice["message"].toObject();
        c.message.role = msgObj["role"].toString().toStdString();
        c.message.content = msgObj["content"].toString().toStdString();
        c.finish_reason = choice["finish_reason"].toString().toStdString();
        response.choices.push_back(c);
    }

    QJsonObject usage = respJson["usage"].toObject();
    response.usage.prompt_tokens = usage["prompt_tokens"].toInt(0);
    response.usage.completion_tokens = usage["completion_tokens"].toInt(0);
    response.usage.total_tokens = usage["total_tokens"].toInt(0);

    emit completed(response);
    clearReply();
}

} // namespace sigorest
