package cloudresource

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"lazymind/core/cloudclient"
	"lazymind/core/common"
)

type contentClient interface {
	GetResourceTree(context.Context, string, string) (cloudclient.ResourceTree, string, error)
	ReadResourceContent(context.Context, string, string, string, string) (cloudclient.ResourceFileContent, error)
}

func (h Handler) readResource(w http.ResponseWriter, r *http.Request) (string, cloudclient.PrivateResource, bool) {
	if common.UserID(r) == "" {
		common.ReplyErr(w, "unauthorized", http.StatusUnauthorized)
		return "", cloudclient.PrivateResource{}, false
	}
	if !cloudclient.ValidResourceID(common.PathVar(r, "resource_id")) {
		common.ReplyErr(w, "invalid request", http.StatusBadRequest)
		return "", cloudclient.PrivateResource{}, false
	}
	if h.Service == nil || h.Service.Cloud == nil {
		common.ReplyErr(w, "LazyMind Cloud is unavailable", http.StatusServiceUnavailable)
		return "", cloudclient.PrivateResource{}, false
	}
	token, _, err := h.Service.cloudContext(r.Context())
	if err != nil {
		replyError(w, err)
		return "", cloudclient.PrivateResource{}, false
	}
	item, _, err := h.Service.Cloud.GetResource(r.Context(), token, strings.TrimSpace(common.PathVar(r, "resource_id")))
	if err != nil {
		replyError(w, err)
		return "", cloudclient.PrivateResource{}, false
	}
	if item.ResourceType != h.ResourceType {
		common.ReplyErr(w, "resource not found", http.StatusNotFound)
		return "", cloudclient.PrivateResource{}, false
	}
	return token, item, true
}

func (h Handler) Get(w http.ResponseWriter, r *http.Request) {
	_, item, ok := h.readResource(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("ETag", `"`+item.ContentHash+`"`)
	common.ReplyOK(w, item)
}

func (h Handler) Tree(w http.ResponseWriter, r *http.Request) {
	token, item, ok := h.readResource(w, r)
	if !ok {
		return
	}
	client, ok := h.Service.Cloud.(contentClient)
	if !ok {
		replyError(w, errors.New("cloud read client unavailable"))
		return
	}
	tree, etag, err := client.GetResourceTree(r.Context(), token, item.ResourceID)
	if err != nil {
		replyError(w, err)
		return
	}
	if tree.ResourceType != h.ResourceType || tree.ContentHash != item.ContentHash {
		common.ReplyAppErr(w, common.NewAppError(http.StatusPreconditionFailed, common.ErrCodeConflict, "Resource version changed"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("ETag", etag)
	common.ReplyOK(w, tree)
}

func (h Handler) Content(w http.ResponseWriter, r *http.Request) {
	// Authenticate locally first, even for malformed Cloud read requests.
	if common.UserID(r) == "" {
		common.ReplyErr(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	token, item, ok := h.readResource(w, r)
	if !ok {
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query) != 1 || len(query["path"]) != 1 || !cloudclient.ValidResourcePath(query.Get("path")) {
		common.ReplyErr(w, "invalid request", http.StatusBadRequest)
		return
	}
	hash, valid := cloudclient.ResourceHashFromETag(r.Header.Get("If-Match"))
	if len(r.Header.Values("If-Match")) != 1 || !valid {
		common.ReplyErr(w, "invalid request", http.StatusPreconditionRequired)
		return
	}
	if item.ContentHash != hash {
		common.ReplyAppErr(w, common.NewAppError(http.StatusPreconditionFailed, common.ErrCodeConflict, "Resource version changed"))
		return
	}
	client, ok := h.Service.Cloud.(contentClient)
	if !ok {
		replyError(w, errors.New("cloud read client unavailable"))
		return
	}
	content, err := client.ReadResourceContent(r.Context(), token, item.ResourceID, query.Get("path"), r.Header.Get("If-Match"))
	if err != nil {
		replyError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("ETag", r.Header.Get("If-Match"))
	common.ReplyOK(w, content)
}
