//**********************************************************************
//      sigoengine/commlog.go
//**********************************************************************
//  Beschreibung: Optionales Protokoll der Provider-Kommunikation
//                (Chat + Embeddings) als JSONL, ein Eintrag pro Call
//**********************************************************************

package sigoengine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// commLogMaxBody begrenzt den mitgeschnittenen Body pro Richtung; der
	// Datenstrom zum Aufrufer bleibt davon unberührt.
	commLogMaxBody  = 10 << 20
	commLogRedacted = "[REDACTED]"
)

// commLogSensitiveHeaders werden vor dem Schreiben maskiert (kanonische Form).
var commLogSensitiveHeaders = map[string]bool{
	"Authorization":       true,
	"Proxy-Authorization": true,
	"X-Api-Key":           true,
	"Api-Key":             true,
	"Cookie":              true,
	"Set-Cookie":          true,
}

// CommLogger schreibt Einträge als JSON Lines. Thread-safe: ein Eintrag wird
// mit einem einzigen Write unter Mutex geschrieben, parallele Calls
// verschränken sich nicht.
type CommLogger struct {
	mu     sync.Mutex
	w      io.Writer
	closer io.Closer
	seq    atomic.Uint64
}

// NewCommLogger schreibt nach w (für Tests oder eigene Writer).
func NewCommLogger(w io.Writer) *CommLogger {
	return &CommLogger{w: w}
}

// OpenCommLog öffnet path im Append-Modus (legt das Verzeichnis an).
// Rechte 0600: die Datei enthält vollständige Prompts und Antworten.
// Append statt Rename-Rotation → logrotate mit copytruncate verwenden.
func OpenCommLog(path string) (*CommLogger, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return nil, fmt.Errorf("comm-log Verzeichnis: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("comm-log öffnen: %w", err)
	}
	return &CommLogger{w: f, closer: f}, nil
}

// Close schließt die Datei (No-Op für NewCommLogger und nil).
func (l *CommLogger) Close() error {
	if l == nil || l.closer == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.closer.Close()
}

// activeCommLog ist der global aktive Logger; nil = Protokoll aus.
var activeCommLog atomic.Pointer[CommLogger]

// SetCommLog aktiviert (oder mit nil deaktiviert) das Protokoll.
func SetCommLog(l *CommLogger) {
	activeCommLog.Store(l)
}

// commLogEntry ist eine JSONL-Zeile.
type commLogEntry struct {
	Seq               uint64            `json:"seq"`
	Time              time.Time         `json:"ts"`
	DurationMs        int64             `json:"duration_ms"`
	HeadersMs         int64             `json:"headers_ms,omitempty"` // Zeit bis Response-Header (TTFB)
	Method            string            `json:"method"`
	URL               string            `json:"url"`
	RequestHeaders    map[string]string `json:"request_headers,omitempty"`
	RequestBody       interface{}       `json:"request_body,omitempty"`
	RequestTruncated  bool              `json:"request_truncated,omitempty"`
	Status            int               `json:"status,omitempty"`
	ResponseHeaders   map[string]string `json:"response_headers,omitempty"`
	ResponseBody      interface{}       `json:"response_body,omitempty"`
	ResponseTruncated bool              `json:"response_truncated,omitempty"`
	Error             string            `json:"error,omitempty"`
}

func (l *CommLogger) write(e *commLogEntry) {
	e.Seq = l.seq.Add(1)
	line, err := json.Marshal(e)
	if err != nil {
		LogWarn("comm-log: Eintrag nicht serialisierbar", map[string]interface{}{"error": err.Error()})
		return
	}
	line = append(line, '\n')
	l.mu.Lock()
	_, err = l.w.Write(line)
	l.mu.Unlock()
	if err != nil {
		LogWarn("comm-log: Schreiben fehlgeschlagen", map[string]interface{}{"error": err.Error()})
	}
}

