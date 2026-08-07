package acl

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lazymind/core/common/orm"

	"github.com/gorilla/mux"
)

// decodeACLResponse helper reads the JSON body of an acl APIResponse.
func decodeACLResponse(t *testing.T, w *httptest.ResponseRecorder) APIResponse {
	t.Helper()
	var resp APIResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

// --- Validation functions ---

// TestValidGranteeType accepts user, group, and tenant but rejects unknown values.
func TestValidGranteeType(t *testing.T) {
	tests := []struct {
		granteeType string
		want        bool
	}{
		{GranteeUser, true},
		{GranteeGroup, true},
		{GranteeTenant, true},
		{"admin", false},
		{"", false},
	}
	for _, tt := range tests {
		got := validGranteeType(tt.granteeType)
		if got != tt.want {
			t.Fatalf("validGranteeType(%q) = %v, want %v", tt.granteeType, got, tt.want)
		}
	}
}

// TestValidPermissionForResource checks permission validity for KB, dataset, and eval_set.
func TestValidPermissionForResource(t *testing.T) {
	tests := []struct {
		resourceType string
		permission   string
		want         bool
	}{
		{ResourceTypeKB, PermissionKBRead, true},
		{ResourceTypeKB, PermissionKBWrite, true},
		{ResourceTypeKB, PermissionKBCreateDoc, true},
		{ResourceTypeKB, PermissionKBDeleteDoc, true},
		{ResourceTypeKB, PermissionKBDelete, true},
		{ResourceTypeKB, PermissionDatasetRead, false}, // wrong resource
		{ResourceTypeEvalSet, PermissionEvalSetRead, true},
		{ResourceTypeEvalSet, PermissionEvalSetWrite, true},
		{"unknown", PermissionKBRead, false},
		{ResourceTypeKB, "bogus_perm", false},
	}
	for _, tt := range tests {
		got := validPermissionForResource(tt.resourceType, tt.permission)
		if got != tt.want {
			t.Fatalf("validPermissionForResource(%q, %q) = %v, want %v", tt.resourceType, tt.permission, got, tt.want)
		}
	}
}

// TestACLErrorCodeFromHTTPStatus maps HTTP statuses to business error codes.
func TestACLErrorCodeFromHTTPStatus(t *testing.T) {
	tests := []struct {
		status int
		want   int
	}{
		{http.StatusBadRequest, 2000103},
		{http.StatusMethodNotAllowed, 2000103},
		{http.StatusUnauthorized, 2000104},
		{http.StatusForbidden, 2000102},
		{http.StatusNotFound, 2000106},
		{http.StatusConflict, 2000107},
		{http.StatusTooManyRequests, 2000108},
		{http.StatusBadGateway, 2000110},
		{http.StatusTeapot, 2000000}, // unknown → default
	}
	for _, tt := range tests {
		got := aclErrorCodeFromHTTPStatus(tt.status)
		if got != tt.want {
			t.Fatalf("aclErrorCodeFromHTTPStatus(%d) = %d, want %d", tt.status, got, tt.want)
		}
	}
}

// TestParsePositiveInt parses valid integers, falls back to default for invalid input.
func TestParsePositiveInt(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		defVal int
		want   int
	}{
		{"valid number", "10", 5, 10},
		{"empty string", "", 5, 5},
		{"negative number", "-1", 5, 5},
		{"non-numeric", "abc", 5, 5},
		{"zero", "0", 5, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parsePositiveInt(tt.input, tt.defVal)
			if got != tt.want {
				t.Fatalf("parsePositiveInt(%q, %d) = %d, want %d", tt.input, tt.defVal, got, tt.want)
			}
		})
	}
}

// --- Reply functions ---

// TestReplyOK writes a 200 response with code 0 and data.
func TestReplyOK(t *testing.T) {
	w := httptest.NewRecorder()
	replyOK(w, map[string]string{"key": "value"})

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusOK)
	}
	resp := decodeACLResponse(t, w)
	if resp.Code != 0 {
		t.Fatalf("code: got %d, want 0", resp.Code)
	}
}

