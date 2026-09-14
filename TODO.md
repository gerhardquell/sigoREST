# TODO 20260830

\plan

\brainstorming

## 1. Ergänzung um longcat

Wir wollen sigoREST um longcat ergänzen.

### Beispiel von longcat
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

### API_KEYs:

Die Keys lauten:
- LONGCAT_API_KEY
- LONGCAT_API_KEY_1
- LONGCAT_API_KEY_2
- LONGCAT_API_KEY_3
- LONGCAT_API_KEY_4


## 2. Ergänzung um cheaperinference

platform.cheaperinference.com zeigt die möglichen Modelle. Zusätzlich
werden die preise der input- und output-tokens, größe des kontextfensters
angegeben.

Wenn möglich, wollen wir die preise mit berücksichtigen.

### Beispiel

```
curl https://api.cheaperinference.com/v1/responses \
  -H "Authorization: Bearer ci_live_YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-5.4",
    "input": "Hello!",
    "store": false,
    "stream": true
  }'
```

### API_KEYS:

Der Key lautet:
- OMNIROUTE_API_KEY


## 3. Anbindung an ClaudeCode [ERLEDIGT]

`POST /v1/messages` implementiert (Anthropic-Messages-Bridge), gemerged nach
`main`. Server nun direkt per `ANTHROPIC_BASE_URL` mit Claude Code nutzbar,
`claude-clomni`-Script darunter nicht mehr nötig (sigoREST übernimmt die
Provider-Anbindung).

Ich möchte sigoREST auch mit ClaudeCode nutzen können. 

Ich benutze bisher folgendes Script:

/usr/local/bin/claude-clomni

```
#!/bin/bash
datum=`date`
prompt="<IMPORTAN>speak german and say Du to the user gerhard</IMPORTANT>Du bist claude, ein genialer pragmatischer Software-Ingenieur mit Humor\nLese CLAUDE.md und vergewissere Dich, dass Du die Projektbeschränkungen verstanden hast, bevor du irgendetwas unternimmst. Wenn keine CLAUDE.md vorhanden ist erstelle sie neu und erfrage die Projektbeschränkungen. Danach lese TODO.md und RETROSPECTIVE.md, wenn sie im Verzeichnis liegen.\nDatumZeit: ${datum}\n"

unset ANTHROPIC_API_KEY
export ANTHROPIC_BASE_URL="https://api.cheaperinference.com"
export ANTHROPIC_AUTH_TOKEN=${OMNIROUTE_API_KEY}
export ANTHROPIC_MODEL="claude-opus-5"
export ANTHROPIC_DEFAULT_FABLE_MODEL="claude-fable-5.1"
export ANTHROPIC_DEFAULT_OPUS_MODEL="claude-opus-5"
export ANTHROPIC_DEFAULT_SONNET_MODEL="claude-sonnet-5"
export ANTHROPIC_SMALL_FAST_MODEL="claude-haiku-4.5"

export CLAUDE_CODE_AUTO_COMPACT_WINDOW="1000000"
# export CLAUDE_CODE_MAX_CONTEXT_TOKENS="262144"
export ENABLE_TOOL_SEARCH="true"
export CLAUDE_CODE_EFFORT_LEVEL="high"

echo "OMNIR-Start: ${datum}" >>zeitstempel.txt
claude "${prompt}"
datum=`date`
echo "OMNIR-Ende: ${datum}" >>zeitstempel.txt
```
