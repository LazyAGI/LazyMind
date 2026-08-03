package common

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// decodeResponse reads the JSON body of a ResponseRecorder into APIResponse.
func decodeResponse(t *testing.T, w *httptest.ResponseRecorder) APIResponse {
	t.Helper()
	var resp APIResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

// TestReplyOK writes a 200 response with code=0 and the supplied data.
func TestReplyOK(t *testing.T) {
	w := httptest.NewRecorder()
	ReplyOK(w, map[string]string{"key": "value"})

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusOK)
	}
	apiResp := decodeResponse(t, w)
	if apiResp.Code != CodeOK {
		t.Fatalf("code: got %d, want %d", apiResp.Code, CodeOK)
	}
}

// TestReplyErr writes an error status code and resolves the error catalog entry.
func TestReplyErr(t *testing.T) {
	w := httptest.NewRecorder()
	ReplyErr(w, "not found", http.StatusNotFound)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusNotFound)
	}
	apiResp := decodeResponse(t, w)
	if apiResp.Code == CodeOK {
		t.Fatal("expected non-zero error code")
	}
}

// TestReplyErrWithData includes data alongside the resolved error.
func TestReplyErrWithData(t *testing.T) {
	w := httptest.NewRecorder()
	ReplyErrWithData(w, "bad request", map[string]string{"field": "name"}, http.StatusBadRequest)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// TestReplyAppErr_NilWritesInternalError responds with 500 when appErr is nil.
func TestReplyAppErr_NilWritesInternalError(t *testing.T) {
	w := httptest.NewRecorder()
	ReplyAppErr(w, nil)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusInternalServerError)
	}
	apiResp := decodeResponse(t, w)
	if apiResp.Code != ErrCodeInternal {
		t.Fatalf("code: got %d, want %d", apiResp.Code, ErrCodeInternal)
	}
}

// TestReplyAppErr_WithDetail includes the detail field in the response.
func TestReplyAppErr_WithDetail(t *testing.T) {
	w := httptest.NewRecorder()
	appErr := NewAppError(http.StatusBadRequest, 2000103, "invalid params").WithDetail("field x is required")
	ReplyAppErr(w, appErr)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusBadRequest)
	}
	apiResp := decodeResponse(t, w)
	if apiResp.Code != 2000103 {
		t.Fatalf("code: got %d, want 2000103", apiResp.Code)
	}
}

// TestErrorCodeFromHTTPStatus maps well-known HTTP codes to business error codes.
func TestErrorCodeFromHTTPStatus(t *testing.T) {
	tests := []struct {
		httpStatus int
		wantCode   int
	}{
		{http.StatusBadRequest, ErrCodeInvalidParams},
		{http.StatusMethodNotAllowed, ErrCodeInvalidParams},
		{http.StatusUnauthorized, ErrCodeUnauthorized},
		{http.StatusForbidden, ErrCodeForbidden},
		{http.StatusNotFound, ErrCodeResourceAbsent},
		{http.StatusConflict, ErrCodeConflict},
		{http.StatusTooManyRequests, ErrCodeRateLimited},
		{http.StatusBadGateway, ErrCodeBadGateway},
		{http.StatusTeapot, ErrCodeInternal}, // unknown → default
	}
	for _, tt := range tests {
		got := ErrorCodeFromHTTPStatus(tt.httpStatus)
		if got != tt.wantCode {
			t.Fatalf("ErrorCodeFromHTTPStatus(%d) = %d, want %d", tt.httpStatus, got, tt.wantCode)
		}
	}
}

// TestReplyJSON writes raw JSON with application/json content type.
func TestReplyJSON(t *testing.T) {
	w := httptest.NewRecorder()
	ReplyJSON(w, map[string]int{"a": 1})

	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Fatalf("Content-Type: got %q, want application/json", ct)
	}
	if w.Body.Len() == 0 {
		t.Fatal("expected non-empty body")
	}
}

// TestMergeErrorDetail_NilData returns data with detail only.
func TestMergeErrorDetail_NilData(t *testing.T) {
	got := mergeErrorDetail(nil, "extra info")
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", got)
	}
	if m["detail"] != "extra info" {
		t.Fatalf("detail: got %v", m["detail"])
	}
}

// TestMergeErrorDetail_WithMapData adds detail to existing map data.
func TestMergeErrorDetail_WithMapData(t *testing.T) {
	data := map[string]any{"field": "name"}
	got := mergeErrorDetail(data, "required")
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", got)
	}
	if m["detail"] != "required" {
		t.Fatalf("detail: got %v", m["detail"])
	}
	if m["field"] != "name" {
		t.Fatalf("original field lost: %v", m)
	}
}
