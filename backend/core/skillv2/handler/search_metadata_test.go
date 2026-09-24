package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lazymind/core/skillv2/testutil"
)

func TestInternalMetadataBatchUpdatesAreAtomicAndOwnerScoped(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	withHandlerDB(t, db)
	setHandlerSkillMetadata(t, db, "skill1", "academic", "external", "Write papers", `["paper"]`)
	call := func(handler http.HandlerFunc, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/internal/skills:metadata", strings.NewReader(body))
		req.Header.Set("X-User-Id", "user_001")
		rec := httptest.NewRecorder()
		handler(rec, req)
		return rec
	}
	rec := call(InternalMetadataUpdate, `{"updates":[{"skill_key":"external/academic","field":"writing","aliases":["论文写作"]},{"skill_key":"external/not-owned","field":"bad"}]}`)
	if rec.Code == 200 {
		t.Fatalf("unowned update accepted: %s", rec.Body)
	}
	var row testutil.SkillRow
	if err := db.Where("id = ?", "skill1").Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Field != "" {
		t.Fatal("failed batch partially updated metadata")
	}
	rec = call(InternalMetadataUpdate, `{"updates":[{"skill_key":"external/academic","field":"writing","aliases":["论文写作"],"keywords":["论文"]}]}`)
	if rec.Code != 200 {
		t.Fatalf("update=%d %s", rec.Code, rec.Body)
	}
	if err := db.Where("id = ?", "skill1").Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Field != "writing" || row.HeadRevisionID == nil || *row.HeadRevisionID != "rev1" {
		t.Fatalf("metadata modified execution revision: %v", row)
	}
	rec = call(InternalMetadata, `{"skill_keys":["external/academic","external/not-owned"]}`)
	if rec.Code != 200 {
		t.Fatalf("read=%d %s", rec.Code, rec.Body)
	}
	var response struct {
		Data struct {
			Skills []struct {
				Field   string
				Aliases []string
			}
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data.Skills) != 1 || response.Data.Skills[0].Field != "writing" || len(response.Data.Skills[0].Aliases) != 1 {
		t.Fatalf("read=%s", rec.Body)
	}
}
func TestInternalSearchAcceptsScalarAndArrayFieldValues(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	withHandlerDB(t, db)
	setHandlerSkillMetadata(t, db, "skill1", "academic", "external", "Write papers", `["paper"]`)
	if err := db.Table("skills").Where("id = ?", "skill1").Update("call_mode", "manual").Error; err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{`"paper"`, `["paper"]`} {
		req := httptest.NewRequest("POST", "/internal/skills:search", strings.NewReader(`{"field":"tags","value":`+value+`,"allowed_skill_keys":["external/academic"]}`))
		req.Header.Set("X-User-Id", "user_001")
		rec := httptest.NewRecorder()
		InternalSearch(rec, req)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"count":1`) {
			t.Fatalf("search=%d %s", rec.Code, rec.Body)
		}
	}
}
