//**********************************************************************
//      sigoREST/anthropic.go
//**********************************************************************
//  Beschreibung: Anthropic-Messages-API-Bridge (POST /v1/messages).
//                Übersetzt Anthropic-Wire-Format <-> internes
//                apiRequest-Format, das sigoengine.CallAPI/CallAPIStream
//                erwarten (dasselbe Format wie /v1/chat/completions nutzt).
//**********************************************************************

package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// AnthropicTool ist ein vom Client angebotenes Tool (Anthropic-Schema).
type AnthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// AnthropicContentBlock deckt die unterstützten Content-Block-Typen ab
// (text, tool_use, tool_result). Andere Typen (image, thinking) werden beim
// Parsen best-effort ignoriert (siehe Spec, "Out of scope").
type AnthropicContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`          // tool_use
	Name      string          `json:"name,omitempty"`        // tool_use
	Input     json.RawMessage `json:"input,omitempty"`       // tool_use
	ToolUseID string          `json:"tool_use_id,omitempty"` // tool_result
	Content   json.RawMessage `json:"content,omitempty"`     // tool_result
	IsError   bool            `json:"is_error,omitempty"`    // tool_result
}

// AnthropicMessage: content ist String ODER Block-Array, deshalb RawMessage.
type AnthropicMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// AnthropicRequest ist der Body von POST /v1/messages.
type AnthropicRequest struct {
	Model         string             `json:"model"`
	MaxTokens     int                `json:"max_tokens"`
	Messages      []AnthropicMessage `json:"messages"`
	System        json.RawMessage    `json:"system,omitempty"`
	Tools         []AnthropicTool    `json:"tools,omitempty"`
	ToolChoice    json.RawMessage    `json:"tool_choice,omitempty"`
	Temperature   *float64           `json:"temperature,omitempty"`
	TopP          *float64           `json:"top_p,omitempty"`
	StopSequences []string           `json:"stop_sequences,omitempty"`
	Stream        bool               `json:"stream,omitempty"`
}

// anthropicRequestToInternal übersetzt einen Anthropic-Messages-Request in
// das interne apiRequest-Format (dasselbe map[string]interface{}, das
// sigoengine.CallAPI/CallAPIStream erwarten). tools/toolChoice werden separat
// zurückgegeben, damit der Aufrufer sie nur bei Bedarf in die apiRequest-Map
// einträgt (leere tools[] soll das Feld nicht erzeugen).
func anthropicRequestToInternal(req *AnthropicRequest) (messages []map[string]interface{}, tools []map[string]interface{}, toolChoice interface{}, err error) {
	if len(req.System) > 0 {
		systemText, sErr := anthropicTextFromRaw(req.System)
		if sErr != nil {
			return nil, nil, nil, fmt.Errorf("system: %w", sErr)
		}
		if systemText != "" {
			messages = append(messages, map[string]interface{}{
				"role": "system", "content": systemText,
			})
		}
	}

	for i, m := range req.Messages {
		var blocks []AnthropicContentBlock
		var plainText string
		if uErr := json.Unmarshal(m.Content, &plainText); uErr == nil {
			blocks = []AnthropicContentBlock{{Type: "text", Text: plainText}}
		} else if uErr := json.Unmarshal(m.Content, &blocks); uErr != nil {
			return nil, nil, nil, fmt.Errorf("messages[%d].content: %w", i, uErr)
		}

		var textParts []string
		var toolCalls []map[string]interface{}
		var toolResultMsgs []map[string]interface{}

		for _, b := range blocks {
			switch b.Type {
			case "text":
				textParts = append(textParts, b.Text)
			case "tool_use":
				toolCalls = append(toolCalls, map[string]interface{}{
					"id":   b.ID,
					"type": "function",
					"function": map[string]interface{}{
						"name":      b.Name,
						"arguments": string(b.Input),
					},
				})
			case "tool_result":
				content, cErr := anthropicTextFromRaw(b.Content)
				if cErr != nil {
					return nil, nil, nil, fmt.Errorf("messages[%d] tool_result: %w", i, cErr)
				}
				toolResultMsgs = append(toolResultMsgs, map[string]interface{}{
					"role":         "tool",
					"tool_call_id": b.ToolUseID,
					"content":      content,
				})
			default:
				// "image", "thinking", unbekannte Typen: bewusst ignoriert (v1-Scope).
				continue
			}
		}

		// tool_result-Blocks zuerst (Provider erwarten die Tool-Antwort direkt
		// im Anschluss an den Assistant-Tool-Call).
		messages = append(messages, toolResultMsgs...)

		if len(textParts) == 0 && len(toolCalls) == 0 {
			continue
		}

		msg := map[string]interface{}{"role": m.Role}
		if len(textParts) > 0 {
			msg["content"] = strings.Join(textParts, "\n")
		} else {
			msg["content"] = nil
		}
		if len(toolCalls) > 0 {
			msg["tool_calls"] = toolCalls
		}
		messages = append(messages, msg)
	}

	for _, t := range req.Tools {
		tools = append(tools, map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.InputSchema,
			},
		})
	}

	if len(req.ToolChoice) > 0 {
		toolChoice, err = anthropicToolChoiceToInternal(req.ToolChoice)
		if err != nil {
			return nil, nil, nil, err
		}
	}

	return messages, tools, toolChoice, nil
}

// anthropicTextFromRaw extrahiert Text aus einem Anthropic-"content"-Feld,
// das entweder ein JSON-String oder ein Array von Content-Blocks ist. Bei
// einem Block-Array werden alle text-Blocks verkettet (newline-getrennt).
func anthropicTextFromRaw(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	var blocks []AnthropicContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", err
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n"), nil
}

// anthropicToolChoiceToInternal übersetzt Anthropics tool_choice-Schema
// ({"type":"auto"|"any"|"tool"|"none", "name"?}) ins OpenAI-Schema
// ("auto"|"required"|{"type":"function","function":{"name"}}|"none").
func anthropicToolChoiceToInternal(raw json.RawMessage) (interface{}, error) {
	var tc struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &tc); err != nil {
		return nil, fmt.Errorf("tool_choice: %w", err)
	}
	switch tc.Type {
	case "auto":
		return "auto", nil
	case "any":
		return "required", nil
	case "none":
		return "none", nil
	case "tool":
		return map[string]interface{}{
			"type":     "function",
			"function": map[string]interface{}{"name": tc.Name},
		}, nil
	default:
		return "auto", nil
	}
}
