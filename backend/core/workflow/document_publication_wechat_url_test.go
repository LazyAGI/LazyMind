package workflow

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"lazymind/core/algo"
	corestore "lazymind/core/store"
)

func TestReadPublicationResolvesOwnedWeChatDraft(t *testing.T) {
	f, input := publicationFixture(t, false)
	input.Provider = "wechat"
	op := preparePublication(t, f, input)
	receipt, _ := json.Marshal(DocumentPublicationReceipt{Provider: "wechat", TargetDocument: json.RawMessage(`{"doc_id":"selected-draft","adapter":"wechat","meta":{"article_index":1,"browser_url":"https://mp.weixin.qq.com/"}}`)})
	mustPublicationErrorNil(t, f.db.Model(op).Updates(map[string]any{"receipt_json": receipt, "status": "succeeded"}).Error)
	oldDB, oldLLM, oldState := corestore.DB(), corestore.LazyLLMDB(), corestore.State()
	corestore.Init(f.db.DB, f.db.DB, oldState)
	t.Cleanup(func() { corestore.Init(oldDB, oldLLM, oldState) })
	reads, requests := 0, 0
	preview := "https://mp.weixin.qq.com/s?tempkey=fixture-first"
	upstreamFail := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/authservice/v1/cloud/connections/internal/chat-enabled":
			if r.URL.Query().Get("owner_user_id") != "owner" || r.URL.Query().Get("provider") != "wechat" {
				t.Error("wrong credential owner/provider")
			}
			_, _ = w.Write([]byte(`{"data":{"items":[{"connection_id":"fixture","provider":"wechat","owner_user_id":"owner","status":"ACTIVE"}]}}`))
		case "/api/authservice/v1/cloud/connections/fixture/token":
			_, _ = w.Write([]byte(`{"data":{"connection_id":"fixture","provider":"wechat","status":"ACTIVE","access_token":"fixture-token"}}`))
		case "/api/document/actions:invoke":
			reads++
			var request algo.DocumentActionInvokeRequest
			mustPublicationErrorNil(t, json.NewDecoder(r.Body).Decode(&request))
			args := request.Arguments.(map[string]any)
			if request.Reference != "builtin:document.wechat_draft_url.v1" || request.Phase != "preview" || args["media_id"] != "selected-draft" || args["article_index"] != float64(1) || request.ToolConfig["wechat"] != "fixture-token" {
				t.Errorf("unexpected lookup arguments")
			}
			if upstreamFail {
				w.WriteHeader(502)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]string{"url": preview}})
		default:
			t.Errorf("unexpected request path: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	t.Setenv("LAZYMIND_AUTH_SERVICE_URL", server.URL)
	read := func(owner string, artifact bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("X-User-Id", owner)
		w := httptest.NewRecorder()
		if artifact {
			r = mux.SetURLVars(r, map[string]string{"artifact_id": f.currentRevisionID})
			ReadArtifactDocumentPublication(w, r)
		} else {
			r = mux.SetURLVars(r, map[string]string{"operation_id": op.ID})
			ReadDocumentPublication(w, r)
		}
		return w
	}
	for _, artifact := range []bool{false, true} {
		w := read("owner", artifact)
		var body struct {
			Data json.RawMessage `json:"data"`
		}
		mustPublicationErrorNil(t, json.Unmarshal(w.Body.Bytes(), &body))
		var status DocumentPublicationStatus
		if artifact {
			var lookup DocumentPublicationLookup
			mustPublicationErrorNil(t, json.Unmarshal(body.Data, &lookup))
			if lookup.Operation == nil {
				t.Fatal("missing operation")
			}
			status = *lookup.Operation
		} else {
			mustPublicationErrorNil(t, json.Unmarshal(body.Data, &status))
		}
		if w.Code != 200 || status.TargetURL != preview || status.Status != "succeeded" {
			t.Fatalf("status=%+v code=%d", status, w.Code)
		}
		preview = "https://mp.weixin.qq.com/s?tempkey=fixture-refreshed"
	}
	if reads != 2 {
		t.Fatalf("reads=%d", reads)
	}
	before := requests
	if w := read("another-owner", false); w.Code != 404 {
		t.Fatalf("wrong owner status=%d", w.Code)
	}
	if requests != before {
		t.Fatal("unauthorized lookup reached provider")
	}
	upstreamFail = true
	w := read("owner", false)
	var failed struct {
		Data DocumentPublicationStatus `json:"data"`
	}
	mustPublicationErrorNil(t, json.Unmarshal(w.Body.Bytes(), &failed))
	if w.Code != 200 || failed.Data.Status != "succeeded" || failed.Data.TargetURL != "" {
		t.Fatalf("preview failure changed write status or exposed console: %+v", failed)
	}
}
