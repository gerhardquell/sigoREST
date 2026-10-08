package sigoengine

import "testing"

// mammouthBudgetBody ist der echte Body aus dem Journal vom 2026-10-08
// (LiteLLM-Gateway hinter Mammouth): HTTP 429, aber kein Rate-Limit,
// sondern ein erschöpftes User-Budget.
const mammouthBudgetBody = `{"error":{"message":"ExceededBudget: User=93085 over budget. Spend=21.67904175, Budget=20.81356348856591","type":"budget_exceeded","param":null,"code":"429"}}`

func TestClassifyHTTPError_BudgetExceeded429IsQuotaNotRetryable(t *testing.T) {
	e := classifyHTTPError(429, mammouthBudgetBody, nil)
	if e.Type != ErrQuotaExceeded {
		t.Fatalf("Type = %q, want %q", e.Type, ErrQuotaExceeded)
	}
	if e.IsRetryable() {
		t.Fatal("Budget-Fehler darf nicht wiederholt werden")
	}
}

func TestClassifyHTTPError_OpenAIInsufficientQuotaIsQuota(t *testing.T) {
	body := `{"error":{"message":"You exceeded your current quota","type":"insufficient_quota","code":"insufficient_quota"}}`
	if e := classifyHTTPError(429, body, nil); e.Type != ErrQuotaExceeded {
		t.Fatalf("Type = %q, want %q", e.Type, ErrQuotaExceeded)
	}
}

func TestClassifyHTTPError_Plain429StaysRateLimit(t *testing.T) {
	e := classifyHTTPError(429, `{"error":{"message":"Rate limit reached, slow down"}}`, nil)
	if e.Type != ErrRateLimit || !e.IsRetryable() {
		t.Fatalf("Type = %q retryable=%v, want %q retryable", e.Type, e.IsRetryable(), ErrRateLimit)
	}
}
