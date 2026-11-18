package academic

import (
	"context"
	"encoding/json"
	"gorm.io/gorm"
	"lazymind/core/capability"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"net/http"
	"testing"
)

func identityImportOp() (capability.InvocationContext, doc.ResourceOperationContext) {
	return capability.InvocationContext{Principal: capability.Principal{UserID: "owner", Permissions: capability.NewPermissionSet("document.write")}}, doc.ResourceOperationContext{Caller: doc.DatasetCatalogCaller{UserID: "owner", Permissions: []string{"document.write"}}, OperationKey: "review", ValidateBeforeCommit: func(context.Context) error { return nil }, ValidateExecution: func(context.Context, *gorm.DB) error { return nil }}
}
func TestAgentImportUnconfirmedPair(t *testing.T) {
	db := paperLibraryTestDB(t)
	t.Setenv("LAZYMIND_RAG_ENABLED", "true")
	call, op := identityImportOp()
	res, e := AgentImportPapers(t.Context(), call, capability.PaperImportInput{KnowledgeBaseID: "papers", Papers: []capability.PaperSelection{{DOI: "10.1234/unrelated", ArxivID: "1706.03762"}}}, op)
	if e != nil {
		t.Fatal(e)
	}
	var w orm.AcademicWork
	db.First(&w)
	t.Logf("accepted=%s shared DOI=%s arxiv=%s", res.Items[0].Status, w.DOINormalized, w.ArxivIDBase)
	if w.DOINormalized != "" && w.ArxivIDBase != "" {
		t.Fatal("unconfirmed identity pair persisted")
	}
}
func TestAgentImportTrustedCandidate(t *testing.T) {
	db := paperLibraryTestDB(t)
	t.Setenv("LAZYMIND_RAG_ENABLED", "true")
	w := orm.AcademicWork{ID: "trusted", CanonicalTitle: "Paper", DOINormalized: "10.1234/trusted", AuthorsJSON: marshal([]string{}), ExternalIDsJSON: marshal(map[string]string{}), ProvenanceJSON: marshal(map[string]any{"trusted_fulltext": true, "pdf_url": "https://example.org/known.pdf", "pdf_provider": "crossref", "pdf_source_type": "open_access_pdf", "pdf_version": "publishedVersion"})}
	if e := db.Create(&w).Error; e != nil {
		t.Fatal(e)
	}
	call, op := identityImportOp()
	res, e := AgentImportPapers(t.Context(), call, capability.PaperImportInput{KnowledgeBaseID: "papers", SourceIDs: []string{"1"}, CurrentSources: []capability.PaperSourceSnapshot{{SourceID: "1", URL: "https://doi.org/10.1234/trusted", Title: "Paper"}}}, op)
	if e != nil {
		t.Fatal(e)
	}
	var item orm.PaperImportItem
	db.Where("batch_id=?", res.BatchID).First(&item)
	var candidate savedImportCandidate
	json.Unmarshal(item.SelectedCandidateJSON, &candidate)
	t.Logf("saved candidate=%s", item.SelectedCandidateJSON)
	if candidate.URL != "https://example.org/known.pdf" || candidate.SourceProvider != "crossref" || candidate.Version != "publishedVersion" || candidate.SourceType != "open_access_pdf" {
		t.Fatalf("trusted PDF candidate changed: %+v", candidate)
	}
	if candidate.VerifiedPaper == nil || candidate.VerifiedPaper.DOI != w.DOINormalized || candidate.VerifiedPaper.PDFURL != candidate.URL || candidate.VerifiedPaper.SourceProvider != "crossref" || candidate.VerifiedPaper.SourceVersion != candidate.Version {
		t.Fatalf("trusted verification lost: %+v", candidate.VerifiedPaper)
	}
	var entry orm.PaperLibraryEntry
	if err := db.Where("id=?", item.LibraryPaperID).Take(&entry).Error; err != nil {
		t.Fatal(err)
	}
	if entry.ArxivVersion != "" {
		t.Fatalf("publisher version became arxiv version: %q", entry.ArxivVersion)
	}
}

