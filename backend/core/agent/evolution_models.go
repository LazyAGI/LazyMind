package agent

import (
	"net/http"
	"strings"

	"lazymind/core/common"
	"lazymind/core/modelconfig"
	"lazymind/core/store"
)

func ListEvolutionModels(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	models, err := modelconfig.LoadEvolutionModels(r.Context(), db, userID)
	if err != nil {
		common.ReplyErr(w, "list models failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, models)
}
