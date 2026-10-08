package sigoengine

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestCallAPIStream_RequestsUsage: TODO 20261008 — OpenAI-kompatible
// Provider schicken im Stream nur dann einen usage-Chunk, wenn
// stream_options.include_usage gesetzt ist. Ohne die Option fiel sigoREST
// still auf EstimateUsage (Runen/3, ohne Cache-Rabatt) zurück und buchte
// für einen Claude-Code-Tag $80 statt der tatsächlich abgerechneten ~$20.
func TestCallAPIStream_RequestsUsage(t *testing.T) {
	cases := []struct {
		name    string
		request map[string]interface{}
		want    map[string]interface{}
	}{
		{"ohne stream_options", map[string]interface{}{"model": "m"},
			map[string]interface{}{"include_usage": true}},
		{"Client-Wert bleibt erhalten", map[string]interface{}{"model": "m", "stream_options": map[string]interface{}{"include_usage": false}},
			map[string]interface{}{"include_usage": false}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got map[string]interface{}
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(body, &got)
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, "data: [DONE]\n\n")
			}))
			defer ts.Close()

			stream, err := CallAPIStream(context.Background(), &ProviderConfig{Endpoint: ts.URL, Model: "m", Type: "openai"}, tc.request)
			if err != nil {
				t.Fatalf("CallAPIStream: %v", err)
			}
			stream.Close()

			so, ok := got["stream_options"].(map[string]interface{})
			if !ok {
				t.Fatalf("stream_options fehlt im Request-Body: %v", got)
			}
			if so["include_usage"] != tc.want["include_usage"] {
				t.Fatalf("stream_options = %v, want %v", so, tc.want)
			}
		})
	}
}
