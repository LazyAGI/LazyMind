package remotefs

import (
	"encoding/base64"
	"net/http"
	"path"
	"sort"
	"strings"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/skillv2/httperr"
	"lazymind/core/store"
)

type pluginFile struct {
	orm.PluginRevisionEntry
	Content []byte
}

func isPluginPath(v string) bool { return v == "plugins" || strings.HasPrefix(v, "plugins/") }

func (h *Handler) pluginResource(r *http.Request, raw string) (orm.PluginResource, string, error) {
	parts := strings.Split(strings.Trim(raw, "/"), "/")
	if len(parts) < 3 || parts[0] != "plugins" {
		return orm.PluginResource{}, "", gorm.ErrRecordNotFound
	}
	var resource orm.PluginResource
	err := h.db.Where("relative_root = ? AND status <> 'revoked'", strings.Join(parts[:3], "/")).First(&resource).Error
	if err != nil {
		return resource, "", err
	}
	requestUser := strings.TrimSpace(r.URL.Query().Get("user_id"))
	if requestUser == "" {
		requestUser = strings.TrimSpace(store.UserID(r))
	}
	if resource.OwnerUserID != "" && resource.OwnerUserID != requestUser {
		return resource, "", gorm.ErrRecordNotFound
	}
	return resource, strings.Join(parts[3:], "/"), nil
}

func (h *Handler) pluginRevision(r *http.Request, resource orm.PluginResource) (string, error) {
	rev := strings.TrimSpace(r.URL.Query().Get("revision_id"))
	if rev == "" {
		rev = resource.HeadRevisionID
	}
	if rev == "" {
		return "", gorm.ErrRecordNotFound
	}
	var count int64
	err := h.db.Model(&orm.PluginRevision{}).Where("id=? AND plugin_resource_id=?", rev, resource.ID).Count(&count).Error
	if err != nil || count != 1 {
		return "", gorm.ErrRecordNotFound
	}
	return rev, nil
}

func (h *Handler) pluginFiles(r *http.Request, raw string) (orm.PluginResource, string, map[string]pluginFile, error) {
	resource, rel, err := h.pluginResource(r, raw)
	if err != nil {
		return resource, rel, nil, err
	}
	rev, err := h.pluginRevision(r, resource)
	if err != nil {
		return resource, rel, nil, err
	}
	var rows []orm.PluginRevisionEntry
	if err = h.db.Where("revision_id=?", rev).Find(&rows).Error; err != nil {
		return resource, rel, nil, err
	}
	out := map[string]pluginFile{}
	for _, e := range rows {
		f := pluginFile{PluginRevisionEntry: e}
		if e.BlobHash != nil {
			var b orm.PluginBlob
			if err = h.db.Where("hash=?", *e.BlobHash).First(&b).Error; err != nil {
				return resource, rel, nil, err
			}
			f.Content = b.Content
		}
		out[e.Path] = f
	}
	return resource, rel, out, nil
}

func (h *Handler) pluginList(w http.ResponseWriter, r *http.Request, raw string) {
	if raw == "plugins" {
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
		return
	}
	_, rel, files, err := h.pluginFiles(r, raw)
	if err != nil {
		replyError(w, err)
		return
	}
	prefix := strings.Trim(rel, "/")
	if prefix != "" {
		prefix += "/"
	}
	seen := map[string]map[string]any{}
	for p, f := range files {
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		tail := strings.TrimPrefix(p, prefix)
		part := strings.SplitN(tail, "/", 2)[0]
		full := path.Join(strings.TrimSuffix(raw, "/"), part)
		if strings.Contains(tail, "/") {
			seen[part] = map[string]any{"name": full, "path": full, "type": "dir"}
		} else {
			seen[part] = map[string]any{"name": full, "path": full, "type": "file", "size": f.Size, "mime": f.Mime, "file_type": f.FileType, "binary": f.Binary}
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	items := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		items = append(items, seen[k])
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) pluginInfo(w http.ResponseWriter, r *http.Request, raw string) {
	_, rel, files, err := h.pluginFiles(r, raw)
	if err != nil {
		replyError(w, err)
		return
	}
	if f, ok := files[rel]; ok {
		writeJSON(w, http.StatusOK, map[string]any{"path": raw, "type": "file", "size": f.Size, "mime": f.Mime, "file_type": f.FileType, "binary": f.Binary})
		return
	}
	prefix := strings.Trim(rel, "/") + "/"
	for p := range files {
		if strings.HasPrefix(p, prefix) {
			writeJSON(w, http.StatusOK, map[string]any{"path": raw, "type": "dir"})
			return
		}
	}
	replyError(w, gorm.ErrRecordNotFound)
}

func (h *Handler) pluginContent(w http.ResponseWriter, r *http.Request, raw string) {
	if r.Method != http.MethodGet {
		httperr.Reply(w, "revision/plugin views are read-only", http.StatusBadRequest)
		return
	}
	_, rel, files, err := h.pluginFiles(r, raw)
	if err != nil {
		replyError(w, err)
		return
	}
	f, ok := files[rel]
	if !ok {
		replyError(w, gorm.ErrRecordNotFound)
		return
	}
	if r.URL.Query().Get("encoding") == "base64" {
		writeJSON(w, http.StatusOK, map[string]any{"encoding": "base64", "content": base64.StdEncoding.EncodeToString(f.Content)})
		return
	}
	if f.Mime != "" {
		w.Header().Set("Content-Type", f.Mime)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(f.Content)
}