func TestAgentImportIdentityBoundary(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		selection                capability.PaperSelection
		existing                 bool
		doi, arxiv, key, version string
	}{
		{name: "unverified pair", selection: capability.PaperSelection{DOI: "10.1234/unrelated", ArxivID: "1706.03762"}, arxiv: "1706.03762", key: "arxiv:1706.03762"},
		{name: "existing DOI must not acquire arxiv", selection: capability.PaperSelection{DOI: "10.1234/unrelated", ArxivID: "1706.03762"}, existing: true, arxiv: "1706.03762", key: "arxiv:1706.03762"},
		{name: "published selection", selection: capability.PaperSelection{DOI: "10.1234/unrelated", ArxivID: "1706.03762", ExactVersion: true}, doi: "10.1234/unrelated", key: "doi:10.1234/unrelated:exact", version: "publishedVersion"},
		{name: "explicit preprint version", selection: capability.PaperSelection{DOI: "10.1234/unrelated", ArxivID: "1706.03762v2"}, arxiv: "1706.03762", key: "arxiv:1706.03762v2:exact", version: "v2"},
		{name: "trusted work pair", selection: capability.PaperSelection{WorkID: "trusted", DOI: "10.1234/unrelated", ArxivID: "1706.03762v2"}, existing: true, doi: "10.1234/unrelated", arxiv: "1706.03762", key: "arxiv:1706.03762v2:exact", version: "v2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := paperLibraryTestDB(t)
			t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", "http://scan.test")
			transport := http.DefaultTransport
			http.DefaultTransport = sourceTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "scan.test" && r.URL.Host != "auth-service:8000" {
					t.Fatalf("submission performed external lookup: %s", r.URL)
				}
				return transport.RoundTrip(r)
			})
			t.Setenv("LAZYMIND_RAG_ENABLED", "true")
			if tc.existing {
				w := orm.AcademicWork{ID: "trusted", CanonicalTitle: "Existing", DOINormalized: "10.1234/unrelated", AuthorsJSON: marshal([]string{}), ExternalIDsJSON: marshal(map[string]string{}), ProvenanceJSON: marshal(map[string]any{})}
				if tc.selection.WorkID != "" {
					w.ArxivIDBase = "1706.03762"
				}
				if err := db.Create(&w).Error; err != nil {
					t.Fatal(err)
				}
			}
			call, op := identityImportOp()
			in := capability.PaperImportInput{KnowledgeBaseID: "papers", Papers: []capability.PaperSelection{tc.selection}}
			result, err := AgentImportPapers(t.Context(), call, in, op)
			if err != nil {
				t.Fatal(err)
			}
			if result.Items[0].PaperKey != tc.key {
				t.Fatalf("receipt identity = %q, want %q", result.Items[0].PaperKey, tc.key)
			}
			var item orm.PaperImportItem
			if err := db.Where("batch_id=?", result.BatchID).Take(&item).Error; err != nil {
				t.Fatal(err)
			}
			var work orm.AcademicWork
			if err := db.Where("id=?", item.AcademicWorkID).Take(&work).Error; err != nil {
				t.Fatal(err)
			}
			if work.DOINormalized != tc.doi || work.ArxivIDBase != tc.arxiv {
				t.Fatalf("persisted identity = DOI %q arxiv %q", work.DOINormalized, work.ArxivIDBase)
			}
			var saved savedImportCandidate
			if err := json.Unmarshal(item.SelectedCandidateJSON, &saved); err != nil {
				t.Fatal(err)
			}
			if saved.Requested == nil {
				t.Fatal("missing worker selection")
			}
			base, _ := NormalizeArxivID(saved.Requested.ArxivID)
			if saved.Requested.DOI != tc.doi || base != tc.arxiv || saved.Version != tc.version {
				t.Fatalf("worker identity = %+v, version=%s", saved.Requested, saved.Version)
			}
			if tc.existing && tc.selection.WorkID == "" {
				var original orm.AcademicWork
				db.Where("id=?", "trusted").Take(&original)
				if original.ArxivIDBase != "" {
					t.Fatal("existing DOI work polluted")
				}
			}
			replay, err := AgentImportPapers(t.Context(), call, in, op)
			if err != nil || replay.BatchID != result.BatchID || replay.Items[0].PaperKey != tc.key {
				t.Fatalf("replay=%+v err=%v", replay, err)
			}
		})
	}
}
