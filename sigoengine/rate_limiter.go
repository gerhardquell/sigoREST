//**********************************************************************
//      sigoengine/rate_limiter.go
//**********************************************************************
//  Beschreibung: Pro-Kanal Rate-Limiter (hybrid).
//  Acquire blockiert bis minInterval seit letztem Call vergangen,
//  spätestens nach maxWait → ErrRateLimited (→ HTTP 429).
//**********************************************************************

package sigoengine

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrRateLimited signalisiert: Kanal ist innerhalb maxWait nicht frei geworden.
// Vom Server als HTTP 429 + Retry-After gemappt.
var ErrRateLimited = errors.New("rate_limit_exceeded")

// RateLimiter verwaltet pro Kanal-Schlüssel den letzten Call-Zeitstempel.
// Runtime-State (nicht in channels.json persistiert).
type RateLimiter struct {
	mu       sync.Mutex
	lastCall map[string]time.Time
}

// NewRateLimiter erzeugt einen leeren Limiter.
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{lastCall: make(map[string]time.Time)}
}

// Acquire wartet bis minInterval seit letztem Call vergangen ist.
// Schläft höchstens maxWait; danach ErrRateLimited.
// ctx-Abbruch bricht Wartezeit ab (→ ctx.Err()).
// Bei Erfolg wird lastCall sofort gesetzt, damit parallele Acquires
// sich serialisieren.
func (rl *RateLimiter) Acquire(ctx context.Context, channelKey string, minInterval, maxWait time.Duration) error {
	deadline := time.Now().Add(maxWait)
	for {
		// Context schon vorbei?
		if err := ctx.Err(); err != nil {
			return err
		}

		rl.mu.Lock()
		now := time.Now()
		last := rl.lastCall[channelKey]
		wait := minInterval - now.Sub(last)
		if wait <= 0 {
			rl.lastCall[channelKey] = now
			rl.mu.Unlock()
			return nil
		}
		rl.mu.Unlock()

		// Wartezeit würde deadline überschreiten → bis deadline schlafen,
		// dann endgültig prüfen.
		if now.Add(wait).After(deadline) {
			sleep := time.Until(deadline)
			if sleep > 0 {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(sleep):
				}
			}
			// Letzter Versuch nach deadline.
			rl.mu.Lock()
			now = time.Now()
			last = rl.lastCall[channelKey]
			if minInterval-now.Sub(last) <= 0 {
				rl.lastCall[channelKey] = now
				rl.mu.Unlock()
				return nil
			}
			rl.mu.Unlock()
			return ErrRateLimited
		}

		// Innerhalb deadline: voll warten, dann Loop erneut
		// (lastCall könnte von anderer goroutine aktualisiert worden sein).
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

// Release ist aktuell ein Noop — lastCall wird in Acquire bei Erfolg
// gesetzt. Existiert als symmetrisches Gegenstück für künftige
// Semaphor-/Waiter-Zählung und explizite Aufrufstelle im Handler.
func (rl *RateLimiter) Release(channelKey string) {}
