//**********************************************************************
//      sigoengine/engine.go
//**********************************************************************
//  Autor    : Gerhard Quell - gquell@skequell.de
//  CoAutor  : claude sonnet 4.6
//  Copyright: 2025 Gerhard Quell - SKEQuell
//  Erstellt : 20260219
//**********************************************************************
// Beschreibung: Shared Engine Package für sigoEngine CLI und sigoREST
//               Exportiert alle relevanten Typen und Funktionen.
//               Thread-safe für parallele REST-Server Nutzung.
//**********************************************************************

package sigoengine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// **********************************************************************
// Konstanten
const (
	DEFAULT_TEMPERATURE float64 = 1.0
	DEFAULT_MAX_TOKENS  int     = 0
	DEFAULT_TIMEOUT     int     = 180
)

// **********************************************************************
// Fehlercodes
const (
	ErrConfigNotFound   = "CONFIG_NOT_FOUND"
	ErrAPIKeyMissing    = "API_KEY_MISSING"
	ErrAPIFailed        = "API_FAILED"
	ErrInvalidInput     = "INVALID_INPUT"
	ErrSessionError     = "SESSION_ERROR"
	ErrCircuitOpen      = "CIRCUIT_OPEN"
	ErrUnexpectedFormat = "UNEXPECTED_FORMAT"
	// Neue Fehlercodes für typisierte Fehlerbehandlung
	ErrRateLimit   = "RATE_LIMIT"
	ErrAuthFailed  = "AUTH_FAILED"
	ErrTimeout     = "TIMEOUT"
	ErrServerError = "SERVER_ERROR"
	ErrClientError = "CLIENT_ERROR"
)

// **********************************************************************
// SigoError - strukturierter Fehlertyp
type SigoError struct {
	Code    string                 `json:"code"`
	Message string                 `json:"message"`
	Err     error                  `json:"error,omitempty"`
	Fields  map[string]interface{} `json:"fields,omitempty"`
}

func (e *SigoError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// NewError erstellt einen strukturierten Fehler
func NewError(code, message string, err error, fields map[string]interface{}) *SigoError {
	return &SigoError{Code: code, Message: message, Err: err, Fields: fields}
}

// **********************************************************************
// APIError - Typisierter Fehler mit HTTP-Status für verbesserte Fehlerbehandlung

type APIError struct {
	Type       string        // "rate_limit", "auth_failed", "timeout", "server_error", "client_error"
	StatusCode int
	Message    string
	RetryAfter time.Duration
	Err        error
}

func (e *APIError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("[%s] %s (HTTP %d): %v", e.Type, e.Message, e.StatusCode, e.Err)
	}
	return fmt.Sprintf("[%s] %s (HTTP %d)", e.Type, e.Message, e.StatusCode)
}

// IsRetryable bestimmt, ob ein Retry bei diesem Fehler sinnvoll ist
func (e *APIError) IsRetryable() bool {
	switch e.Type {
	case ErrRateLimit, ErrTimeout, ErrServerError:
		return true
	case ErrAuthFailed, ErrClientError:
		return false
	default:
		return false
	}
}

// ToSigoError konvertiert APIError zu SigoError für Kompatibilität
func (e *APIError) ToSigoError() *SigoError {
	return NewError(e.Type, e.Message, e.Err, map[string]interface{}{
		"status_code": e.StatusCode,
		"retry_after": e.RetryAfter.Seconds(),
	})
}

// ClassifyError klassifiziert einen Fehler als APIError
func ClassifyError(err error) *APIError {
	if err == nil {
		return nil
	}

	// Prüfe ob es bereits ein APIError ist
	if apiErr, ok := err.(*APIError); ok {
		return apiErr
	}

	// Versuche aus SigoError zu extrahieren
	if sigoErr, ok := err.(*SigoError); ok {
		return &APIError{
			Type:    sigoErr.Code,
			Message: sigoErr.Message,
			Err:     sigoErr.Err,
		}
	}

	// Timeout-Errors erkennen
	if strings.Contains(err.Error(), "timeout") || strings.Contains(err.Error(), "deadline exceeded") {
		return &APIError{
			Type:    ErrTimeout,
			Message: "Request timeout",
			Err:     err,
		}
	}

	// Default: nicht klassifizierbar
	return &APIError{
		Type:    ErrAPIFailed,
		Message: err.Error(),
		Err:     err,
	}
}

// classifyHTTPError klassifiziert HTTP-Status-Codes als APIError
func classifyHTTPError(statusCode int, message string, err error) *APIError {
	switch {
	case statusCode == 429:
		return &APIError{
			Type:       ErrRateLimit,
			StatusCode: statusCode,
			Message:    message,
			Err:        err,
		}
	case statusCode == 401 || statusCode == 403:
		return &APIError{
			Type:       ErrAuthFailed,
			StatusCode: statusCode,
			Message:    message,
			Err:        err,
		}
	case statusCode == 408 || statusCode == 504:
		return &APIError{
			Type:       ErrTimeout,
			StatusCode: statusCode,
			Message:    message,
			Err:        err,
		}
	case statusCode >= 500:
		return &APIError{
			Type:       ErrServerError,
			StatusCode: statusCode,
			Message:    message,
			Err:        err,
		}
	case statusCode >= 400:
		return &APIError{
			Type:       ErrClientError,
			StatusCode: statusCode,
			Message:    message,
			Err:        err,
		}
	default:
		return &APIError{
			Type:       ErrAPIFailed,
			StatusCode: statusCode,
			Message:    message,
			Err:        err,
		}
	}
}

// **********************************************************************
// Logging System - thread-safe
type LogLevel int

const (
	DEBUG LogLevel = iota
	INFO
	WARN
	ERROR
	FATAL
)

// LogConfig hält die Logging-Konfiguration thread-safe
type LogConfig struct {
	mu       sync.RWMutex
	level    LogLevel
	jsonMode bool
	quiet    bool
}

var globalLogConfig = &LogConfig{level: INFO}

// SetLogLevel setzt den Log-Level (thread-safe)
func SetLogLevel(level LogLevel) {
	globalLogConfig.mu.Lock()
	defer globalLogConfig.mu.Unlock()
	globalLogConfig.level = level
}

