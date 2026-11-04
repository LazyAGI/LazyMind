package academic

import (
	"errors"
	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/doc"
	"lazymind/core/paperlibrary"
	"lazymind/core/store"
	"net/http"
	"strings"
)

type libraryMutationError struct {
	Status        int
	Code, Message string
}

func (e *libraryMutationError) Error() string { return e.Message }
func invalidLibraryMutation(code, message string) error {
	return &libraryMutationError{Status: 422, Code: code, Message: message}
}
func libraryAccess(w http.ResponseWriter, r *http.Request, write bool) (string, bool, bool) {
	user, ok := requireUser(w, r)
	if !ok {
		return "", false, false
	}
	dataset := strings.TrimSpace(r.URL.Query().Get("target_dataset_id"))
	if !paperlibrary.ValidID(dataset) {
		replyError(w, 400, "INVALID_REQUEST", "target_dataset_id 不能为空且不得超过 255 字节")
		return "", false, false
	}
	catalog, err := doc.NewDatasetCatalogService(doc.DatasetCatalogServiceDeps{DB: store.DB()})
	if err != nil {
		replyError(w, 500, "PAPER_LIBRARY_UNAVAILABLE", "论文库暂时不可用")
		return "", false, false
	}
	result, err := catalog.GetDatasetAccess(r.Context(), doc.DatasetGetRequest{UserID: user, DatasetID: dataset, Caller: caller(r)})
	if err != nil {
		var access *doc.DatasetServiceError
		if !errors.As(err, &access) || (access.Code != doc.DatasetServiceForbidden && access.Code != doc.DatasetServiceNotFound) {
			replyError(w, 500, "PAPER_LIBRARY_UNAVAILABLE", "论文库暂时不可用")
			return "", false, false
		}
		replyError(w, 403, "TARGET_DATASET_FORBIDDEN", "没有目标论文库的读取权限")
		return "", false, false
	}
	if err := paperlibrary.EnsureLegacy(r.Context(), store.DB(), dataset); err != nil {
		replyError(w, 500, "PAPER_LIBRARY_UNAVAILABLE", "既有论文库转换失败，原始数据已保留")
		return "", false, false
	}
	if result.Scene != "academic_papers" {
		result, err = catalog.GetDatasetAccess(r.Context(), doc.DatasetGetRequest{UserID: user, DatasetID: dataset, Caller: caller(r)})
		if err != nil {
			replyError(w, 500, "PAPER_LIBRARY_UNAVAILABLE", "论文库暂时不可用")
			return "", false, false
		}
	}
	if result.Scene != "academic_papers" {
		replyError(w, 400, "PAPER_LIBRARY_REQUIRED", "目标知识库不是论文库")
		return "", false, false
	}
	canWrite := doc.DatasetAllowsWriteForCaller(r.Context(), caller(r), result)
	if write && !canWrite {
		replyError(w, 403, "TARGET_DATASET_FORBIDDEN", "没有目标论文库的编辑权限")
		return "", false, false
	}
	return dataset, canWrite, true
}
func paperLibraryAccess(w http.ResponseWriter, r *http.Request) (string, bool, bool) {
	return libraryAccess(w, r, false)
}
func GetPaperLibrary(w http.ResponseWriter, r *http.Request) {
	dataset, canWrite, ok := libraryAccess(w, r, false)
	if !ok {
		return
	}
	view, err := loadLibraryView(r, store.DB(), dataset, nil, true)
	if err == nil {
		var counts map[string]paperlibrary.Counts
		counts, err = paperlibrary.Statistics(r.Context(), store.DB(), []string{dataset})
		view.Counts = counts[dataset]
	}
	if err != nil {
		replyLibraryError(w, err)
		return
	}
	view.CanWrite = canWrite
	common.ReplyJSON(w, view)
}
func replyLibraryError(w http.ResponseWriter, err error) {
	var invalid *libraryMutationError
	switch {
	case errors.As(err, &invalid):
		replyError(w, invalid.Status, invalid.Code, invalid.Message)
	case errors.Is(err, paperlibrary.ErrConflict):
		replyError(w, 409, "PAPER_LIBRARY_CONFLICT", err.Error())
	case errors.Is(err, gorm.ErrRecordNotFound):
		replyError(w, 404, "PAPER_NOT_FOUND", "论文、目录或附件不存在，请重新加载")
	default:
		replyError(w, 500, "PAPER_LIBRARY_SAVE_FAILED", "论文操作失败，请重试；已保存的数据未被修改")
	}
}

type libraryChanges struct {
	Data             paperlibrary.Data `json:"data"`
	RemovedPaperIDs  []string          `json:"removedPaperIds,omitempty"`
	RemovedFolderIDs []string          `json:"removedFolderIds,omitempty"`
}

func libraryResult(r *http.Request, db *gorm.DB, dataset string, ids []string) (libraryChanges, error) {
	view, err := loadLibraryView(r, db, dataset, ids, false)
	return libraryChanges{Data: view.Data}, err
}
func replyLibraryChanges(w http.ResponseWriter, r *http.Request, dataset string, ids []string, err error) {
	if err != nil {
		replyLibraryError(w, err)
		return
	}
	out, e := libraryResult(r, store.DB(), dataset, ids)
	if e != nil {
		replyLibraryError(w, e)
		return
	}
	common.ReplyJSON(w, out)
}