// TestReplyErr writes the given HTTP status code and maps it to an error code.
func TestReplyErr(t *testing.T) {
	w := httptest.NewRecorder()
	replyErr(w, "not found", http.StatusNotFound)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusNotFound)
	}
	resp := decodeACLResponse(t, w)
	if resp.Code == 0 {
		t.Fatal("expected non-zero error code")
	}
}

// TestReplyErr_Forbidden maps 403 to the correct error code.
func TestReplyErr_Forbidden(t *testing.T) {
	w := httptest.NewRecorder()
	replyErr(w, "denied", http.StatusForbidden)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusForbidden)
	}
	resp := decodeACLResponse(t, w)
	if resp.Code != 2000102 {
		t.Fatalf("code: got %d, want 2000102", resp.Code)
	}
}

// --- Handler tests with SQLite + httptest ---

// setupACLHandlerTest initializes a SQLite-backed ACL store for handler tests.
func setupACLHandlerTest(t *testing.T) {
	t.Helper()
	ormDB := orm.MigrateTestDB(t,
		&orm.ACLModel{}, &orm.UserGroupModel{}, &orm.KBModel{},
		&orm.VisibilityModel{}, &orm.ACLGroupModel{},
	)
	previousStore := defaultStore
	t.Cleanup(func() { defaultStore = previousStore })
	InitStore(ormDB)
}

// newHandlerRequest builds a request with X-User-Id and optional path vars.
func newHandlerRequest(method, path, body string, vars map[string]string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", "handler-user-1")
	if vars != nil {
		req = mux.SetURLVars(req, vars)
	}
	return req
}

// TestListACL_Handler returns ACL rows for a KB with SQLite backend.
func TestListACL_Handler(t *testing.T) {
	setupACLHandlerTest(t)
	GetStore().EnsureKB("kb-a", "KB A", "owner-a")
	GetStore().AddACL(ResourceTypeKB, "kb-a", GranteeUser, "user-1", PermissionKBRead, "owner-a", nil)

	req := newHandlerRequest("GET", "/api/kb/kb-a/acl", "", map[string]string{"kb_id": "kb-a"})
	w := httptest.NewRecorder()
	ListACL(w, req)

	if w.Code != 200 {
		t.Fatalf("status: got %d, want 200", w.Code)
	}
	resp := decodeACLResponse(t, w)
	if resp.Code != 0 {
		t.Fatalf("code: got %d, want 0", resp.Code)
	}
}

// TestListACL_EmptyKB returns 400.
func TestListACL_EmptyKB(t *testing.T) {
	setupACLHandlerTest(t)
	req := newHandlerRequest("GET", "/api/kb//acl", "", nil)
	w := httptest.NewRecorder()
	ListACL(w, req)

	if w.Code != 400 {
		t.Fatalf("status: got %d, want 400", w.Code)
	}
}

// TestAddACL_Handler creates a new ACL entry via the HTTP handler.
func TestAddACL_Handler(t *testing.T) {
	setupACLHandlerTest(t)
	GetStore().EnsureKB("kb-b", "KB B", "owner-b")

	body := `{"grantee_type":"user","grantee_id":"user-2","permission":"KB_READ"}`
	req := newHandlerRequest("POST", "/api/kb/kb-b/acl", body, map[string]string{"kb_id": "kb-b"})
	w := httptest.NewRecorder()
	AddACL(w, req)

	if w.Code != 200 {
		t.Fatalf("status: got %d, want 200, body: %s", w.Code, w.Body.String())
	}
	resp := decodeACLResponse(t, w)
	if resp.Code != 0 {
		t.Fatalf("code: got %d", resp.Code)
	}
}

// TestAddACL_InvalidKB returns 400.
func TestAddACL_InvalidKB(t *testing.T) {
	setupACLHandlerTest(t)
	req := newHandlerRequest("POST", "/api/kb//acl", `{"grantee_type":"user"}`, nil)
	w := httptest.NewRecorder()
	AddACL(w, req)

	if w.Code != 400 {
		t.Fatalf("status: got %d, want 400", w.Code)
	}
}

