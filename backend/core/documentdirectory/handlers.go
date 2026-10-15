package documentdirectory

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func reply(w http.ResponseWriter, value any, err error) {
	if err == nil {
		common.ReplyOK(w, value)
		return
	}
	status, message := 500, "document directory operation failed"
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		status, message = 404, "document directory not found"
	case errors.Is(err, ErrInput):
		status, message = 400, ErrInput.Error()
	case errors.Is(err, ErrBusy), errors.Is(err, ErrCapacity), errors.Is(err, ErrPinLimit):
		status, message = 409, err.Error()
	}
	common.ReplyErr(w, message, status)
}

func ListHandler(w http.ResponseWriter, r *http.Request) {
	rows, err := List(r.Context(), store.DB(), store.UserID(r))
	reply(w, map[string]any{"directories": rows}, err)
}
func DetailHandler(w http.ResponseWriter, r *http.Request) {
	offset, err := strconv.Atoi(r.URL.Query().Get("offset"))
	if r.URL.Query().Get("offset") == "" {
		offset = 0
		err = nil
	}
	if err != nil || offset < 0 {
		reply(w, nil, ErrInput)
		return
	}
	limit := 100
	if s := r.URL.Query().Get("limit"); s != "" {
		limit, err = strconv.Atoi(s)
	}
	if err != nil || limit < 1 || limit > 100 {
		reply(w, nil, ErrInput)
		return
	}
	page := EntryPageQuery{Offset: offset, Limit: limit, Query: r.URL.Query().Get("query"), State: r.URL.Query().Get("state")}
	if r.URL.Query().Has("group_id") {
		v := r.URL.Query().Get("group_id")
		page.Group = &v
	}
	d, err := Get(r.Context(), store.DB(), store.UserID(r), common.PathVar(r, "directory_id"), page)
	reply(w, d, err)
}
func CommandHandler(w http.ResponseWriter, r *http.Request) {
	var c Command
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 128*1024)).Decode(&c) != nil {
		reply(w, nil, ErrInput)
		return
	}
	value, err := Execute(r.Context(), store.DB(), store.UserID(r), c)
	reply(w, value, err)
}
func BatchesHandler(w http.ResponseWriter, r *http.Request) {
	rows, err := BatchList(r.Context(), store.DB(), store.UserID(r))
	reply(w, map[string]any{"batches": rows}, err)
}

type InternalRequest struct {
	Command
	FolderRequest
	HistoryID    string        `json:"history_id"`
	RunID        string        `json:"run_id"`
	Observations []Observation `json:"observations"`
}

func InternalHandler(w http.ResponseWriter, r *http.Request) {
	expected := strings.TrimSpace(os.Getenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN"))
	if expected == "" || subtle.ConstantTimeCompare([]byte(expected), []byte(r.Header.Get("X-LazyMind-Internal-Token"))) != 1 {
		common.ReplyErr(w, "forbidden", 401)
		return
	}
	var req InternalRequest
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2*1024*1024)).Decode(&req) != nil {
		reply(w, nil, ErrInput)
		return
	}
	db, uid, conv := store.DB(), strings.TrimSpace(store.UserID(r)), common.PathVar(r, "conversation_id")
	if uid == "" || req.HistoryID == "" || req.RunID == "" {
		common.ReplyErr(w, "forbidden", 403)
		return
	}
	var conversation orm.Conversation
	if err := db.WithContext(r.Context()).Where("id=? AND create_user_id=? AND deleted_at IS NULL", conv, uid).Take(&conversation).Error; err != nil {
		reply(w, nil, gorm.ErrRecordNotFound)
		return
	}
	if conversation.RelationType == "sidechat" {
		common.ReplyErr(w, "forbidden", 403)
		return
	}
	var h orm.ChatHistory
	if err := db.WithContext(r.Context()).Where("id=? AND conversation_id=? AND run_id=? AND run_status=?", req.HistoryID, conv, req.RunID, "generating").Take(&h).Error; err != nil {
		reply(w, nil, gorm.ErrRecordNotFound)
		return
	}
	if strings.HasPrefix(req.Operation, "folder_") {
		value, err := folderOperation(r.Context(), db, uid, conv, req.Command, req.FolderRequest)
		reply(w, value, err)
		return
	}
	switch req.Operation {
	case "observations":
		// The question is server-owned, not supplied by a model or tool result.
		query := h.RawContent
		if query == "" {
			query = h.Content
		}
		var ext map[string]any
		_ = json.Unmarshal(h.Ext, &ext)
		if inputs, ok := ext["input"].([]any); ok && query == "" {
			for _, v := range inputs {
				if m, ok := v.(map[string]any); ok {
					if s, ok := m["text"].(string); ok {
						query += s + "\n"
					}
				}
			}
		}
		err := SaveObservations(r.Context(), db, uid, conv, req.HistoryID, req.RunID, query, req.Observations)
		reply(w, map[string]any{"saved": err == nil}, err)
	case "list":
		rows, err := List(r.Context(), db, uid)
		reply(w, map[string]any{"directories": rows}, err)
	case "get":
		if req.Offset < 0 {
			reply(w, nil, ErrInput)
			return
		}
		_, err := Execute(r.Context(), db, uid, Command{Operation: "touch", DirectoryID: req.DirectoryID})
		if err != nil {
			reply(w, nil, err)
			return
		}
		err = SaveObservations(r.Context(), db, uid, conv, req.HistoryID, req.RunID, "", []Observation{{CallID: "use:" + req.DirectoryID, Source: "directory", Operation: "context", Status: "success", Scope: encoded(map[string]string{"directory_id": req.DirectoryID})}})
		if err != nil {
			reply(w, nil, err)
			return
		}
		d, err := Get(r.Context(), db, uid, req.DirectoryID, EntryPageQuery{Offset: req.Offset, Limit: 100})
		reply(w, d, err)
	default:
		req.Command.ConversationID = conv
		value, err := Execute(r.Context(), db, uid, req.Command)
		reply(w, value, err)
	}
}

func FolderNodesHandler(w http.ResponseWriter, r *http.Request) {
	offset, err := strconv.Atoi(r.URL.Query().Get("offset"))
	if r.URL.Query().Get("offset") == "" {
		offset = 0
		err = nil
	}
	if err != nil {
		reply(w, nil, ErrInput)
		return
	}
	result, err := FolderNodes(r.Context(), store.DB(), store.UserID(r), common.PathVar(r, "directory_id"), r.URL.Query().Get("parent_key"), offset)
	reply(w, result, err)
}
