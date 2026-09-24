package resourceupdate

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRetiredWhenToUseChoiceCannotRewriteDescriptions(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/skill-review:when-to-use-choice", strings.NewReader(`{"requestid":"old-review","groups":[]}`))
	rec := httptest.NewRecorder()
	ResolveWhenToUseConflicts(rec, req)
	if rec.Code != http.StatusGone {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
