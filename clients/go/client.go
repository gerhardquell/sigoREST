// Package sigoclient provides a Go client for the sigoREST API.
//
// This client offers a simple, type-safe interface to interact with
// the sigoREST OpenAI-compatible API.
//
// Basic usage:
//
//	client := sigoclient.New("http://127.0.0.1:9080")
//	resp, err := client.Chat(context.Background(), "kimi", "Hello!")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	fmt.Println(resp.Content)
//
// With options:
//
//	resp, err := client.Chat(ctx, "gpt41", "Explain Go routines",
//	    sigoclient.WithSession("my-session"),
//	    sigoclient.WithTemperature(0.7),
//	)
package sigoclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// DefaultTimeout is the default request timeout
const DefaultTimeout = 180 * time.Second

// Client is a client for the sigoREST API
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// ClientOption configures a Client
type ClientOption func(*Client)

// WithHTTPClient sets a custom HTTP client
func WithHTTPClient(httpClient *http.Client) ClientOption {
	return func(c *Client) {
		c.httpClient = httpClient
	}
}

// WithTimeout sets the request timeout
func WithTimeout(timeout time.Duration) ClientOption {
	return func(c *Client) {
		c.httpClient = &http.Client{Timeout: timeout}
	}
}

// New creates a new sigoREST client
func New(baseURL string, opts ...ClientOption) *Client {
	c := &Client{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: DefaultTimeout},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Error represents an API error
type Error struct {
	Message    string
	StatusCode int
	Response   map[string]interface{}
}

func (e *Error) Error() string {
	if e.StatusCode > 0 {
		return fmt.Sprintf("sigoREST error (status %d): %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("sigoREST error: %s", e.Message)
}

// IsError checks if an error is a sigoREST Error
func IsError(err error) (*Error, bool) {
	if err == nil {
		return nil, false
	}
	if e, ok := err.(*Error); ok {
		return e, true
	}
	return nil, false
}

// ChatMessage represents a message in a chat conversation
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest represents a chat completion request
type ChatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Temperature *float64      `json:"temperature,omitempty"`
	MaxTokens   *int          `json:"max_tokens,omitempty"`
	SessionID   string        `json:"session_id,omitempty"`
	Timeout     *int          `json:"timeout,omitempty"`
	Retries     int           `json:"retries,omitempty"`
}

// ChatResponse represents a chat completion response
type ChatResponse struct {
	Content      string `json:"content"`
	Model        string `json:"model"`
	SessionID    string `json:"session_id,omitempty"`
	RawResponse  map[string]interface{}
}

// ModelInfo represents information about an available model
type ModelInfo struct {
	ID                       string  `json:"id"`
	Shortcode                string  `json:"shortcode"`
	Endpoint                 string  `json:"endpoint"`
	APIKey                   string  `json:"apikey"`
	MaxInputTokens           int     `json:"max_input_tokens"`
	MaxOutputTokens          int     `json:"max_output_tokens"`
	InputCost                float64 `json:"input_cost"`
	OutputCost               float64 `json:"output_cost"`
	MinTemperature           float64 `json:"min_temperature"`
	MaxTemperature           float64 `json:"max_temperature"`
	RequiresCompletionTokens bool    `json:"requires_completion_tokens"`
}

// HealthResponse represents the server health status
type HealthResponse struct {
	Status          string                 `json:"status"`
	Timestamp       int64                  `json:"timestamp"`
	AvailableModels int                    `json:"available_models"`
	MemorySet       bool                   `json:"memory_set"`
	CircuitBreakers []CircuitBreakerState  `json:"circuit_breakers,omitempty"`
}

// CircuitBreakerState represents the state of a circuit breaker
type CircuitBreakerState struct {
	Model    string                 `json:"model"`
	Open     bool                   `json:"open"`
	Failures int                    `json:"failures"`
	Details  map[string]interface{} `json:"details,omitempty"`
}

// MemoryBlock represents the global memory configuration
type MemoryBlock struct {
	Content string `json:"content"`
	Cache   bool   `json:"cache"`
}

// ChatOption configures a chat request
type ChatOption func(*ChatRequest)

// WithSession sets the session ID for conversation continuity
func WithSession(sessionID string) ChatOption {
	return func(r *ChatRequest) {
		r.SessionID = sessionID
	}
}

// WithSystemPrompt adds a system prompt to the conversation
func WithSystemPrompt(prompt string) ChatOption {
	return func(r *ChatRequest) {
		// Prepend system message
		messages := []ChatMessage{{Role: "system", Content: prompt}}
		messages = append(messages, r.Messages...)
		r.Messages = messages
	}
}

// WithTemperature sets the temperature
func WithTemperature(temp float64) ChatOption {
	return func(r *ChatRequest) {
		r.Temperature = &temp
	}
}

// WithMaxTokens sets the maximum tokens
func WithMaxTokens(tokens int) ChatOption {
	return func(r *ChatRequest) {
		r.MaxTokens = &tokens
	}
}

// WithTimeout sets the request timeout
func WithTimeoutSeconds(seconds int) ChatOption {
	return func(r *ChatRequest) {
		r.Timeout = &seconds
	}
}

// WithRetries sets the number of retries
func WithRetries(retries int) ChatOption {
	return func(r *ChatRequest) {
		r.Retries = retries
	}
}

// doRequest performs an HTTP request and decodes the response
func (c *Client) doRequest(ctx context.Context, method, path string, body interface{}, result interface{}) error {
	url := c.baseURL + path

	var bodyReader io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return &Error{Message: fmt.Sprintf("failed to marshal request: %v", err)}
		}
		bodyReader = bytes.NewReader(jsonBody)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return &Error{Message: fmt.Sprintf("failed to create request: %v", err)}
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return &Error{Message: fmt.Sprintf("request failed: %v", err)}
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return &Error{Message: fmt.Sprintf("failed to read response: %v", err)}
	}

	if resp.StatusCode >= 400 {
		var errResp struct {
			Error struct {
				Message string `json:"message"`
				Type    string `json:"type"`
				Code    string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(respBody, &errResp); err == nil && errResp.Error.Message != "" {
			return &Error{
				Message:    errResp.Error.Message,
				StatusCode: resp.StatusCode,
			}
		}
		return &Error{
			Message:    string(respBody),
			StatusCode: resp.StatusCode,
		}
	}

	if result != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, result); err != nil {
			return &Error{Message: fmt.Sprintf("failed to decode response: %v", err)}
		}
	}

	return nil
}

