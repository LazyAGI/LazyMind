package vocabulary

import (
	"context"
	"encoding/json"
	"github.com/gorilla/mux"
	"lazymind/core/common"
	"lazymind/core/store"
	"net/http"
	"strings"
	"time"
)

func cloudReplyError(w http.ResponseWriter, err error) {
	// Catalog messages only; do not expose database/credential/transport details.
	message := err.Error()
	if !strings.HasPrefix(message, "vocabulary cloud ") && !strings.HasPrefix(message, "invalid vocabulary cloud ") && !strings.HasPrefix(message, "cloud vocabulary: ") {
		message = "vocabulary cloud storage failed"
	}
	common.ReplyErr(w, message, http.StatusBadRequest)
}
func decodeCloudBody(w http.ResponseWriter, r *http.Request, out any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	if json.NewDecoder(r.Body).Decode(out) != nil {
		common.ReplyErr(w, "invalid vocabulary cloud request", 400)
		return false
	}
	return true
}
func ListCloudConnections(w http.ResponseWriter, r *http.Request) {
	rows, err := service().CloudConnections(r.Context(), store.UserID(r))
	if err != nil {
		cloudReplyError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{"items": rows})
}
func PutCloudConnection(w http.ResponseWriter, r *http.Request) {
	var in CloudConnectionInput
	if !decodeCloudBody(w, r, &in) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	row, err := service().SaveCloudConnection(ctx, store.UserID(r), mux.Vars(r)["provider"], in)
	if err != nil {
		cloudReplyError(w, err)
		return
	}
	common.ReplyOK(w, row)
}
func TestCloudConnection(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if !decodeCloudBody(w, r, &in) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	client, err := service().cloudClientFor(ctx, store.UserID(r), mux.Vars(r)["provider"], in.Token)
	if err != nil {
		cloudReplyError(w, err)
		return
	}
	books, err := client.ListWordbooks(ctx)
	if err != nil {
		cloudReplyError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{"items": books})
}
func CreateCloudWordbook(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
		Name  string `json:"name"`
	}
	if !decodeCloudBody(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Name) == "" || len(in.Name) > 255 {
		common.ReplyErr(w, "invalid vocabulary cloud target", 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	client, err := service().cloudClientFor(ctx, store.UserID(r), mux.Vars(r)["provider"], in.Token)
	if err != nil {
		cloudReplyError(w, err)
		return
	}
	book, err := client.CreateWordbook(ctx, strings.TrimSpace(in.Name))
	if err != nil {
		cloudReplyError(w, err)
		return
	}
	common.ReplyOK(w, book)
}
func DeleteCloudConnection(w http.ResponseWriter, r *http.Request) {
	if err := service().DisconnectCloud(r.Context(), store.UserID(r), mux.Vars(r)["provider"]); err != nil {
		cloudReplyError(w, err)
		return
	}
	common.ReplyOK(w, map[string]bool{"deleted": true})
}
func SyncCloudConnection(w http.ResponseWriter, r *http.Request) {
	n, err := service().BackfillCloud(r.Context(), store.UserID(r), mux.Vars(r)["provider"])
	if err != nil {
		cloudReplyError(w, err)
		return
	}
	common.ReplyOK(w, map[string]int{"queued": n})
}
func ListCloudOperations(w http.ResponseWriter, r *http.Request) {
	rows, err := service().CloudOperations(r.Context(), store.UserID(r))
	if err != nil {
		cloudReplyError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{"items": rows})
}
