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
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"sigorest/sigoengine"
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

// AnthropicUsage ist das usage-Objekt der Anthropic-Response.
type AnthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// AnthropicResponse ist der Body einer nicht-gestreamten POST /v1/messages
// Antwort.
type AnthropicResponse struct {
	ID         string                  `json:"id"`
	Type       string                  `json:"type"`
	Role       string                  `json:"role"`
	Model      string                  `json:"model"`
	Content    []AnthropicContentBlock `json:"content"`
	StopReason string                  `json:"stop_reason,omitempty"`
	Usage      AnthropicUsage          `json:"usage"`
}

// finishReasonToStopReason mappt OpenAI/Anthropic finish_reason-Werte auf
// Anthropics stop_reason-Vokabular. hasToolCalls hat Vorrang, weil manche
// Provider bei Tool-Calls trotzdem finish_reason="stop" liefern.
func finishReasonToStopReason(finishReason string, hasToolCalls bool) string {
	if hasToolCalls {
		return "tool_use"
	}
	switch finishReason {
	case "length":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	case "stop_sequence":
		return "stop_sequence"
	default:
		return "end_turn"
	}
}

// internalToAnthropicResponse baut die Anthropic-Response aus dem
// normalisierten Ergebnis eines CallAPI-Aufrufs.
func internalToAnthropicResponse(model, text string, toolCalls []sigoengine.ToolCall, usage *sigoengine.UsageData, finishReason string) *AnthropicResponse {
	var content []AnthropicContentBlock
	if text != "" {
		content = append(content, AnthropicContentBlock{Type: "text", Text: text})
	}
	for _, tc := range toolCalls {
		input := json.RawMessage(tc.Function.Arguments)
		if !json.Valid(input) {
			input = json.RawMessage("{}")
		}
		content = append(content, AnthropicContentBlock{
			Type:  "tool_use",
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: input,
		})
	}

	resp := &AnthropicResponse{
		ID:         fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		Type:       "message",
		Role:       "assistant",
		Model:      model,
		Content:    content,
		StopReason: finishReasonToStopReason(finishReason, len(toolCalls) > 0),
	}
	if usage != nil {
		resp.Usage = AnthropicUsage{InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens}
	}
	return resp
}

// writeAPIError klassifiziert einen Fehler aus der Provider-Kette und
// schreibt die passende HTTP-Fehlerantwort (Status-Code, Error-Type,
// Retry-After bei Rate-Limits). Eigenständig von handleChatCompletions'
// Fehlerbehandlung, um den bestehenden, produktionskritischen Pfad nicht
// anzufassen.
func (s *Server) writeAPIError(w http.ResponseWriter, modelID string, err error) {
	if err == sigoengine.ErrRateLimited {
		retryAfter := s.rateMaxWait.Seconds()
		if retryAfter < 1 {
			retryAfter = 1
		}
		sigoengine.LogWarn("Alle Kanäle rate-limitiert", map[string]interface{}{
			"model": modelID, "retry_after": retryAfter,
		})
		w.Header().Set("Retry-After", fmt.Sprintf("%.0f", retryAfter))
		writeError(w, "rate limit exceeded: all channels throttled", "rate_limit", http.StatusTooManyRequests)
		return
	}

	apiErr := sigoengine.ClassifyError(err)
	sigoengine.LogError("API-Call fehlgeschlagen", err, map[string]interface{}{
		"model":       modelID,
		"error_type":  apiErr.Type,
		"status_code": apiErr.StatusCode,
	})

	httpStatus := http.StatusBadGateway
	errType := "api_error"
	switch apiErr.Type {
	case sigoengine.ErrRateLimit:
		httpStatus = http.StatusTooManyRequests
		errType = "rate_limit"
		if apiErr.RetryAfter > 0 {
			w.Header().Set("Retry-After", fmt.Sprintf("%.0f", apiErr.RetryAfter.Seconds()))
		}
	case sigoengine.ErrAuthFailed:
		httpStatus = http.StatusUnauthorized
		errType = "auth_failed"
	case sigoengine.ErrTimeout:
		httpStatus = http.StatusGatewayTimeout
		errType = "timeout"
	case sigoengine.ErrServerError:
		httpStatus = http.StatusServiceUnavailable
		errType = "server_error"
	case sigoengine.ErrClientError:
		httpStatus = http.StatusBadRequest
		errType = "client_error"
	case sigoengine.ErrCircuitOpen:
		httpStatus = http.StatusServiceUnavailable
		errType = "circuit_open"
	}
	writeError(w, apiErr.Message, errType, httpStatus)
}

