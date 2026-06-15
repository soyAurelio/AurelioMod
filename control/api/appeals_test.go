package controlapi

import (
	"net/http"
	"testing"
)

// --- Appeals routes exist ---

func TestAppealsRoutes_Exist(t *testing.T) {
	app := New(nil, &mockTokenManager{})

	tests := []struct {
		method string
		path   string
	}{
		{"POST", "/v1/workspaces/ws-test/appeals"},
		{"GET", "/v1/workspaces/ws-test/appeals"},
		{"GET", "/v1/workspaces/ws-test/appeals/app-123"},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			resp := doRequest(t, app, tt.method, tt.path, "valid-token", nil)
			if resp.StatusCode == http.StatusNotFound {
				t.Errorf("route %s %s returned 404 — route not registered", tt.method, tt.path)
			}
		})
	}
}

// --- Appeal submit validation ---

func TestAppealSubmit_MissingAuth(t *testing.T) {
	app := New(nil, &mockTokenManager{})
	resp := doRequest(t, app, "POST", "/v1/workspaces/ws-test/appeals", "", mustJSONBody(`{"audit_id":"evt-001","reason":"I disagree"}`))

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestAppealSubmit_MissingAuditID(t *testing.T) {
	app := New(nil, &mockTokenManager{})
	resp := doRequest(t, app, "POST", "/v1/workspaces/ws-test/appeals", "valid-token", mustJSONBody(`{"reason":"I disagree"}`))

	body := readJSON(t, resp)
	if body["error"] != "audit_id is required" {
		t.Errorf("error = %v, want 'audit_id is required'", body["error"])
	}
}

func TestAppealSubmit_MissingReason(t *testing.T) {
	app := New(nil, &mockTokenManager{})
	resp := doRequest(t, app, "POST", "/v1/workspaces/ws-test/appeals", "valid-token", mustJSONBody(`{"audit_id":"evt-001"}`))

	body := readJSON(t, resp)
	if body["error"] != "reason is required" {
		t.Errorf("error = %v, want 'reason is required'", body["error"])
	}
}

func TestAppealSubmit_InvalidJSON(t *testing.T) {
	app := New(nil, &mockTokenManager{})
	resp := doRequest(t, app, "POST", "/v1/workspaces/ws-test/appeals", "valid-token", mustJSONBody(`not-json`))

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// --- List appeals ---

func TestAppealList_MissingAuth(t *testing.T) {
	app := New(nil, &mockTokenManager{})
	resp := doRequest(t, app, "GET", "/v1/workspaces/ws-test/appeals", "", nil)

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

// --- Get appeal ---

func TestAppealGet_MissingAuth(t *testing.T) {
	app := New(nil, &mockTokenManager{})
	resp := doRequest(t, app, "GET", "/v1/workspaces/ws-test/appeals/app-123", "", nil)

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}
