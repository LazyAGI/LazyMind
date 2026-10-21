package academic

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/store"
)

const zoteroStoredPDFLimit = 128 << 20

// StoreZoteroAttachment runs only after metadata confirmation. The graph must
// already contain the target attachment; uploads cannot create arbitrary papers.
func StoreZoteroAttachment(w http.ResponseWriter, r *http.Request) {
	datasetID, canWrite, ok := paperLibraryAccess(w, r)
	if !ok {
		return
	}
	if !canWrite {
		replyError(w, 403, "TARGET_DATASET_FORBIDDEN", "没有目标论文库的编辑权限")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, zoteroStoredPDFLimit+(1<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
		replyError(w, 400, "INVALID_REQUEST", "PDF 上传无效或超过 128 MiB")
		return
	}
	defer r.MultipartForm.RemoveAll()
	paperID, attachmentID := r.FormValue("paper_id"), r.FormValue("attachment_id")
	if paperID == "" || len(paperID) > 255 || attachmentID == "" || len(attachmentID) > 255 {
		replyError(w, 400, "INVALID_REQUEST", "文献或附件标识无效")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		replyError(w, 400, "INVALID_REQUEST", "请选择 PDF 附件")
		return
	}
	defer file.Close()
	tmp, err := os.CreateTemp("", "lazymind-zotero-*.pdf")
	if err != nil {
		replyError(w, 500, "ZOTERO_STORE_FAILED", "无法暂存 PDF 附件")
		return
	}
	defer os.Remove(tmp.Name())
	hash := md5.New() // Zotero's published attachment checksum uses MD5.
	written, copyErr := io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(file, zoteroStoredPDFLimit+1))
	closeErr := tmp.Close()
	if copyErr != nil || closeErr != nil || written > zoteroStoredPDFLimit {
		replyError(w, 400, "INVALID_REQUEST", "PDF 上传失败或超过 128 MiB")
		return
	}
	if expected := strings.ToLower(strings.TrimSpace(r.FormValue("expected_md5"))); expected != "" && expected != hex.EncodeToString(hash.Sum(nil)) {
		replyError(w, 409, "ZOTERO_ATTACHMENT_CHANGED", "附件校验值与 Zotero 题录不一致，请重新读取题录并选择对应文件")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	result, err := storeZoteroFile(ctx, store.DB(), datasetID, paperID, attachmentID, tmp.Name(), filepath.Base(header.Filename), store.UserID(r), store.UserName(r))
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"code": "ZOTERO_STORE_FAILED", "message": err.Error(), "library": result})
		return
	}
	_ = json.NewEncoder(w).Encode(result)
}

func zoteroGraphTarget(result paperLibraryResponse, paperID, attachmentID string) (map[string]any, map[string]any, map[string]any, error) {
	var graph map[string]any
	if err := json.Unmarshal(result.Data, &graph); err != nil {
		return nil, nil, nil, err
	}
	var paper, attachment map[string]any
	for _, raw := range graph["papers"].([]any) {
		item := raw.(map[string]any)
		if item["id"] == paperID {
			paper = item
		}
	}
	for _, raw := range graph["attachments"].([]any) {
		item := raw.(map[string]any)
		if item["id"] == attachmentID && item["paperId"] == paperID && item["source"] == "zotero" {
			attachment = item
		}
	}
	if paper == nil || attachment == nil {
		return nil, nil, nil, errors.New("Zotero 文献或附件已移除，请重新读取论文库")
	}
	linked := false
	for _, id := range paper["attachmentIds"].([]any) {
		if id == attachmentID {
			linked = true
		}
	}
	if !linked {
		return nil, nil, nil, errors.New("Zotero 附件不属于目标文献")
	}
	return graph, paper, attachment, nil
}

