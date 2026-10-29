package common

import (
	"net/http"
	"testing"
)

func TestPaperImportErrorsKeepSpecificCodesAndDetails(t *testing.T) {
	for _, tc := range []struct {
		message string
		status  int
		code    int
		detail  string
	}{
		{"processing submission outcome requires verification", http.StatusConflict, 2005140, ""},
		{"the previous processing task state is unknown; reconcile it before retrying", http.StatusConflict, 2005140, ""},
		{"target is not a paper library", http.StatusBadRequest, 2005141, ""},
		{"too many folders", http.StatusBadRequest, 2005124, "too many folders"},
		{"invalid or duplicate notes identity", http.StatusBadRequest, 2005125, "invalid or duplicate notes identity"},
		{"folder cycle", http.StatusBadRequest, 2004930, ""},
		{"note identity belongs to another paper", http.StatusConflict, 2005142, ""},
		{"new attachment must reference an existing document", http.StatusBadRequest, 2005143, ""},
		{"legacy document has multiple memberships; original archive retained for explicit resolution", http.StatusConflict, 2005144, ""},
		{"preserve legacy library dataset-1: invalid graph", http.StatusInternalServerError, 2005145, "invalid graph"},
		{"folder name already exists", http.StatusConflict, 2005146, ""},
		{"PDF 已保存，但处理任务未能启动，请重试: timeout", http.StatusInternalServerError, 2005111, "timeout"},
		{"paper identifiers conflict", http.StatusConflict, 2005136, ""},
	} {
		t.Run(tc.message, func(t *testing.T) {
			got := ResolveAppError(tc.message, tc.status)
			if got.Code != tc.code || got.HTTPStatus != tc.status ||
				(tc.detail == "" && got.Detail != nil) || (tc.detail != "" && got.Detail != tc.detail) {
				t.Fatalf("ResolveAppError(%q) = %#v, want code %d, status %d, detail %q", tc.message, got, tc.code, tc.status, tc.detail)
			}
		})
	}
}
