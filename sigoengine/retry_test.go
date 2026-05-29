package sigoengine

import (
	"errors"
	"testing"
	"time"
)

// TestFetchWithRetry_SucceedsAfterTransientFailures simuliert den Boot-Fall:
// DNS noch nicht da (Fehler), dann erfolgreich. Muss bis zum Erfolg retryen.
func TestFetchWithRetry_SucceedsAfterTransientFailures(t *testing.T) {
	attempts := 0
	fetchFn := func() ([]Model, error) {
		attempts++
		if attempts < 3 {
			return nil, errors.New("dial tcp: lookup api.example.ai: no such host")
		}
		return []Model{{ID: "m1"}, {ID: "m2"}}, nil
	}

	models, err := FetchWithRetry("test", 4, time.Millisecond, fetchFn)
	if err != nil {
		t.Fatalf("erwartete Erfolg nach Retries, bekam Fehler: %v", err)
	}
	if attempts != 3 {
		t.Errorf("erwartete 3 Versuche, bekam %d", attempts)
	}
	if len(models) != 2 {
		t.Errorf("erwartete 2 Modelle, bekam %d", len(models))
	}
}

// TestFetchWithRetry_ReturnsErrorAfterExhausting: dauerhafter Fehler →
// gibt nach erschöpften Versuchen den letzten Fehler zurück.
func TestFetchWithRetry_ReturnsErrorAfterExhausting(t *testing.T) {
	attempts := 0
	fetchFn := func() ([]Model, error) {
		attempts++
		return nil, errors.New("permanent failure")
	}

	_, err := FetchWithRetry("test", 4, time.Millisecond, fetchFn)
	if err == nil {
		t.Fatal("erwartete Fehler nach erschöpften Versuchen, bekam nil")
	}
	if attempts != 4 {
		t.Errorf("erwartete 4 Versuche, bekam %d", attempts)
	}
}

// TestFetchWithRetry_SucceedsFirstTry: kein Fehler → genau 1 Versuch, kein Sleep.
func TestFetchWithRetry_SucceedsFirstTry(t *testing.T) {
	attempts := 0
	fetchFn := func() ([]Model, error) {
		attempts++
		return []Model{{ID: "m1"}}, nil
	}

	models, err := FetchWithRetry("test", 4, time.Millisecond, fetchFn)
	if err != nil {
		t.Fatalf("unerwarteter Fehler: %v", err)
	}
	if attempts != 1 {
		t.Errorf("erwartete 1 Versuch, bekam %d", attempts)
	}
	if len(models) != 1 {
		t.Errorf("erwartete 1 Modell, bekam %d", len(models))
	}
}
