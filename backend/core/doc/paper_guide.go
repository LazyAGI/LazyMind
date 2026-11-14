package doc

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"io"
	"lazymind/core/common"
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
func paperGuideError(w http.ResponseWriter, e error) {
	code := e.Error()
	status := 500
	switch {
	case errors.Is(e, paperguide.ErrForbidden), errors.Is(e, gorm.ErrRecordNotFound):
		status = 404
		code = paperguide.ErrForbidden.Error()
	case errors.Is(e, paperguide.ErrModelTimeout):
		status = 504
	case errors.Is(e, paperguide.ErrModelRequest):
		status = 502
	case errors.Is(e, paperguide.ErrDisabled):
		status = 403
	case errors.Is(e, paperguide.ErrRegenerate), errors.Is(e, paperguide.ErrConflict), errors.Is(e, paperguide.ErrPreparation), errors.Is(e, paperguide.ErrPrecision), errors.Is(e, paperguide.ErrSourceChanged):
		status = 409
	case errors.Is(e, paperguide.ErrBudget), errors.Is(e, paperguide.ErrContext), errors.Is(e, paperguide.ErrMaterial), errors.Is(e, paperguide.ErrEvidence), errors.Is(e, paperguide.ErrModelChanged), errors.Is(e, paperguide.ErrQuality):
		status = 422
	case code == "invalid request" || code == "invalid body":
		status = 400
	default:
		code = "paper_guide_generation_failed"
	}
	common.ReplyErrWithData(w, code, map[string]any{"error_code": code}, status)
}
func GetPaperGuide(w http.ResponseWriter, r *http.Request) {
	scope, e := guideScope(r, r.URL.Query().Get("language"))
	if e != nil {
		paperGuideError(w, e)
		return
	}
	out, e := paperGuideService().Status(r.Context(), scope)
	if e != nil {
		paperGuideError(w, e)
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
		paperGuideError(w, e)
		return
	}
	scope, e := guideScope(r, in.Language)
	if e != nil {
		paperGuideError(w, e)
		return
	}
	if len(in.IdempotencyKey) > 128 || len(in.ModelRef) > 256 {
		paperGuideError(w, errors.New("invalid request"))
		return
	}
	out, reused, e := paperGuideService().Generate(r.Context(), scope, in)
	if e != nil {
		paperGuideError(w, e)
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
		paperGuideError(w, e)
		return
	}
	after := int64(0)
	if raw := r.URL.Query().Get("after_version"); raw != "" {
		after, e = strconv.ParseInt(raw, 10, 64)
		if e != nil || after < 0 {
			paperGuideError(w, errors.New("invalid request"))
			return
		}
	}
	out, e := paperGuideService().Revision(r.Context(), scope, common.PathVar(r, "revision"), after)
	if e != nil {
		paperGuideError(w, e)
		return
	}
	common.ReplyOK(w, out)
}
func GetPaperGuideEvidence(w http.ResponseWriter, r *http.Request) {
	scope, e := guideScope(r, r.URL.Query().Get("language"))
	if e != nil {
		paperGuideError(w, e)
		return
	}
	ev, current, e := paperGuideService().Evidence(r.Context(), scope, common.PathVar(r, "revision"), common.PathVar(r, "evidence"))
	if e != nil {
		paperGuideError(w, e)
		return
	}
	common.ReplyOK(w, struct {
		Evidence      paperguide.GuideEvidence `json:"evidence"`
		SourceCurrent bool                     `json:"source_current"`
	}{ev, current})
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
		paperGuideError(w, e)
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
		paperGuideError(w, e)
		return
	}
	service := paperGuideService()
	rev, e := service.Repo.Revision(r.Context(), scope, common.PathVar(r, "run"))
	if e != nil {
		paperGuideError(w, e)
		return
	}
	if e = service.Repo.CheckContract(r.Context(), rev); e != nil {
		paperGuideError(w, e)
		return
	}
	var frozen paperguide.ModelIdentity
	_ = json.Unmarshal([]byte(rev.ModelJSON), &frozen)
	model, _, e := resolvePaperGuideModel(r.Context(), scope, frozen.Ref)
	if e != nil {
		paperGuideError(w, e)
		return
	}
	source, e := service.Source.Load(r.Context(), scope, false)
	if e != nil || source.Hash != rev.SourceSHA256 || rev.IRDigest != "" && (source.Snapshot == nil || source.Snapshot.IRDigest != rev.IRDigest) {
		paperGuideError(w, paperguide.ErrSourceChanged)
		return
	}
	_, e = service.Repo.Resume(r.Context(), scope, rev.ID, model)
	if e != nil {
		paperGuideError(w, e)
		return
	}
	out, e := service.Status(r.Context(), scope)
	if e != nil {
		paperGuideError(w, e)
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
		paperGuideError(w, e)
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
		paperGuideError(w, e)
		return
	}
	out, e := paperGuideService().Selection(r.Context(), scope, common.PathVar(r, "revision"), in)
	if e != nil {
		paperGuideError(w, e)
		return
	}
	common.ReplyOK(w, out)
}
