package doc

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"io"
	"lazymind/core/common"
	"lazymind/core/common/taskdisplay"
	applog "lazymind/core/log"
	"lazymind/core/paperguide"
	"lazymind/core/store"
	"net/http"
	"strconv"
	"strings"
)

func guideScope(r *http.Request, language string) (paperguide.Scope, error) {
	if language == "" {
		language = "zh"
	}
	scope := paperguide.Scope{OwnerID: strings.TrimSpace(store.UserID(r)), DatasetID: datasetIDFromPath(r), DocumentID: documentIDFromPath(r), Language: language}
	if scope.OwnerID == "" || scope.DatasetID == "" || scope.DocumentID == "" || language != "zh" && language != "en" {
		return scope, errors.New("invalid request")
	}
	return scope, nil
}
func guideBody(w http.ResponseWriter, r *http.Request, out any) error {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
	d.DisallowUnknownFields()
	if e := d.Decode(out); e != nil {
		return errors.New("invalid body")
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("invalid body")
	}
	return nil
}
func paperGuideError(w http.ResponseWriter, r *http.Request, e error) {
	code, status := paperguide.ErrGeneration.Error(), http.StatusInternalServerError
	switch {
	case errors.Is(e, gorm.ErrRecordNotFound), errors.Is(e, paperguide.ErrForbidden):
		code, status = paperguide.ErrForbidden.Error(), http.StatusNotFound
	case e.Error() == "invalid request" || e.Error() == "invalid body":
		code, status = e.Error(), http.StatusBadRequest
	default:
		for _, mapping := range []struct {
			cause  error
			status int
		}{
			{paperguide.ErrModelTimeout, 504}, {paperguide.ErrModelRequest, 502}, {paperguide.ErrDisabled, 403},
			{paperguide.ErrRegenerate, 409}, {paperguide.ErrConflict, 409}, {paperguide.ErrPreparation, 409},
			{paperguide.ErrPrecision, 409}, {paperguide.ErrSourceChanged, 409}, {paperguide.ErrBudget, 422},
			{paperguide.ErrContext, 422}, {paperguide.ErrMaterial, 422}, {paperguide.ErrEvidence, 422},
			{paperguide.ErrModelChanged, 422}, {paperguide.ErrQuality, 422},
		} {
			if errors.Is(e, mapping.cause) {
				code, status = mapping.cause.Error(), mapping.status
				break
			}
		}
	}
	id := taskdisplay.PrepareRequest(w, r)
	event := applog.Logger.Warn().Str("component", "paper_guide").Str("request_id", id).Str("error_code", code).Int("http_status", status).Str("method", r.Method)
	if route := mux.CurrentRoute(r); route != nil {
		if pattern, err := route.GetPathTemplate(); err == nil {
			event.Str("route", pattern)
		}
	}
	event.Msg("Paper guide request failed")
	common.ReplyErrWithData(w, code, map[string]any{"error_code": code, "request_id": id}, status)
}
func GetPaperGuide(w http.ResponseWriter, r *http.Request) {
	scope, e := guideScope(r, r.URL.Query().Get("language"))
	if e != nil {
		paperGuideError(w, r, e)
		return
	}
	out, e := paperGuideService().Status(r.Context(), scope)
	if e != nil {
		paperGuideError(w, r, e)
		return
	}
	encoded, _ := json.Marshal(out)
	etag := fmt.Sprintf("\"guide-%x\"", sha256.Sum256(append(encoded, out.CurrentIRDigest...)))
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, no-cache")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(304)
		return
	}
	common.ReplyOK(w, out)
}
func GeneratePaperGuide(w http.ResponseWriter, r *http.Request) {
	var in paperguide.GenerateRequest
	if e := guideBody(w, r, &in); e != nil {
		paperGuideError(w, r, e)
		return
	}
	scope, e := guideScope(r, in.Language)
	if e != nil {
		paperGuideError(w, r, e)
		return
	}
	if len(in.IdempotencyKey) > 128 || len(in.ModelRef) > 256 {
		paperGuideError(w, r, errors.New("invalid request"))
		return
	}
	out, reused, e := paperGuideService().Generate(r.Context(), scope, in)
	if e != nil {
		paperGuideError(w, r, e)
		return
	}
	if reused {
		common.ReplyOK(w, out)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(common.APIResponse{Code: common.CodeOK, Message: "ok", Data: out})
}
func GetPaperGuideRevision(w http.ResponseWriter, r *http.Request) {
	scope, e := guideScope(r, r.URL.Query().Get("language"))
	if e != nil {
		paperGuideError(w, r, e)
		return
	}
	after := int64(0)
	if raw := r.URL.Query().Get("after_version"); raw != "" {
		after, e = strconv.ParseInt(raw, 10, 64)
		if e != nil || after < 0 {
			paperGuideError(w, r, errors.New("invalid request"))
			return
		}
	}
	out, e := paperGuideService().Revision(r.Context(), scope, common.PathVar(r, "revision"), after)
	if e != nil {
		paperGuideError(w, r, e)
		return
	}
	common.ReplyOK(w, out)
}
func GetPaperGuideEvidence(w http.ResponseWriter, r *http.Request) {
	scope, e := guideScope(r, r.URL.Query().Get("language"))
	if e != nil {
		paperGuideError(w, r, e)
		return
	}
	out, e := paperGuideService().Evidence(r.Context(), scope, common.PathVar(r, "revision"), common.PathVar(r, "evidence"))
	if e != nil {
		paperGuideError(w, r, e)
		return
	}
	common.ReplyOK(w, out)
}
func CancelPaperGuide(w http.ResponseWriter, r *http.Request) {
	scope, e := guideScope(r, r.URL.Query().Get("language"))
	if e == nil {
		e = authorizePaperGuide(r.Context(), scope, false)
	}
	if e == nil {
		e = paperGuideService().Repo.Cancel(r.Context(), scope, common.PathVar(r, "run"))
	}
	if e != nil {
		paperGuideError(w, r, e)
		return
	}
	common.ReplyOK(w, map[string]bool{"canceled": true})
}
func ResumePaperGuide(w http.ResponseWriter, r *http.Request) {
	scope, e := guideScope(r, r.URL.Query().Get("language"))
	if e == nil {
		e = authorizePaperGuide(r.Context(), scope, true)
	}
	if e != nil {
		paperGuideError(w, r, e)
		return
	}
	service := paperGuideService()
	rev, e := service.Repo.Revision(r.Context(), scope, common.PathVar(r, "run"))
	if e != nil {
		paperGuideError(w, r, e)
		return
	}
	if e = service.Repo.CheckContract(r.Context(), rev); e != nil {
		paperGuideError(w, r, e)
		return
	}
	var frozen paperguide.ModelIdentity
	_ = json.Unmarshal([]byte(rev.ModelJSON), &frozen)
	model, _, e := resolvePaperGuideModel(r.Context(), scope, frozen.Ref)
	if e != nil {
		paperGuideError(w, r, e)
		return
	}
	source, e := service.Source.Load(r.Context(), scope, false)
	if e != nil || source.Hash != rev.SourceSHA256 || rev.IRDigest != "" && (source.Snapshot == nil || source.Snapshot.IRDigest != rev.IRDigest) {
		paperGuideError(w, r, paperguide.ErrSourceChanged)
		return
	}
	_, e = service.Repo.Resume(r.Context(), scope, rev.ID, model)
	if e != nil {
		paperGuideError(w, r, e)
		return
	}
	out, e := service.Status(r.Context(), scope)
	if e != nil {
		paperGuideError(w, r, e)
		return
	}
	common.ReplyOK(w, out)
}
func SavePaperGuideProgress(w http.ResponseWriter, r *http.Request) {
	scope, e := guideScope(r, r.URL.Query().Get("language"))
	var in paperguide.ProgressRequest
	if e == nil {
		e = guideBody(w, r, &in)
	}
	if e == nil {
		e = paperGuideService().SaveProgress(r.Context(), scope, in)
	}
	if e != nil {
		paperGuideError(w, r, e)
		return
	}
	common.ReplyOK(w, in)
}
func PaperGuideSelectionContext(w http.ResponseWriter, r *http.Request) {
	scope, e := guideScope(r, r.URL.Query().Get("language"))
	var in paperguide.SelectionRequest
	if e == nil {
		e = guideBody(w, r, &in)
	}
	if e != nil {
		paperGuideError(w, r, e)
		return
	}
	out, e := paperGuideService().Selection(r.Context(), scope, common.PathVar(r, "revision"), in)
	if e != nil {
		paperGuideError(w, r, e)
		return
	}
	common.ReplyOK(w, out)
}
