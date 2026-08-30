# TODO 20260830

## Ergänzung um longcat

Wir wollen sigoREST um longcat ergänzen.

## Beispiel von longcat
```
curl -X POST https://api.longcat.ai/openai/v1/chat/completions \
  -H "Authorization: Bearer YOUR_APP_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "LongCat-2.0",
    "messages": [{"role": "user", "content": "Hello!"}],
    "max_tokens": 1000
  }'
```

## API_KEYs:

Die Keys lauten:
- LONGCAT_API_KEY
- LONGCAT_API_KEY_1
- LONGCAT_API_KEY_2
- LONGCAT_API_KEY_3
- LONGCAT_API_KEY_4