// SetJSONMode aktiviert/deaktiviert JSON-Logging (thread-safe)
func SetJSONMode(enabled bool) {
	globalLogConfig.mu.Lock()
	defer globalLogConfig.mu.Unlock()
	globalLogConfig.jsonMode = enabled
}

// SetQuietMode aktiviert/deaktiviert Quiet-Mode (thread-safe)
func SetQuietMode(enabled bool) {
	globalLogConfig.mu.Lock()
	defer globalLogConfig.mu.Unlock()
	globalLogConfig.quiet = enabled
}

// ParseLogLevel konvertiert String zu LogLevel
func ParseLogLevel(s string) LogLevel {
	switch s {
	case "debug":
		return DEBUG
	case "warn":
		return WARN
	case "error":
		return ERROR
	case "fatal":
		return FATAL
	default:
		return INFO
	}
}

// LogEntry ist ein strukturierter Log-Eintrag
type LogEntry struct {
	Time    time.Time              `json:"time"`
	Level   string                 `json:"level"`
	PID     int                    `json:"pid"`
	Message string                 `json:"message"`
	Fields  map[string]interface{} `json:"fields,omitempty"`
}

func levelStr(level LogLevel) string {
	switch level {
	case DEBUG:
		return "DEBUG"
	case INFO:
		return "INFO"
	case WARN:
		return "WARN"
	case ERROR:
		return "ERROR"
	case FATAL:
		return "FATAL"
	default:
		return "UNKNOWN"
	}
}

func doLog(level LogLevel, msg string, fields map[string]interface{}) {
	globalLogConfig.mu.RLock()
	cfgLevel := globalLogConfig.level
	cfgJSON := globalLogConfig.jsonMode
	cfgQuiet := globalLogConfig.quiet
	globalLogConfig.mu.RUnlock()

	if level < cfgLevel {
		return
	}
	if cfgQuiet && level < ERROR {
		return
	}

	entry := LogEntry{
		Time:    time.Now(),
		Level:   levelStr(level),
		PID:     os.Getpid(),
		Message: msg,
		Fields:  fields,
	}

	if cfgJSON {
		data, _ := json.Marshal(entry)
		fmt.Fprintln(os.Stderr, string(data))
	} else {
		fmt.Fprintf(os.Stderr, "%s %-5s pid=%d: %s",
			entry.Time.Format("2006-01-02T15:04:05Z"), entry.Level, entry.PID, msg)
		for k, v := range fields {
			fmt.Fprintf(os.Stderr, " %s=%v", k, v)
		}
		fmt.Fprintln(os.Stderr)
	}
}

// LogDebug loggt auf DEBUG-Level
func LogDebug(msg string, fields ...map[string]interface{}) {
	var f map[string]interface{}
	if len(fields) > 0 {
		f = fields[0]
	}
	doLog(DEBUG, msg, f)
}

// LogInfo loggt auf INFO-Level
func LogInfo(msg string, fields ...map[string]interface{}) {
	var f map[string]interface{}
	if len(fields) > 0 {
		f = fields[0]
	}
	doLog(INFO, msg, f)
}

// LogWarn loggt auf WARN-Level
func LogWarn(msg string, fields ...map[string]interface{}) {
	var f map[string]interface{}
	if len(fields) > 0 {
		f = fields[0]
	}
	doLog(WARN, msg, f)
}

// LogError loggt auf ERROR-Level
func LogError(msg string, err error, fields ...map[string]interface{}) {
	allFields := make(map[string]interface{})
	if len(fields) > 0 {
		for k, v := range fields[0] {
			allFields[k] = v
		}
	}
	if err != nil {
		allFields["error"] = err.Error()
	}
	doLog(ERROR, msg, allFields)
}

