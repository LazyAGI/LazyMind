package academic

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestResearchRecordsIncludeOwnedReviewsAndFrozenCounts(t *testing.T) {
	db := paperFixture(t)
	now := time.Now().UTC()
	expansion := orm.AcademicExpansion{ID: "source", UserID: "u", DatasetID: "papers", Title: "Expansion", RequestOwner: "u", RequestKey: "source", Status: "completed", Version: 1, ResultJSON: marshal(ReviewWorkflowInput{Papers: []WorkflowPaper{{Paper: PaperRef{PaperID: "a"}}, {Paper: PaperRef{PaperID: "b"}}, {Paper: PaperRef{PaperID: "c"}}}})}
	if err := db.Create(&expansion).Error; err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id, owner, dataset, source, kind string
		trashed                          bool
	}{
		{"linked", "u", "papers", "source", "review", false},
		{"direct", "u", "papers", "", "review", false},
		{"foreign", "other", "papers", "source", "review", false},
		{"other-dataset", "u", "other", "", "review", false},
		{"trashed", "u", "papers", "", "review", true},
		{"reflection", "u", "papers", "", "reflection", false},
	} {
		conv := orm.Conversation{ID: tc.id, DisplayName: tc.id, BaseModel: orm.BaseModel{CreateUserID: tc.owner, CreatedAt: now, UpdatedAt: now}}
		if tc.trashed {
			conv.DeletedAt = &now
		}
		if err := db.Create(&conv).Error; err != nil {
			t.Fatal(err)
		}
		h := orm.AcademicHandoff{ID: tc.id, UserID: tc.owner, DatasetID: tc.dataset, ExpansionID: tc.source, ConversationID: tc.id, RequestKey: tc.id, Kind: tc.kind, Version: 1, CreatedAt: now, InputJSON: marshal(ReviewWorkflowInput{Papers: []WorkflowPaper{{Paper: PaperRef{PaperID: "a"}}, {Paper: PaperRef{PaperID: "b"}}}}), ContentsJSON: marshal(handoffContentSnapshot{})}
		if err := db.Create(&h).Error; err != nil {
			t.Fatal(err)
		}
	}
	request := paperRequest("")
	request.URL.RawQuery = "dataset_id=papers&include_reviews=true"
	w := httptest.NewRecorder()
	ListExpansions(w, request)
	var response struct {
		Items   []orm.AcademicExpansion `json:"items"`
		Reviews []academicReviewRecord  `json:"reviews"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(response.Items) != 1 || len(response.Reviews) != 2 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, review := range response.Reviews {
		if review.PaperCount != 2 || review.Title != review.ID || review.ConversationID != review.ID {
			t.Fatalf("lost frozen review metadata: %+v", review)
		}
		if review.ID == "linked" && (!review.SourceAvailable || review.SourceTitle != "Expansion") {
			t.Fatalf("missing source: %+v", review)
		}
		if review.ID == "direct" && (review.SourceAvailable || review.ExpansionID != "") {
			t.Fatalf("invented source: %+v", review)
		}
	}
	if err := db.Model(&expansion).Update("deleted_at", now).Error; err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	ListExpansions(w, request)
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(response.Reviews) != 2 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, review := range response.Reviews {
		if review.SourceAvailable {
			t.Fatalf("deleted source still available: %+v", review)
		}
	}
	request.Header.Set("X-User-Id", "other")
	w = httptest.NewRecorder()
	ListExpansions(w, request)
	if w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
}
