package academic

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
)

func TestResearchRecordsFollowPublishedPaperMaterialProgress(t *testing.T) {
	db := paperFixture(t)
	papers := []WorkflowPaper{{Paper: PaperRef{PaperID: "a"}, IsSeed: true}, {Paper: PaperRef{PaperID: "b"}, IsSeed: true}}
	sources, err := seedSources(db.DB, "papers", papers)
	if err != nil {
		t.Fatal(err)
	}
	for _, goal := range []string{"expansion", "review"} {
		record := orm.AcademicExpansion{
			ID: goal, UserID: "u", DatasetID: "papers", Title: goal, Goal: goal,
			RequestOwner: "u", RequestKey: goal, Status: "completed", Version: 1,
			ResultJSON: marshal(ReviewWorkflowInput{Papers: papers}), SeedSnapshotJSON: marshal(sources),
		}
		if err := db.Create(&record).Error; err != nil {
			t.Fatal(err)
		}
	}
	quality := marshal(contentQuality{Eligible: true, Identity: "matched", Material: "main", Text: "usable"})
	for _, source := range sources {
		content := orm.AcademicPaperContent{
			ID: source.Paper.PaperID, UserID: "u", PaperID: source.Paper.PaperID,
			SourceDatasetID: source.DatasetID, SourceDocumentID: source.DocumentID, SourceHash: source.Hash,
			SourceKind: "kb", SourceKey: source.key(), ParserVersion: orm.AcademicPaperParserVersion,
			Status: "ready", ContentHash: source.Paper.PaperID, PagesJSON: marshal([]string{"text"}), QualityJSON: quality,
		}
		if content.ID == "b" {
			content.Status = "processing"
		}
		if err := db.Create(&content).Error; err != nil {
			t.Fatal(err)
		}
	}
	// Another source of the same paper must not keep this round running.
	if err := db.Create(&orm.AcademicPaperContent{ID: "other-source", UserID: "u", PaperID: "b", SourceKind: "external",
		SourceKey: "another-pdf", ParserVersion: orm.AcademicPaperParserVersion, Status: "processing", PagesJSON: marshal([]string{})}).Error; err != nil {
		t.Fatal(err)
	}
	assertProgress := func(processing, ready int) {
		t.Helper()
		req := paperRequest("")
		req.URL.RawQuery = "dataset_id=papers&include_reviews=true"
		w := httptest.NewRecorder()
		ListExpansions(w, req)
		var listed struct{ Items []academicExpansionRecord }
		if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil || w.Code != 200 || len(listed.Items) != 2 {
			t.Fatal(w.Code, w.Body.String())
		}
		for _, row := range listed.Items {
			if row.Status != "completed" || row.MaterialProgress == nil || row.MaterialProgress.ProcessingCount != processing {
				t.Fatalf("incorrect material progress for %s: %+v", row.Goal, row.MaterialProgress)
			}
			if row.Goal == "review" && (row.MaterialProgress.DistinctReadyCount == nil || *row.MaterialProgress.DistinctReadyCount != ready) {
				t.Fatalf("incorrect distinct review material count: %+v", row.MaterialProgress)
			}
		}
		w = httptest.NewRecorder()
		GetExpansion(w, mux.SetURLVars(paperRequest(""), map[string]string{"expansion_id": "review"}))
		var detail struct {
			MaterialProgress academicMaterialProgress `json:"material_progress"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil || w.Code != 200 || detail.MaterialProgress.ProcessingCount != processing ||
			detail.MaterialProgress.DistinctReadyCount == nil || *detail.MaterialProgress.DistinctReadyCount != ready {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	assertProgress(1, 1)
	if err := db.Model(&orm.AcademicPaperContent{}).Where("id=?", "b").Update("status", "ready").Error; err != nil {
		t.Fatal(err)
	}
	assertProgress(0, 2)
}

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