// **********************************************************************
// Modell-Registry
var MammothModels = map[string]map[string]interface{}{
	// GPT Models (Mammoth.ai)
	"gpt-4.1":      {"shortcode": "gpt41", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 2.0, "output_cost": 8.0, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 128000, "max_output": 8192, "min_temperature": 0.0, "max_temperature": 2.0},
	"gpt-4.1-mini": {"shortcode": "gpt41m", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 0.4, "output_cost": 1.6, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 128000, "max_output": 4096, "min_temperature": 0.0, "max_temperature": 2.0},
	"gpt-4.1-nano": {"shortcode": "gpt41n", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 0.1, "output_cost": 0.4, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 128000, "max_output": 2048, "min_temperature": 0.0, "max_temperature": 2.0},
	"gpt-4o":       {"shortcode": "gpt4o", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 2.5, "output_cost": 10.0, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 128000, "max_output": 4096, "min_temperature": 0.0, "max_temperature": 2.0},
	"o4-mini":      {"shortcode": "o4m", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 1.1, "output_cost": 4.4, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 128000, "max_output": 65536, "min_temperature": 0.0, "max_temperature": 2.0},
	"o3":           {"shortcode": "o3", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 2.0, "output_cost": 8.0, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 200000, "max_output": 100000, "min_temperature": 0.0, "max_temperature": 1.0},
	"gpt-5-mini":   {"shortcode": "gpt5m", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 0.25, "output_cost": 2.0, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 128000, "max_output": 4096, "min_temperature": 0.0, "max_temperature": 2.0, "requires_max_completion_tokens": true},
	"gpt-5-nano":   {"shortcode": "gpt5n", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 0.1, "output_cost": 0.4, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 128000, "max_output": 2048, "min_temperature": 0.0, "max_temperature": 2.0, "requires_max_completion_tokens": true},
	"gpt-5-chat":   {"shortcode": "gpt5chat", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 1.25, "output_cost": 10.0, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 128000, "max_output": 8192, "min_temperature": 0.0, "max_temperature": 2.0, "requires_max_completion_tokens": true},
	"gpt-5.1-chat": {"shortcode": "gpt51chat", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 1.25, "output_cost": 10.0, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 128000, "max_output": 8192, "min_temperature": 0.0, "max_temperature": 2.0, "requires_max_completion_tokens": true},
	"gpt-5.2-chat": {"shortcode": "gpt52chat", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 1.25, "output_cost": 10.0, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 128000, "max_output": 8192, "min_temperature": 0.0, "max_temperature": 2.0, "requires_max_completion_tokens": true},

	// Mistral Models
	"mistral-medium-3":               {"shortcode": "mistral-m", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 0.4, "output_cost": 2.0, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 128000, "max_output": 4096, "min_temperature": 0.0, "max_temperature": 1.0},
	"mistral-medium-3.1":             {"shortcode": "mistral-m31", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 0.4, "output_cost": 2.0, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 128000, "max_output": 4096, "min_temperature": 0.0, "max_temperature": 1.0},
	"mistral-large-2411":             {"shortcode": "mistral-l", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 2.0, "output_cost": 6.0, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 128000, "max_output": 4096, "min_temperature": 0.0, "max_temperature": 1.0},
	"Mistral-Large-3":                {"shortcode": "mistral-l3", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 2.0, "output_cost": 6.0, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 128000, "max_output": 4096, "min_temperature": 0.0, "max_temperature": 1.0},
	"mistral-small-3.2-24b-instruct": {"shortcode": "mistral-s", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 0.1, "output_cost": 0.3, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 32000, "max_output": 8192, "min_temperature": 0.0, "max_temperature": 1.0},
	"magistral-medium-2506":          {"shortcode": "magistral", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 2.0, "output_cost": 5.0, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 128000, "max_output": 4096, "min_temperature": 0.0, "max_temperature": 1.0},
	"magistral-medium-2506-thinking": {"shortcode": "magistral-thinking", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 2.0, "output_cost": 5.0, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 128000, "max_output": 4096, "min_temperature": 0.0, "max_temperature": 1.0},
	"codestral-2508":                 {"shortcode": "codestral", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 0.3, "output_cost": 0.9, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 32000, "max_output": 32768, "min_temperature": 0.0, "max_temperature": 1.0},

	// Grok Models
	"grok-3":                    {"shortcode": "grok3", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 3.0, "output_cost": 15.0, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 131072, "max_output": 8192, "min_temperature": 0.0, "max_temperature": 2.0},
	"grok-3-mini":               {"shortcode": "grok3m", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 0.3, "output_cost": 0.5, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 131072, "max_output": 4096, "min_temperature": 0.0, "max_temperature": 2.0},
	"grok-4-0709":               {"shortcode": "grok4", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 3.0, "output_cost": 15.0, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 131072, "max_output": 8192, "min_temperature": 0.0, "max_temperature": 2.0},
	"grok-4-fast-non-reasoning": {"shortcode": "grok4f", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 0.2, "output_cost": 0.5, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 131072, "max_output": 4096, "min_temperature": 0.0, "max_temperature": 2.0},
	"grok-code-fast-1":          {"shortcode": "grok-code", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 0.3, "output_cost": 0.9, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 32000, "max_output": 32768, "min_temperature": 0.0, "max_temperature": 1.0},

	// Gemini Models
	"gemini-2.5-flash":           {"shortcode": "gemini-f", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 0.3, "output_cost": 2.5, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 1048576, "max_output": 8192, "min_temperature": 0.0, "max_temperature": 2.0},
	"gemini-2.5-pro":             {"shortcode": "gemini-p", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 2.5, "output_cost": 15.0, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 2097152, "max_output": 8192, "min_temperature": 0.0, "max_temperature": 2.0},
	"gemini-2.5-flash-image":     {"shortcode": "gemini-image", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 0.3, "output_cost": 2.5, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 1048576, "max_output": 8192, "min_temperature": 0.0, "max_temperature": 2.0},
	"gemini-3-pro-preview":       {"shortcode": "gemini3p", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 2.5, "output_cost": 15.0, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 2097152, "max_output": 8192, "min_temperature": 0.0, "max_temperature": 2.0},
	"gemini-3-pro-image-preview": {"shortcode": "gemini3p-image", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 2.5, "output_cost": 15.0, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 2097152, "max_output": 8192, "min_temperature": 0.0, "max_temperature": 2.0},

	// DeepSeek Models
	"deepseek-r1-0528":       {"shortcode": "deepseek-r1", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 3.0, "output_cost": 8.0, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 128000, "max_output": 8192, "min_temperature": 0.0, "max_temperature": 1.5},
	"deepseek-v3-0324":       {"shortcode": "deepseek-v3", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 0.9, "output_cost": 0.9, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 128000, "max_output": 8192, "min_temperature": 0.0, "max_temperature": 1.5},
	"deepseek-v3.1":          {"shortcode": "deepseek-v31", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 0.30, "output_cost": 1.0, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 128000, "max_output": 8192, "min_temperature": 0.0, "max_temperature": 1.5},
	"deepseek-v3.1-terminus": {"shortcode": "deepseek-terminus", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 0.30, "output_cost": 1.0, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 128000, "max_output": 8192, "min_temperature": 0.0, "max_temperature": 1.5},
	"deepseek-v3.2-exp":      {"shortcode": "deepseek-exp", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 0.30, "output_cost": 0.45, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 128000, "max_output": 8192, "min_temperature": 0.0, "max_temperature": 1.5},
	"deepseek-v3.2":          {"shortcode": "deepseek-v32", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 0.30, "output_cost": 0.45, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 128000, "max_output": 8192, "min_temperature": 0.0, "max_temperature": 1.5},

	// Llama Models
	"llama-4-maverick": {"shortcode": "llama-mav", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 0.22, "output_cost": 0.88, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 128000, "max_output": 4096, "min_temperature": 0.0, "max_temperature": 1.5},
	"llama-4-scout":    {"shortcode": "llama-scout", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 0.15, "output_cost": 0.6, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 128000, "max_output": 4096, "min_temperature": 0.0, "max_temperature": 1.5},

	// Claude Models
	"claude-3-5-haiku-20241022":  {"shortcode": "claude-h", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 0.8, "output_cost": 4.0, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 200000, "max_output": 8192, "min_temperature": 0.0, "max_temperature": 1.0},
	"claude-3-5-sonnet-20241022": {"shortcode": "claude-s", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 3.0, "output_cost": 15.0, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 200000, "max_output": 8192, "min_temperature": 0.0, "max_temperature": 1.0},
	"claude-3-7-sonnet-20250219": {"shortcode": "claude-s37", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 3.0, "output_cost": 15.0, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 200000, "max_output": 8192, "min_temperature": 0.0, "max_temperature": 1.0},
	"claude-4-sonnet-20250522":   {"shortcode": "claude-s4", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 3.0, "output_cost": 15.0, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 200000, "max_output": 8192, "min_temperature": 0.0, "max_temperature": 1.0},
	"claude-opus-4-1-20250805":   {"shortcode": "claude-41", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 15.0, "output_cost": 75.0, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 200000, "max_output": 8192, "min_temperature": 0.0, "max_temperature": 1.0},
	"claude-haiku-4-5":           {"shortcode": "claude-h45", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 0.8, "output_cost": 4.0, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 200000, "max_output": 8192, "min_temperature": 0.0, "max_temperature": 1.0},
	"claude-sonnet-4-5":          {"shortcode": "claude-s45", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 3.0, "output_cost": 15.0, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 200000, "max_output": 8192, "min_temperature": 0.0, "max_temperature": 1.0},
	"claude-opus-4-5":            {"shortcode": "claude-opus", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 15.0, "output_cost": 75.0, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 200000, "max_output": 8192, "min_temperature": 0.0, "max_temperature": 1.0},

	// Kimi/Moonshot Models (Direct Moonshot.ai)
	"kimi-k2.5":        {"shortcode": "kimi", "endpoint": "https://api.moonshot.ai/v1/chat/completions", "input_cost": 0.6, "output_cost": 3.0, "apikey": "MOONSHOT_API_KEY", "max_tokens": 256000, "max_output": 4096, "min_temperature": 0.0, "max_temperature": 2.0},
	"kimi-k2-instruct": {"shortcode": "kimi-instruct", "endpoint": "https://api.moonshot.ai/v1/chat/completions", "input_cost": 0.8, "output_cost": 4.0, "apikey": "MOONSHOT_API_KEY", "max_tokens": 128000, "max_output": 4096, "min_temperature": 0.0, "max_temperature": 2.0},
	"kimi-k2-thinking": {"shortcode": "kimi-thinking", "endpoint": "https://api.moonshot.ai/v1/chat/completions", "input_cost": 0.6, "output_cost": 2.5, "apikey": "MOONSHOT_API_KEY", "max_tokens": 256000, "max_output": 4096, "min_temperature": 0.0, "max_temperature": 2.0},
	"moonshot-v1-8k":   {"shortcode": "moon-8k", "endpoint": "https://api.moonshot.ai/v1/chat/completions", "input_cost": 0.12, "output_cost": 0.12, "apikey": "MOONSHOT_API_KEY", "max_tokens": 8000, "max_output": 2048, "min_temperature": 0.0, "max_temperature": 2.0},
	"moonshot-v1-32k":  {"shortcode": "moon-32k", "endpoint": "https://api.moonshot.ai/v1/chat/completions", "input_cost": 0.06, "output_cost": 0.06, "apikey": "MOONSHOT_API_KEY", "max_tokens": 32000, "max_output": 4096, "min_temperature": 0.0, "max_temperature": 2.0},
	"moonshot-v1-128k": {"shortcode": "moon-128k", "endpoint": "https://api.moonshot.ai/v1/chat/completions", "input_cost": 0.025, "output_cost": 0.075, "apikey": "MOONSHOT_API_KEY", "max_tokens": 128000, "max_output": 4096, "min_temperature": 0.0, "max_temperature": 2.0},

	// Z.ai Models (Direct Provider)
	"GLM-4.6":             {"shortcode": "zai-glm46", "endpoint": "https://api.z.ai/api/paas/v4/chat/completions", "input_cost": 0.6, "output_cost": 2.2, "apikey": "ZAI_API_KEY", "max_tokens": 200000, "max_output": 4096, "min_temperature": 0.0, "max_temperature": 2.0},
	"GLM-4.5":             {"shortcode": "zai-glm45", "endpoint": "https://api.z.ai/api/paas/v4/chat/completions", "input_cost": 0.6, "output_cost": 2.2, "apikey": "ZAI_API_KEY", "max_tokens": 128000, "max_output": 4096, "min_temperature": 0.0, "max_temperature": 2.0},
	"GLM-4-32B-0414-128K": {"shortcode": "zai-glm432B", "endpoint": "https://api.z.ai/api/paas/v4/chat/completions", "input_cost": 0.1, "output_cost": 0.1, "apikey": "ZAI_API_KEY", "max_tokens": 128000, "max_output": 16000, "min_temperature": 0.0, "max_temperature": 2.0},

	// Qwen Models
	"qwen3-coder":       {"shortcode": "qwen3-coder", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 0.3, "output_cost": 0.9, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 32000, "max_output": 32768, "min_temperature": 0.0, "max_temperature": 1.0},
	"qwen3-coder-flash": {"shortcode": "qwen3-coder-f", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 0.15, "output_cost": 0.45, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 32000, "max_output": 32768, "min_temperature": 0.0, "max_temperature": 1.0},
	"qwen3-coder-plus":  {"shortcode": "qwen3-coder-plus", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 0.6, "output_cost": 1.8, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 32000, "max_output": 32768, "min_temperature": 0.0, "max_temperature": 1.0},

	// Sonar / Perplexity
	"sonar-pro":           {"shortcode": "sonar-pro", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 1.25, "output_cost": 5.0, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 128000, "max_output": 4096, "min_temperature": 0.0, "max_temperature": 2.0},
	"sonar-deep-research": {"shortcode": "sonar-research", "endpoint": "https://api.mammouth.ai/v1/chat/completions", "input_cost": 2.0, "output_cost": 8.0, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 128000, "max_output": 4096, "min_temperature": 0.0, "max_temperature": 2.0},

	// Embedding Models
	"text-embedding-3-large": {"shortcode": "embedding-large", "endpoint": "https://api.mammouth.ai/v1/embeddings", "input_cost": 0.13, "output_cost": 0.13, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 8191, "max_output": 3072, "min_temperature": 0.0, "max_temperature": 0.0},
	"text-embedding-3-small": {"shortcode": "embedding-small", "endpoint": "https://api.mammouth.ai/v1/embeddings", "input_cost": 0.02, "output_cost": 0.02, "apikey": "MAMMOUTH_API_KEY", "max_tokens": 8191, "max_output": 1536, "min_temperature": 0.0, "max_temperature": 0.0},
}

