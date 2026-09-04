package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"lazymind/core/algo"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestWriterSyncReplyUsesProviderSynced(t *testing.T) {
	recorder := httptest.NewRecorder()
	writerSyncReply(recorder, "synced", 2, true, &algo.WriterDocumentSyncResponse{
		Success:        true,
		ProviderSynced: true,
		PatchResult:    json.RawMessage(`{"success":true}`),
		PersistedDocument: json.RawMessage(
			`{"document_id":"doc-1","blocks":[]}`,
		),
	})

	body := recorder.Body.String()
	if !strings.Contains(body, `"provider_synced":true`) {
		t.Fatalf("provider_synced missing from response: %s", body)
	}
	if strings.Contains(body, "feishu_synced") {
		t.Fatalf("legacy sync field leaked into response: %s", body)
	}
}

func TestWriterSyncStatus(t *testing.T) {
	for input, want := range map[int]int{
		http.StatusBadRequest:          http.StatusBadRequest,
		http.StatusUnprocessableEntity: http.StatusBadRequest,
		http.StatusUnauthorized:        http.StatusUnauthorized,
		http.StatusForbidden:           http.StatusForbidden,
		http.StatusConflict:            http.StatusConflict,
		http.StatusInternalServerError: http.StatusBadGateway,
	} {
		if got := writerSyncStatus(input); got != want {
			t.Errorf("writerSyncStatus(%d) = %d, want %d", input, got, want)
		}
	}
}

func TestWriterProviderSelection(t *testing.T) {
	for name, test := range map[string]struct {
		value json.RawMessage
		want  string
	}{
		"bound provider":  {json.RawMessage(`{"provider_binding":{"provider":"notion","document_id":"page-1"}}`), "notion"},
		"target adapter":  {json.RawMessage(`{"adapter":"notion","uri":"https://notion.so/page"}`), "notion"},
		"unbound default": {json.RawMessage(`{"document_id":"local"}`), "feishu"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := writerDocumentProvider(test.value); got != test.want {
				t.Fatalf("provider = %q, want %q", got, test.want)
			}
		})
	}

	config, ok := writerProviderToolConfig(map[string]any{
		"feishu": "feishu-token", "notion": "notion-token",
	}, "notion")
	if !ok || len(config) != 1 || config["notion"] != "notion-token" {
		t.Fatalf("unexpected provider config: %#v, %v", config, ok)
	}
}

func TestWriteBackWriterDocumentRequiresFeishuConfiguration(t *testing.T) {
	authService := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"items":[]}}`))
	}))
	t.Cleanup(authService.Close)
	t.Setenv("LAZYMIND_AUTH_SERVICE_URL", authService.URL)

	db := orm.MigrateTestDB(t,
		&orm.WorkflowSession{},
		&orm.WorkflowSlotRevision{},
		&orm.UserModelProvider{},
		&orm.UserModelProviderGroup{},
		&orm.UserSelectedProvider{},
	)
	store.Init(db.DB, db.DB, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now().UTC()
	if err := db.Create(&orm.WorkflowSession{
		ID: "session", ConversationID: "conversation", WorkflowID: "writer-workflow",
		Status: "completed", CreateUserID: "user-1", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed writer session: %v", err)
	}
	seedWriterRevision(
		t, db, "draft-1", "draft_document", 1, true, "ai",
		json.RawMessage(`{"schema":"text/markdown","data":"# Draft\n\nBody"}`),
	)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/core/workflow-sessions/session/writer-document:write-back",
		strings.NewReader(`{"base_revision":1}`),
	)
	req.Header.Set("X-User-Id", "user-1")
	req = mux.SetURLVars(req, map[string]string{"session_id": "session"})
	recorder := httptest.NewRecorder()

	WriteBackWriterDocument(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
	var response struct {
		Data struct {
			Status   string `json:"status"`
			Provider string `json:"provider"`
		} `json:"data"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Data.Status != "feishu_configuration_required" || response.Data.Provider != "feishu" {
		t.Fatalf("unexpected response data: %+v", response.Data)
	}
	var revisionCount int64
	if err := db.Model(&orm.WorkflowSlotRevision{}).
		Where("session_id = ?", "session").Count(&revisionCount).Error; err != nil {
		t.Fatalf("count writer revisions: %v", err)
	}
	if revisionCount != 1 {
		t.Fatalf("revision count = %d, want 1", revisionCount)
	}
}

