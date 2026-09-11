package chat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func forkEndpointRequest(t *testing.T, sourceID, operation string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := sidechatRequest(http.MethodPost, "/api/core/conversations/"+sourceID+"/"+operation, "u1", string(raw), map[string]string{"conversation_id": sourceID})
	r.Header.Set("Idempotency-Key", "eligibility-test")
	w := httptest.NewRecorder()
	if operation == "fork-preview" {
		PreviewConversationFork(w, r)
	} else {
		CreateConversationFork(w, r)
	}
	return w
}

func TestForkEndpointsRejectUnsuccessfulTargetsAfterSuccessfulPreview(t *testing.T) {
	for _, status := range []string{"failed", "cancelled", "interrupted", "generating"} {
		t.Run(status, func(t *testing.T) {
			db, c, items, request := forkFixture(t, 1)
			store.Init(db, nil, nil)
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			previewRequest := forkPreviewRequest{SourceHistoryID: items[0].ID}
			if w := forkEndpointRequest(t, c.ID, "fork-preview", previewRequest); w.Code != http.StatusOK {
				t.Fatalf("successful preview: %d %s", w.Code, w.Body.String())
			}
			// A saved partial answer must not make a failed or cancelled run eligible.
			if err := db.Model(&items[0]).Update("run_status", status).Error; err != nil {
				t.Fatal(err)
			}
			wantCode := "SOURCE_NOT_COMPLETED"
			if status == "generating" {
				wantCode = "SOURCE_NOT_SETTLED"
			}
			for _, operation := range []string{"fork-preview", "forks"} {
				var body any = previewRequest
				if operation == "forks" {
					body = request
				}
				w := forkEndpointRequest(t, c.ID, operation, body)
				var failure struct {
					Code      string `json:"code"`
					Message   string `json:"message"`
					RequestID string `json:"request_id"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &failure); err != nil {
					t.Fatal(err)
				}
				if w.Code != http.StatusConflict || failure.Code != wantCode || failure.Message == "" || failure.RequestID == "" {
					t.Fatalf("%s accepted %s target: %d %s", operation, status, w.Code, w.Body.String())
				}
			}
			for _, model := range []any{&orm.ConversationForkOrigin{}, &orm.ConversationForkRequest{}} {
				var count int64
				if err := db.Model(model).Count(&count).Error; err != nil || count != 0 {
					t.Fatalf("partial Fork write: %T count=%d err=%v", model, count, err)
				}
			}
			var count int64
			if err := db.Model(&orm.Conversation{}).Count(&count).Error; err != nil || count != 1 {
				t.Fatalf("empty branch created: count=%d err=%v", count, err)
			}
		})
	}
}

func TestForkSuccessfulTargetPreservesStablePrefixAndExcludesUnsuccessfulTail(t *testing.T) {
	for _, status := range []string{"failed", "cancelled", "interrupted", "generating"} {
		t.Run(status, func(t *testing.T) {
			db, c, items, _ := forkFixture(t, 4)
			store.Init(db, nil, nil)
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			if err := db.Model(&items[1]).Update("run_status", "failed").Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&items[3]).Update("run_status", status).Error; err != nil {
				t.Fatal(err)
			}
			w := forkEndpointRequest(t, c.ID, "fork-preview", forkPreviewRequest{SourceHistoryID: items[2].ID})
			var preview forkPreview
			if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil || w.Code != http.StatusOK || !preview.CanFork {
				t.Fatalf("successful target preview: %d %s err=%v", w.Code, w.Body.String(), err)
			}
			request := forkCreateRequest{SourceHistoryID: items[2].ID, ExpectedPrefixRevision: preview.PrefixRevision}
			w = forkEndpointRequest(t, c.ID, "forks", request)
			var result forkResult
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != http.StatusCreated {
				t.Fatalf("successful target create: %d %s err=%v", w.Code, w.Body.String(), err)
			}
			var copied []orm.ChatHistory
			if err := db.Where("conversation_id = ?", result.Conversation["conversation_id"]).Order("seq").Find(&copied).Error; err != nil {
				t.Fatal(err)
			}
			if len(copied) != 3 || copied[1].RunStatus != "failed" || copied[2].Result != items[2].Result || copied[2].RunStatus != "completed" {
				t.Fatalf("incorrect inherited prefix: %#v", copied)
			}
			// The earliest successful node remains eligible as well as the latest one.
			if w := forkEndpointRequest(t, c.ID, "fork-preview", forkPreviewRequest{SourceHistoryID: items[0].ID}); w.Code != http.StatusOK {
				t.Fatalf("earlier successful target rejected: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestForkLegacyTargetRequiresPersistedAnswerWithoutTrackedRun(t *testing.T) {
	for _, test := range []struct {
		name, answer, runID string
		want                int
	}{
		{"saved answer", "legacy answer", "", http.StatusCreated},
		{"missing answer", "", "", http.StatusConflict},
		{"unfinished tracked run", "partial answer", "run-pending", http.StatusConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, c, items, _ := forkFixture(t, 1)
			store.Init(db, nil, nil)
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			items[0].RunStatus, items[0].RunID, items[0].Result = "", test.runID, test.answer
			if err := db.Model(&items[0]).Updates(map[string]any{"run_status": "", "run_id": test.runID, "result": test.answer}).Error; err != nil {
				t.Fatal(err)
			}
			revision, err := forkPrefixRevision(items)
			if err != nil {
				t.Fatal(err)
			}
			w := forkEndpointRequest(t, c.ID, "forks", forkCreateRequest{SourceHistoryID: items[0].ID, ExpectedPrefixRevision: revision})
			if w.Code != test.want {
				t.Fatalf("legacy target status=%d want=%d: %s", w.Code, test.want, w.Body.String())
			}
		})
	}
}
