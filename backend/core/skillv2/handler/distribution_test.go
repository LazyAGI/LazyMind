package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"

	"lazymind/core/common"
	"lazymind/core/skillv2/testutil"
)

func TestDistributionUpgradeStatusReturnsUnmanagedForCustomSkill(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	withHandlerDB(t, db)

	request := httptest.NewRequest(http.MethodGet, "/api/core/skills/skill1/distribution-upgrade", nil)
	request = mux.SetURLVars(request, map[string]string{"skill_id": "skill1"})
	request.Header.Set("X-User-Id", "user_001")
	recorder := httptest.NewRecorder()
	DistributionUpgradeStatus(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response common.APIResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	data, ok := response.Data.(map[string]any)
	if !ok || data["managed"] != false || data["update_available"] != false {
		t.Fatalf("data = %#v", response.Data)
	}
}
