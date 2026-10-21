package academic

import (
	"bytes"
	"encoding/json"
	"io"
	"lazymind/core/acl"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type sourceTransport func(*http.Request) (*http.Response, error)

func (f sourceTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestSourceImportIdempotencyReservationAndInvalidTarget(t *testing.T) {
	db := sourceTestDB(t)
	if e := db.AutoMigrate(&orm.Dataset{}, &orm.ACLModel{}); e != nil {
		t.Fatal(e)
	}
	prev := store.DB()
	store.Init(db, db, nil)
	t.Cleanup(func() { store.Init(prev, prev, nil) })
	transport := http.DefaultTransport
	http.DefaultTransport = sourceTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"items":[{"dataset_id":"target","exists":false,"allowed":true},{"dataset_id":"deleted-target","exists":false,"allowed":true}],"source_map":{}}`))}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = transport })
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", "http://scan.test")
	for _, row := range []any{&orm.Conversation{ID: "chat", BaseModel: orm.BaseModel{CreateUserID: "alice"}}, &orm.ChatHistory{ID: "answer", ConversationID: "chat", RetrievalResult: json.RawMessage(`{"sources":[{"citation_id":"1","url":"https://arxiv.org/abs/2210.03629"},{"citation_id":"2","url":"https://arxiv.org/pdf/2210.03629v2"}]}`)}, &orm.Dataset{ID: "target", DisplayName: "papers", Scene: "academic_papers", BaseModel: orm.BaseModel{CreateUserID: "alice"}}} {
		if e := db.Create(row).Error; e != nil {
			t.Fatal(e)
		}
	}
	post := func(key, target string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"source_context": SourceContext{ConversationID: "chat", HistoryID: "answer"}, "source_ids": []string{"1", "2"}, "target_dataset_id": target, "idempotency_key": key})
		req := httptest.NewRequest("POST", "/academic/imports", bytes.NewReader(body))
		req.Header.Set("X-User-Id", "alice")
		rec := httptest.NewRecorder()
		CreateImport(rec, req)
		return rec
	}
	first := post("same", "target")
	if first.Code != 200 {
		t.Fatal(first.Code, first.Body.String())
	}
	second := post("same", "target")
	if second.Code != 200 {
		t.Fatal(second.Code, second.Body.String())
	}
	var count int64
	db.Model(&orm.PaperImportBatch{}).Count(&count)
	if count != 1 {
		t.Fatal("replay duplicated batch", count)
	}
	db.Model(&orm.PaperImportItem{}).Count(&count)
	if count != 1 {
		t.Fatal("duplicate URLs produced multiple papers", count)
	}
	// Independent requests serialize the reservation under the target dataset lock.
	var wg sync.WaitGroup
	responses := make(chan *httptest.ResponseRecorder, 2)
	for _, key := range []string{"other-1", "other-2"} {
		wg.Add(1)
		go func(k string) { defer wg.Done(); responses <- post(k, "target") }(key)
	}
	wg.Wait()
	close(responses)
	for rec := range responses {
		if rec.Code != 200 {
			t.Fatal("concurrent request", rec.Code, rec.Body.String())
		}
	}
	db.Model(&orm.PaperImportItem{}).Where("status IN ?", []string{"pending", "running"}).Count(&count)
	if count != 1 {
		t.Fatal("multiple active reservations", count)
	}
	rejected := post("missing", "deleted-target")
	if rejected.Code == 200 {
		t.Fatal("missing target accepted")
	}
	db.Model(&orm.PaperImportBatch{}).Count(&count)
	if count != 3 {
		t.Fatal("invalid target created task", count)
	}
	conflict := post("same", "different-target")
	if conflict.Code != 409 {
		t.Fatal("idempotency conflict accepted", conflict.Code)
	}
	// A target can change after the picker is loaded. Recheck its scene under
	// the same lock as batch creation, without writing a task or resource version.
	if e := db.Model(&orm.Dataset{}).Where("id = ?", "target").Update("scene", "general").Error; e != nil {
		t.Fatal(e)
	}
	var before, after orm.Dataset
	db.First(&before, "id = ?", "target")
	if rec := post("wrong-scene", "target"); rec.Code != 409 {
		t.Fatal("non-paper target accepted", rec.Code, rec.Body.String())
	}
	db.First(&after, "id = ?", "target")
	db.Model(&orm.PaperImportBatch{}).Count(&count)
	if count != 3 || after.ResourceVersion != before.ResourceVersion {
		t.Fatal("rejection changed target or created task", count, before.ResourceVersion, after.ResourceVersion)
	}
	if rec := post("same", "target"); rec.Code != 200 {
		t.Fatal("existing receipt cannot be replayed", rec.Code, rec.Body.String())
	}
	body := `{"source_context":{"conversation_id":"chat","history_id":"answer"},"source_ids":["1"],"new_knowledge_base":{"name":"Wrong_scene","scene":"general"},"idempotency_key":"wrong-new-scene"}`
	req := httptest.NewRequest("POST", "/academic/imports", strings.NewReader(body))
	req.Header.Set("X-User-Id", "alice")
	rec := httptest.NewRecorder()
	CreateImport(rec, req)
	db.Model(&orm.Dataset{}).Count(&count)
	if rec.Code != 422 || count != 1 {
		t.Fatal("invalid new scene created a library", rec.Code, count, rec.Body.String())
	}
}

func TestSourceTargetsOnlyWritablePaperCollections(t *testing.T) {
	db := sourceTestDB(t)
	if err := db.AutoMigrate(&orm.Dataset{}, &orm.ACLModel{}, &orm.UserGroupModel{}, &orm.KnowledgeMarketInstall{}, &orm.DefaultDataset{}); err != nil {
		t.Fatal(err)
	}
	previousACL := acl.GetStore()
	acl.InitStore(&orm.DB{DB: db})
	if previousACL != nil {
		savedACL := *previousACL
		t.Cleanup(func() { *acl.GetStore() = savedACL })
	}
	prev := store.DB()
	store.Init(db, db, nil)
	t.Cleanup(func() { store.Init(prev, prev, nil) })
	transport := http.DefaultTransport
	http.DefaultTransport = sourceTransport(func(r *http.Request) (*http.Response, error) {
		var request struct {
			IDs []string `json:"dataset_ids"`
		}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&request)
		}
		items := make([]map[string]any, 0, len(request.IDs))
		for _, id := range request.IDs {
			items = append(items, map[string]any{"dataset_id": id, "exists": false, "allowed": true})
		}
		body, _ := json.Marshal(map[string]any{"items": items, "source_map": map[string]bool{}})
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(body))}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = transport })
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", "http://scan.test")
	for _, row := range []orm.Dataset{
		{ID: "paper", Scene: "academic_papers", BaseModel: orm.BaseModel{CreateUserID: "alice"}},
		{ID: "legacy", Scene: "paper_collection", BaseModel: orm.BaseModel{CreateUserID: "alice"}},
		{ID: "general", Scene: "general", BaseModel: orm.BaseModel{CreateUserID: "alice"}},
		{ID: "private", Scene: "academic_papers", BaseModel: orm.BaseModel{CreateUserID: "bob"}},
		{ID: "readable", Scene: "academic_papers", BaseModel: orm.BaseModel{CreateUserID: "bob"}},
		{ID: "writable", Scene: "academic_papers", BaseModel: orm.BaseModel{CreateUserID: "bob"}},
	} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []orm.ACLModel{
		{ResourceType: acl.ResourceTypeDB, ResourceID: "readable", GranteeType: "user", TargetID: "alice", Permission: acl.PermissionDatasetRead},
		{ResourceType: acl.ResourceTypeDB, ResourceID: "writable", GranteeType: "user", TargetID: "alice", Permission: acl.PermissionDatasetWrite},
	} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest("GET", "/academic/source-targets", nil)
	req.Header.Set("X-User-Id", "alice")
	rec := httptest.NewRecorder()
	ListSourceTargets(rec, req)
	var response struct {
		Items []struct {
			ID string `json:"dataset_id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String(), err)
	}
	want := map[string]bool{"paper": true, "legacy": true, "writable": true}
	for _, row := range response.Items {
		if !want[row.ID] {
			t.Fatal("unexpected target", row.ID)
		}
		delete(want, row.ID)
	}
	if len(want) != 0 {
		t.Fatal("missing writable paper collections", want)
	}
}
