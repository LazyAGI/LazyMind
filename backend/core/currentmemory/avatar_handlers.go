package currentmemory

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"lazymind/core/common"
	"lazymind/core/store"
)

func GetSoulAvatar(w http.ResponseWriter, r *http.Request) {
	NewHandler(store.DB()).getAvatar(w, r, AvatarKindSoul)
}

func PutSoulAvatar(w http.ResponseWriter, r *http.Request) {
	NewHandler(store.DB()).putAvatar(w, r, AvatarKindSoul)
}

func DeleteSoulAvatar(w http.ResponseWriter, r *http.Request) {
	NewHandler(store.DB()).deleteAvatar(w, r, AvatarKindSoul)
}

func GetProfileAvatar(w http.ResponseWriter, r *http.Request) {
	NewHandler(store.DB()).getAvatar(w, r, AvatarKindProfile)
}

func PutProfileAvatar(w http.ResponseWriter, r *http.Request) {
	NewHandler(store.DB()).putAvatar(w, r, AvatarKindProfile)
}

func DeleteProfileAvatar(w http.ResponseWriter, r *http.Request) {
	NewHandler(store.DB()).deleteAvatar(w, r, AvatarKindProfile)
}

func (h *Handler) getAvatar(
	w http.ResponseWriter,
	r *http.Request,
	kind AvatarKind,
) {
	userID, ok := requirePublicUser(w, r)
	if !ok {
		return
	}
	entry, err := h.module.GetAvatar(r.Context(), userID, kind)
	if err != nil {
		replyAvatarError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Type", entry.Mime)
	w.Header().Set("Content-Length", strconv.FormatInt(entry.Size, 10))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(entry.Content)
}

func (h *Handler) putAvatar(
	w http.ResponseWriter,
	r *http.Request,
	kind AvatarKind,
) {
	userID, ok := requirePublicUser(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, AvatarMaxSize+(64<<10))
	if err := r.ParseMultipartForm(AvatarMaxSize + 1); err != nil {
		if isRequestTooLarge(err) {
			common.ReplyErr(w, "avatar file is too large", http.StatusRequestEntityTooLarge)
			return
		}
		common.ReplyErr(w, "invalid avatar upload", http.StatusBadRequest)
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	if r.MultipartForm == nil ||
		multipartFileCount(r) != 1 ||
		len(r.MultipartForm.File["file"]) != 1 {
		common.ReplyErr(w, "avatar file is required", http.StatusBadRequest)
		return
	}
	fileHeader := r.MultipartForm.File["file"][0]
	if fileHeader.Size > AvatarMaxSize {
		common.ReplyErr(w, "avatar file is too large", http.StatusRequestEntityTooLarge)
		return
	}
	file, err := fileHeader.Open()
	if err != nil {
		common.ReplyErr(w, "invalid avatar upload", http.StatusBadRequest)
		return
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, AvatarMaxSize+1))
	if err != nil {
		common.ReplyErr(w, "invalid avatar upload", http.StatusBadRequest)
		return
	}
	if len(content) > AvatarMaxSize {
		common.ReplyErr(w, "avatar file is too large", http.StatusRequestEntityTooLarge)
		return
	}
	if len(content) == 0 {
		common.ReplyErr(w, "avatar file is required", http.StatusBadRequest)
		return
	}
	contentType, supported := DetectAvatarContentType(content)
	if !supported {
		common.ReplyErr(w, "unsupported avatar image", http.StatusBadRequest)
		return
	}
	if declaredHeader := strings.TrimSpace(fileHeader.Header.Get("Content-Type")); declaredHeader != "" {
		declaredType, _, parseErr := mime.ParseMediaType(declaredHeader)
		if parseErr != nil {
			common.ReplyErr(w, "invalid avatar upload", http.StatusBadRequest)
			return
		}
		declaredType = strings.ToLower(strings.TrimSpace(declaredType))
		if declaredType != "application/octet-stream" && declaredType != contentType {
			common.ReplyErr(w, "invalid avatar upload", http.StatusBadRequest)
			return
		}
	}
	result, err := h.module.PutAvatar(r.Context(), userID, kind, content)
	if err != nil {
		replyAvatarError(w, err)
		return
	}
	common.ReplyOK(w, result)
}

func (h *Handler) deleteAvatar(
	w http.ResponseWriter,
	r *http.Request,
	kind AvatarKind,
) {
	userID, ok := requirePublicUser(w, r)
	if !ok {
		return
	}
	if err := h.module.DeleteAvatar(r.Context(), userID, kind); err != nil {
		replyAvatarError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func multipartFileCount(r *http.Request) int {
	if r.MultipartForm == nil {
		return 0
	}
	count := 0
	for _, files := range r.MultipartForm.File {
		count += len(files)
	}
	return count
}

func isRequestTooLarge(err error) bool {
	var maxBytesError *http.MaxBytesError
	return errors.As(err, &maxBytesError) ||
		errors.Is(err, http.ErrBodyReadAfterClose)
}

func replyAvatarError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		common.ReplyErr(w, "avatar not found", http.StatusNotFound)
	case errors.Is(err, ErrInvalidRequest):
		common.ReplyErr(w, "invalid avatar upload", http.StatusBadRequest)
	case errors.Is(err, ErrCorruptAvatar):
		common.ReplyErr(w, "stored avatar is invalid", http.StatusInternalServerError)
	default:
		common.ReplyErr(w, "avatar operation failed", http.StatusInternalServerError)
	}
}
