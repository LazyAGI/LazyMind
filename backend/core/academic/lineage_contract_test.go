package academic

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	workflowstore "lazymind/core/workflow/store"
)

func checkedResearchOutcome(version string, complete bool, reason string, gaps []any) map[string]any {
	status := "incomplete"
	if complete {
		status = "complete"
	}
	return map[string]any{"analysis_version": version, "completion": map[string]any{"status": status, "reason": reason}, "research_complete": complete, "coverage_gaps": gaps}
}

func TestReportRequiresItsEffectiveAnalysisVersion(t *testing.T) {
	db := paperFixture(t)
	record := orm.AcademicExpansion{UserID: "u", PreparationJSON: marshal(expansionPreparation{}), DiscoveryJSON: marshal([]paperDiscovery{})}
	artifacts := []workflowstore.Artifact{
		{Slot: "relation_analysis", Validity: "effective", Selected: true, Value: marshal(map[string]any{"data": checkedResearchOutcome("current", true, "verified", []any{})})},
		{Slot: "expansion_report", ProducerAttemptID: "compose", Validity: "effective", Selected: true, Value: marshal(map[string]any{"data": checkedResearchOutcome("previous", true, "verified", []any{})})},
	}
	if _, err := expansionReportSnapshot(db.DB, record, "session", "compose", artifacts); err == nil {
		t.Fatal("report from an earlier analysis was accepted")
	}
	artifacts[1].Value = marshal(map[string]any{"data": checkedResearchOutcome("current", true, "verified", []any{})})
	raw, err := expansionReportSnapshot(db.DB, record, "session", "compose", artifacts)
	if err != nil {
		t.Fatal(err)
	}
	var report map[string]any
	if err := json.Unmarshal(raw, &report); err != nil || report["analysis_version"] != "current" {
		t.Fatalf("valid report version was lost: %s %v", raw, err)
	}
}

