package currentmemory

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/maintenance"
	"lazymind/core/store"
)

type Handler struct {
	module *Module
	db     *gorm.DB
}

func NewHandler(db *gorm.DB) *Handler {
	return &Handler{module: NewModule(db), db: db}
}

func NewHandlerWithPreferenceIndexMaxItems(db *gorm.DB, maxItems int) *Handler {
	return &Handler{
		module: NewModuleWithPreferenceIndexMaxItems(db, maxItems),
		db:     db,
	}
}

func GetSoul(w http.ResponseWriter, r *http.Request) {
	NewHandler(store.DB()).GetSoul(w, r)
}

func PatchSoul(w http.ResponseWriter, r *http.Request) {
	NewHandler(store.DB()).PatchSoul(w, r)
}

func GetProfile(w http.ResponseWriter, r *http.Request) {
	NewHandler(store.DB()).GetProfile(w, r)
}

func PatchProfile(w http.ResponseWriter, r *http.Request) {
	NewHandler(store.DB()).PatchProfile(w, r)
}

func ListPreferences(w http.ResponseWriter, r *http.Request) {
	NewHandler(store.DB()).ListPreferences(w, r)
}

func GetPreference(w http.ResponseWriter, r *http.Request) {
	NewHandler(store.DB()).GetPreference(w, r)
}

func ReorderPreferences(w http.ResponseWriter, r *http.Request) {
	NewHandler(store.DB()).ReorderPreferences(w, r)
}

func DeletePreference(w http.ResponseWriter, r *http.Request) {
	NewHandler(store.DB()).DeletePreference(w, r)
}

func (h *Handler) GetSoul(w http.ResponseWriter, r *http.Request) {
	userID, ok := requirePublicUser(w, r)
	if !ok {
		return
	}
	result, err := h.module.GetSoul(r.Context(), userID)
	if err != nil {
		replyPublicError(w, err)
		return
	}
	common.ReplyOK(w, result)
}

func (h *Handler) PatchSoul(w http.ResponseWriter, r *http.Request) {
	userID, ok := requirePublicUser(w, r)
	if !ok {
		return
	}
	request, err := decodeOperationsBody(r)
	if err != nil {
		replyPublicError(w, err)
		return
	}
	result, err := h.module.PatchSoul(r.Context(), userID, request)
	if err != nil {
		replyPublicError(w, err)
		return
	}
	common.ReplyOK(w, result)
}

func (h *Handler) GetProfile(w http.ResponseWriter, r *http.Request) {
	userID, ok := requirePublicUser(w, r)
	if !ok {
		return
	}
	result, err := h.module.GetProfile(r.Context(), userID)
	if err != nil {
		replyPublicError(w, err)
		return
	}
	common.ReplyOK(w, result)
}

func (h *Handler) PatchProfile(w http.ResponseWriter, r *http.Request) {
	userID, ok := requirePublicUser(w, r)
	if !ok {
		return
	}
	request, err := decodeOperationsBody(r)
	if err != nil {
		replyPublicError(w, err)
		return
	}
	result, err := h.module.PatchProfile(r.Context(), userID, request)
	if err != nil {
		replyPublicError(w, err)
		return
	}
	common.ReplyOK(w, result)
}

func (h *Handler) ListPreferences(w http.ResponseWriter, r *http.Request) {
	userID, ok := requirePublicUser(w, r)
	if !ok {
		return
	}
	result, err := h.module.ListPreferences(r.Context(), userID)
	if err != nil {
		replyPublicError(w, err)
		return
	}
	common.ReplyOK(w, result)
}

func (h *Handler) GetPreference(w http.ResponseWriter, r *http.Request) {
	userID, ok := requirePublicUser(w, r)
	if !ok {
		return
	}
	result, err := h.module.GetPreference(
		r.Context(),
		userID,
		common.PathVar(r, "name"),
	)
	if err != nil {
		replyPublicError(w, err)
		return
	}
	common.ReplyOK(w, result)
}

func (h *Handler) ReorderPreferences(w http.ResponseWriter, r *http.Request) {
	userID, ok := requirePublicUser(w, r)
	if !ok {
		return
	}
	var request CurrentMemoryPreferenceOrderRequest
	if err := decodeJSONBody(r, &request); err != nil {
		replyPublicError(w, err)
		return
	}
	result, err := h.module.ReorderPreferences(r.Context(), userID, request)
	if err != nil {
		replyPublicError(w, err)
		return
	}
	common.ReplyOK(w, result)
}

func (h *Handler) DeletePreference(w http.ResponseWriter, r *http.Request) {
	userID, ok := requirePublicUser(w, r)
	if !ok {
		return
	}
	err := h.module.DeletePreference(
		r.Context(),
		userID,
		common.PathVar(r, "name"),
	)
	if err != nil {
		replyPublicError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func requirePublicUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID := strings.TrimSpace(common.UserID(r))
	if userID == "" {
		common.ReplyAppErr(
			w,
			common.NewAppError(
				http.StatusUnauthorized,
				common.ErrCodeUnauthorized,
				"X-User-Id is required",
			),
		)
		return "", false
	}
	return userID, true
}

func decodeOperationsBody(r *http.Request) (CurrentMemoryOperationsRequest, error) {
	var request CurrentMemoryOperationsRequest
	if err := decodeJSONBody(r, &request); err != nil {
		return CurrentMemoryOperationsRequest{}, err
	}
	if len(request.Operations) == 0 {
		return CurrentMemoryOperationsRequest{}, fmt.Errorf(
			"%w: at least one operation is required",
			ErrInvalidRequest,
		)
	}
	return request, nil
}

func decodeJSONBody(r *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: invalid JSON body", ErrInvalidRequest)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf(
			"%w: body must contain one JSON value",
			ErrInvalidRequest,
		)
	}
	return nil
}

func replyPublicError(w http.ResponseWriter, err error) {
	var etagConflict *ETagConflictError
	var organizing *maintenance.PreferenceOrganizingError
	switch {
	case errors.As(err, &organizing):
		common.ReplyErrWithData(
			w,
			"preference_organizing",
			map[string]any{"error_code": "preference_organizing", "mutation": "none", "task_id": organizing.TaskID},
			http.StatusConflict,
		)
	case errors.As(err, &etagConflict):
		common.ReplyErrWithData(
			w,
			"preference etag conflict",
			map[string]any{"current_etag": etagConflict.CurrentETag},
			http.StatusConflict,
		)
	case errors.Is(err, ErrInvalidRequest), errors.Is(err, ErrInvalidDocument):
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, ErrNotFound):
		common.ReplyErr(w, "current memory resource not found", http.StatusNotFound)
	case errors.Is(err, ErrConflict):
		common.ReplyErr(w, "current memory update conflict", http.StatusConflict)
	case errors.Is(err, ErrCorruptDocument):
		common.ReplyErr(w, "stored current memory document is invalid", http.StatusInternalServerError)
	default:
		common.ReplyErr(w, "current memory operation failed", http.StatusInternalServerError)
	}
}
