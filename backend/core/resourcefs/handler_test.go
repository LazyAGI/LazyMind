package resourcefs

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestGetUserPreferenceFileSplitsFrontmatter(t *testing.T) {
	db := newResourceFSTestDB(t)
	store.Init(db.DB, nil, nil)
	service := NewService(ServiceDeps{DB: db.DB})
	ref := ResourceRef{UserID: "u1", ResourceType: ResourceTypeUserPreference}
	fullContent := "---\nagent_persona: \"测试助手\"\npreferred_name: \"小明\"\nresponse_style: \"简洁\"\ncustom_key: \"keep\"\n---\n\n正文内容\n"
	if _, err := service.EnsureResource(context.Background(), ref, fullContent); err != nil {
		t.Fatalf("EnsureResource returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/personal-resource/user_preference:file", nil)
	req.Header.Set("X-User-Id", "u1")
	req = mux.SetURLVars(req, map[string]string{"resource_type": "user_preference"})
	rec := httptest.NewRecorder()

	GetFile(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	data := decodeOKData(t, rec.Body.Bytes())
	if got := data["content"]; got != "正文内容\n" {
		t.Fatalf("content = %#v", got)
	}
	if got := data["agent_persona"]; got != "测试助手" {
		t.Fatalf("agent_persona = %#v", got)
	}
	if got := data["preferred_name"]; got != "小明" {
		t.Fatalf("preferred_name = %#v", got)
	}
	if got := data["response_style"]; got != "简洁" {
		t.Fatalf("response_style = %#v", got)
	}
}

func TestWriteUserPreferenceDraftReplacesBodyOnly(t *testing.T) {
	db := newResourceFSTestDB(t)
	store.Init(db.DB, nil, nil)
	service := NewService(ServiceDeps{DB: db.DB})
	ref := ResourceRef{UserID: "u1", ResourceType: ResourceTypeUserPreference}
	fullContent := "---\nagent_persona: \"旧助手\"\npreferred_name: \"旧称呼\"\nresponse_style: \"旧风格\"\ncustom_key: \"keep\"\n---\n\n旧正文\n"
	state, err := service.EnsureResource(context.Background(), ref, fullContent)
	if err != nil {
		t.Fatalf("EnsureResource returned error: %v", err)
	}
	body := []byte(`{
		"content": "新正文\n",
		"agent_persona": "新助手",
		"preferred_name": "新称呼",
		"response_style": "新风格",
		"expected_draft_version": 1,
		"task_id": "frontmatter-test"
	}`)
	req := httptest.NewRequest(http.MethodPut, "/personal-resource/user_preference:file", bytes.NewReader(body))
	req.Header.Set("X-User-Id", "u1")
	req = mux.SetURLVars(req, map[string]string{"resource_type": "user_preference"})
	rec := httptest.NewRecorder()

	WriteDraft(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	draft, err := service.ReadFile(context.Background(), ReadFileRequest{Ref: ref, RefType: FileRefDraft})
	if err != nil {
		t.Fatalf("ReadFile draft returned error: %v", err)
	}
	if !strings.Contains(draft.Content, `agent_persona: "旧助手"`) ||
		!strings.Contains(draft.Content, `preferred_name: "旧称呼"`) ||
		!strings.Contains(draft.Content, `response_style: "旧风格"`) ||
		!strings.Contains(draft.Content, `custom_key: "keep"`) {
		t.Fatalf("draft content did not preserve existing frontmatter: %q", draft.Content)
	}
	if !strings.HasSuffix(draft.Content, "\n\n新正文\n") {
		t.Fatalf("draft content body = %q", draft.Content)
	}
	if draft.DraftVersion != state.DraftVersion+1 {
		t.Fatalf("draft version = %d, want %d", draft.DraftVersion, state.DraftVersion+1)
	}
}

func TestPatchUserPreferenceMetadataUpdatesHeadAndDraftWithoutRevision(t *testing.T) {
	db := newResourceFSTestDB(t)
	store.Init(db.DB, nil, nil)
	service := NewService(ServiceDeps{DB: db.DB})
	ref := ResourceRef{UserID: "u1", ResourceType: ResourceTypeUserPreference}
	ctx := context.Background()
	fullContent := "---\nagent_persona: \"旧助手\"\npreferred_name: \"旧称呼\"\nresponse_style: \"旧风格\"\ncustom_key: \"keep\"\n---\n\n旧正文\n"
	state, err := service.EnsureResource(ctx, ref, fullContent)
	if err != nil {
		t.Fatalf("EnsureResource returned error: %v", err)
	}
	if _, err := service.WriteDraft(ctx, WriteDraftRequest{
		Ref:                  ref,
		Content:              strings.Replace(fullContent, "旧正文", "草稿正文", 1),
		ExpectedDraftVersion: state.DraftVersion,
		UpdatedBy:            "u1",
	}); err != nil {
		t.Fatalf("WriteDraft returned error: %v", err)
	}
	beforeHead, err := service.ReadFile(ctx, ReadFileRequest{Ref: ref, RefType: FileRefHead})
	if err != nil {
		t.Fatalf("ReadFile head returned error: %v", err)
	}
	beforeDraft, err := service.ReadFile(ctx, ReadFileRequest{Ref: ref, RefType: FileRefDraft})
	if err != nil {
		t.Fatalf("ReadFile draft returned error: %v", err)
	}
	preview, err := service.DraftPreview(ctx, DraftPreviewRequest{Ref: ref})
	if err != nil {
		t.Fatalf("DraftPreview returned error: %v", err)
	}
	action, err := service.Action(ctx, ReviewActionRequest{
		Ref:                   ref,
		ReviewID:              preview.ReviewID,
		ExpectedReviewVersion: preview.ReviewVersion,
		UpdatedBy:             "u1",
		Items: []ReviewActionItem{{
			HunkID:   firstReviewHunkID(t, preview),
			Decision: decisionAccepted,
		}},
	})
	if err != nil {
		t.Fatalf("Action returned error: %v", err)
	}
	beforeDraft, err = service.ReadFile(ctx, ReadFileRequest{Ref: ref, RefType: FileRefDraft})
	if err != nil {
		t.Fatalf("ReadFile reviewed draft returned error: %v", err)
	}
	var beforeBatch orm.PersonalResourceReviewActionBatch
	if err := db.Take(&beforeBatch, "session_id = ?", preview.ReviewID).Error; err != nil {
		t.Fatalf("read review action batch: %v", err)
	}

	body := []byte(`{
		"auto_evo": false,
		"agent_persona": "新助手",
		"preferred_name": ""
	}`)
	req := httptest.NewRequest(http.MethodPatch, "/personal-resource/user_preference", bytes.NewReader(body))
	req.Header.Set("X-User-Id", "u1")
	req = mux.SetURLVars(req, map[string]string{"resource_type": "user_preference"})
	rec := httptest.NewRecorder()

	PatchMetadata(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	afterHead, err := service.ReadFile(ctx, ReadFileRequest{Ref: ref, RefType: FileRefHead})
	if err != nil {
		t.Fatalf("ReadFile patched head returned error: %v", err)
	}
	afterDraft, err := service.ReadFile(ctx, ReadFileRequest{Ref: ref, RefType: FileRefDraft})
	if err != nil {
		t.Fatalf("ReadFile patched draft returned error: %v", err)
	}
	for name, content := range map[string]string{"head": afterHead.Content, "draft": afterDraft.Content} {
		if !strings.Contains(content, `agent_persona: "新助手"`) ||
			!strings.Contains(content, `preferred_name: ""`) ||
			!strings.Contains(content, `response_style: "旧风格"`) ||
			!strings.Contains(content, `custom_key: "keep"`) {
			t.Fatalf("%s frontmatter was not patched selectively: %q", name, content)
		}
	}
	if !strings.HasSuffix(afterHead.Content, "\n\n旧正文\n") {
		t.Fatalf("head body changed: %q", afterHead.Content)
	}
	if !strings.HasSuffix(afterDraft.Content, "\n\n草稿正文\n") {
		t.Fatalf("draft body changed: %q", afterDraft.Content)
	}
	if afterHead.RevisionID != beforeHead.RevisionID || afterHead.RevisionNo != beforeHead.RevisionNo {
		t.Fatalf("metadata patch created a new revision: before=%#v after=%#v", beforeHead, afterHead)
	}
	if afterHead.BlobHash == beforeHead.BlobHash || afterDraft.BlobHash == beforeDraft.BlobHash {
		t.Fatalf("metadata patch must reference new blobs: head %q -> %q, draft %q -> %q", beforeHead.BlobHash, afterHead.BlobHash, beforeDraft.BlobHash, afterDraft.BlobHash)
	}
	if afterDraft.DraftVersion != beforeDraft.DraftVersion+1 {
		t.Fatalf("draft version = %d, want %d", afterDraft.DraftVersion, beforeDraft.DraftVersion+1)
	}
	oldHeadBlob, err := findBlob(ctx, db.DB, beforeHead.BlobHash)
	if err != nil {
		t.Fatalf("find old head blob: %v", err)
	}
	if string(oldHeadBlob.Content) != fullContent {
		t.Fatalf("old head blob was mutated: %q", string(oldHeadBlob.Content))
	}
	revisions, err := service.ListRevisions(ctx, ListRevisionsRequest{Ref: ref})
	if err != nil {
		t.Fatalf("ListRevisions returned error: %v", err)
	}
	if len(revisions.Items) != 1 {
		t.Fatalf("metadata patch created revisions: %#v", revisions.Items)
	}
	var resource orm.PersonalResource
	if err := db.Take(&resource, "id = ?", state.ID).Error; err != nil {
		t.Fatalf("read resource: %v", err)
	}
	if resource.AutoEvo {
		t.Fatalf("auto_evo was not updated")
	}
	var review orm.PersonalResourceReviewSession
	if err := db.Take(&review, "id = ?", preview.ReviewID).Error; err != nil {
		t.Fatalf("read review session: %v", err)
	}
	if review.Status != reviewStatusActive || review.DraftVersion != afterDraft.DraftVersion || review.DraftBlobHash != afterDraft.BlobHash {
		t.Fatalf("active review snapshot was not synchronized: %#v", review)
	}
	afterPatchPreview, err := service.DraftPreview(ctx, DraftPreviewRequest{Ref: ref})
	if err != nil {
		t.Fatalf("DraftPreview after metadata patch returned error: %v", err)
	}
	if afterPatchPreview.ReviewID != preview.ReviewID || afterPatchPreview.AcceptedCount != 1 || afterPatchPreview.PendingCount != 0 {
		t.Fatalf("metadata patch replaced the active review: before=%#v after=%#v", preview, afterPatchPreview)
	}
	var afterBatch orm.PersonalResourceReviewActionBatch
	if err := db.Take(&afterBatch, "id = ?", beforeBatch.ID).Error; err != nil {
		t.Fatalf("read patched review action batch: %v", err)
	}
	if afterBatch.BeforeDraftBlobHash == beforeBatch.BeforeDraftBlobHash || afterBatch.AfterDraftBlobHash == beforeBatch.AfterDraftBlobHash {
		t.Fatalf("review action batch still references old blobs: before=%#v after=%#v", beforeBatch, afterBatch)
	}
	if afterPatchPreview.ReviewVersion != action.ReviewVersion {
		t.Fatalf("review version changed: got %d, want %d", afterPatchPreview.ReviewVersion, action.ReviewVersion)
	}
	commit, err := service.CommitDraft(ctx, CommitDraftRequest{
		Ref:                  ref,
		ExpectedDraftVersion: afterDraft.DraftVersion,
		CreatedBy:            "u1",
	})
	if err != nil {
		t.Fatalf("CommitDraft returned error: %v", err)
	}
	if commit.RevisionNo != 2 || !strings.HasSuffix(commit.Content, "\n\n草稿正文\n") ||
		!strings.Contains(commit.Content, `agent_persona: "新助手"`) {
		t.Fatalf("commit did not promote the patched draft: %#v", commit)
	}
}

func TestPatchUserPreferenceMetadataRequiresAtLeastOneField(t *testing.T) {
	db := newResourceFSTestDB(t)
	store.Init(db.DB, nil, nil)
	req := httptest.NewRequest(http.MethodPatch, "/personal-resource/user_preference", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("X-User-Id", "u1")
	req = mux.SetURLVars(req, map[string]string{"resource_type": "user_preference"})
	rec := httptest.NewRecorder()

	PatchMetadata(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func decodeOKData(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var resp struct {
		Code int            `json:"code"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, string(body))
	}
	if resp.Code != 0 {
		t.Fatalf("response code = %d, body=%s", resp.Code, string(body))
	}
	return resp.Data
}
