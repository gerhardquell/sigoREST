package sigoengine

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// TestRateLimiterFirstCallImmediate: erster Acquire blockiert nicht.
func TestRateLimiterFirstCallImmediate(t *testing.T) {
	rl := NewRateLimiter()
	ctx := context.Background()
	start := time.Now()
	if err := rl.Acquire(ctx, "k1", 200*time.Millisecond, 1*time.Second); err != nil {
		t.Fatalf("erster Acquire fehler: %v", err)
	}
	rl.Release("k1")
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Fatalf("erster Acquire sollte sofort sein, war %v", d)
	}
}

// TestRateLimiterWaitsForMinInterval: zweiter Call innerhalb minInterval wartet.
func TestRateLimiterWaitsForMinInterval(t *testing.T) {
	rl := NewRateLimiter()
	ctx := context.Background()

	if err := rl.Acquire(ctx, "k1", 200*time.Millisecond, 1*time.Second); err != nil {
		t.Fatalf("erster Acquire: %v", err)
	}
	rl.Release("k1")

	start := time.Now()
	if err := rl.Acquire(ctx, "k1", 200*time.Millisecond, 1*time.Second); err != nil {
		t.Fatalf("zweiter Acquire: %v", err)
	}
	rl.Release("k1")
	d := time.Since(start)
	if d < 150*time.Millisecond {
		t.Fatalf("zweiter Acquire sollte ~200ms warten, war nur %v", d)
	}
}

// TestRateLimiterReturns429WhenMaxWaitExceeded: maxWait zu kurz → ErrRateLimited.
func TestRateLimiterReturns429WhenMaxWaitExceeded(t *testing.T) {
	rl := NewRateLimiter()
	ctx := context.Background()

	if err := rl.Acquire(ctx, "k1", 500*time.Millisecond, 50*time.Millisecond); err != nil {
		t.Fatalf("erster Acquire: %v", err)
	}
	rl.Release("k1")

	err := rl.Acquire(ctx, "k1", 500*time.Millisecond, 50*time.Millisecond)
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("erwartet ErrRateLimited, got %v", err)
	}
}

// TestRateLimiterContextCancel: ctx-Abbruch während Wartezeit bricht ab.
func TestRateLimiterContextCancel(t *testing.T) {
	rl := NewRateLimiter()
	ctx, cancel := context.WithCancel(context.Background())

	if err := rl.Acquire(ctx, "k1", 500*time.Millisecond, 2*time.Second); err != nil {
		t.Fatalf("erster Acquire: %v", err)
	}
	rl.Release("k1")

	go func() {
		time.Sleep(80 * time.Millisecond)
		cancel()
	}()
	err := rl.Acquire(ctx, "k1", 500*time.Millisecond, 2*time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("erwartet context.Canceled, got %v", err)
	}
}

// TestRateLimiterIndependentKeys: verschiedene Keys blockieren sich nicht.
func TestRateLimiterIndependentKeys(t *testing.T) {
	rl := NewRateLimiter()
	ctx := context.Background()

	if err := rl.Acquire(ctx, "k1", 500*time.Millisecond, 1*time.Second); err != nil {
		t.Fatalf("k1 erster: %v", err)
	}
	rl.Release("k1")

	start := time.Now()
	if err := rl.Acquire(ctx, "k2", 500*time.Millisecond, 1*time.Second); err != nil {
		t.Fatalf("k2 sollte sofort ok sein: %v", err)
	}
	rl.Release("k2")
	if d := time.Since(start); d > 50*time.Millisecond {
		t.Fatalf("k2 sollte nicht warten, war %v", d)
	}
}

// TestRateLimiterConcurrentSerializes: parallele Acquires serialisieren pro Key.
func TestRateLimiterConcurrentSerializes(t *testing.T) {
	rl := NewRateLimiter()
	ctx := context.Background()
	const n = 5
	const interval = 80 * time.Millisecond

	var wg sync.WaitGroup
	timestamps := make([]time.Time, n)
	var mu sync.Mutex
	idx := 0

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := rl.Acquire(ctx, "k1", interval, 5*time.Second); err != nil {
				t.Errorf("Acquire: %v", err)
				return
			}
			defer rl.Release("k1")
			mu.Lock()
			timestamps[idx] = time.Now()
			idx++
			mu.Unlock()
		}()
	}
	wg.Wait()

	// Sortiere Timestamps (goroutine-Reihenfolge willkürlich) und prüfe Abstände.
	mu.Lock()
	defer mu.Unlock()
	// Bubble-Sort reicht für n=5
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if timestamps[j].Before(timestamps[i]) {
				timestamps[i], timestamps[j] = timestamps[j], timestamps[i]
			}
		}
	}
	for i := 1; i < n; i++ {
		gap := timestamps[i].Sub(timestamps[i-1])
		if gap < interval-20*time.Millisecond {
			t.Fatalf("Abstand %d→%d nur %v, erwartet >= %v", i-1, i, gap, interval-20*time.Millisecond)
		}
	}
}
