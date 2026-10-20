package chat

import (
	"net/http"

	"lazymind/core/common"
)

// HTTP rejections use the same terminal as history and SSE. Numeric business
// codes remain available to older clients, but do not define the run outcome.
func replyChatTerminalError(w http.ResponseWriter, message string, status int, terminal *RunTerminal, historyID string) {
	data := map[string]any{"run_terminal": terminal}
	if historyID != "" {
		data["detail"] = map[string]string{"history_id": historyID}
	}
	common.ReplyErrWithData(w, message, data, status)
}
