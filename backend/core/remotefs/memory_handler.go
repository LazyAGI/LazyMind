package remotefs

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
	"strings"

	"lazymind/core/currentmemory"
	"lazymind/core/resourceupdate"
	skillhttperr "lazymind/core/skillv2/httperr"
)

type memoryCurrentHandler struct {
	service *memoryCurrentService
}

func newMemoryCurrentHandler(service *memoryCurrentService) *memoryCurrentHandler {
	return &memoryCurrentHandler{service: service}
}

func (h *memoryCurrentHandler) List(w http.ResponseWriter, r *http.Request, rawPath string) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	entries, err := h.service.list(r.Context(), userID, rawPath)
	if err != nil {
		replyMemoryError(w, err)
		return
	}
	items := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		items = append(items, memoryEntryResponse(entry.Path, entry.EntryType, entry.Size, entry.Mime, entry.FileType, entry.Binary))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *memoryCurrentHandler) Info(w http.ResponseWriter, r *http.Request, rawPath string) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	entry, err := h.service.info(r.Context(), userID, rawPath)
	if err != nil {
		replyMemoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, memoryEntryResponse(
		entry.Path,
		entry.EntryType,
		entry.Size,
		entry.Mime,
		entry.FileType,
		entry.Binary,
	))
}

func (h *memoryCurrentHandler) Exists(w http.ResponseWriter, r *http.Request, rawPath string) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	exists, err := h.service.exists(r.Context(), userID, rawPath)
	if err != nil {
		replyMemoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"exists": exists})
}

func (h *memoryCurrentHandler) Content(w http.ResponseWriter, r *http.Request, rawPath string) {
	switch r.Method {
	case http.MethodGet:
		h.readContent(w, r, rawPath)
	case http.MethodPut:
		h.writeContent(w, r, rawPath)
	default:
		skillhttperr.Reply(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *memoryCurrentHandler) readContent(w http.ResponseWriter, r *http.Request, rawPath string) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	entry, err := h.service.read(r.Context(), userID, rawPath)
	if err != nil {
		replyMemoryError(w, err)
		return
	}
	if r.URL.Query().Get("encoding") == "base64" {
		writeJSON(w, http.StatusOK, map[string]any{
			"encoding": "base64",
			"content":  base64.StdEncoding.EncodeToString(entry.Content),
		})
		return
	}
	if entry.Mime != "" {
		w.Header().Set("Content-Type", entry.Mime)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(entry.Content)
}

func (h *memoryCurrentHandler) writeContent(w http.ResponseWriter, r *http.Request, rawPath string) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	content, err := io.ReadAll(r.Body)
	if err != nil {
		replyMemoryError(w, err)
		return
	}
	entry, err := h.service.write(
		r.Context(),
		userID,
		r.URL.Query().Get("task_id"),
		rawPath,
		content,
		r.Header.Get("Content-Type"),
	)
	if err != nil {
		replyMemoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"path": entry.Path,
		"size": entry.Size,
	})
}

func (h *memoryCurrentHandler) Dir(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	var body struct {
		Path      string `json:"path"`
		Recursive bool   `json:"recursive"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		skillhttperr.Reply(w, "invalid json", http.StatusBadRequest)
		return
	}
	if err := h.service.mkdir(r.Context(), userID, r.URL.Query().Get("task_id"), body.Path, body.Recursive); err != nil {
		replyMemoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *memoryCurrentHandler) Delete(w http.ResponseWriter, r *http.Request, rawPath string) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	if err := h.service.delete(
		r.Context(),
		userID,
		r.URL.Query().Get("task_id"),
		rawPath,
		memoryTruthy(r.URL.Query().Get("recursive")),
	); err != nil {
		replyMemoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *memoryCurrentHandler) Copy(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	var body struct {
		From      string `json:"from"`
		To        string `json:"to"`
		Overwrite bool   `json:"overwrite"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		skillhttperr.Reply(w, "invalid json", http.StatusBadRequest)
		return
	}
	if err := h.service.copy(r.Context(), userID, r.URL.Query().Get("task_id"), body.From, body.To, body.Overwrite); err != nil {
		replyMemoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *memoryCurrentHandler) Move(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	var body struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		skillhttperr.Reply(w, "invalid json", http.StatusBadRequest)
		return
	}
	if err := h.service.move(r.Context(), userID, r.URL.Query().Get("task_id"), body.From, body.To); err != nil {
		replyMemoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func memoryEntryResponse(
	entryPath string,
	entryType string,
	size int64,
	mimeType string,
	fileType string,
	binary bool,
) map[string]any {
	item := map[string]any{
		"name": path.Base(entryPath),
		"path": entryPath,
		"type": entryType,
	}
	if entryType == memoryEntryFile {
		item["size"] = size
		item["mime"] = mimeType
		item["file_type"] = fileType
		item["binary"] = binary
	}
	return item
}

func memoryTruthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

func replyMemoryError(w http.ResponseWriter, err error) {
	var organizingError *resourceupdate.PreferenceOrganizingError
	switch {
	case errors.As(err, &organizingError):
		skillhttperr.ReplyWithCode(
			w,
			"preference_organizing: Preference Organizer is running; this preference write was not saved (mutation=none)",
			http.StatusConflict,
			"preference_organizing",
		)
	case errors.Is(err, currentmemory.ErrInvalidDocument):
		skillhttperr.ReplyWithCode(w, err.Error(), http.StatusBadRequest, skillhttperr.CodeInvalidRequest)
	case errors.Is(err, errMemoryInvalidPath):
		skillhttperr.ReplyWithCode(w, err.Error(), http.StatusBadRequest, skillhttperr.CodeInvalidPath)
	case errors.Is(err, errMemoryNotFound):
		skillhttperr.ReplyWithCode(w, err.Error(), http.StatusNotFound, skillhttperr.CodeNotFound)
	case errors.Is(err, errMemoryConflict):
		skillhttperr.ReplyWithCode(w, err.Error(), http.StatusConflict, skillhttperr.CodePathExists)
	case errors.Is(err, errMemoryProtected):
		skillhttperr.ReplyWithCode(w, err.Error(), http.StatusBadRequest, skillhttperr.CodeInvalidRequest)
	default:
		skillhttperr.ReplyWithCode(w, err.Error(), http.StatusInternalServerError, skillhttperr.CodeInternal)
	}
}
