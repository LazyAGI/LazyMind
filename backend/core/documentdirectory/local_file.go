package documentdirectory

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"lazymind/core/systemdeps"
)

type DirectoryLocalFile struct {
	Path          string `json:"path"`
	Namespace     string `json:"namespace"`
	Filename      string `json:"filename"`
	Size          int64  `json:"size"`
	MimeType      string `json:"mime_type"`
	ContentBase64 string `json:"content_base64"`
	ErrorCode     string `json:"error_code,omitempty"`
}

// The client supplies IDs, never a filesystem path. A user opening a reference
// does not grant the Agent access or change its reading/classification evidence.
func localFileLocator(ctx context.Context, db *gorm.DB, uid, directoryID, entryID string) (map[string]any, error) {
	var directory orm.DocumentDirectory
	if err := db.WithContext(ctx).Where("id=? AND user_id=? AND deleted_at IS NULL AND ready=?", directoryID, uid, true).Take(&directory).Error; err != nil {
		return nil, err
	}
	var entry orm.DocumentDirectoryEntry
	if err := db.WithContext(ctx).Where("id=? AND directory_id=? AND excluded=? AND source=?", entryID, directoryID, false, "local").Take(&entry).Error; err != nil {
		return nil, err
	}
	var locator map[string]any
	if json.Unmarshal(entry.LocatorJSON, &locator) != nil || entry.Namespace == "" || entry.ResourceID == "" {
		return nil, ErrInput
	}
	if path, ok := locator["path"].(string); ok && path != entry.ResourceID {
		return nil, ErrInput
	}
	if kind, ok := locator["kind"].(string); ok && kind != "file" {
		return nil, ErrInput
	}
	return map[string]any{"path": entry.ResourceID, "namespace": entry.Namespace}, nil
}

func ReadLocalFileHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !systemdeps.IsLocalRuntime() {
		common.ReplyErr(w, "forbidden", http.StatusForbidden)
		return
	}
	input, err := localFileLocator(r.Context(), store.DB(), store.UserID(r), common.PathVar(r, "directory_id"), common.PathVar(r, "entry_id"))
	if err != nil {
		reply(w, nil, err)
		return
	}
	input["metadata_only"] = r.URL.Query().Get("metadata_only") == "true"
	var result DirectoryLocalFile
	headers := map[string]string{"X-LazyMind-Internal-Token": os.Getenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN")}
	err = common.ApiPost(r.Context(), common.JoinURL(common.ChatServiceEndpoint(), "/api/document-directories/local-file"), input, headers, &result, 30*time.Second)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		common.ReplyErr(w, "local file unavailable", http.StatusBadGateway)
		return
	}
	common.ReplyOK(w, result)
}
