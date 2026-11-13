package taskcenter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGitHubStarPromptSupportInvalidConfirmationKeepsErrorContract(t *testing.T) {
	for _, body := range []string{`{}`, `{"supported":false}`} {
		req := httptest.NewRequest(http.MethodPut, "/user/github-star-prompt/support", strings.NewReader(body))
		req.Header.Set("X-User-Id", "owner")
		rec := httptest.NewRecorder()
		SupportGitHubStarPrompt(rec, req)
		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if rec.Code != http.StatusBadRequest || payload["code"] != float64(2000103) || payload["message"] != "supported must be true" || len(payload) != 2 {
			t.Fatalf("body=%s status=%d payload=%s", body, rec.Code, rec.Body.String())
		}
	}
}
