package httperr

import (
	"errors"
	"net/http"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/common"
)

const (
	CodeInvalidRequest       = "invalid_request"
	CodeInvalidPath          = "invalid_path"
	CodeEmptyDraft           = "empty_draft"
	CodeEntryTypeConflict    = "entry_type_conflict"
	CodeUnauthenticated      = "unauthenticated"
	CodeForbidden            = "forbidden"
	CodeNotFound             = "not_found"
	CodeDraftConflict        = "draft_conflict"
	CodeDraftVersionConflict = "draft_version_conflict"
	CodePathExists           = "path_exists"
	CodePayloadTooLarge      = "payload_too_large"
	CodeSkillPackageInvalid  = "skill_package_invalid"
	CodeDiffRefMismatch      = "diff_ref_mismatch"
	CodeInternal             = "internal_error"
)

type Semantic struct {
	Status  int
	Code    string
	Message string
}

func Reply(w http.ResponseWriter, message string, status int) {
	ReplySemantic(w, ForMessage(message, status))
}

func ReplyWithCode(w http.ResponseWriter, message string, status int, code string) {
	ReplySemantic(w, Semantic{Status: status, Code: code, Message: message})
}

func ReplyError(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	ReplySemantic(w, ForError(err))
}

func ReplySemantic(w http.ResponseWriter, sem Semantic) {
	if sem.Status == 0 {
		sem.Status = http.StatusInternalServerError
	}
	if strings.TrimSpace(sem.Code) == "" {
		sem.Code = codeForStatus(sem.Status)
	}
	common.ReplyErrWithData(w, sem.Message, map[string]any{"code": sem.Code}, sem.Status)
}

func ForError(err error) Semantic {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Semantic{Status: http.StatusNotFound, Code: CodeNotFound, Message: err.Error()}
	}
	status := statusForMessage(err.Error())
	return ForMessage(err.Error(), status)
}

func ForMessage(message string, status int) Semantic {
	msg := strings.TrimSpace(message)
	if status == 0 {
		status = statusForMessage(msg)
	}
	return Semantic{Status: status, Code: codeForMessage(msg, status), Message: msg}
}

func statusForMessage(message string) int {
	msg := strings.ToLower(strings.TrimSpace(message))
	switch {
	case strings.Contains(msg, "not found"):
		return http.StatusNotFound
	case strings.Contains(msg, "another user"), strings.Contains(msg, "not for target user"),
		strings.Contains(msg, "forbidden"):
		return http.StatusForbidden
	case strings.Contains(msg, "stale draft version"), strings.Contains(msg, "draft base revision conflict"), strings.Contains(msg, "stale review version"),
		strings.Contains(msg, "draft snapshot changed"):
		return http.StatusConflict
	case strings.Contains(msg, "while draft overlay exists"), strings.Contains(msg, "draft belongs to another task"),
		strings.Contains(msg, "cannot rollback while draft overlay exists"):
		return http.StatusConflict
	case strings.Contains(msg, "already exists"), strings.Contains(msg, "duplicate"), strings.Contains(msg, "name conflict"):
		return http.StatusConflict
	case strings.Contains(msg, "skill package must contain"), strings.Contains(msg, "not a valid zip"):
		return http.StatusUnprocessableEntity
	case strings.Contains(msg, "object store"), strings.Contains(msg, "db is not configured"):
		return http.StatusInternalServerError
	default:
		return http.StatusBadRequest
	}
}

func codeForMessage(message string, status int) string {
	msg := strings.ToLower(strings.TrimSpace(message))
	switch {
	case status == http.StatusUnauthorized:
		return CodeUnauthenticated
	case status == http.StatusForbidden:
		return CodeForbidden
	case status == http.StatusNotFound:
		return CodeNotFound
	case status == http.StatusRequestEntityTooLarge:
		return CodePayloadTooLarge
	case status >= http.StatusInternalServerError:
		return CodeInternal
	case strings.Contains(msg, "unsafe path"), strings.Contains(msg, "invalid path"),
		strings.Contains(msg, "path is required"):
		return CodeInvalidPath
	case strings.Contains(msg, "draft overlay is empty"):
		return CodeEmptyDraft
	case strings.Contains(msg, "stale draft version"), strings.Contains(msg, "draft base revision conflict"), strings.Contains(msg, "stale review version"),
		strings.Contains(msg, "draft snapshot changed"):
		return CodeDraftVersionConflict
	case strings.Contains(msg, "while draft overlay exists"), strings.Contains(msg, "draft belongs to another task"),
		strings.Contains(msg, "cannot rollback while draft overlay exists"):
		return CodeDraftConflict
	case strings.Contains(msg, "already exists"), strings.Contains(msg, "duplicate"), strings.Contains(msg, "name conflict"):
		return CodePathExists
	case strings.Contains(msg, "write file over directory"), strings.Contains(msg, "directory over file"),
		strings.Contains(msg, "parent path is a file"), strings.Contains(msg, "path is a directory"):
		return CodeEntryTypeConflict
	case strings.Contains(msg, "skill package must contain"), strings.Contains(msg, "not a valid zip"):
		return CodeSkillPackageInvalid
	case strings.Contains(msg, "diff refs must belong"), strings.Contains(msg, "diff ref mismatch"),
		strings.Contains(msg, "upload context"):
		return CodeDiffRefMismatch
	default:
		return codeForStatus(status)
	}
}

func codeForStatus(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return CodeUnauthenticated
	case http.StatusForbidden:
		return CodeForbidden
	case http.StatusNotFound:
		return CodeNotFound
	case http.StatusConflict:
		return CodeDraftConflict
	case http.StatusRequestEntityTooLarge:
		return CodePayloadTooLarge
	case http.StatusUnprocessableEntity:
		return CodeInvalidRequest
	default:
		if status >= http.StatusInternalServerError {
			return CodeInternal
		}
		return CodeInvalidRequest
	}
}
