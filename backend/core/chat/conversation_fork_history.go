package chat

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"sort"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

type forkHistoryCursor struct{ ConversationID, HistoryID, Direction string }

func forkHistoryToken(conversationID, historyID, direction string) string {
	raw, _ := json.Marshal(forkHistoryCursor{conversationID, historyID, direction})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func serveForkHistoryWindow(w http.ResponseWriter, r *http.Request, convID string) bool {
	anchorID := r.URL.Query().Get("anchor_history_id")
	pageToken := r.URL.Query().Get("anchor_page_token")
	if anchorID == "" && pageToken == "" {
		return false
	}
	db := store.DB().WithContext(r.Context())
	cursor := forkHistoryCursor{ConversationID: convID, HistoryID: anchorID}
	if pageToken != "" {
		raw, err := base64.RawURLEncoding.DecodeString(pageToken)
		if err != nil || len(raw) > 512 || json.Unmarshal(raw, &cursor) != nil || cursor.ConversationID != convID || (cursor.Direction != "older" && cursor.Direction != "newer") {
			forkReplyError(w, forkFail("INVALID_REQUEST"))
			return true
		}
	}
	var target orm.ChatHistory
	if err := db.Where("id = ? AND conversation_id = ?", cursor.HistoryID, convID).Take(&target).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			err = forkFail("SOURCE_UNAVAILABLE")
		}
		forkReplyError(w, err)
		return true
	}
	size, _ := parseConversationHistoryPage(r)
	var window []orm.ChatHistory
	load := func(op string, count int, inclusive bool) error {
		var rows []orm.ChatHistory
		q := db.Where("conversation_id = ?", convID)
		condition := "seq " + op + " ? OR (seq = ? AND (create_time " + op + " ? OR (create_time = ? AND id " + op + " ?)))"
		if inclusive {
			condition = "(" + condition + ") OR id = ?"
		}
		args := []any{target.Seq, target.Seq, target.CreateTime, target.CreateTime, target.ID}
		if inclusive {
			args = append(args, target.ID)
		}
		q = q.Where(condition, args...)
		order := "seq DESC, create_time DESC, id DESC"
		if op == ">" {
			order = "seq ASC, create_time ASC, id ASC"
		}
		if err := q.Order(order).Limit(count).Find(&rows).Error; err != nil {
			return err
		}
		window = append(window, rows...)
		return nil
	}
	var err error
	switch cursor.Direction {
	case "older":
		err = load("<", size, false)
	case "newer":
		err = load(">", size, false)
	default:
		err = load("<", size/2+1, true)
		if err == nil {
			err = load(">", size-size/2-1, false)
		}
	}
	if err != nil {
		forkReplyError(w, err)
		return true
	}
	sort.Slice(window, func(i, j int) bool {
		if window[i].Seq != window[j].Seq {
			return window[i].Seq > window[j].Seq
		}
		if !window[i].CreateTime.Equal(window[j].CreateTime) {
			return window[i].CreateTime.After(window[j].CreateTime)
		}
		return window[i].ID > window[j].ID
	})
	older, newer := "", ""
	if len(window) > 0 {
		hasMore := func(h orm.ChatHistory, op string) (bool, error) {
			var found orm.ChatHistory
			err := db.Select("id").Where("conversation_id = ?", convID).Where("seq "+op+" ? OR (seq = ? AND (create_time "+op+" ? OR (create_time = ? AND id "+op+" ?)))", h.Seq, h.Seq, h.CreateTime, h.CreateTime, h.ID).Take(&found).Error
			if err == gorm.ErrRecordNotFound {
				return false, nil
			}
			return err == nil, err
		}
		last, first := window[len(window)-1], window[0]
		if more, err := hasMore(last, "<"); err != nil {
			forkReplyError(w, err)
			return true
		} else if more {
			older = forkHistoryToken(convID, last.ID, "older")
		}
		if more, err := hasMore(first, ">"); err != nil {
			forkReplyError(w, err)
			return true
		} else if more {
			newer = forkHistoryToken(convID, first.ID, "newer")
		}
	}
	window, err = refreshForkAttachmentsForRead(r.Context(), db, sidechatDatasetCaller(r, store.UserID(r)), window)
	if err != nil {
		forkReplyError(w, err)
		return true
	}
	writeConversationJSON(w, http.StatusOK, map[string]any{"conversation_id": convID, "history": conversationHistoryResponseItems(window), "older_page_token": older, "newer_page_token": newer, "next_page_token": ""})
	return true
}
