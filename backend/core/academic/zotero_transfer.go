package academic

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/paperlibrary"
	"lazymind/core/store"
)

// Bound disk/network work across all users, not merely within one browser.
var zoteroTransferSlots = make(chan struct{}, 3)

type zoteroTransferRequest struct {
	zoteroConfig
	ItemKey      string `json:"item_key"`
	PaperID      string `json:"paper_id"`
	AttachmentID string `json:"attachment_id"`
	ExpectedMD5  string `json:"expected_md5,omitempty"`
}

// TransferZoteroAttachment keeps PDF bytes on the server. Credentials exist only
// for this request; already committed files are reused after interrupted calls.
func TransferZoteroAttachment(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUser(w, r); !ok {
		return
	}
	var input zoteroTransferRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		replyZoteroError(w, zoteroFailure(400, "INVALID_ZOTERO_REQUEST", "Zotero 搬运请求无效"))
		return
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || !zoteroItemKey.MatchString(input.ItemKey) || input.PaperID == "" || len(input.PaperID) > 255 || input.AttachmentID == "" || len(input.AttachmentID) > 255 || (input.ExpectedMD5 != "" && !zoteroMD5.MatchString(input.ExpectedMD5)) {
		replyZoteroError(w, zoteroFailure(400, "INVALID_ZOTERO_REQUEST", "Zotero 搬运请求无效"))
		return
	}
	input.Source = strings.ToLower(strings.TrimSpace(input.Source))
	if err := authorizeLocalZoteroSource(r, input.Source); err != nil {
		replyZoteroError(w, err)
		return
	}
	dataset, _, ok := libraryAccess(w, r, true)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	select {
	case zoteroTransferSlots <- struct{}{}:
		defer func() { <-zoteroTransferSlots }()
	case <-ctx.Done():
		replyZoteroError(w, ctx.Err())
		return
	}
	db := store.DB()
	view, err := paperlibrary.Load(ctx, db, dataset, store.UserID(r), []string{input.PaperID}, false)
	if err != nil {
		replyLibraryError(w, err)
		return
	}
	valid := false
	for _, a := range view.Data.Attachments {
		if a.ID == input.AttachmentID && a.PaperID == input.PaperID && a.Source == "zotero" {
			valid = true
		}
	}
	if !valid {
		replyZoteroError(w, zoteroFailure(409, "INVALID_ZOTERO_REQUEST", "请先确认保存对应的 Zotero 文献与附件"))
		return
	}
	// A completed attachment needs neither another source download nor a parser retry.
	_, member, err := zoteroTarget(db.WithContext(ctx), dataset, input.PaperID, input.AttachmentID)
	if err != nil {
		replyLibraryError(w, err)
		return
	}
	if member.DocumentID != "" {
		var document orm.Document
		if db.WithContext(ctx).Where("id=? AND dataset_id=? AND deleted_at IS NULL", member.DocumentID, dataset).Take(&document).Error == nil && zoteroDocumentFileExists(document) {
			writeZoteroTransferResult(w, input.AttachmentID, member.DocumentID)
			return
		}
	}
	client, err := newZoteroClient(input.zoteroConfig)
	if err != nil {
		replyZoteroError(w, err)
		return
	}
	defer client.http.CloseIdleConnections()
	tmp, err := os.CreateTemp("", "lazymind-zotero-transfer-*.pdf")
	if err != nil {
		replyZoteroError(w, err)
		return
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	hash := md5.New()
	var name string
	if input.Source == "database" {
		name, err = client.copyDatabaseAttachment(ctx, input.ItemKey, io.MultiWriter(tmp, hash))
	} else {
		name, err = client.copyAttachment(ctx, input.ItemKey, io.MultiWriter(tmp, hash))
	}
	if err == nil && input.ExpectedMD5 != "" && !strings.EqualFold(input.ExpectedMD5, hex.EncodeToString(hash.Sum(nil))) {
		err = zoteroFailure(409, "ZOTERO_ATTACHMENT_CHANGED", "附件校验值已改变，请重新预览")
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		_, err = storeZoteroFileResult(ctx, db, dataset, input.PaperID, input.AttachmentID, tmp.Name(), name, store.UserID(r), store.UserName(r), true)
		if err != nil {
			err = zoteroFailure(500, "ZOTERO_STORE_FAILED", err.Error())
		}
	}
	if err != nil {
		replyZoteroError(w, err)
		return
	}
	_, member, err = zoteroTarget(db.WithContext(ctx), dataset, input.PaperID, input.AttachmentID)
	if err != nil || member.DocumentID == "" {
		replyZoteroError(w, zoteroFailure(500, "ZOTERO_STORE_FAILED", "附件未成功关联，请重试"))
		return
	}
	writeZoteroTransferResult(w, input.AttachmentID, member.DocumentID)
}
func writeZoteroTransferResult(w http.ResponseWriter, attachment, document string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]string{"attachment_id": attachment, "document_id": document})
}

func zoteroDocumentFileExists(document orm.Document) bool {
	var ext struct {
		StoredPath string `json:"stored_path"`
	}
	if json.Unmarshal(document.Ext, &ext) != nil || strings.TrimSpace(ext.StoredPath) == "" {
		return false
	}
	info, err := os.Lstat(ext.StoredPath)
	return err == nil && info.Mode().IsRegular()
}
