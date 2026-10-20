package doc

import (
	"errors"
	"io"
	"net/http"

	"lazymind/core/common"
	"lazymind/core/store"
)

// UploadOriginalHTTP is registered behind document.write authentication. It
// receives exactly one byte stream, never a path to open on the Core host.
func UploadOriginalHTTP(w http.ResponseWriter, r *http.Request) {
	caller := datasetCatalogCallerFromRequest(r)
	caller.UserName = store.UserName(r)
	caller.Permissions = []string{"document.write"}
	if err := validOriginalCaller(caller); err != nil {
		common.ReplyErr(w, err.Error(), http.StatusUnauthorized)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, originalMaxBytes+(1<<20))
	reader, err := r.MultipartReader()
	if err != nil {
		common.ReplyErr(w, "multipart file is required", http.StatusBadRequest)
		return
	}
	part, err := reader.NextPart()
	if err != nil {
		common.ReplyErr(w, "one file is required", http.StatusBadRequest)
		return
	}
	defer part.Close()
	if part.FormName() != "file" || part.FileName() == "" {
		common.ReplyErr(w, "one file part named file is required", http.StatusBadRequest)
		return
	}
	service, err := NewOriginalService(OriginalServiceDeps{DB: store.DB()})
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	ref, err := service.AcquireOriginal(r.Context(), caller, AcquireOriginalRequest{Filename: part.FileName(), ContentType: part.Header.Get("Content-Type"), Reader: part})
	if err != nil {
		status := http.StatusBadRequest
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		common.ReplyErr(w, err.Error(), status)
		return
	}
	if next, err := reader.NextPart(); err != io.EOF {
		if next != nil {
			next.Close()
		}
		service.discardOriginal(r.Context(), caller, ref.FileRef)
		common.ReplyErr(w, "exactly one file is accepted per original upload", http.StatusBadRequest)
		return
	}
	common.ReplyJSON(w, ref)
}
