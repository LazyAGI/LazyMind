package remotefs

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"gorm.io/gorm"

	skillhttperr "lazymind/core/skillv2/httperr"
	skillremotefs "lazymind/core/skillv2/remotefs"
	"lazymind/core/store"
)

var (
	errRemoteFSInvalidPath = errors.New("invalid remote fs path")
	errRemoteFSConflict    = errors.New("remote fs conflict")
	errRemoteFSUnsupported = errors.New("remote fs operation unsupported")
)

type Handler struct {
	db     *gorm.DB
	skill  *skillremotefs.Handler
	memory *memoryCurrentHandler
}

func NewHandler(db *gorm.DB) *Handler {
	return &Handler{
		db: db,
		skill: skillremotefs.NewHandler(skillremotefs.HandlerDeps{
			DB:         db,
			BlobStore:  skillremotefs.NewBlobStore(db, skillremotefs.NewLocalObjectStore(skillObjectRoot())),
			StateStore: store.State(),
		}),
		memory: newMemoryCurrentHandler(newMemoryCurrentService(db)),
	}
}

func List(w http.ResponseWriter, r *http.Request) {
	dispatch(w, r, (*Handler).List)
}

func Info(w http.ResponseWriter, r *http.Request) {
	dispatch(w, r, (*Handler).Info)
}

func Exists(w http.ResponseWriter, r *http.Request) {
	dispatch(w, r, (*Handler).Exists)
}

func Content(w http.ResponseWriter, r *http.Request) {
	dispatch(w, r, (*Handler).Content)
}

func Dir(w http.ResponseWriter, r *http.Request) {
	dispatch(w, r, (*Handler).Dir)
}

func Delete(w http.ResponseWriter, r *http.Request) {
	dispatch(w, r, (*Handler).Delete)
}

func Copy(w http.ResponseWriter, r *http.Request) {
	dispatch(w, r, (*Handler).Copy)
}

func Move(w http.ResponseWriter, r *http.Request) {
	dispatch(w, r, (*Handler).Move)
}

func Trash(w http.ResponseWriter, r *http.Request) {
	dispatch(w, r, (*Handler).Trash)
}

func dispatch(
	w http.ResponseWriter,
	r *http.Request,
	operation func(*Handler, http.ResponseWriter, *http.Request),
) {
	if !requireInternalServiceToken(w, r) {
		return
	}
	operation(NewHandler(store.DB()), w, r)
}

func requireInternalServiceToken(w http.ResponseWriter, r *http.Request) bool {
	expected := strings.TrimSpace(os.Getenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN"))
	if expected == "" {
		return true
	}
	provided := strings.TrimSpace(r.Header.Get("X-LazyMind-Internal-Token"))
	if subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
		skillhttperr.ReplyWithCode(
			w,
			"internal token required",
			http.StatusUnauthorized,
			skillhttperr.CodeUnauthenticated,
		)
		return false
	}
	return true
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	pathValue := normalizeRemoteFSPath(r.URL.Query().Get("path"))
	if isPluginPath(pathValue) {
		h.pluginList(w, r, pathValue)
		return
	}
	if isSkillPath(pathValue) {
		h.skill.List(w, requestWithUserAndPath(r, pathValue))
		return
	}
	if isMemoryMountPath(pathValue) {
		h.memory.List(w, r, pathValue)
		return
	}
	replyError(w, errRemoteFSInvalidPath)
}

func (h *Handler) Info(w http.ResponseWriter, r *http.Request) {
	pathValue := normalizeRemoteFSPath(r.URL.Query().Get("path"))
	if isPluginPath(pathValue) {
		h.pluginInfo(w, r, pathValue)
		return
	}
	if isSkillPath(pathValue) {
		h.skill.Info(w, requestWithUserAndPath(r, pathValue))
		return
	}
	if isMemoryMountPath(pathValue) {
		h.memory.Info(w, r, pathValue)
		return
	}
	replyError(w, errRemoteFSInvalidPath)
}

func (h *Handler) Exists(w http.ResponseWriter, r *http.Request) {
	pathValue := normalizeRemoteFSPath(r.URL.Query().Get("path"))
	if isPluginPath(pathValue) {
		_, _, _, err := h.pluginFiles(r, pathValue)
		writeJSON(w, http.StatusOK, map[string]any{"exists": err == nil})
		return
	}
	if isSkillPath(pathValue) {
		h.skill.Exists(w, requestWithUserAndPath(r, pathValue))
		return
	}
	if isMemoryMountPath(pathValue) {
		h.memory.Exists(w, r, pathValue)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"exists": false})
}

func (h *Handler) Content(w http.ResponseWriter, r *http.Request) {
	pathValue := normalizeRemoteFSPath(r.URL.Query().Get("path"))
	if isPluginPath(pathValue) {
		h.pluginContent(w, r, pathValue)
		return
	}
	if isSkillPath(pathValue) {
		h.skill.Content(w, requestWithUserAndPath(r, pathValue))
		return
	}
	if isMemoryMountPath(pathValue) {
		h.memory.Content(w, r, pathValue)
		return
	}
	replyError(w, errRemoteFSInvalidPath)
}

