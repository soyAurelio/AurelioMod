// Package billing handles Stripe subscription checkout, customer portal,
// and webhook processing for AurelioMod workspace plans.
//
// Plans map to WaveSpeed concurrency tiers:
//
//	bronze (free) → 3 concurrent tasks
//	silver       → 100 concurrent tasks
//	gold         → 2000 concurrent tasks
//
// Stripe price IDs are configured via env vars (STRIPE_PRICE_BRONZE, etc.)
// so plans can be changed in the Stripe dashboard without code changes.
package billing

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"

	"github.com/stripe/stripe-go/v84"
	"github.com/stripe/stripe-go/v84/checkout/session"
	"github.com/stripe/stripe-go/v84/webhook"

	portal "github.com/stripe/stripe-go/v84/billingportal/session"
)

// Handler manages Stripe billing operations.
type Handler struct {
	db           *sql.DB
	secretKey    string
	endpointSecret string
	baseURL      string
	priceIDs     map[string]string // plan → stripe price ID
}

// New creates a billing Handler.
// stripeKey is the Stripe secret key (sk_live_... or sk_test_...).
// endpointSecret is the webhook signing secret (whsec_...).
// baseURL is the Control API base URL for success/cancel redirects.
func New(db *sql.DB, stripeKey, endpointSecret, baseURL string) *Handler {
	stripe.Key = stripeKey

	return &Handler{
		db:             db,
		secretKey:      stripeKey,
		endpointSecret: endpointSecret,
		baseURL:        baseURL,
		priceIDs: map[string]string{
			"bronze": os.Getenv("STRIPE_PRICE_BRONZE"),
			"silver": os.Getenv("STRIPE_PRICE_SILVER"),
			"gold":   os.Getenv("STRIPE_PRICE_GOLD"),
		},
	}
}

// --- Request / Response types ---

type checkoutRequest struct {
	WorkspaceID string `json:"workspace_id"`
	Plan        string `json:"plan"` // bronze | silver | gold
}

type checkoutResponse struct {
	URL string `json:"url"`
}

type portalRequest struct {
	WorkspaceID string `json:"workspace_id"`
}

type portalResponse struct {
	URL string `json:"url"`
}

// --- Checkout ---

// HandleCheckout creates a Stripe Checkout Session for subscription signup.
//
//	POST /v1/billing/checkout
//	Body: {"workspace_id": "...", "plan": "silver"}
//	200:  {"url": "https://checkout.stripe.com/..."}
func (h *Handler) HandleCheckout(w http.ResponseWriter, r *http.Request) {
	var req checkoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}

	priceID, ok := h.priceIDs[req.Plan]
	if !ok || priceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("unknown or unconfigured plan: %s", req.Plan),
		})
		return
	}

	params := &stripe.CheckoutSessionParams{
		Mode:              stripe.String(string(stripe.CheckoutSessionModeSubscription)),
		ClientReferenceID: stripe.String(req.WorkspaceID),
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			{Price: stripe.String(priceID), Quantity: stripe.Int64(1)},
		},
		SuccessURL: stripe.String(h.baseURL + "/billing/success?session_id={CHECKOUT_SESSION_ID}"),
		CancelURL:  stripe.String(h.baseURL + "/billing/cancel"),
	}

	s, err := session.New(params)
	if err != nil {
		slog.Error("stripe checkout create failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "checkout creation failed"})
		return
	}

	writeJSON(w, http.StatusOK, checkoutResponse{URL: s.URL})
}

// --- Customer Portal ---

// HandlePortal creates a Stripe Customer Portal session for subscription management.
//
//	POST /v1/billing/portal
//	Body: {"workspace_id": "..."}
//	200:  {"url": "https://billing.stripe.com/..."}
func (h *Handler) HandlePortal(w http.ResponseWriter, r *http.Request) {
	var req portalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}

	var customerID string
	err := h.db.QueryRowContext(r.Context(),
		"SELECT stripe_customer_id FROM workspaces WHERE id = $1", req.WorkspaceID,
	).Scan(&customerID)
	if err == sql.ErrNoRows || customerID == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace has no Stripe customer"})
		return
	}
	if err != nil {
		slog.Error("stripe portal lookup failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "portal creation failed"})
		return
	}

	params := &stripe.BillingPortalSessionParams{
		Customer:  stripe.String(customerID),
		ReturnURL: stripe.String(h.baseURL + "/billing"),
	}

	s, err := portal.New(params)
	if err != nil {
		slog.Error("stripe portal create failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "portal creation failed"})
		return
	}

	writeJSON(w, http.StatusOK, portalResponse{URL: s.URL})
}

// --- Webhook ---

// HandleWebhook processes incoming Stripe webhook events.
// Verifies the signature using the endpoint secret, then dispatches
// to the appropriate handler based on event type.
//
//	POST /v1/webhooks/stripe
//	Header: Stripe-Signature: t=...,v1=...
//	200:  {"received": true}
func (h *Handler) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	const maxBodyBytes = int64(65536)
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	payload, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Error("stripe webhook read failed", "error", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "cannot read body"})
		return
	}

	event, err := webhook.ConstructEvent(payload, r.Header.Get("Stripe-Signature"), h.endpointSecret)
	if err != nil {
		slog.Error("stripe webhook signature verification failed", "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid signature"})
		return
	}

	switch event.Type {
	case "checkout.session.completed":
		h.handleCheckoutCompleted(r.Context(), event)
	case "customer.subscription.updated":
		h.handleSubscriptionUpdated(r.Context(), event)
	case "customer.subscription.deleted":
		h.handleSubscriptionDeleted(r.Context(), event)
	default:
		slog.Debug("stripe unhandled event type", "type", event.Type)
	}

	writeJSON(w, http.StatusOK, map[string]bool{"received": true})
}

