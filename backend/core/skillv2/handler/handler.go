package handler

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/evolution"
	skilldiff "lazymind/core/skillv2/diff"
	skillfs "lazymind/core/skillv2/fs"
	skillhttperr "lazymind/core/skillv2/httperr"
	skillmarket "lazymind/core/skillv2/market"
	skillremotefs "lazymind/core/skillv2/remotefs"
	skillreview "lazymind/core/skillv2/review"
	skillrevision "lazymind/core/skillv2/revision"
	skillsearch "lazymind/core/skillv2/search"
	skillservice "lazymind/core/skillv2/service"
	skillshare "lazymind/core/skillv2/share"
	"lazymind/core/store"
)

type skillSourceRequest struct {
	Type       string `json:"type"`
	UploadID   string `json:"upload_id"`
	Filename   string `json:"filename"`
	StoredPath string `json:"stored_path"`
	URL        string `json:"url"`
}

type legacyChildSkillInput struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Content     string   `json:"content"`
	FileExt     string   `json:"file_ext"`
	AutoEvo     bool     `json:"auto_evo"`
}

type createSkillRequest struct {
	Name        string                  `json:"name"`
	SkillName   string                  `json:"skill_name"`
	Category    string                  `json:"category"`
	Description string                  `json:"description"`
	Tags        []string                `json:"tags"`
	Content     string                  `json:"content"`
	Children    []legacyChildSkillInput `json:"children"`
	AutoEvo     bool                    `json:"auto_evo"`
	IsEnabled   *bool                   `json:"is_enabled"`
	Source      skillSourceRequest      `json:"source"`
}

type patchSkillRequest struct {
	Name        *string             `json:"name"`
	SkillName   *string             `json:"skill_name"`
	Category    *string             `json:"category"`
	Description *string             `json:"description"`
	Tags        *[]string           `json:"tags"`
	Content     *string             `json:"content"`
	AutoEvo     *bool               `json:"auto_evo"`
	IsEnabled   *bool               `json:"is_enabled"`
	Source      *skillSourceRequest `json:"source"`
}

type shareSkillRequest struct {
	TargetUserIDs  []string `json:"target_user_ids"`
	TargetGroupIDs []string `json:"target_group_ids"`
	Message        string   `json:"message"`
}

const (
	shareStatusPendingAccept = "pending_accept"
	shareStatusCompleted     = "completed"
	shareStatusRejected      = "rejected"
	shareStatusFailed        = "failed"
)

func List(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	userID, _, ok := requireUser(w, r)
	if !ok {
		return
	}
	resp, err := newSkillService(db).ListSkills(r.Context(), skillservice.ListSkillsRequest{UserID: userID})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	items, err := filterSkillSummaries(r.Context(), db, resp.Items, r)
	if err != nil {
		replyServiceError(w, err)
		return
	}
	total := len(items)
	items = paginateSkillSummaries(items, r)
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, skillSummaryDTO(item))
	}
	common.ReplyOK(w, map[string]any{
		"items":     out,
		"page":      positiveQueryInt(r, "page", 1),
		"page_size": positiveQueryInt(r, "page_size", 20),
		"total":     total,
	})
}

func ListTags(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	userID, _, ok := requireUser(w, r)
	if !ok {
		return
	}
	var rows []orm.SkillV2Skill
	if err := db.WithContext(r.Context()).
		Select("tags").
		Where("owner_user_id = ? AND deleted_at IS NULL", userID).
		Where("NOT EXISTS (SELECT 1 FROM skill_market_items AS market_items WHERE market_items.source_skill_id = skills.id)").
		Find(&rows).Error; err != nil {
		replyServiceError(w, err)
		return
	}
	set := map[string]struct{}{}
	for _, row := range rows {
		for _, tag := range decodeTags(row.Tags) {
			set[tag] = struct{}{}
		}
	}
	tags := make([]string, 0, len(set))
	for tag := range set {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	common.ReplyOK(w, map[string]any{"tags": tags})
}

func ListCategories(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	userID, _, ok := requireUser(w, r)
	if !ok {
		return
	}
	var rows []orm.SkillV2Skill
	if err := db.WithContext(r.Context()).
		Select("category").
		Where("owner_user_id = ? AND deleted_at IS NULL", userID).
		Where("NOT EXISTS (SELECT 1 FROM skill_market_items AS market_items WHERE market_items.source_skill_id = skills.id)").
		Find(&rows).Error; err != nil {
		replyServiceError(w, err)
		return
	}
	set := map[string]struct{}{}
	for _, row := range rows {
		if category := strings.TrimSpace(row.Category); category != "" {
			set[category] = struct{}{}
		}
	}
	categories := make([]string, 0, len(set))
	for category := range set {
		categories = append(categories, category)
	}
	sort.Strings(categories)
	common.ReplyOK(w, map[string]any{"categories": categories})
}

func Create(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	userID, userName, ok := requireUser(w, r)
	if !ok {
		return
	}
	var req createSkillRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	name := firstNonEmpty(strings.TrimSpace(req.Name), strings.TrimSpace(req.SkillName))
	category := strings.TrimSpace(req.Category)
	externalImport := strings.TrimSpace(req.Content) == "" && len(req.Children) == 0 && req.Source.isExternalImport()
	if !externalImport && (name == "" || category == "") {
		replyError(w, "name/category required", http.StatusBadRequest)
		return
	}
	source, cleanup, err := createSkillSourceFromRequest(name, category, strings.TrimSpace(req.Description), req.Content, req.Children, req.Source)
	if err != nil {
		replyError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if cleanup != nil {
		defer cleanup()
	}
	resp, err := newSkillService(db).CreateSkill(r.Context(), skillservice.CreateSkillRequest{
		OwnerUserID:    userID,
		OwnerUserName:  userName,
		CreateUserID:   userID,
		CreateUserName: userName,
		Name:           name,
		Category:       category,
		Description:    strings.TrimSpace(req.Description),
		Tags:           compactStrings(req.Tags),
		AutoEvo:        req.AutoEvo,
		IsEnabled:      req.IsEnabled,
		Source:         source,
	})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{
		"skill_id":         resp.SkillID,
		"head_revision_id": resp.HeadRevisionID,
	})
}

func (s skillSourceRequest) isExternalImport() bool {
	sourceType := strings.ToLower(strings.TrimSpace(s.Type))
	if sourceType == "" {
		switch {
		case strings.TrimSpace(s.UploadID) != "":
			sourceType = "uploaded_zip"
		case strings.TrimSpace(s.URL) != "":
			sourceType = "url"
		}
	}
	switch sourceType {
	case "uploaded", "upload", "uploaded_zip", "url":
		return true
	default:
		return false
	}
}

func Get(w http.ResponseWriter, r *http.Request) {
	db, skillID, userID, ok := requireOwnedSkill(w, r)
	if !ok {
		return
	}
	detail, err := newSkillService(db).GetSkill(r.Context(), skillservice.GetSkillRequest{SkillID: skillID, UserID: userID})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, skillDetailDTO(detail))
}

func Patch(w http.ResponseWriter, r *http.Request) {
	db, skillID, userID, ok := requireOwnedSkill(w, r)
	if !ok {
		return
	}
	if !ensureUserDraftWriteAllowed(w, r, db, userID, skillID) {
		return
	}
	var req patchSkillRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	name := trimStringPtr(req.Name)
	if name == nil {
		name = trimStringPtr(req.SkillName)
	}
	category := trimStringPtr(req.Category)
	description := trimStringPtr(req.Description)
	tags := compactStringSlicePtr(req.Tags)
	var source *skillservice.SourceInput
	if req.Source != nil {
		converted, err := req.Source.toServiceSource()
		if err != nil {
			replyError(w, err.Error(), http.StatusBadRequest)
			return
		}
		source = &converted
	} else if req.Content != nil {
		converted, cleanup, err := patchContentSourceFromHead(r.Context(), db, skillID, name, category, description, *req.Content)
		if err != nil {
			replyError(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer cleanup()
		source = &converted
	}
	resp, err := newSkillService(db).PatchSkill(r.Context(), skillservice.PatchSkillRequest{
		SkillID:     skillID,
		UserID:      userID,
		Name:        name,
		Category:    category,
		Description: description,
		Tags:        tags,
		AutoEvo:     req.AutoEvo,
		IsEnabled:   req.IsEnabled,
		Source:      source,
	})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{
		"skill_id":         resp.SkillID,
		"head_revision_id": resp.HeadRevisionID,
	})
}

func Delete(w http.ResponseWriter, r *http.Request) {
	db, skillID, userID, ok := requireOwnedSkill(w, r)
	if !ok {
		return
	}
	if !ensureUserDraftWriteAllowed(w, r, db, userID, skillID) {
		return
	}
	if err := newSkillService(db).DeleteSkill(r.Context(), skillservice.DeleteSkillRequest{SkillID: skillID, UserID: userID}); err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{"deleted": true})
}

func Trash(w http.ResponseWriter, r *http.Request) {
	Delete(w, r)
}

func ListTrash(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	userID, _, ok := requireUser(w, r)
	if !ok {
		return
	}
	resp, err := newSkillService(db).ListTrashedSkills(r.Context(), skillservice.ListSkillsRequest{UserID: userID})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	items := filterTrashedSkillSummaries(resp.Items, r)
	total := len(items)
	items = paginateSkillSummaries(items, r)
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, skillSummaryDTO(item))
	}
	common.ReplyOK(w, map[string]any{
		"items":     out,
		"page":      positiveQueryInt(r, "page", 1),
		"page_size": positiveQueryInt(r, "page_size", 20),
		"total":     total,
	})
}

func Restore(w http.ResponseWriter, r *http.Request) {
	db, skillID, userID, ok := requireSkillInTrash(w, r)
	if !ok {
		return
	}
	if err := newSkillService(db).RestoreSkill(r.Context(), skillservice.RestoreSkillRequest{SkillID: skillID, UserID: userID}); err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{"restored": true, "skill_id": skillID})
}

func Purge(w http.ResponseWriter, r *http.Request) {
	db, skillID, userID, ok := requireSkillInTrash(w, r)
	if !ok {
		return
	}
	if err := newSkillService(db).PurgeSkill(r.Context(), skillservice.PurgeSkillRequest{SkillID: skillID, UserID: userID}); err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{"purged": true, "skill_id": skillID})
}

func EmptyTrash(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	userID, _, ok := requireUser(w, r)
	if !ok {
		return
	}
	count, err := newSkillService(db).EmptyTrash(r.Context(), skillservice.EmptyTrashRequest{UserID: userID})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{"purged": count})
}