func storeZoteroFile(ctx context.Context, db *gorm.DB, datasetID, paperID, attachmentID, path, name, userID, userName string) (paperLibraryResponse, error) {
	var empty paperLibraryResponse
	file, err := os.Open(path)
	if err != nil {
		return empty, err
	}
	info, statErr := file.Stat()
	header := make([]byte, 1024)
	n, _ := file.Read(header)
	file.Close()
	if statErr != nil || !info.Mode().IsRegular() || info.Size() > zoteroStoredPDFLimit || !bytes.Contains(header[:n], []byte("%PDF-")) {
		return empty, errors.New("Zotero 附件不是有效 PDF 或超过 128 MiB")
	}
	identity := sha256.Sum256([]byte(datasetID + "\x00" + attachmentID))
	documentID := "zotero_" + hex.EncodeToString(identity[:])
	guard, err := lockArxivImport(ctx, db, "zotero:"+documentID)
	if err != nil {
		return empty, err
	}
	defer guard.release()
	ctx = guard.ctx
	current, err := readPaperLibrary(ctx, db, datasetID)
	if err != nil {
		return empty, err
	}
	_, _, attachment, err := zoteroGraphTarget(current, paperID, attachmentID)
	if err != nil {
		return empty, err
	}
	// A previous successful import may already have an older document identity.
	if saved, ok := attachment["contentRef"].(string); ok && saved != "" {
		documentID = saved
	}
	var document orm.Document
	findErr := db.WithContext(ctx).Where("id = ? AND dataset_id = ?", documentID, datasetID).Take(&document).Error
	var imported doc.LocalFileImportResult
	var importErr error
	switch {
	case errors.Is(findErr, gorm.ErrRecordNotFound):
		// Never replace a removed historical PDF silently. A new explicit import
		// has its own attachment identity and can be selected by the user.
		if saved, _ := attachment["contentRef"].(string); saved != "" {
			return empty, doc.ErrImportedDocumentUnavailable
		}
		imported, importErr = doc.ImportLocalFile(ctx, doc.LocalFileImportRequest{DatasetID: datasetID, DocumentID: documentID, SourcePath: path, DisplayName: name, ContentType: "application/pdf", DataSourceType: "ZOTERO", UserID: userID, UserName: userName})
	case findErr != nil:
		return empty, findErr
	case document.DeletedAt != nil:
		return empty, doc.ErrImportedDocumentUnavailable
	default:
		imported, importErr = doc.ResumeLocalFileImport(ctx, doc.ResumeLocalFileImportRequest{DatasetID: datasetID, DocumentID: documentID, UserID: userID, UserName: userName})
	}
	if imported.DocumentID == "" {
		return empty, fmt.Errorf("Zotero 附件保存失败：%w", importErr)
	}
	// ImportLocalFile allows stored-only imports to keep their saved document
	// after task bookkeeping fails. Keep that recovery behavior, but surface the
	// rejected task here instead of reporting a completed Zotero import.
	if importErr == nil && imported.Start.Status != "STARTED" {
		if imported.Start.Message != "" {
			importErr = fmt.Errorf("PDF 已保存，但处理未成功，请在文档任务中重试：%s", imported.Start.Message)
		} else {
			importErr = errors.New("文献处理任务未能启动")
		}
	}
	if err = guard.check(); err != nil {
		return empty, err
	}
	if err = db.WithContext(ctx).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", documentID, datasetID).Take(&document).Error; err != nil {
		return empty, err
	}
	_, size, exists, err := readArxivDocument(document)
	if err != nil {
		return empty, err
	}
	if !exists {
		return empty, doc.ErrImportedDocumentUnavailable
	}
	// Each CAS attempt starts from the newest graph, preserving concurrent notes,
	// folder edits and attachments. The deterministic document ID survives a crash
	// between document creation and this graph update.
	for attempt := 0; attempt < 5; attempt++ {
		current, err = readPaperLibrary(ctx, db, datasetID)
		if err != nil {
			return empty, err
		}
		graph, paper, att, targetErr := zoteroGraphTarget(current, paperID, attachmentID)
		if targetErr != nil {
			return empty, targetErr
		}
		if prior, _ := att["contentRef"].(string); prior != "" && prior != documentID {
			return empty, errors.New("Zotero 附件已在其他窗口更新，请重新读取论文库")
		}
		att["contentRef"], att["size"], att["mimeType"] = documentID, size, "application/pdf"
		if imported.ContentSHA256 != "" {
			att["contentHash"] = imported.ContentSHA256
		}
		if importErr != nil {
			att["processingError"] = importErr.Error()
			paper["processingError"], paper["parse"] = importErr.Error(), "failed"
		} else {
			oldError, _ := att["processingError"].(string)
			delete(att, "processingError")
			if paper["processingError"] == oldError && oldError != "" {
				delete(paper, "processingError")
				paper["parse"] = "ready"
			}
		}
		raw, marshalErr := json.Marshal(graph)
		if marshalErr != nil {
			return empty, marshalErr
		}
		if _, err = validatePaperLibrary(raw); err != nil {
			return empty, err
		}
		saved, saveErr := savePaperLibrary(ctx, db, datasetID, current.Revision, raw)
		if saveErr != nil {
			return empty, saveErr
		}
		if !saved {
			continue
		}
		result := paperLibraryResponse{Data: raw, Revision: current.Revision + 1, CanWrite: true}
		return result, importErr
	}
	return empty, errors.New("论文库正在其他窗口更新，PDF 已保留，请重试关联")
}
