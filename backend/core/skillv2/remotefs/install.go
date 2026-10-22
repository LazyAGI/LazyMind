package remotefs

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"io"
	"net/http"
	"os"
	"strings"

	skillhttperr "lazymind/core/skillv2/httperr"
	skillservice "lazymind/core/skillv2/service"
)

// InstallSkill imports an entire package in one operation. The task is required
// for mutation authentication; installation never claims another task's draft.
func (h *Handler) InstallSkill(w http.ResponseWriter, r *http.Request) {
	userID, task, ok := requireWriteParams(w, r)
	if !ok {
		return
	}
	if strings.HasPrefix(task.ID, "org_") || strings.HasPrefix(task.ID, "review_") {
		writeHTTPError(w, badRequest("skill installation requires an editor task"))
		return
	}
	var body struct {
		URL              string `json:"url"`
		PackageZIPBase64 string `json:"package_zip_base64"`
		ExpectedName     string `json:"expected_name"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, skillservice.MaxSkillInstallRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		var size *http.MaxBytesError
		if errors.As(err, &size) {
			skillhttperr.Reply(w, "skill package request exceeds the 100 MiB package limit", http.StatusRequestEntityTooLarge)
		} else {
			skillhttperr.Reply(w, "invalid skill install request", http.StatusBadRequest)
		}
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		skillhttperr.Reply(w, "expected exactly one JSON object", http.StatusBadRequest)
		return
	}
	var archive []byte
	if body.PackageZIPBase64 != "" {
		if len(body.PackageZIPBase64) > base64.StdEncoding.EncodedLen(int(skillservice.MaxSkillDownloadBytes)) {
			skillhttperr.Reply(w, "skill package exceeds the 100 MiB package limit", http.StatusRequestEntityTooLarge)
			return
		}
		var err error
		archive, err = base64.StdEncoding.DecodeString(body.PackageZIPBase64)
		if err != nil {
			skillhttperr.Reply(w, "invalid package_zip_base64", http.StatusBadRequest)
			return
		}
	}
	service := skillservice.NewSkillService(skillservice.SkillServiceDeps{DB: h.db, Downloader: skillservice.HTTPZipDownloader{}, BlobStore: h.blobStore.service})
	result, err := service.InstallSkill(r.Context(), skillservice.InstallSkillRequest{OwnerUserID: userID, URL: body.URL, PackageZIP: archive, ExpectedName: body.ExpectedName})
	if err != nil {
		var sqlState interface{ SQLState() string }
		var sqlCode interface{ Code() int }
		var fileError *os.PathError
		if errors.As(err, &sqlState) || errors.As(err, &sqlCode) || errors.As(err, &fileError) || errors.Is(err, gorm.ErrInvalidDB) {
			skillhttperr.ReplyWithCode(w, err.Error(), http.StatusInternalServerError, skillhttperr.CodeInternal)
		} else {
			skillhttperr.ReplyError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, result)
}
