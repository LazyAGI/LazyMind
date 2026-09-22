package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProviderTokenContextAuthorizesExplicitPreBindingBrowse(t *testing.T) {
	t.Parallel()

	handler := NewHandler(
		WithInternalToken("internal-token"),
		WithAccessChecker(allowAccess{}),
	)
	request := httptest.NewRequest(http.MethodPost, "/api/scan/internal/provider-token-context:authorize", strings.NewReader(`{
		"user_id":"user-owner",
		"tenant_id":"tenant-owner",
		"source_id":"",
		"binding_id":"",
		"auth_connection_id":"connection-1",
		"context_mode":"pre_binding_browse",
		"consumer":"datasource",
		"required_capability":"datasource.browse"
	}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-LazyMind-Internal-Token", "internal-token")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", response.Code, response.Body.String())
	}
}

func TestProviderTokenContextRejectsPreBindingPrivilegeExpansion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
	}{
		{
			name: "read capability",
			body: `{"user_id":"user-owner","tenant_id":"tenant-owner","auth_connection_id":"connection-1","context_mode":"pre_binding_browse","consumer":"datasource","required_capability":"datasource.read"}`,
		},
		{
			name: "scheduler consumer",
			body: `{"user_id":"user-owner","tenant_id":"tenant-owner","auth_connection_id":"connection-1","context_mode":"pre_binding_browse","consumer":"scheduler","required_capability":"datasource.browse"}`,
		},
		{
			name: "source binding smuggling",
			body: `{"user_id":"user-owner","tenant_id":"tenant-owner","source_id":"source-1","binding_id":"binding-1","auth_connection_id":"connection-1","context_mode":"pre_binding_browse","consumer":"datasource","required_capability":"datasource.browse"}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			handler := NewHandler(WithInternalToken("internal-token"), WithAccessChecker(allowAccess{}))
			request := httptest.NewRequest(http.MethodPost, "/api/scan/internal/provider-token-context:authorize", strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-LazyMind-Internal-Token", "internal-token")
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code < 400 {
				t.Fatalf("status = %d, want safe failure", response.Code)
			}
		})
	}
}