func TestReportFreezesOnlyTheCheckedContentVersion(t *testing.T) {
	db := paperFixture(t)
	paper := PaperRef{PaperID: "a", SourceDocumentID: "a", Title: "Selected paper", SourceProvider: "openalex", SourceVersion: "publishedVersion"}
	if err := db.Create(&orm.AcademicWorkDocument{AcademicWorkID: paper.PaperID, DatasetID: "papers", DocumentID: "a", SourceProvider: paper.SourceProvider, SourceVersion: paper.SourceVersion}).Error; err != nil {
		t.Fatal(err)
	}
	seeds, err := seedSources(db.DB, "papers", []WorkflowPaper{{Paper: paper, IsSeed: true}})
	if err != nil {
		t.Fatal(err)
	}
	content := orm.AcademicPaperContent{ID: "checked-report-body", UserID: "u", PaperID: paper.PaperID,
		ParserVersion: orm.AcademicPaperParserVersion, SourceKey: seeds[0].key(), SourceKind: "kb",
		SourceDatasetID: "papers", SourceDocumentID: "a", SourceHash: seeds[0].Hash,
		SourceProvider: paper.SourceProvider, SourceVersion: paper.SourceVersion, Status: "ready", ContentHash: "checked-body",
		PagesJSON:   marshal([]ContentPage{{Page: 1, Text: "Original body used by the checked analysis."}}),
		QualityJSON: marshal(contentQuality{Eligible: true, Identity: "matched", Material: "main", Text: "usable"})}
	input := ReviewWorkflowInput{Papers: []WorkflowPaper{{Paper: paper, IsSeed: true}}}
	record := orm.AcademicExpansion{ID: "checked-report-expansion", UserID: "u", DatasetID: "papers", Version: 1, Status: "completed",
		PreparationJSON: marshal(expansionPreparation{}), SeedSnapshotJSON: marshal(seeds), DiscoveryJSON: marshal([]paperDiscovery{}), ResultJSON: marshal(input)}
	for _, row := range []any{&content, &record} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	checkedVersion := map[string]any{"paper_id": paper.PaperID, "status": "ready", "content_hash": content.ContentHash, "source_version": content.SourceVersion}
	report := checkedResearchOutcome("same-analysis", true, "verified", []any{})
	report["source_versions"] = []any{checkedVersion}
	artifacts := []workflowstore.Artifact{
		{Slot: "relation_analysis", Validity: "effective", Selected: true, Value: marshal(map[string]any{"data": checkedResearchOutcome("same-analysis", true, "verified", []any{})})},
		{Slot: "expansion_result", ProducerAttemptID: "compose", Validity: "effective", Selected: true, Value: marshal(map[string]any{"data": input})},
		{Slot: "expansion_report", ProducerAttemptID: "compose", Validity: "effective", Selected: true, Value: marshal(map[string]any{"data": report})},
	}
	// The body changes after composition but before the Go business snapshot.
	if err := db.Model(&content).Updates(map[string]any{"content_hash": "replacement-body", "pages_json": marshal([]ContentPage{{Page: 1, Text: "Replacement body requiring renewed verification."}})}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := expansionReportSnapshot(db.DB, record, "session", "compose", artifacts); err == nil {
		t.Fatal("unchanged analysis_version allowed a report checked against the previous body")
	}
	checkedVersion["content_hash"] = "replacement-body"
	artifacts[2].Value = marshal(map[string]any{"data": report})
	raw, err := expansionReportSnapshot(db.DB, record, "session", "compose", artifacts)
	if err != nil {
		t.Fatalf("report checked against current content could not be frozen: %v", err)
	}
	var frozen struct {
		AnalysisVersion string `json:"analysis_version"`
		Materials       []struct {
			PaperID       string `json:"paper_id"`
			Status        string `json:"status"`
			ContentHash   string `json:"content_hash"`
			SourceVersion string `json:"source_version"`
		} `json:"materials"`
	}
	if err := json.Unmarshal(raw, &frozen); err != nil {
		t.Fatal(err)
	}
	if frozen.AnalysisVersion != "same-analysis" || len(frozen.Materials) != 1 || frozen.Materials[0].PaperID != paper.PaperID || frozen.Materials[0].Status != "ready" || frozen.Materials[0].ContentHash != "replacement-body" || frozen.Materials[0].SourceVersion != paper.SourceVersion {
		t.Fatalf("frozen report lost its checked material version: %s", raw)
	}
}

func TestReportPreservesItsAuthoritativeResearchOutcome(t *testing.T) {
	db := paperFixture(t)
	record := orm.AcademicExpansion{UserID: "u", PreparationJSON: marshal(expansionPreparation{}), DiscoveryJSON: marshal([]paperDiscovery{})}
	gaps := []any{map[string]any{"direction": "method", "kind": "predecessor", "question_id": "origin", "reason": "The original source is not publicly available."}}
	analysis := checkedResearchOutcome("checked-partial", false, "material_limited", gaps)
	selection := map[string]any{"before_count": 2, "changes": []any{map[string]any{"action": "removed", "paper": map[string]any{"paper": map[string]any{"paper_id": "duplicate"}}, "reason": "Duplicate version of the seed paper."}}}
	analysis["selection"] = selection
	artifacts := []workflowstore.Artifact{
		{Slot: "relation_analysis", Validity: "effective", Selected: true, Value: marshal(map[string]any{"data": analysis})},
		{Slot: "expansion_report", ProducerAttemptID: "compose", Validity: "effective", Selected: true},
	}
	for _, field := range []string{"completion", "research_complete", "coverage_gaps", "selection"} {
		t.Run(field, func(t *testing.T) {
			report := checkedResearchOutcome("checked-partial", false, "material_limited", gaps)
			report["selection"] = selection
			switch field {
			case "completion":
				report[field] = map[string]any{"status": "incomplete", "reason": "budget_exhausted"}
			case "research_complete":
				report[field] = true
			case "selection":
				report[field] = map[string]any{"before_count": 1, "changes": []any{}}
			case "coverage_gaps":
				report[field] = []any{}
			}
			artifacts[1].Value = marshal(map[string]any{"data": report})
			if _, err := expansionReportSnapshot(db.DB, record, "session", "compose", artifacts); err == nil {
				t.Fatalf("composition changed the checked %s", field)
			}
		})
	}
	artifacts[1].Value = marshal(map[string]any{"data": analysis})
	raw, err := expansionReportSnapshot(db.DB, record, "session", "compose", artifacts)
	if err != nil {
		t.Fatal(err)
	}
	state, err := readExpansionResearchState(raw)
	if err != nil || state.Completion == nil || state.Completion.Status != "incomplete" || state.Completion.Reason != "material_limited" || state.ResearchComplete == nil || *state.ResearchComplete || string(state.CoverageGaps) != string(marshal(gaps)) {
		t.Fatalf("partial research outcome was not frozen intact: %s %v", raw, err)
	}
}

func TestIncompleteExpansionKeepsCompletedVersionWithoutAutomaticReview(t *testing.T) {
	for _, producer := range []string{"compose_result", "analyze_relations"} {
		t.Run(producer, func(t *testing.T) {
			db := paperFixture(t)
			conversation, err := newAcademicConversation(db.DB, "u", "papers", "research", "expansion")
			if err != nil {
				t.Fatal(err)
			}
			papers := []WorkflowPaper{}
			for _, id := range []string{"a", "b"} {
				if err := db.Create(&orm.AcademicWorkDocument{AcademicWorkID: id, DatasetID: "papers", DocumentID: id, SourceProvider: "openalex", SourceVersion: "publishedVersion"}).Error; err != nil {
					t.Fatal(err)
				}
				papers = append(papers, WorkflowPaper{Paper: PaperRef{PaperID: id, SourceDocumentID: id, SourceProvider: "openalex", SourceVersion: "publishedVersion"}, IsSeed: true})
			}
			seeds, err := seedSources(db.DB, "papers", papers)
			if err != nil {
				t.Fatal(err)
			}
			versions := []any{}
			for _, source := range seeds {
				content := orm.AcademicPaperContent{ID: "partial-" + source.Paper.PaperID, UserID: "u", PaperID: source.Paper.PaperID,
					ParserVersion: orm.AcademicPaperParserVersion, SourceKey: source.key(), SourceKind: "kb", SourceDatasetID: "papers", SourceDocumentID: source.Paper.SourceDocumentID,
					SourceHash: source.Hash, SourceProvider: source.Paper.SourceProvider, SourceVersion: source.Paper.SourceVersion, Status: "ready", ContentHash: "body-" + source.Paper.PaperID,
					PagesJSON: marshal([]ContentPage{{Page: 1, Text: "Body available for review."}}), QualityJSON: marshal(contentQuality{Eligible: true, Identity: "matched", Material: "main", Text: "usable"})}
				if err := db.Create(&content).Error; err != nil {
					t.Fatal(err)
				}
				versions = append(versions, map[string]any{"paper_id": content.PaperID, "status": content.Status, "content_hash": content.ContentHash, "source_version": content.SourceVersion})
			}
			input := ReviewWorkflowInput{UserInstruction: "Trace the actual sources.", CandidateDirections: []string{"method"}, SelectedDirections: []string{"method"}, Papers: papers}
			record := orm.AcademicExpansion{ID: "partial-review", UserID: "u", RequestOwner: "u", RequestKey: "partial-review", DatasetID: "papers", ConversationID: conversation,
				Title: input.UserInstruction, Goal: "review", Status: "running", ResultJSON: marshal(input), SeedSnapshotJSON: marshal(seeds), DiscoveryJSON: marshal([]paperDiscovery{}), PreparationJSON: marshal(expansionPreparation{})}
			session := orm.WorkflowSession{ID: "partial-session", ConversationID: conversation, WorkflowID: "paper_expansion", CreateUserID: "u", Status: "completed"}
			step := orm.WorkflowSessionStep{ID: "partial-compose", SessionID: session.ID, StepID: producer, Status: "succeeded", Validity: "effective", Attempt: 1}
			for _, row := range []any{&record, &session, &step} {
				if err := db.Create(row).Error; err != nil {
					t.Fatal(err)
				}
			}
			gaps := []any{map[string]any{"direction": "method", "kind": "predecessor", "reason": "Verification stopped at the shared deadline."}}
			outcome := checkedResearchOutcome("partial-version", false, "budget_exhausted", gaps)
			outcome["source_versions"] = versions
			artifacts := []workflowstore.Artifact{}
			for slot, value := range map[string]any{"relation_analysis": outcome, "expansion_result": input, "expansion_report": outcome} {
				artifacts = append(artifacts, workflowstore.Artifact{ID: "partial-" + slot, Slot: slot, StepID: producer, Attempt: 1, Revision: 1, ProducerAttemptID: step.ID, CreatedAt: time.Now().UTC(), Validity: "effective", Selected: true, Value: marshal(map[string]any{"data": value})})
			}
			job := asyncjob.Job{ResourceID: record.ID, CreateUserID: "u", PayloadJSON: marshal(expansionProjectionPayload{SessionID: session.ID, CompletedArtifacts: artifacts})}
			for i := 0; i < 2; i++ {
				if _, err := projectAcademicResult(t.Context(), job, nil); err != nil {
					t.Fatalf("partial publication did not settle: %v", err)
				}
			}
			if err := db.Where("id=?", record.ID).Take(&record).Error; err != nil {
				t.Fatal(err)
			}
			var round orm.AcademicExpansionRound
			if err := db.Where("expansion_id=?", record.ID).Take(&round).Error; err != nil || round.Status != "completed" || record.Status != "completed" || record.Version != 1 || record.AutoReviewID != "" {
				t.Fatalf("partial research changed execution status or launched an automatic review: %+v %+v %v", record, round, err)
			}
			var handoffs int64
			if err := db.Model(&orm.AcademicHandoff{}).Where("expansion_id=?", record.ID).Count(&handoffs).Error; err != nil || handoffs != 0 {
				t.Fatalf("partial result was automatically handed off: %d %v", handoffs, err)
			}
			if input, err := completedExpansionInput(db.DB, record, record.Version); err != nil || len(input.Papers) != 2 {
				t.Fatalf("partial version was unavailable for explicit continuation: %+v %v", input, err)
			}
			r := paperRequest("")
			r.URL.RawQuery = url.Values{"conversation_id": {conversation}, "view": {"summary"}}.Encode()
			w := httptest.NewRecorder()
			AcademicChatContext(w, r)
			var summary academicChatSummary
			if err := json.Unmarshal(w.Body.Bytes(), &summary); err != nil || w.Code != 200 || summary.Status != "completed" || summary.Completion == nil || summary.Completion.Reason != "budget_exhausted" || summary.ResearchComplete == nil || *summary.ResearchComplete || len(summary.CoverageGaps) == 0 {
				t.Fatalf("summary lost the distinction between execution and research completion: %d %s %v", w.Code, w.Body.String(), err)
			}
		})
	}
}

func TestLibrarySeedDatesFollowTheSelectedPDFRevision(t *testing.T) {
	for _, tc := range []struct {
		metadataVersion string
		versionDate     string
		wantVersionDate string
	}{
		{metadataVersion: "v1", versionDate: "2024-04-09T18:00:00Z", wantVersionDate: "2024-04-09T18:00:00Z"},
		{metadataVersion: "v2", versionDate: "2025-01-10T18:00:00Z", wantVersionDate: ""},
	} {
		t.Run(tc.metadataVersion, func(t *testing.T) {
			db := paperFixture(t)
			if err := db.Create(&orm.AcademicWorkDocument{AcademicWorkID: "a", DatasetID: "papers", DocumentID: "a", SourceProvider: "arxiv", SourceVersion: "v1"}).Error; err != nil {
				t.Fatal(err)
			}
			metadata := WorkInput{Provenance: map[string]any{"published_at": "2024-04-09T18:00:00Z", "source_version_date": tc.versionDate, "pdf_version": tc.metadataVersion}}
			if err := db.Model(&orm.AcademicDocumentState{}).Where("document_id=?", "a").Update("metadata_json", marshal(metadata)).Error; err != nil {
				t.Fatal(err)
			}
			paper := PaperRef{PaperID: "a", SourceDocumentID: "a", ArxivID: "2404.06654", SourceProvider: "arxiv", SourceVersion: "v2", PublishedAt: "2023-01-01T00:00:00Z", SourceVersionDate: "2025-01-10T18:00:00Z"}
			seeds, err := seedSources(db.DB, "papers", []WorkflowPaper{{Paper: paper, IsSeed: true}})
			if err != nil {
				t.Fatal(err)
			}
			if len(seeds) != 1 {
				t.Fatalf("expected one selected library seed, got %d", len(seeds))
			}
			selected := seeds[0].Paper
			if selected.SourceVersion != "v1" || selected.PublishedAt != "2024-04-09T18:00:00Z" || selected.SourceVersionDate != tc.wantVersionDate {
				t.Fatalf("selected v1 PDF received mismatched document dates: %+v", selected)
			}
		})
	}
}
