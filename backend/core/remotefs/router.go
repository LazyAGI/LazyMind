package remotefs

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/evolution"
	"lazymind/core/preferencefile"
	"lazymind/core/resourcefs"
	skillhttperr "lazymind/core/skillv2/httperr"
	skillremotefs "lazymind/core/skillv2/remotefs"
	"lazymind/core/store"
)

type Handler struct {
	db    *gorm.DB
	skill *skillremotefs.Handler
	fs    *resourcefs.Service
}

func NewHandler(db *gorm.DB) *Handler {
	return &Handler{
		db: db,
		skill: skillremotefs.NewHandler(skillremotefs.HandlerDeps{
			DB:         db,
			BlobStore:  skillremotefs.NewBlobStore(db, skillremotefs.NewLocalObjectStore(skillObjectRoot())),
			StateStore: store.State(),
		}),
		fs: resourcefs.NewService(resourcefs.ServiceDeps{DB: db}),
	}
}

func List(w http.ResponseWriter, r *http.Request) {
	NewHandler(store.DB()).List(w, r)
}

func Info(w http.ResponseWriter, r *http.Request) {
	NewHandler(store.DB()).Info(w, r)
}

func Exists(w http.ResponseWriter, r *http.Request) {
	NewHandler(store.DB()).Exists(w, r)
}

func Content(w http.ResponseWriter, r *http.Request) {
	NewHandler(store.DB()).Content(w, r)
}

func Dir(w http.ResponseWriter, r *http.Request) {
	NewHandler(store.DB()).Dir(w, r)
}

func Delete(w http.ResponseWriter, r *http.Request) {
	NewHandler(store.DB()).Delete(w, r)
}

func Copy(w http.ResponseWriter, r *http.Request) {
	NewHandler(store.DB()).Copy(w, r)
}

func Move(w http.ResponseWriter, r *http.Request) {
	NewHandler(store.DB()).Move(w, r)
}

