package billing

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleCheckout_MissingPlan(t *testing.T) {
	h := &Handler{priceIDs: map[string]string{}}
	body := `{"workspace_id":"test","plan":"unknown"}`
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleCheckout(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleCheckout_InvalidBody(t *testing.T) {
	h := &Handler{priceIDs: map[string]string{}}
	req := httptest.NewRequest("POST", "/", strings.NewReader("not-json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleCheckout(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandlePortal_NoCustomer(t *testing.T) {
	h := &Handler{db: nil}
	body := `{"workspace_id":"no-customer"}`
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandlePortal(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (no customer)", w.Code)
	}
}

func TestHandlePortal_InvalidBody(t *testing.T) {
	h := &Handler{db: nil}
	req := httptest.NewRequest("POST", "/", strings.NewReader("bad"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandlePortal(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleWebhook_NoSignature(t *testing.T) {
	h := &Handler{endpointSecret: "whsec_test"}
	req := httptest.NewRequest("POST", "/", strings.NewReader("{}"))
	w := httptest.NewRecorder()

	h.HandleWebhook(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (no signature)", w.Code)
	}
}

func TestPriceIDToPlan(t *testing.T) {
	priceIDs := map[string]string{
		"bronze": "price_bronze",
		"silver": "price_silver",
		"gold":   "price_gold",
	}

	if p := priceIDToPlan(priceIDs, "price_silver"); p != "silver" {
		t.Errorf("plan = %q, want 'silver'", p)
	}
	if p := priceIDToPlan(priceIDs, "price_unknown"); p != "bronze" {
		t.Errorf("unknown price should default to bronze, got %q", p)
	}
}
