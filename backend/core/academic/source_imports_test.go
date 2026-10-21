package academic

import (
	"bytes"
	"encoding/json"
	"io"
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
	for _, row := range []any{&orm.Conversation{ID: "chat", BaseModel: orm.BaseModel{CreateUserID: "alice"}}, &orm.ChatHistory{ID: "answer", ConversationID: "chat", RetrievalResult: json.RawMessage(`{"sources":[{"citation_id":"1","url":"https://arxiv.org/abs/2210.03629"},{"citation_id":"2","url":"https://arxiv.org/pdf/2210.03629v2"}]}`)}, &orm.Dataset{ID: "target", DisplayName: "papers", BaseModel: orm.BaseModel{CreateUserID: "alice"}}} {
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
}
