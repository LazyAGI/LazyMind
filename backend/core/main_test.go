package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"

	"lazymind/core/externallease"
	workflowstore "lazymind/core/workflow/store"
)

func TestOpenAPIArtifactExportCanBeDisabledForSignedDesktopBundle(t *testing.T) {
	t.Setenv("LAZYMIND_OPENAPI_ARTIFACT_EXPORT_ENABLED", "false")
	if openAPIArtifactExportEnabled() {
		t.Fatal("OpenAPI artifact export should be disabled")
	}

	t.Setenv("LAZYMIND_OPENAPI_ARTIFACT_EXPORT_ENABLED", "")
	if !openAPIArtifactExportEnabled() {
		t.Fatal("OpenAPI artifact export should remain enabled by default")
	}
}

func TestExternalAgentOperationExposesOnlyMCPRuntimeSurface(t *testing.T) {
	tests := []struct {
		method, path string
		want         externallease.Operation
	}{
		{http.MethodPost, "/api/core/mcp/capabilities/v1", externallease.OperationCapabilityRead},
		{http.MethodPost, "/api/core/agent-invocations/inv-1:start", externallease.OperationInvocationWrite},
		{http.MethodGet, "/api/core/workflow-sessions/session-1/projection", externallease.OperationWorkflowRead},
		{http.MethodPost, "/api/core/workflow-sessions/session-1/hosted-attempts/attempt-1:submit", externallease.OperationWorkflowWrite},
		{http.MethodPost, "/api/core/workflow-sessions/session-1/hosted-attempts/attempt-1:delete", ""},
		{http.MethodPost, "/api/core/conversations:chat", ""},
		{http.MethodDelete, "/api/core/workflow-artifacts/artifact-1", ""},
	}
	for _, test := range tests {
		if got := externalAgentOperation(test.method, test.path); got != test.want {
			t.Fatalf("externalAgentOperation(%q, %q) = %q, want %q", test.method, test.path, got, test.want)
		}
	}
}

func TestInvocationConversationScopeIsRequestLocal(t *testing.T) {
	seen := ""
	handler := withInvocationConversationScope(func(w http.ResponseWriter, r *http.Request) {
		seen = workflowstore.ConversationScope(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "/workflow-sessions", nil)
	request.Header.Set("X-LazyMind-Invocation-Conversation-Id", "conversation-1")
	response := httptest.NewRecorder()
	handler(response, request)
	if response.Code != http.StatusNoContent || seen != "conversation-1" || workflowstore.ConversationScope(request.Context()) != "" {
		t.Fatalf("invocation scope leaked or was not propagated: status=%d seen=%q", response.Code, seen)
	}
}

func TestCoreListenAddrDefaultsToCloudPort(t *testing.T) {
	t.Setenv("LAZYMIND_CORE_HOST", "")
	t.Setenv("LAZYMIND_CORE_PORT", "")

	if got := coreListenAddr(); got != ":8000" {
		t.Fatalf("coreListenAddr() = %q, want :8000", got)
	}
}

func TestCoreListenAddrUsesLocalHostAndPort(t *testing.T) {
	t.Setenv("LAZYMIND_CORE_HOST", "127.0.0.1")
	t.Setenv("LAZYMIND_CORE_PORT", "18001")

	if got := coreListenAddr(); got != "127.0.0.1:18001" {
		t.Fatalf("coreListenAddr() = %q, want 127.0.0.1:18001", got)
	}
}

func TestBackgroundJobsEnabledDefaultsTrue(t *testing.T) {
	t.Setenv("LAZYMIND_BACKGROUND_JOBS_ENABLED", "")
	if !backgroundJobsEnabled() {
		t.Fatal("background jobs should be enabled by default")
	}
}

func TestBackgroundJobsEnabledAcceptsFalseValues(t *testing.T) {
	for _, value := range []string{"0", "false", "no", "off", " FALSE "} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("LAZYMIND_BACKGROUND_JOBS_ENABLED", value)
			if backgroundJobsEnabled() {
				t.Fatalf("background jobs should be disabled for %q", value)
			}
		})
	}
}

func TestCapabilityMCPRouteIsMounted(t *testing.T) {
	router := mux.NewRouter()
	registerCoreRoutes(router)
	registerCapabilityMCPRoute(router, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodPost, "/mcp/capabilities/v1", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("capability MCP status = %d, want %d", response.Code, http.StatusNoContent)
	}
}

func TestValidateStartupConfigRequiresInternalToken(t *testing.T) {
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "")
	if err := validateStartupConfig(); err == nil {
		t.Fatal("validateStartupConfig() should reject an empty internal token")
	}

	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "internal-secret")
	if err := validateStartupConfig(); err != nil {
		t.Fatalf("validateStartupConfig() error = %v", err)
	}
}

func TestValidateStartupConfigRejectsInvalidPreferenceCapacity(t *testing.T) {
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "internal-secret")
	for _, value := range []string{"", "0", "-1", "invalid"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("LAZYMIND_PREFERENCE_INDEX_MAX_ITEMS", value)
			if err := validateStartupConfig(); err == nil {
				t.Fatalf("validateStartupConfig() should reject %q", value)
			}
		})
	}
}
