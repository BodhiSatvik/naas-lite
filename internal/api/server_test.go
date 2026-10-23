package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/example/naas-lite/internal/naas"
)

func newTestServer() *Server {
	return New(naas.NewStore(), slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func do(t *testing.T, s http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("error body is not JSON: %s", rec.Body.String())
	}
	return body.Error.Code
}

const validBody = `{"name":"payments-dev","team":"payments","tier":"silver","cpu":4,"memoryGi":8}`

func TestCreateNamespace(t *testing.T) {
	s := newTestServer()
	rec := do(t, s, http.MethodPost, "/v1/namespaces", validBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	var resp namespaceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Namespace.Name != "payments-dev" || resp.ResourceQuota.Spec.Hard["limits.cpu"] != "4" {
		t.Errorf("unexpected response: %+v", resp)
	}
}

func TestCreateNamespaceErrors(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		status int
		code   string
	}{
		{"not json", `{`, http.StatusBadRequest, "invalid_json"},
		{"unknown field", `{"name":"a","team":"b","tier":"gold","cpu":1,"memoryGi":1,"gpu":2}`, http.StatusBadRequest, "invalid_json"},
		{"bad tier", `{"name":"a","team":"b","tier":"platinum","cpu":1,"memoryGi":1}`, http.StatusBadRequest, "invalid_request"},
		{"reserved name", `{"name":"kube-system","team":"b","tier":"gold","cpu":1,"memoryGi":1}`, http.StatusBadRequest, "invalid_request"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, newTestServer(), http.MethodPost, "/v1/namespaces", tt.body)
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tt.status, rec.Body)
			}
			if got := errorCode(t, rec); got != tt.code {
				t.Errorf("code = %q, want %q", got, tt.code)
			}
		})
	}
}

func TestCreateDuplicate(t *testing.T) {
	s := newTestServer()
	do(t, s, http.MethodPost, "/v1/namespaces", validBody)
	rec := do(t, s, http.MethodPost, "/v1/namespaces", validBody)
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "already_exists" {
		t.Fatalf("want 409 already_exists, got %d %s", rec.Code, rec.Body)
	}
}

func TestGetAndList(t *testing.T) {
	s := newTestServer()
	do(t, s, http.MethodPost, "/v1/namespaces", validBody)
	do(t, s, http.MethodPost, "/v1/namespaces", `{"name":"analytics-dev","team":"analytics","tier":"bronze","cpu":1,"memoryGi":2}`)

	if rec := do(t, s, http.MethodGet, "/v1/namespaces/payments-dev", ""); rec.Code != http.StatusOK {
		t.Errorf("get: status = %d", rec.Code)
	}
	rec := do(t, s, http.MethodGet, "/v1/namespaces/missing", "")
	if rec.Code != http.StatusNotFound || errorCode(t, rec) != "not_found" {
		t.Errorf("get missing: want 404 not_found, got %d %s", rec.Code, rec.Body)
	}

	rec = do(t, s, http.MethodGet, "/v1/namespaces", "")
	var list listResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 2 || list.Items[0].Name != "analytics-dev" {
		t.Errorf("list: %+v", list.Items)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	rec := do(t, newTestServer(), http.MethodPut, "/v1/namespaces", validBody)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}