func Tree(w http.ResponseWriter, r *http.Request) {
	db, skillID, _, ok := requireOwnedSkill(w, r)
	if !ok {
		return
	}
	tree, err := newSkillService(db).GetTree(r.Context(), skillservice.TreeRef{SkillID: skillID, RefType: "head"})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, serviceTreeDTO(tree))
}

func File(w http.ResponseWriter, r *http.Request) {
	readFile(w, r, false)
}

func readFile(w http.ResponseWriter, r *http.Request, notFoundAsOK bool) {
	db, skillID, _, ok := requireOwnedSkill(w, r)
	if !ok {
		return
	}
	filePath := strings.TrimSpace(r.URL.Query().Get("path"))
	if filePath == "" {
		replyError(w, "path required", http.StatusBadRequest)
		return
	}
	file, err := newSkillService(db).ReadFile(r.Context(), skillservice.FileRef{SkillID: skillID, RefType: "head", Path: filePath})
	if err != nil {
		if notFoundAsOK && isReadFileNotFound(err) {
			replyServiceErrorOK(w, err)
			return
		}
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, fileContentDTO(file))
}

func FSList(w http.ResponseWriter, r *http.Request) {
	db, skillID, _, ok := requireOwnedSkill(w, r)
	if !ok {
		return
	}
	tree, err := newSkillService(db).GetTree(r.Context(), skillservice.TreeRef{SkillID: skillID, RefType: "head"})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	dirPath := strings.Trim(strings.TrimSpace(r.URL.Query().Get("path")), "/")
	node := findServiceTreeNode(tree, dirPath)
	if node == nil || node.Type != "dir" {
		replyError(w, "path not found", http.StatusNotFound)
		return
	}
	items := make([]map[string]any, 0, len(node.Children))
	for _, child := range node.Children {
		items = append(items, serviceTreeDTO(child))
	}
	common.ReplyOK(w, map[string]any{"items": items})
}

func FSInfo(w http.ResponseWriter, r *http.Request) {
	db, skillID, _, ok := requireOwnedSkill(w, r)
	if !ok {
		return
	}
	tree, err := newSkillService(db).GetTree(r.Context(), skillservice.TreeRef{SkillID: skillID, RefType: "head"})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	node := findServiceTreeNode(tree, strings.Trim(strings.TrimSpace(r.URL.Query().Get("path")), "/"))
	if node == nil {
		replyError(w, "path not found", http.StatusNotFound)
		return
	}
	common.ReplyOK(w, serviceTreeDTO(*node))
}

func FSExists(w http.ResponseWriter, r *http.Request) {
	db, skillID, _, ok := requireOwnedSkill(w, r)
	if !ok {
		return
	}
	tree, err := newSkillService(db).GetTree(r.Context(), skillservice.TreeRef{SkillID: skillID, RefType: "head"})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	node := findServiceTreeNode(tree, strings.Trim(strings.TrimSpace(r.URL.Query().Get("path")), "/"))
	common.ReplyOK(w, map[string]any{"exists": node != nil})
}

func FSContent(w http.ResponseWriter, r *http.Request) {
	readFile(w, r, true)
}

func FSDownload(w http.ResponseWriter, r *http.Request) {
	File(w, r)
}

func DraftExists(w http.ResponseWriter, r *http.Request) {
	db, skillID, _, ok := requireOwnedSkill(w, r)
	if !ok {
		return
	}
	state, err := skillfs.NewDraftStore(skillfs.DraftStoreDeps{DB: db}).HasUncommittedDraft(r.Context(), skillID)
	if err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, draftStateDTO(state))
}

func DraftStatus(w http.ResponseWriter, r *http.Request) {
	db, skillID, userID, ok := requireOwnedSkill(w, r)
	if !ok {
		return
	}
	state, err := newRevisionService(db).DraftStatus(r.Context(), skillrevision.DraftStatusRequest{SkillID: skillID, UserID: userID})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{
		"base_revision_id":      state.BaseRevisionID,
		"task_id":               state.TaskID,
		"conversation_id":       state.ConversationID,
		"draft_version":         state.DraftVersion,
		"has_uncommitted_draft": state.HasUncommittedDraft,
		"overlay_count":         state.OverlayCount,
	})
}

func DraftWriteText(w http.ResponseWriter, r *http.Request) {
	db, skillID, userID, ok := requireOwnedSkill(w, r)
	if !ok {
		return
	}
	if !ensureUserDraftWriteAllowed(w, r, db, userID, skillID) {
		return
	}
	var req struct {
		Path                 string `json:"path"`
		Content              string `json:"content"`
		ExpectedDraftVersion int64  `json:"expected_draft_version"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.ExpectedDraftVersion <= 0 {
		replyError(w, "expected_draft_version required", http.StatusBadRequest)
		return
	}
	resp, err := newDraftFS(db).WriteText(r.Context(), skillfs.WriteTextRequest{
		SkillID:              skillID,
		Path:                 req.Path,
		Content:              req.Content,
		ExpectedDraftVersion: req.ExpectedDraftVersion,
		UserID:               userID,
	})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{"draft_version": resp.DraftVersion, "blob_hash": resp.BlobHash})
}

func DraftUpload(w http.ResponseWriter, r *http.Request) {
	db, skillID, userID, ok := requireOwnedSkill(w, r)
	if !ok {
		return
	}
	if !ensureUserDraftWriteAllowed(w, r, db, userID, skillID) {
		return
	}
	var req struct {
		Path                 string `json:"path"`
		UploadID             string `json:"upload_id"`
		ExpectedDraftVersion int64  `json:"expected_draft_version"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.ExpectedDraftVersion <= 0 {
		replyError(w, "expected_draft_version required", http.StatusBadRequest)
		return
	}
	session, err := dbUploadStore{db: db}.Get(r.Context(), req.UploadID)
	if err != nil {
		replyServiceError(w, err)
		return
	}
	if session.OwnerUserID != userID {
		replyError(w, "upload belongs to another user", http.StatusForbidden)
		return
	}
	if session.State != "completed" {
		replyError(w, "upload is not completed", http.StatusBadRequest)
		return
	}
	data, err := os.ReadFile(session.StoredPath)
	if err != nil {
		replyServiceError(w, err)
		return
	}
	resp, err := newDraftFS(db).WriteFile(r.Context(), skillfs.WriteFileRequest{
		SkillID:              skillID,
		Path:                 req.Path,
		Data:                 data,
		ExpectedDraftVersion: req.ExpectedDraftVersion,
		UserID:               userID,
	})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{"draft_version": resp.DraftVersion, "blob_hash": resp.BlobHash})
}

func DraftMkdir(w http.ResponseWriter, r *http.Request) {
	db, skillID, userID, ok := requireOwnedSkill(w, r)
	if !ok {
		return
	}
	if !ensureUserDraftWriteAllowed(w, r, db, userID, skillID) {
		return
	}
	var req struct {
		Path                 string `json:"path"`
		ExpectedDraftVersion int64  `json:"expected_draft_version"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.ExpectedDraftVersion <= 0 {
		replyError(w, "expected_draft_version required", http.StatusBadRequest)
		return
	}
	resp, err := newDraftFS(db).Mkdir(r.Context(), skillfs.MkdirRequest{SkillID: skillID, Path: req.Path, ExpectedDraftVersion: req.ExpectedDraftVersion, UserID: userID})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{"draft_version": resp.DraftVersion})
}

func DraftDeletePath(w http.ResponseWriter, r *http.Request) {
	db, skillID, userID, ok := requireOwnedSkill(w, r)
	if !ok {
		return
	}
	if !ensureUserDraftWriteAllowed(w, r, db, userID, skillID) {
		return
	}
	var req struct {
		Path                 string `json:"path"`
		Recursive            bool   `json:"recursive"`
		ExpectedDraftVersion int64  `json:"expected_draft_version"`
	}
	if err := decodeOptionalJSON(r, &req); err != nil {
		replyError(w, "invalid body", http.StatusBadRequest)
		return
	}
	req.Path = firstNonEmpty(req.Path, r.URL.Query().Get("path"))
	if req.ExpectedDraftVersion <= 0 {
		req.ExpectedDraftVersion = int64Query(r, "expected_draft_version", 0)
	}
	if req.ExpectedDraftVersion <= 0 {
		replyError(w, "expected_draft_version required", http.StatusBadRequest)
		return
	}
	resp, err := newDraftFS(db).Delete(r.Context(), skillfs.DeleteRequest{
		SkillID:              skillID,
		Path:                 req.Path,
		Recursive:            req.Recursive,
		ExpectedDraftVersion: req.ExpectedDraftVersion,
		UserID:               userID,
	})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{"draft_version": resp.DraftVersion})
}

func DraftMove(w http.ResponseWriter, r *http.Request) {
	db, skillID, userID, ok := requireOwnedSkill(w, r)
	if !ok {
		return
	}
	if !ensureUserDraftWriteAllowed(w, r, db, userID, skillID) {
		return
	}
	var req struct {
		From                 string `json:"from"`
		To                   string `json:"to"`
		ExpectedDraftVersion int64  `json:"expected_draft_version"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.ExpectedDraftVersion <= 0 {
		replyError(w, "expected_draft_version required", http.StatusBadRequest)
		return
	}
	resp, err := newDraftFS(db).Move(r.Context(), skillfs.MoveRequest{
		SkillID:              skillID,
		From:                 req.From,
		To:                   req.To,
		ExpectedDraftVersion: req.ExpectedDraftVersion,
		UserID:               userID,
	})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{"draft_version": resp.DraftVersion})
}

