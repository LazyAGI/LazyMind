package evalset

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"lazymind/core/common"
	"lazymind/core/store"
)

func DownloadImportTemplate(w http.ResponseWriter, r *http.Request) {
	fileType := normalizeImportFileType(common.PathVar(r, "file_type"), "")
	switch fileType {
	case importFileTypeCSV:
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="eval_set_template.csv"`)
		writer := csv.NewWriter(w)
		_ = writer.Write(importTemplateFields)
		writer.Flush()
	case importFileTypeJSON:
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="eval_set_template.json"`)
		_ = json.NewEncoder(w).Encode([]ImportNormalizedRow{{}})
	case importFileTypeXLSX:
		common.ReplyErr(w, "xlsx template is not supported in phase 1", http.StatusBadRequest)
	default:
		common.ReplyErr(w, "unsupported file_type", http.StatusBadRequest)
	}
}

func PreviewEvalSetImport(w http.ResponseWriter, r *http.Request) {
	svc, ok := serviceForRequest(w)
	if !ok {
		return
	}
	userID := strings.TrimSpace(store.UserID(r))
	userName := strings.TrimSpace(store.UserName(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}

	maxFileSize := importMaxFileSize()
	r.Body = http.MaxBytesReader(w, r.Body, maxFileSize+1)
	if err := r.ParseMultipartForm(maxFileSize); err != nil {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
		common.ReplyErr(w, "invalid multipart form", http.StatusBadRequest)
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		common.ReplyErr(w, "no file uploaded", http.StatusBadRequest)
		return
	}
	defer file.Close()

	fileName := ""
	if header != nil {
		fileName = header.Filename
	}
	fileType := normalizeImportFileType(r.FormValue("file_type"), fileName)
	switch fileType {
	case importFileTypeCSV, importFileTypeJSON:
	case importFileTypeXLSX:
		common.ReplyErr(w, "xlsx import is not supported in phase 1", http.StatusBadRequest)
		return
	default:
		common.ReplyErr(w, "unsupported file_type", http.StatusBadRequest)
		return
	}

	data, err := io.ReadAll(io.LimitReader(file, maxFileSize+1))
	if err != nil {
		common.ReplyErr(w, "read file failed", http.StatusBadRequest)
		return
	}
	if int64(len(data)) > maxFileSize {
		common.ReplyErr(w, "file exceeds max size", http.StatusBadRequest)
		return
	}

	resp, err := svc.PreviewImport(r.Context(), fileName, fileType, data, userID, userName)
	if err != nil {
		var validationErr *importValidationError
		if errors.As(err, &validationErr) {
			common.ReplyErrWithData(w, validationErr.Error(), validationErr.response, http.StatusBadRequest)
			return
		}
		if strings.Contains(err.Error(), "unsupported file_type") {
			common.ReplyErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		common.ReplyErr(w, "preview eval set import failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, resp)
}
