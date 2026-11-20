package academic

import (
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"lazymind/core/asyncjob"
	"lazymind/core/capability"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/paperlibrary"
	"net/http"
	"strings"
	"testing"
)

func TestAgentImportSavesLibraryBeforeNetworkAndDeduplicates(t *testing.T) {
	db := paperLibraryTestDB(t)
	t.Setenv("LAZYMIND_RAG_ENABLED", "true")
	old := http.DefaultTransport
	http.DefaultTransport = importErrorTransport(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Host, "arxiv") || strings.Contains(r.URL.Host, "crossref") {
			t.Error("import response waited for external metadata")
		}
		return old.RoundTrip(r)
	})
	t.Cleanup(func() { http.DefaultTransport = old })
	call := capability.InvocationContext{ConversationID: "chat", HistoryID: "turn", Principal: capability.Principal{UserID: "owner", Permissions: capability.NewPermissionSet("document.write")}}
	op := doc.ResourceOperationContext{Caller: doc.DatasetCatalogCaller{UserID: "owner", Permissions: []string{"document.write"}}, OperationKey: "quick", ValidateBeforeCommit: func(context.Context) error { return nil }, ValidateExecution: func(context.Context, *gorm.DB) error { return nil }}
	input := capability.PaperImportInput{KnowledgeBaseID: "papers", Papers: []capability.PaperSelection{{ArxivID: "2210.01936", Title: "NegCLIP", Year: 2023}}}
	first, err := AgentImportPapers(t.Context(), call, input, op)
	if err != nil || first.BatchID == "" {
		t.Fatalf("receipt %+v %v", first, err)
	}
	var entries []orm.PaperLibraryEntry
	db.Where("dataset_id=?", "papers").Find(&entries)
	if len(entries) != 1 {
		t.Fatalf("paper must be visible before worker runs, got %d", len(entries))
	}
	var batch orm.PaperImportBatch
	db.Where("id=?", first.BatchID).Take(&batch)
	if !isMetadataEnrichmentBatch(batch) {
		t.Fatal("Chat must reuse library background enrichment")
	}
	for _, key := range []string{"quick", "another-request"} {
		op.OperationKey = key
		if _, err = AgentImportPapers(t.Context(), call, input, op); err != nil {
			t.Fatal(err)
		}
	}
	var n, jobs int64
	db.Model(&orm.PaperLibraryEntry{}).Count(&n)
	db.Model(&orm.AsyncJob{}).Count(&jobs)
	if n != 1 || jobs != 1 {
		t.Fatalf("duplicate membership/jobs: %d/%d", n, jobs)
	}
	op.OperationKey = "cancel"
	op.ValidateExecution = func(context.Context, *gorm.DB) error { return errors.New("canceled") }
	input.Papers[0].ArxivID = "2302.04761"
	if _, err = AgentImportPapers(t.Context(), call, input, op); err == nil {
		t.Fatal("canceled import accepted")
	}
	db.Model(&orm.PaperLibraryEntry{}).Count(&n)
	if n != 1 {
		t.Fatal("canceled import leaked membership")
	}
}

func TestAgentImportPreservesVersionAndIdentifierOnlyMetadata(t *testing.T) {
	db := paperLibraryTestDB(t)
	t.Setenv("LAZYMIND_RAG_ENABLED", "true")
	call := capability.InvocationContext{Principal: capability.Principal{UserID: "owner", Permissions: capability.NewPermissionSet("document.write")}}
	op := doc.ResourceOperationContext{Caller: doc.DatasetCatalogCaller{UserID: "owner", Permissions: []string{"document.write"}}, OperationKey: "v1", ValidateBeforeCommit: func(context.Context) error { return nil }, ValidateExecution: func(context.Context, *gorm.DB) error { return nil }}
	for _, version := range []string{"v1", "v2"} {
		op.OperationKey = version
		in := capability.PaperImportInput{KnowledgeBaseID: "papers", Papers: []capability.PaperSelection{{ArxivID: "2210.01936" + version}}}
		result, err := AgentImportPapers(t.Context(), call, in, op)
		if err != nil || result.QueuedCount != 1 {
			t.Fatalf("version %s: %+v %v", version, result, err)
		}
		var item orm.PaperImportItem
		db.Where("batch_id=?", result.BatchID).Take(&item)
		var selected FulltextCandidate
		json.Unmarshal(item.SelectedCandidateJSON, &selected)
		if selected.Version != version {
			t.Fatalf("lost version: %+v", selected)
		}
	}
	var n int64
	db.Model(&orm.PaperLibraryEntry{}).Count(&n)
	if n != 1 {
		t.Fatalf("versions should share library membership: %d", n)
	}
	m := paperlibrary.Metadata{Title: "2210.01936", Identifiers: paperlibrary.Identifiers{Arxiv: "2210.01936"}}
	if fillBibliographyMetadata(m, WorkInput{Title: "Canonical title"}).Title != "Canonical title" {
		t.Fatal("identifier placeholder was not enriched")
	}
}