func Commit(w http.ResponseWriter, r *http.Request) {
	db, skillID, userID, ok := requireOwnedSkill(w, r)
	if !ok {
		return
	}
	if !ensureUserDraftWriteAllowed(w, r, db, userID, skillID) {
		return
	}
	var req struct {
		DraftVersion int64 `json:"draft_version"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.DraftVersion <= 0 {
		replyError(w, "draft_version required", http.StatusBadRequest)
		return
	}
	resp, err := newRevisionService(db).CommitDraft(r.Context(), skillrevision.CommitDraftRequest{SkillID: skillID, UserID: userID, DraftVersion: req.DraftVersion})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{"revision_id": resp.RevisionID, "revision_no": resp.RevisionNo})
}

func ListRevisions(w http.ResponseWriter, r *http.Request) {
	db, skillID, userID, ok := requireOwnedSkill(w, r)
	if !ok {
		return
	}
	resp, err := newRevisionService(db).ListRevisions(r.Context(), skillrevision.ListRevisionsRequest{SkillID: skillID, UserID: userID})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	items := make([]map[string]any, 0, len(resp.Items))
	for _, item := range resp.Items {
		items = append(items, revisionDTO(item))
	}
	common.ReplyOK(w, map[string]any{"items": items})
}

func GetRevision(w http.ResponseWriter, r *http.Request) {
	db, skillID, userID, revisionID, ok := requireOwnedRevision(w, r)
	if !ok {
		return
	}
	resp, err := newRevisionService(db).GetRevision(r.Context(), skillrevision.GetRevisionRequest{SkillID: skillID, UserID: userID, RevisionID: revisionID})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, revisionDTO(resp))
}

func GetRevisionTree(w http.ResponseWriter, r *http.Request) {
	db, skillID, userID, revisionID, ok := requireOwnedRevision(w, r)
	if !ok {
		return
	}
	resp, err := newRevisionService(db).GetRevisionTree(r.Context(), skillrevision.GetRevisionTreeRequest{SkillID: skillID, UserID: userID, RevisionID: revisionID})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, revisionTreeDTO(resp))
}

func ReadRevisionFile(w http.ResponseWriter, r *http.Request) {
	db, skillID, _, revisionID, ok := requireOwnedRevision(w, r)
	if !ok {
		return
	}
	filePath := strings.TrimSpace(r.URL.Query().Get("path"))
	if filePath == "" {
		replyError(w, "path required", http.StatusBadRequest)
		return
	}
	resp, err := newRevisionService(db).ReadRevisionFile(r.Context(), skillrevision.ReadRevisionFileRequest{SkillID: skillID, RevisionID: revisionID, Path: filePath})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, revisionFileDTO(resp))
}

func RollbackPreview(w http.ResponseWriter, r *http.Request) {
	db, skillID, userID, ok := requireOwnedSkill(w, r)
	if !ok {
		return
	}
	revisionID, ok := targetRevisionID(w, r)
	if !ok {
		return
	}
	resp, err := newRevisionService(db).RollbackPreview(r.Context(), skillrevision.RollbackPreviewRequest{SkillID: skillID, UserID: userID, TargetRevisionID: revisionID})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	files := make([]map[string]any, 0, len(resp.TreeDiff.Files))
	for _, file := range resp.TreeDiff.Files {
		files = append(files, map[string]any{"path": file.Path, "status": file.Status})
	}
	warnings := make([]map[string]any, 0, len(resp.Warnings))
	for _, warning := range resp.Warnings {
		warnings = append(warnings, map[string]any{"code": warning.Code, "message": warning.Message})
	}
	common.ReplyOK(w, map[string]any{"tree_diff": map[string]any{"files": files}, "warnings": warnings})
}

func Rollback(w http.ResponseWriter, r *http.Request) {
	db, skillID, userID, ok := requireOwnedSkill(w, r)
	if !ok {
		return
	}
	revisionID, ok := targetRevisionID(w, r)
	if !ok {
		return
	}
	resp, err := newRevisionService(db).Rollback(r.Context(), skillrevision.RollbackRequest{SkillID: skillID, UserID: userID, TargetRevisionID: revisionID})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{"head_revision_id": resp.NewHeadRevisionID, "revision_no": resp.RevisionNo})
}

func DeleteRevision(w http.ResponseWriter, r *http.Request) {
	db, skillID, userID, revisionID, ok := requireOwnedRevision(w, r)
	if !ok {
		return
	}
	if err := newRevisionService(db).DeleteRevision(r.Context(), skillrevision.DeleteRevisionRequest{SkillID: skillID, UserID: userID, RevisionID: revisionID}); err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{"deleted": true})
}

func DiffTree(w http.ResponseWriter, r *http.Request) {
	db, userID, oldFS, newFS, opts, _, ok := resolveDiffRequest(w, r)
	_ = db
	if !ok {
		return
	}
	resp, err := skilldiff.NewService(skilldiff.ServiceDeps{}).Compare(r.Context(), oldFS, newFS, opts)
	if err != nil {
		replyServiceError(w, err)
		return
	}
	files := make([]map[string]any, 0, len(resp.Files))
	for _, file := range resp.Files {
		files = append(files, diffFileDTO(file))
	}
	common.ReplyOK(w, map[string]any{"user_id": userID, "files": files, "cache_written": resp.CacheWritten})
}

func DiffFile(w http.ResponseWriter, r *http.Request) {
	db, userID, oldFS, newFS, opts, diffReq, ok := resolveDiffRequest(w, r)
	if !ok {
		return
	}
	if strings.TrimSpace(opts.Path) == "" {
		replyError(w, "path required", http.StatusBadRequest)
		return
	}
	resp, err := skilldiff.NewService(skilldiff.ServiceDeps{}).CompareFile(r.Context(), oldFS, newFS, opts)
	if err != nil {
		replyServiceError(w, err)
		return
	}
	if isDraftReviewDiff(diffReq) && strings.TrimSpace(opts.Mode) == "" && skillHasHeadRevision(r.Context(), db, diffReq.New.SkillID) {
		resp, err = newReviewService(db).PrepareFile(r.Context(), skillreview.PrepareFileRequest{
			SkillID: strings.TrimSpace(diffReq.New.SkillID),
			UserID:  userID,
			File:    resp,
		})
		if err != nil {
			replyServiceError(w, err)
			return
		}
	}
	common.ReplyOK(w, diffFileDTO(resp))
}

func skillHasHeadRevision(ctx context.Context, db *gorm.DB, skillID string) bool {
	var row struct {
		HeadRevisionID *string `gorm:"column:head_revision_id"`
	}
	if err := db.WithContext(ctx).Table("skills").Select("head_revision_id").Where("id = ? AND deleted_at IS NULL", strings.TrimSpace(skillID)).Take(&row).Error; err != nil {
		return false
	}
	return row.HeadRevisionID != nil && strings.TrimSpace(*row.HeadRevisionID) != ""
}

func DraftReviewAction(w http.ResponseWriter, r *http.Request) {
	db, skillID, userID, ok := requireOwnedSkill(w, r)
	if !ok {
		return
	}
	reviewID := strings.TrimSpace(common.PathVar(r, "review_id"))
	if reviewID == "" {
		replyError(w, "missing review_id", http.StatusBadRequest)
		return
	}
	var req struct {
		ExpectedReviewVersion int64 `json:"expected_review_version"`
		Items                 []struct {
			Path     string `json:"path"`
			HunkID   string `json:"hunk_id"`
			Decision string `json:"decision"`
		} `json:"items"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	items := make([]skillreview.ActionItem, 0, len(req.Items))
	for _, item := range req.Items {
		items = append(items, skillreview.ActionItem{Path: strings.TrimSpace(item.Path), HunkID: strings.TrimSpace(item.HunkID), Decision: strings.TrimSpace(item.Decision)})
	}
	resp, err := newReviewService(db).Action(r.Context(), skillreview.ActionRequest{
		SkillID:               skillID,
		UserID:                userID,
		ReviewID:              reviewID,
		ExpectedReviewVersion: req.ExpectedReviewVersion,
		Items:                 items,
	})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{"review_version": resp.ReviewVersion, "batch_id": resp.BatchID, "can_undo": resp.CanUndo})
}

func DraftReviewUndo(w http.ResponseWriter, r *http.Request) {
	db, skillID, userID, ok := requireOwnedSkill(w, r)
	if !ok {
		return
	}
	reviewID := strings.TrimSpace(common.PathVar(r, "review_id"))
	if reviewID == "" {
		replyError(w, "missing review_id", http.StatusBadRequest)
		return
	}
	var req struct {
		ExpectedReviewVersion int64 `json:"expected_review_version"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := newReviewService(db).Undo(r.Context(), skillreview.UndoRequest{
		SkillID:               skillID,
		UserID:                userID,
		ReviewID:              reviewID,
		ExpectedReviewVersion: req.ExpectedReviewVersion,
	})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	items := make([]map[string]any, 0, len(resp.Items))
	for _, item := range resp.Items {
		items = append(items, map[string]any{"path": item.Path, "hunk_id": item.HunkID, "decision": item.Decision})
	}
	common.ReplyOK(w, map[string]any{"review_version": resp.ReviewVersion, "undone_batch_id": resp.UndoneBatchID, "items": items, "can_undo": resp.CanUndo})
}

func DraftReviewCommit(w http.ResponseWriter, r *http.Request) {
	db, skillID, userID, ok := requireOwnedSkill(w, r)
	if !ok {
		return
	}
	reviewID := strings.TrimSpace(common.PathVar(r, "review_id"))
	if reviewID == "" {
		replyError(w, "missing review_id", http.StatusBadRequest)
		return
	}
	var req struct {
		ExpectedReviewVersion int64 `json:"expected_review_version"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := newReviewService(db).Commit(r.Context(), skillreview.CommitRequest{
		SkillID:               skillID,
		UserID:                userID,
		ReviewID:              reviewID,
		ExpectedReviewVersion: req.ExpectedReviewVersion,
	})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{"revision_id": resp.RevisionID, "revision_no": resp.RevisionNo})
}

func RemoteFSList(w http.ResponseWriter, r *http.Request) {
	newRemoteFSHandler().List(w, remoteRequestWithHeaderUser(r))
}

func RemoteFSInfo(w http.ResponseWriter, r *http.Request) {
	newRemoteFSHandler().Info(w, remoteRequestWithHeaderUser(r))
}

func RemoteFSExists(w http.ResponseWriter, r *http.Request) {
	newRemoteFSHandler().Exists(w, remoteRequestWithHeaderUser(r))
}

func RemoteFSContent(w http.ResponseWriter, r *http.Request) {
	newRemoteFSHandler().Content(w, remoteRequestWithHeaderUser(r))
}

func RemoteFSDir(w http.ResponseWriter, r *http.Request) {
	newRemoteFSHandler().Dir(w, remoteRequestWithHeaderUser(r))
}

func RemoteFSDelete(w http.ResponseWriter, r *http.Request) {
	newRemoteFSHandler().DeletePath(w, remoteRequestWithHeaderUser(r))
}

func RemoteFSCopy(w http.ResponseWriter, r *http.Request) {
	newRemoteFSHandler().Copy(w, remoteRequestWithHeaderUser(r))
}

func RemoteFSMove(w http.ResponseWriter, r *http.Request) {
	newRemoteFSHandler().Move(w, remoteRequestWithHeaderUser(r))
}

func RemoteFSTrash(w http.ResponseWriter, r *http.Request) {
	newRemoteFSHandler().Trash(w, remoteRequestWithHeaderUser(r))
}