// commLogHeaders flacht Header ab und maskiert Secrets.
func commLogHeaders(h http.Header) map[string]string {
	if len(h) == 0 {
		return nil
	}
	out := make(map[string]string, len(h))
	for k, v := range h {
		if commLogSensitiveHeaders[http.CanonicalHeaderKey(k)] {
			out[k] = commLogRedacted
			continue
		}
		out[k] = strings.Join(v, ", ")
	}
	return out
}

// commLogBody: gültiges JSON als eingebettetes Objekt (lesbar, per jq
// abfragbar; encoding/json kompaktiert RawMessage → bleibt eine Zeile),
// alles andere (SSE, abgeschnittene Bodies) als String.
func commLogBody(b []byte) interface{} {
	if len(b) == 0 {
		return nil
	}
	if json.Valid(b) {
		return json.RawMessage(b)
	}
	return string(b)
}

// capBytes kürzt b auf commLogMaxBody.
func capBytes(b []byte) ([]byte, bool) {
	if len(b) > commLogMaxBody {
		return b[:commLogMaxBody], true
	}
	return b, false
}

// commLogTransport protokolliert jeden Request/Response über base.
type commLogTransport struct {
	base http.RoundTripper
}

// NewCommLogTransport umhüllt base (nil = http.DefaultTransport). Ist kein
// Logger aktiv (SetCommLog(nil)), reicht er Requests unverändert durch.
func NewCommLogTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &commLogTransport{base: base}
}

func (t *commLogTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	l := activeCommLog.Load()
	if l == nil {
		return t.base.RoundTrip(req)
	}

	start := time.Now()
	e := &commLogEntry{
		Time:           start,
		Method:         req.Method,
		URL:            req.URL.String(),
		RequestHeaders: commLogHeaders(req.Header),
	}

	// Request-Body mitlesen, ohne das Original zu verändern (RoundTripper-
	// Vertrag): Kopie via GetBody, sonst Body lesen und auf einem Klon ersetzen.
	if req.Body != nil && req.Body != http.NoBody {
		var body []byte
		if req.GetBody != nil {
			if rc, err := req.GetBody(); err == nil {
				body, _ = io.ReadAll(rc)
				rc.Close()
			}
		} else {
			b, err := io.ReadAll(req.Body)
			req.Body.Close()
			if err != nil {
				return nil, err
			}
			req = req.Clone(req.Context())
			req.Body = io.NopCloser(bytes.NewReader(b))
			body = b
		}
		capped, trunc := capBytes(body)
		e.RequestBody, e.RequestTruncated = commLogBody(capped), trunc
	}

	resp, err := t.base.RoundTrip(req)
	if err != nil {
		e.DurationMs = time.Since(start).Milliseconds()
		e.Error = err.Error()
		l.write(e)
		return nil, err
	}

	e.HeadersMs = time.Since(start).Milliseconds()
	e.Status = resp.StatusCode
	e.ResponseHeaders = commLogHeaders(resp.Header)
	resp.Body = &commLogResponseBody{rc: resp.Body, entry: e, logger: l, start: start}
	return resp, nil
}

// commLogResponseBody schneidet den Response-Body beim Lesen mit und schreibt den
// Eintrag einmalig beim Close — so enthält er bei SSE den ganzen Stream.
type commLogResponseBody struct {
	rc        io.ReadCloser
	buf       bytes.Buffer
	truncated bool
	entry     *commLogEntry
	logger    *CommLogger
	start     time.Time
	once      sync.Once
}

func (b *commLogResponseBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	if n > 0 && !b.truncated {
		room := commLogMaxBody - b.buf.Len()
		if n > room {
			b.buf.Write(p[:room])
			b.truncated = true
		} else {
			b.buf.Write(p[:n])
		}
	}
	return n, err
}

func (b *commLogResponseBody) Close() error {
	err := b.rc.Close()
	b.once.Do(func() {
		b.entry.DurationMs = time.Since(b.start).Milliseconds()
		b.entry.ResponseBody = commLogBody(b.buf.Bytes())
		b.entry.ResponseTruncated = b.truncated
		b.logger.write(b.entry)
	})
	return err
}