func TestAgentImportKeepsKnownWorkAndPublicationIntent(t *testing.T) {
	db := paperLibraryTestDB(t)
	t.Setenv("LAZYMIND_RAG_ENABLED", "true")
	work := orm.AcademicWork{ID: "known", CanonicalTitle: "Known title", AuthorsJSON: marshal([]string{}), ExternalIDsJSON: marshal(map[string]string{}), ProvenanceJSON: marshal(map[string]string{})}
	if err := db.Create(&work).Error; err != nil {
		t.Fatal(err)
	}
	call := capability.InvocationContext{Principal: capability.Principal{UserID: "owner", Permissions: capability.NewPermissionSet("document.write")}}
	op := doc.ResourceOperationContext{Caller: doc.DatasetCatalogCaller{UserID: "owner", Permissions: []string{"document.write"}}, OperationKey: "known", ValidateBeforeCommit: func(context.Context) error { return nil }, ValidateExecution: func(context.Context, *gorm.DB) error { return nil }}
	result, err := AgentImportPapers(t.Context(), call, capability.PaperImportInput{KnowledgeBaseID: "papers", Papers: []capability.PaperSelection{{WorkID: "known"}}}, op)
	if err != nil {
		t.Fatal(err)
	}
	var item orm.PaperImportItem
	db.Where("batch_id=?", result.BatchID).Take(&item)
	if item.AcademicWorkID != "known" {
		t.Fatalf("lost selected work identity: %s", item.AcademicWorkID)
	}
	op.OperationKey = "published"
	result, err = AgentImportPapers(t.Context(), call, capability.PaperImportInput{KnowledgeBaseID: "papers", Papers: []capability.PaperSelection{{DOI: "10.1234/published", ArxivID: "2210.01936", ExactVersion: true}}}, op)
	if err != nil {
		t.Fatal(err)
	}
	item = orm.PaperImportItem{}
	db.Where("batch_id=?", result.BatchID).Take(&item)
	var selected savedImportCandidate
	json.Unmarshal(item.SelectedCandidateJSON, &selected)
	if selected.Requested == nil || !publicationRequested(*selected.Requested) {
		t.Fatalf("publication intent lost: %s", item.SelectedCandidateJSON)
	}
}

func TestAgentImportReplayKeepsSelectionMappingAfterWorkerUpdates(t *testing.T) {
	db := paperLibraryTestDB(t)
	t.Setenv("LAZYMIND_RAG_ENABLED", "true")
	call := capability.InvocationContext{Principal: capability.Principal{UserID: "owner", Permissions: capability.NewPermissionSet("document.write")}}
	op := doc.ResourceOperationContext{Caller: doc.DatasetCatalogCaller{UserID: "owner", Permissions: []string{"document.write"}}, OperationKey: "batch", ValidateBeforeCommit: func(context.Context) error { return nil }, ValidateExecution: func(context.Context, *gorm.DB) error { return nil }}
	in := capability.PaperImportInput{KnowledgeBaseID: "papers", Papers: []capability.PaperSelection{{DOI: "10.1234/first"}, {DOI: "10.1234/second"}}}
	first, err := AgentImportPapers(t.Context(), call, in, op)
	if err != nil {
		t.Fatal(err)
	}
	var rows []orm.PaperImportItem
	db.Where("batch_id=?", first.BatchID).Find(&rows)
	// Simulate changed physical row order after worker updates. It is not a contract.
	if err := db.Where("batch_id=?", first.BatchID).Delete(&orm.PaperImportItem{}).Error; err != nil {
		t.Fatal(err)
	}
	for i := len(rows) - 1; i >= 0; i-- {
		if err := db.Create(&rows[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	again, err := AgentImportPapers(t.Context(), call, in, op)
	if err != nil {
		t.Fatal(err)
	}
	for i := range first.Items {
		if first.Items[i].LibraryPaperID != again.Items[i].LibraryPaperID {
			t.Fatalf("replay swapped selected papers: %+v / %+v", first, again)
		}
	}
}

func TestAgentImportRetainsSavedEntryWhenPDFUnavailable(t *testing.T) {
	db := paperLibraryTestDB(t)
	t.Setenv("LAZYMIND_RAG_ENABLED", "true")
	call := capability.InvocationContext{Principal: capability.Principal{UserID: "owner", Permissions: capability.NewPermissionSet("document.write")}}
	op := doc.ResourceOperationContext{Caller: doc.DatasetCatalogCaller{UserID: "owner", Permissions: []string{"document.write"}}, OperationKey: "no-pdf", ValidateBeforeCommit: func(context.Context) error { return nil }, ValidateExecution: func(context.Context, *gorm.DB) error { return nil }}
	result, err := AgentImportPapers(t.Context(), call, capability.PaperImportInput{KnowledgeBaseID: "papers", Papers: []capability.PaperSelection{{DOI: "10.1234/unavailable", Title: "Saved paper"}}}, op)
	if err != nil {
		t.Fatal(err)
	}
	var b orm.PaperImportBatch
	db.Where("id=?", result.BatchID).Take(&b)
	var item orm.PaperImportItem
	db.Where("batch_id=?", b.ID).Take(&item)
	db.Model(&orm.AsyncJob{}).Where("id=?", b.AsyncJobID).Updates(map[string]any{"status": "running", "attempt_count": 1})
	ctx, err := prepareSourceTarget(contextWithNoPDF(t), sourceImportCaller(b), b.TargetDatasetID)
	if err != nil {
		t.Fatal(err)
	}
	old := http.DefaultTransport
	http.DefaultTransport = importErrorTransport(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Host, "crossref") {
			return nil, errors.New("provider unavailable")
		}
		return old.RoundTrip(r)
	})
	t.Cleanup(func() { http.DefaultTransport = old })
	err = enrichBibliographyItem(ctx, asyncjob.Job{ID: b.AsyncJobID, AttemptCount: 1}, b, &item, sourceImportCaller(b))
	if err == nil {
		t.Fatal("expected unavailable PDF")
	}
	var n int64
	db.Model(&orm.PaperLibraryEntry{}).Where("id=? AND removed_at IS NULL", result.Items[0].LibraryPaperID).Count(&n)
	if n != 1 || !strings.Contains(importItemFailure(err, item).Message, "题录已保存") {
		t.Fatalf("lost saved-paper receipt: %d %v", n, err)
	}
}