// **********************************************************************
// Shortcode-Lookup (thread-safe via sync.Once)
var (
	shortcodeToModel map[string]string
	shortcodeOnce    sync.Once
)

func buildShortcodeMap() {
	shortcodeOnce.Do(func() {
		shortcodeToModel = make(map[string]string)
		for name, info := range MammothModels {
			if sc, ok := info["shortcode"].(string); ok {
				shortcodeToModel[sc] = name
			}
		}
	})
}

// ResolveModelName löst einen Shortcode oder vollständigen Modellnamen auf
func ResolveModelName(model string) string {
	buildShortcodeMap()
	if fullName, exists := shortcodeToModel[model]; exists {
		return fullName
	}
	return model
}

// **********************************************************************
// GetModelDefaultTokens gibt die Standard-Token-Anzahl für ein Modell zurück
func GetModelDefaultTokens(modelName string) int {
	if info, exists := MammothModels[modelName]; exists {
		if v, ok := info["max_output"].(int); ok {
			return v
		}
	}
	return DEFAULT_MAX_TOKENS
}

// GetModelTemperatureRange gibt Min, Max und Default-Temperatur zurück
func GetModelTemperatureRange(modelName string) (min, max, def float64) {
	min, max = 0.0, 2.0
	if info, exists := MammothModels[modelName]; exists {
		if v, ok := info["min_temperature"].(float64); ok {
			min = v
		}
		if v, ok := info["max_temperature"].(float64); ok {
			max = v
		}
	}
	def = (min + max) / 2.0
	return
}

