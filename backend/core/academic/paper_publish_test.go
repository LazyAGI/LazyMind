package academic

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
)

func TestResolveExpansionPapersUsesOwnedPinnedSourcesAndPersonalState(t *testing.T) {
	db := paperFixture(t)
	papers := []WorkflowPaper{{Paper: PaperRef{PaperID: "a", Title: "Frozen seed"}, IsSeed: true}}
	seeds, err := seedSources(db.DB, "papers", papers)
	if err != nil {
		t.Fatal(err)
	}
	record := orm.AcademicExpansion{ID: "publish-task", UserID: "u", DatasetID: "papers", ConversationID: "chat", RequestOwner: "u", RequestKey: "publish", SeedSnapshotJSON: marshal(seeds), DiscoveryJSON: marshal([]paperDiscovery{}), ResultJSON: marshal(ReviewWorkflowInput{Papers: papers})}
	if err = db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Create(&orm.AcademicUserPaperState{UserID: "u", PaperID: "a", IsImportant: true}).Error; err != nil {
		t.Fatal(err)
	}
	resolve := func(body, user string) *httptest.ResponseRecorder {
		r := mux.SetURLVars(paperRequest(body), map[string]string{"expansion_id": record.ID})
		r.Header.Set("X-User-Id", user)
		w := httptest.NewRecorder()
		ResolveExpansionPapers(w, r)
		return w
	}
	w := resolve(`{"paper_ids":["a"]}`, "u")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result struct {
		Papers   []WorkflowPaper  `json:"papers"`
		Contents []map[string]any `json:"contents"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Papers) != 1 || !result.Papers[0].IsImportant || !result.Papers[0].IsSeed || result.Papers[0].Paper.Title != "Frozen seed" {
		t.Fatalf("non-authoritative reference: %+v", result)
	}
	if len(result.Contents) != 1 || strings.Contains(w.Body.String(), `"pages"`) {
		t.Fatal(w.Body.String())
	}
	for _, tc := range []struct {
		body, user string
		status     int
	}{
		{`{"paper_ids":["b"]}`, "u", 422},
		{`{"paper_ids":["a","a"]}`, "u", 400},
		{`{"paper_ids":[]}`, "u", 200},
		{`{}`, "u", 400},
		{`{"paper_ids":["a"]}`, "other", 404},
	} {
		if got := resolve(tc.body, tc.user); got.Code != tc.status {
			t.Fatalf("%s user=%s: %d %s", tc.body, tc.user, got.Code, got.Body.String())
		}
	}
}
