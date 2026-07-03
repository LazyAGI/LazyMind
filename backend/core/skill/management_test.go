package skill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/evolution"
	"lazymind/core/resourcechange"
	"lazymind/core/store"
)

type generateSkillAPITestResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		DraftStatus        string `json:"draft_status"`
		DraftSourceVersion int64  `json:"draft_source_version"`
		DraftPath          string `json:"draft_path"`
		Outdated           bool   `json:"outdated"`
	} `json:"data"`
}

type draftPreviewAPITestResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		SkillID            string `json:"skill_id"`
		ReviewResultID     string `json:"review_result_id"`
		ReviewStatus       string `json:"review_status"`
		DraftStatus        string `json:"draft_status"`
		DraftSourceVersion int64  `json:"draft_source_version"`
		CurrentContent     string `json:"current_content"`
		DraftContent       string `json:"draft_content"`
		Diff               string `json:"diff"`
		Outdated           bool   `json:"outdated"`
	} `json:"data"`
}

type listSkillsAPITestResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Items []struct {
			SkillID                string   `json:"skill_id"`
			Description            string   `json:"description"`
			Category               string   `json:"category"`
			Tags                   []string `json:"tags"`
			UpdateStatus           string   `json:"update_status"`
			HasPendingReviewResult bool     `json:"has_pending_review_result"`
			ReviewStatus           string   `json:"review_status"`
			Children               []struct {
				SkillID                string   `json:"skill_id"`
				Description            string   `json:"description"`
				Tags                   []string `json:"tags"`
				UpdateStatus           string   `json:"update_status"`
				HasPendingReviewResult bool     `json:"has_pending_review_result"`
				ReviewStatus           string   `json:"review_status"`
			} `json:"children"`
		} `json:"items"`
		Page     int `json:"page"`
		PageSize int `json:"page_size"`
		Total    int `json:"total"`
	} `json:"data"`
}

type listSkillCategoriesAPITestResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Categories []string `json:"categories"`
	} `json:"data"`
}

type listSkillTagsAPITestResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Tags []string `json:"tags"`
	} `json:"data"`
}

type getSkillDetailAPITestResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		SkillID                string   `json:"skill_id"`
		Description            string   `json:"description"`
		Tags                   []string `json:"tags"`
		ParentID               string   `json:"parent_id"`
		ParentSkillID          string   `json:"parent_skill_id"`
		ParentSkillName        string   `json:"parent_skill_name"`
		UpdateStatus           string   `json:"update_status"`
		HasPendingReviewResult bool     `json:"has_pending_review_result"`
		ReviewStatus           string   `json:"review_status"`
		Children               []any    `json:"children"`
	} `json:"data"`
}

