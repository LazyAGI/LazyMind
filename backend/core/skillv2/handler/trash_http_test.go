package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"

	"lazymind/core/skillv2/testutil"
	"lazymind/core/store"
)

func TestSkillTrashWorkflow_HTTP(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	deleteReq := httptest.NewRequest(http.MethodDelete, "/skills/skill1", nil)
	deleteReq = mux.SetURLVars(deleteReq, map[string]string{"skill_id": "skill1"})
	deleteReq.Header.Set("X-User-Id", "user_001")
	deleteRec := httptest.NewRecorder()
	Delete(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", deleteRec.Code, deleteRec.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/skills:trash?page=1&page_size=20", nil)
	listReq.Header.Set("X-User-Id", "user_001")
	listRec := httptest.NewRecorder()
	ListTrash(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list trash status=%d body=%s", listRec.Code, listRec.Body.String())
	}
	var listResponse struct {
		Data struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
			Total int `json:"total"`
		} `json:"data"`
	}
	if err := json.NewDecoder(listRec.Body).Decode(&listResponse); err != nil {
		t.Fatalf("decode trash list: %v", err)
	}
	if listResponse.Data.Total != 1 || len(listResponse.Data.Items) != 1 || listResponse.Data.Items[0].ID != "skill1" {
		t.Fatalf("trash list = %#v, want skill1", listResponse.Data)
	}

	restoreReq := httptest.NewRequest(http.MethodPost, "/skills/skill1:restore", nil)
	restoreReq = mux.SetURLVars(restoreReq, map[string]string{"skill_id": "skill1"})
	restoreReq.Header.Set("X-User-Id", "user_001")
	restoreRec := httptest.NewRecorder()
	Restore(restoreRec, restoreReq)
	if restoreRec.Code != http.StatusOK {
		t.Fatalf("restore status=%d body=%s", restoreRec.Code, restoreRec.Body.String())
	}

	listRec = httptest.NewRecorder()
	ListTrash(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list trash after restore status=%d body=%s", listRec.Code, listRec.Body.String())
	}
	if err := json.NewDecoder(listRec.Body).Decode(&listResponse); err != nil {
		t.Fatalf("decode trash list after restore: %v", err)
	}
	if listResponse.Data.Total != 0 {
		t.Fatalf("trash list after restore total=%d, want 0", listResponse.Data.Total)
	}

	trashReq := httptest.NewRequest(http.MethodPost, "/skills/skill1:trash", nil)
	trashReq = mux.SetURLVars(trashReq, map[string]string{"skill_id": "skill1"})
	trashReq.Header.Set("X-User-Id", "user_001")
	trashRec := httptest.NewRecorder()
	Trash(trashRec, trashReq)
	if trashRec.Code != http.StatusOK {
		t.Fatalf("trash status=%d body=%s", trashRec.Code, trashRec.Body.String())
	}

	purgeReq := httptest.NewRequest(http.MethodDelete, "/skills/skill1:purge", nil)
	purgeReq = mux.SetURLVars(purgeReq, map[string]string{"skill_id": "skill1"})
	purgeReq.Header.Set("X-User-Id", "user_001")
	purgeRec := httptest.NewRecorder()
	Purge(purgeRec, purgeReq)
	if purgeRec.Code != http.StatusOK {
		t.Fatalf("purge status=%d body=%s", purgeRec.Code, purgeRec.Body.String())
	}
	if got := testutil.CountRows(t, db, "skills", "id = ?", "skill1"); got != 0 {
		t.Fatalf("skill row count after purge = %d, want 0", got)
	}
}

func TestEmptyTrash_HTTP(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	testutil.SeedSkillWithRevision(t, db, "skill2", "rev2")
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	for _, skillID := range []string{"skill1", "skill2"} {
		req := httptest.NewRequest(http.MethodDelete, "/skills/"+skillID, nil)
		req = mux.SetURLVars(req, map[string]string{"skill_id": skillID})
		req.Header.Set("X-User-Id", "user_001")
		rec := httptest.NewRecorder()
		Delete(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("delete %s status=%d body=%s", skillID, rec.Code, rec.Body.String())
		}
	}

	emptyReq := httptest.NewRequest(http.MethodDelete, "/skills:trash", nil)
	emptyReq.Header.Set("X-User-Id", "user_001")
	emptyRec := httptest.NewRecorder()
	EmptyTrash(emptyRec, emptyReq)
	if emptyRec.Code != http.StatusOK {
		t.Fatalf("empty trash status=%d body=%s", emptyRec.Code, emptyRec.Body.String())
	}
	var emptyResponse struct {
		Data struct {
			Purged int `json:"purged"`
		} `json:"data"`
	}
	if err := json.NewDecoder(emptyRec.Body).Decode(&emptyResponse); err != nil {
		t.Fatalf("decode empty trash response: %v", err)
	}
	if emptyResponse.Data.Purged != 2 {
		t.Fatalf("purged count = %d, want 2", emptyResponse.Data.Purged)
	}
	if got := testutil.CountRows(t, db, "skills", "owner_user_id = ?", "user_001"); got != 0 {
		t.Fatalf("remaining skill rows = %d, want 0", got)
	}
}