// **********************************************************************
// ProviderConfig beschreibt einen AI-Provider
type ProviderConfig struct {
	Endpoint string            `json:"endpoint"`
	Model    string            `json:"model"`
	APIKey   string            `json:"api_key"`
	Headers  map[string]string `json:"headers,omitempty"`
	Type     string            `json:"type"` // "anthropic","openai","custom","ollama"
}

// **********************************************************************
// Ollama Registry — wird zur Laufzeit via DiscoverOllamaModels befüllt

// OllamaModelInfo beschreibt ein lokal installiertes Ollama-Modell
type OllamaModelInfo struct {
	Shortcode  string // z.B. "ollama-llama3"
	OllamaName string // z.B. "llama3:latest" (echter Ollama-Name)
	Size       int64  `json:"size"`
}

var (
	ollamaRegistry   = make(map[string]OllamaModelInfo) // shortcode → info
	ollamaRegistryMu sync.RWMutex
)

// DiscoverOllamaModels fragt Ollama nach installierten Modellen.
// endpoint z.B. "http://localhost:11434"
// Gibt Anzahl gefundener Modelle zurück (0 wenn Ollama nicht läuft).
func DiscoverOllamaModels(endpoint string) int {
	type ollamaTag struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	}
	type ollamaTagsResponse struct {
		Models []ollamaTag `json:"models"`
	}

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(endpoint + "/api/tags")
	if err != nil {
		LogInfo("Ollama nicht erreichbar", map[string]interface{}{"endpoint": endpoint})
		return 0
	}
	defer resp.Body.Close()

	var tags ollamaTagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		LogWarn("Ollama /api/tags Parse-Fehler", map[string]interface{}{"error": err.Error()})
		return 0
	}

	ollamaRegistryMu.Lock()
	defer ollamaRegistryMu.Unlock()

	// Alten Stand löschen (Modelle könnten entfernt worden sein)
	ollamaRegistry = make(map[string]OllamaModelInfo)

	for _, m := range tags.Models {
		// Shortcode: ":latest" weglassen, andere Tags als Suffix behalten
		// gemma3:12b → ollama-gemma3-12b
		// llama3.2-vision:latest → ollama-llama3.2-vision
		name := m.Name
		shortcode := "ollama-" + strings.ReplaceAll(name, ":", "-")
		if strings.HasSuffix(shortcode, "-latest") {
			shortcode = strings.TrimSuffix(shortcode, "-latest")
		}

		ollamaRegistry[shortcode] = OllamaModelInfo{
			Shortcode:  shortcode,
			OllamaName: name,
			Size:       m.Size,
		}
		LogDebug("Ollama-Modell registriert", map[string]interface{}{
			"shortcode": shortcode, "model": name,
		})
	}

	LogInfo("Ollama Discovery abgeschlossen", map[string]interface{}{
		"endpoint": endpoint, "models": len(ollamaRegistry),
	})
	return len(ollamaRegistry)
}

// GetOllamaModels gibt eine Kopie der aktuellen Ollama-Registry zurück
func GetOllamaModels() map[string]OllamaModelInfo {
	ollamaRegistryMu.RLock()
	defer ollamaRegistryMu.RUnlock()
	result := make(map[string]OllamaModelInfo, len(ollamaRegistry))
	for k, v := range ollamaRegistry {
		result[k] = v
	}
	return result
}

// **********************************************************************
// LoadConfig lädt die Konfiguration für ein Modell aus der Registry + ENV
func LoadConfig(model string) (*ProviderConfig, error) {
	// Zuerst Ollama-Registry prüfen (shortcode direkt, kein Resolve nötig)
	ollamaRegistryMu.RLock()
	ollamaInfo, isOllama := ollamaRegistry[model]
	ollamaRegistryMu.RUnlock()

	if isOllama {
		return &ProviderConfig{
			Endpoint: "http://localhost:11434/v1/chat/completions",
			Model:    ollamaInfo.OllamaName,
			APIKey:   "", // Ollama braucht keinen Key
			Type:     "ollama",
			Headers:  make(map[string]string),
		}, nil
	}

	// MammothModels / Cloud-Modelle
	fullName := ResolveModelName(model)
	info, exists := MammothModels[fullName]
	if !exists {
		return nil, NewError(ErrConfigNotFound, "Model not found in registry", nil,
			map[string]interface{}{"requested": model, "resolved": fullName})
	}

	envVar := info["apikey"].(string)
	apiKey := os.Getenv(envVar)
	if apiKey == "" {
		return nil, NewError(ErrAPIKeyMissing, "API key not set", nil,
			map[string]interface{}{"env_var": envVar, "model": fullName})
	}

	return &ProviderConfig{
		Endpoint: info["endpoint"].(string),
		Model:    fullName,
		APIKey:   apiKey,
		Type:     "mammoth",
		Headers:  make(map[string]string),
	}, nil
}

