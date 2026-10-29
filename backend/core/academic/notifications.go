package academic

import (
	"encoding/json"
	"github.com/gorilla/mux"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"net/http"
	"time"
)

func PaperNotifications(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	var rows []orm.TaskNotification
	if err := store.DB().WithContext(r.Context()).Where("user_id=? AND event IN ? AND read_at IS NULL", user, []string{"paper_info", "paper_ready"}).Order("created_at DESC").Limit(100).Find(&rows).Error; err != nil {
		replyError(w, 500, "READ_FAILED", "通知读取失败")
		return
	}
	type noticeView struct {
		orm.TaskNotification
		Navigation map[string]string `json:"navigation"`
	}
	items := make([]noticeView, 0, len(rows))
	for _, row := range rows {
		var navigation map[string]string
		if json.Unmarshal(row.TargetJSON, &navigation) != nil {
			continue
		}
		items = append(items, noticeView{row, navigation})
	}
	common.ReplyJSON(w, map[string]any{"items": items})
}
func ReadPaperNotification(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	res := store.DB().WithContext(r.Context()).Model(&orm.TaskNotification{}).Where("id=? AND user_id=? AND event IN ?", mux.Vars(r)["notification_id"], user, []string{"paper_info", "paper_ready"}).Update("read_at", time.Now().UTC())
	if res.Error != nil {
		replyError(w, 500, "READ_FAILED", "通知更新失败")
		return
	}
	common.ReplyJSON(w, map[string]bool{"read": true})
}
