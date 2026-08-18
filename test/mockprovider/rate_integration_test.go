package mockprovider

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"sigorest/sigoengine"
)

// postSend schickt einen Chat-Completion-Request an den Mock.
func postSend(url string) (int, error) {
	body := strings.NewReader(`{"model":"mock","messages":[{"role":"user","content":"hi"}]}`)
	req, err := http.NewRequest(http.MethodPost, url+"/v1/chat/completions", body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode, nil
}

// TestMockRejectsBurstOhneLimiter: 8 parallele Requests gegen Mock mit rps=4.
// Ohne Rate-Limiter muss der Mock mindestens einen 429 liefern.
func TestMockRejectsBurstOhneLimiter(t *testing.T) {
	mp := New(4) // 4 req/s erlaubt
	defer mp.Close()

	const n = 8
	results := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			code, err := postSend(mp.URL())
			if err != nil {
				t.Errorf("req %d: %v", i, err)
				return
			}
			results[i] = code
		}(i)
	}
	wg.Wait()

	rejected := 0
	for _, c := range results {
		if c == http.StatusTooManyRequests {
			rejected++
		}
	}
	if rejected == 0 {
		t.Fatalf("erwartet ≥1 429 ohne Limiter, got 0 (codes: %v)", results)
	}
	t.Logf("ohne Limiter: %d/%d Requests mit 429 abgewiesen", rejected, n)
}

// TestLimiterSchuetztMock: gleicher Burst, aber RateLimiter davor
// (minInterval=300ms ≈ 3.3 req/s < mock rps=4). Erwartung: 0 429.
func TestLimiterSchuetztMock(t *testing.T) {
	mp := New(4) // 4 req/s erlaubt
	defer mp.Close()

	rl := sigoengine.NewRateLimiter()
	const minInterval = 300 * time.Millisecond
	const maxWait = 10 * time.Second
	const n = 8

	results := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx := context.Background()
			if err := rl.Acquire(ctx, "mock-0", minInterval, maxWait); err != nil {
				t.Errorf("req %d Acquire: %v", i, err)
				return
			}
			defer rl.Release("mock-0")
			code, err := postSend(mp.URL())
			if err != nil {
				t.Errorf("req %d: %v", i, err)
				return
			}
			results[i] = code
		}(i)
	}
	wg.Wait()

	rejected := 0
	for _, c := range results {
		if c == http.StatusTooManyRequests {
			rejected++
		}
	}
	stats := mp.Stats()
	if rejected != 0 {
		t.Fatalf("erwartet 0 429 mit Limiter, got %d (total=%d rejected=%d)",
			rejected, stats.Total, stats.Rejected)
	}
	t.Logf("mit Limiter: 0/%d 429 — Mock sah %d Requests, 0 abgewiesen", n, stats.Total)
}