// handleMessages implementiert POST /v1/messages (Anthropic-Messages-API).
// Übersetzt Request/Response und nutzt dieselbe Channel-Resolution/
// Failover/Rate-Limiter/Circuit-Breaker-Maschinerie wie handleChatCompletions.
func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "Method not allowed", "invalid_request", http.StatusMethodNotAllowed)
		return
	}

	var req AnthropicRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "Invalid JSON: "+err.Error(), "invalid_request", http.StatusBadRequest)
		return
	}

	s.mu.RLock()
	modelInfo, modelID, exists := s.lookupModel(req.Model)
	s.mu.RUnlock()
	if !exists {
		writeError(w, fmt.Sprintf("Model '%s' nicht gefunden", req.Model), "not_found_error", http.StatusNotFound)
		return
	}

	provider := s.providerForModel(modelID)
	ch, err := s.channelManager.Resolve(provider, "")
	if err != nil {
		writeError(w, err.Error(), "api_error", http.StatusServiceUnavailable)
		return
	}

	messages, tools, toolChoice, err := anthropicRequestToInternal(&req)
	if err != nil {
		writeError(w, "Invalid request: "+err.Error(), "invalid_request", http.StatusBadRequest)
		return
	}

	firstCfg, err := sigoengine.LoadConfigWithChannel(modelID, ch)
	if err != nil {
		writeError(w, err.Error(), "config_error", http.StatusInternalServerError)
		return
	}
	wireModel := firstCfg.Model
	if modelInfo.UpstreamID != "" {
		wireModel = modelInfo.UpstreamID
	}

	apiRequest := map[string]interface{}{
		"model":    wireModel,
		"messages": messages,
	}
	if req.Temperature != nil {
		apiRequest["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		apiRequest["top_p"] = *req.TopP
	}
	if len(req.StopSequences) > 0 {
		apiRequest["stop"] = req.StopSequences
	}
	if len(tools) > 0 {
		apiRequest["tools"] = tools
	}
	if toolChoice != nil {
		apiRequest["tool_choice"] = toolChoice
	}
	if modelInfo.RequiresCompletionTokens {
		apiRequest["max_completion_tokens"] = req.MaxTokens
	} else {
		apiRequest["max_tokens"] = req.MaxTokens
	}

	if req.Stream {
		writeError(w, "streaming not yet supported on /v1/messages", "not_implemented", http.StatusNotImplemented)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 180*time.Second)
	defer cancel()

	var inputBuilder strings.Builder
	for _, m := range req.Messages {
		text, _ := anthropicTextFromRaw(m.Content)
		inputBuilder.WriteString(text)
	}
	inputText := inputBuilder.String()

	channelsToTry := s.channelManager.FailoverList(provider, ch)
	retryConfig := sigoengine.DefaultRetryConfig()

	var responseText string
	var responseUsage *sigoengine.UsageData
	var responseFinishReason string
	var responseToolCalls []sigoengine.ToolCall
	var successfulCh *sigoengine.Channel
	var lastErr error

	for _, currentCh := range channelsToTry {
		cfg, err := sigoengine.LoadConfigWithChannel(modelID, currentCh)
		if err != nil {
			lastErr = err
			continue
		}
		cfg.Endpoint = modelInfo.Endpoint
		cfg.Model = wireModel

		minInt := s.rateMinInterval
		if currentCh.MinInterval > 0 {
			minInt = time.Duration(currentCh.MinInterval) * time.Millisecond
		}
		maxW := s.rateMaxWait
		if currentCh.MaxWait > 0 {
			maxW = time.Duration(currentCh.MaxWait) * time.Millisecond
		}
		if minInt > 0 {
			if err := s.rateLimiter.Acquire(ctx, currentCh.FullName(), minInt, maxW); err != nil {
				lastErr = err
				if err == sigoengine.ErrRateLimited {
					continue
				}
				break
			}
			s.rateLimiter.Release(currentCh.FullName())
		}

		cbKey := fmt.Sprintf("%s#%s", modelID, currentCh.FullName())
		s.mu.Lock()
		if _, exists := s.breakers[cbKey]; !exists {
			s.breakers[cbKey] = sigoengine.NewEnhancedCircuitBreaker(&sigoengine.CircuitBreakerConfig{
				Threshold: 5, Window: 60 * time.Second, Cooldown: 10 * time.Second, HalfOpenMax: 3,
			})
		}
		breaker := s.breakers[cbKey]
		s.mu.Unlock()

		lastErr = sigoengine.RetryWithBackoff(ctx, retryConfig, func() error {
			return breaker.Do(func() error {
				text, u, fr, tc, e := sigoengine.CallAPI(ctx, cfg, apiRequest, 180)
				if e != nil {
					apiErr := sigoengine.ClassifyError(e)
					if apiErr.Type == sigoengine.ErrAuthFailed {
						s.channelManager.Registry().SetActive(currentCh.Provider, currentCh.Name, false)
					}
					return e
				}
				responseText = text
				responseUsage = u
				responseFinishReason = fr
				responseToolCalls = tc
				return nil
			})
		})

		if lastErr == nil {
			successfulCh = currentCh
			s.channelManager.Registry().MarkChannelHealth(currentCh.Provider, currentCh.Name, true, "")
			break
		}

		s.channelManager.Registry().MarkChannelHealth(currentCh.Provider, currentCh.Name, false, lastErr.Error())
		if sigoengine.ClassifyError(lastErr).Type == sigoengine.ErrClientError {
			break
		}
	}

	if lastErr != nil {
		s.writeAPIError(w, modelID, lastErr)
		return
	}

	if responseUsage == nil {
		responseUsage = sigoengine.EstimateUsage(inputText, responseText)
	}
	s.recordUsage(modelID, successfulCh, responseUsage)

	resp := internalToAnthropicResponse(req.Model, responseText, responseToolCalls, responseUsage, responseFinishReason)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// writeAnthropicSSEEvent schreibt ein einzelnes Anthropic-SSE-Event
// (event: + data: + Leerzeile) und flusht sofort.
func writeAnthropicSSEEvent(w http.ResponseWriter, flusher http.Flusher, eventType string, payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, data); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

// streamAnthropicResponse liest einen OpenAI-kompatiblen SSE-Stream (wie von
// sigoengine.CallAPIStream geliefert) und übersetzt ihn live in eine
// Anthropic-Messages-Event-Sequenz. Tool-Call-Argument-Fragmente werden
// unverändert als partial_json durchgereicht (Anthropic erwartet ohnehin
// akkumulierbare JSON-Fragmente, keine Neu-Serialisierung nötig).
func (s *Server) streamAnthropicResponse(w http.ResponseWriter, stream io.ReadCloser, model string) (string, error) {
	defer stream.Close()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		return "", fmt.Errorf("response writer does not support flushing")
	}

	messageID := fmt.Sprintf("msg_%d", time.Now().UnixNano())
	writeAnthropicSSEEvent(w, flusher, "message_start", map[string]interface{}{
		"type": "message_start",
		"message": map[string]interface{}{
			"id": messageID, "type": "message", "role": "assistant", "model": model,
			"content": []interface{}{}, "stop_reason": nil,
			"usage": map[string]interface{}{"input_tokens": 0, "output_tokens": 0},
		},
	})

	var responseText strings.Builder
	nextBlockIndex := 0
	textBlockIndex := -1
	toolBlockIndexByOpenAIIndex := map[int]int{}
	var openBlockIndices []int
	finishReason := ""
	var usage *sigoengine.UsageData

	scanner := bufio.NewScanner(stream)
	buf := make([]byte, 4096)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		dataStr := strings.TrimPrefix(line, "data: ")
		if dataStr == "" || dataStr == "[DONE]" {
			continue
		}
		var chunk map[string]interface{}
		if err := json.Unmarshal([]byte(dataStr), &chunk); err != nil {
			continue
		}

		if u, ok := chunk["usage"].(map[string]interface{}); ok {
			usage = &sigoengine.UsageData{}
			if v, ok := u["prompt_tokens"].(float64); ok {
				usage.InputTokens = int(v)
			}
			if v, ok := u["completion_tokens"].(float64); ok {
				usage.OutputTokens = int(v)
			}
		}

		choices, ok := chunk["choices"].([]interface{})
		if !ok || len(choices) == 0 {
			continue
		}
		choice, ok := choices[0].(map[string]interface{})
		if !ok {
			continue
		}
		if fr, ok := choice["finish_reason"].(string); ok && fr != "" {
			finishReason = fr
		}
		delta, ok := choice["delta"].(map[string]interface{})
		if !ok {
			continue
		}

		if text, ok := delta["content"].(string); ok && text != "" {
			if textBlockIndex == -1 {
				textBlockIndex = nextBlockIndex
				nextBlockIndex++
				openBlockIndices = append(openBlockIndices, textBlockIndex)
				writeAnthropicSSEEvent(w, flusher, "content_block_start", map[string]interface{}{
					"type": "content_block_start", "index": textBlockIndex,
					"content_block": map[string]interface{}{"type": "text", "text": ""},
				})
			}
			responseText.WriteString(text)
			writeAnthropicSSEEvent(w, flusher, "content_block_delta", map[string]interface{}{
				"type": "content_block_delta", "index": textBlockIndex,
				"delta": map[string]interface{}{"type": "text_delta", "text": text},
			})
		}

		if rawToolCalls, ok := delta["tool_calls"].([]interface{}); ok {
			for _, item := range rawToolCalls {
				tc, ok := item.(map[string]interface{})
				if !ok {
					continue
				}
				openAIIdx := 0
				if v, ok := tc["index"].(float64); ok {
					openAIIdx = int(v)
				}
				anthropicIdx, seen := toolBlockIndexByOpenAIIndex[openAIIdx]
				fn, _ := tc["function"].(map[string]interface{})
				if !seen {
					anthropicIdx = nextBlockIndex
					nextBlockIndex++
					toolBlockIndexByOpenAIIndex[openAIIdx] = anthropicIdx
					openBlockIndices = append(openBlockIndices, anthropicIdx)
					id, _ := tc["id"].(string)
					name := ""
					if fn != nil {
						name, _ = fn["name"].(string)
					}
					writeAnthropicSSEEvent(w, flusher, "content_block_start", map[string]interface{}{
						"type": "content_block_start", "index": anthropicIdx,
						"content_block": map[string]interface{}{
							"type": "tool_use", "id": id, "name": name, "input": map[string]interface{}{},
						},
					})
				}
				if fn != nil {
					if args, ok := fn["arguments"].(string); ok && args != "" {
						writeAnthropicSSEEvent(w, flusher, "content_block_delta", map[string]interface{}{
							"type": "content_block_delta", "index": anthropicIdx,
							"delta": map[string]interface{}{"type": "input_json_delta", "partial_json": args},
						})
					}
				}
			}
		}
	}

	for _, idx := range openBlockIndices {
		writeAnthropicSSEEvent(w, flusher, "content_block_stop", map[string]interface{}{
			"type": "content_block_stop", "index": idx,
		})
	}

	hasToolCalls := len(toolBlockIndexByOpenAIIndex) > 0
	stopReason := finishReasonToStopReason(finishReason, hasToolCalls)
	usagePayload := map[string]interface{}{"output_tokens": 0}
	if usage != nil {
		usagePayload["output_tokens"] = usage.OutputTokens
	}
	writeAnthropicSSEEvent(w, flusher, "message_delta", map[string]interface{}{
		"type":  "message_delta",
		"delta": map[string]interface{}{"stop_reason": stopReason, "stop_sequence": nil},
		"usage": usagePayload,
	})
	writeAnthropicSSEEvent(w, flusher, "message_stop", map[string]interface{}{"type": "message_stop"})

	if err := scanner.Err(); err != nil {
		return responseText.String(), err
	}
	return responseText.String(), nil
}
