package sigoengine

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// syncBuffer: bytes.Buffer ist nicht thread-safe, der Logger schreibt aber
// unter eigenem Mutex — der Test liest erst nach Abschluss.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) lines(t *testing.T) []map[string]interface{} {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []map[string]interface{}
	for _, ln := range strings.Split(strings.TrimRight(s.b.String(), "\n"), "\n") {
		if ln == "" {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			t.Fatalf("keine gültige JSONL-Zeile: %v\n%s", err, ln)
		}
		out = append(out, m)
	}
	return out
}

// withCommLog aktiviert einen Test-Logger und setzt ihn danach zurück.
func withCommLog(t *testing.T) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	SetCommLog(NewCommLogger(buf))
	t.Cleanup(func() { SetCommLog(nil) })
	return buf
}

func TestCommLog_FullRequestResponseRedacted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"model":"m1"`) {
			t.Errorf("Upstream bekam Body nicht unverändert: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		// mehrzeiliges JSON: darf die JSONL-Zeile nicht zerreißen
		io.WriteString(w, "{\n  \"id\": \"r1\",\n  \"ok\": true\n}")
	}))
	defer srv.Close()
	buf := withCommLog(t)

	client := &http.Client{Transport: NewCommLogTransport(nil)}
	req, _ := http.NewRequest("POST", srv.URL+"/v1/chat/completions", strings.NewReader(`{"model":"m1"}`))
	req.Header.Set("Authorization", "Bearer sk-geheim")
	req.Header.Set("x-api-key", "sk-auch-geheim")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(got), `"r1"`) {
		t.Fatalf("Aufrufer bekam Body nicht unverändert: %s", got)
	}

	raw := buf.b.String()
	if strings.Contains(raw, "sk-geheim") || strings.Contains(raw, "sk-auch-geheim") {
		t.Fatalf("API-Key im Log:\n%s", raw)
	}
	lines := buf.lines(t)
	if len(lines) != 1 {
		t.Fatalf("erwartet 1 Zeile, bekommen %d:\n%s", len(lines), raw)
	}
	e := lines[0]
	if e["method"] != "POST" || e["status"] != float64(200) {
		t.Errorf("method/status falsch: %v", e)
	}
	if rb, ok := e["request_body"].(map[string]interface{}); !ok || rb["model"] != "m1" {
		t.Errorf("request_body nicht als JSON-Objekt: %#v", e["request_body"])
	}
	if rb, ok := e["response_body"].(map[string]interface{}); !ok || rb["id"] != "r1" {
		t.Errorf("response_body nicht als JSON-Objekt: %#v", e["response_body"])
	}
	hdr := e["request_headers"].(map[string]interface{})
	if hdr["Authorization"] != commLogRedacted || hdr["X-Api-Key"] != commLogRedacted {
		t.Errorf("Header nicht maskiert: %v", hdr)
	}
}

// TestCommLog_StreamLoggedOnClose: SSE-Body wird mitgeschnitten und erst beim
// Close geschrieben — auch wenn der Aufrufer nicht bis EOF liest.
func TestCommLog_StreamLoggedOnClose(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"a\":1}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	buf := withCommLog(t)

	client := &http.Client{Transport: NewCommLogTransport(nil)}
	resp, err := client.Post(srv.URL, "application/json", strings.NewReader(`{"stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	if n := len(buf.lines(t)); n != 0 {
		t.Fatalf("vor Close schon %d Zeilen geschrieben", n)
	}
	resp.Body.Close()
	resp.Body.Close() // doppeltes Close darf nicht doppelt loggen

	lines := buf.lines(t)
	if len(lines) != 1 {
		t.Fatalf("erwartet 1 Zeile, bekommen %d", len(lines))
	}
	if s, _ := lines[0]["response_body"].(string); !strings.Contains(s, "data: [DONE]") {
		t.Errorf("SSE-Body fehlt: %#v", lines[0]["response_body"])
	}
}

// TestCommLog_TransportError: Verbindungsfehler wird mit error-Feld geloggt.
func TestCommLog_TransportError(t *testing.T) {
	buf := withCommLog(t)
	failing := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("dial tcp: connection refused")
	})
	client := &http.Client{Transport: NewCommLogTransport(failing)}
	_, err := client.Post("http://127.0.0.1:1/x", "application/json", strings.NewReader(`{}`))
	if err == nil {
		t.Fatal("Fehler erwartet")
	}
	lines := buf.lines(t)
	if len(lines) != 1 || !strings.Contains(lines[0]["error"].(string), "connection refused") {
		t.Fatalf("Fehler-Eintrag fehlt: %v", lines)
	}
}

// TestCommLog_DisabledIsPassThrough: ohne aktiven Logger nichts schreiben.
func TestCommLog_DisabledIsPassThrough(t *testing.T) {
	SetCommLog(nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	}))
	defer srv.Close()
	client := &http.Client{Transport: NewCommLogTransport(nil)}
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "ok" {
		t.Fatalf("Body = %q", b)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
