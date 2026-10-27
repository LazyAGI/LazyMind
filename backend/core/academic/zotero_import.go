package academic

import (
	"encoding/json"
	"net/http"
	"strings"

	"lazymind/core/common"
	"lazymind/core/paperlibrary"
	"lazymind/core/store"
)

// ImportZoteroSnapshot commits confirmed metadata before any optional PDF uploads.
// Stable source IDs make retries additive; normalized user edits are never replaced.
func ImportZoteroSnapshot(w http.ResponseWriter, r *http.Request) {
	dataset, _, ok := libraryAccess(w, r, true)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 48<<20)
	var input struct {
		Data           json.RawMessage `json:"data"`
		SourceID       string          `json:"source_id"`
		IdempotencyKey string          `json:"idempotency_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil || len(input.Data) == 0 || len(input.SourceID) > 512 || len(input.IdempotencyKey) > 255 || strings.TrimSpace(input.IdempotencyKey) == "" {
		replyError(w, 400, "INVALID_REQUEST", "Zotero 导入内容无效或超过大小限制")
		return
	}
	if err := paperlibrary.ImportSnapshot(r.Context(), store.DB(), dataset, store.UserID(r), input.Data); err != nil {
		replyLibraryError(w, err)
		return
	}
	result, err := zoteroView(r.Context(), store.DB(), dataset, store.UserID(r))
	if err != nil {
		replyLibraryError(w, err)
		return
	}
	common.ReplyJSON(w, result)
}
