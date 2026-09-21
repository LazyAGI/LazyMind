package chat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"lazymind/core/algo"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"lazymind/core/workflow"
)

func TestWriterSyncReplyUsesProviderSynced(t *testing.T) {
	recorder := httptest.NewRecorder()
	writerSyncReply(recorder, "synced", 2, 3, true, &algo.WriterDocumentSyncResponse{
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
	if !strings.Contains(body, `"draft_version":3`) {
		t.Fatalf("draft_version missing from response: %s", body)
	}
	if strings.Contains(body, "feishu_synced") {
		t.Fatalf("legacy sync field leaked into response: %s", body)
	}
}

func TestWriterSyncStatus(t *testing.T) {
	for input, want := range map[int]int{
		http.StatusBadRequest:          http.StatusBadRequest,
		http.StatusUnprocessableEntity: http.StatusUnprocessableEntity,
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

func TestWriterProviderTargetInUseReportsAccuratePartialSuccess(t *testing.T) {
	recorder := httptest.NewRecorder()
	replyWriterProviderTargetLocalConflict(recorder, &algo.WriterDocumentSyncResponse{
		Provider: "notion", ProviderSynced: true,
		PatchResult:       json.RawMessage(`{"success":true}`),
		PersistedDocument: json.RawMessage(`{"document_id":"doc-1"}`),
	})
	var response struct {
		Data struct {
			Code                string `json:"code"`
			ProviderSynced      bool   `json:"provider_synced"`
			ArtifactSaved       bool   `json:"artifact_saved"`
			TargetArtifactSaved bool   `json:"target_artifact_saved"`
			Retryable           bool   `json:"retryable"`
		} `json:"data"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusConflict || response.Data.Code != "PROVIDER_SYNC_LOCAL_CONFLICT" ||
		!response.Data.ProviderSynced || !response.Data.ArtifactSaved ||
		response.Data.TargetArtifactSaved || response.Data.Retryable {
		t.Fatalf("target partial success: status=%d response=%+v", recorder.Code, response.Data)
	}
}

func TestValidateWriterDraftVersionUsesArtifactBacking(t *testing.T) {
	for _, changeSource := range []string{"human", "provider_sync", "host"} {
		t.Run(changeSource, func(t *testing.T) {
			db := orm.MigrateTestDB(t, &orm.DocumentPublicationOperation{}, &orm.DocumentPublicationBinding{}, &orm.WorkflowHumanArtifact{}, &orm.WorkflowSessionStep{}, &orm.WorkflowEvent{}, &orm.WorkflowAttemptInputBinding{}, &orm.WorkflowRouteDecision{}, &orm.WorkflowHumanArtifact{})
			humanID := "artifact-" + changeSource
			if err := db.Create(&orm.WorkflowHumanArtifact{
				ID: humanID, SessionID: "session", Slot: "draft_document",
				ContentType: "json", Value: json.RawMessage(`{"data":"draft"}`),
				DraftVersion: 2, CreatedAt: time.Now().UTC(),
			}).Error; err != nil {
				t.Fatalf("seed artifact: %v", err)
			}
			revision := orm.WorkflowSlotRevision{
				ChangeSource: changeSource, HumanArtifactID: &humanID,
			}
			if err := validateWriterDraftVersion(t.Context(), db.DB, revision, nil); !errors.Is(err, workflow.ErrDraftVersionRequired) {
				t.Fatalf("missing version error = %v, want DRAFT_VERSION_REQUIRED", err)
			}
			stale := int64(1)
			if err := validateWriterDraftVersion(t.Context(), db.DB, revision, &stale); !errors.Is(err, workflow.ErrDraftVersionConflict) {
				t.Fatalf("stale version error = %v, want DRAFT_VERSION_CONFLICT", err)
			}
			current := int64(2)
			if err := validateWriterDraftVersion(t.Context(), db.DB, revision, &current); err != nil {
				t.Fatalf("current version: %v", err)
			}
		})
	}
}

func TestSyncWriterDocumentPersistsProviderSyncRevision(t *testing.T) {
	var providerCalls atomic.Int64
	service := httptest.NewServer(adaptLegacyWriterFixture(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/v1/cloud/connections/internal/chat-enabled") &&
			r.URL.Query().Get("provider") == "notion":
			_, _ = w.Write([]byte(`{"data":{"items":[{"connection_id":"notion-1","provider":"notion","owner_user_id":"user-1","status":"ACTIVE"}]}}`))
		case strings.HasSuffix(r.URL.Path, "/v1/cloud/connections/notion-1/token"):
			_, _ = w.Write([]byte(`{"data":{"connection_id":"notion-1","provider":"notion","status":"ACTIVE","access_token":"notion-token"}}`))
		case r.URL.Path == "/api/workflow/actions:invoke":
			providerCalls.Add(1)
			_, _ = w.Write([]byte(`{"result":{
				"success":true,"changed":true,"provider_synced":true,
				"patch_result":{"success":true},
				"persisted_document":{"document_id":"doc-1","provider_binding":{"provider":"notion","document_id":"page-1"},"blocks":[]},
				"provider":"notion"
			}}`))
		default:
			_, _ = w.Write([]byte(`{"data":{"items":[]}}`))
		}
	})))
	t.Cleanup(service.Close)
	t.Setenv("LAZYMIND_AUTH_SERVICE_URL", service.URL)
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", service.URL)

	db := orm.MigrateTestDB(t, &orm.DocumentPublicationOperation{}, &orm.DocumentPublicationBinding{}, &orm.WorkflowHumanArtifact{}, &orm.WorkflowSessionStep{}, &orm.WorkflowEvent{}, &orm.WorkflowAttemptInputBinding{}, &orm.WorkflowRouteDecision{},
		&orm.WorkflowSession{}, &orm.WorkflowSlotRevision{}, &orm.WorkflowHumanArtifact{},
		&orm.WorkflowEvent{}, &orm.WorkflowAttemptInputBinding{}, &orm.WorkflowRouteDecision{},
		&orm.UserModelProvider{}, &orm.UserModelProviderGroup{}, &orm.UserSelectedProvider{},
	)
	store.Init(db.DB, db.DB, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Now().UTC()
	if err := db.Create(&orm.WorkflowSession{
		ID: "session", ConversationID: "conversation", WorkflowID: "writer-workflow",
		Status: "completed", CreateUserID: "user-1", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed session: %v", err)
	}
	humanID := "human-1"
	if err := db.Create(&orm.WorkflowHumanArtifact{
		ID: humanID, SessionID: "session", Slot: "provider_document", ContentType: "json",
		Value:        json.RawMessage(`{"data":{"document_id":"doc-1","provider_binding":{"provider":"notion","document_id":"page-1"},"blocks":[]}}`),
		DraftVersion: 1, CreatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed artifact: %v", err)
	}
	if err := db.Create(&orm.WorkflowSlotRevision{
		ID: "revision-1", SessionID: "session", SlotID: "provider_document",
		Revision: 1, Selected: true, ChangeSource: "human", HumanArtifactID: &humanID,
		Slot: "provider_document", StepID: "write_document", Attempt: 1, CreatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed revision: %v", err)
	}

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/core/workflow-sessions/session/slots/provider_document/items/idx/-1:sync-writer-document",
		strings.NewReader(`{
			"base_revision":1,"base_draft_version":1,"mode":"draft",
			"source_document":{"document_id":"doc-1","provider_binding":{"provider":"notion","document_id":"page-1"},"blocks":[]},
			"revised_document":{"document_id":"doc-1","provider_binding":{"provider":"notion","document_id":"page-1"},"blocks":[]}
		}`),
	)
	req.Header.Set("X-User-Id", "user-1")
	req = mux.SetURLVars(req, map[string]string{
		"session_id": "session", "slot_id": "provider_document", "list_index": "-1",
	})
	recorder := httptest.NewRecorder()
	SyncWriterDocument(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	var revisions []orm.WorkflowSlotRevision
	if err := db.Where(
		"session_id = ? AND slot_id = ?", "session", "provider_document",
	).Order("revision ASC").Find(&revisions).Error; err != nil {
		t.Fatalf("load revisions: %v", err)
	}
	if len(revisions) != 2 || revisions[0].Selected || !revisions[1].Selected ||
		revisions[1].ChangeSource != "provider_sync" || revisions[1].HumanArtifactID == nil ||
		*revisions[1].HumanArtifactID == humanID {
		t.Fatalf("provider sync revisions = %#v", revisions)
	}
	var oldArtifact orm.WorkflowHumanArtifact
	if err := db.First(&oldArtifact, "id = ?", humanID).Error; err != nil {
		t.Fatalf("load old artifact: %v", err)
	}
	if oldArtifact.DraftVersion != 1 {
		t.Fatalf("old artifact draft version=%d, want 1", oldArtifact.DraftVersion)
	}
	staleReq := httptest.NewRequest(
		http.MethodPost,
		"/api/core/workflow-sessions/session/slots/provider_document/items/idx/-1:sync-writer-document",
		strings.NewReader(`{
			"base_revision":1,"base_draft_version":1,"mode":"draft",
			"source_document":{"document_id":"doc-1","provider_binding":{"provider":"notion","document_id":"page-1"},"blocks":[]},
			"revised_document":{"document_id":"doc-1","title":"stale edit","provider_binding":{"provider":"notion","document_id":"page-1"},"blocks":[]}
		}`),
	)
	staleReq.Header.Set("X-User-Id", "user-1")
	staleReq = mux.SetURLVars(staleReq, map[string]string{
		"session_id": "session", "slot_id": "provider_document", "list_index": "-1",
	})
	staleRecorder := httptest.NewRecorder()
	SyncWriterDocument(staleRecorder, staleReq)
	if staleRecorder.Code != http.StatusConflict ||
		writerErrorCode(t, staleRecorder) != "REVISION_CONFLICT" {
		t.Fatalf("stale sync: status=%d body=%s", staleRecorder.Code, staleRecorder.Body.String())
	}
	if calls := providerCalls.Load(); calls != 1 {
		t.Fatalf("provider calls=%d, want 1", calls)
	}
}

func writerErrorCode(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	var response struct {
		Data struct {
			Code string `json:"code"`
		} `json:"data"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, recorder.Body.String())
	}
	return response.Data.Code
}

func TestWriterMutationEndpointsRequireBaseRevision(t *testing.T) {
	for _, testCase := range []struct {
		name, path, body string
		handler          http.HandlerFunc
		pathVars         map[string]string
	}{
		{
			name: "sync", path: "/sync",
			body:     `{"source_document":{"document_id":"doc"},"revised_document":{"document_id":"doc"}}`,
			handler:  SyncWriterDocument,
			pathVars: map[string]string{"session_id": "session", "slot_id": "draft_document", "list_index": "-1"},
		},
		{
			name: "save", path: "/save", body: `{"document":"# Draft"}`,
			handler:  SaveWriterDocument,
			pathVars: map[string]string{"session_id": "session"},
		},
		{
			name: "write_back", path: "/write-back", body: `{}`,
			handler:  WriteBackWriterDocument,
			pathVars: map[string]string{"session_id": "session"},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, testCase.path, strings.NewReader(testCase.body))
			req = mux.SetURLVars(req, testCase.pathVars)
			recorder := httptest.NewRecorder()
			testCase.handler(recorder, req)
			if recorder.Code != http.StatusBadRequest || writerErrorCode(t, recorder) != "REVISION_REQUIRED" {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestWriterActionErrorDataPreservesStructuredProviderFailure(t *testing.T) {
	err := &common.HTTPError{
		StatusCode: http.StatusBadGateway,
		Message:    "provider response was lost",
		Body: json.RawMessage(`{"detail":{
			"code":"PROVIDER_WRITE_OUTCOME_AMBIGUOUS",
			"message":"provider response was lost",
			"retryable":false,
			"provider":"notion"
		}}`),
	}
	data := writerActionErrorData(err, map[string]any{"status": "write_back_failed"})
	if data["code"] != "PROVIDER_WRITE_OUTCOME_AMBIGUOUS" || data["retryable"] != false ||
		data["provider"] != "notion" {
		t.Fatalf("unexpected structured provider error: %#v", data)
	}
}

func TestWriterProviderSelection(t *testing.T) {
	for name, test := range map[string]struct {
		value json.RawMessage
		want  string
	}{
		"bound provider":   {json.RawMessage(`{"provider_binding":{"provider":"notion","document_id":"page-1"}}`), "notion"},
		"WeChat provider":  {json.RawMessage(`{"provider_binding":{"provider":"wechat","document_id":"draft-1"}}`), "wechat"},
		"target adapter":   {json.RawMessage(`{"adapter":"notion","uri":"https://notion.so/page"}`), "notion"},
		"unbound document": {json.RawMessage(`{"document_id":"local"}`), ""},
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

func TestWriterProviderSelectionSupportsGitHubTarget(t *testing.T) {
	target := json.RawMessage(
		`{"adapter":"github","uri":"githubrepo:/acme/docs/README.md?ref=main"}`,
	)
	if got := writerDocumentProvider(target); got != "github" {
		t.Fatalf("provider = %q, want github", got)
	}
}

func TestWriterProviderSelectionSupportsObsidianTarget(t *testing.T) {
	target := json.RawMessage(
		`{"adapter":"obsidian","uri":"obsidian://vlt_test/Note.md"}`,
	)
	if got := writerDocumentProvider(target); got != "obsidian" {
		t.Fatalf("provider = %q, want obsidian", got)
	}
}

func TestWriterProviderCredentialPolicy(t *testing.T) {
	if writerProviderRequiresToolConfig("obsidian") {
		t.Fatal("Obsidian should not require cloud document credentials")
	}
	for _, provider := range []string{"feishu", "notion", "github", "wechat"} {
		if !writerProviderRequiresToolConfig(provider) {
			t.Fatalf("%s should retain the existing cloud credential requirement", provider)
		}
	}
}

func TestAttachWriterMediaURLs(t *testing.T) {
	uploadRoot := t.TempDir()
	imagePath := filepath.Join(uploadRoot, "session", "diagram.png")
	if err := os.MkdirAll(filepath.Dir(imagePath), 0o755); err != nil {
		t.Fatalf("create image directory: %v", err)
	}
	if err := os.WriteFile(imagePath, []byte("image"), 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}
	t.Setenv("LAZYMIND_UPLOAD_ROOT", uploadRoot)

	db := orm.MigrateTestDB(t, &orm.DocumentPublicationOperation{}, &orm.DocumentPublicationBinding{}, &orm.WorkflowHumanArtifact{}, &orm.WorkflowSessionStep{}, &orm.WorkflowEvent{}, &orm.WorkflowAttemptInputBinding{}, &orm.WorkflowRouteDecision{}, &orm.WorkflowSlotRevision{})
	digest := strings.Repeat("a", 64)
	sourceURI := "https://example.test/diagram.png"
	mediaArtifact, err := json.Marshal(map[string]any{
		"data": map[string]any{
			"assets": map[string]any{
				"diagram": map[string]any{
					"media_asset_id": "diagram-id",
					"uri":            sourceURI,
					"local_path":     imagePath,
					"meta": map[string]any{
						"source_reference": "docs/assets/diagram.png",
						"sha256":           digest,
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal media artifact: %v", err)
	}
	seedWriterRevision(t, db, "media", "media_assets", 1, true, "ai", mediaArtifact)
	seedWriterRevision(t, db, "target", "target_document", 1, true, "ai", json.RawMessage(`{
		"data":{"adapter":"github","meta":{"github_writer_media_aliases":{"assets/custom.png":"diagram-id"}}}
	}`))

	result := map[string]any{"representation": "markdown"}
	attachWriterMediaURLs(context.Background(), db.DB, "session", "draft_document", result)
	urls := result["media_urls"].(map[string]string)
	for _, reference := range []string{
		"docs/assets/diagram.png",
		imagePath,
		sourceURI,
		"assets/custom.png",
		"assets/aa/" + digest + ".png",
	} {
		if url := urls[reference]; !strings.HasPrefix(url, "/static-files/") ||
			!strings.Contains(url, "sig=") || strings.Contains(url, uploadRoot) {
			t.Fatalf("media URL for %q = %q", reference, url)
		}
	}

	irResult := map[string]any{
		"representation": "ir",
		"document": map[string]any{
			"blocks": []any{map[string]any{
				"type": "heading",
				"children": []any{map[string]any{
					"type": "image",
					"references": []any{map[string]any{
						"type": "media_asset",
						"id":   "diagram-id",
						"path": imagePath,
					}},
				}},
			}},
		},
	}
	rawDocument, err := json.Marshal(irResult["document"])
	if err != nil {
		t.Fatalf("marshal IR document: %v", err)
	}
	irResult["document"] = json.RawMessage(rawDocument)
	attachWriterMediaURLs(context.Background(), db.DB, "session", "draft_document", irResult)
	document := irResult["document"].(map[string]any)
	heading := document["blocks"].([]any)[0].(map[string]any)
	image := heading["children"].([]any)[0].(map[string]any)
	references := image["references"].([]any)
	preview := references[1].(map[string]any)
	if preview["type"] != "preview_asset" || preview["id"] != "diagram-id" {
		t.Fatalf("unexpected IR preview reference: %#v", preview)
	}
	if url, _ := preview["url"].(string); !strings.HasPrefix(url, "/static-files/") ||
		!strings.Contains(url, "sig=") || strings.Contains(url, uploadRoot) {
		t.Fatalf("IR preview URL = %q", url)
	}
}

func TestWriteBackWriterDocumentRequiresExplicitProvider(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.DocumentPublicationOperation{}, &orm.DocumentPublicationBinding{}, &orm.WorkflowHumanArtifact{}, &orm.WorkflowSessionStep{}, &orm.WorkflowEvent{}, &orm.WorkflowAttemptInputBinding{}, &orm.WorkflowRouteDecision{},
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
	if response.Data.Status != "provider_selection_required" || response.Data.Provider != "" {
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

func TestWriteBackWriterDocumentUsesBoundGitHubProvider(t *testing.T) {
	requestedProvider := ""
	authService := httptest.NewServer(adaptLegacyWriterFixture(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		requestedProvider = r.URL.Query().Get("provider")
		w.WriteHeader(503)
		_, _ = w.Write([]byte(`{"message":"fixture authorization unavailable"}`))
	})))
	t.Cleanup(authService.Close)
	t.Setenv("LAZYMIND_AUTH_SERVICE_URL", authService.URL)
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", authService.URL)

	db := orm.MigrateTestDB(t, &orm.DocumentPublicationOperation{}, &orm.DocumentPublicationBinding{}, &orm.WorkflowHumanArtifact{}, &orm.WorkflowSessionStep{}, &orm.WorkflowEvent{}, &orm.WorkflowAttemptInputBinding{}, &orm.WorkflowRouteDecision{},
		&orm.WorkflowSession{}, &orm.WorkflowSlotRevision{},
		&orm.UserModelProvider{}, &orm.UserModelProviderGroup{},
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
	seedWriterRevision(t, db, "github-draft", "draft_document", 1, true, "ai",
		json.RawMessage(`{"schema":"text/markdown","data":"# Draft"}`))
	seedWriterRevision(t, db, "github-target", "target_document", 1, true, "ai",
		json.RawMessage(`{"schema":"target","data":{"adapter":"github","uri":"githubrepo:/acme/docs/README.md?ref=main"}}`))

	req := httptest.NewRequest(http.MethodPost,
		"/api/core/workflow-sessions/session/writer-document:write-back",
		strings.NewReader(`{"base_revision":1}`))
	req.Header.Set("X-User-Id", "user-1")
	req = mux.SetURLVars(req, map[string]string{"session_id": "session"})
	recorder := httptest.NewRecorder()
	WriteBackWriterDocument(recorder, req)

	if recorder.Code != http.StatusBadGateway || requestedProvider != "github" ||
		!strings.Contains(recorder.Body.String(), "PROVIDER_CREDENTIALS_UNAVAILABLE") {
		t.Fatalf("unexpected GitHub credential response: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestWriteBackWriterDocumentPersistsFirstMarkdownTarget(t *testing.T) {
	actions := []string{}
	service := httptest.NewServer(adaptLegacyWriterFixture(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/v1/cloud/connections/internal/chat-enabled") &&
			r.URL.Query().Get("provider") == "notion":
			_, _ = w.Write([]byte(`{"data":{"items":[{"connection_id":"notion-1","provider":"notion","owner_user_id":"user-1","status":"ACTIVE"}]}}`))
		case strings.HasSuffix(r.URL.Path, "/v1/cloud/connections/notion-1/token"):
			_, _ = w.Write([]byte(`{"data":{"connection_id":"notion-1","provider":"notion","status":"ACTIVE","access_token":"notion-token"}}`))
		case r.URL.Path == "/api/workflow/actions:invoke":
			var request struct {
				Action string `json:"action"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode action request: %v", err)
			}
			actions = append(actions, request.Action)
			if request.Action == "convert_document" {
				_, _ = w.Write([]byte(`{"result":{
					"provider":"notion",
					"format":"notion_blocks",
					"content":[],
					"source_document":{"document_id":"local-1"},
					"media_references":{}
				}}`))
				break
			}
			_, _ = w.Write([]byte(`{"result":{
				"success":true,
				"changed":true,
				"provider_synced":true,
				"patch_result":{"success":true},
				"persisted_document":"# Draft\n\nBody",
				"representation":"markdown",
				"provider":"notion",
				"write_result":{"doc_id":"page-1"},
				"target_document":{"adapter":"notion","doc_id":"page-1","uri":"notion:/~page/page-1"}
			}}`))
		default:
			_, _ = w.Write([]byte(`{"data":{"items":[]}}`))
		}
	})))
	t.Cleanup(service.Close)
	t.Setenv("LAZYMIND_AUTH_SERVICE_URL", service.URL)
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", service.URL)

	db := orm.MigrateTestDB(t, &orm.DocumentPublicationOperation{}, &orm.DocumentPublicationBinding{}, &orm.WorkflowHumanArtifact{}, &orm.WorkflowSessionStep{}, &orm.WorkflowEvent{}, &orm.WorkflowAttemptInputBinding{}, &orm.WorkflowRouteDecision{},
		&orm.WorkflowSession{}, &orm.WorkflowSlotRevision{}, &orm.WorkflowHumanArtifact{},
		&orm.WorkflowEvent{}, &orm.WorkflowAttemptInputBinding{}, &orm.WorkflowRouteDecision{},
		&orm.UserModelProvider{}, &orm.UserModelProviderGroup{}, &orm.UserSelectedProvider{},
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
	seedWriterRevision(t, db, "draft-1", "draft_document", 1, true, "ai",
		json.RawMessage(`{"schema":"text/markdown","data":"# Draft\n\nBody"}`))

	req := httptest.NewRequest(http.MethodPost,
		"/api/core/workflow-sessions/session/writer-document:write-back",
		strings.NewReader(`{"base_revision":1,"provider":"notion"}`))
	req.Header.Set("X-User-Id", "user-1")
	req = mux.SetURLVars(req, map[string]string{"session_id": "session"})
	recorder := httptest.NewRecorder()
	WriteBackWriterDocument(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if strings.Join(actions, ",") != "convert_document,write_document" {
		t.Fatalf("publication actions = %v, want convert then write", actions)
	}
	targetArtifact, err := loadSelectedWriterArtifact(
		context.Background(), db.DB, "session", "target_document",
	)
	if err != nil {
		t.Fatalf("load first target_document revision: %v", err)
	}
	targetValue, err := writerArtifactData(targetArtifact.Value, false)
	if err != nil {
		t.Fatalf("decode target_document artifact: %v", err)
	}
	var target map[string]any
	if err := json.Unmarshal(targetValue, &target); err != nil {
		t.Fatalf("unmarshal target_document: %v", err)
	}
	if target["adapter"] != "notion" || target["doc_id"] != "page-1" ||
		targetArtifact.Revision.ChangeSource != "provider_sync" {
		t.Fatalf("unexpected first target binding: %#v, revision=%+v", target, targetArtifact.Revision)
	}
	published, err := loadSelectedWriterArtifact(
		context.Background(), db.DB, "session", "draft_document",
	)
	if err != nil {
		t.Fatalf("load provider-confirmed draft revision: %v", err)
	}
	if published.Revision.Revision != 2 || published.Revision.ChangeSource != "provider_sync" {
		t.Fatalf("provider-confirmed draft revision = %+v", published.Revision)
	}
}

func TestWriteBackWriterDocumentReportsProviderSuccessWhenLocalDraftChanged(t *testing.T) {
	providerStarted := make(chan struct{})
	releaseProvider := make(chan struct{})
	var providerOnce sync.Once
	service := httptest.NewServer(adaptLegacyWriterFixture(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/v1/cloud/connections/internal/chat-enabled") &&
			r.URL.Query().Get("provider") == "notion":
			_, _ = w.Write([]byte(`{"data":{"items":[{"connection_id":"notion-1","provider":"notion","owner_user_id":"user-1","status":"ACTIVE"}]}}`))
		case strings.HasSuffix(r.URL.Path, "/v1/cloud/connections/notion-1/token"):
			_, _ = w.Write([]byte(`{"data":{"connection_id":"notion-1","provider":"notion","status":"ACTIVE","access_token":"notion-token"}}`))
		case r.URL.Path == "/api/workflow/actions:invoke":
			var request struct {
				Action string `json:"action"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode action request: %v", err)
				return
			}
			if request.Action == "convert_document" {
				_, _ = w.Write([]byte(`{"result":{"provider":"notion","format":"notion_blocks","content":[],"source_document":{"document_id":"local-1"},"media_references":{}}}`))
				return
			}
			providerOnce.Do(func() { close(providerStarted) })
			<-releaseProvider
			_, _ = w.Write([]byte(`{"result":{"success":true,"changed":true,"provider_synced":true,"patch_result":{"success":true},"persisted_document":"# Published","representation":"markdown","provider":"notion","write_result":{"doc_id":"page-1"},"target_document":{"adapter":"notion","doc_id":"page-1","uri":"notion:/~page/page-1"}}}`))
		default:
			_, _ = w.Write([]byte(`{"data":{"items":[]}}`))
		}
	})))
	t.Cleanup(service.Close)
	t.Setenv("LAZYMIND_AUTH_SERVICE_URL", service.URL)
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", service.URL)

	db := orm.MigrateTestDB(t, &orm.DocumentPublicationOperation{}, &orm.DocumentPublicationBinding{}, &orm.WorkflowHumanArtifact{}, &orm.WorkflowSessionStep{}, &orm.WorkflowEvent{}, &orm.WorkflowAttemptInputBinding{}, &orm.WorkflowRouteDecision{},
		&orm.WorkflowSession{}, &orm.WorkflowSlotRevision{}, &orm.WorkflowHumanArtifact{},
		&orm.UserModelProvider{}, &orm.UserModelProviderGroup{}, &orm.UserSelectedProvider{},
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
	humanID := "human-draft"
	if err := db.Create(&orm.WorkflowHumanArtifact{
		ID: humanID, SessionID: "session", Slot: "draft_document", ContentType: "json",
		Value:        json.RawMessage(`{"schema":"text/markdown","data":"# Original"}`),
		DraftVersion: 1, CreatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed human draft: %v", err)
	}
	if err := db.Create(&orm.WorkflowSlotRevision{
		ID: "revision-1", SessionID: "session", SlotID: "draft_document",
		Revision: 1, Selected: true, ChangeSource: "human", HumanArtifactID: &humanID,
		Slot: "draft_document", StepID: "write_document", Attempt: 1, CreatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed writer revision: %v", err)
	}

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/core/workflow-sessions/session/writer-document:write-back",
		strings.NewReader(`{"base_revision":1,"base_draft_version":1,"provider":"notion"}`),
	)
	req.Header.Set("X-User-Id", "user-1")
	req = mux.SetURLVars(req, map[string]string{"session_id": "session"})
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		WriteBackWriterDocument(recorder, req)
		close(done)
	}()
	<-providerStarted
	if err := db.Model(&orm.WorkflowHumanArtifact{}).
		Where("id = ? AND draft_version = ?", humanID, 1).
		Updates(map[string]any{
			"value":         json.RawMessage(`{"schema":"text/markdown","data":"# Concurrent edit"}`),
			"draft_version": 2,
		}).Error; err != nil {
		t.Fatalf("write concurrent draft: %v", err)
	}
	close(releaseProvider)
	<-done

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data struct {
			Code           string `json:"code"`
			ProviderSynced bool   `json:"provider_synced"`
			ArtifactSaved  bool   `json:"artifact_saved"`
			Retryable      bool   `json:"retryable"`
		} `json:"data"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Data.Code != "PROVIDER_SYNC_LOCAL_CONFLICT" ||
		!response.Data.ProviderSynced || response.Data.ArtifactSaved || response.Data.Retryable {
		t.Fatalf("unexpected partial-success response: %+v", response.Data)
	}
	var revisions []orm.WorkflowSlotRevision
	if err := db.Where(
		"session_id = ? AND slot_id = ?", "session", "draft_document",
	).Find(&revisions).Error; err != nil {
		t.Fatalf("load revisions: %v", err)
	}
	if len(revisions) != 1 || !revisions[0].Selected || revisions[0].HumanArtifactID == nil ||
		*revisions[0].HumanArtifactID != humanID {
		t.Fatalf("draft revisions after local conflict: %#v", revisions)
	}
	var targetRevisions int64
	if err := db.Model(&orm.WorkflowSlotRevision{}).
		Where("session_id = ? AND slot_id = ?", "session", "target_document").
		Count(&targetRevisions).Error; err != nil {
		t.Fatalf("count target revisions: %v", err)
	}
	if targetRevisions != 0 {
		t.Fatalf("target revisions after main CAS conflict = %d, want 0", targetRevisions)
	}
}

func TestWriteBackWriterDocumentReportsProviderSuccessWhenArtifactIsInUse(t *testing.T) {
	var providerCalls atomic.Int64
	var startConsumer func()
	service := httptest.NewServer(adaptLegacyWriterFixture(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/v1/cloud/connections/internal/chat-enabled") &&
			r.URL.Query().Get("provider") == "notion":
			_, _ = w.Write([]byte(`{"data":{"items":[{"connection_id":"notion-1","provider":"notion","owner_user_id":"user-1","status":"ACTIVE"}]}}`))
		case strings.HasSuffix(r.URL.Path, "/v1/cloud/connections/notion-1/token"):
			_, _ = w.Write([]byte(`{"data":{"connection_id":"notion-1","provider":"notion","status":"ACTIVE","access_token":"notion-token"}}`))
		case r.URL.Path == "/api/workflow/actions:invoke":
			providerCalls.Add(1)
			var request struct {
				Action string `json:"action"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode action request: %v", err)
				return
			}
			if request.Action == "convert_document" {
				_, _ = w.Write([]byte(`{"result":{"provider":"notion","format":"notion_blocks","content":[],"source_document":{"document_id":"local-1"},"media_references":{}}}`))
				return
			}
			startConsumer() // The consumer starts after preflight and the external call.
			_, _ = w.Write([]byte(`{"result":{"success":true,"changed":true,"provider_synced":true,"patch_result":{"success":true},"persisted_document":"# Published","representation":"markdown","provider":"notion","write_result":{"doc_id":"page-1"},"target_document":{"adapter":"notion","doc_id":"page-1","uri":"notion:/~page/page-1"}}}`))
		default:
			_, _ = w.Write([]byte(`{"data":{"items":[]}}`))
		}
	})))
	t.Cleanup(service.Close)
	t.Setenv("LAZYMIND_AUTH_SERVICE_URL", service.URL)
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", service.URL)

	db := orm.MigrateTestDB(t, &orm.DocumentPublicationOperation{}, &orm.DocumentPublicationBinding{}, &orm.WorkflowHumanArtifact{}, &orm.WorkflowSessionStep{}, &orm.WorkflowEvent{}, &orm.WorkflowAttemptInputBinding{}, &orm.WorkflowRouteDecision{},
		&orm.WorkflowSession{}, &orm.WorkflowSlotRevision{}, &orm.WorkflowHumanArtifact{},
		&orm.WorkflowSessionStep{}, &orm.WorkflowAttemptInputBinding{}, &orm.WorkflowRouteDecision{},
		&orm.WorkflowEvent{}, &orm.UserModelProvider{}, &orm.UserModelProviderGroup{},
		&orm.UserSelectedProvider{},
	)
	store.Init(db.DB, db.DB, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Now().UTC()
	if err := db.Create(&orm.WorkflowSession{
		ID: "session", ConversationID: "conversation", WorkflowID: "writer-workflow",
		Status: "completed", CreateUserID: "user-1", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	humanID := "human-draft"
	if err := db.Create(&orm.WorkflowHumanArtifact{
		ID: humanID, SessionID: "session", Slot: "draft_document", ContentType: "json",
		Value:        json.RawMessage(`{"schema":"text/markdown","data":"# Original"}`),
		DraftVersion: 1, CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowSlotRevision{
		ID: "revision-1", SessionID: "session", SlotID: "draft_document",
		Revision: 1, Selected: true, ChangeSource: "human", HumanArtifactID: &humanID,
		Slot: "draft_document", StepID: "write_document", Attempt: 1, Validity: "effective", CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	startConsumer = func() {
		if err := db.Create(&orm.WorkflowSessionStep{
			ID: "running-consumer", SessionID: "session", StepID: "consumer", Attempt: 1,
			TaskID: "consumer-task", Status: "running", Validity: "effective", CreatedAt: now, UpdatedAt: now,
		}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&orm.WorkflowAttemptInputBinding{
			ID: "running-binding", SessionID: "session", AttemptID: "running-consumer",
			MaterialID: "draft_document", MaterialRevisionID: "revision-1",
			SourceType: "artifact", CreatedAt: now,
		}).Error; err != nil {
			t.Fatal(err)
		}

	}

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/core/workflow-sessions/session/writer-document:write-back",
		strings.NewReader(`{"base_revision":1,"base_draft_version":1,"provider":"notion"}`),
	)
	req.Header.Set("X-User-Id", "user-1")
	req = mux.SetURLVars(req, map[string]string{"session_id": "session"})
	recorder := httptest.NewRecorder()
	WriteBackWriterDocument(recorder, req)

	if providerCalls.Load() < 2 {
		t.Fatalf("provider actions = %d, want convert and write before local conflict", providerCalls.Load())
	}
	var response struct {
		Data struct {
			Code           string `json:"code"`
			ProviderSynced bool   `json:"provider_synced"`
			ArtifactSaved  bool   `json:"artifact_saved"`
			Retryable      bool   `json:"retryable"`
		} `json:"data"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusConflict || response.Data.Code != "PROVIDER_SYNC_LOCAL_CONFLICT" ||
		!response.Data.ProviderSynced || response.Data.ArtifactSaved || response.Data.Retryable {
		t.Fatalf("partial success: status=%d response=%+v body=%s", recorder.Code, response.Data, recorder.Body.String())
	}
	var revisions []orm.WorkflowSlotRevision
	if err := db.Where("session_id = ? AND slot_id = ?", "session", "draft_document").Find(&revisions).Error; err != nil {
		t.Fatal(err)
	}
	if len(revisions) != 1 || !revisions[0].Selected || revisions[0].ID != "revision-1" {
		t.Fatalf("revisions after in-use conflict = %#v", revisions)
	}
	var source orm.WorkflowHumanArtifact
	if err := db.First(&source, "id = ?", humanID).Error; err != nil {
		t.Fatal(err)
	}
	var sourceValue any
	if err := json.Unmarshal(source.Value, &sourceValue); err != nil {
		t.Fatal(err)
	}
	wantSourceValue := map[string]any{"schema": "text/markdown", "data": "# Original"}
	if source.DraftVersion != 1 || !reflect.DeepEqual(sourceValue, wantSourceValue) {
		t.Fatalf("source changed after in-use conflict: %#v", source)
	}
	var artifacts, events int64
	db.Model(&orm.WorkflowHumanArtifact{}).Where("session_id = ?", "session").Count(&artifacts)
	db.Model(&orm.WorkflowEvent{}).Where("session_id = ?", "session").Count(&events)
	var session orm.WorkflowSession
	db.First(&session, "id = ?", "session")
	var consumer orm.WorkflowSessionStep
	db.First(&consumer, "id = ?", "running-consumer")
	if artifacts != 1 || events != 0 || session.StateVersion != 0 || consumer.Validity != "effective" {
		t.Fatalf("local mutation leaked: artifacts=%d events=%d state=%d consumer=%#v", artifacts, events, session.StateVersion, consumer)
	}
}

func TestWriteBackWriterDocumentRejectsInvalidDraftBaselineBeforeProviderCall(t *testing.T) {
	for _, testCase := range []struct {
		name             string
		request          string
		wantStatus       int
		wantCode         string
		seedDraftVersion int64
	}{
		{
			name: "missing", request: `{"base_revision":1,"provider":"notion"}`,
			wantStatus: http.StatusBadRequest, wantCode: "DRAFT_VERSION_REQUIRED", seedDraftVersion: 1,
		},
		{
			name: "stale", request: `{"base_revision":1,"base_draft_version":1,"provider":"notion"}`,
			wantStatus: http.StatusConflict, wantCode: "DRAFT_VERSION_CONFLICT", seedDraftVersion: 2,
		},
		{
			name: "stale_revision", request: `{"base_revision":2,"base_draft_version":1,"provider":"notion"}`,
			wantStatus: http.StatusConflict, wantCode: "REVISION_CONFLICT", seedDraftVersion: 1,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var providerCalls atomic.Int64
			service := httptest.NewServer(adaptLegacyWriterFixture(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				providerCalls.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
			})))
			t.Cleanup(service.Close)
			t.Setenv("LAZYMIND_AUTH_SERVICE_URL", service.URL)
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", service.URL)

			db := orm.MigrateTestDB(t, &orm.DocumentPublicationOperation{}, &orm.DocumentPublicationBinding{}, &orm.WorkflowHumanArtifact{}, &orm.WorkflowSessionStep{}, &orm.WorkflowEvent{}, &orm.WorkflowAttemptInputBinding{}, &orm.WorkflowRouteDecision{},
				&orm.WorkflowSession{}, &orm.WorkflowSlotRevision{}, &orm.WorkflowHumanArtifact{},
				&orm.UserModelProvider{}, &orm.UserModelProviderGroup{}, &orm.UserSelectedProvider{},
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
			humanID := "human-draft"
			if err := db.Create(&orm.WorkflowHumanArtifact{
				ID: humanID, SessionID: "session", Slot: "draft_document", ContentType: "json",
				Value:        json.RawMessage(`{"schema":"text/markdown","data":"# Draft"}`),
				DraftVersion: testCase.seedDraftVersion, CreatedAt: now,
			}).Error; err != nil {
				t.Fatalf("seed human draft: %v", err)
			}
			if err := db.Create(&orm.WorkflowSlotRevision{
				ID: "revision-1", SessionID: "session", SlotID: "draft_document",
				Revision: 1, Selected: true, ChangeSource: "human", HumanArtifactID: &humanID,
				Slot: "draft_document", StepID: "write_document", Attempt: 1, CreatedAt: now,
			}).Error; err != nil {
				t.Fatalf("seed writer revision: %v", err)
			}

			req := httptest.NewRequest(
				http.MethodPost,
				"/api/core/workflow-sessions/session/writer-document:write-back",
				strings.NewReader(testCase.request),
			)
			req.Header.Set("X-User-Id", "user-1")
			req = mux.SetURLVars(req, map[string]string{"session_id": "session"})
			recorder := httptest.NewRecorder()
			WriteBackWriterDocument(recorder, req)

			if recorder.Code != testCase.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, testCase.wantStatus, recorder.Body.String())
			}
			var response struct {
				Data struct {
					Code string `json:"code"`
				} `json:"data"`
			}
			if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if response.Data.Code != testCase.wantCode {
				t.Fatalf("code = %q, want %q", response.Data.Code, testCase.wantCode)
			}
			if calls := providerCalls.Load(); calls != 0 {
				t.Fatalf("provider calls = %d, want 0", calls)
			}
		})
	}
}

func TestRenderWriterDocumentKeepsIRCanonicalForPinnedWorkflow(t *testing.T) {
	chatService := httptest.NewServer(adaptLegacyWriterFixture(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	})))
	t.Cleanup(chatService.Close)
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", chatService.URL)

	db := orm.MigrateTestDB(t, &orm.DocumentPublicationOperation{}, &orm.DocumentPublicationBinding{}, &orm.WorkflowHumanArtifact{}, &orm.WorkflowSessionStep{}, &orm.WorkflowEvent{}, &orm.WorkflowAttemptInputBinding{}, &orm.WorkflowRouteDecision{},
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

func TestRenderWriterDocumentUnwrapsMarkdownTextArtifact(t *testing.T) {
	chatService := httptest.NewServer(adaptLegacyWriterFixture(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Artifact json.RawMessage `json:"artifact"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode render request: %v", err)
		}
		var artifact struct {
			Schema string `json:"schema"`
			Data   string `json:"data"`
		}
		if err := json.Unmarshal(request.Artifact, &artifact); err != nil {
			t.Fatalf("decode render artifact: %v", err)
		}
		if artifact.Schema != "text/markdown" || artifact.Data != "# Source\n\n![diagram](images/diagram.png)" {
			t.Fatalf("render artifact = %+v, want standard Markdown envelope", artifact)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": map[string]any{
				"title":          "Source",
				"representation": "markdown",
				"document":       "# Source\n\n![diagram](images/diagram.png)",
			},
		})
	})))
	t.Cleanup(chatService.Close)
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", chatService.URL)

	db := orm.MigrateTestDB(t,
		&orm.DocumentPublicationOperation{}, &orm.DocumentPublicationBinding{},
		&orm.WorkflowSession{}, &orm.WorkflowSlotRevision{}, &orm.WorkflowHumanArtifact{},
		&orm.WorkflowSessionStep{}, &orm.WorkflowEvent{}, &orm.WorkflowAttemptInputBinding{},
		&orm.WorkflowRouteDecision{},
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
		ID: "source-1", SessionID: "session", Slot: "source_document", ContentType: "text/markdown",
		Value: json.RawMessage(`{"text":"# Source\n\n![diagram](images/diagram.png)"}`), CreatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed source artifact: %v", err)
	}
	humanID := "source-1"
	if err := db.Create(&orm.WorkflowSlotRevision{
		ID: "revision-1", SessionID: "session", SlotID: "source_document",
		Revision: 1, Selected: true, ChangeSource: "human", HumanArtifactID: &humanID,
		Slot: "source_document", StepID: "prepare", Attempt: 1, CreatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed source revision: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost,
		"/api/core/workflow-sessions/session/writer-document:render",
		strings.NewReader(`{"slot":"source_document"}`))
	req.Header.Set("X-User-Id", "user-1")
	req = mux.SetURLVars(req, map[string]string{"session_id": "session"})
	recorder := httptest.NewRecorder()
	RenderWriterDocument(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestWriterRenderArtifactPreservesSupportedEnvelopes(t *testing.T) {
	for name, value := range map[string]json.RawMessage{
		"standard": json.RawMessage(`{"schema":"text/markdown","data":"# Draft","meta":{"provider":"github"}}`),
		"path":     json.RawMessage(`{"path":"/tmp/source.md","filename":"source.md"}`),
		"ir":       json.RawMessage(`{"document_id":"doc-1","blocks":[]}`),
	} {
		t.Run(name, func(t *testing.T) {
			if got := writerRenderArtifact(value); string(got) != string(value) {
				t.Fatalf("writerRenderArtifact() = %s, want unchanged %s", got, value)
			}
		})
	}
}

func TestSaveWriterDocumentDraftUpdatesInPlaceAndCheckpointCreatesRevision(t *testing.T) {
	receivedNormalizedIR := false
	chatService := httptest.NewServer(adaptLegacyWriterFixture(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(request.Artifact, &edited); err != nil {
			t.Errorf("decode edited artifact: %v", err)
			http.Error(w, "invalid artifact", http.StatusBadRequest)
			return
		}
		var source any
		if err := json.Unmarshal(edited.Data, &source); err != nil {
			t.Errorf("decode edited document: %v", err)
			http.Error(w, "invalid document", http.StatusBadRequest)
			return
		}
		representation := "markdown"
		if document, ok := source.(map[string]any); ok {
			representation = "ir"
			blocks, _ := document["blocks"].([]any)
			if len(blocks) > 0 {
				block, _ := blocks[0].(map[string]any)
				spans, _ := block["spans"].([]any)
				if len(spans) > 0 {
					span, _ := spans[0].(map[string]any)
					_, receivedNormalizedIR = span["style"].(map[string]any)
				}
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": map[string]any{
				"source_document": source,
				"representation":  representation,
				"document":        source,
				"numbering":       map[string]any{},
				"title":           "Draft",
			},
		})
	})))
	t.Cleanup(chatService.Close)
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", chatService.URL)

	db := orm.MigrateTestDB(t, &orm.DocumentPublicationOperation{}, &orm.DocumentPublicationBinding{}, &orm.WorkflowHumanArtifact{}, &orm.WorkflowSessionStep{}, &orm.WorkflowEvent{}, &orm.WorkflowAttemptInputBinding{}, &orm.WorkflowRouteDecision{},
		&orm.WorkflowSession{},
		&orm.WorkflowSlotRevision{},
		&orm.WorkflowHumanArtifact{},
		&orm.WorkflowAttemptInputBinding{},
		&orm.WorkflowRouteDecision{},
		&orm.WorkflowEvent{},
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

	save := func(mode string, document any, baseRevision int, baseDraftVersion int64) (int, int64) {
		body, err := json.Marshal(map[string]any{
			"base_revision":      baseRevision,
			"base_draft_version": baseDraftVersion,
			"document":           document,
			"mode":               mode,
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
				Revision     int   `json:"revision"`
				DraftVersion int64 `json:"draft_version"`
			} `json:"data"`
		}
		if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
			t.Fatalf("decode save response: %v", err)
		}
		return response.Data.Revision, response.Data.DraftVersion
	}

	if revision, draftVersion := save("draft", "# First edit", 3, 1); revision != 3 || draftVersion != 2 {
		t.Fatalf("first draft baseline = revision %d draft %d, want 3 and 2", revision, draftVersion)
	}
	if revision, draftVersion := save("draft", "# Second edit", 3, 2); revision != 3 || draftVersion != 3 {
		t.Fatalf("second draft baseline = revision %d draft %d, want 3 and 3", revision, draftVersion)
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

	if revision, draftVersion := save("checkpoint", "# Final edit", 3, 3); revision != 4 || draftVersion != 1 {
		t.Fatalf("checkpoint baseline = revision %d draft %d, want 4 and 1", revision, draftVersion)
	}
	if err := db.Model(&orm.WorkflowSlotRevision{}).
		Where("session_id = ? AND slot_id = ?", "session", "draft_document").
		Count(&revisionCount).Error; err != nil {
		t.Fatalf("count checkpoint revisions: %v", err)
	}
	if revisionCount != 2 {
		t.Fatalf("checkpoint revision count = %d, want 2", revisionCount)
	}
	staleBody := strings.NewReader(`{
		"base_revision":3,"base_draft_version":3,
		"document":"# Stale","mode":"draft"
	}`)
	staleReq := httptest.NewRequest(
		http.MethodPost,
		"/api/core/workflow-sessions/session/writer-document:save",
		staleBody,
	)
	staleReq.Header.Set("X-User-Id", "user-1")
	staleReq = mux.SetURLVars(staleReq, map[string]string{"session_id": "session"})
	staleRecorder := httptest.NewRecorder()
	SaveWriterDocument(staleRecorder, staleReq)
	if staleRecorder.Code != http.StatusConflict ||
		writerErrorCode(t, staleRecorder) != "REVISION_CONFLICT" {
		t.Fatalf("stale save: status=%d body=%s", staleRecorder.Code, staleRecorder.Body.String())
	}

	legacyIR := map[string]any{
		"document_id": "doc-1",
		"title":       "Draft",
		"blocks": []any{map[string]any{
			"node_id": "divider-1", "type": "divider", "content": "",
			"spans": []any{map[string]any{"text": "", "style": []any{}}},
		}},
	}
	if revision, draftVersion := save("draft", legacyIR, 4, 1); revision != 4 || draftVersion != 2 {
		t.Fatalf("IR draft baseline = revision %d draft %d, want 4 and 2", revision, draftVersion)
	}
	if !receivedNormalizedIR {
		t.Fatal("save forwarded legacy IR style array without normalization")
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
		"metadata":{"source":{"adapter":"notion"},"provider_metadata":{"remote":true},"block_count":1,"source_block_count":1,"semantic":"preserved"},
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
	metadata := document["metadata"].(map[string]any)
	for _, key := range []string{"source", "provider_metadata", "block_count", "source_block_count"} {
		if _, exists := metadata[key]; exists {
			t.Fatalf("provider metadata %q was not removed: %#v", key, metadata)
		}
	}
	if metadata["semantic"] != "preserved" {
		t.Fatalf("provider-neutral metadata was not preserved: %#v", metadata)
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

func TestLoadWriterWriteBackArtifact_PathMarkdownTitleSources(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", root)
	tests := []struct {
		name     string
		filename string
		content  string
		meta     map[string]string
		want     string
	}{
		{
			name:     "internal draft artifact",
			filename: "draft_document.md",
			content:  "# Fresh Document\n",
			want:     "",
		},
		{
			name:     "internal flat draft artifact",
			filename: "flat_draft_document.md",
			content:  "Content without a heading\n",
			want:     "",
		},
		{
			name:     "external markdown filename",
			filename: "uploaded-note.md",
			content:  "# Body Title\n",
			want:     "uploaded-note",
		},
		{
			name:     "explicit metadata title",
			filename: "draft_document.md",
			content:  "Content\n",
			meta:     map[string]string{"title": "Explicit Target"},
			want:     "Explicit Target",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(root, tt.name, tt.filename)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatalf("create artifact directory: %v", err)
			}
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatalf("write Markdown artifact: %v", err)
			}
			value, err := json.Marshal(map[string]any{
				"path":     path,
				"filename": tt.filename,
				"meta":     tt.meta,
			})
			if err != nil {
				t.Fatalf("marshal artifact: %v", err)
			}
			artifact, err := loadWriterWriteBackArtifact(value)
			if err != nil {
				t.Fatalf("load path Markdown: %v", err)
			}
			if artifact.Format != "markdown" || artifact.Markdown != tt.content || artifact.Title != tt.want {
				t.Fatalf("unexpected path Markdown artifact: %+v", artifact)
			}
		})
	}
}

func TestWriterGitHubSyncedMarkdownUnchanged(t *testing.T) {
	artifact := &selectedWriterArtifact{
		Revision: orm.WorkflowSlotRevision{ChangeSource: "provider_sync"},
		Value: json.RawMessage(`{
			"schema":"text/markdown",
			"data":"# Draft\n",
			"meta":{"lazymind_provider_sync":{"confirmed":true,"provider":"github"}}
		}`),
	}
	if !writerGitHubSyncedMarkdownUnchanged(artifact, "# Draft\n") {
		t.Fatal("identical GitHub provider-sync Markdown should be a no-op")
	}
	if writerGitHubSyncedMarkdownUnchanged(artifact, "# Changed\n") {
		t.Fatal("edited GitHub provider-sync Markdown must be saved")
	}
	artifact.Value = json.RawMessage(`{"meta":{"lazymind_provider_sync":{"confirmed":true,"provider":"feishu"}}}`)
	if writerGitHubSyncedMarkdownUnchanged(artifact, "# Draft\n") {
		t.Fatal("non-GitHub provider-sync Markdown must keep its existing save behavior")
	}
}

func TestLoadWriterWriteBackBaseline_UsesSourceDocumentForInitialSync(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.DocumentPublicationOperation{}, &orm.DocumentPublicationBinding{}, &orm.WorkflowHumanArtifact{}, &orm.WorkflowSessionStep{}, &orm.WorkflowEvent{}, &orm.WorkflowAttemptInputBinding{}, &orm.WorkflowRouteDecision{}, &orm.WorkflowSlotRevision{})
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
	db := orm.MigrateTestDB(t, &orm.DocumentPublicationOperation{}, &orm.DocumentPublicationBinding{}, &orm.WorkflowHumanArtifact{}, &orm.WorkflowSessionStep{}, &orm.WorkflowEvent{}, &orm.WorkflowAttemptInputBinding{}, &orm.WorkflowRouteDecision{}, &orm.WorkflowSlotRevision{})
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