func TestRenderWriterDocumentKeepsIRCanonicalForPinnedWorkflow(t *testing.T) {
	chatService := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": map[string]any{
				"title":          "Document",
				"representation": "ir",
				"document": map[string]any{
					"document_id": "doc-1",
					"blocks": []map[string]any{{
						"node_id": "heading-1", "type": "heading",
						"content": "1. Heading", "numbering": map[string]any{"level": 1},
					}},
				},
				"numbering": map[string]any{
					"ordered_style": "hierarchical",
					"entries":       map[string]any{"heading-1": map[string]any{"label": "1."}},
				},
			},
		})
	}))
	t.Cleanup(chatService.Close)
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", chatService.URL)

	db := orm.MigrateTestDB(t,
		&orm.WorkflowSession{},
		&orm.WorkflowSlotRevision{},
		&orm.WorkflowHumanArtifact{},
	)
	store.Init(db.DB, db.DB, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now().UTC()
	if err := db.Create(&orm.WorkflowSession{
		ID: "session", ConversationID: "conversation", WorkflowID: "writer-workflow",
		Status: "completed", CreateUserID: "user-1", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed writer session: %v", err)
	}
	if err := db.Create(&orm.WorkflowHumanArtifact{
		ID: "provider-sync-1", SessionID: "session", Slot: "draft_document", ContentType: "json",
		Value:     json.RawMessage(`{"schema":"lazyllm.tools.writer.data_models.writer_ir.WriterDocument","data":{"document_id":"doc-1","title":"Document","blocks":[{"node_id":"heading-1","type":"heading","content":"Heading","numbering":{"level":1}}]}}`),
		CreatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed provider-sync artifact: %v", err)
	}
	humanID := "provider-sync-1"
	if err := db.Create(&orm.WorkflowSlotRevision{
		ID: "revision-1", SessionID: "session", SlotID: "draft_document",
		Revision: 1, Selected: true, ChangeSource: "provider_sync", HumanArtifactID: &humanID,
		Slot: "draft_document", StepID: "write_document", Attempt: 1, CreatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed writer revision: %v", err)
	}

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/core/workflow-sessions/session/writer-document:render",
		strings.NewReader(`{"slot":"draft_document"}`),
	)
	req.Header.Set("X-User-Id", "user-1")
	req = mux.SetURLVars(req, map[string]string{"session_id": "session"})
	recorder := httptest.NewRecorder()
	RenderWriterDocument(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data struct {
			Document struct {
				Blocks []struct {
					Content string `json:"content"`
				} `json:"blocks"`
			} `json:"document"`
		} `json:"data"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := response.Data.Document.Blocks[0].Content; got != "Heading" {
		t.Fatalf("rendered heading = %q, want canonical content", got)
	}
}

func TestSaveWriterDocumentDraftUpdatesInPlaceAndCheckpointCreatesRevision(t *testing.T) {
	chatService := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/workflow/actions:invoke" {
			t.Errorf("path = %q, want workflow action invoke", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		var request struct {
			Artifact json.RawMessage `json:"artifact"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode workflow action request: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		var edited struct {
			Data string `json:"data"`
		}
		if err := json.Unmarshal(request.Artifact, &edited); err != nil {
			t.Errorf("decode edited artifact: %v", err)
			http.Error(w, "invalid artifact", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": map[string]any{
				"source_document": edited.Data,
				"representation":  "markdown",
				"document":        edited.Data,
				"title":           "Draft",
			},
		})
	}))
	t.Cleanup(chatService.Close)
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", chatService.URL)

	db := orm.MigrateTestDB(t,
		&orm.WorkflowSession{},
		&orm.WorkflowSlotRevision{},
		&orm.WorkflowHumanArtifact{},
	)
	store.Init(db.DB, db.DB, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now().UTC()
	if err := db.Create(&orm.WorkflowSession{
		ID: "session", ConversationID: "conversation", WorkflowID: "writer-workflow",
		Status: "completed", CreateUserID: "user-1", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed writer session: %v", err)
	}
	if err := db.Create(&orm.WorkflowHumanArtifact{
		ID: "human-3", SessionID: "session", Slot: "draft_document", ContentType: "json",
		Value: json.RawMessage(`{"schema":"text/markdown","data":"# Initial"}`), CreatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed human artifact: %v", err)
	}
	humanID := "human-3"
	if err := db.Create(&orm.WorkflowSlotRevision{
		ID: "revision-3", SessionID: "session", SlotID: "draft_document",
		Revision: 3, Selected: true, ChangeSource: "human", HumanArtifactID: &humanID,
		Slot: "draft_document", StepID: "write_document", Attempt: 1, CreatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed writer revision: %v", err)
	}

	save := func(mode, document string, baseRevision int) int {
		body, err := json.Marshal(map[string]any{
			"base_revision": baseRevision,
			"document":      document,
			"mode":          mode,
		})
		if err != nil {
			t.Fatalf("marshal save body: %v", err)
		}
		req := httptest.NewRequest(
			http.MethodPost,
			"/api/core/workflow-sessions/session/writer-document:save",
			strings.NewReader(string(body)),
		)
		req.Header.Set("X-User-Id", "user-1")
		req = mux.SetURLVars(req, map[string]string{"session_id": "session"})
		recorder := httptest.NewRecorder()
		SaveWriterDocument(recorder, req)
		if recorder.Code != http.StatusOK {
			t.Fatalf("save %s status = %d, body=%s", mode, recorder.Code, recorder.Body.String())
		}
		var response struct {
			Data struct {
				Revision int `json:"revision"`
			} `json:"data"`
		}
		if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
			t.Fatalf("decode save response: %v", err)
		}
		return response.Data.Revision
	}

	if revision := save("draft", "# First edit", 3); revision != 3 {
		t.Fatalf("first draft revision = %d, want 3", revision)
	}
	if revision := save("draft", "# Second edit", 3); revision != 3 {
		t.Fatalf("second draft revision = %d, want 3", revision)
	}
	var revisionCount int64
	if err := db.Model(&orm.WorkflowSlotRevision{}).
		Where("session_id = ? AND slot_id = ?", "session", "draft_document").
		Count(&revisionCount).Error; err != nil {
		t.Fatalf("count draft revisions: %v", err)
	}
	if revisionCount != 1 {
		t.Fatalf("draft revision count = %d, want 1", revisionCount)
	}
	var updatedArtifact orm.WorkflowHumanArtifact
	if err := db.First(&updatedArtifact, "id = ?", humanID).Error; err != nil {
		t.Fatalf("load updated human artifact: %v", err)
	}
	var updatedValue struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(updatedArtifact.Value, &updatedValue); err != nil {
		t.Fatalf("decode updated human artifact: %v", err)
	}
	if updatedValue.Data != "# Second edit" {
		t.Fatalf("updated draft = %q, want second edit", updatedValue.Data)
	}

	if revision := save("checkpoint", "# Final edit", 3); revision != 4 {
		t.Fatalf("checkpoint revision = %d, want 4", revision)
	}
	if err := db.Model(&orm.WorkflowSlotRevision{}).
		Where("session_id = ? AND slot_id = ?", "session", "draft_document").
		Count(&revisionCount).Error; err != nil {
		t.Fatalf("count checkpoint revisions: %v", err)
	}
	if revisionCount != 2 {
		t.Fatalf("checkpoint revision count = %d, want 2", revisionCount)
	}
}

func TestNormalizeWriterDocumentForSync_StripsLegacyImagePlaceholderNewline(t *testing.T) {
	normalized, err := normalizeWriterDocumentForSync(json.RawMessage(`{
		"blocks":[
			{"node_id":"image-1","type":"image","content":"\n\ncaption","spans":[{"text":"\n\ncaption","style":[]}]},
			{"node_id":"paragraph-1","type":"paragraph","content":"\nkeep this newline","spans":[{"text":"\nkeep this newline","style":[]}]}
		]
	}`))
	if err != nil {
		t.Fatalf("normalize WriterDocument: %v", err)
	}
	var document struct {
		Blocks []struct {
			Type    string `json:"type"`
			Content string `json:"content"`
			Spans   []struct {
				Text string `json:"text"`
			} `json:"spans"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal(normalized, &document); err != nil {
		t.Fatalf("decode normalized WriterDocument: %v", err)
	}
	if document.Blocks[0].Content != "caption" || document.Blocks[0].Spans[0].Text != "caption" || document.Blocks[1].Content != "\nkeep this newline" {
		t.Fatalf("unexpected normalized blocks: %+v", document.Blocks)
	}
}

func TestPreserveExistingWriterImageBlocks(t *testing.T) {
	source := json.RawMessage(`{
		"blocks":[
			{"node_id":"paragraph-1","type":"paragraph","content":"before"},
			{"node_id":"image-1","type":"image","content":"saved caption","metadata":{"asset":"asset-1"}}
		]
	}`)
	revised := json.RawMessage(`{
		"blocks":[
			{"node_id":"paragraph-1","type":"paragraph","content":"edited text"},
			{"node_id":"image-1","type":"image","content":"\n\nsaved caption","spans":[{"text":"\n\nsaved caption","style":[]}]},
			{"node_id":"image-new","type":"image","content":"new image"}
		]
	}`)

	preserved, err := preserveExistingWriterImageBlocks(source, revised)
	if err != nil {
		t.Fatalf("preserve Writer image blocks: %v", err)
	}
	var document struct {
		Blocks []map[string]any `json:"blocks"`
	}
	if err := json.Unmarshal(preserved, &document); err != nil {
		t.Fatalf("decode preserved WriterDocument: %v", err)
	}
	image := document.Blocks[1]
	_, hasSpans := image["spans"]
	if document.Blocks[0]["content"] != "edited text" || image["content"] != "saved caption" || hasSpans || document.Blocks[2]["content"] != "new image" {
		t.Fatalf("unexpected preserved blocks: %+v", document.Blocks)
	}
}

func TestWriterDocumentIsUnbound(t *testing.T) {
	if !writerDocumentIsUnbound(json.RawMessage(`{"document_id":"local","blocks":[],"provider_binding":{}}`)) {
		t.Fatal("local WriterDocument should be unbound")
	}
	if writerDocumentIsUnbound(json.RawMessage(`{"document_id":"cloud","blocks":[],"provider_binding":{"provider":"feishu","document_id":"doc-1"}}`)) {
		t.Fatal("Feishu WriterDocument should not be unbound")
	}
}

func TestUnbindWriterDocumentClearsNestedProviderState(t *testing.T) {
	unbound, err := unbindWriterDocument(json.RawMessage(`{
		"document_id":"page-1",
		"revision":"rev-1",
		"provider_binding":{"provider":"notion","document_id":"page-1"},
		"blocks":[{
			"node_id":"heading-1",
			"provider_binding":{"provider":"notion","block_id":"block-1"},
			"provider_payload":{"archived":false},
			"children":[{
				"node_id":"paragraph-1",
				"provider_binding":{"provider":"notion","block_id":"block-2"},
				"provider_payload":{"archived":false}
			}]
		}]
	}`))
	if err != nil {
		t.Fatalf("unbind WriterDocument: %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(unbound, &document); err != nil {
		t.Fatalf("decode unbound WriterDocument: %v", err)
	}
	if _, exists := document["revision"]; exists {
		t.Fatal("provider revision was not removed")
	}
	if binding, _ := document["provider_binding"].(map[string]any); len(binding) != 0 {
		t.Fatalf("document provider binding = %#v, want empty", binding)
	}
	blocks := document["blocks"].([]any)
	parent := blocks[0].(map[string]any)
	child := parent["children"].([]any)[0].(map[string]any)
	for name, block := range map[string]map[string]any{"parent": parent, "child": child} {
		if binding, _ := block["provider_binding"].(map[string]any); len(binding) != 0 {
			t.Fatalf("%s provider binding = %#v, want empty", name, binding)
		}
		if payload, _ := block["provider_payload"].(map[string]any); len(payload) != 0 {
			t.Fatalf("%s provider payload = %#v, want empty", name, payload)
		}
	}
}

func TestWriterDocumentRenderSlotIncludesSource(t *testing.T) {
	if slot, ok := writerDocumentRenderSlot("source_document"); !ok || slot != "source_document" {
		t.Fatalf("source_document render slot = %q, %v", slot, ok)
	}
	if _, ok := writerDocumentSlot("source_document"); ok {
		t.Fatal("source_document must remain read-only")
	}
	if slot, ok := writerDocumentSlot("flat_draft_document"); !ok || slot != "flat_draft_document" {
		t.Fatalf("flat_draft_document slot = %q, %v", slot, ok)
	}
}

func TestLoadWriterWriteBackArtifact_InlineMarkdown(t *testing.T) {
	artifact, err := loadWriterWriteBackArtifact(json.RawMessage(
		`{"schema":"text/markdown","data":"# Draft\n"}`,
	))
	if err != nil {
		t.Fatalf("load inline Markdown: %v", err)
	}
	if artifact.Format != "markdown" || artifact.Markdown != "# Draft\n" || artifact.Title != "Draft" {
		t.Fatalf("unexpected inline Markdown artifact: %+v", artifact)
	}
}

func TestLoadWriterWriteBackBaseline_UsesSourceDocumentForInitialSync(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.WorkflowSlotRevision{})
	source := json.RawMessage(`{"data":{"document_id":"feishu-doc","provider_binding":{"provider":"feishu","document_id":"feishu-doc"}}}`)
	seedWriterRevision(t, db, "source", "source_document", 1, true, "ai", source)
	seedWriterRevision(t, db, "draft-1", "draft_document", 1, false, "ai", source)
	seedWriterRevision(t, db, "draft-2", "draft_document", 2, true, "human", source)

	baseline, err := loadWriterWriteBackBaseline(context.Background(), db.DB, "session", "draft_document", 2)
	if err != nil {
		t.Fatalf("load baseline: %v", err)
	}
	if baseline.Revision.SlotID != "source_document" {
		t.Fatalf("baseline slot = %q, want source_document", baseline.Revision.SlotID)
	}
	if baseline.Revision.Revision != 1 {
		t.Fatalf("baseline revision = %d, want 1", baseline.Revision.Revision)
	}
}

func TestLoadWriterWriteBackBaseline_PrefersLatestSyncedDraft(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.WorkflowSlotRevision{})
	source := json.RawMessage(`{"data":{"document_id":"source-doc","provider_binding":{"provider":"feishu","document_id":"source-doc"}}}`)
	syncedDraft := json.RawMessage(`{"data":{"document_id":"synced-doc","provider_binding":{"provider":"feishu","document_id":"synced-doc"}},"meta":{"lazymind_provider_sync":{"confirmed":true}}}`)
	seedWriterRevision(t, db, "source", "source_document", 1, true, "ai", source)
	seedWriterRevision(t, db, "draft-1", "flat_draft_document", 1, false, "host", syncedDraft)
	seedWriterRevision(t, db, "draft-2", "flat_draft_document", 2, false, "human", syncedDraft)
	seedWriterRevision(t, db, "draft-3", "flat_draft_document", 3, true, "human", syncedDraft)

	baseline, err := loadWriterWriteBackBaseline(context.Background(), db.DB, "session", "flat_draft_document", 3)
	if err != nil {
		t.Fatalf("load baseline: %v", err)
	}
	if baseline.Revision.ID != "draft-1" {
		t.Fatalf("baseline id = %q, want draft-1", baseline.Revision.ID)
	}
}

func seedWriterRevision(
	t *testing.T,
	db *orm.DB,
	id, slotID string,
	revision int,
	selected bool,
	changeSource string,
	content json.RawMessage,
) {
	t.Helper()
	if err := db.Create(&orm.WorkflowSlotRevision{
		ID:              id,
		SessionID:       "session",
		SlotID:          slotID,
		Revision:        revision,
		Selected:        selected,
		ContentSnapshot: content,
		ChangeSource:    changeSource,
		Slot:            slotID,
		StepID:          "write_document",
		Attempt:         1,
		CreatedAt:       time.Now().UTC(),
	}).Error; err != nil {
		t.Fatalf("seed revision %s: %v", id, err)
	}
}
