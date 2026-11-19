package academic

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestSourceImportChecksTargetAndVersionBeforeCreatingBatch(t *testing.T) {
	db := sourceTestDB(t)
	if err := db.AutoMigrate(&orm.Dataset{}, &orm.ACLModel{}); err != nil {
		t.Fatal(err)
	}
	previous := store.DB()
	store.Init(db, db, nil)
	t.Cleanup(func() { store.Init(previous, previous, nil) })
	transport := http.DefaultTransport
	http.DefaultTransport = sourceTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"items":[{"dataset_id":"target","exists":false,"allowed":true},{"dataset_id":"other","exists":false,"allowed":true}]}`))}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = transport })
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", "http://scan.test")

	path := filepath.Join(t.TempDir(), "paper.pdf")
	if err := os.WriteFile(path, []byte("%PDF original"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, row := range []any{
		&orm.Dataset{ID: "target", Scene: "academic_papers", BaseModel: orm.BaseModel{CreateUserID: "alice"}},
		&orm.Dataset{ID: "other", Scene: "academic_papers", BaseModel: orm.BaseModel{CreateUserID: "alice"}},
		&orm.Conversation{ID: "chat", BaseModel: orm.BaseModel{CreateUserID: "alice"}},
		&orm.ChatHistory{ID: "answer", ConversationID: "chat", RetrievalResult: json.RawMessage(`[
			{"citation_id":"old","url":"https://arxiv.org/abs/2210.03629v2"},
			{"citation_id":"new","url":"https://arxiv.org/pdf/2210.03629v3"},
			{"citation_id":"unpinned","url":"https://doi.org/10.48550/arxiv.2210.03629"}
		]`)},
		&orm.AcademicWork{ID: "work", ArxivIDBase: "2210.03629", AuthorsJSON: json.RawMessage(`[]`), ExternalIDsJSON: json.RawMessage(`{}`), ProvenanceJSON: json.RawMessage(`{}`)},
		&orm.Document{ID: "pdf", DatasetID: "target", DocumentType: "PDF", Ext: marshal(map[string]string{"stored_path": path})},
		&orm.AcademicWorkDocument{AcademicWorkID: "work", DatasetID: "target", DocumentID: "pdf", SourceProvider: "arxiv", SourceVersion: "v2"},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	request := func(handler http.HandlerFunc, target string, ids []string, key string) *httptest.ResponseRecorder {
		input := map[string]any{"source_context": SourceContext{ConversationID: "chat", HistoryID: "answer"}, "source_ids": ids, "target_dataset_id": target}
		if key != "" {
			input["idempotency_key"] = key
		}
		body := marshal(input)
		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
		req.Header.Set("X-User-Id", "alice")
		rec := httptest.NewRecorder()
		handler(rec, req)
		return rec
	}
	inspect := func(target string) map[string]string {
		rec := request(InspectSources, target, []string{"old", "new", "unpinned"}, "")
		var body struct{ Items []SourceInspection }
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != 200 || len(body.Items) != 3 {
			t.Fatal(rec.Code, rec.Body.String(), err)
		}
		out := make(map[string]string)
		for _, row := range body.Items {
			out[row.SourceID] = row.TargetPresence
		}
		return out
	}
	if got := inspect("target"); got["old"] != "already_present" || got["new"] != "" || got["unpinned"] != "already_present" {
		t.Fatalf("incorrect version presence: %+v", got)
	}
	if got := inspect("other"); got["old"] != "" || got["unpinned"] != "" {
		t.Fatalf("PDF in a different collection blocked import: %+v", got)
	}
	if err := db.Model(&orm.Dataset{}).Where("id = ?", "other").Update("deleted_at", time.Now().UTC()).Error; err != nil {
		t.Fatal(err)
	}
	for _, rec := range []*httptest.ResponseRecorder{
		request(InspectSources, "other", []string{"unpinned"}, ""),
		request(CreateImport, "other", []string{"unpinned"}, "deleted-target"),
	} {
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "TARGET_NOT_FOUND") {
			t.Fatal(rec.Code, rec.Body.String())
		}
	}
	for _, id := range []string{"old", "unpinned"} {
		if rec := request(CreateImport, "target", []string{id}, "repeat-"+id); rec.Code != 409 || !strings.Contains(rec.Body.String(), "SOURCE_ALREADY_PRESENT") {
			t.Fatal(id, rec.Code, rec.Body.String())
		}
	}
	var batches int64
	if err := db.Model(&orm.PaperImportBatch{}).Count(&batches).Error; err != nil || batches != 0 {
		t.Fatalf("rejected submission created a batch: %d, %v", batches, err)
	}
	if rec := request(CreateImport, "target", []string{"new"}, "new-version"); rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
}