func (h *Handler) Dir(w http.ResponseWriter, r *http.Request) {
	data, pathValue, ok := readBodyPath(w, r)
	if !ok {
		return
	}
	r.Body = io.NopCloser(strings.NewReader(string(data)))
	if isPluginPath(pathValue) {
		skillhttperr.Reply(w, "revision/plugin views are read-only", http.StatusBadRequest)
		return
	}
	if isSkillPath(pathValue) {
		h.skill.Dir(w, requestWithUser(r))
		return
	}
	if isMemoryMountPath(pathValue) {
		h.memory.Dir(w, r)
		return
	}
	replyError(w, errRemoteFSUnsupported)
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	pathValue := normalizeRemoteFSPath(r.URL.Query().Get("path"))
	if isPluginPath(pathValue) {
		skillhttperr.Reply(w, "revision/plugin views are read-only", http.StatusBadRequest)
		return
	}
	if isSkillPath(pathValue) {
		h.skill.DeletePath(w, requestWithUserAndPath(r, pathValue))
		return
	}
	if isMemoryMountPath(pathValue) {
		h.memory.Delete(w, r, pathValue)
		return
	}
	replyError(w, errRemoteFSInvalidPath)
}

func (h *Handler) Copy(w http.ResponseWriter, r *http.Request) {
	data, from, to, ok := readBodyPathPair(w, r)
	if !ok {
		return
	}
	r.Body = io.NopCloser(strings.NewReader(string(data)))
	if isPluginPath(from) || isPluginPath(to) {
		skillhttperr.Reply(w, "revision/plugin views are read-only", http.StatusBadRequest)
		return
	}
	fromSkill, toSkill := isSkillPath(from), isSkillPath(to)
	fromMemory, toMemory := isMemoryMountPath(from), isMemoryMountPath(to)
	if fromSkill && toSkill {
		h.skill.Copy(w, requestWithUser(r))
		return
	}
	if fromMemory && toMemory {
		h.memory.Copy(w, r)
		return
	}
	if (fromSkill || toSkill) && (fromMemory || toMemory) {
		skillhttperr.Reply(w, "copy across skill and memory mounts is not allowed", http.StatusBadRequest)
		return
	}
	replyError(w, errRemoteFSUnsupported)
}

func (h *Handler) Move(w http.ResponseWriter, r *http.Request) {
	data, from, to, ok := readBodyPathPair(w, r)
	if !ok {
		return
	}
	r.Body = io.NopCloser(strings.NewReader(string(data)))
	if isPluginPath(from) || isPluginPath(to) {
		skillhttperr.Reply(w, "revision/plugin views are read-only", http.StatusBadRequest)
		return
	}
	fromSkill, toSkill := isSkillPath(from), isSkillPath(to)
	fromMemory, toMemory := isMemoryMountPath(from), isMemoryMountPath(to)
	if fromSkill && toSkill {
		h.skill.Move(w, requestWithUser(r))
		return
	}
	if fromMemory && toMemory {
		h.memory.Move(w, r)
		return
	}
	if (fromSkill || toSkill) && (fromMemory || toMemory) {
		skillhttperr.Reply(w, "move across skill and memory mounts is not allowed", http.StatusBadRequest)
		return
	}
	replyError(w, errRemoteFSUnsupported)
}

func (h *Handler) Trash(w http.ResponseWriter, r *http.Request) {
	pathValue := normalizeRemoteFSPath(r.URL.Query().Get("path"))
	if pathValue == "" {
		var body struct {
			Path string `json:"path"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		pathValue = normalizeRemoteFSPath(body.Path)
	}
	if isSkillPath(pathValue) {
		h.skill.Trash(w, requestWithUserAndPath(r, pathValue))
		return
	}
	replyError(w, errRemoteFSUnsupported)
}

func readBodyPath(w http.ResponseWriter, r *http.Request) ([]byte, string, bool) {
	data, err := io.ReadAll(r.Body)
	if err != nil {
		replyError(w, err)
		return nil, "", false
	}
	r.Body = io.NopCloser(strings.NewReader(string(data)))
	var body struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		skillhttperr.Reply(w, "invalid json", http.StatusBadRequest)
		return nil, "", false
	}
	return data, normalizeRemoteFSPath(body.Path), true
}

func readBodyPathPair(w http.ResponseWriter, r *http.Request) ([]byte, string, string, bool) {
	data, err := io.ReadAll(r.Body)
	if err != nil {
		replyError(w, err)
		return nil, "", "", false
	}
	r.Body = io.NopCloser(strings.NewReader(string(data)))
	var body struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		skillhttperr.Reply(w, "invalid json", http.StatusBadRequest)
		return nil, "", "", false
	}
	return data, normalizeRemoteFSPath(body.From), normalizeRemoteFSPath(body.To), true
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
	return requestWithUserAndPath(r, normalizeRemoteFSPath(r.URL.Query().Get("path")))
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

func normalizeRemoteFSPath(value string) string {
	return strings.TrimLeft(strings.TrimSpace(value), "/")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func replyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errRemoteFSInvalidPath):
		skillhttperr.Reply(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, gorm.ErrRecordNotFound):
		skillhttperr.ReplyWithCode(w, "not found", http.StatusNotFound, skillhttperr.CodeNotFound)
	case errors.Is(err, errRemoteFSConflict):
		skillhttperr.Reply(w, "conflict", http.StatusConflict)
	case errors.Is(err, errRemoteFSUnsupported):
		skillhttperr.Reply(w, "unsupported remote fs operation", http.StatusUnprocessableEntity)
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
