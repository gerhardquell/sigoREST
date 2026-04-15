# Ergänzung um automatischen Modellabruf

## 1. aufgabe
Wir wollen von mammouth die aktuellen Modell life abrufen. Verwendet werden 
soll der API-Aufruf:

https://api.mammouth.ai/public/models

Dazu noch die Moonshot-Modelle - hier ein pythonaufruf:
import requests
url = "https://api.moonshot.ai/v1/models"
headers = {"Authorization": "Bearer <token>"}
response = requests.get(url, headers=headers)
print(response.text)

Und die ZAI-Modelle statisch glm-5.1, glm-4.5-air, glm-4.7

Und zusätzlich sollen alle ollama-modelle verfügbar sein. 

## 2. Aufgabe
für jedes modell sollen die Parameter wie max_token ermittelt werden und 
über die restAPI abrufbar sein.

## 3. Aufgabe
Bevor ein Modell aufgerufen wird, soll es den Server anpingen, bzw. feststellen,
ob der Server des Modells bereit ist.

## 4. Aufgabe
Der Standardprompt soll durch einen eigenen Prompt ersetzt werden können. 