// handleCheckoutCompleted processes a checkout.session.completed event.
// Updates workspace with stripe_customer_id and stripe_subscription_id.
func (h *Handler) handleCheckoutCompleted(ctx context.Context, event stripe.Event) {
	var session stripe.CheckoutSession
	if err := json.Unmarshal(event.Data.Raw, &session); err != nil {
		slog.Error("stripe: failed to unmarshal checkout session", "error", err)
		return
	}

	workspaceID := session.ClientReferenceID
	if workspaceID == "" {
		slog.Warn("stripe: checkout completed without client_reference_id")
		return
	}

	customerID := session.Customer.ID
	subscriptionID := ""
	if session.Subscription != nil {
		subscriptionID = session.Subscription.ID
	}

	_, err := h.db.ExecContext(ctx,
		`UPDATE workspaces SET stripe_customer_id = $1, stripe_subscription_id = $2, updated_at = NOW() WHERE id = $3`,
		customerID, subscriptionID, workspaceID,
	)
	if err != nil {
		slog.Error("stripe: failed to update workspace billing", "error", err, "workspace_id", workspaceID)
		return
	}

	slog.Info("stripe: checkout completed",
		"workspace_id", workspaceID,
		"customer_id", customerID,
		"subscription_id", subscriptionID,
	)
}

// handleSubscriptionUpdated syncs the workspace plan when a subscription changes.
func (h *Handler) handleSubscriptionUpdated(ctx context.Context, event stripe.Event) {
	var sub stripe.Subscription
	if err := json.Unmarshal(event.Data.Raw, &sub); err != nil {
		slog.Error("stripe: failed to unmarshal subscription", "error", err)
		return
	}

	customerID := sub.Customer.ID
	if customerID == "" {
		return
	}

	// Map Stripe price ID → plan name
	plan := priceIDToPlan(h.priceIDs, sub.Items.Data[0].Price.ID)

	_, err := h.db.ExecContext(ctx,
		`UPDATE workspaces SET plan = $1, updated_at = NOW() WHERE stripe_customer_id = $2`,
		plan, customerID,
	)
	if err != nil {
		slog.Error("stripe: failed to update workspace plan", "error", err, "customer_id", customerID)
		return
	}

	slog.Info("stripe: subscription updated",
		"customer_id", customerID,
		"plan", plan,
	)
}

// handleSubscriptionDeleted reverts workspace to bronze when subscription is cancelled.
func (h *Handler) handleSubscriptionDeleted(ctx context.Context, event stripe.Event) {
	var sub stripe.Subscription
	if err := json.Unmarshal(event.Data.Raw, &sub); err != nil {
		slog.Error("stripe: failed to unmarshal subscription", "error", err)
		return
	}

	customerID := sub.Customer.ID
	if customerID == "" {
		return
	}

	_, err := h.db.ExecContext(ctx,
		`UPDATE workspaces SET plan = 'bronze', stripe_subscription_id = NULL, updated_at = NOW() WHERE stripe_customer_id = $1`,
		customerID,
	)
	if err != nil {
		slog.Error("stripe: failed to revert workspace plan", "error", err, "customer_id", customerID)
		return
	}

	slog.Info("stripe: subscription deleted — workspace reverted to bronze",
		"customer_id", customerID,
	)
}

// priceIDToPlan maps a Stripe price ID to a plan name.
func priceIDToPlan(priceIDs map[string]string, priceID string) string {
	for plan, pid := range priceIDs {
		if pid == priceID {
			return plan
		}
	}
	return "bronze" // safe default
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("writeJSON failed", "error", err)
	}
}