// Ping checks if the server is alive
func (c *Client) Ping(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/ping", nil)
	if err != nil {
		return false
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false
	}

	body, _ := io.ReadAll(resp.Body)
	return string(body) == "pong"
}

// Health gets the server health status
func (c *Client) Health(ctx context.Context) (*HealthResponse, error) {
	var health HealthResponse
	if err := c.doRequest(ctx, http.MethodGet, "/api/health", nil, &health); err != nil {
		return nil, err
	}
	return &health, nil
}

// ListModels returns all available models
func (c *Client) ListModels(ctx context.Context) ([]ModelInfo, error) {
	var models []ModelInfo
	if err := c.doRequest(ctx, http.MethodGet, "/api/models", nil, &models); err != nil {
		return nil, err
	}
	return models, nil
}

// Chat sends a chat completion request
func (c *Client) Chat(ctx context.Context, model, message string, opts ...ChatOption) (*ChatResponse, error) {
	req := &ChatRequest{
		Model:    model,
		Messages: []ChatMessage{{Role: "user", Content: message}},
		Retries:  3,
	}

	for _, opt := range opts {
		opt(req)
	}

	var result struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		Model   string `json:"model"`
		Choices []struct {
			Index   int `json:"index"`
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := c.doRequest(ctx, http.MethodPost, "/v1/chat/completions", req, &result); err != nil {
		return nil, err
	}

	if len(result.Choices) == 0 {
		return nil, &Error{Message: "no choices in response"}
	}

	return &ChatResponse{
		Content:   result.Choices[0].Message.Content,
		Model:     result.Model,
		SessionID: req.SessionID,
		RawResponse: map[string]interface{}{
			"id":      result.ID,
			"created": result.Created,
		},
	}, nil
}

// GetMemory gets the global memory block
func (c *Client) GetMemory(ctx context.Context) (*MemoryBlock, error) {
	var mem MemoryBlock
	if err := c.doRequest(ctx, http.MethodGet, "/api/memory", nil, &mem); err != nil {
		return nil, err
	}
	return &mem, nil
}

// SetMemory sets the global memory block
func (c *Client) SetMemory(ctx context.Context, content string, cache bool) (*MemoryBlock, error) {
	mem := &MemoryBlock{Content: content, Cache: cache}
	var result MemoryBlock
	if err := c.doRequest(ctx, http.MethodPut, "/api/memory", mem, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
