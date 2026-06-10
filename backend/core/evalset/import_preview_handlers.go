package evalset

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/xuri/excelize/v2"

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
		_ = writer.Write(importDownloadTemplateFields)
		writer.Flush()
	case importFileTypeJSON:
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="eval_set_template.json"`)
		_ = json.NewEncoder(w).Encode([]map[string]any{importDownloadTemplateRow()})
	case importFileTypeXLSX:
		output, err := buildXLSXImportTemplate()
		if err != nil {
			common.ReplyErr(w, "build xlsx template failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
		w.Header().Set("Content-Disposition", `attachment; filename="eval_set_template.xlsx"`)
		_, _ = w.Write(output)
	default:
		common.ReplyErr(w, "unsupported file_type", http.StatusBadRequest)
	}
}

func importDownloadTemplateRow() map[string]any {
	row := make(map[string]any, len(importDownloadTemplateFields))
	for _, field := range importDownloadTemplateFields {
		if field == "is_deleted" {
			row[field] = false
			continue
		}
		row[field] = ""
	}
	return row
}

func buildXLSXImportTemplate() ([]byte, error) {
	workbook := excelize.NewFile()
	defer workbook.Close()

	sheet := workbook.GetSheetName(0)
	for index, field := range importDownloadTemplateFields {
		cell, err := excelize.CoordinatesToCellName(index+1, 1)
		if err != nil {
			return nil, err
		}
		if err := workbook.SetCellValue(sheet, cell, field); err != nil {
			return nil, err
		}
	}

	var buf bytes.Buffer
	if err := workbook.Write(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
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
	case importFileTypeCSV, importFileTypeJSON, importFileTypeXLSX:
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