func newSkillTestDB(t *testing.T) *orm.DB {
	t.Helper()

	builtinCatalogOnce = sync.Once{}
	builtinCatalogOnce.Do(func() {
		builtinCatalog = []builtinSkill{}
		builtinCatalogErr = nil
	})

	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := orm.Connect(orm.DriverSQLite, dbPath)
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	if err := db.AutoMigrate(orm.AllModelsForDDL()...); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

func setBuiltinCatalogForTest(t *testing.T, items []builtinSkill) {
	t.Helper()
	builtinCatalog = items
	builtinCatalogErr = nil
	t.Cleanup(func() {
		builtinCatalog = []builtinSkill{}
		builtinCatalogErr = nil
	})
}

func testBuiltinSkill(uid, category, name string) builtinSkill {
	return builtinSkill{
		UID:         uid,
		Category:    category,
		Name:        name,
		Description: "Builtin skill for tests",
		Content: fmt.Sprintf(
			"---\nname: %s\ncategory: %s\ndescription: Builtin skill for tests\n---\n# %s\n\nBuiltin body.",
			name,
			category,
			name,
		),
		Children: []builtinSkillFile{
			{
				Name:         "guide",
				Description:  "guide.md",
				RelativePath: "guide.md",
				FileExt:      "md",
				Content:      "Builtin guide.",
			},
		},
	}
}

func createSkillPatchReviewResult(t *testing.T, db *orm.DB, id, userID, skillName, content string, at time.Time) {
	t.Helper()
	if err := db.Create(&orm.SkillReviewResult{
		ID:           id,
		SkillName:    skillName,
		Type:         "patch",
		ReviewStatus: "pending",
		UserID:       userID,
		SkillContent: content,
		Time:         at,
	}).Error; err != nil {
		t.Fatalf("create skill review result: %v", err)
	}
}

func skillReviewResultStatus(t *testing.T, db *orm.DB, id string) string {
	t.Helper()
	var row orm.SkillReviewResult
	if err := db.Select("review_status").Where("id = ?", id).Take(&row).Error; err != nil {
		t.Fatalf("query skill review result %s: %v", id, err)
	}
	return row.ReviewStatus
}

func TestInternalCreateCreatesSkillDirectly(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now()
	conversation := orm.Conversation{
		ID:        "conv-create",
		ChannelID: "default",
		BaseModel: orm.BaseModel{
			CreateUserID:   "u1",
			CreateUserName: "User 1",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	content := "---\nname: release-check\ndescription: Release checklist\n---\n# Release Checklist\n\n1. Run tests.\n2. Verify rollback plan.\n"
	body, err := json.Marshal(map[string]string{
		"session_id": "conv-create_1",
		"category":   "coding",
		"skill_name": "release-check",
		"content":    content,
	})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/skill/create", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	Create(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp getSkillDetailAPITestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("expected code 0, got %d message=%s", resp.Code, resp.Message)
	}
	if strings.TrimSpace(resp.Data.SkillID) == "" {
		t.Fatalf("expected created skill_id in response")
	}

	var suggestionCount int64
	if err := db.Model(&orm.ResourceSuggestion{}).Count(&suggestionCount).Error; err != nil {
		t.Fatalf("count suggestions: %v", err)
	}
	if suggestionCount != 0 {
		t.Fatalf("expected no resource suggestions, got %d", suggestionCount)
	}

	var row orm.SkillResource
	relativePath := evolution.ParentSkillRelativePath("coding", "release-check")
	if err := db.Where("owner_user_id = ? AND relative_path = ?", "u1", relativePath).Take(&row).Error; err != nil {
		t.Fatalf("query created skill: %v", err)
	}
	if row.ID != resp.Data.SkillID {
		t.Fatalf("expected response skill_id %q to match row id %q", resp.Data.SkillID, row.ID)
	}
	if row.Description != "Release checklist" {
		t.Fatalf("expected description %q, got %q", "Release checklist", row.Description)
	}

	if row.Content != content {
		t.Fatalf("expected DB content %q, got %q", content, row.Content)
	}
	if row.ContentSize != int64(len([]byte(content))) {
		t.Fatalf("expected content_size %d, got %d", len([]byte(content)), row.ContentSize)
	}
}

func TestRemoteFSWriteConflictAndDeleteSkill(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now()
	conversation := orm.Conversation{
		ID:        "conv-remote",
		ChannelID: "default",
		BaseModel: orm.BaseModel{
			CreateUserID:   "u1",
			CreateUserName: "User 1",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	oversizedReq := httptest.NewRequest(
		http.MethodPut,
		"/remote-fs/content?path=skills/coding/oversized/SKILL.md&session_id=conv-remote_1",
		strings.NewReader(strings.Repeat("x", remoteFSMaxWriteBytes+1)),
	)
	oversizedRec := httptest.NewRecorder()
	RemoteFSWrite(oversizedRec, oversizedReq)
	if oversizedRec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected oversized write status 413, got %d body=%s", oversizedRec.Code, oversizedRec.Body.String())
	}

	content := "---\nname: remote-skill\ncategory: coding\ndescription: Remote skill.\n---\n# Remote Skill\n\nUse this remotely.\n"
	req := httptest.NewRequest(
		http.MethodPut,
		"/remote-fs/content?path=skills/coding/remote-skill/SKILL.md&session_id=conv-remote_1",
		strings.NewReader(content),
	)
	rec := httptest.NewRecorder()
	RemoteFSWrite(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected write status 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var parent orm.SkillResource
	if err := db.Where("owner_user_id = ? AND relative_path = ?", "u1", evolution.ParentSkillRelativePath("coding", "remote-skill")).Take(&parent).Error; err != nil {
		t.Fatalf("query remote skill: %v", err)
	}
	if parent.Content != content {
		t.Fatalf("expected remote skill content to be preserved, got %q", parent.Content)
	}

	dupReq := httptest.NewRequest(
		http.MethodPut,
		"/remote-fs/content?path=skills/coding/remote-skill/SKILL.md&session_id=conv-remote_1",
		strings.NewReader(content),
	)
	dupRec := httptest.NewRecorder()
	RemoteFSWrite(dupRec, dupReq)
	if dupRec.Code != http.StatusConflict {
		t.Fatalf("expected duplicate write status 409, got %d body=%s", dupRec.Code, dupRec.Body.String())
	}

	if _, err := createChildSkill(context.Background(), db.DB, "u1", "User 1", createSkillRequest{
		Name:            "rules",
		Description:     "Rules",
		Category:        "coding",
		ParentSkillName: "remote-skill",
		Content:         "Child rules",
	}); err != nil {
		t.Fatalf("create child skill: %v", err)
	}

	deleteReq := httptest.NewRequest(
		http.MethodDelete,
		"/remote-fs/path?path=skills/coding/remote-skill&recursive=true&session_id=conv-remote_1",
		nil,
	)
	deleteRec := httptest.NewRecorder()
	RemoteFSDelete(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("expected delete status 200, got %d body=%s", deleteRec.Code, deleteRec.Body.String())
	}

	var remaining int64
	if err := db.Model(&orm.SkillResource{}).Where("owner_user_id = ? AND category = ?", "u1", "coding").Count(&remaining).Error; err != nil {
		t.Fatalf("count remaining skills: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("expected parent and child to be deleted, got %d rows", remaining)
	}
}

func TestDeleteManagedStillRequiresUserHeader(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	req := httptest.NewRequest(http.MethodDelete, "/api/core/skills/skill-1", nil)
	req = mux.SetURLVars(req, map[string]string{"skill_id": "skill-1"})
	rec := httptest.NewRecorder()

	DeleteManaged(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Message != "X-User-Id is required" {
		t.Fatalf("expected missing user header error, got %q", resp.Message)
	}
}

func TestGenerateReturnsOutdatedWhenApprovedSuggestionSnapshotIsStale(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat/rewrite" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"content": "---\nname: git-workflow\ndescription: git workflow\n---\nupdated body",
			},
		})
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("listener unavailable in current test environment: %v", err)
	}
	algoServer := &http.Server{Handler: handler}
	go func() {
		_ = algoServer.Serve(listener)
	}()
	defer func() {
		_ = algoServer.Shutdown(context.Background())
	}()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", fmt.Sprintf("http://%s", listener.Addr().String()))

	relativePath := evolution.ParentSkillRelativePath("coding", "git-workflow")
	currentContent := "---\nname: git-workflow\ndescription: git workflow\n---\ncurrent body"

	now := time.Now()
	skillRow := orm.SkillResource{
		ID:              "skill-1",
		OwnerUserID:     "u1",
		OwnerUserName:   "User 1",
		Category:        "coding",
		ParentSkillName: "git-workflow",
		SkillName:       "git-workflow",
		NodeType:        evolution.SkillNodeTypeParent,
		FileExt:         "md",
		RelativePath:    relativePath,
		Content:         currentContent,
		ContentSize:     int64(len([]byte(currentContent))),
		MimeType:        "text/markdown; charset=utf-8",
		ContentHash:     evolution.HashContent(currentContent),
		Version:         1,
		DraftContent:    "old draft body",
		DraftStatus:     "pending_confirm",
		IsEnabled:       true,
		UpdateStatus:    evolution.UpdateStatusUpToDate,
		CreateUserID:    "u1",
		CreateUserName:  "User 1",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := db.Create(&skillRow).Error; err != nil {
		t.Fatalf("create skill: %v", err)
	}

	suggestion := orm.ResourceSuggestion{
		ID:              "suggestion-1",
		UserID:          "u1",
		ResourceType:    evolution.ResourceTypeSkill,
		ResourceKey:     skillRow.ID,
		Category:        "coding",
		ParentSkillName: "git-workflow",
		SkillName:       "git-workflow",
		FileExt:         "md",
		RelativePath:    relativePath,
		Action:          "modify",
		SessionID:       "session-1",
		SnapshotHash:    evolution.HashContent("older body"),
		Title:           "update workflow",
		Content:         "update skill body",
		Status:          "accepted",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := db.Create(&suggestion).Error; err != nil {
		t.Fatalf("create suggestion: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/core/skills/skill-1:generate", strings.NewReader(`{"user_instruct":"请生成新版"}`))
	req = mux.SetURLVars(req, map[string]string{"skill_id": "skill-1"})
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()

	Generate(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp generateSkillAPITestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("expected code 0, got %d message=%s", resp.Code, resp.Message)
	}
	if resp.Data.DraftStatus != "pending_confirm" {
		t.Fatalf("expected pending_confirm draft status, got %q", resp.Data.DraftStatus)
	}
	if resp.Data.Outdated {
		t.Fatalf("expected outdated=false when generate no longer consumes suggestions")
	}
	var updatedSkill orm.SkillResource
	if err := db.Where("id = ?", "skill-1").Take(&updatedSkill).Error; err != nil {
		t.Fatalf("query updated skill: %v", err)
	}
	if !strings.Contains(updatedSkill.DraftContent, "updated body") {
		t.Fatalf("expected draft_content to be overwritten, got %q", updatedSkill.DraftContent)
	}
	if updatedSkill.UpdateStatus != evolution.UpdateStatusUpToDate {
		t.Fatalf("expected update_status to stay up_to_date after draft generate, got %q", updatedSkill.UpdateStatus)
	}
}

func TestUpdateParentAutoEvoDiscardsPendingDraftWithoutOverwritingSkillContent(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now()
	relativePath := evolution.ParentSkillRelativePath("coding", "git-workflow")
	currentContent := "---\nname: git-workflow\ndescription: git workflow\n---\ncurrent body"
	draftContent := "---\nname: git-workflow\ndescription: git workflow\n---\ndraft body"
	row := orm.SkillResource{
		ID:                 "skill-1",
		OwnerUserID:        "u1",
		OwnerUserName:      "User 1",
		Category:           "coding",
		SkillName:          "git-workflow",
		NodeType:           evolution.SkillNodeTypeParent,
		Description:        "git workflow",
		FileExt:            "md",
		RelativePath:       relativePath,
		Content:            currentContent,
		ContentSize:        int64(len([]byte(currentContent))),
		MimeType:           "text/markdown; charset=utf-8",
		ContentHash:        evolution.HashContent(currentContent),
		Version:            3,
		DraftContent:       draftContent,
		DraftSourceVersion: 3,
		DraftStatus:        "pending_confirm",
		AutoEvo:            false,
		Ext:                evolution.WithDraftSuggestionIDs(nil, []string{"suggestion-1"}),
		IsEnabled:          true,
		UpdateStatus:       "pending_confirm",
		CreateUserID:       "u1",
		CreateUserName:     "User 1",
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create skill: %v", err)
	}

	req := mux.SetURLVars(
		httptest.NewRequest(http.MethodPatch, "/api/core/skills/skill-1", strings.NewReader(`{"content":"request body should not win","auto_evo":true}`)),
		map[string]string{"skill_id": row.ID},
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", "u1")
	req.Header.Set("X-User-Name", "User 1")
	rec := httptest.NewRecorder()

	UpdateManaged(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var updated orm.SkillResource
	if err := db.Where("id = ?", row.ID).Take(&updated).Error; err != nil {
		t.Fatalf("query updated skill: %v", err)
	}
	if updated.Content != row.Content {
		t.Fatalf("expected content to remain %q, got %q", row.Content, updated.Content)
	}
	if updated.Version != row.Version {
		t.Fatalf("expected version to remain %d, got %d", row.Version, updated.Version)
	}
	if strings.TrimSpace(updated.DraftStatus) != "" || updated.DraftContent != "" || updated.DraftSourceVersion != 0 || updated.DraftUpdatedAt != nil {
		t.Fatalf("expected draft to be discarded, got status=%q content=%q source=%d updated_at=%v", updated.DraftStatus, updated.DraftContent, updated.DraftSourceVersion, updated.DraftUpdatedAt)
	}
	if gotIDs := evolution.DraftSuggestionIDs(updated.Ext); len(gotIDs) != 0 {
		t.Fatalf("expected draft suggestion ids to be cleared, got %#v", gotIDs)
	}
	if !updated.AutoEvo {
		t.Fatalf("expected auto_evo to be enabled")
	}
	if updated.AutoEvoGeneration != row.AutoEvoGeneration+1 {
		t.Fatalf("expected auto_evo_generation to increment, got %d", updated.AutoEvoGeneration)
	}
	if updated.UpdateStatus != evolution.UpdateStatusUpToDate {
		t.Fatalf("expected update_status up_to_date, got %q", updated.UpdateStatus)
	}
}

func TestGenerateAllowsUserInstructWithoutSuggestions(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	var algoBody map[string]any
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat/rewrite" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&algoBody); err != nil {
			t.Fatalf("decode algorithm request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"content": "---\nname: git-workflow\ndescription: git workflow\n---\nupdated body",
			},
		})
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("listener unavailable in current test environment: %v", err)
	}
	algoServer := &http.Server{Handler: handler}
	go func() { _ = algoServer.Serve(listener) }()
	defer func() { _ = algoServer.Shutdown(context.Background()) }()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", fmt.Sprintf("http://%s", listener.Addr().String()))

	relativePath := evolution.ParentSkillRelativePath("coding", "git-workflow")
	currentContent := "---\nname: git-workflow\ndescription: git workflow\n---\ncurrent body"
	draftContent := "---\nname: git-workflow\ndescription: git workflow\n---\ndraft body"
	now := time.Now()
	skillRow := orm.SkillResource{
		ID:                 "skill-1",
		OwnerUserID:        "u1",
		OwnerUserName:      "User 1",
		Category:           "coding",
		SkillName:          "git-workflow",
		NodeType:           evolution.SkillNodeTypeParent,
		Description:        "git workflow",
		FileExt:            "md",
		RelativePath:       relativePath,
		Content:            currentContent,
		ContentSize:        int64(len([]byte(currentContent))),
		MimeType:           "text/markdown; charset=utf-8",
		ContentHash:        evolution.HashContent(currentContent),
		Version:            1,
		DraftContent:       draftContent,
		DraftSourceVersion: 1,
		DraftStatus:        "pending_confirm",
		Ext:                evolution.WithDraftSuggestionIDs(nil, []string{"suggestion-1"}),
		IsEnabled:          true,
		UpdateStatus:       evolution.UpdateStatusUpToDate,
		CreateUserID:       "u1",
		CreateUserName:     "User 1",
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := db.Create(&skillRow).Error; err != nil {
		t.Fatalf("create skill: %v", err)
	}
	suggestion := orm.ResourceSuggestion{
		ID:              "suggestion-1",
		UserID:          "u1",
		ResourceType:    evolution.ResourceTypeSkill,
		ResourceKey:     skillRow.ID,
		Category:        "coding",
		ParentSkillName: "git-workflow",
		SkillName:       "git-workflow",
		FileExt:         "md",
		RelativePath:    relativePath,
		Action:          "modify",
		SessionID:       "session-1",
		Title:           "update workflow",
		Content:         "update skill body",
		Status:          "accepted",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := db.Create(&suggestion).Error; err != nil {
		t.Fatalf("create suggestion: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/core/skills/skill-1:generate", strings.NewReader(`{"user_instruct":"只按用户意见生成"}`))
	req = mux.SetURLVars(req, map[string]string{"skill_id": "skill-1"})
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()

	Generate(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if algoBody["user_instruct"] != "只按用户意见生成" {
		t.Fatalf("unexpected user_instruct sent to algorithm: %#v", algoBody["user_instruct"])
	}
	if algoBody["content"] != draftContent {
		t.Fatalf("expected draft content sent to algorithm, got %#v", algoBody["content"])
	}
	if _, ok := algoBody["category"]; ok {
		t.Fatalf("category should not be sent to algorithm: %#v", algoBody["category"])
	}
	if _, ok := algoBody["skill_name"]; ok {
		t.Fatalf("skill_name should not be sent to algorithm: %#v", algoBody["skill_name"])
	}
	if algoBody["task_type"] != "skill" {
		t.Fatalf("expected skill task_type, got %#v", algoBody["task_type"])
	}
	if _, ok := algoBody["suggestions"]; ok {
		t.Fatalf("suggestions should not be sent to algorithm: %#v", algoBody["suggestions"])
	}
	var updatedSkill orm.SkillResource
	if err := db.Where("id = ?", "skill-1").Take(&updatedSkill).Error; err != nil {
		t.Fatalf("query updated skill: %v", err)
	}
	if updatedSkill.DraftStatus != "pending_confirm" {
		t.Fatalf("expected draft_status pending_confirm, got %q", updatedSkill.DraftStatus)
	}
	if updatedSkill.UpdateStatus != evolution.UpdateStatusUpToDate {
		t.Fatalf("expected update_status to stay up_to_date for instruction-only draft, got %q", updatedSkill.UpdateStatus)
	}
	gotIDs := evolution.DraftSuggestionIDs(updatedSkill.Ext)
	if len(gotIDs) != 0 {
		t.Fatalf("expected draft suggestion ids to be cleared, got %#v", gotIDs)
	}

	if updatedSkill.Content != currentContent {
		t.Fatalf("generate should not apply content before review result accept, got %q", updatedSkill.Content)
	}
}

func TestDiscardRejectsPendingSkillReviewResult(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	relativePath := evolution.ParentSkillRelativePath("coding", "git-workflow")
	currentContent := "---\nname: git-workflow\ndescription: git workflow\n---\ncurrent body"
	now := time.Now()
	skillRow := orm.SkillResource{
		ID:                 "skill-1",
		OwnerUserID:        "u1",
		OwnerUserName:      "User 1",
		Category:           "coding",
		ParentSkillName:    "git-workflow",
		SkillName:          "git-workflow",
		NodeType:           evolution.SkillNodeTypeParent,
		Description:        "git workflow",
		FileExt:            "md",
		RelativePath:       relativePath,
		Content:            currentContent,
		ContentSize:        int64(len([]byte(currentContent))),
		MimeType:           "text/markdown; charset=utf-8",
		ContentHash:        evolution.HashContent(currentContent),
		Version:            1,
		DraftContent:       "---\nname: git-workflow\ndescription: git workflow\n---\nlegacy draft body",
		DraftSourceVersion: 1,
		DraftStatus:        "pending_confirm",
		Ext:                evolution.WithDraftSuggestionIDs(nil, []string{"suggestion-1"}),
		IsEnabled:          true,
		UpdateStatus:       "pending_confirm",
		CreateUserID:       "u1",
		CreateUserName:     "User 1",
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := db.Create(&skillRow).Error; err != nil {
		t.Fatalf("create skill: %v", err)
	}
	createSkillPatchReviewResult(t, db, "review-discard", "u1", "git-workflow", "---\nname: git-workflow\ndescription: git workflow\n---\nresult draft body", now.Add(time.Second))

	req := httptest.NewRequest(http.MethodPost, "/api/core/skills/skill-1:discard", nil)
	req = mux.SetURLVars(req, map[string]string{"skill_id": "skill-1"})
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()

	Discard(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if status := skillReviewResultStatus(t, db, "review-discard"); status != "rejected" {
		t.Fatalf("expected review result rejected, got %q", status)
	}
	var updatedSkill orm.SkillResource
	if err := db.Where("id = ?", "skill-1").Take(&updatedSkill).Error; err != nil {
		t.Fatalf("query skill: %v", err)
	}
	if updatedSkill.Content != currentContent || updatedSkill.Version != 1 {
		t.Fatalf("discard should not change skill content/version, got content=%q version=%d", updatedSkill.Content, updatedSkill.Version)
	}
}

func TestGenerateAllowsGeneratedDescriptionChange(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat/rewrite" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"content": "---\nname: git-workflow\ndescription: expanded git workflow\n---\nupdated body",
			},
		})
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("listener unavailable in current test environment: %v", err)
	}
	algoServer := &http.Server{Handler: handler}
	go func() { _ = algoServer.Serve(listener) }()
	defer func() { _ = algoServer.Shutdown(context.Background()) }()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", fmt.Sprintf("http://%s", listener.Addr().String()))

	relativePath := evolution.ParentSkillRelativePath("coding", "git-workflow")
	currentContent := "---\nname: git-workflow\ndescription: git workflow\n---\ncurrent body"
	draftContent := "---\nname: git-workflow\ndescription: git workflow\n---\ndraft body"
	now := time.Now()
	skillRow := orm.SkillResource{
		ID:                 "skill-1",
		OwnerUserID:        "u1",
		OwnerUserName:      "User 1",
		Category:           "coding",
		SkillName:          "git-workflow",
		NodeType:           evolution.SkillNodeTypeParent,
		Description:        "git workflow",
		FileExt:            "md",
		RelativePath:       relativePath,
		Content:            currentContent,
		ContentSize:        int64(len([]byte(currentContent))),
		MimeType:           "text/markdown; charset=utf-8",
		ContentHash:        evolution.HashContent(currentContent),
		Version:            1,
		DraftContent:       draftContent,
		DraftSourceVersion: 1,
		DraftStatus:        "pending_confirm",
		IsEnabled:          true,
		UpdateStatus:       evolution.UpdateStatusUpToDate,
		CreateUserID:       "u1",
		CreateUserName:     "User 1",
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := db.Create(&skillRow).Error; err != nil {
		t.Fatalf("create skill: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/core/skills/skill-1:generate", strings.NewReader(`{"user_instruct":"扩展技能适用范围"}`))
	req = mux.SetURLVars(req, map[string]string{"skill_id": "skill-1"})
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()

	Generate(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var updatedSkill orm.SkillResource
	if err := db.Where("id = ?", "skill-1").Take(&updatedSkill).Error; err != nil {
		t.Fatalf("query updated skill: %v", err)
	}
	if !strings.Contains(updatedSkill.DraftContent, "description: expanded git workflow") {
		t.Fatalf("expected generated description in draft_content, got %q", updatedSkill.DraftContent)
	}
	if updatedSkill.Description != "git workflow" {
		t.Fatalf("generate should not persist description before confirm, got %q", updatedSkill.Description)
	}
}

func TestConfirmPersistsDraftFrontmatterDescription(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	relativePath := evolution.ParentSkillRelativePath("coding", "git-workflow")
	currentContent := "---\nname: git-workflow\ndescription: git workflow\n---\ncurrent body"
	draftContent := "---\nname: git-workflow\ndescription: expanded git workflow\n---\nupdated body"
	now := time.Now()
	skillRow := orm.SkillResource{
		ID:                 "skill-1",
		OwnerUserID:        "u1",
		OwnerUserName:      "User 1",
		Category:           "coding",
		ParentSkillName:    "git-workflow",
		SkillName:          "git-workflow",
		NodeType:           evolution.SkillNodeTypeParent,
		Description:        "git workflow",
		FileExt:            "md",
		RelativePath:       relativePath,
		Content:            currentContent,
		ContentSize:        int64(len([]byte(currentContent))),
		MimeType:           "text/markdown; charset=utf-8",
		ContentHash:        evolution.HashContent(currentContent),
		Version:            2,
		DraftContent:       "---\nname: git-workflow\ndescription: legacy draft\n---\nlegacy body",
		DraftSourceVersion: 2,
		DraftStatus:        "pending_confirm",
		IsEnabled:          true,
		UpdateStatus:       "pending_confirm",
		CreateUserID:       "u1",
		CreateUserName:     "User 1",
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := db.Create(&skillRow).Error; err != nil {
		t.Fatalf("create skill: %v", err)
	}
	createSkillPatchReviewResult(t, db, "review-confirm", "u1", "git-workflow", draftContent, now.Add(time.Second))

	req := httptest.NewRequest(http.MethodPost, "/api/core/skills/skill-1:confirm", nil)
	req = mux.SetURLVars(req, map[string]string{"skill_id": "skill-1"})
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()

	Confirm(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var updatedSkill orm.SkillResource
	if err := db.Where("id = ?", "skill-1").Take(&updatedSkill).Error; err != nil {
		t.Fatalf("query updated skill: %v", err)
	}
	if updatedSkill.Description != "expanded git workflow" {
		t.Fatalf("expected confirmed description to persist, got %q", updatedSkill.Description)
	}
	if updatedSkill.Content != draftContent {
		t.Fatalf("expected content to be confirmed, got %q", updatedSkill.Content)
	}
	if updatedSkill.DraftStatus != "" {
		t.Fatalf("expected draft status to be cleared, got %q", updatedSkill.DraftStatus)
	}
	if status := skillReviewResultStatus(t, db, "review-confirm"); status != "accepted" {
		t.Fatalf("expected review result accepted, got %q", status)
	}
}

func TestDraftPreviewReturnsCurrentDraftAndDiff(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	relativePath := evolution.ParentSkillRelativePath("coding", "git-workflow")
	currentContent := "---\nname: git-workflow\ndescription: git workflow\n---\ncurrent body\n"
	draftContent := "---\nname: git-workflow\ndescription: git workflow\n---\nupdated body\n"

	now := time.Now()
	skillRow := orm.SkillResource{
		ID:                 "skill-1",
		OwnerUserID:        "u1",
		OwnerUserName:      "User 1",
		Category:           "coding",
		ParentSkillName:    "git-workflow",
		SkillName:          "git-workflow",
		NodeType:           evolution.SkillNodeTypeParent,
		FileExt:            "md",
		RelativePath:       relativePath,
		Content:            currentContent,
		ContentSize:        int64(len([]byte(currentContent))),
		MimeType:           "text/markdown; charset=utf-8",
		ContentHash:        evolution.HashContent(currentContent),
		Version:            2,
		DraftContent:       "---\nname: git-workflow\ndescription: legacy draft\n---\nlegacy body\n",
		DraftSourceVersion: 2,
		DraftStatus:        "pending_confirm",
		IsEnabled:          true,
		UpdateStatus:       "pending_confirm",
		CreateUserID:       "u1",
		CreateUserName:     "User 1",
		CreatedAt:          now,
		UpdatedAt:          now,
	}

	if err := db.Create(&skillRow).Error; err != nil {
		t.Fatalf("create skill: %v", err)
	}
	createSkillPatchReviewResult(t, db, "review-preview", "u1", "git-workflow", draftContent, now.Add(time.Second))

	req := httptest.NewRequest(http.MethodGet, "/api/core/skills/skill-1:draft-preview", nil)
	req = mux.SetURLVars(req, map[string]string{"skill_id": "skill-1"})
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()

	DraftPreview(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp draftPreviewAPITestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("expected code 0, got %d message=%s", resp.Code, resp.Message)
	}
	if resp.Data.SkillID != "skill-1" {
		t.Fatalf("expected skill_id skill-1, got %q", resp.Data.SkillID)
	}
	if resp.Data.ReviewResultID != "review-preview" {
		t.Fatalf("expected review_result_id review-preview, got %q", resp.Data.ReviewResultID)
	}
	if resp.Data.ReviewStatus != "pending" || resp.Data.DraftStatus != "pending" {
		t.Fatalf("expected pending review status, got review_status=%q draft_status=%q", resp.Data.ReviewStatus, resp.Data.DraftStatus)
	}
	if resp.Data.DraftSourceVersion != 2 {
		t.Fatalf("expected draft_source_version 2, got %d", resp.Data.DraftSourceVersion)
	}
	if resp.Data.CurrentContent != currentContent {
		t.Fatalf("unexpected current content: %q", resp.Data.CurrentContent)
	}
	if resp.Data.DraftContent != draftContent {
		t.Fatalf("unexpected draft content: %q", resp.Data.DraftContent)
	}
	if !strings.Contains(resp.Data.Diff, "-current body") {
		t.Fatalf("expected diff to contain removed current line, got %q", resp.Data.Diff)
	}
	if !strings.Contains(resp.Data.Diff, "+updated body") {
		t.Fatalf("expected diff to contain added draft line, got %q", resp.Data.Diff)
	}
	if resp.Data.Outdated {
		t.Fatalf("expected outdated=false")
	}
}

func TestDraftPreviewIgnoresLegacySkillResourceDraft(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now()
	currentContent := "---\nname: git-workflow\ndescription: git workflow\n---\ncurrent body"
	row := orm.SkillResource{
		ID:                 "skill-1",
		OwnerUserID:        "u1",
		OwnerUserName:      "User 1",
		Category:           "coding",
		ParentSkillName:    "git-workflow",
		SkillName:          "git-workflow",
		NodeType:           evolution.SkillNodeTypeParent,
		FileExt:            "md",
		RelativePath:       evolution.ParentSkillRelativePath("coding", "git-workflow"),
		Content:            currentContent,
		ContentHash:        evolution.HashContent(currentContent),
		Version:            2,
		DraftContent:       "---\nname: git-workflow\ndescription: legacy draft\n---\nlegacy body",
		DraftSourceVersion: 2,
		DraftStatus:        "pending_confirm",
		IsEnabled:          true,
		UpdateStatus:       "pending_confirm",
		CreateUserID:       "u1",
		CreateUserName:     "User 1",
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create skill: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/core/skills/skill-1:draft-preview", nil)
	req = mux.SetURLVars(req, map[string]string{"skill_id": "skill-1"})
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()

	DraftPreview(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected legacy resource draft to be ignored as 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestListIgnoresLegacyResourceSuggestionsForReviewButtonState(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now()
	parentWithPending := orm.SkillResource{
		ID:              "skill-parent-pending",
		OwnerUserID:     "u1",
		OwnerUserName:   "User 1",
		Category:        "coding",
		ParentSkillName: "git-workflow",
		SkillName:       "git-workflow",
		NodeType:        evolution.SkillNodeTypeParent,
		FileExt:         "md",
		RelativePath:    evolution.ParentSkillRelativePath("coding", "git-workflow"),
		ContentHash:     evolution.HashContent("content-1"),
		Version:         1,
		IsEnabled:       true,
		UpdateStatus:    evolution.UpdateStatusUpToDate,
		CreateUserID:    "u1",
		CreateUserName:  "User 1",
		CreatedAt:       now,
		UpdatedAt:       now.Add(2 * time.Second),
	}
	childWithPending := orm.SkillResource{
		ID:              "skill-child-pending",
		OwnerUserID:     "u1",
		OwnerUserName:   "User 1",
		Category:        "coding",
		ParentSkillName: "git-workflow",
		SkillName:       "rules",
		NodeType:        evolution.SkillNodeTypeChild,
		FileExt:         "md",
		RelativePath:    "coding/git-workflow/rules.md",
		ContentHash:     evolution.HashContent("child-content"),
		Version:         1,
		IsEnabled:       true,
		UpdateStatus:    evolution.UpdateStatusUpToDate,
		CreateUserID:    "u1",
		CreateUserName:  "User 1",
		CreatedAt:       now.Add(500 * time.Millisecond),
		UpdatedAt:       now.Add(2 * time.Second),
	}
	parentAcceptedOnly := orm.SkillResource{
		ID:              "skill-parent-approved",
		OwnerUserID:     "u1",
		OwnerUserName:   "User 1",
		Category:        "coding",
		ParentSkillName: "release-check",
		SkillName:       "release-check",
		NodeType:        evolution.SkillNodeTypeParent,
		FileExt:         "md",
		RelativePath:    evolution.ParentSkillRelativePath("coding", "release-check"),
		ContentHash:     evolution.HashContent("content-2"),
		Version:         1,
		IsEnabled:       true,
		UpdateStatus:    evolution.UpdateStatusUpToDate,
		CreateUserID:    "u1",
		CreateUserName:  "User 1",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := db.Create(&parentWithPending).Error; err != nil {
		t.Fatalf("create pending parent: %v", err)
	}
	if err := db.Create(&childWithPending).Error; err != nil {
		t.Fatalf("create pending child: %v", err)
	}
	if err := db.Create(&parentAcceptedOnly).Error; err != nil {
		t.Fatalf("create accepted-only parent: %v", err)
	}

	suggestions := []orm.ResourceSuggestion{
		{
			ID:              "suggestion-pending",
			UserID:          "u1",
			ResourceType:    evolution.ResourceTypeSkill,
			ResourceKey:     parentWithPending.ID,
			Category:        "coding",
			ParentSkillName: "git-workflow",
			SkillName:       "git-workflow",
			FileExt:         "md",
			RelativePath:    parentWithPending.RelativePath,
			Action:          "modify",
			SessionID:       "session-pending",
			Title:           "pending suggestion",
			Content:         "please review this change",
			Status:          "pending_review",
			CreatedAt:       now,
			UpdatedAt:       now,
		},
		{
			ID:              "suggestion-accepted",
			UserID:          "u1",
			ResourceType:    evolution.ResourceTypeSkill,
			ResourceKey:     parentAcceptedOnly.ID,
			Category:        "coding",
			ParentSkillName: "release-check",
			SkillName:       "release-check",
			FileExt:         "md",
			RelativePath:    parentAcceptedOnly.RelativePath,
			Action:          "modify",
			SessionID:       "session-accepted",
			Title:           "accepted suggestion",
			Content:         "already reviewed",
			Status:          "accepted",
			CreatedAt:       now,
			UpdatedAt:       now,
		},
	}
	if err := db.Create(&suggestions).Error; err != nil {
		t.Fatalf("create suggestions: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/core/skills?page=1&page_size=20", nil)
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()

	List(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp listSkillsAPITestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("expected code 0, got %d message=%s", resp.Code, resp.Message)
	}
	itemsByID := make(map[string]struct {
		hasPending   bool
		reviewStatus string
		children     map[string]struct {
			hasPending   bool
			reviewStatus string
		}
	}, len(resp.Data.Items))
	for _, item := range resp.Data.Items {
		childMap := make(map[string]struct {
			hasPending   bool
			reviewStatus string
		}, len(item.Children))
		for _, child := range item.Children {
			childMap[child.SkillID] = struct {
				hasPending   bool
				reviewStatus string
			}{
				hasPending:   child.HasPendingReviewResult,
				reviewStatus: child.ReviewStatus,
			}
		}
		itemsByID[item.SkillID] = struct {
			hasPending   bool
			reviewStatus string
			children     map[string]struct {
				hasPending   bool
				reviewStatus string
			}
		}{
			hasPending:   item.HasPendingReviewResult,
			reviewStatus: item.ReviewStatus,
			children:     childMap,
		}
	}
	if _, ok := itemsByID[parentWithPending.ID]; !ok {
		t.Fatalf("expected parent %q in list", parentWithPending.ID)
	}
	if _, ok := itemsByID[parentAcceptedOnly.ID]; !ok {
		t.Fatalf("expected parent %q in list", parentAcceptedOnly.ID)
	}

	if itemsByID[parentWithPending.ID].hasPending {
		t.Fatalf("expected parent with legacy pending suggestion not to be marked")
	}
	if itemsByID[parentWithPending.ID].reviewStatus != reviewStatusNone {
		t.Fatalf("expected parent review_status none, got %q", itemsByID[parentWithPending.ID].reviewStatus)
	}
	if itemsByID[parentWithPending.ID].children[childWithPending.ID].hasPending {
		t.Fatalf("expected child not to inherit legacy parent pending suggestion mark")
	}
	if itemsByID[parentWithPending.ID].children[childWithPending.ID].reviewStatus != reviewStatusNone {
		t.Fatalf("expected child review_status none, got %q", itemsByID[parentWithPending.ID].children[childWithPending.ID].reviewStatus)
	}
	if itemsByID[parentAcceptedOnly.ID].hasPending {
		t.Fatalf("expected accepted-only parent not to be marked as pending")
	}
	if itemsByID[parentAcceptedOnly.ID].reviewStatus != reviewStatusNone {
		t.Fatalf("expected accepted-only parent review_status none, got %q", itemsByID[parentAcceptedOnly.ID].reviewStatus)
	}
}

func TestListSkillMarksPendingPatchReviewResult(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now()
	parent := orm.SkillResource{
		ID:              "skill-parent-review-result",
		OwnerUserID:     "u1",
		OwnerUserName:   "User 1",
		Category:        "coding",
		ParentSkillName: "git-workflow",
		SkillName:       "git-workflow",
		NodeType:        evolution.SkillNodeTypeParent,
		FileExt:         "md",
		RelativePath:    evolution.ParentSkillRelativePath("coding", "git-workflow"),
		Content:         "---\nname: git-workflow\ndescription: git workflow\n---\nbody",
		ContentHash:     evolution.HashContent("body"),
		Version:         1,
		IsEnabled:       true,
		UpdateStatus:    evolution.UpdateStatusUpToDate,
		CreateUserID:    "u1",
		CreateUserName:  "User 1",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	child := orm.SkillResource{
		ID:              "skill-child-review-result",
		OwnerUserID:     "u1",
		OwnerUserName:   "User 1",
		Category:        "coding",
		ParentSkillName: "git-workflow",
		SkillName:       "rules",
		NodeType:        evolution.SkillNodeTypeChild,
		FileExt:         "md",
		RelativePath:    "coding/git-workflow/rules.md",
		Content:         "child body",
		ContentHash:     evolution.HashContent("child body"),
		Version:         1,
		IsEnabled:       true,
		UpdateStatus:    evolution.UpdateStatusUpToDate,
		CreateUserID:    "u1",
		CreateUserName:  "User 1",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := db.Create(&parent).Error; err != nil {
		t.Fatalf("create parent: %v", err)
	}
	if err := db.Create(&child).Error; err != nil {
		t.Fatalf("create child: %v", err)
	}
	if err := db.Create(&orm.SkillReviewResult{
		ID:           "review-result-pending",
		SkillName:    "git-workflow",
		Type:         "patch",
		ReviewStatus: "pending",
		UserID:       "u1",
		SkillContent: "---\nname: git-workflow\ndescription: git workflow\n---\nupdated body",
		Time:         now,
	}).Error; err != nil {
		t.Fatalf("create review result: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/core/skills?page=1&page_size=20", nil)
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()

	List(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp listSkillsAPITestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("expected code 0, got %d message=%s", resp.Code, resp.Message)
	}
	var item struct {
		SkillID                string   `json:"skill_id"`
		Description            string   `json:"description"`
		Category               string   `json:"category"`
		Tags                   []string `json:"tags"`
		UpdateStatus           string   `json:"update_status"`
		HasPendingReviewResult bool     `json:"has_pending_review_result"`
		ReviewStatus           string   `json:"review_status"`
		Children               []struct {
			SkillID                string   `json:"skill_id"`
			Description            string   `json:"description"`
			Tags                   []string `json:"tags"`
			UpdateStatus           string   `json:"update_status"`
			HasPendingReviewResult bool     `json:"has_pending_review_result"`
			ReviewStatus           string   `json:"review_status"`
		} `json:"children"`
	}
	for _, candidate := range resp.Data.Items {
		if candidate.SkillID == parent.ID {
			item = candidate
			break
		}
	}
	if item.SkillID == "" {
		t.Fatalf("expected parent %q in list, got %#v", parent.ID, resp.Data.Items)
	}
	if !item.HasPendingReviewResult {
		t.Fatalf("expected parent to be marked by pending review result")
	}
	if item.ReviewStatus != reviewStatusPending {
		t.Fatalf("expected parent review_status pending, got %q", item.ReviewStatus)
	}
	if len(item.Children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(item.Children))
	}
	if item.Children[0].HasPendingReviewResult {
		t.Fatalf("expected child not to inherit pending review result")
	}
	if item.Children[0].ReviewStatus != reviewStatusNone {
		t.Fatalf("expected child review_status none, got %q", item.Children[0].ReviewStatus)
	}
}

func TestListPaginatesAndCountsParentSkills(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now()
	parents := []orm.SkillResource{
		{
			ID:              "skill-parent-one",
			OwnerUserID:     "u1",
			OwnerUserName:   "User 1",
			Category:        "coding",
			ParentSkillName: "workflow",
			SkillName:       "workflow",
			NodeType:        evolution.SkillNodeTypeParent,
			FileExt:         "md",
			RelativePath:    evolution.ParentSkillRelativePath("coding", "workflow"),
			ContentHash:     evolution.HashContent("content-1"),
			Version:         1,
			IsEnabled:       true,
			UpdateStatus:    evolution.UpdateStatusUpToDate,
			CreateUserID:    "u1",
			CreateUserName:  "User 1",
			CreatedAt:       now.Add(3 * time.Second),
			UpdatedAt:       now.Add(3 * time.Second),
		},
		{
			ID:              "skill-parent-two",
			OwnerUserID:     "u1",
			OwnerUserName:   "User 1",
			Category:        "coding",
			ParentSkillName: "release",
			SkillName:       "release",
			NodeType:        evolution.SkillNodeTypeParent,
			FileExt:         "md",
			RelativePath:    evolution.ParentSkillRelativePath("coding", "release"),
			ContentHash:     evolution.HashContent("content-2"),
			Version:         1,
			IsEnabled:       true,
			UpdateStatus:    evolution.UpdateStatusUpToDate,
			CreateUserID:    "u1",
			CreateUserName:  "User 1",
			CreatedAt:       now.Add(2 * time.Second),
			UpdatedAt:       now.Add(2 * time.Second),
		},
		{
			ID:              "skill-parent-three",
			OwnerUserID:     "u1",
			OwnerUserName:   "User 1",
			Category:        "coding",
			ParentSkillName: "deploy",
			SkillName:       "deploy",
			NodeType:        evolution.SkillNodeTypeParent,
			FileExt:         "md",
			RelativePath:    evolution.ParentSkillRelativePath("coding", "deploy"),
			ContentHash:     evolution.HashContent("content-3"),
			Version:         1,
			IsEnabled:       true,
			UpdateStatus:    evolution.UpdateStatusUpToDate,
			CreateUserID:    "u1",
			CreateUserName:  "User 1",
			CreatedAt:       now,
			UpdatedAt:       now.Add(time.Second),
		},
	}
	children := []orm.SkillResource{
		{
			ID:              "skill-child-one",
			OwnerUserID:     "u1",
			OwnerUserName:   "User 1",
			Category:        "coding",
			ParentSkillName: "workflow",
			SkillName:       "rules",
			NodeType:        evolution.SkillNodeTypeChild,
			FileExt:         "md",
			RelativePath:    "coding/workflow/rules.md",
			ContentHash:     evolution.HashContent("child-content-1"),
			Version:         1,
			IsEnabled:       true,
			UpdateStatus:    evolution.UpdateStatusUpToDate,
			CreateUserID:    "u1",
			CreateUserName:  "User 1",
			CreatedAt:       now.Add(100 * time.Millisecond),
			UpdatedAt:       now,
		},
		{
			ID:              "skill-child-two",
			OwnerUserID:     "u1",
			OwnerUserName:   "User 1",
			Category:        "coding",
			ParentSkillName: "workflow",
			SkillName:       "examples",
			NodeType:        evolution.SkillNodeTypeChild,
			FileExt:         "md",
			RelativePath:    "coding/workflow/examples.md",
			ContentHash:     evolution.HashContent("child-content-2"),
			Version:         1,
			IsEnabled:       true,
			UpdateStatus:    evolution.UpdateStatusUpToDate,
			CreateUserID:    "u1",
			CreateUserName:  "User 1",
			CreatedAt:       now.Add(200 * time.Millisecond),
			UpdatedAt:       now,
		},
	}
	if err := db.Create(&parents).Error; err != nil {
		t.Fatalf("create parents: %v", err)
	}
	if err := db.Create(&children).Error; err != nil {
		t.Fatalf("create children: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/core/skills?page=1&page_size=2", nil)
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()

	List(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp listSkillsAPITestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("expected code 0, got %d message=%s", resp.Code, resp.Message)
	}
	if resp.Data.Total != 3 {
		t.Fatalf("expected total 3, got %d", resp.Data.Total)
	}
	if resp.Data.PageSize != 2 {
		t.Fatalf("expected page_size 2, got %d", resp.Data.PageSize)
	}
	if len(resp.Data.Items) != 2 {
		t.Fatalf("expected first page to include 2 parent skills, got %d", len(resp.Data.Items))
	}
	if resp.Data.Items[0].SkillID != "skill-parent-one" || len(resp.Data.Items[0].Children) != 2 {
		t.Fatalf("expected first parent with 2 children, got %#v", resp.Data.Items[0])
	}
	if resp.Data.Items[1].SkillID != "skill-parent-two" {
		t.Fatalf("expected second parent on first page, got %q", resp.Data.Items[1].SkillID)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/core/skills?page=2&page_size=2", nil)
	req.Header.Set("X-User-Id", "u1")
	rec = httptest.NewRecorder()

	List(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode second response: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("expected code 0, got %d message=%s", resp.Code, resp.Message)
	}
	if resp.Data.Total != 3 {
		t.Fatalf("expected total 3 on second page, got %d", resp.Data.Total)
	}
	if len(resp.Data.Items) != 1 || resp.Data.Items[0].SkillID != "skill-parent-three" {
		t.Fatalf("expected second page to include third parent, got %#v", resp.Data.Items)
	}
}

func TestListFiltersSkillsByCategory(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now()
	parents := []orm.SkillResource{
		{
			ID:              "skill-parent-coding",
			OwnerUserID:     "u1",
			OwnerUserName:   "User 1",
			Category:        "coding",
			ParentSkillName: "workflow",
			SkillName:       "workflow",
			NodeType:        evolution.SkillNodeTypeParent,
			FileExt:         "md",
			RelativePath:    evolution.ParentSkillRelativePath("coding", "workflow"),
			ContentHash:     evolution.HashContent("content-1"),
			Version:         1,
			IsEnabled:       true,
			UpdateStatus:    evolution.UpdateStatusUpToDate,
			CreateUserID:    "u1",
			CreateUserName:  "User 1",
			CreatedAt:       now,
			UpdatedAt:       now.Add(time.Second),
		},
		{
			ID:              "skill-parent-writing",
			OwnerUserID:     "u1",
			OwnerUserName:   "User 1",
			Category:        "writing",
			ParentSkillName: "drafting",
			SkillName:       "drafting",
			NodeType:        evolution.SkillNodeTypeParent,
			FileExt:         "md",
			RelativePath:    evolution.ParentSkillRelativePath("writing", "drafting"),
			ContentHash:     evolution.HashContent("content-2"),
			Version:         1,
			IsEnabled:       true,
			UpdateStatus:    evolution.UpdateStatusUpToDate,
			CreateUserID:    "u1",
			CreateUserName:  "User 1",
			CreatedAt:       now,
			UpdatedAt:       now,
		},
	}
	if err := db.Create(&parents).Error; err != nil {
		t.Fatalf("create parents: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/core/skills?category=writing&page=1&page_size=20", nil)
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()

	List(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp listSkillsAPITestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("expected code 0, got %d message=%s", resp.Code, resp.Message)
	}
	if resp.Data.Total != 1 {
		t.Fatalf("expected total 1, got %d", resp.Data.Total)
	}
	if len(resp.Data.Items) != 1 || resp.Data.Items[0].SkillID != "skill-parent-writing" || resp.Data.Items[0].Category != "writing" {
		t.Fatalf("expected only writing skill, got %#v", resp.Data.Items)
	}
}

func TestListCategoriesReturnsAllSkillCategoriesForCurrentUser(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	builtinCatalog = []builtinSkill{{UID: "builtin-review", Category: "review"}}
	builtinCatalogErr = nil
	t.Cleanup(func() {
		builtinCatalog = []builtinSkill{}
		builtinCatalogErr = nil
	})

	now := time.Now()
	rows := []orm.SkillResource{
		{
			ID:              "skill-parent-one",
			OwnerUserID:     "u1",
			OwnerUserName:   "User 1",
			Category:        "coding",
			ParentSkillName: "workflow",
			SkillName:       "workflow",
			NodeType:        evolution.SkillNodeTypeParent,
			FileExt:         "md",
			RelativePath:    evolution.ParentSkillRelativePath("coding", "workflow"),
			ContentHash:     evolution.HashContent("content-1"),
			Version:         1,
			IsEnabled:       true,
			UpdateStatus:    evolution.UpdateStatusUpToDate,
			CreateUserID:    "u1",
			CreateUserName:  "User 1",
			CreatedAt:       now,
			UpdatedAt:       now,
		},
		{
			ID:              "skill-parent-two",
			OwnerUserID:     "u1",
			OwnerUserName:   "User 1",
			Category:        "writing",
			ParentSkillName: "drafting",
			SkillName:       "drafting",
			NodeType:        evolution.SkillNodeTypeParent,
			FileExt:         "md",
			RelativePath:    evolution.ParentSkillRelativePath("writing", "drafting"),
			ContentHash:     evolution.HashContent("content-2"),
			Version:         1,
			IsEnabled:       true,
			UpdateStatus:    evolution.UpdateStatusUpToDate,
			CreateUserID:    "u1",
			CreateUserName:  "User 1",
			CreatedAt:       now,
			UpdatedAt:       now,
		},
		{
			ID:              "skill-child-one",
			OwnerUserID:     "u1",
			OwnerUserName:   "User 1",
			Category:        "child-only",
			ParentSkillName: "workflow",
			SkillName:       "rules",
			NodeType:        evolution.SkillNodeTypeChild,
			FileExt:         "md",
			RelativePath:    "coding/workflow/rules.md",
			ContentHash:     evolution.HashContent("child-content"),
			Version:         1,
			IsEnabled:       true,
			UpdateStatus:    evolution.UpdateStatusUpToDate,
			CreateUserID:    "u1",
			CreateUserName:  "User 1",
			CreatedAt:       now,
			UpdatedAt:       now,
		},
		{
			ID:              "skill-other-user",
			OwnerUserID:     "u2",
			OwnerUserName:   "User 2",
			Category:        "other-user",
			ParentSkillName: "other",
			SkillName:       "other",
			NodeType:        evolution.SkillNodeTypeParent,
			FileExt:         "md",
			RelativePath:    evolution.ParentSkillRelativePath("other-user", "other"),
			ContentHash:     evolution.HashContent("content-3"),
			Version:         1,
			IsEnabled:       true,
			UpdateStatus:    evolution.UpdateStatusUpToDate,
			CreateUserID:    "u2",
			CreateUserName:  "User 2",
			CreatedAt:       now,
			UpdatedAt:       now,
		},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("create skills: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/core/skills/categories", nil)
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()

	ListCategories(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp listSkillCategoriesAPITestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("expected code 0, got %d message=%s", resp.Code, resp.Message)
	}
	want := []string{"coding", "review", "writing"}
	if !reflect.DeepEqual(resp.Data.Categories, want) {
		t.Fatalf("expected categories %#v, got %#v", want, resp.Data.Categories)
	}
}

func TestListCategoriesReturnsEmptyArray(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	req := httptest.NewRequest(http.MethodGet, "/api/core/skills/categories", nil)
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()

	ListCategories(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp listSkillCategoriesAPITestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Data.Categories == nil {
		t.Fatalf("expected empty categories array, got nil")
	}
	if len(resp.Data.Categories) != 0 {
		t.Fatalf("expected no categories, got %#v", resp.Data.Categories)
	}
}

func TestListTagsReturnsAllSkillTagsForCurrentUser(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now()
	rows := []orm.SkillResource{
		{
			ID:              "skill-parent-one",
			OwnerUserID:     "u1",
			OwnerUserName:   "User 1",
			Category:        "coding",
			ParentSkillName: "workflow",
			SkillName:       "workflow",
			NodeType:        evolution.SkillNodeTypeParent,
			Tags:            tagsJSON([]string{"UI", " 产品设计 ", "UI", ""}),
			FileExt:         "md",
			RelativePath:    evolution.ParentSkillRelativePath("coding", "workflow"),
			ContentHash:     evolution.HashContent("content-1"),
			Version:         1,
			IsEnabled:       true,
			UpdateStatus:    evolution.UpdateStatusUpToDate,
			CreateUserID:    "u1",
			CreateUserName:  "User 1",
			CreatedAt:       now,
			UpdatedAt:       now,
		},
		{
			ID:              "skill-child-one",
			OwnerUserID:     "u1",
			OwnerUserName:   "User 1",
			Category:        "coding",
			ParentSkillName: "workflow",
			SkillName:       "rules",
			NodeType:        evolution.SkillNodeTypeChild,
			Tags:            tagsJSON([]string{"规则", "UI"}),
			FileExt:         "md",
			RelativePath:    "coding/workflow/rules.md",
			ContentHash:     evolution.HashContent("child-content"),
			Version:         1,
			IsEnabled:       true,
			UpdateStatus:    evolution.UpdateStatusUpToDate,
			CreateUserID:    "u1",
			CreateUserName:  "User 1",
			CreatedAt:       now,
			UpdatedAt:       now,
		},
		{
			ID:              "skill-other-user",
			OwnerUserID:     "u2",
			OwnerUserName:   "User 2",
			Category:        "coding",
			ParentSkillName: "other",
			SkillName:       "other",
			NodeType:        evolution.SkillNodeTypeParent,
			Tags:            tagsJSON([]string{"其他用户"}),
			FileExt:         "md",
			RelativePath:    evolution.ParentSkillRelativePath("coding", "other"),
			ContentHash:     evolution.HashContent("content-2"),
			Version:         1,
			IsEnabled:       true,
			UpdateStatus:    evolution.UpdateStatusUpToDate,
			CreateUserID:    "u2",
			CreateUserName:  "User 2",
			CreatedAt:       now,
			UpdatedAt:       now,
		},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("create skills: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/core/skills/tags", nil)
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()

	ListTags(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp listSkillTagsAPITestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("expected code 0, got %d message=%s", resp.Code, resp.Message)
	}
	want := []string{"UI", "产品设计", "规则"}
	if !reflect.DeepEqual(resp.Data.Tags, want) {
		t.Fatalf("expected tags %#v, got %#v", want, resp.Data.Tags)
	}
}

func TestListTagsReturnsEmptyArray(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	req := httptest.NewRequest(http.MethodGet, "/api/core/skills/tags", nil)
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()

	ListTags(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp listSkillTagsAPITestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Data.Tags == nil {
		t.Fatalf("expected empty tags array, got nil")
	}
	if len(resp.Data.Tags) != 0 {
		t.Fatalf("expected no tags, got %#v", resp.Data.Tags)
	}
}

func TestListNormalizesPendingConfirmUpdateStatus(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now()
	parent := orm.SkillResource{
		ID:             "skill-parent",
		OwnerUserID:    "u1",
		OwnerUserName:  "User 1",
		Category:       "testing",
		SkillName:      "test-tool-verification",
		NodeType:       evolution.SkillNodeTypeParent,
		Description:    "tool verification",
		FileExt:        "md",
		RelativePath:   evolution.ParentSkillRelativePath("testing", "test-tool-verification"),
		Content:        "---\nname: test-tool-verification\ndescription: tool verification\n---\nbody",
		ContentHash:    evolution.HashContent("body"),
		Version:        1,
		IsEnabled:      true,
		DraftStatus:    "pending_confirm",
		DraftContent:   "draft body",
		UpdateStatus:   "pending_confirm",
		CreateUserID:   "u1",
		CreateUserName: "User 1",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	child := orm.SkillResource{
		ID:              "skill-child",
		OwnerUserID:     "u1",
		OwnerUserName:   "User 1",
		Category:        "testing",
		ParentSkillName: "test-tool-verification",
		SkillName:       "probe",
		NodeType:        evolution.SkillNodeTypeChild,
		Description:     "probe",
		FileExt:         "md",
		RelativePath:    evolution.ChildSkillRelativePath("testing", "test-tool-verification", "probe", "md"),
		Content:         "child body",
		ContentHash:     evolution.HashContent("child body"),
		Version:         1,
		IsEnabled:       true,
		UpdateStatus:    "pending_confirm",
		CreateUserID:    "u1",
		CreateUserName:  "User 1",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := db.Create(&parent).Error; err != nil {
		t.Fatalf("create parent: %v", err)
	}
	if err := db.Create(&child).Error; err != nil {
		t.Fatalf("create child: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/core/skills?page=1&page_size=20", nil)
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()

	List(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp listSkillsAPITestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Data.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(resp.Data.Items))
	}
	if resp.Data.Items[0].UpdateStatus != evolution.UpdateStatusUpToDate {
		t.Fatalf("expected parent update_status up_to_date, got %q", resp.Data.Items[0].UpdateStatus)
	}
	if len(resp.Data.Items[0].Children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(resp.Data.Items[0].Children))
	}
	if resp.Data.Items[0].Children[0].UpdateStatus != evolution.UpdateStatusUpToDate {
		t.Fatalf("expected child update_status up_to_date, got %q", resp.Data.Items[0].Children[0].UpdateStatus)
	}
}

func TestListIgnoresNameOnlySkillSuggestionsWithoutResourceKey(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now()
	parent := orm.SkillResource{
		ID:              "skill-parent-legacy",
		OwnerUserID:     "u1",
		OwnerUserName:   "User 1",
		Category:        "coding",
		ParentSkillName: "git-workflow",
		SkillName:       "git-workflow",
		NodeType:        evolution.SkillNodeTypeParent,
		FileExt:         "md",
		RelativePath:    evolution.ParentSkillRelativePath("coding", "git-workflow"),
		ContentHash:     evolution.HashContent("content-1"),
		Version:         1,
		IsEnabled:       true,
		UpdateStatus:    evolution.UpdateStatusUpToDate,
		CreateUserID:    "u1",
		CreateUserName:  "User 1",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	child := orm.SkillResource{
		ID:              "skill-child-legacy",
		OwnerUserID:     "u1",
		OwnerUserName:   "User 1",
		Category:        "coding",
		ParentSkillName: "git-workflow",
		SkillName:       "rules",
		NodeType:        evolution.SkillNodeTypeChild,
		FileExt:         "md",
		RelativePath:    "coding/git-workflow/rules.md",
		ContentHash:     evolution.HashContent("child-content"),
		Version:         1,
		IsEnabled:       true,
		UpdateStatus:    evolution.UpdateStatusUpToDate,
		CreateUserID:    "u1",
		CreateUserName:  "User 1",
		CreatedAt:       now.Add(500 * time.Millisecond),
		UpdatedAt:       now,
	}
	if err := db.Create(&parent).Error; err != nil {
		t.Fatalf("create parent: %v", err)
	}
	if err := db.Create(&child).Error; err != nil {
		t.Fatalf("create child: %v", err)
	}

	legacySuggestion := orm.ResourceSuggestion{
		ID:              "suggestion-legacy-pending",
		UserID:          "u1",
		ResourceType:    evolution.ResourceTypeSkill,
		ResourceKey:     "",
		Category:        "coding",
		ParentSkillName: "git-workflow",
		SkillName:       "git-workflow",
		FileExt:         "md",
		RelativePath:    "",
		Action:          "modify",
		SessionID:       "session-legacy-pending",
		Title:           "legacy pending suggestion",
		Content:         "legacy change",
		Status:          "pending_review",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := db.Create(&legacySuggestion).Error; err != nil {
		t.Fatalf("create legacy suggestion: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/core/skills?page=1&page_size=20", nil)
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()

	List(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp listSkillsAPITestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("expected code 0, got %d message=%s", resp.Code, resp.Message)
	}
	var item struct {
		SkillID                string   `json:"skill_id"`
		Description            string   `json:"description"`
		Category               string   `json:"category"`
		Tags                   []string `json:"tags"`
		UpdateStatus           string   `json:"update_status"`
		HasPendingReviewResult bool     `json:"has_pending_review_result"`
		ReviewStatus           string   `json:"review_status"`
		Children               []struct {
			SkillID                string   `json:"skill_id"`
			Description            string   `json:"description"`
			Tags                   []string `json:"tags"`
			UpdateStatus           string   `json:"update_status"`
			HasPendingReviewResult bool     `json:"has_pending_review_result"`
			ReviewStatus           string   `json:"review_status"`
		} `json:"children"`
	}
	for _, candidate := range resp.Data.Items {
		if candidate.SkillID == parent.ID {
			item = candidate
			break
		}
	}
	if item.SkillID == "" {
		t.Fatalf("expected parent %q in list, got %#v", parent.ID, resp.Data.Items)
	}
	if item.HasPendingReviewResult {
		t.Fatalf("expected parent not to be marked by name-only suggestion")
	}
	if item.ReviewStatus != reviewStatusNone {
		t.Fatalf("expected parent review_status none, got %q", item.ReviewStatus)
	}
	if len(item.Children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(item.Children))
	}
	if item.Children[0].HasPendingReviewResult {
		t.Fatalf("expected child not to inherit legacy parent pending suggestion mark")
	}
	if item.Children[0].ReviewStatus != reviewStatusNone {
		t.Fatalf("expected child review_status none, got %q", item.Children[0].ReviewStatus)
	}
}

func TestGetChildDetailDoesNotInheritPendingReviewSuggestionsFromParent(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	parentRelativePath := evolution.ParentSkillRelativePath("coding", "git-workflow")
	childRelativePath := "coding/git-workflow/rules.md"
	parentContent := "---\nname: git-workflow\ndescription: git workflow\n---\nparent body"
	childContent := "child body"

	now := time.Now()
	parent := orm.SkillResource{
		ID:              "skill-parent",
		OwnerUserID:     "u1",
		OwnerUserName:   "User 1",
		Category:        "coding",
		ParentSkillName: "git-workflow",
		SkillName:       "git-workflow",
		NodeType:        evolution.SkillNodeTypeParent,
		FileExt:         "md",
		RelativePath:    parentRelativePath,
		Content:         parentContent,
		ContentSize:     int64(len([]byte(parentContent))),
		MimeType:        "text/markdown; charset=utf-8",
		ContentHash:     evolution.HashContent(parentContent),
		Version:         1,
		IsEnabled:       true,
		UpdateStatus:    evolution.UpdateStatusUpToDate,
		CreateUserID:    "u1",
		CreateUserName:  "User 1",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	child := orm.SkillResource{
		ID:              "skill-child",
		OwnerUserID:     "u1",
		OwnerUserName:   "User 1",
		Category:        "coding",
		ParentSkillName: "git-workflow",
		SkillName:       "rules",
		NodeType:        evolution.SkillNodeTypeChild,
		FileExt:         "md",
		RelativePath:    childRelativePath,
		Content:         childContent,
		ContentSize:     int64(len([]byte(childContent))),
		MimeType:        "text/markdown; charset=utf-8",
		ContentHash:     evolution.HashContent(childContent),
		Version:         1,
		IsEnabled:       true,
		UpdateStatus:    evolution.UpdateStatusUpToDate,
		CreateUserID:    "u1",
		CreateUserName:  "User 1",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	suggestion := orm.ResourceSuggestion{
		ID:              "suggestion-pending-child-detail",
		UserID:          "u1",
		ResourceType:    evolution.ResourceTypeSkill,
		ResourceKey:     parent.ID,
		Category:        "coding",
		ParentSkillName: "git-workflow",
		SkillName:       "git-workflow",
		FileExt:         "md",
		RelativePath:    parentRelativePath,
		Action:          "modify",
		SessionID:       "session-child-detail",
		Title:           "pending suggestion",
		Content:         "please review",
		Status:          "pending_review",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := db.Create(&parent).Error; err != nil {
		t.Fatalf("create parent: %v", err)
	}
	if err := db.Create(&child).Error; err != nil {
		t.Fatalf("create child: %v", err)
	}
	if err := db.Create(&suggestion).Error; err != nil {
		t.Fatalf("create suggestion: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/core/skills/skill-child", nil)
	req = mux.SetURLVars(req, map[string]string{"skill_id": child.ID})
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()

	Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp getSkillDetailAPITestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("expected code 0, got %d message=%s", resp.Code, resp.Message)
	}
	if resp.Data.SkillID != child.ID {
		t.Fatalf("expected child skill id %q, got %q", child.ID, resp.Data.SkillID)
	}
	if resp.Data.HasPendingReviewResult {
		t.Fatalf("expected child detail not to inherit pending review suggestion flag")
	}
	if resp.Data.ReviewStatus != reviewStatusNone {
		t.Fatalf("expected child detail review_status none, got %q", resp.Data.ReviewStatus)
	}
	if len(resp.Data.Children) != 0 {
		t.Fatalf("expected child detail to have no children, got %d", len(resp.Data.Children))
	}
}

func TestGetSharedSourceSkillDetailAllowsTargetUser(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now().UTC()
	parentContent := "---\nname: release-check\ndescription: Release checklist\n---\nsource body"
	parent := orm.SkillResource{
		ID:              "skill-source",
		OwnerUserID:     "u1",
		OwnerUserName:   "User 1",
		Category:        "coding",
		ParentSkillName: "",
		SkillName:       "release-check",
		NodeType:        evolution.SkillNodeTypeParent,
		Description:     "Release checklist",
		FileExt:         "md",
		RelativePath:    evolution.ParentSkillRelativePath("coding", "release-check"),
		Content:         parentContent,
		ContentSize:     int64(len([]byte(parentContent))),
		MimeType:        "text/markdown; charset=utf-8",
		ContentHash:     evolution.HashContent(parentContent),
		Version:         1,
		IsEnabled:       true,
		UpdateStatus:    evolution.UpdateStatusUpToDate,
		CreateUserID:    "u1",
		CreateUserName:  "User 1",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	childContent := "child body"
	child := orm.SkillResource{
		ID:              "skill-source-child",
		OwnerUserID:     "u1",
		OwnerUserName:   "User 1",
		Category:        "coding",
		ParentSkillName: "release-check",
		SkillName:       "rules",
		NodeType:        evolution.SkillNodeTypeChild,
		FileExt:         "md",
		RelativePath:    "coding/release-check/rules.md",
		Content:         childContent,
		ContentSize:     int64(len([]byte(childContent))),
		MimeType:        "text/markdown; charset=utf-8",
		ContentHash:     evolution.HashContent(childContent),
		Version:         1,
		IsEnabled:       true,
		UpdateStatus:    evolution.UpdateStatusUpToDate,
		CreateUserID:    "u1",
		CreateUserName:  "User 1",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := db.Create(&[]orm.SkillResource{parent, child}).Error; err != nil {
		t.Fatalf("create source skills: %v", err)
	}
	if err := db.Create(&orm.SkillShareTask{
		ID:                    "share-task",
		SourceUserID:          "u1",
		SourceUserName:        "User 1",
		SourceSkillID:         parent.ID,
		SourceCategory:        parent.Category,
		SourceParentSkillName: parent.SkillName,
		SourceRelativeRoot:    "coding/release-check",
		Message:               "please review",
		CreatedAt:             now,
		UpdatedAt:             now,
	}).Error; err != nil {
		t.Fatalf("create share task: %v", err)
	}
	if err := db.Create(&orm.SkillShareItem{
		ID:             "share-item",
		ShareTaskID:    "share-task",
		TargetUserID:   "u2",
		TargetUserName: "User 2",
		Status:         shareStatusPendingAccept,
		CreatedAt:      now,
		UpdatedAt:      now,
	}).Error; err != nil {
		t.Fatalf("create share item: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/core/skills/skill-source", nil)
	req = mux.SetURLVars(req, map[string]string{"skill_id": parent.ID})
	req.Header.Set("X-User-Id", "u2")
	rec := httptest.NewRecorder()

	Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Code int `json:"code"`
		Data struct {
			SkillID  string `json:"skill_id"`
			Content  string `json:"content"`
			Children []struct {
				SkillID string `json:"skill_id"`
				Content string `json:"content"`
			} `json:"children"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("expected code 0, got %d", resp.Code)
	}
	if resp.Data.SkillID != parent.ID || resp.Data.Content != parentContent {
		t.Fatalf("expected shared source parent detail, got %#v", resp.Data)
	}
	if len(resp.Data.Children) != 1 || resp.Data.Children[0].SkillID != child.ID || resp.Data.Children[0].Content != childContent {
		t.Fatalf("expected shared source child detail, got %#v", resp.Data.Children)
	}

	childReq := httptest.NewRequest(http.MethodGet, "/api/core/skills/skill-source-child", nil)
	childReq = mux.SetURLVars(childReq, map[string]string{"skill_id": child.ID})
	childReq.Header.Set("X-User-Id", "u2")
	childRec := httptest.NewRecorder()

	Get(childRec, childReq)

	if childRec.Code != http.StatusOK {
		t.Fatalf("expected child status 200, got %d body=%s", childRec.Code, childRec.Body.String())
	}
	var childResp struct {
		Code int `json:"code"`
		Data struct {
			SkillID string `json:"skill_id"`
			Content string `json:"content"`
		} `json:"data"`
	}
	if err := json.Unmarshal(childRec.Body.Bytes(), &childResp); err != nil {
		t.Fatalf("decode child response: %v", err)
	}
	if childResp.Code != 0 || childResp.Data.SkillID != child.ID || childResp.Data.Content != childContent {
		t.Fatalf("expected shared source child detail, got %#v", childResp)
	}
}

func TestGetSharedSourceSkillDetailHidesFromUnsharedUser(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now().UTC()
	parentContent := "---\nname: release-check\ndescription: Release checklist\n---\nsource body"
	parent := orm.SkillResource{
		ID:           "skill-source",
		OwnerUserID:  "u1",
		Category:     "coding",
		SkillName:    "release-check",
		NodeType:     evolution.SkillNodeTypeParent,
		FileExt:      "md",
		RelativePath: evolution.ParentSkillRelativePath("coding", "release-check"),
		Content:      parentContent,
		ContentSize:  int64(len([]byte(parentContent))),
		ContentHash:  evolution.HashContent(parentContent),
		Version:      1,
		IsEnabled:    true,
		UpdateStatus: evolution.UpdateStatusUpToDate,
		CreateUserID: "u1",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := db.Create(&parent).Error; err != nil {
		t.Fatalf("create source skill: %v", err)
	}
	if err := db.Create(&orm.SkillShareTask{
		ID:            "share-task",
		SourceUserID:  "u1",
		SourceSkillID: parent.ID,
		CreatedAt:     now,
		UpdatedAt:     now,
	}).Error; err != nil {
		t.Fatalf("create share task: %v", err)
	}
	if err := db.Create(&orm.SkillShareItem{
		ID:           "share-item",
		ShareTaskID:  "share-task",
		TargetUserID: "u2",
		Status:       shareStatusPendingAccept,
		CreatedAt:    now,
		UpdatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("create share item: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/core/skills/skill-source", nil)
	req = mux.SetURLVars(req, map[string]string{"skill_id": parent.ID})
	req.Header.Set("X-User-Id", "u3")
	rec := httptest.NewRecorder()

	Get(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestGetSharedSourceSkillDetailAllowsLegacyRelativeRootMatch(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now().UTC()
	parentContent := "---\nname: release-check\ndescription: Release checklist\n---\nsource body"
	parent := orm.SkillResource{
		ID:              "skill-source-current",
		OwnerUserID:     "u1",
		OwnerUserName:   "User 1",
		Category:        "coding",
		ParentSkillName: "",
		SkillName:       "release-check",
		NodeType:        evolution.SkillNodeTypeParent,
		Description:     "Release checklist",
		FileExt:         "md",
		RelativePath:    evolution.ParentSkillRelativePath("coding", "release-check"),
		Content:         parentContent,
		ContentSize:     int64(len([]byte(parentContent))),
		MimeType:        "text/markdown; charset=utf-8",
		ContentHash:     evolution.HashContent(parentContent),
		Version:         1,
		IsEnabled:       true,
		UpdateStatus:    evolution.UpdateStatusUpToDate,
		CreateUserID:    "u1",
		CreateUserName:  "User 1",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := db.Create(&parent).Error; err != nil {
		t.Fatalf("create source skill: %v", err)
	}
	if err := db.Create(&orm.SkillShareTask{
		ID:                    "share-task",
		SourceUserID:          "u1",
		SourceUserName:        "User 1",
		SourceSkillID:         "skill-source-old",
		SourceCategory:        parent.Category,
		SourceParentSkillName: parent.SkillName,
		SourceRelativeRoot:    "coding/release-check",
		CreatedAt:             now,
		UpdatedAt:             now,
	}).Error; err != nil {
		t.Fatalf("create share task: %v", err)
	}
	if err := db.Create(&orm.SkillShareItem{
		ID:             "share-item",
		ShareTaskID:    "share-task",
		TargetUserID:   "u2",
		TargetUserName: "User 2",
		Status:         shareStatusPendingAccept,
		CreatedAt:      now,
		UpdatedAt:      now,
	}).Error; err != nil {
		t.Fatalf("create share item: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/core/skills/skill-source-current", nil)
	req = mux.SetURLVars(req, map[string]string{"skill_id": parent.ID})
	req.Header.Set("X-User-Id", "u2")
	rec := httptest.NewRecorder()

	Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			SkillID string `json:"skill_id"`
			Content string `json:"content"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != 0 || resp.Data.SkillID != parent.ID || resp.Data.Content != parentContent {
		t.Fatalf("expected shared source detail, got %#v", resp)
	}
}

func TestCreateParentSkillBuildsFrontmatterFromBodyOnlyContent(t *testing.T) {
	db := newSkillTestDB(t)

	req := createSkillRequest{
		Name:        "git-workflow",
		Description: "Git workflow for postman test",
		Category:    "coding",
		Content:     "# Git Workflow\n\nKeep commit history clean and easy to review.",
		AutoEvo:     true,
	}
	if err := createParentSkill(context.Background(), db.DB, "u1", "User 1", req); err != nil {
		t.Fatalf("create parent skill: %v", err)
	}

	var row orm.SkillResource
	if err := db.Where("owner_user_id = ? AND node_type = ?", "u1", evolution.SkillNodeTypeParent).Take(&row).Error; err != nil {
		t.Fatalf("query parent skill: %v", err)
	}

	expectedContent := "---\nname: git-workflow\ncategory: coding\ndescription: Git workflow for postman test\n---\n# Git Workflow\n\nKeep commit history clean and easy to review."
	if row.SkillName != "git-workflow" {
		t.Fatalf("expected skill name git-workflow, got %q", row.SkillName)
	}
	if row.Description != "Git workflow for postman test" {
		t.Fatalf("expected description to be persisted, got %q", row.Description)
	}
	if row.RelativePath != evolution.ParentSkillRelativePath("coding", "git-workflow") {
		t.Fatalf("unexpected relative path: %q", row.RelativePath)
	}
	if row.ContentHash != evolution.HashContent(expectedContent) {
		t.Fatalf("expected content hash to use rebuilt content")
	}

	if row.Content != expectedContent {
		t.Fatalf("unexpected DB content: %q", row.Content)
	}
}

func TestCreateParentSkillPersistsChildDescriptions(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	createReq := createSkillRequest{
		Name:        "git-workflow",
		Description: "Git workflow for postman test",
		Category:    "coding",
		Content:     "# Git Workflow\n\nKeep commit history clean and easy to review.",
		Children: []childSkillInput{
			{
				Name:        "rules",
				Description: "Branching and review rules",
				Tags:        []string{"review", "branch"},
				Content:     "1. Create a feature branch.\n2. Rebase before merging.",
				FileExt:     "md",
			},
		},
	}
	if err := createParentSkill(context.Background(), db.DB, "u1", "User 1", createReq); err != nil {
		t.Fatalf("create parent skill with child: %v", err)
	}

	var parent orm.SkillResource
	if err := db.Where("owner_user_id = ? AND node_type = ?", "u1", evolution.SkillNodeTypeParent).Take(&parent).Error; err != nil {
		t.Fatalf("query parent skill: %v", err)
	}
	var child orm.SkillResource
	if err := db.Where("owner_user_id = ? AND node_type = ?", "u1", evolution.SkillNodeTypeChild).Take(&child).Error; err != nil {
		t.Fatalf("query child skill: %v", err)
	}
	if child.Description != "Branching and review rules" {
		t.Fatalf("expected child description to persist, got %q", child.Description)
	}
	childTags := parseTags(child.Tags)
	if fmt.Sprint(childTags) != "[branch review]" {
		t.Fatalf("expected child tags to persist, got %#v", childTags)
	}

	detail, err := getSkillDetail(context.Background(), db.DB, "u1", child.ID)
	if err != nil {
		t.Fatalf("get child detail: %v", err)
	}
	if detail["description"] != "Branching and review rules" {
		t.Fatalf("expected child detail description, got %#v", detail["description"])
	}
	if fmt.Sprint(detail["tags"]) != "[branch review]" {
		t.Fatalf("expected child detail tags, got %#v", detail["tags"])
	}

	req := httptest.NewRequest(http.MethodGet, "/api/core/skills", nil)
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()
	List(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp listSkillsAPITestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(resp.Data.Items) != 1 || resp.Data.Items[0].SkillID != parent.ID {
		t.Fatalf("expected one parent in list, got %#v", resp.Data.Items)
	}
	if len(resp.Data.Items[0].Children) != 1 || resp.Data.Items[0].Children[0].SkillID != child.ID {
		t.Fatalf("expected one child in list, got %#v", resp.Data.Items[0].Children)
	}
	if resp.Data.Items[0].Children[0].Description != "Branching and review rules" {
		t.Fatalf("expected list child description, got %q", resp.Data.Items[0].Children[0].Description)
	}
	if fmt.Sprint(resp.Data.Items[0].Children[0].Tags) != "[branch review]" {
		t.Fatalf("expected list child tags, got %#v", resp.Data.Items[0].Children[0].Tags)
	}
}

func TestCreateChildSkillPersistsDescription(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	parentReq := createSkillRequest{
		Name:        "git-workflow",
		Description: "Git workflow for postman test",
		Category:    "coding",
		Content:     "# Git Workflow\n\nKeep commit history clean and easy to review.",
	}
	if err := createParentSkill(context.Background(), db.DB, "u1", "User 1", parentReq); err != nil {
		t.Fatalf("create parent skill: %v", err)
	}

	var parent orm.SkillResource
	if err := db.Where("owner_user_id = ? AND node_type = ? AND skill_name = ?", "u1", evolution.SkillNodeTypeParent, "git-workflow").Take(&parent).Error; err != nil {
		t.Fatalf("query parent skill: %v", err)
	}

	reqBody := fmt.Sprintf(`{"name":"rules","description":"Branching and review rules","parent_skill_id":%q,"tags":["review","branch"],"content":"1. Create a feature branch.","file_ext":"md"}`, parent.ID)
	req := httptest.NewRequest(http.MethodPost, "/api/core/skills", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", "u1")
	req.Header.Set("X-User-Name", "User 1")
	rec := httptest.NewRecorder()

	CreateManaged(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp getSkillDetailAPITestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("expected code 0, got %d message=%s", resp.Code, resp.Message)
	}
	if resp.Data.Description != "Branching and review rules" {
		t.Fatalf("expected create response child description, got %q", resp.Data.Description)
	}
	if resp.Data.ParentSkillID != parent.ID {
		t.Fatalf("expected create response parent_skill_id %q, got %q", parent.ID, resp.Data.ParentSkillID)
	}

	var child orm.SkillResource
	if err := db.Where("id = ?", resp.Data.SkillID).Take(&child).Error; err != nil {
		t.Fatalf("query created child skill: %v", err)
	}
	if child.Category != "coding" {
		t.Fatalf("expected child category from parent, got %q", child.Category)
	}
	if child.ParentSkillName != "git-workflow" {
		t.Fatalf("expected child parent_skill_name from parent, got %q", child.ParentSkillName)
	}
	if child.RelativePath != evolution.ChildSkillRelativePath("coding", "git-workflow", "rules", "md") {
		t.Fatalf("unexpected child relative_path: %q", child.RelativePath)
	}
	if child.Description != "Branching and review rules" {
		t.Fatalf("expected child description to persist, got %q", child.Description)
	}
	if fmt.Sprint(parseTags(child.Tags)) != "[branch review]" {
		t.Fatalf("expected child tags to persist, got %#v", parseTags(child.Tags))
	}
}

func TestCreateParentSkillAllowsDuplicateParentNameAcrossCategories(t *testing.T) {
	db := newSkillTestDB(t)

	req := createSkillRequest{
		Name:        "git-workflow",
		Description: "Git workflow for postman test",
		Category:    "coding",
		Content:     "# Git Workflow\n\nKeep commit history clean and easy to review.",
	}
	if err := createParentSkill(context.Background(), db.DB, "u1", "User 1", req); err != nil {
		t.Fatalf("create parent skill: %v", err)
	}

	duplicateReq := createSkillRequest{
		Name:        "git-workflow",
		Description: "Same name in another category",
		Category:    "ops",
		Content:     "# Git Workflow\n\nSame name in another category should be allowed.",
	}
	if err := createParentSkill(context.Background(), db.DB, "u1", "User 1", duplicateReq); err != nil {
		t.Fatalf("create parent skill with same name in another category: %v", err)
	}

	var count int64
	if err := db.Model(&orm.SkillResource{}).Where("owner_user_id = ? AND node_type = ? AND skill_name = ?", "u1", evolution.SkillNodeTypeParent, "git-workflow").Count(&count).Error; err != nil {
		t.Fatalf("count parent skills: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected two parent skills named git-workflow, got %d", count)
	}
}

func TestCreateParentSkillRejectsBuiltinIdentityConflict(t *testing.T) {
	db := newSkillTestDB(t)
	setBuiltinCatalogForTest(t, []builtinSkill{
		testBuiltinSkill("builtin-paper-search", "search", "paper-search"),
	})

	req := createSkillRequest{
		Name:        "paper-search",
		Description: "User paper search",
		Category:    "search",
		Content:     "# Paper Search\n\nSearch papers.",
	}
	err := createParentSkill(context.Background(), db.DB, "u1", "User 1", req)
	if !errors.Is(err, gorm.ErrDuplicatedKey) {
		t.Fatalf("expected builtin duplicate error, got %v", err)
	}
}

func TestCreateParentSkillAllowsBuiltinNameInDifferentCategory(t *testing.T) {
	db := newSkillTestDB(t)
	setBuiltinCatalogForTest(t, []builtinSkill{
		testBuiltinSkill("builtin-paper-search", "search", "paper-search"),
	})

	req := createSkillRequest{
		Name:        "paper-search",
		Description: "Review paper search",
		Category:    "review",
		Content:     "# Paper Search\n\nReview papers.",
	}
	if err := createParentSkill(context.Background(), db.DB, "u1", "User 1", req); err != nil {
		t.Fatalf("create parent skill with builtin name in another category: %v", err)
	}
}

func TestEnableBuiltinSkillRejectsExistingSameCategoryAndName(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	setBuiltinCatalogForTest(t, []builtinSkill{
		testBuiltinSkill("builtin-paper-search", "search", "paper-search"),
	})

	now := time.Now()
	existing := orm.SkillResource{
		ID:              "skill-existing-paper-search",
		OwnerUserID:     "u1",
		OwnerUserName:   "User 1",
		Category:        "search",
		ParentSkillName: "",
		SkillName:       "paper-search",
		NodeType:        evolution.SkillNodeTypeParent,
		Description:     "Existing user skill",
		FileExt:         "md",
		RelativePath:    evolution.ParentSkillRelativePath("search", "paper-search"),
		Content:         "---\nname: paper-search\ncategory: search\ndescription: Existing user skill\n---\n# Paper Search\n\nExisting body.",
		ContentHash:     evolution.HashContent("existing"),
		Version:         1,
		IsEnabled:       true,
		UpdateStatus:    evolution.UpdateStatusUpToDate,
		CreateUserID:    "u1",
		CreateUserName:  "User 1",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := db.Create(&existing).Error; err != nil {
		t.Fatalf("create existing skill: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/core/builtin-skills/builtin-paper-search:enable", nil)
	req = mux.SetURLVars(req, map[string]string{"builtin_skill_uid": "builtin-paper-search"})
	req.Header.Set("X-User-Id", "u1")
	req.Header.Set("X-User-Name", "User 1")
	rec := httptest.NewRecorder()

	EnableBuiltinSkill(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected status 409, got %d body=%s", rec.Code, rec.Body.String())
	}
	var renamedCount int64
	if err := db.Model(&orm.SkillResource{}).
		Where("owner_user_id = ? AND skill_name = ?", "u1", "paper-search-1").
		Count(&renamedCount).Error; err != nil {
		t.Fatalf("count renamed builtin skill: %v", err)
	}
	if renamedCount != 0 {
		t.Fatalf("expected no suffixed builtin copy, got %d", renamedCount)
	}
}

func TestEnableBuiltinSkillAllowsExistingSameNameInDifferentCategory(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	setBuiltinCatalogForTest(t, []builtinSkill{
		testBuiltinSkill("builtin-paper-search", "search", "paper-search"),
	})

	now := time.Now()
	existing := orm.SkillResource{
		ID:              "skill-existing-review-paper-search",
		OwnerUserID:     "u1",
		OwnerUserName:   "User 1",
		Category:        "review",
		ParentSkillName: "",
		SkillName:       "paper-search",
		NodeType:        evolution.SkillNodeTypeParent,
		Description:     "Existing user skill",
		FileExt:         "md",
		RelativePath:    evolution.ParentSkillRelativePath("review", "paper-search"),
		Content:         "---\nname: paper-search\ncategory: review\ndescription: Existing user skill\n---\n# Paper Search\n\nExisting body.",
		ContentHash:     evolution.HashContent("existing"),
		Version:         1,
		IsEnabled:       true,
		UpdateStatus:    evolution.UpdateStatusUpToDate,
		CreateUserID:    "u1",
		CreateUserName:  "User 1",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := db.Create(&existing).Error; err != nil {
		t.Fatalf("create existing skill: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/core/builtin-skills/builtin-paper-search:enable", nil)
	req = mux.SetURLVars(req, map[string]string{"builtin_skill_uid": "builtin-paper-search"})
	req.Header.Set("X-User-Id", "u1")
	req.Header.Set("X-User-Name", "User 1")
	rec := httptest.NewRecorder()

	EnableBuiltinSkill(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var enabled orm.SkillResource
	if err := db.Where("owner_user_id = ? AND category = ? AND skill_name = ? AND origin_builtin_skill_uid = ?",
		"u1", "search", "paper-search", "builtin-paper-search").Take(&enabled).Error; err != nil {
		t.Fatalf("query enabled builtin skill: %v", err)
	}
	var renamedCount int64
	if err := db.Model(&orm.SkillResource{}).
		Where("owner_user_id = ? AND skill_name = ?", "u1", "paper-search-1").
		Count(&renamedCount).Error; err != nil {
		t.Fatalf("count renamed builtin skill: %v", err)
	}
	if renamedCount != 0 {
		t.Fatalf("expected no suffixed builtin copy, got %d", renamedCount)
	}
}

func TestUpdateParentSkillRejectsBuiltinIdentityConflict(t *testing.T) {
	db := newSkillTestDB(t)
	setBuiltinCatalogForTest(t, []builtinSkill{
		testBuiltinSkill("builtin-paper-search", "search", "paper-search"),
	})

	createReq := createSkillRequest{
		Name:        "custom-search",
		Description: "Custom search",
		Category:    "search",
		Content:     "# Custom Search\n\nCustom body.",
	}
	if err := createParentSkill(context.Background(), db.DB, "u1", "User 1", createReq); err != nil {
		t.Fatalf("create parent skill: %v", err)
	}

	var row orm.SkillResource
	if err := db.Where("owner_user_id = ? AND category = ? AND skill_name = ?", "u1", "search", "custom-search").Take(&row).Error; err != nil {
		t.Fatalf("query parent skill: %v", err)
	}
	newName := "paper-search"
	err := updateSkill(context.Background(), db.DB, "u1", "User 1", row.ID, updateSkillRequest{Name: &newName})
	if !errors.Is(err, gorm.ErrDuplicatedKey) {
		t.Fatalf("expected builtin duplicate error, got %v", err)
	}
}

func TestUpdateParentSkillRebuildsContentFromBodyOnlyPayload(t *testing.T) {
	db := newSkillTestDB(t)

	createReq := createSkillRequest{
		Name:        "git-workflow",
		Description: "Git workflow for postman test",
		Category:    "coding",
		Content:     "# Git Workflow\n\nKeep commit history clean and easy to review.",
	}
	if err := createParentSkill(context.Background(), db.DB, "u1", "User 1", createReq); err != nil {
		t.Fatalf("create parent skill: %v", err)
	}

	var row orm.SkillResource
	if err := db.Where("owner_user_id = ? AND node_type = ?", "u1", evolution.SkillNodeTypeParent).Take(&row).Error; err != nil {
		t.Fatalf("query parent skill: %v", err)
	}

	updateReq := updateSkillRequest{
		Description: stringPtr("Updated git workflow"),
		Content:     stringPtr("# Git Workflow\n\nUse small, reviewable commits."),
	}
	if err := updateSkill(context.Background(), db.DB, "u1", "User 1", row.ID, updateReq); err != nil {
		t.Fatalf("update parent skill: %v", err)
	}

	var updated orm.SkillResource
	if err := db.Where("id = ?", row.ID).Take(&updated).Error; err != nil {
		t.Fatalf("query updated parent skill: %v", err)
	}

	expectedContent := "---\nname: git-workflow\ncategory: coding\ndescription: Updated git workflow\n---\n# Git Workflow\n\nUse small, reviewable commits."
	if updated.SkillName != "git-workflow" {
		t.Fatalf("expected skill name to stay git-workflow, got %q", updated.SkillName)
	}
	if updated.Description != "Updated git workflow" {
		t.Fatalf("expected updated description, got %q", updated.Description)
	}
	if updated.Content != expectedContent {
		t.Fatalf("unexpected updated DB content: %q", updated.Content)
	}
}

func TestUpdateParentSkillAllowsDuplicateParentNameAcrossCategories(t *testing.T) {
	db := newSkillTestDB(t)

	firstReq := createSkillRequest{
		Name:        "git-workflow",
		Description: "Git workflow for postman test",
		Category:    "coding",
		Content:     "# Git Workflow\n\nKeep commit history clean and easy to review.",
	}
	if err := createParentSkill(context.Background(), db.DB, "u1", "User 1", firstReq); err != nil {
		t.Fatalf("create first parent skill: %v", err)
	}
	secondReq := createSkillRequest{
		Name:        "release-check",
		Description: "Release checklist",
		Category:    "ops",
		Content:     "# Release Checklist\n\nRun release checks.",
	}
	if err := createParentSkill(context.Background(), db.DB, "u1", "User 1", secondReq); err != nil {
		t.Fatalf("create second parent skill: %v", err)
	}

	var second orm.SkillResource
	if err := db.Where("owner_user_id = ? AND node_type = ? AND skill_name = ?", "u1", evolution.SkillNodeTypeParent, "release-check").Take(&second).Error; err != nil {
		t.Fatalf("query second parent skill: %v", err)
	}

	updateReq := updateSkillRequest{Name: stringPtr("git-workflow")}
	if err := updateSkill(context.Background(), db.DB, "u1", "User 1", second.ID, updateReq); err != nil {
		t.Fatalf("update parent skill with same name in another category: %v", err)
	}

	var updated orm.SkillResource
	if err := db.Where("id = ?", second.ID).Take(&updated).Error; err != nil {
		t.Fatalf("query updated parent skill: %v", err)
	}
	if updated.SkillName != "git-workflow" || updated.Category != "ops" {
		t.Fatalf("expected skill identity ops/git-workflow, got %q/%q", updated.Category, updated.SkillName)
	}
}

func TestUpdateParentSkillMetadataOnlyDoesNotCreateResourceVersion(t *testing.T) {
	db := newSkillTestDB(t)

	createReq := createSkillRequest{
		Name:        "git-workflow",
		Description: "Git workflow for postman test",
		Category:    "coding",
		Content:     "# Git Workflow\n\nKeep commit history clean and easy to review.",
	}
	if err := createParentSkill(context.Background(), db.DB, "u1", "User 1", createReq); err != nil {
		t.Fatalf("create parent skill: %v", err)
	}

	var row orm.SkillResource
	if err := db.Where("owner_user_id = ? AND node_type = ?", "u1", evolution.SkillNodeTypeParent).Take(&row).Error; err != nil {
		t.Fatalf("query parent skill: %v", err)
	}
	if got := countSkillResourceVersions(t, db, row.ID); got != 1 {
		t.Fatalf("expected create to write 1 resource version, got %d", got)
	}

	description := "Updated git workflow"
	if err := updateSkill(context.Background(), db.DB, "u1", "User 1", row.ID, updateSkillRequest{Description: &description}); err != nil {
		t.Fatalf("update parent skill description: %v", err)
	}
	if got := countSkillResourceVersions(t, db, row.ID); got != 1 {
		t.Fatalf("expected metadata-only update to keep 1 resource version, got %d", got)
	}

	content := "# Git Workflow\n\nUse small, reviewable commits."
	if err := updateSkill(context.Background(), db.DB, "u1", "User 1", row.ID, updateSkillRequest{Content: &content}); err != nil {
		t.Fatalf("update parent skill content: %v", err)
	}
	if got := countSkillResourceVersions(t, db, row.ID); got != 2 {
		t.Fatalf("expected content update to write second resource version, got %d", got)
	}
	var latest orm.ResourceVersion
	if err := db.Where("resource_id = ?", row.ID).Order("created_at DESC, id DESC").Take(&latest).Error; err != nil {
		t.Fatalf("query latest resource version: %v", err)
	}
	if latest.ChangeSource != resourcechange.ChangeSourceDirectSave {
		t.Fatalf("expected direct_save version source, got %q", latest.ChangeSource)
	}
	if !strings.Contains(latest.Diff, "+Use small, reviewable commits.") {
		t.Fatalf("expected latest diff to include content body update, got %q", latest.Diff)
	}
}

func TestUpdateParentSkillRejectsParentSkillName(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	createReq := createSkillRequest{
		Name:        "git-workflow",
		Description: "Git workflow for postman test",
		Category:    "coding",
		Content:     "# Git Workflow\n\nKeep commit history clean and easy to review.",
	}
	if err := createParentSkill(context.Background(), db.DB, "u1", "User 1", createReq); err != nil {
		t.Fatalf("create parent skill: %v", err)
	}
	otherReq := createSkillRequest{
		Name:        "release-check",
		Description: "Release checklist",
		Category:    "coding",
		Content:     "# Release Checklist\n\nRun release checks.",
	}
	if err := createParentSkill(context.Background(), db.DB, "u1", "User 1", otherReq); err != nil {
		t.Fatalf("create other parent skill: %v", err)
	}

	var parent orm.SkillResource
	if err := db.Where("owner_user_id = ? AND node_type = ? AND skill_name = ?", "u1", evolution.SkillNodeTypeParent, "git-workflow").Take(&parent).Error; err != nil {
		t.Fatalf("query parent skill: %v", err)
	}

	req := mux.SetURLVars(
		httptest.NewRequest(
			http.MethodPatch,
			"/api/core/skills/"+parent.ID,
			strings.NewReader(`{"parent_skill_name":"release-check"}`),
		),
		map[string]string{"skill_id": parent.ID},
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", "u1")
	req.Header.Set("X-User-Name", "User 1")
	rec := httptest.NewRecorder()

	UpdateManaged(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !strings.Contains(resp.Message, "parent_skill_name cannot be updated") {
		t.Fatalf("expected parent_skill_name update error, got code=%d message=%q", resp.Code, resp.Message)
	}

	var unchanged orm.SkillResource
	if err := db.Where("id = ?", parent.ID).Take(&unchanged).Error; err != nil {
		t.Fatalf("query unchanged parent skill: %v", err)
	}
	if unchanged.NodeType != evolution.SkillNodeTypeParent {
		t.Fatalf("expected skill to remain parent, got %q", unchanged.NodeType)
	}
	if unchanged.ParentSkillName != "" {
		t.Fatalf("expected parent_skill_name to remain empty, got %q", unchanged.ParentSkillName)
	}
}

func TestUpdateParentSkillIgnoresParentSkillID(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	createReq := createSkillRequest{
		Name:        "git-workflow",
		Description: "Git workflow for postman test",
		Category:    "coding",
		Content:     "# Git Workflow\n\nKeep commit history clean and easy to review.",
	}
	if err := createParentSkill(context.Background(), db.DB, "u1", "User 1", createReq); err != nil {
		t.Fatalf("create parent skill: %v", err)
	}
	otherReq := createSkillRequest{
		Name:        "release-check",
		Description: "Release checklist",
		Category:    "coding",
		Content:     "# Release Checklist\n\nRun release checks.",
	}
	if err := createParentSkill(context.Background(), db.DB, "u1", "User 1", otherReq); err != nil {
		t.Fatalf("create other parent skill: %v", err)
	}

	var parent orm.SkillResource
	if err := db.Where("owner_user_id = ? AND node_type = ? AND skill_name = ?", "u1", evolution.SkillNodeTypeParent, "git-workflow").Take(&parent).Error; err != nil {
		t.Fatalf("query parent skill: %v", err)
	}
	var other orm.SkillResource
	if err := db.Where("owner_user_id = ? AND node_type = ? AND skill_name = ?", "u1", evolution.SkillNodeTypeParent, "release-check").Take(&other).Error; err != nil {
		t.Fatalf("query other parent skill: %v", err)
	}

	req := mux.SetURLVars(
		httptest.NewRequest(
			http.MethodPatch,
			"/api/core/skills/"+parent.ID,
			strings.NewReader(fmt.Sprintf(`{"name":"svn-usage","content":"Manual update content.","is_locked":false,"description":"Updated parent description","parent_skill_id":%q,"tags":[],"file_ext":"md"}`, other.ID)),
		),
		map[string]string{"skill_id": parent.ID},
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", "u1")
	req.Header.Set("X-User-Name", "User 1")
	rec := httptest.NewRecorder()

	UpdateManaged(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp getSkillDetailAPITestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("expected code 0, got %d message=%q", resp.Code, resp.Message)
	}
	if resp.Data.Description != "Updated parent description" {
		t.Fatalf("expected description to update, got %q", resp.Data.Description)
	}

	var updated orm.SkillResource
	if err := db.Where("id = ?", parent.ID).Take(&updated).Error; err != nil {
		t.Fatalf("query updated parent skill: %v", err)
	}
	if updated.NodeType != evolution.SkillNodeTypeParent {
		t.Fatalf("expected skill to remain parent, got %q", updated.NodeType)
	}
	if updated.SkillName != "svn-usage" {
		t.Fatalf("expected name to update, got %q", updated.SkillName)
	}
	if updated.ParentSkillName != "" {
		t.Fatalf("expected parent_skill_name to remain empty, got %q", updated.ParentSkillName)
	}
	if updated.Description != "Updated parent description" {
		t.Fatalf("expected description to update, got %q", updated.Description)
	}
	if !strings.Contains(updated.Content, "Manual update content.") {
		t.Fatalf("expected content to update, got %q", updated.Content)
	}

	var otherUnchanged orm.SkillResource
	if err := db.Where("id = ?", other.ID).Take(&otherUnchanged).Error; err != nil {
		t.Fatalf("query other parent skill: %v", err)
	}
	if otherUnchanged.NodeType != evolution.SkillNodeTypeParent {
		t.Fatalf("expected other skill to remain parent, got %q", otherUnchanged.NodeType)
	}
	if otherUnchanged.SkillName != "release-check" {
		t.Fatalf("expected other skill name to stay release-check, got %q", otherUnchanged.SkillName)
	}
}

func TestUpdateChildSkillChangesParentSkill(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	createReq := createSkillRequest{
		Name:        "git-workflow",
		Description: "Git workflow for postman test",
		Category:    "coding",
		Content:     "# Git Workflow\n\nKeep commit history clean and easy to review.",
		Children: []childSkillInput{
			{
				Name:    "rules",
				Content: "1. Create a feature branch.",
				FileExt: "md",
			},
		},
	}
	if err := createParentSkill(context.Background(), db.DB, "u1", "User 1", createReq); err != nil {
		t.Fatalf("create parent skill with child: %v", err)
	}
	otherReq := createSkillRequest{
		Name:        "release-check",
		Description: "Release checklist",
		Category:    "ops",
		Content:     "# Release Checklist\n\nRun release checks.",
	}
	if err := createParentSkill(context.Background(), db.DB, "u1", "User 1", otherReq); err != nil {
		t.Fatalf("create other parent skill: %v", err)
	}
	var targetParent orm.SkillResource
	if err := db.Where("owner_user_id = ? AND node_type = ? AND skill_name = ?", "u1", evolution.SkillNodeTypeParent, "release-check").Take(&targetParent).Error; err != nil {
		t.Fatalf("query target parent skill: %v", err)
	}
	if err := db.Model(&orm.SkillResource{}).Where("id = ?", targetParent.ID).Updates(map[string]any{
		"is_enabled":    false,
		"update_status": "pending_confirm",
	}).Error; err != nil {
		t.Fatalf("update target parent state: %v", err)
	}

	var child orm.SkillResource
	if err := db.Where("owner_user_id = ? AND node_type = ? AND skill_name = ?", "u1", evolution.SkillNodeTypeChild, "rules").Take(&child).Error; err != nil {
		t.Fatalf("query child skill: %v", err)
	}

	req := mux.SetURLVars(
		httptest.NewRequest(
			http.MethodPatch,
			"/api/core/skills/"+child.ID,
			strings.NewReader(fmt.Sprintf(`{"name":"rules","description":"Branching rules","category":"coding","content":"1. Create a feature branch.","file_ext":"md","is_enabled":true,"parent_skill_id":%q,"parent_skill_name":"release-check","tags":[]}`, targetParent.ID)),
		),
		map[string]string{"skill_id": child.ID},
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", "u1")
	req.Header.Set("X-User-Name", "User 1")
	rec := httptest.NewRecorder()

	UpdateManaged(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("expected code 0, got %d message=%q", resp.Code, resp.Message)
	}

	var updated orm.SkillResource
	if err := db.Where("id = ?", child.ID).Take(&updated).Error; err != nil {
		t.Fatalf("query updated child skill: %v", err)
	}
	if updated.NodeType != evolution.SkillNodeTypeChild {
		t.Fatalf("expected skill to remain child, got %q", updated.NodeType)
	}
	if updated.ParentSkillName != "release-check" {
		t.Fatalf("expected parent_skill_name to update to release-check, got %q", updated.ParentSkillName)
	}
	if updated.Category != "ops" {
		t.Fatalf("expected category to update to ops, got %q", updated.Category)
	}
	if updated.RelativePath != evolution.ChildSkillRelativePath("ops", "release-check", "rules", "md") {
		t.Fatalf("unexpected relative_path after parent change: %q", updated.RelativePath)
	}
	if updated.IsEnabled {
		t.Fatalf("expected child to inherit disabled state from target parent")
	}
	if updated.UpdateStatus != evolution.UpdateStatusUpToDate {
		t.Fatalf("expected child update_status to normalize target parent state, got %q", updated.UpdateStatus)
	}
}

func TestUpdateChildSkillUpdatesDescription(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	createReq := createSkillRequest{
		Name:        "git-workflow",
		Description: "Git workflow for postman test",
		Category:    "coding",
		Content:     "# Git Workflow\n\nKeep commit history clean and easy to review.",
		Children: []childSkillInput{
			{
				Name:        "rules",
				Description: "Initial child description",
				Tags:        []string{"initial"},
				Content:     "1. Create a feature branch.",
				FileExt:     "md",
			},
		},
	}
	if err := createParentSkill(context.Background(), db.DB, "u1", "User 1", createReq); err != nil {
		t.Fatalf("create parent skill with child: %v", err)
	}

	var child orm.SkillResource
	if err := db.Where("owner_user_id = ? AND node_type = ? AND skill_name = ?", "u1", evolution.SkillNodeTypeChild, "rules").Take(&child).Error; err != nil {
		t.Fatalf("query child skill: %v", err)
	}

	req := mux.SetURLVars(
		httptest.NewRequest(
			http.MethodPatch,
			"/api/core/skills/"+child.ID,
			strings.NewReader(`{"description":"Updated child description","tags":["review","branch"],"content":"1. Create a feature branch."}`),
		),
		map[string]string{"skill_id": child.ID},
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", "u1")
	req.Header.Set("X-User-Name", "User 1")
	rec := httptest.NewRecorder()

	UpdateManaged(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp getSkillDetailAPITestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("expected code 0, got %d message=%s", resp.Code, resp.Message)
	}
	if resp.Data.Description != "Updated child description" {
		t.Fatalf("expected response child description to update, got %q", resp.Data.Description)
	}
	if fmt.Sprint(resp.Data.Tags) != "[branch review]" {
		t.Fatalf("expected response child tags to update, got %#v", resp.Data.Tags)
	}

	var updated orm.SkillResource
	if err := db.Where("id = ?", child.ID).Take(&updated).Error; err != nil {
		t.Fatalf("query updated child skill: %v", err)
	}
	if updated.Description != "Updated child description" {
		t.Fatalf("expected child description to update, got %q", updated.Description)
	}
	if fmt.Sprint(parseTags(updated.Tags)) != "[branch review]" {
		t.Fatalf("expected child tags to update, got %#v", parseTags(updated.Tags))
	}
	if updated.Content != "1. Create a feature branch." {
		t.Fatalf("expected child content unchanged, got %q", updated.Content)
	}
}

func TestUpdateChildSkillRejectsEmptyParentSkillName(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	createReq := createSkillRequest{
		Name:        "git-workflow",
		Description: "Git workflow for postman test",
		Category:    "coding",
		Content:     "# Git Workflow\n\nKeep commit history clean and easy to review.",
		Children: []childSkillInput{
			{
				Name:    "rules",
				Content: "1. Create a feature branch.",
				FileExt: "md",
			},
		},
	}
	if err := createParentSkill(context.Background(), db.DB, "u1", "User 1", createReq); err != nil {
		t.Fatalf("create parent skill with child: %v", err)
	}

	var child orm.SkillResource
	if err := db.Where("owner_user_id = ? AND node_type = ? AND skill_name = ?", "u1", evolution.SkillNodeTypeChild, "rules").Take(&child).Error; err != nil {
		t.Fatalf("query child skill: %v", err)
	}

	req := mux.SetURLVars(
		httptest.NewRequest(
			http.MethodPatch,
			"/api/core/skills/"+child.ID,
			strings.NewReader(`{"parent_skill_name":""}`),
		),
		map[string]string{"skill_id": child.ID},
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", "u1")
	req.Header.Set("X-User-Name", "User 1")
	rec := httptest.NewRecorder()

	UpdateManaged(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d body=%s", rec.Code, rec.Body.String())
	}

	var unchanged orm.SkillResource
	if err := db.Where("id = ?", child.ID).Take(&unchanged).Error; err != nil {
		t.Fatalf("query unchanged child skill: %v", err)
	}
	if unchanged.ParentSkillName != "git-workflow" {
		t.Fatalf("expected parent_skill_name to remain git-workflow, got %q", unchanged.ParentSkillName)
	}
}

func TestUpdateChildSkillRejectsEmptyParentSkillID(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	createReq := createSkillRequest{
		Name:        "git-workflow",
		Description: "Git workflow for postman test",
		Category:    "coding",
		Content:     "# Git Workflow\n\nKeep commit history clean and easy to review.",
		Children: []childSkillInput{
			{
				Name:    "rules",
				Content: "1. Create a feature branch.",
				FileExt: "md",
			},
		},
	}
	if err := createParentSkill(context.Background(), db.DB, "u1", "User 1", createReq); err != nil {
		t.Fatalf("create parent skill with child: %v", err)
	}

	var child orm.SkillResource
	if err := db.Where("owner_user_id = ? AND node_type = ? AND skill_name = ?", "u1", evolution.SkillNodeTypeChild, "rules").Take(&child).Error; err != nil {
		t.Fatalf("query child skill: %v", err)
	}

	req := mux.SetURLVars(
		httptest.NewRequest(
			http.MethodPatch,
			"/api/core/skills/"+child.ID,
			strings.NewReader(`{"parent_skill_id":""}`),
		),
		map[string]string{"skill_id": child.ID},
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", "u1")
	req.Header.Set("X-User-Name", "User 1")
	rec := httptest.NewRecorder()

	UpdateManaged(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d body=%s", rec.Code, rec.Body.String())
	}

	var unchanged orm.SkillResource
	if err := db.Where("id = ?", child.ID).Take(&unchanged).Error; err != nil {
		t.Fatalf("query unchanged child skill: %v", err)
	}
	if unchanged.ParentSkillName != "git-workflow" {
		t.Fatalf("expected parent_skill_name to remain git-workflow, got %q", unchanged.ParentSkillName)
	}
}

func TestUpdateChildSkillRejectsMissingParentSkillName(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	createReq := createSkillRequest{
		Name:        "git-workflow",
		Description: "Git workflow for postman test",
		Category:    "coding",
		Content:     "# Git Workflow\n\nKeep commit history clean and easy to review.",
		Children: []childSkillInput{
			{
				Name:    "rules",
				Content: "1. Create a feature branch.",
				FileExt: "md",
			},
		},
	}
	if err := createParentSkill(context.Background(), db.DB, "u1", "User 1", createReq); err != nil {
		t.Fatalf("create parent skill with child: %v", err)
	}

	var child orm.SkillResource
	if err := db.Where("owner_user_id = ? AND node_type = ? AND skill_name = ?", "u1", evolution.SkillNodeTypeChild, "rules").Take(&child).Error; err != nil {
		t.Fatalf("query child skill: %v", err)
	}

	req := mux.SetURLVars(
		httptest.NewRequest(
			http.MethodPatch,
			"/api/core/skills/"+child.ID,
			strings.NewReader(`{"parent_skill_name":"not-found"}`),
		),
		map[string]string{"skill_id": child.ID},
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", "u1")
	req.Header.Set("X-User-Name", "User 1")
	rec := httptest.NewRecorder()

	UpdateManaged(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d body=%s", rec.Code, rec.Body.String())
	}

	var unchanged orm.SkillResource
	if err := db.Where("id = ?", child.ID).Take(&unchanged).Error; err != nil {
		t.Fatalf("query unchanged child skill: %v", err)
	}
	if unchanged.ParentSkillName != "git-workflow" {
		t.Fatalf("expected parent_skill_name to remain git-workflow, got %q", unchanged.ParentSkillName)
	}
}

func TestUpdateChildSkillRejectsMissingParentSkillID(t *testing.T) {
	db := newSkillTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	createReq := createSkillRequest{
		Name:        "git-workflow",
		Description: "Git workflow for postman test",
		Category:    "coding",
		Content:     "# Git Workflow\n\nKeep commit history clean and easy to review.",
		Children: []childSkillInput{
			{
				Name:    "rules",
				Content: "1. Create a feature branch.",
				FileExt: "md",
			},
		},
	}
	if err := createParentSkill(context.Background(), db.DB, "u1", "User 1", createReq); err != nil {
		t.Fatalf("create parent skill with child: %v", err)
	}

	var child orm.SkillResource
	if err := db.Where("owner_user_id = ? AND node_type = ? AND skill_name = ?", "u1", evolution.SkillNodeTypeChild, "rules").Take(&child).Error; err != nil {
		t.Fatalf("query child skill: %v", err)
	}

	req := mux.SetURLVars(
		httptest.NewRequest(
			http.MethodPatch,
			"/api/core/skills/"+child.ID,
			strings.NewReader(`{"parent_skill_id":"not-found"}`),
		),
		map[string]string{"skill_id": child.ID},
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", "u1")
	req.Header.Set("X-User-Name", "User 1")
	rec := httptest.NewRecorder()

	UpdateManaged(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d body=%s", rec.Code, rec.Body.String())
	}

	var unchanged orm.SkillResource
	if err := db.Where("id = ?", child.ID).Take(&unchanged).Error; err != nil {
		t.Fatalf("query unchanged child skill: %v", err)
	}
	if unchanged.ParentSkillName != "git-workflow" {
		t.Fatalf("expected parent_skill_name to remain git-workflow, got %q", unchanged.ParentSkillName)
	}
}

func TestUpdateParentSkillRenameMovesChildrenAndRebuildsFrontmatter(t *testing.T) {
	db := newSkillTestDB(t)

	createReq := createSkillRequest{
		Name:        "git-workflow",
		Description: "Git workflow for postman test",
		Category:    "coding",
		Content:     "# Git Workflow\n\nKeep commit history clean and easy to review.",
		Children: []childSkillInput{
			{
				Name:    "rules",
				Content: "1. Create a feature branch.\n2. Rebase before merging.",
				FileExt: "md",
				AutoEvo: true,
			},
		},
	}
	if err := createParentSkill(context.Background(), db.DB, "u1", "User 1", createReq); err != nil {
		t.Fatalf("create parent skill with child: %v", err)
	}

	var parent orm.SkillResource
	if err := db.Where("owner_user_id = ? AND node_type = ?", "u1", evolution.SkillNodeTypeParent).Take(&parent).Error; err != nil {
		t.Fatalf("query parent skill: %v", err)
	}
	var child orm.SkillResource
	if err := db.Where("owner_user_id = ? AND node_type = ?", "u1", evolution.SkillNodeTypeChild).Take(&child).Error; err != nil {
		t.Fatalf("query child skill: %v", err)
	}
	updateReq := updateSkillRequest{
		Name:        stringPtr("git-workflow-renamed"),
		Description: stringPtr("Renamed git workflow"),
	}
	if err := updateSkill(context.Background(), db.DB, "u1", "User 1", parent.ID, updateReq); err != nil {
		t.Fatalf("rename parent skill: %v", err)
	}

	var updatedParent orm.SkillResource
	if err := db.Where("id = ?", parent.ID).Take(&updatedParent).Error; err != nil {
		t.Fatalf("query renamed parent skill: %v", err)
	}
	var updatedChild orm.SkillResource
	if err := db.Where("id = ?", child.ID).Take(&updatedChild).Error; err != nil {
		t.Fatalf("query renamed child skill: %v", err)
	}

	expectedParentContent := "---\nname: git-workflow-renamed\ncategory: coding\ndescription: Renamed git workflow\n---\n# Git Workflow\n\nKeep commit history clean and easy to review."
	if updatedParent.Content != expectedParentContent {
		t.Fatalf("unexpected renamed parent content: %q", updatedParent.Content)
	}
	if updatedParent.SkillName != "git-workflow-renamed" {
		t.Fatalf("expected parent skill to be renamed, got %q", updatedParent.SkillName)
	}
	if updatedParent.RelativePath != evolution.ParentSkillRelativePath("coding", "git-workflow-renamed") {
		t.Fatalf("unexpected renamed parent relative path: %q", updatedParent.RelativePath)
	}

	expectedChildRelativePath := filepath.ToSlash(filepath.Join("coding", "git-workflow-renamed", "rules.md"))
	if updatedChild.ParentSkillName != "git-workflow-renamed" {
		t.Fatalf("expected child parent skill name to update, got %q", updatedChild.ParentSkillName)
	}
	if updatedChild.RelativePath != expectedChildRelativePath {
		t.Fatalf("unexpected child relative path: %q", updatedChild.RelativePath)
	}
}

func TestDeleteChildSkillKeepsParentRecord(t *testing.T) {
	db := newSkillTestDB(t)

	createReq := createSkillRequest{
		Name:        "git-workflow",
		Description: "Git workflow for postman test",
		Category:    "coding",
		Content:     "# Git Workflow\n\nKeep commit history clean and easy to review.",
		Children: []childSkillInput{
			{
				Name:    "rules",
				Content: "1. Create a feature branch.",
				FileExt: "md",
			},
		},
	}
	if err := createParentSkill(context.Background(), db.DB, "u1", "User 1", createReq); err != nil {
		t.Fatalf("create parent skill with child: %v", err)
	}

	var parent orm.SkillResource
	if err := db.Where("owner_user_id = ? AND node_type = ?", "u1", evolution.SkillNodeTypeParent).Take(&parent).Error; err != nil {
		t.Fatalf("query parent skill: %v", err)
	}
	var child orm.SkillResource
	if err := db.Where("owner_user_id = ? AND node_type = ?", "u1", evolution.SkillNodeTypeChild).Take(&child).Error; err != nil {
		t.Fatalf("query child skill: %v", err)
	}

	if err := DeleteSkill(context.Background(), db.DB, "u1", child.ID); err != nil {
		t.Fatalf("delete child skill: %v", err)
	}

	if err := db.Where("id = ?", child.ID).Take(&orm.SkillResource{}).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("expected child record to be deleted, got err=%v", err)
	}
	if err := db.Where("id = ?", parent.ID).Take(&orm.SkillResource{}).Error; err != nil {
		t.Fatalf("expected parent record to remain, got err=%v", err)
	}
}

func TestDeleteChildSkillRemovesRelatedSuggestions(t *testing.T) {
	db := newSkillTestDB(t)

	createReq := createSkillRequest{
		Name:        "git-workflow",
		Description: "Git workflow for postman test",
		Category:    "coding",
		Content:     "# Git Workflow\n\nKeep commit history clean and easy to review.",
		Children: []childSkillInput{
			{
				Name:    "rules",
				Content: "1. Create a feature branch.",
				FileExt: "md",
			},
		},
	}
	if err := createParentSkill(context.Background(), db.DB, "u1", "User 1", createReq); err != nil {
		t.Fatalf("create parent skill with child: %v", err)
	}

	var child orm.SkillResource
	if err := db.Where("owner_user_id = ? AND node_type = ?", "u1", evolution.SkillNodeTypeChild).Take(&child).Error; err != nil {
		t.Fatalf("query child skill: %v", err)
	}
	now := time.Now()
	if err := db.Create(&orm.ResourceSuggestion{
		ID:           "suggestion-child",
		UserID:       "u1",
		ResourceType: evolution.ResourceTypeSkill,
		ResourceKey:  evolution.SkillSuggestionResourceKey(child),
		Action:       "modify",
		SessionID:    "session-1",
		Status:       "pending_review",
		CreatedAt:    now,
		UpdatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("create child suggestion: %v", err)
	}

	if err := DeleteSkill(context.Background(), db.DB, "u1", child.ID); err != nil {
		t.Fatalf("delete child skill: %v", err)
	}

	var suggestionCount int64
	if err := db.Model(&orm.ResourceSuggestion{}).Where("id = ?", "suggestion-child").Count(&suggestionCount).Error; err != nil {
		t.Fatalf("count child suggestions: %v", err)
	}
	if suggestionCount != 0 {
		t.Fatalf("expected related child suggestions to be deleted, got %d", suggestionCount)
	}
}

func TestDeleteParentSkillRemovesChildrenRecords(t *testing.T) {
	db := newSkillTestDB(t)

	createReq := createSkillRequest{
		Name:        "git-workflow",
		Description: "Git workflow for postman test",
		Category:    "coding",
		Content:     "# Git Workflow\n\nKeep commit history clean and easy to review.",
		Children: []childSkillInput{
			{
				Name:    "rules",
				Content: "1. Create a feature branch.",
				FileExt: "md",
			},
			{
				Name:    "checklist",
				Content: "- Rebase before merging.",
				FileExt: "md",
			},
		},
	}
	if err := createParentSkill(context.Background(), db.DB, "u1", "User 1", createReq); err != nil {
		t.Fatalf("create parent skill with children: %v", err)
	}

	var parent orm.SkillResource
	if err := db.Where("owner_user_id = ? AND node_type = ?", "u1", evolution.SkillNodeTypeParent).Take(&parent).Error; err != nil {
		t.Fatalf("query parent skill: %v", err)
	}
	var children []orm.SkillResource
	if err := db.Where("owner_user_id = ? AND node_type = ?", "u1", evolution.SkillNodeTypeChild).Find(&children).Error; err != nil {
		t.Fatalf("query child skills: %v", err)
	}
	if err := DeleteSkill(context.Background(), db.DB, "u1", parent.ID); err != nil {
		t.Fatalf("delete parent skill: %v", err)
	}

	if err := db.Where("id = ?", parent.ID).Take(&orm.SkillResource{}).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("expected parent record to be deleted, got err=%v", err)
	}
	for _, child := range children {
		if err := db.Where("id = ?", child.ID).Take(&orm.SkillResource{}).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("expected child record %s to be deleted, got err=%v", child.ID, err)
		}
	}
}

func TestDeleteParentSkillRemovesRelatedSuggestions(t *testing.T) {
	db := newSkillTestDB(t)

	createReq := createSkillRequest{
		Name:        "git-workflow",
		Description: "Git workflow for postman test",
		Category:    "coding",
		Content:     "# Git Workflow\n\nKeep commit history clean and easy to review.",
		Children: []childSkillInput{
			{
				Name:    "rules",
				Content: "1. Create a feature branch.",
				FileExt: "md",
			},
		},
	}
	if err := createParentSkill(context.Background(), db.DB, "u1", "User 1", createReq); err != nil {
		t.Fatalf("create parent skill with child: %v", err)
	}

	var parent orm.SkillResource
	if err := db.Where("owner_user_id = ? AND node_type = ?", "u1", evolution.SkillNodeTypeParent).Take(&parent).Error; err != nil {
		t.Fatalf("query parent skill: %v", err)
	}
	var child orm.SkillResource
	if err := db.Where("owner_user_id = ? AND node_type = ?", "u1", evolution.SkillNodeTypeChild).Take(&child).Error; err != nil {
		t.Fatalf("query child skill: %v", err)
	}
	now := time.Now()
	suggestions := []orm.ResourceSuggestion{
		{
			ID:           "suggestion-parent",
			UserID:       "u1",
			ResourceType: evolution.ResourceTypeSkill,
			ResourceKey:  evolution.SkillSuggestionResourceKey(parent),
			Action:       "remove",
			SessionID:    "session-1",
			Status:       "pending_review",
			CreatedAt:    now,
			UpdatedAt:    now,
		},
		{
			ID:           "suggestion-child",
			UserID:       "u1",
			ResourceType: evolution.ResourceTypeSkill,
			ResourceKey:  evolution.SkillSuggestionResourceKey(child),
			Action:       "modify",
			SessionID:    "session-1",
			Status:       "pending_review",
			CreatedAt:    now,
			UpdatedAt:    now,
		},
	}
	if err := db.Create(&suggestions).Error; err != nil {
		t.Fatalf("create suggestions: %v", err)
	}

	if err := DeleteSkill(context.Background(), db.DB, "u1", parent.ID); err != nil {
		t.Fatalf("delete parent skill: %v", err)
	}

	var suggestionCount int64
	if err := db.Model(&orm.ResourceSuggestion{}).Where("id IN ?", []string{"suggestion-parent", "suggestion-child"}).Count(&suggestionCount).Error; err != nil {
		t.Fatalf("count parent suggestions: %v", err)
	}
	if suggestionCount != 0 {
		t.Fatalf("expected related parent suggestions to be deleted, got %d", suggestionCount)
	}
}

func stringPtr(value string) *string {
	return &value
}

func countSkillResourceVersions(t *testing.T, db *orm.DB, resourceID string) int64 {
	t.Helper()
	var count int64
	if err := db.Model(&orm.ResourceVersion{}).Where("resource_id = ?", resourceID).Count(&count).Error; err != nil {
		t.Fatalf("count resource versions: %v", err)
	}
	return count
}
