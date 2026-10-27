package workflow

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lazymind/core/common/orm"
)

func TestPublicationGroupWorkersSendFixedContentAndKeepLatestDraft(t *testing.T) {
	f, input := publicationFixture(t, false)
	mustPublicationErrorNil(t, f.db.AutoMigrate(&orm.AsyncJob{}))
	mustPublicationErrorNil(t, f.db.Create(&orm.DocumentPublicationBinding{
		ID: "github-existing", SessionID: input.SessionID, SlotID: input.SlotID, ItemIndex: publicationIndex(input.ListIndex), OwnerUserID: "owner",
		BindingScope: "github", Provider: "github", ConnectionID: "fixture-github",
		TargetDocument: json.RawMessage(`{"adapter":"github","uri":"https://github.com/fixture/repo/blob/main/guide.md","meta":{"publication_revision":"github-last-success","revision":"github-last-sha"}}`),
	}).Error)
	var writes []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/document/providers":
			json.NewEncoder(w).Encode(map[string]any{"providers": []any{
				map[string]any{"id": "github", "capabilities": []string{"create", "replace"}},
				map[string]any{"id": "notion", "capabilities": []string{"create", "replace"}},
			}})
		case strings.HasSuffix(r.URL.Path, "/internal/chat-enabled"):
			provider := r.URL.Query().Get("provider")
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"items": []any{map[string]any{"connection_id": "fixture-" + provider, "provider": provider, "owner_user_id": "owner", "status": "ACTIVE"}}}})
		case strings.HasSuffix(r.URL.Path, "/token"):
			provider := "github"
			if strings.Contains(r.URL.Path, "fixture-notion") {
				provider = "notion"
			}
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"connection_id": "fixture-" + provider, "provider": provider, "status": "ACTIVE", "access_token": "fake-fixture-token"}})
		case r.URL.Path == "/api/document/actions:invoke":
			var request struct {
				Reference string         `json:"reference"`
				Artifact  map[string]any `json:"artifact"`
				Arguments map[string]any `json:"arguments"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			if request.Reference == "builtin:document.convert_document.v1" {
				json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"provider": request.Arguments["provider"], "format": "markdown", "content": request.Artifact["data"], "source_document": map[string]any{"title": "Draft", "revision": "unrelated-source-revision"}, "media_references": map[string]any{}}})
				return
			}
			converted := request.Arguments["converted_document"].(map[string]any)
			source := converted["source_document"].(map[string]any)
			if converted["provider"] == "github" {
				if source["revision"] != "github-last-success" || request.Arguments["target_document"].(map[string]any)["meta"].(map[string]any)["revision"] != "github-last-sha" {
					t.Error("update lost its own remote version preconditions")
				}
			} else if _, present := source["revision"]; present {
				t.Error("new target inherited another provider's revision")
			}
			if converted["provider"] == "notion" && (request.Arguments["title"] != nil || request.Arguments["parent_uri"] != nil || source["title"] != "Draft") {
				t.Error("default publication must retain the converted document title without overriding the provider destination")
			}
			content := converted["content"].(string)
			writes = append(writes, content)
			json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"success": true, "provider_synced": true, "provider": converted["provider"], "patch_result": map[string]any{"success": true}, "persisted_document": content, "representation": "markdown", "target_document": map[string]any{"adapter": converted["provider"], "uri": "https://example.test/fixture-document"}}})
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	t.Setenv("LAZYMIND_AUTH_SERVICE_URL", server.URL)
	request := DocumentPublicationGroupRequest{BaseRevision: input.BaseRevision, BaseDraftVersion: input.BaseDraftVersion, IdempotencyKey: "workers", Targets: []DocumentPublicationTargetInput{
		{Provider: "github", ConnectionID: "fixture-github", Title: "Guide", ParentURI: "https://github.com/fixture/repo"},
		{Provider: "notion", ConnectionID: "fixture-notion"},
	}}
	id, err := CreateDocumentPublicationGroup(t.Context(), f.db.DB, "owner", f.currentRevisionID, request)
	mustPublicationErrorNil(t, err)
	var ops []DocumentPublicationOperation
	mustPublicationErrorNil(t, f.db.Where("publication_id = ?", id).Order("provider").Find(&ops).Error)
	for i, op := range ops {
		if i == 1 {
			mustPublicationErrorNil(t, f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "rollback-current-human").Updates(map[string]any{"value": json.RawMessage(`{"schema":"text/markdown","data":"# Newer edits"}`), "draft_version": 2}).Error)
		}
		_, err := runDocumentPublicationJob(t.Context(), f.db.DB, "owner", op.ID)
		mustPublicationErrorNil(t, err)
		requirePublicationStatus(t, f, op.ID, "succeeded")
	}
	if len(writes) != 2 || writes[0] != "# Draft" || writes[1] != writes[0] {
		t.Fatalf("platform contents differ: %v", writes)
	}
	var human orm.WorkflowHumanArtifact
	mustPublicationErrorNil(t, f.db.First(&human, "id = ?", "rollback-current-human").Error)
	if !strings.Contains(string(human.Value), "Newer edits") || human.DraftVersion != 2 {
		t.Fatal("worker overwrote newer local draft")
	}
	for _, op := range ops {
		_, err := runDocumentPublicationJob(t.Context(), f.db.DB, "owner", op.ID)
		mustPublicationErrorNil(t, err)
	}
	if len(writes) != 2 {
		t.Fatal("worker replay repeated external write")
	}
}

func TestPublicationGroupAtomicIdempotentAndOwned(t *testing.T) {
	f, input := publicationFixture(t, false)
	mustPublicationErrorNil(t, f.db.AutoMigrate(&orm.AsyncJob{}))
	request := DocumentPublicationGroupRequest{BaseRevision: input.BaseRevision, BaseDraftVersion: input.BaseDraftVersion, IdempotencyKey: "confirm-one", Targets: []DocumentPublicationTargetInput{
		{Provider: "github", ConnectionID: "fixture-github", Title: "Guide", ParentURI: "https://github.com/fixture/repo/tree/main/docs"},
		{Provider: "notion", ConnectionID: "fixture-notion", Title: "Guide", ParentURI: "https://www.notion.so/fixture-page"},
	}}
	before := publicationArtifacts(t, f)
	id, err := CreateDocumentPublicationGroup(t.Context(), f.db.DB, "owner", f.currentRevisionID, request)
	mustPublicationErrorNil(t, err)
	again, err := CreateDocumentPublicationGroup(t.Context(), f.db.DB, "owner", f.currentRevisionID, request)
	mustPublicationErrorNil(t, err)
	if id != again || publicationArtifacts(t, f) != before {
		t.Fatal("confirmation changed local artifacts or duplicated group")
	}
	var operations []DocumentPublicationOperation
	mustPublicationErrorNil(t, f.db.Where("publication_id = ?", id).Find(&operations).Error)
	if len(operations) != 2 || operations[0].SourceHash != operations[1].SourceHash {
		t.Fatal("not one fixed input per confirmation")
	}
	var count int64
	mustPublicationErrorNil(t, f.db.Model(&orm.AsyncJob{}).Count(&count).Error)
	if count != 2 {
		t.Fatalf("jobs=%d", count)
	}
	state, err := ReadDocumentPublicationGroup(t.Context(), f.db.DB, "owner", f.currentRevisionID)
	mustPublicationErrorNil(t, err)
	if len(state.PreferredProviders) != 2 || state.CurrentHash != operations[0].SourceHash {
		t.Fatalf("state=%#v", state)
	}
	_, err = ReadDocumentPublicationGroup(t.Context(), f.db.DB, "stranger", f.currentRevisionID)
	requirePublicationError(t, err, "PUBLICATION_NOT_FOUND")
	_, err = CreateDocumentPublicationGroup(t.Context(), f.db.DB, "stranger", f.currentRevisionID, request)
	requirePublicationError(t, err, "PUBLICATION_NOT_FOUND")
	request.Targets = request.Targets[:1]
	_, err = CreateDocumentPublicationGroup(t.Context(), f.db.DB, "owner", f.currentRevisionID, request)
	requirePublicationError(t, err, "PUBLICATION_IDEMPOTENCY_CONFLICT")
}

func TestPublicationGroupRollsBackAllTargetsOnInvalidDestination(t *testing.T) {
	f, input := publicationFixture(t, false)
	mustPublicationErrorNil(t, f.db.AutoMigrate(&orm.AsyncJob{}))
	request := DocumentPublicationGroupRequest{BaseRevision: input.BaseRevision, BaseDraftVersion: input.BaseDraftVersion, IdempotencyKey: "invalid", Targets: []DocumentPublicationTargetInput{
		{Provider: "feishu", ConnectionID: "fixture"},
		{Provider: "github", ConnectionID: "fixture"},
	}}
	_, err := CreateDocumentPublicationGroup(t.Context(), f.db.DB, "owner", f.currentRevisionID, request)
	if err == nil {
		t.Fatal("missing destination accepted")
	}
	for _, model := range []any{&orm.AsyncJob{}, &DocumentPublicationOperation{}, &orm.DocumentPublicationBinding{}} {
		var count int64
		mustPublicationErrorNil(t, f.db.Model(model).Count(&count).Error)
		if count != 0 {
			t.Fatalf("partial confirmation left %d rows", count)
		}
	}
}

func TestPublicationGroupUsesDocumentTitleAndProviderDefaultDestination(t *testing.T) {
	for _, provider := range []string{"feishu", "notion", "wechat"} {
		t.Run(provider, func(t *testing.T) {
			f, input := publicationFixture(t, false)
			mustPublicationErrorNil(t, f.db.AutoMigrate(&orm.AsyncJob{}))
			request := DocumentPublicationGroupRequest{BaseRevision: input.BaseRevision, BaseDraftVersion: input.BaseDraftVersion, IdempotencyKey: "defaults", Targets: []DocumentPublicationTargetInput{
				{Provider: provider, ConnectionID: "fixture-account"},
			}}
			id, err := CreateDocumentPublicationGroup(t.Context(), f.db.DB, "owner", f.currentRevisionID, request)
			mustPublicationErrorNil(t, err)
			var op DocumentPublicationOperation
			mustPublicationErrorNil(t, f.db.First(&op, "publication_id = ?", id).Error)
			if op.Title != "" || op.ParentURI != "" || !strings.Contains(string(op.SourceValue), "# Draft") {
				t.Fatal("publication must pass the fixed document to the adapter without overriding its title or default destination")
			}
			var jobs int64
			mustPublicationErrorNil(t, f.db.Model(&orm.AsyncJob{}).Where("resource_id = ?", op.ID).Count(&jobs).Error)
			if jobs != 1 {
				t.Fatalf("expected one publication job, got %d", jobs)
			}
		})
	}
}

func TestPublicationGroupRetryRequiresSameContentAndDoesNotRequeueSuccess(t *testing.T) {
	f, input := publicationFixture(t, false)
	mustPublicationErrorNil(t, f.db.AutoMigrate(&orm.AsyncJob{}))
	input.PublicationID = "first"
	input.Provider = "github"
	input.AllowBound = true
	op := preparePublication(t, f, input)
	mustPublicationErrorNil(t, FailDocumentPublicationBeforeWrite(t.Context(), f.db.DB, "owner", op.ID))
	newValue := json.RawMessage(`{"schema":"text/markdown","data":"# Changed"}`)
	mustPublicationErrorNil(t, f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "rollback-current-human").Updates(map[string]any{"value": newValue, "draft_version": 2}).Error)
	draft := int64(2)
	request := DocumentPublicationGroupRequest{BaseRevision: input.BaseRevision, BaseDraftVersion: &draft, IdempotencyKey: "retry", Targets: []DocumentPublicationTargetInput{{Provider: "github", ConnectionID: "fixture", Title: "Guide", ParentURI: "https://github.com/fixture/repo", RetryOperationID: op.ID}}}
	_, err := CreateDocumentPublicationGroup(t.Context(), f.db.DB, "owner", f.currentRevisionID, request)
	if err != ErrDraftVersionConflict {
		t.Fatalf("changed content retry=%v", err)
	}
	request.Targets[0].RetryOperationID = ""
	request.IdempotencyKey = "new"
	id, err := CreateDocumentPublicationGroup(t.Context(), f.db.DB, "owner", f.currentRevisionID, request)
	mustPublicationErrorNil(t, err)
	var next DocumentPublicationOperation
	mustPublicationErrorNil(t, f.db.First(&next, "publication_id = ?", id).Error)
	mustPublicationErrorNil(t, ClaimDocumentPublicationWrite(t.Context(), f.db.DB, "owner", next.ID))
	receipt := publicationReceipt()
	receipt.Provider = "github"
	receipt.TargetDocument = json.RawMessage(`{"adapter":"github","uri":"https://github.com/fixture/repo/blob/main/guide.md","meta":{"revision":"fixture-sha"}}`)
	mustPublicationErrorNil(t, ConfirmDocumentPublication(t.Context(), f.db.DB, "owner", next.ID, receipt))
	_, err = FinalizeDocumentPublication(t.Context(), f.db.DB, "owner", next.ID)
	mustPublicationErrorNil(t, err)
	request.IdempotencyKey = "unchanged"
	id, err = CreateDocumentPublicationGroup(t.Context(), f.db.DB, "owner", f.currentRevisionID, request)
	mustPublicationErrorNil(t, err)
	var unchanged DocumentPublicationOperation
	mustPublicationErrorNil(t, f.db.First(&unchanged, "publication_id = ?", id).Error)
	if unchanged.Status != "succeeded" || len(unchanged.SourceValue) != 0 {
		t.Fatal("unchanged content was requeued")
	}
	var count int64
	mustPublicationErrorNil(t, f.db.Model(&orm.AsyncJob{}).Where("resource_id = ?", unchanged.ID).Count(&count).Error)
	if count != 0 {
		t.Fatal("unchanged success scheduled remote write")
	}
}

func TestPublicationWorkerNeverRepeatsStartedWrite(t *testing.T) {
	f, input := publicationFixture(t, false)
	input.PublicationID = "first"
	op := preparePublication(t, f, input)
	mustPublicationErrorNil(t, ClaimDocumentPublicationWrite(t.Context(), f.db.DB, "owner", op.ID))
	_, err := runDocumentPublicationJob(t.Context(), f.db.DB, "owner", op.ID)
	mustPublicationErrorNil(t, err)
	requirePublicationStatus(t, f, op.ID, "outcome_unknown")
}