func MarketInstall(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	userID, userName, ok := requireUser(w, r)
	if !ok {
		return
	}
	marketItemID := firstNonEmpty(common.PathVar(r, "market_item_id"), common.PathVar(r, "item_id"))
	if marketItemID == "" {
		var req struct {
			MarketItemID string `json:"market_item_id"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		marketItemID = strings.TrimSpace(req.MarketItemID)
	}
	resp, err := newMarketService(db).Install(r.Context(), skillmarket.InstallRequest{MarketItemID: marketItemID, UserID: userID, UserName: userName})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{"skill_id": resp.SkillID})
}

func MarketPublish(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	userID, _, ok := requireUser(w, r)
	if !ok {
		return
	}
	var req struct {
		Name     string             `json:"name"`
		Tags     []string           `json:"tags"`
		Category string             `json:"category"`
		Source   skillSourceRequest `json:"source"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	source := skillmarket.SourceInput{Type: strings.TrimSpace(req.Source.Type), UploadID: strings.TrimSpace(req.Source.UploadID)}
	if source.UploadID != "" {
		session, err := dbUploadStore{db: db}.Get(r.Context(), source.UploadID)
		if err != nil {
			replyServiceError(w, err)
			return
		}
		if session.OwnerUserID != userID {
			replyError(w, "upload belongs to another user", http.StatusForbidden)
			return
		}
		if session.State != "completed" {
			replyError(w, "upload is not completed", http.StatusBadRequest)
			return
		}
		source.StoredPath = session.StoredPath
		source.Filename = session.Filename
	}
	resp, err := newMarketService(db).Publish(r.Context(), skillmarket.PublishRequest{
		AdminUserID: userID,
		Name:        strings.TrimSpace(req.Name),
		Tags:        marketRequestTags(req.Tags, req.Category),
		Source:      source,
	})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{"market_item_id": resp.MarketItemID, "source_skill_id": resp.SourceSkillID})
}

func MarketEdit(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	userID, _, ok := requireUser(w, r)
	if !ok {
		return
	}
	var req struct {
		Name        *string             `json:"name"`
		Tags        *[]string           `json:"tags"`
		Category    *string             `json:"category"`
		Description *string             `json:"description"`
		Source      *skillSourceRequest `json:"source"`
		VersionNote *string             `json:"version_note"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	marketItemID := firstNonEmpty(common.PathVar(r, "market_item_id"), common.PathVar(r, "item_id"))
	if req.Name != nil || req.Description != nil || req.Source != nil {
		var item orm.SkillMarketItem
		if err := db.WithContext(r.Context()).Where("id = ?", marketItemID).Take(&item).Error; err != nil {
			replyServiceError(w, err)
			return
		}
		var sourceSkill orm.SkillV2Skill
		if err := db.WithContext(r.Context()).Select("owner_user_id").Where("id = ? AND deleted_at IS NULL", item.SourceSkillID).Take(&sourceSkill).Error; err != nil {
			replyServiceError(w, err)
			return
		}
		name := trimStringPtr(req.Name)
		description := trimStringPtr(req.Description)
		if name != nil && *name == "" {
			replyError(w, "name cannot be empty", http.StatusBadRequest)
			return
		}
		if name != nil {
			if err := newMarketService(db).ValidateNameAvailable(r.Context(), *name, marketItemID); err != nil {
				replyServiceError(w, err)
				return
			}
		}
		var source *skillservice.SourceInput
		if req.Source != nil {
			converted, err := req.Source.toServiceSource()
			if err != nil {
				replyError(w, err.Error(), http.StatusBadRequest)
				return
			}
			source = &converted
		}
		if _, err := newSkillService(db).PatchSkill(r.Context(), skillservice.PatchSkillRequest{
			SkillID:     item.SourceSkillID,
			UserID:      sourceSkill.OwnerUserID,
			Name:        name,
			Description: description,
			Source:      source,
		}); err != nil {
			replyServiceError(w, err)
			return
		}
	}
	tags := req.Tags
	if tags == nil && req.Category != nil {
		legacyCategory := strings.TrimSpace(*req.Category)
		if legacyCategory == "" {
			replyError(w, "category cannot be empty", http.StatusBadRequest)
			return
		}
		legacyTags := []string{legacyCategory}
		tags = &legacyTags
	}
	if req.VersionNote != nil || tags != nil {
		resp, err := newMarketService(db).Edit(r.Context(), skillmarket.EditRequest{AdminUserID: userID, MarketItemID: marketItemID, VersionNote: req.VersionNote, Tags: tags})
		if err != nil {
			replyServiceError(w, err)
			return
		}
		common.ReplyOK(w, map[string]any{"market_item_id": resp.MarketItemID})
		return
	}
	common.ReplyOK(w, map[string]any{"market_item_id": marketItemID})
}

func marketRequestTags(tags []string, legacyCategory string) []string {
	if normalized := compactStrings(tags); len(normalized) > 0 {
		return normalized
	}
	if category := strings.TrimSpace(legacyCategory); category != "" {
		return []string{category}
	}
	return []string{}
}

func MarketUnpublish(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	userID, _, ok := requireUser(w, r)
	if !ok {
		return
	}
	marketItemID := firstNonEmpty(common.PathVar(r, "market_item_id"), common.PathVar(r, "item_id"))
	resp, err := newMarketService(db).Unpublish(r.Context(), skillmarket.UnpublishRequest{AdminUserID: userID, MarketItemID: marketItemID})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{"market_item_id": resp.MarketItemID})
}

func Share(w http.ResponseWriter, r *http.Request) {
	db, skillID, userID, ok := requireOwnedSkill(w, r)
	if !ok {
		return
	}
	var req shareSkillRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	var source orm.SkillV2Skill
	if err := db.WithContext(r.Context()).Where("id = ? AND owner_user_id = ? AND deleted_at IS NULL", skillID, userID).Take(&source).Error; err != nil {
		replyServiceError(w, err)
		return
	}
	targets := expandShareTargets(r, req, userID)
	if len(targets) == 0 {
		replyError(w, "no target users to share", http.StatusBadRequest)
		return
	}
	now := time.Now()
	targetUserNames := common.FetchUserNamesFromAuthService(r, targets)
	task := orm.SkillShareTask{
		ID:                    evolution.NewID(),
		SourceUserID:          userID,
		SourceUserName:        strings.TrimSpace(store.UserName(r)),
		SourceSkillID:         source.ID,
		SourceCategory:        source.Category,
		SourceParentSkillName: source.SkillName,
		SourceRelativeRoot:    firstNonEmpty(source.RelativeRoot, source.Category+"/"+source.SkillName),
		Message:               strings.TrimSpace(req.Message),
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	items := make([]orm.SkillShareItem, 0, len(targets))
	for _, target := range targets {
		items = append(items, orm.SkillShareItem{
			ID:             evolution.NewID(),
			ShareTaskID:    task.ID,
			SourceSkillID:  source.ID,
			TargetUserID:   target,
			TargetUserName: firstNonEmpty(targetUserNames[target], target),
			Status:         shareStatusPendingAccept,
			CreatedAt:      now,
			UpdatedAt:      now,
		})
	}
	if err := db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&task).Error; err != nil {
			return err
		}
		return tx.Create(&items).Error
	}); err != nil {
		replyServiceError(w, err)
		return
	}
	respItems := make([]map[string]any, 0, len(items))
	for _, item := range items {
		respItems = append(respItems, shareCreateItemDTO(item))
	}
	common.ReplyOK(w, map[string]any{"share_task_id": task.ID, "items": respItems})
}

func ListShareTargets(w http.ResponseWriter, r *http.Request) {
	db, skillID, userID, ok := requireOwnedSkill(w, r)
	if !ok {
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	page := positiveQueryInt(r, "page", 1)
	pageSize := positiveQueryInt(r, "page_size", 20)
	if pageSize > 100 {
		pageSize = 100
	}
	var tasks []orm.SkillShareTask
	if err := db.WithContext(r.Context()).Where("source_user_id = ? AND source_skill_id = ?", userID, skillID).Find(&tasks).Error; err != nil {
		replyServiceError(w, err)
		return
	}
	if len(tasks) == 0 {
		common.ReplyOK(w, map[string]any{"skill_id": skillID, "status_summary": shareStatusSummaryDTO(nil), "items": []any{}, "page": page, "page_size": pageSize, "total": 0})
		return
	}
	taskIDs := make([]string, 0, len(tasks))
	taskMap := make(map[string]orm.SkillShareTask, len(tasks))
	for _, task := range tasks {
		taskIDs = append(taskIDs, task.ID)
		taskMap[task.ID] = task
	}
	var items []orm.SkillShareItem
	if err := db.WithContext(r.Context()).Where("share_task_id IN ?", taskIDs).Order("created_at DESC").Order("updated_at DESC").Find(&items).Error; err != nil {
		replyServiceError(w, err)
		return
	}
	latest := map[string]shareRecord{}
	for _, item := range items {
		record := shareRecord{item: item, task: taskMap[item.ShareTaskID]}
		existing, ok := latest[item.TargetUserID]
		if ok && !shareItemNewer(item, existing.item) {
			continue
		}
		latest[item.TargetUserID] = record
	}
	records := make([]shareRecord, 0, len(latest))
	allLatest := make([]shareRecord, 0, len(latest))
	for _, record := range latest {
		allLatest = append(allLatest, record)
		if status != "" && record.item.Status != status {
			continue
		}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		return shareItemNewer(records[i].item, records[j].item)
	})
	total := len(records)
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	respItems := make([]map[string]any, 0, end-start)
	for _, record := range records[start:end] {
		respItems = append(respItems, shareTargetDTO(record))
	}
	common.ReplyOK(w, map[string]any{
		"skill_id":       skillID,
		"status_summary": shareStatusSummaryDTO(allLatest),
		"items":          respItems,
		"page":           page,
		"page_size":      pageSize,
		"total":          total,
	})
}

func IncomingShares(w http.ResponseWriter, r *http.Request) {
	listShareItems(w, r, true)
}

func OutgoingShares(w http.ResponseWriter, r *http.Request) {
	listShareItems(w, r, false)
}

func GetShareItem(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	userID, _, ok := requireUser(w, r)
	if !ok {
		return
	}
	item, task, ok := loadShareItem(w, r, db)
	if !ok {
		return
	}
	if item.TargetUserID != userID && task.SourceUserID != userID {
		replyError(w, "forbidden", http.StatusForbidden)
		return
	}
	sourceSkillID := firstNonEmpty(item.SourceSkillID, task.SourceSkillID)
	detail, err := newSkillService(db).GetSkill(r.Context(), skillservice.GetSkillRequest{SkillID: sourceSkillID, UserID: task.SourceUserID})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{
		"share_item_id": item.ID,
		"status":        item.Status,
		"message":       task.Message,
		"source":        skillDetailDTO(detail),
	})
}

func AcceptShare(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	userID, userName, ok := requireUser(w, r)
	if !ok {
		return
	}
	shareItemID := common.PathVar(r, "share_item_id")
	resp, err := newShareService(db).Accept(r.Context(), skillshare.AcceptRequest{ShareItemID: shareItemID, UserID: userID, UserName: userName})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{"accepted": true, "target_root_skill_id": resp.TargetSkillID, "target_skill_id": resp.TargetSkillID})
}

func RejectShare(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	userID, _, ok := requireUser(w, r)
	if !ok {
		return
	}
	item, _, ok := loadShareItem(w, r, db)
	if !ok {
		return
	}
	if item.TargetUserID != userID {
		replyError(w, "forbidden", http.StatusForbidden)
		return
	}
	if item.Status != "pending" && item.Status != shareStatusPendingAccept {
		replyError(w, "share item is not pending", http.StatusConflict)
		return
	}
	now := time.Now()
	updates := map[string]any{
		"status":     shareStatusRejected,
		"updated_at": now,
	}
	if db.Migrator().HasColumn(&orm.SkillShareItem{}, "rejected_at") {
		updates["rejected_at"] = now
	}
	if err := db.WithContext(r.Context()).Model(&orm.SkillShareItem{}).Where("id = ?", item.ID).Updates(updates).Error; err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{"rejected": true})
}

type internalCreateRequest struct {
	SessionID string `json:"session_id"`
	Category  string `json:"category"`
	SkillName string `json:"skill_name"`
	Content   string `json:"content"`
}

func InternalCreate(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	var req internalCreateRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.Category = strings.TrimSpace(req.Category)
	req.SkillName = strings.TrimSpace(req.SkillName)
	if req.SessionID == "" || req.Category == "" || req.SkillName == "" || strings.TrimSpace(req.Content) == "" {
		replyError(w, "session_id/category/skill_name/content required", http.StatusBadRequest)
		return
	}
	userID, userName, err := evolution.ResolveSessionUser(r.Context(), db, req.SessionID)
	if err != nil || strings.TrimSpace(userID) == "" {
		replyError(w, "unable to resolve session user", http.StatusBadRequest)
		return
	}
	zipPath, err := writeInlineSkillZip(req.Content)
	if err != nil {
		replyServiceError(w, err)
		return
	}
	defer os.Remove(zipPath)
	resp, err := newSkillService(db).CreateSkill(r.Context(), skillservice.CreateSkillRequest{
		OwnerUserID:    userID,
		OwnerUserName:  userName,
		CreateUserID:   userID,
		CreateUserName: userName,
		Name:           req.SkillName,
		Category:       req.Category,
		Source:         skillservice.SourceInput{Type: "local_zip", StoredPath: zipPath, Filename: "internal-create.zip"},
	})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{"skill_id": resp.SkillID, "head_revision_id": resp.HeadRevisionID})
}

func (s skillSourceRequest) toServiceSource() (skillservice.SourceInput, error) {
	sourceType := strings.TrimSpace(s.Type)
	if sourceType == "" {
		if strings.TrimSpace(s.UploadID) != "" {
			sourceType = "uploaded_zip"
		} else if strings.TrimSpace(s.URL) != "" {
			sourceType = "url"
		}
	}
	switch strings.ToLower(sourceType) {
	case "uploaded", "upload", "uploaded_zip":
		if strings.TrimSpace(s.UploadID) == "" {
			return skillservice.SourceInput{}, fmt.Errorf("upload_id required")
		}
		return skillservice.SourceInput{Type: "uploaded_zip", UploadID: strings.TrimSpace(s.UploadID), Filename: strings.TrimSpace(s.Filename)}, nil
	case "url":
		if strings.TrimSpace(s.URL) == "" {
			return skillservice.SourceInput{}, fmt.Errorf("url required")
		}
		return skillservice.SourceInput{Type: "url", URL: strings.TrimSpace(s.URL)}, nil
	default:
		return skillservice.SourceInput{}, fmt.Errorf("unsupported source type %q", sourceType)
	}
}

func createSkillSourceFromRequest(name, category, description, content string, children []legacyChildSkillInput, sourceReq skillSourceRequest) (skillservice.SourceInput, func(), error) {
	if strings.TrimSpace(content) == "" && len(children) == 0 {
		source, err := sourceReq.toServiceSource()
		return source, nil, err
	}
	files, err := legacySkillFiles(name, category, description, content, children)
	if err != nil {
		return skillservice.SourceInput{}, nil, err
	}
	zipPath, err := writeSkillPackageZip(files)
	if err != nil {
		return skillservice.SourceInput{}, nil, err
	}
	cleanup := func() { _ = os.Remove(zipPath) }
	return skillservice.SourceInput{Type: "local_zip", StoredPath: zipPath, Filename: "legacy-create.zip"}, cleanup, nil
}

func legacySkillFiles(name, category, description, content string, children []legacyChildSkillInput) (map[string][]byte, error) {
	skillMD, _, err := legacySkillMD(name, category, description, content)
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{"SKILL.md": []byte(skillMD)}
	for _, child := range children {
		childName := strings.TrimSpace(child.Name)
		if childName == "" {
			return nil, fmt.Errorf("child name required")
		}
		relPath := legacyChildPath(childName, child.FileExt)
		if relPath == "SKILL.md" {
			return nil, fmt.Errorf("child path conflicts with SKILL.md")
		}
		if _, exists := files[relPath]; exists {
			return nil, fmt.Errorf("duplicate child path %s", relPath)
		}
		files[relPath] = []byte(child.Content)
	}
	return files, nil
}

func legacySkillMD(name, category, description, content string) (string, string, error) {
	name = strings.TrimSpace(name)
	category = strings.TrimSpace(category)
	description = strings.TrimSpace(description)
	content = strings.TrimSpace(content)
	if content == "" {
		return "", "", fmt.Errorf("content required")
	}
	if meta, body, ok := parseSkillMDFrontmatter(content); ok {
		metaName := strings.TrimSpace(meta.Name)
		if metaName != "" && metaName != name {
			return "", "", fmt.Errorf("request name and frontmatter name must match")
		}
		metaCategory := strings.TrimSpace(meta.Category)
		if metaCategory != "" && metaCategory != category {
			return "", "", fmt.Errorf("request category and frontmatter category must match")
		}
		resolvedDescription := firstNonEmpty(strings.TrimSpace(meta.Description), description)
		if description != "" && strings.TrimSpace(meta.Description) != "" && strings.TrimSpace(meta.Description) != description {
			return "", "", fmt.Errorf("request description and frontmatter description must match")
		}
		if strings.TrimSpace(body) == "" {
			return "", "", fmt.Errorf("content required")
		}
		if resolvedDescription == "" {
			return "", "", fmt.Errorf("description required")
		}
		return content, resolvedDescription, nil
	}
	if description == "" {
		return "", "", fmt.Errorf("description required")
	}
	frontmatter, err := yaml.Marshal(skillMDFrontmatter{Name: name, Category: category, Description: description})
	if err != nil {
		return "", "", err
	}
	return fmt.Sprintf("---\n%s---\n%s", string(frontmatter), content), description, nil
}

func legacyChildPath(name, fileExt string) string {
	name = strings.Trim(strings.TrimSpace(name), "/")
	if path.Ext(name) != "" {
		return filepath.ToSlash(name)
	}
	ext := strings.TrimSpace(strings.TrimPrefix(fileExt, "."))
	if ext == "" {
		ext = "md"
	}
	return filepath.ToSlash(name + "." + strings.ToLower(ext))
}

func patchContentSourceFromHead(ctx context.Context, db *gorm.DB, skillID string, name, category, description *string, content string) (skillservice.SourceInput, func(), error) {
	files, row, err := skillHeadFiles(ctx, db, skillID)
	if err != nil {
		return skillservice.SourceInput{}, nil, err
	}
	nextName := row.SkillName
	if name != nil {
		nextName = *name
	}
	nextCategory := row.Category
	if category != nil {
		nextCategory = *category
	}
	nextDescription := row.Description
	if description != nil {
		nextDescription = *description
	}
	skillMD, _, err := legacySkillMD(nextName, nextCategory, nextDescription, content)
	if err != nil {
		return skillservice.SourceInput{}, nil, err
	}
	files["SKILL.md"] = []byte(skillMD)
	zipPath, err := writeSkillPackageZip(files)
	if err != nil {
		return skillservice.SourceInput{}, nil, err
	}
	cleanup := func() { _ = os.Remove(zipPath) }
	return skillservice.SourceInput{Type: "local_zip", StoredPath: zipPath, Filename: "legacy-patch.zip"}, cleanup, nil
}

func skillHeadFiles(ctx context.Context, db *gorm.DB, skillID string) (map[string][]byte, orm.SkillV2Skill, error) {
	var skill orm.SkillV2Skill
	if err := db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", skillID).Take(&skill).Error; err != nil {
		return nil, orm.SkillV2Skill{}, err
	}
	if skill.HeadRevisionID == nil || strings.TrimSpace(*skill.HeadRevisionID) == "" {
		return nil, orm.SkillV2Skill{}, fmt.Errorf("skill has no head revision")
	}
	var entries []orm.SkillV2RevisionEntry
	if err := db.WithContext(ctx).Where("revision_id = ? AND entry_type = ?", *skill.HeadRevisionID, "file").Order("path ASC").Find(&entries).Error; err != nil {
		return nil, orm.SkillV2Skill{}, err
	}
	files := make(map[string][]byte, len(entries))
	for _, entry := range entries {
		if entry.BlobHash == nil || strings.TrimSpace(*entry.BlobHash) == "" {
			continue
		}
		var blob orm.SkillV2Blob
		if err := db.WithContext(ctx).Where("hash = ?", *entry.BlobHash).Take(&blob).Error; err != nil {
			return nil, orm.SkillV2Skill{}, err
		}
		data := blob.Content
		if blob.Binary {
			if blob.StorageKey == nil || strings.TrimSpace(*blob.StorageKey) == "" {
				return nil, orm.SkillV2Skill{}, fmt.Errorf("binary blob storage key missing for %s", entry.Path)
			}
			read, err := os.ReadFile(filepath.Join(skillObjectRoot(), filepath.FromSlash(*blob.StorageKey)))
			if err != nil {
				return nil, orm.SkillV2Skill{}, err
			}
			data = read
		}
		files[entry.Path] = data
	}
	return files, skill, nil
}

func newSkillService(db *gorm.DB) *skillservice.SkillService {
	return skillservice.NewSkillService(skillservice.SkillServiceDeps{
		DB:          db,
		UploadStore: dbUploadStore{db: db},
		Downloader:  httpZipDownloader{},
		BlobStore:   skillservice.NewBlobStore(db, skillservice.NewLocalObjectStore(skillObjectRoot())),
	})
}

func newDraftFS(db *gorm.DB) *skillfs.DraftFS {
	return skillfs.NewDraftFS(skillfs.DraftFSDeps{DB: db, BlobStore: skillfs.NewBlobStore(db, skillfs.NewLocalObjectStore(skillObjectRoot()))})
}

func newRevisionService(db *gorm.DB) *skillrevision.Service {
	return skillrevision.NewService(skillrevision.ServiceDeps{DB: db, BlobStore: skillrevision.NewBlobStore(db, skillrevision.NewLocalObjectStore(skillObjectRoot()))})
}

func newReviewService(db *gorm.DB) *skillreview.Service {
	return skillreview.NewService(skillreview.ServiceDeps{DB: db, BlobStore: skillservice.NewBlobStore(db, skillservice.NewLocalObjectStore(skillObjectRoot()))})
}

func newRemoteFSHandler() *skillremotefs.Handler {
	db := store.DB()
	return skillremotefs.NewHandler(skillremotefs.HandlerDeps{DB: db, BlobStore: skillremotefs.NewBlobStore(db, skillremotefs.NewLocalObjectStore(skillObjectRoot())), StateStore: store.State()})
}

func remoteRequestWithHeaderUser(r *http.Request) *http.Request {
	if strings.TrimSpace(r.URL.Query().Get("user_id")) != "" {
		return r
	}
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		return r
	}
	clone := r.Clone(r.Context())
	q := clone.URL.Query()
	q.Set("user_id", userID)
	clone.URL.RawQuery = q.Encode()
	return clone
}

func newMarketService(db *gorm.DB) *skillmarket.Service {
	return skillmarket.NewService(skillmarket.ServiceDeps{DB: db, BlobStore: skillmarket.NewBlobStore(db, skillmarket.NewLocalObjectStore(skillObjectRoot()))})
}

func newShareService(db *gorm.DB) *skillshare.Service {
	return skillshare.NewService(skillshare.ServiceDeps{DB: db, BlobStore: skillshare.NewBlobStore(db, skillshare.NewLocalObjectStore(skillObjectRoot()))})
}

func requireDB(w http.ResponseWriter) (*gorm.DB, bool) {
	db := store.DB()
	if db == nil {
		replyError(w, "store not initialized", http.StatusInternalServerError)
		return nil, false
	}
	return db, true
}

func requireUser(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		replyError(w, "missing X-User-Id", http.StatusBadRequest)
		return "", "", false
	}
	return userID, strings.TrimSpace(store.UserName(r)), true
}

func requireOwnedSkill(w http.ResponseWriter, r *http.Request) (*gorm.DB, string, string, bool) {
	db, ok := requireDB(w)
	if !ok {
		return nil, "", "", false
	}
	userID, _, ok := requireUser(w, r)
	if !ok {
		return nil, "", "", false
	}
	skillID := common.PathVar(r, "skill_id")
	if skillID == "" {
		replyError(w, "missing skill_id", http.StatusBadRequest)
		return nil, "", "", false
	}
	if err := ensureSkillOwner(r.Context(), db, skillID, userID); err != nil {
		replyServiceError(w, err)
		return nil, "", "", false
	}
	return db, skillID, userID, true
}

func requireSkillInTrash(w http.ResponseWriter, r *http.Request) (*gorm.DB, string, string, bool) {
	db, ok := requireDB(w)
	if !ok {
		return nil, "", "", false
	}
	userID, _, ok := requireUser(w, r)
	if !ok {
		return nil, "", "", false
	}
	skillID := common.PathVar(r, "skill_id")
	if skillID == "" {
		replyError(w, "missing skill_id", http.StatusBadRequest)
		return nil, "", "", false
	}
	var row orm.SkillV2Skill
	if err := db.WithContext(r.Context()).Select("id").Where("id = ? AND owner_user_id = ? AND deleted_at IS NOT NULL", skillID, userID).Take(&row).Error; err != nil {
		replyServiceError(w, err)
		return nil, "", "", false
	}
	return db, skillID, userID, true
}

func requireOwnedRevision(w http.ResponseWriter, r *http.Request) (*gorm.DB, string, string, string, bool) {
	db, skillID, userID, ok := requireOwnedSkill(w, r)
	if !ok {
		return nil, "", "", "", false
	}
	revisionID := common.PathVar(r, "revision_id")
	if revisionID == "" {
		replyError(w, "missing revision_id", http.StatusBadRequest)
		return nil, "", "", "", false
	}
	return db, skillID, userID, revisionID, true
}

func ensureSkillOwner(ctx context.Context, db *gorm.DB, skillID, userID string) error {
	var row orm.SkillV2Skill
	return db.WithContext(ctx).Select("id").Where("id = ? AND owner_user_id = ? AND deleted_at IS NULL", skillID, userID).Take(&row).Error
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		replyError(w, "invalid body", http.StatusBadRequest)
		return false
	}
	return true
}

func decodeOptionalJSON(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	err := json.NewDecoder(r.Body).Decode(v)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func replyServiceError(w http.ResponseWriter, err error) {
	skillhttperr.ReplyError(w, err)
}

func replyServiceErrorOK(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	sem := skillhttperr.ForError(err)
	appErr := common.ResolveAppError(sem.Message, sem.Status)
	data := map[string]any{"code": sem.Code}
	if appErr.Detail != nil {
		data["detail"] = appErr.Detail
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(common.APIResponse{Code: appErr.Code, Message: appErr.Message, Data: data})
}

func isReadFileNotFound(err error) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(err.Error())), "file not found:")
}

func replyError(w http.ResponseWriter, message string, status int) {
	skillhttperr.Reply(w, message, status)
}

func targetRevisionID(w http.ResponseWriter, r *http.Request) (string, bool) {
	var req struct {
		RevisionID       string `json:"revision_id"`
		TargetRevisionID string `json:"target_revision_id"`
	}
	if !decodeJSON(w, r, &req) {
		return "", false
	}
	revisionID := firstNonEmpty(req.TargetRevisionID, req.RevisionID)
	if revisionID == "" {
		replyError(w, "target_revision_id required", http.StatusBadRequest)
		return "", false
	}
	return revisionID, true
}

type diffRequest struct {
	Old          diffRefRequest `json:"old"`
	New          diffRefRequest `json:"new"`
	Path         string         `json:"path"`
	ContextLines int            `json:"context_lines"`
	Mode         string         `json:"mode"`
	OldStart     int            `json:"old_start"`
	NewStart     int            `json:"new_start"`
	Lines        int            `json:"lines"`
}

type diffRefRequest struct {
	Type       string `json:"type"`
	SkillID    string `json:"skill_id"`
	RevisionID string `json:"revision_id"`
	UploadID   string `json:"upload_id"`
}

func resolveDiffRequest(w http.ResponseWriter, r *http.Request) (*gorm.DB, string, skilldiff.ReadOnlySkillFS, skilldiff.ReadOnlySkillFS, skilldiff.DiffOptions, diffRequest, bool) {
	db, ok := requireDB(w)
	if !ok {
		return nil, "", nil, nil, skilldiff.DiffOptions{}, diffRequest{}, false
	}
	userID, _, ok := requireUser(w, r)
	if !ok {
		return nil, "", nil, nil, skilldiff.DiffOptions{}, diffRequest{}, false
	}
	var req diffRequest
	if !decodeJSON(w, r, &req) {
		return nil, "", nil, nil, skilldiff.DiffOptions{}, diffRequest{}, false
	}
	if err := authorizeDiffRef(r.Context(), db, userID, req.Old); err != nil {
		replyServiceError(w, err)
		return nil, "", nil, nil, skilldiff.DiffOptions{}, diffRequest{}, false
	}
	if err := authorizeDiffRef(r.Context(), db, userID, req.New); err != nil {
		replyServiceError(w, err)
		return nil, "", nil, nil, skilldiff.DiffOptions{}, diffRequest{}, false
	}
	if req.Old.SkillID != "" && req.New.SkillID != "" && req.Old.SkillID != req.New.SkillID {
		replyError(w, "diff refs must belong to the same skill", http.StatusUnprocessableEntity)
		return nil, "", nil, nil, skilldiff.DiffOptions{}, diffRequest{}, false
	}
	resolver := skilldiff.NewRefResolver(skilldiff.RefResolverDeps{DB: db, UploadStore: dbUploadStore{db: db}})
	oldFS, newFS, err := resolver.ResolvePair(r.Context(), skilldiff.ResolvePairRequest{UserID: userID, Old: req.Old.toDiffRef(), New: req.New.toDiffRef()})
	if err != nil {
		replyServiceError(w, err)
		return nil, "", nil, nil, skilldiff.DiffOptions{}, diffRequest{}, false
	}
	return db, userID, oldFS, newFS, skilldiff.DiffOptions{
		Path:         strings.TrimSpace(req.Path),
		ContextLines: req.ContextLines,
		Mode:         strings.TrimSpace(req.Mode),
		OldStart:     req.OldStart,
		NewStart:     req.NewStart,
		Lines:        req.Lines,
	}, req, true
}

func isDraftReviewDiff(req diffRequest) bool {
	if !strings.EqualFold(strings.TrimSpace(req.New.Type), "draft") || strings.TrimSpace(req.New.SkillID) == "" {
		return false
	}
	if req.Old.SkillID != "" && req.Old.SkillID != req.New.SkillID {
		return false
	}
	oldType := strings.ToLower(strings.TrimSpace(req.Old.Type))
	return oldType == "head" || oldType == "revision"
}

func authorizeDiffRef(ctx context.Context, db *gorm.DB, userID string, ref diffRefRequest) error {
	if ref.SkillID == "" {
		return nil
	}
	return ensureSkillOwner(ctx, db, ref.SkillID, userID)
}

func (r diffRefRequest) toDiffRef() skilldiff.DiffRef {
	return skilldiff.DiffRef{Type: strings.TrimSpace(r.Type), SkillID: strings.TrimSpace(r.SkillID), RevisionID: strings.TrimSpace(r.RevisionID), UploadID: strings.TrimSpace(r.UploadID)}
}

type dbUploadStore struct {
	db *gorm.DB
}

type uploadExt struct {
	StoredPath       string `json:"stored_path"`
	ParseStoredPath  string `json:"parse_stored_path"`
	OriginalFilename string `json:"original_filename"`
	StoredName       string `json:"stored_name"`
	Filename         string `json:"filename"`
	UploadState      string `json:"upload_state"`
	UploadScope      string `json:"upload_scope"`
	CreateUserID     string `json:"create_user_id"`
}

func (s dbUploadStore) Get(ctx context.Context, uploadID string) (skillservice.UploadSession, error) {
	uploadID = strings.TrimSpace(uploadID)
	if uploadID == "" {
		return skillservice.UploadSession{}, fmt.Errorf("upload_id required")
	}
	var file orm.UploadedFile
	if err := s.db.WithContext(ctx).Where("upload_file_id = ?", uploadID).Take(&file).Error; err == nil {
		var ext uploadExt
		_ = json.Unmarshal(file.Ext, &ext)
		storedPath := firstNonEmpty(ext.StoredPath, ext.ParseStoredPath)
		if storedPath == "" {
			return skillservice.UploadSession{}, fmt.Errorf("upload stored_path not found")
		}
		return skillservice.UploadSession{
			UploadID:    uploadID,
			OwnerUserID: file.CreateUserID,
			State:       normalizeUploadState(file.Status),
			StoredPath:  storedPath,
			Filename:    firstNonEmpty(ext.OriginalFilename, ext.Filename, ext.StoredName),
		}, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return skillservice.UploadSession{}, err
	}
	var session orm.UploadSession
	if err := s.db.WithContext(ctx).Where("upload_id = ?", uploadID).Take(&session).Error; err != nil {
		return skillservice.UploadSession{}, err
	}
	var ext uploadExt
	_ = json.Unmarshal(session.Ext, &ext)
	ownerID := firstNonEmpty(ext.CreateUserID, session.CreateUserID)
	storedPath := firstNonEmpty(ext.StoredPath, ext.ParseStoredPath)
	if storedPath == "" && strings.EqualFold(ext.UploadScope, "TEMP") && strings.TrimSpace(ext.StoredName) != "" {
		storedPath = filepath.Join(uploadRoot(), "tmp", "users", safePathPart(ownerID), "files", safePathPart(session.UploadID), ext.StoredName)
	}
	if storedPath == "" {
		return skillservice.UploadSession{}, fmt.Errorf("upload stored_path not found")
	}
	return skillservice.UploadSession{
		UploadID:    session.UploadID,
		OwnerUserID: ownerID,
		State:       normalizeUploadState(firstNonEmpty(ext.UploadState, session.UploadState)),
		StoredPath:  storedPath,
		Filename:    firstNonEmpty(ext.OriginalFilename, ext.Filename, ext.StoredName),
	}, nil
}

type httpZipDownloader struct{}

func (httpZipDownloader) Download(ctx context.Context, rawURL string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", fmt.Errorf("url required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("download failed: %s", resp.Status)
	}
	f, err := os.CreateTemp("", "lazymind-skill-*.zip")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func writeInlineSkillZip(content string) (string, error) {
	f, err := os.CreateTemp("", "lazymind-inline-skill-*.zip")
	if err != nil {
		return "", err
	}
	zipWriter := zip.NewWriter(f)
	entry, err := zipWriter.Create("SKILL.md")
	if err != nil {
		_ = zipWriter.Close()
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", err
	}
	if _, err := entry.Write([]byte(content)); err != nil {
		_ = zipWriter.Close()
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", err
	}
	if err := zipWriter.Close(); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func skillSummaryDTO(item skillservice.SkillSummary) map[string]any {
	out := map[string]any{
		"id":               item.ID,
		"skill_id":         firstNonEmpty(item.SkillID, item.ID),
		"name":             firstNonEmpty(item.Name, item.SkillName),
		"skill_name":       firstNonEmpty(item.SkillName, item.Name),
		"category":         item.Category,
		"description":      item.Description,
		"tags":             item.Tags,
		"head_revision_id": item.HeadRevisionID,
		"file_content":     item.FileContent,
		"auto_evo":         item.AutoEvo,
		"is_enabled":       item.IsEnabled,
		"draft":            draftSummaryDTO(item.Draft),
	}
	if item.DeletedAt != nil {
		out["deleted_at"] = item.DeletedAt
	}
	if strings.TrimSpace(item.DeletedBy) != "" {
		out["deleted_by"] = item.DeletedBy
	}
	return out
}

func skillDetailDTO(item skillservice.SkillDetail) map[string]any {
	out := skillSummaryDTO(item.SkillSummary)
	out["draft"] = draftSummaryDTO(item.Draft)
	return out
}

func draftSummaryDTO(item skillservice.DraftSummary) map[string]any {
	return map[string]any{
		"has_uncommitted_draft": item.HasUncommittedDraft,
		"task_id":               item.TaskID,
		"version":               item.Version,
		"type":                  item.Type,
		"status":                item.Status,
	}
}

func draftStateDTO(item skillfs.DraftState) map[string]any {
	return map[string]any{
		"has_uncommitted_draft": item.HasUncommittedDraft,
		"draft_version":         item.DraftVersion,
		"base_revision_id":      item.BaseRevisionID,
		"task_id":               item.TaskID,
		"conversation_id":       item.ConversationID,
	}
}

func serviceTreeDTO(node skillservice.TreeNode) map[string]any {
	children := make([]map[string]any, 0, len(node.Children))
	for _, child := range node.Children {
		children = append(children, serviceTreeDTO(child))
	}
	return map[string]any{
		"name":      node.Name,
		"path":      node.Path,
		"type":      node.Type,
		"children":  children,
		"blob_hash": node.BlobHash,
		"size":      node.Size,
		"mime":      node.Mime,
		"file_type": node.FileType,
		"binary":    node.Binary,
	}
}

func revisionTreeDTO(node skillrevision.TreeNode) map[string]any {
	children := make([]map[string]any, 0, len(node.Children))
	for _, child := range node.Children {
		children = append(children, revisionTreeDTO(child))
	}
	return map[string]any{"name": node.Name, "path": node.Path, "type": node.Type, "children": children, "blob_hash": node.BlobHash, "size": node.Size, "mime": node.Mime, "file_type": node.FileType, "binary": node.Binary}
}

func fileContentDTO(file skillservice.FileContent) map[string]any {
	return map[string]any{"path": file.Path, "content": file.Content, "binary": file.Binary, "download_url": file.DownloadURL, "mime": file.Mime, "file_type": file.FileType, "blob_hash": file.BlobHash}
}

func revisionFileDTO(file skillrevision.FileContent) map[string]any {
	return map[string]any{"path": file.Path, "content": file.Content, "binary": file.Binary, "download_url": file.DownloadURL, "mime": file.Mime, "file_type": file.FileType, "blob_hash": file.BlobHash}
}

func revisionDTO(item skillrevision.Revision) map[string]any {
	return map[string]any{
		"id":                 item.ID,
		"revision_id":        firstNonEmpty(item.RevisionID, item.ID),
		"skill_id":           item.SkillID,
		"parent_revision_id": item.ParentRevisionID,
		"revision_no":        item.RevisionNo,
		"tree_hash":          item.TreeHash,
		"message":            item.Message,
		"change_source":      item.ChangeSource,
		"created_by":         item.CreatedBy,
		"created_at":         item.CreatedAt,
		"file_content":       item.FileContent,
		"is_head":            item.IsHead,
	}
}

func diffFileDTO(file skilldiff.DiffFile) map[string]any {
	lines := make([]map[string]any, 0, len(file.DiffEntryLines))
	for _, line := range file.DiffEntryLines {
		item := map[string]any{
			"type":                        line.Type,
			"text":                        line.Text,
			"html":                        line.HTML,
			"oldLine":                     line.OldLine,
			"newLine":                     line.NewLine,
			"displayNoNewLineWarning":     line.DisplayNoNewLineWarning,
			"display_no_new_line_warning": line.DisplayNoNewLineWarning,
			"old_line":                    line.OldLine,
			"new_line":                    line.NewLine,
		}
		if line.HunkID != "" {
			item["hunk_id"] = line.HunkID
		}
		if line.Decision != "" {
			item["decision"] = line.Decision
		}
		if line.OldStart > 0 {
			item["old_start"] = line.OldStart
		}
		if line.OldLines > 0 {
			item["old_lines"] = line.OldLines
		}
		if line.NewStart > 0 {
			item["new_start"] = line.NewStart
		}
		if line.NewLines > 0 {
			item["new_lines"] = line.NewLines
		}
		lines = append(lines, item)
	}
	out := map[string]any{"path": file.Path, "type": file.Type, "status": file.Status, "binary": file.Binary, "too_large": file.TooLarge, "cache_written": file.CacheWritten, "diff_entry_lines": lines}
	if file.ReviewID != "" {
		out["review_id"] = file.ReviewID
		out["review_version"] = file.ReviewVersion
		out["draft_version"] = file.DraftVersion
		out["base_revision_id"] = file.BaseRevisionID
		out["draft_snapshot_hash"] = file.DraftSnapshotHash
		out["can_undo"] = file.CanUndo
	}
	if file.HunkCount > 0 {
		out["hunk_count"] = file.HunkCount
		out["pending_count"] = file.PendingCount
		out["accepted_count"] = file.AcceptedCount
		out["rejected_count"] = file.RejectedCount
	}
	return out
}

type shareRecord struct {
	item orm.SkillShareItem
	task orm.SkillShareTask
}

func listShareItems(w http.ResponseWriter, r *http.Request, incoming bool) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	userID, _, ok := requireUser(w, r)
	if !ok {
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	page := positiveQueryInt(r, "page", 1)
	pageSize := positiveQueryInt(r, "page_size", 20)
	if pageSize > 100 {
		pageSize = 100
	}
	var items []orm.SkillShareItem
	query := db.WithContext(r.Context()).Model(&orm.SkillShareItem{})
	if incoming {
		query = query.Where("target_user_id = ?", userID)
	} else {
		var taskIDs []string
		if err := db.WithContext(r.Context()).Model(&orm.SkillShareTask{}).Where("source_user_id = ?", userID).Pluck("id", &taskIDs).Error; err != nil {
			replyServiceError(w, err)
			return
		}
		if len(taskIDs) == 0 {
			common.ReplyOK(w, map[string]any{"items": []any{}, "page": page, "page_size": pageSize, "total": 0})
			return
		}
		query = query.Where("share_task_id IN ?", taskIDs)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		replyServiceError(w, err)
		return
	}
	if err := query.Order("created_at DESC").Limit(pageSize).Offset((page - 1) * pageSize).Find(&items).Error; err != nil {
		replyServiceError(w, err)
		return
	}
	taskMap, err := shareTaskMap(r.Context(), db, items)
	if err != nil {
		replyServiceError(w, err)
		return
	}
	resp := make([]map[string]any, 0, len(items))
	for _, item := range items {
		resp = append(resp, shareListItemDTO(shareRecord{item: item, task: taskMap[item.ShareTaskID]}))
	}
	common.ReplyOK(w, map[string]any{"items": resp, "page": page, "page_size": pageSize, "total": total})
}

func loadShareItem(w http.ResponseWriter, r *http.Request, db *gorm.DB) (*orm.SkillShareItem, *orm.SkillShareTask, bool) {
	shareItemID := common.PathVar(r, "share_item_id")
	if shareItemID == "" {
		replyError(w, "missing share_item_id", http.StatusBadRequest)
		return nil, nil, false
	}
	var item orm.SkillShareItem
	if err := db.WithContext(r.Context()).Where("id = ?", shareItemID).Take(&item).Error; err != nil {
		replyServiceError(w, err)
		return nil, nil, false
	}
	var task orm.SkillShareTask
	if err := db.WithContext(r.Context()).Where("id = ?", item.ShareTaskID).Take(&task).Error; err != nil {
		replyServiceError(w, err)
		return nil, nil, false
	}
	if item.SourceSkillID == "" {
		item.SourceSkillID = task.SourceSkillID
	}
	return &item, &task, true
}

func shareTaskMap(ctx context.Context, db *gorm.DB, items []orm.SkillShareItem) (map[string]orm.SkillShareTask, error) {
	taskIDs := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		if item.ShareTaskID == "" {
			continue
		}
		if _, ok := seen[item.ShareTaskID]; ok {
			continue
		}
		seen[item.ShareTaskID] = struct{}{}
		taskIDs = append(taskIDs, item.ShareTaskID)
	}
	taskMap := map[string]orm.SkillShareTask{}
	if len(taskIDs) == 0 {
		return taskMap, nil
	}
	var tasks []orm.SkillShareTask
	if err := db.WithContext(ctx).Where("id IN ?", taskIDs).Find(&tasks).Error; err != nil {
		return nil, err
	}
	for _, task := range tasks {
		taskMap[task.ID] = task
	}
	return taskMap, nil
}

func expandShareTargets(r *http.Request, req shareSkillRequest, sourceUserID string) []string {
	targets := append([]string{}, compactStrings(req.TargetUserIDs)...)
	for _, userID := range common.FetchGroupUserIDsFromAuthService(r, compactStrings(req.TargetGroupIDs)) {
		targets = append(targets, userID)
	}
	out := make([]string, 0, len(targets))
	seen := map[string]struct{}{}
	for _, target := range targets {
		target = strings.TrimSpace(target)
		if target == "" || target == sourceUserID {
			continue
		}
		if _, ok := seen[target]; ok {
			continue
		}
		seen[target] = struct{}{}
		out = append(out, target)
	}
	sort.Strings(out)
	return out
}

func shareCreateItemDTO(item orm.SkillShareItem) map[string]any {
	return map[string]any{
		"id":                   item.ID,
		"share_item_id":        item.ID,
		"share_task_id":        item.ShareTaskID,
		"target_user_id":       item.TargetUserID,
		"target_user_name":     firstNonEmpty(item.TargetUserName, item.TargetUserID),
		"status":               item.Status,
		"target_root_skill_id": item.TargetRootSkillID,
		"error_message":        item.ErrorMessage,
		"created_at":           item.CreatedAt,
		"updated_at":           item.UpdatedAt,
	}
}

func shareTargetDTO(record shareRecord) map[string]any {
	return map[string]any{
		"target_user_id":       record.item.TargetUserID,
		"target_user_name":     firstNonEmpty(record.item.TargetUserName, record.item.TargetUserID),
		"status":               record.item.Status,
		"share_item_id":        record.item.ID,
		"share_task_id":        record.item.ShareTaskID,
		"message":              record.task.Message,
		"accepted_at":          record.item.AcceptedAt,
		"rejected_at":          record.item.RejectedAt,
		"target_root_skill_id": record.item.TargetRootSkillID,
		"error_message":        record.item.ErrorMessage,
		"shared_at":            record.item.CreatedAt,
		"updated_at":           record.item.UpdatedAt,
	}
}

func shareListItemDTO(record shareRecord) map[string]any {
	return map[string]any{
		"share_item_id":        record.item.ID,
		"share_task_id":        record.item.ShareTaskID,
		"status":               record.item.Status,
		"source_user_id":       record.task.SourceUserID,
		"source_user_name":     record.task.SourceUserName,
		"target_user_id":       record.item.TargetUserID,
		"target_user_name":     firstNonEmpty(record.item.TargetUserName, record.item.TargetUserID),
		"source_skill_id":      firstNonEmpty(record.item.SourceSkillID, record.task.SourceSkillID),
		"source_category":      record.task.SourceCategory,
		"message":              record.task.Message,
		"accepted_at":          record.item.AcceptedAt,
		"rejected_at":          record.item.RejectedAt,
		"target_root_skill_id": record.item.TargetRootSkillID,
		"error_message":        record.item.ErrorMessage,
		"created_at":           record.item.CreatedAt,
		"updated_at":           record.item.UpdatedAt,
	}
}

func shareStatusSummaryDTO(records []shareRecord) map[string]int {
	out := map[string]int{"pending_accept": 0, "completed": 0, "rejected": 0, "failed": 0}
	for _, record := range records {
		switch record.item.Status {
		case "pending", shareStatusPendingAccept:
			out["pending_accept"]++
		case shareStatusCompleted:
			out["completed"]++
		case shareStatusRejected:
			out["rejected"]++
		case shareStatusFailed:
			out["failed"]++
		}
	}
	return out
}

func shareItemNewer(candidate, current orm.SkillShareItem) bool {
	if candidate.CreatedAt.After(current.CreatedAt) {
		return true
	}
	if candidate.CreatedAt.Before(current.CreatedAt) {
		return false
	}
	if candidate.UpdatedAt.After(current.UpdatedAt) {
		return true
	}
	if candidate.UpdatedAt.Before(current.UpdatedAt) {
		return false
	}
	return strings.Compare(strings.TrimSpace(candidate.ID), strings.TrimSpace(current.ID)) > 0
}

func findServiceTreeNode(node skillservice.TreeNode, path string) *skillservice.TreeNode {
	if node.Path == path {
		return &node
	}
	for _, child := range node.Children {
		if found := findServiceTreeNode(child, path); found != nil {
			return found
		}
	}
	return nil
}

func filterSkillSummaries(ctx context.Context, db *gorm.DB, items []skillservice.SkillSummary, r *http.Request) ([]skillservice.SkillSummary, error) {
	keyword := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("keyword")))
	category := strings.TrimSpace(r.URL.Query().Get("category"))
	tags := compactStrings(r.URL.Query()["tags"])
	out := make([]skillservice.SkillSummary, 0, len(items))
	for _, item := range items {
		if category != "" && item.Category != category {
			continue
		}
		if len(tags) > 0 && !hasAllTags(item.Tags, tags) {
			continue
		}
		if keyword != "" && !strings.Contains(strings.ToLower(item.Name+" "+item.SkillName+" "+item.Description), keyword) {
			matched, err := skillHeadTextContains(ctx, db, item.ID, keyword)
			if err != nil {
				return nil, err
			}
			if !matched {
				continue
			}
		}
		out = append(out, item)
	}
	return out, nil
}

func filterTrashedSkillSummaries(items []skillservice.SkillSummary, r *http.Request) []skillservice.SkillSummary {
	keyword := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("keyword")))
	category := strings.TrimSpace(r.URL.Query().Get("category"))
	tags := compactStrings(r.URL.Query()["tags"])
	out := make([]skillservice.SkillSummary, 0, len(items))
	for _, item := range items {
		if category != "" && item.Category != category {
			continue
		}
		if len(tags) > 0 && !hasAllTags(item.Tags, tags) {
			continue
		}
		if keyword != "" && !strings.Contains(strings.ToLower(item.Name+" "+item.SkillName+" "+item.Description), keyword) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func skillHeadTextContains(ctx context.Context, db *gorm.DB, skillID, keyword string) (bool, error) {
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	if keyword == "" {
		return true, nil
	}
	return skillsearch.NewService(skillsearch.ServiceDeps{DB: db}).Contains(ctx, skillID, keyword)
}

func paginateSkillSummaries(items []skillservice.SkillSummary, r *http.Request) []skillservice.SkillSummary {
	page := positiveQueryInt(r, "page", 1)
	pageSize := positiveQueryInt(r, "page_size", 20)
	if pageSize > 100 {
		pageSize = 100
	}
	start := (page - 1) * pageSize
	if start >= len(items) {
		return []skillservice.SkillSummary{}
	}
	end := start + pageSize
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
}

func positiveQueryInt(r *http.Request, key string, fallback int) int {
	v, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get(key)))
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}

func int64Query(r *http.Request, key string, fallback int64) int64 {
	v, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get(key)), 10, 64)
	if err != nil {
		return fallback
	}
	return v
}

func trimStringPtr(v *string) *string {
	if v == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*v)
	return &trimmed
}

func compactStringSlicePtr(v *[]string) *[]string {
	if v == nil {
		return nil
	}
	out := compactStrings(*v)
	return &out
}

func compactStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func hasAllTags(haystack, needles []string) bool {
	set := map[string]struct{}{}
	for _, tag := range haystack {
		set[tag] = struct{}{}
	}
	for _, tag := range needles {
		if _, ok := set[tag]; !ok {
			return false
		}
	}
	return true
}

func decodeTags(raw []byte) []string {
	var tags []string
	_ = json.Unmarshal(raw, &tags)
	return compactStrings(tags)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func normalizeUploadState(state string) string {
	switch strings.ToUpper(strings.TrimSpace(state)) {
	case "UPLOADED", "COMPLETED", "COMPLETE", "DONE", "BOUND":
		return "completed"
	default:
		return strings.ToLower(strings.TrimSpace(state))
	}
}

func skillObjectRoot() string {
	if v := strings.TrimSpace(os.Getenv("LAZYMIND_SKILL_OBJECT_ROOT")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return filepath.Join(uploadRoot(), "skill-objects")
}

func uploadRoot() string {
	if v := strings.TrimSpace(os.Getenv("LAZYMIND_UPLOAD_ROOT")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "/var/lib/lazymind/uploads"
}

func safePathPart(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "..", "")
	s = strings.ReplaceAll(s, "\\", "/")
	s = strings.Trim(s, "/")
	if s == "" {
		return "root"
	}
	replacer := strings.NewReplacer("/", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_")
	return replacer.Replace(s)
}