func Trash(w http.ResponseWriter, r *http.Request) {
	NewHandler(store.DB()).Trash(w, r)
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	pathValue := resourcefs.NormalizePath(r.URL.Query().Get("path"))
	if isPluginPath(pathValue) {
		h.pluginList(w, r, pathValue)
		return
	}
	if isSkillPath(pathValue) {
		h.skill.List(w, requestWithUserAndPath(r, pathValue))
		return
	}
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	switch pathValue {
	case "memory":
		writeJSON(w, http.StatusOK, map[string]any{"items": []map[string]any{
			{"name": "memory.md", "path": resourcefs.MemoryPath, "type": "file"},
			{"name": "user.md", "path": resourcefs.UserPreferencePath, "type": "file"},
		}})
	case resourcefs.MemoryPath, resourcefs.UserPreferencePath:
		ref, err := h.ensurePersonal(r.Context(), userID, pathValue)
		if err != nil {
			replyError(w, err)
			return
		}
		file, err := h.fs.ReadFile(r.Context(), resourcefs.ReadFileRequest{Ref: ref, RefType: resourcefs.FileRefHead})
		if err != nil {
			replyError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": []map[string]any{fileListItem(file)}})
	default:
		replyError(w, resourcefs.ErrInvalidPath)
	}
}

func (h *Handler) Info(w http.ResponseWriter, r *http.Request) {
	pathValue := resourcefs.NormalizePath(r.URL.Query().Get("path"))
	if isPluginPath(pathValue) {
		h.pluginInfo(w, r, pathValue)
		return
	}
	if isSkillPath(pathValue) {
		h.skill.Info(w, requestWithUserAndPath(r, pathValue))
		return
	}
	if pathValue == "memory" {
		writeJSON(w, http.StatusOK, map[string]any{"path": "memory", "type": "dir"})
		return
	}
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	ref, err := h.ensurePersonal(r.Context(), userID, pathValue)
	if err != nil {
		replyError(w, err)
		return
	}
	file, err := h.fs.ReadFile(r.Context(), resourcefs.ReadFileRequest{Ref: ref, RefType: resourcefs.FileRefHead})
	if err != nil {
		replyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, fileListItem(file))
}

func (h *Handler) Exists(w http.ResponseWriter, r *http.Request) {
	pathValue := resourcefs.NormalizePath(r.URL.Query().Get("path"))
	if isPluginPath(pathValue) {
		_, _, _, err := h.pluginFiles(r, pathValue)
		writeJSON(w, http.StatusOK, map[string]any{"exists": err == nil})
		return
	}
	if isSkillPath(pathValue) {
		h.skill.Exists(w, requestWithUserAndPath(r, pathValue))
		return
	}
	if pathValue == "memory" {
		writeJSON(w, http.StatusOK, map[string]any{"exists": true})
		return
	}
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	_, err := h.ensurePersonal(r.Context(), userID, pathValue)
	writeJSON(w, http.StatusOK, map[string]any{"exists": err == nil})
}

func (h *Handler) Content(w http.ResponseWriter, r *http.Request) {
	pathValue := resourcefs.NormalizePath(r.URL.Query().Get("path"))
	if isPluginPath(pathValue) {
		h.pluginContent(w, r, pathValue)
		return
	}
	if isSkillPath(pathValue) {
		h.skill.Content(w, requestWithUserAndPath(r, pathValue))
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.readPersonalContent(w, r, pathValue)
	case http.MethodPut:
		h.writePersonalContent(w, r, pathValue)
	default:
		skillhttperr.Reply(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) Dir(w http.ResponseWriter, r *http.Request) {
	if h.delegateBodyPath(w, r, func() { h.skill.Dir(w, requestWithUser(r)) }) {
		return
	}
	replyError(w, resourcefs.ErrUnsupported)
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	pathValue := resourcefs.NormalizePath(r.URL.Query().Get("path"))
	if isSkillPath(pathValue) {
		h.skill.DeletePath(w, requestWithUserAndPath(r, pathValue))
		return
	}
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	ref, err := h.ensurePersonal(r.Context(), userID, pathValue)
	if err != nil {
		replyError(w, err)
		return
	}
	clearContent := ""
	if ref.ResourceType == resourcefs.ResourceTypeUserPreference {
		clearContent = preferencefile.EmptyPreferenceFileContent()
	}
	draft, err := h.fs.ReadFile(r.Context(), resourcefs.ReadFileRequest{Ref: ref, RefType: resourcefs.FileRefDraft})
	if err != nil {
		replyError(w, err)
		return
	}
	draftResp, err := h.fs.WriteDraft(r.Context(), resourcefs.WriteDraftRequest{
		Ref:                  ref,
		Content:              clearContent,
		ExpectedDraftVersion: draft.DraftVersion,
		TaskID:               strings.TrimSpace(r.URL.Query().Get("task_id")),
		UpdatedBy:            userID,
	})
	if err != nil {
		replyError(w, err)
		return
	}
	commit, err := h.fs.CommitDraft(r.Context(), resourcefs.CommitDraftRequest{
		Ref:                  ref,
		Message:              "clear personal resource",
		SourceRefType:        "remote_fs_delete",
		ExpectedDraftVersion: draftResp.DraftVersion,
		CreatedBy:            userID,
	})
	if err != nil {
		replyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "revision_id": commit.RevisionID, "revision_no": commit.RevisionNo})
}

func (h *Handler) Copy(w http.ResponseWriter, r *http.Request) {
	if h.delegateBodyPathPair(w, r, func() { h.skill.Copy(w, requestWithUser(r)) }) {
		return
	}
	replyError(w, resourcefs.ErrUnsupported)
}

func (h *Handler) Move(w http.ResponseWriter, r *http.Request) {
	if h.delegateBodyPathPair(w, r, func() { h.skill.Move(w, requestWithUser(r)) }) {
		return
	}
	replyError(w, resourcefs.ErrUnsupported)
}

func (h *Handler) Trash(w http.ResponseWriter, r *http.Request) {
	pathValue := resourcefs.NormalizePath(r.URL.Query().Get("path"))
	if pathValue == "" {
		var body struct {
			Path string `json:"path"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		pathValue = resourcefs.NormalizePath(body.Path)
	}
	if isSkillPath(pathValue) {
		h.skill.Trash(w, requestWithUserAndPath(r, pathValue))
		return
	}
	replyError(w, resourcefs.ErrUnsupported)
}

func (h *Handler) readPersonalContent(w http.ResponseWriter, r *http.Request, pathValue string) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	ref, err := h.ensurePersonal(r.Context(), userID, pathValue)
	if err != nil {
		replyError(w, err)
		return
	}
	refType := resourcefs.FileRefHead
	if isMemoryReviewTaskID(r.URL.Query().Get("task_id")) {
		if draft, err := h.fs.ReadFile(r.Context(), resourcefs.ReadFileRequest{Ref: ref, RefType: resourcefs.FileRefDraft}); err == nil && strings.TrimSpace(draft.DraftStatus) == "pending_confirm" {
			refType = resourcefs.FileRefDraft
		}
	}
	file, err := h.fs.ReadFile(r.Context(), resourcefs.ReadFileRequest{Ref: ref, RefType: refType})
	if err != nil {
		replyError(w, err)
		return
	}
	switch r.URL.Query().Get("encoding") {
	case "base64":
		writeJSON(w, http.StatusOK, map[string]any{"encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(file.Content))})
	default:
		if file.Mime != "" {
			w.Header().Set("Content-Type", file.Mime)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(file.Content))
	}
}

func (h *Handler) writePersonalContent(w http.ResponseWriter, r *http.Request, pathValue string) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	taskID := strings.TrimSpace(r.URL.Query().Get("task_id"))
	if taskID == "" {
		skillhttperr.Reply(w, "task_id is required", http.StatusBadRequest)
		return
	}
	ref, err := h.ensurePersonal(r.Context(), userID, pathValue)
	if err != nil {
		replyError(w, err)
		return
	}
	draft, err := h.fs.ReadFile(r.Context(), resourcefs.ReadFileRequest{Ref: ref, RefType: resourcefs.FileRefDraft})
	if err != nil {
		replyError(w, err)
		return
	}
	if !isMemoryReviewTaskID(taskID) && strings.TrimSpace(draft.DraftStatus) == "pending_confirm" {
		replyError(w, resourcefs.ErrConflict)
		return
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		replyError(w, err)
		return
	}
	resp, err := h.fs.WriteDraft(r.Context(), resourcefs.WriteDraftRequest{
		Ref:                  ref,
		Content:              string(data),
		ExpectedDraftVersion: draft.DraftVersion,
		TaskID:               taskID,
		UpdatedBy:            userID,
	})
	if err != nil {
		replyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "draft_version": resp.DraftVersion})
}

func isMemoryReviewTaskID(taskID string) bool {
	return strings.HasPrefix(strings.TrimSpace(taskID), "memory_review_")
}

func (h *Handler) ensurePersonal(ctx context.Context, userID, pathValue string) (resourcefs.ResourceRef, error) {
	resourceType, err := resourcefs.ResourceTypeForPath(pathValue)
	if err != nil {
		return resourcefs.ResourceRef{}, err
	}
	ref := resourcefs.ResourceRef{UserID: userID, ResourceType: resourceType}
	if resourceType != resourcefs.ResourceTypeMemory && resourceType != resourcefs.ResourceTypeUserPreference {
		return resourcefs.ResourceRef{}, resourcefs.ErrInvalidResourceType
	}
	if _, err := evolution.EnsurePersonalResourceContent(ctx, h.db, userID, string(resourceType)); err != nil {
		return resourcefs.ResourceRef{}, err
	}
	return ref, nil
}

func (h *Handler) delegateBodyPath(w http.ResponseWriter, r *http.Request, delegate func()) bool {
	data, err := io.ReadAll(r.Body)
	if err != nil {
		replyError(w, err)
		return true
	}
	r.Body = io.NopCloser(strings.NewReader(string(data)))
	var body struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		skillhttperr.Reply(w, "invalid json", http.StatusBadRequest)
		return true
	}
	if isSkillPath(resourcefs.NormalizePath(body.Path)) {
		r.Body = io.NopCloser(strings.NewReader(string(data)))
		delegate()
		return true
	}
	return false
}

func (h *Handler) delegateBodyPathPair(w http.ResponseWriter, r *http.Request, delegate func()) bool {
	data, err := io.ReadAll(r.Body)
	if err != nil {
		replyError(w, err)
		return true
	}
	r.Body = io.NopCloser(strings.NewReader(string(data)))
	var body struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		skillhttperr.Reply(w, "invalid json", http.StatusBadRequest)
		return true
	}
	if isSkillPath(resourcefs.NormalizePath(body.From)) && isSkillPath(resourcefs.NormalizePath(body.To)) {
		r.Body = io.NopCloser(strings.NewReader(string(data)))
		delegate()
		return true
	}
	return false
}

func fileListItem(file resourcefs.FileResponse) map[string]any {
	return map[string]any{
		"name":      filepath.Base(file.Path),
		"path":      file.Path,
		"type":      "file",
		"size":      file.Size,
		"mime":      file.Mime,
		"file_type": file.FileType,
		"binary":    file.Binary,
	}
}

func requireUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID := strings.TrimSpace(r.URL.Query().Get("user_id"))
	if userID == "" {
		userID = strings.TrimSpace(store.UserID(r))
	}
	if userID == "" {
		skillhttperr.ReplyWithCode(w, "user_id is required", http.StatusUnauthorized, skillhttperr.CodeUnauthenticated)
		return "", false
	}
	return userID, true
}

func requestWithUser(r *http.Request) *http.Request {
	return requestWithUserAndPath(r, resourcefs.NormalizePath(r.URL.Query().Get("path")))
}

func requestWithUserAndPath(r *http.Request, pathValue string) *http.Request {
	clone := r.Clone(r.Context())
	q := clone.URL.Query()
	if strings.TrimSpace(q.Get("user_id")) == "" {
		if userID := strings.TrimSpace(store.UserID(r)); userID != "" {
			q.Set("user_id", userID)
		}
	}
	if pathValue != "" {
		q.Set("path", pathValue)
	}
	clone.URL.RawQuery = q.Encode()
	return clone
}

func isSkillPath(pathValue string) bool {
	return pathValue == "skills" || strings.HasPrefix(pathValue, "skills/")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func replyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, resourcefs.ErrInvalidPath), errors.Is(err, resourcefs.ErrInvalidResourceType):
		skillhttperr.Reply(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, resourcefs.ErrResourceNotFound), errors.Is(err, resourcefs.ErrRevisionNotFound), errors.Is(err, gorm.ErrRecordNotFound):
		skillhttperr.ReplyWithCode(w, "not found", http.StatusNotFound, skillhttperr.CodeNotFound)
	case errors.Is(err, resourcefs.ErrConflict):
		skillhttperr.Reply(w, "conflict", http.StatusConflict)
	case errors.Is(err, resourcefs.ErrUnsupported):
		skillhttperr.Reply(w, "unsupported for personal resource", http.StatusUnprocessableEntity)
	default:
		skillhttperr.Reply(w, err.Error(), http.StatusInternalServerError)
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