// **********************************************************************
// Response - strukturierte API-Antwort
type Response struct {
	Model     string        `json:"model"`
	PID       int           `json:"pid"`
	Timestamp int64         `json:"timestamp"`
	Prompt    string        `json:"prompt,omitempty"`
	Response  string        `json:"response"`
	Error     string        `json:"error,omitempty"`
	Duration  time.Duration `json:"duration_ms"`
}

// **********************************************************************
// Message - eine Chat-Nachricht
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// **********************************************************************
// Session - Gesprächsverlauf
type Session struct {
	History []Message `json:"history"`
}

// LoadSession lädt eine Session aus einer JSON-Datei
func LoadSession(sessionID, model string) *Session {
	if sessionID == "" {
		return &Session{}
	}
	path := fmt.Sprintf(".sessions/%s-%s.json", model, sessionID)
	data, err := os.ReadFile(path)
	if err != nil {
		return &Session{}
	}
	var s Session
	json.Unmarshal(data, &s)
	return &s
}

// Save speichert eine Session auf Disk
func (s *Session) Save(sessionID, model string) {
	if sessionID == "" {
		return
	}
	os.MkdirAll(".sessions", 0755)
	path := fmt.Sprintf(".sessions/%s-%s.json", model, sessionID)
	data, _ := json.Marshal(s)
	os.WriteFile(path, data, 0644)
}

// AddMessage fügt eine Nachricht zur Session hinzu (max. 20)
func (s *Session) AddMessage(role, content string) {
	s.History = append(s.History, Message{Role: role, Content: content})
	if len(s.History) > 20 {
		s.History = s.History[len(s.History)-20:]
	}
}

// BuildMessages baut eine OpenAI-kompatible Messages-Liste auf
func (s *Session) BuildMessages(newPrompt string) []map[string]string {
	var msgs []map[string]string
	for _, m := range s.History {
		msgs = append(msgs, map[string]string{"role": m.Role, "content": m.Content})
	}
	msgs = append(msgs, map[string]string{"role": "user", "content": newPrompt})
	return msgs
}

// **********************************************************************
// CircuitBreaker Konstanten - konfigurierbar
const (
	DefaultCBThreshold   = 5
	DefaultCBWindow      = 60 * time.Second
	DefaultCBCooldown    = 10 * time.Second
	DefaultCBHalfOpenMax = 3
)

// CircuitBreakerState repräsentiert den Zustand des Circuit Breakers
type CircuitBreakerState int

const (
	CBStateClosed CircuitBreakerState = iota
	CBStateOpen
	CBStateHalfOpen
)

func (s CircuitBreakerState) String() string {
	switch s {
	case CBStateClosed:
		return "closed"
	case CBStateOpen:
		return "open"
	case CBStateHalfOpen:
		return "half_open"
	default:
		return "unknown"
	}
}

// CircuitBreakerConfig - Konfiguration für Enhanced Circuit Breaker
type CircuitBreakerConfig struct {
	Threshold   int           // Anzahl Fehler bevor Circuit öffnet
	Window      time.Duration // Zeitfenster für Fehlerzählung
	Cooldown    time.Duration // Zeit bis Half-Open nach Open
	HalfOpenMax int           // Max Requests in Half-Open State
}

// DefaultCircuitBreakerConfig gibt Standard-Konfiguration zurück
func DefaultCircuitBreakerConfig() *CircuitBreakerConfig {
	return &CircuitBreakerConfig{
		Threshold:   DefaultCBThreshold,
		Window:      DefaultCBWindow,
		Cooldown:    DefaultCBCooldown,
		HalfOpenMax: DefaultCBHalfOpenMax,
	}
}

// CircuitBreaker - verhindert Kaskaden-Fehler (Legacy, für Rückwärtskompatibilität)
type CircuitBreaker struct {
	failures  int
	lastFail  time.Time
	threshold int
	timeout   time.Duration
	mu        sync.Mutex
}

// NewCircuitBreaker erstellt einen neuen Circuit Breaker (Legacy)
func NewCircuitBreaker() *CircuitBreaker {
	return &CircuitBreaker{threshold: 3, timeout: 5 * time.Minute}
}

// Do führt fn aus, öffnet den Circuit bei zu vielen Fehlern
func (cb *CircuitBreaker) Do(fn func() error) error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if time.Since(cb.lastFail) > cb.timeout {
		cb.failures = 0
	}
	if cb.failures >= cb.threshold {
		LogWarn("Circuit breaker open", map[string]interface{}{
			"failures": cb.failures, "threshold": cb.threshold,
		})
		return NewError(ErrCircuitOpen, "Circuit breaker open", nil, map[string]interface{}{
			"failures": cb.failures, "threshold": cb.threshold,
		})
	}

	err := fn()
	if err != nil {
		cb.failures++
		cb.lastFail = time.Now()
	} else {
		cb.failures = 0
	}
	return err
}

// IsOpen prüft ob der Circuit Breaker offen ist
func (cb *CircuitBreaker) IsOpen() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if time.Since(cb.lastFail) > cb.timeout {
		return false
	}
	return cb.failures >= cb.threshold
}

// Failures gibt die aktuelle Fehleranzahl zurück
func (cb *CircuitBreaker) Failures() int {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.failures
}

// **********************************************************************
// EnhancedCircuitBreaker - State-Machine mit zeitlichem Fenster
type EnhancedCircuitBreaker struct {
	config           *CircuitBreakerConfig
	state            CircuitBreakerState
	failures         []time.Time // Zeitstempel der Fehler im Window
	halfOpenAttempts int
	lastStateChange  time.Time
	mu               sync.RWMutex
}

// NewEnhancedCircuitBreaker erstellt einen neuen Enhanced Circuit Breaker
func NewEnhancedCircuitBreaker(config *CircuitBreakerConfig) *EnhancedCircuitBreaker {
	if config == nil {
		config = DefaultCircuitBreakerConfig()
	}
	return &EnhancedCircuitBreaker{
		config:          config,
		state:           CBStateClosed,
		failures:        make([]time.Time, 0),
		lastStateChange: time.Now(),
	}
}