// TestAddACL_InvalidGranteeType returns 400.
func TestAddACL_InvalidGranteeType(t *testing.T) {
	setupACLHandlerTest(t)
	GetStore().EnsureKB("kb-c", "KB C", "owner-c")
	body := `{"grantee_type":"admin","grantee_id":"x","permission":"KB_READ"}`
	req := newHandlerRequest("POST", "/api/kb/kb-c/acl", body, map[string]string{"kb_id": "kb-c"})
	w := httptest.NewRecorder()
	AddACL(w, req)

	if w.Code != 400 {
		t.Fatalf("status: got %d, want 400", w.Code)
	}
}

// TestUpdateACL_Handler modifies an existing ACL entry via HTTP.
func TestUpdateACL_Handler(t *testing.T) {
	setupACLHandlerTest(t)
	GetStore().EnsureKB("kb-d", "KB D", "owner-d")
	_ = GetStore().AddACL(ResourceTypeKB, "kb-d", GranteeUser, "user-3", PermissionKBRead, "owner-d", nil)

	vars := map[string]string{"kb_id": "kb-d", "acl_id": "1"}
	body := `{"permission":"KB_WRITE"}`
	req := newHandlerRequest("PUT", "/api/kb/kb-d/acl/1", body, vars)
	w := httptest.NewRecorder()
	UpdateACL(w, req)

	if w.Code != 200 {
		t.Fatalf("status: got %d, want 200, body: %s", w.Code, w.Body.String())
	}
}

// TestUpdateACL_NotFound returns 404.
func TestUpdateACL_NotFound(t *testing.T) {
	setupACLHandlerTest(t)
	GetStore().EnsureKB("kb-e", "KB E", "owner-e")
	vars := map[string]string{"kb_id": "kb-e", "acl_id": "999"}
	req := newHandlerRequest("PUT", "/api/kb/kb-e/acl/999", `{"permission":"KB_READ"}`, vars)
	w := httptest.NewRecorder()
	UpdateACL(w, req)

	if w.Code != 404 {
		t.Fatalf("status: got %d, want 404", w.Code)
	}
}

// TestDeleteACL_Handler removes an ACL entry via HTTP.
func TestDeleteACL_Handler(t *testing.T) {
	setupACLHandlerTest(t)
	GetStore().EnsureKB("kb-f", "KB F", "owner-f")
	_ = GetStore().AddACL(ResourceTypeKB, "kb-f", GranteeUser, "user-4", PermissionKBRead, "owner-f", nil)

	vars := map[string]string{"kb_id": "kb-f", "acl_id": "1"}
	req := newHandlerRequest("DELETE", "/api/kb/kb-f/acl/1", "", vars)
	w := httptest.NewRecorder()
	DeleteACL(w, req)

	if w.Code != 200 {
		t.Fatalf("status: got %d, want 200", w.Code)
	}
}

// TestGetPermission_Handler returns permissions for a KB and user.
func TestGetPermission_Handler(t *testing.T) {
	setupACLHandlerTest(t)
	GetStore().EnsureKB("kb-g", "KB G", "owner-g")
	GetStore().AddACL(ResourceTypeKB, "kb-g", GranteeUser, "handler-user-1", PermissionKBRead, "owner-g", nil)

	req := newHandlerRequest("GET", "/api/kb/kb-g/permission", "", map[string]string{"kb_id": "kb-g"})
	w := httptest.NewRecorder()
	GetPermission(w, req)

	if w.Code != 200 {
		t.Fatalf("status: got %d, want 200", w.Code)
	}
	resp := decodeACLResponse(t, w)
	if resp.Code != 0 {
		t.Fatalf("code: got %d", resp.Code)
	}
}

// TestCanHandler_Action queries whether a user can perform an action on a KB.
func TestCanHandler_Action(t *testing.T) {
	setupACLHandlerTest(t)
	GetStore().EnsureKB("kb-h", "KB H", "owner-h")
	GetStore().AddACL(ResourceTypeKB, "kb-h", GranteeUser, "handler-user-1", PermissionKBWrite, "owner-h", nil)

	req := newHandlerRequest("GET", "/api/kb/kb-h/can?action=read", "",
		map[string]string{"kb_id": "kb-h"})
	w := httptest.NewRecorder()
	CanHandler(w, req)

	if w.Code != 200 {
		t.Fatalf("status: got %d", w.Code)
	}
}

