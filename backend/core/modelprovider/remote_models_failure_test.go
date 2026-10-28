package modelprovider

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRemoteModelsFailureReturnsSafeDistinctReasons(t *testing.T) {
	for _, reason := range []string{"authentication_failed", "permission_denied", "model_list_unavailable", "quota_exceeded", "rate_limited", "timeout"} {
		rec := httptest.NewRecorder()
		replyRemoteModelsFailure(rec, remoteModelsRequestError{status: 503, reason: reason})
		var body struct {
			Data struct {
				Reason string `json:"reason"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if rec.Code != 502 || body.Data.Reason != reason {
			t.Fatalf("reason %s lost: %s", reason, rec.Body.String())
		}
	}
	rec := httptest.NewRecorder()
	replyRemoteModelsFailure(rec, remoteModelsRequestError{reason: "secret https://private.example"})
	if strings.Contains(rec.Body.String(), "private.example") || strings.Contains(rec.Body.String(), "secret") || !strings.Contains(rec.Body.String(), "upstream_error") {
		t.Fatal("unsafe failure response")
	}
}