// State gibt den aktuellen Zustand zurück
func (cb *EnhancedCircuitBreaker) State() CircuitBreakerState {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// cleanupOldFailures entfernt Fehler außerhalb des Zeitfensters
func (cb *EnhancedCircuitBreaker) cleanupOldFailures() {
	cutoff := time.Now().Add(-cb.config.Window)
	newFailures := make([]time.Time, 0)
	for _, t := range cb.failures {
		if t.After(cutoff) {
			newFailures = append(newFailures, t)
		}
	}
	cb.failures = newFailures
}

// Do führt fn aus mit State-Machine-Logik
func (cb *EnhancedCircuitBreaker) Do(fn func() error) error {
	cb.mu.Lock()

	// Prüfe ob wir von Open -> Half-Open wechseln können
	if cb.state == CBStateOpen {
		if time.Since(cb.lastStateChange) >= cb.config.Cooldown {
			LogInfo("Circuit breaker entering half-open", map[string]interface{}{
				"previous_failures": len(cb.failures),
			})
			cb.state = CBStateHalfOpen
			cb.halfOpenAttempts = 0
			cb.lastStateChange = time.Now()
		} else {
			cb.mu.Unlock()
			return NewError(ErrCircuitOpen, "Circuit breaker open", nil, map[string]interface{}{
				"cooldown_remaining": time.Since(cb.lastStateChange) - cb.config.Cooldown,
			})
		}
	}

	// In Half-Open: begrenzte Requests erlauben
	if cb.state == CBStateHalfOpen && cb.halfOpenAttempts >= cb.config.HalfOpenMax {
		cb.mu.Unlock()
		return NewError(ErrCircuitOpen, "Circuit breaker half-open, max attempts reached", nil, nil)
	}

	if cb.state == CBStateHalfOpen {
		cb.halfOpenAttempts++
	}

	cb.cleanupOldFailures()
	cb.mu.Unlock()

	// Funktion ausführen
	err := fn()

	cb.mu.Lock()
	defer cb.mu.Unlock()

	if err != nil {
		// Fehler klassifizieren - nur retryable Fehler zählen
		apiErr := ClassifyError(err)
		if apiErr.IsRetryable() {
			cb.failures = append(cb.failures, time.Now())

			// Prüfe ob Circuit geöffnet werden muss
			if len(cb.failures) >= cb.config.Threshold {
				if cb.state != CBStateOpen {
					LogWarn("Circuit breaker opened", map[string]interface{}{
						"failures":  len(cb.failures),
						"threshold": cb.config.Threshold,
					})
					cb.state = CBStateOpen
					cb.lastStateChange = time.Now()
				}
			} else if cb.state == CBStateHalfOpen {
				// In Half-Open: sofort wieder auf Open
				cb.state = CBStateOpen
				cb.lastStateChange = time.Now()
				LogWarn("Circuit breaker re-opened from half-open", nil)
			}
		}
	} else {
		// Erfolg: bei Half-Open -> Closed, sonst Fehler zurücksetzen
		if cb.state == CBStateHalfOpen {
			LogInfo("Circuit breaker closed (recovered)", nil)
			cb.state = CBStateClosed
			cb.failures = make([]time.Time, 0)
			cb.lastStateChange = time.Now()
		} else if cb.state == CBStateClosed {
			// Im Closed State: alte Fehler bereinigen
			cb.cleanupOldFailures()
		}
	}

	return err
}

// IsOpen prüft ob der Circuit Breaker offen ist
func (cb *EnhancedCircuitBreaker) IsOpen() bool {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	if cb.state == CBStateOpen {
		// Prüfe ob Cooldown abgelaufen
		if time.Since(cb.lastStateChange) >= cb.config.Cooldown {
			return false // Würde bei Do() zu Half-Open wechseln
		}
		return true
	}
	return false
}

// Failures gibt die aktuelle Fehleranzahl im Zeitfenster zurück
func (cb *EnhancedCircuitBreaker) Failures() int {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.cleanupOldFailures()
	return len(cb.failures)
}

// GetStateDetails gibt detaillierte Informationen für Health Checks
func (cb *EnhancedCircuitBreaker) GetStateDetails() map[string]interface{} {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	return map[string]interface{}{
		"state":             cb.state.String(),
		"failures":          len(cb.failures),
		"threshold":         cb.config.Threshold,
		"window_seconds":    cb.config.Window.Seconds(),
		"cooldown_seconds":  cb.config.Cooldown.Seconds(),
		"half_open_max":     cb.config.HalfOpenMax,
		"half_open_attempts": cb.halfOpenAttempts,
		"last_state_change": cb.lastStateChange.Format(time.RFC3339),
	}
}

// **********************************************************************
// RetryConfig - Konfiguration für Exponential Backoff
type RetryConfig struct {
	MaxRetries     int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	BackoffFactor  float64
}

// DefaultRetryConfig gibt Standard-Retry-Konfiguration zurück
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:     3,
		InitialBackoff: 500 * time.Millisecond,
		MaxBackoff:     5 * time.Second,
		BackoffFactor:  2.0,
	}
}

// max gibt das Maximum von zwei time.Duration zurück
func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

// RetryWithBackoff führt eine Funktion mit Exponential Backoff Retry aus
func RetryWithBackoff(ctx context.Context, config RetryConfig, fn func() error) error {
	backoff := config.InitialBackoff

	for attempt := 0; attempt <= config.MaxRetries; attempt++ {
		err := fn()
		if err == nil {
			return nil
		}

		// Letzter Versuch oder kein Retry möglich
		if attempt == config.MaxRetries {
			return err
		}

		// Fehler klassifizieren
		apiErr := ClassifyError(err)

		// Kein Retry bei Client-Fehlern oder Auth-Fehlern
		if !apiErr.IsRetryable() {
			LogDebug("Retry skipped (non-retryable error)", map[string]interface{}{
				"error_type": apiErr.Type,
				"attempt":    attempt + 1,
			})
			return err
		}

		// Retry-After aus Rate-Limit-Fehler extrahieren
		sleepDuration := backoff
		if apiErr.Type == ErrRateLimit && apiErr.RetryAfter > 0 {
			sleepDuration = apiErr.RetryAfter
			LogDebug("Using Retry-After header", map[string]interface{}{
				"retry_after_seconds": sleepDuration.Seconds(),
			})
		}

		LogDebug("Retrying after error", map[string]interface{}{
			"error_type":     apiErr.Type,
			"attempt":        attempt + 1,
			"max_retries":    config.MaxRetries,
			"backoff_ms":     sleepDuration.Milliseconds(),
			"next_backoff_ms": minDuration(time.Duration(float64(backoff)*config.BackoffFactor), config.MaxBackoff).Milliseconds(),
		})

		// Warte mit Context-Respektierung
		select {
		case <-ctx.Done():
			return NewError(ErrTimeout, "Context cancelled during retry backoff", ctx.Err(), nil)
		case <-time.After(sleepDuration):
		}

		// Backoff verdoppeln (exponentiell), aber MaxBackoff nicht überschreiten
		backoff = minDuration(time.Duration(float64(backoff)*config.BackoffFactor), config.MaxBackoff)
	}

	return nil // Sollte nie erreicht werden
}