// TestCanHandler_MissingAction returns 400.
func TestCanHandler_MissingAction(t *testing.T) {
	setupACLHandlerTest(t)
	req := newHandlerRequest("GET", "/api/kb/kb-x/can", "", map[string]string{"kb_id": "kb-x"})
	w := httptest.NewRecorder()
	CanHandler(w, req)

	if w.Code != 400 {
		t.Fatalf("status: got %d, want 400", w.Code)
	}
}

// TestListKB_Handler lists accessible KBs for the current user.
func TestListKB_Handler(t *testing.T) {
	setupACLHandlerTest(t)
	GetStore().EnsureKB("kb-i", "KB I", "owner-i")
	GetStore().AddACL(ResourceTypeKB, "kb-i", GranteeUser, "handler-user-1", PermissionKBRead, "owner-i", nil)

	req := newHandlerRequest("GET", "/api/kb/list", "", nil)
	w := httptest.NewRecorder()
	ListKB(w, req)

	if w.Code != 200 {
		t.Fatalf("status: got %d, want 200", w.Code)
	}
}

// TestBatchAddACL_Handler adds multiple ACL entries in one request.
func TestBatchAddACL_Handler(t *testing.T) {
	setupACLHandlerTest(t)
	GetStore().EnsureKB("kb-j", "KB J", "owner-j")

	body := `{"items":[{"grantee_type":"user","grantee_id":"u1","permission":"KB_READ"},{"grantee_type":"user","grantee_id":"u2","permission":"KB_WRITE"}]}`
	req := newHandlerRequest("POST", "/api/kb/kb-j/acl/batch", body, map[string]string{"kb_id": "kb-j"})
	w := httptest.NewRecorder()
	BatchAddACL(w, req)

	if w.Code != 200 {
		t.Fatalf("status: got %d, want 200, body: %s", w.Code, w.Body.String())
	}
}

// TestGetKBAuthorization_Handler returns authorization grants for a KB.
func TestGetKBAuthorization_Handler(t *testing.T) {
	setupACLHandlerTest(t)
	GetStore().EnsureKB("kb-k", "KB K", "owner-k")
	GetStore().AddACL(ResourceTypeKB, "kb-k", GranteeUser, "handler-user-1", PermissionKBRead, "owner-k", nil)

	req := newHandlerRequest("GET", "/api/kb/kb-k/authorization", "", map[string]string{"kb_id": "kb-k"})
	w := httptest.NewRecorder()
	GetKBAuthorization(w, req)

	if w.Code != 200 {
		t.Fatalf("status: got %d, want 200", w.Code)
	}
}

// TestSetKBAuthorization_InvalidKB returns client error for empty kb_id.
func TestSetKBAuthorization_InvalidKB(t *testing.T) {
	setupACLHandlerTest(t)
	req := newHandlerRequest("POST", "/api/kb//authorization", `{}`, nil)
	w := httptest.NewRecorder()
	SetKBAuthorization(w, req)

	if w.Code < 400 {
		t.Fatalf("status: got %d, want >=400", w.Code)
	}
}

// TestPermissionBatch_Handler queries permissions for multiple KBs.
func TestPermissionBatch_Handler(t *testing.T) {
	setupACLHandlerTest(t)
	GetStore().EnsureKB("kb-m", "KB M", "owner-m")

	body := `{"kb_ids":["kb-m","kb-nonexistent"]}`
	req := newHandlerRequest("POST", "/api/kb/permission/batch", body, nil)
	w := httptest.NewRecorder()
	PermissionBatch(w, req)

	if w.Code != 200 {
		t.Fatalf("status: got %d, want 200", w.Code)
	}
}

// TestListGrantPrincipals_Handler returns grantable principals.
func TestListGrantPrincipals_Handler(t *testing.T) {
	setupACLHandlerTest(t)
	GetStore().EnsureKB("kb-n", "KB N", "owner-n")

	req := newHandlerRequest("GET", "/api/kb/kb-n/grant_principals", "", map[string]string{"kb_id": "kb-n"})
	w := httptest.NewRecorder()
	ListGrantPrincipals(w, req)

	if w.Code != 200 {
		t.Fatalf("status: got %d, want 200", w.Code)
	}
}