// minDuration gibt das Minimum von zwei time.Duration zurück
func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

// **********************************************************************
// ProviderHealth - Status eines Providers für Health Checks
type ProviderHealth struct {
	Model          string                 `json:"model"`
	Status         string                 `json:"status"` // "available", "unavailable", "circuit_open"
	Latency        time.Duration          `json:"latency_ms"`
	LastChecked    time.Time              `json:"last_checked"`
	Error          string                 `json:"error,omitempty"`
	CircuitDetails map[string]interface{} `json:"circuit_details,omitempty"`
}

// ProbeProvider prüft die Erreichbarkeit eines Providers mit Timeout
func ProbeProvider(ctx context.Context, cfg *ProviderConfig) ProviderHealth {
	start := time.Now()
	health := ProviderHealth{
		Model:       cfg.Model,
		LastChecked: start,
	}

	// Kurzer Probe-Request mit kleinem Timeout
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Einfacher Request - wir senden einen ungültigen Prompt um schnell eine Antwort zu bekommen
	// (entweder Fehler oder schneller Erfolg ohne viele Token)
	probeRequest := map[string]interface{}{
		"model":    cfg.Model,
		"messages": []map[string]string{{"role": "user", "content": "ping"}},
		"max_tokens": 1,
	}

	_, err := CallAPI(probeCtx, cfg, probeRequest, 5)
	health.Latency = time.Since(start)

	if err != nil {
		// Prüfe ob es ein erwarteter Fehler ist (z.B. ungültiger API-Key)
		// oder ein Verbindungsfehler
		apiErr := ClassifyError(err)

		switch apiErr.Type {
		case ErrAuthFailed:
			// Auth-Fehler bedeutet der Server ist erreichbar
			health.Status = "available"
			health.Error = "auth_check_required"
		case ErrRateLimit:
			health.Status = "available"
			health.Error = "rate_limited"
		case ErrTimeout:
			health.Status = "unavailable"
			health.Error = "timeout"
		default:
			health.Status = "unavailable"
			health.Error = err.Error()
		}
	} else {
		health.Status = "available"
	}

	return health
}

// **********************************************************************
// CallAPI führt einen HTTP-Call zu einem AI-Provider durch
func CallAPI(ctx context.Context, cfg *ProviderConfig, request map[string]interface{},
	timeoutSec int) (string, error) {

	start := time.Now()
	logF := map[string]interface{}{"endpoint": cfg.Endpoint, "model": cfg.Model}

	LogDebug("Making API request", logF)

	client := &http.Client{Timeout: time.Duration(timeoutSec) * time.Second}
	jsonData, _ := json.Marshal(request)

	req, err := http.NewRequestWithContext(ctx, "POST", cfg.Endpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		LogError("Failed to create request", err, logF)
		return "", NewError(ErrAPIFailed, "Failed to create HTTP request", err, logF)
	}

	req.Header.Set("Content-Type", "application/json")
	if cfg.Type == "anthropic" {
		req.Header.Set("x-api-key", cfg.APIKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	} else if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	for k, v := range cfg.Headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		LogError("HTTP request failed", err, logF)
		return "", NewError(ErrAPIFailed, "HTTP request failed", err, logF)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		logF["status_code"] = resp.StatusCode
		logF["body"] = string(body)
		LogError("HTTP error", nil, logF)

		// Retry-After Header parsen
		var retryAfter time.Duration
		if retryHeader := resp.Header.Get("Retry-After"); retryHeader != "" {
			if seconds, err := strconv.Atoi(retryHeader); err == nil {
				retryAfter = time.Duration(seconds) * time.Second
			}
		}

		// APIError mit Status-Code erstellen
		apiErr := classifyHTTPError(resp.StatusCode, string(body), nil)
		apiErr.RetryAfter = retryAfter
		return "", apiErr
	}

	body, _ := io.ReadAll(resp.Body)
	LogDebug("API response", map[string]interface{}{
		"size_bytes":  len(body),
		"duration_ms": time.Since(start).Milliseconds(),
	})

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		LogError("Failed to parse response", err, logF)
		return "", NewError(ErrAPIFailed, "Failed to parse JSON response", err, logF)
	}

	// Fehler in der API-Antwort
	if errMsg, ok := result["error"].(map[string]interface{}); ok {
		errText := fmt.Sprintf("%v", errMsg["message"])
		LogError("API error in response", nil, map[string]interface{}{"api_error": errText})
		return "", NewError(ErrAPIFailed, errText, nil, logF)
	}

	// Anthropic-Format: content[0].text
	if cfg.Type == "anthropic" {
		if content, ok := result["content"].([]interface{}); ok && len(content) > 0 {
			if text, ok := content[0].(map[string]interface{})["text"].(string); ok {
				return text, nil
			}
		}
	}

	// OpenAI-Format: choices[0].message.content
	if choices, ok := result["choices"].([]interface{}); ok && len(choices) > 0 {
		if msg, ok := choices[0].(map[string]interface{})["message"].(map[string]interface{}); ok {
			if content, ok := msg["content"].(string); ok {
				return content, nil
			}
		}
	}

	LogError("Unexpected response format", nil, logF)
	return "", NewError(ErrUnexpectedFormat, "Unexpected response format", nil, logF)
}
